// Package task redistributes connected, static and tunnel learned routes into
// one published tunnel routing table. The write path rebuilds and publishes a
// complete snapshot, the read path only ever observes a whole table.
package task

import (
	"encoding/json"
	"errors"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Errors returned by the redistributor.
var (
	ErrBadRoute       = errors.New("invalid route")
	ErrBadKind        = errors.New("invalid source kind")
	ErrSourceNotFound = errors.New("source not found")
	ErrKindMismatch   = errors.New("source kind mismatch")
	ErrBadPolicy      = errors.New("invalid policy")
	ErrStaleSnapshot  = errors.New("stale snapshot")
	ErrBadSnapshot    = errors.New("bad snapshot")
)

// SourceKind is the origin class of a redistributed prefix.
type SourceKind string

const (
	// SourceConnected is a directly connected prefix.
	SourceConnected SourceKind = "connected"
	// SourceStatic is an administratively configured prefix.
	SourceStatic SourceKind = "static"
	// SourceTunnel is a prefix learned from a tunnel.
	SourceTunnel SourceKind = "tunnel"
)

// Route is one redistributed prefix as published to the data plane.
type Route struct {
	Prefix      netip.Prefix
	NextHop     netip.Addr
	Metric      int
	SourceID    string
	Kind        SourceKind
	AdvertiseTo []string
}

// Policy controls filtering, metric accumulation and split horizon.
type Policy struct {
	BaseMetric   map[SourceKind]int
	Priority     map[SourceKind]int
	DenyPrefixes []netip.Prefix
	MaxMetric    int
	Targets      []string
	SplitHorizon bool
}

// SourceInfo describes one imported source.
type SourceInfo struct {
	Kind    SourceKind
	ID      string
	Version uint64
	Routes  int
}

// RedistributorStats reports the source and publication counters.
type RedistributorStats struct {
	Sources   int
	Published int
	Version   uint64
}

type sourceKey struct {
	Kind SourceKind
	ID   string
}

type sourceEntry struct {
	Kind    SourceKind
	ID      string
	Version uint64
	Routes  []Route
}

type publishedTable struct {
	Version uint64
	Routes  []Route
}

// RouteRedistributor keeps the imported sources, the policy and the atomic
// published table.
type RouteRedistributor struct {
	mu      sync.Mutex
	policy  Policy
	sources map[sourceKey]sourceEntry
	gen     uint64
	table   atomic.Value
}

const (
	snapshotFormat = "gsb/tunnel-route-redistributor"
	snapshotSchema = 1
)

type snapshotSource struct {
	Kind    SourceKind `json:"kind"`
	ID      string     `json:"id"`
	Version uint64     `json:"version"`
	Routes  []Route    `json:"routes"`
}

type snapshotDoc struct {
	Format           string           `json:"format"`
	Schema           int              `json:"schema"`
	PublishedVersion uint64           `json:"published_version"`
	Policy           Policy           `json:"policy"`
	Sources          []snapshotSource `json:"sources"`
}

// NewRouteRedistributor builds an empty redistributor with the supplied policy.
func NewRouteRedistributor(p Policy) *RouteRedistributor {
	r := &RouteRedistributor{
		policy:  clonePolicy(p),
		sources: make(map[sourceKey]sourceEntry),
	}
	r.table.Store(&publishedTable{Version: 0, Routes: []Route{}})
	return r
}

// AddSource imports or replaces every route of one source.
func (r *RouteRedistributor) AddSource(kind SourceKind, routes []Route) error {
	if !validKind(kind) {
		return ErrBadKind
	}
	if len(routes) == 0 {
		return ErrBadRoute
	}

	normalised := make([]Route, len(routes))
	for i, route := range routes {
		if err := validateRoute(kind, route); err != nil {
			return err
		}
		normalised[i] = normaliseRoute(kind, route.SourceID, route)
	}

	// A call normally carries one source id, but grouping keeps the operation
	// correct and deterministic if several ids are supplied together.
	byID := make(map[string][]Route)
	order := make([]string, 0)
	for _, route := range normalised {
		id := route.SourceID
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = append(byID[id], route)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	next := cloneSources(r.sources)
	for _, id := range order {
		key := sourceKey{Kind: kind, ID: id}
		version := uint64(1)
		if old, ok := next[key]; ok {
			version = old.Version + 1
		}
		next[key] = sourceEntry{
			Kind:    kind,
			ID:      id,
			Version: version,
			Routes:  cloneRoutes(byID[id]),
		}
	}

	table := buildTable(next, r.policy, r.gen+1)
	r.sources = next
	r.gen = table.Version
	r.table.Store(table)
	return nil
}

// WithdrawSource removes one source and every route derived from it.
func (r *RouteRedistributor) WithdrawSource(kind SourceKind, id string) error {
	if !validKind(kind) {
		return ErrBadKind
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := sourceKey{Kind: kind, ID: id}
	if _, ok := r.sources[key]; !ok {
		for existing := range r.sources {
			if existing.ID == id {
				return ErrKindMismatch
			}
		}
		return ErrSourceNotFound
	}

	next := cloneSources(r.sources)
	delete(next, key)
	table := buildTable(next, r.policy, r.gen+1)
	r.sources = next
	r.gen = table.Version
	r.table.Store(table)
	return nil
}

// SetPolicy replaces the redistribution policy and republishes the table.
func (r *RouteRedistributor) SetPolicy(p Policy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	nextPolicy := clonePolicy(p)
	table := buildTable(r.sources, nextPolicy, r.gen+1)
	r.policy = nextPolicy
	r.gen = table.Version
	r.table.Store(table)
	return nil
}

// Rebuild recomputes the complete table, publishes it and returns it.
func (r *RouteRedistributor) Rebuild() ([]Route, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	table := buildTable(r.sources, r.policy, r.gen+1)
	r.gen = table.Version
	r.table.Store(table)
	return cloneRoutes(table.Routes), nil
}

// Lookup resolves a destination through the currently published table with
// longest prefix match.
func (r *RouteRedistributor) Lookup(dst netip.Addr) (Route, bool) {
	table, _ := r.table.Load().(*publishedTable)
	if table == nil || len(table.Routes) == 0 {
		return Route{}, false
	}

	var best Route
	bestBits := -1
	found := false
	for _, route := range table.Routes {
		if !route.Prefix.Contains(dst) {
			continue
		}
		bits := route.Prefix.Bits()
		if !found || bits > bestBits ||
			(bits == bestBits && route.Prefix.String() < best.Prefix.String()) {
			best = route
			bestBits = bits
			found = true
		}
	}
	if !found {
		return Route{}, false
	}
	return cloneRoute(best), true
}

// Sources lists every imported source ordered by kind and identifier.
func (r *RouteRedistributor) Sources() []SourceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	infos := make([]SourceInfo, 0, len(r.sources))
	for _, entry := range r.sources {
		infos = append(infos, SourceInfo{
			Kind:    entry.Kind,
			ID:      entry.ID,
			Version: entry.Version,
			Routes:  len(entry.Routes),
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Kind != infos[j].Kind {
			return infos[i].Kind < infos[j].Kind
		}
		return infos[i].ID < infos[j].ID
	})
	return infos
}

// Stats reports source and publication counters.
func (r *RouteRedistributor) Stats() RedistributorStats {
	r.mu.Lock()
	defer r.mu.Unlock()

	version := r.gen
	published := 0
	if table, ok := r.table.Load().(*publishedTable); ok && table != nil {
		version = table.Version
		published = len(table.Routes)
	}
	return RedistributorStats{
		Sources:   len(r.sources),
		Published: published,
		Version:   version,
	}
}

// Snapshot serialises the sources, their versions and the policy.
func (r *RouteRedistributor) Snapshot() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries := make([]sourceEntry, 0, len(r.sources))
	for _, entry := range r.sources {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].ID < entries[j].ID
	})

	doc := snapshotDoc{
		Format:           snapshotFormat,
		Schema:           snapshotSchema,
		PublishedVersion: r.gen,
		Policy:           clonePolicy(r.policy),
		Sources:          make([]snapshotSource, 0, len(entries)),
	}
	for _, entry := range entries {
		doc.Sources = append(doc.Sources, snapshotSource{
			Kind:    entry.Kind,
			ID:      entry.ID,
			Version: entry.Version,
			Routes:  cloneRoutes(entry.Routes),
		})
	}
	return json.Marshal(doc)
}

// Restore replaces the source table and the policy with a snapshot.
func (r *RouteRedistributor) Restore(data []byte) error {
	var doc snapshotDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return ErrBadSnapshot
	}
	if doc.Format != snapshotFormat || doc.Schema != snapshotSchema {
		return ErrBadSnapshot
	}
	if err := validatePolicy(doc.Policy); err != nil {
		return ErrBadSnapshot
	}

	next := make(map[sourceKey]sourceEntry, len(doc.Sources))
	for _, source := range doc.Sources {
		if !validKind(source.Kind) ||
			strings.TrimSpace(source.ID) == "" ||
			source.Version == 0 ||
			len(source.Routes) == 0 {
			return ErrBadSnapshot
		}
		key := sourceKey{Kind: source.Kind, ID: source.ID}
		if _, duplicate := next[key]; duplicate {
			return ErrBadSnapshot
		}

		routes := make([]Route, 0, len(source.Routes))
		for _, route := range source.Routes {
			if route.SourceID != source.ID {
				return ErrBadSnapshot
			}
			if route.Kind != "" && route.Kind != source.Kind {
				return ErrBadSnapshot
			}
			if err := validateRoute(source.Kind, route); err != nil {
				return ErrBadSnapshot
			}
			routes = append(routes, normaliseRoute(source.Kind, source.ID, route))
		}
		next[key] = sourceEntry{
			Kind:    source.Kind,
			ID:      source.ID,
			Version: source.Version,
			Routes:  routes,
		}
	}

	nextPolicy := clonePolicy(doc.Policy)
	table := buildTable(next, nextPolicy, doc.PublishedVersion)

	r.mu.Lock()
	defer r.mu.Unlock()

	if doc.PublishedVersion < r.gen {
		return ErrStaleSnapshot
	}
	r.sources = next
	r.policy = nextPolicy
	r.gen = table.Version
	r.table.Store(table)
	return nil
}

type routeCandidate struct {
	route     Route
	effective int
	priority  int
}

func buildTable(sources map[sourceKey]sourceEntry, policy Policy, version uint64) *publishedTable {
	keys := make([]sourceKey, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].ID < keys[j].ID
	})

	winners := make(map[netip.Prefix]routeCandidate)
	for _, key := range keys {
		entry := sources[key]
		base := policy.BaseMetric[entry.Kind]
		priority := policy.Priority[entry.Kind]
		for _, stored := range entry.Routes {
			route := stored
			route.Kind = entry.Kind
			route.SourceID = entry.ID
			route.Prefix = route.Prefix.Masked()
			effective := route.Metric + base

			if prefixDenied(route.Prefix, policy.DenyPrefixes) {
				continue
			}
			if policy.MaxMetric > 0 && effective > policy.MaxMetric {
				continue
			}

			route.AdvertiseTo = advertiseTargets(policy, entry.Kind, entry.ID)
			candidate := routeCandidate{
				route:     route,
				effective: effective,
				priority:  priority,
			}
			current, ok := winners[route.Prefix]
			if !ok || candidateBetter(candidate, current) {
				winners[route.Prefix] = candidate
			}
		}
	}

	routes := make([]Route, 0, len(winners))
	for _, candidate := range winners {
		routes = append(routes, candidate.route)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Prefix.String() < routes[j].Prefix.String()
	})
	return &publishedTable{Version: version, Routes: routes}
}

func candidateBetter(candidate, current routeCandidate) bool {
	if candidate.effective != current.effective {
		return candidate.effective < current.effective
	}
	if candidate.priority != current.priority {
		return candidate.priority < current.priority
	}
	if candidate.route.SourceID != current.route.SourceID {
		return candidate.route.SourceID < current.route.SourceID
	}
	if candidate.route.Kind != current.route.Kind {
		return candidate.route.Kind < current.route.Kind
	}
	if cmp := candidate.route.NextHop.Compare(current.route.NextHop); cmp != 0 {
		return cmp < 0
	}
	return candidate.route.Metric < current.route.Metric
}

func advertiseTargets(policy Policy, kind SourceKind, sourceID string) []string {
	targets := make([]string, 0, len(policy.Targets))
	for _, target := range policy.Targets {
		if policy.SplitHorizon && kind == SourceTunnel && target == sourceID {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

func prefixDenied(route netip.Prefix, denies []netip.Prefix) bool {
	for _, deny := range denies {
		if !deny.IsValid() {
			continue
		}
		if deny.Bits() <= route.Bits() && deny.Contains(route.Addr()) {
			return true
		}
	}
	return false
}

func validateRoute(kind SourceKind, route Route) error {
	if !validKind(kind) {
		return ErrBadKind
	}
	if strings.TrimSpace(route.SourceID) == "" {
		return ErrBadRoute
	}
	if !route.Prefix.IsValid() {
		return ErrBadRoute
	}
	if !route.NextHop.IsValid() || route.NextHop.IsUnspecified() {
		return ErrBadRoute
	}
	if route.Metric < 0 {
		return ErrBadRoute
	}
	return nil
}

func validatePolicy(policy Policy) error {
	for _, prefix := range policy.DenyPrefixes {
		if !prefix.IsValid() {
			return ErrBadPolicy
		}
	}
	return nil
}

func validKind(kind SourceKind) bool {
	switch kind {
	case SourceConnected, SourceStatic, SourceTunnel:
		return true
	default:
		return false
	}
}

func normaliseRoute(kind SourceKind, sourceID string, route Route) Route {
	return Route{
		Prefix:   route.Prefix.Masked(),
		NextHop:  route.NextHop,
		Metric:   route.Metric,
		SourceID: sourceID,
		Kind:     kind,
	}
}

func clonePolicy(policy Policy) Policy {
	cloned := Policy{
		MaxMetric:    policy.MaxMetric,
		SplitHorizon: policy.SplitHorizon,
	}
	if policy.BaseMetric != nil {
		cloned.BaseMetric = make(map[SourceKind]int, len(policy.BaseMetric))
		for kind, metric := range policy.BaseMetric {
			cloned.BaseMetric[kind] = metric
		}
	}
	if policy.Priority != nil {
		cloned.Priority = make(map[SourceKind]int, len(policy.Priority))
		for kind, priority := range policy.Priority {
			cloned.Priority[kind] = priority
		}
	}
	if policy.DenyPrefixes != nil {
		cloned.DenyPrefixes = append([]netip.Prefix(nil), policy.DenyPrefixes...)
	}
	if policy.Targets != nil {
		cloned.Targets = append([]string(nil), policy.Targets...)
	}
	return cloned
}

func cloneSources(sources map[sourceKey]sourceEntry) map[sourceKey]sourceEntry {
	cloned := make(map[sourceKey]sourceEntry, len(sources))
	for key, entry := range sources {
		entry.Routes = cloneRoutes(entry.Routes)
		cloned[key] = entry
	}
	return cloned
}

func cloneRoutes(routes []Route) []Route {
	if routes == nil {
		return nil
	}
	cloned := make([]Route, len(routes))
	for i, route := range routes {
		cloned[i] = cloneRoute(route)
	}
	return cloned
}

func cloneRoute(route Route) Route {
	cloned := route
	if route.AdvertiseTo != nil {
		cloned.AdvertiseTo = append([]string(nil), route.AdvertiseTo...)
	}
	return cloned
}
