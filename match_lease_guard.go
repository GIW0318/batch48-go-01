// Package task owns match leases: one live holder per match, a monotonically
// increasing fencing token and takeover after the lease expires.
package task

import (
	"encoding/json"
	"errors"
	"strings"
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

// MaxHeldWritesPerMatch caps the writes parked while a takeover holder is
// still aligning its state.
const MaxHeldWritesPerMatch = 4096

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
	records   map[string]*leaseRecord
}

// leaseRecord is the persisted state of one match lease.
type leaseRecord struct {
	MatchID     string        `json:"match_id"`
	InstanceID  string        `json:"instance_id"`
	Fence       uint64        `json:"fence"`
	State       LeaseState    `json:"state"`
	ExpiresAt   time.Time     `json:"expires_at"`
	TTL         time.Duration `json:"ttl"`
	Aligned     bool          `json:"aligned"`
	StaleWrites int           `json:"stale_writes"`
	HeldWrites  int           `json:"held_writes"`
}

// guardSnapshot is the on-wire format used by Snapshot and Restore.
type guardSnapshot struct {
	Version int                     `json:"version"`
	Records map[string]*leaseRecord `json:"records"`
}

func (r *leaseRecord) view() Lease {
	return Lease{
		MatchID:    r.MatchID,
		InstanceID: r.InstanceID,
		Fence:      r.Fence,
		State:      r.State,
		ExpiresAt:  r.ExpiresAt,
	}
}

// live reports whether the record is a currently held, unexpired lease. Every
// expiry decision goes through this single helper and the injected clock.
func (r *leaseRecord) live(now time.Time) bool {
	return (r.State == LeaseActive || r.State == LeaseAligning) && r.ExpiresAt.After(now)
}

func validateLeaseIDs(matchID, instanceID string) error {
	if matchID == "" {
		return ErrInvalidMatchID
	}
	if strings.TrimSpace(instanceID) == "" {
		return ErrInvalidInstanceID
	}
	return nil
}

// NewMatchLeaseGuard builds a guard whose table holds at most maxLeases match
// records.
func NewMatchLeaseGuard(maxLeases int) *MatchLeaseGuard {
	return &MatchLeaseGuard{
		maxLeases: maxLeases,
		clock:     time.Now,
		records:   make(map[string]*leaseRecord),
	}
}

// SetClock replaces the time source used for lease deadlines.
func (g *MatchLeaseGuard) SetClock(now func() time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now == nil {
		g.clock = time.Now
		return
	}
	g.clock = now
}

// Acquire grants the first lease of a match with fencing token one.
func (g *MatchLeaseGuard) Acquire(matchID, instanceID string, ttl time.Duration) (Lease, error) {
	if err := validateLeaseIDs(matchID, instanceID); err != nil {
		return Lease{}, err
	}
	if ttl <= 0 {
		return Lease{}, ErrInvalidTTL
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.clock()
	if rec, ok := g.records[matchID]; ok {
		if rec.live(now) {
			return Lease{}, ErrLeaseHeld
		}
		if rec.State == LeaseActive || rec.State == LeaseAligning {
			// An expired lease must go through Takeover so the new holder
			// aligns state before writing.
			return Lease{}, ErrLeaseExpired
		}
		// Released record: re-acquire with the next fencing token.
		rec.Fence++
		rec.InstanceID = instanceID
		rec.State = LeaseActive
		rec.Aligned = true
		rec.ExpiresAt = now.Add(ttl)
		rec.TTL = ttl
		rec.HeldWrites = 0
		return rec.view(), nil
	}

	if len(g.records) >= g.maxLeases {
		return Lease{}, ErrLeaseTableFull
	}
	rec := &leaseRecord{
		MatchID:    matchID,
		InstanceID: instanceID,
		Fence:      1,
		State:      LeaseActive,
		Aligned:    true,
		ExpiresAt:  now.Add(ttl),
		TTL:        ttl,
	}
	g.records[matchID] = rec
	return rec.view(), nil
}

// Renew extends a live lease after checking the instance and the fencing
// token.
func (g *MatchLeaseGuard) Renew(matchID, instanceID string, fence uint64) error {
	if err := validateLeaseIDs(matchID, instanceID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok {
		return ErrLeaseNotFound
	}
	now := g.clock()
	if rec.State == LeaseReleased {
		return ErrNotLeaseHolder
	}
	if !rec.ExpiresAt.After(now) {
		// An expired lease can never be brought back to life; takeover is
		// the only way forward.
		return ErrLeaseExpired
	}
	if rec.Fence != fence {
		return ErrFenceMismatch
	}
	if rec.InstanceID != instanceID {
		return ErrNotLeaseHolder
	}
	rec.ExpiresAt = now.Add(rec.TTL)
	return nil
}

// Release gives a lease back before its deadline.
func (g *MatchLeaseGuard) Release(matchID, instanceID string) error {
	if err := validateLeaseIDs(matchID, instanceID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok {
		return ErrLeaseNotFound
	}
	if rec.State == LeaseReleased || rec.InstanceID != instanceID {
		return ErrNotLeaseHolder
	}
	rec.State = LeaseReleased
	rec.Aligned = false
	rec.HeldWrites = 0
	return nil
}

// Takeover grants the lease to a new instance once the old one expired.
func (g *MatchLeaseGuard) Takeover(matchID, instanceID string) (Lease, error) {
	if err := validateLeaseIDs(matchID, instanceID); err != nil {
		return Lease{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok || rec.State == LeaseReleased {
		return Lease{}, ErrLeaseNotFound
	}
	if rec.live(g.clock()) {
		return Lease{}, ErrLeaseActive
	}
	rec.Fence++
	rec.InstanceID = instanceID
	rec.State = LeaseAligning
	rec.Aligned = false
	rec.ExpiresAt = g.clock().Add(rec.TTL)
	rec.HeldWrites = 0
	return rec.view(), nil
}

// Fence reports the highest fencing token issued for a match.
func (g *MatchLeaseGuard) Fence(matchID string) (uint64, error) {
	if matchID == "" {
		return 0, ErrInvalidMatchID
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok {
		return 0, ErrLeaseNotFound
	}
	return rec.Fence, nil
}

// Apply admits a state write only when the caller holds the live lease and
// presents the current fencing token.
func (g *MatchLeaseGuard) Apply(matchID, instanceID string, fence uint64, payload string) error {
	if err := validateLeaseIDs(matchID, instanceID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok {
		return ErrLeaseNotFound
	}
	// Token first: a displaced old holder presenting an old token is a stale
	// writer even though it used to be the lease holder.
	if rec.Fence != fence {
		if fence < rec.Fence {
			rec.StaleWrites++
		}
		return ErrFenceMismatch
	}
	if rec.InstanceID != instanceID || rec.State == LeaseReleased {
		return ErrNotLeaseHolder
	}
	now := g.clock()
	if !rec.ExpiresAt.After(now) {
		return ErrLeaseExpired
	}
	if rec.State == LeaseAligning {
		if rec.HeldWrites >= MaxHeldWritesPerMatch {
			return ErrLeaseTableFull
		}
		rec.HeldWrites++
		return ErrNotAligned
	}
	return nil
}

// Align marks the state of a takeover holder as caught up.
func (g *MatchLeaseGuard) Align(matchID, instanceID string) error {
	if err := validateLeaseIDs(matchID, instanceID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok {
		return ErrLeaseNotFound
	}
	if rec.State == LeaseReleased || rec.InstanceID != instanceID {
		return ErrNotLeaseHolder
	}
	if !rec.ExpiresAt.After(g.clock()) {
		return ErrLeaseExpired
	}
	if rec.State == LeaseAligning {
		rec.State = LeaseActive
		rec.Aligned = true
		rec.HeldWrites = 0
	}
	return nil
}

// StaleWrites reports how many writes were rejected for a stale fencing token.
func (g *MatchLeaseGuard) StaleWrites(matchID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rec, ok := g.records[matchID]; ok {
		return rec.StaleWrites
	}
	return 0
}

// HeldWrites reports how many writes are parked while a takeover holder is
// still aligning its state.
func (g *MatchLeaseGuard) HeldWrites(matchID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rec, ok := g.records[matchID]; ok {
		return rec.HeldWrites
	}
	return 0
}

// Lease reads the live lease of a match.
func (g *MatchLeaseGuard) Lease(matchID string) (Lease, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[matchID]
	if !ok {
		return Lease{}, false
	}
	view := rec.view()
	if !rec.live(g.clock()) {
		if rec.State == LeaseReleased {
			view.State = LeaseReleased
		} else {
			view.State = LeaseExpired
		}
		return view, false
	}
	return view, true
}

// Snapshot serialises the lease table, the fencing counters and the pending
// writes.
func (g *MatchLeaseGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	records := make(map[string]*leaseRecord, len(g.records))
	for matchID, rec := range g.records {
		copy := *rec
		records[matchID] = &copy
	}
	return json.Marshal(guardSnapshot{Version: 1, Records: records})
}

// Restore replaces the whole table with a snapshot, all or nothing.
func (g *MatchLeaseGuard) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}
	var snap guardSnapshot
	if err := json.Unmarshal(data, &snap); err != nil || snap.Records == nil {
		return ErrBadSnapshot
	}
	for _, rec := range snap.Records {
		if rec == nil || rec.MatchID == "" || rec.InstanceID == "" ||
			rec.Fence == 0 || rec.TTL <= 0 || rec.ExpiresAt.IsZero() {
			return ErrBadSnapshot
		}
		switch rec.State {
		case LeaseActive, LeaseAligning, LeaseExpired, LeaseReleased:
		default:
			return ErrBadSnapshot
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.records = snap.Records
	return nil
}
