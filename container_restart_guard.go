// Package task turns one container exit report into a restart decision,
// with exponential backoff, jitter and per-container crash breakers.
package task

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"
)

const defaultMaxAttempts = 256
const snapshotVersion = 1

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
	mu         sync.Mutex
	opts       Options
	containers map[string]*containerState
}

type containerState struct {
	attempts        []Attempt
	crashes         int
	breakerOpen     bool
	breakerReported bool
	hasRestart      bool
	nextRestartAt   int64
	lastCrashAt     int64
	lastAttemptAt   int64
	seq             int
}

// NewRestartPolicyGuard builds a guard with the given policy.
func NewRestartPolicyGuard(opts Options) *RestartPolicyGuard {
	return &RestartPolicyGuard{
		opts:       cloneOptions(opts),
		containers: make(map[string]*containerState),
	}
}

// Report records one exit and returns the restart decision for it.
func (g *RestartPolicyGuard) Report(container string, status ExitStatus, at int64) (Decision, error) {
	if status.Code < 0 {
		return Decision{}, ErrBadExitStatus
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.containerLocked(container)
	effectiveAt := at
	if state.seq > 0 && effectiveAt < state.lastAttemptAt {
		effectiveAt = state.lastAttemptAt
	}

	g.applyStableWindowLocked(state, effectiveAt)

	// A fresh exit report supersedes any restart that was scheduled for the
	// previous exit. Classification below may schedule a new one.
	state.hasRestart = false
	state.nextRestartAt = 0

	if state.breakerOpen {
		if !state.breakerReported {
			g.appendAttemptLocked(state, Attempt{
				At:      effectiveAt,
				Code:    status.Code,
				Signal:  status.Signal,
				Outcome: OutcomeBreakerOpen,
			})
			state.breakerReported = true
		}
		return Decision{Outcome: OutcomeBreakerOpen}, ErrBreakerOpen
	}

	switch {
	case status.Code == 0:
		g.clearCrashStreakLocked(state)
		g.appendAttemptLocked(state, Attempt{
			At:      effectiveAt,
			Code:    status.Code,
			Signal:  status.Signal,
			Outcome: OutcomeNormal,
		})
		return Decision{Outcome: OutcomeNormal}, nil

	case g.isConfigExitCode(status.Code):
		g.appendAttemptLocked(state, Attempt{
			At:      effectiveAt,
			Code:    status.Code,
			Signal:  status.Signal,
			Outcome: OutcomeConfigError,
		})
		return Decision{Outcome: OutcomeConfigError}, nil

	case status.OOMKilled:
		return g.scheduleRetryLocked(container, state, status, effectiveAt)

	case isNormalStopSignal(status.Signal):
		g.clearCrashStreakLocked(state)
		g.appendAttemptLocked(state, Attempt{
			At:      effectiveAt,
			Code:    status.Code,
			Signal:  status.Signal,
			Outcome: OutcomeStopSignal,
		})
		return Decision{Outcome: OutcomeStopSignal}, nil

	case strings.TrimSpace(status.Signal) != "":
		g.appendAttemptLocked(state, Attempt{
			At:      effectiveAt,
			Code:    status.Code,
			Signal:  status.Signal,
			Outcome: OutcomeExternalKill,
		})
		return Decision{Outcome: OutcomeExternalKill}, nil

	default:
		return g.scheduleRetryLocked(container, state, status, effectiveAt)
	}
}

// Due returns the absolute time of the next restart and whether now has reached it.
func (g *RestartPolicyGuard) Due(container string, now int64) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.containers[container]
	if state == nil || !state.hasRestart {
		return 0, false
	}
	return state.nextRestartAt, now >= state.nextRestartAt
}

// CircuitOpen reports whether the breaker for the container is open.
func (g *RestartPolicyGuard) CircuitOpen(container string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.containers[container]
	return state != nil && state.breakerOpen
}

// Reset clears counters and breaker state after an operator start.
func (g *RestartPolicyGuard) Reset(container string, by string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.containers[container]
	if state == nil {
		return ErrUnknownContainer
	}
	if strings.TrimSpace(by) == "" {
		return ErrEmptyOperator
	}

	state.crashes = 0
	state.breakerOpen = false
	state.breakerReported = false
	state.hasRestart = false
	state.nextRestartAt = 0
	state.lastCrashAt = 0
	g.appendAttemptLocked(state, Attempt{
		At:      state.lastAttemptAt,
		Outcome: OutcomeOperatorReset,
	})
	return nil
}

// History lists the recorded attempts for one container.
func (g *RestartPolicyGuard) History(container string) []Attempt {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.containers[container]
	if state == nil {
		return nil
	}
	out := make([]Attempt, len(state.attempts))
	copy(out, state.attempts)
	return out
}

// Snapshot serialises every container state machine.
func (g *RestartPolicyGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := guardSnapshot{
		Version:    snapshotVersion,
		Options:    g.opts,
		Containers: make(map[string]containerSnapshot, len(g.containers)),
	}
	for name, state := range g.containers {
		snap.Containers[name] = containerSnapshot{
			Attempts:        append([]Attempt(nil), state.attempts...),
			Crashes:         state.crashes,
			BreakerOpen:     state.breakerOpen,
			BreakerReported: state.breakerReported,
			HasRestart:      state.hasRestart,
			NextRestartAt:   state.nextRestartAt,
			LastCrashAt:     state.lastCrashAt,
			LastAttemptAt:   state.lastAttemptAt,
			Seq:             state.seq,
		}
	}
	return json.Marshal(snap)
}

// Restore replaces every container state machine from a snapshot.
func (g *RestartPolicyGuard) Restore(payload []byte) error {
	var snap guardSnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return fmt.Errorf("%w: %v", ErrCorruptSnapshot, err)
	}
	if snap.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported snapshot version %d", ErrCorruptSnapshot, snap.Version)
	}

	limit := effectiveMaxAttempts(snap.Options)
	restored := make(map[string]*containerState, len(snap.Containers))
	for name, stored := range snap.Containers {
		if stored.Crashes < 0 || stored.Seq < 0 {
			return fmt.Errorf("%w: invalid counters for container %q", ErrCorruptSnapshot, name)
		}
		if len(stored.Attempts) > limit {
			return fmt.Errorf("%w: history limit exceeded for container %q", ErrCorruptSnapshot, name)
		}
		for i, attempt := range stored.Attempts {
			if attempt.Code < 0 || attempt.Seq < 0 {
				return fmt.Errorf("%w: invalid attempt for container %q", ErrCorruptSnapshot, name)
			}
			if i > 0 && attempt.At < stored.Attempts[i-1].At {
				return fmt.Errorf("%w: history is not chronological for container %q", ErrCorruptSnapshot, name)
			}
		}
		if stored.BreakerOpen && stored.Crashes <= 0 {
			return fmt.Errorf("%w: open breaker without crashes for container %q", ErrCorruptSnapshot, name)
		}
		if stored.BreakerReported && !stored.BreakerOpen {
			return fmt.Errorf("%w: breaker report without an open breaker for container %q", ErrCorruptSnapshot, name)
		}

		restored[name] = &containerState{
			attempts:        append([]Attempt(nil), stored.Attempts...),
			crashes:         stored.Crashes,
			breakerOpen:     stored.BreakerOpen,
			breakerReported: stored.BreakerReported,
			hasRestart:      stored.HasRestart,
			nextRestartAt:   stored.NextRestartAt,
			lastCrashAt:     stored.LastCrashAt,
			lastAttemptAt:   stored.LastAttemptAt,
			seq:             stored.Seq,
		}
	}

	g.mu.Lock()
	g.opts = cloneOptions(snap.Options)
	g.containers = restored
	g.mu.Unlock()
	return nil
}

// Backoff returns the uncapped-then-capped base delay for an attempt count.
func (g *RestartPolicyGuard) Backoff(attempts int) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return backoffFor(g.opts, attempts)
}

// Crashes reports the current consecutive crash count for a container.
func (g *RestartPolicyGuard) Crashes(container string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.containers[container]
	if state == nil {
		return 0
	}
	return state.crashes
}

// Containers lists tracked containers in stable order.
func (g *RestartPolicyGuard) Containers() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	names := make([]string, 0, len(g.containers))
	for name := range g.containers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Policy returns the configuration the guard was built with.
func (g *RestartPolicyGuard) Policy() Options {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cloneOptions(g.opts)
}

type guardSnapshot struct {
	Version    int                          `json:"version"`
	Options    Options                      `json:"options"`
	Containers map[string]containerSnapshot `json:"containers"`
}

type containerSnapshot struct {
	Attempts        []Attempt `json:"attempts"`
	Crashes         int       `json:"crashes"`
	BreakerOpen     bool      `json:"breaker_open"`
	BreakerReported bool      `json:"breaker_reported"`
	HasRestart      bool      `json:"has_restart"`
	NextRestartAt   int64     `json:"next_restart_at"`
	LastCrashAt     int64     `json:"last_crash_at"`
	LastAttemptAt   int64     `json:"last_attempt_at"`
	Seq             int       `json:"seq"`
}

func (g *RestartPolicyGuard) containerLocked(container string) *containerState {
	state := g.containers[container]
	if state == nil {
		state = &containerState{}
		g.containers[container] = state
	}
	return state
}

func (g *RestartPolicyGuard) scheduleRetryLocked(container string, state *containerState, status ExitStatus, at int64) (Decision, error) {
	state.crashes++
	state.lastCrashAt = at

	delay := jitteredDelay(g.opts, container, state.crashes)
	resumeAt := at + delay
	state.hasRestart = true
	state.nextRestartAt = resumeAt

	g.appendAttemptLocked(state, Attempt{
		At:       at,
		Code:     status.Code,
		Signal:   status.Signal,
		Outcome:  OutcomeRetry,
		ResumeAt: resumeAt,
	})

	if g.opts.MaxCrashes > 0 && state.crashes >= g.opts.MaxCrashes {
		state.breakerOpen = true
		state.breakerReported = false
	}

	return Decision{
		Restart:  true,
		DelayMs:  delay,
		ResumeAt: resumeAt,
		Outcome:  OutcomeRetry,
	}, nil
}

func (g *RestartPolicyGuard) appendAttemptLocked(state *containerState, attempt Attempt) {
	state.seq++
	attempt.Seq = state.seq
	if attempt.At < state.lastAttemptAt {
		attempt.At = state.lastAttemptAt
	}
	state.lastAttemptAt = attempt.At
	state.attempts = append(state.attempts, attempt)

	limit := effectiveMaxAttempts(g.opts)
	if len(state.attempts) <= limit {
		return
	}
	kept := make([]Attempt, limit)
	copy(kept, state.attempts[len(state.attempts)-limit:])
	state.attempts = kept
}

func (g *RestartPolicyGuard) applyStableWindowLocked(state *containerState, at int64) {
	if g.opts.StableWindowMs <= 0 || state.crashes == 0 || state.lastCrashAt <= 0 {
		return
	}
	if at >= state.lastCrashAt && at-state.lastCrashAt >= g.opts.StableWindowMs {
		g.clearCrashStreakLocked(state)
	}
}

func (g *RestartPolicyGuard) clearCrashStreakLocked(state *containerState) {
	state.crashes = 0
	state.lastCrashAt = 0
	state.breakerOpen = false
	state.breakerReported = false
}

func (g *RestartPolicyGuard) isConfigExitCode(code int) bool {
	for _, configured := range g.opts.ConfigExitCodes {
		if code == configured {
			return true
		}
	}
	return false
}

func effectiveMaxAttempts(opts Options) int {
	if opts.MaxAttempts > 0 {
		return opts.MaxAttempts
	}
	return defaultMaxAttempts
}

func cloneOptions(opts Options) Options {
	opts.ConfigExitCodes = append([]int(nil), opts.ConfigExitCodes...)
	return opts
}

func backoffFor(opts Options, attempts int) int64 {
	if attempts <= 0 || opts.BaseDelayMs <= 0 {
		return 0
	}

	delay := opts.BaseDelayMs
	if opts.MaxDelayMs > 0 && delay > opts.MaxDelayMs {
		delay = opts.MaxDelayMs
	}
	for i := 1; i < attempts; i++ {
		if opts.MaxDelayMs > 0 && delay >= opts.MaxDelayMs {
			return opts.MaxDelayMs
		}
		if delay > math.MaxInt64/2 {
			delay = math.MaxInt64
		} else {
			delay *= 2
		}
		if opts.MaxDelayMs > 0 && delay > opts.MaxDelayMs {
			return opts.MaxDelayMs
		}
	}
	return delay
}

func jitteredDelay(opts Options, container string, attempts int) int64 {
	base := backoffFor(opts, attempts)
	if base <= 0 {
		return 0
	}

	pct := opts.JitterPct
	if pct < 0 {
		pct = -pct
	}
	if pct > 100 {
		pct = 100
	}
	span := base/100*int64(pct) + base%100*int64(pct)/100
	if span <= 0 {
		return base
	}

	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(container))
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(attempts))
	_, _ = hasher.Write(count[:])
	width := uint64(span)*2 + 1
	delta := int64(hasher.Sum64()%width) - span

	if delta > 0 && base > math.MaxInt64-delta {
		return math.MaxInt64
	}
	if delta < 0 && base < -delta {
		return 0
	}
	return base + delta
}

func isNormalStopSignal(signal string) bool {
	name := strings.ToUpper(strings.TrimSpace(signal))
	name = strings.TrimPrefix(name, "SIG")
	switch name {
	case "TERM", "INT", "QUIT", "15", "2", "3":
		return true
	default:
		return false
	}
}
