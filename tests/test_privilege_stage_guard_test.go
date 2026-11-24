package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-b-01"
)

func ladder() impl.Plan {
	return impl.Plan{
		Stages: []impl.StageSpec{
			{Name: "add-groups", Kind: impl.StageGroups, Groups: []int{44, 27}},
			{Name: "drop-gid", Kind: impl.StageDropGID, Value: 931, DependsOn: []string{"add-groups"}, Irreversible: true},
			{Name: "drop-uid", Kind: impl.StageDropUID, Value: 4040, DependsOn: []string{"drop-gid"}, Irreversible: true},
			{Name: "no-new-privs", Kind: impl.StageNoNewPrivs, DependsOn: []string{"drop-uid"}, Irreversible: true},
		},
	}
}

func mustGuard(t *testing.T, plan impl.Plan) *impl.PrivilegeStageGuard {
	t.Helper()
	g, err := impl.NewPrivilegeStageGuard(plan)
	if err != nil {
		t.Fatalf("NewPrivilegeStageGuard: unexpected error: %v", err)
	}
	if g == nil {
		t.Fatal("NewPrivilegeStageGuard returned a nil guard")
	}
	return g
}

func runAll(t *testing.T, g *impl.PrivilegeStageGuard) {
	t.Helper()
	for _, name := range []string{"add-groups", "drop-gid", "drop-uid", "no-new-privs"} {
		if _, err := g.Run(name); err != nil {
			t.Fatalf("Run(%s): unexpected error: %v", name, err)
		}
	}
}

func TestNewRejectsEmptyLadder(t *testing.T) {
	g, err := impl.NewPrivilegeStageGuard(impl.Plan{})
	if err == nil {
		t.Fatalf("empty ladder must be rejected, got guard %v", g)
	}
	if !errors.Is(err, impl.ErrPlanInvalid) {
		t.Fatalf("empty ladder error = %v, want ErrPlanInvalid", err)
	}
}

func TestNewRejectsDuplicateStageNames(t *testing.T) {
	plan := ladder()
	plan.Stages[2].Name = "drop-gid"
	if _, err := impl.NewPrivilegeStageGuard(plan); !errors.Is(err, impl.ErrPlanInvalid) {
		t.Fatalf("duplicate stage name error = %v, want ErrPlanInvalid", err)
	}
}

func TestNewRejectsDanglingAndForwardDependencies(t *testing.T) {
	missing := ladder()
	missing.Stages[2].DependsOn = []string{"does-not-exist"}
	if _, err := impl.NewPrivilegeStageGuard(missing); !errors.Is(err, impl.ErrPlanInvalid) {
		t.Fatalf("dangling dependency error = %v, want ErrPlanInvalid", err)
	}

	forward := ladder()
	forward.Stages[0].DependsOn = []string{"drop-uid"}
	if _, err := impl.NewPrivilegeStageGuard(forward); !errors.Is(err, impl.ErrPlanInvalid) {
		t.Fatalf("forward dependency error = %v, want ErrPlanInvalid", err)
	}
}

func TestNewRejectsWrongKindOrderAndIrreversibleFlags(t *testing.T) {
	swapped := ladder()
	swapped.Stages[0], swapped.Stages[1] = swapped.Stages[1], swapped.Stages[0]
	if _, err := impl.NewPrivilegeStageGuard(swapped); !errors.Is(err, impl.ErrPlanInvalid) {
		t.Fatalf("gid before groups error = %v, want ErrPlanInvalid", err)
	}

	flagless := ladder()
	flagless.Stages[1].Irreversible = false
	if _, err := impl.NewPrivilegeStageGuard(flagless); !errors.Is(err, impl.ErrPlanInvalid) {
		t.Fatalf("missing irreversible flag error = %v, want ErrPlanInvalid", err)
	}
}

func TestNewAcceptsCanonicalLadder(t *testing.T) {
	g := mustGuard(t, ladder())
	pending := g.Pending()
	if len(pending) != 4 {
		t.Fatalf("Pending length = %d, want 4", len(pending))
	}
	want := []string{"add-groups", "drop-gid", "drop-uid", "no-new-privs"}
	for i := range want {
		if pending[i] != want[i] {
			t.Fatalf("Pending[%d] = %q, want %q", i, pending[i], want[i])
		}
	}
	cur := g.Current()
	if cur.EUID != 0 || cur.EGID != 0 || len(cur.Groups) != 0 || cur.NoNewPrivs {
		t.Fatalf("fresh guard credentials = %+v, want root before any stage", cur)
	}
}

func TestRunGroupsStageReadsBackSortedGroups(t *testing.T) {
	g := mustGuard(t, ladder())
	res, err := g.Run("add-groups")
	if err != nil {
		t.Fatalf("Run(add-groups): unexpected error: %v", err)
	}
	if !res.Applied {
		t.Fatal("add-groups result reported Applied=false")
	}
	cur := g.Current()
	if len(cur.Groups) != 2 || cur.Groups[0] != 27 || cur.Groups[1] != 44 {
		t.Fatalf("current groups = %v, want [27 44]", cur.Groups)
	}
	if cur.EUID != 0 {
		t.Fatalf("current euid = %d, want 0 before the uid drop", cur.EUID)
	}
	if len(res.Readback.Groups) != 2 || res.Readback.Groups[1] != 44 {
		t.Fatalf("readback groups = %v, want [27 44]", res.Readback.Groups)
	}
}

func TestRunGIDStageKeepsSupplementaryGroups(t *testing.T) {
	g := mustGuard(t, ladder())
	if _, err := g.Run("add-groups"); err != nil {
		t.Fatalf("Run(add-groups): %v", err)
	}
	res, err := g.Run("drop-gid")
	if err != nil {
		t.Fatalf("Run(drop-gid): %v", err)
	}
	if res.Credentials.EGID != 931 {
		t.Fatalf("result egid = %d, want 931", res.Credentials.EGID)
	}
	cur := g.Current()
	if cur.EGID != 931 {
		t.Fatalf("current egid = %d, want 931", cur.EGID)
	}
	if len(cur.Groups) != 2 || cur.Groups[0] != 27 || cur.Groups[1] != 44 {
		t.Fatalf("groups after gid drop = %v, want [27 44]", cur.Groups)
	}
}

func TestRunUIDStageClearsCapabilityMask(t *testing.T) {
	g := mustGuard(t, ladder())
	for _, name := range []string{"add-groups", "drop-gid", "drop-uid"} {
		if _, err := g.Run(name); err != nil {
			t.Fatalf("Run(%s): %v", name, err)
		}
	}
	cur := g.Current()
	if cur.EUID != 4040 {
		t.Fatalf("current euid = %d, want 4040", cur.EUID)
	}
	if cur.EGID != 931 {
		t.Fatalf("current egid = %d, want 931", cur.EGID)
	}
	if cur.CapMask != 0 {
		t.Fatalf("capability mask after uid drop = %#x, want 0", cur.CapMask)
	}
}

func TestRunNoNewPrivsStageAndJournal(t *testing.T) {
	g := mustGuard(t, ladder())
	runAll(t, g)
	if !g.Current().NoNewPrivs {
		t.Fatal("NoNewPrivs not set after the last stage")
	}
	done := g.Completed()
	if len(done) != 4 || done[3] != "no-new-privs" {
		t.Fatalf("Completed() = %v, want four stages ending with no-new-privs", done)
	}
	if pending := g.Pending(); len(pending) != 0 {
		t.Fatalf("Pending() = %v, want empty", pending)
	}
	journal := g.Journal()
	if len(journal) != 4 {
		t.Fatalf("journal length = %d, want 4", len(journal))
	}
	if journal[0].Stage != "add-groups" || journal[3].Stage != "no-new-privs" {
		t.Fatalf("journal order = %q..%q, want add-groups..no-new-privs", journal[0].Stage, journal[3].Stage)
	}
	if journal[1].Sequence <= journal[0].Sequence {
		t.Fatalf("journal sequence not increasing: %d then %d", journal[0].Sequence, journal[1].Sequence)
	}
}

func TestRunRejectsUnknownAndOutOfOrderStages(t *testing.T) {
	g := mustGuard(t, ladder())
	if _, err := g.Run("drop-uid"); !errors.Is(err, impl.ErrOutOfOrder) {
		t.Fatalf("Run(drop-uid) before its dependency error = %v, want ErrOutOfOrder", err)
	}
	if cur := g.Current(); cur.EUID != 0 {
		t.Fatalf("euid after rejected stage = %d, want 0", cur.EUID)
	}
	if _, err := g.Run("not-a-stage"); !errors.Is(err, impl.ErrUnknownStage) {
		t.Fatalf("Run(not-a-stage) error = %v, want ErrUnknownStage", err)
	}
}

func TestRunReportsReadbackMismatch(t *testing.T) {
	plan := ladder()
	plan.Probe = func(stage string) impl.Credentials {
		switch stage {
		case "add-groups":
			return impl.Credentials{Groups: []int{27, 44}}
		case "drop-gid":
			return impl.Credentials{EGID: 931, Groups: []int{27, 44}}
		case "drop-uid":
			return impl.Credentials{EUID: 9999, EGID: 931, Groups: []int{27, 44}}
		default:
			return impl.Credentials{EUID: 4040, EGID: 931, NoNewPrivs: true}
		}
	}
	g := mustGuard(t, plan)
	for _, name := range []string{"add-groups", "drop-gid"} {
		if _, err := g.Run(name); err != nil {
			t.Fatalf("Run(%s): %v", name, err)
		}
	}
	if _, err := g.Run("drop-uid"); !errors.Is(err, impl.ErrReadbackMismatch) {
		t.Fatalf("Run(drop-uid) error = %v, want ErrReadbackMismatch", err)
	}
}

func TestVerifyChecksCompletedPendingAndUnknownStages(t *testing.T) {
	g := mustGuard(t, ladder())
	runAll(t, g)
	if err := g.Verify("drop-uid"); err != nil {
		t.Fatalf("Verify(drop-uid) = %v, want nil", err)
	}
	if err := g.Verify("add-groups"); err != nil {
		t.Fatalf("Verify(add-groups) = %v, want nil", err)
	}
	if err := g.Verify("no-such-stage"); !errors.Is(err, impl.ErrUnknownStage) {
		t.Fatalf("Verify(no-such-stage) = %v, want ErrUnknownStage", err)
	}

	fresh := mustGuard(t, ladder())
	if err := fresh.Verify("add-groups"); !errors.Is(err, impl.ErrStagePending) {
		t.Fatalf("Verify on a pending stage = %v, want ErrStagePending", err)
	}
}

func TestSnapshotRestoreKeepsCompletedLadder(t *testing.T) {
	g := mustGuard(t, ladder())
	runAll(t, g)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot(): %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("Snapshot() returned an empty payload")
	}

	restored := mustGuard(t, ladder())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("Restore(): %v", err)
	}
	if err := restored.Verify("no-new-privs"); err != nil {
		t.Fatalf("Verify after restore = %v, want nil", err)
	}
	if cur := restored.Current(); cur.EUID != 4040 || !cur.NoNewPrivs {
		t.Fatalf("restored credentials = %+v, want euid 4040 with NoNewPrivs", cur)
	}
	if pending := restored.Pending(); len(pending) != 0 {
		t.Fatalf("restored Pending() = %v, want empty", pending)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	g := mustGuard(t, ladder())
	if err := g.Restore([]byte("{ this is not a ladder snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot error = %v, want ErrBadSnapshot", err)
	}
	cur := g.Current()
	if cur.EUID != 0 || cur.NoNewPrivs {
		t.Fatalf("guard credentials changed by a rejected snapshot: %+v", cur)
	}
}

func TestConcurrentVerifySeesOneLadderState(t *testing.T) {
	g := mustGuard(t, ladder())
	runAll(t, g)

	const workers = 24
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Verify("no-new-privs"); err != nil {
				errs <- fmt.Errorf("concurrent Verify: %w", err)
				return
			}
			if cur := g.Current(); cur.EUID != 4040 || cur.EGID != 931 || !cur.NoNewPrivs {
				errs <- fmt.Errorf("concurrent Current = %+v", cur)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
