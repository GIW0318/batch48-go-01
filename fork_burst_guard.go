// Package task bounds process creation per tenant with a token bucket, a
// per process fanout cap and a process tree depth cap.
package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("fork burst guard: not implemented")

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

// ForkBurstGuard owns the per tenant registries and token buckets.
type ForkBurstGuard struct {
}

// NewForkBurstGuard builds a guard with the documented defaults.
func NewForkBurstGuard(opts Options) *ForkBurstGuard {
	return &ForkBurstGuard{}
}

// Admit gates one spawn attempt and registers the new process on success.
func (g *ForkBurstGuard) Admit(ev SpawnEvent) error {
	return errNotImplemented
}

// Budget reports the remaining capacity of one tenant.
func (g *ForkBurstGuard) Budget(tenant string) Budget {
	return Budget{}
}

// Attach registers a process that was discovered after it had already forked.
func (g *ForkBurstGuard) Attach(tenant string, pid, ppid int) error {
	return errNotImplemented
}

// Reap terminates the condemned subtrees of one tenant.
func (g *ForkBurstGuard) Reap(tenant string) ([]int, error) {
	return nil, errNotImplemented
}

// Violations returns the recorded violations in occurrence order.
func (g *ForkBurstGuard) Violations() []Violation {
	return nil
}

// ProcessCount reports how many processes of one tenant are registered.
func (g *ForkBurstGuard) ProcessCount(tenant string) int {
	return 0
}

// Children lists the registered children of one pid.
func (g *ForkBurstGuard) Children(pid int) []int {
	return nil
}

// Snapshot serialises every tenant registry and bucket.
func (g *ForkBurstGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot and drops processes the liveness callback reports gone.
func (g *ForkBurstGuard) Restore(b []byte, alive func(int) bool) error {
	return errNotImplemented
}
