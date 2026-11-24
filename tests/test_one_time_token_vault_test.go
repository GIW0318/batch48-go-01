package task

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-03"
)

func vaultConfig(now func() time.Time) impl.VaultConfig {
	return impl.VaultConfig{
		MaxBatch:          4,
		MaxLivePerPurpose: 1000,
		Now:               now,
	}
}

func frozenClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func issueOne(t *testing.T, v *impl.OneTimeTokenVault, tenant, purpose string, ttl time.Duration) impl.Token {
	t.Helper()
	tok, err := v.Issue(tenant, purpose, ttl)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

func TestIssueStoresHashAndReturnsRaw(t *testing.T) {
	now := time.Unix(5000, 0)
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(now)))
	tok := issueOne(t, v, "tenant-1", impl.PurposeInvite, time.Hour)
	if tok.ID == "" {
		t.Fatalf("token id must not be empty")
	}
	if tok.TenantID != "tenant-1" || tok.Purpose != impl.PurposeInvite {
		t.Fatalf("token tags mismatch: tenant=%q purpose=%q", tok.TenantID, tok.Purpose)
	}
	if tok.State != impl.TokenLive {
		t.Fatalf("new token state = %q, want %q", tok.State, impl.TokenLive)
	}
	parts := strings.SplitN(tok.Raw, ".", 2)
	if len(parts) != 2 || parts[0] != tok.ID || parts[1] == "" {
		t.Fatalf("raw token must be id.secret, got %q", tok.Raw)
	}
	if len(tok.Hash) != 64 {
		t.Fatalf("stored hash length = %d, want 64 hex chars", len(tok.Hash))
	}
	if tok.Hash == parts[1] || tok.Hash == tok.Raw {
		t.Fatalf("stored hash must not equal the secret or the raw token")
	}
	if want := now.Add(time.Hour).UnixNano(); tok.ExpiresAt != want {
		t.Fatalf("expiry = %d, want %d", tok.ExpiresAt, want)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok {
		t.Fatalf("issued token must be readable")
	}
	if stored.Raw != "" {
		t.Fatalf("lookup must not echo the raw secret, got %q", stored.Raw)
	}
	if stored.Hash != tok.Hash {
		t.Fatalf("stored hash mismatch: got %q want %q", stored.Hash, tok.Hash)
	}
}

func TestIssueRejectsBadTTLAndBlankArgs(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(5000, 0))))
	if _, err := v.Issue("tenant-1", impl.PurposeInvite, 0); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("zero ttl must fail with ErrInvalidTTL, got %v", err)
	}
	if _, err := v.Issue("tenant-1", impl.PurposeInvite, -time.Minute); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("negative ttl must fail with ErrInvalidTTL, got %v", err)
	}
	if _, err := v.Issue("  ", impl.PurposeInvite, time.Minute); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank tenant must fail with ErrBlankArg, got %v", err)
	}
	if _, err := v.Issue("tenant-1", "", time.Minute); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank purpose must fail with ErrBlankArg, got %v", err)
	}
	if got := v.Count(); got != 0 {
		t.Fatalf("rejected issues left %d records behind", got)
	}
}

func TestRedeemConsumesTokenOnce(t *testing.T) {
	now := time.Unix(6000, 0)
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(now)))
	tok := issueOne(t, v, "tenant-1", impl.PurposeReset, 10*time.Minute)
	subject, err := v.Redeem("tenant-1", impl.PurposeReset, tok.Raw)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if subject.TokenID != tok.ID || subject.TenantID != "tenant-1" || subject.Purpose != impl.PurposeReset {
		t.Fatalf("subject mismatch: %+v", subject)
	}
	if subject.RedeemedAt != now.UnixNano() {
		t.Fatalf("redeemed at %d, want %d", subject.RedeemedAt, now.UnixNano())
	}
	if subject.Seq <= 0 {
		t.Fatalf("subject sequence = %d, want a positive monotonic number", subject.Seq)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeReset, tok.Raw); !errors.Is(err, impl.ErrTokenConsumed) {
		t.Fatalf("second redeem must fail with ErrTokenConsumed, got %v", err)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok || stored.State != impl.TokenRedeemed {
		t.Fatalf("token after redeem: %+v ok=%v, want redeemed", stored, ok)
	}
}

func TestRedeemRejectsTamperedSecret(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(7000, 0))))
	tok := issueOne(t, v, "tenant-1", impl.PurposeInvite, time.Hour)
	if _, err := v.Redeem("tenant-1", impl.PurposeInvite, tok.ID+".forged-secret"); !errors.Is(err, impl.ErrTokenMismatch) {
		t.Fatalf("tampered secret must fail with ErrTokenMismatch, got %v", err)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok || stored.State != impl.TokenLive {
		t.Fatalf("a hash mismatch must not consume the token, got %+v ok=%v", stored, ok)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeInvite, "no-separator-token"); !errors.Is(err, impl.ErrTokenMalformed) {
		t.Fatalf("malformed raw token must fail with ErrTokenMalformed, got %v", err)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeInvite, tok.Raw); err != nil {
		t.Fatalf("the original secret must still work, got %v", err)
	}
}

func TestRedeemRejectsExpiredToken(t *testing.T) {
	now := time.Unix(8000, 0)
	v := impl.NewOneTimeTokenVault(vaultConfig(func() time.Time { return now }))
	tok := issueOne(t, v, "tenant-1", impl.PurposeVerify, time.Minute)
	now = now.Add(2 * time.Minute)
	if _, err := v.Redeem("tenant-1", impl.PurposeVerify, tok.Raw); !errors.Is(err, impl.ErrTokenExpired) {
		t.Fatalf("expired token must fail with ErrTokenExpired, got %v", err)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok || stored.State != impl.TokenExpired {
		t.Fatalf("token after expiry: %+v ok=%v, want expired", stored, ok)
	}
}

func TestRedeemRejectsWrongTenantAndPurpose(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(9000, 0))))
	tok := issueOne(t, v, "tenant-1", impl.PurposeInvite, time.Hour)
	if _, err := v.Redeem("tenant-2", impl.PurposeInvite, tok.Raw); !errors.Is(err, impl.ErrTenantMismatch) {
		t.Fatalf("foreign tenant must fail with ErrTenantMismatch, got %v", err)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeReset, tok.Raw); !errors.Is(err, impl.ErrPurposeMismatch) {
		t.Fatalf("wrong purpose must fail with ErrPurposeMismatch, got %v", err)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok || stored.State != impl.TokenLive {
		t.Fatalf("rejected redemptions must leave the token live, got %+v ok=%v", stored, ok)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeInvite, tok.Raw); err != nil {
		t.Fatalf("matching redemption must succeed, got %v", err)
	}
}

func TestRevokeBlocksRedemption(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(10000, 0))))
	tok := issueOne(t, v, "tenant-1", impl.PurposeReset, time.Hour)
	if err := v.Revoke(tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeReset, tok.Raw); !errors.Is(err, impl.ErrTokenRevoked) {
		t.Fatalf("revoked token must fail with ErrTokenRevoked, got %v", err)
	}
	if err := v.Revoke(tok.ID); err != nil {
		t.Fatalf("revoking a revoked token must be idempotent, got %v", err)
	}
	if err := v.Revoke("tok-404"); !errors.Is(err, impl.ErrTokenNotFound) {
		t.Fatalf("revoking an unknown token must fail with ErrTokenNotFound, got %v", err)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok || stored.State != impl.TokenRevoked {
		t.Fatalf("token after revoke: %+v ok=%v, want revoked", stored, ok)
	}
}

func TestIssueBatchAtomicOnBatchCap(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(11000, 0))))
	if _, err := v.IssueBatch("tenant-1", impl.PurposeInvite, 0); !errors.Is(err, impl.ErrInvalidCount) {
		t.Fatalf("zero count must fail with ErrInvalidCount, got %v", err)
	}
	if _, err := v.IssueBatch("tenant-1", impl.PurposeInvite, 5); !errors.Is(err, impl.ErrBatchTooLarge) {
		t.Fatalf("batch above the cap must fail with ErrBatchTooLarge, got %v", err)
	}
	if got := v.Count(); got != 0 {
		t.Fatalf("a rejected batch left %d records behind, want 0", got)
	}
	quota := v.Stats("tenant-1", impl.PurposeInvite)
	if quota.Issued != 0 {
		t.Fatalf("a rejected batch must not consume quota, issued = %d", quota.Issued)
	}
	if _, err := v.IssueBatch("tenant-1", impl.PurposeInvite, 4); err != nil {
		t.Fatalf("batch at the cap must succeed, got %v", err)
	}
	if got := v.Count(); got != 4 {
		t.Fatalf("record count = %d, want 4", got)
	}
}

func TestIssueBatchIssuesDistinctTokens(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(12000, 0))))
	tokens, err := v.IssueBatch("tenant-2", impl.PurposeVerify, 3)
	if err != nil {
		t.Fatalf("issue batch: %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("batch size = %d, want 3", len(tokens))
	}
	seen := map[string]bool{}
	for i, tok := range tokens {
		if tok.TenantID != "tenant-2" || tok.Purpose != impl.PurposeVerify {
			t.Fatalf("batch token %d tags mismatch: %+v", i, tok)
		}
		if seen[tok.ID] {
			t.Fatalf("batch token id %q issued twice", tok.ID)
		}
		seen[tok.ID] = true
		if tok.Raw == "" || tok.Hash == "" {
			t.Fatalf("batch token %d is missing raw or hash material", i)
		}
	}
	quota := v.Stats("tenant-2", impl.PurposeVerify)
	if quota.Issued != 3 || quota.Live != 3 {
		t.Fatalf("quota after batch = %+v, want issued 3 live 3", quota)
	}
	if _, err := v.Redeem("tenant-2", impl.PurposeVerify, tokens[1].Raw); err != nil {
		t.Fatalf("redeem a batch token: %v", err)
	}
}

func TestPurgeDropsSettledAndExpired(t *testing.T) {
	now := time.Unix(13000, 0)
	v := impl.NewOneTimeTokenVault(vaultConfig(func() time.Time { return now }))
	live := issueOne(t, v, "tenant-1", impl.PurposeInvite, time.Hour)
	spent := issueOne(t, v, "tenant-1", impl.PurposeInvite, time.Hour)
	gone := issueOne(t, v, "tenant-1", impl.PurposeInvite, time.Hour)
	if _, err := v.Redeem("tenant-1", impl.PurposeInvite, spent.Raw); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := v.Revoke(gone.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := v.Count(); got != 3 {
		t.Fatalf("record count before purge = %d, want 3", got)
	}
	if removed := v.Purge(now); removed != 2 {
		t.Fatalf("purge removed %d records, want the 2 settled ones", removed)
	}
	if got := v.Count(); got != 1 {
		t.Fatalf("record count after purge = %d, want 1", got)
	}
	if _, ok := v.Lookup(live.ID); !ok {
		t.Fatalf("a live token must survive a purge")
	}
	now = now.Add(2 * time.Hour)
	if removed := v.Purge(now); removed != 1 {
		t.Fatalf("purge after expiry removed %d records, want 1", removed)
	}
	if got := v.Count(); got != 0 {
		t.Fatalf("record count after the second purge = %d, want 0", got)
	}
}

func TestStatsCountsPerPurpose(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(14000, 0))))
	first := issueOne(t, v, "tenant-3", impl.PurposeInvite, time.Hour)
	issueOne(t, v, "tenant-3", impl.PurposeInvite, time.Hour)
	issueOne(t, v, "tenant-3", impl.PurposeReset, time.Hour)
	if _, err := v.Redeem("tenant-3", impl.PurposeInvite, first.Raw); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	invite := v.Stats("tenant-3", impl.PurposeInvite)
	if invite.Issued != 2 || invite.Live != 1 || invite.Settled != 1 {
		t.Fatalf("invite quota = %+v, want issued 2 live 1 settled 1", invite)
	}
	reset := v.Stats("tenant-3", impl.PurposeReset)
	if reset.Issued != 1 || reset.Live != 1 || reset.Settled != 0 {
		t.Fatalf("reset quota = %+v, want issued 1 live 1 settled 0", reset)
	}
	other := v.Stats("tenant-4", impl.PurposeInvite)
	if other.Issued != 0 || other.Live != 0 {
		t.Fatalf("other tenant quota = %+v, want zero", other)
	}
}

func TestSnapshotRestoreKeepsRedeemedDead(t *testing.T) {
	now := time.Unix(15000, 0)
	cfg := vaultConfig(func() time.Time { return now })
	v := impl.NewOneTimeTokenVault(cfg)
	spent := issueOne(t, v, "tenant-1", impl.PurposeReset, time.Hour)
	alive := issueOne(t, v, "tenant-1", impl.PurposeReset, time.Hour)
	if _, err := v.Redeem("tenant-1", impl.PurposeReset, spent.Raw); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	data, err := v.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := impl.NewOneTimeTokenVault(cfg)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := restored.Redeem("tenant-1", impl.PurposeReset, spent.Raw); !errors.Is(err, impl.ErrTokenConsumed) {
		t.Fatalf("restored redeemed token must stay dead, got %v", err)
	}
	if _, err := restored.Redeem("tenant-1", impl.PurposeReset, alive.Raw); err != nil {
		t.Fatalf("restored live token must still redeem, got %v", err)
	}
	if got := restored.Count(); got != 2 {
		t.Fatalf("restored record count = %d, want 2", got)
	}
}

func TestSnapshotRestoreKeepsIssuedCounters(t *testing.T) {
	now := time.Unix(16000, 0)
	cfg := vaultConfig(func() time.Time { return now })
	v := impl.NewOneTimeTokenVault(cfg)
	tokens, err := v.IssueBatch("tenant-5", impl.PurposeInvite, 3)
	if err != nil {
		t.Fatalf("issue batch: %v", err)
	}
	if _, err := v.Redeem("tenant-5", impl.PurposeInvite, tokens[0].Raw); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if removed := v.Purge(now); removed != 1 {
		t.Fatalf("purge removed %d, want 1", removed)
	}
	data, err := v.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewOneTimeTokenVault(cfg)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	quota := restored.Stats("tenant-5", impl.PurposeInvite)
	if quota.Issued != 3 {
		t.Fatalf("restored issued counter = %d, want the cumulative 3", quota.Issued)
	}
	if quota.Live != 2 {
		t.Fatalf("restored live counter = %d, want 2", quota.Live)
	}
	if got := restored.Count(); got != 2 {
		t.Fatalf("restored record count = %d, want 2", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(17000, 0))))
	tok := issueOne(t, v, "tenant-1", impl.PurposeVerify, time.Hour)
	if err := v.Restore([]byte("not-json-at-all")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	if got := v.Count(); got != 1 {
		t.Fatalf("a rejected restore must not touch live records, count = %d", got)
	}
	if _, err := v.Redeem("tenant-1", impl.PurposeVerify, tok.Raw); err != nil {
		t.Fatalf("live token must keep working after a rejected restore, got %v", err)
	}
}

func TestConcurrentRedeemOnlyOnce(t *testing.T) {
	v := impl.NewOneTimeTokenVault(vaultConfig(frozenClock(time.Unix(18000, 0))))
	tok := issueOne(t, v, "tenant-6", impl.PurposeReset, time.Hour)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, consumed := 0, 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := v.Redeem("tenant-6", impl.PurposeReset, tok.Raw)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, impl.ErrTokenConsumed):
				consumed++
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("concurrent redeem succeeded %d times, want exactly 1", won)
	}
	if consumed != 11 {
		t.Fatalf("concurrent redeem reported %d consumed errors, want 11", consumed)
	}
	stored, ok := v.Lookup(tok.ID)
	if !ok || stored.State != impl.TokenRedeemed {
		t.Fatalf("token after the race: %+v ok=%v, want redeemed", stored, ok)
	}
}
