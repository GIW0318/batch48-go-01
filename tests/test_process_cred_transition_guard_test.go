package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-11"
)

func TestStepOrderIsFixed(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	got := g.StepOrder()
	want := []impl.Step{impl.StepGroups, impl.StepPrimaryGroup, impl.StepUser, impl.StepEnvCleanup, impl.StepNoNewPrivs}
	if len(got) != len(want) {
		t.Fatalf("step order has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d is %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPlanEmitsFixedStepsInOrder(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	if err := g.SetGroupWhitelist([]int{10, 20}); err != nil {
		t.Fatalf("registering whitelist failed: %v", err)
	}
	plan, err := g.Plan(impl.Target{Pid: 321, Threads: 1}, impl.Creds{Uid: 0, Gid: 10, Groups: []int{10, 20}})
	if err != nil {
		t.Fatalf("planning a plain transition failed: %v", err)
	}
	if len(plan.Steps) != 5 {
		t.Fatalf("plan has %d steps, want 5", len(plan.Steps))
	}
	want := []impl.Step{impl.StepGroups, impl.StepPrimaryGroup, impl.StepUser, impl.StepEnvCleanup, impl.StepNoNewPrivs}
	for i := range want {
		if plan.Steps[i] != want[i] {
			t.Fatalf("plan step %d is %q, want %q", i, plan.Steps[i], want[i])
		}
	}
	if !plan.Revertible {
		t.Fatalf("plan that keeps the same user must stay revertible")
	}
}

func TestPlanRejectsMultithreadedTarget(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	_, err := g.Plan(impl.Target{Pid: 322, Threads: 4}, impl.Creds{Uid: 0, Gid: 0, Groups: nil})
	if !errors.Is(err, impl.ErrThreadCount) {
		t.Fatalf("expected ErrThreadCount for a 4 thread target, got %v", err)
	}
}

func TestPlanRejectsGroupCountAboveQuota(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 2, MaxEnvVars: 16})
	_, err := g.Plan(impl.Target{Pid: 323, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: []int{1, 2, 3}})
	if !errors.Is(err, impl.ErrGroupQuota) {
		t.Fatalf("expected ErrGroupQuota for 3 groups with quota 2, got %v", err)
	}
}

func TestPlanRejectsGroupOutsideWhitelist(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	if err := g.SetGroupWhitelist([]int{10, 20}); err != nil {
		t.Fatalf("registering whitelist failed: %v", err)
	}
	_, err := g.Plan(impl.Target{Pid: 324, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: []int{10, 30}})
	if !errors.Is(err, impl.ErrGroupNotAllowed) {
		t.Fatalf("expected ErrGroupNotAllowed for group 30, got %v", err)
	}
}

func TestPlanMarksCrossUserPlanIrreversible(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 325, Threads: 1}, impl.Creds{Uid: 1000, Gid: 1000, Groups: []int{1000}})
	if err != nil {
		t.Fatalf("planning a user switch failed: %v", err)
	}
	if plan.Revertible {
		t.Fatalf("plan that switches user must be marked irreversible")
	}
	if len(plan.Steps) != 5 {
		t.Fatalf("irreversible plan still carries 5 steps, got %d", len(plan.Steps))
	}
}

func TestApplyRequiresApprovalForIrreversiblePlan(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 326, Threads: 1}, impl.Creds{Uid: 1000, Gid: 1000, Groups: []int{1000}})
	if err != nil {
		t.Fatalf("planning a user switch failed: %v", err)
	}
	if _, err := g.Apply(plan, false); !errors.Is(err, impl.ErrNonRevertible) {
		t.Fatalf("expected ErrNonRevertible without approval, got %v", err)
	}
	if state := g.Inspect(); len(state.Applied) != 0 {
		t.Fatalf("rejected apply must not record any step, got %d", len(state.Applied))
	}
}

func TestApplyRunsStepsInFixedOrder(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	if err := g.SetGroupWhitelist([]int{10, 20}); err != nil {
		t.Fatalf("registering whitelist failed: %v", err)
	}
	plan, err := g.Plan(impl.Target{Pid: 327, Threads: 1}, impl.Creds{Uid: 0, Gid: 10, Groups: []int{20, 10}})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	res, err := g.Apply(plan, false)
	if err != nil {
		t.Fatalf("applying a revertible plan failed: %v", err)
	}
	want := []impl.Step{impl.StepGroups, impl.StepPrimaryGroup, impl.StepUser, impl.StepEnvCleanup, impl.StepNoNewPrivs}
	if len(res.Steps) != 5 {
		t.Fatalf("apply reported %d steps, want 5", len(res.Steps))
	}
	for i := range want {
		if res.Steps[i] != want[i] {
			t.Fatalf("applied step %d is %q, want %q", i, res.Steps[i], want[i])
		}
	}
	if state := g.Inspect(); len(state.Applied) != 5 {
		t.Fatalf("state records %d applied steps, want 5", len(state.Applied))
	}
}

func TestApplyTwiceIsRejected(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 328, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: []int{7}})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if _, err := g.Apply(plan, false); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if _, err := g.Apply(plan, false); !errors.Is(err, impl.ErrAlreadyApplied) {
		t.Fatalf("expected ErrAlreadyApplied on the second apply, got %v", err)
	}
	if state := g.Inspect(); len(state.Applied) != 5 {
		t.Fatalf("second apply must not change progress, got %d steps", len(state.Applied))
	}
}

func TestRollbackUndoesStepsInReverseOrder(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 329, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: []int{9}})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if _, err := g.Apply(plan, false); err != nil {
		t.Fatalf("applying failed: %v", err)
	}
	if err := g.Rollback(); err != nil {
		t.Fatalf("rollback of a revertible plan failed: %v", err)
	}
	if state := g.Inspect(); len(state.Applied) != 0 {
		t.Fatalf("rollback left %d applied steps, want 0", len(state.Applied))
	}
	log := g.AuditLog()
	if len(log) != 10 {
		t.Fatalf("audit log has %d records, want 10 (5 apply + 5 rollback)", len(log))
	}
	wantTail := []string{"rollback:no_new_privs", "rollback:env_cleanup", "rollback:user", "rollback:primary_group", "rollback:groups"}
	for i, want := range wantTail {
		if log[5+i] != want {
			t.Fatalf("rollback record %d is %q, want %q", i, log[5+i], want)
		}
	}
}

func TestRollbackAfterUserSwitchIsRejected(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 330, Threads: 1}, impl.Creds{Uid: 1000, Gid: 1000, Groups: nil})
	if err != nil {
		t.Fatalf("planning a user switch failed: %v", err)
	}
	if _, err := g.Apply(plan, true); err != nil {
		t.Fatalf("approved apply failed: %v", err)
	}
	if err := g.Rollback(); !errors.Is(err, impl.ErrIrreversible) {
		t.Fatalf("expected ErrIrreversible after the user switch, got %v", err)
	}
	if state := g.Inspect(); len(state.Applied) != 5 {
		t.Fatalf("rejected rollback must keep the 5 applied steps, got %d", len(state.Applied))
	}
}

func TestInspectReportsProgress(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	if state := g.Inspect(); len(state.Applied) != 0 {
		t.Fatalf("fresh guard reports %d applied steps, want 0", len(state.Applied))
	}
	want := impl.Creds{Uid: 0, Gid: 10, Groups: []int{10}}
	plan, err := g.Plan(impl.Target{Pid: 331, Threads: 1}, want)
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if _, err := g.Apply(plan, false); err != nil {
		t.Fatalf("applying failed: %v", err)
	}
	state := g.Inspect()
	if state.PlanID != plan.ID {
		t.Fatalf("state reports plan %d, want %d", state.PlanID, plan.ID)
	}
	if state.Credentials.Uid != want.Uid || state.Credentials.Gid != want.Gid {
		t.Fatalf("state credentials are %+v, want uid=%d gid=%d", state.Credentials, want.Uid, want.Gid)
	}
	if !state.NoNewPrivs {
		t.Fatalf("state must report no_new_privs after a full apply")
	}
}

func TestSnapshotRestoreDoesNotReplaySteps(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 332, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: nil})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if _, err := g.Apply(plan, false); err != nil {
		t.Fatalf("applying failed: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	other := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	state := other.Inspect()
	if len(state.Applied) != 5 || state.PlanID != plan.ID {
		t.Fatalf("restored state is %+v, want plan %d with 5 steps", state, plan.ID)
	}
	if _, err := other.Apply(plan, false); !errors.Is(err, impl.ErrAlreadyApplied) {
		t.Fatalf("restored guard must not replay completed steps, got %v", err)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 333, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: nil})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if _, err := g.Apply(plan, false); err != nil {
		t.Fatalf("applying failed: %v", err)
	}
	if err := g.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("expected ErrBadSnapshot for garbage input, got %v", err)
	}
	if state := g.Inspect(); len(state.Applied) != 5 {
		t.Fatalf("corrupt restore must not touch live state, got %d steps", len(state.Applied))
	}
}

func TestStatsCountsTransitions(t *testing.T) {
	g := impl.NewCredTransitionGuard(impl.Limits{MaxGroups: 8, MaxEnvVars: 16})
	plan, err := g.Plan(impl.Target{Pid: 334, Threads: 1}, impl.Creds{Uid: 0, Gid: 0, Groups: nil})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if _, err := g.Apply(plan, false); err != nil {
		t.Fatalf("applying failed: %v", err)
	}
	if err := g.Rollback(); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if _, err := g.Plan(impl.Target{Pid: 335, Threads: 3}, impl.Creds{Uid: 0, Gid: 0, Groups: nil}); !errors.Is(err, impl.ErrThreadCount) {
		t.Fatalf("expected the multithreaded plan to be rejected, got %v", err)
	}
	stats := g.Stats()
	if stats.Planned != 1 || stats.Applied != 1 || stats.RolledBack != 1 || stats.Rejected != 1 {
		t.Fatalf("stats are %+v, want planned=1 applied=1 rolledBack=1 rejected=1", stats)
	}
}
