// Package task drives season phases: it attributes timestamped writes to
// the phase whose interval contains them and settles a season once.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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
}

// NewSeasonPhaseController builds a controller with hard table caps.
func NewSeasonPhaseController(maxPhases, maxSettlements int) (*SeasonPhaseController, error) {
	return nil, errNotImplemented
}

// SetClock replaces the time source used while settling.
func (c *SeasonPhaseController) SetClock(now func() time.Time) {
}

// EnterPhase opens the next phase and closes the current one at at.
func (c *SeasonPhaseController) EnterPhase(seasonID, phase string, at time.Time) error {
	return errNotImplemented
}

// Submit attributes a timestamped write to the phase window containing at.
func (c *SeasonPhaseController) Submit(seasonID, subject string, v Value, at time.Time) error {
	return errNotImplemented
}

// Settle closes the current phase and produces exactly one settlement.
func (c *SeasonPhaseController) Settle(seasonID string) (Settlement, error) {
	return Settlement{}, errNotImplemented
}

// Reopen opens a fresh window after a settlement without touching rewards.
func (c *SeasonPhaseController) Reopen(seasonID string, at time.Time) error {
	return errNotImplemented
}

// Status reports the current phase of one season.
func (c *SeasonPhaseController) Status(seasonID string) (Phase, error) {
	return Phase{}, errNotImplemented
}

// Boundaries lists every phase of one season in order.
func (c *SeasonPhaseController) Boundaries(seasonID string) ([]Phase, error) {
	return nil, errNotImplemented
}

// PhaseTotal sums one subject inside one phase.
func (c *SeasonPhaseController) PhaseTotal(seasonID, phase, subject string) (int64, error) {
	return 0, errNotImplemented
}

// GrandTotal sums one subject across every phase of a season.
func (c *SeasonPhaseController) GrandTotal(seasonID, subject string) (int64, error) {
	return 0, errNotImplemented
}

// Rejections lists the refused writes of one season, oldest first.
func (c *SeasonPhaseController) Rejections(seasonID string) []Rejection {
	return nil
}

// Snapshot serialises phases, windows, entries and settlements.
func (c *SeasonPhaseController) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the controller from a snapshot, all or nothing.
func (c *SeasonPhaseController) Restore(data []byte) error {
	return errNotImplemented
}
