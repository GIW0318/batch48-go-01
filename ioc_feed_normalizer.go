package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the IOC normalizer.
var (
	ErrInvalidIndicator = errors.New("ioc_feed_normalizer: invalid indicator")
	ErrTypeConflict     = errors.New("ioc_feed_normalizer: indicator type conflict")
	ErrInvalidFeed      = errors.New("ioc_feed_normalizer: invalid feed")
	ErrFeedTooLarge     = errors.New("ioc_feed_normalizer: feed over indicator limit")
	ErrSetLimit         = errors.New("ioc_feed_normalizer: merged set over limit")
	ErrUnknownSource    = errors.New("ioc_feed_normalizer: unknown source")
	ErrBadSnapshot      = errors.New("ioc_feed_normalizer: bad snapshot")
	ErrInvalidLimit     = errors.New("ioc_feed_normalizer: invalid limit")
)

// Indicator is one raw threat indicator as reported by a feed.
type Indicator struct {
	Value  string
	Kind   string
	TTL    int64
	Source string
}

// Feed is one source batch of indicators.
type Feed struct {
	Name       string
	DefaultTTL int64
	Indicators []Indicator
}

// Source is one feed that contributed to a merged entry.
type Source struct {
	Name string
	TTL  int64
}

// IocEntry is one normalised indicator with every contributing source.
type IocEntry struct {
	Value   string
	Kind    string
	TTL     int64
	Sources []Source
}

// IocSet is the merged, ordered result of a normalisation round.
type IocSet struct {
	Entries []IocEntry
}

// Len reports how many entries the set holds.
func (s *IocSet) Len() int {
	return 0
}

// Values returns the normalised indicator values in stable order.
func (s *IocSet) Values() []string {
	return nil
}

// NormalizerOptions bounds one feed and the merged set.
type NormalizerOptions struct {
	MaxEntries int
	MaxPerFeed int
}

// NormalizerStats is the accumulated counter view of the normalizer.
type NormalizerStats struct {
	Feeds    int
	Ingested int
	Rejected int
	Entries  int
}

// IocNormalizer folds multi-source intelligence into one matchable set.
type IocNormalizer struct {
	opts NormalizerOptions
}

// NewIocNormalizer builds a normalizer with the given limits.
func NewIocNormalizer(opts NormalizerOptions) *IocNormalizer {
	return &IocNormalizer{opts: opts}
}

// Ingest validates and stores one whole feed.
func (n *IocNormalizer) Ingest(f Feed) error {
	return errNotImplemented
}

// Normalize canonicalises one indicator and infers its kind.
func (n *IocNormalizer) Normalize(ind Indicator) (Indicator, error) {
	return Indicator{}, errNotImplemented
}

// Merge folds every ingested feed into one ordered set.
func (n *IocNormalizer) Merge() (*IocSet, error) {
	return nil, errNotImplemented
}

// Lookup returns the sources of a value, matching IPs by prefix containment.
func (n *IocNormalizer) Lookup(v string) ([]Source, bool) {
	return nil, false
}

// Entries returns the entries of the last successful merge.
func (n *IocNormalizer) Entries() []IocEntry {
	return nil
}

// DropSource removes every indicator contributed by a feed.
func (n *IocNormalizer) DropSource(name string) error {
	return errNotImplemented
}

// Stats returns a consistent view of the counters.
func (n *IocNormalizer) Stats() NormalizerStats {
	return NormalizerStats{}
}

// Snapshot serialises the whole normalizer state.
func (n *IocNormalizer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole normalizer state from a snapshot.
func (n *IocNormalizer) Restore(data []byte) error {
	return errNotImplemented
}

// Reset returns the normalizer to its initial state.
func (n *IocNormalizer) Reset() error {
	return errNotImplemented
}
