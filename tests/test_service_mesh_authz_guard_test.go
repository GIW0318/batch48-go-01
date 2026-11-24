package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-04"
)

func applyPolicies(t *testing.T, g *impl.MeshAuthzGuard, generation int, policies []impl.Policy) {
	t.Helper()
	if _, err := g.ApplyPolicySet(impl.PolicySet{Generation: generation, Policies: policies}); err != nil {
		t.Fatalf("apply policy set generation %d: %v", generation, err)
	}
}

func basePolicy() impl.Policy {
	return impl.Policy{
		ID:         "p1",
		Principal:  "spiffe://acme/web",
		Namespace:  "payments",
		Workload:   "ledger",
		Method:     "GET",
		PathPrefix: "/api/v1",
		Effect:     impl.EffectAllow,
	}
}

func baseRequest() impl.AuthRequest {
	return impl.AuthRequest{
		ConnectionID: "c1",
		Principal:    "spiffe://acme/web",
		Namespace:    "payments",
		Workload:     "ledger",
		Method:       "GET",
		Path:         "/api/v1/orders",
	}
}

func TestApplyPolicySetRejectsStaleGeneration(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	if _, err := g.ApplyPolicySet(impl.PolicySet{Generation: 1, Policies: []impl.Policy{basePolicy()}}); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("repeated generation error = %v, want %v", err, impl.ErrStaleGeneration)
	}
	if _, err := g.ApplyPolicySet(impl.PolicySet{Generation: 3}); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("skipped generation error = %v, want %v", err, impl.ErrStaleGeneration)
	}
	if got := g.Generation(); got != 1 {
		t.Fatalf("generation after rejected sets = %d, want 1", got)
	}
}

func TestApplyPolicySetRejectsDuplicatePolicyID(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	first := basePolicy()
	second := basePolicy()
	second.PathPrefix = "/api/v2"
	_, err := g.ApplyPolicySet(impl.PolicySet{Generation: 1, Policies: []impl.Policy{first, second}})
	if !errors.Is(err, impl.ErrDuplicatePolicy) {
		t.Fatalf("duplicate policy id error = %v, want %v", err, impl.ErrDuplicatePolicy)
	}
}

func TestApplyPolicySetRejectsInvalidEffect(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	bad := basePolicy()
	bad.Effect = impl.Effect("permit")
	if _, err := g.ApplyPolicySet(impl.PolicySet{Generation: 1, Policies: []impl.Policy{bad}}); !errors.Is(err, impl.ErrInvalidEffect) {
		t.Fatalf("invalid effect error = %v, want %v", err, impl.ErrInvalidEffect)
	}
}

func TestAuthorizeAllowsMatchingPolicy(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	d, err := g.Authorize(baseRequest())
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if d.Effect != impl.EffectAllow {
		t.Fatalf("effect = %q, want %q", d.Effect, impl.EffectAllow)
	}
	if d.PolicyID != "p1" {
		t.Fatalf("winning policy = %q, want %q", d.PolicyID, "p1")
	}
	if d.Generation != 1 {
		t.Fatalf("decision generation = %d, want 1", d.Generation)
	}
	if d.Cached {
		t.Fatalf("first decision must not be marked cached")
	}
}

func TestAuthorizeDenyWinsOverAllow(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	allow := impl.Policy{
		ID: "allow-1", Principal: "spiffe://acme/web", Namespace: "payments", Workload: "ledger",
		Method: "GET", PathPrefix: "/api", Effect: impl.EffectAllow, Order: 0,
	}
	deny := impl.Policy{
		ID: "deny-1", Principal: "spiffe://acme/web", Namespace: "payments", Workload: "ledger",
		Method: "GET", PathPrefix: "/api", Effect: impl.EffectDeny, Order: 1,
	}
	applyPolicies(t, g, 1, []impl.Policy{allow, deny})
	req := baseRequest()
	req.Path = "/api/orders"
	d, err := g.Authorize(req)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if d.Effect != impl.EffectDeny {
		t.Fatalf("effect = %q, want %q when deny and allow both match", d.Effect, impl.EffectDeny)
	}
	if d.PolicyID != "deny-1" {
		t.Fatalf("winning policy = %q, want %q", d.PolicyID, "deny-1")
	}
}

func TestAuthorizeFallsBackToNamespaceDefault(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, nil)
	if err := g.SetDefaultAction("payments", impl.EffectAllow); err != nil {
		t.Fatalf("set namespace default: %v", err)
	}
	req := baseRequest()
	d, err := g.Authorize(req)
	if err != nil {
		t.Fatalf("authorize with namespace default: %v", err)
	}
	if d.Effect != impl.EffectAllow {
		t.Fatalf("namespace default effect = %q, want %q", d.Effect, impl.EffectAllow)
	}
	if d.PolicyID != "" {
		t.Fatalf("namespace default decision must not carry a policy id, got %q", d.PolicyID)
	}
	other := req
	other.Namespace = "search"
	d, err = g.Authorize(other)
	if err != nil {
		t.Fatalf("authorize unknown namespace: %v", err)
	}
	if d.Effect != impl.EffectDeny {
		t.Fatalf("unknown namespace effect = %q, want %q", d.Effect, impl.EffectDeny)
	}
}

func TestAuthorizeEnforcesConnectionQuota(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	if err := g.SetConnQuota("c1", 2); err != nil {
		t.Fatalf("set connection quota: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := g.Authorize(baseRequest()); err != nil {
			t.Fatalf("authorize %d: %v", i+1, err)
		}
	}
	if _, err := g.Authorize(baseRequest()); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("over quota error = %v, want %v", err, impl.ErrQuotaExceeded)
	}
	if got := g.ConnUsage("c1"); got != 2 {
		t.Fatalf("connection usage = %d, want 2", got)
	}
}

func TestAuthorizeQuotaIsPerConnection(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	if err := g.SetConnQuota("c1", 1); err != nil {
		t.Fatalf("set c1 quota: %v", err)
	}
	if err := g.SetConnQuota("c2", 3); err != nil {
		t.Fatalf("set c2 quota: %v", err)
	}
	if _, err := g.Authorize(baseRequest()); err != nil {
		t.Fatalf("first c1 request: %v", err)
	}
	req := baseRequest()
	req.ConnectionID = "c2"
	if _, err := g.Authorize(req); err != nil {
		t.Fatalf("c2 request must not inherit c1 usage: %v", err)
	}
	req.ConnectionID = "c1"
	if _, err := g.Authorize(req); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("c1 second request error = %v, want %v", err, impl.ErrQuotaExceeded)
	}
}

func TestAuthorizeCacheHitIsMarked(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	if _, err := g.Authorize(baseRequest()); err != nil {
		t.Fatalf("first authorize: %v", err)
	}
	d, err := g.Authorize(baseRequest())
	if err != nil {
		t.Fatalf("second authorize: %v", err)
	}
	if !d.Cached {
		t.Fatalf("second identical decision must be served from cache")
	}
	if got := g.Stats().CacheHits; got != 1 {
		t.Fatalf("cache hits = %d, want 1", got)
	}
}

func TestInvalidateRemovesCachedKey(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	req := baseRequest()
	if _, err := g.Authorize(req); err != nil {
		t.Fatalf("first authorize: %v", err)
	}
	key := impl.CacheKey{
		Principal: req.Principal,
		Namespace: req.Namespace,
		Workload:  req.Workload,
		Method:    req.Method,
		Path:      req.Path,
	}
	if got := g.Invalidate(key); got != 1 {
		t.Fatalf("invalidated entries = %d, want 1", got)
	}
	d, err := g.Authorize(req)
	if err != nil {
		t.Fatalf("authorize after invalidate: %v", err)
	}
	if d.Cached {
		t.Fatalf("decision after invalidate must be evaluated again, not cached")
	}
}

func TestCacheSizeRespectsCapacity(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	policy := basePolicy()
	policy.PathPrefix = "/api"
	applyPolicies(t, g, 1, []impl.Policy{policy})
	if err := g.SetCacheCapacity(2); err != nil {
		t.Fatalf("set cache capacity: %v", err)
	}
	for _, path := range []string{"/api/a", "/api/b", "/api/c"} {
		req := baseRequest()
		req.Path = path
		if _, err := g.Authorize(req); err != nil {
			t.Fatalf("authorize %s: %v", path, err)
		}
	}
	if got := g.CacheSize(); got != 2 {
		t.Fatalf("cache size = %d, want the ceiling 2", got)
	}
}

func TestPathPrefixMismatchFallsToDefault(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	req := baseRequest()
	req.Path = "/api/v2/orders"
	d, err := g.Authorize(req)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if d.Effect != impl.EffectDeny {
		t.Fatalf("effect = %q, want %q for a path outside the policy prefix", d.Effect, impl.EffectDeny)
	}
	if d.PolicyID != "" {
		t.Fatalf("path mismatch must not blame a policy, got %q", d.PolicyID)
	}
}

func TestSnapshotRestorePreservesDecisions(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	if _, err := g.Authorize(baseRequest()); err != nil {
		t.Fatalf("authorize before snapshot: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewMeshAuthzGuard(impl.EffectAllow)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Generation(); got != 1 {
		t.Fatalf("restored generation = %d, want 1", got)
	}
	if got := restored.Stats().Evaluations; got != 1 {
		t.Fatalf("restored evaluation counter = %d, want 1", got)
	}
	d, err := restored.Authorize(baseRequest())
	if err != nil {
		t.Fatalf("authorize after restore: %v", err)
	}
	if d.Effect != impl.EffectAllow || d.PolicyID != "p1" {
		t.Fatalf("restored decision = %+v, want allow from p1", d)
	}
	unmatched := baseRequest()
	unmatched.Namespace = "search"
	d, err = restored.Authorize(unmatched)
	if err != nil {
		t.Fatalf("authorize unmatched after restore: %v", err)
	}
	if d.Effect != impl.EffectDeny {
		t.Fatalf("restored global default effect = %q, want %q", d.Effect, impl.EffectDeny)
	}
}

func TestRestoreRejectsEmptySnapshot(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	applyPolicies(t, g, 1, []impl.Policy{basePolicy()})
	if err := g.Restore(impl.AuthzSnapshot{}); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("empty snapshot error = %v, want %v", err, impl.ErrBadSnapshot)
	}
	if got := g.Generation(); got != 1 {
		t.Fatalf("generation after rejected restore = %d, want 1", got)
	}
}

func TestStatsCountAllowAndDeny(t *testing.T) {
	g := impl.NewMeshAuthzGuard(impl.EffectDeny)
	allow := basePolicy()
	allow.PathPrefix = "/allow"
	deny := basePolicy()
	deny.ID = "p2"
	deny.PathPrefix = "/deny"
	deny.Effect = impl.EffectDeny
	applyPolicies(t, g, 1, []impl.Policy{allow, deny})
	for _, path := range []string{"/allow/a", "/deny/b", "/other/c"} {
		req := baseRequest()
		req.Path = path
		if _, err := g.Authorize(req); err != nil {
			t.Fatalf("authorize %s: %v", path, err)
		}
	}
	stats := g.Stats()
	if stats.Evaluations != 3 {
		t.Fatalf("evaluations = %d, want 3", stats.Evaluations)
	}
	if stats.Allows != 1 {
		t.Fatalf("allows = %d, want 1", stats.Allows)
	}
	if stats.Denies != 2 {
		t.Fatalf("denies = %d, want 2", stats.Denies)
	}
}
