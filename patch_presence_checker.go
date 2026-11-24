package task

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

const (
	defaultMaxBatch  = 1024
	defaultCacheSize = 128

	snapshotMagic   = "gsb/batch48-go-safety-c-14:patch-presence-checker"
	snapshotVersion = 1

	statusNotAffected = "not_affected"
	statusFixed       = "fixed"
	statusVulnerable  = "vulnerable"
	statusUncertain   = "uncertain"
)

// Sentinel errors reported by the patch presence checker.
var (
	ErrInvalidFix      = errors.New("patch_presence_checker: invalid fix")
	ErrDuplicateFix    = errors.New("patch_presence_checker: duplicate fix")
	ErrUnknownFix      = errors.New("patch_presence_checker: unknown fix")
	ErrInvalidVersion  = errors.New("patch_presence_checker: invalid version")
	ErrInvalidRef      = errors.New("patch_presence_checker: invalid artifact reference")
	ErrBatchTooLarge   = errors.New("patch_presence_checker: batch over limit")
	ErrUnknownArtifact = errors.New("patch_presence_checker: unknown artifact")
	ErrBadSnapshot     = errors.New("patch_presence_checker: bad snapshot")
	ErrInvalidLimit    = errors.New("patch_presence_checker: invalid limit")
)

// Fix is one upstream fix with its version range and function fingerprint.
type Fix struct {
	ID           string
	Component    string
	IntroducedIn string
	FixedIn      string
	Function     string
	Digest       string
}

// Function is one function of a built artifact as a normalised instruction list.
type Function struct {
	Name         string
	Instructions []string
}

// ArtifactRef identifies one built artifact and carries its functions.
type ArtifactRef struct {
	ID        string
	Component string
	Version   string
	Functions []Function
}

// Presence is the verdict for one artifact.
type Presence struct {
	Status     string
	Confidence int
	Evidence   []string
}

// Explanation replays the evidence steps behind one verdict.
type Explanation struct {
	FixID string
	Steps []string
}

// CheckerOptions bounds the batch size and the verdict cache.
type CheckerOptions struct {
	MaxBatch  int
	CacheSize int
}

// CheckerStats is the accumulated counter view of the checker.
type CheckerStats struct {
	Registered int
	Checked    int
	CacheHits  int
	Uncertain  int
}

type cacheEntry struct {
	id          string
	presence    Presence
	explanation Explanation
}

type flight struct {
	done        chan struct{}
	presence    Presence
	explanation Explanation
	err         error
}

// PatchPresenceChecker decides whether an artifact already contains a fix.
type PatchPresenceChecker struct {
	opts CheckerOptions

	mu           sync.Mutex
	fixes        map[string]Fix
	byComponent  map[string][]string
	cache        map[string]*list.Element
	lru          *list.List
	explanations map[string]Explanation
	stats        CheckerStats
	inflight     map[string]*flight
}

// NewPatchPresenceChecker builds a checker with the given limits.
func NewPatchPresenceChecker(opts CheckerOptions) *PatchPresenceChecker {
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = defaultMaxBatch
	}
	if opts.CacheSize <= 0 {
		opts.CacheSize = defaultCacheSize
	}
	c := &PatchPresenceChecker{opts: opts}
	c.resetLocked()
	return c
}

func (c *PatchPresenceChecker) resetLocked() {
	c.fixes = make(map[string]Fix)
	c.byComponent = make(map[string][]string)
	c.cache = make(map[string]*list.Element)
	c.lru = list.New()
	c.explanations = make(map[string]Explanation)
	c.stats = CheckerStats{}
	c.inflight = make(map[string]*flight)
}

func (c *PatchPresenceChecker) ensureInitLocked() {
	if c.fixes == nil {
		c.fixes = make(map[string]Fix)
	}
	if c.byComponent == nil {
		c.byComponent = make(map[string][]string)
	}
	if c.cache == nil {
		c.cache = make(map[string]*list.Element)
	}
	if c.lru == nil {
		c.lru = list.New()
	}
	if c.explanations == nil {
		c.explanations = make(map[string]Explanation)
	}
	if c.inflight == nil {
		c.inflight = make(map[string]*flight)
	}
	if c.opts.MaxBatch <= 0 {
		c.opts.MaxBatch = defaultMaxBatch
	}
	if c.opts.CacheSize <= 0 {
		c.opts.CacheSize = defaultCacheSize
	}
}

// Register adds one fix to the checker.
func (c *PatchPresenceChecker) Register(f Fix) error {
	if !validFix(f) {
		return ErrInvalidFix
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitLocked()

	if _, exists := c.fixes[f.ID]; exists {
		return ErrDuplicateFix
	}
	c.fixes[f.ID] = f
	c.byComponent[f.Component] = append(c.byComponent[f.Component], f.ID)
	c.stats.Registered++
	return nil
}

func validFix(f Fix) bool {
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Component) == "" {
		return false
	}
	if !isHexDigest(f.Digest) {
		return false
	}
	cmp, err := compareVersionStrings(f.IntroducedIn, f.FixedIn)
	if err != nil || cmp >= 0 {
		return false
	}
	return true
}

func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'f') ||
			(ch >= 'A' && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}

// Check decides the presence of the component fix in one artifact.
func (c *PatchPresenceChecker) Check(ref ArtifactRef) (Presence, error) {
	if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.Component) == "" {
		return Presence{}, ErrInvalidRef
	}

	c.mu.Lock()
	c.ensureInitLocked()

	ids, known := c.byComponent[ref.Component]
	if !known {
		c.mu.Unlock()
		return Presence{}, ErrUnknownFix
	}

	if elem, ok := c.cache[ref.ID]; ok {
		entry := elem.Value.(*cacheEntry)
		c.stats.CacheHits++
		c.lru.MoveToFront(elem)
		presence := clonePresence(entry.presence)
		c.mu.Unlock()
		return presence, nil
	}

	if f, ok := c.inflight[ref.ID]; ok {
		c.mu.Unlock()
		<-f.done

		c.mu.Lock()
		err := f.err
		var presence Presence
		if err == nil {
			c.stats.CacheHits++
			if elem, found := c.cache[ref.ID]; found {
				c.lru.MoveToFront(elem)
			}
			presence = clonePresence(f.presence)
		}
		c.mu.Unlock()
		if err != nil {
			return Presence{}, err
		}
		return presence, nil
	}

	candidates := make([]Fix, 0, len(ids))
	for _, id := range ids {
		candidates = append(candidates, c.fixes[id])
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[ref.ID] = f
	c.mu.Unlock()

	presence, explanation, err := c.evaluate(ref, candidates)

	c.mu.Lock()
	if err == nil {
		c.stats.Checked++
		if presence.Status == statusUncertain {
			c.stats.Uncertain++
		}
		c.cacheVerdictLocked(ref.ID, presence, explanation)
		c.explanations[ref.ID] = cloneExplanation(explanation)
	}
	f.presence = clonePresence(presence)
	f.explanation = cloneExplanation(explanation)
	f.err = err
	delete(c.inflight, ref.ID)
	close(f.done)
	c.mu.Unlock()

	if err != nil {
		return Presence{}, err
	}
	return presence, nil
}

func (c *PatchPresenceChecker) evaluate(ref ArtifactRef, fixes []Fix) (Presence, Explanation, error) {
	fix, err := selectFix(ref.Version, fixes)
	if err != nil {
		return Presence{}, Explanation{}, err
	}

	cmpIntroduced, err := compareVersionStrings(ref.Version, fix.IntroducedIn)
	if err != nil {
		return Presence{}, Explanation{}, err
	}
	cmpFixed, err := compareVersionStrings(ref.Version, fix.FixedIn)
	if err != nil {
		return Presence{}, Explanation{}, err
	}

	fingerprint := c.Fingerprint(instructionsFor(ref, fix.Function))
	fingerprintMatches := strings.EqualFold(fingerprint, fix.Digest)

	var presence Presence
	switch {
	case cmpIntroduced < 0:
		presence = Presence{
			Status:     statusNotAffected,
			Confidence: 100,
			Evidence:   []string{"version"},
		}
	case cmpFixed >= 0:
		if fingerprintMatches {
			presence = Presence{
				Status:     statusFixed,
				Confidence: 100,
				Evidence:   []string{"version", "fingerprint"},
			}
		} else {
			presence = Presence{
				Status:     statusUncertain,
				Confidence: 0,
				Evidence:   []string{"version", "fingerprint"},
			}
		}
	default:
		if fingerprintMatches {
			presence = Presence{
				Status:     statusFixed,
				Confidence: 70,
				Evidence:   []string{"fingerprint"},
			}
		} else {
			presence = Presence{
				Status:     statusVulnerable,
				Confidence: 100,
				Evidence:   []string{"version", "fingerprint"},
			}
		}
	}

	explanation := Explanation{
		FixID: fix.ID,
		Steps: []string{"version", "fingerprint", presence.Status},
	}
	return presence, explanation, nil
}

func selectFix(version string, fixes []Fix) (Fix, error) {
	if len(fixes) == 0 {
		return Fix{}, ErrUnknownFix
	}
	if _, err := parseVersion(version); err != nil {
		return Fix{}, err
	}

	ordered := append([]Fix(nil), fixes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	var chosen *Fix
	var chosenIntroduced string
	for i := range ordered {
		cmp, err := compareVersionStrings(version, ordered[i].IntroducedIn)
		if err != nil {
			return Fix{}, err
		}
		if cmp < 0 {
			continue
		}
		if chosen == nil {
			chosen = &ordered[i]
			chosenIntroduced = ordered[i].IntroducedIn
			continue
		}
		cmpIntroduced, err := compareVersionStrings(ordered[i].IntroducedIn, chosenIntroduced)
		if err != nil {
			return Fix{}, err
		}
		if cmpIntroduced > 0 || (cmpIntroduced == 0 && ordered[i].ID < chosen.ID) {
			chosen = &ordered[i]
			chosenIntroduced = ordered[i].IntroducedIn
		}
	}
	if chosen != nil {
		return *chosen, nil
	}

	// The artifact predates every registered fix. Pick the earliest one
	// deterministically so the verdict is still explainable.
	chosen = &ordered[0]
	for i := 1; i < len(ordered); i++ {
		cmp, err := compareVersionStrings(ordered[i].IntroducedIn, chosen.IntroducedIn)
		if err != nil {
			return Fix{}, err
		}
		if cmp < 0 || (cmp == 0 && ordered[i].ID < chosen.ID) {
			chosen = &ordered[i]
		}
	}
	return *chosen, nil
}

func instructionsFor(ref ArtifactRef, function string) []string {
	for i := range ref.Functions {
		if ref.Functions[i].Name == function {
			return ref.Functions[i].Instructions
		}
	}
	return nil
}

func (c *PatchPresenceChecker) cacheVerdictLocked(id string, presence Presence, explanation Explanation) {
	if c.opts.CacheSize <= 0 {
		return
	}
	if elem, ok := c.cache[id]; ok {
		entry := elem.Value.(*cacheEntry)
		entry.presence = clonePresence(presence)
		entry.explanation = cloneExplanation(explanation)
		c.lru.MoveToFront(elem)
		return
	}

	entry := &cacheEntry{
		id:          id,
		presence:    clonePresence(presence),
		explanation: cloneExplanation(explanation),
	}
	c.cache[id] = c.lru.PushFront(entry)
	for c.lru.Len() > c.opts.CacheSize {
		back := c.lru.Back()
		if back == nil {
			break
		}
		evicted := back.Value.(*cacheEntry)
		delete(c.cache, evicted.id)
		c.lru.Remove(back)
	}
}

// Explain replays the evidence of the last verdict for an artifact.
func (c *PatchPresenceChecker) Explain(id string) (Explanation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitLocked()

	explanation, ok := c.explanations[id]
	if !ok {
		return Explanation{}, ErrUnknownArtifact
	}
	return cloneExplanation(explanation), nil
}

// Batch checks many artifacts, deduplicating by artifact id.
func (c *PatchPresenceChecker) Batch(refs []ArtifactRef) (map[string]Presence, error) {
	c.mu.Lock()
	c.ensureInitLocked()
	maxBatch := c.opts.MaxBatch
	c.mu.Unlock()

	if len(refs) > maxBatch {
		return nil, ErrBatchTooLarge
	}

	result := make(map[string]Presence, len(refs))
	seen := make(map[string]bool, len(refs))
	duplicates := make(map[string]int)
	for i := range refs {
		id := refs[i].ID
		if seen[id] {
			duplicates[id]++
			continue
		}
		seen[id] = true
		presence, err := c.Check(refs[i])
		if err != nil {
			return nil, err
		}
		result[id] = presence
	}

	c.mu.Lock()
	for _, count := range duplicates {
		c.stats.CacheHits += count
	}
	c.mu.Unlock()
	return result, nil
}

// Fingerprint normalises an instruction list and returns its SHA-256 digest.
func (c *PatchPresenceChecker) Fingerprint(instructions []string) string {
	normalised := make([]string, 0, len(instructions))
	for _, instruction := range instructions {
		fields := strings.Fields(strings.ToLower(instruction))
		if len(fields) == 0 {
			continue
		}
		for i := range fields {
			if strings.HasPrefix(fields[i], "sym:") {
				fields[i] = "sym:*"
			}
		}
		joined := strings.Join(fields, " ")
		if joined == "nop" {
			continue
		}
		normalised = append(normalised, joined)
	}

	sum := sha256.Sum256([]byte(strings.Join(normalised, "\n")))
	return hex.EncodeToString(sum[:])
}

// CompareVersions compares two dotted numeric versions.
func (c *PatchPresenceChecker) CompareVersions(a, b string) (int, error) {
	return compareVersionStrings(a, b)
}

func parseVersion(version string) ([]string, error) {
	if version == "" {
		return nil, ErrInvalidVersion
	}
	parts := strings.Split(version, ".")
	for _, part := range parts {
		if part == "" {
			return nil, ErrInvalidVersion
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return nil, ErrInvalidVersion
			}
		}
	}
	return parts, nil
}

func compareVersionStrings(a, b string) (int, error) {
	left, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	right, err := parseVersion(b)
	if err != nil {
		return 0, err
	}

	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for i := 0; i < length; i++ {
		var l, r string
		if i < len(left) {
			l = normaliseNumericSegment(left[i])
		} else {
			l = "0"
		}
		if i < len(right) {
			r = normaliseNumericSegment(right[i])
		} else {
			r = "0"
		}
		if len(l) < len(r) {
			return -1, nil
		}
		if len(l) > len(r) {
			return 1, nil
		}
		if l < r {
			return -1, nil
		}
		if l > r {
			return 1, nil
		}
	}
	return 0, nil
}

func normaliseNumericSegment(segment string) string {
	segment = strings.TrimLeft(segment, "0")
	if segment == "" {
		return "0"
	}
	return segment
}

// Fixes returns every registered fix ordered by id.
func (c *PatchPresenceChecker) Fixes() []Fix {
	c.mu.Lock()
	fixes := make([]Fix, 0, len(c.fixes))
	for _, fix := range c.fixes {
		fixes = append(fixes, fix)
	}
	c.mu.Unlock()

	sort.Slice(fixes, func(i, j int) bool { return fixes[i].ID < fixes[j].ID })
	return fixes
}

// Cached returns the cached verdict for an artifact id.
func (c *PatchPresenceChecker) Cached(id string) (Presence, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitLocked()

	elem, ok := c.cache[id]
	if !ok {
		return Presence{}, false
	}
	return clonePresence(elem.Value.(*cacheEntry).presence), true
}

// Stats returns a consistent view of the counters.
func (c *PatchPresenceChecker) Stats() CheckerStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitLocked()
	return c.stats
}

type snapshotCache struct {
	ID       string   `json:"id"`
	Presence Presence `json:"presence"`
}

type snapshotExplanation struct {
	ID    string   `json:"id"`
	FixID string   `json:"fix_id"`
	Steps []string `json:"steps"`
}

type snapshotData struct {
	Magic        string                `json:"magic"`
	Version      int                   `json:"version"`
	Fixes        []Fix                 `json:"fixes"`
	Cache        []snapshotCache       `json:"cache"`
	Explanations []snapshotExplanation `json:"explanations"`
	Stats        CheckerStats          `json:"stats"`
}

// Snapshot serialises the whole checker state.
func (c *PatchPresenceChecker) Snapshot() ([]byte, error) {
	c.mu.Lock()
	c.ensureInitLocked()

	fixes := make([]Fix, 0, len(c.fixes))
	for _, fix := range c.fixes {
		fixes = append(fixes, fix)
	}
	sort.Slice(fixes, func(i, j int) bool { return fixes[i].ID < fixes[j].ID })

	cache := make([]snapshotCache, 0, c.lru.Len())
	for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*cacheEntry)
		cache = append(cache, snapshotCache{
			ID:       entry.id,
			Presence: clonePresence(entry.presence),
		})
	}

	explanationIDs := make([]string, 0, len(c.explanations))
	for id := range c.explanations {
		explanationIDs = append(explanationIDs, id)
	}
	sort.Strings(explanationIDs)
	explanations := make([]snapshotExplanation, 0, len(explanationIDs))
	for _, id := range explanationIDs {
		explanation := c.explanations[id]
		explanations = append(explanations, snapshotExplanation{
			ID:    id,
			FixID: explanation.FixID,
			Steps: cloneStrings(explanation.Steps),
		})
	}

	data := snapshotData{
		Magic:        snapshotMagic,
		Version:      snapshotVersion,
		Fixes:        fixes,
		Cache:        cache,
		Explanations: explanations,
		Stats:        c.stats,
	}
	c.mu.Unlock()

	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// Restore replaces the whole checker state from a snapshot.
func (c *PatchPresenceChecker) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}

	var snapshot snapshotData
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrBadSnapshot
	}
	if snapshot.Magic != snapshotMagic || snapshot.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	fixes := make(map[string]Fix, len(snapshot.Fixes))
	byComponent := make(map[string][]string)
	for _, fix := range snapshot.Fixes {
		if !validFix(fix) {
			return ErrBadSnapshot
		}
		if _, exists := fixes[fix.ID]; exists {
			return ErrBadSnapshot
		}
		fixes[fix.ID] = fix
		byComponent[fix.Component] = append(byComponent[fix.Component], fix.ID)
	}
	for component := range byComponent {
		sort.Strings(byComponent[component])
	}

	if snapshot.Stats.Registered != len(fixes) ||
		snapshot.Stats.Checked < 0 ||
		snapshot.Stats.CacheHits < 0 ||
		snapshot.Stats.Uncertain < 0 ||
		snapshot.Stats.Uncertain > snapshot.Stats.Checked {
		return ErrBadSnapshot
	}

	explanations := make(map[string]Explanation, len(snapshot.Explanations))
	for _, explanation := range snapshot.Explanations {
		if explanation.ID == "" || explanation.FixID == "" || len(explanation.Steps) == 0 {
			return ErrBadSnapshot
		}
		if _, exists := explanations[explanation.ID]; exists {
			return ErrBadSnapshot
		}
		if _, exists := fixes[explanation.FixID]; !exists {
			return ErrBadSnapshot
		}
		explanations[explanation.ID] = Explanation{
			FixID: explanation.FixID,
			Steps: cloneStrings(explanation.Steps),
		}
	}

	c.mu.Lock()
	c.ensureInitLocked()
	cacheSize := c.opts.CacheSize
	c.mu.Unlock()

	cache := make(map[string]*list.Element, len(snapshot.Cache))
	lru := list.New()
	for _, cached := range snapshot.Cache {
		if cached.ID == "" || !validPresence(cached.Presence) {
			return ErrBadSnapshot
		}
		if _, exists := cache[cached.ID]; exists {
			return ErrBadSnapshot
		}
		if _, exists := explanations[cached.ID]; !exists {
			return ErrBadSnapshot
		}
		entry := &cacheEntry{
			id:          cached.ID,
			presence:    clonePresence(cached.Presence),
			explanation: cloneExplanation(explanations[cached.ID]),
		}
		cache[cached.ID] = lru.PushBack(entry)
	}
	for lru.Len() > cacheSize {
		back := lru.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*cacheEntry)
		delete(cache, entry.id)
		lru.Remove(back)
	}

	c.mu.Lock()
	c.ensureInitLocked()
	c.fixes = fixes
	c.byComponent = byComponent
	c.cache = cache
	c.lru = lru
	c.explanations = explanations
	c.stats = snapshot.Stats
	c.inflight = make(map[string]*flight)
	c.mu.Unlock()
	return nil
}

func validPresence(p Presence) bool {
	if p.Confidence < 0 || p.Confidence > 100 {
		return false
	}
	switch p.Status {
	case statusNotAffected, statusFixed, statusVulnerable, statusUncertain:
		return true
	default:
		return false
	}
}

// Reset returns the checker to its initial state.
func (c *PatchPresenceChecker) Reset() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked()
	return nil
}

func clonePresence(p Presence) Presence {
	p.Evidence = cloneStrings(p.Evidence)
	return p
}

func cloneExplanation(e Explanation) Explanation {
	e.Steps = cloneStrings(e.Steps)
	return e
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
