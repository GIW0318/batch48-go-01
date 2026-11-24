package task

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-09"
)

var pinBaseTime = time.Date(2026, 5, 4, 9, 30, 0, 0, time.UTC)

type pinClock struct {
	now time.Time
}

func (c *pinClock) Now() time.Time {
	return c.now
}

func hexFingerprint(seed string) string {
	return strings.Repeat(seed, 32)
}

func pinConnection(t *testing.T, p *impl.PeerIdentityPinner, connID string) impl.Pin {
	t.Helper()
	pin, err := p.Pin(connID, "spiffe://mesh/ns-a/svc-a", hexFingerprint("ab"))
	if err != nil {
		t.Fatalf("pinning %s failed: %v", connID, err)
	}
	return pin
}

func TestPinNormalisesFingerprint(t *testing.T) {
	clock := &pinClock{now: pinBaseTime}
	p := impl.NewPeerIdentityPinner(impl.PinOptions{Now: clock.Now})
	chunks := make([]string, 32)
	for i := range chunks {
		chunks[i] = "AB"
	}
	raw := strings.Join(chunks, ":")
	pin, err := p.Pin("conn-1", "spiffe://mesh/ns-a/svc-a", raw)
	if err != nil {
		t.Fatalf("pin failed: %v", err)
	}
	if pin.Fingerprint != strings.Repeat("ab", 32) {
		t.Fatalf("fingerprint = %q, want the lower case colon free form", pin.Fingerprint)
	}
	if pin.Generation != 1 {
		t.Fatalf("first pin generation = %d, want 1", pin.Generation)
	}
	if !pin.PinnedAt.Equal(pinBaseTime) {
		t.Fatalf("pin time = %s, want %s", pin.PinnedAt, pinBaseTime)
	}
	if pin.ConnID != "conn-1" || pin.Principal != "spiffe://mesh/ns-a/svc-a" {
		t.Fatalf("pin = %+v, want conn-1 with the peer principal", pin)
	}
}

func TestPinRejectsInvalidInput(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	if _, err := p.Pin("", "spiffe://mesh/svc", hexFingerprint("ab")); !errors.Is(err, impl.ErrInvalidPin) {
		t.Fatalf("empty connection id = %v, want ErrInvalidPin", err)
	}
	if _, err := p.Pin("conn-1", "", hexFingerprint("ab")); !errors.Is(err, impl.ErrInvalidPin) {
		t.Fatalf("empty principal = %v, want ErrInvalidPin", err)
	}
	if _, err := p.Pin("conn-1", "spiffe://mesh/svc", "abcd"); !errors.Is(err, impl.ErrInvalidFingerprint) {
		t.Fatalf("short fingerprint = %v, want ErrInvalidFingerprint", err)
	}
	if _, err := p.Pin("conn-1", "spiffe://mesh/svc", strings.Repeat("zz", 32)); !errors.Is(err, impl.ErrInvalidFingerprint) {
		t.Fatalf("non hex fingerprint = %v, want ErrInvalidFingerprint", err)
	}
	if got := p.ConnectionCount(); got != 0 {
		t.Fatalf("rejected pins left %d connections", got)
	}
}

func TestPinRejectsSecondPinOnSameConnection(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	first := pinConnection(t, p, "conn-1")
	if _, err := p.Pin("conn-1", "spiffe://mesh/other", hexFingerprint("cd")); !errors.Is(err, impl.ErrAlreadyPinned) {
		t.Fatalf("second pin on the same connection = %v, want ErrAlreadyPinned", err)
	}
	current, err := p.Check("conn-1")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if current.Principal != first.Principal || current.Fingerprint != first.Fingerprint {
		t.Fatalf("check returned %+v, want the first pin %+v", current, first)
	}
}

func TestConnectionLimitAndRelease(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{MaxConnections: 2})
	pinConnection(t, p, "conn-1")
	pinConnection(t, p, "conn-2")
	if _, err := p.Pin("conn-3", "spiffe://mesh/svc", hexFingerprint("ab")); !errors.Is(err, impl.ErrConnLimit) {
		t.Fatalf("third connection over the limit = %v, want ErrConnLimit", err)
	}
	if got := p.ConnectionCount(); got != 2 {
		t.Fatalf("connection count = %d, want 2", got)
	}
	if err := p.Close("conn-1"); err != nil {
		t.Fatalf("closing a pinned connection failed: %v", err)
	}
	if _, err := p.Pin("conn-3", "spiffe://mesh/svc", hexFingerprint("ab")); err != nil {
		t.Fatalf("pin after releasing a slot failed: %v", err)
	}
	if got := p.ConnectionCount(); got != 2 {
		t.Fatalf("connection count after reuse = %d, want 2", got)
	}
}

func TestCheckRequiresPinnedConnection(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	if _, err := p.Check("conn-unknown"); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("check on an unknown connection = %v, want ErrUnknownConn", err)
	}
	pinConnection(t, p, "conn-1")
	current, err := p.Check("conn-1")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if current.Principal != "spiffe://mesh/ns-a/svc-a" || current.Generation != 1 {
		t.Fatalf("check returned %+v, want the pinned principal at generation 1", current)
	}
}

func TestAuthorizeUsesPinnedPrincipal(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	pinConnection(t, p, "conn-1")
	ok, err := p.Authorize("conn-1", "spiffe://mesh/ns-a/svc-a")
	if err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	if !ok {
		t.Fatalf("the pinned principal was not authorized")
	}
	ok, err = p.Authorize("conn-1", "spiffe://mesh/ns-b/svc-b")
	if err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	if ok {
		t.Fatalf("a foreign principal was authorized on the pinned connection")
	}
	if _, err := p.Authorize("conn-unknown", "spiffe://mesh/ns-a/svc-a"); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("authorize on an unknown connection = %v, want ErrUnknownConn", err)
	}
}

func TestRotateAndCommitNewIdentity(t *testing.T) {
	clock := &pinClock{now: pinBaseTime}
	p := impl.NewPeerIdentityPinner(impl.PinOptions{Now: clock.Now})
	pinConnection(t, p, "conn-1")
	if _, err := p.Rotate("conn-unknown", "spiffe://mesh/ns-c/svc-c", hexFingerprint("ef")); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("rotating an unknown connection = %v, want ErrUnknownConn", err)
	}
	if _, err := p.Rotate("conn-1", "spiffe://mesh/ns-c/svc-c", "not-hex"); !errors.Is(err, impl.ErrInvalidFingerprint) {
		t.Fatalf("rotating with a bad fingerprint = %v, want ErrInvalidFingerprint", err)
	}
	if _, err := p.Rotate("conn-1", "", hexFingerprint("ef")); !errors.Is(err, impl.ErrInvalidPin) {
		t.Fatalf("rotating to an empty principal = %v, want ErrInvalidPin", err)
	}
	generation, err := p.Rotate("conn-1", "spiffe://mesh/ns-c/svc-c", hexFingerprint("ef"))
	if err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if generation != 2 {
		t.Fatalf("new generation = %d, want 2", generation)
	}
	clock.now = pinBaseTime.Add(time.Minute)
	if err := p.Commit("conn-1", generation); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	current, err := p.Check("conn-1")
	if err != nil {
		t.Fatalf("check after commit failed: %v", err)
	}
	if current.Principal != "spiffe://mesh/ns-c/svc-c" {
		t.Fatalf("principal after commit = %q, want the renegotiated one", current.Principal)
	}
	if current.Fingerprint != hexFingerprint("ef") || current.Generation != 2 {
		t.Fatalf("pin after commit = %+v, want the new fingerprint at generation 2", current)
	}
}

func TestCommitRejectsStaleGeneration(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	pinConnection(t, p, "conn-1")
	generation, err := p.Rotate("conn-1", "spiffe://mesh/ns-c/svc-c", hexFingerprint("ef"))
	if err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if err := p.Commit("conn-1", generation-1); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("committing the old generation = %v, want ErrStaleGeneration", err)
	}
	current, err := p.Check("conn-1")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if current.Principal != "spiffe://mesh/ns-a/svc-a" || current.Generation != 1 {
		t.Fatalf("a rejected commit changed the pin to %+v", current)
	}
	pinConnection(t, p, "conn-2")
	if err := p.Commit("conn-2", 1); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("committing without a rotation = %v, want ErrStaleGeneration", err)
	}
}

func TestRequestTracking(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	pinConnection(t, p, "conn-1")
	for i := 0; i < 2; i++ {
		if err := p.BeginRequest("conn-1"); err != nil {
			t.Fatalf("begin request %d failed: %v", i, err)
		}
	}
	if got := p.Pending("conn-1"); got != 2 {
		t.Fatalf("pending = %d, want 2", got)
	}
	if err := p.EndRequest("conn-1"); err != nil {
		t.Fatalf("end request failed: %v", err)
	}
	if got := p.Pending("conn-1"); got != 1 {
		t.Fatalf("pending after one completion = %d, want 1", got)
	}
	if err := p.EndRequest("conn-1"); err != nil {
		t.Fatalf("second end request failed: %v", err)
	}
	if err := p.EndRequest("conn-1"); !errors.Is(err, impl.ErrNoPending) {
		t.Fatalf("ending without a pending request = %v, want ErrNoPending", err)
	}
}

func TestPendingLimitPerConnection(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{MaxPendingPerConn: 2})
	pinConnection(t, p, "conn-1")
	for i := 0; i < 2; i++ {
		if err := p.BeginRequest("conn-1"); err != nil {
			t.Fatalf("begin request %d failed: %v", i, err)
		}
	}
	if err := p.BeginRequest("conn-1"); !errors.Is(err, impl.ErrPendingLimit) {
		t.Fatalf("third pending request = %v, want ErrPendingLimit", err)
	}
	if got := p.Pending("conn-1"); got != 2 {
		t.Fatalf("pending = %d after the rejected request, want 2", got)
	}
}

func TestDrainRejectsRequestsAfterDeadline(t *testing.T) {
	clock := &pinClock{now: pinBaseTime}
	p := impl.NewPeerIdentityPinner(impl.PinOptions{Now: clock.Now})
	pinConnection(t, p, "conn-1")
	pinConnection(t, p, "conn-2")
	if err := p.BeginRequest("conn-1"); err != nil {
		t.Fatalf("begin request failed: %v", err)
	}
	if err := p.Drain("conn-1", pinBaseTime.Add(-time.Second)); err != nil {
		t.Fatalf("drain failed: %v", err)
	}
	if err := p.BeginRequest("conn-1"); !errors.Is(err, impl.ErrDraining) {
		t.Fatalf("begin after the drain deadline = %v, want ErrDraining", err)
	}
	if got := p.Pending("conn-1"); got != 1 {
		t.Fatalf("pending = %d after the rejected request, want the in flight 1", got)
	}
	if err := p.EndRequest("conn-1"); err != nil {
		t.Fatalf("draining connection must still complete in flight work: %v", err)
	}
	if err := p.Drain("conn-2", pinBaseTime.Add(time.Minute)); err != nil {
		t.Fatalf("drain failed: %v", err)
	}
	if err := p.BeginRequest("conn-2"); err != nil {
		t.Fatalf("begin before the drain deadline must be accepted, got %v", err)
	}
	if err := p.Drain("conn-missing", pinBaseTime); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("draining an unknown connection = %v, want ErrUnknownConn", err)
	}
}

func TestCloseDropsPinnedIdentity(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	pinConnection(t, p, "conn-1")
	if err := p.Close("conn-1"); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if got := p.ConnectionCount(); got != 0 {
		t.Fatalf("connection count after close = %d, want 0", got)
	}
	if _, err := p.Check("conn-1"); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("check after close = %v, want ErrUnknownConn", err)
	}
	if err := p.BeginRequest("conn-1"); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("begin after close = %v, want ErrUnknownConn", err)
	}
	if err := p.Close("conn-1"); !errors.Is(err, impl.ErrUnknownConn) {
		t.Fatalf("closing an unknown connection = %v, want ErrUnknownConn", err)
	}
}

func TestSnapshotRestoreKeepsPinsAndGeneration(t *testing.T) {
	clock := &pinClock{now: pinBaseTime}
	p := impl.NewPeerIdentityPinner(impl.PinOptions{Now: clock.Now})
	pinConnection(t, p, "conn-1")
	generation, err := p.Rotate("conn-1", "spiffe://mesh/ns-c/svc-c", hexFingerprint("ef"))
	if err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if err := p.Commit("conn-1", generation); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if err := p.BeginRequest("conn-1"); err != nil {
		t.Fatalf("begin request failed: %v", err)
	}
	data, err := p.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := impl.NewPeerIdentityPinner(impl.PinOptions{Now: clock.Now})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := restored.ConnectionCount(); got != 1 {
		t.Fatalf("restored connection count = %d, want 1", got)
	}
	current, err := restored.Check("conn-1")
	if err != nil {
		t.Fatalf("check after restore failed: %v", err)
	}
	if current.Principal != "spiffe://mesh/ns-c/svc-c" || current.Generation != 2 {
		t.Fatalf("restored pin = %+v, want the committed identity at generation 2", current)
	}
	if got := restored.Pending("conn-1"); got != 1 {
		t.Fatalf("restored pending requests = %d, want 1", got)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restoring garbage = %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentPinHasSingleWinner(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = p.Pin("conn-1", "spiffe://mesh/ns-a/svc-a", hexFingerprint("ab"))
		}(i)
	}
	wg.Wait()
	wins := 0
	for i := 0; i < workers; i++ {
		if errs[i] == nil {
			wins++
			continue
		}
		if !errors.Is(errs[i], impl.ErrAlreadyPinned) {
			t.Fatalf("worker %d got %v, want nil or ErrAlreadyPinned", i, errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent pins succeeded, want exactly 1", wins)
	}
	if got := p.ConnectionCount(); got != 1 {
		t.Fatalf("connection count after the race = %d, want 1", got)
	}
}

func TestConcurrentRotateHasSingleWinner(t *testing.T) {
	p := impl.NewPeerIdentityPinner(impl.PinOptions{})
	pinConnection(t, p, "conn-1")
	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = p.Rotate("conn-1", "spiffe://mesh/ns-c/svc-c", hexFingerprint("ef"))
		}(i)
	}
	wg.Wait()
	wins := 0
	for i := 0; i < workers; i++ {
		if errs[i] == nil {
			wins++
			continue
		}
		if !errors.Is(errs[i], impl.ErrRotationPending) {
			t.Fatalf("worker %d got %v, want nil or ErrRotationPending", i, errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent renegotiations started, want exactly 1", wins)
	}
	if err := p.Commit("conn-1", 2); err != nil {
		t.Fatalf("committing the single winner failed: %v", err)
	}
}
