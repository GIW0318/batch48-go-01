package task

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-c-15"
)

func newLedger(opts impl.LedgerOptions) *impl.ScanCoverageLedger {
	return impl.NewScanCoverageLedger(opts)
}

func register(t *testing.T, l *impl.ScanCoverageLedger, id string, priority int, rules ...string) {
	t.Helper()
	if err := l.RegisterAsset(impl.Asset{ID: id, Rules: rules, Priority: priority}); err != nil {
		t.Fatalf("register asset %s: %v", id, err)
	}
}

func TestRegisterAssetRejectsInvalidFields(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	if err := l.RegisterAsset(impl.Asset{ID: "", Rules: []string{"r1"}, Priority: 1}); !errors.Is(err, impl.ErrInvalidAsset) {
		t.Fatalf("empty asset id: want ErrInvalidAsset, got %v", err)
	}
	if err := l.RegisterAsset(impl.Asset{ID: "a1", Rules: []string{"r1"}, Priority: 9}); !errors.Is(err, impl.ErrInvalidAsset) {
		t.Fatalf("priority 9: want ErrInvalidAsset, got %v", err)
	}
	if err := l.RegisterAsset(impl.Asset{ID: "a2", Priority: 1}); !errors.Is(err, impl.ErrInvalidAsset) {
		t.Fatalf("empty rule set: want ErrInvalidAsset, got %v", err)
	}
}

func TestRegisterAssetEnforcesLimit(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 1, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 1, "r1")
	if err := l.RegisterAsset(impl.Asset{ID: "a2", Rules: []string{"r1"}, Priority: 1}); !errors.Is(err, impl.ErrAssetLimit) {
		t.Fatalf("second asset with MaxAssets 1: want ErrAssetLimit, got %v", err)
	}
}

func TestRegisterAssetRejectsDuplicate(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 1, "r1")
	if err := l.RegisterAsset(impl.Asset{ID: "a1", Rules: []string{"r1"}, Priority: 1}); !errors.Is(err, impl.ErrInvalidAsset) {
		t.Fatalf("duplicate asset id: want ErrInvalidAsset, got %v", err)
	}
}

func TestMarkRunRejectsUnknownAsset(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	run := impl.Run{AssetID: "ghost", Rules: []string{"r1"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(run); !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("run for unknown asset: want ErrUnknownAsset, got %v", err)
	}
}

func TestMarkRunRejectsUnknownRule(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 1, "r1", "r2")
	run := impl.Run{AssetID: "a1", Rules: []string{"r1", "r9"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(run); !errors.Is(err, impl.ErrUnknownRule) {
		t.Fatalf("rule outside the asset rule set: want ErrUnknownRule, got %v", err)
	}
	if got := len(l.Coverage("a1")); got != 0 {
		t.Fatalf("rejected run must not record coverage, got %d records", got)
	}
}

func TestMarkRunRejectsStaleVersions(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 1, "r1")
	if err := l.SetRuleVersion(2); err != nil {
		t.Fatalf("set rule version: %v", err)
	}
	staleRule := impl.Run{AssetID: "a1", Rules: []string{"r1"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(staleRule); !errors.Is(err, impl.ErrStaleRuleVersion) {
		t.Fatalf("stale rule version: want ErrStaleRuleVersion, got %v", err)
	}
	staleScanner := impl.Run{AssetID: "a1", Rules: []string{"r1"}, RuleVersion: 2, ScannerID: "s1", ScannerVersion: 9, At: 100}
	if err := l.MarkRun(staleScanner); !errors.Is(err, impl.ErrStaleScannerVersion) {
		t.Fatalf("stale scanner version: want ErrStaleScannerVersion, got %v", err)
	}
}

func TestMarkRunRecordsCoverage(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 2, "r1", "r2")
	run := impl.Run{AssetID: "a1", Rules: []string{"r2", "r1", "r2"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(run); err != nil {
		t.Fatalf("mark run: %v", err)
	}
	records := l.Coverage("a1")
	if len(records) != 2 {
		t.Fatalf("coverage records: want 2 (duplicate rule deduped), got %d", len(records))
	}
	if records[0].RuleID != "r1" || records[1].RuleID != "r2" {
		t.Fatalf("coverage order: want r1 then r2, got %+v", records)
	}
	for _, rec := range records {
		if rec.RuleVersion != 1 || rec.ScannerVersion != 1 || rec.ScannerID != "s1" || rec.At != 100 {
			t.Fatalf("coverage record: got %+v", rec)
		}
	}
	stats := l.Stats()
	if stats.Covered != 2 || stats.Runs != 1 {
		t.Fatalf("stats: want covered=2 runs=1, got %+v", stats)
	}
}

func TestRepeatedRunRefreshesTimestamp(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 2, "r1", "r2")
	first := impl.Run{AssetID: "a1", Rules: []string{"r1", "r2"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(first); err != nil {
		t.Fatalf("first run: %v", err)
	}
	second := impl.Run{AssetID: "a1", Rules: []string{"r1", "r2"}, RuleVersion: 1, ScannerID: "s2", ScannerVersion: 1, At: 250}
	if err := l.MarkRun(second); err != nil {
		t.Fatalf("second run: %v", err)
	}
	records := l.Coverage("a1")
	if len(records) != 2 {
		t.Fatalf("coverage records: want 2, got %d", len(records))
	}
	for _, rec := range records {
		if rec.At != 250 || rec.ScannerID != "s2" {
			t.Fatalf("record must be refreshed to the latest run, got %+v", rec)
		}
	}
	stats := l.Stats()
	if stats.Covered != 2 || stats.Runs != 2 {
		t.Fatalf("stats: want covered=2 runs=2, got %+v", stats)
	}
}

func TestGapsSortedByPriorityAssetRule(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 10, Shards: 1})
	register(t, l, "a1", 1, "r1", "r2")
	register(t, l, "a2", 3, "r2")
	register(t, l, "a3", 2, "r1")
	gaps, err := l.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("gaps: %v", err)
	}
	want := []impl.Gap{
		{AssetID: "a2", RuleID: "r2", Priority: 3, Reason: "missing"},
		{AssetID: "a3", RuleID: "r1", Priority: 2, Reason: "missing"},
		{AssetID: "a1", RuleID: "r1", Priority: 1, Reason: "missing"},
		{AssetID: "a1", RuleID: "r2", Priority: 1, Reason: "missing"},
	}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gap order: want %+v, got %+v", want, gaps)
	}
	if got := l.Stats().Gaps; got != 4 {
		t.Fatalf("gap total: want 4, got %d", got)
	}
}

func TestGapsRespectsLimitAndReportsTotal(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 10, Shards: 1})
	register(t, l, "a1", 1, "r1", "r2")
	register(t, l, "a2", 3, "r2")
	register(t, l, "a3", 2, "r1")
	gaps, err := l.Gaps(impl.Query{Limit: 2})
	if err != nil {
		t.Fatalf("gaps: %v", err)
	}
	if len(gaps) != 2 {
		t.Fatalf("limited gaps: want 2, got %d", len(gaps))
	}
	if gaps[0].AssetID != "a2" || gaps[1].AssetID != "a3" {
		t.Fatalf("limited gap order: got %+v", gaps)
	}
	if got := l.Stats().Gaps; got != 4 {
		t.Fatalf("gap total must count all gaps, want 4, got %d", got)
	}
}

func TestInvalidateProducesGaps(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 2, "r1", "r2")
	run := impl.Run{AssetID: "a1", Rules: []string{"r1", "r2"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(run); err != nil {
		t.Fatalf("mark run: %v", err)
	}
	gaps, err := l.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("gaps after run: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("full coverage must leave no gaps, got %+v", gaps)
	}
	if err := l.Invalidate("a1"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	gaps, err = l.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("gaps after invalidate: %v", err)
	}
	if len(gaps) != 2 {
		t.Fatalf("gaps after invalidate: want 2, got %d", len(gaps))
	}
	for _, g := range gaps {
		if g.Reason != "missing" {
			t.Fatalf("invalidated coverage reason: want missing, got %q", g.Reason)
		}
	}
	if got := l.Stats().Covered; got != 0 {
		t.Fatalf("covered after invalidate: want 0, got %d", got)
	}
}

func TestSetRuleVersionInvalidatesCoverage(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 1})
	register(t, l, "a1", 2, "r1", "r2")
	run := impl.Run{AssetID: "a1", Rules: []string{"r1", "r2"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(run); err != nil {
		t.Fatalf("mark run: %v", err)
	}
	if err := l.SetRuleVersion(2); err != nil {
		t.Fatalf("set rule version: %v", err)
	}
	if got := l.RuleVersion(); got != 2 {
		t.Fatalf("rule version: want 2, got %d", got)
	}
	gaps, err := l.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("gaps after version change: %v", err)
	}
	if len(gaps) != 2 {
		t.Fatalf("gaps after version change: want 2, got %d", len(gaps))
	}
	for _, g := range gaps {
		if g.Reason != "stale" {
			t.Fatalf("version-invalidated reason: want stale, got %q", g.Reason)
		}
	}
}

func TestShardOfIsStableAndInRange(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 4})
	ids := []string{"asset-a", "asset-b", "asset-c", "asset-d", "asset-e", "asset-f", "asset-g", "asset-h", "asset-i", "asset-j", "asset-k", "asset-l"}
	seen := map[int]bool{}
	for _, id := range ids {
		shard := l.ShardOf(id)
		if shard < 0 || shard >= 4 {
			t.Fatalf("shard for %q out of range: %d", id, shard)
		}
		if again := l.ShardOf(id); again != shard {
			t.Fatalf("shard for %q is not stable: %d then %d", id, shard, again)
		}
		seen[shard] = true
	}
	if len(seen) < 2 {
		t.Fatalf("shard distribution: twelve asset ids all landed in the same shard %v", seen)
	}
}

func TestSnapshotRestoreKeepsGaps(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 2})
	register(t, l, "a1", 3, "r1", "r2")
	register(t, l, "a2", 1, "r1")
	run := impl.Run{AssetID: "a1", Rules: []string{"r1"}, RuleVersion: 1, ScannerID: "s1", ScannerVersion: 1, At: 100}
	if err := l.MarkRun(run); err != nil {
		t.Fatalf("mark run: %v", err)
	}
	before, err := l.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("gaps before snapshot: %v", err)
	}
	data, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 2})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	after, err := restored.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("gaps after restore: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("gaps after restore: want %+v, got %+v", before, after)
	}
	if want, got := l.Stats(), restored.Stats(); want != got {
		t.Fatalf("stats after restore: want %+v, got %+v", want, got)
	}
	if err := restored.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot: want ErrBadSnapshot, got %v", err)
	}
}

func TestConcurrentRunsAndGapsAreConsistent(t *testing.T) {
	l := newLedger(impl.LedgerOptions{MaxAssets: 8, MaxGaps: 8, Shards: 2})
	register(t, l, "a1", 2, "r1", "r2", "r3")
	rules := []string{"r1", "r2", "r3"}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				run := impl.Run{
					AssetID:        "a1",
					Rules:          rules,
					RuleVersion:    1,
					ScannerID:      "s1",
					ScannerVersion: 1,
					At:             int64(100 + worker*20 + i),
				}
				if err := l.MarkRun(run); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				gaps, err := l.Gaps(impl.Query{})
				if err != nil {
					errs <- err
					return
				}
				if len(gaps) != 0 && len(gaps) != len(rules) {
					errs <- errors.New("gap report saw a partially applied run")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent consistency: %v", err)
	}
	gaps, err := l.Gaps(impl.Query{})
	if err != nil {
		t.Fatalf("final gaps: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("final gaps after full coverage: want 0, got %d", len(gaps))
	}
}
