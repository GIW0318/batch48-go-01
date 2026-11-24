package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-22"
)

const (
	govTenant = "tenant-a"
	govRoute  = "route-a"
	govConn   = "conn-a"
)

func govRouteKey(route string) string { return impl.RouteKey(govTenant, route) }

func govConnKey(route, conn string) string { return impl.ConnKey(govTenant, route, conn) }

func govLimits(t *testing.T, g *impl.FlowRateGovernor, tenantBurst, routeBurst, connBurst uint64, rate float64) {
	t.Helper()
	if err := g.SetLimit(impl.LevelTenant, govTenant, rate, tenantBurst); err != nil {
		t.Fatalf("set tenant limit: %v", err)
	}
	if err := g.SetLimit(impl.LevelRoute, govRouteKey(govRoute), rate, routeBurst); err != nil {
		t.Fatalf("set route limit: %v", err)
	}
	if err := g.SetLimit(impl.LevelConn, govConnKey(govRoute, govConn), rate, connBurst); err != nil {
		t.Fatalf("set connection limit: %v", err)
	}
}

func govAllow(t *testing.T, g *impl.FlowRateGovernor, route, conn string, cost uint64, now time.Time) impl.Decision {
	t.Helper()
	d, err := g.Allow(govTenant, route, conn, cost, now)
	if err != nil {
		t.Fatalf("allow cost %d on %s/%s: %v", cost, route, conn, err)
	}
	return d
}

func TestAllowWithinBurstSucceeds(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 1)

	d := govAllow(t, g, govRoute, govConn, 30, t0)
	if !d.Allowed {
		t.Fatalf("a request of 30 inside a burst of 100 must be admitted")
	}
	if d.Remaining != 70 {
		t.Fatalf("tenant remainder after admitting 30 = %d, want 70", d.Remaining)
	}
	if d.Ticket.ID == 0 {
		t.Fatalf("an admitted request must carry a ticket identity")
	}
}

func TestAllowRejectsWhenBucketEmpty(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 10, 10, 10, 1)

	if d := govAllow(t, g, govRoute, govConn, 10, t0); !d.Allowed {
		t.Fatalf("the first request of 10 must consume the whole burst")
	}
	d, err := g.Allow(govTenant, govRoute, govConn, 1, t0)
	if err != nil {
		t.Fatalf("rejected request must not return a transport error: %v", err)
	}
	if d.Allowed {
		t.Fatalf("a request above the empty bucket must be rejected")
	}
	if d.Wait <= 0 {
		t.Fatalf("rejection must report a positive retry delay, got %v", d.Wait)
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 0 {
		t.Fatalf("tenant remainder after exhausting the burst = %d, want 0", got)
	}
}

func TestAllowDeductsAllThreeLevels(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 1)

	govAllow(t, g, govRoute, govConn, 40, t0)
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 60 {
		t.Fatalf("tenant level holds %d tokens, want 60", got)
	}
	if got := g.Remaining(impl.LevelRoute, govRouteKey(govRoute)); got != 60 {
		t.Fatalf("route level holds %d tokens, want 60", got)
	}
	if got := g.Remaining(impl.LevelConn, govConnKey(govRoute, govConn)); got != 60 {
		t.Fatalf("connection level holds %d tokens, want 60", got)
	}
}

func TestParentGatesChildAcrossRoutes(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	if err := g.SetLimit(impl.LevelTenant, govTenant, 1, 100); err != nil {
		t.Fatalf("set tenant limit: %v", err)
	}
	if err := g.SetLimit(impl.LevelRoute, govRouteKey("route-a"), 1, 100); err != nil {
		t.Fatalf("set route-a limit: %v", err)
	}
	if err := g.SetLimit(impl.LevelConn, govConnKey("route-a", "conn-a"), 1, 100); err != nil {
		t.Fatalf("set conn-a limit: %v", err)
	}
	if err := g.SetLimit(impl.LevelRoute, govRouteKey("route-b"), 1, 100); err != nil {
		t.Fatalf("set route-b limit: %v", err)
	}
	if err := g.SetLimit(impl.LevelConn, govConnKey("route-b", "conn-b"), 1, 100); err != nil {
		t.Fatalf("set conn-b limit: %v", err)
	}

	if d := govAllow(t, g, "route-a", "conn-a", 60, t0); !d.Allowed {
		t.Fatalf("the first route must fit inside the tenant budget")
	}
	d, err := g.Allow(govTenant, "route-b", "conn-b", 60, t0)
	if err != nil {
		t.Fatalf("allow on route-b: %v", err)
	}
	if d.Allowed {
		t.Fatalf("route-b must not be admitted once the tenant budget is short")
	}
	if d.Wait <= 0 {
		t.Fatalf("parent rejection must report a positive retry delay, got %v", d.Wait)
	}
	if got := g.Remaining(impl.LevelRoute, govRouteKey("route-b")); got != 100 {
		t.Fatalf("rejected request must not touch the sibling route bucket, remaining = %d", got)
	}
}

func TestRefillUsesElapsedTime(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 20)

	if d := govAllow(t, g, govRoute, govConn, 100, t0); !d.Allowed {
		t.Fatalf("the first request must consume the whole burst")
	}
	d, err := g.Allow(govTenant, govRoute, govConn, 5, t0.Add(250*time.Millisecond))
	if err != nil {
		t.Fatalf("allow after a quarter second: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("20 tokens per second for 250ms must refill exactly 5 tokens")
	}
	if d.Remaining != 0 {
		t.Fatalf("remainder after spending the refilled tokens = %d, want 0", d.Remaining)
	}
	next, err := g.Allow(govTenant, govRoute, govConn, 1, t0.Add(250*time.Millisecond))
	if err != nil {
		t.Fatalf("allow right after the refill: %v", err)
	}
	if next.Allowed {
		t.Fatalf("no tokens are left at the same instant, the request must be rejected")
	}
}

func TestClockRollbackDoesNotRefill(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 1)

	govAllow(t, g, govRoute, govConn, 30, t0)
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 70 {
		t.Fatalf("remainder before the clock jump = %d, want 70", got)
	}
	if d := govAllow(t, g, govRoute, govConn, 10, t0.Add(-5*time.Second)); !d.Allowed {
		t.Fatalf("a stale timestamp must not block a request that still fits")
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 60 {
		t.Fatalf("a stale timestamp must not mint tokens, remainder = %d, want 60", got)
	}
	d, err := g.Allow(govTenant, govRoute, govConn, 61, t0.Add(time.Second))
	if err != nil {
		t.Fatalf("allow one second after the original timestamp: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("one second at one token per second must refill exactly one token")
	}
	if d.Remaining != 0 {
		t.Fatalf("rollback must not move the refill origin backwards, remainder = %d, want 0", d.Remaining)
	}
}

func TestRejectWaitMatchesDeficit(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 10, 10, 10, 2)

	govAllow(t, g, govRoute, govConn, 10, t0)
	d, err := g.Allow(govTenant, govRoute, govConn, 4, t0)
	if err != nil {
		t.Fatalf("allow a request that must be rejected: %v", err)
	}
	if d.Allowed {
		t.Fatalf("the empty bucket must reject the request")
	}
	if d.Wait != 2*time.Second {
		t.Fatalf("four missing tokens at two tokens per second retry after %v, want 2s", d.Wait)
	}
}

func TestBorrowAllowsChildBeyondOwnBurst(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 20, 100, 1)

	d := govAllow(t, g, govRoute, govConn, 30, t0)
	if !d.Allowed {
		t.Fatalf("the route may borrow the missing ten tokens for one window")
	}
	if got := g.Remaining(impl.LevelRoute, govRouteKey(govRoute)); got != 0 {
		t.Fatalf("route remainder after borrowing = %d, want 0", got)
	}
	if got := g.Borrowed(impl.LevelRoute, govRouteKey(govRoute)); got != 10 {
		t.Fatalf("route borrowed amount = %d, want 10", got)
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 70 {
		t.Fatalf("the tenant must still pay for the whole request, remainder = %d, want 70", got)
	}
}

func TestBorrowRepaidOnSweep(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 20, 100, 1)
	govAllow(t, g, govRoute, govConn, 30, t0)

	g.Sweep(t0.Add(1500 * time.Millisecond))
	if got := g.Borrowed(impl.LevelRoute, govRouteKey(govRoute)); got != 0 {
		t.Fatalf("the borrowing must be settled by the next window, still owed %d", got)
	}
	if got := g.Remaining(impl.LevelRoute, govRouteKey(govRoute)); got != 0 {
		t.Fatalf("the refilled token must go to the debt first, route remainder = %d, want 0", got)
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 71 {
		t.Fatalf("tenant remainder after a 1.5s refill = %d, want 71", got)
	}
}

func TestRefundRestoresOwnLevel(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 1)

	d := govAllow(t, g, govRoute, govConn, 40, t0)
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 60 {
		t.Fatalf("tenant remainder before the refund = %d, want 60", got)
	}
	if err := g.Refund(d.Ticket); err != nil {
		t.Fatalf("refund of an admitted request: %v", err)
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 100 {
		t.Fatalf("tenant remainder after the refund = %d, want 100", got)
	}
	if got := g.Remaining(impl.LevelRoute, govRouteKey(govRoute)); got != 100 {
		t.Fatalf("route remainder after the refund = %d, want 100", got)
	}
	if got := g.Remaining(impl.LevelConn, govConnKey(govRoute, govConn)); got != 100 {
		t.Fatalf("connection remainder after the refund = %d, want 100", got)
	}
}

func TestRefundUnknownTicketRejected(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	if err := g.Refund(impl.Ticket{}); !errors.Is(err, impl.ErrUnknownTicket) {
		t.Fatalf("refunding a ticket that was never issued must fail with ErrUnknownTicket, got %v", err)
	}
}

func TestSetLimitValidatesInputs(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	if err := g.SetLimit(impl.LevelTenant, govTenant, -1, 10); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("a negative rate must fail with ErrBadLimit, got %v", err)
	}
	if err := g.SetLimit(impl.LevelTenant, govTenant, 1, 0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("a zero burst must fail with ErrBadLimit, got %v", err)
	}
	if err := g.SetLimit(impl.Level(9), govTenant, 1, 10); !errors.Is(err, impl.ErrBadLevel) {
		t.Fatalf("an unknown level must fail with ErrBadLevel, got %v", err)
	}
	if err := g.SetLimit(impl.LevelTenant, "", 1, 10); !errors.Is(err, impl.ErrBlankKey) {
		t.Fatalf("a blank key must fail with ErrBlankKey, got %v", err)
	}
}

func TestSetLimitClampsRemainder(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 1)
	govAllow(t, g, govRoute, govConn, 30, t0)
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 70 {
		t.Fatalf("tenant remainder before the limit change = %d, want 70", got)
	}
	if err := g.SetLimit(impl.LevelTenant, govTenant, 1, 50); err != nil {
		t.Fatalf("lowering the tenant burst to 50: %v", err)
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 50 {
		t.Fatalf("remainder must be clamped to the new burst, got %d, want 50", got)
	}
}

func TestSnapshotRestorePreservesRemainders(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 100, 100, 100, 1)
	govAllow(t, g, govRoute, govConn, 40, t0)

	snap := g.Snapshot()
	if snap.Version != 1 {
		t.Fatalf("snapshot version = %d, want 1", snap.Version)
	}
	restored := impl.NewFlowRateGovernor()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restoring a snapshot taken by the same build: %v", err)
	}
	if got := restored.Remaining(impl.LevelTenant, govTenant); got != 60 {
		t.Fatalf("restored tenant remainder = %d, want 60", got)
	}
	if got := restored.Remaining(impl.LevelRoute, govRouteKey(govRoute)); got != 60 {
		t.Fatalf("restored route remainder = %d, want 60", got)
	}
	d, err := restored.Allow(govTenant, govRoute, govConn, 50, t0)
	if err != nil {
		t.Fatalf("allow on the restored governor: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("the restored governor must admit a request that still fits")
	}
	if got := restored.Remaining(impl.LevelTenant, govTenant); got != 10 {
		t.Fatalf("restored tenant remainder after spending 50 = %d, want 10", got)
	}
}

func TestConcurrentAllowNeverOverIssues(t *testing.T) {
	g := impl.NewFlowRateGovernor()
	t0 := time.Unix(1700000000, 0)
	govLimits(t, g, 50, 50, 50, 1)

	var allowed int64
	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := g.Allow(govTenant, govRoute, govConn, 5, t0)
			if err != nil {
				errCh <- err
				return
			}
			if d.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent allow returned an error: %v", err)
	}
	if got := atomic.LoadInt64(&allowed); got != 10 {
		t.Fatalf("a burst of 50 admits exactly ten requests of 5, got %d", got)
	}
	if got := g.Remaining(impl.LevelTenant, govTenant); got != 0 {
		t.Fatalf("tenant remainder after the burst is spent = %d, want 0", got)
	}
}
