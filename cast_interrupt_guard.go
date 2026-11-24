package task

import (
	"errors"
	"sync"
)

// Sentinels reported by the cast interrupt guard.
var (
	errNotImplemented = errors.New("not implemented")

	ErrInvalidRequest = errors.New("invalid cast request")
	ErrCastInProgress = errors.New("cast already in progress")
	ErrNoActiveCast   = errors.New("no active cast")
	ErrCastIncomplete = errors.New("cast has not finished")
	ErrSilenced       = errors.New("caster is silenced")
	ErrInvalidPush    = errors.New("invalid push back")
	ErrBadSnapshot    = errors.New("snapshot payload rejected")
)

// Options carries the hard limits of the casting subsystem.
type Options struct {
	MaxCastMs    int64
	MaxTickCount int
}

// CastRequest is one submitted cast.
type CastRequest struct {
	Player  string
	Spell   string
	CastMs  int64
	Channel bool
	Ticks   int
	TickMs  int64
}

// Cast is the accepted cast as seen by the caller.
type Cast struct {
	Player    string
	Spell     string
	StartedAt int64
	EndsAt    int64
	Channel   bool
	Ticks     int
	TickMs    int64
}

// Tick is one channel tick emitted by Advance.
type Tick struct {
	Spell string
	Index int
	At    int64
}

// CastResult is the final settlement of one cast.
type CastResult struct {
	Player    string
	Spell     string
	StartedAt int64
	EndedAt   int64
	Completed bool
	Ticks     int
	Reason    string
}

type castState struct {
	req       CastRequest
	endsAt    int64
	tickShift int64
	nextTick  int
	ticksDone int
}

// CastInterruptGuard settles one character's casting from start to finish.
type CastInterruptGuard struct {
	mu            sync.Mutex
	opts          Options
	active        *castState
	last          CastResult
	haveLast      bool
	silencedUntil int64
}

// NewCastInterruptGuard builds a guard with the given limits.
func NewCastInterruptGuard(opts Options) *CastInterruptGuard {
	return &CastInterruptGuard{opts: opts}
}

// Begin starts a cast at the caller supplied millisecond.
func (g *CastInterruptGuard) Begin(req CastRequest, at int64) (Cast, error) {
	return Cast{}, errNotImplemented
}

// Advance settles every channel tick that is due at the given millisecond.
func (g *CastInterruptGuard) Advance(at int64) ([]Tick, error) {
	return nil, errNotImplemented
}

// Interrupt cancels the running cast with a reason.
func (g *CastInterruptGuard) Interrupt(reason string, at int64) error {
	return errNotImplemented
}

// Finish completes the running cast at the given millisecond.
func (g *CastInterruptGuard) Finish(at int64) (CastResult, error) {
	return CastResult{}, errNotImplemented
}

// PushBack shifts the completion time of the running cast.
func (g *CastInterruptGuard) PushBack(ms int64, at int64) error {
	return errNotImplemented
}

// Silence blocks new casts until the given millisecond.
func (g *CastInterruptGuard) Silence(until int64) error {
	return errNotImplemented
}

// Active returns the cast that is currently running.
func (g *CastInterruptGuard) Active() (Cast, bool) {
	return Cast{}, false
}

// LastResult returns the most recent finished or interrupted cast.
func (g *CastInterruptGuard) LastResult() (CastResult, bool) {
	return CastResult{}, false
}

// SilenceRemaining reports the milliseconds left on the silence window.
func (g *CastInterruptGuard) SilenceRemaining(at int64) int64 {
	return 0
}

// Snapshot serialises the guard state.
func (g *CastInterruptGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot payload.
func (g *CastInterruptGuard) Restore(payload []byte) error {
	return errNotImplemented
}
