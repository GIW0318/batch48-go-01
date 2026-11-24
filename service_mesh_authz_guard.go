package task

import "errors"

// errNotImplemented is returned by behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the mesh authorization guard.
var (
	ErrInvalidPolicy   = errors.New("service_mesh_authz_guard: invalid policy")
	ErrDuplicatePolicy = errors.New("service_mesh_authz_guard: duplicate policy id")
	ErrStaleGeneration = errors.New("service_mesh_authz_guard: stale policy generation")
	ErrInvalidEffect   = errors.New("service_mesh_authz_guard: invalid effect")
	ErrQuotaExceeded   = errors.New("service_mesh_authz_guard: connection quota exceeded")
	ErrInvalidConfig   = errors.New("service_mesh_authz_guard: invalid configuration")
	ErrBadSnapshot     = errors.New("service_mesh_authz_guard: bad snapshot")
)

// Effect is the outcome of one authorization decision.
type Effect string

// Supported decision effects.
const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Policy is one authorization rule of a policy set.
type Policy struct {
	ID         string
	Principal  string
	Namespace  string
	Workload   string
	Method     string
	PathPrefix string
	Effect     Effect
	Order      int
}

// PolicySet is one complete generation of policies.
type PolicySet struct {
	Generation int
	Policies   []Policy
}

// AuthRequest is one in-flight service-to-service call.
type AuthRequest struct {
	ConnectionID string
	Principal    string
	Namespace    string
	Workload     string
	Method       string
	Path         string
}

// Decision is the authorization outcome of one request.
type Decision struct {
	Effect     Effect
	PolicyID   string
	Generation int
	Cached     bool
	Reason     string
}

// CacheKey identifies one cached decision.
type CacheKey struct {
	Principal string
	Namespace string
	Workload  string
	Method    string
	Path      string
}

// ApplyReport describes one policy set application.
type ApplyReport struct {
	Generation   int
	Applied      int
	CacheCleared int
}

// NamespaceDefault is the fallback action of one namespace.
type NamespaceDefault struct {
	Namespace string
	Effect    Effect
}

// AuthzStats aggregates evaluation counters.
type AuthzStats struct {
	Evaluations   int
	Allows        int
	Denies        int
	CacheHits     int
	CacheMisses   int
	QuotaRejected int
}

// AuthzSnapshot is the portable form of a whole guard.
type AuthzSnapshot struct {
	DefaultEffect     Effect
	Generation        int
	Policies          []Policy
	NamespaceDefaults []NamespaceDefault
	Stats             AuthzStats
}

// MeshAuthzGuard evaluates principal policies on the data plane.
type MeshAuthzGuard struct {
	defaultEffect Effect
}

// NewMeshAuthzGuard builds a guard with a global fallback action.
func NewMeshAuthzGuard(defaultEffect Effect) *MeshAuthzGuard {
	return &MeshAuthzGuard{}
}

// ApplyPolicySet atomically replaces the whole policy set with a new generation.
func (g *MeshAuthzGuard) ApplyPolicySet(set PolicySet) (ApplyReport, error) {
	return ApplyReport{}, errNotImplemented
}

// Authorize decides one request.
func (g *MeshAuthzGuard) Authorize(req AuthRequest) (Decision, error) {
	return Decision{}, errNotImplemented
}

// Invalidate drops one cached decision and reports how many entries went away.
func (g *MeshAuthzGuard) Invalidate(key CacheKey) int {
	return 0
}

// SetDefaultAction sets the fallback action of one namespace.
func (g *MeshAuthzGuard) SetDefaultAction(namespace string, eff Effect) error {
	return errNotImplemented
}

// SetConnQuota sets the request ceiling of one connection.
func (g *MeshAuthzGuard) SetConnQuota(conn string, quota int) error {
	return errNotImplemented
}

// ConnUsage reports how many requests one connection has spent.
func (g *MeshAuthzGuard) ConnUsage(conn string) int {
	return 0
}

// SetCacheCapacity changes the decision cache ceiling.
func (g *MeshAuthzGuard) SetCacheCapacity(n int) error {
	return errNotImplemented
}

// CacheSize reports how many decisions are cached.
func (g *MeshAuthzGuard) CacheSize() int {
	return 0
}

// Generation reports the applied policy generation.
func (g *MeshAuthzGuard) Generation() int {
	return 0
}

// PolicyCount reports how many policies are in force.
func (g *MeshAuthzGuard) PolicyCount() int {
	return 0
}

// Snapshot captures policies, defaults, generation and counters.
func (g *MeshAuthzGuard) Snapshot() (AuthzSnapshot, error) {
	return AuthzSnapshot{}, errNotImplemented
}

// Restore replaces the guard contents with a snapshot.
func (g *MeshAuthzGuard) Restore(snap AuthzSnapshot) error {
	return errNotImplemented
}

// Stats aggregates evaluation counters.
func (g *MeshAuthzGuard) Stats() AuthzStats {
	return AuthzStats{}
}
