package task

import (
	"errors"
	"sort"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-b-05"
)

func frozenOptions() impl.Options {
	return impl.Options{
		RatePerSecond: 1000,
		Burst:         50,
		MaxFanout:     4,
		MaxDepth:      6,
		Now:           func() time.Time { return time.Unix(1700000000, 0) },
	}
}

func newGuard(t *testing.T, opts impl.Options) *impl.ForkBurstGuard {
	t.Helper()
	g := impl.NewForkBurstGuard(opts)
	if g == nil {
		t.Fatal("NewForkBurstGuard returned a nil guard")
	}
	return g
}

func mustAdmit(t *testing.T, g *impl.ForkBurstGuard, ev impl.SpawnEvent) {
	t.Helper()
	if err := g.Admit(ev); err != nil {
		t.Fatalf("Admit(%+v): unexpected error: %v", ev, err)
	}
}

func sortedInts(in []int) []int {
	out := append([]int(nil), in...)
	sort.Ints(out)
	return out
}

func TestAdmitRejectsBlankTenantAndBadPID(t *testing.T) {
	g := newGuard(t, frozenOptions())
	if err := g.Admit(impl.SpawnEvent{Tenant: "", PID: 10}); !errors.Is(err, impl.ErrBadTenant) {
		t.Fatalf("blank tenant error = %v, want ErrBadTenant", err)
	}
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 0}); !errors.Is(err, impl.ErrBadEvent) {
		t.Fatalf("pid 0 error = %v, want ErrBadEvent", err)
	}
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 5, PPID: -1}); !errors.Is(err, impl.ErrBadEvent) {
		t.Fatalf("negative ppid error = %v, want ErrBadEvent", err)
	}
}

func TestAdmitRegistersRootAndChild(t *testing.T) {
	g := newGuard(t, frozenOptions())
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 100})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 101, PPID: 100})
	if count := g.ProcessCount("a"); count != 2 {
		t.Fatalf("ProcessCount = %d, want 2", count)
	}
	children := g.Children(100)
	if len(children) != 1 || children[0] != 101 {
		t.Fatalf("Children(100) = %v, want [101]", children)
	}
	budget := g.Budget("a")
	if budget.Tokens != 48 {
		t.Fatalf("remaining tokens = %d, want 48", budget.Tokens)
	}
	if budget.Capacity != 50 {
		t.Fatalf("capacity = %d, want 50", budget.Capacity)
	}
}

func TestAdmitRejectsUnknownParent(t *testing.T) {
	g := newGuard(t, frozenOptions())
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 201, PPID: 999}); !errors.Is(err, impl.ErrUnknownParent) {
		t.Fatalf("unknown parent error = %v, want ErrUnknownParent", err)
	}
	if count := g.ProcessCount("a"); count != 0 {
		t.Fatalf("ProcessCount after a rejected child = %d, want 0", count)
	}
}

func TestAdmitRejectsDuplicatePID(t *testing.T) {
	g := newGuard(t, frozenOptions())
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 300})
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 300}); !errors.Is(err, impl.ErrDuplicatePID) {
		t.Fatalf("duplicate pid error = %v, want ErrDuplicatePID", err)
	}
	if count := g.ProcessCount("a"); count != 1 {
		t.Fatalf("ProcessCount after a duplicate = %d, want 1", count)
	}
}

func TestAdmitEnforcesFanoutCap(t *testing.T) {
	opts := frozenOptions()
	opts.MaxFanout = 2
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 400})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 401, PPID: 400})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 402, PPID: 400})
	err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 403, PPID: 400})
	if !errors.Is(err, impl.ErrFanoutExceeded) {
		t.Fatalf("third child error = %v, want ErrFanoutExceeded", err)
	}
	violations := g.Violations()
	if len(violations) != 1 {
		t.Fatalf("violation count = %d, want 1", len(violations))
	}
	last := violations[0]
	if last.Kind != impl.ViolationFanout || last.PID != 400 || last.Tenant != "a" {
		t.Fatalf("violation = %+v, want fanout by tenant a at pid 400", last)
	}
}

func TestAdmitEnforcesDepthCap(t *testing.T) {
	opts := frozenOptions()
	opts.MaxDepth = 2
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 500})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 501, PPID: 500})
	err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 502, PPID: 501})
	if !errors.Is(err, impl.ErrDepthExceeded) {
		t.Fatalf("grandchild error = %v, want ErrDepthExceeded", err)
	}
	violations := g.Violations()
	if len(violations) != 1 || violations[0].Kind != impl.ViolationDepth || violations[0].PID != 502 {
		t.Fatalf("depth violation = %+v, want kind depth at pid 502", violations)
	}
	if count := g.ProcessCount("a"); count != 2 {
		t.Fatalf("ProcessCount after a depth violation = %d, want 2", count)
	}
}

func TestAdmitEnforcesRateBurst(t *testing.T) {
	opts := frozenOptions()
	opts.Burst = 3
	g := newGuard(t, opts)
	for _, pid := range []int{600, 601, 602} {
		mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: pid})
	}
	err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 603})
	if !errors.Is(err, impl.ErrRateLimited) {
		t.Fatalf("spawn above the burst error = %v, want ErrRateLimited", err)
	}
	if count := g.ProcessCount("a"); count != 3 {
		t.Fatalf("ProcessCount after rate limiting = %d, want 3", count)
	}
	if got := g.Budget("a"); got.Tokens != 0 {
		t.Fatalf("remaining tokens = %d, want 0", got.Tokens)
	}
	violations := g.Violations()
	if len(violations) != 1 || violations[0].Kind != impl.ViolationRate || violations[0].PID != 603 {
		t.Fatalf("rate violation = %+v, want kind rate at pid 603", violations)
	}
}

func TestBudgetReflectsTokensAndBlockedTenant(t *testing.T) {
	opts := frozenOptions()
	opts.Burst = 4
	opts.MaxFanout = 1
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 700})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 701, PPID: 700})
	budget := g.Budget("a")
	if budget.Tokens != 2 || budget.Blocked {
		t.Fatalf("budget before the breach = %+v, want 2 tokens and not blocked", budget)
	}
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 702, PPID: 700}); !errors.Is(err, impl.ErrFanoutExceeded) {
		t.Fatalf("second child error = %v, want ErrFanoutExceeded", err)
	}
	if !g.Budget("a").Blocked {
		t.Fatal("offending tenant is not blocked after the fanout breach")
	}
	other := g.Budget("b")
	if other.Blocked {
		t.Fatal("an unrelated tenant was blocked by another tenant's breach")
	}
	if other.Tokens != 4 || other.Processes != 0 {
		t.Fatalf("unrelated tenant budget = %+v, want 4 tokens and no processes", other)
	}
}

func TestAttachRegistersDiscoveredProcess(t *testing.T) {
	g := newGuard(t, frozenOptions())
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 800})
	if err := g.Attach("a", 801, 800); err != nil {
		t.Fatalf("Attach: unexpected error: %v", err)
	}
	if count := g.ProcessCount("a"); count != 2 {
		t.Fatalf("ProcessCount = %d, want 2", count)
	}
	children := g.Children(800)
	if len(children) != 1 || children[0] != 801 {
		t.Fatalf("Children(800) = %v, want [801]", children)
	}
}

func TestAttachIsIdempotentAndEnforcesFanout(t *testing.T) {
	opts := frozenOptions()
	opts.MaxFanout = 1
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 900})
	if err := g.Attach("a", 901, 900); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	if err := g.Attach("a", 901, 900); err != nil {
		t.Fatalf("repeated Attach for the same pair error = %v, want nil", err)
	}
	if err := g.Attach("a", 902, 900); !errors.Is(err, impl.ErrFanoutExceeded) {
		t.Fatalf("Attach above the fanout cap error = %v, want ErrFanoutExceeded", err)
	}
}

func TestReapTerminatesCondemnedSubtree(t *testing.T) {
	opts := frozenOptions()
	opts.MaxFanout = 2
	killed := make(map[int]bool)
	opts.Kill = func(pid int) error {
		killed[pid] = true
		return nil
	}
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1001, PPID: 1000})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1002, PPID: 1000})
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 1003, PPID: 1000}); !errors.Is(err, impl.ErrFanoutExceeded) {
		t.Fatalf("breaching child error = %v, want ErrFanoutExceeded", err)
	}
	reaped, err := g.Reap("a")
	if err != nil {
		t.Fatalf("Reap: unexpected error: %v", err)
	}
	got := sortedInts(reaped)
	want := []int{1000, 1001, 1002}
	if len(got) != len(want) {
		t.Fatalf("reaped pids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reaped pids = %v, want %v", got, want)
		}
	}
	for _, pid := range want {
		if !killed[pid] {
			t.Fatalf("pid %d was not signalled during reap", pid)
		}
	}
	if count := g.ProcessCount("a"); count != 0 {
		t.Fatalf("ProcessCount after reap = %d, want 0", count)
	}
	if g.Budget("a").Blocked {
		t.Fatal("tenant stayed blocked after its subtree was reaped")
	}
}

func TestReapWithoutViolationReturnsEmpty(t *testing.T) {
	g := newGuard(t, frozenOptions())
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1100})
	reaped, err := g.Reap("a")
	if err != nil {
		t.Fatalf("Reap: unexpected error: %v", err)
	}
	if len(reaped) != 0 {
		t.Fatalf("reaped pids = %v, want none", reaped)
	}
	if count := g.ProcessCount("a"); count != 1 {
		t.Fatalf("ProcessCount after a no-op reap = %d, want 1", count)
	}
}

func TestViolationsRecordTenantKindAndPID(t *testing.T) {
	opts := frozenOptions()
	opts.Burst = 1
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1200})
	if err := g.Admit(impl.SpawnEvent{Tenant: "a", PID: 1201}); !errors.Is(err, impl.ErrRateLimited) {
		t.Fatalf("second root error = %v, want ErrRateLimited", err)
	}
	violations := g.Violations()
	if len(violations) != 1 {
		t.Fatalf("violation count = %d, want 1", len(violations))
	}
	if violations[0].Tenant != "a" || violations[0].Kind != impl.ViolationRate || violations[0].PID != 1201 {
		t.Fatalf("violation = %+v, want tenant a, kind rate, pid 1201", violations[0])
	}
}

func TestSnapshotRestoreKeepsLiveTree(t *testing.T) {
	opts := frozenOptions()
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1300})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1301, PPID: 1300})
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("Snapshot returned an empty payload")
	}

	restored := newGuard(t, opts)
	if err := restored.Restore(snap, func(int) bool { return true }); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if count := restored.ProcessCount("a"); count != 2 {
		t.Fatalf("restored ProcessCount = %d, want 2", count)
	}
	if err := restored.Admit(impl.SpawnEvent{Tenant: "a", PID: 1302, PPID: 1301}); err != nil {
		t.Fatalf("Admit grandchild after restore: %v", err)
	}
	children := restored.Children(1301)
	if len(children) != 1 || children[0] != 1302 {
		t.Fatalf("Children(1301) after restore = %v, want [1302]", children)
	}
}

func TestRestoreRejectsCorruptAndDropsVanishedNodes(t *testing.T) {
	opts := frozenOptions()
	g := newGuard(t, opts)
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1400})
	mustAdmit(t, g, impl.SpawnEvent{Tenant: "a", PID: 1401, PPID: 1400})
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newGuard(t, opts)
	if err := restored.Restore([]byte("not-a-snapshot"), func(int) bool { return true }); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot error = %v, want ErrBadSnapshot", err)
	}
	if err := restored.Restore(snap, nil); !errors.Is(err, impl.ErrLivenessUnavailable) {
		t.Fatalf("restore without a liveness callback error = %v, want ErrLivenessUnavailable", err)
	}
	if err := restored.Restore(snap, func(pid int) bool { return pid != 1401 }); err != nil {
		t.Fatalf("Restore with liveness filtering: %v", err)
	}
	if count := restored.ProcessCount("a"); count != 1 {
		t.Fatalf("ProcessCount after dropping a vanished node = %d, want 1", count)
	}
	if children := restored.Children(1400); len(children) != 0 {
		t.Fatalf("Children(1400) = %v, want none after the child vanished", children)
	}
}
