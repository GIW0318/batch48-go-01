// Package task decides which entities fit into one snapshot packet for a
// client: the per tick budget truncates the dirty set by priority, skipped
// entities accumulate priority and destroys always leave in the same packet.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Wire sizes charged against the per tick budget.
const (
	PacketHeaderBytes  = 16
	EntityRecordBytes  = 24
	DestroyRecordBytes = 8
)

// StarvationBoost is added to the effective priority for every build that
// skipped an entity.
const StarvationBoost = 10

// MinSendFrequency is the number of ticks within which the own entity and the
// allied entities must be sent at least once.
const MinSendFrequency = 3

// Errors reported by the send governor.
var (
	ErrInvalidClient = errors.New("client id is required")
	ErrUnknownClient = errors.New("client has no send budget")
	ErrInvalidBudget = errors.New("budget must be positive")
	ErrInvalidEntity = errors.New("entity id must be greater than zero")
	ErrInvalidTick   = errors.New("tick must be greater than zero")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

// EntityID identifies one networked entity.
type EntityID uint32

// PacketPlan is the outcome of one Build call.
type PacketPlan struct {
	Tick       uint64     `json:"tick"`
	Full       bool       `json:"full"`
	Baseline   uint64     `json:"baseline"`
	EntityIDs  []EntityID `json:"entity_ids"`
	DestroyIDs []EntityID `json:"destroy_ids"`
	Skipped    []EntityID `json:"skipped"`
	Bytes      int        `json:"bytes"`
}

// SnapshotGovernor owns the per client budgets and priority accumulators.
type SnapshotGovernor struct {
	mu       sync.Mutex
	tickRate int
	clients  map[string]*clientState
}

// clientState is the per client bookkeeping of the governor.
type clientState struct {
	budget       int
	dirty        map[EntityID]int
	skips        map[EntityID]int
	self         EntityID
	allies       map[EntityID]bool
	leaving      map[EntityID]bool
	ackedTick    uint64
	lastPlan     map[EntityID]bool
	lastPlanTick uint64
}

// NewSnapshotGovernor builds a governor for the given server tick rate.
func NewSnapshotGovernor(tickRate int) *SnapshotGovernor {
	if tickRate <= 0 {
		tickRate = 1
	}
	return &SnapshotGovernor{tickRate: tickRate, clients: map[string]*clientState{}}
}

// SetBudget installs the per second byte budget of a client.
func (g *SnapshotGovernor) SetBudget(clientID string, bps int) error {
	return errNotImplemented
}

// Budget reports the per second byte budget of a client.
func (g *SnapshotGovernor) Budget(clientID string) int {
	return 0
}

// MarkDirty marks an entity as changed for a client.
func (g *SnapshotGovernor) MarkDirty(clientID string, entity EntityID, priority int) error {
	return errNotImplemented
}

// MarkSelf marks the own entity of a client.
func (g *SnapshotGovernor) MarkSelf(clientID string, entity EntityID) error {
	return errNotImplemented
}

// MarkAlly marks an entity as friendly for a client.
func (g *SnapshotGovernor) MarkAlly(clientID string, entity EntityID) error {
	return errNotImplemented
}

// Leave records that an entity left the interest range of a client.
func (g *SnapshotGovernor) Leave(clientID string, entity EntityID) error {
	return errNotImplemented
}

// Build assembles the packet plan of one tick for a client.
func (g *SnapshotGovernor) Build(clientID string, tick uint64) (PacketPlan, error) {
	return PacketPlan{}, errNotImplemented
}

// Ack confirms that a client received the packet of the given tick.
func (g *SnapshotGovernor) Ack(clientID string, tick uint64) error {
	return errNotImplemented
}

// Acked reports the newest acknowledged tick of a client.
func (g *SnapshotGovernor) Acked(clientID string) uint64 {
	return 0
}

// Pending reports how many entities of a client are dirty.
func (g *SnapshotGovernor) Pending(clientID string) int {
	return 0
}

// Snapshot serialises the budgets, the dirty sets and the priority state.
func (g *SnapshotGovernor) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole governor state with a payload.
func (g *SnapshotGovernor) Restore(data []byte) error {
	return errNotImplemented
}
