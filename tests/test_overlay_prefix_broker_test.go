package task

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-21"
)

func newBroker(limit int) *impl.PrefixBroker {
	return impl.NewPrefixBroker(limit)
}

func mustRegister(t *testing.T, b *impl.PrefixBroker, tenant, prefix string, metric uint32) {
	t.Helper()
	if err := b.Register(tenant, netip.MustParsePrefix(prefix), impl.Attrs{Metric: metric}); err != nil {
		t.Fatalf("register %s for %s: %v", prefix, tenant, err)
	}
}

func TestRegisterRejectsNonCanonicalPrefix(t *testing.T) {
	b := newBroker(4)
	err := b.Register("tenant-a", netip.MustParsePrefix("10.0.0.5/24"), impl.Attrs{})
	if !errors.Is(err, impl.ErrNotCanonical) {
		t.Fatalf("prefix with host bits set must be rejected with ErrNotCanonical, got %v", err)
	}
	if got := b.Total(); got != 0 {
		t.Fatalf("rejected prefix must not be recorded, total = %d", got)
	}
}

func TestRegisterRejectsBlankTenant(t *testing.T) {
	b := newBroker(4)
	err := b.Register("", netip.MustParsePrefix("10.0.0.0/24"), impl.Attrs{})
	if !errors.Is(err, impl.ErrBlankTenant) {
		t.Fatalf("blank tenant must be rejected with ErrBlankTenant, got %v", err)
	}
}

func TestRegisterRejectsDuplicatePrefix(t *testing.T) {
	b := newBroker(4)
	p := netip.MustParsePrefix("10.10.0.0/16")
	if err := b.Register("tenant-a", p, impl.Attrs{Metric: 1}); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := b.Register("tenant-a", p, impl.Attrs{Metric: 2})
	if !errors.Is(err, impl.ErrDuplicatePrefix) {
		t.Fatalf("repeated registration must fail with ErrDuplicatePrefix, got %v", err)
	}
	if got := b.Count("tenant-a"); got != 1 {
		t.Fatalf("duplicate registration must not add a second entry, count = %d", got)
	}
}

func TestRegisterRejectsSameTenantOverlap(t *testing.T) {
	b := newBroker(8)
	mustRegister(t, b, "tenant-a", "10.0.0.0/8", 1)
	err := b.Register("tenant-a", netip.MustParsePrefix("10.1.0.0/16"), impl.Attrs{})
	if !errors.Is(err, impl.ErrSameTenantOverlap) {
		t.Fatalf("overlap inside one tenant must fail with ErrSameTenantOverlap, got %v", err)
	}
	if got := b.Count("tenant-a"); got != 1 {
		t.Fatalf("rejected overlap must leave the tenant with one prefix, count = %d", got)
	}
}

func TestRegisterRejectsCrossTenantOverlap(t *testing.T) {
	b := newBroker(8)
	mustRegister(t, b, "tenant-a", "10.0.0.0/8", 1)
	err := b.Register("tenant-b", netip.MustParsePrefix("10.1.0.0/16"), impl.Attrs{})
	if !errors.Is(err, impl.ErrCrossTenantOverlap) {
		t.Fatalf("cross tenant overlap without transit must fail with ErrCrossTenantOverlap, got %v", err)
	}
	if got := b.Count("tenant-b"); got != 0 {
		t.Fatalf("rejected cross tenant prefix must not be recorded, count = %d", got)
	}
}

func TestTransitAllowsOverlapAndLongestPrefixWins(t *testing.T) {
	b := newBroker(8)
	b.SetTransit("tenant-a", "tenant-b", true)
	mustRegister(t, b, "tenant-a", "10.0.0.0/8", 10)
	mustRegister(t, b, "tenant-b", "10.1.0.0/16", 20)

	tenant, ok := b.Lookup(netip.MustParseAddr("10.1.2.3"))
	if !ok || tenant != "tenant-b" {
		t.Fatalf("lookup inside the more specific prefix = (%q, %v), want (tenant-b, true)", tenant, ok)
	}
	tenant, ok = b.Lookup(netip.MustParseAddr("10.2.2.3"))
	if !ok || tenant != "tenant-a" {
		t.Fatalf("lookup outside the more specific prefix = (%q, %v), want (tenant-a, true)", tenant, ok)
	}
	if _, ok := b.Lookup(netip.MustParseAddr("192.168.1.1")); ok {
		t.Fatalf("address outside every registered prefix must not match")
	}
}

func TestPrefixLimitLeavesTableUntouched(t *testing.T) {
	b := newBroker(2)
	mustRegister(t, b, "tenant-a", "10.0.0.0/24", 1)
	mustRegister(t, b, "tenant-a", "10.0.1.0/24", 2)

	err := b.Register("tenant-a", netip.MustParsePrefix("10.0.2.0/24"), impl.Attrs{})
	if !errors.Is(err, impl.ErrPrefixLimit) {
		t.Fatalf("registration beyond the per tenant limit must fail with ErrPrefixLimit, got %v", err)
	}
	if got := b.Count("tenant-a"); got != 2 {
		t.Fatalf("rejected registration must leave the two prefixes intact, count = %d", got)
	}
	if tenant, ok := b.Lookup(netip.MustParseAddr("10.0.1.9")); !ok || tenant != "tenant-a" {
		t.Fatalf("existing prefixes must stay reachable, got (%q, %v)", tenant, ok)
	}
}

func TestWithdrawUnknownPrefixReportsError(t *testing.T) {
	b := newBroker(4)
	mustRegister(t, b, "tenant-a", "10.0.0.0/24", 1)

	if err := b.Withdraw("tenant-a", netip.MustParsePrefix("10.9.0.0/24")); !errors.Is(err, impl.ErrUnknownPrefix) {
		t.Fatalf("withdrawing an unregistered prefix must fail with ErrUnknownPrefix, got %v", err)
	}
	if err := b.Withdraw("tenant-ghost", netip.MustParsePrefix("10.0.0.0/24")); !errors.Is(err, impl.ErrUnknownTenant) {
		t.Fatalf("withdrawing from an unknown tenant must fail with ErrUnknownTenant, got %v", err)
	}
	if got := b.Count("tenant-a"); got != 1 {
		t.Fatalf("failed withdrawals must not change the table, count = %d", got)
	}
}

func TestWithdrawRemovesPrefixFromLookupAndCount(t *testing.T) {
	b := newBroker(4)
	mustRegister(t, b, "tenant-a", "192.168.0.0/16", 1)
	if _, ok := b.Lookup(netip.MustParseAddr("192.168.4.4")); !ok {
		t.Fatalf("registered prefix must be reachable before the withdrawal")
	}

	if err := b.Withdraw("tenant-a", netip.MustParsePrefix("192.168.0.0/16")); err != nil {
		t.Fatalf("withdraw failed: %v", err)
	}
	if _, ok := b.Lookup(netip.MustParseAddr("192.168.4.4")); ok {
		t.Fatalf("withdrawn prefix must disappear from the lookup table")
	}
	if got := b.Count("tenant-a"); got != 0 {
		t.Fatalf("withdrawal must decrement the tenant count, got %d", got)
	}
	if got := b.Total(); got != 0 {
		t.Fatalf("withdrawal must decrement the global count, got %d", got)
	}
}

func TestFilterBlocksUnpermittedRegistration(t *testing.T) {
	b := newBroker(4)
	f := impl.Filter{Allow: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}
	if err := b.SetFilter("tenant-a", f); err != nil {
		t.Fatalf("setting a valid filter failed: %v", err)
	}
	err := b.Register("tenant-a", netip.MustParsePrefix("10.0.0.0/8"), impl.Attrs{})
	if !errors.Is(err, impl.ErrFiltered) {
		t.Fatalf("prefix outside the allow list must fail with ErrFiltered, got %v", err)
	}
	if got := b.Count("tenant-a"); got != 0 {
		t.Fatalf("filtered prefix must not be recorded, count = %d", got)
	}
}

func TestFilterHidesPrefixFromLookup(t *testing.T) {
	b := newBroker(4)
	mustRegister(t, b, "tenant-a", "10.0.0.0/8", 1)
	if _, ok := b.Lookup(netip.MustParseAddr("10.1.1.1")); !ok {
		t.Fatalf("registered prefix must be reachable before the filter changes")
	}

	deny := impl.Filter{Deny: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	if err := b.SetFilter("tenant-a", deny); err != nil {
		t.Fatalf("setting a deny filter failed: %v", err)
	}
	if _, ok := b.Lookup(netip.MustParseAddr("10.1.1.1")); ok {
		t.Fatalf("prefix denied by the filter must not be returned by the lookup")
	}
	if got := b.Count("tenant-a"); got != 1 {
		t.Fatalf("filter changes must not drop the registration count, got %d", got)
	}
}

func TestTransitIsSymmetric(t *testing.T) {
	b := newBroker(4)
	if b.TransitAllowed("tenant-a", "tenant-b") {
		t.Fatalf("transit must be off before it is granted")
	}
	b.SetTransit("tenant-a", "tenant-b", true)
	if !b.TransitAllowed("tenant-a", "tenant-b") || !b.TransitAllowed("tenant-b", "tenant-a") {
		t.Fatalf("transit grant must hold in both directions")
	}
	b.SetTransit("tenant-b", "tenant-a", false)
	if b.TransitAllowed("tenant-a", "tenant-b") || b.TransitAllowed("tenant-b", "tenant-a") {
		t.Fatalf("transit revocation must clear both directions")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	b := newBroker(8)
	mustRegister(t, b, "tenant-a", "10.0.0.0/8", 5)
	mustRegister(t, b, "tenant-b", "192.168.0.0/16", 9)
	snap := b.Snapshot()
	if snap.Version != 1 {
		t.Fatalf("snapshot version = %d, want 1", snap.Version)
	}

	restored := newBroker(1)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restoring a snapshot taken by the same build failed: %v", err)
	}
	if got := restored.Total(); got != 2 {
		t.Fatalf("restored broker holds %d prefixes, want 2", got)
	}
	if got := restored.Count("tenant-a"); got != 1 {
		t.Fatalf("restored tenant-a holds %d prefixes, want 1", got)
	}
	if tenant, ok := restored.Lookup(netip.MustParseAddr("10.9.9.9")); !ok || tenant != "tenant-a" {
		t.Fatalf("restored lookup for tenant-a = (%q, %v)", tenant, ok)
	}
	if tenant, ok := restored.Lookup(netip.MustParseAddr("192.168.7.7")); !ok || tenant != "tenant-b" {
		t.Fatalf("restored lookup for tenant-b = (%q, %v)", tenant, ok)
	}
}

func TestRestoreRejectsForeignVersion(t *testing.T) {
	b := newBroker(4)
	err := b.Restore(impl.Snapshot{Version: 99})
	if !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("snapshot version 99 must be rejected with ErrBadSnapshot, got %v", err)
	}
}

func TestConcurrentRegisterIsSerialized(t *testing.T) {
	b := newBroker(64)
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		addr := netip.MustParseAddr("10.0.0.1")
		for {
			select {
			case <-stop:
				return
			default:
				b.Lookup(addr)
				runtime.Gosched()
			}
		}
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tenant := fmt.Sprintf("tenant-%d", idx)
			for j := 0; j < 4; j++ {
				p := netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.0/24", idx, j))
				if err := b.Register(tenant, p, impl.Attrs{Metric: uint32(j)}); err != nil {
					errCh <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	<-readerDone
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent registration failed: %v", err)
	}
	if got := b.Total(); got != 32 {
		t.Fatalf("after concurrent registration total = %d, want 32", got)
	}

	for i := 0; i < 8; i++ {
		tenant := fmt.Sprintf("tenant-%d", i)
		for j := 0; j < 4; j++ {
			p := netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.0/24", i, j))
			if err := b.Withdraw(tenant, p); err != nil {
				t.Fatalf("withdraw %s from %s failed: %v", p, tenant, err)
			}
		}
	}
	if got := b.Total(); got != 0 {
		t.Fatalf("after withdrawing every prefix total = %d, want 0", got)
	}
}
