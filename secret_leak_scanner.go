package task

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

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

const (
	snapshotFormat  = "batch48-go-safety-c-13-secret-leak-scanner"
	snapshotVersion = 1
)

// SecretLeakScanner finds hardcoded credentials in text and repository trees.
type SecretLeakScanner struct {
	mu sync.RWMutex

	opts ScannerOptions

	baseline     map[string]struct{}
	baselineList []string
	contexts     []Context

	findings             []Finding
	rawFindings          []Finding
	previousFingerprints []string

	stats ScannerStats
}

type findingKey struct {
	RuleID string
	Value  string
}

type snapshotData struct {
	Format               string         `json:"format"`
	Version              int            `json:"version"`
	Options              ScannerOptions `json:"options"`
	Baseline             []string       `json:"baseline"`
	Contexts             []Context      `json:"contexts"`
	RawFindings          []Finding      `json:"raw_findings"`
	PreviousFingerprints []string       `json:"previous_fingerprints"`
	Stats                ScannerStats   `json:"stats"`
}

// NewSecretLeakScanner builds a scanner with the given limits.
func NewSecretLeakScanner(opts ScannerOptions) *SecretLeakScanner {
	opts.Prefixes = append([]string(nil), opts.Prefixes...)
	return &SecretLeakScanner{
		opts:     opts,
		baseline: make(map[string]struct{}),
	}
}

// ScanFile scans one file content and replaces the current result set.
func (s *SecretLeakScanner) ScanFile(path string, data []byte) ([]Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(data) > s.opts.MaxFileBytes {
		s.discardRoundLocked()
		return nil, ErrFileTooLarge
	}

	if s.pathAllowedLocked(path) {
		s.previousFingerprints = fingerprintsOfFindings(s.rawFindings)
		s.rawFindings = []Finding{}
		s.findings = []Finding{}
		s.stats.Files++
		s.stats.Suppressed++
		return []Finding{}, nil
	}

	raw, candidates, err := scanContent(path, data, s.opts)
	if err != nil {
		s.discardRoundLocked()
		return nil, err
	}

	visible, suppressed := s.filterBaselineLocked(raw)
	s.previousFingerprints = fingerprintsOfFindings(s.rawFindings)
	s.rawFindings = raw
	s.findings = visible

	s.stats.Files++
	s.stats.Candidates += candidates
	s.stats.Findings += len(visible)
	s.stats.Suppressed += suppressed

	return cloneFindings(visible), nil
}

// ScanTree scans a directory tree in path order and replaces the result set.
func (s *SecretLeakScanner) ScanTree(root string) ([]Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paths := make([]string, 0)
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if walkErr != nil {
		s.discardRoundLocked()
		return nil, walkErr
	}
	sort.Strings(paths)

	raw := make([]Finding, 0)
	files := 0
	candidates := 0
	suppressedFiles := 0

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			s.discardRoundLocked()
			return nil, err
		}
		if info.Size() > int64(s.opts.MaxFileBytes) {
			s.discardRoundLocked()
			return nil, ErrFileTooLarge
		}

		files++
		if s.pathAllowedLocked(path) {
			suppressedFiles++
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			s.discardRoundLocked()
			return nil, err
		}
		if len(data) > s.opts.MaxFileBytes {
			s.discardRoundLocked()
			return nil, ErrFileTooLarge
		}

		fileFindings, fileCandidates, err := scanContent(path, data, s.opts)
		if err != nil {
			s.discardRoundLocked()
			return nil, err
		}
		raw = append(raw, fileFindings...)
		candidates += fileCandidates
	}

	if candidates > s.opts.MaxCandidates {
		s.discardRoundLocked()
		return nil, ErrCandidateLimit
	}

	sortFindings(raw)
	visible, suppressedBaseline := s.filterBaselineLocked(raw)

	s.previousFingerprints = fingerprintsOfFindings(s.rawFindings)
	s.rawFindings = raw
	s.findings = visible

	s.stats.Files += files
	s.stats.Candidates += candidates
	s.stats.Findings += len(visible)
	s.stats.Suppressed += suppressedFiles + suppressedBaseline

	return cloneFindings(visible), nil
}

// SetBaseline installs the accepted fingerprint set.
func (s *SecretLeakScanner) SetBaseline(fingerprints []string) error {
	normalized, set, err := normalizeBaseline(fingerprints)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.baselineList = normalized
	s.baseline = set
	s.findings, _ = s.filterBaselineLocked(s.rawFindings)
	return nil
}

// Allow registers one allowlisted scanning context.
func (s *SecretLeakScanner) Allow(ctx Context) error {
	if (ctx.PathSuffix == "" && ctx.Marker == "") || ctx.Reason == "" {
		return ErrInvalidContext
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts = append(s.contexts, ctx)
	return nil
}

// Findings returns the findings of the most recent scan.
func (s *SecretLeakScanner) Findings() []Finding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneFindings(s.findings)
}

// BaselineDiff compares the baseline with the most recent scan.
func (s *SecretLeakScanner) BaselineDiff() (BaselineDiff, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := fingerprintSet(s.rawFindings)

	added := make([]string, 0)
	for fp := range current {
		if _, ok := s.baseline[fp]; !ok {
			added = append(added, fp)
		}
	}
	sort.Strings(added)

	removedSet := make(map[string]struct{})
	for fp := range s.baseline {
		removedSet[fp] = struct{}{}
	}
	for _, fp := range s.previousFingerprints {
		removedSet[fp] = struct{}{}
	}

	removed := make([]string, 0)
	for fp := range removedSet {
		if _, ok := current[fp]; !ok {
			removed = append(removed, fp)
		}
	}
	sort.Strings(removed)

	return BaselineDiff{Added: added, Removed: removed}, nil
}

// Entropy is the Shannon entropy of the given string in bits per byte.
func (s *SecretLeakScanner) Entropy(v string) float64 {
	return byteEntropy(v)
}

// Fingerprint is the stable 64 character hexadecimal identity of a finding.
func (s *SecretLeakScanner) Fingerprint(f Finding) string {
	return fingerprintOfFinding(f)
}

// Stats returns a consistent view of the counters.
func (s *SecretLeakScanner) Stats() ScannerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// Snapshot serialises the whole scanner state.
func (s *SecretLeakScanner) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dto := snapshotData{
		Format:               snapshotFormat,
		Version:              snapshotVersion,
		Options:              cloneOptions(s.opts),
		Baseline:             append([]string{}, s.baselineList...),
		Contexts:             cloneContexts(s.contexts),
		RawFindings:          cloneFindings(s.rawFindings),
		PreviousFingerprints: append([]string{}, s.previousFingerprints...),
		Stats:                s.stats,
	}

	data, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("secret_leak_scanner: snapshot: %w", err)
	}
	return data, nil
}

// Restore replaces the whole scanner state from a snapshot.
func (s *SecretLeakScanner) Restore(data []byte) error {
	var dto snapshotData
	if err := json.Unmarshal(data, &dto); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(dto); err != nil {
		return err
	}

	baselineList, baselineSet, err := normalizeBaseline(dto.Baseline)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	previous, err := normalizeFingerprintList(dto.PreviousFingerprints)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}

	opts := cloneOptions(dto.Options)
	contexts := cloneContexts(dto.Contexts)
	raw := cloneFindings(dto.RawFindings)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.opts = opts
	s.baselineList = baselineList
	s.baseline = baselineSet
	s.contexts = contexts
	s.rawFindings = raw
	s.previousFingerprints = previous
	s.stats = dto.Stats
	s.findings, _ = s.filterBaselineLocked(raw)

	return nil
}

// Reset returns the scanner to its initial state.
func (s *SecretLeakScanner) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.baseline = make(map[string]struct{})
	s.baselineList = nil
	s.contexts = nil
	s.findings = nil
	s.rawFindings = nil
	s.previousFingerprints = nil
	s.stats = ScannerStats{}
	return nil
}

func scanContent(path string, data []byte, opts ScannerOptions) ([]Finding, int, error) {
	groups := make(map[findingKey]*Finding)
	candidates := 0
	lineNumber := 1
	lineStart := 0

	for lineStart <= len(data) {
		lineEnd := lineStart
		for lineEnd < len(data) && data[lineEnd] != '\n' {
			lineEnd++
		}
		line := data[lineStart:lineEnd]
		hasAssignmentContext := lineHasSensitiveAssignment(line)

		for offset := 0; offset < len(line); {
			r, size := utf8.DecodeRune(line[offset:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				offset += size
				continue
			}

			tokenStart := offset
			offset += size
			for offset < len(line) {
				nextRune, nextSize := utf8.DecodeRune(line[offset:])
				if !unicode.IsLetter(nextRune) && !unicode.IsDigit(nextRune) {
					break
				}
				offset += nextSize
			}

			value := string(line[tokenStart:offset])
			if len(value) < 16 {
				continue
			}

			ruleID := ruleForCandidate(value, hasAssignmentContext, opts.Prefixes)
			if ruleID == "" {
				continue
			}

			candidates++
			if candidates > opts.MaxCandidates {
				return nil, candidates, ErrCandidateLimit
			}

			entropy := byteEntropy(value)
			if entropy < opts.MinEntropy {
				continue
			}

			key := findingKey{RuleID: ruleID, Value: value}
			finding, ok := groups[key]
			if !ok {
				finding = &Finding{
					RuleID:      ruleID,
					Path:        path,
					Value:       value,
					Entropy:     entropy,
					Occurrences: make([]Location, 0, 1),
				}
				groups[key] = finding
			}
			finding.Occurrences = append(finding.Occurrences, Location{
				Line:   lineNumber,
				Column: tokenStart + 1,
			})
		}

		if lineEnd == len(data) {
			break
		}
		lineStart = lineEnd + 1
		lineNumber++
	}

	result := make([]Finding, 0, len(groups))
	for _, finding := range groups {
		finding.Occurrences = uniqueSortedLocations(finding.Occurrences)
		result = append(result, *finding)
	}
	sortFindings(result)
	return result, candidates, nil
}

func ruleForCandidate(value string, assignmentContext bool, prefixes []string) string {
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(value, prefix) {
			return "prefix:" + prefix
		}
	}
	if assignmentContext {
		return "generic_secret"
	}
	return ""
}

func byteEntropy(value string) float64 {
	if value == "" {
		return 0
	}

	var frequencies [256]int
	for i := 0; i < len(value); i++ {
		frequencies[value[i]]++
	}

	total := float64(len(value))
	entropy := 0.0
	for _, count := range frequencies {
		if count == 0 {
			continue
		}
		probability := float64(count) / total
		entropy -= probability * math.Log2(probability)
	}
	return entropy
}

func lineHasSensitiveAssignment(line []byte) bool {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '=':
			if i+1 < len(line) && line[i+1] == '=' {
				continue
			}
			if i > 0 && (line[i-1] == '=' || line[i-1] == '!' || line[i-1] == '<' || line[i-1] == '>') {
				continue
			}
			if i+1 < len(line) && line[i+1] == '>' {
				continue
			}
			if sensitiveKey(assignmentKeyBefore(line, i)) {
				return true
			}
		case ':':
			if i+1 < len(line) && line[i+1] == ':' {
				continue
			}
			if sensitiveKey(assignmentKeyBefore(line, i)) {
				return true
			}
		}
	}
	return false
}

func assignmentKeyBefore(line []byte, operator int) string {
	end := operator
	for end > 0 && isSpaceByte(line[end-1]) {
		end--
	}
	for end > 0 && (isQuoteByte(line[end-1]) || isClosingBracketByte(line[end-1])) {
		end--
	}

	keyEnd := end
	start := end
	for start > 0 && isKeyByte(line[start-1]) {
		start--
	}
	if start == keyEnd {
		return ""
	}
	return strings.ToLower(string(line[start:keyEnd]))
}

func sensitiveKey(key string) bool {
	for _, suffix := range []string{"password", "secret", "token", "key"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func isQuoteByte(c byte) bool {
	return c == '"' || c == '\'' || c == '`'
}

func isClosingBracketByte(c byte) bool {
	return c == ']' || c == ')' || c == '}'
}

func isKeyByte(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '-' || c == '.'
}

func uniqueSortedLocations(locations []Location) []Location {
	sort.SliceStable(locations, func(i, j int) bool {
		if locations[i].Line != locations[j].Line {
			return locations[i].Line < locations[j].Line
		}
		return locations[i].Column < locations[j].Column
	})

	unique := make([]Location, 0, len(locations))
	for _, location := range locations {
		if len(unique) > 0 && unique[len(unique)-1] == location {
			continue
		}
		unique = append(unique, location)
	}
	return unique
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].RuleID != findings[j].RuleID {
			return findings[i].RuleID < findings[j].RuleID
		}
		return findings[i].Value < findings[j].Value
	})
}

func (s *SecretLeakScanner) pathAllowedLocked(path string) bool {
	normalizedPath := filepath.ToSlash(path)
	for _, allow := range s.contexts {
		if allow.PathSuffix == "" {
			continue
		}
		if strings.HasSuffix(normalizedPath, filepath.ToSlash(allow.PathSuffix)) {
			return true
		}
	}
	return false
}

func (s *SecretLeakScanner) discardRoundLocked() {
	s.previousFingerprints = fingerprintsOfFindings(s.rawFindings)
	s.rawFindings = []Finding{}
	s.findings = []Finding{}
}

func (s *SecretLeakScanner) filterBaselineLocked(findings []Finding) ([]Finding, int) {
	visible := make([]Finding, 0, len(findings))
	suppressed := 0
	for _, finding := range findings {
		fp := fingerprintOfFinding(finding)
		if _, ok := s.baseline[fp]; ok {
			suppressed++
			continue
		}
		visible = append(visible, cloneFinding(finding))
	}
	return visible, suppressed
}

func fingerprintOfFinding(finding Finding) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range []string{finding.RuleID, finding.Path, finding.Value} {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func fingerprintsOfFindings(findings []Finding) []string {
	set := fingerprintSet(findings)
	result := make([]string, 0, len(set))
	for fp := range set {
		result = append(result, fp)
	}
	sort.Strings(result)
	return result
}

func fingerprintSet(findings []Finding) map[string]struct{} {
	set := make(map[string]struct{}, len(findings))
	for _, finding := range findings {
		set[fingerprintOfFinding(finding)] = struct{}{}
	}
	return set
}

func normalizeBaseline(fingerprints []string) ([]string, map[string]struct{}, error) {
	normalized := make([]string, 0, len(fingerprints))
	set := make(map[string]struct{}, len(fingerprints))
	for _, fingerprint := range fingerprints {
		if fingerprint == "" || !validFingerprint(fingerprint) {
			return nil, nil, ErrInvalidBaseline
		}
		fingerprint = strings.ToLower(fingerprint)
		if _, exists := set[fingerprint]; exists {
			return nil, nil, ErrInvalidBaseline
		}
		set[fingerprint] = struct{}{}
		normalized = append(normalized, fingerprint)
	}
	sort.Strings(normalized)
	return normalized, set, nil
}

func normalizeFingerprintList(fingerprints []string) ([]string, error) {
	set := make(map[string]struct{}, len(fingerprints))
	normalized := make([]string, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		if !validFingerprint(fingerprint) {
			return nil, ErrInvalidBaseline
		}
		fingerprint = strings.ToLower(fingerprint)
		if _, exists := set[fingerprint]; exists {
			continue
		}
		set[fingerprint] = struct{}{}
		normalized = append(normalized, fingerprint)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func validFingerprint(fingerprint string) bool {
	if len(fingerprint) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(fingerprint)
	return err == nil && len(decoded) == 32
}

func validContext(ctx Context) bool {
	return (ctx.PathSuffix != "" || ctx.Marker != "") && ctx.Reason != ""
}

func validateSnapshot(dto snapshotData) error {
	if dto.Format != snapshotFormat || dto.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if dto.Stats.Files < 0 || dto.Stats.Candidates < 0 || dto.Stats.Findings < 0 || dto.Stats.Suppressed < 0 {
		return ErrBadSnapshot
	}
	if _, _, err := normalizeBaseline(dto.Baseline); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if _, err := normalizeFingerprintList(dto.PreviousFingerprints); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	for _, ctx := range dto.Contexts {
		if !validContext(ctx) {
			return ErrBadSnapshot
		}
	}
	for _, finding := range dto.RawFindings {
		if finding.RuleID == "" || finding.Value == "" || len(finding.Occurrences) == 0 {
			return ErrBadSnapshot
		}
		for _, location := range finding.Occurrences {
			if location.Line < 1 || location.Column < 1 {
				return ErrBadSnapshot
			}
		}
	}
	return nil
}

func cloneOptions(options ScannerOptions) ScannerOptions {
	options.Prefixes = append([]string(nil), options.Prefixes...)
	return options
}

func cloneContexts(contexts []Context) []Context {
	return append(make([]Context, 0, len(contexts)), contexts...)
}

func cloneFindings(findings []Finding) []Finding {
	cloned := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		cloned = append(cloned, cloneFinding(finding))
	}
	return cloned
}

func cloneFinding(finding Finding) Finding {
	if finding.Occurrences != nil {
		finding.Occurrences = append([]Location(nil), finding.Occurrences...)
	}
	return finding
}
