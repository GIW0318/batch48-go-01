package task

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-d-02"
)

var dekKey = []byte("0123456789abcdef0123456789abcdef")

func mustRegisterDEK(t *testing.T, g *impl.KeyWrapGuard, id string, maxWraps int) {
	t.Helper()
	if err := g.RegisterDEK(id, dekKey, maxWraps); err != nil {
		t.Fatalf("register dek %s: %v", id, err)
	}
}

func mustWrap(t *testing.T, g *impl.KeyWrapGuard, dekID, subject string) impl.Envelope {
	t.Helper()
	env, err := g.Wrap(dekID, subject)
	if err != nil {
		t.Fatalf("wrap %s for %s: %v", dekID, subject, err)
	}
	return env
}

func TestRegisterDEKRejectsDuplicate(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	if err := g.RegisterDEK("dek-a", []byte("ffffffffffffffff"), 4); !errors.Is(err, impl.ErrDEKExists) {
		t.Fatalf("duplicate dek error = %v, want %v", err, impl.ErrDEKExists)
	}
}

func TestRegisterDEKRejectsShortKey(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	if err := g.RegisterDEK("dek-a", []byte("short"), 4); !errors.Is(err, impl.ErrInvalidDEK) {
		t.Fatalf("short key error = %v, want %v", err, impl.ErrInvalidDEK)
	}
	if err := g.RegisterDEK("dek-b", dekKey, 0); !errors.Is(err, impl.ErrInvalidDEK) {
		t.Fatalf("zero wrap limit error = %v, want %v", err, impl.ErrInvalidDEK)
	}
}

func TestWrapBindsPurposeSubjectGeneration(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	env := mustWrap(t, g, "dek-a", "svc/backup")
	if env.ID == "" {
		t.Fatalf("envelope id must not be empty")
	}
	if env.DEKID != "dek-a" {
		t.Fatalf("envelope dek = %q, want %q", env.DEKID, "dek-a")
	}
	if env.Subject != "svc/backup" {
		t.Fatalf("envelope subject = %q, want %q", env.Subject, "svc/backup")
	}
	if env.Purpose != impl.PurposeData {
		t.Fatalf("envelope purpose = %q, want %q", env.Purpose, impl.PurposeData)
	}
	if env.Generation != 1 {
		t.Fatalf("envelope generation = %d, want 1", env.Generation)
	}
	if env.Algorithm != impl.AlgorithmAES256GCM {
		t.Fatalf("envelope algorithm = %q, want %q", env.Algorithm, impl.AlgorithmAES256GCM)
	}
	if len(env.Wrapped) == 0 {
		t.Fatalf("wrapped material must not be empty")
	}
}

func TestWrapRejectsUnknownDEK(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	if _, err := g.Wrap("dek-missing", "svc/backup"); !errors.Is(err, impl.ErrDEKNotFound) {
		t.Fatalf("unknown dek wrap error = %v, want %v", err, impl.ErrDEKNotFound)
	}
}

func TestWrapEnforcesPerDEKWrapLimit(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 2)
	mustWrap(t, g, "dek-a", "svc/a")
	mustWrap(t, g, "dek-a", "svc/b")
	if _, err := g.Wrap("dek-a", "svc/c"); !errors.Is(err, impl.ErrWrapQuotaExhausted) {
		t.Fatalf("wrap over dek limit error = %v, want %v", err, impl.ErrWrapQuotaExhausted)
	}
	st, err := g.DEKStatus("dek-a")
	if err != nil {
		t.Fatalf("dek status: %v", err)
	}
	if st.Wraps != 2 {
		t.Fatalf("dek wrap count = %d, want 2", st.Wraps)
	}
}

func TestWrapEnforcesGenerationQuota(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 10)
	gen, err := g.RotateKEK(impl.AlgorithmChaCha20, 2)
	if err != nil {
		t.Fatalf("rotate kek: %v", err)
	}
	if gen != 2 {
		t.Fatalf("rotated generation = %d, want 2", gen)
	}
	mustWrap(t, g, "dek-a", "svc/a")
	mustWrap(t, g, "dek-a", "svc/b")
	if _, err := g.Wrap("dek-a", "svc/c"); !errors.Is(err, impl.ErrKEKQuotaExhausted) {
		t.Fatalf("wrap over generation quota error = %v, want %v", err, impl.ErrKEKQuotaExhausted)
	}
	if got := g.QuotaRemaining(); got != 0 {
		t.Fatalf("quota remaining = %d, want 0", got)
	}
}

func TestUnwrapReturnsRegisteredKey(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	env := mustWrap(t, g, "dek-a", "svc/backup")
	got, err := g.Unwrap(env)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, dekKey) {
		t.Fatalf("unwrapped key = %x, want %x", got, dekKey)
	}
}

func TestUnwrapRejectsForeignPurpose(t *testing.T) {
	writer := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, writer, "dek-a", 4)
	env := mustWrap(t, writer, "dek-a", "svc/backup")
	reader := impl.NewKeyWrapGuard(impl.PurposeExport)
	if _, err := reader.Unwrap(env); !errors.Is(err, impl.ErrPurposeMismatch) {
		t.Fatalf("foreign purpose error = %v, want %v", err, impl.ErrPurposeMismatch)
	}
	if got := reader.Stats().Failures; got != 1 {
		t.Fatalf("failure count on foreign purpose = %d, want 1", got)
	}
}

func TestUnwrapRejectsSubjectTamper(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	env := mustWrap(t, g, "dek-a", "svc/backup")
	tampered := env
	tampered.Subject = "svc/other"
	if _, err := g.Unwrap(tampered); !errors.Is(err, impl.ErrSubjectMismatch) {
		t.Fatalf("tampered subject error = %v, want %v", err, impl.ErrSubjectMismatch)
	}
	if got := g.Stats().Failures; got != 1 {
		t.Fatalf("failure count = %d, want 1", got)
	}
}

func TestBreakerOpensAfterThreshold(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 10)
	valid := mustWrap(t, g, "dek-a", "svc/backup")
	if err := g.SetFailureThreshold(3); err != nil {
		t.Fatalf("set failure threshold: %v", err)
	}
	for i := 0; i < 3; i++ {
		tampered := valid
		tampered.Subject = "svc/tampered"
		if _, err := g.Unwrap(tampered); !errors.Is(err, impl.ErrSubjectMismatch) {
			t.Fatalf("tampered attempt %d error = %v, want %v", i+1, err, impl.ErrSubjectMismatch)
		}
	}
	if !g.BreakerOpen() {
		t.Fatalf("breaker must be open after 3 consecutive failures")
	}
	if _, err := g.Unwrap(valid); !errors.Is(err, impl.ErrBreakerOpen) {
		t.Fatalf("unwrap while breaker open error = %v, want %v", err, impl.ErrBreakerOpen)
	}
}

func TestRotateKEKAdvancesGeneration(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	gen, err := g.RotateKEK(impl.AlgorithmChaCha20, 8)
	if err != nil {
		t.Fatalf("rotate kek: %v", err)
	}
	if gen != 2 || g.Generation() != 2 {
		t.Fatalf("generation after rotation = %d (vault %d), want 2", gen, g.Generation())
	}
	env := mustWrap(t, g, "dek-a", "svc/backup")
	if env.Generation != 2 {
		t.Fatalf("envelope generation = %d, want 2", env.Generation)
	}
	if env.Algorithm != impl.AlgorithmChaCha20 {
		t.Fatalf("envelope algorithm = %q, want %q", env.Algorithm, impl.AlgorithmChaCha20)
	}
}

func TestResealBatchMovesEnvelopesToCurrentGeneration(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	mustRegisterDEK(t, g, "dek-b", 4)
	mustWrap(t, g, "dek-a", "svc/a")
	mustWrap(t, g, "dek-b", "svc/b")
	if _, err := g.RotateKEK(impl.AlgorithmChaCha20, 10); err != nil {
		t.Fatalf("rotate kek: %v", err)
	}
	rep, err := g.ResealBatch(impl.PurposeData)
	if err != nil {
		t.Fatalf("reseal batch: %v", err)
	}
	if rep.From != 1 || rep.To != 2 {
		t.Fatalf("reseal report = %+v, want 1 -> 2", rep)
	}
	if rep.Resealed != 2 {
		t.Fatalf("resealed count = %d, want 2", rep.Resealed)
	}
	st, err := g.DEKStatus("dek-a")
	if err != nil {
		t.Fatalf("dek status: %v", err)
	}
	if st.Generation != 2 || st.Algorithm != impl.AlgorithmChaCha20 {
		t.Fatalf("dek after reseal = gen %d alg %q, want gen 2 alg %q", st.Generation, st.Algorithm, impl.AlgorithmChaCha20)
	}
}

func TestResealBatchAbortsWhenQuotaShort(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 4)
	mustRegisterDEK(t, g, "dek-b", 4)
	mustWrap(t, g, "dek-a", "svc/a")
	mustWrap(t, g, "dek-b", "svc/b")
	if _, err := g.RotateKEK(impl.AlgorithmChaCha20, 1); err != nil {
		t.Fatalf("rotate kek: %v", err)
	}
	if _, err := g.ResealBatch(impl.PurposeData); !errors.Is(err, impl.ErrKEKQuotaExhausted) {
		t.Fatalf("short quota reseal error = %v, want %v", err, impl.ErrKEKQuotaExhausted)
	}
	st, err := g.DEKStatus("dek-a")
	if err != nil {
		t.Fatalf("dek status: %v", err)
	}
	if st.Generation != 1 {
		t.Fatalf("dek generation after aborted reseal = %d, want 1", st.Generation)
	}
	if got := g.Stats().Resealed; got != 0 {
		t.Fatalf("resealed counter after aborted batch = %d, want 0", got)
	}
	if got := g.QuotaRemaining(); got != 1 {
		t.Fatalf("quota remaining after aborted batch = %d, want 1", got)
	}
}

func TestSnapshotRestoreKeepsQuotaAndBreaker(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 6)
	env := mustWrap(t, g, "dek-a", "svc/a")
	mustWrap(t, g, "dek-a", "svc/b")
	mustWrap(t, g, "dek-a", "svc/c")
	if err := g.SetFailureThreshold(2); err != nil {
		t.Fatalf("set failure threshold: %v", err)
	}
	for i := 0; i < 2; i++ {
		tampered := env
		tampered.Subject = "svc/tampered"
		if _, err := g.Unwrap(tampered); !errors.Is(err, impl.ErrSubjectMismatch) {
			t.Fatalf("tampered attempt %d error = %v, want %v", i+1, err, impl.ErrSubjectMismatch)
		}
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewKeyWrapGuard(impl.PurposeData)
	if err := restored.SetFailureThreshold(9); err != nil {
		t.Fatalf("set threshold before restore: %v", err)
	}
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.BreakerOpen() {
		t.Fatalf("breaker state must survive restore")
	}
	if got := restored.QuotaRemaining(); got != 61 {
		t.Fatalf("restored quota remaining = %d, want 61", got)
	}
	if got := restored.Stats().Wraps; got != 3 {
		t.Fatalf("restored wrap counter = %d, want 3", got)
	}
}

func TestConcurrentWrapStopsAtWrapLimit(t *testing.T) {
	g := impl.NewKeyWrapGuard(impl.PurposeData)
	mustRegisterDEK(t, g, "dek-a", 10)
	const attempts = 40
	envs := make(chan impl.Envelope, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, err := g.Wrap("dek-a", "svc/x")
			if err != nil {
				errs <- err
				return
			}
			envs <- env
		}()
	}
	wg.Wait()
	close(envs)
	close(errs)
	accepted := 0
	ids := make(map[string]bool)
	for env := range envs {
		accepted++
		if ids[env.ID] {
			t.Fatalf("duplicate envelope id %q", env.ID)
		}
		ids[env.ID] = true
	}
	if accepted != 10 {
		t.Fatalf("accepted wraps = %d, want exactly 10", accepted)
	}
	rejected := 0
	for err := range errs {
		if errors.Is(err, impl.ErrWrapQuotaExhausted) {
			rejected++
		}
	}
	if rejected != attempts-10 {
		t.Fatalf("rejected wraps = %d, want %d", rejected, attempts-10)
	}
	st, err := g.DEKStatus("dek-a")
	if err != nil {
		t.Fatalf("dek status: %v", err)
	}
	if st.Wraps != 10 {
		t.Fatalf("dek wrap count = %d, want 10", st.Wraps)
	}
}
