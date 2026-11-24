package task

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

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

const snapshotFormat = "rule-bundle-swapper-snapshot-v1"

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
	mu   sync.RWMutex
	opts SwapperOptions

	staged       map[uint64]*RulePack
	retained     map[uint64]*RulePack
	published    map[uint64]struct{}
	offlineOrder []uint64
	active       *RulePack

	hits      map[string]uint64
	nextHitID uint64
	stats     SwapperStats
}

// NewRuleBundleSwapper builds a swapper with the given limits.
func NewRuleBundleSwapper(opts SwapperOptions) *RuleBundleSwapper {
	return &RuleBundleSwapper{
		opts:      opts,
		staged:    make(map[uint64]*RulePack),
		retained:  make(map[uint64]*RulePack),
		published: make(map[uint64]struct{}),
		hits:      make(map[string]uint64),
	}
}

func (s *RuleBundleSwapper) ensureStateLocked() {
	if s.staged == nil {
		s.staged = make(map[uint64]*RulePack)
	}
	if s.retained == nil {
		s.retained = make(map[uint64]*RulePack)
	}
	if s.published == nil {
		s.published = make(map[uint64]struct{})
	}
	if s.hits == nil {
		s.hits = make(map[string]uint64)
	}
}

func clonePack(pack *RulePack) *RulePack {
	if pack == nil {
		return nil
	}
	clone := *pack
	clone.Rules = append([]Rule(nil), pack.Rules...)
	return &clone
}

func validatePack(pack *RulePack) error {
	if pack.Version == 0 || pack.Name == "" || len(pack.Rules) == 0 {
		return ErrInvalidPack
	}

	seenIDs := make(map[string]struct{}, len(pack.Rules))
	for _, rule := range pack.Rules {
		if rule.ID == "" || rule.Pattern == "" {
			return ErrInvalidPack
		}
		if _, exists := seenIDs[rule.ID]; exists {
			return ErrInvalidPack
		}
		seenIDs[rule.ID] = struct{}{}
		if rule.Severity < 1 || rule.Severity > 5 {
			return ErrInvalidPack
		}
	}
	return nil
}

func writeDigestUint(writer io.Writer, value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	_, _ = writer.Write(buffer[:])
}

func writeDigestString(writer io.Writer, value string) {
	writeDigestUint(writer, uint64(len(value)))
	_, _ = io.WriteString(writer, value)
}

func digestPack(pack *RulePack) string {
	if pack == nil {
		return ""
	}

	hasher := sha256.New()
	writeDigestUint(hasher, uint64(len(pack.Rules)))

	ruleDigests := make([]string, 0, len(pack.Rules))
	for _, rule := range pack.Rules {
		var ruleDigest strings.Builder
		writeDigestString(&ruleDigest, rule.ID)
		writeDigestString(&ruleDigest, rule.Pattern)
		writeDigestUint(&ruleDigest, uint64(rule.Severity))
		ruleDigests = append(ruleDigests, ruleDigest.String())
	}
	sort.Strings(ruleDigests)

	for _, ruleDigest := range ruleDigests {
		writeDigestString(hasher, ruleDigest)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// Stage validates a pack, parks it in the staging area and returns its version.
func (s *RuleBundleSwapper) Stage(pack *RulePack) (uint64, error) {
	if pack == nil {
		return 0, ErrNilPack
	}
	if err := validatePack(pack); err != nil {
		return 0, err
	}

	contentDigest := digestPack(pack)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureStateLocked()

	if existing, exists := s.staged[pack.Version]; exists {
		if existing.Name != pack.Name || digestPack(existing) != contentDigest {
			return 0, ErrVersionConflict
		}
		return pack.Version, nil
	}

	if len(s.staged) >= s.opts.MaxStaged {
		return 0, ErrStageLimit
	}

	s.staged[pack.Version] = clonePack(pack)
	s.stats.Staged++
	return pack.Version, nil
}

func removeUint64(values []uint64, target uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func (s *RuleBundleSwapper) activateLocked(pack *RulePack) {
	version := pack.Version
	oldVersion := uint64(0)
	if s.active != nil {
		oldVersion = s.active.Version
	}

	if oldVersion == version {
		s.active = pack
		return
	}

	s.offlineOrder = removeUint64(s.offlineOrder, version)
	if oldVersion != 0 {
		s.offlineOrder = append(s.offlineOrder, oldVersion)
	}
	s.active = pack
}

func (s *RuleBundleSwapper) enforceRetentionLocked() {
	for len(s.retained) > s.opts.MaxRetained {
		evictedVersion := uint64(0)

		for len(s.offlineOrder) > 0 {
			candidate := s.offlineOrder[0]
			s.offlineOrder = s.offlineOrder[1:]
			if _, exists := s.retained[candidate]; !exists {
				continue
			}
			if s.active != nil && candidate == s.active.Version {
				continue
			}
			evictedVersion = candidate
			break
		}

		if evictedVersion == 0 {
			for version := range s.retained {
				if s.active != nil && version == s.active.Version {
					continue
				}
				if evictedVersion == 0 || version < evictedVersion {
					evictedVersion = version
				}
			}
		}

		if evictedVersion == 0 {
			return
		}

		delete(s.retained, evictedVersion)
		s.offlineOrder = removeUint64(s.offlineOrder, evictedVersion)
		s.stats.Evicted++
	}
}

// Publish switches the live pack to a staged version.
func (s *RuleBundleSwapper) Publish(version uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureStateLocked()

	if s.active != nil && s.active.Version == version {
		return nil
	}

	pack, exists := s.staged[version]
	if !exists {
		return ErrNotStaged
	}

	delete(s.staged, version)
	s.retained[version] = pack
	s.published[version] = struct{}{}
	s.activateLocked(pack)
	s.stats.Published++
	s.enforceRetentionLocked()
	return nil
}

// Rollback switches the live pack back to a previously published version.
func (s *RuleBundleSwapper) Rollback(version uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureStateLocked()

	if _, published := s.published[version]; !published {
		return ErrUnknownBundle
	}

	pack, retained := s.retained[version]
	if !retained {
		return ErrBundleEvicted
	}

	if s.active != nil && s.active.Version == version {
		return nil
	}

	s.activateLocked(pack)
	s.stats.Rollbacks++
	return nil
}

// Active returns the live pack, or nil when nothing was ever published.
func (s *RuleBundleSwapper) Active() *RulePack {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePack(s.active)
}

// Evaluate scans a sample with the live pack and records every hit.
func (s *RuleBundleSwapper) Evaluate(sample string) ([]Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active == nil {
		return nil, ErrUnknownBundle
	}

	pack := s.active
	hits := make([]Hit, 0, len(pack.Rules))

	for _, rule := range pack.Rules {
		if !strings.Contains(sample, rule.Pattern) {
			continue
		}

		s.nextHitID++
		hitID := "hit-" + strconv.FormatUint(s.nextHitID, 10)
		hit := Hit{
			ID:      hitID,
			RuleID:  rule.ID,
			Version: pack.Version,
			Sample:  sample,
		}
		hits = append(hits, hit)
		s.hits[hitID] = pack.Version
		s.stats.Hits++
	}

	return hits, nil
}

// ResolveHit returns the pack version that produced a recorded hit.
func (s *RuleBundleSwapper) ResolveHit(hitID string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	version, exists := s.hits[hitID]
	if !exists {
		return 0, ErrHitNotFound
	}
	return version, nil
}

// Retained lists the versions still available for rollback, ascending.
func (s *RuleBundleSwapper) Retained() []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	versions := make([]uint64, 0, len(s.retained))
	for version := range s.retained {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i] < versions[j]
	})
	return versions
}

// Pack returns a staged or retained pack by version.
func (s *RuleBundleSwapper) Pack(version uint64) (*RulePack, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if pack, exists := s.staged[version]; exists {
		return clonePack(pack), true
	}
	if pack, exists := s.retained[version]; exists {
		return clonePack(pack), true
	}
	return nil, false
}

// Digest is the canonical content digest of a pack; rule order does not matter.
func (s *RuleBundleSwapper) Digest(pack *RulePack) string {
	return digestPack(pack)
}

// Stats returns a consistent view of the counters.
func (s *RuleBundleSwapper) Stats() SwapperStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

type snapshotData struct {
	Staged       map[uint64]*RulePack `json:"staged"`
	Retained     map[uint64]*RulePack `json:"retained"`
	Published    map[uint64]struct{}  `json:"published"`
	OfflineOrder []uint64             `json:"offline_order"`
	ActiveVer    uint64               `json:"active_version"`
	Hits         map[string]uint64    `json:"hits"`
	NextHitID    uint64               `json:"next_hit_id"`
	Stats        SwapperStats         `json:"stats"`
}

type snapshotEnvelope struct {
	Format   string          `json:"format"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Snapshot serialises the whole swapper state.
func (s *RuleBundleSwapper) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	payload, err := json.Marshal(snapshotData{
		Staged:       s.staged,
		Retained:     s.retained,
		Published:    s.published,
		OfflineOrder: append([]uint64(nil), s.offlineOrder...),
		ActiveVer:    activeVersionLocked(s.active),
		Hits:         s.hits,
		NextHitID:    s.nextHitID,
		Stats:        s.stats,
	})
	if err != nil {
		return nil, err
	}
	checksum := sha256.Sum256(payload)
	envelope := snapshotEnvelope{
		Format:   snapshotFormat,
		Checksum: hex.EncodeToString(checksum[:]),
		Payload:  payload,
	}
	return json.Marshal(envelope)
}

func activeVersionLocked(active *RulePack) uint64 {
	if active == nil {
		return 0
	}
	return active.Version
}

func validateSnapshot(data *snapshotData) error {
	if data == nil {
		return errors.New("empty snapshot")
	}

	for version, pack := range data.Staged {
		if pack == nil || pack.Version != version || validatePack(pack) != nil {
			return errors.New("invalid staged pack")
		}
	}

	for version, pack := range data.Retained {
		if pack == nil || pack.Version != version || validatePack(pack) != nil {
			return errors.New("invalid retained pack")
		}
	}

	if data.ActiveVer != 0 {
		pack, exists := data.Retained[data.ActiveVer]
		if !exists || pack == nil {
			return errors.New("active pack is not retained")
		}
	}

	offlineSeen := make(map[uint64]struct{}, len(data.OfflineOrder))
	for _, version := range data.OfflineOrder {
		if _, exists := data.Retained[version]; !exists {
			return errors.New("offline version is not retained")
		}
		if version == data.ActiveVer {
			return errors.New("active version is offline")
		}
		if _, duplicate := offlineSeen[version]; duplicate {
			return errors.New("duplicate offline version")
		}
		offlineSeen[version] = struct{}{}
	}

	for version := range data.Retained {
		if version != data.ActiveVer {
			if _, listed := offlineSeen[version]; !listed {
				return errors.New("retained offline version lacks ordering")
			}
		}
		if data.Published == nil {
			return errors.New("missing published versions")
		}
		if _, published := data.Published[version]; !published {
			return errors.New("retained version was not published")
		}
	}

	for version := range data.Published {
		if version == 0 {
			return errors.New("invalid published version")
		}
	}

	for hitID, version := range data.Hits {
		if hitID == "" || version == 0 {
			return errors.New("invalid hit binding")
		}
		if len(data.Published) > 0 {
			if _, published := data.Published[version]; !published {
				return errors.New("hit references unpublished version")
			}
		}
	}

	if data.Stats.Staged < 0 || data.Stats.Published < 0 || data.Stats.Rollbacks < 0 ||
		data.Stats.Hits < 0 || data.Stats.Evicted < 0 {
		return errors.New("negative statistic")
	}

	nextHitID := data.NextHitID
	for hitID := range data.Hits {
		if !strings.HasPrefix(hitID, "hit-") {
			continue
		}
		sequence, err := strconv.ParseUint(strings.TrimPrefix(hitID, "hit-"), 10, 64)
		if err == nil && sequence > nextHitID {
			nextHitID = sequence
		}
	}
	data.NextHitID = nextHitID

	return nil
}

// Restore replaces the whole swapper state from a snapshot.
func (s *RuleBundleSwapper) Restore(raw []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%w: decode envelope: %v", ErrBadSnapshot, err)
	}
	if envelope.Format != snapshotFormat || len(envelope.Payload) == 0 || envelope.Checksum == "" {
		return fmt.Errorf("%w: invalid envelope", ErrBadSnapshot)
	}

	checksum := sha256.Sum256(envelope.Payload)
	if envelope.Checksum != hex.EncodeToString(checksum[:]) {
		return fmt.Errorf("%w: checksum mismatch", ErrBadSnapshot)
	}

	var data snapshotData
	if err := json.Unmarshal(envelope.Payload, &data); err != nil {
		return fmt.Errorf("%w: decode state: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(&data); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.staged = make(map[uint64]*RulePack, len(data.Staged))
	for version, pack := range data.Staged {
		s.staged[version] = clonePack(pack)
	}

	s.retained = make(map[uint64]*RulePack, len(data.Retained))
	for version, pack := range data.Retained {
		s.retained[version] = clonePack(pack)
	}

	s.published = make(map[uint64]struct{}, len(data.Published))
	for version := range data.Published {
		s.published[version] = struct{}{}
	}

	s.hits = make(map[string]uint64, len(data.Hits))
	for hitID, version := range data.Hits {
		s.hits[hitID] = version
	}

	s.offlineOrder = append([]uint64(nil), data.OfflineOrder...)
	if data.ActiveVer != 0 {
		s.active = clonePack(s.retained[data.ActiveVer])
	} else {
		s.active = nil
	}
	s.nextHitID = data.NextHitID
	s.stats = data.Stats
	return nil
}

// Reset returns the swapper to its initial state.
func (s *RuleBundleSwapper) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.staged = make(map[uint64]*RulePack)
	s.retained = make(map[uint64]*RulePack)
	s.published = make(map[uint64]struct{})
	s.offlineOrder = nil
	s.active = nil
	s.hits = make(map[string]uint64)
	s.nextHitID = 0
	s.stats = SwapperStats{}
	return nil
}
