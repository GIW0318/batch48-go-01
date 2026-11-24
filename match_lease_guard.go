// Package task owns match leases: one live holder per match, a monotonically
// increasing fencing token and takeover after the lease expires.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// LeaseState is the readiness of a lease holder.
type LeaseState string

// Lease states.
const (
	LeaseActive   LeaseState = "active"
	LeaseAligning LeaseState = "aligning"
	LeaseExpired  LeaseState = "expired"
	LeaseReleased LeaseState = "released"
)

// MaxLeaseTable caps the number of match records kept in the table.
const MaxLeaseTable = 4096

// Errors returned by the match lease guard.
var (
	ErrInvalidMatchID    = errors.New("match id is required")
	ErrInvalidInstanceID = errors.New("instance id is required")
	ErrInvalidTTL        = errors.New("ttl must be positive")
	ErrLeaseHeld         = errors.New("match already has a live lease")
	ErrLeaseActive       = errors.New("match lease is still active")
	ErrLeaseExpired      = errors.New("match lease has expired")
	ErrLeaseNotFound     = errors.New("match has no lease record")
	ErrNotLeaseHolder    = errors.New("instance does not hold the lease")
	ErrFenceMismatch     = errors.New("fencing token does not match")
	ErrLeaseTableFull    = errors.New("lease table is full")
	ErrNotAligned        = errors.New("lease holder has not finished state alignment")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// Lease is the caller facing view of one match lease.
type Lease struct {
	MatchID    string     `json:"match_id"`
	InstanceID string     `json:"instance_id"`
	Fence      uint64     `json:"fence"`
	State      LeaseState `json:"state"`
	ExpiresAt  time.Time  `json:"expires_at"`
}

// MatchLeaseGuard keeps the lease table and the fencing counters.
type MatchLeaseGuard struct {
	mu        sync.Mutex
	maxLeases int
	clock     func() time.Time
}

// NewMatchLeaseGuard builds a guard whose table holds at most maxLeases match
// records.
func NewMatchLeaseGuard(maxLeases int) *MatchLeaseGuard {
	return &MatchLeaseGuard{maxLeases: maxLeases, clock: time.Now}
}

// SetClock replaces the time source used for lease deadlines.
func (g *MatchLeaseGuard) SetClock(now func() time.Time) {
}

// Acquire grants the first lease of a match with fencing token one.
func (g *MatchLeaseGuard) Acquire(matchID, instanceID string, ttl time.Duration) (Lease, error) {
	return Lease{}, errNotImplemented
}

// Renew extends a live lease after checking the instance and the fencing
// token.
func (g *MatchLeaseGuard) Renew(matchID, instanceID string, fence uint64) error {
	return errNotImplemented
}

// Release gives a lease back before its deadline.
func (g *MatchLeaseGuard) Release(matchID, instanceID string) error {
	return errNotImplemented
}

// Takeover grants the lease to a new instance once the old one expired.
func (g *MatchLeaseGuard) Takeover(matchID, instanceID string) (Lease, error) {
	return Lease{}, errNotImplemented
}

// Fence reports the highest fencing token issued for a match.
func (g *MatchLeaseGuard) Fence(matchID string) (uint64, error) {
	return 0, errNotImplemented
}

// Apply admits a state write only when the caller holds the live lease and
// presents the current fencing token.
func (g *MatchLeaseGuard) Apply(matchID, instanceID string, fence uint64, payload string) error {
	return errNotImplemented
}

// Align marks the state of a takeover holder as caught up.
func (g *MatchLeaseGuard) Align(matchID, instanceID string) error {
	return errNotImplemented
}

// StaleWrites reports how many writes were rejected for a stale fencing token.
func (g *MatchLeaseGuard) StaleWrites(matchID string) int {
	return 0
}

// HeldWrites reports how many writes are parked while a takeover holder is
// still aligning its state.
func (g *MatchLeaseGuard) HeldWrites(matchID string) int {
	return 0
}

// Lease reads the live lease of a match.
func (g *MatchLeaseGuard) Lease(matchID string) (Lease, bool) {
	return Lease{}, false
}

// Snapshot serialises the lease table, the fencing counters and the pending
// writes.
func (g *MatchLeaseGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole table with a snapshot, all or nothing.
func (g *MatchLeaseGuard) Restore(data []byte) error {
	return errNotImplemented
}
