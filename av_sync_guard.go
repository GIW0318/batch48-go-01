package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// ClockSource names the timeline used as the sync master.
type ClockSource string

// Supported master clocks.
const (
	MasterAudio    ClockSource = "audio"
	MasterVideo    ClockSource = "video"
	MasterExternal ClockSource = "external"
)

// Decision actions returned by Decide.
const (
	ActionKeep          = "keep"
	ActionDropVideo     = "drop_video"
	ActionRepeatVideo   = "repeat_video"
	ActionResampleAudio = "resample_audio"
)

// Errors reported by the sync guard.
var (
	ErrClockUnsupported  = errors.New("master clock is not supported")
	ErrNoMasterClock     = errors.New("master clock is not set")
	ErrVideoOrder        = errors.New("video presentation time is not increasing")
	ErrAudioBlock        = errors.New("audio block is not usable")
	ErrThresholdsInvalid = errors.New("sync thresholds are not usable")
	ErrStateCorrupt      = errors.New("sync guard state is corrupt")
)

// VideoFrame is one decoded video frame waiting for presentation.
type VideoFrame struct {
	PTS      int64
	Keyframe bool
	Seq      int64
}

// AudioBlock is one audio block and the playback position the device reports.
type AudioBlock struct {
	PTS      int64
	Duration int64
	Position int64
}

// Thresholds configures drift estimation and compensation.
type Thresholds struct {
	SyncWindow       int64
	MaxStepPerCall   int64
	SmoothingSamples int
	AudioQuantum     int64
	MaxQueue         int
}

// SyncDecision is the outcome of one Decide call.
type SyncDecision struct {
	Action        string
	Frame         VideoFrame
	RepeatCount   int
	ResampleRatio float64
	Drift         int64
	DriftAfter    int64
}

// SyncStats reports the counters of the guard.
type SyncStats struct {
	Decides      uint64
	Drops        uint64
	Repeats      uint64
	Resamples    uint64
	KeyframeDrops uint64
	PushedVideo  uint64
	PushedAudio  uint64
	Rejected     uint64
	ResetSamples uint64
	InSync       bool
}

// AvSyncGuard keeps video presentation aligned with the master clock.
type AvSyncGuard struct {
	mu sync.Mutex
}

// NewAvSyncGuard builds a guard with the given thresholds.
func NewAvSyncGuard(th Thresholds) *AvSyncGuard {
	return &AvSyncGuard{}
}

// SetMasterClock selects the timeline every correction is measured against.
func (g *AvSyncGuard) SetMasterClock(src ClockSource) error {
	return errNotImplemented
}

// MasterClock reports the selected master clock.
func (g *AvSyncGuard) MasterClock() ClockSource {
	return ""
}

// PushVideo queues one decoded video frame.
func (g *AvSyncGuard) PushVideo(frame VideoFrame) error {
	return errNotImplemented
}

// PushAudio queues one audio block with the reported playback position.
func (g *AvSyncGuard) PushAudio(block AudioBlock) error {
	return errNotImplemented
}

// Decide estimates the drift and returns the next compensation step.
func (g *AvSyncGuard) Decide() (SyncDecision, error) {
	return SyncDecision{}, errNotImplemented
}

// SetThresholds replaces the estimation and compensation thresholds.
func (g *AvSyncGuard) SetThresholds(th Thresholds) error {
	return errNotImplemented
}

// ResetSource clears the samples, the compensation offset and the queues.
func (g *AvSyncGuard) ResetSource() error {
	return errNotImplemented
}

// DriftEstimate reports the smoothed drift in microseconds.
func (g *AvSyncGuard) DriftEstimate() int64 {
	return 0
}

// QueueLen reports the queued video frames and audio blocks.
func (g *AvSyncGuard) QueueLen() (int, int) {
	return 0, 0
}

// PendingPTS reports the presentation times of the queued video frames.
func (g *AvSyncGuard) PendingPTS() []int64 {
	return nil
}

// Snapshot serialises the clocks, thresholds, samples, queues and counters.
func (g *AvSyncGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard state from a snapshot.
func (g *AvSyncGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Stats reports the guard counters.
func (g *AvSyncGuard) Stats() SyncStats {
	return SyncStats{}
}
