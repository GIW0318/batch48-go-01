package task

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	impl "gsb/batch48-go-safety-b-01"
)

func extraLadder() impl.Plan {
	return impl.Plan{
		Stages: []impl.StageSpec{
			{Name: "add-groups", Kind: impl.StageGroups, Groups: []int{44, 27}},
			{Name: "drop-gid", Kind: impl.StageDropGID, Value: 931, DependsOn: []string{"add-groups"}, Irreversible: true},
			{Name: "drop-uid", Kind: impl.StageDropUID, Value: 4040, DependsOn: []string{"drop-gid"}, Irreversible: true},
			{Name: "no-new-privs", Kind: impl.StageNoNewPrivs, DependsOn: []string{"drop-uid"}, Irreversible: true},
		},
	}
}

func extraMustGuard(t *testing.T, plan impl.Plan) *impl.PrivilegeStageGuard {
	t.Helper()
	g, err := impl.NewPrivilegeStageGuard(plan)
	if err != nil {
		t.Fatalf("NewPrivilegeStageGuard: %v", err)
	}
	if g == nil {
		t.Fatal("NewPrivilegeStageGuard returned a nil guard")
	}
	return g
}

func extraRunAll(t *testing.T, g *impl.PrivilegeStageGuard) {
	t.Helper()
	for _, stage := range []string{"add-groups", "drop-gid", "drop-uid", "no-new-privs"} {
		if _, err := g.Run(stage); err != nil {
			t.Fatalf("Run(%s): %v", stage, err)
		}
	}
}

func TestExtraConcurrentRunSameStageAppliesOnce(t *testing.T) {
	g := extraMustGuard(t, extraLadder())

	const workers = 64
	var applied atomic.Int32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := g.Run("add-groups")
			if err != nil {
				errs <- err
				return
			}
			if result.Applied {
				applied.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if got := applied.Load(); got != 1 {
		t.Fatalf("applied count = %d, want 1", got)
	}
	if journal := g.Journal(); len(journal) != 1 {
		t.Fatalf("journal length = %d, want 1", len(journal))
	}
	if cur := g.Current(); len(cur.Groups) != 2 || cur.Groups[0] != 27 || cur.Groups[1] != 44 {
		t.Fatalf("groups = %v, want [27 44]", cur.Groups)
	}
}

func TestExtraConcurrentVerifyAndRunStayConsistent(t *testing.T) {
	g := extraMustGuard(t, extraLadder())

	for _, stage := range []string{"add-groups", "drop-gid", "drop-uid", "no-new-privs"} {
		const verifiers = 32
		errs := make(chan error, verifiers)
		var wg sync.WaitGroup
		for i := 0; i < verifiers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := g.Verify(stage)
				if err != nil && !errors.Is(err, impl.ErrStagePending) {
					errs <- err
				}
				_ = g.Current()
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Run(stage); err != nil {
				errs <- err
			}
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
	}

	if err := g.Verify("no-new-privs"); err != nil {
		t.Fatalf("Verify after concurrent run: %v", err)
	}
	journal := g.Journal()
	if len(journal) != 4 {
		t.Fatalf("journal length = %d, want 4", len(journal))
	}
	for i := 1; i < len(journal); i++ {
		if journal[i].Sequence <= journal[i-1].Sequence {
			t.Fatalf("journal sequence is not increasing: %+v", journal)
		}
	}
}

func TestExtraSnapshotResumeOnFreshGuard(t *testing.T) {
	original := extraMustGuard(t, extraLadder())
	for _, stage := range []string{"add-groups", "drop-gid"} {
		if _, err := original.Run(stage); err != nil {
			t.Fatalf("Run(%s): %v", stage, err)
		}
	}

	snapshot, err := original.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restarted := extraMustGuard(t, extraLadder())
	if err := restarted.Restore(snapshot); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := restarted.Restore(snapshot); err != nil {
		t.Fatalf("second Restore: %v", err)
	}

	if err := restarted.Verify("drop-gid"); err != nil {
		t.Fatalf("Verify restored stage: %v", err)
	}
	if got, want := restarted.Completed(), []string{"add-groups", "drop-gid"}; !extraEqualStrings(got, want) {
		t.Fatalf("Completed = %v, want %v", got, want)
	}
	if got, want := restarted.Pending(), []string{"drop-uid", "no-new-privs"}; !extraEqualStrings(got, want) {
		t.Fatalf("Pending = %v, want %v", got, want)
	}
	if cur := restarted.Current(); cur.EGID != 931 || cur.EUID != 0 || len(cur.Groups) != 2 {
		t.Fatalf("restored Current = %+v", cur)
	}

	for _, stage := range []string{"drop-uid", "no-new-privs"} {
		if _, err := restarted.Run(stage); err != nil {
			t.Fatalf("Run(%s) after restart: %v", stage, err)
		}
	}
	journal := restarted.Journal()
	if len(journal) != 4 {
		t.Fatalf("journal length after resume = %d, want 4", len(journal))
	}
	for i := 1; i < len(journal); i++ {
		if journal[i].Sequence <= journal[i-1].Sequence {
			t.Fatalf("journal sequence is not increasing: %+v", journal)
		}
	}
}

func TestExtraRollbackRejectedWithoutRestoringCapabilities(t *testing.T) {
	g := extraMustGuard(t, extraLadder())
	extraRunAll(t, g)

	before := g.Current()
	beforeJournal := g.Journal()
	if _, err := g.Run("add-groups"); !errors.Is(err, impl.ErrOutOfOrder) {
		t.Fatalf("rollback error = %v, want ErrOutOfOrder", err)
	}
	after := g.Current()
	if after.EUID != before.EUID || after.EGID != before.EGID || after.CapMask != 0 || !after.NoNewPrivs {
		t.Fatalf("rollback changed credentials: before %+v, after %+v", before, after)
	}
	if len(g.Journal()) != len(beforeJournal) {
		t.Fatal("rollback appended a journal entry")
	}
}

func TestExtraFailureKeepsLevelAndSurvivesRestart(t *testing.T) {
	plan := extraLadder()
	plan.Probe = func(stage string) impl.Credentials {
		switch stage {
		case "add-groups":
			return impl.Credentials{Groups: []int{27, 44}}
		case "drop-gid":
			return impl.Credentials{EGID: 931, Groups: []int{27, 44}}
		case "drop-uid":
			return impl.Credentials{EUID: 9999, EGID: 931, Groups: []int{27, 44}}
		default:
			return impl.Credentials{EUID: 4040, EGID: 931, Groups: []int{27, 44}, NoNewPrivs: true}
		}
	}

	g := extraMustGuard(t, plan)
	for _, stage := range []string{"add-groups", "drop-gid"} {
		if _, err := g.Run(stage); err != nil {
			t.Fatalf("Run(%s): %v", stage, err)
		}
	}
	before := g.Current()
	if _, err := g.Run("drop-uid"); !errors.Is(err, impl.ErrReadbackMismatch) {
		t.Fatalf("Run(drop-uid) error = %v, want ErrReadbackMismatch", err)
	}
	if cur := g.Current(); cur.EUID != before.EUID || cur.EGID != before.EGID || len(cur.Groups) != 2 {
		t.Fatalf("failed stage advanced the committed level: before %+v, after %+v", before, cur)
	}
	if got, want := g.Completed(), []string{"add-groups", "drop-gid"}; !extraEqualStrings(got, want) {
		t.Fatalf("Completed = %v, want %v", got, want)
	}
	if _, err := g.Run("no-new-privs"); !errors.Is(err, impl.ErrOutOfOrder) {
		t.Fatalf("later stage error = %v, want ErrOutOfOrder", err)
	}
	verifyErr := g.Verify("drop-uid")
	if !errors.Is(verifyErr, impl.ErrStagePending) {
		t.Fatalf("Verify(drop-uid) error = %v, want ErrStagePending", verifyErr)
	}
	if !strings.Contains(verifyErr.Error(), "euid") {
		t.Fatalf("Verify(drop-uid) error lacks field detail: %v", verifyErr)
	}

	snapshot, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restarted := extraMustGuard(t, extraLadder())
	if err := restarted.Restore(snapshot); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if cur, restored := restarted.Current(), g.Current(); cur.EUID != restored.EUID || cur.EGID != restored.EGID {
		t.Fatalf("restored Current = %+v, want %+v", cur, restored)
	}
	if got, want := restarted.Completed(), []string{"add-groups", "drop-gid"}; !extraEqualStrings(got, want) {
		t.Fatalf("restored Completed = %v, want %v", got, want)
	}
	if got, want := restarted.Pending(), []string{"drop-uid", "no-new-privs"}; !extraEqualStrings(got, want) {
		t.Fatalf("restored Pending = %v, want %v", got, want)
	}
	if _, err := restarted.Run("drop-uid"); err != nil {
		t.Fatalf("Run(drop-uid) after restart: %v", err)
	}
	if _, err := restarted.Run("no-new-privs"); err != nil {
		t.Fatalf("Run(no-new-privs) after restart: %v", err)
	}
	if cur := restarted.Current(); cur.EUID != 4040 || cur.CapMask != 0 || !cur.NoNewPrivs {
		t.Fatalf("Current after resumed ladder = %+v", cur)
	}
}

func TestExtraCurrentUsesObservedReadback(t *testing.T) {
	plan := extraLadder()
	plan.Probe = func(stage string) impl.Credentials {
		return impl.Credentials{RealUID: 77, RealGID: 88, EUID: 0, EGID: 0, Groups: []int{27, 44}}
	}
	g := extraMustGuard(t, plan)
	result, err := g.Run("add-groups")
	if err != nil {
		t.Fatalf("Run(add-groups): %v", err)
	}
	if result.Credentials.RealUID != 0 || result.Readback.RealUID != 77 {
		t.Fatalf("result planned/readback = %+v/%+v", result.Credentials, result.Readback)
	}
	if cur := g.Current(); cur.RealUID != 77 || cur.RealGID != 88 {
		t.Fatalf("Current = %+v, want observed readback", cur)
	}
}

func extraEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
