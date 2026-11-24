// Package task arbitrates the egress bandwidth of one link between three
// traffic classes. Every class owns a guaranteed quota, idle quota may be
// borrowed, and a high priority flow can roll back the smallest possible set
// of lower priority flows when the link is fully committed.
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

// Errors reported by the arbiter.
var (
	ErrInvalidConfig         = errors.New("invalid arbiter config")
	ErrGuaranteeOverCapacity = errors.New("guaranteed quotas exceed link capacity")
	ErrInvalidCapacity       = errors.New("invalid link capacity")
	ErrZeroDemand            = errors.New("zero demand")
	ErrInvalidClass          = errors.New("invalid traffic class")
	ErrFlowExists            = errors.New("flow already admitted")
	ErrFlowLimit             = errors.New("flow limit reached")
	ErrUnknownFlow           = errors.New("unknown flow")
	ErrSameClass             = errors.New("flow already in that class")
	ErrClassGuaranteeFull    = errors.New("target class cannot cover the flow")
	ErrInvalidPolicy         = errors.New("invalid arbiter policy")
	ErrBadSnapshot           = errors.New("bad snapshot")
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

// classOrder is also the order in which idle guarantees are borrowed and in
// which guarantees are shrunk when the link capacity is reduced.
var classOrder = [...]Class{ClassBE, ClassAF, ClassEF}

// quotaPart records which class pool paid for a part of a flow's grant.
// Keeping this breakdown, rather than only the flow's class, is what makes a
// release return borrowed quota to the actual lender.
type quotaPart struct {
	owner  Class
	amount uint64
}

type flowState struct {
	id        FlowID
	class     Class
	demand    uint64
	granted   uint64
	parts     []quotaPart
	admitted  bool
	order     uint64
	waitTicks int
	preempted bool
}

// TrafficClassArbiter serialises every admission decision of one link.
type TrafficClassArbiter struct {
	mu sync.Mutex

	capacity   uint64
	guarantees map[Class]uint64
	maxFlows   int
	policy     ArbiterPolicy

	usage   map[Class]uint64
	entries map[FlowID]*flowState
	queue   []FlowID

	nextOrder uint64
	stats     ArbiterStats
}

func validClass(cls Class) bool {
	return cls == ClassBE || cls == ClassAF || cls == ClassEF
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func addOverflows(a, b uint64) bool {
	return ^uint64(0)-a < b
}

// NewTrafficClassArbiter validates the config and builds the arbiter.
func NewTrafficClassArbiter(cfg ArbiterConfig) (*TrafficClassArbiter, error) {
	if cfg.Capacity == 0 || cfg.MaxFlows <= 0 {
		return nil, ErrInvalidConfig
	}

	guarantees := map[Class]uint64{
		ClassBE: 0,
		ClassAF: 0,
		ClassEF: 0,
	}
	for cls, amount := range cfg.Guarantees {
		if !validClass(cls) {
			return nil, ErrInvalidConfig
		}
		guarantees[cls] = amount
	}

	var total uint64
	for _, cls := range classOrder {
		if addOverflows(total, guarantees[cls]) {
			return nil, ErrGuaranteeOverCapacity
		}
		total += guarantees[cls]
	}
	if total > cfg.Capacity {
		return nil, ErrGuaranteeOverCapacity
	}

	return &TrafficClassArbiter{
		capacity:   cfg.Capacity,
		guarantees: guarantees,
		maxFlows:   cfg.MaxFlows,
		policy: ArbiterPolicy{
			Borrowing:  true,
			Preemption: true,
			AgingTicks: 3,
		},
		usage: map[Class]uint64{
			ClassBE: 0,
			ClassAF: 0,
			ClassEF: 0,
		},
		entries:   make(map[FlowID]*flowState),
		nextOrder: 1,
	}, nil
}

func cloneUsage(usage map[Class]uint64) map[Class]uint64 {
	return map[Class]uint64{
		ClassBE: usage[ClassBE],
		ClassAF: usage[ClassAF],
		ClassEF: usage[ClassEF],
	}
}

// planGrantWithUsage computes an attribution without mutating the arbiter.
// It first uses the flow's own guarantee, then idle guarantees in BE/AF/EF
// order, and finally unassigned link capacity (when borrowing is enabled).
// Any unassigned capacity is attributed to the requesting class.
func (a *TrafficClassArbiter) planGrantWithUsage(cls Class, demand uint64, usage map[Class]uint64) ([]quotaPart, uint64) {
	parts := make([]quotaPart, 0, 4)
	remaining := demand

	add := func(owner Class, amount uint64) {
		if amount == 0 {
			return
		}
		if n := len(parts); n > 0 && parts[n-1].owner == owner {
			parts[n-1].amount += amount
		} else {
			parts = append(parts, quotaPart{owner: owner, amount: amount})
		}
	}

	if used := usage[cls]; used < a.guarantees[cls] {
		take := minU64(remaining, a.guarantees[cls]-used)
		add(cls, take)
		remaining -= take
	}

	if remaining > 0 && a.policy.Borrowing {
		for _, lender := range classOrder {
			if lender == cls {
				continue
			}
			used := usage[lender]
			if used >= a.guarantees[lender] {
				continue
			}
			take := minU64(remaining, a.guarantees[lender]-used)
			add(lender, take)
			remaining -= take
			if remaining == 0 {
				break
			}
		}
	}

	if remaining > 0 && a.policy.Borrowing {
		total := uint64(0)
		for _, owner := range classOrder {
			total += usage[owner]
		}
		planned := demand - remaining
		if total <= a.capacity && planned <= a.capacity-total {
			slack := a.capacity - total - planned
			take := minU64(remaining, slack)
			add(cls, take)
			remaining -= take
		}
	}

	return parts, demand - remaining
}

func (a *TrafficClassArbiter) planGrantLocked(cls Class, demand uint64) ([]quotaPart, uint64) {
	return a.planGrantWithUsage(cls, demand, a.usage)
}

func (a *TrafficClassArbiter) applyPartsLocked(parts []quotaPart) {
	for _, part := range parts {
		a.usage[part.owner] += part.amount
	}
}

func (a *TrafficClassArbiter) releasePartsLocked(parts []quotaPart) {
	for _, part := range parts {
		if a.usage[part.owner] >= part.amount {
			a.usage[part.owner] -= part.amount
		} else {
			a.usage[part.owner] = 0
		}
	}
}

func borrowedClasses(parts []quotaPart, own Class) []Class {
	var result []Class
	seen := make(map[Class]bool, len(parts))
	for _, part := range parts {
		if part.amount == 0 || part.owner == own || seen[part.owner] {
			continue
		}
		seen[part.owner] = true
		result = append(result, part.owner)
	}
	return result
}

func (a *TrafficClassArbiter) effectiveClass(st *flowState) Class {
	if a.policy.AgingTicks <= 0 || st.waitTicks < a.policy.AgingTicks {
		return st.class
	}
	boost := st.waitTicks / a.policy.AgingTicks
	value := int(st.class) + boost
	if value > int(ClassEF) {
		value = int(ClassEF)
	}
	return Class(value)
}

func (a *TrafficClassArbiter) queueLessLocked(left, right *flowState) bool {
	if left.preempted != right.preempted {
		return left.preempted
	}
	leftClass := a.effectiveClass(left)
	rightClass := a.effectiveClass(right)
	if leftClass != rightClass {
		return leftClass > rightClass
	}
	return left.order < right.order
}

func (a *TrafficClassArbiter) sortQueueLocked() {
	sort.SliceStable(a.queue, func(i, j int) bool {
		left := a.entries[a.queue[i]]
		right := a.entries[a.queue[j]]
		if left == nil || right == nil {
			return a.queue[i] < a.queue[j]
		}
		return a.queueLessLocked(left, right)
	})
}

func (a *TrafficClassArbiter) dequeueLocked(flow FlowID) {
	for i, id := range a.queue {
		if id == flow {
			copy(a.queue[i:], a.queue[i+1:])
			a.queue = a.queue[:len(a.queue)-1]
			return
		}
	}
}

func (a *TrafficClassArbiter) enqueueLocked(flow FlowID) {
	for _, id := range a.queue {
		if id == flow {
			return
		}
	}
	a.queue = append(a.queue, flow)
	a.sortQueueLocked()
}

func (a *TrafficClassArbiter) queuePositionLocked(flow FlowID) int {
	for i, id := range a.queue {
		if id == flow {
			return i + 1
		}
	}
	return 0
}

func (a *TrafficClassArbiter) commitGrantLocked(st *flowState, parts []quotaPart, granted uint64) {
	a.dequeueLocked(st.id)
	a.applyPartsLocked(parts)
	st.parts = parts
	st.granted = granted
	st.admitted = true
	st.waitTicks = 0
	st.preempted = false
	a.stats.Grants++
	if granted < st.demand {
		a.stats.PartialGrants++
	}
}

// Admit asks for quota for one flow and grants what the pools can cover.
func (a *TrafficClassArbiter) Admit(flow FlowID, cls Class, demand uint64) (Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if demand == 0 {
		a.stats.Rejections++
		return Decision{}, ErrZeroDemand
	}
	if !validClass(cls) {
		a.stats.Rejections++
		return Decision{}, ErrInvalidClass
	}
	if _, exists := a.entries[flow]; exists {
		a.stats.Rejections++
		return Decision{}, ErrFlowExists
	}
	if len(a.entries) >= a.maxFlows {
		a.stats.Rejections++
		return Decision{}, ErrFlowLimit
	}

	st := &flowState{
		id:     flow,
		class:  cls,
		demand: demand,
		order:  a.nextOrder,
	}
	a.nextOrder++
	a.entries[flow] = st

	parts, granted := a.planGrantLocked(cls, demand)
	if granted == 0 {
		a.enqueueLocked(flow)
		a.stats.Queued++
		return Decision{
			Flow:          flow,
			Class:         cls,
			Admitted:      false,
			Granted:       0,
			QueuePosition: a.queuePositionLocked(flow),
		}, nil
	}

	a.commitGrantLocked(st, parts, granted)
	return Decision{
		Flow:         flow,
		Class:        cls,
		Admitted:     true,
		Granted:      granted,
		BorrowedFrom: borrowedClasses(parts, cls),
	}, nil
}

// Release removes the flow and returns its quota to the lending pools.
func (a *TrafficClassArbiter) Release(flow FlowID) {
	a.mu.Lock()
	defer a.mu.Unlock()

	st, exists := a.entries[flow]
	if !exists {
		return
	}
	if st.admitted {
		a.releasePartsLocked(st.parts)
	} else {
		a.dequeueLocked(flow)
	}
	delete(a.entries, flow)
	a.stats.Releases++
}

// Reclass moves an admitted flow to another class.
func (a *TrafficClassArbiter) Reclass(flow FlowID, cls Class) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !validClass(cls) {
		return ErrInvalidClass
	}
	st, exists := a.entries[flow]
	if !exists {
		return ErrUnknownFlow
	}
	if st.class == cls {
		return ErrSameClass
	}

	if !st.admitted {
		st.class = cls
		a.sortQueueLocked()
		return nil
	}

	// Releasing the old attribution first lets the target class use quota that
	// this same flow is giving back. The old attribution is restored verbatim
	// if the target cannot cover the whole demand.
	a.releasePartsLocked(st.parts)
	parts, granted := a.planGrantLocked(cls, st.demand)
	if granted < st.demand {
		a.applyPartsLocked(st.parts)
		return ErrClassGuaranteeFull
	}

	a.applyPartsLocked(parts)
	st.class = cls
	st.parts = parts
	st.granted = granted
	return nil
}

func (a *TrafficClassArbiter) subsetSatisfiesLocked(waiter *flowState, victims []*flowState) bool {
	usage := cloneUsage(a.usage)
	for _, victim := range victims {
		for _, part := range victim.parts {
			if usage[part.owner] < part.amount {
				return false
			}
			usage[part.owner] -= part.amount
		}
	}
	_, granted := a.planGrantWithUsage(waiter.class, waiter.demand, usage)
	return granted == waiter.demand
}

func sumGrants(victims []*flowState) uint64 {
	var total uint64
	for _, victim := range victims {
		total += victim.granted
	}
	return total
}

// selectVictimsLocked returns the smallest set of lower-priority flows whose
// removal lets the waiter receive its full demand. Among sets of equal size,
// the one releasing the least quota wins; flows are then ordered by ID so the
// result is deterministic.
func (a *TrafficClassArbiter) selectVictimsLocked(waiter *flowState) []*flowState {
	if !a.policy.Preemption {
		return nil
	}

	candidates := make([]*flowState, 0)
	for _, st := range a.entries {
		if st.admitted && st.class < waiter.class {
			candidates = append(candidates, st)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].id < candidates[j].id
	})

	// Exact search is cheap for normal flow-table sizes and guarantees the
	// "minimal set" property. Fall back to a deterministic greedy choice for
	// unusually large tables to avoid exponential work.
	if len(candidates) > 18 {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].granted != candidates[j].granted {
				return candidates[i].granted > candidates[j].granted
			}
			return candidates[i].id < candidates[j].id
		})
		chosen := make([]*flowState, 0, len(candidates))
		for _, candidate := range candidates {
			chosen = append(chosen, candidate)
			if a.subsetSatisfiesLocked(waiter, chosen) {
				return chosen
			}
		}
		return nil
	}

	for size := 1; size <= len(candidates); size++ {
		var best []*flowState
		bestReleased := ^uint64(0)
		found := false
		combo := make([]*flowState, 0, size)

		var walk func(start int)
		walk = func(start int) {
			if len(combo) == size {
				if !a.subsetSatisfiesLocked(waiter, combo) {
					return
				}
				found = true
				released := sumGrants(combo)
				if released < bestReleased {
					bestReleased = released
					best = append(best[:0], combo...)
				}
				return
			}
			remainingSlots := size - len(combo)
			for i := start; i <= len(candidates)-remainingSlots; i++ {
				combo = append(combo, candidates[i])
				walk(i + 1)
				combo = combo[:len(combo)-1]
			}
		}
		walk(0)
		if found {
			return best
		}
	}
	return nil
}

func (a *TrafficClassArbiter) preemptLocked(waiter *flowState, victims []*flowState) []Preemption {
	releasedByID := make(map[FlowID]uint64, len(victims))
	for _, victim := range victims {
		releasedByID[victim.id] = victim.granted
		a.releasePartsLocked(victim.parts)
		victim.parts = nil
		victim.granted = 0
		victim.admitted = false
		victim.waitTicks = 0
		victim.preempted = true
		a.enqueueLocked(victim.id)
		a.stats.Queued++
	}
	a.stats.Preemptions += len(victims)

	sort.Slice(victims, func(i, j int) bool {
		return victims[i].id < victims[j].id
	})
	result := make([]Preemption, 0, len(victims))
	for _, victim := range victims {
		result = append(result, Preemption{
			Flow:          victim.id,
			Class:         victim.class,
			Released:      releasedByID[victim.id],
			PreemptedBy:   waiter.id,
			QueuePosition: a.queuePositionLocked(victim.id),
		})
	}
	return result
}

func (a *TrafficClassArbiter) serviceWaitingLocked(st *flowState) []Preemption {
	parts, granted := a.planGrantLocked(st.class, st.demand)
	if granted >= st.demand {
		a.commitGrantLocked(st, parts, granted)
		return nil
	}

	if a.policy.Preemption {
		victims := a.selectVictimsLocked(st)
		if len(victims) > 0 {
			// The waiter is being served now, so its old queue slot must not
			// affect the positions assigned to its victims.
			a.dequeueLocked(st.id)
			preemptions := a.preemptLocked(st, victims)
			newParts, newGranted := a.planGrantLocked(st.class, st.demand)
			if newGranted > 0 {
				a.commitGrantLocked(st, newParts, newGranted)
			} else {
				a.enqueueLocked(st.id)
			}
			return preemptions
		}
	}

	if granted > 0 {
		a.commitGrantLocked(st, parts, granted)
	} else {
		st.waitTicks++
	}
	return nil
}

// Tick advances the arbiter clock, retries waiting flows and rolls back lower
// priority flows when a higher priority flow cannot be satisfied.
func (a *TrafficClassArbiter) Tick(now time.Time) []Preemption {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.stats.Ticks++
	_ = now
	a.sortQueueLocked()
	waiting := append([]FlowID(nil), a.queue...)
	var preemptions []Preemption
	for _, flow := range waiting {
		st := a.entries[flow]
		if st == nil || st.admitted {
			continue
		}
		preemptions = append(preemptions, a.serviceWaitingLocked(st)...)
	}
	a.sortQueueLocked()
	return preemptions
}

// SetPolicy replaces the dynamic policy.
func (a *TrafficClassArbiter) SetPolicy(p ArbiterPolicy) error {
	if p.AgingTicks < 0 {
		return ErrInvalidPolicy
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.policy = p
	a.sortQueueLocked()
	return nil
}

// trimAttributionLocked reduces quota attributed to owner until need bytes
// have been released. Lower-priority flows give up quota first.
func (a *TrafficClassArbiter) trimAttributionLocked(owner Class, need uint64, demotions *[]Demotion, indexes map[FlowID]int) {
	candidates := make([]*flowState, 0)
	for _, st := range a.entries {
		if !st.admitted {
			continue
		}
		for _, part := range st.parts {
			if part.owner == owner && part.amount > 0 {
				candidates = append(candidates, st)
				break
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].class != candidates[j].class {
			return candidates[i].class < candidates[j].class
		}
		return candidates[i].order > candidates[j].order
	})

	for _, st := range candidates {
		if need == 0 {
			break
		}
		for i := 0; i < len(st.parts) && need > 0; {
			part := &st.parts[i]
			if part.owner != owner || part.amount == 0 {
				i++
				continue
			}
			take := minU64(need, part.amount)
			part.amount -= take
			st.granted -= take
			a.usage[owner] -= take
			need -= take

			index, seen := indexes[st.id]
			if !seen {
				index = len(*demotions)
				indexes[st.id] = index
				*demotions = append(*demotions, Demotion{Flow: st.id, Class: st.class})
			}
			(*demotions)[index].Released += take

			if part.amount == 0 {
				st.parts = append(st.parts[:i], st.parts[i+1:]...)
			} else {
				i++
			}
		}
		if st.granted == 0 {
			delete(a.entries, st.id)
		}
	}
}

func (a *TrafficClassArbiter) recordDemotion(demotions *[]Demotion, indexes map[FlowID]int, st *flowState, amount uint64) {
	index, seen := indexes[st.id]
	if !seen {
		index = len(*demotions)
		indexes[st.id] = index
		*demotions = append(*demotions, Demotion{Flow: st.id, Class: st.class})
	}
	(*demotions)[index].Released += amount
}

// trimGlobalLocked is used when guarantees still fit the new capacity but
// outstanding slack attributions do not. It releases quota from the least
// important flows first, regardless of which pool paid for it.
func (a *TrafficClassArbiter) trimGlobalLocked(need uint64, demotions *[]Demotion, indexes map[FlowID]int) {
	candidates := make([]*flowState, 0, len(a.entries))
	for _, st := range a.entries {
		if st.admitted && st.granted > 0 {
			candidates = append(candidates, st)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].class != candidates[j].class {
			return candidates[i].class < candidates[j].class
		}
		return candidates[i].order > candidates[j].order
	})

	for _, st := range candidates {
		if need == 0 {
			break
		}
		for i := 0; i < len(st.parts) && need > 0; {
			part := &st.parts[i]
			take := minU64(need, part.amount)
			part.amount -= take
			st.granted -= take
			a.usage[part.owner] -= take
			need -= take
			a.recordDemotion(demotions, indexes, st, take)

			if part.amount == 0 {
				st.parts = append(st.parts[:i], st.parts[i+1:]...)
			} else {
				i++
			}
		}
		if st.granted == 0 {
			delete(a.entries, st.id)
		}
	}
}

// SetCapacity changes the link capacity, shrinking quotas when needed.
func (a *TrafficClassArbiter) SetCapacity(capacity uint64) ([]Demotion, error) {
	if capacity == 0 {
		return nil, ErrInvalidCapacity
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if capacity == a.capacity {
		return nil, nil
	}

	newGuarantees := map[Class]uint64{
		ClassBE: a.guarantees[ClassBE],
		ClassAF: a.guarantees[ClassAF],
		ClassEF: a.guarantees[ClassEF],
	}
	totalGuarantees := newGuarantees[ClassBE] + newGuarantees[ClassAF] + newGuarantees[ClassEF]

	var demotions []Demotion
	indexes := make(map[FlowID]int)
	if capacity < totalGuarantees {
		excess := totalGuarantees - capacity
		for _, cls := range classOrder {
			if excess == 0 {
				break
			}
			take := minU64(excess, newGuarantees[cls])
			newGuarantees[cls] -= take
			excess -= take
		}

		for _, cls := range classOrder {
			if a.usage[cls] <= newGuarantees[cls] {
				continue
			}
			need := a.usage[cls] - newGuarantees[cls]
			a.trimAttributionLocked(cls, need, &demotions, indexes)
		}
	} else {
		totalUsage := a.usage[ClassBE] + a.usage[ClassAF] + a.usage[ClassEF]
		if totalUsage > capacity {
			a.trimGlobalLocked(totalUsage-capacity, &demotions, indexes)
		}
	}

	a.capacity = capacity
	a.guarantees = newGuarantees
	a.stats.Demotions += len(demotions)
	return demotions, nil
}

// Usage reports how much of the class quota is currently attributed.
func (a *TrafficClassArbiter) Usage(cls Class) uint64 {
	if !validClass(cls) {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage[cls]
}

// Flows lists the admitted flows in ascending order.
func (a *TrafficClassArbiter) Flows() []FlowID {
	a.mu.Lock()
	defer a.mu.Unlock()

	flows := make([]FlowID, 0, len(a.entries))
	for flow, st := range a.entries {
		if st.admitted {
			flows = append(flows, flow)
		}
	}
	sort.Slice(flows, func(i, j int) bool {
		return flows[i] < flows[j]
	})
	return flows
}

func (a *TrafficClassArbiter) borrowedLocked() uint64 {
	var borrowed uint64
	for _, st := range a.entries {
		if !st.admitted {
			continue
		}
		for _, part := range st.parts {
			if part.owner != st.class {
				borrowed += part.amount
			}
		}
	}
	return borrowed
}

func (a *TrafficClassArbiter) statsLocked() ArbiterStats {
	stats := a.stats
	stats.Flows = 0
	for _, st := range a.entries {
		if st.admitted {
			stats.Flows++
		}
	}
	stats.Waiting = len(a.queue)
	stats.EFUsed = a.usage[ClassEF]
	stats.AFUsed = a.usage[ClassAF]
	stats.BEUsed = a.usage[ClassBE]
	stats.Borrowed = a.borrowedLocked()
	return stats
}

// Stats returns a copy of the arbiter counters.
func (a *TrafficClassArbiter) Stats() ArbiterStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statsLocked()
}

const snapshotVersion = 1

type snapshotClasses struct {
	EF uint64 `json:"ef"`
	AF uint64 `json:"af"`
	BE uint64 `json:"be"`
}

type snapshotPart struct {
	Owner  Class  `json:"owner"`
	Amount uint64 `json:"amount"`
}

type snapshotFlow struct {
	ID        FlowID         `json:"id"`
	Class     Class          `json:"class"`
	Demand    uint64         `json:"demand"`
	Granted   uint64         `json:"granted"`
	Parts     []snapshotPart `json:"parts,omitempty"`
	Admitted  bool           `json:"admitted"`
	Order     uint64         `json:"order"`
	WaitTicks int            `json:"wait_ticks,omitempty"`
	Preempted bool           `json:"preempted,omitempty"`
}

type snapshotState struct {
	Version    int             `json:"version"`
	Capacity   uint64          `json:"capacity"`
	Guarantees snapshotClasses `json:"guarantees"`
	MaxFlows   int             `json:"max_flows"`
	Policy     ArbiterPolicy   `json:"policy"`
	NextOrder  uint64          `json:"next_order"`
	Usage      snapshotClasses `json:"usage"`
	Flows      []snapshotFlow  `json:"flows"`
	Queue      []FlowID        `json:"queue"`
	Stats      ArbiterStats    `json:"stats"`
	Checksum   string          `json:"checksum"`
}

func classesFromMap(values map[Class]uint64) snapshotClasses {
	return snapshotClasses{
		EF: values[ClassEF],
		AF: values[ClassAF],
		BE: values[ClassBE],
	}
}

func snapshotChecksum(state snapshotState) (string, error) {
	state.Checksum = ""
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Snapshot serialises quotas, borrow attribution, queues and counters.
func (a *TrafficClassArbiter) Snapshot() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	flows := make([]*flowState, 0, len(a.entries))
	for _, st := range a.entries {
		flows = append(flows, st)
	}
	sort.Slice(flows, func(i, j int) bool {
		return flows[i].id < flows[j].id
	})

	snapshotFlows := make([]snapshotFlow, 0, len(flows))
	for _, st := range flows {
		parts := make([]snapshotPart, 0, len(st.parts))
		for _, part := range st.parts {
			parts = append(parts, snapshotPart{Owner: part.owner, Amount: part.amount})
		}
		snapshotFlows = append(snapshotFlows, snapshotFlow{
			ID:        st.id,
			Class:     st.class,
			Demand:    st.demand,
			Granted:   st.granted,
			Parts:     parts,
			Admitted:  st.admitted,
			Order:     st.order,
			WaitTicks: st.waitTicks,
			Preempted: st.preempted,
		})
	}

	state := snapshotState{
		Version:    snapshotVersion,
		Capacity:   a.capacity,
		Guarantees: classesFromMap(a.guarantees),
		MaxFlows:   a.maxFlows,
		Policy:     a.policy,
		NextOrder:  a.nextOrder,
		Usage:      classesFromMap(a.usage),
		Flows:      snapshotFlows,
		Queue:      append([]FlowID(nil), a.queue...),
		Stats:      a.statsLocked(),
	}
	checksum, err := snapshotChecksum(state)
	if err != nil {
		return nil, err
	}
	state.Checksum = checksum
	return json.Marshal(state)
}

func countAdmitted(entries map[FlowID]*flowState) int {
	count := 0
	for _, st := range entries {
		if st.admitted {
			count++
		}
	}
	return count
}

func buildRestoredState(state snapshotState) (*TrafficClassArbiter, error) {
	if state.Version != snapshotVersion || state.Capacity == 0 || state.MaxFlows <= 0 || state.Policy.AgingTicks < 0 {
		return nil, ErrBadSnapshot
	}

	guarantees := map[Class]uint64{
		ClassBE: state.Guarantees.BE,
		ClassAF: state.Guarantees.AF,
		ClassEF: state.Guarantees.EF,
	}
	var guaranteeTotal uint64
	for _, cls := range classOrder {
		if addOverflows(guaranteeTotal, guarantees[cls]) {
			return nil, ErrBadSnapshot
		}
		guaranteeTotal += guarantees[cls]
	}
	if guaranteeTotal > state.Capacity {
		return nil, ErrBadSnapshot
	}

	if len(state.Flows) > state.MaxFlows {
		return nil, ErrBadSnapshot
	}

	entries := make(map[FlowID]*flowState, len(state.Flows))
	expectedUsage := map[Class]uint64{
		ClassBE: 0,
		ClassAF: 0,
		ClassEF: 0,
	}
	maxOrder := uint64(0)
	orders := make(map[uint64]bool, len(state.Flows))
	for _, saved := range state.Flows {
		if !validClass(saved.Class) || saved.Demand == 0 || saved.Order == 0 {
			return nil, ErrBadSnapshot
		}
		if _, exists := entries[saved.ID]; exists {
			return nil, ErrBadSnapshot
		}
		if orders[saved.Order] {
			return nil, ErrBadSnapshot
		}
		orders[saved.Order] = true
		if saved.Order > maxOrder {
			maxOrder = saved.Order
		}

		st := &flowState{
			id:        saved.ID,
			class:     saved.Class,
			demand:    saved.Demand,
			granted:   saved.Granted,
			admitted:  saved.Admitted,
			order:     saved.Order,
			waitTicks: saved.WaitTicks,
			preempted: saved.Preempted,
		}

		var partTotal uint64
		for _, savedPart := range saved.Parts {
			if !validClass(savedPart.Owner) || savedPart.Amount == 0 {
				return nil, ErrBadSnapshot
			}
			if addOverflows(partTotal, savedPart.Amount) {
				return nil, ErrBadSnapshot
			}
			partTotal += savedPart.Amount
			if addOverflows(expectedUsage[savedPart.Owner], savedPart.Amount) {
				return nil, ErrBadSnapshot
			}
			expectedUsage[savedPart.Owner] += savedPart.Amount
			st.parts = append(st.parts, quotaPart{owner: savedPart.Owner, amount: savedPart.Amount})
		}

		if saved.Admitted {
			if saved.Granted == 0 || saved.Granted != partTotal || saved.Granted > saved.Demand ||
				saved.WaitTicks != 0 || saved.Preempted {
				return nil, ErrBadSnapshot
			}
		} else {
			if saved.Granted != 0 || len(saved.Parts) != 0 || saved.WaitTicks < 0 {
				return nil, ErrBadSnapshot
			}
		}
		entries[saved.ID] = st
	}

	if state.NextOrder == 0 || state.NextOrder <= maxOrder {
		return nil, ErrBadSnapshot
	}

	usage := map[Class]uint64{
		ClassBE: state.Usage.BE,
		ClassAF: state.Usage.AF,
		ClassEF: state.Usage.EF,
	}
	var usageTotal uint64
	for _, cls := range classOrder {
		if usage[cls] != expectedUsage[cls] {
			return nil, ErrBadSnapshot
		}
		if addOverflows(usageTotal, usage[cls]) {
			return nil, ErrBadSnapshot
		}
		usageTotal += usage[cls]
	}
	if usageTotal > state.Capacity {
		return nil, ErrBadSnapshot
	}

	if len(state.Queue) != len(entries)-countAdmitted(entries) {
		return nil, ErrBadSnapshot
	}
	seenQueue := make(map[FlowID]bool, len(state.Queue))
	for _, flow := range state.Queue {
		if seenQueue[flow] {
			return nil, ErrBadSnapshot
		}
		st, exists := entries[flow]
		if !exists || st.admitted {
			return nil, ErrBadSnapshot
		}
		seenQueue[flow] = true
	}

	stats := state.Stats
	if stats.Flows < 0 || stats.Waiting < 0 || stats.Grants < 0 || stats.Queued < 0 ||
		stats.PartialGrants < 0 || stats.Releases < 0 || stats.Preemptions < 0 ||
		stats.Rejections < 0 || stats.Ticks < 0 || stats.Demotions < 0 {
		return nil, ErrBadSnapshot
	}
	borrowed := uint64(0)
	for _, st := range entries {
		if !st.admitted {
			continue
		}
		for _, part := range st.parts {
			if part.owner != st.class {
				borrowed += part.amount
			}
		}
	}
	if stats.Flows != countAdmitted(entries) || stats.Waiting != len(state.Queue) ||
		stats.BEUsed != usage[ClassBE] || stats.AFUsed != usage[ClassAF] ||
		stats.EFUsed != usage[ClassEF] || stats.Borrowed != borrowed {
		return nil, ErrBadSnapshot
	}

	return &TrafficClassArbiter{
		capacity:   state.Capacity,
		guarantees: guarantees,
		maxFlows:   state.MaxFlows,
		policy:     state.Policy,
		usage:      usage,
		entries:    entries,
		queue:      append([]FlowID(nil), state.Queue...),
		nextOrder:  state.NextOrder,
		stats:      state.Stats,
	}, nil
}

// Restore replaces the arbiter state with a snapshot.
func (a *TrafficClassArbiter) Restore(data []byte) error {
	var state snapshotState
	if len(data) == 0 {
		return ErrBadSnapshot
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return ErrBadSnapshot
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrBadSnapshot
	}

	checksum := state.Checksum
	if checksum == "" {
		return ErrBadSnapshot
	}
	expected, err := snapshotChecksum(state)
	if err != nil || checksum != expected {
		return ErrBadSnapshot
	}
	state.Checksum = checksum
	restored, err := buildRestoredState(state)
	if err != nil {
		return ErrBadSnapshot
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.capacity = restored.capacity
	a.guarantees = restored.guarantees
	a.maxFlows = restored.maxFlows
	a.policy = restored.policy
	a.usage = restored.usage
	a.entries = restored.entries
	a.queue = restored.queue
	a.nextOrder = restored.nextOrder
	a.stats = restored.stats
	return nil
}
