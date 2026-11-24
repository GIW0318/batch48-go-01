package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-12"
)

func newArbiter(hysteresis, fadeSeconds float64) *impl.LightBudgetArbiter {
	return impl.NewLightBudgetArbiter(impl.ArbiterConfig{
		Hysteresis:   hysteresis,
		FadeSeconds:  fadeSeconds,
		DefaultQuota: 4,
	})
}

func light(id int, category string, coverage, intensity float64, shadow bool) impl.Light {
	return impl.Light{
		ID:            id,
		Category:      category,
		Coverage:      coverage,
		Intensity:     intensity,
		ShadowCapable: shadow,
	}
}

func submit(t *testing.T, a *impl.LightBudgetArbiter, lights ...impl.Light) {
	t.Helper()
	if err := a.Submit(lights); err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
}

func selectWith(t *testing.T, a *impl.LightBudgetArbiter, budget impl.Budget) impl.Selection {
	t.Helper()
	sel, err := a.Select(budget)
	if err != nil {
		t.Fatalf("Select returned unexpected error: %v", err)
	}
	return sel
}

func equalIDs(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSubmitRejectsDuplicateLightIDs(t *testing.T) {
	a := newArbiter(0, 1)
	err := a.Submit([]impl.Light{
		light(1, "key", 2, 5, true),
		light(1, "fill", 1, 1, false),
	})
	if !errors.Is(err, impl.ErrDuplicateLight) {
		t.Fatalf("Submit with duplicate ids: got %v, want ErrDuplicateLight", err)
	}
	if got := a.Stats().Candidates; got != 0 {
		t.Fatalf("Stats().Candidates after rejected Submit = %d, want 0", got)
	}
}

func TestSelectWithoutLightsReturnsErrNoLights(t *testing.T) {
	a := newArbiter(0, 1)
	if _, err := a.Select(impl.Budget{Shaded: 2, Shadowed: 1, MaxEvaluations: 4}); !errors.Is(err, impl.ErrNoLights) {
		t.Fatalf("Select without Submit: got %v, want ErrNoLights", err)
	}
}

func TestSelectRejectsOutOfRangeBudget(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a, light(1, "key", 1, 1, true))
	if _, err := a.Select(impl.Budget{Shaded: -1, Shadowed: 0, MaxEvaluations: 1}); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("Select with negative shaded count: got %v, want ErrBadBudget", err)
	}
	if _, err := a.Select(impl.Budget{Shaded: 1, Shadowed: 2, MaxEvaluations: 1}); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("Select with shadowed above shaded: got %v, want ErrBadBudget", err)
	}
}

func TestSelectOrdersByScoreThenID(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a,
		light(1, "key", 2, 3, false),
		light(2, "key", 5, 2, false),
		light(3, "key", 3, 2, false),
	)
	budget := impl.Budget{Shaded: 3, Shadowed: 0, MaxEvaluations: 8}
	first := selectWith(t, a, budget)
	if !equalIDs(first.Shaded, []int{2, 1, 3}) {
		t.Fatalf("Shaded = %v, want score order [2 1 3]", first.Shaded)
	}
	second := selectWith(t, a, budget)
	if !equalIDs(second.Shaded, first.Shaded) {
		t.Fatalf("second Select = %v, want the deterministic result %v", second.Shaded, first.Shaded)
	}
}

func TestSelectAppliesPerCategoryQuota(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a,
		light(1, "sign", 3, 3, false),
		light(2, "sign", 1, 7, false),
		light(3, "fill", 4, 2, false),
	)
	sel := selectWith(t, a, impl.Budget{
		Shaded:         3,
		Shadowed:       0,
		MaxEvaluations: 8,
		CategoryQuota:  map[string]int{"sign": 1, "fill": 1},
	})
	if !equalIDs(sel.Shaded, []int{1, 3}) {
		t.Fatalf("Shaded = %v, want the top sign light and the fill light [1 3]", sel.Shaded)
	}
}

func TestShadowedIsSubsetOfShaded(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a,
		light(1, "key", 5, 2, true),
		light(2, "key", 4, 2, true),
		light(3, "key", 6, 2, false),
	)
	sel := selectWith(t, a, impl.Budget{Shaded: 2, Shadowed: 1, MaxEvaluations: 8})
	if !equalIDs(sel.Shaded, []int{3, 1}) {
		t.Fatalf("Shaded = %v, want [3 1]", sel.Shaded)
	}
	if !equalIDs(sel.Shadowed, []int{1}) {
		t.Fatalf("Shadowed = %v, want only the shadow capable light [1]", sel.Shadowed)
	}
}

func TestHysteresisKeepsIncumbentWithinBand(t *testing.T) {
	a := newArbiter(1.5, 1)
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 3, 3, false),
	)
	budget := impl.Budget{Shaded: 1, Shadowed: 0, MaxEvaluations: 4}
	if sel := selectWith(t, a, budget); !equalIDs(sel.Shaded, []int{1}) {
		t.Fatalf("first Shaded = %v, want [1]", sel.Shaded)
	}
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 1, 11, false),
	)
	if sel := selectWith(t, a, budget); !equalIDs(sel.Shaded, []int{1}) {
		t.Fatalf("Shaded with hysteresis = %v, want the incumbent [1]", sel.Shaded)
	}
}

func TestWithoutHysteresisHigherScoreWins(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 3, 3, false),
	)
	budget := impl.Budget{Shaded: 1, Shadowed: 0, MaxEvaluations: 4}
	if sel := selectWith(t, a, budget); !equalIDs(sel.Shaded, []int{1}) {
		t.Fatalf("first Shaded = %v, want [1]", sel.Shaded)
	}
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 1, 11, false),
	)
	if sel := selectWith(t, a, budget); !equalIDs(sel.Shaded, []int{2}) {
		t.Fatalf("Shaded without hysteresis = %v, want the higher score [2]", sel.Shaded)
	}
}

func TestEvictedLightStartsFading(t *testing.T) {
	a := newArbiter(0, 2)
	submit(t, a, light(1, "key", 2, 5, false))
	budget := impl.Budget{Shaded: 1, Shadowed: 0, MaxEvaluations: 4}
	selectWith(t, a, budget)
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 4, 5, false),
	)
	sel := selectWith(t, a, budget)
	if !equalIDs(sel.Shaded, []int{2}) {
		t.Fatalf("Shaded = %v, want [2]", sel.Shaded)
	}
	if !equalIDs(sel.Fading, []int{1}) {
		t.Fatalf("Fading = %v, want the evicted light [1]", sel.Fading)
	}
}

func TestAdvanceFrameCompletesFade(t *testing.T) {
	a := newArbiter(0, 2)
	submit(t, a, light(1, "key", 2, 5, false))
	budget := impl.Budget{Shaded: 1, Shadowed: 0, MaxEvaluations: 4}
	selectWith(t, a, budget)
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 4, 5, false),
	)
	selectWith(t, a, budget)
	if err := a.AdvanceFrame(1); err != nil {
		t.Fatalf("AdvanceFrame(1) returned unexpected error: %v", err)
	}
	if !equalIDs(a.FadingLights(), []int{1}) {
		t.Fatalf("FadingLights after one second = %v, want [1]", a.FadingLights())
	}
	if err := a.AdvanceFrame(1.1); err != nil {
		t.Fatalf("AdvanceFrame(1.1) returned unexpected error: %v", err)
	}
	if got := a.FadingLights(); len(got) != 0 {
		t.Fatalf("FadingLights after the fade duration = %v, want none", got)
	}
}

func TestAdvanceFrameRejectsNonPositiveDelta(t *testing.T) {
	a := newArbiter(0, 1)
	if err := a.AdvanceFrame(0); !errors.Is(err, impl.ErrBadDelta) {
		t.Fatalf("AdvanceFrame(0): got %v, want ErrBadDelta", err)
	}
	if err := a.AdvanceFrame(-0.5); !errors.Is(err, impl.ErrBadDelta) {
		t.Fatalf("AdvanceFrame(-0.5): got %v, want ErrBadDelta", err)
	}
}

func TestBudgetChangeKeepsInFlightFade(t *testing.T) {
	a := newArbiter(0, 2)
	submit(t, a, light(1, "key", 2, 5, false))
	selectWith(t, a, impl.Budget{Shaded: 1, Shadowed: 0, MaxEvaluations: 4})
	submit(t, a,
		light(1, "key", 2, 5, false),
		light(2, "key", 4, 5, false),
	)
	selectWith(t, a, impl.Budget{Shaded: 1, Shadowed: 0, MaxEvaluations: 4})
	sel := selectWith(t, a, impl.Budget{Shaded: 1, Shadowed: 1, MaxEvaluations: 4})
	if !equalIDs(sel.Fading, []int{1}) {
		t.Fatalf("Fading after a budget change = %v, want the in flight light [1]", sel.Fading)
	}
}

func TestExplainRejectsUnknownLight(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a, light(1, "key", 1, 1, false))
	if _, err := a.Explain(99); !errors.Is(err, impl.ErrUnknownLight) {
		t.Fatalf("Explain(99): got %v, want ErrUnknownLight", err)
	}
}

func TestExplainReportsScoreAndFlags(t *testing.T) {
	a := newArbiter(0, 1)
	submit(t, a, light(1, "key", 2, 5, true))
	selectWith(t, a, impl.Budget{Shaded: 1, Shadowed: 1, MaxEvaluations: 4})
	reason, err := a.Explain(1)
	if err != nil {
		t.Fatalf("Explain(1) returned unexpected error: %v", err)
	}
	if reason.Score != 10 {
		t.Fatalf("Reason.Score = %v, want 10", reason.Score)
	}
	if reason.Category != "key" || !reason.Shaded || !reason.Shadowed || reason.Fading {
		t.Fatalf("Reason = %+v, want category key, shaded and shadowed but not fading", reason)
	}
	if reason.Note == "" {
		t.Fatalf("Reason.Note is empty, want a short explanation")
	}
}

func TestConcurrentSubmitSelectAndExplain(t *testing.T) {
	a := newArbiter(0, 1)
	lights := []impl.Light{
		light(1, "key", 1, 8, true),
		light(2, "key", 1, 7, true),
		light(3, "fill", 1, 6, false),
		light(4, "fill", 1, 5, false),
		light(5, "sign", 1, 4, false),
		light(6, "sign", 1, 3, false),
		light(7, "key", 1, 2, false),
		light(8, "key", 1, 1, false),
	}
	submit(t, a, lights...)
	budget := impl.Budget{Shaded: 4, Shadowed: 2, MaxEvaluations: 8}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				if _, err := a.Select(budget); err != nil {
					t.Errorf("concurrent Select returned error: %v", err)
					return
				}
				if err := a.AdvanceFrame(0.016); err != nil {
					t.Errorf("concurrent AdvanceFrame returned error: %v", err)
					return
				}
				if _, err := a.Explain(1); err != nil {
					t.Errorf("concurrent Explain returned error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := a.Stats().Candidates; got != 8 {
		t.Fatalf("Stats().Candidates = %d, want 8", got)
	}
	last, ok := a.LastSelection()
	if !ok {
		t.Fatalf("LastSelection missing after concurrent arbitration")
	}
	if len(last.Shaded) != 4 {
		t.Fatalf("last selection holds %d shaded lights, want 4", len(last.Shaded))
	}
	if a.Frame() < 1 {
		t.Fatalf("Frame() = %d after advanced frames, want at least 1", a.Frame())
	}
}
