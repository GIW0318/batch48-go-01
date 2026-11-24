// Package task keeps the multi factor enrollment ledger and drives one
// authentication ceremony at a time from challenge to single use grant.
package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Factor enrollment states.
const (
	FactorPending     = "pending"
	FactorActive      = "active"
	FactorCompromised = "compromised"
	FactorRemoved     = "removed"
)

// Factor kinds.
const (
	KindKnowledge  = "knowledge"
	KindPossession = "possession"
	KindInherence  = "inherence"
)

// Ceremony states.
const (
	CeremonyOpen     = "open"
	CeremonyRedeemed = "redeemed"
)

// Grant states.
const (
	GrantValid = "valid"
)

// Verdict reason codes.
const (
	ReasonOK              = "reason_ok"
	ReasonProofMismatch   = "reason_proof_mismatch"
	ReasonDuplicateFactor = "reason_duplicate_factor"
	ReasonFactorInactive  = "reason_factor_inactive"
	ReasonFactorUnknown   = "reason_factor_unknown"
	ReasonAccountLocked   = "reason_account_locked"
)

// Errors returned by the guard.
var (
	ErrBlankArg           = errors.New("user, factor and session identifiers are required")
	ErrFactorKind         = errors.New("unknown factor kind")
	ErrFactorExists       = errors.New("factor already enrolled")
	ErrFactorNotFound     = errors.New("factor not found")
	ErrFactorLimit        = errors.New("factor limit reached for this account")
	ErrFactorState        = errors.New("factor is not in a state that allows the operation")
	ErrNoActiveFactor     = errors.New("account has no active factor")
	ErrCeremonyInFlight   = errors.New("another ceremony is already in flight")
	ErrCeremonyNotFound   = errors.New("ceremony not found")
	ErrCeremonyConsumed   = errors.New("ceremony already consumed")
	ErrCeremonyIncomplete = errors.New("ceremony has not collected enough factors")
	ErrAccountLocked      = errors.New("account locked after repeated factor failures")
	ErrBadSnapshot        = errors.New("snapshot payload is invalid")
)

// Factor is one registered authentication factor.
type Factor struct {
	UserID   string `json:"user_id"`
	FactorID string `json:"factor_id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Failures int    `json:"failures"`
}

// Ceremony is one in flight multi factor challenge bound to a session.
type Ceremony struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Challenge string `json:"challenge"`
	State     string `json:"state"`
	Verified  int    `json:"verified"`
	Seq       int64  `json:"seq"`
}

// Verdict is the outcome of one factor verification.
type Verdict struct {
	CeremonyID string `json:"ceremony_id"`
	FactorID   string `json:"factor_id"`
	Accepted   bool   `json:"accepted"`
	Reason     string `json:"reason"`
	Verified   int    `json:"verified"`
}

// Grant is the single use artifact minted by a redeemed ceremony.
type Grant struct {
	ID         string   `json:"id"`
	CeremonyID string   `json:"ceremony_id"`
	UserID     string   `json:"user_id"`
	SessionID  string   `json:"session_id"`
	Factors    []string `json:"factors"`
	State      string   `json:"state"`
	Seq        int64    `json:"seq"`
}

// GuardConfig carries the caps, the lock window, the factor target and the
// two injected dependencies of the guard.
type GuardConfig struct {
	MaxFactorsPerUser int
	MaxFailures       int
	LockDuration      time.Duration
	RequiredFactors   int
	Now               func() time.Time
	ProofVerifier     func(factorID, proof string) bool
}

// MfaCeremonyGuard owns the factor ledger, the ceremony table and the locks.
type MfaCeremonyGuard struct {
	cfg GuardConfig
}

// NewMfaCeremonyGuard builds a guard with the given policy.
func NewMfaCeremonyGuard(cfg GuardConfig) *MfaCeremonyGuard {
	return &MfaCeremonyGuard{cfg: cfg}
}

// EnrollFactor registers a factor in the pending state.
func (g *MfaCeremonyGuard) EnrollFactor(userID, factorID, kind string) (Factor, error) {
	return Factor{}, errNotImplemented
}

// ConfirmFactor promotes a pending factor to active.
func (g *MfaCeremonyGuard) ConfirmFactor(userID, factorID string) error {
	return errNotImplemented
}

// BeginCeremony opens the only allowed challenge for an account.
func (g *MfaCeremonyGuard) BeginCeremony(userID, sessionID string) (Ceremony, error) {
	return Ceremony{}, errNotImplemented
}

// VerifyFactor checks one distinct factor against the open ceremony.
func (g *MfaCeremonyGuard) VerifyFactor(ceremonyID, factorID, proof string) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// RedeemCeremony turns a complete ceremony into one single use grant.
func (g *MfaCeremonyGuard) RedeemCeremony(ceremonyID string) (Grant, error) {
	return Grant{}, errNotImplemented
}

// RevokeFactor tombstones a factor without reusing its identifier.
func (g *MfaCeremonyGuard) RevokeFactor(userID, factorID string) error {
	return errNotImplemented
}

// Factors lists the ledger of one account in factor id order.
func (g *MfaCeremonyGuard) Factors(userID string) []Factor {
	return nil
}

// Factor reads one ledger entry.
func (g *MfaCeremonyGuard) Factor(userID, factorID string) (Factor, bool) {
	return Factor{}, false
}

// Ceremony reads one ceremony record.
func (g *MfaCeremonyGuard) Ceremony(ceremonyID string) (Ceremony, bool) {
	return Ceremony{}, false
}

// LockRemaining reports how long the account stays locked.
func (g *MfaCeremonyGuard) LockRemaining(userID string) time.Duration {
	return 0
}

// Snapshot serialises the ledger, the ceremonies, the grants and the locks.
func (g *MfaCeremonyGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole state with a snapshot, all or nothing.
func (g *MfaCeremonyGuard) Restore(data []byte) error {
	return errNotImplemented
}
