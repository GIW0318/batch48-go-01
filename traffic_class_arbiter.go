// Package task arbitrates the egress bandwidth of one link between three
// traffic classes. Every class owns a guaranteed quota, idle quota may be
// borrowed, and a high priority flow can roll back the smallest possible set
// of lower priority flows when the link is fully committed.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the arbiter.
var (
	ErrInvalidConfig          = errors.New("invalid arbiter config")
	ErrGuaranteeOverCapacity  = errors.New("guaranteed quotas exceed link capacity")
	ErrInvalidCapacity        = errors.New("invalid link capacity")
	ErrZeroDemand             = errors.New("zero demand")
	ErrInvalidClass           = errors.New("invalid traffic class")
	ErrFlowExists             = errors.New("flow already admitted")
	ErrFlowLimit              = errors.New("flow limit reached")
	ErrUnknownFlow            = errors.New("unknown flow")
	ErrSameClass              = errors.New("flow already in that class")
	ErrClassGuaranteeFull     = errors.New("target class cannot cover the flow")
	ErrInvalidPolicy          = errors.New("invalid arbiter policy")
	ErrBadSnapshot            = errors.New("bad snapshot")
)

// Class is one of the three egress traffic classes.
type Class uint8

// Traffic classes ordered from the least to the most important.
const (
	ClassBE Class = 1
	ClassAF Class = 2
	ClassEF Class = 3
)

// FlowID identifies one egress flow.
type FlowID uint64

// ArbiterConfig is the static side of the arbiter.
type ArbiterConfig struct {
	Capacity   uint64
	Guarantees map[Class]uint64
	MaxFlows   int
}

// ArbiterPolicy toggles the dynamic behaviour.
type ArbiterPolicy struct {
	Borrowing  bool
	Preemption bool
	AgingTicks int
}

// Decision is the reply of one Admit call.
type Decision struct {
	Flow          FlowID
	Class         Class
	Admitted      bool
	Granted       uint64
	BorrowedFrom  []Class
	QueuePosition int
}

// Preemption describes one flow rolled back by Tick.
type Preemption struct {
	Flow          FlowID
	Class         Class
	Released      uint64
	PreemptedBy   FlowID
	QueuePosition int
}

// Demotion describes one flow released when the link capacity shrank.
type Demotion struct {
	Flow     FlowID
	Class    Class
	Released uint64
}

// ArbiterStats counts what the arbiter did so far.
type ArbiterStats struct {
	Flows         int
	Waiting       int
	Grants        int
	Queued        int
	PartialGrants int
	Releases      int
	Preemptions   int
	Rejections    int
	Ticks         int
	Demotions     int
	EFUsed        uint64
	AFUsed        uint64
	BEUsed        uint64
	Borrowed      uint64
}

// TrafficClassArbiter serialises every admission decision of one link.
type TrafficClassArbiter struct {
	mu    sync.Mutex
	stats ArbiterStats
}

// NewTrafficClassArbiter validates the config and builds the arbiter.
func NewTrafficClassArbiter(cfg ArbiterConfig) (*TrafficClassArbiter, error) {
	return nil, errNotImplemented
}

// Admit asks for quota for one flow and grants what the pools can cover.
func (a *TrafficClassArbiter) Admit(flow FlowID, cls Class, demand uint64) (Decision, error) {
	return Decision{}, errNotImplemented
}

// Release removes the flow and returns its quota to the lending pools.
func (a *TrafficClassArbiter) Release(flow FlowID) {
}

// Reclass moves an admitted flow to another class.
func (a *TrafficClassArbiter) Reclass(flow FlowID, cls Class) error {
	return errNotImplemented
}

// Tick advances the arbiter clock, retries waiting flows and rolls back lower
// priority flows when a higher priority flow cannot be satisfied.
func (a *TrafficClassArbiter) Tick(now time.Time) []Preemption {
	return nil
}

// SetPolicy replaces the dynamic policy.
func (a *TrafficClassArbiter) SetPolicy(p ArbiterPolicy) error {
	return errNotImplemented
}

// SetCapacity changes the link capacity, shrinking quotas when needed.
func (a *TrafficClassArbiter) SetCapacity(capacity uint64) ([]Demotion, error) {
	return nil, errNotImplemented
}

// Usage reports how much of the class quota is currently attributed.
func (a *TrafficClassArbiter) Usage(cls Class) uint64 {
	return 0
}

// Flows lists the admitted flows in ascending order.
func (a *TrafficClassArbiter) Flows() []FlowID {
	return nil
}

// Stats returns a copy of the arbiter counters.
func (a *TrafficClassArbiter) Stats() ArbiterStats {
	return ArbiterStats{}
}

// Snapshot serialises quotas, borrow attribution, queues and counters.
func (a *TrafficClassArbiter) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the arbiter state with a snapshot.
func (a *TrafficClassArbiter) Restore(data []byte) error {
	return errNotImplemented
}
