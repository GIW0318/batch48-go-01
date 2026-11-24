package task

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-15"
)

func constNode(id string, value float64) impl.Node {
	return impl.Node{ID: id, Kind: impl.KindConstant, Params: map[string]float64{"value": value}}
}

func binaryNode(id, kind string) impl.Node {
	return impl.Node{ID: id, Kind: kind}
}

func outPort(id string) impl.Port {
	return impl.Port{Node: id, Name: "out", Type: "float"}
}

func inPort(id, name string) impl.Port {
	return impl.Port{Node: id, Name: name, Type: "float"}
}

func buildChain(t *testing.T, b *impl.MaterialGraphBaker) {
	t.Helper()
	if err := b.AddNode(constNode("c1", 2)); err != nil {
		t.Fatalf("AddNode(c1) returned unexpected error: %v", err)
	}
	if err := b.AddNode(constNode("c2", 3)); err != nil {
		t.Fatalf("AddNode(c2) returned unexpected error: %v", err)
	}
	if err := b.AddNode(binaryNode("n1", impl.KindAdd)); err != nil {
		t.Fatalf("AddNode(n1) returned unexpected error: %v", err)
	}
	if err := b.Connect(outPort("c1"), inPort("n1", "a")); err != nil {
		t.Fatalf("Connect(c1 -> n1.a) returned unexpected error: %v", err)
	}
	if err := b.Connect(outPort("c2"), inPort("n1", "b")); err != nil {
		t.Fatalf("Connect(c2 -> n1.b) returned unexpected error: %v", err)
	}
}

func TestAddNodeRejectsDuplicateID(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(constNode("c1", 1)); err != nil {
		t.Fatalf("first AddNode returned unexpected error: %v", err)
	}
	if err := b.AddNode(constNode("c1", 2)); !errors.Is(err, impl.ErrNodeExists) {
		t.Fatalf("duplicate AddNode: got %v, want ErrNodeExists", err)
	}
	if got := b.Stats().Nodes; got != 1 {
		t.Fatalf("Stats().Nodes after rejected AddNode = %d, want 1", got)
	}
}

func TestAddNodeRejectsBadIDAndKind(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(impl.Node{ID: "", Kind: impl.KindConstant}); !errors.Is(err, impl.ErrBadNode) {
		t.Fatalf("AddNode with empty id: got %v, want ErrBadNode", err)
	}
	if err := b.AddNode(impl.Node{ID: "x", Kind: "noise"}); !errors.Is(err, impl.ErrUnknownKind) {
		t.Fatalf("AddNode with unknown kind: got %v, want ErrUnknownKind", err)
	}
}

func TestConnectRejectsMissingNodes(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(binaryNode("n1", impl.KindAdd)); err != nil {
		t.Fatalf("AddNode returned unexpected error: %v", err)
	}
	err := b.Connect(outPort("ghost"), inPort("n1", "a"))
	if !errors.Is(err, impl.ErrNodeMissing) {
		t.Fatalf("Connect from a missing node: got %v, want ErrNodeMissing", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("Connect error %q does not name the missing node", err.Error())
	}
}

func TestConnectRejectsUnknownPort(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(constNode("c1", 1)); err != nil {
		t.Fatalf("AddNode(c1) returned unexpected error: %v", err)
	}
	if err := b.AddNode(binaryNode("n1", impl.KindAdd)); err != nil {
		t.Fatalf("AddNode(n1) returned unexpected error: %v", err)
	}
	if err := b.Connect(outPort("c1"), inPort("n1", "zzz")); !errors.Is(err, impl.ErrUnknownPort) {
		t.Fatalf("Connect to an unknown input: got %v, want ErrUnknownPort", err)
	}
	badFrom := impl.Port{Node: "c1", Name: "a", Type: "float"}
	if err := b.Connect(badFrom, inPort("n1", "a")); !errors.Is(err, impl.ErrUnknownPort) {
		t.Fatalf("Connect from an unknown output: got %v, want ErrUnknownPort", err)
	}
}

func TestConnectRejectsTypeMismatch(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	color := impl.Node{ID: "tint", Kind: impl.KindConstant, Params: map[string]float64{"value": 1}, OutputType: "color"}
	if err := b.AddNode(color); err != nil {
		t.Fatalf("AddNode(color) returned unexpected error: %v", err)
	}
	if err := b.AddNode(binaryNode("n1", impl.KindAdd)); err != nil {
		t.Fatalf("AddNode(n1) returned unexpected error: %v", err)
	}
	from := impl.Port{Node: "tint", Name: "out", Type: "color"}
	if err := b.Connect(from, inPort("n1", "a")); !errors.Is(err, impl.ErrPortType) {
		t.Fatalf("Connect color to float: got %v, want ErrPortType", err)
	}
}

func TestConnectRejectsCycle(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(binaryNode("n1", impl.KindAdd)); err != nil {
		t.Fatalf("AddNode(n1) returned unexpected error: %v", err)
	}
	if err := b.AddNode(binaryNode("n2", impl.KindMul)); err != nil {
		t.Fatalf("AddNode(n2) returned unexpected error: %v", err)
	}
	if err := b.Connect(outPort("n2"), inPort("n1", "a")); err != nil {
		t.Fatalf("Connect(n2 -> n1.a) returned unexpected error: %v", err)
	}
	if err := b.Connect(outPort("n1"), inPort("n2", "a")); !errors.Is(err, impl.ErrCycle) {
		t.Fatalf("Connect creating a cycle: got %v, want ErrCycle", err)
	}
}

func TestBakeRejectsDanglingInput(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(constNode("c1", 1)); err != nil {
		t.Fatalf("AddNode(c1) returned unexpected error: %v", err)
	}
	if err := b.AddNode(binaryNode("n1", impl.KindAdd)); err != nil {
		t.Fatalf("AddNode(n1) returned unexpected error: %v", err)
	}
	if err := b.Connect(outPort("c1"), inPort("n1", "a")); err != nil {
		t.Fatalf("Connect returned unexpected error: %v", err)
	}
	_, err := b.Bake()
	if !errors.Is(err, impl.ErrDangling) {
		t.Fatalf("Bake with a dangling input: got %v, want ErrDangling", err)
	}
	if !strings.Contains(err.Error(), "n1") {
		t.Fatalf("Bake error %q does not name the dangling node", err.Error())
	}
}

func TestBakeEvaluatesTopologicalOrder(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	buildChain(t, b)
	def, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake returned unexpected error: %v", err)
	}
	if def == nil {
		t.Fatalf("Bake returned a nil material definition")
	}
	if got := def.Values["c1"]; got != 2 {
		t.Fatalf("Values[c1] = %v, want 2", got)
	}
	if got := def.Values["c2"]; got != 3 {
		t.Fatalf("Values[c2] = %v, want 3", got)
	}
	if got := def.Values["n1"]; got != 5 {
		t.Fatalf("Values[n1] = %v, want the sum 5", got)
	}
	if len(def.Order) != 3 {
		t.Fatalf("Order = %v, want three nodes", def.Order)
	}
	pos := map[string]int{}
	for i, id := range def.Order {
		pos[id] = i
	}
	if pos["n1"] < pos["c1"] || pos["n1"] < pos["c2"] {
		t.Fatalf("Order = %v, want both constants before n1", def.Order)
	}
}

func TestBakeIsDeterministic(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	buildChain(t, b)
	first, err := b.Bake()
	if err != nil {
		t.Fatalf("first Bake returned unexpected error: %v", err)
	}
	second, err := b.Bake()
	if err != nil {
		t.Fatalf("second Bake returned unexpected error: %v", err)
	}
	if first.Hash != second.Hash {
		t.Fatalf("Hash changed between identical bakes: %q -> %q", first.Hash, second.Hash)
	}
	if first.Version != second.Version {
		t.Fatalf("Version changed between identical bakes: %d -> %d", first.Version, second.Version)
	}
	if !reflect.DeepEqual(first.Values, second.Values) {
		t.Fatalf("Values changed between identical bakes: %v -> %v", first.Values, second.Values)
	}
	if !reflect.DeepEqual(first.Order, second.Order) {
		t.Fatalf("Order changed between identical bakes: %v -> %v", first.Order, second.Order)
	}
}

func TestKeywordsNormalizeAndAffectHash(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if err := b.AddNode(constNode("c1", 1)); err != nil {
		t.Fatalf("AddNode returned unexpected error: %v", err)
	}
	if err := b.SetKeywords([]string{"Metal", " rough ", "metal"}); err != nil {
		t.Fatalf("SetKeywords returned unexpected error: %v", err)
	}
	want := []string{"metal", "rough"}
	got := b.Keywords()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Keywords = %v, want normalized %v", got, want)
	}
	first, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake with keywords returned unexpected error: %v", err)
	}
	if err := b.SetKeywords([]string{"rough", "metal", "rough"}); err != nil {
		t.Fatalf("SetKeywords reordered returned unexpected error: %v", err)
	}
	same, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake with reordered keywords returned unexpected error: %v", err)
	}
	if first.Hash != same.Hash {
		t.Fatalf("Hash depends on keyword order: %q vs %q", first.Hash, same.Hash)
	}
	if err := b.SetKeywords([]string{"metal"}); err != nil {
		t.Fatalf("SetKeywords changed set returned unexpected error: %v", err)
	}
	different, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake with a different keyword set returned unexpected error: %v", err)
	}
	if different.Hash == first.Hash {
		t.Fatalf("Hash stayed %q after the keyword set changed", first.Hash)
	}
}

func TestInvalidateAdvancesVersion(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	buildChain(t, b)
	first, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake returned unexpected error: %v", err)
	}
	if err := b.Invalidate("does-not-exist"); !errors.Is(err, impl.ErrNodeMissing) {
		t.Fatalf("Invalidate(unknown): got %v, want ErrNodeMissing", err)
	}
	if err := b.Invalidate("c1"); err != nil {
		t.Fatalf("Invalidate(c1) returned unexpected error: %v", err)
	}
	second, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake after Invalidate returned unexpected error: %v", err)
	}
	if second.Version <= first.Version {
		t.Fatalf("Version did not advance after Invalidate: %d -> %d", first.Version, second.Version)
	}
	if got := second.Values["n1"]; got != 5 {
		t.Fatalf("Values[n1] after Invalidate = %v, want 5", got)
	}
}

func TestRemoveNodeRejectsReferencedNode(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	buildChain(t, b)
	if err := b.RemoveNode("c1"); !errors.Is(err, impl.ErrNodeHasDependents) {
		t.Fatalf("RemoveNode(c1): got %v, want ErrNodeHasDependents", err)
	}
	if err := b.RemoveNode("n1"); err != nil {
		t.Fatalf("RemoveNode(n1) returned unexpected error: %v", err)
	}
	if _, ok := b.Node("n1"); ok {
		t.Fatalf("Node(n1) is still present after RemoveNode")
	}
	if got := b.Stats().Nodes; got != 2 {
		t.Fatalf("Stats().Nodes after RemoveNode = %d, want 2", got)
	}
}

func TestBakeRejectsEmptyGraph(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	if _, err := b.Bake(); !errors.Is(err, impl.ErrEmptyGraph) {
		t.Fatalf("Bake with an empty graph: got %v, want ErrEmptyGraph", err)
	}
}

func TestSnapshotRestoreKeepsGraph(t *testing.T) {
	b := impl.NewMaterialGraphBaker("metal")
	buildChain(t, b)
	if err := b.SetKeywords([]string{"Rough"}); err != nil {
		t.Fatalf("SetKeywords returned unexpected error: %v", err)
	}
	first, err := b.Bake()
	if err != nil {
		t.Fatalf("Bake returned unexpected error: %v", err)
	}
	payload, err := b.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned unexpected error: %v", err)
	}
	fresh := impl.NewMaterialGraphBaker("other")
	if err := fresh.Restore(payload); err != nil {
		t.Fatalf("Restore returned unexpected error: %v", err)
	}
	restored, err := fresh.Bake()
	if err != nil {
		t.Fatalf("Bake after Restore returned unexpected error: %v", err)
	}
	if restored.Name != "metal" {
		t.Fatalf("Name after Restore = %q, want metal", restored.Name)
	}
	if restored.Hash != first.Hash {
		t.Fatalf("Hash after Restore = %q, want %q", restored.Hash, first.Hash)
	}
	if err := fresh.Restore([]byte("garbage")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(garbage): got %v, want ErrBadSnapshot", err)
	}
	if again, err := fresh.Bake(); err != nil || again.Hash != first.Hash {
		t.Fatalf("graph changed after a rejected Restore: err=%v", err)
	}
}

func TestConcurrentBakeAndInvalidate(t *testing.T) {
	b := impl.NewMaterialGraphBaker("mat")
	buildChain(t, b)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 4; round++ {
				if _, err := b.Bake(); err != nil {
					t.Errorf("concurrent Bake returned error: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 0; round < 4; round++ {
			if err := b.InvalidateAll(); err != nil {
				t.Errorf("concurrent InvalidateAll returned error: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	def, err := b.Bake()
	if err != nil {
		t.Fatalf("final Bake returned unexpected error: %v", err)
	}
	if def == nil {
		t.Fatalf("final Bake returned a nil definition")
	}
	if got := b.Stats().Nodes; got != 3 {
		t.Fatalf("Stats().Nodes after concurrent bakes = %d, want 3", got)
	}
}
