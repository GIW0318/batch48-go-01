package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-28"
)

func newRunner(opts impl.Options) *impl.BehaviorTreeRunner {
	return impl.NewBehaviorTreeRunner(opts)
}

func loadTree(t *testing.T, g *impl.BehaviorTreeRunner, root impl.Node) {
	t.Helper()
	if err := g.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func traceIDs(steps []impl.Step) []string {
	ids := make([]string, len(steps))
	for i, s := range steps {
		ids[i] = s.NodeID
	}
	return ids
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestLoadRejectsBadNode(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	if err := g.Load(impl.Node{Kind: "condition", Key: "hp", Op: "gt", Value: 1}); !errors.Is(err, impl.ErrInvalidNode) {
		t.Fatalf("node without id error = %v, want ErrInvalidNode", err)
	}
	if err := g.Load(impl.Node{ID: "root", Kind: "condition", Key: "hp", Op: "between", Value: 1}); !errors.Is(err, impl.ErrInvalidNode) {
		t.Fatalf("condition with unknown comparison error = %v, want ErrInvalidNode", err)
	}
}

func TestLoadRejectsUnknownKind(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	if err := g.Load(impl.Node{ID: "root", Kind: "dance"}); !errors.Is(err, impl.ErrUnknownKind) {
		t.Fatalf("unknown kind error = %v, want ErrUnknownKind", err)
	}
}

func TestLoadRejectsEmptyComposite(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	for _, kind := range []string{"sequence", "selector", "parallel"} {
		if err := g.Load(impl.Node{ID: "root", Kind: kind}); !errors.Is(err, impl.ErrEmptyChildren) {
			t.Fatalf("%s without children error = %v, want ErrEmptyChildren", kind, err)
		}
	}
}

func TestTickBeforeLoad(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	if _, err := g.Tick("a1", 0); !errors.Is(err, impl.ErrNotLoaded) {
		t.Fatalf("tick before load error = %v, want ErrNotLoaded", err)
	}
	if got := g.Loaded(); got {
		t.Fatal("runner reports loaded before any tree was installed")
	}
}

func TestConditionSuccess(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20, Blackboard: map[string]int{"hp": 80}})
	loadTree(t, g, impl.Node{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 50})
	got, err := g.Tick("a1", 7)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != impl.StatusSuccess {
		t.Fatalf("status = %v, want StatusSuccess", got)
	}
	if ids := traceIDs(g.Trace("a1")); !equalIDs(ids, []string{"c1"}) {
		t.Fatalf("evaluated nodes = %v, want [c1]", ids)
	}
	if g.Trace("a1")[0].At != 7 {
		t.Fatalf("step time = %d, want 7", g.Trace("a1")[0].At)
	}
}

func TestConditionFailure(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20, Blackboard: map[string]int{"hp": 80}})
	loadTree(t, g, impl.Node{ID: "c1", Kind: "condition", Key: "hp", Op: "lt", Value: 50})
	got, err := g.Tick("a1", 0)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != impl.StatusFailure {
		t.Fatalf("status = %v, want StatusFailure", got)
	}
}

func TestSequenceRunsChildrenInOrder(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	loadTree(t, g, impl.Node{ID: "root", Kind: "sequence", Children: []impl.Node{
		{ID: "s1", Kind: "set", Key: "a", Value: 1},
		{ID: "c1", Kind: "condition", Key: "a", Op: "eq", Value: 1},
		{ID: "s2", Kind: "set", Key: "b", Value: 2},
	}})
	got, err := g.Tick("a1", 0)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != impl.StatusSuccess {
		t.Fatalf("status = %v, want StatusSuccess", got)
	}
	if ids := traceIDs(g.Trace("a1")); !equalIDs(ids, []string{"s1", "c1", "s2"}) {
		t.Fatalf("evaluated nodes = %v, want [s1 c1 s2]", ids)
	}
	board := g.Blackboard("a1")
	if board["a"] != 1 || board["b"] != 2 {
		t.Fatalf("blackboard = %v, want a=1 b=2", board)
	}
}

func TestSequenceStopsAtFirstFailure(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20, Blackboard: map[string]int{"hp": 80}})
	loadTree(t, g, impl.Node{ID: "root", Kind: "sequence", Children: []impl.Node{
		{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 100},
		{ID: "s1", Kind: "set", Key: "b", Value: 2},
	}})
	got, err := g.Tick("a1", 0)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != impl.StatusFailure {
		t.Fatalf("status = %v, want StatusFailure", got)
	}
	if ids := traceIDs(g.Trace("a1")); !equalIDs(ids, []string{"c1"}) {
		t.Fatalf("evaluated nodes = %v, want only [c1]", ids)
	}
	if _, ok := g.Blackboard("a1")["b"]; ok {
		t.Fatal("a child after the failing condition was executed")
	}
}

func TestSelectorFallsThrough(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20, Blackboard: map[string]int{"hp": 80}})
	loadTree(t, g, impl.Node{ID: "root", Kind: "selector", Children: []impl.Node{
		{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 100},
		{ID: "s1", Kind: "set", Key: "b", Value: 5},
	}})
	got, err := g.Tick("a1", 0)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != impl.StatusSuccess {
		t.Fatalf("status = %v, want StatusSuccess", got)
	}
	if ids := traceIDs(g.Trace("a1")); !equalIDs(ids, []string{"c1", "s1"}) {
		t.Fatalf("evaluated nodes = %v, want [c1 s1]", ids)
	}
	if got := g.Blackboard("a1")["b"]; got != 5 {
		t.Fatalf("blackboard b = %d, want 5", got)
	}
}

func TestParallelTicksEveryChild(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20, Blackboard: map[string]int{"hp": 80}})
	loadTree(t, g, impl.Node{ID: "root", Kind: "parallel", Children: []impl.Node{
		{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 10},
		{ID: "c2", Kind: "condition", Key: "hp", Op: "lt", Value: 100},
	}})
	got, err := g.Tick("a1", 0)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != impl.StatusSuccess {
		t.Fatalf("status = %v, want StatusSuccess", got)
	}
	if ids := traceIDs(g.Trace("a1")); !equalIDs(ids, []string{"c1", "c2"}) {
		t.Fatalf("evaluated nodes = %v, want [c1 c2]", ids)
	}
}

func TestSetBudgetRejectsBadValues(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	loadTree(t, g, impl.Node{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 0})
	if _, err := g.Tick("a1", 0); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := g.SetBudget("a1", 0); !errors.Is(err, impl.ErrInvalidBudget) {
		t.Fatalf("zero budget error = %v, want ErrInvalidBudget", err)
	}
	if err := g.SetBudget("ghost", 5); !errors.Is(err, impl.ErrUnknownAgent) {
		t.Fatalf("budget of unknown agent error = %v, want ErrUnknownAgent", err)
	}
}

func TestInterruptRequiresRunningAgent(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	loadTree(t, g, impl.Node{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 0})
	if err := g.Interrupt("ghost", "manual"); !errors.Is(err, impl.ErrUnknownAgent) {
		t.Fatalf("interrupt of unknown agent error = %v, want ErrUnknownAgent", err)
	}
	if _, err := g.Tick("a1", 0); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := g.Interrupt("a1", "manual"); !errors.Is(err, impl.ErrNotRunning) {
		t.Fatalf("interrupt of an idle agent error = %v, want ErrNotRunning", err)
	}
}

func TestBlackboardIsPerAgentCopy(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	loadTree(t, g, impl.Node{ID: "s1", Kind: "set", Key: "gold", Value: 7})
	if err := g.SetBlackboard("a", map[string]int{"gold": 1}); err != nil {
		t.Fatalf("SetBlackboard a: %v", err)
	}
	if err := g.SetBlackboard("b", map[string]int{"gold": 2}); err != nil {
		t.Fatalf("SetBlackboard b: %v", err)
	}
	if _, err := g.Tick("a", 0); err != nil {
		t.Fatalf("Tick a: %v", err)
	}
	if got := g.Blackboard("a")["gold"]; got != 7 {
		t.Fatalf("agent a gold = %d, want 7", got)
	}
	if got := g.Blackboard("b")["gold"]; got != 2 {
		t.Fatalf("agent b gold = %d, want the untouched 2", got)
	}
	board := g.Blackboard("a")
	board["gold"] = 99
	if got := g.Blackboard("a")["gold"]; got != 7 {
		t.Fatalf("mutating a returned blackboard changed the stored value to %d", got)
	}
}

func TestTraceIsBoundedAndOrdered(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 3, Blackboard: map[string]int{"hp": 5}})
	loadTree(t, g, impl.Node{ID: "c1", Kind: "condition", Key: "hp", Op: "gt", Value: 0})
	for at := int64(1); at <= 5; at++ {
		if _, err := g.Tick("a1", at); err != nil {
			t.Fatalf("Tick at %d: %v", at, err)
		}
	}
	steps := g.Trace("a1")
	if len(steps) != 3 {
		t.Fatalf("trace length = %d, want the limit 3", len(steps))
	}
	for i, step := range steps {
		if step.NodeID != "c1" {
			t.Fatalf("step %d node = %q, want c1", i, step.NodeID)
		}
		if step.Status != impl.StatusSuccess {
			t.Fatalf("step %d status = %v, want StatusSuccess", i, step.Status)
		}
		if i > 0 && step.At < steps[i-1].At {
			t.Fatalf("trace time went backwards at step %d: %d after %d", i, step.At, steps[i-1].At)
		}
	}
	if steps[0].At != 3 || steps[2].At != 5 {
		t.Fatalf("trace times = [%d %d %d], want the newest [3 4 5]", steps[0].At, steps[1].At, steps[2].At)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	g := newRunner(impl.Options{Budget: 20, TraceLimit: 20})
	loadTree(t, g, impl.Node{ID: "root", Kind: "sequence", Children: []impl.Node{
		{ID: "s1", Kind: "set", Key: "a", Value: 1},
		{ID: "c1", Kind: "condition", Key: "a", Op: "eq", Value: 1},
	}})
	if _, err := g.Tick("a1", 1); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := g.Tick("a1", 2); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if len(g.Trace("a1")) != 4 {
		t.Fatalf("trace length before restore = %d, want 4", len(g.Trace("a1")))
	}
	if err := g.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(g.Trace("a1")) != 2 {
		t.Fatalf("trace length after restore = %d, want 2", len(g.Trace("a1")))
	}
	if got := g.Blackboard("a1")["a"]; got != 1 {
		t.Fatalf("blackboard a after restore = %d, want 1", got)
	}
	if got := g.State("a1"); got != impl.StatusSuccess {
		t.Fatalf("state after restore = %v, want StatusSuccess", got)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot error = %v, want ErrBadSnapshot", err)
	}
	if len(g.Trace("a1")) != 2 {
		t.Fatalf("trace length after a rejected restore = %d, want 2", len(g.Trace("a1")))
	}
}
