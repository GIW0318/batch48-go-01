package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the mapper.
var (
	ErrInvalidAsset    = errors.New("attack_surface_mapper: invalid asset")
	ErrAssetLimit      = errors.New("attack_surface_mapper: asset limit reached")
	ErrUnknownAsset     = errors.New("attack_surface_mapper: unknown asset")
	ErrInvalidPolicy   = errors.New("attack_surface_mapper: invalid policy")
	ErrSelfPolicy      = errors.New("attack_surface_mapper: policy from asset to itself")
	ErrDuplicatePolicy = errors.New("attack_surface_mapper: duplicate policy")
	ErrPolicyLimit     = errors.New("attack_surface_mapper: policy limit reached")
	ErrUnknownPolicy   = errors.New("attack_surface_mapper: unknown policy")
	ErrUnknownPort     = errors.New("attack_surface_mapper: unknown port")
	ErrHopLimit        = errors.New("attack_surface_mapper: hop limit reached")
	ErrBadSnapshot     = errors.New("attack_surface_mapper: bad snapshot")
)

// Asset is one host or service in the inventory.
type Asset struct {
	ID      string
	Tenant  string
	Kind    string
	Ports   []int
	Exposed bool
}

// Policy is one allowed network edge.
type Policy struct {
	From        string
	To          string
	Port        int
	Allow       bool
	CrossTenant bool
}

// Entry is the starting point of a reachability query.
type Entry struct {
	Asset string
	Port  int
}

// Path is one shortest route to an exposed asset.
type Path struct {
	Nodes []string
	Hops  int
	Port  int
}

// Exposure is one externally visible asset port.
type Exposure struct {
	Asset  string
	Tenant string
	Port   int
}

// Delta is the difference between the live surface and a snapshot.
type Delta struct {
	Added   []Exposure
	Removed []Exposure
}

// Snapshot is a replayable copy of the whole mapper state.
type Snapshot struct {
	Version   int
	Assets    []Asset
	Policies  []Policy
	Exposures []Exposure
}

// MapperOptions tunes asset, policy and hop limits.
type MapperOptions struct {
	MaxAssets   int
	MaxPolicies int
	MaxHops     int
}

// MapperStats is a consistent view of the mapper counters.
type MapperStats struct {
	Assets    int
	Policies  int
	Exposures int
	Version   int
}

// AttackSurfaceMapper derives the exposed surface from assets and policies.
type AttackSurfaceMapper struct {
	opts MapperOptions
}

// NewAttackSurfaceMapper builds a mapper with the given options.
func NewAttackSurfaceMapper(opts MapperOptions) *AttackSurfaceMapper {
	return &AttackSurfaceMapper{opts: opts}
}

// Upsert inserts or replaces an asset by identifier.
func (m *AttackSurfaceMapper) Upsert(a Asset) error {
	return errNotImplemented
}

// Remove deletes an asset and every policy touching it.
func (m *AttackSurfaceMapper) Remove(id string) error {
	return errNotImplemented
}

// Asset returns the stored asset.
func (m *AttackSurfaceMapper) Asset(id string) (Asset, bool) {
	return Asset{}, false
}

// AddPolicy registers one allowed edge.
func (m *AttackSurfaceMapper) AddPolicy(p Policy) error {
	return errNotImplemented
}

// RemovePolicy deletes one policy by endpoints and port.
func (m *AttackSurfaceMapper) RemovePolicy(from, to string, port int) error {
	return errNotImplemented
}

// Policies returns every policy ordered by endpoints and port.
func (m *AttackSurfaceMapper) Policies() []Policy {
	return nil
}

// Reachable returns the shortest routes to exposed assets.
func (m *AttackSurfaceMapper) Reachable(e Entry) ([]Path, error) {
	return nil, errNotImplemented
}

// Exposures returns the externally visible asset ports.
func (m *AttackSurfaceMapper) Exposures() []Exposure {
	return nil
}

// Diff compares the live surface with a snapshot.
func (m *AttackSurfaceMapper) Diff(s Snapshot) (Delta, error) {
	return Delta{}, errNotImplemented
}

// Snapshot copies the whole mapper state.
func (m *AttackSurfaceMapper) Snapshot() (Snapshot, error) {
	return Snapshot{}, errNotImplemented
}

// Restore replaces the mapper state with a snapshot.
func (m *AttackSurfaceMapper) Restore(s Snapshot) error {
	return errNotImplemented
}

// Stats returns a consistent snapshot of the counters.
func (m *AttackSurfaceMapper) Stats() MapperStats {
	return MapperStats{}
}

// Reset drops every asset, policy and counter.
func (m *AttackSurfaceMapper) Reset() error {
	return errNotImplemented
}
