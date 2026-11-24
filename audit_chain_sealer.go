// Package task seals audit records into a hash chain, signs checkpoints and
// verifies the surviving range after old records were trimmed.
package task

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

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

// sealedEntry keeps the original record next to its chain position so that
// Verify can recompute the digest long after the record was sealed.
type sealedEntry struct {
	Record
	Prev   [32]byte `json:"prev"`
	Digest [32]byte `json:"digest"`
}

// persistedState is the snapshot layout written by Snapshot and read by
// Restore. It captures the head, every retained entry and the checkpoints.
type persistedState struct {
	HeadSeq     uint64        `json:"head_seq"`
	HeadDigest  [32]byte      `json:"head_digest"`
	Entries     []sealedEntry `json:"entries"`
	Checkpoints []Checkpoint  `json:"checkpoints"`
}

// AuditChainSealer owns the chain, the checkpoints and the retention window.
type AuditChainSealer struct {
	mu          sync.Mutex
	opts        Options
	entries     []sealedEntry
	headSeq     uint64
	headDigest  [32]byte
	checkpoints []Checkpoint
}

// NewAuditChainSealer builds a sealer with the given key and caps.
func NewAuditChainSealer(opts Options) *AuditChainSealer {
	return &AuditChainSealer{opts: opts}
}

// digestFor computes the chain digest for one record: SHA-256 over the
// previous digest, the 8-byte big-endian sequence, tenant, action and payload,
// with a 0x00 byte separating every field.
func digestFor(prev [32]byte, rec Record) [32]byte {
	h := sha256.New()
	h.Write(prev[:])
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], rec.Seq)
	h.Write(seq[:])
	h.Write([]byte{0x00})
	h.Write([]byte(rec.Tenant))
	h.Write([]byte{0x00})
	h.Write([]byte(rec.Action))
	h.Write([]byte{0x00})
	h.Write([]byte(rec.Payload))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// signatureFor derives the HMAC-SHA256 checkpoint signature over the signed
// head position. The signature is always computed before the checkpoint is
// published to the checkpoint table.
func (s *AuditChainSealer) signatureFor(seq uint64, digest [32]byte) [32]byte {
	mac := hmac.New(sha256.New, s.opts.Key)
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], seq)
	mac.Write(seqBuf[:])
	mac.Write(digest[:])
	var sig [32]byte
	copy(sig[:], mac.Sum(nil))
	return sig
}

// earliestLocked returns the smallest retained sequence, or 0 when the chain
// is empty. Retained entries are always contiguous.
func (s *AuditChainSealer) earliestLocked() uint64 {
	if len(s.entries) == 0 {
		return 0
	}
	return s.entries[0].Seq
}

// Append seals one record at the next sequence number.
func (s *AuditChainSealer) Append(rec Record) (Sealed, error) {
	if strings.TrimSpace(rec.Tenant) == "" || strings.TrimSpace(rec.Action) == "" {
		return Sealed{}, ErrBadRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.Seq != s.headSeq+1 {
		return Sealed{}, ErrSeqConflict
	}
	if len(s.entries) >= s.opts.MaxRecords {
		return Sealed{}, ErrRecordLimit
	}

	prev := s.headDigest
	digest := digestFor(prev, rec)
	entry := sealedEntry{Record: rec, Prev: prev, Digest: digest}

	// Record and head move together under the same lock: a reader can never
	// observe a head that points at a record that has not been stored yet.
	s.entries = append(s.entries, entry)
	s.headSeq = rec.Seq
	s.headDigest = digest

	return Sealed{Seq: rec.Seq, Prev: prev, Digest: digest}, nil
}

// Checkpoint signs the current head.
func (s *AuditChainSealer) Checkpoint() (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := Checkpoint{
		Seq:    s.headSeq,
		Digest: s.headDigest,
		At:     time.Now().Unix(),
	}
	cp.Signature = s.signatureFor(cp.Seq, cp.Digest)

	s.checkpoints = append(s.checkpoints, cp)
	if len(s.checkpoints) > s.opts.MaxCheckpoints {
		if s.opts.MaxCheckpoints <= 0 {
			s.checkpoints = s.checkpoints[:0]
		} else {
			drop := len(s.checkpoints) - s.opts.MaxCheckpoints
			s.checkpoints = append([]Checkpoint(nil), s.checkpoints[drop:]...)
		}
	}
	return cp, nil
}

// Verify recomputes the chain between two sequence numbers.
func (s *AuditChainSealer) Verify(from, to uint64) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if from < 1 || from > to || to > s.headSeq {
		return Report{}, ErrRangeInvalid
	}

	rep := Report{From: from, To: to, Valid: true}
	earliest := s.earliestLocked()
	var prevSeq uint64
	var prevDigest [32]byte
	havePrev := false

	for seq := from; seq <= to; seq++ {
		if seq < earliest || seq > s.headSeq {
			// Sequences trimmed by the retention window are reported as gaps,
			// never as tampering, and do not break the surviving chain.
			rep.Gaps = append(rep.Gaps, seq)
			havePrev = false
			continue
		}

		entry := s.entries[int(seq-earliest)]

		// Anchor the first retained record on its stored predecessor digest
		// (the hand-off at the truncation point). Consecutive retained records
		// must additionally link to the previous retained digest.
		if havePrev && prevSeq+1 == seq && entry.Prev != prevDigest {
			rep.Valid = false
			rep.Reason = ReasonDigestMismatch
		}
		if recomputed := digestFor(entry.Prev, entry.Record); recomputed != entry.Digest {
			rep.Valid = false
			rep.Reason = ReasonDigestMismatch
		}

		rep.Checked++
		prevSeq = seq
		prevDigest = entry.Digest
		havePrev = true
	}

	return rep, nil
}

// Truncate drops records older than the given sequence number.
func (s *AuditChainSealer) Truncate(before uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if before > s.headSeq {
		return ErrTruncateAhead
	}
	earliest := s.earliestLocked()
	if len(s.entries) == 0 || before <= earliest {
		return nil
	}

	cut := int(before - earliest)
	s.entries = append([]sealedEntry(nil), s.entries[cut:]...)
	return nil
}

// Head reports the latest sealed sequence and digest.
func (s *AuditChainSealer) Head() (uint64, [32]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headSeq, s.headDigest
}

// Snapshot serialises the head, retained records and checkpoints.
func (s *AuditChainSealer) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := persistedState{
		HeadSeq:     s.headSeq,
		HeadDigest:  s.headDigest,
		Entries:     append([]sealedEntry(nil), s.entries...),
		Checkpoints: append([]Checkpoint(nil), s.checkpoints...),
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, ErrBadSnapshot
	}
	return payload, nil
}

// validate checks snapshot structure only. Cryptographic chain re-verification
// is deliberately left to Verify so a tampered snapshot can be restored and
// then audited.
func (p persistedState) validate() error {
	if len(p.Entries) == 0 {
		if p.HeadSeq != 0 || p.HeadDigest != ([32]byte{}) {
			return ErrBadSnapshot
		}
		return nil
	}

	firstSeq := p.Entries[0].Seq
	if firstSeq < 1 {
		return ErrBadSnapshot
	}
	for i, entry := range p.Entries {
		if entry.Seq != firstSeq+uint64(i) {
			return ErrBadSnapshot
		}
	}
	last := p.Entries[len(p.Entries)-1]
	if p.HeadSeq != last.Seq || p.HeadDigest != last.Digest {
		return ErrBadSnapshot
	}
	return nil
}

// Restore replaces the sealer state from a snapshot.
func (s *AuditChainSealer) Restore(payload []byte) error {
	var state persistedState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ErrBadSnapshot
	}
	if err := state.validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	checkpoints := append([]Checkpoint(nil), state.Checkpoints...)
	if len(checkpoints) > s.opts.MaxCheckpoints {
		if s.opts.MaxCheckpoints <= 0 {
			checkpoints = nil
		} else {
			drop := len(checkpoints) - s.opts.MaxCheckpoints
			checkpoints = checkpoints[drop:]
		}
	}

	s.entries = append([]sealedEntry(nil), state.Entries...)
	s.headSeq = state.HeadSeq
	s.headDigest = state.HeadDigest
	s.checkpoints = checkpoints
	return nil
}

// VerifyCheckpoint checks the signature of one checkpoint.
func (s *AuditChainSealer) VerifyCheckpoint(cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := s.signatureFor(cp.Seq, cp.Digest)
	if !hmac.Equal(want[:], cp.Signature[:]) {
		return ErrBadCheckpoint
	}
	return nil
}

// Records lists the retained chain entries in sequence order.
func (s *AuditChainSealer) Records() []Sealed {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Sealed, len(s.entries))
	for i, entry := range s.entries {
		out[i] = Sealed{Seq: entry.Seq, Prev: entry.Prev, Digest: entry.Digest}
	}
	return out
}

// Earliest reports the smallest retained sequence number.
func (s *AuditChainSealer) Earliest() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.earliestLocked()
}

// Checkpoints lists the stored checkpoints in creation order.
func (s *AuditChainSealer) Checkpoints() []Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Checkpoint(nil), s.checkpoints...)
}
