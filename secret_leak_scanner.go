package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the secret leak scanner.
var (
	ErrFileTooLarge     = errors.New("secret_leak_scanner: file over size limit")
	ErrCandidateLimit   = errors.New("secret_leak_scanner: candidate limit reached")
	ErrInvalidBaseline  = errors.New("secret_leak_scanner: invalid baseline fingerprint")
	ErrInvalidContext   = errors.New("secret_leak_scanner: invalid context")
	ErrInvalidThreshold = errors.New("secret_leak_scanner: invalid entropy threshold")
	ErrInvalidOptions   = errors.New("secret_leak_scanner: invalid options")
	ErrBadSnapshot      = errors.New("secret_leak_scanner: bad snapshot")
)

// Location is one 1-based line and column of a secret occurrence.
type Location struct {
	Line   int
	Column int
}

// Finding is one deduplicated hardcoded credential.
type Finding struct {
	RuleID      string
	Path        string
	Value       string
	Occurrences []Location
	Entropy     float64
}

// Context is one allowlisted scan context.
type Context struct {
	PathSuffix string
	Marker     string
	Reason     string
}

// BaselineDiff separates newly appeared findings from disappeared baseline ones.
type BaselineDiff struct {
	Added   []string
	Removed []string
}

// ScannerOptions tunes candidate detection and scan limits.
type ScannerOptions struct {
	MaxFileBytes  int
	MaxCandidates int
	MinEntropy    float64
	Prefixes      []string
}

// ScannerStats is the accumulated counter view of the scanner.
type ScannerStats struct {
	Files      int
	Candidates int
	Findings   int
	Suppressed int
}

// SecretLeakScanner finds hardcoded credentials in text and repository trees.
type SecretLeakScanner struct {
	opts ScannerOptions
}

// NewSecretLeakScanner builds a scanner with the given limits.
func NewSecretLeakScanner(opts ScannerOptions) *SecretLeakScanner {
	return &SecretLeakScanner{opts: opts}
}

// ScanFile scans one file content and replaces the current result set.
func (s *SecretLeakScanner) ScanFile(path string, data []byte) ([]Finding, error) {
	return nil, errNotImplemented
}

// ScanTree scans a directory tree in path order and replaces the result set.
func (s *SecretLeakScanner) ScanTree(root string) ([]Finding, error) {
	return nil, errNotImplemented
}

// SetBaseline installs the accepted fingerprint set.
func (s *SecretLeakScanner) SetBaseline(fingerprints []string) error {
	return errNotImplemented
}

// Allow registers one allowlisted scanning context.
func (s *SecretLeakScanner) Allow(ctx Context) error {
	return errNotImplemented
}

// Findings returns the findings of the most recent scan.
func (s *SecretLeakScanner) Findings() []Finding {
	return nil
}

// BaselineDiff compares the baseline with the most recent scan.
func (s *SecretLeakScanner) BaselineDiff() (BaselineDiff, error) {
	return BaselineDiff{}, errNotImplemented
}

// Entropy is the Shannon entropy of the given string in bits per byte.
func (s *SecretLeakScanner) Entropy(v string) float64 {
	return 0
}

// Fingerprint is the stable 64 character hexadecimal identity of a finding.
func (s *SecretLeakScanner) Fingerprint(f Finding) string {
	return ""
}

// Stats returns a consistent view of the counters.
func (s *SecretLeakScanner) Stats() ScannerStats {
	return ScannerStats{}
}

// Snapshot serialises the whole scanner state.
func (s *SecretLeakScanner) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole scanner state from a snapshot.
func (s *SecretLeakScanner) Restore(data []byte) error {
	return errNotImplemented
}

// Reset returns the scanner to its initial state.
func (s *SecretLeakScanner) Reset() error {
	return errNotImplemented
}
