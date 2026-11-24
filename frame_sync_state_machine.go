// Package task drives a frame synchronised battle: it buffers per frame
// inputs, decides whether to wait, advance or degrade, and keeps a
// recomputable checksum per advanced frame.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Input is one player command addressed to one logical frame.
type Input struct {
	PlayerID string `json:"player_id"`
	Frame    uint64 `json:"frame"`
	Seq      int    `json:"seq"`
	Action   string `json:"action"`
}

// FrameSyncConfig carries the wait and catch up budgets.
type FrameSyncConfig struct {
	MaxWaitFrames  int `json:"max_wait_frames"`
	CatchUpPerTick int `json:"catch_up_per_tick"`
}

// TickResult is what one Tick produced.
type TickResult struct {
	Advanced int    `json:"advanced"`
	Frame    uint64 `json:"frame"`
	Checksum uint64 `json:"checksum"`
	Waited   bool   `json:"waited"`
	Degraded bool   `json:"degraded"`
	Lag      int    `json:"lag"`
}

// Errors returned by the machine.
var (
	ErrInvalidConfig  = errors.New("frame sync config is invalid")
	ErrInvalidFrame   = errors.New("frame numbers start at one")
	ErrUnknownPlayer  = errors.New("player is not part of this battle")
	ErrDuplicateInput = errors.New("input already submitted for this frame")
	ErrFrameClosed    = errors.New("frame has already been applied")
	ErrInvalidBudget  = errors.New("catch up budget must be positive")
	ErrPaused         = errors.New("machine is paused")
	ErrAlreadyPaused  = errors.New("machine is already paused")
	ErrNotPaused      = errors.New("machine is not paused")
	ErrBadSnapshot    = errors.New("snapshot payload is invalid")
)

// FrameSyncMachine is the deterministic frame pump of one battle.
type FrameSyncMachine struct {
	mu      sync.Mutex
	players []string
	cfg     FrameSyncConfig
}

// NewFrameSyncMachine builds a machine for the given roster and budgets.
func NewFrameSyncMachine(players []string, cfg FrameSyncConfig) (*FrameSyncMachine, error) {
	return nil, errNotImplemented
}

// Submit buffers one player input for a future frame.
func (m *FrameSyncMachine) Submit(playerID string, frame uint64, in Input) error {
	return errNotImplemented
}

// Tick advances up to the configured budget, degrading after a long wait.
func (m *FrameSyncMachine) Tick(now time.Time) (TickResult, error) {
	return TickResult{}, errNotImplemented
}

// WaitDepth counts the frames blocked on a missing player input.
func (m *FrameSyncMachine) WaitDepth() int {
	return 0
}

// CatchUp applies at most maxFrames fully collected frames.
func (m *FrameSyncMachine) CatchUp(maxFrames int) (int, error) {
	return 0, errNotImplemented
}

// Pause freezes advancement but keeps buffering inputs.
func (m *FrameSyncMachine) Pause() error {
	return errNotImplemented
}

// Resume lets Tick advance again.
func (m *FrameSyncMachine) Resume() error {
	return errNotImplemented
}

// Frame reports the last applied frame.
func (m *FrameSyncMachine) Frame() uint64 {
	return 0
}

// ChecksumAt reads the stored checksum of one applied frame.
func (m *FrameSyncMachine) ChecksumAt(frame uint64) (uint64, bool) {
	return 0, false
}

// DegradedFrames lists the frames that advanced without every input.
func (m *FrameSyncMachine) DegradedFrames() []uint64 {
	return nil
}

// Snapshot serialises the frame, the checksum history and the input log.
func (m *FrameSyncMachine) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the machine from a snapshot, all or nothing.
func (m *FrameSyncMachine) Restore(data []byte) error {
	return errNotImplemented
}
