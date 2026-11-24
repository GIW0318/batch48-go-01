// Package task scans large files and network streams in fixed chunks for exact
// and gapped signatures, reporting every hit once with absolute offsets.
package task

import "errors"

var errNotImplemented = errors.New("stream signature scanner: not implemented")

// Errors surfaced while building indexes and feeding chunks.
var (
	ErrNoIndex            = errors.New("stream signature scanner: no index installed")
	ErrPatternInvalid     = errors.New("stream signature scanner: pattern invalid")
	ErrAtomTooShort       = errors.New("stream signature scanner: atom too short")
	ErrTooManyAtoms       = errors.New("stream signature scanner: too many atoms")
	ErrBadWindow          = errors.New("stream signature scanner: bad window")
	ErrChunkTooLarge      = errors.New("stream signature scanner: chunk too large")
	ErrOffsetGap          = errors.New("stream signature scanner: offset gap")
	ErrCandidatesExceeded = errors.New("stream signature scanner: candidates exceeded")
)

// PatternKind selects how a pattern is matched.
type PatternKind int

// Supported pattern kinds.
const (
	Exact PatternKind = iota
	Gapped
)

// Pattern is one signature to look for in the stream.
type Pattern struct {
	ID     string
	Kind   PatternKind
	Prefix []byte
	Suffix []byte
	Gap    int
}

// Hit is one confirmed match with absolute stream offsets; End is exclusive.
type Hit struct {
	Pattern string
	Start   int64
	End     int64
}

// AtomIndex is one complete, immutable build of the pattern bucketing.
type AtomIndex struct {
	version int
}

// Options configures a scanner instance.
type Options struct {
	Window        int
	MaxChunk      int
	MaxAtoms      int
	MaxCandidates int
}

// ScanStats counts scanning activity.
type ScanStats struct {
	Chunks       uint64
	Bytes        int64
	Hits         uint64
	Candidates   uint64
	IndexVersion int
}

// StreamSignatureScanner matches patterns against a stream fed in chunks.
type StreamSignatureScanner struct {
	opts Options
}

// NewStreamSignatureScanner returns a scanner configured by opts.
func NewStreamSignatureScanner(opts Options) *StreamSignatureScanner {
	return &StreamSignatureScanner{opts: opts}
}

// BuildAtoms validates patterns, builds an atom index and installs it.
func (s *StreamSignatureScanner) BuildAtoms(patterns []Pattern) (*AtomIndex, error) {
	return nil, errNotImplemented
}

// Install switches the scanner to a previously built index.
func (s *StreamSignatureScanner) Install(idx *AtomIndex) error {
	return errNotImplemented
}

// Feed scans one chunk that starts at absolute offset off.
func (s *StreamSignatureScanner) Feed(chunk []byte, off int64) ([]Hit, error) {
	return nil, errNotImplemented
}

// Flush confirms hits still held in the trailing window.
func (s *StreamSignatureScanner) Flush() []Hit {
	return nil
}

// SetWindow sets how many trailing bytes are held back before reporting.
func (s *StreamSignatureScanner) SetWindow(n int) error {
	return errNotImplemented
}

// Window reports the current trailing window size.
func (s *StreamSignatureScanner) Window() int {
	return 0
}

// Stats reports counters since the last reset.
func (s *StreamSignatureScanner) Stats() ScanStats {
	return ScanStats{}
}

// Reset drops the installed index, pending candidates and counters.
func (s *StreamSignatureScanner) Reset() error {
	return errNotImplemented
}

// Size reports how many atom buckets the index holds.
func (idx *AtomIndex) Size() int {
	return 0
}

// Patterns reports the pattern identifiers in lexical order.
func (idx *AtomIndex) Patterns() []string {
	return nil
}

// Version reports the build generation of the index.
func (idx *AtomIndex) Version() int {
	return 0
}

// Fingerprint returns a stable digest of the indexed patterns.
func (idx *AtomIndex) Fingerprint() uint64 {
	return 0
}
