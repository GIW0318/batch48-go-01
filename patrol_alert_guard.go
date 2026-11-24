package task

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// errNotImplemented is kept for compatibility with the original skeleton.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the patrol guard.
var (
	ErrInvalidNPC   = errors.New("npc name rejected")
	ErrEmptyRoute   = errors.New("patrol route is empty")
	ErrDuplicateNPC = errors.New("npc already assigned")
	ErrUnknownNPC   = errors.New("npc has no patrol state")
	ErrInvalidEvent = errors.New("sense event rejected")
	ErrNotInCombat  = errors.New("npc is not engaged")
	ErrBadSnapshot  = errors.New("snapshot payload rejected")
)

const (
	alertPatrol    = 0
	alertSuspicion = 1
	alertSearch    = 2
	alertCombat    = 3

	snapshotVersion = 1
)

// Point is one position on the patrol map.
type Point struct{ X, Y int }

// SenseEvent is one perception report delivered for an npc.
type SenseEvent struct {
	Kind     string
	Target   string
	Distance int
}

// Alert is the alert state of one npc.
type Alert struct {
	NPC    string
	Group  string
	Level  int
	Since  int64
	Target string
}

// Move is the result of one patrol step.
type Move struct {
	NPC       string
	From      Point
	To        Point
	Waypoint  int
	Holding   bool
	Returning bool
}

// Options carries the tuning of one patrol build.
type Options struct {
	Speed       int
	SightRange  int
	SoundRange  int
	HoldTicks   int64
	CombatTicks int64
	DecayTicks  []int64
	GroupOf     map[string]string
}

type npcState struct {
	route    []Point
	position Point
	waypoint int
	holding  bool
	holdTill int64
	level    int
	since    int64
	target   string
	group    string
	helpers  []string
}

// PatrolAlertGuard drives patrol routes and the shared alert table.
type PatrolAlertGuard struct {
	mu    sync.Mutex
	opts  Options
	npcs  map[string]*npcState
	order []string
}

// NewPatrolAlertGuard builds a guard for the given tuning.
func NewPatrolAlertGuard(opts Options) *PatrolAlertGuard {
	opts.DecayTicks = append([]int64(nil), opts.DecayTicks...)
	if opts.GroupOf != nil {
		groups := make(map[string]string, len(opts.GroupOf))
		for npc, group := range opts.GroupOf {
			groups[npc] = group
		}
		opts.GroupOf = groups
	}
	return &PatrolAlertGuard{opts: opts, npcs: make(map[string]*npcState)}
}

// Assign gives one npc a patrol route and resets its alert state.
func (g *PatrolAlertGuard) Assign(npc string, route []Point) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if npc == "" {
		return ErrInvalidNPC
	}
	if _, exists := g.npcs[npc]; exists {
		return ErrDuplicateNPC
	}
	if len(route) == 0 {
		return ErrEmptyRoute
	}

	stateRoute := append([]Point(nil), route...)
	waypoint := 0
	if len(stateRoute) > 1 {
		waypoint = 1
	}
	g.npcs[npc] = &npcState{
		route:    stateRoute,
		position: stateRoute[0],
		waypoint: waypoint,
		group:    g.opts.GroupOf[npc],
	}
	g.order = append(g.order, npc)
	return nil
}

// Sense applies one perception event and returns the resulting alert.
func (g *PatrolAlertGuard) Sense(npc string, ev SenseEvent, at int64) (Alert, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.npcs[npc]
	if !ok {
		return Alert{}, ErrUnknownNPC
	}
	if ev.Distance < 0 {
		return Alert{}, ErrInvalidEvent
	}

	switch ev.Kind {
	case "sight":
		if ev.Distance > g.opts.SightRange {
			return g.alertLocked(npc), nil
		}
		if st.level < alertCombat {
			st.level++
		}
		st.target = ev.Target
		st.since = at
		if st.level >= alertCombat {
			st.level = alertCombat
			g.raiseGroupLocked(npc, at)
		}
	case "sound":
		if ev.Distance > g.opts.SoundRange {
			return g.alertLocked(npc), nil
		}
		if st.level < alertSearch {
			st.level++
			st.target = ev.Target
			st.since = at
		}
	default:
		return Alert{}, ErrInvalidEvent
	}
	return g.alertLocked(npc), nil
}

// Step advances one npc along its route.
func (g *PatrolAlertGuard) Step(npc string, at int64) (Move, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.npcs[npc]
	if !ok {
		return Move{}, ErrUnknownNPC
	}

	mv := Move{
		NPC:      npc,
		From:     st.position,
		To:       st.position,
		Waypoint: st.waypoint,
	}
	if st.level >= alertCombat {
		mv.Holding = true
		return mv, nil
	}

	if st.holding {
		if at < st.holdTill {
			mv.Holding = true
			return mv, nil
		}
		st.holding = false
		st.waypoint = (st.waypoint + 1) % len(st.route)
		mv.Waypoint = st.waypoint
	}

	if st.waypoint < 0 || st.waypoint >= len(st.route) {
		st.waypoint = 0
		mv.Waypoint = 0
	}
	target := st.route[st.waypoint]
	st.position = moveToward(st.position, target, g.opts.Speed)
	mv.To = st.position
	mv.Waypoint = st.waypoint

	if st.position == target {
		st.holding = true
		st.holdTill = at + g.opts.HoldTicks
		mv.Holding = true
	}
	return mv, nil
}

func moveToward(from, to Point, speed int) Point {
	if speed <= 0 {
		return from
	}
	remaining := speed
	if from.X != to.X {
		dx := to.X - from.X
		step := dx
		if step < 0 {
			step = -step
		}
		if step > remaining {
			step = remaining
		}
		if dx < 0 {
			from.X -= step
		} else {
			from.X += step
		}
		remaining -= step
	}
	if remaining > 0 && from.Y != to.Y {
		dy := to.Y - from.Y
		step := dy
		if step < 0 {
			step = -step
		}
		if step > remaining {
			step = remaining
		}
		if dy < 0 {
			from.Y -= step
		} else {
			from.Y += step
		}
	}
	return from
}

// CallHelp returns the group peers that answered the combat call of one npc.
func (g *PatrolAlertGuard) CallHelp(npc string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.npcs[npc]
	if !ok {
		return nil, ErrUnknownNPC
	}
	if st.level < alertCombat {
		return nil, ErrNotInCombat
	}
	return g.raiseGroupLocked(npc, st.since), nil
}

// raiseGroupLocked atomically merges a help call into the shared alert table.
// Group broadcasts only raise levels, never lower them.
func (g *PatrolAlertGuard) raiseGroupLocked(npc string, at int64) []string {
	st := g.npcs[npc]
	if st.group == "" {
		st.helpers = nil
		return []string{}
	}

	helpers := make([]string, 0)
	for _, peerName := range g.order {
		if peerName == npc {
			continue
		}
		peer := g.npcs[peerName]
		if peer.group != st.group {
			continue
		}
		helpers = append(helpers, peerName)
		if peer.level < alertSearch {
			peer.level = alertSearch
			peer.since = at
			peer.target = st.target
		}
	}
	sort.Strings(helpers)
	st.helpers = append([]string(nil), helpers...)
	return helpers
}

// Decay lowers the alert level according to the elapsed time.
func (g *PatrolAlertGuard) Decay(npc string, at int64) Alert {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.npcs[npc]
	if !ok {
		return Alert{}
	}
	if st.level > alertPatrol {
		window := g.decayWindow(st.level)
		if window < 0 {
			window = 0
		}
		if at >= st.since && at-st.since >= window {
			st.since += window
			st.level--
			if st.level == alertPatrol {
				st.since = 0
				st.target = ""
				st.helpers = nil
			}
		}
	}
	return g.alertLocked(npc)
}

func (g *PatrolAlertGuard) decayWindow(level int) int64 {
	if level >= alertCombat && g.opts.CombatTicks > 0 {
		return g.opts.CombatTicks
	}
	idx := level - 1
	if idx < 0 || idx >= len(g.opts.DecayTicks) {
		return 0
	}
	return g.opts.DecayTicks[idx]
}

// AlertOf reports the current alert of one npc.
func (g *PatrolAlertGuard) AlertOf(npc string) Alert {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.alertLocked(npc)
}

func (g *PatrolAlertGuard) alertLocked(npc string) Alert {
	st, ok := g.npcs[npc]
	if !ok {
		return Alert{}
	}
	return Alert{
		NPC:    npc,
		Group:  st.group,
		Level:  st.level,
		Since:  st.since,
		Target: st.target,
	}
}

// Route returns the assigned route of one npc.
func (g *PatrolAlertGuard) Route(npc string) []Point {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.npcs[npc]
	if !ok {
		return nil
	}
	return append([]Point(nil), st.route...)
}

// Position reports the current position of one npc.
func (g *PatrolAlertGuard) Position(npc string) (Point, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.npcs[npc]
	if !ok {
		return Point{}, false
	}
	return st.position, true
}

type snapshotNPC struct {
	NPC      string   `json:"npc"`
	Route    []Point  `json:"route"`
	Position Point    `json:"position"`
	Waypoint int      `json:"waypoint"`
	Holding  bool     `json:"holding"`
	HoldTill int64    `json:"hold_till"`
	Level    int      `json:"level"`
	Since    int64    `json:"since"`
	Target   string   `json:"target,omitempty"`
	Group    string   `json:"group,omitempty"`
	Helpers  []string `json:"helpers,omitempty"`
}

type snapshotData struct {
	Version int           `json:"version"`
	NPCs    []snapshotNPC `json:"npcs"`
}

// Snapshot serialises every npc state and the shared alert table.
func (g *PatrolAlertGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	data := snapshotData{
		Version: snapshotVersion,
		NPCs:    make([]snapshotNPC, 0, len(g.order)),
	}
	for _, npc := range g.order {
		st, ok := g.npcs[npc]
		if !ok {
			continue
		}
		data.NPCs = append(data.NPCs, snapshotNPC{
			NPC:      npc,
			Route:    append([]Point(nil), st.route...),
			Position: st.position,
			Waypoint: st.waypoint,
			Holding:  st.holding,
			HoldTill: st.holdTill,
			Level:    st.level,
			Since:    st.since,
			Target:   st.target,
			Group:    st.group,
			Helpers:  append([]string(nil), st.helpers...),
		})
	}
	return json.Marshal(data)
}

// Restore replaces the guard state from a snapshot.
func (g *PatrolAlertGuard) Restore(data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if snap.NPCs == nil {
		return ErrBadSnapshot
	}

	npcs := make(map[string]*npcState, len(snap.NPCs))
	order := make([]string, 0, len(snap.NPCs))
	for _, saved := range snap.NPCs {
		if saved.NPC == "" || len(saved.Route) == 0 {
			return ErrBadSnapshot
		}
		if _, exists := npcs[saved.NPC]; exists {
			return ErrBadSnapshot
		}
		if saved.Waypoint < 0 || saved.Waypoint >= len(saved.Route) {
			return ErrBadSnapshot
		}
		if saved.Level < alertPatrol || saved.Level > alertCombat {
			return ErrBadSnapshot
		}

		route := append([]Point(nil), saved.Route...)
		group := saved.Group
		if group == "" {
			group = g.opts.GroupOf[saved.NPC]
		}
		npcs[saved.NPC] = &npcState{
			route:    route,
			position: saved.Position,
			waypoint: saved.Waypoint,
			holding:  saved.Holding,
			holdTill: saved.HoldTill,
			level:    saved.Level,
			since:    saved.Since,
			target:   saved.Target,
			group:    group,
			helpers:  append([]string(nil), saved.Helpers...),
		}
		order = append(order, saved.NPC)
	}

	g.npcs = npcs
	g.order = order
	return nil
}
