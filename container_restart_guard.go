// Package task turns one container exit report into a restart decision,
// with exponential backoff, jitter and per-container crash breakers.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

// Outcomes attached to decisions and history entries.
const (
	OutcomeNormal        = "normal_exit"
	OutcomeStopSignal    = "stop_signal"
	OutcomeConfigError   = "config_error"
	OutcomeExternalKill  = "external_kill"
	OutcomeRetry         = "retry"
	OutcomeBreakerOpen   = "breaker_open"
	OutcomeOperatorReset = "operator_reset"
)

// Errors reported by the guard.
var (
	ErrUnknownContainer = errors.New("container is not tracked")
	ErrBadExitStatus    = errors.New("exit status is incomplete")
	ErrBreakerOpen      = errors.New("restart breaker is open")
	ErrCorruptSnapshot  = errors.New("restart snapshot is corrupt")
	ErrEmptyOperator    = errors.New("operator identity is required")
)

// ExitStatus is what the runtime observed when the container went away.
type ExitStatus struct {
	Code      int    `json:"code"`
	Signal    string `json:"signal,omitempty"`
	OOMKilled bool   `json:"oom_killed"`
}

// Decision is the restart verdict produced for one report.
type Decision struct {
	Restart  bool   `json:"restart"`
	DelayMs  int64  `json:"delay_ms"`
	ResumeAt int64  `json:"resume_at"`
	Outcome  string `json:"outcome"`
}

// Attempt is one history entry for a container.
type Attempt struct {
	Seq      int    `json:"seq"`
	At       int64  `json:"at"`
	Code     int    `json:"code"`
	Signal   string `json:"signal,omitempty"`
	Outcome  string `json:"outcome"`
	ResumeAt int64  `json:"resume_at"`
}

// Options configures backoff, breaker thresholds and history bounds.
type Options struct {
	BaseDelayMs     int64 `json:"base_delay_ms"`
	MaxDelayMs      int64 `json:"max_delay_ms"`
	JitterPct       int   `json:"jitter_pct"`
	MaxCrashes      int   `json:"max_crashes"`
	StableWindowMs  int64 `json:"stable_window_ms"`
	MaxAttempts     int   `json:"max_attempts"`
	ConfigExitCodes []int `json:"config_exit_codes,omitempty"`
}

// RestartPolicyGuard keeps one independent state machine per container.
type RestartPolicyGuard struct {
	opts Options
	mu   sync.Mutex
}

// NewRestartPolicyGuard builds a guard with the given policy.
func NewRestartPolicyGuard(opts Options) *RestartPolicyGuard {
	return &RestartPolicyGuard{opts: opts}
}

// Report records one exit and returns the restart decision for it.
func (g *RestartPolicyGuard) Report(container string, status ExitStatus, at int64) (Decision, error) {
	return Decision{}, errNotImplemented
}

// Due returns the absolute time of the next restart and whether now has reached it.
func (g *RestartPolicyGuard) Due(container string, now int64) (int64, bool) {
	return 0, false
}

// CircuitOpen reports whether the breaker for the container is open.
func (g *RestartPolicyGuard) CircuitOpen(container string) bool {
	return false
}

// Reset clears counters and breaker state after an operator start.
func (g *RestartPolicyGuard) Reset(container string, by string) error {
	return errNotImplemented
}

// History lists the recorded attempts for one container.
func (g *RestartPolicyGuard) History(container string) []Attempt {
	return nil
}

// Snapshot serialises every container state machine.
func (g *RestartPolicyGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces every container state machine from a snapshot.
func (g *RestartPolicyGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Backoff returns the uncapped-then-capped base delay for an attempt count.
func (g *RestartPolicyGuard) Backoff(attempts int) int64 {
	return 0
}

// Crashes reports the current consecutive crash count for a container.
func (g *RestartPolicyGuard) Crashes(container string) int {
	return 0
}

// Containers lists tracked containers in stable order.
func (g *RestartPolicyGuard) Containers() []string {
	return nil
}

// Policy returns the configuration the guard was built with.
func (g *RestartPolicyGuard) Policy() Options {
	return g.opts
}
