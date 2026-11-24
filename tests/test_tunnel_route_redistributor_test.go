package task

import (
	"errors"
	"net/netip"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-30"
)

func t5Prefix(s string) netip.Prefix {
	return netip.MustParsePrefix(s)
}

func t5Addr(s string) netip.Addr {
	return netip.MustParseAddr(s)
}

func t5Route(prefix, nextHop string, metric int, source string) impl.Route {
	return impl.Route{
		Prefix:   t5Prefix(prefix),
		NextHop:  t5Addr(nextHop),
		Metric:   metric,
		SourceID: source,
	}
}

func t5Policy() impl.Policy {
	return impl.Policy{
		BaseMetric: map[impl.SourceKind]int{
			impl.SourceConnected: 0,
			impl.SourceStatic:    10,
			impl.SourceTunnel:    20,
		},
		Priority: map[impl.SourceKind]int{
			impl.SourceConnected: 1,
			impl.SourceStatic:    2,
			impl.SourceTunnel:    3,
		},
		Targets:      []string{"tun-a", "tun-b"},
		SplitHorizon: true,
	}
}

func TestAddSourceRejectsInvalidRoutes(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{{
		Prefix:   netip.Prefix{},
		NextHop:  t5Addr("192.0.2.1"),
		SourceID: "s1",
	}}); !errors.Is(err, impl.ErrBadRoute) {
		t.Fatalf("invalid prefix must report ErrBadRoute, got %v", err)
	}
	if err := r.AddSource(impl.SourceConnected, []impl.Route{{
		Prefix:   t5Prefix("10.0.0.0/24"),
		NextHop:  netip.Addr{},
		SourceID: "s1",
	}}); !errors.Is(err, impl.ErrBadRoute) {
		t.Fatalf("invalid next hop must report ErrBadRoute, got %v", err)
	}
	if err := r.AddSource(impl.SourceConnected, []impl.Route{{
		Prefix:   t5Prefix("10.0.0.0/24"),
		NextHop:  t5Addr("192.0.2.1"),
		SourceID: "   ",
	}}); !errors.Is(err, impl.ErrBadRoute) {
		t.Fatalf("blank source id must report ErrBadRoute, got %v", err)
	}
	if err := r.AddSource(impl.SourceKind("bogus"), []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); !errors.Is(err, impl.ErrBadKind) {
		t.Fatalf("unknown source kind must report ErrBadKind, got %v", err)
	}
}

func TestAddSourceReplacesSourceAndBumpsVersion(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("first import must succeed, got %v", err)
	}
	infos := r.Sources()
	if len(infos) != 1 || infos[0].Version != 1 || infos[0].Routes != 1 {
		t.Fatalf("source table after first import = %+v, want one version 1 source", infos)
	}
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.9.0.0/24", "192.0.2.2", 10, "s1"),
	}); err != nil {
		t.Fatalf("replacement import must succeed, got %v", err)
	}
	infos = r.Sources()
	if len(infos) != 1 || infos[0].Version != 2 {
		t.Fatalf("source table after replacement = %+v, want one version 2 source", infos)
	}
	if _, ok := r.Lookup(t5Addr("10.0.0.5")); ok {
		t.Fatalf("the replaced prefix must not survive")
	}
	got, ok := r.Lookup(t5Addr("10.9.0.5"))
	if !ok || got.Prefix != t5Prefix("10.9.0.0/24") {
		t.Fatalf("replacement prefix lookup = %+v/%v, want 10.9.0.0/24", got, ok)
	}
}

func TestLookupUsesLongestPrefix(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/8", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceStatic, []impl.Route{
		t5Route("10.1.0.0/16", "192.0.2.2", 20, "s2"),
	}); err != nil {
		t.Fatalf("static import must succeed, got %v", err)
	}
	got, ok := r.Lookup(t5Addr("10.1.2.3"))
	if !ok || got.Prefix != t5Prefix("10.1.0.0/16") || got.SourceID != "s2" {
		t.Fatalf("longest prefix lookup = %+v/%v, want the /16 from s2", got, ok)
	}
	got, ok = r.Lookup(t5Addr("10.9.9.9"))
	if !ok || got.Prefix != t5Prefix("10.0.0.0/8") || got.SourceID != "s1" {
		t.Fatalf("fallback lookup = %+v/%v, want the /8 from s1", got, ok)
	}
}

func TestSamePrefixPrefersLowerEffectiveMetric(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.5.0.0/24", "192.0.2.1", 5, "s-conn"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceStatic, []impl.Route{
		t5Route("10.5.0.0/24", "192.0.2.2", 1, "s-stat"),
	}); err != nil {
		t.Fatalf("static import must succeed, got %v", err)
	}
	got, ok := r.Lookup(t5Addr("10.5.0.9"))
	if !ok || got.SourceID != "s-conn" {
		t.Fatalf("winner = %+v, want s-conn with effective metric 5", got)
	}
}

func TestEqualMetricTieBreakBySourceID(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.6.0.0/24", "192.0.2.1", 10, "s-z"),
	}); err != nil {
		t.Fatalf("first import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.6.0.0/24", "192.0.2.2", 10, "s-a"),
	}); err != nil {
		t.Fatalf("second import must succeed, got %v", err)
	}
	got, ok := r.Lookup(t5Addr("10.6.0.9"))
	if !ok || got.SourceID != "s-a" {
		t.Fatalf("winner = %+v, want the lexicographically smaller source id s-a", got)
	}
}

func TestSourcePriorityBreaksMetricTie(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.7.0.0/24", "192.0.2.1", 10, "s-z"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceStatic, []impl.Route{
		t5Route("10.7.0.0/24", "192.0.2.2", 0, "s-a"),
	}); err != nil {
		t.Fatalf("static import must succeed, got %v", err)
	}
	got, ok := r.Lookup(t5Addr("10.7.0.9"))
	if !ok || got.SourceID != "s-z" || got.Kind != impl.SourceConnected {
		t.Fatalf("winner = %+v, want the connected source with the better kind priority", got)
	}
}

func TestWithdrawSourceRemovesOnlyItsRoutes(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.8.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceStatic, []impl.Route{
		t5Route("10.9.0.0/24", "192.0.2.2", 20, "s2"),
	}); err != nil {
		t.Fatalf("static import must succeed, got %v", err)
	}
	if err := r.WithdrawSource(impl.SourceConnected, "s1"); err != nil {
		t.Fatalf("withdraw must succeed, got %v", err)
	}
	if _, ok := r.Lookup(t5Addr("10.8.0.5")); ok {
		t.Fatalf("withdrawn source route must disappear")
	}
	got, ok := r.Lookup(t5Addr("10.9.0.5"))
	if !ok || got.SourceID != "s2" {
		t.Fatalf("other source route = %+v/%v, want s2 untouched", got, ok)
	}
	if got := len(r.Sources()); got != 1 {
		t.Fatalf("sources after withdraw = %d, want 1", got)
	}
}

func TestWithdrawSourceReportsMissingAndMismatched(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.WithdrawSource(impl.SourceConnected, "ghost"); !errors.Is(err, impl.ErrSourceNotFound) {
		t.Fatalf("unknown source must report ErrSourceNotFound, got %v", err)
	}
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("import must succeed, got %v", err)
	}
	if err := r.WithdrawSource(impl.SourceStatic, "s1"); !errors.Is(err, impl.ErrKindMismatch) {
		t.Fatalf("wrong kind must report ErrKindMismatch, got %v", err)
	}
	if _, ok := r.Lookup(t5Addr("10.0.0.5")); !ok {
		t.Fatalf("a mismatched withdraw must not remove the source")
	}
}

func TestPolicyDenyPrefixFiltersPublishedTable(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("import must succeed, got %v", err)
	}
	p := t5Policy()
	p.DenyPrefixes = []netip.Prefix{t5Prefix("10.0.0.0/16")}
	if err := r.SetPolicy(p); err != nil {
		t.Fatalf("policy change must succeed, got %v", err)
	}
	table, err := r.Rebuild()
	if err != nil {
		t.Fatalf("rebuild must succeed, got %v", err)
	}
	if len(table) != 0 {
		t.Fatalf("published table = %+v, want the denied prefix removed", table)
	}
	if _, ok := r.Lookup(t5Addr("10.0.0.5")); ok {
		t.Fatalf("denied prefix must not resolve")
	}
}

func TestPolicyMaxMetricFiltersRoutes(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 100, "s1"),
	}); err != nil {
		t.Fatalf("import must succeed, got %v", err)
	}
	p := t5Policy()
	p.MaxMetric = 50
	if err := r.SetPolicy(p); err != nil {
		t.Fatalf("policy change must succeed, got %v", err)
	}
	if _, ok := r.Lookup(t5Addr("10.0.0.5")); ok {
		t.Fatalf("route above the metric ceiling must not resolve")
	}
}

func TestSplitHorizonExcludesOriginTunnel(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceTunnel, []impl.Route{
		t5Route("10.10.0.0/24", "192.0.2.1", 5, "tun-a"),
	}); err != nil {
		t.Fatalf("tunnel import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.11.0.0/24", "192.0.2.2", 5, "edge"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	table, err := r.Rebuild()
	if err != nil {
		t.Fatalf("rebuild must succeed, got %v", err)
	}
	byPrefix := make(map[netip.Prefix]impl.Route, len(table))
	for _, route := range table {
		byPrefix[route.Prefix] = route
	}
	tunnel, ok := byPrefix[t5Prefix("10.10.0.0/24")]
	if !ok {
		t.Fatalf("tunnel route missing from the published table")
	}
	if !reflect.DeepEqual(tunnel.AdvertiseTo, []string{"tun-b"}) {
		t.Fatalf("tunnel route targets = %v, want only tun-b", tunnel.AdvertiseTo)
	}
	connected, ok := byPrefix[t5Prefix("10.11.0.0/24")]
	if !ok {
		t.Fatalf("connected route missing from the published table")
	}
	if !reflect.DeepEqual(connected.AdvertiseTo, []string{"tun-a", "tun-b"}) {
		t.Fatalf("connected route targets = %v, want both tunnels", connected.AdvertiseTo)
	}
}

func TestRebuildReturnsCompleteSortedTable(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.2.0.0/24", "192.0.2.1", 10, "s1"),
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceStatic, []impl.Route{
		t5Route("10.1.0.0/24", "192.0.2.2", 20, "s2"),
	}); err != nil {
		t.Fatalf("static import must succeed, got %v", err)
	}
	table, err := r.Rebuild()
	if err != nil {
		t.Fatalf("rebuild must succeed, got %v", err)
	}
	if len(table) != 3 {
		t.Fatalf("published table length = %d, want 3", len(table))
	}
	want := []string{"10.0.0.0/24", "10.1.0.0/24", "10.2.0.0/24"}
	for i, route := range table {
		if route.Prefix.String() != want[i] {
			t.Fatalf("published prefix %d = %s, want %s", i, route.Prefix, want[i])
		}
	}
}

func TestConcurrentLookupNeverSeesPartialTable(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("base import must succeed, got %v", err)
	}
	if _, ok := r.Lookup(t5Addr("10.0.0.5")); !ok {
		t.Fatalf("base route must resolve")
	}
	errCh := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				got, ok := r.Lookup(t5Addr("10.0.0.5"))
				if !ok || got.SourceID != "s1" {
					errCh <- errors.New("concurrent lookup observed a table without the stable route")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			if err := r.AddSource(impl.SourceStatic, []impl.Route{
				t5Route("10.9.0.0/24", "192.0.2.9", 20, "s2"),
			}); err != nil {
				errCh <- err
				return
			}
			if _, err := r.Rebuild(); err != nil {
				errCh <- err
				return
			}
			if err := r.WithdrawSource(impl.SourceStatic, "s2"); err != nil {
				errCh <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent rebuild or lookup failed: %v", err)
	}
}

func TestSnapshotRestoreKeepsSourcesAndTable(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.AddSource(impl.SourceConnected, []impl.Route{
		t5Route("10.0.0.0/24", "192.0.2.1", 10, "s1"),
	}); err != nil {
		t.Fatalf("connected import must succeed, got %v", err)
	}
	if err := r.AddSource(impl.SourceStatic, []impl.Route{
		t5Route("10.1.0.0/24", "192.0.2.2", 20, "s2"),
	}); err != nil {
		t.Fatalf("static import must succeed, got %v", err)
	}
	snap, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewRouteRedistributor(t5Policy())
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	before := r.Sources()
	after := restored.Sources()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("restored sources = %+v, want %+v", after, before)
	}
	got, ok := restored.Lookup(t5Addr("10.1.0.5"))
	if !ok || got.SourceID != "s2" {
		t.Fatalf("restored lookup = %+v/%v, want the s2 route", got, ok)
	}
	if got := restored.Stats().Published; got != 2 {
		t.Fatalf("restored published routes = %d, want 2", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	r := impl.NewRouteRedistributor(t5Policy())
	if err := r.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must report ErrBadSnapshot, got %v", err)
	}
}
