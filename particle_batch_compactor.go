// Package task compacts particle emissions into material sub-batches, recycles
// instance slots through a serialized ring and enforces a per-frame budget.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the particle batch compactor.
var (
	ErrBadCount       = errors.New("particle count must be positive")
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

// ParticleBatchCompactor groups particles into sub-batches and owns the
// instance slot ring.
type ParticleBatchCompactor struct {
	slotCapacity int
	batchLimit   int
	budget       Budget
}

// NewParticleBatchCompactor builds a compactor with slotCapacity instance slots.
func NewParticleBatchCompactor(slotCapacity int) *ParticleBatchCompactor {
	return &ParticleBatchCompactor{
		slotCapacity: slotCapacity,
		batchLimit:   64,
		budget: Budget{
			MaxPerFrame: slotCapacity,
			MaxQueued:   slotCapacity,
			MaxSlots:    slotCapacity,
		},
	}
}

// SetBudget replaces the emission budget after validating it.
func (c *ParticleBatchCompactor) SetBudget(b Budget) error { return errNotImplemented }

// Budget returns the budget currently in force.
func (c *ParticleBatchCompactor) Budget() Budget { return Budget{} }

// Emit registers Count particles for one frame and returns their ids.
func (c *ParticleBatchCompactor) Emit(req EmitRequest) ([]int, error) {
	return nil, errNotImplemented
}

// Compact merges live particles into one sub-batch per batch key.
func (c *ParticleBatchCompactor) Compact() ([]Batch, error) { return nil, errNotImplemented }

// Release returns the slots of the given particle ids to the ring.
func (c *ParticleBatchCompactor) Release(ids []int) error { return errNotImplemented }

// SlotCount reports the number of free instance slots.
func (c *ParticleBatchCompactor) SlotCount() int { return 0 }

// ActiveCount reports the number of live particles.
func (c *ParticleBatchCompactor) ActiveCount() int { return 0 }

// QueueDepth reports how many particles are waiting for budget.
func (c *ParticleBatchCompactor) QueueDepth() int { return 0 }

// Resolve maps a particle id back to its slot and serial.
func (c *ParticleBatchCompactor) Resolve(id int) (SlotRef, bool) {
	return SlotRef{}, false
}

// SetBatchLimit changes the hard cap of compacted sub-batches.
func (c *ParticleBatchCompactor) SetBatchLimit(n int) error { return errNotImplemented }

// BatchCount reports how many sub-batches the last compaction produced.
func (c *ParticleBatchCompactor) BatchCount() int { return 0 }

// DrainQueue emits queued particles for frame using the remaining budget.
func (c *ParticleBatchCompactor) DrainQueue(frame int) ([]int, error) {
	return nil, errNotImplemented
}

// Stats returns a snapshot of the compactor counters.
func (c *ParticleBatchCompactor) Stats() CompactorStats { return CompactorStats{} }
