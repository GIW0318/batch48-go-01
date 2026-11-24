// Package task drives season phases: it attributes timestamped writes to
// the phase whose interval contains them and settles a season once.
package task

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// maxRejections caps the per season rejection log.
const maxRejections = 64

// Rejection reasons recorded on a refused write.
const (
	ReasonPhaseSettled  = "phase_settled"
	ReasonBeforeStart   = "before_season_start"
	ReasonOutsidePhases = "outside_phase_window"
)

// Value is one timestamped write submitted for a season.
type Value struct {
	Amount int64  `json:"amount"`
	Tag    string `json:"tag"`
}

// Phase is the caller facing view of one phase.
type Phase struct {
	SeasonID  string    `json:"season_id"`
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Settled   bool      `json:"settled"`
	Reopened  bool      `json:"reopened"`
}

// Entry is one accepted write.
type Entry struct {
	Subject string `json:"subject"`
	Amount  int64  `json:"amount"`
	Phase   string `json:"phase"`
}

// Settlement is the single result produced for one settle call.
type Settlement struct {
	SeasonID  string    `json:"season_id"`
	Phase     string    `json:"phase"`
	SettledAt time.Time `json:"settled_at"`
	Entries   []Entry   `json:"entries"`
	Total     int64     `json:"total"`
	Duplicate bool      `json:"duplicate"`
}

// Rejection records one refused write so support can replay it.
type Rejection struct {
	SeasonID string    `json:"season_id"`
	Subject  string    `json:"subject"`
	Phase    string    `json:"phase"`
	At       time.Time `json:"at"`
	Reason   string    `json:"reason"`
}

// Errors returned by the controller.
var (
	ErrInvalidConfig     = errors.New("controller limits are invalid")
	ErrSeasonNotFound    = errors.New("season not found")
	ErrPhaseNotFound     = errors.New("phase not found")
	ErrInvalidSeason     = errors.New("season id is required")
	ErrInvalidPhase      = errors.New("phase name is required")
	ErrPhaseExists       = errors.New("phase already exists in this season")
	ErrTimeRegression    = errors.New("phase time must move forward")
	ErrPhaseLimit        = errors.New("phase limit reached for this season")
	ErrSettlementLimit   = errors.New("settlement limit reached for this season")
	ErrInvalidSubject    = errors.New("subject is required")
	ErrInvalidValue      = errors.New("amount must not be negative")
	ErrBeforeSeasonStart = errors.New("write predates the season")
	ErrOutOfPhase        = errors.New("write is outside every phase window")
	ErrPhaseSettled      = errors.New("phase is already settled")
	ErrNotSettled        = errors.New("phase is not settled")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// SeasonPhaseController owns the phase table and the settlement records.
type SeasonPhaseController struct {
	mu             sync.Mutex
	maxPhases      int
	maxSettlements int
	clock          func() time.Time
	seasons        map[string]*season
	order          []string
}

// record is one accepted write kept with its arrival timestamp.
type record struct {
	Subject string    `json:"subject"`
	Amount  int64     `json:"amount"`
	Phase   string    `json:"phase"`
	At      time.Time `json:"at"`
}

// phaseState is the internal phase: committed boundaries plus the window
// that currently accepts writes (WindowFrom moves only on reopen).
type phaseState struct {
	SeasonID   string    `json:"season_id"`
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	Settled    bool      `json:"settled"`
	Reopened   bool      `json:"reopened"`
	WindowFrom time.Time `json:"window_from"`
}

// view projects the internal state into the caller facing Phase.
func (p *phaseState) view() Phase {
	return Phase{
		SeasonID:  p.SeasonID,
		Name:      p.Name,
		StartedAt: p.StartedAt,
		EndedAt:   p.EndedAt,
		Settled:   p.Settled,
		Reopened:  p.Reopened,
	}
}

// season is the per season state: phases, writes, settlements, rejections.
type season struct {
	id          string
	phases      []phaseState
	records     []record
	settlements []Settlement
	byPhase     map[string]int
	rejections  []Rejection
}

// addRejection appends one refused write, honouring the hard cap.
func (s *season) addRejection(r Rejection) {
	if len(s.rejections) >= maxRejections {
		return
	}
	s.rejections = append(s.rejections, r)
}

// snapshot is the serialised controller state.
type snapshot struct {
	MaxPhases      int              `json:"max_phases"`
	MaxSettlements int              `json:"max_settlements"`
	Seasons        []seasonSnapshot `json:"seasons"`
}

// seasonSnapshot is the serialised state of one season.
type seasonSnapshot struct {
	SeasonID    string       `json:"season_id"`
	Phases      []phaseState `json:"phases"`
	Records     []record     `json:"records"`
	Settlements []Settlement `json:"settlements"`
	Rejections  []Rejection  `json:"rejections"`
}

// NewSeasonPhaseController builds a controller with hard table caps.
func NewSeasonPhaseController(maxPhases, maxSettlements int) (*SeasonPhaseController, error) {
	if maxPhases <= 0 || maxSettlements <= 0 {
		return nil, ErrInvalidConfig
	}
	return &SeasonPhaseController{
		maxPhases:      maxPhases,
		maxSettlements: maxSettlements,
		clock:          time.Now,
		seasons:        make(map[string]*season),
	}, nil
}

// SetClock replaces the time source used while settling.
func (c *SeasonPhaseController) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	c.clock = now
}

// EnterPhase opens the next phase and closes the current one at at.
func (c *SeasonPhaseController) EnterPhase(seasonID, phase string, at time.Time) error {
	if seasonID == "" {
		return ErrInvalidSeason
	}
	if phase == "" {
		return ErrInvalidPhase
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if ok {
		for _, p := range s.phases {
			if p.Name == phase {
				return ErrPhaseExists
			}
		}
		last := s.phases[len(s.phases)-1]
		boundary := last.WindowFrom
		if last.EndedAt.After(boundary) {
			boundary = last.EndedAt
		}
		if !at.After(boundary) {
			return ErrTimeRegression
		}
		if len(s.phases) >= c.maxPhases {
			return ErrPhaseLimit
		}
	} else {
		s = &season{id: seasonID, byPhase: make(map[string]int)}
		c.seasons[seasonID] = s
		c.order = append(c.order, seasonID)
	}
	if len(s.phases) > 0 {
		last := &s.phases[len(s.phases)-1]
		if last.EndedAt.IsZero() {
			last.EndedAt = at
		}
	}
	s.phases = append(s.phases, phaseState{SeasonID: seasonID, Name: phase, StartedAt: at, WindowFrom: at})
	return nil
}

// Submit attributes a timestamped write to the phase window containing at.
func (c *SeasonPhaseController) Submit(seasonID, subject string, v Value, at time.Time) error {
	if subject == "" {
		return ErrInvalidSubject
	}
	if v.Amount < 0 {
		return ErrInvalidValue
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return ErrSeasonNotFound
	}
	if at.Before(s.phases[0].StartedAt) {
		s.addRejection(Rejection{SeasonID: seasonID, Subject: subject, At: at, Reason: ReasonBeforeStart})
		return ErrBeforeSeasonStart
	}
	for i := range s.phases {
		p := &s.phases[i]
		if p.Settled {
			if !at.Before(p.StartedAt) && (p.EndedAt.IsZero() || at.Before(p.EndedAt)) {
				s.addRejection(Rejection{SeasonID: seasonID, Subject: subject, Phase: p.Name, At: at, Reason: ReasonPhaseSettled})
				return ErrPhaseSettled
			}
			continue
		}
		if at.Before(p.WindowFrom) {
			continue
		}
		if !p.EndedAt.IsZero() && !at.Before(p.EndedAt) {
			continue
		}
		s.records = append(s.records, record{Subject: subject, Amount: v.Amount, Phase: p.Name, At: at})
		return nil
	}
	s.addRejection(Rejection{SeasonID: seasonID, Subject: subject, At: at, Reason: ReasonOutsidePhases})
	return ErrOutOfPhase
}

// Settle closes the current phase and produces exactly one settlement.
func (c *SeasonPhaseController) Settle(seasonID string) (Settlement, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return Settlement{}, ErrSeasonNotFound
	}
	cur := s.phases[len(s.phases)-1]
	if cur.Settled {
		dup := s.settlements[s.byPhase[cur.Name]]
		dup.Duplicate = true
		return dup, nil
	}
	if len(s.settlements) >= c.maxSettlements {
		return Settlement{}, ErrSettlementLimit
	}
	now := c.clock()
	sums := make(map[string]int64)
	for _, r := range s.records {
		if r.Phase != cur.Name {
			continue
		}
		if r.At.Before(cur.WindowFrom) {
			continue
		}
		if !cur.EndedAt.IsZero() && !r.At.Before(cur.EndedAt) {
			continue
		}
		sums[r.Subject] += r.Amount
	}
	subjects := make([]string, 0, len(sums))
	for subject := range sums {
		subjects = append(subjects, subject)
	}
	sort.Strings(subjects)
	entries := make([]Entry, 0, len(subjects))
	var total int64
	for _, subject := range subjects {
		entries = append(entries, Entry{Subject: subject, Amount: sums[subject], Phase: cur.Name})
		total += sums[subject]
	}
	settlement := Settlement{
		SeasonID:  seasonID,
		Phase:     cur.Name,
		SettledAt: now,
		Entries:   entries,
		Total:     total,
	}
	s.settlements = append(s.settlements, settlement)
	s.byPhase[cur.Name] = len(s.settlements) - 1
	last := &s.phases[len(s.phases)-1]
	last.Settled = true
	last.EndedAt = now
	return settlement, nil
}

// Reopen opens a fresh window after a settlement without touching rewards.
func (c *SeasonPhaseController) Reopen(seasonID string, at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return ErrSeasonNotFound
	}
	last := &s.phases[len(s.phases)-1]
	if !last.Settled {
		return ErrNotSettled
	}
	if at.Before(last.EndedAt) {
		return ErrTimeRegression
	}
	last.Settled = false
	last.Reopened = true
	last.WindowFrom = at
	last.EndedAt = time.Time{}
	return nil
}

// Status reports the current phase of one season.
func (c *SeasonPhaseController) Status(seasonID string) (Phase, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return Phase{}, ErrSeasonNotFound
	}
	return s.phases[len(s.phases)-1].view(), nil
}

// Boundaries lists every phase of one season in order.
func (c *SeasonPhaseController) Boundaries(seasonID string) ([]Phase, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return nil, ErrSeasonNotFound
	}
	phases := make([]Phase, len(s.phases))
	for i := range s.phases {
		phases[i] = s.phases[i].view()
	}
	return phases, nil
}

// PhaseTotal sums one subject inside one phase.
func (c *SeasonPhaseController) PhaseTotal(seasonID, phase, subject string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return 0, ErrSeasonNotFound
	}
	found := false
	for _, p := range s.phases {
		if p.Name == phase {
			found = true
			break
		}
	}
	if !found {
		return 0, ErrPhaseNotFound
	}
	var total int64
	for _, r := range s.records {
		if r.Phase == phase && r.Subject == subject {
			total += r.Amount
		}
	}
	return total, nil
}

// GrandTotal sums one subject across every phase of a season.
func (c *SeasonPhaseController) GrandTotal(seasonID, subject string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return 0, ErrSeasonNotFound
	}
	var total int64
	for _, r := range s.records {
		if r.Subject == subject {
			total += r.Amount
		}
	}
	return total, nil
}

// Rejections lists the refused writes of one season, oldest first.
func (c *SeasonPhaseController) Rejections(seasonID string) []Rejection {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seasons[seasonID]
	if !ok {
		return nil
	}
	rejections := make([]Rejection, len(s.rejections))
	copy(rejections, s.rejections)
	return rejections
}

// Snapshot serialises phases, windows, entries and settlements.
func (c *SeasonPhaseController) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := snapshot{
		MaxPhases:      c.maxPhases,
		MaxSettlements: c.maxSettlements,
		Seasons:        make([]seasonSnapshot, 0, len(c.order)),
	}
	for _, id := range c.order {
		s := c.seasons[id]
		snap.Seasons = append(snap.Seasons, seasonSnapshot{
			SeasonID:    s.id,
			Phases:      s.phases,
			Records:     s.records,
			Settlements: s.settlements,
			Rejections:  s.rejections,
		})
	}
	return json.Marshal(snap)
}

// Restore rebuilds the controller from a snapshot, all or nothing.
func (c *SeasonPhaseController) Restore(data []byte) error {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.MaxPhases <= 0 || snap.MaxSettlements <= 0 {
		return ErrBadSnapshot
	}
	seasons := make(map[string]*season, len(snap.Seasons))
	order := make([]string, 0, len(snap.Seasons))
	for _, ss := range snap.Seasons {
		if ss.SeasonID == "" || len(ss.Phases) == 0 {
			return ErrBadSnapshot
		}
		if _, dup := seasons[ss.SeasonID]; dup {
			return ErrBadSnapshot
		}
		s := &season{
			id:          ss.SeasonID,
			phases:      ss.Phases,
			records:     ss.Records,
			settlements: ss.Settlements,
			rejections:  ss.Rejections,
			byPhase:     make(map[string]int),
		}
		for i := range s.phases {
			if s.phases[i].WindowFrom.IsZero() {
				s.phases[i].WindowFrom = s.phases[i].StartedAt
			}
		}
		for i, st := range ss.Settlements {
			s.byPhase[st.Phase] = i
		}
		seasons[ss.SeasonID] = s
		order = append(order, ss.SeasonID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxPhases = snap.MaxPhases
	c.maxSettlements = snap.MaxSettlements
	c.seasons = seasons
	c.order = order
	return nil
}
