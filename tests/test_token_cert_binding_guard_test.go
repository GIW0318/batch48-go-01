package task

import (
	"errors"
	"strings"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-14"
)

const testClient = "payments-api"

func fingerprintA() string {
	return strings.Repeat("a1", 32)
}

func fingerprintB() string {
	return strings.Repeat("0f", 32)
}

// colonise turns a compact fingerprint into the upper case colon separated form
// clients copy from their certificate viewers.
func colonise(fp string) string {
	var parts []string
	for i := 0; i < len(fp); i += 2 {
		parts = append(parts, strings.ToUpper(fp[i:i+2]))
	}
	return strings.Join(parts, ":")
}

func testCertLimits() impl.CertLimits {
	return impl.CertLimits{
		MaxTokensPerClient: 4,
		MaxTokenTTL:        time.Hour,
		ProofTTL:           5 * time.Minute,
	}
}

func newCertGuard() *impl.TokenCertBindingGuard {
	return impl.NewTokenCertBindingGuard(testCertLimits())
}

func mustIssue(t *testing.T, g *impl.TokenCertBindingGuard, fp string) impl.Token {
	t.Helper()
	token, err := g.Issue(testClient, fp, 30*time.Minute)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	return token
}

func TestIssueRejectsBlankInputs(t *testing.T) {
	g := newCertGuard()
	if _, err := g.Issue("  ", fingerprintA(), 30*time.Minute); !errors.Is(err, impl.ErrBlankClient) {
		t.Fatalf("blank client must be rejected with ErrBlankClient, got %v", err)
	}
	if _, err := g.Issue(testClient, "   ", 30*time.Minute); !errors.Is(err, impl.ErrBlankFingerprint) {
		t.Fatalf("blank fingerprint must be rejected with ErrBlankFingerprint, got %v", err)
	}
}

func TestIssueNormalizesFingerprint(t *testing.T) {
	g := newCertGuard()
	token, err := g.Issue(testClient, colonise(fingerprintA()), 30*time.Minute)
	if err != nil {
		t.Fatalf("issue must accept a colon separated fingerprint, got %v", err)
	}
	if token.Fingerprint != fingerprintA() {
		t.Fatalf("stored fingerprint = %q, want the lowercase colon free form", token.Fingerprint)
	}
	if !token.Bound {
		t.Fatalf("issued token must start bound")
	}
	if token.TokenID == "" || token.ClientID != testClient {
		t.Fatalf("token identity = %q/%q, want a token id for %s", token.TokenID, token.ClientID, testClient)
	}
}

func TestIssueEnforcesTTLCap(t *testing.T) {
	g := newCertGuard()
	if _, err := g.Issue(testClient, fingerprintA(), 0); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("zero ttl must be rejected with ErrInvalidTTL, got %v", err)
	}
	token, err := g.Issue(testClient, fingerprintA(), 10*time.Hour)
	if err != nil {
		t.Fatalf("issue must succeed with a long ttl, got %v", err)
	}
	if got := token.ExpiresAt.Sub(token.IssuedAt); got != time.Hour {
		t.Fatalf("token lifetime = %v, want it clamped to the one hour cap", got)
	}
}

func TestIssueEnforcesPerClientCap(t *testing.T) {
	limits := testCertLimits()
	limits.MaxTokensPerClient = 2
	g := impl.NewTokenCertBindingGuard(limits)
	mustIssue(t, g, fingerprintA())
	mustIssue(t, g, fingerprintB())
	if _, err := g.Issue(testClient, fingerprintA(), 30*time.Minute); !errors.Is(err, impl.ErrBindingLimit) {
		t.Fatalf("third binding must exceed the per client cap with ErrBindingLimit, got %v", err)
	}
	if got := g.Tokens(testClient); len(got) != 2 {
		t.Fatalf("live bindings = %d, want 2", len(got))
	}
}

func TestIssueProducesDistinctTokenIDs(t *testing.T) {
	g := newCertGuard()
	first := mustIssue(t, g, fingerprintA())
	second := mustIssue(t, g, fingerprintB())
	if first.TokenID == second.TokenID {
		t.Fatalf("two issued tokens share the id %q", first.TokenID)
	}
	if got := g.Tokens(testClient); len(got) != 2 {
		t.Fatalf("live bindings = %d, want 2", len(got))
	}
}

func TestIssuePendingRequiresBindBeforeUse(t *testing.T) {
	g := newCertGuard()
	token, err := g.IssuePending(testClient, 30*time.Minute)
	if err != nil {
		t.Fatalf("pending issue must succeed, got %v", err)
	}
	if token.Bound {
		t.Fatalf("pending token must not report itself as bound")
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("presenting a pending token must build the wire string, got %v", err)
	}
	verdict, err := g.VerifyPresented(raw, fingerprintA(), time.Now().UTC())
	if !errors.Is(err, impl.ErrTokenUnbound) {
		t.Fatalf("unbound token must be rejected with ErrTokenUnbound, got %v", err)
	}
	if verdict.OK || verdict.Reason != "token_unbound" {
		t.Fatalf("verdict = %+v, want a non ok token_unbound verdict", verdict)
	}
}

func TestBindAttachesPendingToken(t *testing.T) {
	g := newCertGuard()
	token, err := g.IssuePending(testClient, 30*time.Minute)
	if err != nil {
		t.Fatalf("pending issue must succeed, got %v", err)
	}
	if err := g.Bind(token.TokenID, colonise(fingerprintA())); err != nil {
		t.Fatalf("bind must succeed, got %v", err)
	}
	tokens := g.Tokens(testClient)
	if len(tokens) != 1 || !tokens[0].Bound || tokens[0].Fingerprint != fingerprintA() {
		t.Fatalf("bound token = %+v, want it bound to %q", tokens, fingerprintA())
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	verdict, err := g.VerifyPresented(raw, fingerprintA(), time.Now().UTC())
	if err != nil {
		t.Fatalf("bound token must verify with its certificate, got %v", err)
	}
	if !verdict.OK || verdict.TokenID != token.TokenID {
		t.Fatalf("verdict = %+v, want ok for %s", verdict, token.TokenID)
	}
}

func TestBindRejectsAlreadyBoundToken(t *testing.T) {
	g := newCertGuard()
	token := mustIssue(t, g, fingerprintA())
	if err := g.Bind(token.TokenID, fingerprintB()); !errors.Is(err, impl.ErrAlreadyBound) {
		t.Fatalf("binding an already bound token must report ErrAlreadyBound, got %v", err)
	}
}

func TestBindRejectsUnknownToken(t *testing.T) {
	g := newCertGuard()
	if err := g.Bind("no-such-token", fingerprintA()); !errors.Is(err, impl.ErrUnknownToken) {
		t.Fatalf("binding an unknown token must report ErrUnknownToken, got %v", err)
	}
}

func TestVerifyPresentedAcceptsBoundCert(t *testing.T) {
	g := newCertGuard()
	token := mustIssue(t, g, fingerprintA())
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	verdict, err := g.VerifyPresented(raw, colonise(fingerprintA()), time.Now().UTC())
	if err != nil {
		t.Fatalf("bound certificate must be accepted, got %v", err)
	}
	if !verdict.OK || verdict.Reason != "ok" || verdict.ClientID != testClient {
		t.Fatalf("verdict = %+v, want ok for %s", verdict, testClient)
	}
}

func TestVerifyPresentedRejectsExpiredToken(t *testing.T) {
	g := newCertGuard()
	token := mustIssue(t, g, fingerprintA())
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed, got %v", err)
	}
	verdict, err := g.VerifyPresented(raw, fingerprintA(), token.IssuedAt.Add(2*time.Hour))
	if !errors.Is(err, impl.ErrTokenExpired) {
		t.Fatalf("expired token must be rejected with ErrTokenExpired, got %v", err)
	}
	if verdict.OK || verdict.Reason != "token_expired" {
		t.Fatalf("verdict = %+v, want a non ok token_expired verdict", verdict)
	}
}

func TestVerifyPresentedRejectsUnknownToken(t *testing.T) {
	g := newCertGuard()
	if _, err := g.VerifyPresented("tcb1.missing-token.deadbeefdeadbeef", fingerprintA(), time.Now().UTC()); !errors.Is(err, impl.ErrUnknownToken) {
		t.Fatalf("presentation of an unknown token must report ErrUnknownToken, got %v", err)
	}
	if _, err := g.VerifyPresented("not-a-token", fingerprintA(), time.Now().UTC()); !errors.Is(err, impl.ErrMalformedToken) {
		t.Fatalf("malformed token string must report ErrMalformedToken, got %v", err)
	}
}

func TestRevokeBindingDisablesToken(t *testing.T) {
	g := newCertGuard()
	token := mustIssue(t, g, fingerprintA())
	if err := g.RevokeBinding(token.TokenID); err != nil {
		t.Fatalf("revoke must succeed, got %v", err)
	}
	if got := g.Tokens(testClient); len(got) != 0 {
		t.Fatalf("revoked binding must leave no live tokens, got %v", got)
	}
	if _, err := g.Present(token.TokenID); !errors.Is(err, impl.ErrUnknownToken) {
		t.Fatalf("revoked token must no longer be presentable, got %v", err)
	}
}

func TestRebindAfterValidProofMovesBinding(t *testing.T) {
	g := newCertGuard()
	token := mustIssue(t, g, fingerprintA())
	now := time.Now().UTC()
	proof := impl.Proof{
		TokenID:   token.TokenID,
		Subject:   "user-1",
		Method:    "reauth",
		IssuedAt:  now.Add(-30 * time.Second),
		ExpiresAt: now.Add(2 * time.Minute),
	}
	if err := g.Rebind(token.TokenID, colonise(fingerprintB()), proof); err != nil {
		t.Fatalf("rebind after a fresh reauthentication must succeed, got %v", err)
	}
	tokens := g.Tokens(testClient)
	if len(tokens) != 1 || tokens[0].Fingerprint != fingerprintB() {
		t.Fatalf("binding after rebind = %+v, want %q", tokens, fingerprintB())
	}
	raw, err := g.Present(token.TokenID)
	if err != nil {
		t.Fatalf("present must succeed after rebind, got %v", err)
	}
	verdict, err := g.VerifyPresented(raw, fingerprintB(), time.Now().UTC())
	if err != nil {
		t.Fatalf("new certificate must verify after rebind, got %v", err)
	}
	if !verdict.OK || verdict.TokenID != token.TokenID {
		t.Fatalf("verdict = %+v, want ok for %s", verdict, token.TokenID)
	}
}

func TestSnapshotRestoreKeepsBindings(t *testing.T) {
	g := newCertGuard()
	first := mustIssue(t, g, fingerprintA())
	mustIssue(t, g, fingerprintB())
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}

	restored := newCertGuard()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.Tokens(testClient); len(got) != 2 {
		t.Fatalf("restored bindings = %d, want 2", len(got))
	}
	raw, err := restored.Present(first.TokenID)
	if err != nil {
		t.Fatalf("restored token must be presentable, got %v", err)
	}
	if _, err := restored.VerifyPresented(raw, fingerprintA(), time.Now().UTC()); err != nil {
		t.Fatalf("restored binding must verify, got %v", err)
	}
}
