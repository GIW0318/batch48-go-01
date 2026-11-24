// Package task indexes a replay stream into checksummed blocks, keeps
// keyframe marks and answers seeks by replaying forward from a keyframe.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Frame is one replay frame with an absolute tick number.
type Frame struct {
	Tick    uint64        `json:"tick"`
	Time    time.Duration `json:"time"`
	Payload []byte        `json:"payload"`
	Key     bool          `json:"key"`
}

// BlockInfo describes one committed block.
type BlockInfo struct {
	ID        uint64        `json:"id"`
	StartTick uint64        `json:"start_tick"`
	Count     int           `json:"count"`
	StartTime time.Duration `json:"start_time"`
	EndTime   time.Duration `json:"end_time"`
	Checksum  uint64        `json:"checksum"`
	KeyTicks  []uint64      `json:"key_ticks"`
}

// Cursor points into a committed block.
type Cursor struct {
	BlockID  uint64 `json:"block_id"`
	Offset   int    `json:"offset"`
	Tick     uint64 `json:"tick"`
	Degraded bool   `json:"degraded"`
}

// ReplayConfig bounds block size, keyframe spacing and block count.
type ReplayConfig struct {
	BlockFrames int `json:"block_frames"`
	KeyframeGap int `json:"keyframe_gap"`
	MaxBlocks   int `json:"max_blocks"`
}

// Errors returned by the indexer.
var (
	ErrInvalidConfig    = errors.New("replay config is invalid")
	ErrEmptyPayload     = errors.New("frame payload is required")
	ErrInvalidTick      = errors.New("tick numbers start at one")
	ErrNonMonotonicTick = errors.New("tick must increase")
	ErrTimeRegression   = errors.New("frame time must not go backwards")
	ErrNoData           = errors.New("no committed replay data")
	ErrOutOfRange       = errors.New("time is outside the retained replay")
	ErrBlockNotFound    = errors.New("replay block not found")
	ErrBlockCorrupt     = errors.New("replay block checksum mismatch")
	ErrInvalidKeep      = errors.New("keep count must be positive")
	ErrOffsetOutOfRange = errors.New("cursor offset is outside the block")
	ErrBadSnapshot      = errors.New("snapshot payload is invalid")
)

// ReplayIndexer owns the committed blocks and the block under construction.
type ReplayIndexer struct {
	mu  sync.RWMutex
	cfg ReplayConfig
}

// NewReplayIndexer builds an indexer for the given layout.
func NewReplayIndexer(cfg ReplayConfig) (*ReplayIndexer, error) {
	return nil, errNotImplemented
}

// Append buffers one frame and commits a block once it is full.
func (r *ReplayIndexer) Append(f Frame) error {
	return errNotImplemented
}

// Flush commits the block under construction, if any.
func (r *ReplayIndexer) Flush() error {
	return errNotImplemented
}

// Blocks lists the committed blocks in commit order.
func (r *ReplayIndexer) Blocks() []BlockInfo {
	return nil
}

// Seek returns the keyframe cursor a player must replay from to reach at.
func (r *ReplayIndexer) Seek(at time.Duration) (Cursor, error) {
	return Cursor{}, errNotImplemented
}

// Locate finds the first frame at or after at by replaying from its keyframe.
func (r *ReplayIndexer) Locate(at time.Duration) (uint64, error) {
	return 0, errNotImplemented
}

// ReadBlock returns the frames from the cursor to the end of its block.
func (r *ReplayIndexer) ReadBlock(cursor Cursor) ([]Frame, error) {
	return nil, errNotImplemented
}

// Verify recomputes the checksum of one block.
func (r *ReplayIndexer) Verify(blockID uint64) error {
	return errNotImplemented
}

// InjectCorruption marks one block as damaged so Verify reports it.
func (r *ReplayIndexer) InjectCorruption(blockID uint64) error {
	return errNotImplemented
}

// Prune drops the oldest blocks so that at most keep blocks remain.
func (r *ReplayIndexer) Prune(keep int) (int, error) {
	return 0, errNotImplemented
}

// Snapshot serialises the committed blocks, the open block and the cursor
// bookkeeping.
func (r *ReplayIndexer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole indexer state from a snapshot, all or nothing.
func (r *ReplayIndexer) Restore(data []byte) error {
	return errNotImplemented
}
