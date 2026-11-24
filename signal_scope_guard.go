// Package task decides whether a signal may travel between two tracked
// processes and audits every delivery attempt.
package task

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

// Signal names understood by the guard.
type Signal string

// Known signals.
const (
	SigTerm Signal = "TERM"
	SigKill Signal = "KILL"
	SigHup  Signal = "HUP"
	SigStop Signal = "STOP"
)

// Verdict codes carried by allow and audit records.
const (
	CodeSelfSignal     = "self_signal"
	CodeSameDomain     = "same_domain"
	CodeGuardianBridge = "guardian_bridge"
	CodeForeignDomain  = "foreign_domain"
	CodeRateLimited    = "rate_limited"
)

// Errors reported by the guard.
var (
	ErrBadProcess      = errors.New("process registration is incomplete")
	ErrBadSignal       = errors.New("signal name is unknown")
	ErrPidTaken        = errors.New("pid is already registered")
	ErrRegistryFull    = errors.New("process registry is full")
	ErrUnknownTarget   = errors.New("process is not registered")
	ErrScopeDenied     = errors.New("delivery is outside the caller scope")
	ErrSignalThrottled = errors.New("sender signal budget is exhausted")
	ErrRestoreDamaged  = errors.New("scope snapshot is damaged")
)

// Proc is one registered process.
type Proc struct {
	Pid       int    `json:"pid"`
	Domain    string `json:"domain"`
	StartTime int64  `json:"start_time"`
	Comm      string `json:"comm,omitempty"`
	Guardian  bool   `json:"guardian"`
}

// Verdict is the answer to a permission question.
type Verdict struct {
	Allow bool   `json:"allow"`
	Code  string `json:"code"`
}

// Record is one audited delivery attempt.
type Record struct {
	Seq     int    `json:"seq"`
	Sig     Signal `json:"sig"`
	From    int    `json:"from"`
	To      int    `json:"to"`
	Allowed bool   `json:"allowed"`
	Code    string `json:"code"`
}

// Options configures the registry, the cross domain allowlist and the sender budget.
type Options struct {
	Burst           int          `json:"burst"`
	WindowMs        int64        `json:"window_ms"`
	MaxProcs        int          `json:"max_procs"`
	GuardianSignals []Signal     `json:"guardian_signals,omitempty"`
	Now             func() int64 `json:"-"`
}

// maxAuditRecords caps the on-disk audit trail. Oldest records are dropped
// once the cap is reached, sequence numbers stay gap free.
const maxAuditRecords = 4096

// senderBudget is the fixed window accounting for one sender pid.
type senderBudget struct {
	WindowStart int64 `json:"window_start"`
	Used        int   `json:"used"`
	KillUsed    int   `json:"kill_used"`
}

// snapshotPayload is the serialisable state of a guard.
type snapshotPayload struct {
	Procs      []Proc               `json:"procs"`
	Audit      []Record             `json:"audit"`
	NextSeq    int                  `json:"next_seq"`
	Generation int64                `json:"generation"`
	Budgets    map[int]senderBudget `json:"budgets"`
}

// SignalScopeGuard owns the process registry and the audit trail.
type SignalScopeGuard struct {
	opts       Options
	mu         sync.Mutex
	procs      map[int]Proc
	audits     []Record
	budgets    map[int]senderBudget
	nextSeq    int
	generation int64
}

// NewSignalScopeGuard builds a guard with the given policy.
func NewSignalScopeGuard(opts Options) *SignalScopeGuard {
	return &SignalScopeGuard{
		opts:    opts,
		procs:   make(map[int]Proc),
		budgets: make(map[int]senderBudget),
	}
}

// Register adds one process to the registry.
func (g *SignalScopeGuard) Register(proc Proc) error {
	if proc.Pid <= 0 || strings.TrimSpace(proc.Domain) == "" {
		return ErrBadProcess
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.procs[proc.Pid]; exists {
		return ErrPidTaken
	}
	if g.opts.MaxProcs > 0 && len(g.procs) >= g.opts.MaxProcs {
		return ErrRegistryFull
	}
	g.generation++
	g.procs[proc.Pid] = proc
	return nil
}

// Unregister removes one process from the registry.
func (g *SignalScopeGuard) Unregister(pid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.procs[pid]; !ok {
		return ErrUnknownTarget
	}
	delete(g.procs, pid)
	// Advancing the generation invalidates any verdict cached for the pid
	// before the process went away; a later reuse starts a fresh generation.
	g.generation++
	return nil
}

// Allow answers whether a signal would be permitted, without consuming budget.
func (g *SignalScopeGuard) Allow(sig Signal, from, to int) (Verdict, error) {
	if sig == "" {
		return Verdict{}, ErrBadSignal
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	sender, ok := g.procs[from]
	if !ok {
		return Verdict{}, ErrUnknownTarget
	}
	target, ok := g.procs[to]
	if !ok {
		return Verdict{}, ErrUnknownTarget
	}
	return g.verdict(sig, sender, target), nil
}

// verdict computes the scope decision for two live registrations. Callers
// must hold g.mu.
func (g *SignalScopeGuard) verdict(sig Signal, sender, target Proc) Verdict {
	if sender.Pid == target.Pid {
		return Verdict{Allow: true, Code: CodeSelfSignal}
	}
	if sender.Domain == target.Domain {
		return Verdict{Allow: true, Code: CodeSameDomain}
	}
	if sender.Guardian {
		for _, allowed := range g.opts.GuardianSignals {
			if allowed == sig {
				return Verdict{Allow: true, Code: CodeGuardianBridge}
			}
		}
	}
	return Verdict{Allow: false, Code: CodeForeignDomain}
}

func (g *SignalScopeGuard) now() int64 {
	if g.opts.Now != nil {
		return g.opts.Now()
	}
	return 0
}

// windowFor rolls the fixed sender window forward when now leaves it.
// Callers must hold g.mu.
func (g *SignalScopeGuard) windowFor(from int, now int64) senderBudget {
	budget, ok := g.budgets[from]
	if !ok || (g.opts.WindowMs > 0 && now-budget.WindowStart >= g.opts.WindowMs) {
		return senderBudget{WindowStart: now}
	}
	return budget
}

// Deliver revalidates the scope, applies the sender budget and audits the result.
func (g *SignalScopeGuard) Deliver(sig Signal, from, to int) error {
	if sig == "" {
		return ErrBadSignal
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	sender, ok := g.procs[from]
	if !ok {
		return ErrUnknownTarget
	}
	target, ok := g.procs[to]
	if !ok {
		return ErrUnknownTarget
	}

	// Revalidate from the live registrations every time: a verdict cached
	// against an older generation must not survive pid reuse.
	verdict := g.verdict(sig, sender, target)

	var err error
	if !verdict.Allow {
		err = ErrScopeDenied
	} else if g.opts.Burst > 0 && sig != SigKill {
		budget := g.windowFor(from, now)
		if budget.Used >= g.opts.Burst {
			verdict = Verdict{Allow: false, Code: CodeRateLimited}
			err = ErrSignalThrottled
		} else {
			budget.Used++
			g.budgets[from] = budget
		}
	} else if sig == SigKill {
		// SIGKILL can never be throttled, but it still consumes accounting
		// so the storm shows up in the sender's audit trail and snapshot.
		budget := g.windowFor(from, now)
		budget.KillUsed++
		g.budgets[from] = budget
	}

	g.appendRecord(Record{
		Seq:     g.nextSeq + 1,
		Sig:     sig,
		From:    from,
		To:      to,
		Allowed: verdict.Allow,
		Code:    verdict.Code,
	})
	return err
}

// RateLimit returns the remaining signal budget for a sender at the given time.
func (g *SignalScopeGuard) RateLimit(from int, now int64) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.opts.Burst <= 0 {
		return 0, nil
	}
	budget, ok := g.budgets[from]
	if !ok {
		return g.opts.Burst, nil
	}
	if g.opts.WindowMs > 0 && now-budget.WindowStart >= g.opts.WindowMs {
		return g.opts.Burst, nil
	}
	left := g.opts.Burst - budget.Used
	if left < 0 {
		left = 0
	}
	return left, nil
}

// appendRecord keeps the audit trail gap free and within its hard cap.
// Callers must hold g.mu.
func (g *SignalScopeGuard) appendRecord(rec Record) {
	g.nextSeq = rec.Seq
	if len(g.audits) >= maxAuditRecords {
		copy(g.audits, g.audits[1:])
		g.audits[len(g.audits)-1] = rec
		return
	}
	g.audits = append(g.audits, rec)
}

// Audit returns every recorded delivery attempt in order.
func (g *SignalScopeGuard) Audit() []Record {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Record, len(g.audits))
	copy(out, g.audits)
	return out
}

// Snapshot serialises the registry and the audit trail.
func (g *SignalScopeGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	procs := make([]Proc, 0, len(g.procs))
	for _, proc := range g.procs {
		procs = append(procs, proc)
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].Pid < procs[j].Pid })

	audits := make([]Record, len(g.audits))
	copy(audits, g.audits)

	budgets := make(map[int]senderBudget, len(g.budgets))
	for pid, budget := range g.budgets {
		budgets[pid] = budget
	}

	payload := snapshotPayload{
		Procs:      procs,
		Audit:      audits,
		NextSeq:    g.nextSeq,
		Generation: g.generation,
		Budgets:    budgets,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces the registry and the audit trail from a snapshot.
func (g *SignalScopeGuard) Restore(payload []byte) error {
	var snap snapshotPayload
	if err := json.Unmarshal(payload, &snap); err != nil {
		return ErrRestoreDamaged
	}
	if snap.Procs == nil || snap.Audit == nil || snap.Budgets == nil {
		return ErrRestoreDamaged
	}
	if g.opts.MaxProcs > 0 && len(snap.Procs) > g.opts.MaxProcs {
		return ErrRestoreDamaged
	}

	procs := make(map[int]Proc, len(snap.Procs))
	for _, proc := range snap.Procs {
		if proc.Pid <= 0 || strings.TrimSpace(proc.Domain) == "" {
			return ErrRestoreDamaged
		}
		if _, dup := procs[proc.Pid]; dup {
			return ErrRestoreDamaged
		}
		procs[proc.Pid] = proc
	}

	var prevSeq int
	for _, rec := range snap.Audit {
		if rec.Seq != prevSeq+1 {
			return ErrRestoreDamaged
		}
		prevSeq = rec.Seq
	}
	if snap.NextSeq != prevSeq {
		return ErrRestoreDamaged
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.procs = procs
	g.audits = make([]Record, len(snap.Audit))
	copy(g.audits, snap.Audit)
	g.budgets = make(map[int]senderBudget, len(snap.Budgets))
	for pid, budget := range snap.Budgets {
		g.budgets[pid] = budget
	}
	g.nextSeq = snap.NextSeq
	g.generation = snap.Generation
	return nil
}

// Lookup returns one registered process by pid.
func (g *SignalScopeGuard) Lookup(pid int) (Proc, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	proc, ok := g.procs[pid]
	return proc, ok
}

// Registered lists the registry in stable pid order.
func (g *SignalScopeGuard) Registered() []Proc {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Proc, 0, len(g.procs))
	for _, proc := range g.procs {
		out = append(out, proc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pid < out[j].Pid })
	return out
}
