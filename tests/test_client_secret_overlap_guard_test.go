package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-12"
)

const testClient = "billing-svc"

func overlapStart() time.Time {
	return time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
}

func testSecretLimits() impl.SecretLimits {
	return impl.SecretLimits{
		OverlapWindow:  10 * time.Minute,
		MaxGenerations: 2,
		MaxClients:     16,
	}
}

func newGuard() *impl.ClientSecretOverlapGuard {
	return impl.NewClientSecretOverlapGuard(testSecretLimits())
}

func mustRegister(t *testing.T, g *impl.ClientSecretOverlapGuard, at time.Time) impl.Generation {
	t.Helper()
	gen, err := g.Register(testClient, at)
	if err != nil {
		t.Fatalf("register must succeed, got %v", err)
	}
	return gen
}

func TestRegisterIssuesActiveGeneration(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	gen := mustRegister(t, g, start)
	if gen.Gen != 1 {
		t.Fatalf("first generation = %d, want 1", gen.Gen)
	}
	if gen.Status != "active" {
		t.Fatalf("first generation status = %q, want active", gen.Status)
	}
	if len(gen.Secret) != 64 {
		t.Fatalf("secret length = %d, want a 64 character derived secret", len(gen.Secret))
	}
	if !gen.ExpiresAt.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("generation expiry = %v, want %v", gen.ExpiresAt, start.Add(10*time.Minute))
	}
	if got := g.Generations(testClient); len(got) != 1 || got[0].Gen != 1 {
		t.Fatalf("generation table = %v, want exactly generation 1", got)
	}
}

func TestRegisterRejectsBlankAndDuplicateClient(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	if _, err := g.Register("  ", start); !errors.Is(err, impl.ErrBlankClient) {
		t.Fatalf("blank client id must be rejected with ErrBlankClient, got %v", err)
	}
	mustRegister(t, g, start)
	if _, err := g.Register(testClient, start); !errors.Is(err, impl.ErrClientExists) {
		t.Fatalf("duplicate registration must be rejected with ErrClientExists, got %v", err)
	}
}

func TestRotateRequiresRegisteredClient(t *testing.T) {
	g := newGuard()
	if _, err := g.Rotate("never-registered", overlapStart()); !errors.Is(err, impl.ErrClientNotFound) {
		t.Fatalf("rotation of an unknown client must report ErrClientNotFound, got %v", err)
	}
}

func TestRotateProducesNextGeneration(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	first := mustRegister(t, g, start)
	second, err := g.Rotate(testClient, start.Add(time.Minute))
	if err != nil {
		t.Fatalf("rotation must succeed, got %v", err)
	}
	if second.Gen != 2 {
		t.Fatalf("rotated generation = %d, want 2", second.Gen)
	}
	if second.Secret == first.Secret {
		t.Fatalf("rotation must mint a fresh secret")
	}
	if second.Status != "active" {
		t.Fatalf("rotated generation status = %q, want active", second.Status)
	}
	if got := g.Generations(testClient); len(got) != 2 {
		t.Fatalf("generation table size = %d, want 2 during the overlap window", len(got))
	}
}

func TestRotateReplayAtSameInstantIsIdempotent(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	at := start.Add(time.Minute)
	first, err := g.Rotate(testClient, at)
	if err != nil {
		t.Fatalf("first rotation must succeed, got %v", err)
	}
	replay, err := g.Rotate(testClient, at)
	if err != nil {
		t.Fatalf("replayed rotation must succeed, got %v", err)
	}
	if replay.Gen != first.Gen || replay.Secret != first.Secret {
		t.Fatalf("replay returned generation %d/%q, want the original %d/%q", replay.Gen, replay.Secret, first.Gen, first.Secret)
	}
	if got := g.Generations(testClient); len(got) != 2 {
		t.Fatalf("replayed rotation produced %d generations, want 2", len(got))
	}
}

func TestVerifyAcceptsCurrentSecret(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	gen := mustRegister(t, g, start)
	verdict, err := g.Verify(testClient, gen.Secret, start.Add(time.Minute))
	if err != nil {
		t.Fatalf("current secret must verify, got %v", err)
	}
	if !verdict.OK || verdict.Gen != 1 || verdict.Reason != "ok" {
		t.Fatalf("verdict = %+v, want ok for generation 1", verdict)
	}
}

func TestVerifyAcceptsOverlappingPreviousSecret(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	first := mustRegister(t, g, start)
	if _, err := g.Rotate(testClient, start.Add(time.Minute)); err != nil {
		t.Fatalf("rotation must succeed, got %v", err)
	}
	verdict, err := g.Verify(testClient, first.Secret, start.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("previous secret inside the overlap window must verify, got %v", err)
	}
	if !verdict.OK || verdict.Gen != 1 {
		t.Fatalf("verdict = %+v, want ok for the overlapping generation 1", verdict)
	}
}

func TestVerifyReportsExpiredGeneration(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	first := mustRegister(t, g, start)
	if _, err := g.Rotate(testClient, start.Add(time.Minute)); err != nil {
		t.Fatalf("rotation must succeed, got %v", err)
	}
	verdict, err := g.Verify(testClient, first.Secret, start.Add(11*time.Minute))
	if !errors.Is(err, impl.ErrSecretExpired) {
		t.Fatalf("secret past its overlap window must report ErrSecretExpired, got %v", err)
	}
	if verdict.OK || verdict.Reason != "secret_expired" || verdict.Gen != 1 {
		t.Fatalf("verdict = %+v, want a non ok secret_expired verdict for generation 1", verdict)
	}
}

func TestVerifyReportsUnknownSecret(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	verdict, err := g.Verify(testClient, "0000000000000000", start.Add(time.Minute))
	if !errors.Is(err, impl.ErrSecretUnknown) {
		t.Fatalf("unknown secret must report ErrSecretUnknown, got %v", err)
	}
	if verdict.OK || verdict.Reason != "no_match" || verdict.Gen != 0 {
		t.Fatalf("verdict = %+v, want a non ok no_match verdict", verdict)
	}
}

func TestVerifyRejectsBlankSecret(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	if _, err := g.Verify(testClient, "   ", start.Add(time.Minute)); !errors.Is(err, impl.ErrBadSecret) {
		t.Fatalf("blank secret must be rejected with ErrBadSecret, got %v", err)
	}
}

func TestActivateSwitchesCurrentGeneration(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	second, err := g.Rotate(testClient, start.Add(time.Minute))
	if err != nil {
		t.Fatalf("rotation must succeed, got %v", err)
	}
	if err := g.Activate(testClient, 1); err != nil {
		t.Fatalf("activation of the overlapping generation must succeed, got %v", err)
	}
	gens := g.Generations(testClient)
	if len(gens) != 2 {
		t.Fatalf("generation table size = %d, want 2", len(gens))
	}
	if gens[0].Gen != 1 || gens[0].Status != "active" {
		t.Fatalf("generation 1 status = %q, want active after activation", gens[0].Status)
	}
	if gens[1].Gen != 2 || gens[1].Status != "previous" {
		t.Fatalf("generation 2 status = %q, want previous after activation", gens[1].Status)
	}
	if _, err := g.Verify(testClient, second.Secret, start.Add(2*time.Minute)); err != nil {
		t.Fatalf("deactivated but unexpired secret must still verify, got %v", err)
	}
}

func TestActivateRejectsUnknownGeneration(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	if err := g.Activate(testClient, 99); !errors.Is(err, impl.ErrGenerationNotFound) {
		t.Fatalf("activation of an unknown generation must report ErrGenerationNotFound, got %v", err)
	}
}

func TestExpireSweepsLapsedGenerations(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	if _, err := g.Rotate(testClient, start.Add(time.Minute)); err != nil {
		t.Fatalf("rotation must succeed, got %v", err)
	}
	if swept := g.Expire(start.Add(10*time.Minute + 30*time.Second)); swept != 1 {
		t.Fatalf("expired generations swept = %d, want 1", swept)
	}
	if got := g.Generations(testClient); len(got) != 1 || got[0].Gen != 2 {
		t.Fatalf("generation table after expiry = %v, want only generation 2", got)
	}
}

func TestSnapshotRestorePreservesGenerations(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	first := mustRegister(t, g, start)
	if _, err := g.Rotate(testClient, start.Add(time.Minute)); err != nil {
		t.Fatalf("rotation must succeed, got %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}

	restored := newGuard()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	gens := restored.Generations(testClient)
	if len(gens) != 2 {
		t.Fatalf("restored generation table size = %d, want 2", len(gens))
	}
	verdict, err := restored.Verify(testClient, first.Secret, start.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("restored secret must verify during the overlap window, got %v", err)
	}
	if !verdict.OK || verdict.Gen != 1 {
		t.Fatalf("restored verdict = %+v, want ok for generation 1", verdict)
	}
}

func TestConcurrentRotateAtSameInstantCreatesOneGeneration(t *testing.T) {
	g := newGuard()
	start := overlapStart()
	mustRegister(t, g, start)
	at := start.Add(time.Minute)

	const workers = 8
	results := make([]impl.Generation, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = g.Rotate(testClient, at)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent rotation %d failed: %v", i, err)
		}
	}
	for i, gen := range results {
		if gen.Gen != 2 {
			t.Fatalf("concurrent rotation %d returned generation %d, want 2", i, gen.Gen)
		}
		if gen.Secret != results[0].Secret {
			t.Fatalf("concurrent rotation %d minted a different secret", i)
		}
	}
	if got := g.Generations(testClient); len(got) != 2 {
		t.Fatalf("generation table after concurrent rotation = %d entries, want 2", len(got))
	}
}
