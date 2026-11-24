package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-07"
)

var guardBaseTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type stepClock struct {
	now time.Time
}

func (c *stepClock) Now() time.Time {
	return c.now
}

func newGuard(t *testing.T, opts impl.GuardOptions) *impl.IdpMixupGuard {
	t.Helper()
	return impl.NewIdpMixupGuard(opts)
}

func registerProvider(t *testing.T, g *impl.IdpMixupGuard) {
	t.Helper()
	if err := g.RegisterIdP("idp-a", "https://idp-a.example.com/oidc", "client-a"); err != nil {
		t.Fatalf("registering the provider failed: %v", err)
	}
}

func TestRegisterIdPStoresIssuerAndClient(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{})
	registerProvider(t, g)
	idp, err := g.IdP("idp-a")
	if err != nil {
		t.Fatalf("reading the registered provider failed: %v", err)
	}
	if idp.ID != "idp-a" || idp.ClientID != "client-a" {
		t.Fatalf("provider = %+v, want idp-a with client-a", idp)
	}
	if idp.Issuer != "https://idp-a.example.com/oidc" {
		t.Fatalf("issuer = %q, want the registered issuer", idp.Issuer)
	}
	if _, err := g.IdP("idp-missing"); !errors.Is(err, impl.ErrUnknownIdP) {
		t.Fatalf("reading an unknown provider = %v, want ErrUnknownIdP", err)
	}
}

func TestRegisterIdPRejectsInvalidFields(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{})
	if err := g.RegisterIdP("", "https://idp-a.example.com", "client-a"); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty provider id = %v, want ErrInvalidRequest", err)
	}
	if err := g.RegisterIdP("idp-a", "https://idp-a.example.com", ""); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty client id = %v, want ErrInvalidRequest", err)
	}
	if err := g.RegisterIdP("idp-a", "http://idp-a.example.com", "client-a"); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("plain http issuer = %v, want ErrInvalidRequest", err)
	}
	registerProvider(t, g)
	if err := g.RegisterIdP("idp-a", "https://idp-a.example.com/oidc", "client-a"); !errors.Is(err, impl.ErrDuplicateIdP) {
		t.Fatalf("re-registering a provider = %v, want ErrDuplicateIdP", err)
	}
}

func TestBeginCreatesHangingRequest(t *testing.T) {
	clock := &stepClock{now: guardBaseTime}
	g := newGuard(t, impl.GuardOptions{TTL: 5 * time.Minute, Now: clock.Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if len(req.State) != 32 {
		t.Fatalf("state %q has length %d, want 32 hex characters", req.State, len(req.State))
	}
	if req.SessionID != "sess-1" || req.IdPID != "idp-a" {
		t.Fatalf("request = %+v, want sess-1 bound to idp-a", req)
	}
	if req.RedirectURI != "https://rp.example.com/callback" {
		t.Fatalf("redirect uri = %q, want the initiating redirect", req.RedirectURI)
	}
	if !req.ExpiresAt.Equal(guardBaseTime.Add(5 * time.Minute)) {
		t.Fatalf("expiry = %s, want %s", req.ExpiresAt, guardBaseTime.Add(5*time.Minute))
	}
	if got := g.Pending("sess-1"); got != 1 {
		t.Fatalf("pending for sess-1 = %d, want 1", got)
	}
	if got := g.PendingTotal(); got != 1 {
		t.Fatalf("total pending = %d, want 1", got)
	}
}

func TestBeginRejectsUnknownProvider(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{})
	registerProvider(t, g)
	if _, err := g.Begin("sess-1", "idp-unknown", "https://rp.example.com/callback"); !errors.Is(err, impl.ErrUnknownIdP) {
		t.Fatalf("begin with an unknown provider = %v, want ErrUnknownIdP", err)
	}
	if got := g.PendingTotal(); got != 0 {
		t.Fatalf("a rejected begin left %d hanging requests", got)
	}
}

func TestBeginRejectsInvalidRequest(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{})
	registerProvider(t, g)
	if _, err := g.Begin("", "idp-a", "https://rp.example.com/callback"); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty session = %v, want ErrInvalidRequest", err)
	}
	if _, err := g.Begin("sess-1", "idp-a", ""); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("empty redirect = %v, want ErrInvalidRequest", err)
	}
	if _, err := g.Begin("sess-1", "idp-a", "http://rp.example.com/callback"); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("plain http redirect = %v, want ErrInvalidRequest", err)
	}
}

func TestPendingLimitPerSession(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{MaxPendingPerSession: 2})
	registerProvider(t, g)
	for i := 0; i < 2; i++ {
		if _, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback"); err != nil {
			t.Fatalf("begin %d failed: %v", i, err)
		}
	}
	if _, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback"); !errors.Is(err, impl.ErrPendingLimit) {
		t.Fatalf("third begin over the limit = %v, want ErrPendingLimit", err)
	}
	if got := g.Pending("sess-1"); got != 2 {
		t.Fatalf("pending after the rejected begin = %d, want 2", got)
	}
}

func TestCallbackUnknownState(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{})
	registerProvider(t, g)
	if _, err := g.Callback("deadbeef", "https://idp-a.example.com/oidc", "code-1"); !errors.Is(err, impl.ErrUnknownState) {
		t.Fatalf("callback with an unknown state = %v, want ErrUnknownState", err)
	}
}

func TestCallbackAcceptsAndConsumes(t *testing.T) {
	clock := &stepClock{now: guardBaseTime}
	g := newGuard(t, impl.GuardOptions{TTL: 5 * time.Minute, Now: clock.Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	res, err := g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1")
	if err != nil {
		t.Fatalf("callback failed: %v", err)
	}
	if res.SessionID != "sess-1" || res.IdPID != "idp-a" || res.Code != "code-1" {
		t.Fatalf("result = %+v, want sess-1/idp-a/code-1", res)
	}
	if res.Issuer != "https://idp-a.example.com/oidc" {
		t.Fatalf("result issuer = %q, want the registered issuer", res.Issuer)
	}
	if !res.CompletedAt.Equal(guardBaseTime) {
		t.Fatalf("completion time = %s, want %s", res.CompletedAt, guardBaseTime)
	}
	if got := g.Pending("sess-1"); got != 0 {
		t.Fatalf("pending after a successful callback = %d, want 0", got)
	}
	if got := g.Completed(); got != 1 {
		t.Fatalf("completed counter = %d, want 1", got)
	}
	if _, err := g.IssuerFor(req.State); !errors.Is(err, impl.ErrUnknownState) {
		t.Fatalf("issuer lookup for a consumed state = %v, want ErrUnknownState", err)
	}
}

func TestCallbackReplayRejected(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{Now: (&stepClock{now: guardBaseTime}).Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1"); err != nil {
		t.Fatalf("first callback failed: %v", err)
	}
	if _, err := g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1"); !errors.Is(err, impl.ErrReplayed) {
		t.Fatalf("replayed callback = %v, want ErrReplayed", err)
	}
	if got := g.Completed(); got != 1 {
		t.Fatalf("completed counter after a replay = %d, want 1", got)
	}
}

func TestCallbackIssuerMismatchKeepsRequest(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{Now: (&stepClock{now: guardBaseTime}).Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	issuer, err := g.IssuerFor(req.State)
	if err != nil {
		t.Fatalf("issuer lookup failed: %v", err)
	}
	if issuer != "https://idp-a.example.com/oidc" {
		t.Fatalf("issuer for the hanging state = %q, want idp-a", issuer)
	}
	if _, err := g.Callback(req.State, "https://evil.example.com", "code-1"); !errors.Is(err, impl.ErrIssuerMismatch) {
		t.Fatalf("callback from a foreign issuer = %v, want ErrIssuerMismatch", err)
	}
	if got := g.Pending("sess-1"); got != 1 {
		t.Fatalf("a mismatched issuer consumed the request, pending = %d", got)
	}
	if _, err := g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1"); err != nil {
		t.Fatalf("callback after the mismatch failed: %v", err)
	}
}

func TestCallbackExpiredRequest(t *testing.T) {
	clock := &stepClock{now: guardBaseTime}
	g := newGuard(t, impl.GuardOptions{TTL: 5 * time.Minute, Now: clock.Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	clock.now = guardBaseTime.Add(6 * time.Minute)
	if _, err := g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1"); !errors.Is(err, impl.ErrExpired) {
		t.Fatalf("callback after the ttl = %v, want ErrExpired", err)
	}
	if got := g.PendingTotal(); got != 0 {
		t.Fatalf("expired request still pending, total = %d", got)
	}
	if got := g.Completed(); got != 0 {
		t.Fatalf("expired callback counted as completed, got %d", got)
	}
}

func TestCallbackRequiresCode(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{Now: (&stepClock{now: guardBaseTime}).Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := g.Callback(req.State, "https://idp-a.example.com/oidc", ""); !errors.Is(err, impl.ErrInvalidRequest) {
		t.Fatalf("callback without a code = %v, want ErrInvalidRequest", err)
	}
	if got := g.Pending("sess-1"); got != 1 {
		t.Fatalf("a codeless callback consumed the request, pending = %d", got)
	}
}

func TestAbandonDropsRequest(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{Now: (&stepClock{now: guardBaseTime}).Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if err := g.Abandon(req.State); err != nil {
		t.Fatalf("abandon failed: %v", err)
	}
	if got := g.Pending("sess-1"); got != 0 {
		t.Fatalf("pending after abandon = %d, want 0", got)
	}
	if err := g.Abandon(req.State); !errors.Is(err, impl.ErrUnknownState) {
		t.Fatalf("second abandon = %v, want ErrUnknownState", err)
	}
	if _, err := g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1"); !errors.Is(err, impl.ErrUnknownState) {
		t.Fatalf("callback after abandon = %v, want ErrUnknownState", err)
	}
}

func TestSnapshotRestoreKeepsPendingRequests(t *testing.T) {
	clock := &stepClock{now: guardBaseTime}
	g := newGuard(t, impl.GuardOptions{TTL: 5 * time.Minute, Now: clock.Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := impl.NewIdpMixupGuard(impl.GuardOptions{Now: clock.Now})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := restored.Pending("sess-1"); got != 1 {
		t.Fatalf("restored pending = %d, want 1", got)
	}
	if _, err := restored.Callback(req.State, "https://idp-a.example.com/oidc", "code-1"); err != nil {
		t.Fatalf("callback after restore failed: %v", err)
	}
	if got := restored.Completed(); got != 1 {
		t.Fatalf("restored completed counter = %d, want 1", got)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restoring garbage = %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentCallbackHasSingleWinner(t *testing.T) {
	g := newGuard(t, impl.GuardOptions{Now: (&stepClock{now: guardBaseTime}).Now})
	registerProvider(t, g)
	req, err := g.Begin("sess-1", "idp-a", "https://rp.example.com/callback")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = g.Callback(req.State, "https://idp-a.example.com/oidc", "code-1")
		}(i)
	}
	wg.Wait()
	wins := 0
	for i := 0; i < workers; i++ {
		if errs[i] == nil {
			wins++
			continue
		}
		if !errors.Is(errs[i], impl.ErrReplayed) {
			t.Fatalf("worker %d got %v, want nil or ErrReplayed", i, errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent callbacks succeeded, want exactly 1", wins)
	}
	if got := g.Completed(); got != 1 {
		t.Fatalf("completed counter = %d after the race, want 1", got)
	}
}
