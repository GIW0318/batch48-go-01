// Package task coalesces concurrent resource load requests for the same key
// into one in-flight load and keeps results resident by reference count.
package task

import (
	"errors"
	"strings"
	"sync"
)

// Errors reported by the load coalescer.
var (
	ErrEmptyKey      = errors.New("load key is empty after normalization")
	ErrBadPriority   = errors.New("priority is outside the supported range")
	ErrBadLimit      = errors.New("key limit must be a positive number")
	ErrKeyLimit      = errors.New("key count reached the configured limit")
	ErrNoLoad        = errors.New("no load is in flight for this key")
	ErrUnknownTicket = errors.New("ticket is unknown or already consumed")
	ErrUnknownKey    = errors.New("key is not tracked")
	ErrNotReady      = errors.New("load result is not ready")
)

const (
	minPriority = 0
	maxPriority = 99
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

// loadEntry tracks one normalized key and the requesters currently holding it.
type loadEntry struct {
	state    State
	priority int
	payload  Payload
	loadErr  error
	// tickets holds the live, unconsumed tickets of the current generation,
	// mapped to the priority each requester asked for.
	// Tickets from a failed generation are dropped, so they cannot settle
	// the retry that replaces them.
	tickets map[uint64]int
}

// LoadCoalescer merges requests that ask for the same key.
type LoadCoalescer struct {
	mu      sync.Mutex
	maxKeys int
	entries map[string]*loadEntry
	nextSeq uint64

	requests  int
	started   int
	shared    int
	completed int
	failed    int
	cancelled int
	evicted   int
}

// NewLoadCoalescer builds a coalescer with a hard key limit.
func NewLoadCoalescer(maxKeys int) *LoadCoalescer {
	c := &LoadCoalescer{
		maxKeys: maxKeys,
		entries: make(map[string]*loadEntry),
	}
	if maxKeys < 1 {
		c.maxKeys = 1
	}
	return c
}

// SetMaxKeys changes the hard key limit.
func (c *LoadCoalescer) SetMaxKeys(n int) error {
	if n < 1 {
		return ErrBadLimit
	}
	c.mu.Lock()
	c.maxKeys = n
	c.mu.Unlock()
	return nil
}

// NormalizeKey maps spelling variants of one key onto a single form.
func (c *LoadCoalescer) NormalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// Request joins an in-flight load or starts one and returns a ticket.
func (c *LoadCoalescer) Request(key string, prio int) (Ticket, error) {
	normalized := c.NormalizeKey(key)
	if normalized == "" {
		return Ticket{}, ErrEmptyKey
	}
	if prio < minPriority || prio > maxPriority {
		return Ticket{}, ErrBadPriority
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests++

	entry, ok := c.entries[normalized]
	if !ok {
		if len(c.entries) >= c.maxKeys {
			return Ticket{}, ErrKeyLimit
		}
		entry = &loadEntry{
			state:    StateLoading,
			priority: prio,
			tickets:  make(map[uint64]int),
		}
		c.entries[normalized] = entry
	}

	c.nextSeq++
	seq := c.nextSeq
	ticket := Ticket{Key: normalized, Seq: seq}

	switch entry.state {
	case StateLoading:
		if len(entry.tickets) > 0 {
			// A load is already in flight: join it instead of starting another.
			c.shared++
			if prio > entry.priority {
				entry.priority = prio
			}
		} else {
			// Retry after a failure is a brand new generation and load.
			entry.state = StateLoading
			entry.priority = prio
			entry.payload = Payload{}
			entry.loadErr = nil
			c.started++
		}
	case StateReady:
		// The published result serves the new requester without a reload.
		c.shared++
		if prio > entry.priority {
			entry.priority = prio
		}
	case StateFailed:
		// A failed entry retains its key slot; a retry starts a fresh load
		// with the reference count reset to one.
		entry.state = StateLoading
		entry.priority = prio
		entry.payload = Payload{}
		entry.loadErr = nil
		c.started++
	}

	entry.tickets[seq] = prio
	return ticket, nil
}

// Complete publishes the result of the in-flight load for a key.
func (c *LoadCoalescer) Complete(key string, res Payload, err error) error {
	normalized := c.NormalizeKey(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[normalized]
	if !ok || entry.state != StateLoading {
		return ErrNoLoad
	}

	if err != nil {
		entry.state = StateFailed
		entry.loadErr = err
		entry.payload = Payload{}
		// Every outstanding ticket belonged to the failed generation; none of
		// them may settle the next attempt.
		entry.tickets = make(map[uint64]int)
		c.failed++
		return nil
	}

	entry.state = StateReady
	entry.payload = res
	entry.loadErr = nil
	c.completed++
	return nil
}

// Cancel withdraws one requester from a key.
func (c *LoadCoalescer) Cancel(t Ticket) error {
	normalized := c.NormalizeKey(t.Key)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[normalized]
	if !ok {
		return ErrUnknownTicket
	}
	if _, live := entry.tickets[t.Seq]; !live {
		return ErrUnknownTicket
	}

	delete(entry.tickets, t.Seq)
	c.cancelled++

	if len(entry.tickets) > 0 {
		if entry.state == StateLoading || entry.state == StateReady {
			entry.recomputePriority()
		}
		return nil
	}

	// No requester remains.
	if entry.state == StateReady {
		c.evicted++
	}
	delete(c.entries, normalized)
	return nil
}

// Status reports the state of a key.
func (c *LoadCoalescer) Status(key string) (State, error) {
	normalized := c.NormalizeKey(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[normalized]
	if !ok {
		return StateIdle, ErrUnknownKey
	}
	return entry.state, nil
}

// RefCount reports how many requesters a key currently has.
func (c *LoadCoalescer) RefCount(key string) int {
	normalized := c.NormalizeKey(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[normalized]; ok {
		return len(entry.tickets)
	}
	return 0
}

// Priority reports the effective priority of a key.
func (c *LoadCoalescer) Priority(key string) int {
	normalized := c.NormalizeKey(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[normalized]; ok {
		return entry.priority
	}
	return 0
}

// InFlight reports how many keys are loading.
func (c *LoadCoalescer) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	inFlight := 0
	for _, entry := range c.entries {
		if entry.state == StateLoading {
			inFlight++
		}
	}
	return inFlight
}

// Resident reports how many keys hold a published result.
func (c *LoadCoalescer) Resident() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	resident := 0
	for _, entry := range c.entries {
		if entry.state == StateReady {
			resident++
		}
	}
	return resident
}

// Result returns the payload of a ready key.
func (c *LoadCoalescer) Result(key string) (Payload, bool, error) {
	normalized := c.NormalizeKey(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[normalized]
	if !ok {
		return Payload{}, false, ErrUnknownKey
	}
	if entry.state != StateReady {
		return Payload{}, false, ErrNotReady
	}
	return entry.payload, true, nil
}

// recomputePriority rolls the effective priority back to the maximum priority
// of the requesters that still hold tickets.
func (e *loadEntry) recomputePriority() {
	highest := minPriority
	for _, prio := range e.tickets {
		if prio > highest {
			highest = prio
		}
	}
	e.priority = highest
}

// Stats returns a snapshot of the coalescer counters.
func (c *LoadCoalescer) Stats() CoalescerStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	inFlight, resident := 0, 0
	for _, entry := range c.entries {
		switch entry.state {
		case StateLoading:
			inFlight++
		case StateReady:
			resident++
		}
	}

	return CoalescerStats{
		Requests:  c.requests,
		Started:   c.started,
		Shared:    c.shared,
		Completed: c.completed,
		Failed:    c.failed,
		Cancelled: c.cancelled,
		Evicted:   c.evicted,
		Resident:  resident,
		InFlight:  inFlight,
	}
}
