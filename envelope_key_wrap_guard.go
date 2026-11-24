package task

import "errors"

// errNotImplemented is returned by behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the key wrap guard.
var (
	ErrInvalidPurpose     = errors.New("envelope_key_wrap_guard: invalid purpose")
	ErrInvalidAlgorithm   = errors.New("envelope_key_wrap_guard: invalid algorithm")
	ErrInvalidDEK         = errors.New("envelope_key_wrap_guard: invalid dek")
	ErrDEKExists          = errors.New("envelope_key_wrap_guard: dek already registered")
	ErrDEKNotFound        = errors.New("envelope_key_wrap_guard: unknown dek")
	ErrWrapQuotaExhausted = errors.New("envelope_key_wrap_guard: dek wrap limit reached")
	ErrKEKQuotaExhausted  = errors.New("envelope_key_wrap_guard: kek quota exhausted")
	ErrEnvelopeUnknown    = errors.New("envelope_key_wrap_guard: unknown envelope")
	ErrPurposeMismatch    = errors.New("envelope_key_wrap_guard: envelope purpose mismatch")
	ErrSubjectMismatch    = errors.New("envelope_key_wrap_guard: envelope subject mismatch")
	ErrAlgorithmMismatch  = errors.New("envelope_key_wrap_guard: envelope algorithm mismatch")
	ErrGenerationRetired  = errors.New("envelope_key_wrap_guard: kek generation retired")
	ErrBreakerOpen        = errors.New("envelope_key_wrap_guard: unwrap breaker open")
	ErrInvalidThreshold   = errors.New("envelope_key_wrap_guard: invalid failure threshold")
	ErrBadSnapshot        = errors.New("envelope_key_wrap_guard: bad snapshot")
)

// Purpose scopes what a wrapped data key may be used for.
type Purpose string

// Supported envelope purposes.
const (
	PurposeData   Purpose = "data"
	PurposeBackup Purpose = "backup"
	PurposeExport Purpose = "export"
)

// Algorithm identifies the wrapping algorithm of one KEK generation.
type Algorithm string

// Supported wrapping algorithms.
const (
	AlgorithmAES256GCM Algorithm = "aes-256-gcm"
	AlgorithmChaCha20  Algorithm = "chacha20-poly1305"
)

// Envelope is the wrapped form of one data key.
type Envelope struct {
	ID         string
	DEKID      string
	Subject    string
	Purpose    Purpose
	Generation int
	Algorithm  Algorithm
	Wrapped    []byte
}

// DEKStatus is the observable state of one registered data key.
type DEKStatus struct {
	ID         string
	Subject    string
	Purpose    Purpose
	Wraps      int
	MaxWraps   int
	Generation int
	Algorithm  Algorithm
	EnvelopeID string
}

// ResealReport describes one batch re-wrap of a purpose.
type ResealReport struct {
	From     int
	To       int
	Resealed int
	Skipped  int
}

// DEKSnapshot is the portable form of one registered data key.
type DEKSnapshot struct {
	ID         string
	Subject    string
	Purpose    Purpose
	Key        []byte
	Wraps      int
	MaxWraps   int
	Generation int
	Algorithm  Algorithm
	EnvelopeID string
}

// KeyWrapSnapshot is the portable form of a whole guard.
type KeyWrapSnapshot struct {
	Purpose          Purpose
	Generation       int
	Algorithm        Algorithm
	Quota            int
	QuotaUsed        int
	FailureThreshold int
	Failures         int
	DEKs             []DEKSnapshot
	Envelopes        []Envelope
	Retired          []int
}

// KeyWrapStats aggregates guard counters.
type KeyWrapStats struct {
	DEKs      int
	Envelopes int
	Wraps     int
	Unwraps   int
	Failures  int
	Resealed  int
}

// KeyWrapGuard wraps data keys under a versioned KEK ring for one purpose.
type KeyWrapGuard struct {
	purpose Purpose
}

// NewKeyWrapGuard builds a guard scoped to a single purpose.
func NewKeyWrapGuard(purpose Purpose) *KeyWrapGuard {
	return &KeyWrapGuard{}
}

// RegisterDEK registers one data key with a wrap ceiling.
func (g *KeyWrapGuard) RegisterDEK(id string, key []byte, maxWraps int) error {
	return errNotImplemented
}

// Wrap produces an envelope bound to purpose, subject and generation.
func (g *KeyWrapGuard) Wrap(dekID, subject string) (Envelope, error) {
	return Envelope{}, errNotImplemented
}

// Unwrap verifies every binding of an envelope before returning the data key.
func (g *KeyWrapGuard) Unwrap(env Envelope) ([]byte, error) {
	return nil, errNotImplemented
}

// RotateKEK advances the key ring to the next generation.
func (g *KeyWrapGuard) RotateKEK(alg Algorithm, quota int) (int, error) {
	return 0, errNotImplemented
}

// ResealBatch re-wraps every envelope of one purpose onto the current generation.
func (g *KeyWrapGuard) ResealBatch(purpose Purpose) (ResealReport, error) {
	return ResealReport{}, errNotImplemented
}

// RetireGeneration retires one KEK generation.
func (g *KeyWrapGuard) RetireGeneration(gen int) error {
	return errNotImplemented
}

// AuditQueue lists envelopes that reference retired generations.
func (g *KeyWrapGuard) AuditQueue() []string {
	return nil
}

// SetFailureThreshold sets how many unwrap failures open the breaker.
func (g *KeyWrapGuard) SetFailureThreshold(n int) error {
	return errNotImplemented
}

// BreakerOpen reports whether unwrap is currently cut off.
func (g *KeyWrapGuard) BreakerOpen() bool {
	return false
}

// DEKStatus reports one registered data key.
func (g *KeyWrapGuard) DEKStatus(id string) (DEKStatus, error) {
	return DEKStatus{}, errNotImplemented
}

// QuotaRemaining reports the unused wrap allowance of the current generation.
func (g *KeyWrapGuard) QuotaRemaining() int {
	return 0
}

// Generation reports the KEK generation currently in force.
func (g *KeyWrapGuard) Generation() int {
	return 0
}

// Snapshot captures the key ring, envelopes and counters.
func (g *KeyWrapGuard) Snapshot() (KeyWrapSnapshot, error) {
	return KeyWrapSnapshot{}, errNotImplemented
}

// Restore replaces the guard contents with a snapshot.
func (g *KeyWrapGuard) Restore(snap KeyWrapSnapshot) error {
	return errNotImplemented
}

// Stats aggregates guard counters.
func (g *KeyWrapGuard) Stats() KeyWrapStats {
	return KeyWrapStats{}
}
