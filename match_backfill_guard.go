// Package task screens mid-match backfill candidates, holds a seat per claim
// and keeps the backfill accounting away from the main roster totals.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// CandidateState is the pool position of one backfill candidate.
type CandidateState string

// Candidate states.
const (
	CandidateIdle     CandidateState = "idle"
	CandidateClaimed  CandidateState = "claimed"
	CandidateAttached CandidateState = "attached"
)

// MaxCandidatePool caps the number of registered candidates.
const MaxCandidatePool = 2048

// Errors returned by the backfill guard.
var (
	ErrInvalidCandidateID = errors.New("candidate id is required")
	ErrInvalidRegion      = errors.New("region is required")
	ErrInvalidRating      = errors.New("rating must not be negative")
	ErrInvalidLatency     = errors.New("latency must be positive")
	ErrInvalidMatchID     = errors.New("match id is required")
	ErrInvalidRemaining   = errors.New("remaining time must not be negative")
	ErrCandidateExists    = errors.New("candidate already registered")
	ErrCandidateUnknown   = errors.New("candidate is not registered")
	ErrPoolFull           = errors.New("candidate pool is full")
	ErrInvalidNeed        = errors.New("need must be positive")
	ErrMatchUnknown       = errors.New("match is not open for backfill")
	ErrBackfillClosed     = errors.New("backfill is closed for this match")
	ErrNotClaimed         = errors.New("candidate is not claimed for this match")
	ErrSeatTaken          = errors.New("seat already holds a candidate")
	ErrAlreadyAttached    = errors.New("candidate is already attached")
	ErrBadSnapshot        = errors.New("snapshot payload is invalid")
)

// Candidate is one player waiting to be flown into a running match.
type Candidate struct {
	ID        string         `json:"id"`
	Region    string         `json:"region"`
	Rating    int            `json:"rating"`
	LatencyMS int            `json:"latency_ms"`
	State     CandidateState `json:"state"`
	MatchID   string         `json:"match_id"`
	SeatID    string         `json:"seat_id"`
	ClaimedAt time.Time      `json:"claimed_at"`
}

// BackfillResult is the separate result book of one backfilled player.
type BackfillResult struct {
	CandidateID string `json:"candidate_id"`
	Win         bool   `json:"win"`
}

// BackfillGuard owns the candidate pool, the per-match claims and the seat
// bindings.
type BackfillGuard struct {
	mu           sync.Mutex
	poolCap      int
	minRemaining time.Duration
	claimTTL     time.Duration
	clock        func() time.Time
}

// NewBackfillGuard builds a guard with a pool cap, a remaining-time threshold
// and a claim timeout.
func NewBackfillGuard(poolCap int, minRemaining, claimTTL time.Duration) *BackfillGuard {
	return &BackfillGuard{poolCap: poolCap, minRemaining: minRemaining, claimTTL: claimTTL, clock: time.Now}
}

// SetClock replaces the time source used for claim deadlines.
func (g *BackfillGuard) SetClock(now func() time.Time) {
}

// OpenMatch registers the region, the current headcount and the remaining
// time of a running match.
func (g *BackfillGuard) OpenMatch(matchID, region string, basePlayers int, remaining time.Duration) error {
	return errNotImplemented
}

// Register adds one candidate to the pool.
func (g *BackfillGuard) Register(candidateID, region string, rating int, latencyMS int) error {
	return errNotImplemented
}

// Pick returns up to need idle candidates of the match region, ordered by
// latency, then rating, then id.
func (g *BackfillGuard) Pick(matchID string, need int) ([]Candidate, error) {
	return nil, errNotImplemented
}

// Claim reserves one candidate for a match; the bool reports whether this
// caller won the candidate.
func (g *BackfillGuard) Claim(matchID, candidateID string) (bool, error) {
	return false, errNotImplemented
}

// Attach binds a claimed candidate to one seat of the match.
func (g *BackfillGuard) Attach(matchID, candidateID, seatID string) error {
	return errNotImplemented
}

// Abort drops a claim and puts the candidate back into the idle pool.
func (g *BackfillGuard) Abort(matchID, candidateID string) error {
	return errNotImplemented
}

// RecordResult books the result of one backfilled player separately from the
// main roster.
func (g *BackfillGuard) RecordResult(matchID, candidateID string, win bool) error {
	return errNotImplemented
}

// BackfillResults lists the separate result book of one match.
func (g *BackfillGuard) BackfillResults(matchID string) []BackfillResult {
	return nil
}

// Headcount is the total number of players in the match, backfill included.
func (g *BackfillGuard) Headcount(matchID string) int {
	return 0
}

// Remaining reads the remaining time of a match.
func (g *BackfillGuard) Remaining(matchID string) time.Duration {
	return 0
}

// Candidate reads one candidate.
func (g *BackfillGuard) Candidate(candidateID string) (Candidate, bool) {
	return Candidate{}, false
}

// Claimed lists the candidates currently claimed by one match, ordered by id.
func (g *BackfillGuard) Claimed(matchID string) []Candidate {
	return nil
}

// Seats maps every occupied seat of a match to its candidate.
func (g *BackfillGuard) Seats(matchID string) map[string]string {
	return nil
}

// ExpireClaims releases claims that stayed unfulfilled for longer than the
// claim timeout.
func (g *BackfillGuard) ExpireClaims(now time.Time) []Candidate {
	return nil
}

// Snapshot serialises the pool, the match records, the claims and the seat
// bindings.
func (g *BackfillGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole guard state with a snapshot, all or nothing.
func (g *BackfillGuard) Restore(data []byte) error {
	return errNotImplemented
}
