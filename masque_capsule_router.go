// Package task implements the capsule framing front end of a CONNECT-UDP
// proxy: it parses the capsule stream of one HTTP connection, binds datagram
// payloads to local UDP flows, frames outbound datagrams back into capsules
// and keeps the whole flow table recoverable from a snapshot.
package task

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Errors reported by the router.
var (
	ErrZeroFlowID      = errors.New("zero flow id")
	ErrFlowLimit       = errors.New("flow limit reached")
	ErrDuplicateFlow   = errors.New("flow already open")
	ErrDraining        = errors.New("router is draining")
	ErrUnknownFlow     = errors.New("unknown flow")
	ErrQueueFull       = errors.New("flow queue full")
	ErrPayloadTooLarge = errors.New("datagram payload too large")
	ErrCapsuleTooLarge = errors.New("capsule length above limit")
	ErrConnClosed      = errors.New("connection closed")
	ErrDrainTimeout    = errors.New("drain deadline expired")
	ErrBadSnapshot     = errors.New("bad snapshot")
)

// Capsule types carried on the proxy connection.
const (
	CapsuleDatagram uint64 = 0x00
	CapsuleClose    uint64 = 0x01
	CapsulePadding  uint64 = 0x02
	CapsuleGoAway   uint64 = 0x04
)

// ProtocolError describes a malformed capsule frame. FlowID is the flow the
// parser was serving when the frame was rejected, or zero when the frame
// carried no usable flow id.
type ProtocolError struct {
	Reason string
	FlowID uint64
	Offset int
}

// Error implements the error interface.
func (e *ProtocolError) Error() string {
	if e.FlowID != 0 {
		return fmt.Sprintf("capsule protocol error at offset %d for flow %d: %s", e.Offset, e.FlowID, e.Reason)
	}
	return fmt.Sprintf("capsule protocol error at offset %d: %s", e.Offset, e.Reason)
}

// CapsuleLimits bounds the flow table and the per flow queues.
type CapsuleLimits struct {
	MaxFlows         int
	MaxQueuePerFlow  int
	MaxCapsuleBytes  uint64
	MaxDatagramBytes uint64
}

// Capsule is one decoded capsule frame.
type Capsule struct {
	Type    uint64
	Payload []byte
}

// InboundDatagram is one datagram lifted out of the capsule stream.
type InboundDatagram struct {
	FlowID  uint64
	Payload []byte
	Seq     uint64
}

// RouterStats counts what the connection carried so far.
type RouterStats struct {
	Capsules            int
	DatagramsIn         int
	DatagramsOut        int
	DatagramsDropped    int
	UnknownCapsules     int
	GoAwayCapsules      int
	RejectedAfterGoAway int
	ProtocolErrors      int
	DrainTimeouts       int
}

// flowState is the per flow queue pair.
type flowState struct {
	id       uint64
	nextSeq  uint64
	inbound  []InboundDatagram
	outbound []Capsule
}

// CapsuleRouter routes capsules of one proxy connection to local flows.
type CapsuleRouter struct {
	mu       sync.Mutex
	limits   CapsuleLimits
	flows    map[uint64]*flowState
	partial  []byte
	consumed int64
	draining bool
	deadline time.Time
	goAway   bool
	failed   bool
	stats    RouterStats
}

// NewCapsuleRouter builds a router with the supplied limits.
func NewCapsuleRouter(limits CapsuleLimits) *CapsuleRouter {
	return &CapsuleRouter{
		limits: limits,
		flows:  make(map[uint64]*flowState),
	}
}

// Feed consumes one chunk of the capsule stream. Partial frames are kept for
// the next call, several frames inside one chunk are all consumed.
func (r *CapsuleRouter) Feed(chunk []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed {
		return ErrConnClosed
	}
	if len(chunk) == 0 {
		return nil
	}
	r.partial = append(r.partial, chunk...)
	for len(r.partial) > 0 {
		typ, n1 := binary.Uvarint(r.partial)
		if n1 == 0 {
			return nil
		}
		if n1 < 0 {
			r.failed = true
			return r.protocolErrorLocked("capsule type varint overflow", 0, r.consumed)
		}
		length, n2 := binary.Uvarint(r.partial[n1:])
		if n2 == 0 {
			return nil
		}
		if n2 < 0 {
			r.failed = true
			return r.protocolErrorLocked("capsule length varint overflow", 0, r.consumed+int64(n1))
		}
		if !minimalUvarint(length, n2) {
			r.failed = true
			return r.protocolErrorLocked("non-minimal capsule length", 0, r.consumed+int64(n1))
		}
		if r.limits.MaxCapsuleBytes > 0 && length > r.limits.MaxCapsuleBytes {
			r.stats.ProtocolErrors++
			r.failed = true
			return ErrCapsuleTooLarge
		}
		header := n1 + n2
		if uint64(len(r.partial)-header) < length {
			return nil
		}
		payload := r.partial[header : header+int(length)]
		frameOffset := r.consumed
		r.consumed += int64(header) + int64(length)
		r.partial = r.partial[header+int(length):]
		if len(r.partial) == 0 {
			r.partial = nil
		}
		if err := r.handleCapsuleLocked(typ, payload, frameOffset, header); err != nil {
			return err
		}
	}
	return nil
}

// handleCapsuleLocked dispatches one fully reassembled capsule.
func (r *CapsuleRouter) handleCapsuleLocked(typ uint64, payload []byte, offset int64, headerLen int) error {
	r.stats.Capsules++
	switch typ {
	case CapsuleDatagram:
		if len(payload) == 0 {
			return r.protocolErrorLocked("empty datagram capsule", 0, offset)
		}
		flowID, n := binary.Uvarint(payload)
		if n <= 0 {
			return r.protocolErrorLocked("bad flow id varint", 0, offset+int64(headerLen))
		}
		if !minimalUvarint(flowID, n) {
			return r.protocolErrorLocked("non-minimal flow id", flowID, offset+int64(headerLen))
		}
		body := payload[n:]
		if r.limits.MaxDatagramBytes > 0 && uint64(len(body)) > r.limits.MaxDatagramBytes {
			return r.protocolErrorLocked("datagram payload too large", flowID, offset+int64(headerLen))
		}
		fs, ok := r.flows[flowID]
		if !ok {
			r.stats.DatagramsDropped++
			return nil
		}
		if r.goAway {
			r.stats.RejectedAfterGoAway++
			return nil
		}
		if r.limits.MaxQueuePerFlow > 0 && len(fs.inbound) >= r.limits.MaxQueuePerFlow {
			r.stats.DatagramsDropped++
			return nil
		}
		cp := make([]byte, len(body))
		copy(cp, body)
		fs.inbound = append(fs.inbound, InboundDatagram{FlowID: flowID, Payload: cp, Seq: fs.nextSeq})
		fs.nextSeq++
		r.stats.DatagramsIn++
	case CapsulePadding:
		// Padding carries no data and does not disturb later frames.
	case CapsuleGoAway:
		r.stats.GoAwayCapsules++
		r.goAway = true
	case CapsuleClose:
		// Connection level close capsule: counted but no flow action.
	default:
		r.stats.UnknownCapsules++
	}
	return nil
}

// protocolErrorLocked counts and builds a protocol violation.
func (r *CapsuleRouter) protocolErrorLocked(reason string, flowID uint64, offset int64) error {
	r.stats.ProtocolErrors++
	return &ProtocolError{Reason: reason, FlowID: flowID, Offset: int(offset)}
}

// minimalUvarint reports whether n bytes are the shortest encoding of v.
func minimalUvarint(v uint64, n int) bool {
	want := 1
	for v >= 0x80 {
		v >>= 7
		want++
	}
	return n == want
}

// appendUvarint appends the minimal uvarint encoding of v.
func appendUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

// OpenFlow registers one local UDP flow.
func (r *CapsuleRouter) OpenFlow(flowID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if flowID == 0 {
		return ErrZeroFlowID
	}
	if r.draining {
		return ErrDraining
	}
	if _, ok := r.flows[flowID]; ok {
		return ErrDuplicateFlow
	}
	if r.limits.MaxFlows > 0 && len(r.flows) >= r.limits.MaxFlows {
		return ErrFlowLimit
	}
	r.flows[flowID] = &flowState{id: flowID, nextSeq: 1}
	return nil
}

// CloseFlow removes one flow together with both of its queues.
func (r *CapsuleRouter) CloseFlow(flowID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.flows, flowID)
}

// Deliver queues one local UDP reply for the flow, framed as a datagram
// capsule on the outbound side.
func (r *CapsuleRouter) Deliver(flowID uint64, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fs, ok := r.flows[flowID]
	if !ok {
		return ErrUnknownFlow
	}
	if r.limits.MaxDatagramBytes > 0 && uint64(len(payload)) > r.limits.MaxDatagramBytes {
		return ErrPayloadTooLarge
	}
	if r.limits.MaxQueuePerFlow > 0 && len(fs.outbound) >= r.limits.MaxQueuePerFlow {
		return ErrQueueFull
	}
	body := appendUvarint(nil, flowID)
	body = append(body, payload...)
	fs.outbound = append(fs.outbound, Capsule{Type: CapsuleDatagram, Payload: body})
	r.stats.DatagramsOut++
	return nil
}

// Drain stops accepting new flows and closes the outbound side once every
// queue is empty. A deadline that is already in the past fails immediately
// when something is still queued.
func (r *CapsuleRouter) Drain(deadline time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.draining = true
	r.deadline = deadline
	backlog := 0
	for _, fs := range r.flows {
		backlog += len(fs.outbound)
	}
	if backlog == 0 {
		return nil
	}
	if !time.Now().Before(deadline) {
		r.stats.DrainTimeouts++
		return ErrDrainTimeout
	}
	return nil
}

// TakeInbound removes and returns the datagrams queued for the flow.
func (r *CapsuleRouter) TakeInbound(flowID uint64) []InboundDatagram {
	r.mu.Lock()
	defer r.mu.Unlock()
	fs, ok := r.flows[flowID]
	if !ok {
		return nil
	}
	out := fs.inbound
	fs.inbound = nil
	return out
}

// TakeOutbound removes and returns the capsules queued for the flow.
func (r *CapsuleRouter) TakeOutbound(flowID uint64) []Capsule {
	r.mu.Lock()
	defer r.mu.Unlock()
	fs, ok := r.flows[flowID]
	if !ok {
		return nil
	}
	out := fs.outbound
	fs.outbound = nil
	return out
}

// Pending reports the queued datagram counts of one flow.
func (r *CapsuleRouter) Pending(flowID uint64) (inbound int, outbound int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fs, ok := r.flows[flowID]
	if !ok {
		return 0, 0
	}
	return len(fs.inbound), len(fs.outbound)
}

// FlowIDs lists the open flows in ascending order.
func (r *CapsuleRouter) FlowIDs() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]uint64, 0, len(r.flows))
	for id := range r.flows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Stats returns a copy of the connection counters.
func (r *CapsuleRouter) Stats() RouterStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// flowSnapshot is the serialisable form of one flow.
type flowSnapshot struct {
	ID       uint64            `json:"id"`
	NextSeq  uint64            `json:"next_seq"`
	Inbound  []InboundDatagram `json:"inbound"`
	Outbound []Capsule         `json:"outbound"`
}

// routerSnapshot is the serialisable form of the whole router state. The
// half frame sitting in the read buffer is deliberately excluded.
type routerSnapshot struct {
	Flows    []flowSnapshot `json:"flows"`
	Draining bool           `json:"draining"`
	Deadline time.Time      `json:"deadline"`
	GoAway   bool           `json:"goaway"`
	Failed   bool           `json:"failed"`
	Stats    RouterStats    `json:"stats"`
}

// Snapshot serialises the flow table and the outbound queues. A half frame
// still sitting in the read buffer is not part of the snapshot.
func (r *CapsuleRouter) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := routerSnapshot{
		Draining: r.draining,
		Deadline: r.deadline,
		GoAway:   r.goAway,
		Failed:   r.failed,
		Stats:    r.stats,
	}
	ids := make([]uint64, 0, len(r.flows))
	for id := range r.flows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		fs := r.flows[id]
		snap.Flows = append(snap.Flows, flowSnapshot{
			ID:       id,
			NextSeq:  fs.nextSeq,
			Inbound:  fs.inbound,
			Outbound: fs.outbound,
		})
	}
	return json.Marshal(&snap)
}

// Restore replaces the flow table with a snapshot.
func (r *CapsuleRouter) Restore(data []byte) error {
	var snap routerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.limits.MaxFlows > 0 && len(snap.Flows) > r.limits.MaxFlows {
		return ErrBadSnapshot
	}
	flows := make(map[uint64]*flowState, len(snap.Flows))
	for _, f := range snap.Flows {
		if f.ID == 0 {
			return ErrBadSnapshot
		}
		if _, dup := flows[f.ID]; dup {
			return ErrBadSnapshot
		}
		if r.limits.MaxQueuePerFlow > 0 &&
			(len(f.Inbound) > r.limits.MaxQueuePerFlow || len(f.Outbound) > r.limits.MaxQueuePerFlow) {
			return ErrBadSnapshot
		}
		nextSeq := f.NextSeq
		if nextSeq == 0 {
			nextSeq = 1
		}
		flows[f.ID] = &flowState{
			id:       f.ID,
			nextSeq:  nextSeq,
			inbound:  f.Inbound,
			outbound: f.Outbound,
		}
	}
	r.flows = flows
	r.partial = nil
	r.draining = snap.Draining
	r.deadline = snap.Deadline
	r.goAway = snap.GoAway
	r.failed = snap.Failed
	r.stats = snap.Stats
	return nil
}
