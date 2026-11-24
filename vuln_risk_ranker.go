// Package task turns scanner findings into a stable, quota limited repair queue
// using integer per-mille scoring.
package task

import "errors"

var errNotImplemented = errors.New("vuln risk ranker: not implemented")

// Errors surfaced while scoring and ranking findings.
var (
	ErrWeightsSum     = errors.New("vuln risk ranker: weights must sum to 1000")
	ErrWeightRange    = errors.New("vuln risk ranker: weight out of range")
	ErrDuplicateAsset = errors.New("vuln risk ranker: duplicate asset")
	ErrAssetRange     = errors.New("vuln risk ranker: asset factor out of range")
	ErrUnknownAsset   = errors.New("vuln risk ranker: unknown asset")
	ErrBadVuln        = errors.New("vuln risk ranker: bad vulnerability")
	ErrEmptyInput     = errors.New("vuln risk ranker: empty input")
	ErrQueueFull      = errors.New("vuln risk ranker: queue full")
	ErrBadK           = errors.New("vuln risk ranker: bad k")
	ErrEmptyRanking   = errors.New("vuln risk ranker: no ranking yet")
)

// Vuln is one scanner finding on one asset.
type Vuln struct {
	CVE     string
	CVSS    int
	EPSS    *int
	Exploit bool
	Asset   string
}

// Asset is one piece of infrastructure the finding lands on.
type Asset struct {
	ID          string
	Exposure    int
	Criticality int
}

// Weights holds the three per-mille score weights.
type Weights struct {
	Exploitability int
	Exposure       int
	Asset          int
}

// Score is the per-mille risk of one finding on one asset.
type Score struct {
	Value int
}

// Ranked is one queue entry.
type Ranked struct {
	CVE    string
	Value  int
	Assets []string
}

// RankerStats counts ranking activity.
type RankerStats struct {
	Scored     uint64
	Ranked     uint64
	Rebalanced uint64
	Merged     uint64
}

// Options configures a ranker instance.
type Options struct {
	MaxQueue       int
	DefaultWeights Weights
}

// VulnRiskRanker scores findings and keeps the ranked repair queue.
type VulnRiskRanker struct {
	opts Options
}

// NewVulnRiskRanker returns a ranker configured by opts.
func NewVulnRiskRanker(opts Options) *VulnRiskRanker {
	return &VulnRiskRanker{opts: opts}
}

// AddAsset registers one asset.
func (r *VulnRiskRanker) AddAsset(a Asset) error {
	return errNotImplemented
}

// Assets reports the registered assets in lexical order.
func (r *VulnRiskRanker) Assets() []Asset {
	return nil
}

// Score computes the per-mille risk of one finding on one registered asset.
func (r *VulnRiskRanker) Score(v Vuln, a Asset) (Score, error) {
	return Score{}, errNotImplemented
}

// Rank scores the findings and rebuilds the queue.
func (r *VulnRiskRanker) Rank(vulns []Vuln) ([]Ranked, error) {
	return nil, errNotImplemented
}

// Rebalance changes the weights and recomputes the queue.
func (r *VulnRiskRanker) Rebalance(w Weights) error {
	return errNotImplemented
}

// TopK returns the first k queue entries and counts the rest.
func (r *VulnRiskRanker) TopK(k int) ([]Ranked, error) {
	return nil, errNotImplemented
}

// Truncated reports how many entries the last TopK dropped.
func (r *VulnRiskRanker) Truncated() int {
	return 0
}

// Result reports the current queue.
func (r *VulnRiskRanker) Result() []Ranked {
	return nil
}

// Weights reports the active weights.
func (r *VulnRiskRanker) Weights() Weights {
	return Weights{}
}

// Reset drops the queue, the truncation counter and the statistics.
func (r *VulnRiskRanker) Reset() error {
	return errNotImplemented
}

// Stats reports counters since the last reset.
func (r *VulnRiskRanker) Stats() RankerStats {
	return RankerStats{}
}
