package task

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func selfCheckOpts() Options {
	return Options{
		MaxProcs:            64,
		MaxHistoryPerTenant: 64,
		ProtectWindow:       100,
		MaxReclaimPerWindow: 1 << 20,
	}
}

func selfCheckRegister(t *testing.T, a *OomArbiter, procs ...Proc) {
	t.Helper()
	for _, p := range procs {
		if err := a.Register(p); err != nil {
			t.Fatalf("register pid %d: %v", p.PID, err)
		}
	}
}

// Same-window Pressure and Commit hammered concurrently: planning is
// deterministic and identical plans must commit exactly once, leaving exactly
// one plan worth of ledger entries. Run with -race.
func TestSelfCheckConcurrentSameWindowPressureCommit(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 30},
		Proc{PID: 2, Tenant: "t1", RSS: 10, Weight: 20},
		Proc{PID: 3, Tenant: "t1", RSS: 10, Weight: 10},
	)

	const goroutines = 48
	var wg sync.WaitGroup
	var commitsOK int64
	var committed int64
	var stale int64
	var unexpected int64

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			plan, err := a.Pressure(PressureEvent{Window: 77, Need: 30, At: 1000})
			if err != nil {
				atomic.AddInt64(&unexpected, 1)
				return
			}
			if plan.Reclaimed+plan.Shortfall != 30 {
				t.Errorf("invariant: reclaimed %d + shortfall %d != need 30",
					plan.Reclaimed, plan.Shortfall)
			}
			err = a.Commit(plan)
			switch {
			case err == nil:
				atomic.AddInt64(&commitsOK, 1)
			case errors.Is(err, ErrPlanCommitted):
				atomic.AddInt64(&committed, 1)
			case errors.Is(err, ErrStalePlan):
				atomic.AddInt64(&stale, 1)
			default:
				atomic.AddInt64(&unexpected, 1)
				t.Errorf("unexpected commit error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("unexpected errors: %d", unexpected)
	}
	if commitsOK != 1 {
		t.Fatalf("successful commits = %d, want exactly 1", commitsOK)
	}
	if commitsOK+committed+stale != goroutines {
		t.Fatalf("accounted outcomes %d want %d", commitsOK+committed+stale, goroutines)
	}
	if stale != 0 {
		t.Fatalf("same-window commits must not be stale, got %d", stale)
	}

	history := a.History("t1")
	if len(history) != 3 {
		t.Fatalf("history len = %d, want 3 (single plan)", len(history))
	}
	kills := 0
	for _, d := range history {
		if d.Action == ActionKill && d.Window == 77 {
			kills++
		}
	}
	if kills != 3 {
		t.Fatalf("kill decisions = %d, want 3", kills)
	}
}

// Snapshot immediately after commit, restore into a fresh arbiter, replay the
// same plan (must not double count) and then prove a new window still works.
func TestSelfCheckSnapshotRestoreRestartNoDoubleCount(t *testing.T) {
	opts := selfCheckOpts()
	a := NewOomArbiter(opts)
	selfCheckRegister(t, a,
		Proc{PID: 101, Tenant: "t1", RSS: 10, Weight: 90},
		Proc{PID: 102, Tenant: "t1", RSS: 10, Weight: 10},
	)
	plan, err := a.Pressure(PressureEvent{Window: 11, Need: 10, At: 900})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatal(err)
	}

	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	b := NewOomArbiter(opts)
	if err := b.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(b.History("t1")); got != 1 {
		t.Fatalf("restored history = %d, want 1", got)
	}
	if _, ok := b.Proc(101); !ok {
		t.Fatal("restored registry lost pid 101")
	}

	if err := b.Commit(plan); !errors.Is(err, ErrPlanCommitted) {
		t.Fatalf("replay after restart err = %v, want ErrPlanCommitted", err)
	}
	if got := len(b.History("t1")); got != 1 {
		t.Fatalf("history after replay = %d, still want 1", got)
	}
	kills := 0
	for _, d := range b.History("t1") {
		if d.Action == ActionKill {
			kills++
		}
	}
	if kills != 1 {
		t.Fatalf("kills after restart/replay = %d, want 1", kills)
	}

	next, err := b.Pressure(PressureEvent{Window: 12, Need: 10, At: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(next); err != nil {
		t.Fatalf("new window after restore: %v", err)
	}
	if got := len(b.History("t1")); got != 2 {
		t.Fatalf("history after new window = %d, want 2", got)
	}
}

// A pinned high-weight process is skipped while the pin is live and becomes
// selectable again once event time passes ProtectWindow.
func TestSelfCheckProtectionExpiryNextRound(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 100, Weight: 90},
		Proc{PID: 2, Tenant: "t1", RSS: 100, Weight: 10},
	)
	if err := a.Protect("t1", []int{1}); err != nil {
		t.Fatal(err)
	}

	protected, err := a.Pressure(PressureEvent{Window: 1, Need: 100, At: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(protected.Victims) != 1 || protected.Victims[0].PID != 2 {
		t.Fatalf("protected plan victims = %+v, want only pid 2", protected.Victims)
	}
	if !protected.Victims[0].Kill {
		t.Fatal("pid 2 should be fully reclaimed/killed")
	}
	if err := a.Commit(protected); err != nil {
		t.Fatal(err)
	}
	var sawProtectedSkip bool
	for _, d := range a.History("t1") {
		if d.PID == 1 && d.Action == ActionSkip && d.Reason == ReasonProtected {
			sawProtectedSkip = true
		}
	}
	if !sawProtectedSkip {
		t.Fatalf("history %+v missing protected skip for pid 1", a.History("t1"))
	}

	// At = 200 is beyond the armed expiry (10 + 100); next round restores it.
	expired, err := a.Pressure(PressureEvent{Window: 2, Need: 100, At: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(expired.Victims) == 0 || expired.Victims[0].PID != 1 {
		t.Fatalf("after expiry victims = %+v, want pid 1 first", expired.Victims)
	}
}

// Protection must survive a snapshot/restore cycle and still age correctly.
func TestSelfCheckProtectionSnapshotPersists(t *testing.T) {
	opts := selfCheckOpts()
	a := NewOomArbiter(opts)
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 100, Weight: 90},
		Proc{PID: 2, Tenant: "t1", RSS: 100, Weight: 10},
	)
	if err := a.Protect("t1", []int{1}); err != nil {
		t.Fatal(err)
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	b := NewOomArbiter(opts)
	if err := b.Restore(snap); err != nil {
		t.Fatal(err)
	}
	plan, err := b.Pressure(PressureEvent{Window: 1, Need: 50, At: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Victims) != 1 || plan.Victims[0].PID != 2 {
		t.Fatalf("restored protection plan = %+v, want pid 2 only", plan.Victims)
	}
}

// Equal weights break by RSS desc then pid asc, deterministically.
func TestSelfCheckTieBreakRSSThenPID(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 3, Tenant: "t1", RSS: 50, Weight: 5},
		Proc{PID: 1, Tenant: "t1", RSS: 100, Weight: 5},
		Proc{PID: 2, Tenant: "t1", RSS: 100, Weight: 5},
		Proc{PID: 4, Tenant: "t1", RSS: 50, Weight: 5},
	)
	want := []int{1, 2, 3, 4}
	for round := 0; round < 3; round++ {
		plan, err := a.Pressure(PressureEvent{
			Window: int64(round + 1), Need: 400, At: int64(round) * 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Victims) != 4 {
			t.Fatalf("round %d victims = %d, want 4", round, len(plan.Victims))
		}
		for i, pid := range want {
			if plan.Victims[i].PID != pid {
				t.Fatalf("round %d position %d pid = %d, want %d (full: %+v)",
					round, i, plan.Victims[i].PID, pid, plan.Victims)
			}
		}
	}
}

// A plan never fills a shortfall from another tenant.
func TestSelfCheckNoCrossTenantBorrow(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "alpha", RSS: 10, Weight: 90},
		Proc{PID: 3, Tenant: "alpha", RSS: 10, Weight: 1},
		Proc{PID: 2, Tenant: "beta", RSS: 1000, Weight: 80},
	)
	plan, err := a.Pressure(PressureEvent{Window: 1, Need: 100, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reclaimed != 20 || plan.Shortfall != 80 {
		t.Fatalf("reclaimed %d shortfall %d, want 20/80", plan.Reclaimed, plan.Shortfall)
	}
	for _, v := range plan.Victims {
		if v.Tenant != "alpha" {
			t.Fatalf("borrowed foreign tenant victim %+v", v)
		}
	}
	if len(plan.Victims) != 2 {
		t.Fatalf("victims = %d, want both alpha procs only", len(plan.Victims))
	}
}

// The sole replica is never killed and the event only reports shortfall.
func TestSelfCheckLastReplicaNotKillable(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "solo", RSS: 100, Weight: 90},
		Proc{PID: 2, Tenant: "other", RSS: 100, Weight: 80},
	)
	plan, err := a.Pressure(PressureEvent{Window: 1, Need: 1000, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Killable {
		t.Fatal("sole-replica plan must not be killable")
	}
	if len(plan.Victims) != 0 {
		t.Fatalf("sole replica must not be reclaimed, victims=%+v", plan.Victims)
	}
	if plan.Reclaimed != 0 || plan.Shortfall != 1000 {
		t.Fatalf("got reclaimed %d shortfall %d, want 0/1000",
			plan.Reclaimed, plan.Shortfall)
	}
	if err := a.Commit(plan); err != nil {
		t.Fatal(err)
	}
	h := a.History("solo")
	if len(h) != 1 || h[0].Action != ActionSkip || h[0].Reason != ReasonLastReplica {
		t.Fatalf("solo history = %+v, want one last_replica skip", h)
	}
	if got := len(a.History("other")); got != 0 {
		t.Fatalf("other tenant history = %d, want 0", got)
	}
}

// Same window + pid kill is counted once; a revised plan only folds bytes.
func TestSelfCheckWindowPIDKillDedup(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 100, Weight: 90},
		Proc{PID: 2, Tenant: "t1", RSS: 100, Weight: 10},
	)
	first, err := a.Pressure(PressureEvent{Window: 9, Need: 100, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Commit(first); err != nil {
		t.Fatal(err)
	}

	// A distinct (revised) plan for the same latest window references pid 1
	// as a kill again and adds a partial reclaim of pid 2.
	revised := Plan{
		Window: 9,
		Victims: []Victim{
			{PID: 1, Tenant: "t1", Bytes: 100, Kill: true},
			{PID: 2, Tenant: "t1", Bytes: 40, Kill: false},
		},
		Reclaimed: 140,
	}
	if err := a.Commit(revised); err != nil {
		t.Fatalf("revised same-window plan: %v", err)
	}

	var killCount int
	var sawDedup bool
	for _, d := range a.History("t1") {
		if d.Window == 9 && d.PID == 1 && d.Action == ActionKill {
			killCount++
		}
		if d.Window == 9 && d.PID == 1 && d.Reason == ReasonWindowDedup {
			sawDedup = true
		}
	}
	if killCount != 1 {
		t.Fatalf("kill count for window9 pid1 = %d, want 1", killCount)
	}
	if !sawDedup {
		t.Fatalf("history %+v missing window_dedup entry", a.History("t1"))
	}
}

// MaxReclaimPerWindow is a hard ceiling that turns the remainder into shortfall.
func TestSelfCheckMaxReclaimPerWindowCap(t *testing.T) {
	opts := selfCheckOpts()
	opts.MaxReclaimPerWindow = 25
	a := NewOomArbiter(opts)
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 100, Weight: 90},
		Proc{PID: 2, Tenant: "t1", RSS: 100, Weight: 10},
	)
	plan, err := a.Pressure(PressureEvent{Window: 1, Need: 100, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reclaimed != 25 || plan.Shortfall != 75 {
		t.Fatalf("reclaimed %d shortfall %d, want 25/75",
			plan.Reclaimed, plan.Shortfall)
	}
	if len(plan.Victims) != 1 || plan.Victims[0].Bytes != 25 || plan.Victims[0].Kill {
		t.Fatalf("capped victims = %+v, want one partial 25-byte victim", plan.Victims)
	}
}

// Every plan honours Reclaimed + Shortfall == Need and byte/kill consistency.
func TestSelfCheckPlanInvariantsAcrossNeeds(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 90},
		Proc{PID: 2, Tenant: "t1", RSS: 20, Weight: 50},
		Proc{PID: 3, Tenant: "t1", RSS: 30, Weight: 10},
	)
	for need := uint64(1); need <= 80; need++ {
		plan, err := a.Pressure(PressureEvent{
			Window: int64(need), Need: need, At: int64(need),
		})
		if err != nil {
			t.Fatalf("need %d: %v", need, err)
		}
		if plan.Reclaimed+plan.Shortfall != need {
			t.Fatalf("need %d: reclaimed %d + shortfall %d != need",
				need, plan.Reclaimed, plan.Shortfall)
		}
		var victimBytes uint64
		for _, v := range plan.Victims {
			victimBytes += v.Bytes
			if v.Kill && v.Bytes == 0 {
				t.Fatalf("need %d: kill victim with zero bytes", need)
			}
		}
		if plan.Reclaimed != victimBytes {
			t.Fatalf("need %d: reclaimed %d != victim bytes %d",
				need, plan.Reclaimed, victimBytes)
		}
		if plan.Reclaimed > need {
			t.Fatalf("need %d: reclaimed %d exceeds need", need, plan.Reclaimed)
		}
	}
}

// Invalid events leave the current event marker untouched.
func TestSelfCheckBadEventDoesNotMoveWindow(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a, Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 1})
	good, err := a.Pressure(PressureEvent{Window: 5, Need: 10, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pressure(PressureEvent{Window: 0, Need: 10}); !errors.Is(err, ErrBadEvent) {
		t.Fatalf("zero window err = %v want ErrBadEvent", err)
	}
	if _, err := a.Pressure(PressureEvent{Window: 9, Need: 0}); !errors.Is(err, ErrBadEvent) {
		t.Fatalf("zero need err = %v want ErrBadEvent", err)
	}
	if err := a.Commit(good); err != nil {
		t.Fatalf("plan from window 5 should still commit: %v", err)
	}
}

// Protect argument validation and Tenants listing.
func TestSelfCheckProtectValidationAndTenants(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a, Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 1})

	if err := a.Protect("  ", []int{1}); !errors.Is(err, ErrBadTenant) {
		t.Fatalf("blank tenant err = %v want ErrBadTenant", err)
	}
	if err := a.Protect("t1", []int{404}); !errors.Is(err, ErrUnknownPID) {
		t.Fatalf("unknown pid err = %v want ErrUnknownPID", err)
	}
	if err := a.Protect("other", []int{1}); !errors.Is(err, ErrUnknownPID) {
		t.Fatalf("foreign pid err = %v want ErrUnknownPID", err)
	}
	if got := fmt.Sprint(a.Tenants()); got != "[t1]" {
		t.Fatalf("tenants = %q want [t1]", got)
	}
	if err := a.Unprotect("t1", []int{1}); err != nil {
		t.Fatalf("unprotect: %v", err)
	}
}

// Corrupt or unsupported snapshots must fail and leave live state intact.
func TestSelfCheckBadSnapshotsKeepState(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a, Proc{PID: 111, Tenant: "t1", RSS: 10, Weight: 5})

	bad := [][]byte{
		nil,
		{},
		[]byte("not-a-snapshot"),
		[]byte(`{"version":999}`),
		[]byte(`{"version":1,"procs":[{"pid":0,"tenant":"t1","rss":1}]}`),
		[]byte(`{"version":1,"procs":[{"pid":2,"tenant":"","rss":1}]}`),
	}
	for i, payload := range bad {
		if err := a.Restore(payload); !errors.Is(err, ErrBadSnapshot) {
			t.Fatalf("payload %d: err = %v want ErrBadSnapshot", i, err)
		}
		if _, ok := a.Proc(111); !ok {
			t.Fatalf("payload %d: live registry damaged", i)
		}
	}
}

// Zero-value options stay usable (limits disabled rather than pathological).
func TestSelfCheckZeroOptionsUsable(t *testing.T) {
	a := NewOomArbiter(Options{})
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t", RSS: 10, Weight: 2},
		Proc{PID: 2, Tenant: "t", RSS: 10, Weight: 1},
	)
	if _, err := a.Pressure(PressureEvent{Window: 1, Need: 5, At: 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.Register(Proc{PID: 3, Tenant: "t", RSS: 10, Weight: 0}); err != nil {
		t.Fatalf("register with zero options: %v", err)
	}
	plan, err := a.Pressure(PressureEvent{Window: 1, Need: 5, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reclaimed != 5 || plan.Shortfall != 0 {
		t.Fatalf("zero options plan = %+v", plan)
	}
}

// Pressure never records history no matter how often it is repeated.
func TestSelfCheckRepeatedPressureNoLedgerGrowth(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a, Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 1})
	for i := 0; i < 5; i++ {
		if _, err := a.Pressure(PressureEvent{Window: 1, Need: 10, At: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(a.History("t1")); got != 0 {
		t.Fatalf("history after repeated pressure = %d, want 0", got)
	}
}

// Snapshot/Restore and History stay race-free while planning/commit happen.
func TestSelfCheckConcurrentSnapshotsAndHistory(t *testing.T) {
	a := NewOomArbiter(selfCheckOpts())
	selfCheckRegister(t, a,
		Proc{PID: 1, Tenant: "t1", RSS: 10, Weight: 40},
		Proc{PID: 2, Tenant: "t1", RSS: 10, Weight: 30},
		Proc{PID: 3, Tenant: "t1", RSS: 10, Weight: 20},
		Proc{PID: 4, Tenant: "t2", RSS: 10, Weight: 5},
	)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				plan, err := a.Pressure(PressureEvent{
					Window: 5, Need: 20, At: int64(i),
				})
				if err != nil {
					t.Errorf("pressure: %v", err)
					return
				}
				_ = a.Commit(plan)
			}
		}()
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				snap, err := a.Snapshot()
				if err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
				restored := NewOomArbiter(selfCheckOpts())
				if err := restored.Restore(snap); err != nil {
					t.Errorf("concurrent snapshot restore: %v", err)
					return
				}
				_ = restored.History("t1")
				_ = a.History("t1")
			}
		}()
	}
	wg.Wait()

	if got := len(a.History("t1")); got != 2 {
		t.Fatalf("final t1 history = %d, want 2 from single winning plan", got)
	}
}
