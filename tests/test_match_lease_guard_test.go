package task

import (
	"errors"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-08"
)

var leaseBase = time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)

type leaseClock struct {
	now time.Time
}

func (c *leaseClock) Now() time.Time {
	return c.now
}

func (c *leaseClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func newLeaseGuard(max int, clk *leaseClock) *impl.MatchLeaseGuard {
	g := impl.NewMatchLeaseGuard(max)
	g.SetClock(clk.Now)
	return g
}

func TestAcquireGrantsFirstFence(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	lease, err := g.Acquire("match-1", "instance-a", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire returned error: %v", err)
	}
	if lease.Fence != 1 {
		t.Fatalf("first fencing token = %d, want 1", lease.Fence)
	}
	if lease.State != impl.LeaseActive {
		t.Fatalf("new lease state = %q, want %q", lease.State, impl.LeaseActive)
	}
	if want := leaseBase.Add(30 * time.Second); !lease.ExpiresAt.Equal(want) {
		t.Fatalf("lease deadline = %v, want %v", lease.ExpiresAt, want)
	}
	if lease.InstanceID != "instance-a" || lease.MatchID != "match-1" {
		t.Fatalf("lease bound to %q/%q, want instance-a/match-1", lease.InstanceID, lease.MatchID)
	}
}

func TestAcquireRejectsSecondInstance(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := g.Acquire("match-1", "instance-b", 30*time.Second); !errors.Is(err, impl.ErrLeaseHeld) {
		t.Fatalf("second acquire on a live match = %v, want ErrLeaseHeld", err)
	}
}

func TestAcquireRejectsNonPositiveTTL(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 0); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("zero ttl = %v, want ErrInvalidTTL", err)
	}
	if _, err := g.Acquire("match-1", "instance-a", -5*time.Second); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("negative ttl = %v, want ErrInvalidTTL", err)
	}
	if _, ok := g.Lease("match-1"); ok {
		t.Fatalf("a rejected acquire must not leave a lease behind")
	}
}

func TestAcquireRejectsBlankIDs(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("", "instance-a", time.Second); !errors.Is(err, impl.ErrInvalidMatchID) {
		t.Fatalf("blank match id = %v, want ErrInvalidMatchID", err)
	}
	if _, err := g.Acquire("match-1", "  ", time.Second); !errors.Is(err, impl.ErrInvalidInstanceID) {
		t.Fatalf("blank instance id = %v, want ErrInvalidInstanceID", err)
	}
}

func TestAcquireRespectsTableLimit(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(2, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := g.Acquire("match-2", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	if _, err := g.Acquire("match-3", "instance-a", 30*time.Second); !errors.Is(err, impl.ErrLeaseTableFull) {
		t.Fatalf("acquire beyond the table limit = %v, want ErrLeaseTableFull", err)
	}
}

func TestRenewExtendsDeadline(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	clk.Advance(10 * time.Second)
	if err := g.Renew("match-1", "instance-a", 1); err != nil {
		t.Fatalf("renew failed: %v", err)
	}
	lease, ok := g.Lease("match-1")
	if !ok {
		t.Fatalf("lease missing after renew")
	}
	if want := leaseBase.Add(40 * time.Second); !lease.ExpiresAt.Equal(want) {
		t.Fatalf("renewed deadline = %v, want %v", lease.ExpiresAt, want)
	}
}

func TestRenewRejectsWrongFence(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := g.Renew("match-1", "instance-a", 2); !errors.Is(err, impl.ErrFenceMismatch) {
		t.Fatalf("renew with a wrong token = %v, want ErrFenceMismatch", err)
	}
}

func TestRenewRejectsOtherInstance(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := g.Renew("match-1", "instance-b", 1); !errors.Is(err, impl.ErrNotLeaseHolder) {
		t.Fatalf("renew by a foreign instance = %v, want ErrNotLeaseHolder", err)
	}
}

func TestReleaseAllowsNextHolder(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := g.Release("match-1", "instance-a"); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if _, ok := g.Lease("match-1"); ok {
		t.Fatalf("released lease must not read as live")
	}
	next, err := g.Acquire("match-1", "instance-b", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	if next.Fence != 2 {
		t.Fatalf("fencing token after release = %d, want 2", next.Fence)
	}
}

func TestReleaseRejectsNonHolder(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := g.Release("match-1", "instance-b"); !errors.Is(err, impl.ErrNotLeaseHolder) {
		t.Fatalf("release by a foreign instance = %v, want ErrNotLeaseHolder", err)
	}
	if _, ok := g.Lease("match-1"); !ok {
		t.Fatalf("a rejected release must leave the lease in place")
	}
}

func TestTakeoverBumpsFenceAfterExpiry(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	clk.Advance(31 * time.Second)
	lease, err := g.Takeover("match-1", "instance-b")
	if err != nil {
		t.Fatalf("takeover after expiry failed: %v", err)
	}
	if lease.Fence != 2 {
		t.Fatalf("takeover fencing token = %d, want 2", lease.Fence)
	}
	if lease.State != impl.LeaseAligning {
		t.Fatalf("takeover lease state = %q, want %q before state alignment", lease.State, impl.LeaseAligning)
	}
	if want := clk.Now().Add(30 * time.Second); !lease.ExpiresAt.Equal(want) {
		t.Fatalf("takeover deadline = %v, want %v", lease.ExpiresAt, want)
	}
}

func TestTakeoverRejectsLiveLease(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if _, err := g.Takeover("match-1", "instance-b"); !errors.Is(err, impl.ErrLeaseActive) {
		t.Fatalf("takeover of a live lease = %v, want ErrLeaseActive", err)
	}
}

func TestFenceReportsCurrentToken(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Fence("match-9"); !errors.Is(err, impl.ErrLeaseNotFound) {
		t.Fatalf("fence of an unknown match = %v, want ErrLeaseNotFound", err)
	}
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if got, err := g.Fence("match-1"); err != nil || got != 1 {
		t.Fatalf("fence after acquire = %d (err=%v), want 1", got, err)
	}
}

func TestApplyChecksFence(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := g.Apply("match-1", "instance-a", 2, "score+1"); !errors.Is(err, impl.ErrFenceMismatch) {
		t.Fatalf("write with an unissued token = %v, want ErrFenceMismatch", err)
	}
	if err := g.Apply("match-1", "instance-b", 1, "score+1"); !errors.Is(err, impl.ErrNotLeaseHolder) {
		t.Fatalf("write by a foreign instance = %v, want ErrNotLeaseHolder", err)
	}
	if err := g.Apply("match-1", "instance-a", 1, "score+1"); err != nil {
		t.Fatalf("write with the current token failed: %v", err)
	}
}

func TestSnapshotRestoreKeepsFenceAndDeadline(t *testing.T) {
	clk := &leaseClock{now: leaseBase}
	g := newLeaseGuard(8, clk)
	if _, err := g.Acquire("match-1", "instance-a", 30*time.Second); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	clk2 := &leaseClock{now: leaseBase}
	restored := newLeaseGuard(8, clk2)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	fence, err := restored.Fence("match-1")
	if err != nil {
		t.Fatalf("fence after restore failed: %v", err)
	}
	if fence != 1 {
		t.Fatalf("fence after restore = %d, want 1", fence)
	}
	lease, ok := restored.Lease("match-1")
	if !ok {
		t.Fatalf("lease missing after restore")
	}
	if want := leaseBase.Add(30 * time.Second); !lease.ExpiresAt.Equal(want) {
		t.Fatalf("restored deadline = %v, want %v", lease.ExpiresAt, want)
	}
	if err := restored.Apply("match-1", "instance-a", 1, "score+2"); err != nil {
		t.Fatalf("write with the restored token failed: %v", err)
	}
}
