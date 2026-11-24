package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-20"
)

func TestRegisterRejectsDuplicateSystem(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register system: %v", err)
	}
	if err := g.Register("physics", 2, 0); !errors.Is(err, impl.ErrSystemExists) {
		t.Fatalf("duplicate system error = %v, want ErrSystemExists", err)
	}
}

func TestRegisterRejectsNonPositiveWeight(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 0, 0); !errors.Is(err, impl.ErrInvalidWeight) {
		t.Fatalf("zero weight error = %v, want ErrInvalidWeight", err)
	}
	if err := g.Register("physics", -1, 0); !errors.Is(err, impl.ErrInvalidWeight) {
		t.Fatalf("negative weight error = %v, want ErrInvalidWeight", err)
	}
}

func TestRegisterRejectsNegativeMinimum(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, -5); !errors.Is(err, impl.ErrInvalidMin) {
		t.Fatalf("negative minimum error = %v, want ErrInvalidMin", err)
	}
}

func TestQuotaFollowsWeightAndMinimum(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 7, 0); err != nil {
		t.Fatalf("register physics: %v", err)
	}
	if err := g.Register("ai", 3, 30); err != nil {
		t.Fatalf("register ai: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	physics, err := g.Stats("physics")
	if err != nil {
		t.Fatalf("physics stats: %v", err)
	}
	ai, err := g.Stats("ai")
	if err != nil {
		t.Fatalf("ai stats: %v", err)
	}
	if physics.Quota != 49 {
		t.Fatalf("physics quota = %d, want 49", physics.Quota)
	}
	if ai.Quota != 51 {
		t.Fatalf("ai quota = %d, want 51 from the 30 ms floor plus its weighted share", ai.Quota)
	}
	if got := g.Remaining("physics"); got != 49 {
		t.Fatalf("physics remaining = %d, want 49", got)
	}
}

func TestBeginTickTwiceFails(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register system: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("first begin tick: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 1)); !errors.Is(err, impl.ErrTickInProgress) {
		t.Fatalf("second begin tick error = %v, want ErrTickInProgress", err)
	}
}

func TestEndTickWithoutBeginFails(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register system: %v", err)
	}
	if _, err := g.EndTick(); !errors.Is(err, impl.ErrTickNotStarted) {
		t.Fatalf("end tick without begin error = %v, want ErrTickNotStarted", err)
	}
}

func TestSpendWithinQuotaReducesRemaining(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register system: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	if err := g.Spend("physics", 30); err != nil {
		t.Fatalf("spend inside quota: %v", err)
	}
	if got := g.Remaining("physics"); got != 70 {
		t.Fatalf("remaining = %d, want 70", got)
	}
	st, err := g.Stats("physics")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Spent != 30 {
		t.Fatalf("spent = %d, want 30", st.Spent)
	}
}

func TestSpendExceedingQuotaWithoutPoolRejected(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 7, 0); err != nil {
		t.Fatalf("register physics: %v", err)
	}
	if err := g.Register("ai", 3, 30); err != nil {
		t.Fatalf("register ai: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	if err := g.Spend("ai", 51); err != nil {
		t.Fatalf("spend ai quota: %v", err)
	}
	if err := g.Spend("physics", 50); !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("overspend error = %v, want ErrBudgetExceeded", err)
	}
	if got := g.Remaining("physics"); got != 49 {
		t.Fatalf("remaining after rejected spend = %d, want 49", got)
	}
}

func TestSpendBorrowsFromUnusedQuotaAndRecordsBorrowed(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 7, 0); err != nil {
		t.Fatalf("register physics: %v", err)
	}
	if err := g.Register("ai", 3, 30); err != nil {
		t.Fatalf("register ai: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	if err := g.Spend("ai", 40); err != nil {
		t.Fatalf("spend ai: %v", err)
	}
	if err := g.Spend("physics", 55); err != nil {
		t.Fatalf("borrowing spend rejected: %v", err)
	}
	st, err := g.Stats("physics")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Borrowed != 6 {
		t.Fatalf("borrowed = %d, want 6 ms above the 49 ms quota", st.Borrowed)
	}
	if st.Spent != 55 {
		t.Fatalf("spent = %d, want 55", st.Spent)
	}
}

func TestSpendRejectsNonPositiveAmount(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register system: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	if err := g.Spend("physics", 0); !errors.Is(err, impl.ErrInvalidSpend) {
		t.Fatalf("zero spend error = %v, want ErrInvalidSpend", err)
	}
	if err := g.Spend("physics", -5); !errors.Is(err, impl.ErrInvalidSpend) {
		t.Fatalf("negative spend error = %v, want ErrInvalidSpend", err)
	}
}

func TestSpendRejectsUnknownSystem(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register system: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	if err := g.Spend("ghost", 5); !errors.Is(err, impl.ErrSystemUnknown) {
		t.Fatalf("unknown system error = %v, want ErrSystemUnknown", err)
	}
}

func TestEndTickReportsPlan(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 7, 0); err != nil {
		t.Fatalf("register physics: %v", err)
	}
	if err := g.Register("ai", 3, 30); err != nil {
		t.Fatalf("register ai: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	plan, err := g.EndTick()
	if err != nil {
		t.Fatalf("end tick: %v", err)
	}
	if plan.Tick != 1 || plan.Budget != 100 {
		t.Fatalf("plan = %+v, want tick 1 with budget 100", plan)
	}
	if plan.Allocated["physics"] != 49 || plan.Allocated["ai"] != 51 {
		t.Fatalf("allocated = %+v, want physics 49 and ai 51", plan.Allocated)
	}
	if err := g.BeginTick(time.Unix(0, 1)); err != nil {
		t.Fatalf("begin second tick: %v", err)
	}
	plan, err = g.EndTick()
	if err != nil {
		t.Fatalf("end second tick: %v", err)
	}
	if plan.Tick != 2 {
		t.Fatalf("second plan tick = %d, want 2", plan.Tick)
	}
}

func TestBorrowedOverrunDegradesAfterTwoTicks(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 7, 0); err != nil {
		t.Fatalf("register physics: %v", err)
	}
	if err := g.Register("ai", 3, 30); err != nil {
		t.Fatalf("register ai: %v", err)
	}
	for tick := 0; tick < 2; tick++ {
		if err := g.BeginTick(time.Unix(int64(tick), 0)); err != nil {
			t.Fatalf("begin tick %d: %v", tick, err)
		}
		if err := g.Spend("ai", 40); err != nil {
			t.Fatalf("spend ai in tick %d: %v", tick, err)
		}
		if err := g.Spend("physics", 55); err != nil {
			t.Fatalf("spend physics in tick %d: %v", tick, err)
		}
		if _, err := g.EndTick(); err != nil {
			t.Fatalf("end tick %d: %v", tick, err)
		}
	}
	if got := g.DegradeLevel(); got != 1 {
		t.Fatalf("degrade level = %d, want 1 after two overrun ticks", got)
	}
	st, err := g.Stats("physics")
	if err != nil {
		t.Fatalf("physics stats: %v", err)
	}
	if st.Overruns != 2 {
		t.Fatalf("overruns = %d, want 2", st.Overruns)
	}
}

func TestSetDegradeLevelRejectsOutOfRange(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.SetDegradeLevel(-1); !errors.Is(err, impl.ErrInvalidLevel) {
		t.Fatalf("negative level error = %v, want ErrInvalidLevel", err)
	}
	if err := g.SetDegradeLevel(4); !errors.Is(err, impl.ErrInvalidLevel) {
		t.Fatalf("oversized level error = %v, want ErrInvalidLevel", err)
	}
	if err := g.SetDegradeLevel(2); err != nil {
		t.Fatalf("valid level rejected: %v", err)
	}
	if got := g.DegradeLevel(); got != 2 {
		t.Fatalf("degrade level = %d, want 2", got)
	}
}

func TestConcurrentSpendNeverExceedsQuotas(t *testing.T) {
	g := impl.NewTickBudgetGovernor(100)
	if err := g.Register("physics", 1, 0); err != nil {
		t.Fatalf("register physics: %v", err)
	}
	if err := g.Register("ai", 1, 0); err != nil {
		t.Fatalf("register ai: %v", err)
	}
	if err := g.BeginTick(time.Unix(0, 0)); err != nil {
		t.Fatalf("begin tick: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := g.Spend("physics", 5); err != nil && !errors.Is(err, impl.ErrBudgetExceeded) {
					t.Errorf("physics spend error = %v, want nil or ErrBudgetExceeded", err)
				}
				if err := g.Spend("ai", 5); err != nil && !errors.Is(err, impl.ErrBudgetExceeded) {
					t.Errorf("ai spend error = %v, want nil or ErrBudgetExceeded", err)
				}
			}
		}()
	}
	wg.Wait()
	physics, err := g.Stats("physics")
	if err != nil {
		t.Fatalf("physics stats: %v", err)
	}
	ai, err := g.Stats("ai")
	if err != nil {
		t.Fatalf("ai stats: %v", err)
	}
	if total := physics.Spent + ai.Spent; total != 100 {
		t.Fatalf("total spent = %d, want the whole 100 ms budget consumed exactly once", total)
	}
}
