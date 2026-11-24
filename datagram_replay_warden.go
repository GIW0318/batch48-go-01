// Package task keeps the receive side of an encrypted datagram channel
// replay-safe: every security epoch owns a 64 bit sliding window, retired
// epochs keep a grace window, and a far-ahead sequence triggers a bounded
// resynchronisation handshake.
package task

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// WindowBits is the width of the sliding bitmap.
const WindowBits = 64

// snapshotVersion is the only snapshot layout this build understands.
const snapshotVersion = 1

// Errors reported by the warden.
var (
	ErrBadLimits    = errors.New("limits are invalid")
	ErrFutureEpoch  = errors.New("epoch is ahead of the current one")
	ErrBadEpoch     = errors.New("epoch does not advance")
	ErrBadResync    = errors.New("resynchronisation request is invalid")
	ErrBadSession   = errors.New("session id is blank")
	ErrNotResyncing = errors.New("warden is not resynchronising")
	ErrBadSnapshot  = errors.New("snapshot payload is invalid")
)

// Verdict is the judgement for one received datagram.
type Verdict int

// Judgements reported by Accept.
const (
	VerdictReject Verdict = iota
	VerdictAccept
	VerdictDuplicate
	VerdictTooOld
	VerdictEpochExpired
	VerdictStashed
	VerdictStashDropped
)

// Limits caps a warden.
type Limits struct {
	MaxStash       int
	MaxSkip        uint64
	EpochTolerance uint32
	EpochGrace     time.Duration
}

// DefaultLimits returns the operating limits of a receive warden.
func DefaultLimits() Limits {
	return Limits{
		MaxStash:       16,
		MaxSkip:        512,
		EpochTolerance: 2,
		EpochGrace:     30 * time.Second,
	}
}

// normalizeLimits fills unset limits with the operating defaults so a zero
// value never silently means "unbounded" or "zero sized".
func normalizeLimits(lim Limits) Limits {
	def := DefaultLimits()
	if lim.MaxStash <= 0 {
		lim.MaxStash = def.MaxStash
	}
	if lim.MaxSkip == 0 {
		lim.MaxSkip = def.MaxSkip
	}
	if lim.EpochTolerance == 0 {
		lim.EpochTolerance = def.EpochTolerance
	}
	if lim.EpochGrace <= 0 {
		lim.EpochGrace = def.EpochGrace
	}
	return lim
}

type epochWindow struct {
	epoch    uint32
	highest  uint64
	mask     uint64
	seen     bool
	switched time.Time
}

type stashEntry struct {
	epoch uint32
	seq   uint64
	at    time.Time
}

// ReplayWarden owns the windows of every live epoch.
type ReplayWarden struct {
	mu           sync.Mutex
	lim          Limits
	current      uint32
	switchedAt   time.Time
	windows      map[uint32]*epochWindow
	resyncing    bool
	resyncFrom   uint64
	sessionID    string
	stash        []stashEntry
	accepted     uint64
	duplicates   uint64
	dropped      uint64
	stashedTotal uint64
}

// NewReplayWarden builds a warden for the epoch zero.
func NewReplayWarden(lim Limits) *ReplayWarden {
	return &ReplayWarden{
		lim:     normalizeLimits(lim),
		windows: make(map[uint32]*epochWindow),
	}
}

// Accept judges one datagram of the given epoch at the given receive time.
//
// The whole judgement, including bitmap/highest updates and counter
// increments, runs under the warden lock so concurrent receives can never
// accept the same sequence twice.
func (w *ReplayWarden) Accept(epoch uint32, seq uint64, now time.Time) (Verdict, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if epoch > w.current {
		return VerdictReject, fmt.Errorf("%w: epoch %d is ahead of current epoch %d",
			ErrFutureEpoch, epoch, w.current)
	}

	if epoch < w.current {
		win := w.windows[epoch]
		if w.epochExpiredLocked(epoch, win, now) {
			return VerdictEpochExpired, nil
		}
	}

	// While a resynchronisation handshake is open every eligible datagram is
	// parked in the bounded stash and re-judged once the new base is applied.
	if w.resyncing {
		return w.stashLocked(stashEntry{epoch: epoch, seq: seq, at: now}), nil
	}

	return w.judgeLocked(epoch, seq, true), nil
}

// epochExpiredLocked reports whether an old epoch is no longer served either
// because it falls outside the tolerated generation count or because its
// switch grace period elapsed. Callers must hold w.mu.
func (w *ReplayWarden) epochExpiredLocked(epoch uint32, win *epochWindow, now time.Time) bool {
	if w.current-epoch > w.lim.EpochTolerance {
		return true
	}

	switched := w.switchedAt
	if win != nil && !win.switched.IsZero() {
		switched = win.switched
	}
	if switched.IsZero() || w.lim.EpochGrace <= 0 {
		return false
	}
	return now.Sub(switched) >= w.lim.EpochGrace
}

// stashLocked appends one packet while the handshake is open. The stash has a
// hard capacity: overflow packets are counted as dropped and never expand it.
// Callers must hold w.mu.
func (w *ReplayWarden) stashLocked(entry stashEntry) Verdict {
	if len(w.stash) >= w.lim.MaxStash {
		w.dropped++
		return VerdictStashDropped
	}
	w.stash = append(w.stash, entry)
	w.stashedTotal++
	return VerdictStashed
}

// judgeLocked applies the 64 bit sliding replay window to one sequence. When
// allowResync is true a jump larger than MaxSkip opens a resynchronisation
// handshake and parks the packet instead of yanking the window forward.
// Callers must hold w.mu.
func (w *ReplayWarden) judgeLocked(epoch uint32, seq uint64, allowResync bool) Verdict {
	win := w.windows[epoch]
	if win == nil || !win.seen {
		if win == nil {
			win = &epochWindow{epoch: epoch}
			w.windows[epoch] = win
		}
		// An explicit established flag is required: sequence zero is a
		// perfectly valid first sequence and must not read as "no window".
		win.seen = true
		win.highest = seq
		win.mask = 1 << 0
		w.accepted++
		return VerdictAccept
	}

	if seq > win.highest {
		jump := seq - win.highest
		if allowResync && jump > w.lim.MaxSkip {
			w.resyncing = true
			if win.highest < ^uint64(0) {
				w.resyncFrom = win.highest + 1
			} else {
				w.resyncFrom = win.highest
			}
			return w.stashLocked(stashEntry{epoch: epoch, seq: seq})
		}

		// Move highest forward and shift the bitmap in the same critical
		// section. A jump of WindowBits or more shifts every old bit out.
		if jump >= WindowBits {
			win.mask = 1
		} else {
			win.mask = (win.mask << jump) | 1
		}
		win.highest = seq
		w.accepted++
		return VerdictAccept
	}

	behind := win.highest - seq
	if behind >= WindowBits {
		return VerdictTooOld
	}
	bit := uint64(1) << behind
	if win.mask&bit != 0 {
		w.duplicates++
		return VerdictDuplicate
	}
	win.mask |= bit
	w.accepted++
	return VerdictAccept
}

// AdvanceEpoch retires the current epoch and selects a strictly newer one.
func (w *ReplayWarden) AdvanceEpoch(newEpoch uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if newEpoch <= w.current {
		return fmt.Errorf("%w: new epoch %d is not greater than current epoch %d",
			ErrBadEpoch, newEpoch, w.current)
	}

	switched := time.Now()
	if old := w.windows[w.current]; old != nil && old.switched.IsZero() {
		old.switched = switched
	}
	w.switchedAt = switched
	w.current = newEpoch

	// Generations beyond the tolerated count are dropped eagerly; the grace
	// period itself is enforced lazily against the receive timestamp.
	for epoch := range w.windows {
		if newEpoch-epoch > w.lim.EpochTolerance {
			delete(w.windows, epoch)
		}
	}
	return nil
}

// RequestResync asks the sender to restart at the given sequence. A repeated
// request while a handshake is already open is an idempotent no-op.
func (w *ReplayWarden) RequestResync(fromSeq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if fromSeq == 0 {
		return fmt.Errorf("%w: from sequence must not be zero", ErrBadResync)
	}
	if w.resyncing {
		return nil
	}
	w.resyncing = true
	w.resyncFrom = fromSeq
	return nil
}

// ApplyResync installs the agreed base sequence and re-judges every stashed
// datagram against the new window. Validation happens before any state move.
func (w *ReplayWarden) ApplyResync(sessionID string, base uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.resyncing {
		return ErrNotResyncing
	}
	if strings.TrimSpace(sessionID) == "" {
		return ErrBadSession
	}
	if base == 0 {
		return fmt.Errorf("%w: base sequence must not be zero", ErrBadResync)
	}
	win := w.windows[w.current]
	if win != nil && win.seen && base < win.highest {
		return fmt.Errorf("%w: base %d is behind highest sequence %d",
			ErrBadResync, base, win.highest)
	}

	if win == nil {
		win = &epochWindow{epoch: w.current}
		w.windows[w.current] = win
	}
	win.seen = true
	win.highest = base
	win.mask = 1

	pending := w.stash
	w.stash = nil
	w.resyncing = false
	w.resyncFrom = 0
	w.sessionID = sessionID

	for _, entry := range pending {
		w.revalidateStashedLocked(entry)
	}
	return nil
}

// revalidateStashedLocked re-judges one parked packet after a resync base was
// installed. Packets that fit the new window are admitted, repeats count as
// duplicates and packets that can no longer be validated count as drops.
// Callers must hold w.mu.
func (w *ReplayWarden) revalidateStashedLocked(entry stashEntry) {
	if entry.epoch > w.current {
		w.dropped++
		return
	}
	if entry.epoch < w.current {
		win := w.windows[entry.epoch]
		if w.epochExpiredLocked(entry.epoch, win, entry.at) {
			w.dropped++
			return
		}
	}

	win := w.windows[entry.epoch]
	if win == nil || !win.seen {
		// No prior window for this epoch: admit against its own sequence.
		w.judgeLocked(entry.epoch, entry.seq, false)
		return
	}

	switch {
	case entry.seq > win.highest && entry.seq-win.highest > w.lim.MaxSkip:
		// Still too far ahead of the agreed window: cannot validate, drop.
		w.dropped++
	case entry.seq <= win.highest && win.highest-entry.seq >= WindowBits:
		// Aged out of the new 64 bit window while parked.
		w.dropped++
	default:
		w.judgeLocked(entry.epoch, entry.seq, false)
	}
}

// CurrentEpoch reports the epoch currently selected.
func (w *ReplayWarden) CurrentEpoch() uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current
}

// Highest reports the highest accepted sequence of one epoch.
func (w *ReplayWarden) Highest(epoch uint32) (uint64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	win := w.windows[epoch]
	if win == nil || !win.seen {
		return 0, false
	}
	return win.highest, true
}

// WindowSeen reports whether one sequence already sits in the window.
func (w *ReplayWarden) WindowSeen(epoch uint32, seq uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	win := w.windows[epoch]
	if win == nil || !win.seen {
		return false
	}
	if seq > win.highest {
		return false
	}
	behind := win.highest - seq
	if behind >= WindowBits {
		return false
	}
	return win.mask&(uint64(1)<<behind) != 0
}

// Resyncing reports whether the handshake is in progress.
func (w *ReplayWarden) Resyncing() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.resyncing
}

// Stashed reports how many packets the handshake is holding.
func (w *ReplayWarden) Stashed() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.stash)
}

// Accepted reports how many packets entered a window.
func (w *ReplayWarden) Accepted() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.accepted
}

// Duplicates reports how many in-window repeats were rejected.
func (w *ReplayWarden) Duplicates() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.duplicates
}

// Dropped reports how many packets were discarded instead of stashing or
// during revalidation after a resynchronisation.
func (w *ReplayWarden) Dropped() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}

// snapshotWindow is the serialised form of one epoch bitmap.
type snapshotWindow struct {
	Epoch          uint32 `json:"epoch"`
	Highest        uint64 `json:"highest"`
	Mask           uint64 `json:"mask"`
	SwitchedUnixNS int64  `json:"switched_unix_ns,omitempty"`
}

// snapshotStash is one parked datagram.
type snapshotStash struct {
	Epoch uint32 `json:"epoch"`
	Seq   uint64 `json:"seq"`
	AtNS  int64  `json:"at_unix_ns,omitempty"`
}

// snapshotState carries the complete warden state, including the bitmap.
type snapshotState struct {
	Version      int              `json:"version"`
	Current      uint32           `json:"current"`
	SwitchedAtNS int64            `json:"switched_at_unix_ns"`
	Resyncing    bool             `json:"resyncing"`
	ResyncFrom   uint64           `json:"resync_from,omitempty"`
	SessionID    string           `json:"session_id,omitempty"`
	Windows      []snapshotWindow `json:"windows"`
	Stash        []snapshotStash  `json:"stash,omitempty"`
	Accepted     uint64           `json:"accepted"`
	Duplicates   uint64           `json:"duplicates"`
	Dropped      uint64           `json:"dropped"`
	StashedTotal uint64           `json:"stashed_total,omitempty"`
}

// snapshotEnvelope protects the state bytes with a checksum so a truncated or
// tampered payload can never be half-restored.
type snapshotEnvelope struct {
	Version  int             `json:"version"`
	State    json.RawMessage `json:"state"`
	Checksum string          `json:"checksum"`
}

func timeToUnixNS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func timeFromUnixNS(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// Snapshot serialises both the bitmap and the highest sequence of every window.
func (w *ReplayWarden) Snapshot() ([]byte, error) {
	w.mu.Lock()
	st := snapshotState{
		Version:      snapshotVersion,
		Current:      w.current,
		SwitchedAtNS: timeToUnixNS(w.switchedAt),
		Resyncing:    w.resyncing,
		ResyncFrom:   w.resyncFrom,
		SessionID:    w.sessionID,
		Windows:      make([]snapshotWindow, 0, len(w.windows)),
		Stash:        make([]snapshotStash, 0, len(w.stash)),
		Accepted:     w.accepted,
		Duplicates:   w.duplicates,
		Dropped:      w.dropped,
		StashedTotal: w.stashedTotal,
	}
	epochs := make([]uint32, 0, len(w.windows))
	for epoch := range w.windows {
		epochs = append(epochs, epoch)
	}
	sort.Slice(epochs, func(i, j int) bool { return epochs[i] < epochs[j] })
	for _, epoch := range epochs {
		win := w.windows[epoch]
		if win == nil || !win.seen {
			continue
		}
		st.Windows = append(st.Windows, snapshotWindow{
			Epoch:          win.epoch,
			Highest:        win.highest,
			Mask:           win.mask,
			SwitchedUnixNS: timeToUnixNS(win.switched),
		})
	}
	for _, entry := range w.stash {
		st.Stash = append(st.Stash, snapshotStash{
			Epoch: entry.epoch,
			Seq:   entry.seq,
			AtNS:  timeToUnixNS(entry.at),
		})
	}
	w.mu.Unlock()

	stateBytes, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot state: %w", err)
	}
	sum := sha256.Sum256(stateBytes)
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		State:    stateBytes,
		Checksum: hex.EncodeToString(sum[:]),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot envelope: %w", err)
	}
	return payload, nil
}

// Restore rebuilds the warden from a snapshot, either completely or not at
// all. The payload is parsed, checksummed and validated before any live state
// is touched.
func (w *ReplayWarden) Restore(payload []byte) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var envelope snapshotEnvelope
	if err := dec.Decode(&envelope); err != nil {
		return fmt.Errorf("%w: decode envelope: %v", ErrBadSnapshot, err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing data after snapshot", ErrBadSnapshot)
	}
	if envelope.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, envelope.Version)
	}
	if len(envelope.State) == 0 {
		return fmt.Errorf("%w: missing state", ErrBadSnapshot)
	}
	sum := sha256.Sum256(envelope.State)
	wantChecksum := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(wantChecksum), []byte(envelope.Checksum)) != 1 {
		return fmt.Errorf("%w: checksum mismatch", ErrBadSnapshot)
	}

	var st snapshotState
	if err := json.Unmarshal(envelope.State, &st); err != nil {
		return fmt.Errorf("%w: decode state: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(st); err != nil {
		return err
	}

	windows := make(map[uint32]*epochWindow, len(st.Windows))
	for _, sw := range st.Windows {
		windows[sw.Epoch] = &epochWindow{
			epoch:    sw.Epoch,
			highest:  sw.Highest,
			mask:     sw.Mask,
			seen:     true,
			switched: timeFromUnixNS(sw.SwitchedUnixNS),
		}
	}
	stash := make([]stashEntry, 0, len(st.Stash))
	if st.Resyncing {
		for _, ss := range st.Stash {
			stash = append(stash, stashEntry{
				epoch: ss.Epoch,
				seq:   ss.Seq,
				at:    timeFromUnixNS(ss.AtNS),
			})
		}
	}

	w.mu.Lock()
	w.current = st.Current
	w.switchedAt = timeFromUnixNS(st.SwitchedAtNS)
	w.windows = windows
	w.resyncing = st.Resyncing
	w.resyncFrom = st.ResyncFrom
	w.sessionID = st.SessionID
	w.stash = stash
	w.accepted = st.Accepted
	w.duplicates = st.Duplicates
	w.dropped = st.Dropped
	w.stashedTotal = st.StashedTotal
	w.mu.Unlock()
	return nil
}

// validateSnapshot checks every invariant that a serialised state must hold.
func validateSnapshot(st snapshotState) error {
	if st.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported state version %d", ErrBadSnapshot, st.Version)
	}
	seen := make(map[uint32]struct{}, len(st.Windows))
	for _, sw := range st.Windows {
		if _, dup := seen[sw.Epoch]; dup {
			return fmt.Errorf("%w: duplicate epoch window %d", ErrBadSnapshot, sw.Epoch)
		}
		seen[sw.Epoch] = struct{}{}
		if sw.Epoch > st.Current {
			return fmt.Errorf("%w: window epoch %d is ahead of current %d",
				ErrBadSnapshot, sw.Epoch, st.Current)
		}
		// An established window always has its highest sequence marked in
		// bit zero; a missing bit means bitmap and highest disagree.
		if sw.Mask&1 == 0 {
			return fmt.Errorf("%w: window %d bitmap does not mark highest",
				ErrBadSnapshot, sw.Epoch)
		}
	}
	return nil
}
