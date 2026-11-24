// Package task bounds process creation per tenant with a token bucket, a
// per process fanout cap and a process tree depth cap.
package task

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Errors returned by the guard.
var (
	ErrBadTenant           = errors.New("fork burst guard: bad tenant")
	ErrBadEvent            = errors.New("fork burst guard: bad spawn event")
	ErrUnknownParent       = errors.New("fork burst guard: unknown parent")
	ErrDuplicatePID        = errors.New("fork burst guard: duplicate pid")
	ErrTenantBlocked       = errors.New("fork burst guard: tenant blocked")
	ErrRateLimited         = errors.New("fork burst guard: spawn rate limited")
	ErrFanoutExceeded      = errors.New("fork burst guard: fanout exceeded")
	ErrDepthExceeded       = errors.New("fork burst guard: tree depth exceeded")
	ErrBadSnapshot         = errors.New("fork burst guard: bad snapshot")
	ErrLivenessUnavailable = errors.New("fork burst guard: liveness callback required")
)

// Violation kinds recorded by the guard.
const (
	ViolationRate   = "rate"
	ViolationFanout = "fanout"
	ViolationDepth  = "depth"
)

// Options configures the token bucket, the tree bounds and the kill hook.
type Options struct {
	RatePerSecond int
	Burst         int
	MaxFanout     int
	MaxDepth      int
	Now           func() time.Time
	Kill          func(pid int) error
}

// SpawnEvent is one process creation observed by the guard.
type SpawnEvent struct {
	Tenant string
	PID    int
	PPID   int
}

// Budget is the remaining spawn capacity of one tenant.
type Budget struct {
	Tenant    string
	Tokens    int
	Capacity  int
	Processes int
	Blocked   bool
}

// Violation records one breached limit.
type Violation struct {
	Tenant string
	Kind   string
	PID    int
	At     time.Time
}

// procNode is one registered process. The depth is fixed at registration
// time so a vanished parent can never make the subtree look shallower.
type procNode struct {
	pid   int
	ppid  int
	depth int
}

// tenantState holds the registry, the token bucket and the block flag of
// one tenant.
type tenantState struct {
	procs    map[int]*procNode
	children map[int][]int
	blocked  bool
	tokens   float64
	last     time.Time
}

func newTenantState() *tenantState {
	return &tenantState{
		procs:    make(map[int]*procNode),
		children: make(map[int][]int),
	}
}

func (ts *tenantState) register(pid, ppid, depth int) {
	ts.procs[pid] = &procNode{pid: pid, ppid: ppid, depth: depth}
	if ppid != 0 {
		ts.children[ppid] = append(ts.children[ppid], pid)
	}
}

// leafToRoot lists every registered pid so that children always precede
// their parent.
func (ts *tenantState) leafToRoot() []int {
	order := make([]int, 0, len(ts.procs))
	visited := make(map[int]bool, len(ts.procs))
	var visit func(pid int)
	visit = func(pid int) {
		if visited[pid] {
			return
		}
		visited[pid] = true
		for _, child := range ts.children[pid] {
			visit(child)
		}
		order = append(order, pid)
	}
	for pid := range ts.procs {
		visit(pid)
	}
	return order
}

// ForkBurstGuard owns the per tenant registries and token buckets.
type ForkBurstGuard struct {
	mu         sync.RWMutex
	rate       int
	burst      int
	maxFanout  int
	maxDepth   int
	now        func() time.Time
	kill       func(pid int) error
	tenants    map[string]*tenantState
	violations []Violation
}

// NewForkBurstGuard builds a guard with the documented defaults.
func NewForkBurstGuard(opts Options) *ForkBurstGuard {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	kill := opts.Kill
	if kill == nil {
		kill = func(int) error { return nil }
	}
	burst := opts.Burst
	if burst < 0 {
		burst = 0
	}
	rate := opts.RatePerSecond
	if rate < 0 {
		rate = 0
	}
	return &ForkBurstGuard{
		rate:      rate,
		burst:     burst,
		maxFanout: opts.MaxFanout,
		maxDepth:  opts.MaxDepth,
		now:       now,
		kill:      kill,
		tenants:   make(map[string]*tenantState),
	}
}

// refill tops the bucket up to the elapsed whole milliseconds worth of
// tokens; the sub-millisecond remainder stays credited to the bucket.
func (g *ForkBurstGuard) refill(ts *tenantState, now time.Time) {
	if ts.last.IsZero() {
		ts.tokens = float64(g.burst)
		ts.last = now
		return
	}
	ms := now.Sub(ts.last).Milliseconds()
	if ms <= 0 {
		return
	}
	ts.tokens += float64(ms) * float64(g.rate) / 1000
	if cap := float64(g.burst); ts.tokens > cap {
		ts.tokens = cap
	}
	ts.last = ts.last.Add(time.Duration(ms) * time.Millisecond)
}

// tenant returns the state for one tenant, creating and refilling it.
// Callers must hold the write lock.
func (g *ForkBurstGuard) tenant(name string, now time.Time) *tenantState {
	ts, ok := g.tenants[name]
	if !ok {
		ts = newTenantState()
		g.tenants[name] = ts
	}
	g.refill(ts, now)
	return ts
}

// breach blocks the offending tenant and records exactly one violation.
func (g *ForkBurstGuard) breach(ts *tenantState, tenant, kind string, pid int, now time.Time) {
	ts.blocked = true
	g.violations = append(g.violations, Violation{Tenant: tenant, Kind: kind, PID: pid, At: now})
}

// Admit gates one spawn attempt and registers the new process on success.
func (g *ForkBurstGuard) Admit(ev SpawnEvent) error {
	if ev.Tenant == "" {
		return ErrBadTenant
	}
	if ev.PID <= 0 || ev.PPID < 0 {
		return ErrBadEvent
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	ts := g.tenant(ev.Tenant, now)
	if ts.blocked {
		return ErrTenantBlocked
	}
	if _, ok := ts.procs[ev.PID]; ok {
		return ErrDuplicatePID
	}
	depth := 1
	if ev.PPID != 0 {
		parent, ok := ts.procs[ev.PPID]
		if !ok {
			return ErrUnknownParent
		}
		if g.maxFanout > 0 && len(ts.children[ev.PPID]) >= g.maxFanout {
			g.breach(ts, ev.Tenant, ViolationFanout, ev.PPID, now)
			return ErrFanoutExceeded
		}
		depth = parent.depth + 1
	}
	if g.maxDepth > 0 && depth > g.maxDepth {
		g.breach(ts, ev.Tenant, ViolationDepth, ev.PID, now)
		return ErrDepthExceeded
	}
	if ts.tokens < 1 {
		g.breach(ts, ev.Tenant, ViolationRate, ev.PID, now)
		return ErrRateLimited
	}
	ts.tokens--
	ts.register(ev.PID, ev.PPID, depth)
	return nil
}

// Budget reports the remaining capacity of one tenant.
func (g *ForkBurstGuard) Budget(tenant string) Budget {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := Budget{Tenant: tenant, Capacity: g.burst}
	ts, ok := g.tenants[tenant]
	if !ok {
		b.Tokens = g.burst
		return b
	}
	g.refill(ts, g.now())
	b.Tokens = int(ts.tokens)
	b.Processes = len(ts.procs)
	b.Blocked = ts.blocked
	return b
}

// Attach registers a process that was discovered after it had already forked.
func (g *ForkBurstGuard) Attach(tenant string, pid, ppid int) error {
	if tenant == "" {
		return ErrBadTenant
	}
	if pid <= 0 || ppid < 0 {
		return ErrBadEvent
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	ts := g.tenant(tenant, now)
	if ts.blocked {
		return ErrTenantBlocked
	}
	if node, ok := ts.procs[pid]; ok {
		if node.ppid == ppid {
			return nil
		}
		return ErrDuplicatePID
	}
	depth := 1
	if ppid != 0 {
		parent, ok := ts.procs[ppid]
		if !ok {
			return ErrUnknownParent
		}
		if g.maxFanout > 0 && len(ts.children[ppid]) >= g.maxFanout {
			g.breach(ts, tenant, ViolationFanout, ppid, now)
			return ErrFanoutExceeded
		}
		depth = parent.depth + 1
	}
	if g.maxDepth > 0 && depth > g.maxDepth {
		g.breach(ts, tenant, ViolationDepth, pid, now)
		return ErrDepthExceeded
	}
	ts.register(pid, ppid, depth)
	return nil
}

// Reap terminates the condemned subtrees of one tenant.
func (g *ForkBurstGuard) Reap(tenant string) ([]int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ts, ok := g.tenants[tenant]
	if !ok || !ts.blocked {
		return nil, nil
	}
	// The write lock freezes the subtree: no Admit or Attach can register
	// into it while the kill signals are delivered leaf to root.
	order := ts.leafToRoot()
	for _, pid := range order {
		// A process that exited on its own mid-reap is not a failure.
		_ = g.kill(pid)
	}
	ts.procs = make(map[int]*procNode)
	ts.children = make(map[int][]int)
	ts.blocked = false
	return order, nil
}

// Violations returns the recorded violations in occurrence order.
func (g *ForkBurstGuard) Violations() []Violation {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]Violation(nil), g.violations...)
}

// ProcessCount reports how many processes of one tenant are registered.
func (g *ForkBurstGuard) ProcessCount(tenant string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if ts, ok := g.tenants[tenant]; ok {
		return len(ts.procs)
	}
	return 0
}

// Children lists the registered children of one pid.
func (g *ForkBurstGuard) Children(pid int) []int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, ts := range g.tenants {
		if kids, ok := ts.children[pid]; ok {
			return append([]int(nil), kids...)
		}
	}
	return nil
}

const snapshotVersion = 1

type procSnapshot struct {
	PID   int `json:"pid"`
	PPID  int `json:"ppid"`
	Depth int `json:"depth"`
}

type tenantSnapshot struct {
	Blocked bool           `json:"blocked"`
	Tokens  float64        `json:"tokens"`
	Procs   []procSnapshot `json:"procs"`
}

type snapshotFile struct {
	Version int                       `json:"version"`
	Tenants map[string]tenantSnapshot `json:"tenants"`
}

// Snapshot serialises every tenant registry and bucket.
func (g *ForkBurstGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	snap := snapshotFile{Version: snapshotVersion, Tenants: make(map[string]tenantSnapshot, len(g.tenants))}
	for name, ts := range g.tenants {
		g.refill(ts, now)
		tsnap := tenantSnapshot{Blocked: ts.blocked, Tokens: ts.tokens}
		for _, node := range ts.procs {
			tsnap.Procs = append(tsnap.Procs, procSnapshot{PID: node.pid, PPID: node.ppid, Depth: node.depth})
		}
		snap.Tenants[name] = tsnap
	}
	return json.Marshal(snap)
}

// Restore loads a snapshot and drops processes the liveness callback reports gone.
func (g *ForkBurstGuard) Restore(b []byte, alive func(int) bool) error {
	if alive == nil {
		return ErrLivenessUnavailable
	}
	var snap snapshotFile
	if err := json.Unmarshal(b, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.Version != snapshotVersion || snap.Tenants == nil {
		return ErrBadSnapshot
	}
	tenants := make(map[string]*tenantState, len(snap.Tenants))
	for name, tsnap := range snap.Tenants {
		if name == "" || tsnap.Tokens < 0 {
			return ErrBadSnapshot
		}
		ts := newTenantState()
		ts.blocked = tsnap.Blocked
		ts.tokens = tsnap.Tokens
		if cap := float64(g.burst); ts.tokens > cap {
			ts.tokens = cap
		}
		for _, p := range tsnap.Procs {
			if p.PID <= 0 || p.PPID < 0 || p.Depth < 1 {
				return ErrBadSnapshot
			}
			if _, dup := ts.procs[p.PID]; dup {
				return ErrBadSnapshot
			}
			if !alive(p.PID) {
				continue
			}
			ts.procs[p.PID] = &procNode{pid: p.PID, ppid: p.PPID, depth: p.Depth}
		}
		// Clean up parent-child relations: a survivor whose parent is gone
		// becomes a root but keeps its recorded depth.
		for _, node := range ts.procs {
			if node.ppid != 0 {
				if _, ok := ts.procs[node.ppid]; !ok {
					node.ppid = 0
				}
			}
		}
		for _, node := range ts.procs {
			if node.ppid != 0 {
				ts.children[node.ppid] = append(ts.children[node.ppid], node.pid)
			}
		}
		tenants[name] = ts
	}
	now := g.now()
	for _, ts := range tenants {
		ts.last = now
	}
	g.mu.Lock()
	g.tenants = tenants
	g.mu.Unlock()
	return nil
}
