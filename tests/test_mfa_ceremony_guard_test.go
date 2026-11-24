package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-02"
)

func fixedVerifier(factorID, proof string) bool {
	return proof == "proof:"+factorID
}

func mfaConfig(now func() time.Time) impl.GuardConfig {
	return impl.GuardConfig{
		MaxFactorsPerUser: 4,
		MaxFailures:       3,
		LockDuration:      5 * time.Minute,
		RequiredFactors:   2,
		Now:               now,
		ProofVerifier:     fixedVerifier,
	}
}

func freeze(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func activeFactor(t *testing.T, g *impl.MfaCeremonyGuard, userID, factorID, kind string) {
	t.Helper()
	if _, err := g.EnrollFactor(userID, factorID, kind); err != nil {
		t.Fatalf("enroll %s/%s: %v", userID, factorID, err)
	}
	if err := g.ConfirmFactor(userID, factorID); err != nil {
		t.Fatalf("confirm %s/%s: %v", userID, factorID, err)
	}
}

func TestEnrollFactorStartsPending(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	factor, err := g.EnrollFactor("user-1", "factor-a", impl.KindKnowledge)
	if err != nil {
		t.Fatalf("enroll factor: %v", err)
	}
	if factor.UserID != "user-1" || factor.FactorID != "factor-a" {
		t.Fatalf("factor identity mismatch: %+v", factor)
	}
	if factor.Kind != impl.KindKnowledge {
		t.Fatalf("factor kind = %q, want %q", factor.Kind, impl.KindKnowledge)
	}
	if factor.State != impl.FactorPending {
		t.Fatalf("new factor state = %q, want %q", factor.State, impl.FactorPending)
	}
	if _, ok := g.Factor("user-1", "factor-a"); !ok {
		t.Fatalf("enrolled factor must be readable")
	}
	list := g.Factors("user-1")
	if len(list) != 1 || list[0].FactorID != "factor-a" {
		t.Fatalf("factor list = %+v, want exactly factor-a", list)
	}
}

func TestEnrollFactorRejectsUnknownKindAndBlank(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	if _, err := g.EnrollFactor("user-2", "factor-a", "smell"); !errors.Is(err, impl.ErrFactorKind) {
		t.Fatalf("unknown factor kind must fail with ErrFactorKind, got %v", err)
	}
	if _, err := g.EnrollFactor("  ", "factor-a", impl.KindKnowledge); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank user id must fail with ErrBlankArg, got %v", err)
	}
	if _, err := g.EnrollFactor("user-2", "", impl.KindKnowledge); !errors.Is(err, impl.ErrBlankArg) {
		t.Fatalf("blank factor id must fail with ErrBlankArg, got %v", err)
	}
	if len(g.Factors("user-2")) != 0 {
		t.Fatalf("rejected enrollments must not leave factors behind")
	}
}

func TestEnrollFactorRejectsDuplicateAndLimit(t *testing.T) {
	cfg := mfaConfig(freeze(time.Unix(1000, 0)))
	cfg.MaxFactorsPerUser = 2
	g := impl.NewMfaCeremonyGuard(cfg)
	if _, err := g.EnrollFactor("user-3", "factor-a", impl.KindKnowledge); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	if _, err := g.EnrollFactor("user-3", "factor-a", impl.KindPossession); !errors.Is(err, impl.ErrFactorExists) {
		t.Fatalf("duplicate factor id must fail with ErrFactorExists, got %v", err)
	}
	if _, err := g.EnrollFactor("user-3", "factor-b", impl.KindPossession); err != nil {
		t.Fatalf("second enroll: %v", err)
	}
	if _, err := g.EnrollFactor("user-3", "factor-c", impl.KindInherence); !errors.Is(err, impl.ErrFactorLimit) {
		t.Fatalf("third factor must hit the per user cap, got %v", err)
	}
	if got := len(g.Factors("user-3")); got != 2 {
		t.Fatalf("factor count = %d, want the cap 2", got)
	}
}

func TestConfirmFactorActivatesPendingOnly(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	if _, err := g.EnrollFactor("user-4", "factor-a", impl.KindKnowledge); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := g.ConfirmFactor("user-4", "factor-a"); err != nil {
		t.Fatalf("confirm pending factor: %v", err)
	}
	factor, ok := g.Factor("user-4", "factor-a")
	if !ok || factor.State != impl.FactorActive {
		t.Fatalf("factor after confirm: %+v ok=%v, want active", factor, ok)
	}
	if err := g.ConfirmFactor("user-4", "factor-a"); !errors.Is(err, impl.ErrFactorState) {
		t.Fatalf("confirming an active factor must fail with ErrFactorState, got %v", err)
	}
	if err := g.ConfirmFactor("user-4", "factor-404"); !errors.Is(err, impl.ErrFactorNotFound) {
		t.Fatalf("confirming an unknown factor must fail with ErrFactorNotFound, got %v", err)
	}
}

func TestBeginCeremonyRequiresActiveFactor(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	if _, err := g.EnrollFactor("user-5", "factor-a", impl.KindKnowledge); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if _, err := g.BeginCeremony("user-5", "session-1"); !errors.Is(err, impl.ErrNoActiveFactor) {
		t.Fatalf("ceremony with only pending factors must fail with ErrNoActiveFactor, got %v", err)
	}
	if err := g.ConfirmFactor("user-5", "factor-a"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	c, err := g.BeginCeremony("user-5", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	if c.ID == "" || c.Challenge == "" {
		t.Fatalf("ceremony must carry an id and a one time challenge, got %+v", c)
	}
	if c.UserID != "user-5" || c.SessionID != "session-1" {
		t.Fatalf("ceremony binding mismatch: %+v", c)
	}
	if c.State != impl.CeremonyOpen || c.Verified != 0 {
		t.Fatalf("fresh ceremony state=%q verified=%d, want open/0", c.State, c.Verified)
	}
}

func TestBeginCeremonyRejectsSecondInFlight(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-6", "factor-a", impl.KindKnowledge)
	if _, err := g.BeginCeremony("user-6", "session-1"); err != nil {
		t.Fatalf("first ceremony: %v", err)
	}
	if _, err := g.BeginCeremony("user-6", "session-1"); !errors.Is(err, impl.ErrCeremonyInFlight) {
		t.Fatalf("second ceremony for the same account must fail with ErrCeremonyInFlight, got %v", err)
	}
	activeFactor(t, g, "user-7", "factor-a", impl.KindKnowledge)
	if _, err := g.BeginCeremony("user-7", "session-2"); err != nil {
		t.Fatalf("other accounts must not be blocked, got %v", err)
	}
}

func TestConcurrentBeginCeremonyAllowsSingleChallenge(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-8", "factor-a", impl.KindKnowledge)
	var wg sync.WaitGroup
	var mu sync.Mutex
	started, inFlight := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := g.BeginCeremony("user-8", "session-x")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				started++
			case errors.Is(err, impl.ErrCeremonyInFlight):
				inFlight++
			}
		}()
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("concurrent begins started %d ceremonies, want exactly 1", started)
	}
	if inFlight != 7 {
		t.Fatalf("concurrent begins reported %d in flight errors, want 7", inFlight)
	}
}

func TestVerifyTwoDistinctFactorsCountsTwo(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-9", "factor-a", impl.KindKnowledge)
	activeFactor(t, g, "user-9", "factor-b", impl.KindPossession)
	c, err := g.BeginCeremony("user-9", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	first, err := g.VerifyFactor(c.ID, "factor-a", "proof:factor-a")
	if err != nil {
		t.Fatalf("verify first factor: %v", err)
	}
	if !first.Accepted || first.Reason != impl.ReasonOK {
		t.Fatalf("first verification accepted=%v reason=%q, want accepted/ok", first.Accepted, first.Reason)
	}
	if first.Verified != 1 {
		t.Fatalf("verified counter after one factor = %d, want 1", first.Verified)
	}
	second, err := g.VerifyFactor(c.ID, "factor-b", "proof:factor-b")
	if err != nil {
		t.Fatalf("verify second factor: %v", err)
	}
	if !second.Accepted || second.Verified != 2 {
		t.Fatalf("second verification accepted=%v verified=%d, want accepted/2", second.Accepted, second.Verified)
	}
}

func TestVerifyWrongProofRejected(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-10", "factor-a", impl.KindKnowledge)
	c, err := g.BeginCeremony("user-10", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	v, err := g.VerifyFactor(c.ID, "factor-a", "proof:forged")
	if err != nil {
		t.Fatalf("a rejected proof must not return an unexpected error, got %v", err)
	}
	if v.Accepted {
		t.Fatalf("forged proof must not be accepted")
	}
	if v.Reason != impl.ReasonProofMismatch {
		t.Fatalf("rejected proof reason = %q, want %q", v.Reason, impl.ReasonProofMismatch)
	}
	if v.Verified != 0 {
		t.Fatalf("verified counter after a rejected proof = %d, want 0", v.Verified)
	}
	if _, err := g.VerifyFactor("cer-404", "factor-a", "proof:factor-a"); !errors.Is(err, impl.ErrCeremonyNotFound) {
		t.Fatalf("unknown ceremony must fail with ErrCeremonyNotFound, got %v", err)
	}
}

func TestRedeemNeedsRequiredFactors(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-11", "factor-b", impl.KindKnowledge)
	activeFactor(t, g, "user-11", "factor-a", impl.KindPossession)
	c, err := g.BeginCeremony("user-11", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	if _, err := g.VerifyFactor(c.ID, "factor-b", "proof:factor-b"); err != nil {
		t.Fatalf("verify factor-b: %v", err)
	}
	if _, err := g.RedeemCeremony(c.ID); !errors.Is(err, impl.ErrCeremonyIncomplete) {
		t.Fatalf("redeem with one of two factors must fail with ErrCeremonyIncomplete, got %v", err)
	}
	if _, err := g.VerifyFactor(c.ID, "factor-a", "proof:factor-a"); err != nil {
		t.Fatalf("verify factor-a: %v", err)
	}
	grant, err := g.RedeemCeremony(c.ID)
	if err != nil {
		t.Fatalf("redeem ceremony: %v", err)
	}
	if grant.CeremonyID != c.ID || grant.UserID != "user-11" || grant.SessionID != "session-1" {
		t.Fatalf("grant binding mismatch: %+v", grant)
	}
	if len(grant.Factors) != 2 || grant.Factors[0] != "factor-a" || grant.Factors[1] != "factor-b" {
		t.Fatalf("grant factors = %v, want the sorted pair [factor-a factor-b]", grant.Factors)
	}
	if grant.State != impl.GrantValid {
		t.Fatalf("grant state = %q, want %q", grant.State, impl.GrantValid)
	}
	cer, ok := g.Ceremony(c.ID)
	if !ok || cer.State != impl.CeremonyRedeemed {
		t.Fatalf("ceremony after redeem: %+v ok=%v, want redeemed", cer, ok)
	}
}

func TestRedeemConsumesCeremonyOnce(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-12", "factor-a", impl.KindKnowledge)
	activeFactor(t, g, "user-12", "factor-b", impl.KindPossession)
	c, err := g.BeginCeremony("user-12", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	for _, factorID := range []string{"factor-a", "factor-b"} {
		if _, err := g.VerifyFactor(c.ID, factorID, "proof:"+factorID); err != nil {
			t.Fatalf("verify %s: %v", factorID, err)
		}
	}
	first, err := g.RedeemCeremony(c.ID)
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	second, err := g.RedeemCeremony(c.ID)
	if !errors.Is(err, impl.ErrCeremonyConsumed) {
		t.Fatalf("second redeem must fail with ErrCeremonyConsumed, got %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("a rejected redeem must not mint another grant")
	}
	if _, err := g.RedeemCeremony("cer-404"); !errors.Is(err, impl.ErrCeremonyNotFound) {
		t.Fatalf("unknown ceremony must fail with ErrCeremonyNotFound, got %v", err)
	}
}

func TestKnowledgeFailuresLockAccount(t *testing.T) {
	now := time.Unix(1000, 0)
	cfg := mfaConfig(func() time.Time { return now })
	g := impl.NewMfaCeremonyGuard(cfg)
	activeFactor(t, g, "user-13", "factor-a", impl.KindKnowledge)
	c, err := g.BeginCeremony("user-13", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	for i := 0; i < cfg.MaxFailures; i++ {
		v, err := g.VerifyFactor(c.ID, "factor-a", "proof:bad")
		if err != nil {
			t.Fatalf("failure round %d returned an unexpected error: %v", i, err)
		}
		if v.Accepted {
			t.Fatalf("failure round %d was accepted", i)
		}
	}
	if got := g.LockRemaining("user-13"); got != cfg.LockDuration {
		t.Fatalf("lock remaining = %v, want %v", got, cfg.LockDuration)
	}
	if _, err := g.BeginCeremony("user-13", "session-1"); !errors.Is(err, impl.ErrAccountLocked) {
		t.Fatalf("locked account must fail with ErrAccountLocked, got %v", err)
	}
	now = now.Add(cfg.LockDuration)
	if got := g.LockRemaining("user-13"); got != 0 {
		t.Fatalf("lock remaining after expiry = %v, want 0", got)
	}
	if _, err := g.BeginCeremony("user-13", "session-1"); err != nil {
		t.Fatalf("ceremony after the lock expires: %v", err)
	}
}

func TestRevokeFactorMarksRemoved(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-14", "factor-a", impl.KindKnowledge)
	if err := g.RevokeFactor("user-14", "factor-a"); err != nil {
		t.Fatalf("revoke factor: %v", err)
	}
	factor, ok := g.Factor("user-14", "factor-a")
	if !ok || factor.State != impl.FactorRemoved {
		t.Fatalf("factor after revoke: %+v ok=%v, want removed", factor, ok)
	}
	if err := g.ConfirmFactor("user-14", "factor-a"); !errors.Is(err, impl.ErrFactorState) {
		t.Fatalf("confirming a removed factor must fail with ErrFactorState, got %v", err)
	}
	if err := g.RevokeFactor("user-14", "factor-404"); !errors.Is(err, impl.ErrFactorNotFound) {
		t.Fatalf("revoking an unknown factor must fail with ErrFactorNotFound, got %v", err)
	}
}

func TestSnapshotRestoreKeepsCeremonyAndLock(t *testing.T) {
	now := time.Unix(2000, 0)
	cfg := mfaConfig(func() time.Time { return now })
	g := impl.NewMfaCeremonyGuard(cfg)
	activeFactor(t, g, "user-15", "factor-a", impl.KindKnowledge)
	activeFactor(t, g, "user-15", "factor-b", impl.KindPossession)
	c, err := g.BeginCeremony("user-15", "session-1")
	if err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	if _, err := g.VerifyFactor(c.ID, "factor-a", "proof:factor-a"); err != nil {
		t.Fatalf("verify factor-a: %v", err)
	}
	activeFactor(t, g, "user-16", "factor-a", impl.KindKnowledge)
	locked, err := g.BeginCeremony("user-16", "session-2")
	if err != nil {
		t.Fatalf("begin ceremony for locked user: %v", err)
	}
	for i := 0; i < cfg.MaxFailures; i++ {
		if _, err := g.VerifyFactor(locked.ID, "factor-a", "proof:bad"); err != nil {
			t.Fatalf("lock round %d: %v", i, err)
		}
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := impl.NewMfaCeremonyGuard(cfg)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	cer, ok := restored.Ceremony(c.ID)
	if !ok || cer.State != impl.CeremonyOpen || cer.Verified != 1 {
		t.Fatalf("restored ceremony = %+v ok=%v, want open with 1 verified factor", cer, ok)
	}
	if _, err := restored.RedeemCeremony(c.ID); !errors.Is(err, impl.ErrCeremonyIncomplete) {
		t.Fatalf("restored ceremony must still need the second factor, got %v", err)
	}
	if _, err := restored.VerifyFactor(c.ID, "factor-b", "proof:factor-b"); err != nil {
		t.Fatalf("verify factor-b after restore: %v", err)
	}
	if _, err := restored.RedeemCeremony(c.ID); err != nil {
		t.Fatalf("redeem after restore must succeed, got %v", err)
	}
	if got := restored.LockRemaining("user-16"); got != cfg.LockDuration {
		t.Fatalf("restored lock remaining = %v, want %v", got, cfg.LockDuration)
	}
	if _, err := restored.BeginCeremony("user-16", "session-2"); !errors.Is(err, impl.ErrAccountLocked) {
		t.Fatalf("restored account must stay locked, got %v", err)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	g := impl.NewMfaCeremonyGuard(mfaConfig(freeze(time.Unix(1000, 0))))
	activeFactor(t, g, "user-17", "factor-a", impl.KindKnowledge)
	if _, err := g.BeginCeremony("user-17", "session-1"); err != nil {
		t.Fatalf("begin ceremony: %v", err)
	}
	if err := g.Restore([]byte("<xml>nope</xml>")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	if _, err := g.BeginCeremony("user-17", "session-1"); !errors.Is(err, impl.ErrCeremonyInFlight) {
		t.Fatalf("live ceremony must survive a rejected restore, got %v", err)
	}
	factor, ok := g.Factor("user-17", "factor-a")
	if !ok || factor.State != impl.FactorActive {
		t.Fatalf("live factor must survive a rejected restore, got %+v ok=%v", factor, ok)
	}
}
