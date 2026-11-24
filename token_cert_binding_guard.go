// Package task binds access tokens to the leaf certificate that may present
// them, moves a binding only after re-authentication and keeps token lifetime
// and per client binding counts bounded.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors returned by the guard.
var (
	ErrBlankClient         = errors.New("blank client")
	ErrBlankFingerprint    = errors.New("blank certificate fingerprint")
	ErrBadFingerprint      = errors.New("bad certificate fingerprint")
	ErrInvalidTTL          = errors.New("invalid token ttl")
	ErrBindingLimit        = errors.New("binding limit")
	ErrUnknownToken        = errors.New("unknown token")
	ErrAlreadyBound        = errors.New("token already bound")
	ErrTokenUnbound        = errors.New("token not bound")
	ErrTokenExpired        = errors.New("token expired")
	ErrFingerprintMismatch = errors.New("leaf certificate fingerprint mismatch")
	ErrMalformedToken      = errors.New("malformed token")
	ErrProofInvalid        = errors.New("reauthentication proof invalid")
	ErrProofMismatch       = errors.New("reauthentication proof mismatch")
	ErrUnknownBinding      = errors.New("unknown binding")
	ErrBadSnapshot         = errors.New("bad snapshot")
)

// Verdict reasons.
const (
	ReasonOK           = "ok"
	ReasonTokenExpired = "token_expired"
	ReasonUnbound      = "token_unbound"
	ReasonMismatch     = "fingerprint_mismatch"
	ReasonForged       = "token_forged"
)

// ProofMethodReauth is the only accepted re-authentication proof method.
const ProofMethodReauth = "reauth"

const tokenPrefix = "tcb1."

// CertLimits bounds token lifetime and the number of live bindings per client.
type CertLimits struct {
	MaxTokensPerClient int
	MaxTokenTTL        time.Duration
	ProofTTL           time.Duration
}

// Token is one access token bound to a leaf certificate fingerprint.
type Token struct {
	TokenID     string
	ClientID    string
	Fingerprint string
	Bound       bool
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

// Proof is the evidence that the subject re-authenticated before a rebind.
type Proof struct {
	TokenID   string
	Subject   string
	Method    string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Verdict is the outcome of one presentation check.
type Verdict struct {
	OK       bool
	TokenID  string
	ClientID string
	Reason   string
}

// TokenCertBindingGuard keeps the token to certificate binding table.
type TokenCertBindingGuard struct {
	mu     sync.Mutex
	limits CertLimits
	tokens map[string]*Token
	seq    int
}

// NewTokenCertBindingGuard builds a guard with the supplied limits.
func NewTokenCertBindingGuard(limits CertLimits) *TokenCertBindingGuard {
	return &TokenCertBindingGuard{
		limits: limits,
		tokens: make(map[string]*Token),
	}
}

// NormalizeFingerprint folds a fingerprint into lowercase colon free hex.
func (g *TokenCertBindingGuard) NormalizeFingerprint(raw string) (string, error) {
	return "", errNotImplemented
}

// Issue mints a token already bound to the supplied leaf certificate.
func (g *TokenCertBindingGuard) Issue(clientID, certFingerprint string, ttl time.Duration) (Token, error) {
	return Token{}, errNotImplemented
}

// IssuePending mints a token that still has to be bound to a certificate.
func (g *TokenCertBindingGuard) IssuePending(clientID string, ttl time.Duration) (Token, error) {
	return Token{}, errNotImplemented
}

// Bind attaches a pending token to one leaf certificate.
func (g *TokenCertBindingGuard) Bind(tokenID, certFingerprint string) error {
	return errNotImplemented
}

// Present builds the opaque token string a client sends on the wire.
func (g *TokenCertBindingGuard) Present(tokenID string) (string, error) {
	return "", errNotImplemented
}

// VerifyPresented checks the presented token string and leaf fingerprint.
func (g *TokenCertBindingGuard) VerifyPresented(rawToken, presentedFingerprint string, now time.Time) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// Rebind moves a binding to a new leaf certificate after re-authentication.
func (g *TokenCertBindingGuard) Rebind(tokenID, newFingerprint string, reauthProof Proof) error {
	return errNotImplemented
}

// RevokeBinding drops one token binding.
func (g *TokenCertBindingGuard) RevokeBinding(tokenID string) error {
	return errNotImplemented
}

// Tokens lists the live tokens of one client ordered by token id.
func (g *TokenCertBindingGuard) Tokens(clientID string) []Token {
	return nil
}

// Snapshot serialises every token binding.
func (g *TokenCertBindingGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces every token binding with a snapshot.
func (g *TokenCertBindingGuard) Restore(data []byte) error {
	return errNotImplemented
}
