package task

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors reported by the coverage ledger.
var (
	ErrInvalidAsset        = errors.New("scan_coverage_ledger: invalid asset")
	ErrAssetLimit          = errors.New("scan_coverage_ledger: asset limit reached")
	ErrUnknownAsset        = errors.New("scan_coverage_ledger: unknown asset")
	ErrInvalidRun          = errors.New("scan_coverage_ledger: invalid run")
	ErrUnknownRule         = errors.New("scan_coverage_ledger: rule outside asset rule set")
	ErrStaleRuleVersion    = errors.New("scan_coverage_ledger: stale rule version")
	ErrStaleScannerVersion = errors.New("scan_coverage_ledger: stale scanner version")
	ErrInvalidQuery        = errors.New("scan_coverage_ledger: invalid query")
	ErrInvalidVersion      = errors.New("scan_coverage_ledger: invalid version")
	ErrBadSnapshot         = errors.New("scan_coverage_ledger: bad snapshot")
)

const (
	ledgerSnapshotMagic  = "gsb-scan-coverage-ledger"
	ledgerSnapshotFormat = 1
	gapReasonMissing     = "missing"
	gapReasonStale       = "stale"
	initialLedgerVersion = 1
	minAssetPriority     = 1
	maxAssetPriority     = 5
)

// Asset is one registered asset with its expected rule set.
type Asset struct {
	ID       string
	Rules    []string
	Priority int
}

// Coverage is one proven asset and rule combination.
type Coverage struct {
	AssetID        string
	RuleID         string
	RuleVersion    int
	ScannerID      string
	ScannerVersion int
	At             int64
}

// Run is one scanner report for one asset.
type Run struct {
	AssetID        string
	Rules          []string
	RuleVersion    int
	ScannerID      string
	ScannerVersion int
	At             int64
}

// Query narrows the gap report.
type Query struct {
	Assets      []string
	MinPriority int
	Limit       int
}

// Gap is one asset and rule combination without valid coverage.
type Gap struct {
	AssetID  string
	RuleID   string
	Priority int
	Reason   string
}

// LedgerOptions bounds the asset table and the gap report.
type LedgerOptions struct {
	MaxAssets      int
	MaxGaps        int
	Shards         int
	ScannerVersion int
}

// LedgerStats is the accumulated counter view of the ledger.
type LedgerStats struct {
	Assets         int
	Covered        int
	Gaps           int
	Runs           int
	RuleVersion    int
	ScannerVersion int
}

type coverageVersion struct {
	ruleVersion    int
	scannerVersion int
}

type coverageRecord struct {
	coverage Coverage
	valid    bool
}

// ScanCoverageLedger tracks which asset and rule pairs were really scanned.
//
// Coverage is indexed by asset, rule and then version pair. Keeping records
// from older versions is deliberate: a gap caused by a version switch must be
// reported as stale rather than missing.
type ScanCoverageLedger struct {
	mu       sync.RWMutex
	opts     LedgerOptions
	assets   map[string]Asset
	coverage map[string]map[string]map[coverageVersion]coverageRecord
	runs     int

	ruleVersion    int
	scannerVersion int
}

type snapshotCoverage struct {
	Coverage
	Valid bool `json:"valid"`
}

type snapshotState struct {
	Magic          string             `json:"magic"`
	Format         int                `json:"format"`
	MaxAssets      int                `json:"max_assets"`
	MaxGaps        int                `json:"max_gaps"`
	Shards         int                `json:"shards"`
	Assets         []Asset            `json:"assets"`
	Coverage       []snapshotCoverage `json:"coverage"`
	Runs           int                `json:"runs"`
	RuleVersion    int                `json:"rule_version"`
	ScannerVersion int                `json:"scanner_version"`
}

// NewScanCoverageLedger builds a ledger with the given limits.
func NewScanCoverageLedger(opts LedgerOptions) *ScanCoverageLedger {
	scannerVersion := opts.ScannerVersion
	if scannerVersion <= 0 {
		scannerVersion = initialLedgerVersion
	}
	if opts.MaxAssets < 0 {
		opts.MaxAssets = 0
	}
	if opts.MaxGaps < 0 {
		opts.MaxGaps = 0
	}
	if opts.Shards < 0 {
		opts.Shards = 0
	}
	return &ScanCoverageLedger{
		opts:           opts,
		assets:         make(map[string]Asset),
		coverage:       make(map[string]map[string]map[coverageVersion]coverageRecord),
		ruleVersion:    initialLedgerVersion,
		scannerVersion: scannerVersion,
	}
}

// RegisterAsset adds one asset with its expected rule set.
func (l *ScanCoverageLedger) RegisterAsset(a Asset) error {
	if strings.TrimSpace(a.ID) == "" || len(a.Rules) == 0 ||
		a.Priority < minAssetPriority || a.Priority > maxAssetPriority {
		return ErrInvalidAsset
	}

	ruleSet := make(map[string]struct{}, len(a.Rules))
	for _, rule := range a.Rules {
		ruleSet[rule] = struct{}{}
	}
	rules := make([]string, 0, len(ruleSet))
	for rule := range ruleSet {
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.assets[a.ID]; exists {
		return ErrInvalidAsset
	}
	if l.opts.MaxAssets > 0 && len(l.assets) >= l.opts.MaxAssets {
		return ErrAssetLimit
	}

	l.assets[a.ID] = Asset{
		ID:       a.ID,
		Rules:    rules,
		Priority: a.Priority,
	}
	return nil
}

// MarkRun records a successful scanner report as valid coverage.
func (l *ScanCoverageLedger) MarkRun(r Run) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	asset, exists := l.assets[r.AssetID]
	if !exists {
		return ErrUnknownAsset
	}
	if strings.TrimSpace(r.ScannerID) == "" || r.At <= 0 || len(r.Rules) == 0 {
		return ErrInvalidRun
	}
	if r.RuleVersion != l.ruleVersion {
		return ErrStaleRuleVersion
	}
	if r.ScannerVersion != l.scannerVersion {
		return ErrStaleScannerVersion
	}

	allowedRules := make(map[string]struct{}, len(asset.Rules))
	for _, rule := range asset.Rules {
		allowedRules[rule] = struct{}{}
	}

	rules := make([]string, 0, len(r.Rules))
	seen := make(map[string]struct{}, len(r.Rules))
	for _, rule := range r.Rules {
		if _, ok := allowedRules[rule]; !ok {
			return ErrUnknownRule
		}
		if _, duplicate := seen[rule]; duplicate {
			continue
		}
		seen[rule] = struct{}{}
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	byRule := l.coverage[r.AssetID]
	if byRule == nil {
		byRule = make(map[string]map[coverageVersion]coverageRecord)
		l.coverage[r.AssetID] = byRule
	}
	version := coverageVersion{
		ruleVersion:    r.RuleVersion,
		scannerVersion: r.ScannerVersion,
	}
	for _, rule := range rules {
		versions := byRule[rule]
		if versions == nil {
			versions = make(map[coverageVersion]coverageRecord)
			byRule[rule] = versions
		}
		versions[version] = coverageRecord{
			coverage: Coverage{
				AssetID:        r.AssetID,
				RuleID:         rule,
				RuleVersion:    r.RuleVersion,
				ScannerID:      r.ScannerID,
				ScannerVersion: r.ScannerVersion,
				At:             r.At,
			},
			valid: true,
		}
	}

	l.runs++
	return nil
}

// Gaps reports the missing or stale coverage combinations.
func (l *ScanCoverageLedger) Gaps(q Query) ([]Gap, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	selected := make(map[string]struct{}, len(q.Assets))
	for _, assetID := range q.Assets {
		if _, exists := l.assets[assetID]; !exists {
			return nil, ErrUnknownAsset
		}
		selected[assetID] = struct{}{}
	}

	gaps := make([]Gap, 0)
	for assetID, asset := range l.assets {
		if len(selected) > 0 {
			if _, ok := selected[assetID]; !ok {
				continue
			}
		}
		if q.MinPriority > 0 && asset.Priority < q.MinPriority {
			continue
		}
		for _, ruleID := range asset.Rules {
			reason, isGap := l.gapReasonLocked(assetID, ruleID)
			if !isGap {
				continue
			}
			gaps = append(gaps, Gap{
				AssetID:  assetID,
				RuleID:   ruleID,
				Priority: asset.Priority,
				Reason:   reason,
			})
		}
	}

	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Priority != gaps[j].Priority {
			return gaps[i].Priority > gaps[j].Priority
		}
		if gaps[i].AssetID != gaps[j].AssetID {
			return gaps[i].AssetID < gaps[j].AssetID
		}
		return gaps[i].RuleID < gaps[j].RuleID
	})

	limit := q.Limit
	if limit == 0 {
		limit = l.opts.MaxGaps
	}
	if limit > 0 && len(gaps) > limit {
		gaps = gaps[:limit]
	}
	return gaps, nil
}

// Invalidate drops every coverage record of one asset.
func (l *ScanCoverageLedger) Invalidate(assetID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.assets[assetID]; !exists {
		return ErrUnknownAsset
	}
	delete(l.coverage, assetID)
	return nil
}

// Coverage returns the recorded combinations of one asset.
func (l *ScanCoverageLedger) Coverage(assetID string) []Coverage {
	l.mu.RLock()
	defer l.mu.RUnlock()

	byRule, exists := l.coverage[assetID]
	if !exists {
		return nil
	}

	records := make([]Coverage, 0)
	for _, versions := range byRule {
		for _, record := range versions {
			records = append(records, record.coverage)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].RuleID != records[j].RuleID {
			return records[i].RuleID < records[j].RuleID
		}
		if records[i].RuleVersion != records[j].RuleVersion {
			return records[i].RuleVersion < records[j].RuleVersion
		}
		if records[i].ScannerVersion != records[j].ScannerVersion {
			return records[i].ScannerVersion < records[j].ScannerVersion
		}
		if records[i].ScannerID != records[j].ScannerID {
			return records[i].ScannerID < records[j].ScannerID
		}
		return records[i].At < records[j].At
	})
	return records
}

// Assets lists the registered asset ids in ascending order.
func (l *ScanCoverageLedger) Assets() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	ids := make([]string, 0, len(l.assets))
	for id := range l.assets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SetRuleVersion switches the rule set version and invalidates old coverage.
func (l *ScanCoverageLedger) SetRuleVersion(v int) error {
	if v <= 0 {
		return ErrInvalidVersion
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.ruleVersion = v
	for _, byRule := range l.coverage {
		for _, versions := range byRule {
			for key, record := range versions {
				if key.ruleVersion != v {
					record.valid = false
					versions[key] = record
				}
			}
		}
	}
	return nil
}

// RuleVersion returns the current rule set version.
func (l *ScanCoverageLedger) RuleVersion() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.ruleVersion
}

// SetScannerVersion switches the scanner version and invalidates old coverage.
func (l *ScanCoverageLedger) SetScannerVersion(v int) error {
	if v <= 0 {
		return ErrInvalidVersion
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.scannerVersion = v
	for _, byRule := range l.coverage {
		for _, versions := range byRule {
			for key, record := range versions {
				if key.scannerVersion != v {
					record.valid = false
					versions[key] = record
				}
			}
		}
	}
	return nil
}

// ScannerVersion returns the current scanner version.
func (l *ScanCoverageLedger) ScannerVersion() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.scannerVersion
}

// ShardOf returns the stable shard index of an asset id.
func (l *ScanCoverageLedger) ShardOf(assetID string) int {
	l.mu.RLock()
	shards := l.opts.Shards
	l.mu.RUnlock()
	if shards < 1 {
		shards = 1
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(assetID))
	return int(hash.Sum64() % uint64(shards))
}

// Stats returns a consistent view of the counters.
func (l *ScanCoverageLedger) Stats() LedgerStats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	covered := 0
	gaps := 0
	for assetID, asset := range l.assets {
		for _, ruleID := range asset.Rules {
			if l.hasValidCoverageLocked(assetID, ruleID) {
				covered++
			} else {
				gaps++
			}
		}
	}
	return LedgerStats{
		Assets:         len(l.assets),
		Covered:        covered,
		Gaps:           gaps,
		Runs:           l.runs,
		RuleVersion:    l.ruleVersion,
		ScannerVersion: l.scannerVersion,
	}
}

// Snapshot serialises the whole ledger state.
func (l *ScanCoverageLedger) Snapshot() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	assets := make([]Asset, 0, len(l.assets))
	for _, asset := range l.assets {
		assets = append(assets, Asset{
			ID:       asset.ID,
			Rules:    append([]string(nil), asset.Rules...),
			Priority: asset.Priority,
		})
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].ID < assets[j].ID
	})

	records := make([]snapshotCoverage, 0)
	for _, byRule := range l.coverage {
		for _, versions := range byRule {
			for _, record := range versions {
				records = append(records, snapshotCoverage{
					Coverage: record.coverage,
					Valid:    record.valid,
				})
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.RuleVersion != b.RuleVersion {
			return a.RuleVersion < b.RuleVersion
		}
		if a.ScannerVersion != b.ScannerVersion {
			return a.ScannerVersion < b.ScannerVersion
		}
		if a.ScannerID != b.ScannerID {
			return a.ScannerID < b.ScannerID
		}
		return a.At < b.At
	})

	state := snapshotState{
		Magic:          ledgerSnapshotMagic,
		Format:         ledgerSnapshotFormat,
		MaxAssets:      l.opts.MaxAssets,
		MaxGaps:        l.opts.MaxGaps,
		Shards:         l.opts.Shards,
		Assets:         assets,
		Coverage:       records,
		Runs:           l.runs,
		RuleVersion:    l.ruleVersion,
		ScannerVersion: l.scannerVersion,
	}
	return json.Marshal(state)
}

// Restore replaces the whole ledger state from a snapshot.
func (l *ScanCoverageLedger) Restore(data []byte) error {
	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Magic != ledgerSnapshotMagic || state.Format != ledgerSnapshotFormat {
		return ErrBadSnapshot
	}
	if state.RuleVersion <= 0 || state.ScannerVersion <= 0 || state.Runs < 0 {
		return ErrBadSnapshot
	}
	if state.MaxAssets < 0 || state.MaxGaps < 0 || state.Shards < 0 {
		return ErrBadSnapshot
	}

	assets := make(map[string]Asset, len(state.Assets))
	for _, asset := range state.Assets {
		if strings.TrimSpace(asset.ID) == "" || len(asset.Rules) == 0 ||
			asset.Priority < minAssetPriority || asset.Priority > maxAssetPriority {
			return ErrBadSnapshot
		}
		if _, duplicate := assets[asset.ID]; duplicate {
			return ErrBadSnapshot
		}
		for i := 1; i < len(asset.Rules); i++ {
			if asset.Rules[i-1] >= asset.Rules[i] {
				return ErrBadSnapshot
			}
		}
		assets[asset.ID] = Asset{
			ID:       asset.ID,
			Rules:    append([]string(nil), asset.Rules...),
			Priority: asset.Priority,
		}
	}

	coverage := make(map[string]map[string]map[coverageVersion]coverageRecord)
	for _, record := range state.Coverage {
		asset, exists := assets[record.AssetID]
		if !exists || record.RuleVersion <= 0 || record.ScannerVersion <= 0 ||
			record.At <= 0 || strings.TrimSpace(record.ScannerID) == "" {
			return ErrBadSnapshot
		}
		if !containsRule(asset.Rules, record.RuleID) {
			return ErrBadSnapshot
		}

		byRule := coverage[record.AssetID]
		if byRule == nil {
			byRule = make(map[string]map[coverageVersion]coverageRecord)
			coverage[record.AssetID] = byRule
		}
		versions := byRule[record.RuleID]
		if versions == nil {
			versions = make(map[coverageVersion]coverageRecord)
			byRule[record.RuleID] = versions
		}
		key := coverageVersion{
			ruleVersion:    record.RuleVersion,
			scannerVersion: record.ScannerVersion,
		}
		if _, duplicate := versions[key]; duplicate {
			return ErrBadSnapshot
		}
		if record.Valid &&
			(record.RuleVersion != state.RuleVersion || record.ScannerVersion != state.ScannerVersion) {
			return ErrBadSnapshot
		}
		versions[key] = coverageRecord{
			coverage: Coverage{
				AssetID:        record.AssetID,
				RuleID:         record.RuleID,
				RuleVersion:    record.RuleVersion,
				ScannerID:      record.ScannerID,
				ScannerVersion: record.ScannerVersion,
				At:             record.At,
			},
			valid: record.Valid,
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.opts.MaxAssets = state.MaxAssets
	l.opts.MaxGaps = state.MaxGaps
	l.opts.Shards = state.Shards
	l.assets = assets
	l.coverage = coverage
	l.runs = state.Runs
	l.ruleVersion = state.RuleVersion
	l.scannerVersion = state.ScannerVersion
	return nil
}

// Reset returns the ledger to its initial state.
func (l *ScanCoverageLedger) Reset() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.assets = make(map[string]Asset)
	l.coverage = make(map[string]map[string]map[coverageVersion]coverageRecord)
	l.runs = 0
	l.ruleVersion = initialLedgerVersion
	l.scannerVersion = initialLedgerVersion
	return nil
}

func (l *ScanCoverageLedger) gapReasonLocked(assetID, ruleID string) (string, bool) {
	versions := l.coverage[assetID][ruleID]
	if len(versions) == 0 {
		return gapReasonMissing, true
	}
	for key, record := range versions {
		if record.valid &&
			key.ruleVersion == l.ruleVersion &&
			key.scannerVersion == l.scannerVersion {
			return "", false
		}
	}
	return gapReasonStale, true
}

func (l *ScanCoverageLedger) hasValidCoverageLocked(assetID, ruleID string) bool {
	for key, record := range l.coverage[assetID][ruleID] {
		if record.valid &&
			key.ruleVersion == l.ruleVersion &&
			key.scannerVersion == l.scannerVersion {
			return true
		}
	}
	return false
}

func containsRule(rules []string, rule string) bool {
	i := sort.SearchStrings(rules, rule)
	return i < len(rules) && rules[i] == rule
}
