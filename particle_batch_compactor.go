// Package task compacts particle emissions into material sub-batches, recycles
// instance slots through a serialized ring and enforces a per-frame budget.
package task

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Errors reported by the particle batch compactor.
var (
	ErrBadCount       = errors.New("particle count must be positive")
	ErrBadBlend       = errors.New("unsupported blend mode")
	ErrBadBudget      = errors.New("budget values must not be negative")
	ErrBudgetTooSmall = errors.New("slot budget is below the live particle count")
	ErrSlotCapacity   = errors.New("request exceeds the instance slot capacity")
	ErrBadBatchLimit  = errors.New("batch limit must be positive")
	ErrBatchLimit     = errors.New("compacted batch count exceeds the configured limit")
	ErrUnknownID      = errors.New("particle id is unknown or already released")
	ErrQueueFull      = errors.New("emission queue is full")
)

// BlendMode selects the blend function of a particle sub-batch.
type BlendMode int

// Supported blend modes.
const (
	BlendAlpha BlendMode = iota
	BlendAdd
	BlendMultiply
)

// BatchKey identifies a sub-batch: material, blend mode and atlas page.
type BatchKey struct {
	Material  string
	Blend     BlendMode
	AtlasPage int
}

// EmitRequest asks for Count particles sharing Key on one frame.
type EmitRequest struct {
	Key      BatchKey
	Count    int
	Frame    int
	Priority int
	Lifetime int
}

// SlotRef is the stable identity of one live particle instance.
type SlotRef struct {
	ID     int
	Slot   int
	Serial uint64
}

// Batch is one compacted sub-batch in emission order.
type Batch struct {
	Key   BatchKey
	Count int
	IDs   []int
}

// Budget caps emission and slot usage.
type Budget struct {
	MaxPerFrame int
	MaxQueued   int
	MaxSlots    int
}

// CompactorStats summarises compactor counters.
type CompactorStats struct {
	Emitted  int
	Queued   int
	Dequeued int
	Rejected int
	Released int
	Reused   int
	Batches  int
}

type particle struct {
	id     int
	slot   int
	serial uint64
	key    BatchKey
}

type queuedEmission struct {
	request EmitRequest
	ids     []int
}

// ParticleBatchCompactor groups particles into sub-batches and owns the
// instance slot ring.
type ParticleBatchCompactor struct {
	mu           sync.Mutex
	slotCapacity int
	batchLimit   int
	budget       Budget

	freeSlots  []int
	serials    []uint64
	particles  map[int]*particle
	nextID     int
	queue      []queuedEmission
	queueDepth int
	frameUsed  map[int]int
	batchCount int
	stats      CompactorStats
}

// NewParticleBatchCompactor builds a compactor with slotCapacity instance slots.
func NewParticleBatchCompactor(slotCapacity int) *ParticleBatchCompactor {
	if slotCapacity < 0 {
		slotCapacity = 0
	}

	freeSlots := make([]int, slotCapacity)
	for i := range freeSlots {
		freeSlots[i] = slotCapacity - 1 - i
	}

	return &ParticleBatchCompactor{
		slotCapacity: slotCapacity,
		batchLimit:   64,
		budget: Budget{
			MaxPerFrame: slotCapacity,
			MaxQueued:   slotCapacity,
			MaxSlots:    slotCapacity,
		},
		freeSlots: freeSlots,
		serials:   make([]uint64, slotCapacity),
		particles: make(map[int]*particle),
		frameUsed: make(map[int]int),
	}
}

// SetBudget replaces the emission budget after validating it.
func (c *ParticleBatchCompactor) SetBudget(b Budget) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if b.MaxPerFrame < 0 || b.MaxQueued < 0 || b.MaxSlots < 0 {
		return fmt.Errorf("%w: %+v", ErrBadBudget, b)
	}
	if b.MaxSlots > c.slotCapacity {
		return fmt.Errorf("%w: max slots %d exceeds capacity %d", ErrSlotCapacity, b.MaxSlots, c.slotCapacity)
	}
	if b.MaxSlots < len(c.particles) {
		return fmt.Errorf("%w: max slots %d is below active particles %d", ErrBudgetTooSmall, b.MaxSlots, len(c.particles))
	}

	c.budget = b
	return nil
}

// Budget returns the budget currently in force.
func (c *ParticleBatchCompactor) Budget() Budget {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.budget
}

// Emit registers Count particles for one frame and returns their ids.
func (c *ParticleBatchCompactor) Emit(req EmitRequest) ([]int, error) {
	if req.Count <= 0 {
		return nil, ErrBadCount
	}
	if !validBlend(req.Key.Blend) {
		return nil, fmt.Errorf("%w: %d", ErrBadBlend, req.Key.Blend)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	immediate := min(req.Count, c.frameRemainingLocked(req.Frame), c.slotRemainingLocked())
	remaining := req.Count - immediate

	queued := 0
	if remaining > 0 {
		room := c.budget.MaxQueued - c.queueDepth
		room = max(0, room)
		queued = min(remaining, room)
	}
	rejected := remaining - queued
	accepted := immediate + queued
	reservedIDs := c.reserveIDsLocked(accepted)
	ids := append([]int(nil), reservedIDs[:immediate]...)

	if err := c.activateLocked(req.Key, reservedIDs[:immediate]); err != nil {
		return nil, err
	}
	c.frameUsed[req.Frame] += immediate

	if queued > 0 {
		queuedIDs := reservedIDs[immediate:accepted]
		queuedRequest := req
		queuedRequest.Count = queued
		c.queue = append(c.queue, queuedEmission{request: queuedRequest, ids: queuedIDs})
		c.queueDepth += queued
		c.stats.Queued += queued
	}
	if rejected > 0 {
		c.stats.Rejected += rejected
		return ids, fmt.Errorf("%w: rejected %d of %d requested particles", ErrQueueFull, rejected, req.Count)
	}

	return ids, nil
}

// Compact merges live particles into one sub-batch per batch key.
func (c *ParticleBatchCompactor) Compact() ([]Batch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	grouped := make(map[BatchKey][]int)
	for id, particle := range c.particles {
		grouped[particle.key] = append(grouped[particle.key], id)
	}

	keys := make([]BatchKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].AtlasPage != keys[j].AtlasPage {
			return keys[i].AtlasPage < keys[j].AtlasPage
		}
		if keys[i].Material != keys[j].Material {
			return keys[i].Material < keys[j].Material
		}
		return keys[i].Blend < keys[j].Blend
	})

	if len(keys) > c.batchLimit {
		return nil, fmt.Errorf("%w: %d batches exceed limit %d", ErrBatchLimit, len(keys), c.batchLimit)
	}

	batches := make([]Batch, 0, len(keys))
	for _, key := range keys {
		ids := grouped[key]
		sort.Ints(ids)
		batches = append(batches, Batch{
			Key:   key,
			Count: len(ids),
			IDs:   ids,
		})
	}

	c.batchCount = len(batches)
	c.stats.Batches = c.batchCount
	return batches, nil
}

// Release returns the slots of the given particle ids to the ring.
func (c *ParticleBatchCompactor) Release(ids []int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	toRelease := make([]*particle, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}

		particle, ok := c.particles[id]
		if !ok {
			return fmt.Errorf("%w: %d", ErrUnknownID, id)
		}
		toRelease = append(toRelease, particle)
	}

	for _, particle := range toRelease {
		delete(c.particles, particle.id)
		c.freeSlots = append(c.freeSlots, particle.slot)
	}
	c.stats.Released += len(toRelease)
	return nil
}

// SlotCount reports the number of free instance slots.
func (c *ParticleBatchCompactor) SlotCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.freeSlots)
}

// ActiveCount reports the number of live particles.
func (c *ParticleBatchCompactor) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.particles)
}

// QueueDepth reports how many particles are waiting for budget.
func (c *ParticleBatchCompactor) QueueDepth() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queueDepth
}

// Resolve maps a particle id back to its slot and serial.
func (c *ParticleBatchCompactor) Resolve(id int) (SlotRef, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	particle, ok := c.particles[id]
	if !ok || c.serials[particle.slot] != particle.serial {
		return SlotRef{}, false
	}
	return SlotRef{
		ID:     particle.id,
		Slot:   particle.slot,
		Serial: particle.serial,
	}, true
}

// SetBatchLimit changes the hard cap of compacted sub-batches.
func (c *ParticleBatchCompactor) SetBatchLimit(n int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if n <= 0 {
		return fmt.Errorf("%w: %d", ErrBadBatchLimit, n)
	}
	c.batchLimit = n
	return nil
}

// BatchCount reports how many sub-batches the last compaction produced.
func (c *ParticleBatchCompactor) BatchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.batchCount
}

// DrainQueue emits queued particles for frame using the remaining budget.
func (c *ParticleBatchCompactor) DrainQueue(frame int) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ids := make([]int, 0)
	for c.queueDepth > 0 {
		frameRemaining := c.frameRemainingLocked(frame)
		slotRemaining := c.slotRemainingLocked()
		if frameRemaining <= 0 || slotRemaining <= 0 {
			break
		}

		queued := c.queue[0]
		count := min(len(queued.ids), frameRemaining, slotRemaining)
		if count <= 0 {
			c.queue = c.queue[1:]
			continue
		}

		activatedIDs := queued.ids[:count]
		if err := c.activateLocked(queued.request.Key, activatedIDs); err != nil {
			return ids, err
		}

		ids = append(ids, activatedIDs...)
		c.frameUsed[frame] += count
		c.queueDepth -= count
		c.stats.Dequeued += count

		remainingIDs := queued.ids[count:]
		if len(remainingIDs) == 0 {
			c.queue[0] = queuedEmission{}
			c.queue = c.queue[1:]
		} else {
			c.queue[0].ids = remainingIDs
		}
	}

	return ids, nil
}

// Stats returns a snapshot of the compactor counters.
func (c *ParticleBatchCompactor) Stats() CompactorStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *ParticleBatchCompactor) reserveIDsLocked(count int) []int {
	ids := make([]int, count)
	for i := range ids {
		c.nextID++
		ids[i] = c.nextID
	}
	return ids
}

func (c *ParticleBatchCompactor) activateLocked(key BatchKey, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > len(c.freeSlots) || len(c.particles)+len(ids) > c.budget.MaxSlots {
		return fmt.Errorf("%w: %d particles need slots, %d free", ErrSlotCapacity, len(ids), len(c.freeSlots))
	}

	for _, id := range ids {
		last := len(c.freeSlots) - 1
		slot := c.freeSlots[last]
		c.freeSlots = c.freeSlots[:last]

		if c.serials[slot] > 0 {
			c.stats.Reused++
		}
		c.serials[slot]++

		serial := c.serials[slot]
		c.particles[id] = &particle{
			id:     id,
			slot:   slot,
			serial: serial,
			key:    key,
		}
		c.stats.Emitted++
	}
	return nil
}

func (c *ParticleBatchCompactor) frameRemainingLocked(frame int) int {
	remaining := c.budget.MaxPerFrame - c.frameUsed[frame]
	return max(0, remaining)
}

func (c *ParticleBatchCompactor) slotRemainingLocked() int {
	bySlots := len(c.freeSlots)
	byBudget := c.budget.MaxSlots - len(c.particles)
	return max(0, min(bySlots, byBudget))
}

func validBlend(blend BlendMode) bool {
	switch blend {
	case BlendAlpha, BlendAdd, BlendMultiply:
		return true
	default:
		return false
	}
}
