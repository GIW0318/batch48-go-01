package task

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
)

// Sentinels reported by the authority transfer manager.
var (
	errNotImplemented    = errors.New("not implemented")
	ErrEntityUnknown     = errors.New("prop not registered")
	ErrEntityExists      = errors.New("prop already registered")
	ErrNotOwner          = errors.New("player does not control the prop")
	ErrSelfTransfer      = errors.New("transfer target equals current owner")
	ErrTransferPending   = errors.New("prop already has a pending transfer")
	ErrTransferUnknown   = errors.New("transfer id unknown or already finished")
	ErrNoPendingTransfer = errors.New("prop has no pending transfer")
	ErrStaleInput        = errors.New("input sequence already applied")
	ErrInputBufferFull   = errors.New("transfer input buffer is full")
	ErrCommitSeqInvalid  = errors.New("commit sequence must exceed every buffered sequence")
	ErrBadSnapshot       = errors.New("snapshot payload rejected")
)

const (
	defaultInputBufferLimit = 32
	snapshotVersion         = 1
)

// EntityID identifies a pickup prop.
type EntityID int

// PlayerID identifies a player session.
type PlayerID int

// TransferID identifies one authority transfer request.
type TransferID uint64

// Command is one player action for a prop.
type Command struct {
	Name  string
	Value int
}

// TransferStats is the observable manager counter block.
type TransferStats struct {
	Props            int
	PendingTransfers int
	BufferedInputs   int
	Replayed         uint64
}

type bufferedInput struct {
	seq uint64
	cmd Command
}

type pendingTransfer struct {
	id     TransferID
	from   PlayerID
	to     PlayerID
	buffer []bufferedInput
}

type propState struct {
	owner        PlayerID
	lastSeq      uint64
	rejected     uint64
	formerOwners map[PlayerID]struct{}
	pending      *pendingTransfer
}

// AuthorityTransfer owns the authoritative holder of every pickup prop.
type AuthorityTransfer struct {
	mu             sync.Mutex
	props          map[EntityID]*propState
	transfers      map[TransferID]EntityID
	nextTransferID uint64
	suspicion      map[PlayerID]int
	replayed       uint64
}

// NewAuthorityTransfer builds an empty transfer manager.
func NewAuthorityTransfer() *AuthorityTransfer {
	return &AuthorityTransfer{
		props:     make(map[EntityID]*propState),
		transfers: make(map[TransferID]EntityID),
		suspicion: make(map[PlayerID]int),
	}
}

func (a *AuthorityTransfer) initLocked() {
	if a.props == nil {
		a.props = make(map[EntityID]*propState)
	}
	if a.transfers == nil {
		a.transfers = make(map[TransferID]EntityID)
	}
	if a.suspicion == nil {
		a.suspicion = make(map[PlayerID]int)
	}
}

func satAddUint64(v, delta uint64) uint64 {
	if v > math.MaxUint64-delta {
		return math.MaxUint64
	}
	return v + delta
}

func satAddInt(v, delta int) int {
	if delta > 0 && v > math.MaxInt-delta {
		return math.MaxInt
	}
	return v + delta
}

// RegisterProp creates a prop owned by the given player at the given sequence.
func (a *AuthorityTransfer) RegisterProp(entityID EntityID, owner PlayerID, seq uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if _, ok := a.props[entityID]; ok {
		return ErrEntityExists
	}
	a.props[entityID] = &propState{
		owner:        owner,
		lastSeq:      seq,
		formerOwners: make(map[PlayerID]struct{}),
	}
	return nil
}

// Request asks to move authority for a prop from one player to another.
func (a *AuthorityTransfer) Request(entityID EntityID, from, to PlayerID) (TransferID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	prop, ok := a.props[entityID]
	if !ok {
		return 0, ErrEntityUnknown
	}
	if from != prop.owner {
		return 0, ErrNotOwner
	}
	if from == to {
		return 0, ErrSelfTransfer
	}
	if prop.pending != nil {
		return 0, ErrTransferPending
	}

	a.nextTransferID++
	id := TransferID(a.nextTransferID)
	prop.pending = &pendingTransfer{id: id, from: from, to: to}
	a.transfers[id] = entityID
	return id, nil
}

// Commit finishes the pending transfer at the given commit sequence.
func (a *AuthorityTransfer) Commit(entityID EntityID, atSeq uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	prop, ok := a.props[entityID]
	if !ok {
		return ErrEntityUnknown
	}
	pending := prop.pending
	if pending == nil {
		return ErrNoPendingTransfer
	}
	for _, input := range pending.buffer {
		if atSeq <= input.seq {
			return ErrCommitSeqInvalid
		}
	}

	prop.owner = pending.to
	if atSeq > prop.lastSeq {
		prop.lastSeq = atSeq
	}
	prop.formerOwners[pending.from] = struct{}{}
	a.replayed = satAddUint64(a.replayed, uint64(len(pending.buffer)))
	delete(a.transfers, pending.id)
	prop.pending = nil
	return nil
}

func (a *AuthorityTransfer) rejectNonOwnerLocked(prop *propState, player PlayerID) {
	if _, former := prop.formerOwners[player]; former {
		prop.rejected = satAddUint64(prop.rejected, 1)
		a.replayed = satAddUint64(a.replayed, 1)
		return
	}
	a.suspicion[player] = satAddInt(a.suspicion[player], 1)
}

// Input accepts one player command for a prop.
func (a *AuthorityTransfer) Input(entityID EntityID, owner PlayerID, seq uint64, cmd Command) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	prop, ok := a.props[entityID]
	if !ok {
		return ErrEntityUnknown
	}

	if pending := prop.pending; pending != nil {
		switch owner {
		case pending.from:
			if seq <= prop.lastSeq {
				return ErrStaleInput
			}
			prop.lastSeq = seq
			return nil
		case pending.to:
			buffer := pending.buffer
			index := sort.Search(len(buffer), func(i int) bool {
				return buffer[i].seq >= seq
			})
			if index < len(buffer) && buffer[index].seq == seq {
				return ErrStaleInput
			}
			if len(buffer) >= defaultInputBufferLimit {
				return ErrInputBufferFull
			}
			buffer = append(buffer, bufferedInput{})
			copy(buffer[index+1:], buffer[index:])
			buffer[index] = bufferedInput{seq: seq, cmd: cmd}
			pending.buffer = buffer
			return nil
		default:
			a.rejectNonOwnerLocked(prop, owner)
			return ErrNotOwner
		}
	}

	if owner != prop.owner {
		a.rejectNonOwnerLocked(prop, owner)
		return ErrNotOwner
	}
	if seq <= prop.lastSeq {
		return ErrStaleInput
	}
	prop.lastSeq = seq
	return nil
}

// Cancel drops a pending transfer and keeps the original owner.
func (a *AuthorityTransfer) Cancel(transferID TransferID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	entityID, ok := a.transfers[transferID]
	if !ok {
		return ErrTransferUnknown
	}
	prop, ok := a.props[entityID]
	if !ok || prop.pending == nil || prop.pending.id != transferID {
		delete(a.transfers, transferID)
		return ErrTransferUnknown
	}
	prop.pending = nil
	delete(a.transfers, transferID)
	return nil
}

// Destroy removes a prop from the world.
func (a *AuthorityTransfer) Destroy(entityID EntityID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	prop, ok := a.props[entityID]
	if !ok {
		return ErrEntityUnknown
	}
	if prop.pending != nil {
		delete(a.transfers, prop.pending.id)
	}
	delete(a.props, entityID)
	return nil
}

// Owner reports the current authoritative holder.
func (a *AuthorityTransfer) Owner(entityID EntityID) (PlayerID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	prop, ok := a.props[entityID]
	if !ok {
		return 0, ErrEntityUnknown
	}
	return prop.owner, nil
}

// Pending reports the pending transfer of a prop, if any.
func (a *AuthorityTransfer) Pending(entityID EntityID) (TransferID, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	prop, ok := a.props[entityID]
	if !ok || prop.pending == nil {
		return 0, false
	}
	return prop.pending.id, true
}

// Buffered reports how many inputs wait in the transfer buffer.
func (a *AuthorityTransfer) Buffered(entityID EntityID) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	prop, ok := a.props[entityID]
	if !ok || prop.pending == nil {
		return 0
	}
	return len(prop.pending.buffer)
}

// RejectedInputs reports how many inputs were rejected after a transfer.
func (a *AuthorityTransfer) RejectedInputs(entityID EntityID) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	prop, ok := a.props[entityID]
	if !ok {
		return 0
	}
	return prop.rejected
}

// Suspicion reports the anti cheat suspicion score of a player.
func (a *AuthorityTransfer) Suspicion(player PlayerID) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.suspicion[player]
}

// Stats reports manager counters.
func (a *AuthorityTransfer) Stats() TransferStats {
	a.mu.Lock()
	defer a.mu.Unlock()

	stats := TransferStats{Props: len(a.props), Replayed: a.replayed}
	for _, prop := range a.props {
		if prop.pending == nil {
			continue
		}
		stats.PendingTransfers = satAddInt(stats.PendingTransfers, 1)
		stats.BufferedInputs = satAddInt(stats.BufferedInputs, len(prop.pending.buffer))
	}
	return stats
}

type snapshotInput struct {
	Seq uint64  `json:"seq"`
	Cmd Command `json:"cmd"`
}

type snapshotPending struct {
	ID     TransferID      `json:"id"`
	From   PlayerID        `json:"from"`
	To     PlayerID        `json:"to"`
	Buffer []snapshotInput `json:"buffer"`
}

type snapshotProp struct {
	Entity       EntityID         `json:"entity"`
	Owner        PlayerID         `json:"owner"`
	LastSeq      uint64           `json:"last_seq"`
	Rejected     uint64           `json:"rejected"`
	FormerOwners []PlayerID       `json:"former_owners,omitempty"`
	Pending      *snapshotPending `json:"pending,omitempty"`
}

type snapshotSuspicion struct {
	Player PlayerID `json:"player"`
	Score  int      `json:"score"`
}

type snapshotState struct {
	Version        int                 `json:"version"`
	NextTransferID uint64              `json:"next_transfer_id"`
	Replayed       uint64              `json:"replayed"`
	Props          []snapshotProp      `json:"props"`
	Suspicion      []snapshotSuspicion `json:"suspicion,omitempty"`
}

// Snapshot serialises the whole manager state.
func (a *AuthorityTransfer) Snapshot() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	state := snapshotState{
		Version:        snapshotVersion,
		NextTransferID: a.nextTransferID,
		Replayed:       a.replayed,
	}

	entities := make([]EntityID, 0, len(a.props))
	for entityID := range a.props {
		entities = append(entities, entityID)
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i] < entities[j] })

	for _, entityID := range entities {
		prop := a.props[entityID]
		entry := snapshotProp{
			Entity:   entityID,
			Owner:    prop.owner,
			LastSeq:  prop.lastSeq,
			Rejected: prop.rejected,
		}
		if len(prop.formerOwners) > 0 {
			entry.FormerOwners = make([]PlayerID, 0, len(prop.formerOwners))
			for player := range prop.formerOwners {
				entry.FormerOwners = append(entry.FormerOwners, player)
			}
			sort.Slice(entry.FormerOwners, func(i, j int) bool {
				return entry.FormerOwners[i] < entry.FormerOwners[j]
			})
		}
		if pending := prop.pending; pending != nil {
			saved := &snapshotPending{ID: pending.id, From: pending.from, To: pending.to}
			saved.Buffer = make([]snapshotInput, len(pending.buffer))
			for i, input := range pending.buffer {
				saved.Buffer[i] = snapshotInput{Seq: input.seq, Cmd: input.cmd}
			}
			entry.Pending = saved
		}
		state.Props = append(state.Props, entry)
	}

	players := make([]PlayerID, 0, len(a.suspicion))
	for player := range a.suspicion {
		players = append(players, player)
	}
	sort.Slice(players, func(i, j int) bool { return players[i] < players[j] })
	for _, player := range players {
		state.Suspicion = append(state.Suspicion, snapshotSuspicion{
			Player: player,
			Score:  a.suspicion[player],
		})
	}

	return json.Marshal(state)
}

// Restore rebuilds the manager state from a snapshot payload.
func (a *AuthorityTransfer) Restore(payload []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	var state snapshotState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	props := make(map[EntityID]*propState, len(state.Props))
	seenTransfers := make(map[TransferID]struct{}, len(state.Props))
	var highestTransferID uint64

	for _, saved := range state.Props {
		if _, duplicate := props[saved.Entity]; duplicate {
			return ErrBadSnapshot
		}
		prop := &propState{
			owner:        saved.Owner,
			lastSeq:      saved.LastSeq,
			rejected:     saved.Rejected,
			formerOwners: make(map[PlayerID]struct{}, len(saved.FormerOwners)),
		}
		for _, player := range saved.FormerOwners {
			prop.formerOwners[player] = struct{}{}
		}
		if pending := saved.Pending; pending != nil {
			if pending.ID == 0 || pending.From == pending.To {
				return ErrBadSnapshot
			}
			if _, duplicate := seenTransfers[pending.ID]; duplicate {
				return ErrBadSnapshot
			}
			seenTransfers[pending.ID] = struct{}{}
			if uint64(pending.ID) > highestTransferID {
				highestTransferID = uint64(pending.ID)
			}
		}
		props[saved.Entity] = prop
	}

	suspicion := make(map[PlayerID]int, len(state.Suspicion))
	for _, saved := range state.Suspicion {
		if saved.Score < 0 {
			return ErrBadSnapshot
		}
		if _, duplicate := suspicion[saved.Player]; duplicate {
			return ErrBadSnapshot
		}
		suspicion[saved.Player] = saved.Score
	}

	nextTransferID := state.NextTransferID
	if nextTransferID <= highestTransferID {
		nextTransferID = highestTransferID + 1
	}
	if nextTransferID == 0 {
		nextTransferID = 1
	}

	a.props = props
	a.transfers = make(map[TransferID]EntityID)
	a.nextTransferID = nextTransferID
	a.suspicion = suspicion
	a.replayed = state.Replayed
	return nil
}
