package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sentinel errors reported by the suppressor.
var (
	ErrInvalidException = errors.New("alert_suppressor: invalid exception")
	ErrExceptionLimit   = errors.New("alert_suppressor: exception table full")
	ErrInvalidTarget    = errors.New("alert_suppressor: invalid target")
	ErrBadSnapshot      = errors.New("alert_suppressor: bad snapshot")
)

// Reason classifies the decision taken by Suppress.
type Reason string

// Decision reasons.
const (
	ReasonEmitted       Reason = "emitted"
	ReasonDuplicate     Reason = "duplicate"
	ReasonException     Reason = "exception"
	ReasonBaselineShift Reason = "baseline-shift"
)

// Alert is one finding produced by a detection rule.
type Alert struct {
	ID       string
	RuleID   string
	Target   string
	Port     int
	Baseline int
	Severity int
}

// Exception suppresses every alert carrying the given fingerprint until Expires.
type Exception struct {
	Fingerprint string
	Expires     time.Time
	Note        string
}

// SuppressorOptions tunes the suppression window and table capacities.
type SuppressorOptions struct {
	Window          time.Duration
	MaxExceptions   int
	MaxFingerprints int
}

// SuppressorStats is a consistent view of the suppressor counters.
type SuppressorStats struct {
	Emitted      int
	Suppressed   int
	Evicted      int
	Exceptions   int
	Fingerprints int
}

type fingerprintState struct {
	Fingerprint string    `json:"fingerprint"`
	Target      string    `json:"target"`
	WindowStart time.Time `json:"window_start"`
	Count       int       `json:"count"`
	Baseline    int       `json:"baseline"`
}

type snapshotState struct {
	Window          time.Duration      `json:"window"`
	MaxExceptions   int                `json:"max_exceptions"`
	MaxFingerprints int                `json:"max_fingerprints"`
	Emitted         int                `json:"emitted"`
	Suppressed      int                `json:"suppressed"`
	Evicted         int                `json:"evicted"`
	Clock           time.Time          `json:"clock"`
	Fingerprints    []fingerprintState `json:"fingerprints"`
	Baselines       map[string]int     `json:"baselines"`
	Exceptions      []Exception        `json:"exceptions"`
}

type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	State    json.RawMessage `json:"state"`
}

const snapshotVersion = 1

// AlertSuppressor decides whether an alert is reported or suppressed.
type AlertSuppressor struct {
	mu sync.Mutex

	opts SuppressorOptions

	fingerprints map[string]fingerprintState
	baselines    map[string]int
	exceptions   map[string]Exception

	emitted    int
	suppressed int
	evicted    int

	// clock is the latest observation supplied to Suppress. Methods without a
	// time argument use it when purging expired exceptions.
	clock time.Time
}

// NewAlertSuppressor builds a suppressor with the given options.
func NewAlertSuppressor(opts SuppressorOptions) *AlertSuppressor {
	return &AlertSuppressor{
		opts:         normalizeOptions(opts),
		fingerprints: make(map[string]fingerprintState),
		baselines:    make(map[string]int),
		exceptions:   make(map[string]Exception),
	}
}

func normalizeOptions(opts SuppressorOptions) SuppressorOptions {
	if opts.Window <= 0 {
		opts.Window = 5 * time.Minute
	}
	if opts.MaxExceptions <= 0 {
		opts.MaxExceptions = 128
	}
	if opts.MaxFingerprints <= 0 {
		opts.MaxFingerprints = 4096
	}
	return opts
}

// Fingerprint derives the stable alert identity from rule, target and port.
func (s *AlertSuppressor) Fingerprint(a Alert) string {
	rule := strings.ToLower(strings.TrimSpace(a.RuleID))
	target := strings.TrimSpace(a.Target)
	return rule + "|" + target + "|" + strconv.Itoa(a.Port)
}

func (s *AlertSuppressor) purgeExpiredLocked(now time.Time) {
	if now.IsZero() {
		return
	}
	for fp, exception := range s.exceptions {
		if exception.Expires.IsZero() || exception.Expires.After(now) {
			continue
		}
		delete(s.exceptions, fp)
		// An exception expiry starts a fresh window. Dropping the recorded
		// window guarantees the first alert after expiry is emitted.
		delete(s.fingerprints, fp)
	}
}

func (s *AlertSuppressor) ensureFingerprintCapacityLocked() {
	for len(s.fingerprints) >= s.opts.MaxFingerprints {
		victim := ""
		var victimEnd time.Time
		for fp, state := range s.fingerprints {
			end := state.WindowStart.Add(s.opts.Window)
			if victim == "" || end.Before(victimEnd) || (end.Equal(victimEnd) && fp < victim) {
				victim = fp
				victimEnd = end
			}
		}
		if victim == "" {
			return
		}
		delete(s.fingerprints, victim)
		s.evicted++
	}
}

func (s *AlertSuppressor) emitFingerprintLocked(fp, target string, now time.Time, baseline int) {
	if state, ok := s.fingerprints[fp]; ok {
		state.Target = target
		state.WindowStart = now
		state.Count = 1
		state.Baseline = baseline
		s.fingerprints[fp] = state
		return
	}
	s.ensureFingerprintCapacityLocked()
	s.fingerprints[fp] = fingerprintState{
		Fingerprint: fp,
		Target:      target,
		WindowStart: now,
		Count:       1,
		Baseline:    baseline,
	}
}

// Suppress reports whether the alert is suppressed and why.
func (s *AlertSuppressor) Suppress(a Alert, now time.Time) (bool, Reason) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clock.IsZero() || now.After(s.clock) {
		s.clock = now
	}
	s.purgeExpiredLocked(now)

	fp := s.Fingerprint(a)
	if _, ok := s.exceptions[fp]; ok {
		s.suppressed++
		return true, ReasonException
	}

	target := strings.TrimSpace(a.Target)
	currentBaseline := s.baselines[target]
	if a.Baseline != currentBaseline {
		s.emitFingerprintLocked(fp, target, now, currentBaseline)
		s.emitted++
		return false, ReasonBaselineShift
	}

	state, exists := s.fingerprints[fp]
	if !exists {
		s.emitFingerprintLocked(fp, target, now, currentBaseline)
		s.emitted++
		return false, ReasonEmitted
	}

	if !now.Before(state.WindowStart.Add(s.opts.Window)) {
		s.emitFingerprintLocked(fp, target, now, currentBaseline)
		s.emitted++
		return false, ReasonEmitted
	}

	state.Target = target
	state.Baseline = currentBaseline
	state.Count++
	s.fingerprints[fp] = state
	s.suppressed++
	return true, ReasonDuplicate
}

// AddException installs a whitelist entry with an expiry instant.
func (s *AlertSuppressor) AddException(e Exception) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(e.Fingerprint) == "" {
		return ErrInvalidException
	}
	s.purgeExpiredLocked(s.clock)
	if _, exists := s.exceptions[e.Fingerprint]; !exists && len(s.exceptions) >= s.opts.MaxExceptions {
		return ErrExceptionLimit
	}
	s.exceptions[e.Fingerprint] = e
	return nil
}

// RemoveException deletes a whitelist entry by fingerprint.
func (s *AlertSuppressor) RemoveException(fp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.exceptions[fp]; !exists {
		return ErrInvalidException
	}
	delete(s.exceptions, fp)
	return nil
}

func sortExceptions(exceptions []Exception) {
	sort.Slice(exceptions, func(i, j int) bool {
		if exceptions[i].Expires.Equal(exceptions[j].Expires) {
			return exceptions[i].Fingerprint < exceptions[j].Fingerprint
		}
		return exceptions[i].Expires.Before(exceptions[j].Expires)
	})
}

// Exceptions returns the active whitelist entries ordered by expiry.
func (s *AlertSuppressor) Exceptions() []Exception {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.purgeExpiredLocked(s.clock)
	result := make([]Exception, 0, len(s.exceptions))
	for _, exception := range s.exceptions {
		result = append(result, exception)
	}
	sortExceptions(result)
	return result
}

// Count returns how many alerts of the fingerprint were reported or suppressed.
func (s *AlertSuppressor) Count(fp string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.fingerprints[fp].Count
}

// SetBaseline pins the current baseline version of a target.
func (s *AlertSuppressor) SetBaseline(target string, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	target = strings.TrimSpace(target)
	if target == "" || version < 0 {
		return ErrInvalidTarget
	}
	s.baselines[target] = version
	return nil
}

// Baseline returns the baseline version of a target, zero when unset.
func (s *AlertSuppressor) Baseline(target string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.baselines[strings.TrimSpace(target)]
}

// Recount reconciles the counters with the recorded baselines.
func (s *AlertSuppressor) Recount() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for fp, state := range s.fingerprints {
		if state.Baseline == s.baselines[state.Target] {
			continue
		}
		delete(s.fingerprints, fp)
		if s.emitted > 0 {
			s.emitted--
		}
		if state.Count > 1 {
			s.suppressed -= state.Count - 1
			if s.suppressed < 0 {
				s.suppressed = 0
			}
		}
	}
	return nil
}

// Stats returns a consistent snapshot of the counters.
func (s *AlertSuppressor) Stats() SuppressorStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.purgeExpiredLocked(s.clock)
	return SuppressorStats{
		Emitted:      s.emitted,
		Suppressed:   s.suppressed,
		Evicted:      s.evicted,
		Exceptions:   len(s.exceptions),
		Fingerprints: len(s.fingerprints),
	}
}

// Reset drops every counter, baseline and exception.
func (s *AlertSuppressor) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fingerprints = make(map[string]fingerprintState)
	s.baselines = make(map[string]int)
	s.exceptions = make(map[string]Exception)
	s.emitted = 0
	s.suppressed = 0
	s.evicted = 0
	s.clock = time.Time{}
	return nil
}

// Snapshot serialises the whole suppressor state.
func (s *AlertSuppressor) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := snapshotState{
		Window:          s.opts.Window,
		MaxExceptions:   s.opts.MaxExceptions,
		MaxFingerprints: s.opts.MaxFingerprints,
		Emitted:         s.emitted,
		Suppressed:      s.suppressed,
		Evicted:         s.evicted,
		Clock:           s.clock,
		Fingerprints:    make([]fingerprintState, 0, len(s.fingerprints)),
		Baselines:       make(map[string]int, len(s.baselines)),
		Exceptions:      make([]Exception, 0, len(s.exceptions)),
	}
	for _, fingerprint := range s.fingerprints {
		state.Fingerprints = append(state.Fingerprints, fingerprint)
	}
	sort.Slice(state.Fingerprints, func(i, j int) bool {
		return state.Fingerprints[i].Fingerprint < state.Fingerprints[j].Fingerprint
	})
	for target, version := range s.baselines {
		state.Baselines[target] = version
	}
	for _, exception := range s.exceptions {
		state.Exceptions = append(state.Exceptions, exception)
	}
	sortExceptions(state.Exceptions)

	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		Checksum: hex.EncodeToString(sum[:]),
		State:    raw,
	}
	return json.Marshal(envelope)
}

// Restore loads a state produced by Snapshot.
func (s *AlertSuppressor) Restore(data []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ErrBadSnapshot
	}
	if envelope.Version != snapshotVersion || len(envelope.State) == 0 {
		return ErrBadSnapshot
	}
	sum := sha256.Sum256(envelope.State)
	if !strings.EqualFold(envelope.Checksum, hex.EncodeToString(sum[:])) {
		return ErrBadSnapshot
	}

	var state snapshotState
	if err := json.Unmarshal(envelope.State, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Emitted < 0 || state.Suppressed < 0 || state.Evicted < 0 {
		return ErrBadSnapshot
	}

	opts := normalizeOptions(SuppressorOptions{
		Window:          state.Window,
		MaxExceptions:   state.MaxExceptions,
		MaxFingerprints: state.MaxFingerprints,
	})
	fingerprints := make(map[string]fingerprintState, len(state.Fingerprints))
	for _, fingerprint := range state.Fingerprints {
		if fingerprint.Fingerprint == "" || fingerprint.Count <= 0 {
			return ErrBadSnapshot
		}
		if _, exists := fingerprints[fingerprint.Fingerprint]; exists {
			return ErrBadSnapshot
		}
		fingerprints[fingerprint.Fingerprint] = fingerprint
	}
	baselines := make(map[string]int, len(state.Baselines))
	for target, version := range state.Baselines {
		if version < 0 {
			return ErrBadSnapshot
		}
		baselines[target] = version
	}
	exceptions := make(map[string]Exception, len(state.Exceptions))
	for _, exception := range state.Exceptions {
		if strings.TrimSpace(exception.Fingerprint) == "" {
			return ErrBadSnapshot
		}
		if _, exists := exceptions[exception.Fingerprint]; exists {
			return ErrBadSnapshot
		}
		exceptions[exception.Fingerprint] = exception
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.opts = opts
	s.fingerprints = fingerprints
	s.baselines = baselines
	s.exceptions = exceptions
	s.emitted = state.Emitted
	s.suppressed = state.Suppressed
	s.evicted = state.Evicted
	s.clock = state.Clock
	return nil
}
