// Package task bridges two links with different flow control: one side hands
// out credit windows, the other expects stop-and-wait acknowledgements, and
// both directions share one send buffer pool.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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
	closed   uint8
	credit   [2]uint64
	inflight [2]uint64
	seq      [2]uint64
	awaiting [2]bool
	outBytes [2]uint64
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
	return errNotImplemented
}

// Grant adds credit for one direction.
func (b *CreditBridge) Grant(dir Direction, bytes uint64) error {
	return errNotImplemented
}

// Consume releases data of one direction into the shared pool and the
// downstream stop-and-wait link.
func (b *CreditBridge) Consume(dir Direction, bytes uint64) error {
	return errNotImplemented
}

// Ack confirms the outstanding stop-and-wait sequence of one direction.
func (b *CreditBridge) Ack(dir Direction, seq uint64) error {
	return errNotImplemented
}

// HalfClose stops new data in one direction while the other keeps flowing.
func (b *CreditBridge) HalfClose(dir Direction) error {
	return errNotImplemented
}

// Drain blocks until both directions have no unacknowledged bytes.
func (b *CreditBridge) Drain() error {
	return errNotImplemented
}

// Credit reports the remaining credit of one direction.
func (b *CreditBridge) Credit(dir Direction) uint64 {
	return 0
}

// Inflight reports the unacknowledged bytes of one direction.
func (b *CreditBridge) Inflight(dir Direction) uint64 {
	return 0
}

// Seq reports the last sequence handed to one direction.
func (b *CreditBridge) Seq(dir Direction) uint64 {
	return 0
}

// Awaiting reports whether one direction waits for an acknowledgement.
func (b *CreditBridge) Awaiting(dir Direction) bool {
	return false
}

// HalfClosed reports whether one direction was half closed.
func (b *CreditBridge) HalfClosed(dir Direction) bool {
	return false
}

// PoolFree reports the bytes still free in the shared pool.
func (b *CreditBridge) PoolFree() uint64 {
	return 0
}

// Paused reports whether the downstream link is paused.
func (b *CreditBridge) Paused() bool {
	return false
}

// Snapshot serialises both directions and the shared pool occupancy.
func (b *CreditBridge) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the bridge from a snapshot, either completely or not at
// all.
func (b *CreditBridge) Restore(payload []byte) error {
	return errNotImplemented
}
