package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

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
	ErrBadSnapshot         = errors.New("scan_coverage_ledger: bad snapshot")
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

// ScanCoverageLedger tracks which asset and rule pairs were really scanned.
type ScanCoverageLedger struct {
	opts LedgerOptions
}

// NewScanCoverageLedger builds a ledger with the given limits.
func NewScanCoverageLedger(opts LedgerOptions) *ScanCoverageLedger {
	return &ScanCoverageLedger{opts: opts}
}

// RegisterAsset adds one asset with its expected rule set.
func (l *ScanCoverageLedger) RegisterAsset(a Asset) error {
	return errNotImplemented
}

// MarkRun records a successful scanner report as valid coverage.
func (l *ScanCoverageLedger) MarkRun(r Run) error {
	return errNotImplemented
}

// Gaps reports the missing or stale coverage combinations.
func (l *ScanCoverageLedger) Gaps(q Query) ([]Gap, error) {
	return nil, errNotImplemented
}

// Invalidate drops every coverage record of one asset.
func (l *ScanCoverageLedger) Invalidate(assetID string) error {
	return errNotImplemented
}

// Coverage returns the recorded combinations of one asset.
func (l *ScanCoverageLedger) Coverage(assetID string) []Coverage {
	return nil
}

// Assets lists the registered asset ids in ascending order.
func (l *ScanCoverageLedger) Assets() []string {
	return nil
}

// SetRuleVersion switches the rule set version and invalidates old coverage.
func (l *ScanCoverageLedger) SetRuleVersion(v int) error {
	return errNotImplemented
}

// RuleVersion returns the current rule set version.
func (l *ScanCoverageLedger) RuleVersion() int {
	return 0
}

// SetScannerVersion switches the scanner version and invalidates old coverage.
func (l *ScanCoverageLedger) SetScannerVersion(v int) error {
	return errNotImplemented
}

// ScannerVersion returns the current scanner version.
func (l *ScanCoverageLedger) ScannerVersion() int {
	return 0
}

// ShardOf returns the stable shard index of an asset id.
func (l *ScanCoverageLedger) ShardOf(assetID string) int {
	return 0
}

// Stats returns a consistent view of the counters.
func (l *ScanCoverageLedger) Stats() LedgerStats {
	return LedgerStats{}
}

// Snapshot serialises the whole ledger state.
func (l *ScanCoverageLedger) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole ledger state from a snapshot.
func (l *ScanCoverageLedger) Restore(data []byte) error {
	return errNotImplemented
}

// Reset returns the ledger to its initial state.
func (l *ScanCoverageLedger) Reset() error {
	return errNotImplemented
}
