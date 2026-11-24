package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-20"
)

var baseTime = time.Unix(1700000000, 0)

func newBalancer(t *testing.T) *impl.SRVBalancer {
	t.Helper()
	b, err := impl.NewSRVBalancer(impl.Policy{
		FailThreshold: 2,
		Cooldown:      30 * time.Second,
		MaxKeys:       16,
	})
	if err != nil {
		t.Fatalf("balancer build failed: %v", err)
	}
	return b
}

func record(host string, port uint16, priority, weight uint16, ttl time.Duration) impl.SRVRecord {
	return impl.SRVRecord{Host: host, Port: port, Priority: priority, Weight: weight, TTL: ttl}
}

func target(host string, port uint16) impl.Target {
	return impl.Target{Host: host, Port: port}
}

func mustUpdate(t *testing.T, b *impl.SRVBalancer, records []impl.SRVRecord) {
	t.Helper()
	if err := b.Update(records); err != nil {
		t.Fatalf("update must succeed, got %v", err)
	}
}

func pickTarget(t *testing.T, b *impl.SRVBalancer, key string) impl.Target {
	t.Helper()
	got, err := b.Pick(key)
	if err != nil {
		t.Fatalf("pick %q failed: %v", key, err)
	}
	return got
}

func TestUpdateRejectsEmptySet(t *testing.T) {
	b := newBalancer(t)
	if err := b.Update(nil); !errors.Is(err, impl.ErrEmptyRecordSet) {
		t.Fatalf("empty update must report ErrEmptyRecordSet, got %v", err)
	}
	if err := b.Update([]impl.SRVRecord{}); !errors.Is(err, impl.ErrEmptyRecordSet) {
		t.Fatalf("empty slice update must report ErrEmptyRecordSet, got %v", err)
	}
	if got := b.Stats().Records; got != 0 {
		t.Fatalf("record count after rejected updates = %d, want 0", got)
	}
}

func TestUpdateRejectsDuplicateTarget(t *testing.T) {
	b := newBalancer(t)
	err := b.Update([]impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("a.example", 8080, 10, 5, time.Hour),
	})
	if !errors.Is(err, impl.ErrDuplicateTarget) {
		t.Fatalf("duplicate host:port must report ErrDuplicateTarget, got %v", err)
	}
	if got := b.Stats().Records; got != 0 {
		t.Fatalf("record count after the rejected update = %d, want 0", got)
	}
}

func TestUpdateRejectsInvalidRecord(t *testing.T) {
	b := newBalancer(t)
	cases := [][]impl.SRVRecord{
		{record("", 8080, 10, 1, time.Hour)},
		{record("a.example", 0, 10, 1, time.Hour)},
		{record("a.example", 8080, 10, 1, 0)},
	}
	for i, records := range cases {
		if err := b.Update(records); !errors.Is(err, impl.ErrInvalidRecord) {
			t.Fatalf("case %d must report ErrInvalidRecord, got %v", i, err)
		}
	}
	if got := b.Stats().Records; got != 0 {
		t.Fatalf("record count after rejected updates = %d, want 0", got)
	}
}

func TestWeightedRotationIsExact(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 2, time.Hour),
		record("c.example", 8080, 10, 1, time.Hour),
	})
	want := []impl.Target{
		target("b.example", 8080),
		target("a.example", 8080),
		target("c.example", 8080),
		target("b.example", 8080),
		target("b.example", 8080),
		target("a.example", 8080),
		target("c.example", 8080),
		target("b.example", 8080),
	}
	for i, exp := range want {
		if got := pickTarget(t, b, ""); got != exp {
			t.Fatalf("pick %d = %v, want %v", i, got, exp)
		}
	}
	if got := b.Stats().Picks; got != len(want) {
		t.Fatalf("pick counter = %d, want %d", got, len(want))
	}
}

func TestLowestPriorityOnly(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("backup.example", 8080, 20, 5, time.Hour),
		record("primary.example", 8080, 10, 1, time.Hour),
	})
	for i := 0; i < 6; i++ {
		if got := pickTarget(t, b, ""); got != target("primary.example", 8080) {
			t.Fatalf("pick %d = %v, want the primary priority target", i, got)
		}
	}
	targets := b.Targets()
	if len(targets) != 2 || targets[0] != target("primary.example", 8080) || targets[1] != target("backup.example", 8080) {
		t.Fatalf("targets = %v, want primary then backup", targets)
	}
}

func TestWeightZeroIsFallbackOnly(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("zero-a.example", 8080, 10, 0, time.Hour),
		record("zero-b.example", 8080, 10, 0, time.Hour),
	})
	want := []impl.Target{
		target("zero-a.example", 8080),
		target("zero-b.example", 8080),
		target("zero-a.example", 8080),
		target("zero-b.example", 8080),
	}
	for i, exp := range want {
		if got := pickTarget(t, b, ""); got != exp {
			t.Fatalf("pick %d = %v, want %v", i, got, exp)
		}
	}
	if got := b.Stats().WeightZeroPicks; got != 4 {
		t.Fatalf("weight zero pick counter = %d, want 4", got)
	}
}

func TestHealthSuppressionRedirectsPicks(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 1, time.Hour),
	})
	sick := target("a.example", 8080)
	for i := 0; i < 2; i++ {
		if err := b.ReportHealth(sick, false); err != nil {
			t.Fatalf("health report must succeed, got %v", err)
		}
	}
	if b.Healthy(sick) {
		t.Fatalf("target must be suppressed after two consecutive failures")
	}
	if got := b.Stats().Suppressed; got != 1 {
		t.Fatalf("suppressed counter = %d, want 1", got)
	}
	for i := 0; i < 6; i++ {
		if got := pickTarget(t, b, ""); got != target("b.example", 8080) {
			t.Fatalf("pick %d = %v, want the healthy target only", i, got)
		}
	}
}

func TestHalfOpenProbeIsSingle(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 1, time.Hour),
	})
	probe := target("a.example", 8080)
	other := target("b.example", 8080)
	for i := 0; i < 2; i++ {
		if err := b.ReportHealth(probe, false); err != nil {
			t.Fatalf("health report must succeed, got %v", err)
		}
	}
	b.Expire(baseTime.Add(31 * time.Second))
	if got := pickTarget(t, b, "key-one"); got != probe {
		t.Fatalf("first pick after the cooldown = %v, want the single probe target %v", got, probe)
	}
	if got := pickTarget(t, b, "key-two"); got != other {
		t.Fatalf("second pick while probing = %v, want %v", got, other)
	}
	if got := b.Stats().Probes; got != 1 {
		t.Fatalf("probe counter = %d, want 1", got)
	}
	if err := b.ReportHealth(probe, true); err != nil {
		t.Fatalf("probe result must be recorded, got %v", err)
	}
	if !b.Healthy(probe) {
		t.Fatalf("a successful probe must restore the target")
	}
}

func TestAllSuppressedReturnsExplicitError(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 1, time.Hour),
	})
	for _, tgt := range []impl.Target{target("a.example", 8080), target("b.example", 8080)} {
		for i := 0; i < 2; i++ {
			if err := b.ReportHealth(tgt, false); err != nil {
				t.Fatalf("health report must succeed, got %v", err)
			}
		}
	}
	if _, err := b.Pick(""); !errors.Is(err, impl.ErrNoHealthyBackend) {
		t.Fatalf("pick with every target suppressed must report ErrNoHealthyBackend, got %v", err)
	}
	if got := b.Stats().Suppressed; got != 2 {
		t.Fatalf("suppressed counter = %d, want 2", got)
	}
}

func TestHealthSuccessResetsFailureCounter(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 1, time.Hour),
	})
	a := target("a.example", 8080)
	if err := b.ReportHealth(a, false); err != nil {
		t.Fatalf("failure report must succeed, got %v", err)
	}
	if err := b.ReportHealth(a, true); err != nil {
		t.Fatalf("success report must succeed, got %v", err)
	}
	if err := b.ReportHealth(a, false); err != nil {
		t.Fatalf("second failure report must succeed, got %v", err)
	}
	if !b.Healthy(a) {
		t.Fatalf("one failure after a success must not suppress the target")
	}
	if got := b.Stats().Suppressed; got != 0 {
		t.Fatalf("suppressed counter = %d, want 0", got)
	}
	for i := 0; i < 5; i++ {
		if _, err := b.Pick(""); err != nil {
			t.Fatalf("pick %d failed unexpectedly: %v", i, err)
		}
	}
}

func TestTTLStaleKeepsServingOldSet(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{record("a.example", 8080, 10, 1, 10*time.Second)})
	if got := b.Stats().Stale; got {
		t.Fatalf("a fresh record set must not be stale")
	}
	if got := pickTarget(t, b, ""); got != target("a.example", 8080) {
		t.Fatalf("pick = %v, want the loaded target", got)
	}
	b.Expire(baseTime.Add(11 * time.Second))
	if !b.Stats().Stale {
		t.Fatalf("the record set must be stale after its TTL passed")
	}
	if got := pickTarget(t, b, ""); got != target("a.example", 8080) {
		t.Fatalf("pick after expiry = %v, want the old target kept as fallback", got)
	}
	mustUpdate(t, b, []impl.SRVRecord{record("a.example", 8080, 10, 1, 30*time.Second)})
	if got := b.Stats().Stale; got {
		t.Fatalf("a fresh update must clear the stale mark")
	}
}

func TestStickyKeyPinsTarget(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 1, time.Hour),
	})
	first := pickTarget(t, b, "alpha")
	for i := 0; i < 3; i++ {
		if got := pickTarget(t, b, "alpha"); got != first {
			t.Fatalf("repeat pick %d for the same key = %v, want sticky %v", i, got, first)
		}
	}
	other := pickTarget(t, b, "beta")
	if other == first {
		t.Fatalf("a different key must advance the rotation, got %v twice", other)
	}
	if got := b.Stats().StickyKeys; got != 2 {
		t.Fatalf("sticky key counter = %d, want 2", got)
	}
}

func TestSnapshotRestoreContinuesRotation(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 2, time.Hour),
		record("c.example", 8080, 10, 1, time.Hour),
	})
	for i, exp := range []impl.Target{
		target("b.example", 8080),
		target("a.example", 8080),
		target("c.example", 8080),
	} {
		if got := pickTarget(t, b, ""); got != exp {
			t.Fatalf("warm up pick %d = %v, want %v", i, got, exp)
		}
	}
	snap, err := b.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := newBalancer(t)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	want := []impl.Target{
		target("b.example", 8080),
		target("b.example", 8080),
		target("a.example", 8080),
		target("c.example", 8080),
		target("b.example", 8080),
	}
	for i, exp := range want {
		if got := pickTarget(t, restored, ""); got != exp {
			t.Fatalf("restored pick %d = %v, want the rotation to continue with %v", i, got, exp)
		}
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage snapshot must report ErrBadSnapshot, got %v", err)
	}
	if got := pickTarget(t, restored, ""); got != target("b.example", 8080) {
		t.Fatalf("pick after a rejected restore = %v, want the rotation state untouched", got)
	}
}

func TestValidationAndUnknownTarget(t *testing.T) {
	b := newBalancer(t)
	if _, err := b.Pick(""); !errors.Is(err, impl.ErrNoRecords) {
		t.Fatalf("pick before any update must report ErrNoRecords, got %v", err)
	}
	if err := b.SetPolicy(impl.Policy{FailThreshold: 0, Cooldown: time.Second, MaxKeys: 4}); !errors.Is(err, impl.ErrInvalidPolicy) {
		t.Fatalf("a zero failure threshold must report ErrInvalidPolicy, got %v", err)
	}
	if _, err := impl.NewSRVBalancer(impl.Policy{FailThreshold: 0}); !errors.Is(err, impl.ErrInvalidPolicy) {
		t.Fatalf("building with a zero failure threshold must report ErrInvalidPolicy, got %v", err)
	}
	if err := b.ReportHealth(target("missing.example", 8080), true); !errors.Is(err, impl.ErrUnknownTarget) {
		t.Fatalf("health report for an unknown target must report ErrUnknownTarget, got %v", err)
	}
	if err := b.SetPolicy(impl.Policy{FailThreshold: 3, Cooldown: time.Minute, MaxKeys: 8}); err != nil {
		t.Fatalf("a valid policy must apply, got %v", err)
	}
}

func TestConcurrentPicksAreSerialized(t *testing.T) {
	b := newBalancer(t)
	b.Expire(baseTime)
	mustUpdate(t, b, []impl.SRVRecord{
		record("a.example", 8080, 10, 1, time.Hour),
		record("b.example", 8080, 10, 2, time.Hour),
		record("c.example", 8080, 10, 1, time.Hour),
	})
	valid := map[impl.Target]bool{
		target("a.example", 8080): true,
		target("b.example", 8080): true,
		target("c.example", 8080): true,
	}
	const workers = 6
	const rounds = 20
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				got, err := b.Pick("")
				if err != nil {
					errCh <- fmt.Errorf("pick failed: %w", err)
					return
				}
				if !valid[got] {
					errCh <- fmt.Errorf("pick returned an unknown target %v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent pick failed: %v", err)
	}
	if got := b.Stats().Picks; got != workers*rounds {
		t.Fatalf("pick counter = %d, want %d", got, workers*rounds)
	}
}
