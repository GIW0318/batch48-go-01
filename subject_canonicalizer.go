// Package task canonicalises tenant scoped subject identifiers, keeps a per
// tenant reservation table and reports positional differences against the
// canonical form.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors returned by the canonicalizer.
var (
	ErrBlankTenant      = errors.New("blank tenant")
	ErrBlankIdentity    = errors.New("blank identity")
	ErrUnknownKind      = errors.New("unknown kind")
	ErrKeyTooLong       = errors.New("key too long")
	ErrEmptySubject     = errors.New("empty subject after normalization")
	ErrMalformedEmail   = errors.New("malformed email")
	ErrReservationLimit = errors.New("reservation limit")
	ErrBadSnapshot      = errors.New("bad snapshot")
)

// Subject kinds accepted by the canonicalizer.
const (
	KindAccount = "account"
	KindEmail   = "email"
	KindGroup   = "group"
)

// Reservation actions kept in the short history of one key.
const (
	ActionReserve = "reserve"
	ActionRelease = "release"
)

// CanonRules bounds the reservation table and the key length.
type CanonRules struct {
	MaxReservationsPerTenant int
	HistoryDepth             int
	MaxKeyLen                int
}

// Key is the canonical form of a subject identifier.
type Key struct {
	TenantID string
	Kind     string
	Value    string
	Digest   string
}

// Conflict is one positional difference between the raw and canonical form.
type Conflict struct {
	Position int
	Want     rune
	Got      rune
	Reason   string
}

// Reservation is one entry of the short history kept for a reserved key.
type Reservation struct {
	Key       string
	AccountID string
	At        time.Time
	Action    string
}

// SubjectCanonicalizer keeps reservations and history isolated per tenant.
type SubjectCanonicalizer struct {
	mu       sync.Mutex
	rules    CanonRules
	reserved map[string]map[string]Reservation
	history  map[string]map[string][]Reservation
}

// NewSubjectCanonicalizer builds a canonicalizer with the supplied rules.
func NewSubjectCanonicalizer(rules CanonRules) *SubjectCanonicalizer {
	return &SubjectCanonicalizer{
		rules:    rules,
		reserved: make(map[string]map[string]Reservation),
		history:  make(map[string]map[string][]Reservation),
	}
}

// Canonicalize folds the raw identifier of one kind into its canonical key.
func (c *SubjectCanonicalizer) Canonicalize(tenantID, kind, raw string) (Key, error) {
	return Key{}, errNotImplemented
}

// Reserve claims one canonical key for an account inside a tenant.
func (c *SubjectCanonicalizer) Reserve(tenantID, key, accountID string) (bool, error) {
	return false, errNotImplemented
}

// Release drops the reservation of one canonical key.
func (c *SubjectCanonicalizer) Release(tenantID, key string) error {
	return errNotImplemented
}

// Lookup returns the account that owns a canonical key inside a tenant.
func (c *SubjectCanonicalizer) Lookup(tenantID, key string) (string, bool) {
	return "", false
}

// Conflicts reports where the raw identifier differs from its canonical form.
func (c *SubjectCanonicalizer) Conflicts(tenantID, kind, raw string) ([]Conflict, error) {
	return nil, errNotImplemented
}

// History returns the short reservation history of one key, oldest first.
func (c *SubjectCanonicalizer) History(tenantID, key string) []Reservation {
	return nil
}

// ReserveCount reports how many keys the tenant currently holds.
func (c *SubjectCanonicalizer) ReserveCount(tenantID string) int {
	return 0
}

// Snapshot serialises reservations and history of every tenant.
func (c *SubjectCanonicalizer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces reservations and history with a snapshot.
func (c *SubjectCanonicalizer) Restore(data []byte) error {
	return errNotImplemented
}
