package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-d-06"
)

func newGuard() *impl.NacBindingGuard {
	return impl.NewNacBindingGuard(impl.NacOptions{
		LeaseSeconds:    5,
		IsolateCapacity: 4,
		MaxBindings:     64,
		AllowedVlans:    []int{10, 20},
	})
}

func authenticate(t *testing.T, g *impl.NacBindingGuard, port, mac string, vlan int) impl.NacBinding {
	t.Helper()
	b, err := g.Authenticate(port, impl.DeviceIdentity{MAC: mac, Tenant: "tenant-1", CertCN: "cn-" + mac}, vlan)
	if err != nil {
		t.Fatalf("authenticate %s/%s: %v", port, mac, err)
	}
	return b
}

func TestAuthenticateBindsPortAndVlan(t *testing.T) {
	g := newGuard()
	b := authenticate(t, g, "p1", "aa:01", 10)
	if b.Port != "p1" || b.MAC != "aa:01" || b.Vlan != 10 {
		t.Fatalf("binding fields mismatch: %+v", b)
	}
	if b.Remaining != 5 || b.Lease != 5 {
		t.Fatalf("lease = %d remaining = %d, want 5/5", b.Lease, b.Remaining)
	}
	if b.State != "bound" {
		t.Fatalf("state = %q, want bound", b.State)
	}
	got, ok := g.BindingOf("p1")
	if !ok || got.MAC != "aa:01" {
		t.Fatalf("BindingOf(p1) = %+v, %v; want bound aa:01", got, ok)
	}
}

func TestAuthenticateRejectsInvalidIdentity(t *testing.T) {
	g := newGuard()
	if _, err := g.Authenticate("", impl.DeviceIdentity{MAC: "aa:02"}, 10); !errors.Is(err, impl.ErrInvalidPort) {
		t.Fatalf("empty port: want ErrInvalidPort, got %v", err)
	}
	if _, err := g.Authenticate("p2", impl.DeviceIdentity{MAC: ""}, 10); !errors.Is(err, impl.ErrInvalidPort) {
		t.Fatalf("empty mac: want ErrInvalidPort, got %v", err)
	}
	if _, ok := g.BindingOf("p2"); ok {
		t.Fatalf("rejected authentication must not create a binding")
	}
}

func TestAuthenticateRejectsUnauthorizedVlan(t *testing.T) {
	g := newGuard()
	if _, err := g.Authenticate("p3", impl.DeviceIdentity{MAC: "aa:03"}, 30); !errors.Is(err, impl.ErrUnauthorizedVlan) {
		t.Fatalf("vlan 30 outside whitelist: want ErrUnauthorizedVlan, got %v", err)
	}
	if got := g.Stats().Denied; got != 1 {
		t.Fatalf("denied count = %d, want 1", got)
	}
}

func TestAuthenticateRenewsLeaseForSameDevice(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p4", "aa:04", 10)
	if expired := g.Expire(3); len(expired) != 0 {
		t.Fatalf("no lease should expire yet, got %v", expired)
	}
	b, ok := g.BindingOf("p4")
	if !ok || b.Remaining != 2 {
		t.Fatalf("remaining after 3s = %+v, %v; want 2", b, ok)
	}
	authenticate(t, g, "p4", "aa:04", 10)
	again, ok := g.BindingOf("p4")
	if !ok || again.Remaining != 5 {
		t.Fatalf("remaining after renewal = %+v, %v; want 5", again, ok)
	}
	if got := g.Stats().Active; got != 1 {
		t.Fatalf("active bindings = %d, want 1", got)
	}
}

func TestAuthenticateRejectsConflictingDevices(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p5", "aa:05", 10)
	if _, err := g.Authenticate("p5", impl.DeviceIdentity{MAC: "aa:06"}, 10); !errors.Is(err, impl.ErrPortBound) {
		t.Fatalf("second device on bound port: want ErrPortBound, got %v", err)
	}
	held, ok := g.BindingOf("p5")
	if !ok || held.MAC != "aa:05" {
		t.Fatalf("existing binding was disturbed: %+v, %v", held, ok)
	}
	if _, err := g.Authenticate("p6", impl.DeviceIdentity{MAC: "aa:05"}, 10); !errors.Is(err, impl.ErrIdentityConflict) {
		t.Fatalf("device bound elsewhere: want ErrIdentityConflict, got %v", err)
	}
}

func TestRebindMovesDeviceToNewPort(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p7", "aa:07", 10)
	moved, err := g.Rebind("p8", impl.DeviceIdentity{MAC: "aa:07"}, 20)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if moved.Port != "p8" || moved.Vlan != 20 || moved.Remaining != 5 {
		t.Fatalf("rebound binding = %+v", moved)
	}
	if _, ok := g.BindingOf("p7"); ok {
		t.Fatalf("old port p7 must be released after rebind")
	}
	if got := g.Stats().Rebound; got != 1 {
		t.Fatalf("rebound count = %d, want 1", got)
	}
}

func TestRebindRejectsUnknownDevice(t *testing.T) {
	g := newGuard()
	if _, err := g.Rebind("p9", impl.DeviceIdentity{MAC: "aa:09"}, 10); !errors.Is(err, impl.ErrUnknownDevice) {
		t.Fatalf("rebind unknown device: want ErrUnknownDevice, got %v", err)
	}
}

func TestReleaseFreesPortAndRejectsSecondRelease(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p10", "aa:10", 10)
	if err := g.Release("p10"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok := g.BindingOf("p10"); ok {
		t.Fatalf("released port still bound")
	}
	if got := g.Stats().Released; got != 1 {
		t.Fatalf("released count = %d, want 1", got)
	}
	if err := g.Release("p10"); !errors.Is(err, impl.ErrUnknownPort) {
		t.Fatalf("second release: want ErrUnknownPort, got %v", err)
	}
}

func TestExpireReleasesExpiredPortsInOrder(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p-b", "aa:11", 10)
	authenticate(t, g, "p-a", "aa:12", 10)
	expired := g.Expire(5)
	if len(expired) != 2 || expired[0] != "p-a" || expired[1] != "p-b" {
		t.Fatalf("expired ports = %v, want [p-a p-b]", expired)
	}
	if _, ok := g.BindingOf("p-a"); ok {
		t.Fatalf("expired binding p-a still present")
	}
	if got := g.Stats().Expired; got != 2 {
		t.Fatalf("expired count = %d, want 2", got)
	}
}

func TestIsolateMovesBindingIntoDomain(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p11", "aa:13", 10)
	if err := g.Isolate("p11", "posture-failed"); err != nil {
		t.Fatalf("isolate: %v", err)
	}
	isolated := g.IsolatedPorts()
	if len(isolated) != 1 || isolated[0] != "p11" {
		t.Fatalf("isolated ports = %v, want [p11]", isolated)
	}
	b, ok := g.BindingOf("p11")
	if !ok || b.State != "isolated" || b.Reason != "posture-failed" {
		t.Fatalf("isolated binding = %+v, %v", b, ok)
	}
	if got := g.Stats().Isolated; got != 1 {
		t.Fatalf("isolated count = %d, want 1", got)
	}
}

func TestIsolateRejectsUnknownPort(t *testing.T) {
	g := newGuard()
	if err := g.Isolate("p-missing", "manual"); !errors.Is(err, impl.ErrUnknownPort) {
		t.Fatalf("isolate unknown port: want ErrUnknownPort, got %v", err)
	}
	if len(g.IsolatedPorts()) != 0 {
		t.Fatalf("failed isolation must not create isolation entries")
	}
}

func TestSetVlanWhitelistReplacesPolicy(t *testing.T) {
	g := newGuard()
	if err := g.SetVlanWhitelist([]int{20}); err != nil {
		t.Fatalf("set whitelist: %v", err)
	}
	if _, err := g.Authenticate("p12", impl.DeviceIdentity{MAC: "aa:14"}, 10); !errors.Is(err, impl.ErrUnauthorizedVlan) {
		t.Fatalf("old vlan 10 must now be rejected, got %v", err)
	}
	if _, err := g.Authenticate("p12", impl.DeviceIdentity{MAC: "aa:14"}, 20); err != nil {
		t.Fatalf("new vlan 20 must be accepted, got %v", err)
	}
}

func TestAuditTrailKeepsTransitionOrder(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p13", "aa:15", 10)
	if err := g.Isolate("p13", "manual"); err != nil {
		t.Fatalf("isolate: %v", err)
	}
	if err := g.Release("p13"); err != nil {
		t.Fatalf("release: %v", err)
	}
	trail := g.AuditTrail()
	if len(trail) != 3 {
		t.Fatalf("audit length = %d, want 3", len(trail))
	}
	wantActions := []string{"authenticate", "isolate", "release"}
	for i, want := range wantActions {
		if trail[i].Action != want {
			t.Fatalf("audit[%d].Action = %q, want %q", i, trail[i].Action, want)
		}
	}
	if trail[0].Port != "p13" || trail[0].MAC != "aa:15" {
		t.Fatalf("audit[0] lost port or device: %+v", trail[0])
	}
	if seq := g.Stats().AuditEvents; seq != 3 {
		t.Fatalf("audit event counter = %d, want 3", seq)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	g := newGuard()
	authenticate(t, g, "p14", "aa:16", 10)
	if err := g.Isolate("p14", "audit"); err != nil {
		t.Fatalf("isolate: %v", err)
	}
	if expired := g.Expire(2); len(expired) != 0 {
		t.Fatalf("no lease expires at 2s, got %v", expired)
	}
	blob, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := newGuard()
	if err := restored.Restore(blob); err != nil {
		t.Fatalf("restore: %v", err)
	}
	b, ok := restored.BindingOf("p14")
	if !ok || b.Remaining != 3 || b.State != "isolated" {
		t.Fatalf("restored binding = %+v, %v; want remaining 3 isolated", b, ok)
	}
	if got := restored.Stats().Active; got != 1 {
		t.Fatalf("restored active = %d, want 1", got)
	}
	if ports := restored.IsolatedPorts(); len(ports) != 1 || ports[0] != "p14" {
		t.Fatalf("restored isolated ports = %v", ports)
	}
}

func TestConcurrentAuthenticateCountsEveryPort(t *testing.T) {
	g := newGuard()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			port := "port-" + string(rune('a'+i))
			_, _ = g.Authenticate(port, impl.DeviceIdentity{MAC: "mac-" + string(rune('a'+i))}, 10)
		}(i)
	}
	wg.Wait()
	stats := g.Stats()
	if stats.Authenticated != 20 {
		t.Fatalf("authenticated = %d, want 20", stats.Authenticated)
	}
	if stats.Active != 20 {
		t.Fatalf("active = %d, want 20", stats.Active)
	}
}
