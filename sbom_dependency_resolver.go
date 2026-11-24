package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Sentinel errors reported by the resolver.
var (
	ErrInvalidDocument   = errors.New("sbom_dependency_resolver: invalid document")
	ErrDuplicateDocument = errors.New("sbom_dependency_resolver: duplicate document")
	ErrDocumentLimit     = errors.New("sbom_dependency_resolver: document limit reached")
	ErrUnknownCoordinate = errors.New("sbom_dependency_resolver: unknown coordinate")
	ErrCyclicDependency  = errors.New("sbom_dependency_resolver: cyclic dependency")
	ErrDepthLimit        = errors.New("sbom_dependency_resolver: depth limit reached")
	ErrNodeLimit         = errors.New("sbom_dependency_resolver: node limit reached")
	ErrVersionConflict   = errors.New("sbom_dependency_resolver: version conflict")
	ErrBadSnapshot       = errors.New("sbom_dependency_resolver: bad snapshot")
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

type catalogVersion struct {
	version    Version
	text       string
	licenses   []string
	licenseSet map[string]struct{}
	scopes     []string
	scopeSet   map[string]struct{}
}

type catalog map[string]map[string]*catalogVersion

type edgeKey struct {
	from       string
	to         string
	constraint string
	scope      string
}

type dependencyArc struct {
	to    string
	scope string
}

type dependencyGraph map[string][]dependencyArc

type resolvedConstraint struct {
	op      string
	version Version
	text    string
}

type snapshotCatalogEntry struct {
	Coordinate string   `json:"coordinate"`
	Version    string   `json:"version"`
	Licenses   []string `json:"licenses"`
	Scopes     []string `json:"scopes"`
}

type resolverSnapshot struct {
	Format    string                 `json:"format"`
	Options   ResolverOptions        `json:"options"`
	Documents []SBOM                 `json:"documents"`
	Catalog   []snapshotCatalogEntry `json:"catalog"`
	Conflicts []Conflict             `json:"conflicts"`
	Stats     ResolverStats          `json:"stats"`
	Graph     Graph                  `json:"graph"`
}

const snapshotFormat = "sbom-dependency-resolver/v1"

// SbomDependencyResolver merges SBOM documents into one graph.
type SbomDependencyResolver struct {
	mu         sync.RWMutex
	opts       ResolverOptions
	docs       []SBOM
	names      map[string]struct{}
	catalog    catalog
	edges      []Edge
	roots      []Ref
	conflicts  []Conflict
	lastGraph  Graph
	generation uint64
}

// NewSbomDependencyResolver builds a resolver with the given options.
func NewSbomDependencyResolver(opts ResolverOptions) *SbomDependencyResolver {
	if opts.MaxDocuments == 0 {
		opts.MaxDocuments = 8
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 16
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = 4096
	}
	return &SbomDependencyResolver{
		opts:    opts,
		names:   make(map[string]struct{}),
		catalog: make(catalog),
	}
}

// ParseVersion parses a strict MAJOR.MINOR.PATCH triplet.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}

	var values [3]int
	for i, part := range parts {
		if part == "" {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return Version{}, fmt.Errorf("invalid version %q", s)
			}
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q: %w", s, err)
		}
		values[i] = value
	}
	return Version{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

// Coordinate renders the canonical "group/name" coordinate.
func Coordinate(r Ref) string {
	group := strings.ToLower(strings.TrimSpace(r.Group))
	name := strings.ToLower(strings.TrimSpace(r.Name))
	if group == "" || name == "" {
		return ""
	}
	return group + "/" + name
}

// AddDocument registers one SBOM document.
func (r *SbomDependencyResolver) AddDocument(d SBOM) error {
	document, err := normalizeDocument(d)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDocument, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.names[document.Name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateDocument, document.Name)
	}
	if len(r.docs) >= r.opts.MaxDocuments {
		return ErrDocumentLimit
	}

	documents := make([]SBOM, len(r.docs)+1)
	copy(documents, r.docs)
	documents[len(r.docs)] = document

	catalog, edges, roots := buildState(documents)
	names := make(map[string]struct{}, len(r.names)+1)
	for name := range r.names {
		names[name] = struct{}{}
	}
	names[document.Name] = struct{}{}

	r.docs = documents
	r.names = names
	r.catalog = catalog
	r.edges = edges
	r.roots = roots
	r.conflicts = nil
	r.lastGraph = Graph{}
	r.generation++
	return nil
}

// Documents returns how many documents are registered.
func (r *SbomDependencyResolver) Documents() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.docs)
}

// Closure returns the dependency closure of one coordinate.
func (r *SbomDependencyResolver) Closure(ref Ref) ([]Node, error) {
	root := Coordinate(ref)
	if root == "" {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCoordinate, root)
	}

	r.mu.RLock()
	catalog := r.catalog
	edges := r.edges
	opts := r.opts
	r.mu.RUnlock()

	graph := buildDependencyGraph(edges)
	if !catalogHasCoordinate(catalog, root) && !graphHasEndpoint(graph, root) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCoordinate, root)
	}

	depths, err := breadthFirstDepths([]string{root}, graph, opts.MaxDepth, opts.MaxNodes)
	if err != nil {
		return nil, err
	}
	if cycle := findCycle([]string{root}, graph); cycle != nil {
		return nil, cycleError(cycle)
	}

	runtime := runtimeReachable([]string{root}, graph, catalog)
	nodes := make([]Node, 0, len(depths))
	for coordinate, depth := range depths {
		version, license := highestCatalogVersion(catalog[coordinate])
		scope := "dev"
		if runtime[coordinate] {
			scope = "runtime"
		}
		nodes = append(nodes, Node{
			Coordinate: coordinate,
			Version:    version,
			License:    license,
			Depth:      depth,
			Scope:      scope,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Depth != nodes[j].Depth {
			return nodes[i].Depth < nodes[j].Depth
		}
		return nodes[i].Coordinate < nodes[j].Coordinate
	})
	return nodes, nil
}

// Resolve solves versions for every reachable coordinate.
func (r *SbomDependencyResolver) Resolve() (Graph, error) {
	for {
		r.mu.RLock()
		generation := r.generation
		catalog := r.catalog
		edges := r.edges
		roots := r.roots
		opts := r.opts
		r.mu.RUnlock()

		graph, conflicts, err := solve(catalog, edges, roots, opts)

		r.mu.Lock()
		if generation != r.generation {
			r.mu.Unlock()
			continue
		}
		r.conflicts = cloneConflicts(conflicts)
		r.lastGraph = cloneGraph(graph)
		r.mu.Unlock()
		return graph, err
	}
}

// Explain renders a human readable conflict description.
func (r *SbomDependencyResolver) Explain(c Conflict) string {
	if len(c.Constraints) > 0 {
		constraints := append([]string(nil), c.Constraints...)
		sort.Strings(constraints)
		return c.Coordinate + ": no version satisfies " + strings.Join(constraints, ", ")
	}
	if len(c.Licenses) > 0 {
		return c.Coordinate + ": divergent licenses " + strings.Join(c.Licenses, ", ")
	}
	if c.Reason != "" {
		return c.Coordinate + ": " + c.Reason
	}
	return c.Coordinate + ": unresolved"
}

// Conflicts returns the conflicts found by the last Resolve.
func (r *SbomDependencyResolver) Conflicts() []Conflict {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneConflicts(r.conflicts)
}

// Stats returns a consistent snapshot of the counters.
func (r *SbomDependencyResolver) Stats() ResolverStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return statsLocked(r.docs, r.catalog, r.edges, r.lastGraph)
}

// Reset drops every document and cached result.
func (r *SbomDependencyResolver) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.docs = nil
	r.names = make(map[string]struct{})
	r.catalog = make(catalog)
	r.edges = nil
	r.roots = nil
	r.conflicts = nil
	r.lastGraph = Graph{}
	r.generation++
	return nil
}

// Snapshot serialises the resolver state.
func (r *SbomDependencyResolver) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot := resolverSnapshot{
		Format:    snapshotFormat,
		Options:   r.opts,
		Documents: cloneDocuments(r.docs),
		Catalog:   catalogEntries(r.catalog),
		Conflicts: cloneConflicts(r.conflicts),
		Stats:     statsLocked(r.docs, r.catalog, r.edges, r.lastGraph),
		Graph:     cloneGraph(r.lastGraph),
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return data, nil
}

// Restore loads a state produced by Snapshot.
func (r *SbomDependencyResolver) Restore(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var snapshot resolverSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if snapshot.Format != snapshotFormat {
		return fmt.Errorf("%w: unsupported format", ErrBadSnapshot)
	}
	if snapshot.Options.MaxDocuments == 0 {
		snapshot.Options.MaxDocuments = 8
	}
	if snapshot.Options.MaxDepth == 0 {
		snapshot.Options.MaxDepth = 16
	}
	if snapshot.Options.MaxNodes == 0 {
		snapshot.Options.MaxNodes = 4096
	}

	documents := make([]SBOM, 0, len(snapshot.Documents))
	names := make(map[string]struct{}, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		normalized, err := normalizeDocument(document)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
		}
		if _, exists := names[normalized.Name]; exists {
			return fmt.Errorf("%w: duplicate document %s", ErrBadSnapshot, normalized.Name)
		}
		names[normalized.Name] = struct{}{}
		documents = append(documents, normalized)
	}

	catalog, edges, roots := buildState(documents)
	if !reflect.DeepEqual(snapshot.Catalog, catalogEntries(catalog)) {
		return fmt.Errorf("%w: catalog does not match documents", ErrBadSnapshot)
	}
	stats := statsLocked(documents, catalog, edges, snapshot.Graph)
	if snapshot.Stats != stats {
		return fmt.Errorf("%w: statistics do not match documents", ErrBadSnapshot)
	}

	r.docs = documents
	r.names = names
	r.catalog = catalog
	r.edges = edges
	r.roots = roots
	r.conflicts = cloneConflicts(snapshot.Conflicts)
	r.lastGraph = cloneGraph(snapshot.Graph)
	r.opts = snapshot.Options
	r.generation++
	return nil
}

func normalizeDocument(d SBOM) (SBOM, error) {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return SBOM{}, errors.New("document name is empty")
	}

	document := SBOM{
		Name:       name,
		Roots:      make([]Ref, 0, len(d.Roots)),
		Components: make([]Component, 0, len(d.Components)),
		Edges:      make([]Edge, 0, len(d.Edges)),
	}

	for _, root := range d.Roots {
		normalized, ok := normalizeRef(root)
		if !ok {
			return SBOM{}, fmt.Errorf("invalid root coordinate %q/%q", root.Group, root.Name)
		}
		document.Roots = append(document.Roots, normalized)
	}

	for _, component := range d.Components {
		ref, ok := normalizeRef(Ref{Group: component.Group, Name: component.Name})
		if !ok {
			return SBOM{}, fmt.Errorf("invalid component coordinate %q/%q", component.Group, component.Name)
		}
		version, err := ParseVersion(component.Version)
		if err != nil {
			return SBOM{}, fmt.Errorf("component %s/%s: %v", ref.Group, ref.Name, err)
		}
		document.Components = append(document.Components, Component{
			Group:   ref.Group,
			Name:    ref.Name,
			Version: formatVersion(version),
			License: strings.TrimSpace(component.License),
			Scope:   normalizeScope(component.Scope),
		})
	}

	for _, edge := range d.Edges {
		from, fromOK := normalizeRef(edge.From)
		to, toOK := normalizeRef(edge.To)
		if !fromOK {
			return SBOM{}, fmt.Errorf("invalid edge source %q/%q", edge.From.Group, edge.From.Name)
		}
		if !toOK {
			return SBOM{}, fmt.Errorf("invalid edge target %q/%q", edge.To.Group, edge.To.Name)
		}
		constraint, err := parseConstraint(edge.Constraint)
		if err != nil {
			return SBOM{}, err
		}
		document.Edges = append(document.Edges, Edge{
			From:       from,
			To:         to,
			Constraint: constraint.text,
			Scope:      normalizeScope(edge.Scope),
		})
	}
	return document, nil
}

func normalizeRef(r Ref) (Ref, bool) {
	group := strings.ToLower(strings.TrimSpace(r.Group))
	name := strings.ToLower(strings.TrimSpace(r.Name))
	if group == "" || name == "" {
		return Ref{}, false
	}
	return Ref{Group: group, Name: name}, true
}

func normalizeScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return "runtime"
	}
	return scope
}

func formatVersion(version Version) string {
	return fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch)
}

func compareVersions(a, b Version) int {
	switch {
	case a.Major < b.Major:
		return -1
	case a.Major > b.Major:
		return 1
	case a.Minor < b.Minor:
		return -1
	case a.Minor > b.Minor:
		return 1
	case a.Patch < b.Patch:
		return -1
	case a.Patch > b.Patch:
		return 1
	default:
		return 0
	}
}

func parseConstraint(value string) (resolvedConstraint, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return resolvedConstraint{}, errors.New("constraint is empty")
	}

	op := "="
	versionText := text
	switch {
	case strings.HasPrefix(text, ">="):
		op, versionText = ">=", text[2:]
	case strings.HasPrefix(text, "<="):
		op, versionText = "<=", text[2:]
	case strings.HasPrefix(text, ">"):
		op, versionText = ">", text[1:]
	case strings.HasPrefix(text, "<"):
		op, versionText = "<", text[1:]
	case strings.HasPrefix(text, "="):
		op, versionText = "=", text[1:]
	case strings.HasPrefix(text, "^"):
		op, versionText = "^", text[1:]
	case strings.HasPrefix(text, "~"):
		op, versionText = "~", text[1:]
	}
	versionText = strings.TrimSpace(versionText)
	version, err := ParseVersion(versionText)
	if err != nil {
		return resolvedConstraint{}, fmt.Errorf("invalid constraint %q: %v", value, err)
	}
	return resolvedConstraint{
		op:      op,
		version: version,
		text:    op + formatVersion(version),
	}, nil
}

func (c resolvedConstraint) satisfies(version Version) bool {
	comparison := compareVersions(version, c.version)
	switch c.op {
	case "=":
		return comparison == 0
	case ">":
		return comparison > 0
	case ">=":
		return comparison >= 0
	case "<":
		return comparison < 0
	case "<=":
		return comparison <= 0
	case "^":
		return comparison >= 0 && version.Major == c.version.Major
	case "~":
		return comparison >= 0 && version.Major == c.version.Major && version.Minor == c.version.Minor
	default:
		return false
	}
}

func buildState(documents []SBOM) (catalog, []Edge, []Ref) {
	result := make(catalog)
	edgeSet := make(map[edgeKey]Edge)
	rootSet := make(map[string]struct{})
	roots := make([]Ref, 0)

	for _, document := range documents {
		for _, component := range document.Components {
			coordinate := Coordinate(Ref{Group: component.Group, Name: component.Name})
			version, _ := ParseVersion(component.Version)
			versionText := formatVersion(version)

			versions := result[coordinate]
			if versions == nil {
				versions = make(map[string]*catalogVersion)
				result[coordinate] = versions
			}
			entry := versions[versionText]
			if entry == nil {
				entry = &catalogVersion{
					version:    version,
					text:       versionText,
					licenseSet: make(map[string]struct{}),
					scopeSet:   make(map[string]struct{}),
				}
				versions[versionText] = entry
			}
			if component.License != "" {
				if _, exists := entry.licenseSet[component.License]; !exists {
					entry.licenseSet[component.License] = struct{}{}
					entry.licenses = append(entry.licenses, component.License)
				}
			}
			scope := normalizeScope(component.Scope)
			if _, exists := entry.scopeSet[scope]; !exists {
				entry.scopeSet[scope] = struct{}{}
				entry.scopes = append(entry.scopes, scope)
			}
		}

		for _, edge := range document.Edges {
			key := edgeKey{
				from:       Coordinate(edge.From),
				to:         Coordinate(edge.To),
				constraint: edge.Constraint,
				scope:      edge.Scope,
			}
			edgeSet[key] = edge
		}

		for _, root := range document.Roots {
			coordinate := Coordinate(root)
			if _, exists := rootSet[coordinate]; exists {
				continue
			}
			rootSet[coordinate] = struct{}{}
			roots = append(roots, root)
		}
	}

	edges := make([]Edge, 0, len(edgeSet))
	for _, edge := range edgeSet {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		left, right := edges[i], edges[j]
		if Coordinate(left.From) != Coordinate(right.From) {
			return Coordinate(left.From) < Coordinate(right.From)
		}
		if Coordinate(left.To) != Coordinate(right.To) {
			return Coordinate(left.To) < Coordinate(right.To)
		}
		if left.Constraint != right.Constraint {
			return left.Constraint < right.Constraint
		}
		return left.Scope < right.Scope
	})
	return result, edges, roots
}

func buildDependencyGraph(edges []Edge) dependencyGraph {
	arcSet := make(map[string]map[dependencyArc]struct{})
	for _, edge := range edges {
		from := Coordinate(edge.From)
		to := Coordinate(edge.To)
		arcs := arcSet[from]
		if arcs == nil {
			arcs = make(map[dependencyArc]struct{})
			arcSet[from] = arcs
		}
		arcs[dependencyArc{to: to, scope: edge.Scope}] = struct{}{}
	}

	graph := make(dependencyGraph, len(arcSet))
	for from, arcs := range arcSet {
		list := make([]dependencyArc, 0, len(arcs))
		for arc := range arcs {
			list = append(list, arc)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].to != list[j].to {
				return list[i].to < list[j].to
			}
			return list[i].scope < list[j].scope
		})
		graph[from] = list
	}
	return graph
}

func graphTargets(graph dependencyGraph) map[string][]string {
	targets := make(map[string][]string, len(graph))
	for from, arcs := range graph {
		seen := make(map[string]struct{})
		list := make([]string, 0, len(arcs))
		for _, arc := range arcs {
			if _, exists := seen[arc.to]; exists {
				continue
			}
			seen[arc.to] = struct{}{}
			list = append(list, arc.to)
		}
		sort.Strings(list)
		targets[from] = list
	}
	return targets
}

func catalogHasCoordinate(catalog catalog, coordinate string) bool {
	return len(catalog[coordinate]) > 0
}

func graphHasEndpoint(graph dependencyGraph, coordinate string) bool {
	if len(graph[coordinate]) > 0 {
		return true
	}
	for _, arcs := range graph {
		for _, arc := range arcs {
			if arc.to == coordinate {
				return true
			}
		}
	}
	return false
}

func breadthFirstDepths(roots []string, graph dependencyGraph, maxDepth, maxNodes int) (map[string]int, error) {
	queue := append([]string(nil), roots...)
	depths := make(map[string]int, len(roots))
	for _, root := range roots {
		if _, exists := depths[root]; !exists {
			if 0 > maxDepth {
				return nil, ErrDepthLimit
			}
			depths[root] = 0
		}
	}
	if len(depths) > maxNodes {
		return nil, ErrNodeLimit
	}

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		depth := depths[current]
		for _, arc := range graph[current] {
			if _, exists := depths[arc.to]; exists {
				continue
			}
			nextDepth := depth + 1
			if nextDepth > maxDepth {
				return nil, ErrDepthLimit
			}
			depths[arc.to] = nextDepth
			if len(depths) > maxNodes {
				return nil, ErrNodeLimit
			}
			queue = append(queue, arc.to)
		}
	}
	return depths, nil
}

func findCycle(roots []string, graph dependencyGraph) []string {
	targets := graphTargets(graph)
	const (
		white = 0
		gray  = 1
		black = 2
	)
	type frame struct {
		node string
		next int
	}

	colors := make(map[string]int)
	sortedRoots := append([]string(nil), roots...)
	sort.Strings(sortedRoots)
	for _, root := range sortedRoots {
		if colors[root] != white {
			continue
		}

		path := []string{root}
		positions := map[string]int{root: 0}
		stack := []frame{{node: root}}
		colors[root] = gray

		for len(stack) > 0 {
			current := &stack[len(stack)-1]
			children := targets[current.node]
			if current.next >= len(children) {
				colors[current.node] = black
				delete(positions, current.node)
				path = path[:len(path)-1]
				stack = stack[:len(stack)-1]
				continue
			}

			child := children[current.next]
			current.next++
			switch colors[child] {
			case white:
				colors[child] = gray
				positions[child] = len(path)
				path = append(path, child)
				stack = append(stack, frame{node: child})
			case gray:
				start := positions[child]
				cycle := append([]string(nil), path[start:]...)
				cycle = append(cycle, child)
				return cycle
			}
		}
	}
	return nil
}

func cycleError(cycle []string) error {
	return fmt.Errorf("%w: cycle: %s", ErrCyclicDependency, strings.Join(cycle, " -> "))
}

func runtimeReachable(roots []string, graph dependencyGraph, catalog catalog) map[string]bool {
	runtime := make(map[string]bool)
	queue := make([]string, 0, len(roots))
	for _, root := range roots {
		if nodeCanBeRuntime(catalog, root) && !runtime[root] {
			runtime[root] = true
			queue = append(queue, root)
		}
	}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for _, arc := range graph[current] {
			if arc.scope == "dev" {
				continue
			}
			if !nodeCanBeRuntime(catalog, arc.to) || runtime[arc.to] {
				continue
			}
			runtime[arc.to] = true
			queue = append(queue, arc.to)
		}
	}
	return runtime
}

func nodeCanBeRuntime(catalog catalog, coordinate string) bool {
	versions := catalog[coordinate]
	if len(versions) == 0 {
		return true
	}
	for _, version := range versions {
		for _, scope := range version.scopes {
			if scope != "dev" {
				return true
			}
		}
	}
	return false
}

func highestCatalogVersion(versions map[string]*catalogVersion) (string, string) {
	if len(versions) == 0 {
		return "", ""
	}
	var selected *catalogVersion
	for _, version := range versions {
		if selected == nil || compareVersions(version.version, selected.version) > 0 {
			selected = version
		}
	}
	return selected.text, strings.Join(selected.licenses, ", ")
}

func solve(catalog catalog, edges []Edge, roots []Ref, opts ResolverOptions) (Graph, []Conflict, error) {
	graph := Graph{
		Nodes: make([]Node, 0),
		Edges: make([]GraphEdge, 0),
	}

	dependencyGraph := buildDependencyGraph(edges)
	rootCoordinates := make([]string, 0, len(roots))
	rootSet := make(map[string]struct{})
	for _, root := range roots {
		coordinate := Coordinate(root)
		if _, exists := rootSet[coordinate]; exists {
			continue
		}
		rootSet[coordinate] = struct{}{}
		rootCoordinates = append(rootCoordinates, coordinate)
	}
	sort.Strings(rootCoordinates)

	depths, err := breadthFirstDepths(rootCoordinates, dependencyGraph, opts.MaxDepth, opts.MaxNodes)
	if err != nil {
		return graph, nil, err
	}
	if cycle := findCycle(rootCoordinates, dependencyGraph); cycle != nil {
		return graph, nil, cycleError(cycle)
	}

	incoming := make(map[string][]resolvedConstraint)
	for _, edge := range edges {
		from := Coordinate(edge.From)
		to := Coordinate(edge.To)
		if _, reachable := depths[from]; !reachable {
			continue
		}
		if _, reachable := depths[to]; !reachable {
			continue
		}
		constraint, err := parseConstraint(edge.Constraint)
		if err != nil {
			continue
		}
		incoming[to] = append(incoming[to], constraint)
	}

	conflicts := licenseConflicts(catalog)
	selected := make(map[string]*catalogVersion, len(depths))
	var versionConflicts []string
	coordinates := make([]string, 0, len(depths))
	for coordinate := range depths {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)

	for _, coordinate := range coordinates {
		constraints := deduplicateConstraints(incoming[coordinate])
		candidate := highestSatisfyingVersion(catalog[coordinate], constraints)
		if candidate == nil {
			constraintTexts := make([]string, 0, len(constraints))
			for _, constraint := range constraints {
				constraintTexts = append(constraintTexts, constraint.text)
			}
			sort.Strings(constraintTexts)
			conflicts = append(conflicts, Conflict{
				Coordinate:  coordinate,
				Constraints: constraintTexts,
				Reason:      "no version satisfies constraints",
			})
			versionConflicts = append(versionConflicts, coordinate)
			continue
		}
		selected[coordinate] = candidate
	}
	sortConflicts(conflicts)

	if len(versionConflicts) > 0 {
		sort.Strings(versionConflicts)
		return graph, conflicts, fmt.Errorf(
			"%w: %s",
			ErrVersionConflict,
			strings.Join(versionConflicts, ", "),
		)
	}

	runtime := runtimeReachable(rootCoordinates, dependencyGraph, catalog)
	for _, coordinate := range coordinates {
		candidate := selected[coordinate]
		scope := "dev"
		if runtime[coordinate] {
			scope = "runtime"
		}
		graph.Nodes = append(graph.Nodes, Node{
			Coordinate: coordinate,
			Version:    candidate.text,
			License:    strings.Join(candidate.licenses, ", "),
			Depth:      depths[coordinate],
			Scope:      scope,
		})
	}

	edgeSet := make(map[GraphEdge]struct{})
	for _, edge := range edges {
		from := Coordinate(edge.From)
		to := Coordinate(edge.To)
		if _, reachable := depths[from]; !reachable {
			continue
		}
		if _, reachable := depths[to]; !reachable {
			continue
		}
		edgeSet[GraphEdge{From: from, To: to}] = struct{}{}
	}
	for edge := range edgeSet {
		graph.Edges = append(graph.Edges, edge)
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].From != graph.Edges[j].From {
			return graph.Edges[i].From < graph.Edges[j].From
		}
		return graph.Edges[i].To < graph.Edges[j].To
	})
	sort.Slice(graph.Nodes, func(i, j int) bool {
		return graph.Nodes[i].Coordinate < graph.Nodes[j].Coordinate
	})
	return graph, conflicts, nil
}

func deduplicateConstraints(constraints []resolvedConstraint) []resolvedConstraint {
	if len(constraints) == 0 {
		return nil
	}
	byText := make(map[string]resolvedConstraint, len(constraints))
	for _, constraint := range constraints {
		byText[constraint.text] = constraint
	}
	result := make([]resolvedConstraint, 0, len(byText))
	for _, constraint := range byText {
		result = append(result, constraint)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].text < result[j].text
	})
	return result
}

func highestSatisfyingVersion(versions map[string]*catalogVersion, constraints []resolvedConstraint) *catalogVersion {
	var selected *catalogVersion
	for _, version := range versions {
		satisfies := true
		for _, constraint := range constraints {
			if !constraint.satisfies(version.version) {
				satisfies = false
				break
			}
		}
		if !satisfies {
			continue
		}
		if selected == nil || compareVersions(version.version, selected.version) > 0 {
			selected = version
		}
	}
	return selected
}

func licenseConflicts(catalog catalog) []Conflict {
	coordinates := make([]string, 0, len(catalog))
	for coordinate := range catalog {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)

	conflicts := make([]Conflict, 0)
	for _, coordinate := range coordinates {
		versions := make([]*catalogVersion, 0, len(catalog[coordinate]))
		for _, version := range catalog[coordinate] {
			versions = append(versions, version)
		}
		sort.Slice(versions, func(i, j int) bool {
			return compareVersions(versions[i].version, versions[j].version) < 0
		})
		for _, version := range versions {
			if len(version.licenses) <= 1 {
				continue
			}
			conflicts = append(conflicts, Conflict{
				Coordinate: coordinate,
				Licenses:   append([]string(nil), version.licenses...),
				Reason:     "divergent licenses",
			})
		}
	}
	return conflicts
}

func sortConflicts(conflicts []Conflict) {
	sort.Slice(conflicts, func(i, j int) bool {
		left, right := conflicts[i], conflicts[j]
		if left.Coordinate != right.Coordinate {
			return left.Coordinate < right.Coordinate
		}
		if left.Reason != right.Reason {
			return left.Reason < right.Reason
		}
		if strings.Join(left.Constraints, "\x00") != strings.Join(right.Constraints, "\x00") {
			return strings.Join(left.Constraints, "\x00") < strings.Join(right.Constraints, "\x00")
		}
		return strings.Join(left.Licenses, "\x00") < strings.Join(right.Licenses, "\x00")
	})
}

func cloneGraph(graph Graph) Graph {
	return Graph{
		Nodes: append([]Node(nil), graph.Nodes...),
		Edges: append([]GraphEdge(nil), graph.Edges...),
	}
}

func cloneConflicts(conflicts []Conflict) []Conflict {
	if conflicts == nil {
		return nil
	}
	result := make([]Conflict, len(conflicts))
	for i, conflict := range conflicts {
		result[i] = Conflict{
			Coordinate:  conflict.Coordinate,
			Constraints: append([]string(nil), conflict.Constraints...),
			Licenses:    append([]string(nil), conflict.Licenses...),
			Reason:      conflict.Reason,
		}
	}
	return result
}

func cloneDocuments(documents []SBOM) []SBOM {
	if documents == nil {
		return nil
	}
	result := make([]SBOM, len(documents))
	for i, document := range documents {
		result[i] = SBOM{
			Name:       document.Name,
			Roots:      append([]Ref(nil), document.Roots...),
			Components: append([]Component(nil), document.Components...),
			Edges:      append([]Edge(nil), document.Edges...),
		}
	}
	return result
}

func statsLocked(documents []SBOM, catalog catalog, edges []Edge, graph Graph) ResolverStats {
	components := 0
	for _, versions := range catalog {
		components += len(versions)
	}
	return ResolverStats{
		Documents:  len(documents),
		Components: components,
		Edges:      len(edges),
		Nodes:      len(graph.Nodes),
	}
}

func catalogEntries(catalog catalog) []snapshotCatalogEntry {
	coordinates := make([]string, 0, len(catalog))
	for coordinate := range catalog {
		coordinates = append(coordinates, coordinate)
	}
	sort.Strings(coordinates)

	entries := make([]snapshotCatalogEntry, 0)
	for _, coordinate := range coordinates {
		versions := make([]*catalogVersion, 0, len(catalog[coordinate]))
		for _, version := range catalog[coordinate] {
			versions = append(versions, version)
		}
		sort.Slice(versions, func(i, j int) bool {
			return compareVersions(versions[i].version, versions[j].version) < 0
		})
		for _, version := range versions {
			scopes := append([]string(nil), version.scopes...)
			sort.Strings(scopes)
			entries = append(entries, snapshotCatalogEntry{
				Coordinate: coordinate,
				Version:    version.text,
				Licenses:   append([]string(nil), version.licenses...),
				Scopes:     scopes,
			})
		}
	}
	return entries
}
