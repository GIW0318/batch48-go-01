package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-c-07"
)

func newScheduler(ttl time.Duration, now *time.Time) *impl.ScanLeaseScheduler {
	return impl.NewScanLeaseScheduler(impl.SchedulerOptions{
		LeaseTTL: ttl,
		Now:      func() time.Time { return *now },
	})
}

func enqueue(t *testing.T, s *impl.ScanLeaseScheduler, id string, units, shards, priority int) {
	t.Helper()
	if err := s.Enqueue(impl.Target{ID: id, Host: id + ".corp", Units: units, Shards: shards, Priority: priority}); err != nil {
		t.Fatalf("enqueue %s: unexpected error %v", id, err)
	}
}

func lease(t *testing.T, s *impl.ScanLeaseScheduler, worker string) *impl.Shard {
	t.Helper()
	shard, err := s.Lease(worker)
	if err != nil {
		t.Fatalf("lease for %s: unexpected error %v", worker, err)
	}
	return shard
}

func TestEnqueueRejectsInvalidTarget(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	bad := []impl.Target{
		{ID: "", Host: "h", Units: 10, Shards: 2},
		{ID: "t", Host: "", Units: 10, Shards: 2},
		{ID: "t", Host: "h", Units: 0, Shards: 2},
		{ID: "t", Host: "h", Units: 10, Shards: 0},
		{ID: "t", Host: "h", Units: 10, Shards: 65},
	}
	for i, target := range bad {
		if err := s.Enqueue(target); !errors.Is(err, impl.ErrInvalidTarget) {
			t.Fatalf("case %d: error = %v, want ErrInvalidTarget", i, err)
		}
	}
	if got := s.Pending(); got != 0 {
		t.Fatalf("pending after rejected targets = %d, want 0", got)
	}
}

func TestEnqueueRejectsDuplicateTarget(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-1", 10, 2, 0)
	if err := s.Enqueue(impl.Target{ID: "t-1", Host: "other", Units: 10, Shards: 2}); !errors.Is(err, impl.ErrDuplicateTarget) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateTarget", err)
	}
	if got := s.Pending(); got != 2 {
		t.Fatalf("pending = %d, want 2", got)
	}
	if got := s.Stats().Enqueued; got != 2 {
		t.Fatalf("enqueued stat = %d, want 2", got)
	}
}

func TestEnqueueSplitsContiguousShardRanges(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-2", 10, 3, 0)
	want := [][2]int{{0, 3}, {3, 6}, {6, 10}}
	for i, span := range want {
		shard := lease(t, s, "w-1")
		if shard.Index != i || shard.Begin != span[0] || shard.End != span[1] {
			t.Fatalf("shard %d = index %d [%d,%d), want index %d [%d,%d)", i, shard.Index, shard.Begin, shard.End, i, span[0], span[1])
		}
	}
	enqueue(t, s, "t-3", 7, 7, 0)
	for i := 0; i < 7; i++ {
		shard := lease(t, s, "w-2")
		if shard.Begin != i || shard.End != i+1 {
			t.Fatalf("single unit shard %d = [%d,%d)", i, shard.Begin, shard.End)
		}
	}
}

func TestLeaseDispatchesByPriorityThenIDThenIndex(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-b", 10, 2, 1)
	enqueue(t, s, "t-a", 10, 1, 1)
	enqueue(t, s, "t-c", 10, 1, 5)
	want := [][2]interface{}{{"t-a", 0}, {"t-b", 0}, {"t-b", 1}, {"t-c", 0}}
	for i, pair := range want {
		shard := lease(t, s, "w-1")
		if shard.TargetID != pair[0].(string) || shard.Index != pair[1].(int) {
			t.Fatalf("lease %d = %s/%d, want %s/%d", i, shard.TargetID, shard.Index, pair[0], pair[1])
		}
	}
}

func TestLeaseAssignsMonotonicLeaseIDsAndExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-4", 4, 2, 0)
	first := lease(t, s, "w-1")
	second := lease(t, s, "w-2")
	if first.LeaseID != 1 || second.LeaseID != 2 {
		t.Fatalf("lease ids = %d, %d, want 1, 2", first.LeaseID, second.LeaseID)
	}
	if first.Seq != 1 || second.Seq != 1 {
		t.Fatalf("first dispatch seq = %d, %d, want 1, 1", first.Seq, second.Seq)
	}
	if !first.Expires.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("expiry = %v, want %v", first.Expires, now.Add(10*time.Second))
	}
	if first.Worker != "w-1" || second.Worker != "w-2" {
		t.Fatalf("workers = %q, %q", first.Worker, second.Worker)
	}
}

func TestLeaseReturnsNoWorkWhenExhausted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-5", 4, 2, 0)
	lease(t, s, "w-1")
	lease(t, s, "w-2")
	if _, err := s.Lease("w-3"); !errors.Is(err, impl.ErrNoWork) {
		t.Fatalf("exhausted lease error = %v, want ErrNoWork", err)
	}
	if got := s.Pending(); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
	if got := s.InFlight(); got != 2 {
		t.Fatalf("in flight = %d, want 2", got)
	}
}

func TestRenewExtendsExpiryBeyondOriginalTTL(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-6", 4, 1, 0)
	shard := lease(t, s, "w-1")
	now = now.Add(8 * time.Second)
	if err := s.Renew(shard.LeaseID); err != nil {
		t.Fatalf("renew: unexpected error %v", err)
	}
	now = now.Add(7 * time.Second)
	if got := s.Reclaim(now); got != 0 {
		t.Fatalf("reclaimed after renewal = %d, want 0", got)
	}
	if got := s.InFlight(); got != 1 {
		t.Fatalf("in flight = %d, want 1", got)
	}
}

func TestRenewUnknownAndExpiredLease(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-7", 4, 1, 0)
	if err := s.Renew(999); !errors.Is(err, impl.ErrUnknownLease) {
		t.Fatalf("unknown lease error = %v, want ErrUnknownLease", err)
	}
	shard := lease(t, s, "w-1")
	now = now.Add(11 * time.Second)
	if err := s.Renew(shard.LeaseID); !errors.Is(err, impl.ErrLeaseExpired) {
		t.Fatalf("expired renew error = %v, want ErrLeaseExpired", err)
	}
}

func TestCompleteRecordsResultAndRetiresShard(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-8", 4, 2, 0)
	first := lease(t, s, "w-1")
	res := impl.ShardResult{TargetID: first.TargetID, Index: first.Index, Seq: first.Seq, Findings: 2, Digest: "d-first"}
	if err := s.Complete(first.LeaseID, res); err != nil {
		t.Fatalf("complete: unexpected error %v", err)
	}
	got, ok := s.Reported("t-8", 0)
	if !ok {
		t.Fatal("completed shard has no recorded result")
	}
	if got.Digest != "d-first" || got.Findings != 2 {
		t.Fatalf("recorded result = %+v", got)
	}
	second := lease(t, s, "w-2")
	if second.Index != 1 {
		t.Fatalf("second lease index = %d, want 1", second.Index)
	}
	if err := s.Complete(second.LeaseID, impl.ShardResult{TargetID: "t-8", Index: 1, Seq: second.Seq, Digest: "d-second"}); err != nil {
		t.Fatalf("complete second: unexpected error %v", err)
	}
	if _, err := s.Lease("w-3"); !errors.Is(err, impl.ErrNoWork) {
		t.Fatalf("lease after all completed = %v, want ErrNoWork", err)
	}
	if got := s.Stats().Completed; got != 2 {
		t.Fatalf("completed stat = %d, want 2", got)
	}
	results := s.Results()
	if len(results) != 2 || results[0].Digest != "d-first" || results[1].Digest != "d-second" {
		t.Fatalf("results = %+v", results)
	}
}

func TestCompleteValidatesResultPayload(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-9", 4, 1, 0)
	shard := lease(t, s, "w-1")
	if err := s.Complete(shard.LeaseID, impl.ShardResult{TargetID: "t-other", Index: 0}); !errors.Is(err, impl.ErrInvalidResult) {
		t.Fatalf("wrong target error = %v, want ErrInvalidResult", err)
	}
	if err := s.Complete(shard.LeaseID, impl.ShardResult{TargetID: "t-9", Index: 4}); !errors.Is(err, impl.ErrInvalidResult) {
		t.Fatalf("wrong index error = %v, want ErrInvalidResult", err)
	}
	if err := s.Renew(shard.LeaseID); err != nil {
		t.Fatalf("lease must stay valid after rejected results, got %v", err)
	}
	if err := s.Complete(shard.LeaseID, impl.ShardResult{TargetID: "t-9", Index: 0, Seq: shard.Seq, Digest: "ok"}); err != nil {
		t.Fatalf("complete after rejections: unexpected error %v", err)
	}
}

func TestCompleteWithExpiredLeaseRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-10", 4, 1, 0)
	shard := lease(t, s, "w-1")
	now = now.Add(11 * time.Second)
	err := s.Complete(shard.LeaseID, impl.ShardResult{TargetID: "t-10", Index: 0, Seq: shard.Seq, Digest: "late"})
	if !errors.Is(err, impl.ErrLeaseExpired) {
		t.Fatalf("late complete error = %v, want ErrLeaseExpired", err)
	}
	if _, ok := s.Reported("t-10", 0); ok {
		t.Fatal("expired completion must not be recorded")
	}
	if got := len(s.Results()); got != 0 {
		t.Fatalf("results = %d, want 0", got)
	}
}

func TestReclaimRequeuesExpiredShards(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-11", 4, 2, 0)
	lease(t, s, "w-1")
	lease(t, s, "w-2")
	if got := s.Reclaim(now); got != 0 {
		t.Fatalf("reclaim before expiry = %d, want 0", got)
	}
	now = now.Add(11 * time.Second)
	if got := s.Reclaim(now); got != 2 {
		t.Fatalf("reclaim after expiry = %d, want 2", got)
	}
	if got := s.Pending(); got != 2 {
		t.Fatalf("pending after reclaim = %d, want 2", got)
	}
	if got := s.InFlight(); got != 0 {
		t.Fatalf("in flight after reclaim = %d, want 0", got)
	}
	if got := s.Stats().Reclaimed; got != 2 {
		t.Fatalf("reclaimed stat = %d, want 2", got)
	}
	again := lease(t, s, "w-3")
	if again.Seq != 2 {
		t.Fatalf("redispatch seq = %d, want 2", again.Seq)
	}
}

func TestConcurrentLeaseAssignsEachShardOnce(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-12", 8, 8, 0)
	var mu sync.Mutex
	seen := make(map[string]int)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				shard, err := s.Lease("worker")
				if err != nil {
					return
				}
				key := shard.TargetID + "/" + string(rune('0'+shard.Index))
				mu.Lock()
				seen[key]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 8 {
		t.Fatalf("distinct shards leased = %d, want 8", len(seen))
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("shard %s leased %d times, want 1", key, count)
		}
	}
	if got := s.Stats().Leased; got != 8 {
		t.Fatalf("leased stat = %d, want 8", got)
	}
	if got := s.Pending(); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
}

func TestSnapshotRestoreKeepsPendingAndInFlight(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	enqueue(t, s, "t-13", 6, 3, 0)
	first := lease(t, s, "w-1")
	if err := s.Complete(first.LeaseID, impl.ShardResult{TargetID: "t-13", Index: first.Index, Seq: first.Seq, Digest: "done"}); err != nil {
		t.Fatalf("complete: unexpected error %v", err)
	}
	inFlight := lease(t, s, "w-2")
	data, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := newScheduler(10*time.Second, &now)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if restored.Stats() != s.Stats() {
		t.Fatalf("restored stats = %+v, want %+v", restored.Stats(), s.Stats())
	}
	if err := restored.Renew(inFlight.LeaseID); err != nil {
		t.Fatalf("renew restored lease: unexpected error %v", err)
	}
	next := lease(t, restored, "w-3")
	if next.Index != 2 || next.Seq != 1 {
		t.Fatalf("next shard = index %d seq %d, want index 2 seq 1", next.Index, next.Seq)
	}
	if got, ok := restored.Reported("t-13", 0); !ok || got.Digest != "done" {
		t.Fatalf("restored result = %+v ok=%v", got, ok)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := newScheduler(10*time.Second, &now)
	if err := s.Restore([]byte(`{"lease_ttl":`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore error = %v, want ErrBadSnapshot", err)
	}
	if got := s.Stats().Pending; got != 0 {
		t.Fatalf("pending after failed restore = %d, want 0", got)
	}
}
