package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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
	opts Options
}

// NewDungeonMechanismFSM builds an empty state machine.
func NewDungeonMechanismFSM(opts Options) *DungeonMechanismFSM {
	return &DungeonMechanismFSM{opts: opts}
}

// Load validates and installs a dungeon definition.
func (f *DungeonMechanismFSM) Load(spec DungeonDef) error {
	return errNotImplemented
}

// Activate switches a mechanism on and returns the resulting effects.
func (f *DungeonMechanismFSM) Activate(mech, by string, at int64) ([]Effect, error) {
	return nil, errNotImplemented
}

// Deactivate switches a mechanism off and returns the resulting effects.
func (f *DungeonMechanismFSM) Deactivate(mech, by string, at int64) ([]Effect, error) {
	return nil, errNotImplemented
}

// State returns the current state of one mechanism.
func (f *DungeonMechanismFSM) State(mech string) string {
	return ""
}

// Doors returns a copy of the unlock flags of every door.
func (f *DungeonMechanismFSM) Doors() map[string]bool {
	return nil
}

// DoorUnlocked reports whether one door is unlocked.
func (f *DungeonMechanismFSM) DoorUnlocked(door string) bool {
	return false
}

// Trigger records an event and returns effects for doors it unlocks.
func (f *DungeonMechanismFSM) Trigger(event Event, at int64) ([]Effect, error) {
	return nil, errNotImplemented
}

// EventCount returns how often an event kind has fired.
func (f *DungeonMechanismFSM) EventCount(kind string) int {
	return 0
}

// Mechanisms returns every mechanism name in ascending order.
func (f *DungeonMechanismFSM) Mechanisms() []string {
	return nil
}

// Reset returns one room to its initial state.
func (f *DungeonMechanismFSM) Reset(room string) error {
	return errNotImplemented
}

// Snapshot serializes the definition and the runtime state.
func (f *DungeonMechanismFSM) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the state machine with a previously taken snapshot.
func (f *DungeonMechanismFSM) Restore(data []byte) error {
	return errNotImplemented
}
