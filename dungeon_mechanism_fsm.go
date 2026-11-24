package task

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

var (
	// ErrNotLoaded is returned before a dungeon definition was loaded.
	ErrNotLoaded = errors.New("dungeon: no dungeon definition loaded")
	// ErrDuplicateMechanism is returned when two mechanisms share a name.
	ErrDuplicateMechanism = errors.New("dungeon: duplicate mechanism")
	// ErrUnknownRoom is returned for a room that is not part of the definition.
	ErrUnknownRoom = errors.New("dungeon: unknown room")
	// ErrUnknownMechanism is returned for a mechanism that is not part of the definition.
	ErrUnknownMechanism = errors.New("dungeon: unknown mechanism")
	// ErrUnknownDoor is returned for a door that is not part of the definition.
	ErrUnknownDoor = errors.New("dungeon: unknown door")
	// ErrBadMode is returned for an unsupported door mode.
	ErrBadMode = errors.New("dungeon: unsupported door mode")
	// ErrNotToggleable is returned when a one-shot mechanism is deactivated.
	ErrNotToggleable = errors.New("dungeon: mechanism cannot be deactivated")
	// ErrBadSnapshot is returned when snapshot bytes cannot be decoded.
	ErrBadSnapshot = errors.New("dungeon: bad snapshot")
)

// Effect is one observable consequence of a mechanism change.
type Effect struct {
	Kind   string
	Target string
	Room   string
	At     int64
}

// Mechanism states.
const (
	stateIdle   = "idle"
	stateActive = "active"
	stateUsed   = "used"
)

// Door modes.
const (
	modeAll   = "all"
	modeAny   = "any"
	modeCount = "count"
)

// MechDef describes one mechanism placed in a room.
type MechDef struct {
	Name      string
	Room      string
	Kind      string
	Latched   bool
	Momentary bool
	Once      bool
	Permanent bool
	Reward    string
}

// DoorDef describes the unlock requirements of one door.
type DoorDef struct {
	Name     string
	Room     string
	Requires []string
	Mode     string
	Count    int
	Ordered  bool
}

// DungeonDef is the full definition of a dungeon run.
type DungeonDef struct {
	Rooms      []string
	Mechanisms []MechDef
	Doors      []DoorDef
}

// Event is an arbitrary dungeon event that can feed counters.
type Event struct {
	Kind  string
	Room  string
	By    string
	Count int
	At    int64
}

// Options configures a DungeonMechanismFSM.
type Options struct {
	DefaultRoom string
}

// DungeonMechanismFSM drives mechanisms and doors of one dungeon.
type DungeonMechanismFSM struct {
	opts   Options
	mu     sync.Mutex
	loaded bool
	def    DungeonDef
	mechs  map[string]MechDef
	states map[string]string
	doors  map[string]bool
	events map[string]int
}

// NewDungeonMechanismFSM builds an empty state machine.
func NewDungeonMechanismFSM(opts Options) *DungeonMechanismFSM {
	return &DungeonMechanismFSM{opts: opts}
}

// Load validates and installs a dungeon definition.
func (f *DungeonMechanismFSM) Load(spec DungeonDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := validateDef(spec); err != nil {
		return err
	}
	f.installLocked(spec, nil, nil, nil)
	return nil
}

// validateDef checks a dungeon definition for consistency.
func validateDef(spec DungeonDef) error {
	seen := make(map[string]bool, len(spec.Mechanisms))
	for _, m := range spec.Mechanisms {
		if seen[m.Name] {
			return ErrDuplicateMechanism
		}
		seen[m.Name] = true
	}
	for _, d := range spec.Doors {
		switch d.Mode {
		case modeAll, modeAny:
			for _, req := range d.Requires {
				if !seen[req] {
					return ErrUnknownMechanism
				}
			}
		case modeCount:
			// count requirements may also name event kinds.
		default:
			return ErrBadMode
		}
	}
	return nil
}

// installLocked replaces the definition and runtime state. Nil maps are
// rebuilt into their initial values. The caller must hold f.mu.
func (f *DungeonMechanismFSM) installLocked(def DungeonDef, states map[string]string, doors map[string]bool, events map[string]int) {
	f.def = def
	f.mechs = make(map[string]MechDef, len(def.Mechanisms))
	f.states = make(map[string]string, len(def.Mechanisms))
	for _, m := range def.Mechanisms {
		f.mechs[m.Name] = m
		if s, ok := states[m.Name]; ok {
			f.states[m.Name] = s
		} else {
			f.states[m.Name] = stateIdle
		}
	}
	f.doors = make(map[string]bool, len(def.Doors))
	for _, d := range def.Doors {
		f.doors[d.Name] = doors[d.Name]
	}
	f.events = make(map[string]int, len(events))
	for kind, n := range events {
		f.events[kind] = n
	}
	f.loaded = true
}

// Activate switches a mechanism on and returns the resulting effects.
func (f *DungeonMechanismFSM) Activate(mech, by string, at int64) ([]Effect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return nil, ErrNotLoaded
	}
	m, ok := f.mechs[mech]
	if !ok {
		return nil, ErrUnknownMechanism
	}
	if m.Once {
		if f.states[mech] == stateUsed {
			return nil, nil
		}
		f.states[mech] = stateUsed
		effects := []Effect{{Kind: "reward", Target: mech, Room: m.Room, At: at}}
		return append(effects, f.checkDoorsLocked(at)...), nil
	}
	if f.states[mech] != stateIdle {
		return nil, nil
	}
	f.states[mech] = stateActive
	effects := []Effect{{Kind: "activate", Target: mech, Room: m.Room, At: at}}
	return append(effects, f.checkDoorsLocked(at)...), nil
}

// Deactivate switches a mechanism off and returns the resulting effects.
func (f *DungeonMechanismFSM) Deactivate(mech, by string, at int64) ([]Effect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return nil, ErrNotLoaded
	}
	m, ok := f.mechs[mech]
	if !ok {
		return nil, ErrUnknownMechanism
	}
	if m.Once {
		return nil, ErrNotToggleable
	}
	if f.states[mech] != stateActive {
		return nil, nil
	}
	f.states[mech] = stateIdle
	return []Effect{{Kind: "deactivate", Target: mech, Room: m.Room, At: at}}, nil
}

// State returns the current state of one mechanism.
func (f *DungeonMechanismFSM) State(mech string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return ""
	}
	return f.states[mech]
}

// Doors returns a copy of the unlock flags of every door.
func (f *DungeonMechanismFSM) Doors() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return nil
	}
	out := make(map[string]bool, len(f.doors))
	for name, unlocked := range f.doors {
		out[name] = unlocked
	}
	return out
}

// DoorUnlocked reports whether one door is unlocked.
func (f *DungeonMechanismFSM) DoorUnlocked(door string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return false
	}
	return f.doors[door]
}

// Trigger records an event and returns effects for doors it unlocks.
func (f *DungeonMechanismFSM) Trigger(event Event, at int64) ([]Effect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return nil, ErrNotLoaded
	}
	n := event.Count
	if n < 1 {
		n = 1
	}
	f.events[event.Kind] += n
	var effects []Effect
	if event.Kind == "leave" {
		for _, m := range f.def.Mechanisms {
			if !m.Momentary || m.Room != event.Room {
				continue
			}
			if f.states[m.Name] != stateActive {
				continue
			}
			f.states[m.Name] = stateIdle
			effects = append(effects, Effect{Kind: "deactivate", Target: m.Name, Room: m.Room, At: at})
		}
	}
	return append(effects, f.checkDoorsLocked(at)...), nil
}

// EventCount returns how often an event kind has fired.
func (f *DungeonMechanismFSM) EventCount(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return 0
	}
	return f.events[kind]
}

// Mechanisms returns every mechanism name in ascending order.
func (f *DungeonMechanismFSM) Mechanisms() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return nil
	}
	names := make([]string, 0, len(f.def.Mechanisms))
	for _, m := range f.def.Mechanisms {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names
}

// Reset returns one room to its initial state.
func (f *DungeonMechanismFSM) Reset(room string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return ErrNotLoaded
	}
	known := false
	for _, r := range f.def.Rooms {
		if r == room {
			known = true
			break
		}
	}
	if !known {
		return ErrUnknownRoom
	}
	for _, m := range f.def.Mechanisms {
		if m.Room != room {
			continue
		}
		if m.Once && m.Permanent && f.states[m.Name] == stateUsed {
			continue
		}
		f.states[m.Name] = stateIdle
	}
	for _, d := range f.def.Doors {
		if d.Room == room {
			f.doors[d.Name] = false
		}
	}
	return nil
}

// snapshotData is the serialized form of the whole state machine.
type snapshotData struct {
	Def    DungeonDef
	States map[string]string
	Doors  map[string]bool
	Events map[string]int
}

// Snapshot serializes the definition and the runtime state.
func (f *DungeonMechanismFSM) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return nil, ErrNotLoaded
	}
	states := make(map[string]string, len(f.states))
	for name, s := range f.states {
		states[name] = s
	}
	doors := make(map[string]bool, len(f.doors))
	for name, unlocked := range f.doors {
		doors[name] = unlocked
	}
	events := make(map[string]int, len(f.events))
	for kind, n := range f.events {
		events[kind] = n
	}
	return json.Marshal(snapshotData{Def: f.def, States: states, Doors: doors, Events: events})
}

// Restore replaces the state machine with a previously taken snapshot.
func (f *DungeonMechanismFSM) Restore(data []byte) error {
	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	if err := validateDef(snap.Def); err != nil {
		return ErrBadSnapshot
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installLocked(snap.Def, snap.States, snap.Doors, snap.Events)
	return nil
}

// checkDoorsLocked unlocks every newly satisfied door in definition order
// and returns the matching unlock effects. The caller must hold f.mu.
func (f *DungeonMechanismFSM) checkDoorsLocked(at int64) []Effect {
	var effects []Effect
	for _, d := range f.def.Doors {
		if f.doors[d.Name] {
			continue
		}
		if !f.doorSatisfiedLocked(d) {
			continue
		}
		f.doors[d.Name] = true
		effects = append(effects, Effect{Kind: "unlock", Target: d.Name, Room: d.Room, At: at})
	}
	return effects
}

// doorSatisfiedLocked reports whether a door's unlock condition holds.
// The caller must hold f.mu.
func (f *DungeonMechanismFSM) doorSatisfiedLocked(d DoorDef) bool {
	switch d.Mode {
	case modeAll:
		for _, req := range d.Requires {
			if f.states[req] != stateActive {
				return false
			}
		}
		return true
	case modeAny:
		for _, req := range d.Requires {
			if f.states[req] == stateActive {
				return true
			}
		}
		return false
	case modeCount:
		total := 0
		for _, req := range d.Requires {
			if f.states[req] == stateActive {
				total++
			}
			total += f.events[req]
		}
		return total >= d.Count
	}
	return false
}
