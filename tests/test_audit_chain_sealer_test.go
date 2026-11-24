package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-b-08"
)

func chainOptions() impl.Options {
	return impl.Options{
		Key:            []byte("audit-chain-test-key"),
		MaxRecords:     32,
		MaxCheckpoints: 4,
	}
}

func sealDigestFor(prev [32]byte, rec impl.Record) [32]byte {
	h := sha256.New()
	h.Write(prev[:])
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], rec.Seq)
	h.Write(seq[:])
	h.Write([]byte{0})
	h.Write([]byte(rec.Tenant))
	h.Write([]byte{0})
	h.Write([]byte(rec.Action))
	h.Write([]byte{0})
	h.Write([]byte(rec.Payload))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func appendSealed(t *testing.T, s *impl.AuditChainSealer, rec impl.Record) impl.Sealed {
	t.Helper()
	sealed, err := s.Append(rec)
	if err != nil {
		t.Fatalf("append seq %d: %v", rec.Seq, err)
	}
	return sealed
}

func TestAppendFirstRecordSealsAgainstZeroPrev(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	rec := impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "role-admin"}
	sealed, err := s.Append(rec)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	if sealed.Seq != 1 {
		t.Fatalf("sealed sequence = %d, want 1", sealed.Seq)
	}
	if sealed.Prev != ([32]byte{}) {
		t.Fatalf("first record must link to the zero digest")
	}
	if want := sealDigestFor([32]byte{}, rec); sealed.Digest != want {
		t.Fatalf("first digest does not match the documented encoding")
	}
}

func TestAppendLinksEachRecordToPreviousDigest(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	first := appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	second := appendSealed(t, s, impl.Record{Seq: 2, Tenant: "tenant-b", Action: "revoke", Payload: "p2"})
	if second.Prev != first.Digest {
		t.Fatalf("second record prev does not equal the first digest")
	}
	if want := sealDigestFor(first.Digest, impl.Record{Seq: 2, Tenant: "tenant-b", Action: "revoke", Payload: "p2"}); second.Digest != want {
		t.Fatalf("second digest does not chain from the first")
	}
}

func TestAppendRejectsSkippedSequence(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	first := appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	if _, err := s.Append(impl.Record{Seq: 3, Tenant: "tenant-a", Action: "grant", Payload: "p3"}); !errors.Is(err, impl.ErrSeqConflict) {
		t.Fatalf("skipped sequence must fail with ErrSeqConflict, got %v", err)
	}
	seq, digest := s.Head()
	if seq != 1 {
		t.Fatalf("head sequence = %d, want 1", seq)
	}
	if digest != first.Digest {
		t.Fatalf("head digest moved after a rejected append")
	}
}

func TestAppendRejectsBlankRecord(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	if _, err := s.Append(impl.Record{Seq: 1, Tenant: "  ", Action: "grant", Payload: "p1"}); !errors.Is(err, impl.ErrBadRecord) {
		t.Fatalf("blank tenant must fail with ErrBadRecord, got %v", err)
	}
	if _, err := s.Append(impl.Record{Seq: 1, Tenant: "tenant-a", Action: "", Payload: "p1"}); !errors.Is(err, impl.ErrBadRecord) {
		t.Fatalf("blank action must fail with ErrBadRecord, got %v", err)
	}
	if seq, _ := s.Head(); seq != 0 {
		t.Fatalf("rejected records must not advance the head, got %d", seq)
	}
}

func TestAppendEnforcesRecordLimit(t *testing.T) {
	opts := chainOptions()
	opts.MaxRecords = 2
	s := impl.NewAuditChainSealer(opts)
	appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	appendSealed(t, s, impl.Record{Seq: 2, Tenant: "tenant-a", Action: "grant", Payload: "p2"})
	if _, err := s.Append(impl.Record{Seq: 3, Tenant: "tenant-a", Action: "grant", Payload: "p3"}); !errors.Is(err, impl.ErrRecordLimit) {
		t.Fatalf("third record must fail with ErrRecordLimit, got %v", err)
	}
	if got := len(s.Records()); got != 2 {
		t.Fatalf("retained records = %d, want 2", got)
	}
}

func TestHeadPublishesLatestSeqAndDigest(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	second := appendSealed(t, s, impl.Record{Seq: 2, Tenant: "tenant-a", Action: "grant", Payload: "p2"})
	seq, digest := s.Head()
	if seq != 2 {
		t.Fatalf("head sequence = %d, want 2", seq)
	}
	if digest != second.Digest {
		t.Fatalf("head digest does not match the last sealed record")
	}
}

func TestCheckpointSignsHeadAndRejectsTamper(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	second := appendSealed(t, s, impl.Record{Seq: 2, Tenant: "tenant-a", Action: "grant", Payload: "p2"})
	cp, err := s.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if cp.Seq != 2 {
		t.Fatalf("checkpoint sequence = %d, want 2", cp.Seq)
	}
	if cp.Digest != second.Digest {
		t.Fatalf("checkpoint digest does not match the head")
	}
	if cp.Signature == ([32]byte{}) {
		t.Fatalf("checkpoint must carry a signature")
	}
	if err := s.VerifyCheckpoint(cp); err != nil {
		t.Fatalf("fresh checkpoint must verify, got %v", err)
	}
	bad := cp
	bad.Digest[0] ^= 0xff
	if err := s.VerifyCheckpoint(bad); !errors.Is(err, impl.ErrBadCheckpoint) {
		t.Fatalf("tampered checkpoint must fail with ErrBadCheckpoint, got %v", err)
	}
}

func TestCheckpointsCappedAtMax(t *testing.T) {
	opts := chainOptions()
	opts.MaxCheckpoints = 2
	s := impl.NewAuditChainSealer(opts)
	for seq := uint64(1); seq <= 3; seq++ {
		appendSealed(t, s, impl.Record{Seq: seq, Tenant: "tenant-a", Action: "grant", Payload: "p"})
		if _, err := s.Checkpoint(); err != nil {
			t.Fatalf("checkpoint at %d: %v", seq, err)
		}
	}
	cps := s.Checkpoints()
	if len(cps) != 2 {
		t.Fatalf("checkpoint count = %d, want 2", len(cps))
	}
	if cps[0].Seq != 2 || cps[1].Seq != 3 {
		t.Fatalf("kept checkpoints = %d,%d, want 2,3", cps[0].Seq, cps[1].Seq)
	}
}

func TestVerifyReportsValidFullRange(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	for seq := uint64(1); seq <= 3; seq++ {
		appendSealed(t, s, impl.Record{Seq: seq, Tenant: "tenant-a", Action: "grant", Payload: "p"})
	}
	rep, err := s.Verify(1, 3)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rep.Valid {
		t.Fatalf("untouched chain must verify, reason = %q", rep.Reason)
	}
	if rep.Checked != 3 {
		t.Fatalf("checked records = %d, want 3", rep.Checked)
	}
	if len(rep.Gaps) != 0 {
		t.Fatalf("unexpected gaps: %v", rep.Gaps)
	}
	if rep.Reason != "" {
		t.Fatalf("valid chain must not carry a reason, got %q", rep.Reason)
	}
}

func TestVerifyDetectsTamperedPayloadAfterRestore(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "pay-one"})
	appendSealed(t, s, impl.Record{Seq: 2, Tenant: "tenant-a", Action: "grant", Payload: "pay-alpha-1"})
	appendSealed(t, s, impl.Record{Seq: 3, Tenant: "tenant-a", Action: "grant", Payload: "pay-three"})
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	tampered := bytes.ReplaceAll(snap, []byte("pay-alpha-1"), []byte("pay-alphb-1"))
	other := impl.NewAuditChainSealer(chainOptions())
	if err := other.Restore(tampered); err != nil {
		t.Fatalf("restore tampered snapshot: %v", err)
	}
	rep, err := other.Verify(1, 3)
	if err != nil {
		t.Fatalf("verify after restore: %v", err)
	}
	if rep.Valid {
		t.Fatalf("tampered payload must not verify")
	}
	if rep.Reason != impl.ReasonDigestMismatch {
		t.Fatalf("report reason = %q, want %q", rep.Reason, impl.ReasonDigestMismatch)
	}
}

func TestVerifyRejectsOutOfRangeRequest(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	for seq := uint64(1); seq <= 3; seq++ {
		appendSealed(t, s, impl.Record{Seq: seq, Tenant: "tenant-a", Action: "grant", Payload: "p"})
	}
	if _, err := s.Verify(0, 1); !errors.Is(err, impl.ErrRangeInvalid) {
		t.Fatalf("from 0 must fail with ErrRangeInvalid, got %v", err)
	}
	if _, err := s.Verify(2, 1); !errors.Is(err, impl.ErrRangeInvalid) {
		t.Fatalf("from above to must fail with ErrRangeInvalid, got %v", err)
	}
	if _, err := s.Verify(1, 9); !errors.Is(err, impl.ErrRangeInvalid) {
		t.Fatalf("to beyond head must fail with ErrRangeInvalid, got %v", err)
	}
}

func TestTruncateDropsOldRecords(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	for seq := uint64(1); seq <= 4; seq++ {
		appendSealed(t, s, impl.Record{Seq: seq, Tenant: "tenant-a", Action: "grant", Payload: "p"})
	}
	if err := s.Truncate(3); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	kept := s.Records()
	if len(kept) != 2 {
		t.Fatalf("retained records = %d, want 2", len(kept))
	}
	if kept[0].Seq != 3 || kept[1].Seq != 4 {
		t.Fatalf("retained sequences = %d,%d, want 3,4", kept[0].Seq, kept[1].Seq)
	}
	if got := s.Earliest(); got != 3 {
		t.Fatalf("earliest retained sequence = %d, want 3", got)
	}
	if seq, _ := s.Head(); seq != 4 {
		t.Fatalf("head after truncate = %d, want 4", seq)
	}
}

func TestTruncateRejectsAheadOfHead(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	appendSealed(t, s, impl.Record{Seq: 2, Tenant: "tenant-a", Action: "grant", Payload: "p2"})
	if err := s.Truncate(99); !errors.Is(err, impl.ErrTruncateAhead) {
		t.Fatalf("truncate beyond head must fail with ErrTruncateAhead, got %v", err)
	}
	if seq, _ := s.Head(); seq != 2 {
		t.Fatalf("head after rejected truncate = %d, want 2", seq)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	for seq := uint64(1); seq <= 3; seq++ {
		appendSealed(t, s, impl.Record{Seq: seq, Tenant: "tenant-a", Action: "grant", Payload: "p"})
	}
	if _, err := s.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewAuditChainSealer(chainOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if seq, _ := other.Head(); seq != 3 {
		t.Fatalf("restored head = %d, want 3", seq)
	}
	if got := len(other.Records()); got != 3 {
		t.Fatalf("restored records = %d, want 3", got)
	}
	if got := len(other.Checkpoints()); got != 1 {
		t.Fatalf("restored checkpoints = %d, want 1", got)
	}
	rep, err := other.Verify(1, 3)
	if err != nil {
		t.Fatalf("verify after restore: %v", err)
	}
	if !rep.Valid {
		t.Fatalf("restored chain must verify, reason = %q", rep.Reason)
	}
}

func TestRestoreRejectsCorruptSnapshotKeepsHead(t *testing.T) {
	s := impl.NewAuditChainSealer(chainOptions())
	sealed := appendSealed(t, s, impl.Record{Seq: 1, Tenant: "tenant-a", Action: "grant", Payload: "p1"})
	if err := s.Restore([]byte("}{ broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	seq, digest := s.Head()
	if seq != 1 {
		t.Fatalf("head sequence after rejected restore = %d, want 1", seq)
	}
	if digest != sealed.Digest {
		t.Fatalf("head digest changed after rejected restore")
	}
}
