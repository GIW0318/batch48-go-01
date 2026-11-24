package task

import (
	"errors"
	"reflect"
	"testing"

	impl "gsb/batch48-go-safety-c-10"
)

func newMapper(opts impl.MapperOptions) *impl.AttackSurfaceMapper {
	return impl.NewAttackSurfaceMapper(opts)
}

func asset(id, tenant string, ports []int, exposed bool) impl.Asset {
	return impl.Asset{ID: id, Tenant: tenant, Kind: "host", Ports: ports, Exposed: exposed}
}

func mustUpsert(t *testing.T, m *impl.AttackSurfaceMapper, a impl.Asset) {
	t.Helper()
	if err := m.Upsert(a); err != nil {
		t.Fatalf("upsert %s: unexpected error %v", a.ID, err)
	}
}

func mustPolicy(t *testing.T, m *impl.AttackSurfaceMapper, p impl.Policy) {
	t.Helper()
	if err := m.AddPolicy(p); err != nil {
		t.Fatalf("add policy %s->%s: unexpected error %v", p.From, p.To, err)
	}
}

func TestUpsertNormalizesPortsAndCountsVersion(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("web-01", "t-1", []int{443, 80, 443}, true))
	got, ok := m.Asset("web-01")
	if !ok {
		t.Fatal("asset web-01 not stored")
	}
	if !reflect.DeepEqual(got.Ports, []int{80, 443}) {
		t.Fatalf("ports = %v, want [80 443]", got.Ports)
	}
	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("version = %d, want 1", snapshot.Version)
	}
}

func TestUpsertRejectsInvalidAssets(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	bad := []impl.Asset{
		asset("", "t-1", []int{80}, false),
		asset("a", "", []int{80}, false),
		asset("a", "t-1", []int{0}, false),
		asset("a", "t-1", []int{65536}, false),
	}
	for i, candidate := range bad {
		if err := m.Upsert(candidate); !errors.Is(err, impl.ErrInvalidAsset) {
			t.Fatalf("case %d: error = %v, want ErrInvalidAsset", i, err)
		}
	}
	limited := newMapper(impl.MapperOptions{MaxAssets: 2})
	mustUpsert(t, limited, asset("a", "t-1", []int{80}, false))
	mustUpsert(t, limited, asset("b", "t-1", []int{80}, false))
	if err := limited.Upsert(asset("c", "t-1", []int{80}, false)); !errors.Is(err, impl.ErrAssetLimit) {
		t.Fatalf("asset limit error = %v, want ErrAssetLimit", err)
	}
	if got := limited.Stats().Assets; got != 2 {
		t.Fatalf("assets = %d, want 2", got)
	}
}

func TestUpsertReplacesExistingAssetByID(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("web-01", "t-1", []int{80}, true))
	mustUpsert(t, m, asset("web-01", "t-1", []int{8080}, false))
	got, ok := m.Asset("web-01")
	if !ok {
		t.Fatal("replaced asset missing")
	}
	if !reflect.DeepEqual(got.Ports, []int{8080}) || got.Exposed {
		t.Fatalf("replaced asset = %+v", got)
	}
	if got := m.Stats().Assets; got != 1 {
		t.Fatalf("assets = %d, want 1", got)
	}
	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("version = %d, want 2", snapshot.Version)
	}
}

func TestRemoveDropsAssetAndItsPolicies(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("edge-01", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("app-01", "t-1", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "app-01", Port: 443, Allow: true})
	if got := len(m.Policies()); got != 1 {
		t.Fatalf("policies = %d, want 1", got)
	}
	if err := m.Remove("edge-01"); err != nil {
		t.Fatalf("remove: unexpected error %v", err)
	}
	if _, ok := m.Asset("edge-01"); ok {
		t.Fatal("removed asset still stored")
	}
	if got := len(m.Policies()); got != 0 {
		t.Fatalf("policies after removal = %d, want 0", got)
	}
	if got := m.Stats().Policies; got != 0 {
		t.Fatalf("policy stat after removal = %d, want 0", got)
	}
	exposures := m.Exposures()
	if len(exposures) != 1 || exposures[0].Asset != "app-01" {
		t.Fatalf("exposures = %+v", exposures)
	}
}

func TestRemoveUnknownAssetError(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	if err := m.Remove("missing"); !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("remove unknown error = %v, want ErrUnknownAsset", err)
	}
}

func TestAddPolicyValidatesEndpointsAndSelfLoop(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("a", "t-1", []int{80}, false))
	mustUpsert(t, m, asset("b", "t-1", []int{80}, false))
	if err := m.AddPolicy(impl.Policy{From: "a", To: "missing", Port: 80, Allow: true}); !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("unknown target error = %v, want ErrUnknownAsset", err)
	}
	if err := m.AddPolicy(impl.Policy{From: "missing", To: "a", Port: 80, Allow: true}); !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("unknown source error = %v, want ErrUnknownAsset", err)
	}
	if err := m.AddPolicy(impl.Policy{From: "a", To: "a", Port: 80, Allow: true}); !errors.Is(err, impl.ErrSelfPolicy) {
		t.Fatalf("self policy error = %v, want ErrSelfPolicy", err)
	}
	if err := m.AddPolicy(impl.Policy{From: "a", To: "b", Port: 70000, Allow: true}); !errors.Is(err, impl.ErrInvalidPolicy) {
		t.Fatalf("bad port error = %v, want ErrInvalidPolicy", err)
	}
	if got := len(m.Policies()); got != 0 {
		t.Fatalf("policies after rejections = %d, want 0", got)
	}
}

func TestAddPolicyDuplicateAndLimit(t *testing.T) {
	m := newMapper(impl.MapperOptions{MaxPolicies: 2})
	mustUpsert(t, m, asset("a", "t-1", []int{443, 80}, false))
	mustUpsert(t, m, asset("b", "t-1", []int{443, 80}, false))
	mustPolicy(t, m, impl.Policy{From: "a", To: "b", Port: 443, Allow: true})
	if err := m.AddPolicy(impl.Policy{From: "a", To: "b", Port: 443, Allow: false}); !errors.Is(err, impl.ErrDuplicatePolicy) {
		t.Fatalf("duplicate error = %v, want ErrDuplicatePolicy", err)
	}
	mustPolicy(t, m, impl.Policy{From: "a", To: "b", Port: 80, Allow: true})
	if err := m.AddPolicy(impl.Policy{From: "a", To: "b", Port: 8080, Allow: true}); !errors.Is(err, impl.ErrPolicyLimit) {
		t.Fatalf("policy limit error = %v, want ErrPolicyLimit", err)
	}
	if got := len(m.Policies()); got != 2 {
		t.Fatalf("policies = %d, want 2", got)
	}
}

func TestReachableSingleHopExactPath(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("edge-01", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("app-01", "t-1", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "app-01", Port: 443, Allow: true})
	paths, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 443})
	if err != nil {
		t.Fatalf("reachable: unexpected error %v", err)
	}
	want := []impl.Path{{Nodes: []string{"edge-01", "app-01"}, Hops: 1, Port: 443}}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestReachableRequiresPortMatchAndKnownEntry(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("edge-01", "t-1", []int{22, 443}, false))
	mustUpsert(t, m, asset("app-01", "t-1", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "app-01", Port: 443, Allow: true})
	paths, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 22})
	if err != nil {
		t.Fatalf("reachable on port 22: unexpected error %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths on port 22 = %d, want 0", len(paths))
	}
	if _, err := m.Reachable(impl.Entry{Asset: "missing", Port: 443}); !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("unknown entry error = %v, want ErrUnknownAsset", err)
	}
	if _, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 8080}); !errors.Is(err, impl.ErrUnknownPort) {
		t.Fatalf("unknown port error = %v, want ErrUnknownPort", err)
	}
}

func TestReachableCrossTenantNeedsExplicitAllow(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("src", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("dst", "t-2", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "src", To: "dst", Port: 443, Allow: true})
	paths, err := m.Reachable(impl.Entry{Asset: "src", Port: 443})
	if err != nil {
		t.Fatalf("reachable: unexpected error %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("cross tenant paths without allowance = %d, want 0", len(paths))
	}
	if err := m.RemovePolicy("src", "dst", 443); err != nil {
		t.Fatalf("remove policy: unexpected error %v", err)
	}
	mustPolicy(t, m, impl.Policy{From: "src", To: "dst", Port: 443, Allow: true, CrossTenant: true})
	paths, err = m.Reachable(impl.Entry{Asset: "src", Port: 443})
	if err != nil {
		t.Fatalf("reachable after allowance: unexpected error %v", err)
	}
	if len(paths) != 1 || paths[0].Nodes[1] != "dst" {
		t.Fatalf("paths after allowance = %+v", paths)
	}
}

func TestReachablePrefersShortestAndLexicographicPath(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	for _, id := range []string{"edge-01", "a-01", "b-01"} {
		mustUpsert(t, m, asset(id, "t-1", []int{443}, false))
	}
	mustUpsert(t, m, asset("dest-01", "t-1", []int{443}, true))
	mustUpsert(t, m, asset("dest-02", "t-1", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "b-01", Port: 443, Allow: true})
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "a-01", Port: 443, Allow: true})
	mustPolicy(t, m, impl.Policy{From: "b-01", To: "dest-01", Port: 443, Allow: true})
	mustPolicy(t, m, impl.Policy{From: "a-01", To: "dest-01", Port: 443, Allow: true})
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "dest-02", Port: 443, Allow: true})
	paths, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 443})
	if err != nil {
		t.Fatalf("reachable: unexpected error %v", err)
	}
	want := []impl.Path{
		{Nodes: []string{"edge-01", "dest-02"}, Hops: 1, Port: 443},
		{Nodes: []string{"edge-01", "a-01", "dest-01"}, Hops: 2, Port: 443},
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestReachableEnforcesHopLimit(t *testing.T) {
	m := newMapper(impl.MapperOptions{MaxHops: 1})
	mustUpsert(t, m, asset("edge-01", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("mid-01", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("dest-01", "t-1", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "mid-01", Port: 443, Allow: true})
	mustPolicy(t, m, impl.Policy{From: "mid-01", To: "dest-01", Port: 443, Allow: true})
	if _, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 443}); !errors.Is(err, impl.ErrHopLimit) {
		t.Fatalf("hop limit error = %v, want ErrHopLimit", err)
	}
}

func TestDiffStableOrderAndSnapshotVersion(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("app-01", "t-1", []int{443}, true))
	mustUpsert(t, m, asset("app-02", "t-1", []int{8080, 80}, true))
	mustPolicy(t, m, impl.Policy{From: "app-01", To: "app-02", Port: 443, Allow: true})
	exposures := m.Exposures()
	wantExposures := []impl.Exposure{
		{Asset: "app-01", Tenant: "t-1", Port: 443},
		{Asset: "app-02", Tenant: "t-1", Port: 80},
		{Asset: "app-02", Tenant: "t-1", Port: 8080},
	}
	if !reflect.DeepEqual(exposures, wantExposures) {
		t.Fatalf("exposures = %+v, want %+v", exposures, wantExposures)
	}
	baseline, err := m.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	if baseline.Version != 3 {
		t.Fatalf("baseline version = %d, want 3", baseline.Version)
	}
	mustUpsert(t, m, asset("app-01", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("app-03", "t-1", []int{22}, true))
	delta, err := m.Diff(baseline)
	if err != nil {
		t.Fatalf("diff: unexpected error %v", err)
	}
	wantAdded := []impl.Exposure{{Asset: "app-03", Tenant: "t-1", Port: 22}}
	wantRemoved := []impl.Exposure{{Asset: "app-01", Tenant: "t-1", Port: 443}}
	if !reflect.DeepEqual(delta.Added, wantAdded) {
		t.Fatalf("added = %+v, want %+v", delta.Added, wantAdded)
	}
	if !reflect.DeepEqual(delta.Removed, wantRemoved) {
		t.Fatalf("removed = %+v, want %+v", delta.Removed, wantRemoved)
	}
	current, err := m.Snapshot()
	if err != nil {
		t.Fatalf("second snapshot: unexpected error %v", err)
	}
	if current.Version != 5 {
		t.Fatalf("current version = %d, want 5", current.Version)
	}
}

func TestSnapshotRestoreKeepsReachableAndDiffEmpty(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	mustUpsert(t, m, asset("edge-01", "t-1", []int{443}, false))
	mustUpsert(t, m, asset("app-01", "t-1", []int{443}, true))
	mustPolicy(t, m, impl.Policy{From: "edge-01", To: "app-01", Port: 443, Allow: true})
	before, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 443})
	if err != nil {
		t.Fatalf("reachable before snapshot: unexpected error %v", err)
	}
	baseline, err := m.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: unexpected error %v", err)
	}
	if err := m.RemovePolicy("edge-01", "app-01", 443); err != nil {
		t.Fatalf("remove policy: unexpected error %v", err)
	}
	empty, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 443})
	if err != nil {
		t.Fatalf("reachable after removal: unexpected error %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("paths after policy removal = %d, want 0", len(empty))
	}
	if err := m.Restore(baseline); err != nil {
		t.Fatalf("restore: unexpected error %v", err)
	}
	after, err := m.Reachable(impl.Entry{Asset: "edge-01", Port: 443})
	if err != nil {
		t.Fatalf("reachable after restore: unexpected error %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("paths after restore = %+v, want %+v", after, before)
	}
	delta, err := m.Diff(baseline)
	if err != nil {
		t.Fatalf("diff after restore: unexpected error %v", err)
	}
	if len(delta.Added) != 0 || len(delta.Removed) != 0 {
		t.Fatalf("diff after restore = %+v, want empty", delta)
	}
}

func TestRestoreRejectsBadSnapshot(t *testing.T) {
	m := newMapper(impl.MapperOptions{})
	if err := m.Restore(impl.Snapshot{}); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("zero version error = %v, want ErrBadSnapshot", err)
	}
	invalidAsset := impl.Snapshot{Version: 1, Assets: []impl.Asset{asset("a", "", []int{80}, false)}}
	if err := m.Restore(invalidAsset); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("invalid asset error = %v, want ErrBadSnapshot", err)
	}
	danglingPolicy := impl.Snapshot{Version: 1, Policies: []impl.Policy{{From: "a", To: "b", Port: 80, Allow: true}}}
	if err := m.Restore(danglingPolicy); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("dangling policy error = %v, want ErrBadSnapshot", err)
	}
	if _, err := m.Diff(impl.Snapshot{}); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("diff against empty snapshot error = %v, want ErrBadSnapshot", err)
	}
	if got := m.Stats().Assets; got != 0 {
		t.Fatalf("assets after failed restores = %d, want 0", got)
	}
}
