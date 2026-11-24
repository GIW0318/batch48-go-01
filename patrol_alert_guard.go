package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
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
	return &PatrolAlertGuard{opts: opts, npcs: make(map[string]*npcState)}
}

// Assign gives one npc a patrol route and resets its alert state.
func (g *PatrolAlertGuard) Assign(npc string, route []Point) error {
	return errNotImplemented
}

// Sense applies one perception event and returns the resulting alert.
func (g *PatrolAlertGuard) Sense(npc string, ev SenseEvent, at int64) (Alert, error) {
	return Alert{}, errNotImplemented
}

// Step advances one npc along its route.
func (g *PatrolAlertGuard) Step(npc string, at int64) (Move, error) {
	return Move{}, errNotImplemented
}

// CallHelp returns the group peers that answered the combat call of one npc.
func (g *PatrolAlertGuard) CallHelp(npc string) ([]string, error) {
	return nil, errNotImplemented
}

// Decay lowers the alert level according to the elapsed time.
func (g *PatrolAlertGuard) Decay(npc string, at int64) Alert {
	return Alert{}
}

// AlertOf reports the current alert of one npc.
func (g *PatrolAlertGuard) AlertOf(npc string) Alert {
	return Alert{}
}

// Route returns the assigned route of one npc.
func (g *PatrolAlertGuard) Route(npc string) []Point {
	return nil
}

// Position reports the current position of one npc.
func (g *PatrolAlertGuard) Position(npc string) (Point, bool) {
	return Point{}, false
}

// Snapshot serialises every npc state and the shared alert table.
func (g *PatrolAlertGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot.
func (g *PatrolAlertGuard) Restore(data []byte) error {
	return errNotImplemented
}
