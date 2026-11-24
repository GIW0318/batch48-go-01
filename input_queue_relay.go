// Package task relays client input upstream: duplicate frames are collapsed,
// out of order frames are buffered by frame number and the acknowledgement
// watermark only moves forward.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Per-player and per-frame hard limits.
const (
	MaxPerPlayerQueue   = 1024
	MaxSingleInputBytes = 4096
)

// Input is one frame worth of client input for a player.
type Input struct {
	PlayerID string `json:"player_id"`
	Frame    uint64 `json:"frame"`
	Priority int    `json:"priority"`
	MoveX    int    `json:"move_x"`
	MoveY    int    `json:"move_y"`
	Action   string `json:"action"`
}

// PushResult reports what happened to one pushed input.
type PushResult struct {
	Accepted  bool `json:"accepted"`
	Duplicate bool `json:"duplicate"`
	Merged    bool `json:"merged"`
	Acked     bool `json:"acked"`
	Buffered  int  `json:"buffered"`
}

// Errors returned by the input relay.
var (
	ErrInvalidPlayerID = errors.New("player id is required")
	ErrInvalidFrame    = errors.New("frame number must be greater than zero")
	ErrQueueFull       = errors.New("per player queue is full")
	ErrInvalidMax      = errors.New("flush limit must not be negative")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// InputQueueRelay owns the per-player receive queues and the watermarks.
type InputQueueRelay struct {
	mu           sync.Mutex
	perPlayerCap int
	lateWindow   uint64
}

// NewInputQueueRelay builds a relay with a per-player queue cap and a late
// arrival window measured in frames.
func NewInputQueueRelay(perPlayerCap int, lateWindow uint64) *InputQueueRelay {
	return &InputQueueRelay{perPlayerCap: perPlayerCap, lateWindow: lateWindow}
}

// Push accepts one frame of input for a player.
func (q *InputQueueRelay) Push(playerID string, frame uint64, in Input) (PushResult, error) {
	return PushResult{}, errNotImplemented
}

// Ack advances the downstream acknowledgement watermark of a player.
func (q *InputQueueRelay) Ack(playerID string, frame uint64) error {
	return errNotImplemented
}

// Flush hands at most max buffered inputs of a player to the downstream, in
// frame order.
func (q *InputQueueRelay) Flush(playerID string, max int) []Input {
	return nil
}

// Window reports the confirmed watermark and the highest frame seen.
func (q *InputQueueRelay) Window() (uint64, uint64) {
	return 0, 0
}

// Dropped reports how many inputs of a player were dropped as too late.
func (q *InputQueueRelay) Dropped(playerID string) int {
	return 0
}

// Pending reports how many inputs of a player still wait for Flush.
func (q *InputQueueRelay) Pending(playerID string) int {
	return 0
}

// Resend lists the unacknowledged inputs of a player after a reconnect,
// keeping the original frame numbers.
func (q *InputQueueRelay) Resend(playerID string) []Input {
	return nil
}

// HighestAcked reports the acknowledgement watermark of one player.
func (q *InputQueueRelay) HighestAcked(playerID string) uint64 {
	return 0
}

// Players lists the players that ever pushed input, in ascending id order.
func (q *InputQueueRelay) Players() []string {
	return nil
}

// Snapshot serialises the queues, the watermarks and the drop counters.
func (q *InputQueueRelay) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole relay state with a snapshot, all or nothing.
func (q *InputQueueRelay) Restore(data []byte) error {
	return errNotImplemented
}
