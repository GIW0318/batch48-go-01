// Package task redistributes connected, static and tunnel learned routes into
// one published tunnel routing table. The write path rebuilds and publishes a
// complete snapshot, the read path only ever observes a whole table.
package task

import (
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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
	sources map[string]sourceEntry
	gen     uint64
	table   atomic.Value
}

// NewRouteRedistributor builds an empty redistributor with the supplied policy.
func NewRouteRedistributor(p Policy) *RouteRedistributor {
	return &RouteRedistributor{
		policy:  p,
		sources: make(map[string]sourceEntry),
	}
}

// AddSource imports or replaces every route of one source.
func (r *RouteRedistributor) AddSource(kind SourceKind, routes []Route) error {
	return errNotImplemented
}

// WithdrawSource removes one source and every route derived from it.
func (r *RouteRedistributor) WithdrawSource(kind SourceKind, id string) error {
	return errNotImplemented
}

// SetPolicy replaces the redistribution policy and republishes the table.
func (r *RouteRedistributor) SetPolicy(p Policy) error {
	return errNotImplemented
}

// Rebuild recomputes the complete table, publishes it and returns it.
func (r *RouteRedistributor) Rebuild() ([]Route, error) {
	return nil, errNotImplemented
}

// Lookup resolves a destination through the currently published table with
// longest prefix match.
func (r *RouteRedistributor) Lookup(dst netip.Addr) (Route, bool) {
	return Route{}, false
}

// Sources lists every imported source ordered by kind and identifier.
func (r *RouteRedistributor) Sources() []SourceInfo {
	return nil
}

// Stats reports source and publication counters.
func (r *RouteRedistributor) Stats() RedistributorStats {
	return RedistributorStats{}
}

// Snapshot serialises the sources, their versions and the policy.
func (r *RouteRedistributor) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the source table and the policy with a snapshot.
func (r *RouteRedistributor) Restore(data []byte) error {
	return errNotImplemented
}
