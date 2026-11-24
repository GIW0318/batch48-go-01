// Package task arbitrates which processes give memory back when a pressure
// event lands, keeping planning and committing as separate steps.
package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

// Actions recorded on every arbitrated decision.
const (
	ActionReclaim = "reclaim"
	ActionKill    = "kill"
	ActionSkip    = "skip"
)

// Reason codes attached to a decision.
const (
	ReasonWeightOrder = "weight_order"
	ReasonProtected   = "protected"
	ReasonLastReplica = "last_replica"
	ReasonWindowDedup = "window_dedup"
)

// Errors returned by the arbiter.
var (
	ErrBadProc       = errors.New("process record is invalid")
	ErrDuplicatePID  = errors.New("process id already registered")
	ErrProcLimit     = errors.New("process table limit reached")
	ErrBadEvent      = errors.New("pressure event is invalid")
	ErrUnknownPID    = errors.New("process id is not registered")
	ErrStalePlan     = errors.New("plan does not belong to the latest event")
	ErrPlanCommitted = errors.New("plan was already committed")
	ErrBadTenant     = errors.New("tenant is required")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

// Proc is one registered process.
type Proc struct {
	PID    int    `json:"pid"`
	Tenant string `json:"tenant"`
	RSS    uint64 `json:"rss"`
	Weight int    `json:"weight"`
}

// PressureEvent is one memory pressure signal for a window.
type PressureEvent struct {
	Window int64  `json:"window"`
	Need   uint64 `json:"need"`
	At     int64  `json:"at"`
}

// Victim is one process the plan wants memory back from.
type Victim struct {
	PID    int    `json:"pid"`
	Tenant string `json:"tenant"`
	Bytes  uint64 `json:"bytes"`
	Kill   bool   `json:"kill"`
}

// Plan is the outcome of one pressure event before anything is committed.
type Plan struct {
	Window    int64    `json:"window"`
	Victims   []Victim `json:"victims,omitempty"`
	Reclaimed uint64   `json:"reclaimed"`
	Shortfall uint64   `json:"shortfall"`
	Killable  bool     `json:"killable"`
}

// Decision is one replayable arbitration entry.
type Decision struct {
	Window int64  `json:"window"`
	Tenant string `json:"tenant"`
	PID    int    `json:"pid"`
	Action string `json:"action"`
	Bytes  uint64 `json:"bytes"`
	Reason string `json:"reason"`
}

// Options bounds the arbiter tables and protection ageing.
type Options struct {
	MaxProcs            int    `json:"max_procs"`
	MaxHistoryPerTenant int    `json:"max_history_per_tenant"`
	ProtectWindow       int64  `json:"protect_window"`
	MaxReclaimPerWindow uint64 `json:"max_reclaim_per_window"`
}

// snapshotVersion tags the serialised state format.
const snapshotVersion = 1

// protection is an expiring pin. Expiry is measured in event time: the pin is
// armed against the first pressure event that observes it, so callers do not
// need to supply a timestamp to Protect.
type protection struct {
	Armed    bool  `json:"armed"`
	ExpireAt int64 `json:"expire_at"`
}

// pidAccum is the committed cumulative state for one (window, pid) pair.
type pidAccum struct {
	Killed   bool   `json:"killed"`
	Released uint64 `json:"released"`
}

// planAudit keeps the explainable, ordered decision template for a plan that
// Pressure produced. It is ephemeral and only needed until Commit.
type planAudit struct {
	Window int64      `json:"window"`
	Tenant string     `json:"tenant"`
	Items  []Decision `json:"items"`
}

// snapshotData is the canonical serialised arbiter state.
type snapshotData struct {
	Version     int                            `json:"version"`
	Procs       []Proc                         `json:"procs,omitempty"`
	History     map[string][]Decision          `json:"history,omitempty"`
	Protects    map[string]map[int]*protection `json:"protects,omitempty"`
	Accum       map[int64]map[int]*pidAccum    `json:"accum,omitempty"`
	CommittedFP map[string]int64               `json:"committed_fp,omitempty"`
	LastWindow  int64                          `json:"last_window"`
	LastAt      int64                          `json:"last_at"`
	HaveEvent   bool                           `json:"have_event"`
}

// OomArbiter owns the process registry, protections and decision ledger.
type OomArbiter struct {
	mu   sync.Mutex
	opts Options

	procs    map[int]Proc
	history  map[string][]Decision
	protects map[string]map[int]*protection

	haveEvent  bool
	lastWindow int64
	lastAt     int64

	committedFP map[string]int64
	accum       map[int64]map[int]*pidAccum
	audits      map[string]*planAudit
}

// NewOomArbiter builds an arbiter with the given table bounds.
func NewOomArbiter(opts Options) *OomArbiter {
	return &OomArbiter{
		opts:        opts,
		procs:       make(map[int]Proc),
		history:     make(map[string][]Decision),
		protects:    make(map[string]map[int]*protection),
		committedFP: make(map[string]int64),
		accum:       make(map[int64]map[int]*pidAccum),
		audits:      make(map[string]*planAudit),
	}
}

// Register adds one process to the arbitration input.
func (a *OomArbiter) Register(proc Proc) error {
	if proc.PID <= 0 || proc.RSS == 0 || strings.TrimSpace(proc.Tenant) == "" {
		return ErrBadProc
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.procs[proc.PID]; exists {
		return ErrDuplicatePID
	}
	if a.opts.MaxProcs > 0 && len(a.procs) >= a.opts.MaxProcs {
		return ErrProcLimit
	}
	a.procs[proc.PID] = proc
	return nil
}

// rankProcs returns procs ordered by weight desc, RSS desc then pid asc.
// sort.SliceStable makes the ordering fully deterministic for equal keys.
func rankProcs(procs map[int]Proc) []Proc {
	ranked := make([]Proc, 0, len(procs))
	for _, proc := range procs {
		ranked = append(ranked, proc)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Weight != ranked[j].Weight {
			return ranked[i].Weight > ranked[j].Weight
		}
		if ranked[i].RSS != ranked[j].RSS {
			return ranked[i].RSS > ranked[j].RSS
		}
		return ranked[i].PID < ranked[j].PID
	})
	return ranked
}

// isProtectedLocked reports whether pid is pinned at event time at. A pin is
// armed on its first observed event and expires once ProtectWindow has
// elapsed; with a non-positive window it never protects, so pins cannot become
// permanent.
func (a *OomArbiter) isProtectedLocked(tenant string, pid int, at int64) bool {
	set := a.protects[tenant]
	if set == nil {
		return false
	}
	pin := set[pid]
	if pin == nil {
		return false
	}
	if !pin.Armed {
		pin.Armed = true
		pin.ExpireAt = at + a.opts.ProtectWindow
	}
	return at < pin.ExpireAt
}

// Pressure produces a plan for one pressure event without changing the
// decision ledger. It only records which event is current so stale plans can be
// detected at commit time.
func (a *OomArbiter) Pressure(ev PressureEvent) (Plan, error) {
	if ev.Window <= 0 || ev.Need == 0 {
		return Plan{}, ErrBadEvent
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.haveEvent = true
	a.lastWindow = ev.Window
	a.lastAt = ev.At

	// Plans for any other window can never be committed again; drop their
	// pending audit templates.
	for fp, audit := range a.audits {
		if audit.Window != ev.Window {
			delete(a.audits, fp)
		}
	}

	plan := Plan{Window: ev.Window}
	ranked := rankProcs(a.procs)
	if len(ranked) == 0 {
		plan.Shortfall = ev.Need
		return plan, nil
	}

	// A pressure event is node wide, but one plan never borrows memory from a
	// second tenant. The highest ranked process names the arbitrated tenant.
	target := ranked[0].Tenant
	replicaCount := 0
	for _, proc := range a.procs {
		if proc.Tenant == target {
			replicaCount++
		}
	}
	plan.Killable = replicaCount > 1

	needLeft := ev.Need
	capSet := a.opts.MaxReclaimPerWindow > 0
	capLeft := a.opts.MaxReclaimPerWindow
	audit := &planAudit{Window: ev.Window, Tenant: target}

	for _, proc := range ranked {
		if proc.Tenant != target {
			continue
		}
		if needLeft == 0 || (capSet && capLeft == 0) {
			break
		}

		if a.isProtectedLocked(target, proc.PID, ev.At) {
			audit.Items = append(audit.Items, Decision{
				Window: ev.Window, Tenant: target, PID: proc.PID,
				Action: ActionSkip, Reason: ReasonProtected,
			})
			continue
		}

		if replicaCount == 1 {
			audit.Items = append(audit.Items, Decision{
				Window: ev.Window, Tenant: target, PID: proc.PID,
				Action: ActionSkip, Reason: ReasonLastReplica,
			})
			continue
		}

		want := needLeft
		if capSet && capLeft < want {
			want = capLeft
		}
		take := proc.RSS
		if want < take {
			take = want
		}
		if take == 0 {
			break
		}

		victim := Victim{PID: proc.PID, Tenant: target, Bytes: take}
		action := ActionReclaim
		if take == proc.RSS {
			victim.Kill = true
			action = ActionKill
		}

		plan.Victims = append(plan.Victims, victim)
		plan.Reclaimed += take
		needLeft -= take
		if capSet {
			capLeft -= take
		}

		audit.Items = append(audit.Items, Decision{
			Window: ev.Window, Tenant: target, PID: proc.PID,
			Action: action, Bytes: take, Reason: ReasonWeightOrder,
		})

		// A partial reclaim is always the last victim: it either filled the
		// need or exhausted the per-window cap.
		if take < proc.RSS {
			break
		}
	}

	plan.Shortfall = needLeft
	a.audits[fingerprintPlan(plan)] = audit
	return plan, nil
}

// fingerprintPlan identifies one concrete plan so an exact resubmission can be
// rejected with ErrPlanCommitted while a revised plan for the same window can
// still flow through per-(window, pid) deduplication.
func fingerprintPlan(plan Plan) string {
	payload, _ := json.Marshal(plan)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// decisionsFromPlan builds ledger entries for a Commit whose plan was not
// produced by the latest Pressure call (for example a restored or crafted one).
func decisionsFromPlan(plan Plan) []Decision {
	items := make([]Decision, 0, len(plan.Victims))
	for _, victim := range plan.Victims {
		action := ActionReclaim
		if victim.Kill {
			action = ActionKill
		}
		items = append(items, Decision{
			Window: plan.Window,
			Tenant: victim.Tenant,
			PID:    victim.PID,
			Action: action,
			Bytes:  victim.Bytes,
			Reason: ReasonWeightOrder,
		})
	}
	return items
}

// Commit applies a previously produced plan. It is the only step that writes
// history or cumulative accounting. Validation and the whole read/modify/write
// happen in one critical section.
func (a *OomArbiter) Commit(plan Plan) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.haveEvent || plan.Window != a.lastWindow {
		return ErrStalePlan
	}
	fingerprint := fingerprintPlan(plan)
	if _, committed := a.committedFP[fingerprint]; committed {
		return ErrPlanCommitted
	}

	items := decisionsFromPlan(plan)
	if audit, ok := a.audits[fingerprint]; ok && audit != nil {
		items = audit.Items
	}

	accMap := a.accum[plan.Window]
	if accMap == nil {
		accMap = make(map[int]*pidAccum)
		a.accum[plan.Window] = accMap
	}

	affectedTenants := make(map[string]struct{})
	for _, item := range items {
		if item.Window == 0 {
			item.Window = plan.Window
		}
		affectedTenants[item.Tenant] = struct{}{}

		if item.Action == ActionSkip {
			a.history[item.Tenant] = append(a.history[item.Tenant], item)
			continue
		}

		acc := accMap[item.PID]
		if acc == nil {
			acc = &pidAccum{}
			accMap[item.PID] = acc
		}

		if item.Action == ActionKill && acc.Killed {
			// Same window and pid already counted as a kill: count the kill
			// once and only fold the newly reported released bytes in.
			if item.Bytes > acc.Released {
				acc.Released = item.Bytes
			}
			dedup := item
			dedup.Action = ActionReclaim
			dedup.Reason = ReasonWindowDedup
			a.history[item.Tenant] = append(a.history[item.Tenant], dedup)
			continue
		}

		if item.Action == ActionKill {
			acc.Killed = true
		}
		acc.Released += item.Bytes
		a.history[item.Tenant] = append(a.history[item.Tenant], item)
	}

	for tenant := range affectedTenants {
		a.trimHistoryLocked(tenant)
	}
	a.committedFP[fingerprint] = plan.Window
	return nil
}

// trimHistoryLocked drops the oldest entries beyond the per-tenant hard cap.
func (a *OomArbiter) trimHistoryLocked(tenant string) {
	limit := a.opts.MaxHistoryPerTenant
	history := a.history[tenant]
	if limit > 0 && len(history) > limit {
		a.history[tenant] = history[len(history)-limit:]
	}
}

// Protect pins the given processes of a tenant against arbitration until the
// event-time protection window elapses. Unknown or foreign pids are rejected.
func (a *OomArbiter) Protect(tenant string, pids []int) error {
	if strings.TrimSpace(tenant) == "" {
		return ErrBadTenant
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, pid := range pids {
		proc, ok := a.procs[pid]
		if !ok || proc.Tenant != tenant {
			return ErrUnknownPID
		}
	}

	set := a.protects[tenant]
	if set == nil {
		set = make(map[int]*protection)
		a.protects[tenant] = set
	}
	for _, pid := range pids {
		// A fresh, un-armed pin renews the window from the next event.
		set[pid] = &protection{}
	}
	return nil
}

// Unprotect lifts a previous protection entry.
func (a *OomArbiter) Unprotect(tenant string, pids []int) error {
	if strings.TrimSpace(tenant) == "" {
		return ErrBadTenant
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, pid := range pids {
		proc, ok := a.procs[pid]
		if !ok || proc.Tenant != tenant {
			return ErrUnknownPID
		}
	}

	if set := a.protects[tenant]; set != nil {
		for _, pid := range pids {
			delete(set, pid)
		}
		if len(set) == 0 {
			delete(a.protects, tenant)
		}
	}
	return nil
}

// History returns the committed decisions of one tenant in commit order. The
// returned slice is a copy and safe for callers to retain or modify.
func (a *OomArbiter) History(tenant string) []Decision {
	a.mu.Lock()
	defer a.mu.Unlock()

	source := a.history[tenant]
	if len(source) == 0 {
		return nil
	}
	out := make([]Decision, len(source))
	copy(out, source)
	return out
}

// Proc looks one process up by pid.
func (a *OomArbiter) Proc(pid int) (Proc, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	proc, ok := a.procs[pid]
	return proc, ok
}

// Tenants lists tenants that currently own at least one process, sorted.
func (a *OomArbiter) Tenants() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	seen := make(map[string]struct{})
	for _, proc := range a.procs {
		seen[proc.Tenant] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for tenant := range seen {
		out = append(out, tenant)
	}
	sort.Strings(out)
	return out
}

// Snapshot serialises the registry, protections and decision ledger together
// with commit deduplication state.
func (a *OomArbiter) Snapshot() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	procs := make([]Proc, 0, len(a.procs))
	for _, proc := range a.procs {
		procs = append(procs, proc)
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })

	data := snapshotData{
		Version:     snapshotVersion,
		Procs:       procs,
		History:     a.history,
		Protects:    a.protects,
		Accum:       a.accum,
		CommittedFP: a.committedFP,
		LastWindow:  a.lastWindow,
		LastAt:      a.lastAt,
		HaveEvent:   a.haveEvent,
	}
	return json.Marshal(&data)
}

// Restore replaces all arbiter state from a snapshot. The payload is fully
// decoded and validated before the live state is touched, so a bad snapshot
// leaves the running arbiter exactly as it was.
func (a *OomArbiter) Restore(payload []byte) error {
	if len(payload) == 0 {
		return ErrBadSnapshot
	}

	var data snapshotData
	if err := json.Unmarshal(payload, &data); err != nil {
		return ErrBadSnapshot
	}
	if data.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	procs := make(map[int]Proc, len(data.Procs))
	for _, proc := range data.Procs {
		if proc.PID <= 0 || proc.RSS == 0 || strings.TrimSpace(proc.Tenant) == "" {
			return ErrBadSnapshot
		}
		if _, dup := procs[proc.PID]; dup {
			return ErrBadSnapshot
		}
		procs[proc.PID] = proc
	}

	history := make(map[string][]Decision, len(data.History))
	for tenant, decisions := range data.History {
		if strings.TrimSpace(tenant) == "" {
			return ErrBadSnapshot
		}
		copied := append([]Decision(nil), decisions...)
		if limit := a.opts.MaxHistoryPerTenant; limit > 0 && len(copied) > limit {
			copied = copied[len(copied)-limit:]
		}
		history[tenant] = copied
	}

	protects := make(map[string]map[int]*protection, len(data.Protects))
	for tenant, set := range data.Protects {
		if strings.TrimSpace(tenant) == "" {
			return ErrBadSnapshot
		}
		copiedSet := make(map[int]*protection, len(set))
		for pid, pin := range set {
			if pin == nil {
				pin = &protection{}
			}
			pinCopy := *pin
			copiedSet[pid] = &pinCopy
		}
		protects[tenant] = copiedSet
	}

	accum := make(map[int64]map[int]*pidAccum, len(data.Accum))
	for window, set := range data.Accum {
		copiedSet := make(map[int]*pidAccum, len(set))
		for pid, acc := range set {
			if acc == nil {
				acc = &pidAccum{}
			}
			accCopy := *acc
			copiedSet[pid] = &accCopy
		}
		accum[window] = copiedSet
	}

	committedFP := make(map[string]int64, len(data.CommittedFP))
	for fp, window := range data.CommittedFP {
		committedFP[fp] = window
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.procs = procs
	a.history = history
	a.protects = protects
	a.accum = accum
	a.committedFP = committedFP
	a.lastWindow = data.LastWindow
	a.lastAt = data.LastAt
	a.haveEvent = data.HaveEvent
	a.audits = make(map[string]*planAudit)
	return nil
}
