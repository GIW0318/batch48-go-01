package task

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-05"
)

func ranker() *impl.VulnRiskRanker {
	return impl.NewVulnRiskRanker(impl.Options{MaxQueue: 100})
}

func epss(v int) *int { return &v }

func assetA() impl.Asset {
	return impl.Asset{ID: "asset-a", Exposure: 700, Criticality: 500}
}

func assetB() impl.Asset {
	return impl.Asset{ID: "asset-b", Exposure: 300, Criticality: 900}
}

func register(t *testing.T, r *impl.VulnRiskRanker, assets ...impl.Asset) {
	t.Helper()
	for _, a := range assets {
		if err := r.AddAsset(a); err != nil {
			t.Fatalf("AddAsset(%s): unexpected error: %v", a.ID, err)
		}
	}
}

func TestRebalanceRejectsWeightSumNotThousand(t *testing.T) {
	r := ranker()
	err := r.Rebalance(impl.Weights{Exploitability: 400, Exposure: 300, Asset: 200})
	if !errors.Is(err, impl.ErrWeightsSum) {
		t.Fatalf("weight sum error = %v, want ErrWeightsSum", err)
	}
}

func TestRebalanceRejectsNegativeComponent(t *testing.T) {
	r := ranker()
	err := r.Rebalance(impl.Weights{Exploitability: -1, Exposure: 501, Asset: 500})
	if !errors.Is(err, impl.ErrWeightRange) {
		t.Fatalf("negative weight error = %v, want ErrWeightRange", err)
	}
}

func TestAddAssetRejectsDuplicateAndOutOfRange(t *testing.T) {
	r := ranker()
	register(t, r, assetA())
	if err := r.AddAsset(assetA()); !errors.Is(err, impl.ErrDuplicateAsset) {
		t.Fatalf("duplicate asset error = %v, want ErrDuplicateAsset", err)
	}
	if err := r.AddAsset(impl.Asset{ID: "asset-c", Exposure: 1001, Criticality: 0}); !errors.Is(err, impl.ErrAssetRange) {
		t.Fatalf("asset range error = %v, want ErrAssetRange", err)
	}
}

func TestScoreRejectsUnknownAsset(t *testing.T) {
	r := ranker()
	v := impl.Vuln{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "ghost"}
	_, err := r.Score(v, impl.Asset{ID: "ghost", Exposure: 100, Criticality: 100})
	if !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("unknown asset error = %v, want ErrUnknownAsset", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("error %q must name the asset", err.Error())
	}
}

func TestScoreRejectsInvalidVuln(t *testing.T) {
	r := ranker()
	register(t, r, assetA())
	if _, err := r.Score(impl.Vuln{CVE: "CVE-2024-0001", CVSS: 1200, Asset: "asset-a"}, assetA()); !errors.Is(err, impl.ErrBadVuln) {
		t.Fatalf("out of range cvss error = %v, want ErrBadVuln", err)
	}
	if _, err := r.Score(impl.Vuln{CVE: "", CVSS: 500, Asset: "asset-a"}, assetA()); !errors.Is(err, impl.ErrBadVuln) {
		t.Fatalf("empty cve error = %v, want ErrBadVuln", err)
	}
}

func TestScoreExactPermilleComposition(t *testing.T) {
	r := ranker()
	register(t, r, assetA())
	v := impl.Vuln{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "asset-a"}
	score, err := r.Score(v, assetA())
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if score.Value != 600 {
		t.Fatalf("Score = %d, want 600", score.Value)
	}
}

func TestScoreCapsExploitBonus(t *testing.T) {
	r := ranker()
	zero := impl.Asset{ID: "zero", Exposure: 0, Criticality: 0}
	register(t, r, zero)
	v := impl.Vuln{CVE: "CVE-2024-0009", CVSS: 1000, EPSS: epss(1000), Exploit: true, Asset: "zero"}
	score, err := r.Score(v, zero)
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if score.Value != 400 {
		t.Fatalf("Score = %d, want 400 after the exploitability cap", score.Value)
	}
}

func queue() []impl.Vuln {
	return []impl.Vuln{
		{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "asset-a"},
		{CVE: "CVE-2024-0002", CVSS: 900, EPSS: epss(500), Asset: "asset-b"},
		{CVE: "CVE-2024-0003", CVSS: 200, EPSS: epss(200), Asset: "asset-a"},
	}
}

func TestRankSortsDescending(t *testing.T) {
	r := ranker()
	register(t, r, assetA(), assetB())
	ranked, err := r.Rank(queue())
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	want := []impl.Ranked{
		{CVE: "CVE-2024-0002", Value: 640, Assets: []string{"asset-b"}},
		{CVE: "CVE-2024-0001", Value: 600, Assets: []string{"asset-a"}},
		{CVE: "CVE-2024-0003", Value: 440, Assets: []string{"asset-a"}},
	}
	if !reflect.DeepEqual(ranked, want) {
		t.Fatalf("Rank = %+v, want %+v", ranked, want)
	}
}

func TestRankTieBreaksByCVE(t *testing.T) {
	r := ranker()
	register(t, r, assetA())
	vulns := []impl.Vuln{
		{CVE: "CVE-2024-0002", CVSS: 800, EPSS: epss(400), Asset: "asset-a"},
		{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "asset-a"},
	}
	ranked, err := r.Rank(vulns)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	if len(ranked) != 2 || ranked[0].CVE != "CVE-2024-0001" || ranked[1].CVE != "CVE-2024-0002" {
		t.Fatalf("Rank = %+v, want CVE-2024-0001 before CVE-2024-0002", ranked)
	}
	if ranked[0].Value != ranked[1].Value {
		t.Fatalf("tie values differ: %+v", ranked)
	}
}

func TestRankRejectsUnknownAsset(t *testing.T) {
	r := ranker()
	register(t, r, assetA())
	vulns := []impl.Vuln{{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "ghost"}}
	_, err := r.Rank(vulns)
	if !errors.Is(err, impl.ErrUnknownAsset) {
		t.Fatalf("rank unknown asset error = %v, want ErrUnknownAsset", err)
	}
}

func TestRankRejectsQueueOverflow(t *testing.T) {
	r := impl.NewVulnRiskRanker(impl.Options{MaxQueue: 2})
	register(t, r, assetA())
	if _, err := r.Rank(nil); !errors.Is(err, impl.ErrEmptyInput) {
		t.Fatalf("empty ranking input error = %v, want ErrEmptyInput", err)
	}
	vulns := []impl.Vuln{
		{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "asset-a"},
		{CVE: "CVE-2024-0002", CVSS: 700, EPSS: epss(300), Asset: "asset-a"},
		{CVE: "CVE-2024-0003", CVSS: 600, EPSS: epss(200), Asset: "asset-a"},
	}
	if _, err := r.Rank(vulns); !errors.Is(err, impl.ErrQueueFull) {
		t.Fatalf("queue overflow error = %v, want ErrQueueFull", err)
	}
}

func TestRebalanceRecomputesConsistently(t *testing.T) {
	r := ranker()
	register(t, r, assetA(), assetB())
	if _, err := r.Rank(queue()); err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	if err := r.Rebalance(impl.Weights{Exploitability: 500, Exposure: 250, Asset: 250}); err != nil {
		t.Fatalf("Rebalance: unexpected error: %v", err)
	}
	ranked := r.Result()
	if len(ranked) != 3 {
		t.Fatalf("Result has %d entries, want 3", len(ranked))
	}
	wantValues := []int{650, 600, 400}
	for i, want := range wantValues {
		if ranked[i].Value != want {
			t.Fatalf("entry %d = %+v, want value %d recomputed under the new weights", i, ranked[i], want)
		}
	}
}

func TestTopKTruncatesAndCounts(t *testing.T) {
	r := ranker()
	register(t, r, assetA())
	vulns := []impl.Vuln{
		{CVE: "CVE-2024-0001", CVSS: 800, EPSS: epss(400), Asset: "asset-a"},
		{CVE: "CVE-2024-0002", CVSS: 700, EPSS: epss(300), Asset: "asset-a"},
		{CVE: "CVE-2024-0003", CVSS: 600, EPSS: epss(200), Asset: "asset-a"},
		{CVE: "CVE-2024-0004", CVSS: 500, EPSS: epss(100), Asset: "asset-a"},
	}
	if _, err := r.Rank(vulns); err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	top, err := r.TopK(2)
	if err != nil {
		t.Fatalf("TopK(2): unexpected error: %v", err)
	}
	if len(top) != 2 || top[0].Value != 600 || top[1].Value != 560 {
		t.Fatalf("TopK(2) = %+v, want the two highest entries", top)
	}
	if got := r.Truncated(); got != 2 {
		t.Fatalf("Truncated = %d, want 2", got)
	}
	all, err := r.TopK(10)
	if err != nil {
		t.Fatalf("TopK(10): unexpected error: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("TopK(10) returned %d entries, want 4", len(all))
	}
	if got := r.Truncated(); got != 0 {
		t.Fatalf("Truncated = %d after an untruncated call, want 0", got)
	}
}

func TestTopKRejectsBadK(t *testing.T) {
	fresh := ranker()
	if _, err := fresh.TopK(1); !errors.Is(err, impl.ErrEmptyRanking) {
		t.Fatalf("TopK before Rank error = %v, want ErrEmptyRanking", err)
	}
	r := ranker()
	register(t, r, assetA())
	if _, err := r.Rank(queue()); err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	for _, k := range []int{0, -3} {
		if _, err := r.TopK(k); !errors.Is(err, impl.ErrBadK) {
			t.Fatalf("TopK(%d) error = %v, want ErrBadK", k, err)
		}
	}
}

func TestRankIsDeterministic(t *testing.T) {
	r := ranker()
	register(t, r, assetA(), assetB())
	first, err := r.Rank(queue())
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	second, err := r.Rank(queue())
	if err != nil {
		t.Fatalf("second Rank: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second Rank = %+v, want identical to %+v", second, first)
	}
}
