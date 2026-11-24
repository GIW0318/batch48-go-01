package task

import (
	"errors"
	"time"
)

// errNotImplemented marks behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

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

// AlertSuppressor decides whether an alert is reported or suppressed.
type AlertSuppressor struct {
	opts SuppressorOptions
}

// NewAlertSuppressor builds a suppressor with the given options.
func NewAlertSuppressor(opts SuppressorOptions) *AlertSuppressor {
	return &AlertSuppressor{opts: opts}
}

// Fingerprint derives the stable alert identity from rule, target and port.
func (s *AlertSuppressor) Fingerprint(a Alert) string {
	return ""
}

// Suppress reports whether the alert is suppressed and why.
func (s *AlertSuppressor) Suppress(a Alert, now time.Time) (bool, Reason) {
	return false, ""
}

// AddException installs a whitelist entry with an expiry instant.
func (s *AlertSuppressor) AddException(e Exception) error {
	return errNotImplemented
}

// RemoveException deletes a whitelist entry by fingerprint.
func (s *AlertSuppressor) RemoveException(fp string) error {
	return errNotImplemented
}

// Exceptions returns the active whitelist entries ordered by expiry.
func (s *AlertSuppressor) Exceptions() []Exception {
	return nil
}

// Count returns how many alerts of the fingerprint were reported or suppressed.
func (s *AlertSuppressor) Count(fp string) int {
	return 0
}

// SetBaseline pins the current baseline version of a target.
func (s *AlertSuppressor) SetBaseline(target string, version int) error {
	return errNotImplemented
}

// Baseline returns the baseline version of a target, zero when unset.
func (s *AlertSuppressor) Baseline(target string) int {
	return 0
}

// Recount reconciles the counters with the recorded baselines.
func (s *AlertSuppressor) Recount() error {
	return errNotImplemented
}

// Stats returns a consistent snapshot of the counters.
func (s *AlertSuppressor) Stats() SuppressorStats {
	return SuppressorStats{}
}

// Reset drops every counter, baseline and exception.
func (s *AlertSuppressor) Reset() error {
	return errNotImplemented
}

// Snapshot serialises the whole suppressor state.
func (s *AlertSuppressor) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a state produced by Snapshot.
func (s *AlertSuppressor) Restore(data []byte) error {
	return errNotImplemented
}
