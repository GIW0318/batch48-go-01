package task

import "errors"

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

// AuthorityTransfer owns the authoritative holder of every pickup prop.
type AuthorityTransfer struct {
}

// NewAuthorityTransfer builds an empty transfer manager.
func NewAuthorityTransfer() *AuthorityTransfer {
	return &AuthorityTransfer{}
}

// RegisterProp creates a prop owned by the given player at the given sequence.
func (a *AuthorityTransfer) RegisterProp(entityID EntityID, owner PlayerID, seq uint64) error {
	return errNotImplemented
}

// Request asks to move authority for a prop from one player to another.
func (a *AuthorityTransfer) Request(entityID EntityID, from, to PlayerID) (TransferID, error) {
	return 0, errNotImplemented
}

// Commit finishes the pending transfer at the given commit sequence.
func (a *AuthorityTransfer) Commit(entityID EntityID, atSeq uint64) error {
	return errNotImplemented
}

// Input accepts one player command for a prop.
func (a *AuthorityTransfer) Input(entityID EntityID, owner PlayerID, seq uint64, cmd Command) error {
	return errNotImplemented
}

// Cancel drops a pending transfer and keeps the original owner.
func (a *AuthorityTransfer) Cancel(transferID TransferID) error {
	return errNotImplemented
}

// Destroy removes a prop from the world.
func (a *AuthorityTransfer) Destroy(entityID EntityID) error {
	return errNotImplemented
}

// Owner reports the current authoritative holder.
func (a *AuthorityTransfer) Owner(entityID EntityID) (PlayerID, error) {
	return 0, errNotImplemented
}

// Pending reports the pending transfer of a prop, if any.
func (a *AuthorityTransfer) Pending(entityID EntityID) (TransferID, bool) {
	return 0, false
}

// Buffered reports how many inputs wait in the transfer buffer.
func (a *AuthorityTransfer) Buffered(entityID EntityID) int {
	return 0
}

// RejectedInputs reports how many inputs were rejected after a transfer.
func (a *AuthorityTransfer) RejectedInputs(entityID EntityID) uint64 {
	return 0
}

// Suspicion reports the anti cheat suspicion score of a player.
func (a *AuthorityTransfer) Suspicion(player PlayerID) int {
	return 0
}

// Stats reports manager counters.
func (a *AuthorityTransfer) Stats() TransferStats {
	return TransferStats{}
}

// Snapshot serialises the whole manager state.
func (a *AuthorityTransfer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the manager state from a snapshot payload.
func (a *AuthorityTransfer) Restore(payload []byte) error {
	return errNotImplemented
}
