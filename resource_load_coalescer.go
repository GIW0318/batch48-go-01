// Package task coalesces concurrent resource load requests for the same key
// into one in-flight load and keeps results resident by reference count.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the load coalescer.
var (
	ErrEmptyKey      = errors.New("load key is empty after normalization")
	ErrBadPriority   = errors.New("priority is outside the supported range")
	ErrKeyLimit      = errors.New("key count reached the configured limit")
	ErrNoLoad        = errors.New("no load is in flight for this key")
	ErrUnknownTicket = errors.New("ticket is unknown or already consumed")
	ErrUnknownKey    = errors.New("key is not tracked")
	ErrNotReady      = errors.New("load result is not ready")
)

// State is the lifecycle state of one load key.
type State int

// Load states.
const (
	StateIdle State = iota
	StateLoading
	StateReady
	StateFailed
)

// Ticket identifies one request on one load key.
type Ticket struct {
	Key string
	Seq uint64
}

// Payload is the result of one load.
type Payload struct {
	Bytes  int
	Format string
}

// CoalescerStats summarises coalescer counters.
type CoalescerStats struct {
	Requests  int
	Started   int
	Shared    int
	Completed int
	Failed    int
	Cancelled int
	Evicted   int
	Resident  int
	InFlight  int
}

// LoadCoalescer merges requests that ask for the same key.
type LoadCoalescer struct {
	maxKeys int
}

// NewLoadCoalescer builds a coalescer with a hard key limit.
func NewLoadCoalescer(maxKeys int) *LoadCoalescer {
	return &LoadCoalescer{maxKeys: maxKeys}
}

// SetMaxKeys changes the hard key limit.
func (c *LoadCoalescer) SetMaxKeys(n int) error { return errNotImplemented }

// NormalizeKey maps spelling variants of one key onto a single form.
func (c *LoadCoalescer) NormalizeKey(key string) string { return "" }

// Request joins an in-flight load or starts one and returns a ticket.
func (c *LoadCoalescer) Request(key string, prio int) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// Complete publishes the result of the in-flight load for a key.
func (c *LoadCoalescer) Complete(key string, res Payload, err error) error {
	return errNotImplemented
}

// Cancel withdraws one requester from a key.
func (c *LoadCoalescer) Cancel(t Ticket) error { return errNotImplemented }

// Status reports the state of a key.
func (c *LoadCoalescer) Status(key string) (State, error) { return StateIdle, errNotImplemented }

// RefCount reports how many requesters a key currently has.
func (c *LoadCoalescer) RefCount(key string) int { return 0 }

// Priority reports the effective priority of a key.
func (c *LoadCoalescer) Priority(key string) int { return 0 }

// InFlight reports how many keys are loading.
func (c *LoadCoalescer) InFlight() int { return 0 }

// Resident reports how many keys hold a published result.
func (c *LoadCoalescer) Resident() int { return 0 }

// Result returns the payload of a ready key.
func (c *LoadCoalescer) Result(key string) (Payload, bool, error) {
	return Payload{}, false, errNotImplemented
}

// Stats returns a snapshot of the coalescer counters.
func (c *LoadCoalescer) Stats() CoalescerStats { return CoalescerStats{} }
