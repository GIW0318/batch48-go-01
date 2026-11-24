// Package task seals audit records into a hash chain, signs checkpoints and
// verifies the surviving range after old records were trimmed.
package task

import "errors"

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

// Reason codes carried by a verification report.
const (
	ReasonDigestMismatch = "digest_mismatch"
	ReasonGap            = "gap"
)

// Errors returned by the sealer.
var (
	ErrBadRecord     = errors.New("audit record is invalid")
	ErrSeqConflict   = errors.New("record sequence is not the next one")
	ErrRecordLimit   = errors.New("retained record limit reached")
	ErrRangeInvalid  = errors.New("verify range is invalid")
	ErrBadCheckpoint = errors.New("checkpoint signature does not match")
	ErrTruncateAhead = errors.New("truncate point is ahead of the chain head")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

// Record is one audit entry submitted for sealing.
type Record struct {
	Seq     uint64 `json:"seq"`
	Tenant  string `json:"tenant"`
	Action  string `json:"action"`
	Payload string `json:"payload"`
}

// Sealed is the chain position a record received.
type Sealed struct {
	Seq    uint64   `json:"seq"`
	Prev   [32]byte `json:"prev"`
	Digest [32]byte `json:"digest"`
}

// Checkpoint is a signed claim about the chain head.
type Checkpoint struct {
	Seq       uint64   `json:"seq"`
	Digest    [32]byte `json:"digest"`
	Signature [32]byte `json:"signature"`
	At        int64    `json:"at"`
}

// Report is the outcome of verifying one range.
type Report struct {
	From    uint64   `json:"from"`
	To      uint64   `json:"to"`
	Checked int      `json:"checked"`
	Gaps    []uint64 `json:"gaps,omitempty"`
	Valid   bool     `json:"valid"`
	Reason  string   `json:"reason,omitempty"`
}

// Options configures the signing key and the two retention caps.
type Options struct {
	Key            []byte `json:"key"`
	MaxRecords     int    `json:"max_records"`
	MaxCheckpoints int    `json:"max_checkpoints"`
}

// AuditChainSealer owns the chain, the checkpoints and the retention window.
type AuditChainSealer struct {
	opts Options
}

// NewAuditChainSealer builds a sealer with the given key and caps.
func NewAuditChainSealer(opts Options) *AuditChainSealer {
	return &AuditChainSealer{opts: opts}
}

// Append seals one record at the next sequence number.
func (s *AuditChainSealer) Append(rec Record) (Sealed, error) {
	return Sealed{}, errNotImplemented
}

// Checkpoint signs the current head.
func (s *AuditChainSealer) Checkpoint() (Checkpoint, error) {
	return Checkpoint{}, errNotImplemented
}

// Verify recomputes the chain between two sequence numbers.
func (s *AuditChainSealer) Verify(from, to uint64) (Report, error) {
	return Report{}, errNotImplemented
}

// Truncate drops records older than the given sequence number.
func (s *AuditChainSealer) Truncate(before uint64) error {
	return errNotImplemented
}

// Head reports the latest sealed sequence and digest.
func (s *AuditChainSealer) Head() (uint64, [32]byte) {
	return 0, [32]byte{}
}

// Snapshot serialises the head, retained records and checkpoints.
func (s *AuditChainSealer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the sealer state from a snapshot.
func (s *AuditChainSealer) Restore(payload []byte) error {
	return errNotImplemented
}

// VerifyCheckpoint checks the signature of one checkpoint.
func (s *AuditChainSealer) VerifyCheckpoint(cp Checkpoint) error {
	return errNotImplemented
}

// Records lists the retained chain entries in sequence order.
func (s *AuditChainSealer) Records() []Sealed {
	return nil
}

// Earliest reports the smallest retained sequence number.
func (s *AuditChainSealer) Earliest() uint64 {
	return 0
}

// Checkpoints lists the stored checkpoints in creation order.
func (s *AuditChainSealer) Checkpoints() []Checkpoint {
	return nil
}
