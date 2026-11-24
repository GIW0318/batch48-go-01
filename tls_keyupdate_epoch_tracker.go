// Package task tracks the TLS 1.3 key update epochs of a terminating proxy.
// The proxy does not decrypt application data: it watches record headers,
// keeps one epoch and one record sequence per direction, answers KeyUpdate
// messages that request an update and keeps the whole state restartable.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Errors reported by the tracker.
var (
	ErrInvalidConfig     = errors.New("invalid tracker config")
	ErrInvalidDirection  = errors.New("invalid direction")
	ErrReplayRecord      = errors.New("record sequence replayed or out of order")
	ErrEpochGap          = errors.New("record epoch skips ahead")
	ErrEpochTooOld       = errors.New("record epoch below the tolerance window")
	ErrEpochExhausted    = errors.New("key update epoch limit reached")
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

// validDirection reports whether d is one of the two tracked directions.
func validDirection(d Direction) bool {
	return d == DirClientToProxy || d == DirProxyToClient
}

// Opposite returns the other direction, or zero for an unknown direction.
func (d Direction) Opposite() Direction {
	switch d {
	case DirClientToProxy:
		return DirProxyToClient
	case DirProxyToClient:
		return DirClientToProxy
	default:
		return 0
	}
}

// String renders the direction for logs.
func (d Direction) String() string {
	switch d {
	case DirClientToProxy:
		return "client_to_proxy"
	case DirProxyToClient:
		return "proxy_to_client"
	default:
		return "unknown_direction"
	}
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
	if cfg.MaxEpochs < 2 {
		return nil, fmt.Errorf("%w: MaxEpochs must be at least 2", ErrInvalidConfig)
	}
	t := &KeyUpdateTracker{
		cfg: cfg,
		dirs: map[Direction]*dirState{
			DirClientToProxy: {epoch: epochInitial},
			DirProxyToClient: {epoch: epochInitial},
		},
	}
	return t, nil
}

// epochInitial is the first epoch used by every direction.
const epochInitial uint16 = 1

// ObserveRecord accounts for one record header seen on the wire.
func (t *KeyUpdateTracker) ObserveRecord(dir Direction, epoch uint16, seq uint64) error {
	if !validDirection(dir) {
		return ErrInvalidDirection
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stats.Closing {
		return ErrClosing
	}

	st := t.dirs[dir]
	switch {
	case epoch > st.epoch:
		// A future epoch is only valid once a KeyUpdate has advanced the
		// tracked epoch. This covers the immediate next epoch as well.
		t.stats.GapRejected++
		return ErrEpochGap
	case epoch < st.epoch:
		// Delayed records from the previous epoch generations are allowed
		// through while they stay inside the tolerance window. They never
		// move the current epoch sequence forward.
		if st.epoch-epoch <= t.cfg.EpochTolerance {
			t.stats.ToleratedRecords++
			return nil
		}
		t.stats.TooOldRejected++
		return ErrEpochTooOld
	default:
		// The expected sequence is the only accepted one: duplicates and
		// forward jumps are both replayed/out-of-order records.
		if seq != st.nextSeq {
			t.stats.ReplayedRecords++
			return ErrReplayRecord
		}
		st.nextSeq++
		t.stats.Records++
		return nil
	}
}

// RequestUpdate asks the peer to update its keys in the given direction.
func (t *KeyUpdateTracker) RequestUpdate(dir Direction) error {
	if !validDirection(dir) {
		return ErrInvalidDirection
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stats.Closing {
		return ErrClosing
	}

	st := t.dirs[dir]
	if st.pendingUpdate {
		// Consecutive local requests collapse onto the single KeyUpdate
		// that is still waiting to be written.
		t.stats.CoalescedRequests++
		return nil
	}

	if st.epoch >= t.cfg.MaxEpochs {
		t.stats.Closing = true
		return ErrEpochExhausted
	}

	st.epoch++
	st.nextSeq = 0
	st.pendingUpdate = true
	t.stats.LocalUpdates++
	t.stats.EpochRollovers++
	return nil
}

// OnKeyUpdate handles a KeyUpdate message seen in the given direction and
// reports whether a response KeyUpdate has to be written.
func (t *KeyUpdateTracker) OnKeyUpdate(dir Direction, requested bool) (bool, error) {
	if !validDirection(dir) {
		return false, ErrInvalidDirection
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stats.Closing {
		return false, ErrClosing
	}

	st := t.dirs[dir]
	if st.epoch >= t.cfg.MaxEpochs {
		// Refuse the advancement and close the whole connection before
		// mutating any half of the state.
		t.stats.Closing = true
		return false, ErrEpochExhausted
	}

	// Commit the epoch rollover and sequence reset together.
	st.epoch++
	st.nextSeq = 0
	t.stats.PeerUpdates++
	t.stats.EpochRollovers++

	if !requested {
		return false, nil
	}

	// The response is written in the opposite direction. Arm it exactly
	// once: a request already pending only refreshes the response epoch to
	// the newest generation and is coalesced into the same reply.
	resp := t.dirs[dir.Opposite()]
	newlyArmed := !resp.pendingResponse
	resp.pendingResponse = true
	resp.responseEpoch = st.epoch
	return newlyArmed, nil
}

// EpochOf returns the current epoch of the direction.
func (t *KeyUpdateTracker) EpochOf(dir Direction) uint16 {
	if !validDirection(dir) {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dirs[dir].epoch
}

// NextSeq returns the next expected record sequence of the direction.
func (t *KeyUpdateTracker) NextSeq(dir Direction) uint64 {
	if !validDirection(dir) {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dirs[dir].nextSeq
}

// ResponseEpoch returns the epoch a pending response must be written in.
func (t *KeyUpdateTracker) ResponseEpoch(dir Direction) (uint16, error) {
	if !validDirection(dir) {
		return 0, ErrInvalidDirection
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.dirs[dir]
	if !st.pendingResponse {
		return 0, ErrNoPendingResponse
	}
	return st.responseEpoch, nil
}

// MarkResponseSent clears the pending response of the direction.
func (t *KeyUpdateTracker) MarkResponseSent(dir Direction) error {
	if !validDirection(dir) {
		return ErrInvalidDirection
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.dirs[dir]
	if !st.pendingResponse {
		return ErrNoPendingResponse
	}
	st.pendingResponse = false
	st.responseEpoch = 0
	t.stats.ResponsesWritten++
	return nil
}

// MarkUpdateSent clears the pending local key update of the direction.
func (t *KeyUpdateTracker) MarkUpdateSent(dir Direction) error {
	if !validDirection(dir) {
		return ErrInvalidDirection
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.dirs[dir]
	if !st.pendingUpdate {
		return ErrNoPendingUpdate
	}
	st.pendingUpdate = false
	t.stats.UpdatesWritten++
	return nil
}

// Stats returns a copy of the tracker counters.
func (t *KeyUpdateTracker) Stats() KeyUpdateStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// Snapshot serialises both direction states and the counters.
func (t *KeyUpdateTracker) Snapshot() ([]byte, error) {
	t.mu.Lock()
	directions := make([]dirSnapshot, 0, 2)
	for _, dir := range []Direction{DirClientToProxy, DirProxyToClient} {
		st := t.dirs[dir]
		directions = append(directions, dirSnapshot{
			Dir:             dir,
			Epoch:           st.epoch,
			NextSeq:         st.nextSeq,
			PendingResponse: st.pendingResponse,
			ResponseEpoch:   st.responseEpoch,
			PendingUpdate:   st.pendingUpdate,
		})
	}
	stats := t.stats
	t.mu.Unlock()

	doc := snapshotDocument{
		Version: snapshotFormatVersion,
		Stats:   stats,
		Dirs:    directions,
	}
	data, err := json.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces the tracker state with a snapshot.
func (t *KeyUpdateTracker) Restore(data []byte) error {
	var doc snapshotDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if err := doc.validate(); err != nil {
		return err
	}

	next := make(map[Direction]*dirState, 2)
	for _, item := range doc.Dirs {
		next[item.Dir] = &dirState{
			epoch:           item.Epoch,
			nextSeq:         item.NextSeq,
			pendingResponse: item.PendingResponse,
			responseEpoch:   item.ResponseEpoch,
			pendingUpdate:   item.PendingUpdate,
		}
	}

	t.mu.Lock()
	t.dirs = next
	t.stats = doc.Stats
	t.mu.Unlock()
	return nil
}

// snapshotFormatVersion guards the serialised snapshot layout.
const snapshotFormatVersion = 1

// snapshotDocument is the versioned JSON envelope used by Snapshot/Restore.
type snapshotDocument struct {
	Version uint16         `json:"version"`
	Stats   KeyUpdateStats `json:"stats"`
	Dirs    []dirSnapshot  `json:"dirs"`
}

// dirSnapshot is the serialised form of one dirState.
type dirSnapshot struct {
	Dir             Direction `json:"dir"`
	Epoch           uint16    `json:"epoch"`
	NextSeq         uint64    `json:"next_seq"`
	PendingResponse bool      `json:"pending_response"`
	ResponseEpoch   uint16    `json:"response_epoch"`
	PendingUpdate   bool      `json:"pending_update"`
}

// validate performs a full parse-time check so that a bad snapshot is rejected
// before any live state is replaced.
func (d snapshotDocument) validate() error {
	bad := func(detail string) error {
		return fmt.Errorf("%w: %s", ErrBadSnapshot, detail)
	}

	if d.Version != snapshotFormatVersion {
		return bad("unsupported snapshot version")
	}

	if len(d.Dirs) != 2 {
		return bad("snapshot must contain both directions")
	}

	seen := make(map[Direction]bool, 2)
	for _, item := range d.Dirs {
		if !validDirection(item.Dir) {
			return bad("invalid direction in snapshot")
		}
		if seen[item.Dir] {
			return bad("duplicate direction in snapshot")
		}
		seen[item.Dir] = true

		if item.Epoch < epochInitial {
			return bad("epoch below the initial value")
		}
		if item.PendingResponse && item.ResponseEpoch < epochInitial {
			return bad("pending response without a response epoch")
		}
	}

	if d.Stats.Records < 0 ||
		d.Stats.ToleratedRecords < 0 ||
		d.Stats.ReplayedRecords < 0 ||
		d.Stats.GapRejected < 0 ||
		d.Stats.TooOldRejected < 0 ||
		d.Stats.PeerUpdates < 0 ||
		d.Stats.LocalUpdates < 0 ||
		d.Stats.CoalescedRequests < 0 ||
		d.Stats.ResponsesWritten < 0 ||
		d.Stats.UpdatesWritten < 0 ||
		d.Stats.EpochRollovers < 0 {
		return bad("negative counter in snapshot")
	}

	return nil
}
