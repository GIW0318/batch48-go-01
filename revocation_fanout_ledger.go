// Package task fans revocation events out to validation nodes and tracks the
// per node confirmation watermark with a bounded event retention window.
package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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
	cfg LedgerConfig
}

// NewRevocationFanoutLedger builds a ledger with the given caps.
func NewRevocationFanoutLedger(cfg LedgerConfig) *RevocationFanoutLedger {
	return &RevocationFanoutLedger{cfg: cfg}
}

// Append assigns consecutive monotonic sequence numbers to a whole batch.
func (l *RevocationFanoutLedger) Append(events []RevocationEvent) (uint64, error) {
	return 0, errNotImplemented
}

// RegisterNode adds one validation node to the fan out table.
func (l *RevocationFanoutLedger) RegisterNode(nodeID string) error {
	return errNotImplemented
}

// Ack advances the node watermark by exactly one step at a time.
func (l *RevocationFanoutLedger) Ack(nodeID string, seq uint64) error {
	return errNotImplemented
}

// Pending lists the sequences the node still has to confirm.
func (l *RevocationFanoutLedger) Pending(nodeID string) ([]uint64, error) {
	return nil, errNotImplemented
}

// Backfill re-sends the events a node is missing, up to the requested head.
func (l *RevocationFanoutLedger) Backfill(nodeID string, to uint64) (int, error) {
	return 0, errNotImplemented
}

// Watermark reads the last confirmed sequence of one node.
func (l *RevocationFanoutLedger) Watermark(nodeID string) (uint64, bool) {
	return 0, false
}

// NodeStatus reads the fan out view of one node.
func (l *RevocationFanoutLedger) NodeStatus(nodeID string) (NodeStatus, bool) {
	return NodeStatus{}, false
}

// Degrade parks a node that cannot keep up.
func (l *RevocationFanoutLedger) Degrade(nodeID, reason string) error {
	return errNotImplemented
}

// Reinstate brings a parked node back with a clean retry budget.
func (l *RevocationFanoutLedger) Reinstate(nodeID string) error {
	return errNotImplemented
}

// Latest reports the head sequence of the ledger.
func (l *RevocationFanoutLedger) Latest() uint64 {
	return 0
}

// Event reads one retained event.
func (l *RevocationFanoutLedger) Event(seq uint64) (RevocationEvent, bool) {
	return RevocationEvent{}, false
}

// Snapshot serialises the retained window, the head and the node table.
func (l *RevocationFanoutLedger) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the ledger state with a snapshot, all or nothing.
func (l *RevocationFanoutLedger) Restore(data []byte) error {
	return errNotImplemented
}
