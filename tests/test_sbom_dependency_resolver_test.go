package task

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-08"
)

func appRef() impl.Ref  { return impl.Ref{Group: "ACME", Name: "App"} }
func libRef() impl.Ref  { return impl.Ref{Group: "ACME", Name: "Lib"} }
func utilRef() impl.Ref { return impl.Ref{Group: "ACME", Name: "Util"} }

func closureDocument() impl.SBOM {
	return impl.SBOM{
		Name:  "app-sbom",
		Roots: []impl.Ref{appRef()},
		Components: []impl.Component{
			{Group: "ACME", Name: "App", Version: "1.0.0", License: "MIT", Scope: "runtime"},
			{Group: "ACME", Name: "Lib", Version: "1.2.0", License: "Apache-2.0", Scope: "runtime"},
			{Group: "ACME", Name: "Util", Version: "0.9.1", License: "BSD-3-Clause", Scope: "runtime"},
		},
		Edges: []impl.Edge{
			{From: appRef(), To: libRef(), Constraint: ">=1.2.0", Scope: "runtime"},
			{From: libRef(), To: utilRef(), Constraint: "=0.9.1", Scope: "runtime"},
		},
	}
}

func resolver() *impl.SbomDependencyResolver {
	return impl.NewSbomDependencyResolver(impl.ResolverOptions{})
}

func addDoc(t *testing.T, r *impl.SbomDependencyResolver, d impl.SBOM) {
	t.Helper()
	if err := r.AddDocument(d); err != nil {
		t.Fatalf("add document %s: unexpected error %v", d.Name, err)
	}
}

func TestParseVersionAcceptsStrictTriplets(t *testing.T) {
	got, err := impl.ParseVersion("1.2.3")
	if err != nil {
		t.Fatalf("parse 1.2.3: unexpected error %v", err)
	}
	if got != (impl.Version{Major: 1, Minor: 2, Patch: 3}) {
		t.Fatalf("parsed version = %+v", got)
	}
	for _, bad := range []string{"1.2", "1.2.x", "v1.2.3", ""} {
		if _, err := impl.ParseVersion(bad); err == nil {
			t.Fatalf("version %q must be rejected", bad)
		}
	}
}

func TestCoordinateNormalizesGroupAndName(t *testing.T) {
	if got := impl.Coordinate(impl.Ref{Group: "ACME", Name: "Lib"}); got != "acme/lib" {
		t.Fatalf("coordinate = %q, want %q", got, "acme/lib")
	}
	if got := impl.Coordinate(impl.Ref{Group: " Acme ", Name: " Util "}); got != "acme/util" {
		t.Fatalf("trimmed coordinate = %q, want %q", got, "acme/util")
	}
}

func TestAddDocumentValidatesNameAndCounts(t *testing.T) {
	r := resolver()
	addDoc(t, r, closureDocument())
	if got := r.Documents(); got != 1 {
		t.Fatalf("documents = %d, want 1", got)
	}
	if got := r.Stats().Components; got != 3 {
		t.Fatalf("component stat = %d, want 3", got)
	}
	empty := closureDocument()
	empty.Name = ""
	if err := r.AddDocument(empty); !errors.Is(err, impl.ErrInvalidDocument) {
		t.Fatalf("empty name error = %v, want ErrInvalidDocument", err)
	}
	if err := r.AddDocument(closureDocument()); !errors.Is(err, impl.ErrDuplicateDocument) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateDocument", err)
	}
	if got := r.Documents(); got != 1 {
		t.Fatalf("documents after rejections = %d, want 1", got)
	}
}

func TestAddDocumentRejectsMalformedVersionsAndConstraints(t *testing.T) {
	r := resolver()
	badVersion := closureDocument()
	badVersion.Name = "bad-version"
	badVersion.Components[1].Version = "1.x"
	if err := r.AddDocument(badVersion); !errors.Is(err, impl.ErrInvalidDocument) {
		t.Fatalf("bad version error = %v, want ErrInvalidDocument", err)
	}
	badConstraint := closureDocument()
	badConstraint.Name = "bad-constraint"
	badConstraint.Edges[0].Constraint = ">>1.0.0"
	if err := r.AddDocument(badConstraint); !errors.Is(err, impl.ErrInvalidDocument) {
		t.Fatalf("bad constraint error = %v, want ErrInvalidDocument", err)
	}
	if got := r.Documents(); got != 0 {
		t.Fatalf("documents after rejected documents = %d, want 0", got)
	}
}

func TestAddDocumentHonoursDocumentLimit(t *testing.T) {
	r := impl.NewSbomDependencyResolver(impl.ResolverOptions{MaxDocuments: 2})
	for _, name := range []string{"doc-a", "doc-b"} {
		d := closureDocument()
		d.Name = name
		d.Components = d.Components[:1]
		d.Edges = nil
		addDoc(t, r, d)
	}
	third := closureDocument()
	third.Name = "doc-c"
	if err := r.AddDocument(third); !errors.Is(err, impl.ErrDocumentLimit) {
		t.Fatalf("third document error = %v, want ErrDocumentLimit", err)
	}
	if got := r.Documents(); got != 2 {
		t.Fatalf("documents = %d, want 2", got)
	}
}

func TestClosureReturnsDepthOrderedNodes(t *testing.T) {
	r := resolver()
	addDoc(t, r, closureDocument())
	nodes, err := r.Closure(appRef())
	if err != nil {
		t.Fatalf("closure: unexpected error %v", err)
	}
	want := []impl.Node{
		{Coordinate: "acme/app", Version: "1.0.0", License: "MIT", Depth: 0, Scope: "runtime"},
		{Coordinate: "acme/lib", Version: "1.2.0", License: "Apache-2.0", Depth: 1, Scope: "runtime"},
		{Coordinate: "acme/util", Version: "0.9.1", License: "BSD-3-Clause", Depth: 2, Scope: "runtime"},
	}
	if len(nodes) != len(want) {
		t.Fatalf("closure nodes = %d, want %d", len(nodes), len(want))
	}
	for i := range want {
		if nodes[i] != want[i] {
			t.Fatalf("node %d = %+v, want %+v", i, nodes[i], want[i])
		}
	}
}

func TestClosureDeduplicatesMultiplePaths(t *testing.T) {
	r := resolver()
	a := impl.Ref{Group: "ACME", Name: "A"}
	b := impl.Ref{Group: "ACME", Name: "B"}
	c := impl.Ref{Group: "ACME", Name: "C"}
	addDoc(t, r, impl.SBOM{
		Name:  "diamond",
		Roots: []impl.Ref{appRef()},
		Components: []impl.Component{
			{Group: "ACME", Name: "App", Version: "1.0.0", License: "MIT"},
			{Group: "ACME", Name: "A", Version: "1.0.0", License: "MIT"},
			{Group: "ACME", Name: "B", Version: "1.0.0", License: "MIT"},
			{Group: "ACME", Name: "C", Version: "1.0.0", License: "MIT"},
		},
		Edges: []impl.Edge{
			{From: appRef(), To: a, Constraint: "=1.0.0"},
			{From: appRef(), To: b, Constraint: "=1.0.0"},
			{From: a, To: c, Constraint: "=1.0.0"},
			{From: b, To: c, Constraint: "=1.0.0"},
		},
	})
	nodes, err := r.Closure(appRef())
	if err != nil {
		t.Fatalf("closure: unexpected error %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("closure nodes = %d, want 4", len(nodes))
	}
	if nodes[3].Coordinate != "acme/c" || nodes[3].Depth != 2 {
		t.Fatalf("shared node = %+v, want acme/c at depth 2", nodes[3])
	}
}

func TestClosureRejectsUnknownRoot(t *testing.T) {
	r := resolver()
	addDoc(t, r, closureDocument())
	if _, err := r.Closure(impl.Ref{Group: "ACME", Name: "Missing"}); !errors.Is(err, impl.ErrUnknownCoordinate) {
		t.Fatalf("unknown root error = %v, want ErrUnknownCoordinate", err)
	}
}

func TestClosureDetectsCycle(t *testing.T) {
	r := resolver()
	a := impl.Ref{Group: "ACME", Name: "A"}
	b := impl.Ref{Group: "ACME", Name: "B"}
	c := impl.Ref{Group: "ACME", Name: "C"}
	addDoc(t, r, impl.SBOM{
		Name:  "cyclic",
		Roots: []impl.Ref{a},
		Components: []impl.Component{
			{Group: "ACME", Name: "A", Version: "1.0.0"},
			{Group: "ACME", Name: "B", Version: "1.0.0"},
			{Group: "ACME", Name: "C", Version: "1.0.0"},
		},
		Edges: []impl.Edge{
			{From: a, To: b, Constraint: "=1.0.0"},
			{From: b, To: c, Constraint: "=1.0.0"},
			{From: c, To: a, Constraint: "=1.0.0"},
		},
	})
	_, err := r.Closure(a)
	if !errors.Is(err, impl.ErrCyclicDependency) {
		t.Fatalf("cycle error = %v, want ErrCyclicDependency", err)
	}
	message := err.Error()
	if !strings.Contains(message, "acme/a") || !strings.Contains(message, "acme/c") {
		t.Fatalf("cycle message = %q, want ring nodes named", message)
	}
}

func TestClosureEnforcesDepthLimit(t *testing.T) {
	r := impl.NewSbomDependencyResolver(impl.ResolverOptions{MaxDepth: 2})
	r1 := impl.Ref{Group: "ACME", Name: "R"}
	l1 := impl.Ref{Group: "ACME", Name: "L1"}
	l2 := impl.Ref{Group: "ACME", Name: "L2"}
	l3 := impl.Ref{Group: "ACME", Name: "L3"}
	addDoc(t, r, impl.SBOM{
		Name:  "chain",
		Roots: []impl.Ref{r1},
		Components: []impl.Component{
			{Group: "ACME", Name: "R", Version: "1.0.0"},
			{Group: "ACME", Name: "L1", Version: "1.0.0"},
			{Group: "ACME", Name: "L2", Version: "1.0.0"},
			{Group: "ACME", Name: "L3", Version: "1.0.0"},
		},
		Edges: []impl.Edge{
			{From: r1, To: l1, Constraint: "=1.0.0"},
			{From: l1, To: l2, Constraint: "=1.0.0"},
			{From: l2, To: l3, Constraint: "=1.0.0"},
		},
	})
	if _, err := r.Closure(r1); !errors.Is(err, impl.ErrDepthLimit) {
		t.Fatalf("depth limit error = %v, want ErrDepthLimit", err)
	}
}

func TestResolveChoosesHighestSatisfyingVersion(t *testing.T) {
	r := resolver()
	other := impl.Ref{Group: "ACME", Name: "Other"}
	addDoc(t, r, impl.SBOM{
		Name:  "versions",
		Roots: []impl.Ref{appRef()},
		Components: []impl.Component{
			{Group: "ACME", Name: "App", Version: "1.0.0", License: "MIT"},
			{Group: "ACME", Name: "Lib", Version: "1.2.0", License: "Apache-2.0"},
			{Group: "ACME", Name: "Lib", Version: "1.4.0", License: "Apache-2.0"},
			{Group: "ACME", Name: "Lib", Version: "2.0.0", License: "Apache-2.0"},
			{Group: "ACME", Name: "Other", Version: "1.0.0", License: "MIT"},
		},
		Edges: []impl.Edge{
			{From: appRef(), To: libRef(), Constraint: ">=1.2.0"},
			{From: appRef(), To: other, Constraint: "=1.0.0"},
			{From: other, To: libRef(), Constraint: "<2.0.0"},
		},
	})
	graph, err := r.Resolve()
	if err != nil {
		t.Fatalf("resolve: unexpected error %v", err)
	}
	versions := make(map[string]string)
	for _, node := range graph.Nodes {
		versions[node.Coordinate] = node.Version
	}
	if len(versions) != 3 {
		t.Fatalf("graph nodes = %d, want 3", len(versions))
	}
	if versions["acme/lib"] != "1.4.0" {
		t.Fatalf("lib version = %q, want 1.4.0", versions["acme/lib"])
	}
	if versions["acme/app"] != "1.0.0" || versions["acme/other"] != "1.0.0" {
		t.Fatalf("graph versions = %+v", versions)
	}
}

func TestResolveReportsVersionConflictAndExplain(t *testing.T) {
	r := resolver()
	addDoc(t, r, impl.SBOM{
		Name:  "conflict",
		Roots: []impl.Ref{appRef()},
		Components: []impl.Component{
			{Group: "ACME", Name: "App", Version: "1.0.0", License: "MIT"},
			{Group: "ACME", Name: "Lib", Version: "1.2.0", License: "Apache-2.0"},
			{Group: "ACME", Name: "Lib", Version: "1.4.0", License: "Apache-2.0"},
		},
		Edges: []impl.Edge{
			{From: appRef(), To: libRef(), Constraint: ">=1.4.0"},
			{From: appRef(), To: libRef(), Constraint: "<1.4.0"},
		},
	})
	if _, err := r.Resolve(); !errors.Is(err, impl.ErrVersionConflict) {
		t.Fatalf("conflict error = %v, want ErrVersionConflict", err)
	}
	conflicts := r.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	if conflicts[0].Coordinate != "acme/lib" {
		t.Fatalf("conflict coordinate = %q, want acme/lib", conflicts[0].Coordinate)
	}
	if got := conflicts[0].Constraints; !reflect.DeepEqual(got, []string{"<1.4.0", ">=1.4.0"}) {
		t.Fatalf("conflict constraints = %v", got)
	}
	want := "acme/lib: no version satisfies <1.4.0, >=1.4.0"
	if got := r.Explain(conflicts[0]); got != want {
		t.Fatalf("explain = %q, want %q", got, want)
	}
}

func TestResolveDeduplicatesEdgesAcrossDocuments(t *testing.T) {
	r := resolver()
	first := impl.SBOM{
		Name:  "doc-one",
		Roots: []impl.Ref{appRef()},
		Components: []impl.Component{
			{Group: "ACME", Name: "App", Version: "1.0.0", License: "MIT"},
			{Group: "ACME", Name: "Lib", Version: "1.2.0", License: "Apache-2.0"},
			{Group: "ACME", Name: "Other", Version: "1.0.0", License: "MIT"},
		},
		Edges: []impl.Edge{
			{From: appRef(), To: libRef(), Constraint: ">=1.2.0"},
			{From: appRef(), To: impl.Ref{Group: "ACME", Name: "Other"}, Constraint: "=1.0.0"},
		},
	}
	second := impl.SBOM{
		Name:  "doc-two",
		Roots: []impl.Ref{appRef()},
		Components: []impl.Component{
			{Group: "ACME", Name: "Lib", Version: "1.4.0", License: "Apache-2.0"},
		},
		Edges: []impl.Edge{
			{From: appRef(), To: libRef(), Constraint: ">=1.2.0"},
		},
	}
	addDoc(t, r, first)
	addDoc(t, r, second)
	graph, err := r.Resolve()
	if err != nil {
		t.Fatalf("resolve: unexpected error %v", err)
	}
	want := []impl.GraphEdge{
		{From: "acme/app", To: "acme/lib"},
		{From: "acme/app", To: "acme/other"},
	}
	if !reflect.DeepEqual(graph.Edges, want) {
		t.Fatalf("graph edges = %+v, want %+v", graph.Edges, want)
	}
}

func TestSnapshotRestoreKeepsDocumentsAndGraph(t *testing.T) {
	r := resolver()
	addDoc(t, r, closureDocument())
	before, err := r.Resolve()
	if err != nil {
		t.Fatalf("resolve: unexpected error %v", err)
	}
	data, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	restored := resolver()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	if got := restored.Documents(); got != 1 {
		t.Fatalf("restored documents = %d, want 1", got)
	}
	after, err := restored.Resolve()
	if err != nil {
		t.Fatalf("resolve after restore: unexpected error %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("restored graph = %+v, want %+v", after, before)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	r := resolver()
	if err := r.Restore([]byte("not json at all")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore error = %v, want ErrBadSnapshot", err)
	}
	if got := r.Documents(); got != 0 {
		t.Fatalf("documents after failed restore = %d, want 0", got)
	}
}
