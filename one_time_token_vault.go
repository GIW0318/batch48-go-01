// Package task issues single use invite, reset and verification tokens and
// keeps only hashed material at rest with per tenant purpose quotas.
package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Token purposes.
const (
	PurposeInvite = "invite"
	PurposeReset  = "reset"
	PurposeVerify = "verify"
)

// Token states.
const (
	TokenLive     = "live"
	TokenRedeemed = "redeemed"
	TokenRevoked  = "revoked"
	TokenExpired  = "expired"
)

// Errors returned by the vault.
var (
	ErrBlankArg        = errors.New("tenant and purpose are required")
	ErrInvalidTTL      = errors.New("token ttl must be positive")
	ErrInvalidCount    = errors.New("batch size must be positive")
	ErrBatchTooLarge   = errors.New("batch size above the configured cap")
	ErrQuotaExceeded   = errors.New("issue quota exhausted for this tenant and purpose")
	ErrTokenMalformed  = errors.New("raw token is not in id.secret form")
	ErrTokenNotFound   = errors.New("token not found")
	ErrTokenMismatch   = errors.New("token secret does not match the stored hash")
	ErrTokenExpired    = errors.New("token expired")
	ErrTokenRevoked    = errors.New("token revoked")
	ErrTokenConsumed   = errors.New("token already redeemed")
	ErrTenantMismatch  = errors.New("token belongs to another tenant")
	ErrPurposeMismatch = errors.New("token belongs to another purpose")
	ErrConfirmFailed   = errors.New("issuer confirmation failed")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Token is the issue time view of a token. Raw is only filled on issue, the
// stored record keeps the hash and never the secret.
type Token struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Purpose   string `json:"purpose"`
	State     string `json:"state"`
	ExpiresAt int64  `json:"expires_at"`
	Raw       string `json:"raw,omitempty"`
	Hash      string `json:"hash"`
}

// Subject is the account that redeemed a token.
type Subject struct {
	TokenID    string `json:"token_id"`
	TenantID   string `json:"tenant_id"`
	Purpose    string `json:"purpose"`
	RedeemedAt int64  `json:"redeemed_at"`
	Seq        int64  `json:"seq"`
}

// Quota is the per tenant per purpose accounting view.
type Quota struct {
	TenantID string `json:"tenant_id"`
	Purpose  string `json:"purpose"`
	Issued   int    `json:"issued"`
	Live     int    `json:"live"`
	Settled  int    `json:"settled"`
}

// VaultConfig carries the caps and the two injected dependencies.
type VaultConfig struct {
	MaxBatch          int
	MaxLivePerPurpose int
	DefaultTTL        time.Duration
	Now               func() time.Time
	Confirm           func(Subject) error
}

// OneTimeTokenVault owns the token table and the quota counters.
type OneTimeTokenVault struct {
	cfg VaultConfig
}

// NewOneTimeTokenVault builds a vault with the given caps.
func NewOneTimeTokenVault(cfg VaultConfig) *OneTimeTokenVault {
	return &OneTimeTokenVault{cfg: cfg}
}

// Issue mints one token for the tenant and purpose.
func (v *OneTimeTokenVault) Issue(tenantID, purpose string, ttl time.Duration) (Token, error) {
	return Token{}, errNotImplemented
}

// IssueBatch mints a batch of tokens or nothing at all.
func (v *OneTimeTokenVault) IssueBatch(tenantID, purpose string, n int) ([]Token, error) {
	return nil, errNotImplemented
}

// Redeem consumes a token atomically and returns the bound subject.
func (v *OneTimeTokenVault) Redeem(tenantID, purpose, raw string) (Subject, error) {
	return Subject{}, errNotImplemented
}

// Revoke kills a live token and keeps the record for audit.
func (v *OneTimeTokenVault) Revoke(tokenID string) error {
	return errNotImplemented
}

// Purge drops settled and expired records and reports how many went away.
func (v *OneTimeTokenVault) Purge(now time.Time) int {
	return 0
}

// Lookup reads one record without the secret.
func (v *OneTimeTokenVault) Lookup(tokenID string) (Token, bool) {
	return Token{}, false
}

// Count reports how many records are currently retained.
func (v *OneTimeTokenVault) Count() int {
	return 0
}

// Stats reports the quota view of one tenant and purpose.
func (v *OneTimeTokenVault) Stats(tenantID, purpose string) Quota {
	return Quota{}
}

// Snapshot serialises records, the quota counters and the sequence.
func (v *OneTimeTokenVault) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the vault state with a snapshot, all or nothing.
func (v *OneTimeTokenVault) Restore(data []byte) error {
	return errNotImplemented
}
