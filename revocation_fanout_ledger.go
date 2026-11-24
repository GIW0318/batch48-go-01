// Package task fans revocation events out to validation nodes and tracks the
// per node confirmation watermark with a bounded event retention window.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

// Revocation event kinds.
const (
	KindCredential  = "credential"
	KindSession     = "session"
	KindCertificate = "certificate"
)

// Node states.
const (
	NodeActive    = "active"
	NodeDegraded  = "degraded"
	NodeResyncReq = "resync_required"
)

// Errors returned by the ledger.
var (
	ErrBlankArg       = errors.New("node id and reason are required")
	ErrEmptyBatch     = errors.New("append batch is empty")
	ErrInvalidEvent   = errors.New("revocation event is incomplete")
	ErrNodeExists     = errors.New("node already registered")
	ErrNodeLimit      = errors.New("node table is full")
	ErrUnknownNode    = errors.New("node not registered")
	ErrSeqAhead       = errors.New("acknowledged sequence is ahead of the ledger head")
	ErrAckGap         = errors.New("acknowledged sequence leaves a hole")
	ErrNodeDegraded   = errors.New("node is degraded")
	ErrResyncRequired = errors.New("node is behind the retention window and needs a full resync")
	ErrBadSnapshot    = errors.New("snapshot payload is invalid")
)

// RevocationEvent is one credential revocation to be fanned out.
type RevocationEvent struct {
	Seq     uint64 `json:"seq"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
}

// NodeStatus is the fan out view of one validation node.
type NodeStatus struct {
	NodeID    string `json:"node_id"`
	State     string `json:"state"`
	Watermark uint64 `json:"watermark"`
	Latest    uint64 `json:"latest"`
	Lag       uint64 `json:"lag"`
	Retries   int    `json:"retries"`
	Reason    string `json:"reason"`
}

// LedgerConfig bounds the event window, the node table and the retry budget.
type LedgerConfig struct {
	MaxEvents   int
	MaxNodes    int
	MaxRetries  int
	MaxBackfill int
}

// RevocationFanoutLedger owns the event window and the node watermarks.
type RevocationFanoutLedger struct {
	mu     sync.Mutex
	cfg    LedgerConfig
	events map[uint64]RevocationEvent
	head   uint64
	nodes  map[string]*nodeState
}

// nodeState is the per node bookkeeping kept behind the ledger lock.
type nodeState struct {
	NodeID    string `json:"node_id"`
	State     string `json:"state"`
	Watermark uint64 `json:"watermark"`
	Retries   int    `json:"retries"`
	Reason    string `json:"reason"`
}

// NewRevocationFanoutLedger builds a ledger with the given caps.
func NewRevocationFanoutLedger(cfg LedgerConfig) *RevocationFanoutLedger {
	return &RevocationFanoutLedger{
		cfg:    cfg,
		events: make(map[uint64]RevocationEvent),
		nodes:  make(map[string]*nodeState),
	}
}

// windowLow reports the oldest sequence the retention window still keeps.
func (l *RevocationFanoutLedger) windowLow() uint64 {
	if l.cfg.MaxEvents <= 0 || l.head <= uint64(l.cfg.MaxEvents) {
		return 1
	}
	return l.head - uint64(l.cfg.MaxEvents) + 1
}

// Append assigns consecutive monotonic sequence numbers to a whole batch.
func (l *RevocationFanoutLedger) Append(events []RevocationEvent) (uint64, error) {
	if len(events) == 0 {
		return 0, ErrEmptyBatch
	}
	for _, event := range events {
		if strings.TrimSpace(event.Subject) == "" ||
			strings.TrimSpace(event.Kind) == "" ||
			strings.TrimSpace(event.Reason) == "" {
			return 0, ErrInvalidEvent
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for _, event := range events {
		l.head++
		event.Seq = l.head
		l.events[event.Seq] = event
		if l.cfg.MaxEvents > 0 && len(l.events) > l.cfg.MaxEvents {
			delete(l.events, l.head-uint64(l.cfg.MaxEvents))
		}
	}
	return l.head, nil
}

// RegisterNode adds one validation node to the fan out table.
func (l *RevocationFanoutLedger) RegisterNode(nodeID string) error {
	if strings.TrimSpace(nodeID) == "" {
		return ErrBlankArg
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.nodes[nodeID]; ok {
		return ErrNodeExists
	}
	if l.cfg.MaxNodes > 0 && len(l.nodes) >= l.cfg.MaxNodes {
		return ErrNodeLimit
	}
	l.nodes[nodeID] = &nodeState{NodeID: nodeID, State: NodeActive}
	return nil
}

// Ack advances the node watermark by exactly one step at a time.
func (l *RevocationFanoutLedger) Ack(nodeID string, seq uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return ErrUnknownNode
	}
	if seq <= node.Watermark {
		return nil
	}
	if seq > l.head {
		return ErrSeqAhead
	}
	if seq != node.Watermark+1 {
		return ErrAckGap
	}
	if _, retained := l.events[seq]; !retained {
		node.State = NodeResyncReq
		return ErrResyncRequired
	}
	node.Watermark = seq
	return nil
}

// Pending lists the sequences the node still has to confirm.
func (l *RevocationFanoutLedger) Pending(nodeID string) ([]uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return nil, ErrUnknownNode
	}
	pending := make([]uint64, 0)
	for seq := node.Watermark + 1; seq <= l.head; seq++ {
		if _, retained := l.events[seq]; retained {
			pending = append(pending, seq)
		}
	}
	return pending, nil
}

// Backfill re-sends the events a node is missing, up to the requested head.
func (l *RevocationFanoutLedger) Backfill(nodeID string, to uint64) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return 0, ErrUnknownNode
	}
	if node.State == NodeDegraded {
		return 0, ErrNodeDegraded
	}

	head := l.head
	if to > head {
		to = head
	}

	count := 0
	if to > node.Watermark {
		low := l.windowLow()
		if node.Watermark+1 < low {
			node.State = NodeResyncReq
			return 0, ErrResyncRequired
		}
		for seq := node.Watermark + 1; seq <= to; seq++ {
			if _, retained := l.events[seq]; retained {
				count++
			}
		}
	}

	if count > 0 {
		node.Retries++
		if l.cfg.MaxRetries >= 0 && node.Retries > l.cfg.MaxRetries {
			node.State = NodeDegraded
			return 0, ErrNodeDegraded
		}
		if l.cfg.MaxBackfill > 0 && count > l.cfg.MaxBackfill {
			count = l.cfg.MaxBackfill
		}
	}
	return count, nil
}

// Watermark reads the last confirmed sequence of one node.
func (l *RevocationFanoutLedger) Watermark(nodeID string) (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return 0, false
	}
	return node.Watermark, true
}

// NodeStatus reads the fan out view of one node.
func (l *RevocationFanoutLedger) NodeStatus(nodeID string) (NodeStatus, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return NodeStatus{}, false
	}
	lag := uint64(0)
	if l.head > node.Watermark {
		lag = l.head - node.Watermark
	}
	return NodeStatus{
		NodeID:    node.NodeID,
		State:     node.State,
		Watermark: node.Watermark,
		Latest:    l.head,
		Lag:       lag,
		Retries:   node.Retries,
		Reason:    node.Reason,
	}, true
}

// Degrade parks a node that cannot keep up.
func (l *RevocationFanoutLedger) Degrade(nodeID, reason string) error {
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(reason) == "" {
		return ErrBlankArg
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return ErrUnknownNode
	}
	node.State = NodeDegraded
	node.Reason = reason
	return nil
}

// Reinstate brings a parked node back with a clean retry budget.
func (l *RevocationFanoutLedger) Reinstate(nodeID string) error {
	if strings.TrimSpace(nodeID) == "" {
		return ErrBlankArg
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	node, ok := l.nodes[nodeID]
	if !ok {
		return ErrUnknownNode
	}
	node.State = NodeActive
	node.Retries = 0
	node.Reason = ""
	return nil
}

// Latest reports the head sequence of the ledger.
func (l *RevocationFanoutLedger) Latest() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.head
}

// Event reads one retained event.
func (l *RevocationFanoutLedger) Event(seq uint64) (RevocationEvent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	event, ok := l.events[seq]
	return event, ok
}

// snapshotFormat is the on the wire representation of a ledger snapshot.
type snapshotFormat struct {
	Version int               `json:"version"`
	Head    uint64            `json:"head"`
	Events  []RevocationEvent `json:"events"`
	Nodes   []*nodeState      `json:"nodes"`
}

// Snapshot serialises the retained window, the head and the node table.
func (l *RevocationFanoutLedger) Snapshot() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	snapshot := snapshotFormat{
		Version: 1,
		Head:    l.head,
		Events:  make([]RevocationEvent, 0, len(l.events)),
		Nodes:   make([]*nodeState, 0, len(l.nodes)),
	}
	for seq := l.windowLow(); seq <= l.head; seq++ {
		if event, ok := l.events[seq]; ok {
			snapshot.Events = append(snapshot.Events, event)
		}
	}
	for _, node := range l.nodes {
		snapshot.Nodes = append(snapshot.Nodes, node)
	}
	sort.Slice(snapshot.Nodes, func(i, j int) bool {
		return snapshot.Nodes[i].NodeID < snapshot.Nodes[j].NodeID
	})
	return json.Marshal(snapshot)
}

// Restore replaces the ledger state with a snapshot, all or nothing.
func (l *RevocationFanoutLedger) Restore(data []byte) error {
	var snapshot snapshotFormat
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return ErrBadSnapshot
	}
	if decoder.More() {
		return ErrBadSnapshot
	}
	if snapshot.Version != 1 {
		return ErrBadSnapshot
	}

	events := make(map[uint64]RevocationEvent, len(snapshot.Events))
	var lastSeq uint64
	for _, event := range snapshot.Events {
		if event.Seq == 0 || event.Seq <= lastSeq {
			return ErrBadSnapshot
		}
		if strings.TrimSpace(event.Subject) == "" ||
			strings.TrimSpace(event.Kind) == "" ||
			strings.TrimSpace(event.Reason) == "" {
			return ErrBadSnapshot
		}
		lastSeq = event.Seq
		events[event.Seq] = event
	}
	if lastSeq != snapshot.Head {
		return ErrBadSnapshot
	}

	nodes := make(map[string]*nodeState, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node == nil || strings.TrimSpace(node.NodeID) == "" {
			return ErrBadSnapshot
		}
		if _, exists := nodes[node.NodeID]; exists {
			return ErrBadSnapshot
		}
		switch node.State {
		case NodeActive, NodeDegraded, NodeResyncReq:
		default:
			return ErrBadSnapshot
		}
		if node.Watermark > snapshot.Head || node.Retries < 0 {
			return ErrBadSnapshot
		}
		nodeCopy := *node
		nodes[node.NodeID] = &nodeCopy
	}
	if l.cfg.MaxNodes > 0 && len(nodes) > l.cfg.MaxNodes {
		return ErrBadSnapshot
	}
	if l.cfg.MaxEvents > 0 && len(events) > l.cfg.MaxEvents {
		return ErrBadSnapshot
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.head = snapshot.Head
	l.events = events
	l.nodes = nodes
	return nil
}
