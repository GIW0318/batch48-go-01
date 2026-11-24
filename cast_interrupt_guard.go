package task

import (
	"encoding/json"
	"errors"
	"sync"
)

// Sentinels reported by the cast interrupt guard.
var (
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
	startedAt int64
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
	if req.Spell == "" || req.CastMs < 0 {
		return Cast{}, ErrInvalidRequest
	}
	if req.Channel && (req.Ticks <= 0 || req.TickMs <= 0) {
		return Cast{}, ErrInvalidRequest
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active != nil {
		return Cast{}, ErrCastInProgress
	}
	if at < g.silencedUntil {
		return Cast{}, ErrSilenced
	}
	castMs := req.CastMs
	if g.opts.MaxCastMs > 0 && castMs > g.opts.MaxCastMs {
		castMs = g.opts.MaxCastMs
	}
	endsAt := at + castMs
	cast := Cast{
		Player:    req.Player,
		Spell:     req.Spell,
		StartedAt: at,
		EndsAt:    endsAt,
		Channel:   req.Channel,
		Ticks:     req.Ticks,
		TickMs:    req.TickMs,
	}
	if castMs == 0 {
		g.last = CastResult{
			Player:    req.Player,
			Spell:     req.Spell,
			StartedAt: at,
			EndedAt:   at,
			Completed: true,
		}
		g.haveLast = true
		return cast, nil
	}
	g.active = &castState{req: req, startedAt: at, endsAt: endsAt}
	return cast, nil
}

// settleTicksLocked emits every channel tick due at the given millisecond and
// commits the tick bookkeeping in the same critical section. The caller must
// hold g.mu.
func (g *CastInterruptGuard) settleTicksLocked(at int64) []Tick {
	st := g.active
	if st == nil || !st.req.Channel {
		return nil
	}
	var out []Tick
	for st.nextTick < st.req.Ticks {
		due := st.startedAt + st.tickShift + int64(st.nextTick+1)*st.req.TickMs
		if due > at || due > st.endsAt {
			break
		}
		out = append(out, Tick{Spell: st.req.Spell, Index: st.nextTick, At: due})
		st.nextTick++
		st.ticksDone++
	}
	return out
}

// Advance settles every channel tick that is due at the given millisecond.
func (g *CastInterruptGuard) Advance(at int64) ([]Tick, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.settleTicksLocked(at), nil
}

// Interrupt cancels the running cast with a reason.
func (g *CastInterruptGuard) Interrupt(reason string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.active
	if st == nil {
		return ErrNoActiveCast
	}
	// Ticks landing on the interrupt millisecond settle before the cancel.
	g.settleTicksLocked(at)
	g.last = CastResult{
		Player:    st.req.Player,
		Spell:     st.req.Spell,
		StartedAt: st.startedAt,
		EndedAt:   at,
		Completed: false,
		Ticks:     st.ticksDone,
		Reason:    reason,
	}
	g.haveLast = true
	g.active = nil
	return nil
}

// Finish completes the running cast at the given millisecond.
func (g *CastInterruptGuard) Finish(at int64) (CastResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.active
	if st == nil {
		if g.haveLast {
			return g.last, nil
		}
		return CastResult{}, ErrNoActiveCast
	}
	if at < st.endsAt {
		return CastResult{}, ErrCastIncomplete
	}
	g.settleTicksLocked(at)
	res := CastResult{
		Player:    st.req.Player,
		Spell:     st.req.Spell,
		StartedAt: st.startedAt,
		EndedAt:   st.endsAt,
		Completed: true,
		Ticks:     st.ticksDone,
	}
	g.active = nil
	g.last = res
	g.haveLast = true
	return res, nil
}

// PushBack shifts the completion time of the running cast.
func (g *CastInterruptGuard) PushBack(ms int64, at int64) error {
	if ms <= 0 {
		return ErrInvalidPush
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.active
	if st == nil {
		return ErrNoActiveCast
	}
	st.endsAt += ms
	st.tickShift += ms
	if g.opts.MaxCastMs > 0 {
		if cap := st.startedAt + g.opts.MaxCastMs; st.endsAt > cap {
			st.tickShift -= st.endsAt - cap
			st.endsAt = cap
		}
	}
	return nil
}

// Silence blocks new casts until the given millisecond.
func (g *CastInterruptGuard) Silence(until int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if until > g.silencedUntil {
		g.silencedUntil = until
	}
	return nil
}

// Active returns the cast that is currently running.
func (g *CastInterruptGuard) Active() (Cast, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.active
	if st == nil {
		return Cast{}, false
	}
	return Cast{
		Player:    st.req.Player,
		Spell:     st.req.Spell,
		StartedAt: st.startedAt,
		EndsAt:    st.endsAt,
		Channel:   st.req.Channel,
		Ticks:     st.req.Ticks,
		TickMs:    st.req.TickMs,
	}, true
}

// LastResult returns the most recent finished or interrupted cast.
func (g *CastInterruptGuard) LastResult() (CastResult, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last, g.haveLast
}

// SilenceRemaining reports the milliseconds left on the silence window.
func (g *CastInterruptGuard) SilenceRemaining(at int64) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if at >= g.silencedUntil {
		return 0
	}
	return g.silencedUntil - at
}

// castSnapshot is the serialisable form of castState.
type castSnapshot struct {
	Req       CastRequest `json:"req"`
	StartedAt int64       `json:"startedAt"`
	EndsAt    int64       `json:"endsAt"`
	TickShift int64       `json:"tickShift"`
	NextTick  int         `json:"nextTick"`
	TicksDone int         `json:"ticksDone"`
}

// guardSnapshot is the serialisable form of the whole guard.
type guardSnapshot struct {
	Active        *castSnapshot `json:"active"`
	Last          CastResult    `json:"last"`
	HaveLast      bool          `json:"haveLast"`
	SilencedUntil int64         `json:"silencedUntil"`
}

// Snapshot serialises the guard state.
func (g *CastInterruptGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := guardSnapshot{
		Last:          g.last,
		HaveLast:      g.haveLast,
		SilencedUntil: g.silencedUntil,
	}
	if st := g.active; st != nil {
		snap.Active = &castSnapshot{
			Req:       st.req,
			StartedAt: st.startedAt,
			EndsAt:    st.endsAt,
			TickShift: st.tickShift,
			NextTick:  st.nextTick,
			TicksDone: st.ticksDone,
		}
	}
	return json.Marshal(snap)
}

// Restore replaces the guard state from a snapshot payload.
func (g *CastInterruptGuard) Restore(payload []byte) error {
	var snap guardSnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return ErrBadSnapshot
	}
	var active *castState
	if snap.Active != nil {
		cs := snap.Active
		req := cs.Req
		if req.Spell == "" || req.CastMs < 0 || cs.EndsAt < cs.StartedAt {
			return ErrBadSnapshot
		}
		if req.Channel {
			if req.Ticks <= 0 || req.TickMs <= 0 {
				return ErrBadSnapshot
			}
			if cs.TicksDone < 0 || cs.TicksDone > req.Ticks || cs.NextTick != cs.TicksDone {
				return ErrBadSnapshot
			}
		}
		active = &castState{
			req:       req,
			startedAt: cs.StartedAt,
			endsAt:    cs.EndsAt,
			tickShift: cs.TickShift,
			nextTick:  cs.NextTick,
			ticksDone: cs.TicksDone,
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active = active
	g.last = snap.Last
	g.haveLast = snap.HaveLast
	g.silencedUntil = snap.SilencedUntil
	return nil
}
