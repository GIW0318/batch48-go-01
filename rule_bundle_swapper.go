package task

import "errors"

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the rule bundle swapper.
var (
	ErrNilPack         = errors.New("rule_bundle_swapper: nil pack")
	ErrInvalidPack     = errors.New("rule_bundle_swapper: invalid pack")
	ErrVersionConflict = errors.New("rule_bundle_swapper: version conflict")
	ErrUnknownBundle   = errors.New("rule_bundle_swapper: unknown bundle")
	ErrBundleEvicted   = errors.New("rule_bundle_swapper: bundle evicted")
	ErrNotStaged       = errors.New("rule_bundle_swapper: bundle not staged")
	ErrHitNotFound     = errors.New("rule_bundle_swapper: hit not found")
	ErrStageLimit      = errors.New("rule_bundle_swapper: staged limit reached")
	ErrBadSnapshot     = errors.New("rule_bundle_swapper: bad snapshot")
	ErrInvalidLimit    = errors.New("rule_bundle_swapper: invalid limit")
)

// Rule is one matching rule inside a pack.
type Rule struct {
	ID       string
	Pattern  string
	Severity int
}

// RulePack is one complete, indivisible rule bundle.
type RulePack struct {
	Version uint64
	Name    string
	Rules   []Rule
}

// Hit is one match produced by the active pack.
type Hit struct {
	ID      string
	RuleID  string
	Version uint64
	Sample  string
}

// SwapperOptions bounds the staging area and the retained version set.
type SwapperOptions struct {
	MaxStaged   int
	MaxRetained int
}

// SwapperStats is the accumulated counter view of the swapper.
type SwapperStats struct {
	Staged    int
	Published int
	Rollbacks int
	Hits      int
	Evicted   int
}

// RuleBundleSwapper swaps whole rule packs at runtime.
type RuleBundleSwapper struct {
	opts SwapperOptions
}

// NewRuleBundleSwapper builds a swapper with the given limits.
func NewRuleBundleSwapper(opts SwapperOptions) *RuleBundleSwapper {
	return &RuleBundleSwapper{opts: opts}
}

// Stage validates a pack, parks it in the staging area and returns its version.
func (s *RuleBundleSwapper) Stage(p *RulePack) (uint64, error) {
	return 0, errNotImplemented
}

// Publish switches the live pack to a staged version.
func (s *RuleBundleSwapper) Publish(ver uint64) error {
	return errNotImplemented
}

// Rollback switches the live pack back to a previously published version.
func (s *RuleBundleSwapper) Rollback(ver uint64) error {
	return errNotImplemented
}

// Active returns the live pack, or nil when nothing was ever published.
func (s *RuleBundleSwapper) Active() *RulePack {
	return nil
}

// Evaluate scans a sample with the live pack and records every hit.
func (s *RuleBundleSwapper) Evaluate(sample string) ([]Hit, error) {
	return nil, errNotImplemented
}

// ResolveHit returns the pack version that produced a recorded hit.
func (s *RuleBundleSwapper) ResolveHit(hitID string) (uint64, error) {
	return 0, errNotImplemented
}

// Retained lists the versions still resolvable for recorded hits, ascending.
func (s *RuleBundleSwapper) Retained() []uint64 {
	return nil
}

// Pack returns a staged or retained pack by version.
func (s *RuleBundleSwapper) Pack(ver uint64) (*RulePack, bool) {
	return nil, false
}

// Digest is the canonical content digest of a pack; rule order does not matter.
func (s *RuleBundleSwapper) Digest(p *RulePack) string {
	return ""
}

// Stats returns a consistent view of the counters.
func (s *RuleBundleSwapper) Stats() SwapperStats {
	return SwapperStats{}
}

// Snapshot serialises the whole swapper state.
func (s *RuleBundleSwapper) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole swapper state from a snapshot.
func (s *RuleBundleSwapper) Restore(data []byte) error {
	return errNotImplemented
}

// Reset returns the swapper to its initial state.
func (s *RuleBundleSwapper) Reset() error {
	return errNotImplemented
}
