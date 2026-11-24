package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the extractor.
var (
	ErrTruncated         = errors.New("binary_feature_extractor: truncated sample")
	ErrUnknownFormat     = errors.New("binary_feature_extractor: unknown format")
	ErrUnsupportedFormat = errors.New("binary_feature_extractor: unsupported format")
	ErrSectionLimit      = errors.New("binary_feature_extractor: too many sections")
	ErrImportLimit       = errors.New("binary_feature_extractor: too many imports")
	ErrFeatureLimit      = errors.New("binary_feature_extractor: feature table full")
	ErrBadSnapshot       = errors.New("binary_feature_extractor: bad snapshot")
)

// Format is the container format detected from the sample header.
type Format string

// Known container formats.
const (
	FormatPE      Format = "pe"
	FormatELF     Format = "elf"
	FormatUnknown Format = "unknown"
)

// Section is one section header entry.
type Section struct {
	Name        string
	VirtualSize uint32
	RawSize     uint32
	Offset      uint32
	Truncated   bool
}

// Import is one imported symbol.
type Import struct {
	Library string
	Symbol  string
	Ordinal uint16
}

// Feature is the searchable fingerprint of one sample.
type Feature struct {
	Digest   string
	Format   Format
	Sections []Section
	Imports  []Import
	Fuzzy    uint64
	Seen     int
}

// ExtractorOptions tunes section, import and feature table capacities.
type ExtractorOptions struct {
	MaxSections int
	MaxImports  int
	MaxFeatures int
}

// ExtractorStats is a consistent view of the extractor counters.
type ExtractorStats struct {
	Extracted int
	Rejected  int
	Sections  int
	Imports   int
	Features  int
}

// BinaryFingerprinter extracts stable features from PE and ELF samples.
type BinaryFingerprinter struct {
	opts ExtractorOptions
}

// NewBinaryFingerprinter builds a fingerprinter with the given options.
func NewBinaryFingerprinter(opts ExtractorOptions) *BinaryFingerprinter {
	return &BinaryFingerprinter{opts: opts}
}

// Detect reports the container format of the sample.
func (b *BinaryFingerprinter) Detect(data []byte) (Format, error) {
	return FormatUnknown, errNotImplemented
}

// Sections returns the section table sorted by name.
func (b *BinaryFingerprinter) Sections(data []byte) ([]Section, error) {
	return nil, errNotImplemented
}

// Imports returns the imported symbols sorted by library and name.
func (b *BinaryFingerprinter) Imports(data []byte) ([]Import, error) {
	return nil, errNotImplemented
}

// FuzzyHash returns the stable 64 bit fuzzy digest of the sample.
func (b *BinaryFingerprinter) FuzzyHash(data []byte) (uint64, error) {
	return 0, errNotImplemented
}

// Stable reports whether a feature is fully normalised.
func (b *BinaryFingerprinter) Stable(f Feature) bool {
	return false
}

// Extract builds the feature of one sample and appends it to the table.
func (b *BinaryFingerprinter) Extract(data []byte) (Feature, error) {
	return Feature{}, errNotImplemented
}

// Features returns the recorded feature table.
func (b *BinaryFingerprinter) Features() []Feature {
	return nil
}

// Capacity returns the maximum number of retained features.
func (b *BinaryFingerprinter) Capacity() int {
	return 0
}

// Reset drops the feature table and counters.
func (b *BinaryFingerprinter) Reset() error {
	return errNotImplemented
}

// Stats returns a consistent snapshot of the counters.
func (b *BinaryFingerprinter) Stats() ExtractorStats {
	return ExtractorStats{}
}

// Snapshot serialises the feature table and counters.
func (b *BinaryFingerprinter) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a state produced by Snapshot.
func (b *BinaryFingerprinter) Restore(data []byte) error {
	return errNotImplemented
}
