package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

var (
	// ErrAlreadyJoined is returned when a player joins the duel twice.
	ErrAlreadyJoined = errors.New("duel: player already joined")
	// ErrDuelFull is returned when a third player tries to join.
	ErrDuelFull = errors.New("duel: duel is full")
	// ErrAlreadyStarted is returned when the duel was already begun.
	ErrAlreadyStarted = errors.New("duel: duel already started")
	// ErrNotEnoughPlayers is returned when fewer than two players joined.
	ErrNotEnoughPlayers = errors.New("duel: two players required")
	// ErrNotStarted is returned when acting before the duel begins.
	ErrNotStarted = errors.New("duel: duel not started")
	// ErrOutOfTurn is returned when a player acts outside its own turn.
	ErrOutOfTurn = errors.New("duel: not this players turn")
	// ErrDuelOver is returned when acting after the duel ended.
	ErrDuelOver = errors.New("duel: duel is over")
	// ErrOnCooldown is returned when a skill is still cooling down.
	ErrOnCooldown = errors.New("duel: action on cooldown")
	// ErrNotEnoughActions is returned when the action costs more than the remaining points.
	ErrNotEnoughActions = errors.New("duel: not enough action points")
	// ErrUnknownPlayer is returned for a player that never joined.
	ErrUnknownPlayer = errors.New("duel: unknown player")
	// ErrBadSnapshot is returned when snapshot bytes cannot be decoded.
	ErrBadSnapshot = errors.New("duel: bad snapshot")
)

// Action is one submitted or automatic duel action.
type Action struct {
	Kind     string
	Target   string
	Power    int
	Cost     int
	Cooldown int
}

// Turn is the public view of the current turn.
type Turn struct {
	Round       int
	Player      string
	ActionsLeft int
}

// Result is the final settlement of a duel.
type Result struct {
	Winner string
	Loser  string
	Reason string
	Rounds int
}

// Options configures a DuelRoundGuard.
type Options struct {
	MaxHP       int
	ActionPoints int
	AutoAction  Action
	BleedDamage int
}

// DuelRoundGuard drives one turn based duel.
type DuelRoundGuard struct {
	opts Options
}

// NewDuelRoundGuard builds a duel guard with the supplied rules.
func NewDuelRoundGuard(opts Options) *DuelRoundGuard {
	return &DuelRoundGuard{opts: opts}
}

// Join adds a player with its initiative value.
func (g *DuelRoundGuard) Join(player string, init int) error {
	return errNotImplemented
}

// Begin starts the duel and returns the first turn.
func (g *DuelRoundGuard) Begin(at int64) (Turn, error) {
	return Turn{}, errNotImplemented
}

// Act applies one action of the player whose turn it is.
func (g *DuelRoundGuard) Act(player string, action Action) (Turn, error) {
	return Turn{}, errNotImplemented
}

// Timeout applies the configured automatic action for the current player.
func (g *DuelRoundGuard) Timeout(at int64) (Action, error) {
	return Action{}, errNotImplemented
}

// Surrender ends the duel in favour of the opponent.
func (g *DuelRoundGuard) Surrender(player string) error {
	return errNotImplemented
}

// Disconnect ends the duel because one player dropped out.
func (g *DuelRoundGuard) Disconnect(player string) error {
	return errNotImplemented
}

// Result returns the settlement once the duel is over.
func (g *DuelRoundGuard) Result() (Result, bool) {
	return Result{}, false
}

// TurnOrder returns the players in initiative order.
func (g *DuelRoundGuard) TurnOrder() []string {
	return nil
}

// ActionPoints returns the remaining action points of one player.
func (g *DuelRoundGuard) ActionPoints(player string) int {
	return 0
}

// HP returns the current hit points of one player.
func (g *DuelRoundGuard) HP(player string) int {
	return 0
}

// Round returns the current round number.
func (g *DuelRoundGuard) Round() int {
	return 0
}

// Snapshot serializes the full duel state.
func (g *DuelRoundGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state with a previously taken snapshot.
func (g *DuelRoundGuard) Restore(data []byte) error {
	return errNotImplemented
}
