package task

import "errors"

// Sentinels reported by the session clock estimator.
var (
	errNotImplemented = errors.New("not implemented")
	ErrInvalidSample  = errors.New("sample rejected: receive time must be after send time")
	ErrClockRollback  = errors.New("sample rejected: local clock moved backwards")
	ErrInvalidWindow  = errors.New("window size must be at least one")
	ErrBadSnapshot    = errors.New("snapshot payload rejected")
)

// EstimatorStats is the observable estimator counter block.
type EstimatorStats struct {
	Samples  int
	Rejected uint64
	Offset   int64
	RTT      int
}

// SessionClockEstimator estimates the client to server clock offset and RTT.
type SessionClockEstimator struct {
}

// NewSessionClockEstimator builds an estimator over a sliding window of samples.
func NewSessionClockEstimator(window int) *SessionClockEstimator {
	return &SessionClockEstimator{}
}

// Sample records one ping/pong round trip.
func (e *SessionClockEstimator) Sample(send, recv, serverMs int64) error {
	return errNotImplemented
}

// Offset reports the robust clock offset estimate in milliseconds.
func (e *SessionClockEstimator) Offset() int64 {
	return 0
}

// RTT reports the median round trip time of the window in milliseconds.
func (e *SessionClockEstimator) RTT() int {
	return 0
}

// Reset clears every sample and the current estimate.
func (e *SessionClockEstimator) Reset() {
}

// SetWindow changes the sliding window size and trims older samples.
func (e *SessionClockEstimator) SetWindow(n int) error {
	return errNotImplemented
}

// Samples reports how many samples the window currently holds.
func (e *SessionClockEstimator) Samples() int {
	return 0
}

// Rejected reports how many samples were refused.
func (e *SessionClockEstimator) Rejected() uint64 {
	return 0
}

// Stats reports estimator counters and the current estimate.
func (e *SessionClockEstimator) Stats() EstimatorStats {
	return EstimatorStats{}
}

// Snapshot serialises the window samples and the current estimate.
func (e *SessionClockEstimator) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the estimator state from a snapshot payload.
func (e *SessionClockEstimator) Restore(payload []byte) error {
	return errNotImplemented
}
