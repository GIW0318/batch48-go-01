package task

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-b-08"
)

func extraOptions() impl.Options {
	return impl.Options{
		Key:            []byte("audit-chain-extra-key"),
		MaxRecords:     128,
		MaxCheckpoints: 6,
	}
}

func mustAppend(t *testing.T, s *impl.AuditChainSealer, seq uint64, payload string) impl.Sealed {
	t.Helper()
	sealed, err := s.Append(impl.Record{
		Seq:     seq,
		Tenant:  "tenant-extra",
		Action:  "grant",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("append seq %d: %v", seq, err)
	}
	return sealed
}

// Appends, checkpoints and head reads interleave under the race detector.
// Every published checkpoint must match a real chain position and verify.
func TestConcurrentAppendAndCheckpoint(t *testing.T) {
	s := impl.NewAuditChainSealer(extraOptions())
	const total = 100

	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for seq := uint64(1); seq <= total; seq++ {
			if _, err := s.Append(impl.Record{
				Seq:     seq,
				Tenant:  "tenant-extra",
				Action:  "grant",
				Payload: "payload",
			}); err != nil {
				t.Errorf("append seq %d: %v", seq, err)
				return
			}
		}
	}()

	var readers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < total; i++ {
				if _, err := s.Checkpoint(); err != nil {
					t.Errorf("checkpoint: %v", err)
					return
				}
				if seq, _ := s.Head(); seq > total {
					t.Errorf("head seq %d beyond appended range", seq)
					return
				}
			}
		}()
	}

	writer.Wait()
	readers.Wait()

	headSeq, headDigest := s.Head()
	if headSeq != total {
		t.Fatalf("head seq = %d, want %d", headSeq, total)
	}
	if len(s.Records()) != total {
		t.Fatalf("retained records = %d, want %d", len(s.Records()), total)
	}

	bySeq := make(map[uint64][32]byte, len(s.Records()))
	for _, sealed := range s.Records() {
		bySeq[sealed.Seq] = sealed.Digest
	}
	if bySeq[total] != headDigest {
		t.Fatalf("head digest does not pair with the last record")
	}

	cps := s.Checkpoints()
	if len(cps) > 6 {
		t.Fatalf("checkpoint count = %d, hard cap is 6", len(cps))
	}
	prevSeq := uint64(0)
	for _, cp := range cps {
		if cp.Seq < prevSeq {
			t.Fatalf("checkpoints out of creation order: %d after %d", cp.Seq, prevSeq)
		}
		prevSeq = cp.Seq
		if err := s.VerifyCheckpoint(cp); err != nil {
			t.Fatalf("checkpoint at %d must verify: %v", cp.Seq, err)
		}
		if cp.Seq == 0 {
			if cp.Digest != ([32]byte{}) {
				t.Fatalf("zero-head checkpoint must carry the zero digest")
			}
			continue
		}
		if want, ok := bySeq[cp.Seq]; !ok || want != cp.Digest {
			t.Fatalf("checkpoint %d digest does not match the sealed record", cp.Seq)
		}
	}

	rep, err := s.Verify(1, total)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rep.Valid || rep.Checked != total || len(rep.Gaps) != 0 {
		t.Fatalf("full chain report = %+v, want valid with %d checked", rep, total)
	}
}

// After truncation the surviving chain verifies from its stored predecessor
// anchor: trimmed sequences are gaps, never tampering.
func TestVerifyAfterTruncateAlignsAtAnchor(t *testing.T) {
	s := impl.NewAuditChainSealer(extraOptions())
	digests := map[uint64][32]byte{}
	for seq := uint64(1); seq <= 6; seq++ {
		sealed := mustAppend(t, s, seq, "payload-"+string(rune('0'+seq)))
		digests[seq] = sealed.Digest
	}

	if err := s.Truncate(4); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if got := s.Earliest(); got != 4 {
		t.Fatalf("earliest = %d, want 4", got)
	}
	kept := s.Records()
	if len(kept) != 3 || kept[0].Seq != 4 {
		t.Fatalf("kept records = %+v, want seq 4,5,6", kept)
	}
	if kept[0].Prev != digests[3] {
		t.Fatalf("first retained record must anchor on the digest of seq 3, not zero")
	}

	rep, err := s.Verify(4, 6)
	if err != nil {
		t.Fatalf("verify surviving range: %v", err)
	}
	if rep.From != 4 || rep.To != 6 {
		t.Fatalf("report range = %d..%d, want 4..6", rep.From, rep.To)
	}
	if !rep.Valid || rep.Checked != 3 || len(rep.Gaps) != 0 || rep.Reason != "" {
		t.Fatalf("surviving range report = %+v, want valid with 3 checked", rep)
	}

	rep, err = s.Verify(1, 6)
	if err != nil {
		t.Fatalf("verify across the trimmed prefix: %v", err)
	}
	if !rep.Valid {
		t.Fatalf("trimmed prefix must not invalidate the chain, reason = %q", rep.Reason)
	}
	if rep.Checked != 3 || len(rep.Gaps) != 3 {
		t.Fatalf("trimmed range report = %+v, want 3 checked and 3 gaps", rep)
	}

	rep, err = s.Verify(3, 6)
	if err != nil {
		t.Fatalf("verify starting inside the gap: %v", err)
	}
	if !rep.Valid || len(rep.Gaps) != 1 || rep.Gaps[0] != 3 {
		t.Fatalf("partial-gap report = %+v, want one gap at seq 3", rep)
	}

	if _, err := s.Verify(4, 7); !errors.Is(err, impl.ErrRangeInvalid) {
		t.Fatalf("to beyond head must fail with ErrRangeInvalid, got %v", err)
	}

	// Tampering with a surviving payload after a restart must still surface.
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	tampered := bytes.ReplaceAll(snap, []byte("payload-5"), []byte("payload-X"))
	other := impl.NewAuditChainSealer(extraOptions())
	if err := other.Restore(tampered); err != nil {
		t.Fatalf("restore tampered snapshot: %v", err)
	}
	rep, err = other.Verify(4, 6)
	if err != nil {
		t.Fatalf("verify tampered range: %v", err)
	}
	if rep.Valid || rep.Reason != impl.ReasonDigestMismatch {
		t.Fatalf("tampered report = %+v, want %q", rep, impl.ReasonDigestMismatch)
	}
}

// A restored sealer keeps the head pairing, rejects a repeated sequence and
// then continues the chain without overwriting any sealed record.
func TestSnapshotRestoreThenAppendContinues(t *testing.T) {
	s := impl.NewAuditChainSealer(extraOptions())
	for seq := uint64(1); seq <= 3; seq++ {
		mustAppend(t, s, seq, "payload")
	}
	cp, err := s.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	other := impl.NewAuditChainSealer(extraOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	before := other.Records()

	if _, err := other.Append(impl.Record{Seq: 3, Tenant: "tenant-extra", Action: "grant", Payload: "rewrite"}); !errors.Is(err, impl.ErrSeqConflict) {
		t.Fatalf("repeated seq after restore must fail with ErrSeqConflict, got %v", err)
	}
	if seq, _ := other.Head(); seq != 3 {
		t.Fatalf("head moved after rejected append: %d", seq)
	}
	if got := other.Records(); len(got) != len(before) || got[2].Digest != before[2].Digest {
		t.Fatalf("sealed record was replaced by the rejected append")
	}

	prevHeadSeq, prevHeadDigest := other.Head()
	sealed, err := other.Append(impl.Record{Seq: 4, Tenant: "tenant-extra", Action: "grant", Payload: "payload"})
	if err != nil {
		t.Fatalf("append after restore: %v", err)
	}
	if prevHeadSeq != 3 || sealed.Prev != prevHeadDigest {
		t.Fatalf("new record must chain from the restored head digest")
	}
	if headSeq, headDigest := other.Head(); headSeq != 4 || headDigest != sealed.Digest {
		t.Fatalf("head must pair with the newly sealed record")
	}

	rep, err := other.Verify(1, 4)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rep.Valid || rep.Checked != 4 {
		t.Fatalf("restored-and-extended report = %+v", rep)
	}

	restoredCPs := other.Checkpoints()
	if len(restoredCPs) != 1 {
		t.Fatalf("restored checkpoints = %d, want 1", len(restoredCPs))
	}
	if err := other.VerifyCheckpoint(cp); err != nil {
		t.Fatalf("restored checkpoint must still verify: %v", err)
	}
}

// Trimming frees retention capacity and the next append continues the chain
// from the surviving head instead of restarting at a zero predecessor.
func TestTruncateThenAppendContinues(t *testing.T) {
	opts := extraOptions()
	opts.MaxRecords = 5
	s := impl.NewAuditChainSealer(opts)

	var digest2 [32]byte
	for seq := uint64(1); seq <= 5; seq++ {
		sealed := mustAppend(t, s, seq, "payload")
		if seq == 2 {
			digest2 = sealed.Digest
		}
	}
	if _, err := s.Append(impl.Record{Seq: 6, Tenant: "tenant-extra", Action: "grant", Payload: "payload"}); !errors.Is(err, impl.ErrRecordLimit) {
		t.Fatalf("full retention window must fail with ErrRecordLimit, got %v", err)
	}

	if err := s.Truncate(3); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if got := len(s.Records()); got != 3 {
		t.Fatalf("retained records = %d, want 3", got)
	}
	if s.Records()[0].Prev != digest2 {
		t.Fatalf("truncation must preserve the predecessor anchor of seq 3")
	}

	headBefore, headDigestBefore := s.Head()
	sealed, err := s.Append(impl.Record{Seq: 6, Tenant: "tenant-extra", Action: "grant", Payload: "payload"})
	if err != nil {
		t.Fatalf("append after truncate: %v", err)
	}
	if headBefore != 5 || sealed.Prev != headDigestBefore {
		t.Fatalf("seq 6 must chain from the surviving seq 5 digest")
	}

	rep, err := s.Verify(3, 6)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rep.Valid || rep.Checked != 4 || len(rep.Gaps) != 0 {
		t.Fatalf("extended surviving report = %+v, want 4 checked without gaps", rep)
	}
	if seq, digest := s.Head(); seq != 6 || digest != sealed.Digest {
		t.Fatalf("head = (%d, ...), want seq 6 paired with the new digest", seq)
	}
}
