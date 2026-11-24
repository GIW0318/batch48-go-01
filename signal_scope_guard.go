// Package task decides whether a signal may travel between two tracked
// processes and audits every delivery attempt.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

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

// SignalScopeGuard owns the process registry and the audit trail.
type SignalScopeGuard struct {
	opts Options
	mu   sync.Mutex
}

// NewSignalScopeGuard builds a guard with the given policy.
func NewSignalScopeGuard(opts Options) *SignalScopeGuard {
	return &SignalScopeGuard{opts: opts}
}

// Register adds one process to the registry.
func (g *SignalScopeGuard) Register(proc Proc) error {
	return errNotImplemented
}

// Unregister removes one process from the registry.
func (g *SignalScopeGuard) Unregister(pid int) error {
	return errNotImplemented
}

// Allow answers whether a signal would be permitted, without consuming budget.
func (g *SignalScopeGuard) Allow(sig Signal, from, to int) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// Deliver revalidates the scope, applies the sender budget and audits the result.
func (g *SignalScopeGuard) Deliver(sig Signal, from, to int) error {
	return errNotImplemented
}

// RateLimit returns the remaining signal budget for a sender at the given time.
func (g *SignalScopeGuard) RateLimit(from int, now int64) (int, error) {
	return 0, errNotImplemented
}

// Audit returns every recorded delivery attempt in order.
func (g *SignalScopeGuard) Audit() []Record {
	return nil
}

// Snapshot serialises the registry and the audit trail.
func (g *SignalScopeGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the registry and the audit trail from a snapshot.
func (g *SignalScopeGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Lookup returns one registered process by pid.
func (g *SignalScopeGuard) Lookup(pid int) (Proc, bool) {
	return Proc{}, false
}

// Registered lists the registry in stable pid order.
func (g *SignalScopeGuard) Registered() []Proc {
	return nil
}

// Policy returns the configuration the guard was built with.
func (g *SignalScopeGuard) Policy() Options {
	return g.opts
}
