package task

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-29"
)

type t4Clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *t4Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *t4Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func t4Start() time.Time {
	return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
}

func t4Options(clock *t4Clock) impl.GovernorOptions {
	return impl.GovernorOptions{
		InitialWindow:      4,
		MinWindow:          1,
		MaxWindow:          32,
		SampleWindow:       4,
		GrowthStep:         1,
		ShrinkFactor:       0.7,
		ErrorRateThreshold: 0.5,
		Timeout:            100 * time.Millisecond,
		OpenCooldown:       time.Second,
		ProbeBackoff:       500 * time.Millisecond,
		QueueLimit:         8,
		Now:                clock.Now,
	}
}

func openT4Breaker(t *testing.T, g *impl.ConcurrencyGovernor) {
	t.Helper()
	for i := 0; i < 4; i++ {
		tk, err := g.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d while opening the breaker failed: %v", i, err)
		}
		ok := i == 3
		if err := g.Release(tk, time.Millisecond, ok); err != nil {
			t.Fatalf("release %d while opening the breaker failed: %v", i, err)
		}
	}
	if got := g.State(); got != impl.StateOpen {
		t.Fatalf("breaker state after the error batch = %q, want open", got)
	}
}

func TestAcquireWithinWindowSucceeds(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	g := impl.NewConcurrencyGovernor(t4Options(clock))
	first, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire must succeed, got %v", err)
	}
	second, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("second acquire must succeed, got %v", err)
	}
	if got := g.State(); got != impl.StateClosed {
		t.Fatalf("breaker state = %q, want closed", got)
	}
	if got := g.Window(); got != 4 {
		t.Fatalf("window = %d, want 4", got)
	}
	if got := g.Inflight(); got != 2 {
		t.Fatalf("inflight = %d, want 2", got)
	}
	if got := g.Stats().Inflight; got != 2 {
		t.Fatalf("stats inflight = %d, want 2", got)
	}
	if err := g.Release(first, time.Millisecond, true); err != nil {
		t.Fatalf("first release must succeed, got %v", err)
	}
	if err := g.Release(second, time.Millisecond, true); err != nil {
		t.Fatalf("second release must succeed, got %v", err)
	}
	if got := g.Inflight(); got != 0 {
		t.Fatalf("inflight after release = %d, want 0", got)
	}
}

func TestAcquireRejectsWhenQueueIsFull(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.InitialWindow = 1
	opts.QueueLimit = 0
	g := impl.NewConcurrencyGovernor(opts)
	tk, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire must succeed, got %v", err)
	}
	if _, err := g.Acquire(context.Background()); !errors.Is(err, impl.ErrQueueFull) {
		t.Fatalf("acquire with a full queue must report ErrQueueFull, got %v", err)
	}
	if err := g.Release(tk, time.Millisecond, true); err != nil {
		t.Fatalf("release must succeed, got %v", err)
	}
}

func TestAcquireHonorsCanceledContext(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.InitialWindow = 1
	opts.QueueLimit = 4
	g := impl.NewConcurrencyGovernor(opts)
	tk, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire must succeed, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire on a canceled context must report context.Canceled, got %v", err)
	}
	if err := g.Release(tk, time.Millisecond, true); err != nil {
		t.Fatalf("release must succeed, got %v", err)
	}
}

func TestAcquireBlocksUntilRelease(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.InitialWindow = 1
	opts.QueueLimit = 2
	g := impl.NewConcurrencyGovernor(opts)
	first, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire must succeed, got %v", err)
	}
	type result struct {
		tk  impl.Ticket
		err error
	}
	done := make(chan result, 1)
	go func() {
		tk, err := g.Acquire(context.Background())
		done <- result{tk: tk, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && g.Queued() == 0 {
		time.Sleep(time.Millisecond)
	}
	if got := g.Queued(); got != 1 {
		t.Fatalf("queued callers = %d, want 1", got)
	}
	if err := g.Release(first, time.Millisecond, true); err != nil {
		t.Fatalf("release must succeed, got %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("queued acquire must succeed after release, got %v", got.err)
		}
		if got.tk.ID == 0 {
			t.Fatalf("queued acquire returned an empty ticket")
		}
		if err := g.Release(got.tk, time.Millisecond, true); err != nil {
			t.Fatalf("release of the queued ticket must succeed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("queued acquire did not finish after release")
	}
}

func TestSetBoundsValidatesAndClamps(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	g := impl.NewConcurrencyGovernor(t4Options(clock))
	if err := g.SetBounds(0, 4); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("zero min must report ErrBadBounds, got %v", err)
	}
	if err := g.SetBounds(5, 3); !errors.Is(err, impl.ErrBadBounds) {
		t.Fatalf("max below min must report ErrBadBounds, got %v", err)
	}
	if err := g.SetBounds(2, 8); err != nil {
		t.Fatalf("valid bounds must be accepted, got %v", err)
	}
	if got := g.Window(); got != 4 {
		t.Fatalf("window after widening bounds = %d, want the unchanged 4", got)
	}
}

func TestSuccessfulSamplesGrowWindow(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	g := impl.NewConcurrencyGovernor(t4Options(clock))
	for i := 0; i < 4; i++ {
		tk, err := g.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d must succeed, got %v", i, err)
		}
		if err := g.Release(tk, time.Millisecond, true); err != nil {
			t.Fatalf("release %d must succeed, got %v", i, err)
		}
	}
	if got := g.Window(); got != 5 {
		t.Fatalf("window after one full success sample batch = %d, want 5", got)
	}
}

func TestBusinessErrorsDoNotShrinkWindow(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.ErrorRateThreshold = 1.0
	g := impl.NewConcurrencyGovernor(opts)
	for i := 0; i < 4; i++ {
		tk, err := g.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d must succeed, got %v", i, err)
		}
		if err := g.Release(tk, time.Millisecond, false); err != nil {
			t.Fatalf("release %d must succeed, got %v", i, err)
		}
	}
	if got := g.Window(); got != 4 {
		t.Fatalf("window after business errors = %d, want the unchanged 4", got)
	}
	if got := g.State(); got != impl.StateClosed {
		t.Fatalf("breaker state = %q, want closed while the error rate stays at the threshold", got)
	}
}

func TestTimeoutsShrinkWindow(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.InitialWindow = 8
	opts.ErrorRateThreshold = 1.0
	g := impl.NewConcurrencyGovernor(opts)
	for i := 0; i < 4; i++ {
		tk, err := g.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d must succeed, got %v", i, err)
		}
		if err := g.Release(tk, 200*time.Millisecond, false); err != nil {
			t.Fatalf("release %d must succeed, got %v", i, err)
		}
	}
	if got := g.Window(); got != 5 {
		t.Fatalf("window after a full timeout sample batch = %d, want 5", got)
	}
}

func TestShrinkStopsAtMinimumWindow(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.InitialWindow = 3
	opts.MinWindow = 3
	opts.ErrorRateThreshold = 1.0
	g := impl.NewConcurrencyGovernor(opts)
	for i := 0; i < 4; i++ {
		tk, err := g.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d must succeed, got %v", i, err)
		}
		if err := g.Release(tk, 200*time.Millisecond, false); err != nil {
			t.Fatalf("release %d must succeed, got %v", i, err)
		}
	}
	if got := g.Window(); got != 3 {
		t.Fatalf("window after shrinking below the minimum = %d, want 3", got)
	}
}

func TestBreakerOpensOnHighErrorRate(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	g := impl.NewConcurrencyGovernor(t4Options(clock))
	openT4Breaker(t, g)
	if _, err := g.Acquire(context.Background()); !errors.Is(err, impl.ErrCircuitOpen) {
		t.Fatalf("acquire while the breaker is open must report ErrCircuitOpen, got %v", err)
	}
}

func TestRestoreKeepsBreakerCooldown(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	g := impl.NewConcurrencyGovernor(opts)
	openT4Breaker(t, g)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewConcurrencyGovernor(opts)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.State(); got != impl.StateOpen {
		t.Fatalf("restored breaker state = %q, want open", got)
	}
	if _, err := restored.Acquire(context.Background()); !errors.Is(err, impl.ErrCircuitOpen) {
		t.Fatalf("restored breaker must keep its cooldown, got %v", err)
	}
	clock.Advance(time.Second + time.Millisecond)
	probe, err := restored.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after the cooldown must be admitted as a probe, got %v", err)
	}
	if got := restored.State(); got != impl.StateHalfOpen {
		t.Fatalf("breaker state after the cooldown = %q, want half_open", got)
	}
	if err := restored.Release(probe, time.Millisecond, true); err != nil {
		t.Fatalf("releasing the successful probe must succeed, got %v", err)
	}
	if got := restored.State(); got != impl.StateClosed {
		t.Fatalf("breaker state after a successful probe = %q, want closed", got)
	}
}

func TestSnapshotRestorePreservesWindow(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	g := impl.NewConcurrencyGovernor(opts)
	for i := 0; i < 4; i++ {
		tk, err := g.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d must succeed, got %v", i, err)
		}
		if err := g.Release(tk, time.Millisecond, true); err != nil {
			t.Fatalf("release %d must succeed, got %v", i, err)
		}
	}
	if got := g.Window(); got != 5 {
		t.Fatalf("window before snapshot = %d, want 5", got)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewConcurrencyGovernor(opts)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.Window(); got != 5 {
		t.Fatalf("restored window = %d, want 5", got)
	}
	if got := restored.State(); got != impl.StateClosed {
		t.Fatalf("restored state = %q, want closed", got)
	}
	if got := restored.Inflight(); got != 0 {
		t.Fatalf("restored inflight = %d, want 0", got)
	}
}

func TestConcurrentAcquireRespectsWindow(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	opts.InitialWindow = 3
	opts.MaxWindow = 3
	opts.SampleWindow = 100
	opts.QueueLimit = 32
	opts.Timeout = time.Second
	g := impl.NewConcurrencyGovernor(opts)

	const workers = 10
	var current int32
	var maxSeen int32
	var successes int32
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk, err := g.Acquire(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			atomic.AddInt32(&successes, 1)
			running := atomic.AddInt32(&current, 1)
			for {
				seen := atomic.LoadInt32(&maxSeen)
				if running <= seen || atomic.CompareAndSwapInt32(&maxSeen, seen, running) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&current, -1)
			if err := g.Release(tk, time.Millisecond, true); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent acquire failed: %v", err)
	}
	if got := atomic.LoadInt32(&successes); got != workers {
		t.Fatalf("successful acquires = %d, want %d", got, workers)
	}
	if got := atomic.LoadInt32(&maxSeen); got > 3 {
		t.Fatalf("observed %d concurrent tickets, want at most 3", got)
	}
}

func TestHalfOpenAdmitsSingleProbe(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	opts := t4Options(clock)
	g := impl.NewConcurrencyGovernor(opts)
	openT4Breaker(t, g)
	clock.Advance(time.Second + time.Millisecond)
	probe, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("probe acquire must succeed, got %v", err)
	}
	if got := g.State(); got != impl.StateHalfOpen {
		t.Fatalf("breaker state = %q, want half_open", got)
	}
	if _, err := g.Acquire(context.Background()); !errors.Is(err, impl.ErrCircuitOpen) {
		t.Fatalf("second acquire while probing must report ErrCircuitOpen, got %v", err)
	}
	if err := g.Release(probe, time.Millisecond, true); err != nil {
		t.Fatalf("releasing the probe must succeed, got %v", err)
	}
	if got := g.State(); got != impl.StateClosed {
		t.Fatalf("breaker state after a successful probe = %q, want closed", got)
	}
}

func TestReleaseRejectsUnknownTickets(t *testing.T) {
	clock := &t4Clock{now: t4Start()}
	g := impl.NewConcurrencyGovernor(t4Options(clock))
	if err := g.Release(impl.Ticket{}, time.Millisecond, true); !errors.Is(err, impl.ErrBadTicket) {
		t.Fatalf("zero ticket must report ErrBadTicket, got %v", err)
	}
	if err := g.Release(impl.Ticket{ID: 999}, time.Millisecond, true); !errors.Is(err, impl.ErrBadTicket) {
		t.Fatalf("unknown ticket must report ErrBadTicket, got %v", err)
	}
}
