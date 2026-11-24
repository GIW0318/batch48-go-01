package task

import "errors"

// Sentinels reported by the input redundancy framer.
var (
	errNotImplemented     = errors.New("not implemented")
	ErrPlayerUnknown      = errors.New("player not registered")
	ErrPlayerExists       = errors.New("player already registered")
	ErrInvalidFrame       = errors.New("frame number must be positive")
	ErrInvalidRedundancy  = errors.New("redundancy must be between 1 and 8")
	ErrInvalidBufferLimit = errors.New("buffer limit must be at least one")
	ErrInvalidGrace       = errors.New("afk grace must be at least one")
	ErrInputConflict      = errors.New("conflicting input for the same player and frame")
	ErrBufferFull         = errors.New("pending frame buffer is full")
	ErrBadSnapshot        = errors.New("snapshot payload rejected")
)

// PlayerID identifies one input source.
type PlayerID int

// Input is the per frame input of one player.
type Input struct {
	Player  PlayerID
	MoveX   int
	MoveY   int
	Actions uint32
}

// Packet carries one redundant copy of a frame input.
type Packet struct {
	Player PlayerID
	Frame  uint64
	Copy   int
	Input  Input
}

// FramerStats is the observable framer counter block.
type FramerStats struct {
	Players    int
	Duplicates uint64
	Expired    uint64
	Pending    int
}

// InputRedundancyFramer encodes and reassembles redundant frame inputs.
type InputRedundancyFramer struct {
}

// NewInputRedundancyFramer builds a framer that repeats each input in n packets.
func NewInputRedundancyFramer(redundancy int) *InputRedundancyFramer {
	return &InputRedundancyFramer{}
}

// SetRedundancy changes how many packets repeat each input.
func (f *InputRedundancyFramer) SetRedundancy(n int) error {
	return errNotImplemented
}

// SetBufferLimit caps the number of pending frames kept per player.
func (f *InputRedundancyFramer) SetBufferLimit(n int) error {
	return errNotImplemented
}

// SetAFKGrace configures how many frames without input mark a player as afk.
func (f *InputRedundancyFramer) SetAFKGrace(n int) error {
	return errNotImplemented
}

// AddPlayer registers a player that participates from its join frame on.
func (f *InputRedundancyFramer) AddPlayer(player PlayerID, joinFrame uint64) error {
	return errNotImplemented
}

// RemovePlayer drops a player from the readiness requirement.
func (f *InputRedundancyFramer) RemovePlayer(player PlayerID) error {
	return errNotImplemented
}

// Encode repeats one frame input into the configured number of packets.
func (f *InputRedundancyFramer) Encode(frame uint64, in Input) ([]Packet, error) {
	return nil, errNotImplemented
}

// Ingest accepts one redundant packet and reports the frame it belongs to.
func (f *InputRedundancyFramer) Ingest(p Packet) (frame uint64, in Input, ok bool, err error) {
	return 0, Input{}, false, errNotImplemented
}

// Ready reports whether every participating player has delivered a frame.
func (f *InputRedundancyFramer) Ready(frame uint64) bool {
	return false
}

// DropBefore discards pending frames older than the given frame.
func (f *InputRedundancyFramer) DropBefore(frame uint64) {
}

// PendingFrames reports how many distinct frames wait for a player.
func (f *InputRedundancyFramer) PendingFrames(player PlayerID) int {
	return 0
}

// Stats reports framer counters.
func (f *InputRedundancyFramer) Stats() FramerStats {
	return FramerStats{}
}

// Snapshot serialises the whole framer state.
func (f *InputRedundancyFramer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the framer state from a snapshot payload.
func (f *InputRedundancyFramer) Restore(payload []byte) error {
	return errNotImplemented
}
