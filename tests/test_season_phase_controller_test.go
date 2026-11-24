package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-04"
)

func seasonStart() time.Time {
	return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
}

func TestNewControllerValidatesLimits(t *testing.T) {
	if _, err := impl.NewSeasonPhaseController(0, 4); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero phase limit must report ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewSeasonPhaseController(4, 0); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero settlement limit must report ErrInvalidConfig, got %v", err)
	}
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("valid limits must build a controller, got %v", err)
	}
	if _, err := ctrl.Status("season-missing"); !errors.Is(err, impl.ErrSeasonNotFound) {
		t.Fatalf("unknown season status must report ErrSeasonNotFound, got %v", err)
	}
}

func TestEnterPhaseOpensSeason(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("opening the first phase must succeed, got %v", err)
	}
	status, err := ctrl.Status("season-1")
	if err != nil {
		t.Fatalf("status must succeed, got %v", err)
	}
	if status.SeasonID != "season-1" || status.Name != "regular" {
		t.Fatalf("status = %+v, want season-1 in phase regular", status)
	}
	if !status.StartedAt.Equal(start) {
		t.Fatalf("phase start = %v, want %v", status.StartedAt, start)
	}
	if !status.EndedAt.IsZero() {
		t.Fatalf("open phase end = %v, want the zero time", status.EndedAt)
	}
	if status.Settled || status.Reopened {
		t.Fatalf("fresh phase settled=%v reopened=%v, want both false", status.Settled, status.Reopened)
	}
	boundaries, err := ctrl.Boundaries("season-1")
	if err != nil {
		t.Fatalf("boundaries must succeed, got %v", err)
	}
	if len(boundaries) != 1 || boundaries[0].Name != "regular" {
		t.Fatalf("boundaries = %+v, want exactly the regular phase", boundaries)
	}
}

func TestEnterPhaseRejectsInvalidNames(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("", "regular", start); !errors.Is(err, impl.ErrInvalidSeason) {
		t.Fatalf("empty season must report ErrInvalidSeason, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "", start); !errors.Is(err, impl.ErrInvalidPhase) {
		t.Fatalf("empty phase must report ErrInvalidPhase, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("first phase must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "playoffs", start.Add(time.Hour)); err != nil {
		t.Fatalf("second phase must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "regular", start.Add(2*time.Hour)); !errors.Is(err, impl.ErrPhaseExists) {
		t.Fatalf("reusing a phase name must report ErrPhaseExists, got %v", err)
	}
}

func TestEnterPhaseRejectsNonIncreasingTime(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("first phase must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "playoffs", start); !errors.Is(err, impl.ErrTimeRegression) {
		t.Fatalf("same instant must report ErrTimeRegression, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "playoffs", start.Add(-time.Minute)); !errors.Is(err, impl.ErrTimeRegression) {
		t.Fatalf("earlier instant must report ErrTimeRegression, got %v", err)
	}
}

func TestEnterPhaseClosesThePreviousBoundary(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	cutover := start.Add(24 * time.Hour)
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "playoffs", cutover); err != nil {
		t.Fatalf("playoffs must open, got %v", err)
	}
	boundaries, err := ctrl.Boundaries("season-1")
	if err != nil {
		t.Fatalf("boundaries must succeed, got %v", err)
	}
	if len(boundaries) != 2 {
		t.Fatalf("boundaries = %+v, want two phases", boundaries)
	}
	if !boundaries[0].EndedAt.Equal(cutover) {
		t.Fatalf("regular end = %v, want the cutover %v", boundaries[0].EndedAt, cutover)
	}
	if !boundaries[1].StartedAt.Equal(cutover) || !boundaries[1].EndedAt.IsZero() {
		t.Fatalf("playoffs window = %v..%v, want an open window starting at the cutover", boundaries[1].StartedAt, boundaries[1].EndedAt)
	}
	status, err := ctrl.Status("season-1")
	if err != nil || status.Name != "playoffs" {
		t.Fatalf("status = %+v err %v, want playoffs", status, err)
	}
}

func TestPhaseLimitEnforced(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(2, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("season-1", "phase-1", start); err != nil {
		t.Fatalf("first phase must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "phase-2", start.Add(time.Hour)); err != nil {
		t.Fatalf("second phase must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "phase-3", start.Add(2*time.Hour)); !errors.Is(err, impl.ErrPhaseLimit) {
		t.Fatalf("third phase must report ErrPhaseLimit, got %v", err)
	}
}

func TestSubmitAttributesWritesByTimestamp(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	cutover := start.Add(24 * time.Hour)
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 5, Tag: "match"}, start.Add(5*time.Hour)); err != nil {
		t.Fatalf("write inside regular must succeed, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "playoffs", cutover); err != nil {
		t.Fatalf("playoffs must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 3, Tag: "match"}, cutover.Add(5*time.Hour)); err != nil {
		t.Fatalf("write inside playoffs must succeed, got %v", err)
	}
	regular, err := ctrl.PhaseTotal("season-1", "regular", "player-1")
	if err != nil {
		t.Fatalf("phase total must succeed, got %v", err)
	}
	if regular != 5 {
		t.Fatalf("regular total = %d, want 5", regular)
	}
	playoffs, err := ctrl.PhaseTotal("season-1", "playoffs", "player-1")
	if err != nil {
		t.Fatalf("phase total must succeed, got %v", err)
	}
	if playoffs != 3 {
		t.Fatalf("playoffs total = %d, want 3", playoffs)
	}
	grand, err := ctrl.GrandTotal("season-1", "player-1")
	if err != nil {
		t.Fatalf("grand total must succeed, got %v", err)
	}
	if grand != 8 {
		t.Fatalf("grand total = %d, want 8", grand)
	}
	if _, err := ctrl.PhaseTotal("season-1", "regular", "player-9"); err != nil {
		t.Fatalf("a subject without writes must still succeed, got %v", err)
	}
	if _, err := ctrl.PhaseTotal("season-1", "phase-missing", "player-1"); !errors.Is(err, impl.ErrPhaseNotFound) {
		t.Fatalf("unknown phase total must report ErrPhaseNotFound, got %v", err)
	}
}

func TestSubmitRejectsBeforeSeasonStart(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 1}, start.Add(-time.Second)); !errors.Is(err, impl.ErrBeforeSeasonStart) {
		t.Fatalf("write before the season must report ErrBeforeSeasonStart, got %v", err)
	}
	if err := ctrl.Submit("season-missing", "player-1", impl.Value{Amount: 1}, start); !errors.Is(err, impl.ErrSeasonNotFound) {
		t.Fatalf("write into an unknown season must report ErrSeasonNotFound, got %v", err)
	}
}

func TestSubmitValidatesSubjectAndAmount(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "", impl.Value{Amount: 1}, start.Add(time.Hour)); !errors.Is(err, impl.ErrInvalidSubject) {
		t.Fatalf("empty subject must report ErrInvalidSubject, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: -1}, start.Add(time.Hour)); !errors.Is(err, impl.ErrInvalidValue) {
		t.Fatalf("negative amount must report ErrInvalidValue, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 0}, start.Add(time.Hour)); err != nil {
		t.Fatalf("zero amount must be accepted, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-2", impl.Value{Amount: 4}, start.Add(time.Hour)); err != nil {
		t.Fatalf("positive amount must be accepted, got %v", err)
	}
	total, err := ctrl.GrandTotal("season-1", "player-2")
	if err != nil || total != 4 {
		t.Fatalf("grand total = %d err %v, want 4", total, err)
	}
}

func TestSettleSummarisesEntriesInOrder(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	settleAt := start.Add(20 * time.Hour)
	now := settleAt
	ctrl.SetClock(func() time.Time { return now })
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	for _, write := range []struct {
		subject string
		amount  int64
	}{
		{"player-b", 5},
		{"player-a", 7},
		{"player-c", 3},
	} {
		if err := ctrl.Submit("season-1", write.subject, impl.Value{Amount: write.amount}, start.Add(time.Hour)); err != nil {
			t.Fatalf("submit %s must succeed, got %v", write.subject, err)
		}
	}
	settlement, err := ctrl.Settle("season-1")
	if err != nil {
		t.Fatalf("settle must succeed, got %v", err)
	}
	if settlement.SeasonID != "season-1" || settlement.Phase != "regular" {
		t.Fatalf("settlement = %+v, want season-1 phase regular", settlement)
	}
	if !settlement.SettledAt.Equal(settleAt) {
		t.Fatalf("settlement time = %v, want %v", settlement.SettledAt, settleAt)
	}
	if len(settlement.Entries) != 3 {
		t.Fatalf("settlement carries %d entries, want 3", len(settlement.Entries))
	}
	want := []string{"player-a", "player-b", "player-c"}
	for i, subject := range want {
		if settlement.Entries[i].Subject != subject {
			t.Fatalf("settlement entry %d = %q, want %q", i, settlement.Entries[i].Subject, subject)
		}
	}
	if settlement.Entries[0].Amount != 7 || settlement.Entries[1].Amount != 5 || settlement.Entries[2].Amount != 3 {
		t.Fatalf("settlement amounts = %d,%d,%d, want 7,5,3", settlement.Entries[0].Amount, settlement.Entries[1].Amount, settlement.Entries[2].Amount)
	}
	if settlement.Total != 15 {
		t.Fatalf("settlement total = %d, want 15", settlement.Total)
	}
	if settlement.Duplicate {
		t.Fatalf("first settlement must not be marked duplicate")
	}
	status, err := ctrl.Status("season-1")
	if err != nil {
		t.Fatalf("status must succeed, got %v", err)
	}
	if !status.Settled || !status.EndedAt.Equal(settleAt) {
		t.Fatalf("settled status = %+v, want settled at %v", status, settleAt)
	}
	boundaries, err := ctrl.Boundaries("season-1")
	if err != nil || !boundaries[0].EndedAt.Equal(settleAt) {
		t.Fatalf("phase boundary end = %v err %v, want %v", boundaries[0].EndedAt, err, settleAt)
	}
}

func TestSettleIsIdempotent(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	now := start.Add(10 * time.Hour)
	ctrl.SetClock(func() time.Time { return now })
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 9}, start.Add(time.Hour)); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	first, err := ctrl.Settle("season-1")
	if err != nil {
		t.Fatalf("settle must succeed, got %v", err)
	}
	now = now.Add(time.Hour)
	second, err := ctrl.Settle("season-1")
	if err != nil {
		t.Fatalf("repeat settle must succeed, got %v", err)
	}
	if !second.Duplicate {
		t.Fatalf("repeat settle must be marked duplicate")
	}
	if second.Total != first.Total || len(second.Entries) != len(first.Entries) {
		t.Fatalf("repeat settle produced %+v, want the same result as %+v", second, first)
	}
	if !second.SettledAt.Equal(first.SettledAt) {
		t.Fatalf("repeat settle time = %v, want the original %v", second.SettledAt, first.SettledAt)
	}
	total, err := ctrl.GrandTotal("season-1", "player-1")
	if err != nil || total != 9 {
		t.Fatalf("grand total after repeat settle = %d err %v, want 9", total, err)
	}
}

func TestReopenRequiresSettledPhase(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	now := start.Add(10 * time.Hour)
	ctrl.SetClock(func() time.Time { return now })
	if err := ctrl.Reopen("season-missing", start); !errors.Is(err, impl.ErrSeasonNotFound) {
		t.Fatalf("reopen on an unknown season must report ErrSeasonNotFound, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.Reopen("season-1", start.Add(time.Hour)); !errors.Is(err, impl.ErrNotSettled) {
		t.Fatalf("reopen before settle must report ErrNotSettled, got %v", err)
	}
	if _, err := ctrl.Settle("season-1"); err != nil {
		t.Fatalf("settle must succeed, got %v", err)
	}
	if err := ctrl.Reopen("season-1", start.Add(5*time.Hour)); !errors.Is(err, impl.ErrTimeRegression) {
		t.Fatalf("reopen before the settlement must report ErrTimeRegression, got %v", err)
	}
	if err := ctrl.Reopen("season-1", start.Add(11*time.Hour)); err != nil {
		t.Fatalf("reopen after the settlement must succeed, got %v", err)
	}
	status, err := ctrl.Status("season-1")
	if err != nil {
		t.Fatalf("status must succeed, got %v", err)
	}
	if status.Settled {
		t.Fatalf("reopened phase must not report settled")
	}
	if !status.Reopened {
		t.Fatalf("reopened phase must be marked reopened")
	}
	if !status.EndedAt.IsZero() {
		t.Fatalf("reopened phase end = %v, want the zero time", status.EndedAt)
	}
}

func TestSettlementLimitEnforced(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(3, 1)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	now := start.Add(2 * time.Hour)
	ctrl.SetClock(func() time.Time { return now })
	if err := ctrl.EnterPhase("season-1", "phase-1", start); err != nil {
		t.Fatalf("first phase must open, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "phase-2", start.Add(time.Hour)); err != nil {
		t.Fatalf("second phase must open, got %v", err)
	}
	if _, err := ctrl.Settle("season-1"); err != nil {
		t.Fatalf("first settlement must succeed, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "phase-3", start.Add(3*time.Hour)); err != nil {
		t.Fatalf("third phase must open, got %v", err)
	}
	now = start.Add(4 * time.Hour)
	if _, err := ctrl.Settle("season-1"); !errors.Is(err, impl.ErrSettlementLimit) {
		t.Fatalf("second settlement must report ErrSettlementLimit, got %v", err)
	}
}

func TestConcurrentSubmitAccountsEveryEntry(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				if submitErr := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 1}, start.Add(time.Hour)); submitErr != nil {
					t.Errorf("submit failed: %v", submitErr)
					return
				}
			}
		}()
	}
	wg.Wait()
	total, err := ctrl.GrandTotal("season-1", "player-1")
	if err != nil {
		t.Fatalf("grand total must succeed, got %v", err)
	}
	if want := int64(workers * perWorker); total != want {
		t.Fatalf("grand total = %d, want %d", total, want)
	}
	phaseTotal, err := ctrl.PhaseTotal("season-1", "regular", "player-1")
	if err != nil {
		t.Fatalf("phase total must succeed, got %v", err)
	}
	if phaseTotal != total {
		t.Fatalf("phase total = %d, want the same as the grand total %d", phaseTotal, total)
	}
}

func TestSnapshotRestoreKeepsBoundariesAndTotals(t *testing.T) {
	ctrl, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	start := seasonStart()
	cutover := start.Add(24 * time.Hour)
	now := cutover.Add(10 * time.Hour)
	ctrl.SetClock(func() time.Time { return now })
	if err := ctrl.EnterPhase("season-1", "regular", start); err != nil {
		t.Fatalf("regular must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 5}, start.Add(time.Hour)); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-2", impl.Value{Amount: 3}, start.Add(2*time.Hour)); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if err := ctrl.EnterPhase("season-1", "playoffs", cutover); err != nil {
		t.Fatalf("playoffs must open, got %v", err)
	}
	if err := ctrl.Submit("season-1", "player-1", impl.Value{Amount: 7}, cutover.Add(time.Hour)); err != nil {
		t.Fatalf("submit must succeed, got %v", err)
	}
	if _, err := ctrl.Settle("season-1"); err != nil {
		t.Fatalf("settle must succeed, got %v", err)
	}
	data, err := ctrl.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}

	restored, err := impl.NewSeasonPhaseController(4, 4)
	if err != nil {
		t.Fatalf("controller must build, got %v", err)
	}
	restored.SetClock(func() time.Time { return now })
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	boundaries, err := restored.Boundaries("season-1")
	if err != nil {
		t.Fatalf("boundaries must succeed, got %v", err)
	}
	if len(boundaries) != 2 || boundaries[0].Name != "regular" || boundaries[1].Name != "playoffs" {
		t.Fatalf("restored boundaries = %+v, want regular then playoffs", boundaries)
	}
	if !boundaries[0].EndedAt.Equal(cutover) {
		t.Fatalf("restored regular end = %v, want %v", boundaries[0].EndedAt, cutover)
	}
	grand, err := restored.GrandTotal("season-1", "player-1")
	if err != nil || grand != 12 {
		t.Fatalf("restored grand total = %d err %v, want 12", grand, err)
	}
	regular, err := restored.PhaseTotal("season-1", "regular", "player-2")
	if err != nil || regular != 3 {
		t.Fatalf("restored regular total = %d err %v, want 3", regular, err)
	}
	status, err := restored.Status("season-1")
	if err != nil {
		t.Fatalf("status must succeed, got %v", err)
	}
	if !status.Settled || status.Name != "playoffs" {
		t.Fatalf("restored status = %+v, want the settled playoffs phase", status)
	}
	repeat, err := restored.Settle("season-1")
	if err != nil {
		t.Fatalf("settle after restore must succeed, got %v", err)
	}
	if !repeat.Duplicate || repeat.Total != 7 {
		t.Fatalf("settle after restore = %+v, want a duplicate settlement of 7", repeat)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
}
