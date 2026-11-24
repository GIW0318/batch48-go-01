package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

var (
	// ErrUnknownQuest is returned when the quest id was never defined.
	ErrUnknownQuest = errors.New("escort: unknown quest")
	// ErrNoParty is returned when a party list is empty.
	ErrNoParty = errors.New("escort: empty party")
	// ErrNoSuchEscort is returned for an escort id that does not exist.
	ErrNoSuchEscort = errors.New("escort: unknown escort")
	// ErrEscortFinished is returned when an escort is no longer active.
	ErrEscortFinished = errors.New("escort: escort already finished")
	// ErrAlreadyJoined is returned when a player joins twice.
	ErrAlreadyJoined = errors.New("escort: player already in party")
	// ErrNotInEscort is returned when a player that is not a member tries to leave.
	ErrNotInEscort = errors.New("escort: player not in party")
	// ErrNotFailed is returned when a retry is requested for a running escort.
	ErrNotFailed = errors.New("escort: escort is not failed")
	// ErrInvalidDistance is returned for a negative advance distance.
	ErrInvalidDistance = errors.New("escort: invalid distance")
	// ErrBadSnapshot is returned when snapshot bytes cannot be decoded.
	ErrBadSnapshot = errors.New("escort: bad snapshot")
)

// QuestDef describes one escort route definition.
type QuestDef struct {
	ID          string
	RouteLength int
	Stages      int
}

// Options configures a EscortQuestGuard.
type Options struct {
	Quests             []QuestDef
	EscortRadius       int
	GraceMillis        int64
	CombatSpeedPercent int
}

// Escort is the observable state of one running escort instance.
type Escort struct {
	ID            string
	QuestID       string
	State         string
	StageIndex    int
	StageProgress int
	Members       []string
	ElapsedMillis int64
	FailReason    string
}

// Stage is the progress of the stage the escort is currently walking.
type Stage struct {
	EscortID  string
	Index     int
	Progress  int
	Required  int
	Completed bool
}

// EscortQuestGuard drives escort quest instances.
type EscortQuestGuard struct {
	opts Options
}

// NewEscortQuestGuard builds a guard with the supplied quest definitions.
func NewEscortQuestGuard(opts Options) *EscortQuestGuard {
	return &EscortQuestGuard{opts: opts}
}

// Start begins a new escort of questID with a comma separated party list.
func (g *EscortQuestGuard) Start(questID, party string, at int64) (Escort, error) {
	return Escort{}, errNotImplemented
}

// Advance moves the escorted npc forward by dist at the given time.
func (g *EscortQuestGuard) Advance(escortID string, dist int, at int64) (Stage, error) {
	return Stage{}, errNotImplemented
}

// Join adds a player to the party of an active escort.
func (g *EscortQuestGuard) Join(escortID, player string, at int64) error {
	return errNotImplemented
}

// Leave marks a party member as straggling.
func (g *EscortQuestGuard) Leave(escortID, player string, at int64) error {
	return errNotImplemented
}

// Fail ends an escort with the given reason.
func (g *EscortQuestGuard) Fail(escortID, reason string, at int64) error {
	return errNotImplemented
}

// Retry restarts a failed escort at the stage where it failed.
func (g *EscortQuestGuard) Retry(escortID string, at int64) (Escort, error) {
	return Escort{}, errNotImplemented
}

// Tick evaluates the straggler grace deadlines at the given time.
func (g *EscortQuestGuard) Tick(escortID string, at int64) error {
	return errNotImplemented
}

// SetCombat switches the slow combat advance mode on or off.
func (g *EscortQuestGuard) SetCombat(escortID string, on bool, at int64) error {
	return errNotImplemented
}

// Members returns the party members in join order.
func (g *EscortQuestGuard) Members(escortID string) []string {
	return nil
}

// Away returns the members currently outside the escort radius.
func (g *EscortQuestGuard) Away(escortID string) []string {
	return nil
}

// Stage returns the progress of the current stage.
func (g *EscortQuestGuard) Stage(escortID string) (Stage, error) {
	return Stage{}, errNotImplemented
}

// Elapsed returns the recorded travel time of an escort in milliseconds.
func (g *EscortQuestGuard) Elapsed(escortID string) int64 {
	return 0
}

// Status returns a copy of the full escort state.
func (g *EscortQuestGuard) Status(escortID string) (Escort, error) {
	return Escort{}, errNotImplemented
}

// Snapshot serializes all quest definitions and running escorts.
func (g *EscortQuestGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state with a previously taken snapshot.
func (g *EscortQuestGuard) Restore(data []byte) error {
	return errNotImplemented
}
