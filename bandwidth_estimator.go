package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Estimator states reported by State.
const (
	StateHold     = "hold"
	StateIncrease = "increase"
	StateDecrease = "decrease"
)

// Errors reported by the estimator.
var (
	ErrBoundsInvalid   = errors.New("bitrate bounds are not usable")
	ErrSampleRejected  = errors.New("bandwidth sample is not usable")
	ErrCooldownInvalid = errors.New("cooldown duration is negative")
	ErrStateCorrupt    = errors.New("bandwidth estimator state is corrupt")
)

// Sample is one feedback group of packets with send and arrival times.
type Sample struct {
	GroupID   uint64
	SendMs    int64
	ArrivalMs int64
	Bytes     int
	Received  int
	Lost      int
}

// Bounds configures the estimator and the target bitrate policy.
type Bounds struct {
	MinBitrate        int
	MaxBitrate        int
	StartBitrate      int
	IncreaseStep      int
	DecreaseFactor    float64
	GradientThreshold int64
	OverloadHoldMs    int64
	LossThreshold     float64
	CooldownMs        int64
	WindowSize        int
}

// EstimatorStats reports the counters of the estimator.
type EstimatorStats struct {
	Samples        uint64
	BadSamples     uint64
	Duplicates     uint64
	Overuse        uint64
	Underuse       uint64
	Increases      uint64
	Decreases      uint64
	Clamped        uint64
	LossGated      uint64
	CooldownBlocks uint64
	StateSwitches  uint64
}

// BandwidthEstimator derives a target bitrate from arrival time gradients.
type BandwidthEstimator struct {
	mu sync.Mutex
}

// NewBandwidthEstimator builds an estimator with the given bounds.
func NewBandwidthEstimator(bounds Bounds) (*BandwidthEstimator, error) {
	return nil, errNotImplemented
}

// AddSample feeds one feedback group into the delay gradient window.
func (e *BandwidthEstimator) AddSample(sample Sample) error {
	return errNotImplemented
}

// Target reports the bitrate the sender should use now.
func (e *BandwidthEstimator) Target() int {
	return 0
}

// SetBounds replaces the allowed bitrate range.
func (e *BandwidthEstimator) SetBounds(minBitrate, maxBitrate int) error {
	return errNotImplemented
}

// EnterCooldown forces a cooldown of the given duration in milliseconds.
func (e *BandwidthEstimator) EnterCooldown(millis int64) error {
	return errNotImplemented
}

// CooldownRemaining reports the remaining cooldown in milliseconds.
func (e *BandwidthEstimator) CooldownRemaining() int64 {
	return 0
}

// State reports the current estimator state.
func (e *BandwidthEstimator) State() string {
	return ""
}

// Snapshot serialises the window, the state, the target and the counters.
func (e *BandwidthEstimator) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the estimator state from a snapshot.
func (e *BandwidthEstimator) Restore(payload []byte) error {
	return errNotImplemented
}

// Stats reports the estimator counters.
func (e *BandwidthEstimator) Stats() EstimatorStats {
	return EstimatorStats{}
}

// Reset clears the window, the target and the counters.
func (e *BandwidthEstimator) Reset() error {
	return errNotImplemented
}
