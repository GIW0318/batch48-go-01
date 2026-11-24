// Package task renders entity snapshots on a playback timeline: snapshots are
// kept sorted by server tick, the playback position is derived from the
// configured delay, and missing data is covered by bounded extrapolation.
package task

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

// SnapshotBufferCap is the hard limit on snapshots kept in the timeline.
const SnapshotBufferCap = 32

// DefaultTeleportThreshold is the distance above which a position jump is not
// interpolated.
const DefaultTeleportThreshold = 64.0

// Errors reported by the interpolation clock.
var (
	ErrInvalidTick     = errors.New("snapshot tick must be greater than zero")
	ErrDuplicateTick   = errors.New("snapshot tick is already buffered")
	ErrInvalidEntity   = errors.New("entity id must be greater than zero and unique inside a snapshot")
	ErrInvalidTickRate = errors.New("tick rate must be positive")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// EntityState is the wire state of one entity inside a snapshot.
type EntityState struct {
	EntityID uint32  `json:"entity_id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

// Snapshot is one server tick worth of entity states.
type Snapshot struct {
	Tick     uint64        `json:"tick"`
	Entities []EntityState `json:"entities"`
}

// State is the rendered state for a playback position.
type State struct {
	Tick         uint64        `json:"tick"`
	Entities     []EntityState `json:"entities"`
	Extrapolated bool          `json:"extrapolated"`
	Stale        bool          `json:"stale"`
}

// InterpolationClock keeps the snapshot timeline and the playback delay.
type InterpolationClock struct {
	mu           sync.Mutex
	tickRate     int
	delay        time.Duration
	extrapolate  time.Duration
	teleportDist float64
	snapshots    []Snapshot
	dropped      int
	stale        bool
}

// NewInterpolationClock builds a clock for the given tick rate and initial
// playback delay.
func NewInterpolationClock(tickRate int, initialDelay time.Duration) *InterpolationClock {
	if tickRate <= 0 {
		tickRate = 1
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	return &InterpolationClock{
		tickRate:     tickRate,
		delay:        initialDelay,
		teleportDist: DefaultTeleportThreshold,
	}
}

// Push inserts one snapshot into the timeline.
func (c *InterpolationClock) Push(s Snapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if s.Tick == 0 {
		return ErrInvalidTick
	}

	seen := make(map[uint32]struct{}, len(s.Entities))
	for _, e := range s.Entities {
		if e.EntityID == 0 {
			return ErrInvalidEntity
		}
		if _, dup := seen[e.EntityID]; dup {
			return ErrInvalidEntity
		}
		seen[e.EntityID] = struct{}{}
	}

	idx := sort.Search(len(c.snapshots), func(i int) bool {
		return c.snapshots[i].Tick >= s.Tick
	})
	if idx < len(c.snapshots) && c.snapshots[idx].Tick == s.Tick {
		c.dropped++
		return ErrDuplicateTick
	}

	entities := append([]EntityState(nil), s.Entities...)
	s.Entities = entities
	c.snapshots = append(c.snapshots, Snapshot{})
	copy(c.snapshots[idx+1:], c.snapshots[idx:])
	c.snapshots[idx] = s

	if len(c.snapshots) > SnapshotBufferCap {
		c.snapshots = append([]Snapshot(nil), c.snapshots[1:]...)
		c.dropped++
	}
	return nil
}

// Sample renders the state for the given client tick.
func (c *InterpolationClock) Sample(tick uint64) (State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stale = false

	if len(c.snapshots) == 0 {
		return State{}, false
	}

	delayTicks := c.delayTicksLocked()
	var playTick uint64
	if delayTicks >= tick {
		playTick = 0
	} else {
		playTick = tick - delayTicks
	}

	if playTick < c.snapshots[0].Tick {
		return State{}, false
	}

	newest := c.snapshots[len(c.snapshots)-1]

	idx := sort.Search(len(c.snapshots), func(i int) bool {
		return c.snapshots[i].Tick >= playTick
	})
	if idx < len(c.snapshots) && c.snapshots[idx].Tick == playTick {
		return State{
			Tick:     c.snapshots[idx].Tick,
			Entities: sortedEntityCopy(c.snapshots[idx].Entities),
		}, true
	}

	if playTick < newest.Tick {
		older := c.snapshots[idx-1]
		newer := c.snapshots[idx]
		span := float64(newer.Tick - older.Tick)
		frac := float64(playTick-older.Tick) / span

		olderByID := make(map[uint32]EntityState, len(older.Entities))
		for _, e := range older.Entities {
			olderByID[e.EntityID] = e
		}

		entities := make([]EntityState, 0, len(newer.Entities))
		for _, ne := range newer.Entities {
			oe, ok := olderByID[ne.EntityID]
			if !ok {
				entities = append(entities, ne)
				continue
			}
			dx := ne.X - oe.X
			dy := ne.Y - oe.Y
			perTick := math.Sqrt(dx*dx+dy*dy) / span
			if perTick > c.teleportDist {
				entities = append(entities, ne)
				continue
			}
			entities = append(entities, EntityState{
				EntityID: ne.EntityID,
				X:        oe.X + dx*frac,
				Y:        oe.Y + dy*frac,
			})
		}
		sortEntities(entities)
		return State{Tick: newer.Tick, Entities: entities}, true
	}

	overshootTicks := playTick - newest.Tick
	clamped := overshootTicks
	limitTicks := c.extrapolateTicksLocked()
	if clamped > limitTicks {
		clamped = limitTicks
	}
	stale := false
	if c.extrapolate <= 0 {
		// With no budget configured the playback head cannot leave the
		// buffered timeline: freeze immediately and flag the data as stale.
		stale = true
	} else if overshootTicks > 3*limitTicks {
		// The frozen state is only entered once the play head has run well
		// past the configured budget, so short bursts of jitter inside a
		// few budget windows still render smooth extrapolated motion.
		stale = true
	}

	entities := make([]EntityState, 0, len(newest.Entities))
	var previous Snapshot
	hasPrevious := len(c.snapshots) >= 2
	spanTicks := float64(1)
	if hasPrevious {
		previous = c.snapshots[len(c.snapshots)-2]
		spanTicks = float64(newest.Tick - previous.Tick)
	}
	prevByID := make(map[uint32]EntityState)
	if hasPrevious {
		for _, e := range previous.Entities {
			prevByID[e.EntityID] = e
		}
	}
	for _, ne := range newest.Entities {
		pe, ok := prevByID[ne.EntityID]
		if !ok {
			entities = append(entities, ne)
			continue
		}
		vx := (ne.X - pe.X) / spanTicks
		vy := (ne.Y - pe.Y) / spanTicks
		entities = append(entities, EntityState{
			EntityID: ne.EntityID,
			X:        ne.X + vx*float64(clamped),
			Y:        ne.Y + vy*float64(clamped),
		})
	}
	sortEntities(entities)

	c.stale = stale
	return State{
		Tick:         newest.Tick,
		Entities:     entities,
		Extrapolated: true,
		Stale:        stale,
	}, true
}

// SetDelay sets the playback delay used by Sample.
func (c *InterpolationClock) SetDelay(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if d < 0 {
		d = 0
	}
	if span := c.tickSpanLocked(); span > 0 && d < span {
		d = span
	}
	c.delay = d
}

// Delay reports the playback delay currently in use.
func (c *InterpolationClock) Delay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.delay
}

// SetExtrapolationLimit sets how far Sample may run past the newest snapshot.
func (c *InterpolationClock) SetExtrapolationLimit(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d < 0 {
		d = 0
	}
	c.extrapolate = d
}

// ExtrapolationLimit reports the configured extrapolation limit.
func (c *InterpolationClock) ExtrapolationLimit() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.extrapolate
}

// SetTeleportThreshold sets the distance above which a jump is not
// interpolated.
func (c *InterpolationClock) SetTeleportThreshold(v float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.teleportDist = v
}

// TeleportThreshold reports the configured teleport threshold.
func (c *InterpolationClock) TeleportThreshold() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.teleportDist
}

// Queued reports how many snapshots are buffered.
func (c *InterpolationClock) Queued() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.snapshots)
}

// Dropped reports how many snapshots were discarded as duplicates or evicted.
func (c *InterpolationClock) Dropped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}

// Ticks lists the buffered snapshot ticks in ascending order.
func (c *InterpolationClock) Ticks() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticks := make([]uint64, len(c.snapshots))
	for i, s := range c.snapshots {
		ticks[i] = s.Tick
	}
	return ticks
}

// TickSpan reports the time span between the oldest and the newest snapshot.
func (c *InterpolationClock) TickSpan() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tickSpanLocked()
}

// Stale reports whether the last Sample ran past the extrapolation limit.
func (c *InterpolationClock) Stale() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stale
}

// Snapshot serialises the timeline and the delay state.
func (c *InterpolationClock) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshots := make([]Snapshot, len(c.snapshots))
	for i, s := range c.snapshots {
		snapshots[i] = Snapshot{
			Tick:     s.Tick,
			Entities: append([]EntityState(nil), s.Entities...),
		}
	}
	payload := clockPayload{
		Magic:        clockPayloadMagic,
		TickRate:     c.tickRate,
		Snapshots:    snapshots,
		Delay:        c.delay,
		Extrapolate:  c.extrapolate,
		TeleportDist: c.teleportDist,
		Dropped:      c.dropped,
		Stale:        c.stale,
	}
	data, err := json.Marshal(&payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces the whole clock state with a payload.
func (c *InterpolationClock) Restore(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var payload clockPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ErrBadSnapshot
	}
	if payload.Magic != clockPayloadMagic {
		return ErrBadSnapshot
	}
	if payload.TickRate <= 0 {
		return ErrBadSnapshot
	}
	if len(payload.Snapshots) > SnapshotBufferCap {
		return ErrBadSnapshot
	}

	snapshots := make([]Snapshot, 0, len(payload.Snapshots))
	prevTick := uint64(0)
	for _, s := range payload.Snapshots {
		if s.Tick == 0 {
			return ErrBadSnapshot
		}
		if s.Tick <= prevTick {
			return ErrBadSnapshot
		}
		seen := make(map[uint32]struct{}, len(s.Entities))
		entities := make([]EntityState, len(s.Entities))
		for i, e := range s.Entities {
			if e.EntityID == 0 {
				return ErrBadSnapshot
			}
			if _, dup := seen[e.EntityID]; dup {
				return ErrBadSnapshot
			}
			seen[e.EntityID] = struct{}{}
			entities[i] = e
		}
		prevTick = s.Tick
		snapshots = append(snapshots, Snapshot{Tick: s.Tick, Entities: entities})
	}

	delay := payload.Delay
	if delay < 0 {
		delay = 0
	}
	extrapolate := payload.Extrapolate
	if extrapolate < 0 {
		extrapolate = 0
	}

	c.snapshots = snapshots
	c.delay = delay
	c.extrapolate = extrapolate
	c.teleportDist = payload.TeleportDist
	c.dropped = payload.Dropped
	c.stale = payload.Stale
	return nil
}

const clockPayloadMagic = "interpolation-clock-v1"

type clockPayload struct {
	Magic        string        `json:"magic"`
	TickRate     int           `json:"tick_rate"`
	Snapshots    []Snapshot    `json:"snapshots"`
	Delay        time.Duration `json:"delay"`
	Extrapolate  time.Duration `json:"extrapolation_limit"`
	TeleportDist float64       `json:"teleport_threshold"`
	Dropped      int           `json:"dropped"`
	Stale        bool          `json:"stale"`
}

func (c *InterpolationClock) delayTicksLocked() uint64 {
	if c.delay <= 0 || c.tickRate <= 0 {
		return 0
	}
	return uint64(int64(c.delay) * int64(c.tickRate) / int64(time.Second))
}

func (c *InterpolationClock) extrapolateTicksLocked() uint64 {
	if c.extrapolate <= 0 || c.tickRate <= 0 {
		return 0
	}
	return uint64(int64(c.extrapolate) * int64(c.tickRate) / int64(time.Second))
}

func (c *InterpolationClock) tickSpanLocked() time.Duration {
	n := len(c.snapshots)
	if n < 2 {
		return 0
	}
	tickDelta := c.snapshots[n-1].Tick - c.snapshots[0].Tick
	return time.Duration(tickDelta) * time.Second / time.Duration(c.tickRate)
}

func sortedEntityCopy(in []EntityState) []EntityState {
	out := append([]EntityState(nil), in...)
	sortEntities(out)
	return out
}

func sortEntities(entities []EntityState) {
	sort.Slice(entities, func(i, j int) bool {
		return entities[i].EntityID < entities[j].EntityID
	})
}
