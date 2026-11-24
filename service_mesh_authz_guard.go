package task

import (
	"container/list"
	"errors"
	"sort"
	"strings"
	"sync"
)

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

// policyView is one immutable generation of policies plus namespace defaults.
// Authorize captures the whole view under lock and evaluates against it without
// holding the lock, so an in-flight request never sees a half-applied set.
type policyView struct {
	generation       int
	policies         []Policy
	namespaceDefault map[string]Effect
}

// cacheEntry stores a decision together with its LRU list element.
type cacheEntry struct {
	key      CacheKey
	decision Decision
	element  *list.Element
}

// defaultCacheCapacity bounds the decision cache when nobody configured one.
const defaultCacheCapacity = 1024

// MeshAuthzGuard evaluates principal policies on the data plane.
type MeshAuthzGuard struct {
	mu sync.RWMutex

	defaultEffect Effect
	view          *policyView

	cache         map[CacheKey]*cacheEntry
	cacheLRU      *list.List
	cacheCapacity int

	connQuota map[string]int
	connUsage map[string]int

	stats AuthzStats
}

// NewMeshAuthzGuard builds a guard with a global fallback action.
func NewMeshAuthzGuard(defaultEffect Effect) *MeshAuthzGuard {
	return &MeshAuthzGuard{
		defaultEffect: defaultEffect,
		view: &policyView{
			generation:       0,
			policies:         []Policy{},
			namespaceDefault: map[string]Effect{},
		},
		cache:         map[CacheKey]*cacheEntry{},
		cacheLRU:      list.New(),
		cacheCapacity: defaultCacheCapacity,
		connQuota:     map[string]int{},
		connUsage:     map[string]int{},
	}
}

// validatePolicySet checks a whole incoming set before any state is touched,
// so a rejected generation leaves the guard completely unchanged.
func validatePolicySet(set PolicySet) error {
	seen := make(map[string]struct{}, len(set.Policies))
	for i := range set.Policies {
		p := set.Policies[i]
		if strings.TrimSpace(p.ID) == "" {
			return ErrInvalidPolicy
		}
		if _, ok := seen[p.ID]; ok {
			return ErrDuplicatePolicy
		}
		seen[p.ID] = struct{}{}
		if p.PathPrefix == "" {
			return ErrInvalidPolicy
		}
		if p.Effect != EffectAllow && p.Effect != EffectDeny {
			return ErrInvalidEffect
		}
	}
	return nil
}

// rankPolicies returns the deterministic precedence order: deny always beats
// allow, then explicit Order, then the more specific (longer) path prefix,
// then the policy id. No map iteration ever participates in the decision.
func rankPolicies(policies []Policy) []Policy {
	ranked := make([]Policy, len(policies))
	copy(ranked, policies)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Effect != b.Effect {
			return a.Effect == EffectDeny
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if len(a.PathPrefix) != len(b.PathPrefix) {
			return len(a.PathPrefix) > len(b.PathPrefix)
		}
		return a.ID < b.ID
	})
	return ranked
}

func policyMatches(p Policy, req AuthRequest) bool {
	return p.Principal == req.Principal &&
		p.Namespace == req.Namespace &&
		p.Workload == req.Workload &&
		p.Method == req.Method &&
		strings.HasPrefix(req.Path, p.PathPrefix)
}

// ApplyPolicySet atomically replaces the whole policy set with a new generation.
func (g *MeshAuthzGuard) ApplyPolicySet(set PolicySet) (ApplyReport, error) {
	if err := validatePolicySet(set); err != nil {
		return ApplyReport{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Generations are strictly consecutive: repeats and gaps both break the
	// monotonic sequence and must not move the guard backwards or forwards.
	if set.Generation != g.view.generation+1 {
		return ApplyReport{}, ErrStaleGeneration
	}

	next := &policyView{
		generation:       set.Generation,
		policies:         rankPolicies(set.Policies),
		namespaceDefault: make(map[string]Effect, len(g.view.namespaceDefault)),
	}
	for ns, eff := range g.view.namespaceDefault {
		next.namespaceDefault[ns] = eff
	}

	// The new generation may invalidate any cached decision; when relevance
	// cannot be proven per key the only safe action is a full cache flush.
	cleared := g.clearCacheLocked()
	g.view = next

	return ApplyReport{
		Generation:   set.Generation,
		Applied:      len(set.Policies),
		CacheCleared: cleared,
	}, nil
}

// Authorize decides one request.
func (g *MeshAuthzGuard) Authorize(req AuthRequest) (Decision, error) {
	g.mu.Lock()

	// Quota is a hard per-connection ceiling enforced before the cache so a
	// cached decision cannot spend past it.
	used := g.connUsage[req.ConnectionID]
	if quota, ok := g.connQuota[req.ConnectionID]; ok && used >= quota {
		g.stats.QuotaRejected++
		g.mu.Unlock()
		return Decision{}, ErrQuotaExceeded
	}

	key := CacheKey{
		Principal: req.Principal,
		Namespace: req.Namespace,
		Workload:  req.Workload,
		Method:    req.Method,
		Path:      req.Path,
	}
	if entry, ok := g.cache[key]; ok {
		g.cacheLRU.MoveToFront(entry.element)
		g.connUsage[req.ConnectionID] = used + 1
		g.stats.Evaluations++
		g.stats.CacheHits++
		cached := entry.decision
		cached.Cached = true
		if cached.Effect == EffectAllow {
			g.stats.Allows++
		} else {
			g.stats.Denies++
		}
		g.mu.Unlock()
		return cached, nil
	}

	// Capture the immutable view the whole decision is rendered against.
	view := g.view
	defaultEffect := g.defaultEffect
	g.stats.CacheMisses++
	g.mu.Unlock()

	decision := evaluate(view, defaultEffect, req)

	g.mu.Lock()
	g.connUsage[req.ConnectionID] = used + 1
	g.stats.Evaluations++
	if decision.Effect == EffectAllow {
		g.stats.Allows++
	} else {
		g.stats.Denies++
	}
	g.putCacheLocked(key, decision)
	g.mu.Unlock()

	return decision, nil
}

// evaluate runs against one immutable policy view and never touches guard
// state, so concurrent callers can never observe a mixed generation.
func evaluate(view *policyView, globalDefault Effect, req AuthRequest) Decision {
	for i := range view.policies {
		p := view.policies[i]
		if policyMatches(p, req) {
			return Decision{
				Effect:     p.Effect,
				PolicyID:   p.ID,
				Generation: view.generation,
				Cached:     false,
				Reason:     "matched policy " + p.ID,
			}
		}
	}

	eff, ok := view.namespaceDefault[req.Namespace]
	if !ok {
		eff = globalDefault
	}
	reason := "namespace default action"
	if !ok {
		reason = "global default action"
	}
	return Decision{
		Effect:     eff,
		PolicyID:   "",
		Generation: view.generation,
		Cached:     false,
		Reason:     reason,
	}
}

// putCacheLocked inserts a decision and enforces the hard capacity ceiling by
// evicting the least recently used entry instead of growing past the limit.
func (g *MeshAuthzGuard) putCacheLocked(key CacheKey, decision Decision) {
	if entry, ok := g.cache[key]; ok {
		entry.decision = decision
		g.cacheLRU.MoveToFront(entry.element)
		return
	}
	if g.cacheCapacity <= 0 {
		return
	}
	for len(g.cache) >= g.cacheCapacity {
		oldest := g.cacheLRU.Back()
		if oldest == nil {
			break
		}
		evicted := oldest.Value.(CacheKey)
		g.cacheLRU.Remove(oldest)
		delete(g.cache, evicted)
	}
	element := g.cacheLRU.PushFront(key)
	g.cache[key] = &cacheEntry{
		key:      key,
		decision: decision,
		element:  element,
	}
}

func (g *MeshAuthzGuard) clearCacheLocked() int {
	removed := len(g.cache)
	g.cache = map[CacheKey]*cacheEntry{}
	g.cacheLRU.Init()
	return removed
}

// Invalidate drops one cached decision and reports how many entries went away.
func (g *MeshAuthzGuard) Invalidate(key CacheKey) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.cache[key]
	if !ok {
		return 0
	}
	g.cacheLRU.Remove(entry.element)
	delete(g.cache, key)
	return 1
}

// SetDefaultAction sets the fallback action of one namespace.
func (g *MeshAuthzGuard) SetDefaultAction(namespace string, eff Effect) error {
	if strings.TrimSpace(namespace) == "" {
		return ErrInvalidConfig
	}
	if eff != EffectAllow && eff != EffectDeny {
		return ErrInvalidEffect
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.view.namespaceDefault[namespace] = eff
	// Defaults participate in every fallback decision, so flush stale ones.
	g.clearCacheLocked()
	return nil
}

// SetConnQuota sets the request ceiling of one connection.
func (g *MeshAuthzGuard) SetConnQuota(conn string, quota int) error {
	if strings.TrimSpace(conn) == "" {
		return ErrInvalidConfig
	}
	if quota < 0 {
		return ErrInvalidConfig
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.connQuota[conn] = quota
	return nil
}

// ConnUsage reports how many requests one connection has spent.
func (g *MeshAuthzGuard) ConnUsage(conn string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.connUsage[conn]
}

// SetCacheCapacity changes the decision cache ceiling.
func (g *MeshAuthzGuard) SetCacheCapacity(n int) error {
	if n < 0 {
		return ErrInvalidConfig
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cacheCapacity = n
	for len(g.cache) > n {
		oldest := g.cacheLRU.Back()
		if oldest == nil {
			break
		}
		evicted := oldest.Value.(CacheKey)
		g.cacheLRU.Remove(oldest)
		delete(g.cache, evicted)
	}
	return nil
}

// CacheSize reports how many decisions are cached.
func (g *MeshAuthzGuard) CacheSize() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.cache)
}

// Generation reports the applied policy generation.
func (g *MeshAuthzGuard) Generation() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.view.generation
}

// PolicyCount reports how many policies are in force.
func (g *MeshAuthzGuard) PolicyCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.view.policies)
}

// Snapshot captures policies, defaults, generation and counters.
func (g *MeshAuthzGuard) Snapshot() (AuthzSnapshot, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	snap := AuthzSnapshot{
		DefaultEffect: g.defaultEffect,
		Generation:    g.view.generation,
		Policies:      append([]Policy(nil), g.view.policies...),
		Stats:         g.stats,
	}
	if len(g.view.namespaceDefault) > 0 {
		snap.NamespaceDefaults = make([]NamespaceDefault, 0, len(g.view.namespaceDefault))
		for ns, eff := range g.view.namespaceDefault {
			snap.NamespaceDefaults = append(snap.NamespaceDefaults, NamespaceDefault{
				Namespace: ns,
				Effect:    eff,
			})
		}
		sort.Slice(snap.NamespaceDefaults, func(i, j int) bool {
			return snap.NamespaceDefaults[i].Namespace < snap.NamespaceDefaults[j].Namespace
		})
	}
	return snap, nil
}

// Restore replaces the guard contents with a snapshot.
func (g *MeshAuthzGuard) Restore(snap AuthzSnapshot) error {
	if snap.Generation <= 0 {
		return ErrBadSnapshot
	}
	if snap.DefaultEffect != EffectAllow && snap.DefaultEffect != EffectDeny {
		return ErrBadSnapshot
	}
	if err := validatePolicySet(PolicySet{
		Generation: snap.Generation,
		Policies:   snap.Policies,
	}); err != nil {
		return errors.Join(ErrBadSnapshot, err)
	}

	namespaceDefault := make(map[string]Effect, len(snap.NamespaceDefaults))
	for _, nd := range snap.NamespaceDefaults {
		if strings.TrimSpace(nd.Namespace) == "" {
			return ErrBadSnapshot
		}
		if nd.Effect != EffectAllow && nd.Effect != EffectDeny {
			return ErrBadSnapshot
		}
		namespaceDefault[nd.Namespace] = nd.Effect
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.defaultEffect = snap.DefaultEffect
	g.view = &policyView{
		generation:       snap.Generation,
		policies:         rankPolicies(snap.Policies),
		namespaceDefault: namespaceDefault,
	}
	g.clearCacheLocked()
	g.connQuota = map[string]int{}
	g.connUsage = map[string]int{}
	g.stats = snap.Stats
	return nil
}

// Stats aggregates evaluation counters.
func (g *MeshAuthzGuard) Stats() AuthzStats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.stats
}
