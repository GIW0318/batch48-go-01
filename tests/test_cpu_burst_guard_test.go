package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-b-15"
)

func burstOptions() impl.Options {
	return impl.Options{
		PeriodMs:    100,
		MinQuotaMs:  5,
		MaxBorrowMs: 50,
		MaxTenants:  6,
	}
}

func TestSetShareRejectsOutOfRange(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetShare("alpha", -1); !errors.Is(err, impl.ErrBadShare) {
		t.Fatalf("negative share = %v, want ErrBadShare", err)
	}
	if err := g.SetShare("alpha", 10001); !errors.Is(err, impl.ErrBadShare) {
		t.Fatalf("oversized share = %v, want ErrBadShare", err)
	}
}

func TestSetShareAllocatesProportionalQuotas(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetShare("alpha", 30); err != nil {
		t.Fatalf("share alpha: %v", err)
	}
	if err := g.SetShare("beta", 70); err != nil {
		t.Fatalf("share beta: %v", err)
	}
	a, err := g.Usage("alpha")
	if err != nil {
		t.Fatalf("usage alpha: %v", err)
	}
	if a.QuotaMs != 30 {
		t.Fatalf("alpha quota = %d, want 30", a.QuotaMs)
	}
	b, err := g.Usage("beta")
	if err != nil {
		t.Fatalf("usage beta: %v", err)
	}
	if b.QuotaMs != 70 {
		t.Fatalf("beta quota = %d, want 70", b.QuotaMs)
	}
	names := g.Tenants()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("tenants = %v, want [alpha beta]", names)
	}
}

func TestSetQuotaRejectsBadPeriod(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 0, 10); !errors.Is(err, impl.ErrBadQuota) {
		t.Fatalf("zero period = %v, want ErrBadQuota", err)
	}
	if err := g.SetQuota("alpha", -5, 10); !errors.Is(err, impl.ErrBadQuota) {
		t.Fatalf("negative period = %v, want ErrBadQuota", err)
	}
}

func TestSetQuotaRejectsNegativeQuota(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, -1); !errors.Is(err, impl.ErrBadQuota) {
		t.Fatalf("negative quota = %v, want ErrBadQuota", err)
	}
}

func TestSetQuotaStoresPeriodAndQuota(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	u, err := g.Usage("alpha")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if u.Tenant != "alpha" || u.PeriodMs != 100 || u.QuotaMs != 40 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestTenantLimitEnforced(t *testing.T) {
	opts := burstOptions()
	opts.MaxTenants = 1
	g := impl.NewCpuBurstGuard(opts)
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("first tenant: %v", err)
	}
	if err := g.SetShare("beta", 10); !errors.Is(err, impl.ErrTenantLimit) {
		t.Fatalf("second tenant = %v, want ErrTenantLimit", err)
	}
}

func TestThrottledCountsPerTenant(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("quota alpha: %v", err)
	}
	if err := g.SetQuota("beta", 100, 20); err != nil {
		t.Fatalf("quota beta: %v", err)
	}
	if err := g.Throttled("alpha", 100); err != nil {
		t.Fatalf("throttle alpha 1: %v", err)
	}
	if err := g.Throttled("alpha", 200); err != nil {
		t.Fatalf("throttle alpha 2: %v", err)
	}
	if err := g.Throttled("beta", 100); err != nil {
		t.Fatalf("throttle beta: %v", err)
	}
	a, err := g.Usage("alpha")
	if err != nil || a.Throttled != 2 {
		t.Fatalf("alpha throttled = %d err %v, want 2", a.Throttled, err)
	}
	b, err := g.Usage("beta")
	if err != nil || b.Throttled != 1 {
		t.Fatalf("beta throttled = %d err %v, want 1", b.Throttled, err)
	}
}

func TestThrottledUnknownTenant(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.Throttled("ghost", 100); !errors.Is(err, impl.ErrUnknownTenant) {
		t.Fatalf("throttle unknown = %v, want ErrUnknownTenant", err)
	}
}

func TestBorrowGrantsUpToIdle(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("quota alpha: %v", err)
	}
	if err := g.SetQuota("beta", 100, 20); err != nil {
		t.Fatalf("quota beta: %v", err)
	}
	if got := g.Idle(); got != 40 {
		t.Fatalf("idle = %d, want 40", got)
	}
	if got, err := g.Borrow("alpha", 30); err != nil || got != 30 {
		t.Fatalf("first borrow = %d err %v, want 30", got, err)
	}
	if got, err := g.Borrow("alpha", 20); err != nil || got != 10 {
		t.Fatalf("second borrow = %d err %v, want 10", got, err)
	}
	if got, err := g.Borrowed("alpha"); err != nil || got != 40 {
		t.Fatalf("borrowed = %d err %v, want 40", got, err)
	}
	u, err := g.Usage("alpha")
	if err != nil || u.BorrowedMs != 40 {
		t.Fatalf("usage borrowed = %d err %v, want 40", u.BorrowedMs, err)
	}
}

func TestBorrowRejectsNonPositiveRequest(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("quota: %v", err)
	}
	if _, err := g.Borrow("alpha", 0); !errors.Is(err, impl.ErrBadBorrow) {
		t.Fatalf("zero borrow = %v, want ErrBadBorrow", err)
	}
	if _, err := g.Borrow("alpha", -3); !errors.Is(err, impl.ErrBadBorrow) {
		t.Fatalf("negative borrow = %v, want ErrBadBorrow", err)
	}
}

func TestBorrowUnknownTenant(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if _, err := g.Borrow("ghost", 5); !errors.Is(err, impl.ErrUnknownTenant) {
		t.Fatalf("borrow unknown = %v, want ErrUnknownTenant", err)
	}
}

func TestRebalanceReturnsWeightedQuotas(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetShare("alpha", 10); err != nil {
		t.Fatalf("share alpha: %v", err)
	}
	if err := g.SetShare("beta", 30); err != nil {
		t.Fatalf("share beta: %v", err)
	}
	quotas, err := g.Rebalance()
	if err != nil {
		t.Fatalf("rebalance: %v", err)
	}
	if len(quotas) != 2 {
		t.Fatalf("rebalanced tenants = %d, want 2", len(quotas))
	}
	if quotas["alpha"] != 25 || quotas["beta"] != 75 {
		t.Fatalf("quotas = %v, want alpha 25 beta 75", quotas)
	}
}

func TestUsageUnknownTenant(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if _, err := g.Usage("ghost"); !errors.Is(err, impl.ErrUnknownTenant) {
		t.Fatalf("usage unknown = %v, want ErrUnknownTenant", err)
	}
}

func TestSnapshotRestoreKeepsCounters(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("quota: %v", err)
	}
	if err := g.Throttled("alpha", 100); err != nil {
		t.Fatalf("throttle: %v", err)
	}
	if _, err := g.Borrow("alpha", 15); err != nil {
		t.Fatalf("borrow: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewCpuBurstGuard(burstOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	u, err := other.Usage("alpha")
	if err != nil {
		t.Fatalf("usage after restore: %v", err)
	}
	if u.QuotaMs != 40 || u.Throttled != 1 || u.BorrowedMs != 15 {
		t.Fatalf("restored usage = %+v", u)
	}
	if got := other.Idle(); got != 45 {
		t.Fatalf("restored idle = %d, want 45", got)
	}
}

func TestRestoreRejectsCorruptLedger(t *testing.T) {
	g := impl.NewCpuBurstGuard(burstOptions())
	if err := g.SetQuota("alpha", 100, 40); err != nil {
		t.Fatalf("quota: %v", err)
	}
	if err := g.Restore([]byte("garbage")); !errors.Is(err, impl.ErrLedgerCorrupt) {
		t.Fatalf("corrupt restore = %v, want ErrLedgerCorrupt", err)
	}
	if _, err := g.Usage("alpha"); err != nil {
		t.Fatalf("rejected restore lost live tenant: %v", err)
	}
}
