package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-b-05"
)

// A fork storm from one tenant must stop exactly at the fanout cap even
// when every spawn races the others.
func TestConcurrentForkStormStopsAtFanoutCap(t *testing.T) {
	opts := frozenOptions()
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 2000})

	const workers = 16
	var wg sync.WaitGroup
	var succeeded atomic.Int32
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: pid, PPID: 2000})
			if err == nil {
				succeeded.Add(1)
			}
			errs <- err
		}(2001 + i)
	}
	wg.Wait()
	close(errs)
	if got := succeeded.Load(); got != int32(opts.MaxFanout) {
		t.Fatalf("successful concurrent spawns = %d, want %d", got, opts.MaxFanout)
	}
	for err := range errs {
		if err != nil && !errors.Is(err, impl.ErrFanoutExceeded) && !errors.Is(err, impl.ErrTenantBlocked) {
			t.Fatalf("unexpected concurrent admit error: %v", err)
		}
	}
	if count := g.ProcessCount("a"); count != opts.MaxFanout+1 {
		t.Fatalf("ProcessCount = %d, want %d", count, opts.MaxFanout+1)
	}
	if kids := g.Children(2000); len(kids) != opts.MaxFanout {
		t.Fatalf("Children(2000) has %d entries, want %d", len(kids), opts.MaxFanout)
	}
}

// Reap racing Admit must freeze the condemned subtree, kill leaf to root
// and leave the tenant unblocked afterwards.
func TestReapAndAdmitInterleaved(t *testing.T) {
	opts := frozenOptions()
	opts.MaxFanout = 1
	opts.Burst = 1 << 20
	var killOrder []int
	opts.Kill = func(pid int) error {
		killOrder = append(killOrder, pid)
		return nil
	}
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 3000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 3001, PPID: 3000})
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 3002, PPID: 3000}); !errors.Is(err, impl.ErrFanoutExceeded) {
		t.Fatalf("breach error = %v, want ErrFanoutExceeded", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var nextPID atomic.Int32
	nextPID.Store(4000)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				pid := int(nextPID.Add(1))
				err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: pid})
				if err != nil && !errors.Is(err, impl.ErrTenantBlocked) {
					t.Errorf("admit racing reap: unexpected error %v", err)
					return
				}
			}
		}()
	}
	reaped, err := g.Reap("a")
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	close(stop)
	wg.Wait()

	if got := sortedInts(reaped); len(got) != 2 || got[0] != 3000 || got[1] != 3001 {
		t.Fatalf("reaped = %v, want [3000 3001]", reaped)
	}
	if len(killOrder) != 2 || killOrder[0] != 3001 || killOrder[1] != 3000 {
		t.Fatalf("kill order = %v, want leaf-to-root [3001 3000]", killOrder)
	}
	if g.Budget("a").Blocked {
		t.Fatal("tenant still blocked after reap")
	}
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 3999})
}

// A restored guard must keep enforcing depth and rate with the exact
// state captured by the snapshot.
func TestSnapshotRestoreThenContinueRegistration(t *testing.T) {
	opts := frozenOptions()
	opts.Burst = 10
	opts.MaxDepth = 3
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 5000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 5001, PPID: 5000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 5002, PPID: 5001})
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newGuard(t, opts)
	if err := restored.Restore(snap, func(int) bool { return true }); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if count := restored.ProcessCount("a"); count != 3 {
		t.Fatalf("restored ProcessCount = %d, want 3", count)
	}
	if budget := restored.Budget("a"); budget.Tokens != 7 {
		t.Fatalf("restored tokens = %d, want 7", budget.Tokens)
	}
	mustAdmit(t, restored, impl.SpawnEvent{Tenant: "a", PID: 5004, PPID: 5001})
	if err := restored.Admit(impl.SpawnEvent{Tenant: "a", PID: 5003, PPID: 5002}); !errors.Is(err, impl.ErrDepthExceeded) {
		t.Fatalf("level-4 spawn after restore error = %v, want ErrDepthExceeded", err)
	}
}

// A node whose parent vanished keeps its recorded depth, so the subtree
// must not suddenly become shallower after a restore.
func TestRestoreKeepsDepthWhenParentVanishes(t *testing.T) {
	opts := frozenOptions()
	opts.MaxDepth = 3
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 6000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 6001, PPID: 6000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 6002, PPID: 6001})
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newGuard(t, opts)
	if err := restored.Restore(snap, func(pid int) bool { return pid != 6001 }); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if count := restored.ProcessCount("a"); count != 2 {
		t.Fatalf("ProcessCount after dropping the middle node = %d, want 2", count)
	}
	if kids := restored.Children(6000); len(kids) != 0 {
		t.Fatalf("Children(6000) = %v, want none after 6001 vanished", kids)
	}
	if err := restored.Admit(impl.SpawnEvent{Tenant: "a", PID: 6003, PPID: 6002}); !errors.Is(err, impl.ErrDepthExceeded) {
		t.Fatalf("child of the depth-3 survivor error = %v, want ErrDepthExceeded", err)
	}
}

// The bucket refills per millisecond, not per second: one millisecond of
// wait buys exactly one token at 1000 tokens/s.
func TestTokenBucketRefillsAtMillisecondGranularity(t *testing.T) {
	now := time.Unix(1700000000, 0)
	opts := frozenOptions()
	opts.Burst = 1
	opts.RatePerSecond = 1000
	opts.Now = func() time.Time { return now }
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 7000})
	now = now.Add(time.Millisecond)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 7001})
	now = now.Add(time.Millisecond)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 7002})
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 7003}); !errors.Is(err, impl.ErrRateLimited) {
		t.Fatalf("spawn without any refill error = %v, want ErrRateLimited", err)
	}
}
