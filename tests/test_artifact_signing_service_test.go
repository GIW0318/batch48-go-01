package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-d-05"
)

var (
	artifactDigest = "sha256:" + strings.Repeat("a", 64)
	provDigest     = "sha256:" + strings.Repeat("b", 64)
)

func registerProdKey(t *testing.T, s *impl.SigningService, quota int) {
	t.Helper()
	spec := impl.KeySpec{Partition: impl.EnvProd, Generation: 1, KeyID: "key-prod-1", Algorithm: "ecdsa-p256", Quota: quota}
	if err := s.RegisterKey(spec); err != nil {
		t.Fatalf("register prod key: %v", err)
	}
}

func signProd(t *testing.T, s *impl.SigningService) impl.Receipt {
	t.Helper()
	rec, err := s.Sign(impl.SignRequest{
		Artifact:       "registry.acme/app:1.4.0",
		ArtifactDigest: artifactDigest,
		Environment:    impl.EnvProd,
		Provenance:     impl.Provenance{ProvenanceDigest: provDigest, ArtifactDigest: artifactDigest, Builder: "ci-7"},
	})
	if err != nil {
		t.Fatalf("sign artifact: %v", err)
	}
	return rec
}

func revokeProd(t *testing.T, s *impl.SigningService) {
	t.Helper()
	if _, err := s.BeginRevocation(impl.EnvProd, 1); err != nil {
		t.Fatalf("begin revocation: %v", err)
	}
	if _, err := s.SettleRevocation(impl.EnvProd); err != nil {
		t.Fatalf("settle revocation: %v", err)
	}
}

func TestRegisterKeyRejectsInvalidPartition(t *testing.T) {
	s := impl.NewSigningService()
	err := s.RegisterKey(impl.KeySpec{Partition: impl.Environment("qa"), Generation: 1, KeyID: "key-qa-1", Algorithm: "ecdsa-p256", Quota: 2})
	if !errors.Is(err, impl.ErrInvalidEnvironment) {
		t.Fatalf("invalid partition error = %v, want %v", err, impl.ErrInvalidEnvironment)
	}
}

func TestRegisterKeyRejectsNonMonotonicGeneration(t *testing.T) {
	s := impl.NewSigningService()
	if err := s.RegisterKey(impl.KeySpec{Partition: impl.EnvProd, Generation: 2, KeyID: "key-prod-2", Algorithm: "ecdsa-p256", Quota: 2}); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("first generation two error = %v, want %v", err, impl.ErrStaleGeneration)
	}
	registerProdKey(t, s, 4)
	if err := s.RegisterKey(impl.KeySpec{Partition: impl.EnvProd, Generation: 1, KeyID: "key-prod-1b", Algorithm: "ecdsa-p256", Quota: 2}); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("repeated generation error = %v, want %v", err, impl.ErrStaleGeneration)
	}
	if got, err := s.Generation(impl.EnvProd); err != nil || got != 1 {
		t.Fatalf("generation = %d (err %v), want 1", got, err)
	}
}

func TestSignRejectsMalformedDigest(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 4)
	_, err := s.Sign(impl.SignRequest{
		Artifact:       "registry.acme/app:1.4.0",
		ArtifactDigest: "sha256:xyz",
		Environment:    impl.EnvProd,
		Provenance:     impl.Provenance{ProvenanceDigest: provDigest, ArtifactDigest: "sha256:xyz"},
	})
	if !errors.Is(err, impl.ErrInvalidDigest) {
		t.Fatalf("malformed artifact digest error = %v, want %v", err, impl.ErrInvalidDigest)
	}
	_, err = s.Sign(impl.SignRequest{
		Artifact:       "registry.acme/app:1.4.0",
		ArtifactDigest: artifactDigest,
		Environment:    impl.EnvProd,
		Provenance:     impl.Provenance{ProvenanceDigest: "", ArtifactDigest: artifactDigest},
	})
	if !errors.Is(err, impl.ErrInvalidDigest) {
		t.Fatalf("empty provenance digest error = %v, want %v", err, impl.ErrInvalidDigest)
	}
}

func TestSignRejectsUnknownEnvironment(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 4)
	_, err := s.Sign(impl.SignRequest{
		Artifact:       "registry.acme/app:1.4.0",
		ArtifactDigest: artifactDigest,
		Environment:    impl.Environment("qa"),
		Provenance:     impl.Provenance{ProvenanceDigest: provDigest, ArtifactDigest: artifactDigest},
	})
	if !errors.Is(err, impl.ErrInvalidEnvironment) {
		t.Fatalf("unknown environment error = %v, want %v", err, impl.ErrInvalidEnvironment)
	}
}

func TestSignRejectsProvenanceArtifactMismatch(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 4)
	other := "sha256:" + strings.Repeat("c", 64)
	_, err := s.Sign(impl.SignRequest{
		Artifact:       "registry.acme/app:1.4.0",
		ArtifactDigest: artifactDigest,
		Environment:    impl.EnvProd,
		Provenance:     impl.Provenance{ProvenanceDigest: provDigest, ArtifactDigest: other},
	})
	if !errors.Is(err, impl.ErrProvenanceMismatch) {
		t.Fatalf("provenance mismatch error = %v, want %v", err, impl.ErrProvenanceMismatch)
	}
}

func TestSignProducesBoundReceipt(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 4)
	rec := signProd(t, s)
	if rec.SignID == "" {
		t.Fatalf("receipt sign id must not be empty")
	}
	if rec.Environment != impl.EnvProd {
		t.Fatalf("receipt environment = %q, want %q", rec.Environment, impl.EnvProd)
	}
	if rec.Generation != 1 {
		t.Fatalf("receipt generation = %d, want 1", rec.Generation)
	}
	if rec.Algorithm != "ecdsa-p256" {
		t.Fatalf("receipt algorithm = %q, want %q", rec.Algorithm, "ecdsa-p256")
	}
	if rec.ArtifactDigest != artifactDigest || rec.ProvenanceDigest != provDigest {
		t.Fatalf("receipt digests not bound to the request: %+v", rec)
	}
	if rec.Revoked {
		t.Fatalf("fresh receipt must not be revoked")
	}
	if rec.ResignOf != "" {
		t.Fatalf("fresh receipt must not reference an original, got %q", rec.ResignOf)
	}
}

func TestSignEnforcesPartitionQuota(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 2)
	signProd(t, s)
	signProd(t, s)
	_, err := s.Sign(impl.SignRequest{
		Artifact:       "registry.acme/app:1.4.1",
		ArtifactDigest: artifactDigest,
		Environment:    impl.EnvProd,
		Provenance:     impl.Provenance{ProvenanceDigest: provDigest, ArtifactDigest: artifactDigest},
	})
	if !errors.Is(err, impl.ErrQuotaExhausted) {
		t.Fatalf("over quota error = %v, want %v", err, impl.ErrQuotaExhausted)
	}
	remaining, err := s.QuotaRemaining(impl.EnvProd)
	if err != nil {
		t.Fatalf("quota remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("quota remaining = %d, want 0", remaining)
	}
}

func TestQueryReceiptUnknownID(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 2)
	if _, err := s.QueryReceipt("sig-missing"); !errors.Is(err, impl.ErrReceiptNotFound) {
		t.Fatalf("unknown receipt error = %v, want %v", err, impl.ErrReceiptNotFound)
	}
}

func TestRevokeMarksReceiptsRevoked(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	first := signProd(t, s)
	second := signProd(t, s)
	report, err := s.BeginRevocation(impl.EnvProd, 1)
	if err != nil {
		t.Fatalf("begin revocation: %v", err)
	}
	if report.Pending != 2 || report.Settled {
		t.Fatalf("begin report = %+v, want pending 2 and not settled", report)
	}
	report, err = s.SettleRevocation(impl.EnvProd)
	if err != nil {
		t.Fatalf("settle revocation: %v", err)
	}
	if report.Revoked != 2 || !report.Settled {
		t.Fatalf("settle report = %+v, want revoked 2 and settled", report)
	}
	for _, id := range []string{first.SignID, second.SignID} {
		rec, err := s.QueryReceipt(id)
		if err != nil {
			t.Fatalf("query receipt %s: %v", id, err)
		}
		if !rec.Revoked {
			t.Fatalf("receipt %s must be revoked", id)
		}
	}
}

func TestResignCreatesNewReceiptReferencingOriginal(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	original := signProd(t, s)
	revokeProd(t, s)
	report, err := s.ResignBatch([]string{original.SignID})
	if err != nil {
		t.Fatalf("resign batch: %v", err)
	}
	if report.Resigned != 1 {
		t.Fatalf("resigned count = %d, want 1", report.Resigned)
	}
	if len(report.NewSignIDs) != 1 {
		t.Fatalf("new sign ids = %d, want 1", len(report.NewSignIDs))
	}
	if report.NewSignIDs[0] == original.SignID {
		t.Fatalf("replacement must get a new sign id")
	}
	replacement, err := s.QueryReceipt(report.NewSignIDs[0])
	if err != nil {
		t.Fatalf("query replacement: %v", err)
	}
	if replacement.ResignOf != original.SignID {
		t.Fatalf("replacement references %q, want %q", replacement.ResignOf, original.SignID)
	}
	if replacement.Revoked {
		t.Fatalf("replacement receipt must not be revoked")
	}
	if replacement.Generation != 1 {
		t.Fatalf("replacement generation = %d, want 1", replacement.Generation)
	}
}

func TestResignIsIdempotentPerOriginalID(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	original := signProd(t, s)
	revokeProd(t, s)
	if _, err := s.ResignBatch([]string{original.SignID}); err != nil {
		t.Fatalf("first resign: %v", err)
	}
	before := s.Stats().Receipts
	report, err := s.ResignBatch([]string{original.SignID})
	if err != nil {
		t.Fatalf("second resign: %v", err)
	}
	if report.Resigned != 0 {
		t.Fatalf("second resign created %d new receipts, want 0", report.Resigned)
	}
	if report.AlreadyResigned != 1 {
		t.Fatalf("already resigned count = %d, want 1", report.AlreadyResigned)
	}
	if got := s.Stats().Receipts; got != before {
		t.Fatalf("receipt count changed from %d to %d on idempotent resign", before, got)
	}
}

func TestResignSkipsLiveReceipts(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	live := signProd(t, s)
	report, err := s.ResignBatch([]string{live.SignID})
	if err != nil {
		t.Fatalf("resign live receipt: %v", err)
	}
	if report.Skipped != 1 {
		t.Fatalf("skipped count = %d, want 1", report.Skipped)
	}
	if report.Resigned != 0 {
		t.Fatalf("resigned count = %d, want 0", report.Resigned)
	}
}

func TestLedgerKeepsOccurrenceOrder(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	original := signProd(t, s)
	revokeProd(t, s)
	if _, err := s.ResignBatch([]string{original.SignID}); err != nil {
		t.Fatalf("resign: %v", err)
	}
	ledger := s.Ledger()
	want := []string{"key_registered", "signed", "revocation_begun", "revocation_settled", "resigned"}
	if len(ledger) != len(want) {
		t.Fatalf("ledger entries = %d, want %d", len(ledger), len(want))
	}
	for i, kind := range want {
		if ledger[i].Kind != kind {
			t.Fatalf("ledger entry %d kind = %q, want %q", i, ledger[i].Kind, kind)
		}
		if ledger[i].Seq != i+1 {
			t.Fatalf("ledger entry %d seq = %d, want %d", i, ledger[i].Seq, i+1)
		}
	}
}

func TestSnapshotRestorePreservesQuotaAndReceipts(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	first := signProd(t, s)
	signProd(t, s)
	revokeProd(t, s)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewSigningService()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rec, err := restored.QueryReceipt(first.SignID)
	if err != nil {
		t.Fatalf("query restored receipt: %v", err)
	}
	if !rec.Revoked {
		t.Fatalf("restored receipt must stay revoked")
	}
	remaining, err := restored.QuotaRemaining(impl.EnvProd)
	if err != nil {
		t.Fatalf("restored quota remaining: %v", err)
	}
	if remaining != 3 {
		t.Fatalf("restored quota remaining = %d, want 3", remaining)
	}
	if got := restored.Stats().Receipts; got != 2 {
		t.Fatalf("restored receipt count = %d, want 2", got)
	}
}

func TestRestoreRejectsEmptySnapshot(t *testing.T) {
	s := impl.NewSigningService()
	registerProdKey(t, s, 5)
	signProd(t, s)
	if err := s.Restore(impl.SigningSnapshot{}); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("empty snapshot error = %v, want %v", err, impl.ErrBadSnapshot)
	}
	if got := s.Stats().Receipts; got != 1 {
		t.Fatalf("receipt count after rejected restore = %d, want 1", got)
	}
}
