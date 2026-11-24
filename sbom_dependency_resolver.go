package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the resolver.
var (
	ErrInvalidDocument  = errors.New("sbom_dependency_resolver: invalid document")
	ErrDuplicateDocument = errors.New("sbom_dependency_resolver: duplicate document")
	ErrDocumentLimit    = errors.New("sbom_dependency_resolver: document limit reached")
	ErrUnknownCoordinate = errors.New("sbom_dependency_resolver: unknown coordinate")
	ErrCyclicDependency = errors.New("sbom_dependency_resolver: cyclic dependency")
	ErrDepthLimit       = errors.New("sbom_dependency_resolver: depth limit reached")
	ErrNodeLimit        = errors.New("sbom_dependency_resolver: node limit reached")
	ErrVersionConflict  = errors.New("sbom_dependency_resolver: version conflict")
	ErrBadSnapshot      = errors.New("sbom_dependency_resolver: bad snapshot")
)

// Version is a parsed semantic version triplet.
type Version struct {
	Major int
	Minor int
	Patch int
}

// Component is one catalogued package version.
type Component struct {
	Group   string
	Name    string
	Version string
	License string
	Scope   string
}

// Ref identifies a component coordinate without a version.
type Ref struct {
	Group string
	Name  string
}

// Edge is a dependency range between two coordinates.
type Edge struct {
	From       Ref
	To         Ref
	Constraint string
	Scope      string
}

// SBOM is one dependency document.
type SBOM struct {
	Name       string
	Roots      []Ref
	Components []Component
	Edges      []Edge
}

// Node is one resolved component inside a graph.
type Node struct {
	Coordinate string
	Version    string
	License    string
	Depth      int
	Scope      string
}

// GraphEdge is a deduplicated edge of the resolved graph.
type GraphEdge struct {
	From string
	To   string
}

// Graph is the resolved dependency graph.
type Graph struct {
	Nodes []Node
	Edges []GraphEdge
}

// Conflict explains why a coordinate cannot be resolved.
type Conflict struct {
	Coordinate  string
	Constraints []string
	Licenses    []string
	Reason      string
}

// ResolverOptions tunes document, depth and node limits.
type ResolverOptions struct {
	MaxDocuments int
	MaxDepth     int
	MaxNodes     int
}

// ResolverStats is a consistent view of the resolver counters.
type ResolverStats struct {
	Documents  int
	Components int
	Edges      int
	Nodes      int
}

// SbomDependencyResolver merges SBOM documents into one graph.
type SbomDependencyResolver struct {
	opts ResolverOptions
}

// NewSbomDependencyResolver builds a resolver with the given options.
func NewSbomDependencyResolver(opts ResolverOptions) *SbomDependencyResolver {
	return &SbomDependencyResolver{opts: opts}
}

// ParseVersion parses a strict MAJOR.MINOR.PATCH triplet.
func ParseVersion(s string) (Version, error) {
	return Version{}, errNotImplemented
}

// Coordinate renders the canonical "group/name" coordinate.
func Coordinate(r Ref) string {
	return ""
}

// AddDocument registers one SBOM document.
func (r *SbomDependencyResolver) AddDocument(d SBOM) error {
	return errNotImplemented
}

// Documents returns how many documents are registered.
func (r *SbomDependencyResolver) Documents() int {
	return 0
}

// Closure returns the dependency closure of one coordinate.
func (r *SbomDependencyResolver) Closure(ref Ref) ([]Node, error) {
	return nil, errNotImplemented
}

// Resolve solves versions for every reachable coordinate.
func (r *SbomDependencyResolver) Resolve() (Graph, error) {
	return Graph{}, errNotImplemented
}

// Explain renders a human readable conflict description.
func (r *SbomDependencyResolver) Explain(c Conflict) string {
	return ""
}

// Conflicts returns the conflicts found by the last Resolve.
func (r *SbomDependencyResolver) Conflicts() []Conflict {
	return nil
}

// Stats returns a consistent snapshot of the counters.
func (r *SbomDependencyResolver) Stats() ResolverStats {
	return ResolverStats{}
}

// Reset drops every document and cached result.
func (r *SbomDependencyResolver) Reset() error {
	return errNotImplemented
}

// Snapshot serialises the resolver state.
func (r *SbomDependencyResolver) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a state produced by Snapshot.
func (r *SbomDependencyResolver) Restore(data []byte) error {
	return errNotImplemented
}
