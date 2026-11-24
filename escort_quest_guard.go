package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

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

const (
	escortStateActive    = "active"
	escortStateFailed    = "failed"
	escortStateCompleted = "completed"

	snapshotVersion = 1

	exitReasonReassigned = "reassigned"
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

// ExitRecord records a player leaving an escort, including automatic
// reassignment to another escort.
type ExitRecord struct {
	EscortID string `json:"escort_id"`
	Player   string `json:"player"`
	At       int64  `json:"at"`
	Reason   string `json:"reason"`
}

type memberState struct {
	Name           string `json:"name"`
	Away           bool   `json:"away"`
	AwaySince      int64  `json:"away_since"`
	AwayUsedMillis int64  `json:"away_used_millis"`
}

type escortState struct {
	ID             string        `json:"id"`
	QuestID        string        `json:"quest_id"`
	State          string        `json:"state"`
	StageIndex     int           `json:"stage_index"`
	StageProgress  int           `json:"stage_progress"`
	StageCount     int           `json:"stage_count"`
	Required       int           `json:"required"`
	StartedAt      int64         `json:"started_at"`
	LastAt         int64         `json:"last_at"`
	ElapsedMillis  int64         `json:"elapsed_millis"`
	FailReason     string        `json:"fail_reason,omitempty"`
	Combat         bool          `json:"combat,omitempty"`
	Members        []memberState `json:"members"`
	Exits          []ExitRecord  `json:"exits,omitempty"`
	RewardedStages []int         `json:"rewarded_stages,omitempty"`
}

type snapshotData struct {
	Version      int               `json:"version"`
	Options      Options           `json:"options"`
	Seq          uint64            `json:"seq"`
	Escorts      []*escortState    `json:"escorts"`
	PlayerEscort map[string]string `json:"player_escort"`
}

type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Data     json.RawMessage `json:"data"`
}

// EscortQuestGuard drives escort quest instances.
type EscortQuestGuard struct {
	mu           sync.Mutex
	opts         Options
	escorts      map[string]*escortState
	playerEscort map[string]string
	seq          uint64
}

// NewEscortQuestGuard builds a guard with the supplied quest definitions.
func NewEscortQuestGuard(opts Options) *EscortQuestGuard {
	opts.Quests = append([]QuestDef(nil), opts.Quests...)
	return &EscortQuestGuard{
		opts:         opts,
		escorts:      make(map[string]*escortState),
		playerEscort: make(map[string]string),
	}
}

func (g *EscortQuestGuard) initLocked() {
	if g.escorts == nil {
		g.escorts = make(map[string]*escortState)
	}
	if g.playerEscort == nil {
		g.playerEscort = make(map[string]string)
	}
}

func (g *EscortQuestGuard) questDefLocked(questID string) (QuestDef, bool) {
	for _, quest := range g.opts.Quests {
		if quest.ID == questID {
			return quest, true
		}
	}
	return QuestDef{}, false
}

func normalizeNames(values []string) []string {
	names := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func (g *EscortQuestGuard) transferPlayerLocked(player, newEscortID string, at int64) {
	oldEscortID := g.playerEscort[player]
	if oldEscortID == "" || oldEscortID == newEscortID {
		return
	}

	if oldEscort := g.escorts[oldEscortID]; oldEscort != nil {
		oldEscort.Exits = append(oldEscort.Exits, ExitRecord{
			EscortID: oldEscortID,
			Player:   player,
			At:       at,
			Reason:   exitReasonReassigned,
		})
		oldEscort.removeMemberLocked(player)
	}
	g.playerEscort[player] = newEscortID
}

func (e *escortState) removeMemberLocked(player string) bool {
	for i, member := range e.Members {
		if member.Name != player {
			continue
		}
		e.Members = append(e.Members[:i], e.Members[i+1:]...)
		return true
	}
	return false
}

func (e *escortState) memberIndexLocked(player string) int {
	for i := range e.Members {
		if e.Members[i].Name == player {
			return i
		}
	}
	return -1
}

func (e *escortState) statusLocked() Escort {
	members := make([]string, 0, len(e.Members))
	for _, member := range e.Members {
		members = append(members, member.Name)
	}

	return Escort{
		ID:            e.ID,
		QuestID:       e.QuestID,
		State:         e.State,
		StageIndex:    e.StageIndex,
		StageProgress: e.StageProgress,
		Members:       members,
		ElapsedMillis: e.ElapsedMillis,
		FailReason:    e.FailReason,
	}
}

func (e *escortState) stageLocked() Stage {
	return Stage{
		EscortID:  e.ID,
		Index:     e.StageIndex,
		Progress:  e.StageProgress,
		Required:  e.Required,
		Completed: e.State == escortStateCompleted,
	}
}

func (e *escortState) touchLocked(at int64) {
	if e.State != escortStateActive {
		return
	}
	if at >= e.LastAt {
		elapsed := at - e.StartedAt
		if elapsed < 0 {
			elapsed = 0
		}
		e.ElapsedMillis = elapsed
	}
	if at > e.LastAt {
		e.LastAt = at
	}
}

func (e *escortState) failLocked(reason string, at int64) {
	if e.State != escortStateActive {
		return
	}
	elapsed := at - e.StartedAt
	if elapsed < 0 {
		elapsed = 0
	}
	e.ElapsedMillis = elapsed
	e.LastAt = at
	e.State = escortStateFailed
	e.FailReason = reason
}

func (e *escortState) rewardStageLocked(stage int) {
	for _, rewarded := range e.RewardedStages {
		if rewarded == stage {
			return
		}
	}
	e.RewardedStages = append(e.RewardedStages, stage)
}

// Start begins a new escort of questID with a comma separated party list.
func (g *EscortQuestGuard) Start(questID, party string, at int64) (Escort, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	quest, ok := g.questDefLocked(questID)
	if !ok {
		return Escort{}, ErrUnknownQuest
	}

	names := normalizeNames(strings.Split(party, ","))
	if len(names) == 0 {
		return Escort{}, ErrNoParty
	}

	stageCount := quest.Stages
	if stageCount < 1 {
		stageCount = 1
	}
	required := 0
	if quest.Stages > 0 {
		required = quest.RouteLength / quest.Stages
	} else {
		required = quest.RouteLength
	}
	if required < 0 {
		required = 0
	}

	g.seq++
	id := "escort-" + strconv.FormatUint(g.seq, 10)
	escort := &escortState{
		ID:            id,
		QuestID:       questID,
		State:         escortStateActive,
		StageCount:    stageCount,
		Required:      required,
		StartedAt:     at,
		LastAt:        at,
		ElapsedMillis: 0,
		Members:       make([]memberState, 0, len(names)),
	}
	g.escorts[id] = escort

	for _, name := range names {
		g.transferPlayerLocked(name, id, at)
		escort.Members = append(escort.Members, memberState{Name: name})
		g.playerEscort[name] = id
	}

	return escort.statusLocked(), nil
}

// Advance moves the escorted npc forward by dist at the given time.
func (g *EscortQuestGuard) Advance(escortID string, dist int, at int64) (Stage, error) {
	if dist < 0 {
		return Stage{}, ErrInvalidDistance
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return Stage{}, ErrNoSuchEscort
	}
	if escort.State != escortStateActive {
		return Stage{}, ErrEscortFinished
	}

	escort.touchLocked(at)

	moved := dist
	if escort.Combat {
		moved = dist * g.opts.CombatSpeedPercent / 100
		if moved < 0 {
			moved = 0
		}
	}
	escort.StageProgress += moved

	if escort.Required > 0 && escort.StageProgress < escort.Required {
		return escort.stageLocked(), nil
	}

	escort.StageProgress = escort.Required
	escort.rewardStageLocked(escort.StageIndex)

	if escort.StageIndex >= escort.StageCount-1 {
		escort.State = escortStateCompleted
		escort.touchLocked(at)
		return escort.stageLocked(), nil
	}

	escort.StageIndex++
	escort.StageProgress = 0
	return escort.stageLocked(), nil
}

// Join adds a player to the party of an active escort. An existing member who
// is currently away uses Join as the atomic rejoin operation.
func (g *EscortQuestGuard) Join(escortID, player string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return ErrNoSuchEscort
	}

	player = strings.TrimSpace(player)
	if player == "" {
		return ErrNotInEscort
	}

	if index := escort.memberIndexLocked(player); index >= 0 {
		member := &escort.Members[index]
		if !member.Away {
			return ErrAlreadyJoined
		}

		if at >= member.AwaySince {
			member.AwayUsedMillis += at - member.AwaySince
		}
		member.Away = false
		member.AwaySince = 0
		escort.touchLocked(at)
		return nil
	}

	g.transferPlayerLocked(player, escortID, at)
	escort.Members = append(escort.Members, memberState{Name: player})
	g.playerEscort[player] = escortID
	escort.touchLocked(at)
	return nil
}

// Leave marks a party member as straggling.
func (g *EscortQuestGuard) Leave(escortID, player string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return ErrNoSuchEscort
	}

	player = strings.TrimSpace(player)
	index := escort.memberIndexLocked(player)
	if index < 0 {
		return ErrNotInEscort
	}

	escort.touchLocked(at)
	member := &escort.Members[index]
	if !member.Away {
		member.Away = true
		member.AwaySince = at
	}
	return nil
}

// Fail ends an escort with the given reason.
func (g *EscortQuestGuard) Fail(escortID, reason string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return ErrNoSuchEscort
	}
	if escort.State != escortStateActive {
		return ErrEscortFinished
	}

	escort.failLocked(reason, at)
	return nil
}

// Retry restarts a failed escort at the stage where it failed.
func (g *EscortQuestGuard) Retry(escortID string, at int64) (Escort, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return Escort{}, ErrNoSuchEscort
	}
	if escort.State != escortStateFailed {
		return Escort{}, ErrNotFailed
	}

	elapsed := escort.ElapsedMillis
	failedStage := escort.StageIndex

	escort.State = escortStateActive
	escort.FailReason = ""
	escort.StageIndex = failedStage
	escort.StageProgress = 0
	escort.StartedAt = at - elapsed
	escort.LastAt = at
	escort.ElapsedMillis = elapsed

	rewarded := make([]int, 0, len(escort.RewardedStages))
	for _, stage := range escort.RewardedStages {
		if stage < failedStage {
			rewarded = append(rewarded, stage)
		}
	}
	escort.RewardedStages = rewarded

	for i := range escort.Members {
		escort.Members[i].Away = false
		escort.Members[i].AwaySince = 0
		escort.Members[i].AwayUsedMillis = 0
	}

	return escort.statusLocked(), nil
}

// Tick evaluates the straggler grace deadlines at the given time.
func (g *EscortQuestGuard) Tick(escortID string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return ErrNoSuchEscort
	}
	if escort.State != escortStateActive {
		return nil
	}

	escort.touchLocked(at)

	for i := range escort.Members {
		member := &escort.Members[i]
		used := member.AwayUsedMillis
		if member.Away && at >= member.AwaySince {
			used += at - member.AwaySince
		}
		if used > g.opts.GraceMillis {
			escort.failLocked("grace-expired", at)
			return nil
		}
	}

	return nil
}

// SetCombat switches the slow combat advance mode on or off.
func (g *EscortQuestGuard) SetCombat(escortID string, on bool, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escort, ok := g.escorts[escortID]
	if !ok {
		return ErrNoSuchEscort
	}

	escort.Combat = on
	escort.touchLocked(at)
	if at > escort.LastAt {
		escort.LastAt = at
	}
	return nil
}

// Members returns the party members in join order.
func (g *EscortQuestGuard) Members(escortID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return nil
	}
	return escort.statusLocked().Members
}

// Away returns the members currently outside the escort radius.
func (g *EscortQuestGuard) Away(escortID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return nil
	}

	away := make([]string, 0)
	for _, member := range escort.Members {
		if member.Away {
			away = append(away, member.Name)
		}
	}
	return away
}

// Stage returns the progress of the current stage.
func (g *EscortQuestGuard) Stage(escortID string) (Stage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return Stage{}, ErrNoSuchEscort
	}
	return escort.stageLocked(), nil
}

// Elapsed returns the recorded travel time of an escort in milliseconds.
func (g *EscortQuestGuard) Elapsed(escortID string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return 0
	}
	return escort.ElapsedMillis
}

// Status returns a copy of the full escort state.
func (g *EscortQuestGuard) Status(escortID string) (Escort, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return Escort{}, ErrNoSuchEscort
	}
	return escort.statusLocked(), nil
}

// ExitRecords returns automatic and historical exit records for an escort.
func (g *EscortQuestGuard) ExitRecords(escortID string) []ExitRecord {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return nil
	}
	return append([]ExitRecord(nil), escort.Exits...)
}

// RewardedStages returns a copy of the stage settlement ids.
func (g *EscortQuestGuard) RewardedStages(escortID string) []int {
	g.mu.Lock()
	defer g.mu.Unlock()

	escort, ok := g.escorts[escortID]
	if !ok {
		return nil
	}
	return append([]int(nil), escort.RewardedStages...)
}

// Snapshot serializes all quest definitions and running escorts.
func (g *EscortQuestGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()

	escorts := make([]*escortState, 0, len(g.escorts))
	for _, escort := range g.escorts {
		escorts = append(escorts, escort)
	}
	sort.Slice(escorts, func(i, j int) bool {
		return escorts[i].ID < escorts[j].ID
	})

	playerEscort := make(map[string]string, len(g.playerEscort))
	for player, id := range g.playerEscort {
		playerEscort[player] = id
	}

	payload, err := json.Marshal(snapshotData{
		Version:      snapshotVersion,
		Options:      g.opts,
		Seq:          g.seq,
		Escorts:      escorts,
		PlayerEscort: playerEscort,
	})
	if err != nil {
		return nil, err
	}

	checksum := sha256.Sum256(payload)
	envelope, err := json.Marshal(snapshotEnvelope{
		Version:  snapshotVersion,
		Checksum: hex.EncodeToString(checksum[:]),
		Data:     payload,
	})
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

// Restore replaces the guard state with a previously taken snapshot.
func (g *EscortQuestGuard) Restore(data []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("%w: invalid snapshot envelope: %v", ErrBadSnapshot, err)
	}
	if envelope.Version != snapshotVersion || len(envelope.Data) == 0 {
		return fmt.Errorf("%w: unsupported snapshot version", ErrBadSnapshot)
	}

	checksum := sha256.Sum256(envelope.Data)
	if hex.EncodeToString(checksum[:]) != envelope.Checksum {
		return fmt.Errorf("%w: checksum mismatch", ErrBadSnapshot)
	}

	var snapshot snapshotData
	if err := json.Unmarshal(envelope.Data, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid snapshot payload: %v", ErrBadSnapshot, err)
	}
	if snapshot.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported payload version", ErrBadSnapshot)
	}

	escorts := make(map[string]*escortState, len(snapshot.Escorts))
	playerEscort := make(map[string]string)
	questIDs := make(map[string]bool, len(snapshot.Options.Quests))
	for _, quest := range snapshot.Options.Quests {
		questIDs[quest.ID] = true
	}

	var maxSequence uint64
	for _, escort := range snapshot.Escorts {
		if escort == nil || escort.ID == "" || escort.QuestID == "" {
			return fmt.Errorf("%w: escort is missing identity", ErrBadSnapshot)
		}
		if _, exists := escorts[escort.ID]; exists {
			return fmt.Errorf("%w: duplicate escort %q", ErrBadSnapshot, escort.ID)
		}
		if !questIDs[escort.QuestID] {
			return fmt.Errorf("%w: missing quest %q", ErrBadSnapshot, escort.QuestID)
		}
		if escort.State != escortStateActive &&
			escort.State != escortStateFailed &&
			escort.State != escortStateCompleted {
			return fmt.Errorf("%w: invalid escort state %q", ErrBadSnapshot, escort.State)
		}
		if escort.StageCount < 1 || escort.StageIndex < 0 || escort.StageIndex >= escort.StageCount {
			return fmt.Errorf("%w: invalid stage index", ErrBadSnapshot)
		}
		if escort.Required < 0 || escort.StageProgress < 0 || escort.StageProgress > escort.Required {
			return fmt.Errorf("%w: invalid stage progress", ErrBadSnapshot)
		}
		if escort.ElapsedMillis < 0 {
			return fmt.Errorf("%w: negative recorded duration", ErrBadSnapshot)
		}

		if id, ok := strings.CutPrefix(escort.ID, "escort-"); ok {
			if sequence, err := strconv.ParseUint(id, 10, 64); err == nil && sequence > maxSequence {
				maxSequence = sequence
			}
		}

		for i := range escort.Members {
			member := &escort.Members[i]
			if member.Name == "" {
				return fmt.Errorf("%w: empty member name", ErrBadSnapshot)
			}
			if member.AwayUsedMillis < 0 {
				return fmt.Errorf("%w: negative away duration", ErrBadSnapshot)
			}
			if !member.Away {
				member.AwaySince = 0
			}
			if other, exists := playerEscort[member.Name]; exists {
				return fmt.Errorf("%w: player %q in %q and %q", ErrBadSnapshot, member.Name, other, escort.ID)
			}
			playerEscort[member.Name] = escort.ID
		}

		escorts[escort.ID] = escort
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.opts = snapshot.Options
	g.escorts = escorts
	g.playerEscort = playerEscort
	if snapshot.Seq > maxSequence {
		g.seq = snapshot.Seq
	} else {
		g.seq = maxSequence
	}
	return nil
}
