// Package task bridges two links with different flow control: one side hands
// out credit windows, the other expects stop-and-wait acknowledgements, and
// both directions share one send buffer pool.
package task

import (
	"encoding/json"
	"errors"
	"sync"
)

// snapshotVersion is the only snapshot layout this build understands.
const snapshotVersion = 1

// Errors reported by the bridge.
var (
	ErrBadDir            = errors.New("direction is unknown")
	ErrBadLimits         = errors.New("limits are invalid")
	ErrBadLink           = errors.New("link is missing")
	ErrNotAttached       = errors.New("bridge is not attached")
	ErrAlreadyAttached   = errors.New("bridge is already attached")
	ErrBadCredit         = errors.New("credit grant is invalid")
	ErrCreditOverflow    = errors.New("credit grant exceeds the ceiling")
	ErrNoCredit          = errors.New("no credit is available")
	ErrAwaitingAck       = errors.New("previous data is still unacknowledged")
	ErrPoolExhausted     = errors.New("shared buffer pool is exhausted")
	ErrBadData           = errors.New("data amount is invalid")
	ErrNoPending         = errors.New("direction has no outstanding data")
	ErrBadAck            = errors.New("acknowledgement does not match the outstanding sequence")
	ErrAlreadyHalfClosed = errors.New("direction is already half closed")
	ErrHalfClosed        = errors.New("direction is half closed")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// Direction selects one leg of the bidirectional bridge.
type Direction int

// The two legs of the bridge.
const (
	DirUp Direction = iota
	DirDown
)

func (d Direction) valid() bool {
	return d == DirUp || d == DirDown
}

func (d Direction) other() Direction {
	if d == DirUp {
		return DirDown
	}
	return DirUp
}

// Link is one downstream carrier with pause and resume signalling.
type Link interface {
	Pause() error
	Resume() error
}

// Limits caps a bridge.
type Limits struct {
	PoolBytes uint64
	MaxCredit uint64
}

// DefaultLimits returns the operating limits of a bridge.
func DefaultLimits() Limits {
	return Limits{
		PoolBytes: 1 << 20,
		MaxCredit: 1 << 20,
	}
}

// CreditBridge owns both directions and the shared buffer pool.
type CreditBridge struct {
	mu       sync.Mutex
	cond     *sync.Cond
	lim      Limits
	up       Link
	down     Link
	attached bool
	credit   [2]uint64
	inflight [2]uint64
	seq      [2]uint64
	awaiting [2]bool
	outBytes [2]uint64
	closed   [2]bool
	paused   bool
}

// NewCreditBridge builds a detached bridge.
func NewCreditBridge(lim Limits) *CreditBridge {
	b := &CreditBridge{lim: lim}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Attach connects the credit side and the stop-and-wait side.
func (b *CreditBridge) Attach(up, down Link) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if up == nil || down == nil {
		return ErrBadLink
	}
	if b.attached {
		return ErrAlreadyAttached
	}
	if b.lim.PoolBytes == 0 || b.lim.MaxCredit == 0 {
		return ErrBadLimits
	}
	b.up = up
	b.down = down
	b.attached = true
	b.refreshPauseLocked()
	return nil
}

// Grant adds credit for one direction.
func (b *CreditBridge) Grant(dir Direction, bytes uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached {
		return ErrNotAttached
	}
	if !dir.valid() {
		return ErrBadDir
	}
	if bytes == 0 {
		return ErrBadCredit
	}
	if b.closed[dir] {
		return ErrHalfClosed
	}
	if bytes > b.lim.MaxCredit || b.credit[dir] > b.lim.MaxCredit-bytes {
		return ErrCreditOverflow
	}
	b.credit[dir] += bytes
	b.refreshPauseLocked()
	return nil
}

// Consume releases data of one direction into the shared pool and the
// downstream stop-and-wait link.
func (b *CreditBridge) Consume(dir Direction, bytes uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached {
		return ErrNotAttached
	}
	if !dir.valid() {
		return ErrBadDir
	}
	if bytes == 0 {
		return ErrBadData
	}
	if b.closed[dir] {
		return ErrHalfClosed
	}
	if b.awaiting[dir] {
		return ErrAwaitingAck
	}
	if bytes > b.credit[dir] {
		return ErrNoCredit
	}
	totalInflight := b.inflight[DirUp] + b.inflight[DirDown]
	if bytes > b.lim.PoolBytes-totalInflight {
		return ErrPoolExhausted
	}
	if bytes > b.dirQuota(dir)+b.borrowable(dir) {
		return ErrPoolExhausted
	}
	b.credit[dir] -= bytes
	b.inflight[dir] += bytes
	b.outBytes[dir] = b.inflight[dir]
	b.seq[dir]++
	b.awaiting[dir] = true
	b.refreshPauseLocked()
	return nil
}

// Ack confirms the outstanding stop-and-wait sequence of one direction.
func (b *CreditBridge) Ack(dir Direction, seq uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached {
		return ErrNotAttached
	}
	if !dir.valid() {
		return ErrBadDir
	}
	if !b.awaiting[dir] {
		return ErrNoPending
	}
	if seq != b.seq[dir] {
		return ErrBadAck
	}
	b.inflight[dir] = 0
	b.outBytes[dir] = 0
	b.awaiting[dir] = false
	b.cond.Broadcast()
	return nil
}

// HalfClose stops new data in one direction while the other keeps flowing.
func (b *CreditBridge) HalfClose(dir Direction) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached {
		return ErrNotAttached
	}
	if !dir.valid() {
		return ErrBadDir
	}
	if b.closed[dir] {
		return ErrAlreadyHalfClosed
	}
	b.closed[dir] = true
	b.refreshPauseLocked()
	return nil
}

// Drain blocks until both directions have no unacknowledged bytes.
func (b *CreditBridge) Drain() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached {
		return ErrNotAttached
	}
	for b.awaiting[DirUp] || b.awaiting[DirDown] {
		b.cond.Wait()
	}
	return nil
}

// Credit reports the remaining credit of one direction.
func (b *CreditBridge) Credit(dir Direction) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !dir.valid() {
		return 0
	}
	return b.credit[dir]
}

// Inflight reports the unacknowledged bytes of one direction.
func (b *CreditBridge) Inflight(dir Direction) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !dir.valid() {
		return 0
	}
	return b.inflight[dir]
}

// Seq reports the last sequence handed to one direction.
func (b *CreditBridge) Seq(dir Direction) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !dir.valid() {
		return 0
	}
	return b.seq[dir]
}

// Awaiting reports whether one direction waits for an acknowledgement.
func (b *CreditBridge) Awaiting(dir Direction) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !dir.valid() {
		return false
	}
	return b.awaiting[dir]
}

// HalfClosed reports whether one direction was half closed.
func (b *CreditBridge) HalfClosed(dir Direction) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !dir.valid() {
		return false
	}
	return b.closed[dir]
}

// PoolFree reports the bytes still free in the shared pool.
func (b *CreditBridge) PoolFree() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lim.PoolBytes - b.inflight[DirUp] - b.inflight[DirDown]
}

// Paused reports whether the downstream link is paused.
func (b *CreditBridge) Paused() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.paused
}

// snapshotPayload is the serialised layout of a bridge.
type snapshotPayload struct {
	Version  int       `json:"version"`
	Credit   [2]uint64 `json:"credit"`
	Inflight [2]uint64 `json:"inflight"`
	Seq      [2]uint64 `json:"seq"`
	Awaiting [2]bool   `json:"awaiting"`
	OutBytes [2]uint64 `json:"out_bytes"`
	Closed   [2]bool   `json:"closed"`
}

// Snapshot serialises both directions and the shared pool occupancy.
func (b *CreditBridge) Snapshot() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.attached {
		return nil, ErrNotAttached
	}
	payload := snapshotPayload{
		Version:  snapshotVersion,
		Credit:   b.credit,
		Inflight: b.inflight,
		Seq:      b.seq,
		Awaiting: b.awaiting,
		OutBytes: b.outBytes,
		Closed:   b.closed,
	}
	raw, err := json.Marshal(&payload)
	if err != nil {
		return nil, ErrBadSnapshot
	}
	return raw, nil
}

// Restore rebuilds the bridge from a snapshot, either completely or not at
// all.
func (b *CreditBridge) Restore(payload []byte) error {
	if len(payload) == 0 {
		return ErrBadSnapshot
	}
	var parsed snapshotPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return ErrBadSnapshot
	}
	if parsed.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if b.lim.PoolBytes == 0 || b.lim.MaxCredit == 0 {
		return ErrBadLimits
	}
	var totalInflight uint64
	for dir := DirUp; dir <= DirDown; dir++ {
		if parsed.Credit[dir] > b.lim.MaxCredit {
			return ErrBadSnapshot
		}
		if parsed.Inflight[dir] > 0 && (!parsed.Awaiting[dir] || parsed.Seq[dir] == 0) {
			return ErrBadSnapshot
		}
		if parsed.Awaiting[dir] && parsed.Seq[dir] == 0 {
			return ErrBadSnapshot
		}
		if parsed.OutBytes[dir] != parsed.Inflight[dir] {
			return ErrBadSnapshot
		}
		totalInflight += parsed.Inflight[dir]
	}
	if totalInflight > b.lim.PoolBytes {
		return ErrBadSnapshot
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.credit = parsed.Credit
	b.inflight = parsed.Inflight
	b.seq = parsed.Seq
	b.awaiting = parsed.Awaiting
	b.outBytes = parsed.OutBytes
	b.closed = parsed.Closed
	if b.attached {
		b.refreshPauseLocked()
	}
	b.cond.Broadcast()
	return nil
}

// dirQuota is the guaranteed fair share of the pool for one direction.
func (b *CreditBridge) dirQuota(dir Direction) uint64 {
	if dir == DirUp {
		return b.lim.PoolBytes - b.lim.PoolBytes/2
	}
	return b.lim.PoolBytes / 2
}

// borrowable is the unused slice of the other direction's quota that this
// direction may claim while the other side holds outstanding data.
func (b *CreditBridge) borrowable(dir Direction) uint64 {
	other := dir.other()
	quota := b.dirQuota(other)
	used := b.inflight[other]
	if used >= quota {
		return 0
	}
	return quota - used
}

// refreshPauseLocked drives the downstream link purely from the credit that
// is still spendable on the legs that remain open.
func (b *CreditBridge) refreshPauseLocked() {
	shouldPause := false
	for dir := DirUp; dir <= DirDown; dir++ {
		if b.closed[dir] {
			continue
		}
		if b.credit[dir] == 0 {
			shouldPause = true
		}
	}
	if shouldPause == b.paused || b.down == nil {
		return
	}
	if shouldPause {
		if err := b.down.Pause(); err != nil {
			return
		}
		b.paused = true
		return
	}
	if err := b.down.Resume(); err != nil {
		return
	}
	b.paused = false
}
