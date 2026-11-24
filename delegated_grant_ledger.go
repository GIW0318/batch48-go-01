package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by DelegatedGrantLedger.
var (
	ErrInvalidSubject    = errors.New("invalid subject id")
	ErrInvalidDelegation = errors.New("invalid delegation")
	ErrEmptyPermission   = errors.New("empty permission set")
	ErrInvalidExpiry     = errors.New("invalid grant expiry")
	ErrNotSubset         = errors.New("delegated permissions are not a subset of the delegator")
	ErrDepthLimit        = errors.New("delegation depth limit reached")
	ErrGrantLimit        = errors.New("grant limit reached for the subject")
	ErrUnknownGrant      = errors.New("unknown grant")
	ErrAlreadyAccepted   = errors.New("grant already accepted")
	ErrBadSnapshot       = errors.New("bad snapshot")
)

// Permission is one resource and action pair.
type Permission struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// PermissionSet is a collection of permissions.
type PermissionSet []Permission

// Grant is one delegation of permissions.
type Grant struct {
	ID        string        `json:"id"`
	FromID    string        `json:"from_id"`
	ToID      string        `json:"to_id"`
	Perms     PermissionSet `json:"perms"`
	ExpiresAt time.Time     `json:"expires_at"`
	Accepted  bool          `json:"accepted"`
	Revoked   bool          `json:"revoked"`
	Depth     int           `json:"depth"`
	ParentID  string        `json:"parent_id"`
}

// Source explains where one effective permission came from.
type Source struct {
	GrantID    string     `json:"grant_id"`
	Permission Permission `json:"permission"`
	Chain      []string   `json:"chain"`
}

// LedgerOptions bounds the ledger.
type LedgerOptions struct {
	MaxDepth            int
	MaxGrantsPerSubject int
	Now                 func() time.Time
}

// DelegatedGrantLedger registers, evaluates and revokes delegated grants.
type DelegatedGrantLedger struct {
	mu       sync.Mutex
	maxDepth int
	maxCount int
	now      func() time.Time
}

// NewDelegatedGrantLedger builds an empty ledger.
func NewDelegatedGrantLedger(opts LedgerOptions) *DelegatedGrantLedger {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &DelegatedGrantLedger{
		maxDepth: opts.MaxDepth,
		maxCount: opts.MaxGrantsPerSubject,
		now:      now,
	}
}

// Seed gives a subject its own permissions.
func (l *DelegatedGrantLedger) Seed(subjectID string, perms PermissionSet) error {
	return errNotImplemented
}

// Delegate registers a delegation from one subject to another.
func (l *DelegatedGrantLedger) Delegate(fromID, toID string, perms PermissionSet, expiresAt time.Time) (Grant, error) {
	return Grant{}, errNotImplemented
}

// Accept marks a delegation as accepted by the delegate.
func (l *DelegatedGrantLedger) Accept(grantID string) error {
	return errNotImplemented
}

// Effective evaluates the permissions of a subject at a point in time.
func (l *DelegatedGrantLedger) Effective(subjectID string, at time.Time) (PermissionSet, error) {
	return nil, errNotImplemented
}

// Sources explains the origin of every effective permission of a subject.
func (l *DelegatedGrantLedger) Sources(subjectID string, at time.Time) ([]Source, error) {
	return nil, errNotImplemented
}

// Revoke removes one grant and every delegation derived from it.
func (l *DelegatedGrantLedger) Revoke(grantID string) (int, error) {
	return 0, errNotImplemented
}

// Cascade revokes every grant issued by one subject.
func (l *DelegatedGrantLedger) Cascade(subjectID string) (int, error) {
	return 0, errNotImplemented
}

// GrantCount reports the live grants a subject holds.
func (l *DelegatedGrantLedger) GrantCount(subjectID string) (int, error) {
	return 0, errNotImplemented
}

// Depth reports how many delegation hops a grant sits from its root.
func (l *DelegatedGrantLedger) Depth(grantID string) (int, error) {
	return 0, errNotImplemented
}

// Snapshot serialises the whole ledger state.
func (l *DelegatedGrantLedger) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the ledger state from a snapshot.
func (l *DelegatedGrantLedger) Restore(data []byte) error {
	return errNotImplemented
}
