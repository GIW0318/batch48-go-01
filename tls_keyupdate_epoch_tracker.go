// Package task tracks the TLS 1.3 key update epochs of a terminating proxy.
// The proxy does not decrypt application data: it watches record headers,
// keeps one epoch and one record sequence per direction, answers KeyUpdate
// messages that request an update and keeps the whole state restartable.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the tracker.
var (
	ErrInvalidConfig    = errors.New("invalid tracker config")
	ErrInvalidDirection = errors.New("invalid direction")
	ErrReplayRecord     = errors.New("record sequence replayed or out of order")
	ErrEpochGap         = errors.New("record epoch skips ahead")
	ErrEpochTooOld      = errors.New("record epoch below the tolerance window")
	ErrEpochExhausted   = errors.New("key update epoch limit reached")
	ErrNoPendingResponse = errors.New("no key update response pending")
	ErrNoPendingUpdate   = errors.New("no local key update pending")
	ErrClosing           = errors.New("connection is closing")
	ErrBadSnapshot       = errors.New("bad snapshot")
)

// Direction identifies one direction of a TLS connection.
type Direction uint8

// The two traffic directions.
const (
	DirClientToProxy Direction = 1
	DirProxyToClient Direction = 2
)

// Opposite returns the other direction, or zero for an unknown direction.
func (d Direction) Opposite() Direction {
	return 0
}

// String renders the direction for logs.
func (d Direction) String() string {
	return ""
}

// KeyUpdateConfig bounds the tracker.
type KeyUpdateConfig struct {
	MaxEpochs      uint16
	EpochTolerance uint16
}

// KeyUpdateStats counts what one connection carried so far.
type KeyUpdateStats struct {
	Records           int
	ToleratedRecords  int
	ReplayedRecords   int
	GapRejected       int
	TooOldRejected    int
	PeerUpdates       int
	LocalUpdates      int
	CoalescedRequests int
	ResponsesWritten  int
	UpdatesWritten    int
	EpochRollovers    int
	Closing           bool
}

// dirState is the per direction epoch bookkeeping.
type dirState struct {
	epoch           uint16
	nextSeq         uint64
	pendingResponse bool
	responseEpoch   uint16
	pendingUpdate   bool
}

// KeyUpdateTracker follows both directions of one TLS connection.
type KeyUpdateTracker struct {
	mu    sync.Mutex
	cfg   KeyUpdateConfig
	dirs  map[Direction]*dirState
	stats KeyUpdateStats
}

// NewKeyUpdateTracker builds a tracker for one connection.
func NewKeyUpdateTracker(cfg KeyUpdateConfig) (*KeyUpdateTracker, error) {
	return nil, errNotImplemented
}

// ObserveRecord accounts for one record header seen on the wire.
func (t *KeyUpdateTracker) ObserveRecord(dir Direction, epoch uint16, seq uint64) error {
	return errNotImplemented
}

// RequestUpdate asks the peer to update its keys in the given direction.
func (t *KeyUpdateTracker) RequestUpdate(dir Direction) error {
	return errNotImplemented
}

// OnKeyUpdate handles a KeyUpdate message seen in the given direction and
// reports whether a response KeyUpdate has to be written.
func (t *KeyUpdateTracker) OnKeyUpdate(dir Direction, requested bool) (bool, error) {
	return false, errNotImplemented
}

// EpochOf returns the current epoch of the direction.
func (t *KeyUpdateTracker) EpochOf(dir Direction) uint16 {
	return 0
}

// NextSeq returns the next expected record sequence of the direction.
func (t *KeyUpdateTracker) NextSeq(dir Direction) uint64 {
	return 0
}

// ResponseEpoch returns the epoch a pending response must be written in.
func (t *KeyUpdateTracker) ResponseEpoch(dir Direction) (uint16, error) {
	return 0, errNotImplemented
}

// MarkResponseSent clears the pending response of the direction.
func (t *KeyUpdateTracker) MarkResponseSent(dir Direction) error {
	return errNotImplemented
}

// MarkUpdateSent clears the pending local key update of the direction.
func (t *KeyUpdateTracker) MarkUpdateSent(dir Direction) error {
	return errNotImplemented
}

// Stats returns a copy of the tracker counters.
func (t *KeyUpdateTracker) Stats() KeyUpdateStats {
	return KeyUpdateStats{}
}

// Snapshot serialises both direction states and the counters.
func (t *KeyUpdateTracker) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the tracker state with a snapshot.
func (t *KeyUpdateTracker) Restore(data []byte) error {
	return errNotImplemented
}
