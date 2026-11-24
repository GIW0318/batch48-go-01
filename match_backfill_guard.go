// Package task screens mid-match backfill candidates, holds a seat per claim
// and keeps the backfill accounting away from the main roster totals.
package task

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// CandidateState is the pool position of one backfill candidate.
type CandidateState string

// Candidate states.
const (
	CandidateIdle     CandidateState = "idle"
	CandidateClaimed  CandidateState = "claimed"
	CandidateAttached CandidateState = "attached"
)

// MaxCandidatePool is the hard ceiling for every table owned by the guard:
// the candidate registry, the per-match claim bindings and the seat tables
// can never hold more entries than this.
const MaxCandidatePool = 2048

// SnapshotVersion is the only snapshot wire format understood by Restore.
const SnapshotVersion = 1

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

// candidateRecord is the persisted candidate view plus the absolute claim
// deadline used by the timeout sweep.
type candidateRecord struct {
	Candidate
	ClaimDeadline time.Time `json:"claim_deadline"`
}

// matchRecord stores the latest metadata reported for one match together
// with its seat bindings. Claims themselves stay on the candidates so the
// pool, the per-match holds and the seats never diverge.
type matchRecord struct {
	MatchID     string            `json:"match_id"`
	Region      string            `json:"region"`
	BasePlayers int               `json:"base_players"`
	Remaining   time.Duration     `json:"remaining"`
	Seats       map[string]string `json:"seats"`
}

// snapshotPayload is the on-wire format used by Snapshot and Restore.
type snapshotPayload struct {
	Version    int                         `json:"version"`
	Candidates map[string]*candidateRecord `json:"candidates"`
	Matches    map[string]*matchRecord     `json:"matches"`
	Results    map[string][]BackfillResult `json:"results"`
}

// BackfillGuard owns the candidate pool, the per-match claims and the seat
// bindings. Every mutable access is serialised through mu so a claim is an
// atomic state transition and snapshots observe all-old or all-new state.
type BackfillGuard struct {
	mu           sync.Mutex
	poolCap      int
	minRemaining time.Duration
	claimTTL     time.Duration
	clock        func() time.Time

	candidates map[string]*candidateRecord
	matches    map[string]*matchRecord
	results    map[string][]BackfillResult
}

// NewBackfillGuard builds a guard with a pool cap, a remaining-time threshold
// and a claim timeout.
func NewBackfillGuard(poolCap int, minRemaining, claimTTL time.Duration) *BackfillGuard {
	if poolCap < 0 {
		poolCap = 0
	}
	if poolCap > MaxCandidatePool {
		poolCap = MaxCandidatePool
	}
	return &BackfillGuard{
		poolCap:      poolCap,
		minRemaining: minRemaining,
		claimTTL:     claimTTL,
		clock:        time.Now,
		candidates:   make(map[string]*candidateRecord),
		matches:      make(map[string]*matchRecord),
		results:      make(map[string][]BackfillResult),
	}
}

// SetClock replaces the time source used for claim deadlines. A nil clock
// restores the wall clock.
func (g *BackfillGuard) SetClock(now func() time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now == nil {
		g.clock = time.Now
		return
	}
	g.clock = now
}

func (g *BackfillGuard) nowLocked() time.Time {
	if g.clock == nil {
		return time.Now()
	}
	return g.clock()
}

// backfillOpenLocked reports whether the match still has enough time for a
// backfill. The comparison is strict: remaining equal to the threshold is
// still accepted.
func (g *BackfillGuard) backfillOpenLocked(m *matchRecord) bool {
	return m.Remaining >= g.minRemaining
}

// resetCandidateBinding returns a candidate to the idle pool and clears every
// field owned by a claim or seat binding. It is the single rollback path used
// by Abort, the timeout sweep and backfill closure.
func resetCandidateBinding(c *candidateRecord) {
	c.State = CandidateIdle
	c.MatchID = ""
	c.SeatID = ""
	c.ClaimedAt = time.Time{}
	c.ClaimDeadline = time.Time{}
}

// deleteSeatBindingLocked removes the seat entry pointing at the candidate,
// if it still belongs to it.
func (g *BackfillGuard) deleteSeatBindingLocked(c *candidateRecord) {
	if c == nil || c.MatchID == "" || c.SeatID == "" {
		return
	}
	if m, ok := g.matches[c.MatchID]; ok && m.Seats != nil {
		if holder := m.Seats[c.SeatID]; holder == c.ID {
			delete(m.Seats, c.SeatID)
		}
	}
}

// withdrawClaimsLocked rolls back every unfulfilled claim of one match.
// Attached candidates have already entered the match and are never withdrawn.
func (g *BackfillGuard) withdrawClaimsLocked(matchID string) []Candidate {
	released := make([]Candidate, 0)
	for _, c := range g.candidates {
		if c.MatchID == matchID && c.State == CandidateClaimed {
			resetCandidateBinding(c)
			released = append(released, c.Candidate)
		}
	}
	return released
}

// OpenMatch registers the region, the current headcount and the remaining
// time of a running match. Opening the same match again replaces its latest
// metadata. Reporting a remaining time below the threshold closes backfill
// for the match and withdraws every pending (not yet attached) claim.
func (g *BackfillGuard) OpenMatch(matchID, region string, basePlayers int, remaining time.Duration) error {
	if strings.TrimSpace(matchID) == "" {
		return ErrInvalidMatchID
	}
	if remaining < 0 {
		return ErrInvalidRemaining
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	m, ok := g.matches[matchID]
	if !ok {
		if len(g.matches) >= MaxCandidatePool {
			return ErrPoolFull
		}
		m = &matchRecord{MatchID: matchID, Seats: make(map[string]string)}
		g.matches[matchID] = m
	}
	if m.Seats == nil {
		m.Seats = make(map[string]string)
	}
	m.Region = region
	m.BasePlayers = basePlayers
	m.Remaining = remaining

	if !g.backfillOpenLocked(m) {
		g.withdrawClaimsLocked(matchID)
	}
	return nil
}

// Register adds one candidate to the pool. A rejected registration never
// mutates the pool.
func (g *BackfillGuard) Register(candidateID, region string, rating int, latencyMS int) error {
	if strings.TrimSpace(candidateID) == "" {
		return ErrInvalidCandidateID
	}
	if strings.TrimSpace(region) == "" {
		return ErrInvalidRegion
	}
	if rating < 0 {
		return ErrInvalidRating
	}
	if latencyMS <= 0 {
		return ErrInvalidLatency
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.candidates[candidateID]; exists {
		return ErrCandidateExists
	}
	if len(g.candidates) >= g.poolCap || len(g.candidates) >= MaxCandidatePool {
		return ErrPoolFull
	}

	g.candidates[candidateID] = &candidateRecord{
		Candidate: Candidate{
			ID:        candidateID,
			Region:    region,
			Rating:    rating,
			LatencyMS: latencyMS,
			State:     CandidateIdle,
		},
	}
	return nil
}

// Pick returns up to need idle candidates from the match region, ordered by
// latency ascending, rating descending and id ascending. It is a read-only
// query and never changes candidate state.
func (g *BackfillGuard) Pick(matchID string, need int) ([]Candidate, error) {
	if need <= 0 {
		return nil, ErrInvalidNeed
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	m, ok := g.matches[matchID]
	if !ok {
		return nil, ErrMatchUnknown
	}
	if !g.backfillOpenLocked(m) {
		return nil, ErrBackfillClosed
	}

	picked := make([]Candidate, 0)
	for _, c := range g.candidates {
		if c.State == CandidateIdle && c.Region == m.Region {
			picked = append(picked, c.Candidate)
		}
	}

	sort.Slice(picked, func(i, j int) bool {
		if picked[i].LatencyMS != picked[j].LatencyMS {
			return picked[i].LatencyMS < picked[j].LatencyMS
		}
		if picked[i].Rating != picked[j].Rating {
			return picked[i].Rating > picked[j].Rating
		}
		return picked[i].ID < picked[j].ID
	})

	if len(picked) > need {
		picked = picked[:need]
	}
	return picked, nil
}

// Claim atomically reserves one candidate for a match. Exactly one concurrent
// caller wins; every other caller receives (false, nil). A candidate that is
// not idle can never be claimed again until its binding is rolled back.
func (g *BackfillGuard) Claim(matchID, candidateID string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	m, ok := g.matches[matchID]
	if !ok {
		return false, ErrMatchUnknown
	}
	if !g.backfillOpenLocked(m) {
		// The match crossed the threshold without a refreshed OpenMatch
		// (defensive path): pending holds are withdrawn before refusing.
		g.withdrawClaimsLocked(matchID)
		return false, ErrBackfillClosed
	}

	c, ok := g.candidates[candidateID]
	if !ok {
		return false, ErrCandidateUnknown
	}

	// Only the idle -> claimed transition is a win. Every losing caller,
	// including sequential re-claims, gets false without an error.
	if c.State != CandidateIdle {
		return false, nil
	}

	now := g.nowLocked()
	c.State = CandidateClaimed
	c.MatchID = matchID
	c.SeatID = ""
	c.ClaimedAt = now
	c.ClaimDeadline = now.Add(g.claimTTL)
	return true, nil
}

// Attach binds a claimed candidate to one seat of the match.
func (g *BackfillGuard) Attach(matchID, candidateID, seatID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	m, ok := g.matches[matchID]
	if !ok {
		return ErrMatchUnknown
	}
	c, ok := g.candidates[candidateID]
	if !ok {
		return ErrCandidateUnknown
	}
	if c.State == CandidateAttached {
		if c.MatchID == matchID {
			return ErrAlreadyAttached
		}
		return ErrNotClaimed
	}
	if c.State != CandidateClaimed || c.MatchID != matchID {
		return ErrNotClaimed
	}
	if holder, taken := m.Seats[seatID]; taken && holder != candidateID {
		return ErrSeatTaken
	}
	if _, taken := m.Seats[seatID]; !taken && len(m.Seats) >= MaxCandidatePool {
		return ErrPoolFull
	}

	m.Seats[seatID] = candidateID
	c.State = CandidateAttached
	c.MatchID = matchID
	c.SeatID = seatID
	return nil
}

// Abort drops a claim (or an attachment) and returns the candidate to the
// idle pool. It is the explicit rollback path and also frees a bound seat.
func (g *BackfillGuard) Abort(matchID, candidateID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	c, ok := g.candidates[candidateID]
	if !ok {
		return ErrCandidateUnknown
	}
	if c.State == CandidateIdle || c.MatchID == "" || c.MatchID != matchID {
		return ErrNotClaimed
	}

	g.deleteSeatBindingLocked(c)
	resetCandidateBinding(c)
	return nil
}

// RecordResult books the outcome of one backfilled player separately from the
// main roster. A result can only be recorded for a candidate bound to the
// match.
func (g *BackfillGuard) RecordResult(matchID, candidateID string, win bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.matches[matchID]; !ok {
		return ErrMatchUnknown
	}
	c, ok := g.candidates[candidateID]
	if !ok {
		return ErrCandidateUnknown
	}
	if c.MatchID != matchID || c.State == CandidateIdle {
		return ErrNotClaimed
	}

	g.results[matchID] = append(g.results[matchID], BackfillResult{
		CandidateID: candidateID,
		Win:         win,
	})
	return nil
}

// BackfillResults lists the separate result book of one match in recording
// order.
func (g *BackfillGuard) BackfillResults(matchID string) []BackfillResult {
	g.mu.Lock()
	defer g.mu.Unlock()

	in := g.results[matchID]
	out := make([]BackfillResult, len(in))
	copy(out, in)
	return out
}

// Headcount is the total number of players in the match, backfill included.
// Every candidate currently bound to the match occupies one player slot;
// aborted, expired or withdrawn claims clear the binding and drop back out.
func (g *BackfillGuard) Headcount(matchID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	m, ok := g.matches[matchID]
	if !ok {
		return 0
	}
	total := m.BasePlayers
	for _, c := range g.candidates {
		if c.MatchID == matchID {
			total++
		}
	}
	return total
}

// Remaining reads the latest remaining time recorded for a match. An unknown
// match reports zero.
func (g *BackfillGuard) Remaining(matchID string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()

	if m, ok := g.matches[matchID]; ok {
		return m.Remaining
	}
	return 0
}

// Candidate reads one candidate. The returned value is a copy.
func (g *BackfillGuard) Candidate(candidateID string) (Candidate, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	c, ok := g.candidates[candidateID]
	if !ok {
		return Candidate{}, false
	}
	return c.Candidate, true
}

// Claimed lists the candidates currently in the claimed state for one match,
// ordered by candidate id ascending. Attached candidates have moved past the
// claim stage and are exposed through Seats instead.
func (g *BackfillGuard) Claimed(matchID string) []Candidate {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]Candidate, 0)
	for _, c := range g.candidates {
		if c.MatchID == matchID && c.State == CandidateClaimed {
			out = append(out, c.Candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Seats maps every occupied seat of a match to its candidate. The returned
// map is a copy safe for the caller to mutate.
func (g *BackfillGuard) Seats(matchID string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make(map[string]string)
	if m, ok := g.matches[matchID]; ok {
		for seatID, candidateID := range m.Seats {
			out[seatID] = candidateID
		}
	}
	return out
}

// ExpireClaims releases claims whose deadline is at or before now. Attached
// candidates have already entered the match and are never returned to the
// pool. Only claims released by this call are returned, so sweeping twice
// with the same time yields an empty second round.
func (g *BackfillGuard) ExpireClaims(now time.Time) []Candidate {
	g.mu.Lock()
	defer g.mu.Unlock()

	due := make([]*candidateRecord, 0)
	for _, c := range g.candidates {
		if c.State == CandidateClaimed && !now.Before(c.ClaimDeadline) {
			due = append(due, c)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })

	released := make([]Candidate, 0, len(due))
	for _, c := range due {
		resetCandidateBinding(c)
		released = append(released, c.Candidate)
	}
	return released
}

// Snapshot serialises the candidate states, the absolute claim deadlines,
// the matches and the seat bindings.
func (g *BackfillGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	payload := snapshotPayload{
		Version:    SnapshotVersion,
		Candidates: make(map[string]*candidateRecord, len(g.candidates)),
		Matches:    make(map[string]*matchRecord, len(g.matches)),
		Results:    make(map[string][]BackfillResult, len(g.results)),
	}
	for id, c := range g.candidates {
		copyCandidate := *c
		payload.Candidates[id] = &copyCandidate
	}
	for id, m := range g.matches {
		copyMatch := *m
		copyMatch.Seats = make(map[string]string, len(m.Seats))
		for seatID, candidateID := range m.Seats {
			copyMatch.Seats[seatID] = candidateID
		}
		payload.Matches[id] = &copyMatch
	}
	for matchID, rows := range g.results {
		payload.Results[matchID] = append(make([]BackfillResult, 0, len(rows)), rows...)
	}
	return json.Marshal(payload)
}

// Restore replaces the whole guard state with a snapshot. The payload is fully
// parsed and validated before any live state is touched, so a bad snapshot
// leaves the current guard untouched. Constructor configuration and the
// injected clock are retained.
func (g *BackfillGuard) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}

	var payload snapshotPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ErrBadSnapshot
	}
	if payload.Version != SnapshotVersion {
		return ErrBadSnapshot
	}

	newCandidates, err := decodeSnapshotCandidates(payload.Candidates)
	if err != nil {
		return err
	}
	newMatches, err := decodeSnapshotMatches(payload.Matches, newCandidates)
	if err != nil {
		return err
	}
	newResults, err := decodeSnapshotResults(payload.Results, newMatches)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.candidates = newCandidates
	g.matches = newMatches
	g.results = newResults
	return nil
}

func decodeSnapshotCandidates(in map[string]*candidateRecord) (map[string]*candidateRecord, error) {
	out := make(map[string]*candidateRecord, len(in))
	if len(in) > MaxCandidatePool {
		return nil, ErrBadSnapshot
	}
	for key, c := range in {
		if c == nil || key == "" || c.ID != key {
			return nil, ErrBadSnapshot
		}
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Region) == "" {
			return nil, ErrBadSnapshot
		}
		if c.Rating < 0 || c.LatencyMS <= 0 {
			return nil, ErrBadSnapshot
		}
		switch c.State {
		case CandidateIdle:
			if c.MatchID != "" || c.SeatID != "" || !c.ClaimedAt.IsZero() || !c.ClaimDeadline.IsZero() {
				return nil, ErrBadSnapshot
			}
		case CandidateClaimed:
			if c.MatchID == "" || c.SeatID != "" || c.ClaimDeadline.IsZero() {
				return nil, ErrBadSnapshot
			}
		case CandidateAttached:
			if c.MatchID == "" {
				return nil, ErrBadSnapshot
			}
		default:
			return nil, ErrBadSnapshot
		}
		out[c.ID] = c
	}
	return out, nil
}

func decodeSnapshotMatches(in map[string]*matchRecord, candidates map[string]*candidateRecord) (map[string]*matchRecord, error) {
	out := make(map[string]*matchRecord, len(in))
	for key, m := range in {
		if m == nil || key == "" || m.MatchID != key || m.Remaining < 0 {
			return nil, ErrBadSnapshot
		}
		if m.Seats == nil {
			m.Seats = make(map[string]string)
		}
		if len(m.Seats) > MaxCandidatePool {
			return nil, ErrBadSnapshot
		}

		// Every persisted seat entry must match an attached candidate. An
		// empty seat key is still a legal binding because Attach does not
		// forbid it.
		for seatID, candidateID := range m.Seats {
			if candidateID == "" {
				return nil, ErrBadSnapshot
			}
			c, ok := candidates[candidateID]
			if !ok || c.State != CandidateAttached || c.MatchID != key || c.SeatID != seatID {
				return nil, ErrBadSnapshot
			}
		}
		// ...and every attached candidate of this match must own exactly one
		// persisted seat.
		for _, c := range candidates {
			if c.State == CandidateAttached && c.MatchID == key {
				holder, ok := m.Seats[c.SeatID]
				if !ok || holder != c.ID {
					return nil, ErrBadSnapshot
				}
			}
		}
		out[key] = m
	}

	// Claims and attachments cannot reference missing matches.
	for _, c := range candidates {
		if c.MatchID != "" {
			if _, ok := out[c.MatchID]; !ok {
				return nil, ErrBadSnapshot
			}
		}
	}
	return out, nil
}

func decodeSnapshotResults(in map[string][]BackfillResult, matches map[string]*matchRecord) (map[string][]BackfillResult, error) {
	out := make(map[string][]BackfillResult, len(in))
	for matchID, rows := range in {
		if _, ok := matches[matchID]; !ok {
			return nil, ErrBadSnapshot
		}
		cloned := make([]BackfillResult, 0, len(rows))
		for _, row := range rows {
			if row.CandidateID == "" {
				return nil, ErrBadSnapshot
			}
			cloned = append(cloned, row)
		}
		out[matchID] = cloned
	}
	return out, nil
}
