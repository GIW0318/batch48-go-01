package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-b-07"
)

func arbiterOptions() impl.Options {
	return impl.Options{
		MaxProcs:            16,
		MaxHistoryPerTenant: 8,
		ProtectWindow:       100,
		MaxReclaimPerWindow: 1 << 20,
	}
}

func registerBatch(t *testing.T, a *impl.OomArbiter, procs ...impl.Proc) {
	t.Helper()
	for _, p := range procs {
		if err := a.Register(p); err != nil {
			t.Fatalf("register pid %d: %v", p.PID, err)
		}
	}
}

func TestRegisterRejectsBadProc(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	if err := a.Register(impl.Proc{PID: 0, Tenant: "t1", RSS: 100, Weight: 1}); !errors.Is(err, impl.ErrBadProc) {
		t.Fatalf("pid 0 must fail with ErrBadProc, got %v", err)
	}
	if err := a.Register(impl.Proc{PID: 5, Tenant: "   ", RSS: 100, Weight: 1}); !errors.Is(err, impl.ErrBadProc) {
		t.Fatalf("blank tenant must fail with ErrBadProc, got %v", err)
	}
	if err := a.Register(impl.Proc{PID: 6, Tenant: "t1", RSS: 0, Weight: 1}); !errors.Is(err, impl.ErrBadProc) {
		t.Fatalf("zero RSS must fail with ErrBadProc, got %v", err)
	}
}

func TestRegisterRejectsDuplicatePID(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a, impl.Proc{PID: 10, Tenant: "t1", RSS: 100, Weight: 20})
	err := a.Register(impl.Proc{PID: 10, Tenant: "t1", RSS: 50, Weight: 30})
	if !errors.Is(err, impl.ErrDuplicatePID) {
		t.Fatalf("duplicate pid must fail with ErrDuplicatePID, got %v", err)
	}
	got, ok := a.Proc(10)
	if !ok {
		t.Fatalf("original process vanished after a rejected duplicate")
	}
	if got.RSS != 100 {
		t.Fatalf("original process changed, rss = %d want 100", got.RSS)
	}
}

func TestRegisterEnforcesProcLimit(t *testing.T) {
	opts := arbiterOptions()
	opts.MaxProcs = 2
	a := impl.NewOomArbiter(opts)
	registerBatch(t, a,
		impl.Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 1},
		impl.Proc{PID: 2, Tenant: "t1", RSS: 10, Weight: 2},
	)
	err := a.Register(impl.Proc{PID: 3, Tenant: "t1", RSS: 10, Weight: 3})
	if !errors.Is(err, impl.ErrProcLimit) {
		t.Fatalf("third process must fail with ErrProcLimit, got %v", err)
	}
	if _, ok := a.Proc(3); ok {
		t.Fatalf("rejected process must not be registered")
	}
}

func TestPressureRejectsBadEvent(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 11, Tenant: "t1", RSS: 10, Weight: 1},
		impl.Proc{PID: 12, Tenant: "t1", RSS: 10, Weight: 2},
	)
	if _, err := a.Pressure(impl.PressureEvent{Window: 0, Need: 10, At: 100}); !errors.Is(err, impl.ErrBadEvent) {
		t.Fatalf("zero window must fail with ErrBadEvent, got %v", err)
	}
	if _, err := a.Pressure(impl.PressureEvent{Window: 1, Need: 0, At: 100}); !errors.Is(err, impl.ErrBadEvent) {
		t.Fatalf("zero need must fail with ErrBadEvent, got %v", err)
	}
}

func TestPressureOrdersVictimsByWeight(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 11, Tenant: "t1", RSS: 10, Weight: 10},
		impl.Proc{PID: 12, Tenant: "t1", RSS: 10, Weight: 90},
		impl.Proc{PID: 13, Tenant: "t1", RSS: 10, Weight: 50},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 1, Need: 30, At: 100})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if len(plan.Victims) != 3 {
		t.Fatalf("victim count = %d, want 3", len(plan.Victims))
	}
	want := []int{12, 13, 11}
	for i, pid := range want {
		if plan.Victims[i].PID != pid {
			t.Fatalf("victim %d pid = %d, want %d", i, plan.Victims[i].PID, pid)
		}
	}
	if plan.Reclaimed != 30 {
		t.Fatalf("planned reclaimed bytes = %d, want 30", plan.Reclaimed)
	}
	if plan.Shortfall != 0 {
		t.Fatalf("shortfall = %d, want 0", plan.Shortfall)
	}
}

func TestPressureIsReadOnlyUntilCommit(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 21, Tenant: "t1", RSS: 10, Weight: 90},
		impl.Proc{PID: 22, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 1, Need: 10, At: 100})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if got := len(a.History("t1")); got != 0 {
		t.Fatalf("pressure must not record decisions before commit, history = %d", got)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := len(a.History("t1")); got != 1 {
		t.Fatalf("history after commit = %d, want 1", got)
	}
}

func TestCommitRecordsKillDecision(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 31, Tenant: "t1", RSS: 10, Weight: 90},
		impl.Proc{PID: 32, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 4, Need: 10, At: 200})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatalf("commit: %v", err)
	}
	h := a.History("t1")
	if len(h) != 1 {
		t.Fatalf("history length = %d, want 1", len(h))
	}
	d := h[0]
	if d.Window != 4 || d.PID != 31 || d.Tenant != "t1" {
		t.Fatalf("decision identity = window %d pid %d tenant %q", d.Window, d.PID, d.Tenant)
	}
	if d.Action != impl.ActionKill {
		t.Fatalf("decision action = %q, want %q", d.Action, impl.ActionKill)
	}
	if d.Bytes != 10 {
		t.Fatalf("decision bytes = %d, want 10", d.Bytes)
	}
	if d.Reason == "" {
		t.Fatalf("decision must carry a reason code")
	}
}

func TestCommitRejectsStalePlan(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 41, Tenant: "t1", RSS: 10, Weight: 90},
		impl.Proc{PID: 42, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 5, Need: 10, At: 300})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	plan.Window = 4
	if err := a.Commit(plan); !errors.Is(err, impl.ErrStalePlan) {
		t.Fatalf("stale plan must fail with ErrStalePlan, got %v", err)
	}
	if got := len(a.History("t1")); got != 0 {
		t.Fatalf("rejected plan must not write history, got %d entries", got)
	}
}

func TestCommitRejectsSecondCommit(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 51, Tenant: "t1", RSS: 10, Weight: 90},
		impl.Proc{PID: 52, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 6, Need: 10, At: 400})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := a.Commit(plan); !errors.Is(err, impl.ErrPlanCommitted) {
		t.Fatalf("second commit must fail with ErrPlanCommitted, got %v", err)
	}
	if got := len(a.History("t1")); got != 1 {
		t.Fatalf("history after double commit = %d, want 1", got)
	}
}

func TestPlanStopsAtNeedWithPartialLastVictim(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 61, Tenant: "t1", RSS: 100, Weight: 90},
		impl.Proc{PID: 62, Tenant: "t1", RSS: 100, Weight: 50},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 7, Need: 150, At: 500})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if len(plan.Victims) != 2 {
		t.Fatalf("victim count = %d, want 2", len(plan.Victims))
	}
	if plan.Victims[0].PID != 61 || plan.Victims[0].Bytes != 100 || !plan.Victims[0].Kill {
		t.Fatalf("first victim = pid %d bytes %d kill %v, want full kill of 61",
			plan.Victims[0].PID, plan.Victims[0].Bytes, plan.Victims[0].Kill)
	}
	if plan.Victims[1].PID != 62 || plan.Victims[1].Bytes != 50 || plan.Victims[1].Kill {
		t.Fatalf("second victim = pid %d bytes %d kill %v, want partial reclaim of 62",
			plan.Victims[1].PID, plan.Victims[1].Bytes, plan.Victims[1].Kill)
	}
	if plan.Reclaimed != 150 {
		t.Fatalf("reclaimed = %d, want 150", plan.Reclaimed)
	}
}

func TestPlanReportsShortfallWhenMemoryIsScarce(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 71, Tenant: "t1", RSS: 100, Weight: 90},
		impl.Proc{PID: 72, Tenant: "t1", RSS: 200, Weight: 50},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 8, Need: 1000, At: 600})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if plan.Reclaimed != 300 {
		t.Fatalf("reclaimed = %d, want 300", plan.Reclaimed)
	}
	if plan.Shortfall != 700 {
		t.Fatalf("shortfall = %d, want 700", plan.Shortfall)
	}
	if !plan.Killable {
		t.Fatalf("plan with full kills must be marked killable")
	}
}

func TestHistoryCappedPerTenant(t *testing.T) {
	opts := arbiterOptions()
	opts.MaxHistoryPerTenant = 3
	a := impl.NewOomArbiter(opts)
	registerBatch(t, a,
		impl.Proc{PID: 81, Tenant: "t1", RSS: 10, Weight: 50},
		impl.Proc{PID: 82, Tenant: "t1", RSS: 10, Weight: 40},
		impl.Proc{PID: 83, Tenant: "t1", RSS: 10, Weight: 30},
		impl.Proc{PID: 84, Tenant: "t1", RSS: 10, Weight: 20},
		impl.Proc{PID: 85, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 9, Need: 50, At: 700})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if len(plan.Victims) != 5 {
		t.Fatalf("victim count = %d, want 5", len(plan.Victims))
	}
	if err := a.Commit(plan); err != nil {
		t.Fatalf("commit: %v", err)
	}
	h := a.History("t1")
	if len(h) != 3 {
		t.Fatalf("history length = %d, want 3 after trimming", len(h))
	}
	if h[0].PID != plan.Victims[2].PID {
		t.Fatalf("oldest kept decision pid = %d, want %d", h[0].PID, plan.Victims[2].PID)
	}
	if h[2].PID != plan.Victims[4].PID {
		t.Fatalf("newest decision pid = %d, want %d", h[2].PID, plan.Victims[4].PID)
	}
}

func TestHistoryIsolatedPerTenant(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 91, Tenant: "alpha", RSS: 10, Weight: 90},
		impl.Proc{PID: 92, Tenant: "alpha", RSS: 10, Weight: 10},
		impl.Proc{PID: 93, Tenant: "beta", RSS: 10, Weight: 80},
		impl.Proc{PID: 94, Tenant: "beta", RSS: 10, Weight: 20},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 10, Need: 10, At: 800})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := len(a.History("alpha")); got != 1 {
		t.Fatalf("alpha history = %d, want 1", got)
	}
	if got := len(a.History("beta")); got != 0 {
		t.Fatalf("beta history = %d, want 0", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a,
		impl.Proc{PID: 101, Tenant: "t1", RSS: 10, Weight: 90},
		impl.Proc{PID: 102, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(impl.PressureEvent{Window: 11, Need: 10, At: 900})
	if err != nil {
		t.Fatalf("pressure event: %v", err)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatalf("commit: %v", err)
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot payload must not be empty")
	}
	restored := impl.NewOomArbiter(arbiterOptions())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(restored.History("t1")); got != 1 {
		t.Fatalf("restored history length = %d, want 1", got)
	}
	if _, ok := restored.Proc(101); !ok {
		t.Fatalf("restored registry lost pid 101")
	}
}

func TestRestoreRejectsCorruptSnapshotKeepsRegistry(t *testing.T) {
	a := impl.NewOomArbiter(arbiterOptions())
	registerBatch(t, a, impl.Proc{PID: 111, Tenant: "t1", RSS: 10, Weight: 5})
	if err := a.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	got, ok := a.Proc(111)
	if !ok {
		t.Fatalf("rejected restore must keep the live registry")
	}
	if got.Tenant != "t1" {
		t.Fatalf("live process tenant = %q, want t1", got.Tenant)
	}
}
