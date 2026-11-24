package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-31"
)

func escortOptions() impl.Options {
	return impl.Options{
		Quests: []impl.QuestDef{
			{ID: "q1", RouteLength: 200, Stages: 2},
			{ID: "q2", RouteLength: 150, Stages: 3},
		},
		EscortRadius:       30,
		GraceMillis:        3000,
		CombatSpeedPercent: 50,
	}
}

func mustStart(t *testing.T, g *impl.EscortQuestGuard, quest, party string, at int64) impl.Escort {
	t.Helper()
	e, err := g.Start(quest, party, at)
	if err != nil {
		t.Fatalf("start %s with party %q: unexpected error %v", quest, party, err)
	}
	return e
}

func TestStartCreatesActiveEscort(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e, err := g.Start("q1", "alice,bob", 1000)
	if err != nil {
		t.Fatalf("start: unexpected error %v", err)
	}
	if e.ID == "" {
		t.Fatalf("escort id must not be empty")
	}
	if e.QuestID != "q1" {
		t.Fatalf("quest id = %q, want q1", e.QuestID)
	}
	if e.State != "active" {
		t.Fatalf("state = %q, want active", e.State)
	}
	if e.StageIndex != 0 {
		t.Fatalf("stage index = %d, want 0", e.StageIndex)
	}
	if len(e.Members) != 2 || e.Members[0] != "alice" || e.Members[1] != "bob" {
		t.Fatalf("members = %v, want [alice bob]", e.Members)
	}
	if e.ElapsedMillis != 0 {
		t.Fatalf("elapsed = %d, want 0 at start", e.ElapsedMillis)
	}
}

func TestStartRejectsUnknownQuest(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	if _, err := g.Start("q9", "alice", 1000); !errors.Is(err, impl.ErrUnknownQuest) {
		t.Fatalf("unknown quest error = %v, want ErrUnknownQuest", err)
	}
}

func TestStartRejectsBlankParty(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	if _, err := g.Start("q1", "  ", 1000); !errors.Is(err, impl.ErrNoParty) {
		t.Fatalf("blank party error = %v, want ErrNoParty", err)
	}
}

func TestAdvanceAccumulatesStageProgress(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	st, err := g.Advance(e.ID, 30, 1300)
	if err != nil {
		t.Fatalf("advance: unexpected error %v", err)
	}
	if st.Index != 0 || st.Progress != 30 || st.Required != 100 || st.Completed {
		t.Fatalf("stage = %+v, want index 0 progress 30 required 100 not completed", st)
	}
	cur, err := g.Stage(e.ID)
	if err != nil {
		t.Fatalf("stage: unexpected error %v", err)
	}
	if cur.Progress != 30 || cur.Required != 100 {
		t.Fatalf("read back stage = %+v, want progress 30 required 100", cur)
	}
	if got := g.Elapsed(e.ID); got != 300 {
		t.Fatalf("elapsed = %d, want 300", got)
	}
}

func TestAdvanceInCombatUsesReducedSpeed(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if err := g.SetCombat(e.ID, true, 1200); err != nil {
		t.Fatalf("set combat on: unexpected error %v", err)
	}
	st, err := g.Advance(e.ID, 40, 1400)
	if err != nil {
		t.Fatalf("advance in combat: unexpected error %v", err)
	}
	if st.Progress != 20 {
		t.Fatalf("progress in combat = %d, want 20 at half speed", st.Progress)
	}
	if err := g.SetCombat(e.ID, false, 1500); err != nil {
		t.Fatalf("set combat off: unexpected error %v", err)
	}
	st, err = g.Advance(e.ID, 40, 1600)
	if err != nil {
		t.Fatalf("advance out of combat: unexpected error %v", err)
	}
	if st.Progress != 60 {
		t.Fatalf("progress after combat = %d, want 60", st.Progress)
	}
}

func TestAdvanceCompletesStageAndMovesOn(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	st, err := g.Advance(e.ID, 100, 1300)
	if err != nil {
		t.Fatalf("advance: unexpected error %v", err)
	}
	if st.Index != 1 || st.Progress != 0 || st.Required != 100 || st.Completed {
		t.Fatalf("stage after completion = %+v, want index 1 progress 0 required 100 not completed", st)
	}
	esc, err := g.Status(e.ID)
	if err != nil {
		t.Fatalf("status: unexpected error %v", err)
	}
	if esc.State != "active" || esc.StageIndex != 1 {
		t.Fatalf("escort = state %q stage %d, want active stage 1", esc.State, esc.StageIndex)
	}
}

func TestAdvanceCompletesRoute(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if _, err := g.Advance(e.ID, 100, 1200); err != nil {
		t.Fatalf("first stage: unexpected error %v", err)
	}
	st, err := g.Advance(e.ID, 100, 1300)
	if err != nil {
		t.Fatalf("second stage: unexpected error %v", err)
	}
	if st.Index != 1 || st.Progress != 100 || !st.Completed {
		t.Fatalf("final stage = %+v, want index 1 progress 100 completed", st)
	}
	esc, err := g.Status(e.ID)
	if err != nil {
		t.Fatalf("status: unexpected error %v", err)
	}
	if esc.State != "completed" {
		t.Fatalf("state = %q, want completed", esc.State)
	}
}

func TestAdvanceAfterFinishRejected(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if _, err := g.Advance(e.ID, 100, 1200); err != nil {
		t.Fatalf("first stage: unexpected error %v", err)
	}
	if _, err := g.Advance(e.ID, 100, 1300); err != nil {
		t.Fatalf("second stage: unexpected error %v", err)
	}
	if _, err := g.Advance(e.ID, 10, 1400); !errors.Is(err, impl.ErrEscortFinished) {
		t.Fatalf("advance after finish error = %v, want ErrEscortFinished", err)
	}
}

func TestAdvanceRejectsNegativeDistance(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if _, err := g.Advance(e.ID, -5, 1200); !errors.Is(err, impl.ErrInvalidDistance) {
		t.Fatalf("negative distance error = %v, want ErrInvalidDistance", err)
	}
}

func TestJoinAddsMemberAndRejectsDuplicate(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if err := g.Join(e.ID, "bob", 1100); err != nil {
		t.Fatalf("join: unexpected error %v", err)
	}
	members := g.Members(e.ID)
	if len(members) != 2 || members[0] != "alice" || members[1] != "bob" {
		t.Fatalf("members = %v, want [alice bob]", members)
	}
	if err := g.Join(e.ID, "bob", 1200); !errors.Is(err, impl.ErrAlreadyJoined) {
		t.Fatalf("duplicate join error = %v, want ErrAlreadyJoined", err)
	}
}

func TestLeaveMarksMemberAway(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice,bob", 1000)
	if err := g.Leave(e.ID, "bob", 1500); err != nil {
		t.Fatalf("leave: unexpected error %v", err)
	}
	if members := g.Members(e.ID); len(members) != 2 {
		t.Fatalf("members after leave = %v, want both players still listed", members)
	}
	away := g.Away(e.ID)
	if len(away) != 1 || away[0] != "bob" {
		t.Fatalf("away = %v, want [bob]", away)
	}
}

func TestLeaveUnknownPlayerRejected(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if err := g.Leave(e.ID, "zoe", 1500); !errors.Is(err, impl.ErrNotInEscort) {
		t.Fatalf("unknown player leave error = %v, want ErrNotInEscort", err)
	}
}

func TestGraceExpiryFailsEscort(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice,bob", 1000)
	if err := g.Leave(e.ID, "alice", 1500); err != nil {
		t.Fatalf("leave alice: unexpected error %v", err)
	}
	if err := g.Leave(e.ID, "bob", 1500); err != nil {
		t.Fatalf("leave bob: unexpected error %v", err)
	}
	if err := g.Tick(e.ID, 5000); err != nil {
		t.Fatalf("tick: unexpected error %v", err)
	}
	esc, err := g.Status(e.ID)
	if err != nil {
		t.Fatalf("status: unexpected error %v", err)
	}
	if esc.State != "failed" {
		t.Fatalf("state after grace expiry = %q, want failed", esc.State)
	}
	if esc.FailReason != "grace-expired" {
		t.Fatalf("fail reason = %q, want grace-expired", esc.FailReason)
	}
}

func TestFailThenRetryKeepsElapsed(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e := mustStart(t, g, "q1", "alice", 1000)
	if _, err := g.Advance(e.ID, 50, 1500); err != nil {
		t.Fatalf("advance: unexpected error %v", err)
	}
	if err := g.Fail(e.ID, "npc-dead", 2000); err != nil {
		t.Fatalf("fail: unexpected error %v", err)
	}
	failed, err := g.Status(e.ID)
	if err != nil {
		t.Fatalf("status after fail: unexpected error %v", err)
	}
	if failed.State != "failed" || failed.FailReason != "npc-dead" {
		t.Fatalf("failed escort = state %q reason %q, want failed/npc-dead", failed.State, failed.FailReason)
	}
	if failed.ElapsedMillis != 1000 {
		t.Fatalf("elapsed after fail = %d, want 1000", failed.ElapsedMillis)
	}
	retried, err := g.Retry(e.ID, 5000)
	if err != nil {
		t.Fatalf("retry: unexpected error %v", err)
	}
	if retried.State != "active" || retried.StageIndex != 0 {
		t.Fatalf("retried escort = state %q stage %d, want active stage 0", retried.State, retried.StageIndex)
	}
	if retried.ElapsedMillis != 1000 {
		t.Fatalf("retried elapsed = %d, want the recorded 1000 kept", retried.ElapsedMillis)
	}
	if _, err := g.Advance(e.ID, 10, 6000); err != nil {
		t.Fatalf("advance after retry: unexpected error %v", err)
	}
	if got := g.Elapsed(e.ID); got != 2000 {
		t.Fatalf("elapsed after retry advance = %d, want 2000", got)
	}
}

func TestSnapshotRestoreKeepsEscorts(t *testing.T) {
	g := impl.NewEscortQuestGuard(escortOptions())
	e1 := mustStart(t, g, "q1", "alice", 1000)
	if _, err := g.Advance(e1.ID, 40, 1300); err != nil {
		t.Fatalf("advance q1: unexpected error %v", err)
	}
	e2 := mustStart(t, g, "q2", "carol", 2000)
	if _, err := g.Advance(e2.ID, 10, 2200); err != nil {
		t.Fatalf("advance q2: unexpected error %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := impl.NewEscortQuestGuard(impl.Options{})
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	st1, err := restored.Stage(e1.ID)
	if err != nil {
		t.Fatalf("stage after restore: unexpected error %v", err)
	}
	if st1.Progress != 40 || st1.Required != 100 {
		t.Fatalf("restored q1 stage = %+v, want progress 40 required 100", st1)
	}
	st2, err := restored.Stage(e2.ID)
	if err != nil {
		t.Fatalf("stage q2 after restore: unexpected error %v", err)
	}
	if st2.Progress != 10 || st2.Required != 50 {
		t.Fatalf("restored q2 stage = %+v, want progress 10 required 50", st2)
	}
	if members := restored.Members(e1.ID); len(members) != 1 || members[0] != "alice" {
		t.Fatalf("restored members = %v, want [alice]", members)
	}
	if err := restored.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage restore error = %v, want ErrBadSnapshot", err)
	}
}
