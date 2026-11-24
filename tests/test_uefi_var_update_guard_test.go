package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-12"
)

func stageCommit(t *testing.T, g *impl.UefiVarGuard, variable string, ops []impl.Op, ts int64, counter uint64) {
	t.Helper()
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: ts}
	batch, err := g.StageUpdate(variable, ops, chain, counter)
	if err != nil {
		t.Fatalf("staging %s failed: %v", variable, err)
	}
	if _, err := g.Validate(batch); err != nil {
		t.Fatalf("validating %s failed: %v", variable, err)
	}
	if err := g.Commit(batch); err != nil {
		t.Fatalf("committing %s failed: %v", variable, err)
	}
}

func TestStageUpdateRejectsUnknownVariable(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 10}
	_, err := g.StageUpdate("db2", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, chain, 1)
	if !errors.Is(err, impl.ErrUnknownVariable) {
		t.Fatalf("expected ErrUnknownVariable for db2, got %v", err)
	}
}

func TestStageUpdateRejectsEmptyEntries(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 10}
	_, err := g.StageUpdate("db", nil, chain, 1)
	if !errors.Is(err, impl.ErrEmptyBatch) {
		t.Fatalf("expected ErrEmptyBatch for an empty operation list, got %v", err)
	}
}

func TestValidateRejectsBadSignatureChain(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	chain := impl.Chain{Signer: "root-ca", Signature: "rawbytes", IssuedAt: 10}
	batch, err := g.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, chain, 1)
	if err != nil {
		t.Fatalf("staging should accept the payload for later validation, got %v", err)
	}
	if _, err := g.Validate(batch); !errors.Is(err, impl.ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

func TestValidateRejectsStaleTimestamp(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, 100, 1)
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 100}
	batch, err := g.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "bb"}}}, chain, 2)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(batch); !errors.Is(err, impl.ErrStaleTimestamp) {
		t.Fatalf("expected ErrStaleTimestamp for an equal timestamp, got %v", err)
	}
}

func TestValidateRejectsRevokedEntry(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	if err := g.Revoke("badc0de"); err != nil {
		t.Fatalf("revoking failed: %v", err)
	}
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 10}
	ops := []impl.Op{
		{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}},
		{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "badc0de"}},
	}
	batch, err := g.StageUpdate("db", ops, chain, 1)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(batch); !errors.Is(err, impl.ErrAlreadyRevoked) {
		t.Fatalf("expected ErrAlreadyRevoked, got %v", err)
	}
}

func TestCommitAppendsEntries(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa", Name: "first"}}}, 100, 1)
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "bb", Name: "second"}}}, 200, 2)
	got := g.Entries("db")
	if len(got) != 2 {
		t.Fatalf("db holds %d entries, want 2", len(got))
	}
	if got[0].Hash != "aa" || got[1].Hash != "bb" {
		t.Fatalf("db append order is %q,%q, want aa,bb", got[0].Hash, got[1].Hash)
	}
	if g.RollbackCounter() != 2 {
		t.Fatalf("rollback counter is %d, want 2", g.RollbackCounter())
	}
}

func TestCommitRequiresSuccessfulValidation(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 10}
	batch, err := g.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, chain, 1)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if err := g.Commit(batch); !errors.Is(err, impl.ErrBatchNotStaged) {
		t.Fatalf("expected ErrBatchNotStaged without validation, got %v", err)
	}
	if got := g.Entries("db"); len(got) != 0 {
		t.Fatalf("unvalidated commit wrote %d entries, want 0", len(got))
	}
}

func TestCommitIsAtomicWhenValidationFails(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	if err := g.Revoke("badc0de"); err != nil {
		t.Fatalf("revoking failed: %v", err)
	}
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 10}
	ops := []impl.Op{
		{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}},
		{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "badc0de"}},
	}
	batch, err := g.StageUpdate("db", ops, chain, 1)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(batch); !errors.Is(err, impl.ErrAlreadyRevoked) {
		t.Fatalf("expected ErrAlreadyRevoked, got %v", err)
	}
	if err := g.Commit(batch); !errors.Is(err, impl.ErrBatchNotStaged) {
		t.Fatalf("expected ErrBatchNotStaged after failed validation, got %v", err)
	}
	if got := g.Entries("db"); len(got) != 0 {
		t.Fatalf("failed batch left %d entries in db, want 0", len(got))
	}
}

func TestDeleteModeRemovesNamedEntry(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "h1"}}}, 100, 1)
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "h2"}}}, 200, 2)
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "h3"}}}, 300, 3)
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeDelete, Entry: impl.Entry{Hash: "h2"}}}, 400, 4)
	got := g.Entries("db")
	if len(got) != 2 {
		t.Fatalf("db holds %d entries after delete, want 2", len(got))
	}
	if got[0].Hash != "h1" || got[1].Hash != "h3" {
		t.Fatalf("delete left %q,%q, want h1,h3", got[0].Hash, got[1].Hash)
	}
}

func TestQuotaRejectsOverflowOnKek(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 1})
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 10}
	ops := []impl.Op{
		{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "k1"}},
		{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "k2"}},
	}
	batch, err := g.StageUpdate("KEK", ops, chain, 1)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(batch); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded on KEK quota 1, got %v", err)
	}
	if err := g.Commit(batch); err == nil {
		t.Fatalf("commit of an over-quota batch must fail")
	}
	if got := g.Entries("KEK"); len(got) != 0 {
		t.Fatalf("over-quota batch left %d KEK entries, want 0", len(got))
	}
}

func TestTrustAnchorChangeRequiresAuthToken(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	anchor := impl.Entry{Owner: "kek-owner", Hash: "kek-new", Name: "platform key"}
	if err := g.SetTrustAnchor(anchor, impl.Auth{}); !errors.Is(err, impl.ErrAuthRequired) {
		t.Fatalf("expected ErrAuthRequired without a token, got %v", err)
	}
	if err := g.SetTrustAnchor(anchor, impl.Auth{Token: "trust:platform-admin"}); err != nil {
		t.Fatalf("authorised anchor change failed: %v", err)
	}
	got := g.Entries("KEK")
	if len(got) != 1 || got[0].Hash != "kek-new" {
		t.Fatalf("KEK is %+v, want the single new anchor", got)
	}
}

func TestRevokeRejectsDuplicateAndAppends(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	if err := g.Revoke("deadbeef"); err != nil {
		t.Fatalf("first revoke failed: %v", err)
	}
	if got := g.Entries("dbx"); len(got) != 1 || got[0].Hash != "deadbeef" {
		t.Fatalf("dbx is %+v, want the revoked hash", got)
	}
	if err := g.Revoke("deadbeef"); !errors.Is(err, impl.ErrAlreadyRevoked) {
		t.Fatalf("expected ErrAlreadyRevoked on the duplicate, got %v", err)
	}
	if got := g.Entries("dbx"); len(got) != 1 {
		t.Fatalf("duplicate revoke grew dbx to %d entries", len(got))
	}
}

func TestRollbackCounterMustIncrease(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, 100, 5)
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 200}
	same, err := g.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "bb"}}}, chain, 5)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(same); !errors.Is(err, impl.ErrRollbackRegression) {
		t.Fatalf("expected ErrRollbackRegression for an equal counter, got %v", err)
	}
	lower, err := g.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "cc"}}}, chain, 4)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(lower); !errors.Is(err, impl.ErrRollbackRegression) {
		t.Fatalf("expected ErrRollbackRegression for a lower counter, got %v", err)
	}
	if g.RollbackCounter() != 5 {
		t.Fatalf("counter moved to %d after rejected batches, want 5", g.RollbackCounter())
	}
}

func TestSnapshotRestoreKeepsRollbackCounter(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, 100, 7)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	other := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if other.RollbackCounter() != 7 {
		t.Fatalf("restored counter is %d, want 7", other.RollbackCounter())
	}
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 300}
	batch, err := other.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "bb"}}}, chain, 6)
	if err != nil {
		t.Fatalf("staging after restore failed: %v", err)
	}
	if _, err := other.Validate(batch); !errors.Is(err, impl.ErrRollbackRegression) {
		t.Fatalf("expected ErrRollbackRegression after restore, got %v", err)
	}
	if got := other.Entries("db"); len(got) != 1 || got[0].Hash != "aa" {
		t.Fatalf("restored db is %+v, want the single aa entry", got)
	}
}

func TestStatsCountsStagedCommittedAndRejected(t *testing.T) {
	g := impl.NewUefiVarGuard(impl.Limits{MaxDbEntries: 8, MaxDbxEntries: 8, MaxKekEntries: 8})
	stageCommit(t, g, "db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "aa"}}}, 100, 1)
	if err := g.Revoke("badc0de"); err != nil {
		t.Fatalf("revoking failed: %v", err)
	}
	chain := impl.Chain{Signer: "root-ca", Signature: "sig:abcd1234", IssuedAt: 200}
	batch, err := g.StageUpdate("db", []impl.Op{{Mode: impl.ModeAppend, Entry: impl.Entry{Hash: "badc0de"}}}, chain, 2)
	if err != nil {
		t.Fatalf("staging failed: %v", err)
	}
	if _, err := g.Validate(batch); !errors.Is(err, impl.ErrAlreadyRevoked) {
		t.Fatalf("expected ErrAlreadyRevoked, got %v", err)
	}
	stats := g.Stats()
	if stats.Staged != 2 || stats.Committed != 1 || stats.Rejected != 1 || stats.Revoked != 1 {
		t.Fatalf("stats are %+v, want staged=2 committed=1 rejected=1 revoked=1", stats)
	}
}
