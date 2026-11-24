package task

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-03"
)

func tracker() *impl.TaintTracker {
	return impl.NewTaintTracker(impl.Options{MaxPaths: 64, MaxIterations: 16})
}

func mustStep(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustSolve(t *testing.T, tr *impl.TaintTracker) impl.Solution {
	t.Helper()
	sol, err := tr.Solve()
	if err != nil {
		t.Fatalf("Solve: unexpected error: %v", err)
	}
	return sol
}

func TestMarkSourceRejectsEmptyLabels(t *testing.T) {
	tr := tracker()
	if err := tr.MarkSource("", []string{"x"}); !errors.Is(err, impl.ErrBadNode) {
		t.Fatalf("empty node error = %v, want ErrBadNode", err)
	}
	if err := tr.MarkSource("a", nil); !errors.Is(err, impl.ErrEmptyLabels) {
		t.Fatalf("empty labels error = %v, want ErrEmptyLabels", err)
	}
}

func TestAddSinkRejectsDuplicate(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.AddSink("out", "file"))
	err := tr.AddSink("out", "file")
	if !errors.Is(err, impl.ErrDuplicateSink) {
		t.Fatalf("duplicate sink error = %v, want ErrDuplicateSink", err)
	}
}

func TestPropagateRejectsUnknownEdgeKind(t *testing.T) {
	tr := tracker()
	err := tr.Propagate("a", "b", impl.EdgeKind(9))
	if !errors.Is(err, impl.ErrUnknownEdge) {
		t.Fatalf("unknown edge error = %v, want ErrUnknownEdge", err)
	}
}

func TestAliasPropagatesBothDirections(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("a", []string{"secret"}))
	mustStep(t, tr.Propagate("a", "b", impl.EdgeAlias))
	mustStep(t, tr.AddSink("b", "log"))
	mustSolve(t, tr)
	labels, err := tr.Labels("b")
	if err != nil {
		t.Fatalf("Labels(b): %v", err)
	}
	if !slices.Equal(labels, []string{"secret"}) {
		t.Fatalf("Labels(b) = %v, want [secret]", labels)
	}
	back, err := tr.Labels("a")
	if err != nil {
		t.Fatalf("Labels(a): %v", err)
	}
	if !slices.Equal(back, []string{"secret"}) {
		t.Fatalf("Labels(a) = %v, want [secret]", back)
	}
}

func TestLabelsMergeAsSortedUnion(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("a", []string{"z", "a"}))
	mustStep(t, tr.Propagate("a", "c", impl.EdgeAssign))
	mustStep(t, tr.AddSink("c", "file"))
	mustSolve(t, tr)
	labels, err := tr.Labels("c")
	if err != nil {
		t.Fatalf("Labels(c): %v", err)
	}
	if !slices.Equal(labels, []string{"a", "z"}) {
		t.Fatalf("Labels(c) = %v, want sorted union [a z]", labels)
	}
}

func TestSanitizerPrunesOnlyNamedLabels(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("s", []string{"x", "y"}))
	mustStep(t, tr.Propagate("s", "v", impl.EdgeAssign))
	mustStep(t, tr.ApplySanitizer("v", []string{"x"}))
	mustStep(t, tr.AddSink("v", "file"))
	mustSolve(t, tr)
	hits, err := tr.ReachableSinks()
	if err != nil {
		t.Fatalf("ReachableSinks: %v", err)
	}
	want := []impl.SinkHit{{Node: "v", Kind: "file", Labels: []string{"y"}}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
}

func TestSanitizerBlocksDownstreamSink(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("s", []string{"x"}))
	mustStep(t, tr.Propagate("s", "v", impl.EdgeAssign))
	mustStep(t, tr.Propagate("v", "w", impl.EdgeAssign))
	mustStep(t, tr.ApplySanitizer("v", []string{"x"}))
	mustStep(t, tr.AddSink("w", "net"))
	sol := mustSolve(t, tr)
	hits, err := tr.ReachableSinks()
	if err != nil {
		t.Fatalf("ReachableSinks: %v", err)
	}
	if len(hits) != 0 || len(sol.Sinks) != 0 {
		t.Fatalf("sanitized path still reaches a sink: %+v / %+v", hits, sol.Sinks)
	}
}

func TestCallEdgeUsesCompleteSummary(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.SetSummary("readEnv", impl.Summary{
		Function:     "readEnv",
		Complete:     true,
		ReturnLabels: []string{"env"},
	}))
	mustStep(t, tr.Propagate("readEnv", "dst", impl.EdgeCall))
	mustStep(t, tr.AddSink("dst", "log"))
	mustSolve(t, tr)
	labels, err := tr.Labels("dst")
	if err != nil {
		t.Fatalf("Labels(dst): %v", err)
	}
	if !slices.Equal(labels, []string{"env"}) {
		t.Fatalf("Labels(dst) = %v, want [env]", labels)
	}
}

func TestCallEdgeRequiresSummary(t *testing.T) {
	tr := tracker()
	err := tr.Propagate("missing", "dst", impl.EdgeCall)
	if !errors.Is(err, impl.ErrNoSummary) {
		t.Fatalf("missing summary error = %v, want ErrNoSummary", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error %q must name the callee", err.Error())
	}
}

func TestReachableSinksRequiresSolve(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("a", []string{"x"}))
	mustStep(t, tr.AddSink("a", "file"))
	if _, err := tr.ReachableSinks(); !errors.Is(err, impl.ErrNotSolved) {
		t.Fatalf("ReachableSinks before Solve = %v, want ErrNotSolved", err)
	}
}

func TestReachableSinksSortedAndDeduplicated(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("zs", []string{"l"}))
	mustStep(t, tr.MarkSource("as", []string{"l"}))
	mustStep(t, tr.AddSink("zs", "net"))
	mustStep(t, tr.AddSink("as", "net"))
	mustStep(t, tr.AddSink("zs", "file"))
	mustSolve(t, tr)
	hits, err := tr.ReachableSinks()
	if err != nil {
		t.Fatalf("ReachableSinks: %v", err)
	}
	want := []impl.SinkHit{
		{Node: "as", Kind: "net", Labels: []string{"l"}},
		{Node: "zs", Kind: "file", Labels: []string{"l"}},
		{Node: "zs", Kind: "net", Labels: []string{"l"}},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
}

func TestSolveIsDeterministic(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("a", []string{"x"}))
	mustStep(t, tr.Propagate("a", "b", impl.EdgeAssign))
	mustStep(t, tr.Propagate("b", "c", impl.EdgeAlias))
	mustStep(t, tr.AddSink("b", "net"))
	mustStep(t, tr.AddSink("c", "file"))
	first := mustSolve(t, tr)
	second := mustSolve(t, tr)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second Solve = %+v, want identical to %+v", second, first)
	}
	if first.Iterations != second.Iterations {
		t.Fatalf("iterations differ: %d vs %d", first.Iterations, second.Iterations)
	}
}

func TestBudgetTruncationMarksIncomplete(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.SetBudget(16, 1))
	mustStep(t, tr.MarkSource("a", []string{"x"}))
	mustStep(t, tr.Propagate("d", "e", impl.EdgeAssign))
	mustStep(t, tr.Propagate("c", "d", impl.EdgeAssign))
	mustStep(t, tr.Propagate("b", "c", impl.EdgeAssign))
	mustStep(t, tr.Propagate("a", "b", impl.EdgeAssign))
	mustStep(t, tr.AddSink("e", "file"))
	sol := mustSolve(t, tr)
	if sol.Complete {
		t.Fatal("Solve with a one-iteration budget must not report a complete solution")
	}
	st := tr.Stats()
	if !st.Truncated {
		t.Fatalf("Stats().Truncated = false, want true: %+v", st)
	}
	if st.Iterations != 1 {
		t.Fatalf("Stats().Iterations = %d, want the budget cap 1", st.Iterations)
	}
}

func TestResetClearsSolution(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("a", []string{"x"}))
	mustStep(t, tr.AddSink("a", "file"))
	mustSolve(t, tr)
	mustStep(t, tr.Reset())
	if _, err := tr.ReachableSinks(); !errors.Is(err, impl.ErrNotSolved) {
		t.Fatalf("ReachableSinks after Reset = %v, want ErrNotSolved", err)
	}
	if st := tr.Stats(); st.Iterations != 0 || st.Paths != 0 || st.Truncated {
		t.Fatalf("Stats after Reset = %+v, want zero", st)
	}
}

func TestNodesSorted(t *testing.T) {
	tr := tracker()
	mustStep(t, tr.MarkSource("mid", []string{"x"}))
	mustStep(t, tr.Propagate("mid", "zeta", impl.EdgeAssign))
	mustStep(t, tr.Propagate("mid", "alpha", impl.EdgeAssign))
	if got, want := tr.Nodes(), []string{"alpha", "mid", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("Nodes = %v, want %v", got, want)
	}
}
