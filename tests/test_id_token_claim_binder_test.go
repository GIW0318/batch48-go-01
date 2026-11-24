package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-11"
)

const testClientID = "web-app"

func baseTime() time.Time {
	return time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
}

func testLimits() impl.BinderLimits {
	return impl.BinderLimits{
		MaxSessionsPerUser:   4,
		MaxAudiencesPerToken: 4,
		MaxAuthAge:           8 * time.Hour,
		ClockSkew:            2 * time.Minute,
		NonceTTL:             time.Hour,
	}
}

func binderWith(limits impl.BinderLimits) *impl.IdTokenClaimBinder {
	return impl.NewIdTokenClaimBinder(impl.BinderConfig{ClientID: testClientID, Limits: limits})
}

func newBinder() *impl.IdTokenClaimBinder {
	return binderWith(testLimits())
}

// claimsFor builds a claim set that is valid at the given instant.
func claimsFor(b *impl.IdTokenClaimBinder, subject string, aud []string, at time.Time, nonce string) impl.Claims {
	return impl.Claims{
		Subject:         subject,
		Audiences:       aud,
		AuthorizedParty: testClientID,
		Issuer:          "https://issuer.example",
		IssuedAt:        at.Add(-2 * time.Minute),
		AuthTime:        at.Add(-30 * time.Second),
		ExpiresAt:       at.Add(30 * time.Minute),
		Nonce:           nonce,
		AccessTokenHash: b.Digest("access-1"),
	}
}

func TestBindStoresSessionBinding(t *testing.T) {
	b := newBinder()
	now := baseTime()
	binding, err := b.Bind("sess-1", claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a"))
	if err != nil {
		t.Fatalf("bind must succeed for a clean claim set, got %v", err)
	}
	if binding.SessionID != "sess-1" || binding.UserID != "user-1" {
		t.Fatalf("binding identity mismatch: session=%q user=%q", binding.SessionID, binding.UserID)
	}
	if binding.Seq != 1 {
		t.Fatalf("first binding sequence = %d, want 1", binding.Seq)
	}
	if len(binding.Audiences) != 1 || binding.Audiences[0] != testClientID {
		t.Fatalf("binding audiences = %v, want [%s]", binding.Audiences, testClientID)
	}
	if binding.TokenHash != b.Digest("access-1") {
		t.Fatalf("binding token digest = %q, want digest of the bound access token", binding.TokenHash)
	}
	sessions := b.Sessions("user-1")
	if len(sessions) != 1 || sessions[0] != "sess-1" {
		t.Fatalf("sessions of user-1 = %v, want [sess-1]", sessions)
	}
}

func TestBindRejectsBlankIdentity(t *testing.T) {
	b := newBinder()
	now := baseTime()
	if _, err := b.Bind("   ", claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")); !errors.Is(err, impl.ErrBlankIdentity) {
		t.Fatalf("blank session id must be rejected with ErrBlankIdentity, got %v", err)
	}
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-b")
	claims.Subject = "  "
	if _, err := b.Bind("sess-1", claims); !errors.Is(err, impl.ErrBlankIdentity) {
		t.Fatalf("blank subject must be rejected with ErrBlankIdentity, got %v", err)
	}
	if got := b.Sessions("user-1"); len(got) != 0 {
		t.Fatalf("rejected bindings must not be stored, got %v", got)
	}
}

func TestBindRejectsEmptyAudience(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", nil, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); !errors.Is(err, impl.ErrNoAudience) {
		t.Fatalf("empty audience set must be rejected with ErrNoAudience, got %v", err)
	}
}

func TestBindEnforcesPerUserSessionLimit(t *testing.T) {
	limits := testLimits()
	limits.MaxSessionsPerUser = 2
	b := binderWith(limits)
	now := baseTime()
	if _, err := b.Bind("sess-1", claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")); err != nil {
		t.Fatalf("first session must bind, got %v", err)
	}
	if _, err := b.Bind("sess-2", claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-b")); err != nil {
		t.Fatalf("second session must bind, got %v", err)
	}
	if _, err := b.Bind("sess-3", claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-c")); !errors.Is(err, impl.ErrSessionLimit) {
		t.Fatalf("third session must exceed the per user cap with ErrSessionLimit, got %v", err)
	}
	if got := b.Sessions("user-1"); len(got) != 2 {
		t.Fatalf("session cap must keep exactly 2 sessions, got %d (%v)", len(got), got)
	}
}

func TestValidateAcceptsMatchingToken(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	raw, err := b.Seal(claims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	res, err := b.Validate("sess-1", raw, now)
	if err != nil {
		t.Fatalf("validate must accept the bound token, got %v", err)
	}
	if !res.Valid || res.Reason != "ok" {
		t.Fatalf("validated result = valid %v reason %q, want true/ok", res.Valid, res.Reason)
	}
	if res.UserID != "user-1" || res.SessionID != "sess-1" {
		t.Fatalf("validated identity = %q/%q, want user-1/sess-1", res.UserID, res.SessionID)
	}
	if res.AuthAge != 30*time.Second {
		t.Fatalf("auth age = %v, want 30s measured from the authentication instant", res.AuthAge)
	}
}

func TestValidateRejectsAudienceOutsideBinding(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	tokenClaims := claims
	tokenClaims.Audiences = []string{"admin-api"}
	raw, err := b.Seal(tokenClaims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	res, err := b.Validate("sess-1", raw, now)
	if !errors.Is(err, impl.ErrAudienceMismatch) {
		t.Fatalf("audience outside the binding must be rejected with ErrAudienceMismatch, got %v", err)
	}
	if res.Valid {
		t.Fatalf("rejected token must not be reported as valid")
	}
}

func TestValidateRejectsExpiredToken(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	claims.ExpiresAt = now.Add(-time.Second)
	raw, err := b.Seal(claims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	if _, err := b.Validate("sess-1", raw, now); !errors.Is(err, impl.ErrTokenExpired) {
		t.Fatalf("token past its expiry must be rejected with ErrTokenExpired, got %v", err)
	}
}

func TestValidateRejectsStaleAuthTime(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	claims.AuthTime = now.Add(-9 * time.Hour)
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	raw, err := b.Seal(claims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	if _, err := b.Validate("sess-1", raw, now); !errors.Is(err, impl.ErrAuthAgeExceeded) {
		t.Fatalf("authentication older than the cap must be rejected with ErrAuthAgeExceeded, got %v", err)
	}
}

func TestValidateRejectsAccessTokenMismatch(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	tokenClaims := claims
	tokenClaims.AccessTokenHash = b.Digest("access-2")
	raw, err := b.Seal(tokenClaims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	if _, err := b.Validate("sess-1", raw, now); !errors.Is(err, impl.ErrTokenPairMismatch) {
		t.Fatalf("pairing digest mismatch must be rejected with ErrTokenPairMismatch, got %v", err)
	}
}

func TestValidateRejectsClockAheadBeyondSkew(t *testing.T) {
	b := newBinder()
	now := baseTime()

	future := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	future.AuthTime = now.Add(time.Minute)
	if _, err := b.Bind("sess-ok", future); err != nil {
		t.Fatalf("bind must accept a claim set for later validation, got %v", err)
	}
	raw, err := b.Seal(future)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	res, err := b.Validate("sess-ok", raw, now)
	if err != nil {
		t.Fatalf("authentication inside the skew tolerance must be accepted, got %v", err)
	}
	if res.AuthAge != 0 {
		t.Fatalf("auth age of a slightly future authentication = %v, want 0", res.AuthAge)
	}

	beyond := claimsFor(b, "user-2", []string{"web-app"}, now, "nonce-b")
	beyond.AuthTime = now.Add(3 * time.Minute)
	if _, err := b.Bind("sess-bad", beyond); err != nil {
		t.Fatalf("bind must accept a claim set for later validation, got %v", err)
	}
	rawBad, err := b.Seal(beyond)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	if _, err := b.Validate("sess-bad", rawBad, now); !errors.Is(err, impl.ErrClockAhead) {
		t.Fatalf("authentication beyond the skew tolerance must be rejected with ErrClockAhead, got %v", err)
	}
}

func TestNonceIsSingleUse(t *testing.T) {
	b := newBinder()
	now := baseTime()
	if _, err := b.Bind("sess-1", claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	if err := b.ConsumeNonce("sess-1", "nonce-a"); err != nil {
		t.Fatalf("first nonce consumption must succeed, got %v", err)
	}
	if err := b.ConsumeNonce("sess-1", "nonce-a"); !errors.Is(err, impl.ErrNonceReused) {
		t.Fatalf("second nonce consumption must report ErrNonceReused, got %v", err)
	}
	if err := b.ConsumeNonce("sess-1", "never-issued"); !errors.Is(err, impl.ErrNonceUnknown) {
		t.Fatalf("unknown nonce must report ErrNonceUnknown, got %v", err)
	}
}

func TestValidateConsumesNonceOnSuccess(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	raw, err := b.Seal(claims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	if _, err := b.Validate("sess-1", raw, now); err != nil {
		t.Fatalf("first validation must succeed, got %v", err)
	}
	if _, err := b.Validate("sess-1", raw, now); !errors.Is(err, impl.ErrNonceReused) {
		t.Fatalf("replaying the same token must report ErrNonceReused, got %v", err)
	}
}

func TestNeedsReauthReportsAuthAge(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	claims.AuthTime = now.Add(-time.Hour)
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	if need, reason := b.NeedsReauth("sess-1", now); need || reason != "" {
		t.Fatalf("fresh session must not need reauth, got need=%v reason=%q", need, reason)
	}
	need, reason := b.NeedsReauth("sess-1", now.Add(9*time.Hour))
	if !need {
		t.Fatalf("session past the authentication age must need reauth")
	}
	if reason != "auth_age_exceeded" {
		t.Fatalf("reauth reason = %q, want auth_age_exceeded", reason)
	}
}

func TestSnapshotRestoreKeepsBindings(t *testing.T) {
	b := newBinder()
	now := baseTime()
	claims := claimsFor(b, "user-1", []string{"web-app"}, now, "nonce-a")
	if _, err := b.Bind("sess-1", claims); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	raw, err := b.Seal(claims)
	if err != nil {
		t.Fatalf("seal must succeed, got %v", err)
	}
	snap, err := b.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}

	restored := newBinder()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.Sessions("user-1"); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("restored sessions = %v, want [sess-1]", got)
	}
	if _, err := restored.Validate("sess-1", raw, now); err != nil {
		t.Fatalf("binding and nonce state must survive the restore, got %v", err)
	}
	binding, err := restored.Bind("sess-2", claimsFor(restored, "user-1", []string{"web-app"}, now, "nonce-b"))
	if err != nil {
		t.Fatalf("bind after restore must succeed, got %v", err)
	}
	if binding.Seq <= 1 {
		t.Fatalf("session sequence after restore = %d, want greater than the restored sequence 1", binding.Seq)
	}
}

func TestConcurrentBindSerializesSessions(t *testing.T) {
	limits := testLimits()
	limits.MaxSessionsPerUser = 32
	b := binderWith(limits)
	now := baseTime()

	const workers = 8
	errs := make([]error, workers)
	seqs := make([]int, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			binding, err := b.Bind(
				fmt.Sprintf("sess-%d", i),
				claimsFor(b, "user-1", []string{"web-app"}, now, fmt.Sprintf("nonce-%d", i)),
			)
			errs[i] = err
			seqs[i] = binding.Seq
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent bind %d failed: %v", i, err)
		}
	}
	seen := make(map[int]bool, workers)
	for i, seq := range seqs {
		if seq <= 0 {
			t.Fatalf("concurrent bind %d got sequence %d", i, seq)
		}
		if seen[seq] {
			t.Fatalf("duplicate session sequence %d handed out concurrently", seq)
		}
		seen[seq] = true
	}
	if got := b.Sessions("user-1"); len(got) != workers {
		t.Fatalf("concurrent bindings stored = %d, want %d", len(got), workers)
	}
}
