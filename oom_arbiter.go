// Package task arbitrates which processes give memory back when a pressure
// event lands, keeping planning and committing as separate steps.
package task

import "errors"

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

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

// OomArbiter owns the process registry, protections and decision ledger.
type OomArbiter struct {
	opts Options
}

// NewOomArbiter builds an arbiter with the given table bounds.
func NewOomArbiter(opts Options) *OomArbiter {
	return &OomArbiter{opts: opts}
}

// Register adds one process to the arbitration input.
func (a *OomArbiter) Register(proc Proc) error {
	return errNotImplemented
}

// Pressure produces a plan for one pressure event without changing any ledger.
func (a *OomArbiter) Pressure(ev PressureEvent) (Plan, error) {
	return Plan{}, errNotImplemented
}

// Commit applies a previously produced plan.
func (a *OomArbiter) Commit(plan Plan) error {
	return errNotImplemented
}

// Protect pins the given processes of a tenant.
func (a *OomArbiter) Protect(tenant string, pids []int) error {
	return errNotImplemented
}

// Unprotect lifts a previous protection entry.
func (a *OomArbiter) Unprotect(tenant string, pids []int) error {
	return errNotImplemented
}

// History returns the committed decisions of one tenant in order.
func (a *OomArbiter) History(tenant string) []Decision {
	return nil
}

// Proc looks one process up by pid.
func (a *OomArbiter) Proc(pid int) (Proc, bool) {
	return Proc{}, false
}

// Tenants lists tenants that currently own at least one process.
func (a *OomArbiter) Tenants() []string {
	return nil
}

// Snapshot serialises the registry, protections and decision ledger.
func (a *OomArbiter) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the arbiter state from a snapshot.
func (a *OomArbiter) Restore(payload []byte) error {
	return errNotImplemented
}
