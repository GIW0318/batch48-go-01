package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// Sentinels reported by the session clock estimator.
var (
	ErrInvalidSample = errors.New("sample rejected: receive time must be after send time")
	ErrClockRollback = errors.New("sample rejected: local clock moved backwards")
	ErrInvalidWindow = errors.New("window size must be at least one")
	ErrBadSnapshot   = errors.New("snapshot payload rejected")
)

const (
	snapshotVersion = 1

	// A server-side clock correction is large compared with ordinary network
	// jitter. It is only trusted after several consecutive samples agree on the
	// new offset.
	serverJumpThresholdMs = int64(1000)
	serverJumpConfirm     = 3
)

// EstimatorStats is the observable estimator counter block.
type EstimatorStats struct {
	Samples  int
	Rejected uint64
	Offset   int64
	RTT      int
}

type clockSample struct {
	send   int64
	recv   int64
	server int64
	offset int64
	rtt    int
}

// SessionClockEstimator estimates the client to server clock offset and RTT.
type SessionClockEstimator struct {
	mu sync.RWMutex

	window   int
	samples  []clockSample
	offset   int64
	rtt      int
	rejected uint64

	lastSend int64
	lastRecv int64
	hasLast  bool

	jumpCandidate    int64
	jumpMatches      int
	hasJumpCandidate bool
}

// NewSessionClockEstimator builds an estimator over a sliding window of samples.
func NewSessionClockEstimator(window int) *SessionClockEstimator {
	if window < 1 {
		window = 1
	}
	return &SessionClockEstimator{window: window}
}

// Sample records one ping/pong round trip.
func (e *SessionClockEstimator) Sample(send, recv, serverMs int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if recv <= send {
		e.rejected++
		return ErrInvalidSample
	}
	if e.hasLast && (send < e.lastSend || recv < e.lastRecv) {
		e.rejected++
		return ErrClockRollback
	}

	midpoint := send + (recv-send)/2
	sample := clockSample{
		send:   send,
		recv:   recv,
		server: serverMs,
		offset: serverMs - midpoint,
		rtt:    int(recv - send),
	}

	if len(e.samples) > 0 && abs64(sample.offset-e.offset) > serverJumpThresholdMs {
		if e.hasJumpCandidate && sameDirection(sample.offset-e.offset, e.jumpCandidate-e.offset) {
			e.jumpMatches++
		} else {
			e.jumpCandidate = sample.offset
			e.jumpMatches = 1
			e.hasJumpCandidate = true
		}
		if e.jumpMatches < serverJumpConfirm {
			e.rejected++
			return ErrInvalidSample
		}

		// The new server clock domain is confirmed. Samples from the old
		// domain must not keep the median pinned to the stale offset.
		e.samples = e.samples[:0]
		e.samples = append(e.samples, sample)
		e.offset = sample.offset
		e.rtt = sample.rtt
		e.lastSend = send
		e.lastRecv = recv
		e.hasLast = true
		e.clearJumpCandidateLocked()
		return nil
	}

	// A lone sample far from the current estimate is just an outlier. Once the
	// stream agrees with the old baseline again, forget any pending jump.
	e.clearJumpCandidateLocked()
	e.appendLocked(sample)
	return nil
}

// Offset reports the robust clock offset estimate in milliseconds.
func (e *SessionClockEstimator) Offset() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.offset
}

// RTT reports the median round trip time of the window in milliseconds.
func (e *SessionClockEstimator) RTT() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rtt
}

// Reset clears every sample and the current estimate.
func (e *SessionClockEstimator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.samples = nil
	e.offset = 0
	e.rtt = 0
	e.rejected = 0
	e.lastSend = 0
	e.lastRecv = 0
	e.hasLast = false
	e.clearJumpCandidateLocked()
}

// SetWindow changes the sliding window size and trims older samples.
func (e *SessionClockEstimator) SetWindow(n int) error {
	if n < 1 {
		return ErrInvalidWindow
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.window = n
	if len(e.samples) > n {
		keep := len(e.samples) - n
		copy(e.samples, e.samples[keep:])
		e.samples = e.samples[:n]
	}
	e.recomputeLocked()
	e.clearJumpCandidateLocked()
	return nil
}

// Samples reports how many samples the window currently holds.
func (e *SessionClockEstimator) Samples() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.samples)
}

// Rejected reports how many samples were refused.
func (e *SessionClockEstimator) Rejected() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rejected
}

// Stats reports estimator counters and the current estimate.
func (e *SessionClockEstimator) Stats() EstimatorStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return EstimatorStats{
		Samples:  len(e.samples),
		Rejected: e.rejected,
		Offset:   e.offset,
		RTT:      e.rtt,
	}
}

type snapshotSample struct {
	Send   int64 `json:"send"`
	Recv   int64 `json:"recv"`
	Server int64 `json:"server"`
}

type snapshotPayload struct {
	Window   int              `json:"window"`
	Samples  []snapshotSample `json:"samples"`
	Offset   int64            `json:"offset"`
	RTT      int              `json:"rtt"`
	Rejected uint64           `json:"rejected"`
	LastSend int64            `json:"last_send,omitempty"`
	LastRecv int64            `json:"last_recv,omitempty"`
	HasLast  bool             `json:"has_last,omitempty"`
}

type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Payload  json.RawMessage `json:"payload"`
	Checksum string          `json:"checksum"`
}

// Snapshot serialises the window samples and the current estimate.
func (e *SessionClockEstimator) Snapshot() ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	payload := snapshotPayload{
		Window:   e.window,
		Samples:  make([]snapshotSample, len(e.samples)),
		Offset:   e.offset,
		RTT:      e.rtt,
		Rejected: e.rejected,
		LastSend: e.lastSend,
		LastRecv: e.lastRecv,
		HasLast:  e.hasLast,
	}
	for i, sample := range e.samples {
		payload.Samples[i] = snapshotSample{
			Send:   sample.send,
			Recv:   sample.recv,
			Server: sample.server,
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		Payload:  raw,
		Checksum: hex.EncodeToString(sum[:]),
	}
	return json.Marshal(envelope)
}

// Restore rebuilds the estimator state from a snapshot payload.
func (e *SessionClockEstimator) Restore(payload []byte) error {
	if len(payload) == 0 {
		return ErrBadSnapshot
	}

	var envelope snapshotEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ErrBadSnapshot
	}
	if envelope.Version != snapshotVersion || len(envelope.Payload) == 0 {
		return ErrBadSnapshot
	}
	sum := sha256.Sum256(envelope.Payload)
	if envelope.Checksum != hex.EncodeToString(sum[:]) {
		return ErrBadSnapshot
	}

	var state snapshotPayload
	if err := json.Unmarshal(envelope.Payload, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Window < 1 || len(state.Samples) > state.Window {
		return ErrBadSnapshot
	}

	restored := make([]clockSample, 0, len(state.Samples))
	var prevSend, prevRecv int64
	for i, sample := range state.Samples {
		if sample.Recv <= sample.Send {
			return ErrBadSnapshot
		}
		if i > 0 && (sample.Send < prevSend || sample.Recv < prevRecv) {
			return ErrBadSnapshot
		}
		midpoint := sample.Send + (sample.Recv-sample.Send)/2
		restored = append(restored, clockSample{
			send:   sample.Send,
			recv:   sample.Recv,
			server: sample.Server,
			offset: sample.Server - midpoint,
			rtt:    int(sample.Recv - sample.Send),
		})
		prevSend = sample.Send
		prevRecv = sample.Recv
	}

	snapOffset, snapRTT := medians(restored)
	if len(restored) > 0 && (state.Offset != snapOffset || state.RTT != snapRTT) {
		return ErrBadSnapshot
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if len(restored) > e.window {
		restored = restored[len(restored)-e.window:]
	}
	e.samples = restored
	e.recomputeLocked()
	e.rejected = state.Rejected
	e.clearJumpCandidateLocked()

	if len(restored) > 0 {
		newest := restored[len(restored)-1]
		e.lastSend = newest.send
		e.lastRecv = newest.recv
		e.hasLast = true
	} else if state.HasLast {
		e.lastSend = state.LastSend
		e.lastRecv = state.LastRecv
		e.hasLast = true
	} else {
		e.lastSend = 0
		e.lastRecv = 0
		e.hasLast = false
	}
	return nil
}

func (e *SessionClockEstimator) appendLocked(sample clockSample) {
	e.samples = append(e.samples, sample)
	if len(e.samples) > e.window {
		keep := len(e.samples) - e.window
		copy(e.samples, e.samples[keep:])
		e.samples = e.samples[:e.window]
	}
	e.lastSend = sample.send
	e.lastRecv = sample.recv
	e.hasLast = true
	e.recomputeLocked()
}

func (e *SessionClockEstimator) recomputeLocked() {
	e.offset, e.rtt = medians(e.samples)
}

func (e *SessionClockEstimator) clearJumpCandidateLocked() {
	e.jumpCandidate = 0
	e.jumpMatches = 0
	e.hasJumpCandidate = false
}

func medians(samples []clockSample) (int64, int) {
	n := len(samples)
	if n == 0 {
		return 0, 0
	}

	offsets := make([]int64, n)
	rtts := make([]int, n)
	for i, sample := range samples {
		offsets[i] = sample.offset
		rtts[i] = sample.rtt
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	sort.Ints(rtts)

	// For an even count the lower of the two middle values is used so the
	// offset and RTT estimates share exactly the same definition.
	middle := (n - 1) / 2
	return offsets[middle], rtts[middle]
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func sameDirection(a, b int64) bool {
	return (a > 0 && b > 0) || (a < 0 && b < 0)
}
