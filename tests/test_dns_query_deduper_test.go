package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-28"
)

func t3Start() time.Time {
	return time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
}

func t3Options() impl.DeduperOptions {
	return impl.DeduperOptions{
		NegativeTTL:     30 * time.Second,
		ServfailTTL:     5 * time.Second,
		WaitTimeout:     2 * time.Second,
		MaxCacheEntries: 128,
	}
}

func positiveUpstream(calls *int32, ttls ...uint32) impl.UpstreamFunc {
	return func(q impl.Question) (impl.Answer, error) {
		atomic.AddInt32(calls, 1)
		records := make([]impl.Record, 0, len(ttls))
		for i, ttl := range ttls {
			records = append(records, impl.Record{Data: "192.0.2." + string(rune('1'+i)), TTL: ttl})
		}
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeSuccess, Records: records}, nil
	}
}

func TestResolveRejectsBadQuestionAndClient(t *testing.T) {
	var calls int32
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	if _, err := d.Resolve(impl.Question{Name: "  ", Type: impl.TypeA}, "c1", t3Start()); !errors.Is(err, impl.ErrBadQuestion) {
		t.Fatalf("blank qname must report ErrBadQuestion, got %v", err)
	}
	if _, err := d.Resolve(impl.Question{Name: "example.com", Type: 0}, "c1", t3Start()); !errors.Is(err, impl.ErrBadQuestion) {
		t.Fatalf("zero qtype must report ErrBadQuestion, got %v", err)
	}
	if _, err := d.Resolve(impl.Question{Name: "example.com", Type: impl.TypeA}, " ", t3Start()); !errors.Is(err, impl.ErrBadClient) {
		t.Fatalf("blank client must report ErrBadClient, got %v", err)
	}
}

func TestResolveCachesSuccessWithMinimalRecordTTL(t *testing.T) {
	var calls int32
	upstream := positiveUpstream(&calls, 300, 120, 600)
	d := impl.NewQueryDeduper(upstream, t3Options())
	q := impl.Question{Name: "example.com", Type: impl.TypeA}
	start := t3Start()

	ans, err := d.Resolve(q, "c1", start)
	if err != nil {
		t.Fatalf("first resolve must succeed, got %v", err)
	}
	if ans.TTL != 120*time.Second {
		t.Fatalf("answer TTL = %v, want the smallest record TTL 2m", ans.TTL)
	}
	if !ans.ExpiresAt.Equal(start.Add(120 * time.Second)) {
		t.Fatalf("answer expiry = %v, want %v", ans.ExpiresAt, start.Add(120*time.Second))
	}
	if _, err := d.Resolve(q, "c1", start.Add(119*time.Second)); err != nil {
		t.Fatalf("cached resolve inside the TTL must succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls inside the TTL = %d, want 1", got)
	}
	if _, err := d.Resolve(q, "c1", start.Add(121*time.Second)); err != nil {
		t.Fatalf("resolve after the TTL must succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls after the TTL = %d, want 2", got)
	}
}

func TestResolveNegativeCachesNXDomain(t *testing.T) {
	var calls int32
	upstream := func(q impl.Question) (impl.Answer, error) {
		atomic.AddInt32(&calls, 1)
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeNXDomain}, nil
	}
	d := impl.NewQueryDeduper(upstream, t3Options())
	q := impl.Question{Name: "missing.example", Type: impl.TypeA}
	start := t3Start()

	ans, err := d.Resolve(q, "c1", start)
	if !errors.Is(err, impl.ErrNXDomain) {
		t.Fatalf("nxdomain must report ErrNXDomain, got %v", err)
	}
	if ans.RCode != impl.RCodeNXDomain {
		t.Fatalf("answer rcode = %d, want nxdomain", ans.RCode)
	}
	if _, err := d.Resolve(q, "c1", start.Add(29*time.Second)); !errors.Is(err, impl.ErrNXDomain) {
		t.Fatalf("negative cache hit must still report ErrNXDomain, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls inside the negative TTL = %d, want 1", got)
	}
	if _, err := d.Resolve(q, "c1", start.Add(31*time.Second)); !errors.Is(err, impl.ErrNXDomain) {
		t.Fatalf("resolve after the negative TTL must report ErrNXDomain, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls after the negative TTL = %d, want 2", got)
	}
}

func TestResolveUsesShortServfailCache(t *testing.T) {
	var calls int32
	upstream := func(q impl.Question) (impl.Answer, error) {
		atomic.AddInt32(&calls, 1)
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeServfail}, nil
	}
	d := impl.NewQueryDeduper(upstream, t3Options())
	q := impl.Question{Name: "broken.example", Type: impl.TypeA}
	start := t3Start()

	if _, err := d.Resolve(q, "c1", start); !errors.Is(err, impl.ErrServfail) {
		t.Fatalf("servfail must report ErrServfail, got %v", err)
	}
	if _, err := d.Resolve(q, "c1", start.Add(4*time.Second)); !errors.Is(err, impl.ErrServfail) {
		t.Fatalf("servfail cache hit must report ErrServfail, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls inside the servfail TTL = %d, want 1", got)
	}
	if _, err := d.Resolve(q, "c1", start.Add(6*time.Second)); !errors.Is(err, impl.ErrServfail) {
		t.Fatalf("resolve after the servfail TTL must report ErrServfail, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls after the servfail TTL = %d, want 2", got)
	}
}

func TestResolveWaiterTimeout(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := func(q impl.Question) (impl.Answer, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeSuccess, Records: []impl.Record{{TTL: 60}}}, nil
	}
	opts := t3Options()
	opts.WaitTimeout = 50 * time.Millisecond
	d := impl.NewQueryDeduper(upstream, opts)
	q := impl.Question{Name: "slow.example", Type: impl.TypeA}
	start := t3Start()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = d.Resolve(q, "c1", start)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream was never called")
	}
	if _, err := d.Resolve(q, "c2", start); !errors.Is(err, impl.ErrTimeout) {
		t.Fatalf("waiter past its patience must report ErrTimeout, got %v", err)
	}
	close(release)
	wg.Wait()
}

func TestResolveQuotaExhaustionSkipsUpstream(t *testing.T) {
	var calls int32
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	if err := d.SetQuota("c1", 1, 1); err != nil {
		t.Fatalf("setting a quota must succeed, got %v", err)
	}
	start := t3Start()
	if _, err := d.Resolve(impl.Question{Name: "one.example", Type: impl.TypeA}, "c1", start); err != nil {
		t.Fatalf("first resolve must consume the only token, got %v", err)
	}
	if _, err := d.Resolve(impl.Question{Name: "two.example", Type: impl.TypeA}, "c1", start); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("exhausted client must report ErrQuotaExceeded, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls after quota rejection = %d, want 1", got)
	}
}

func TestResolveQuotaIsPerClient(t *testing.T) {
	var calls int32
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	if err := d.SetQuota("c1", 1, 1); err != nil {
		t.Fatalf("setting a quota must succeed, got %v", err)
	}
	start := t3Start()
	if _, err := d.Resolve(impl.Question{Name: "one.example", Type: impl.TypeA}, "c1", start); err != nil {
		t.Fatalf("first resolve must succeed, got %v", err)
	}
	if _, err := d.Resolve(impl.Question{Name: "two.example", Type: impl.TypeA}, "c2", start); err != nil {
		t.Fatalf("a client without a configured quota must not be limited, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	if _, err := d.Resolve(impl.Question{Name: "three.example", Type: impl.TypeA}, "c1", start); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("exhausted client must stay limited, got %v", err)
	}
}

func TestInvalidateDropsEveryTypeOfTheName(t *testing.T) {
	var calls int32
	upstream := func(q impl.Question) (impl.Answer, error) {
		atomic.AddInt32(&calls, 1)
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeSuccess, Records: []impl.Record{{TTL: 60}}}, nil
	}
	d := impl.NewQueryDeduper(upstream, t3Options())
	start := t3Start()
	qA := impl.Question{Name: "example.com", Type: impl.TypeA}
	qAAAA := impl.Question{Name: "example.com", Type: impl.TypeAAAA}

	if _, err := d.Resolve(qA, "c1", start); err != nil {
		t.Fatalf("A resolve must succeed, got %v", err)
	}
	if _, err := d.Resolve(qAAAA, "c1", start); err != nil {
		t.Fatalf("AAAA resolve must succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls for two types = %d, want 2", got)
	}
	d.Invalidate("Example.COM.")
	if _, err := d.Resolve(qA, "c1", start.Add(time.Second)); err != nil {
		t.Fatalf("A resolve after invalidation must succeed, got %v", err)
	}
	if _, err := d.Resolve(qAAAA, "c1", start.Add(time.Second)); err != nil {
		t.Fatalf("AAAA resolve after invalidation must succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("upstream calls after invalidation = %d, want 4", got)
	}
}

func TestSetQuotaRejectsInvalidValues(t *testing.T) {
	d := impl.NewQueryDeduper(positiveUpstream(new(int32), 60), t3Options())
	if err := d.SetQuota("c1", 0, 1); !errors.Is(err, impl.ErrBadQuota) {
		t.Fatalf("zero qps must report ErrBadQuota, got %v", err)
	}
	if err := d.SetQuota("c1", 1, 0); !errors.Is(err, impl.ErrBadQuota) {
		t.Fatalf("zero burst must report ErrBadQuota, got %v", err)
	}
	if err := d.SetQuota("   ", 1, 1); !errors.Is(err, impl.ErrBadClient) {
		t.Fatalf("blank client must report ErrBadClient, got %v", err)
	}
}

func TestConcurrentResolveCoalescesToOneUpstreamCall(t *testing.T) {
	var calls int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := func(q impl.Question) (impl.Answer, error) {
		atomic.AddInt32(&calls, 1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeSuccess, Records: []impl.Record{{TTL: 60}}}, nil
	}
	d := impl.NewQueryDeduper(upstream, t3Options())
	q := impl.Question{Name: "hot.example", Type: impl.TypeA}
	start := t3Start()

	const workers = 6
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			if _, err := d.Resolve(q, "c1", start); err != nil {
				errCh <- err
			}
		}()
	}
	close(gate)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream was never called")
	}
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("coalesced resolve failed: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls for one coalesced question = %d, want 1", got)
	}
}

func TestConcurrentResolveDifferentTypesDoNotCoalesce(t *testing.T) {
	var calls int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	upstream := func(q impl.Question) (impl.Answer, error) {
		atomic.AddInt32(&calls, 1)
		started <- struct{}{}
		<-release
		return impl.Answer{Name: q.Name, Type: q.Type, RCode: impl.RCodeSuccess, Records: []impl.Record{{TTL: 60}}}, nil
	}
	d := impl.NewQueryDeduper(upstream, t3Options())
	start := t3Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = d.Resolve(impl.Question{Name: "example.com", Type: impl.TypeA}, "c1", start)
	}()
	go func() {
		defer wg.Done()
		_, _ = d.Resolve(impl.Question{Name: "example.com", Type: impl.TypeAAAA}, "c1", start)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("both question types must reach upstream independently")
		}
	}
	close(release)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls for A and AAAA = %d, want 2", got)
	}
}

func TestSweepRemovesExpiredEntries(t *testing.T) {
	var calls int32
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	q := impl.Question{Name: "sweep.example", Type: impl.TypeA}
	start := t3Start()
	if _, err := d.Resolve(q, "c1", start); err != nil {
		t.Fatalf("resolve must succeed, got %v", err)
	}
	if got := d.Sweep(start.Add(59 * time.Second)); got != 0 {
		t.Fatalf("sweep inside the TTL removed %d entries, want 0", got)
	}
	if got := d.Sweep(start.Add(61 * time.Second)); got != 1 {
		t.Fatalf("sweep after the TTL removed %d entries, want 1", got)
	}
	if got := d.CacheSize(); got != 0 {
		t.Fatalf("cache size after sweep = %d, want 0", got)
	}
	if _, err := d.Resolve(q, "c1", start.Add(62*time.Second)); err != nil {
		t.Fatalf("resolve after sweep must succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls after sweep = %d, want 2", got)
	}
}

func TestSnapshotRestoreKeepsFreshEntries(t *testing.T) {
	var calls int32
	start := t3Start()
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	q := impl.Question{Name: "restore.example", Type: impl.TypeA}
	if _, err := d.Resolve(q, "c1", start); err != nil {
		t.Fatalf("resolve must succeed, got %v", err)
	}
	snap, err := d.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.CacheSize(); got != 1 {
		t.Fatalf("restored cache size = %d, want 1", got)
	}
	if _, err := restored.Resolve(q, "c1", start.Add(30*time.Second)); err != nil {
		t.Fatalf("restored fresh entry must serve the query, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls after restoring a fresh entry = %d, want 1", got)
	}
}

func TestRestoreDoesNotResurrectExpiredEntries(t *testing.T) {
	var calls int32
	start := t3Start()
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 30), t3Options())
	q := impl.Question{Name: "stale.example", Type: impl.TypeA}
	if _, err := d.Resolve(q, "c1", start); err != nil {
		t.Fatalf("resolve must succeed, got %v", err)
	}
	snap, err := d.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewQueryDeduper(positiveUpstream(&calls, 30), t3Options())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if _, err := restored.Resolve(q, "c1", start.Add(31*time.Second)); err != nil {
		t.Fatalf("resolve after restore must succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expired restored entry served without an upstream call, calls = %d", got)
	}
}

func TestSnapshotRestoreKeepsQuotaWindow(t *testing.T) {
	var calls int32
	start := t3Start()
	d := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	if err := d.SetQuota("c1", 1, 1); err != nil {
		t.Fatalf("setting a quota must succeed, got %v", err)
	}
	if _, err := d.Resolve(impl.Question{Name: "one.example", Type: impl.TypeA}, "c1", start); err != nil {
		t.Fatalf("first resolve must succeed, got %v", err)
	}
	snap, err := d.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewQueryDeduper(positiveUpstream(&calls, 60), t3Options())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if _, err := restored.Resolve(impl.Question{Name: "two.example", Type: impl.TypeA}, "c1", start); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("restored quota window must still be exhausted, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls after quota restore = %d, want 1", got)
	}
}
