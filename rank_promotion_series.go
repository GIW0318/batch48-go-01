// Package task runs promotion series (best of three), books every game once
// and applies the season soft reset without stacking the same season twice.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// SeriesState is the lifecycle position of one promotion series.
type SeriesState string

// Series states.
const (
	SeriesRunning  SeriesState = "running"
	SeriesComplete SeriesState = "complete"
	SeriesResolved SeriesState = "resolved"
	SeriesAborted  SeriesState = "aborted"
)

// Outcome values written into Outcome.Result.
const (
	ResultPromoted = "promoted"
	ResultRetained = "retained"
	ResultDemoted  = "demoted"
)

// Series length and tier ladder bounds.
const (
	WinsToClose       = 2
	LossesToClose     = 2
	MaxGamesPerSeries = 3
	SoftResetStep     = 200
)

// TierLadder lists the tiers from the lowest to the highest.
var TierLadder = []string{"bronze", "silver", "gold", "platinum", "diamond"}

// Errors returned by the promotion series.
var (
	ErrUnknownTier         = errors.New("unknown tier")
	ErrPlayerNotRegistered = errors.New("player is not registered")
	ErrInvalidPlayerID     = errors.New("player id is required")
	ErrInvalidSeason       = errors.New("season id is required")
	ErrInvalidMatchID      = errors.New("match id is required")
	ErrInvalidReason       = errors.New("abort reason is required")
	ErrSeriesNotFound      = errors.New("series not found")
	ErrSeriesExists        = errors.New("player already has a running series")
	ErrSeriesClosed        = errors.New("series is already closed")
	ErrSeriesAborted       = errors.New("series was aborted")
	ErrSeriesIncomplete    = errors.New("series is not complete yet")
	ErrDuplicateMatch      = errors.New("match already recorded in this series")
	ErrAlreadyResolved     = errors.New("series was already resolved")
	ErrBadSnapshot         = errors.New("snapshot payload is invalid")
)

// GameRecord is one game booked into a series.
type GameRecord struct {
	Index   int    `json:"index"`
	MatchID string `json:"match_id"`
	Win     bool   `json:"win"`
	Forfeit bool   `json:"forfeit"`
}

// Series is the caller facing view of one promotion run.
type Series struct {
	ID       string      `json:"id"`
	PlayerID string      `json:"player_id"`
	SeasonID string      `json:"season_id"`
	State    SeriesState `json:"state"`
	Tier     string      `json:"tier"`
	Wins     int         `json:"wins"`
	Losses   int         `json:"losses"`
	Games    int         `json:"games"`
}

// Outcome is the settlement of a finished series.
type Outcome struct {
	SeriesID string `json:"series_id"`
	PlayerID string `json:"player_id"`
	SeasonID string `json:"season_id"`
	Result   string `json:"result"`
	Tier     string `json:"tier"`
	NewTier  string `json:"new_tier"`
	Wins     int    `json:"wins"`
	Losses   int    `json:"losses"`
}

// PromotionSeries owns every series, game record and player tier.
type PromotionSeries struct {
	mu         sync.Mutex
	nextSeries int
}

// NewPromotionSeries builds an empty promotion book.
func NewPromotionSeries() *PromotionSeries {
	return &PromotionSeries{}
}

// Register records a player at the baseline rating of the given tier.
func (s *PromotionSeries) Register(playerID, tier string) error {
	return errNotImplemented
}

// OpenSeries starts a best of three for a registered player and freezes the
// tier the series is played at.
func (s *PromotionSeries) OpenSeries(playerID, seasonID string) (Series, error) {
	return Series{}, errNotImplemented
}

// RecordGame books one game result, once per match id.
func (s *PromotionSeries) RecordGame(seriesID, matchID string, win bool) (Series, error) {
	return Series{}, errNotImplemented
}

// RecordDisconnect books a disconnect defeat for one match id.
func (s *PromotionSeries) RecordDisconnect(seriesID, matchID string) (Series, error) {
	return Series{}, errNotImplemented
}

// Resolve settles a complete series into a promotion, a retention or a
// demotion.
func (s *PromotionSeries) Resolve(seriesID string) (Outcome, error) {
	return Outcome{}, errNotImplemented
}

// Abort stops a running series without any tier change.
func (s *PromotionSeries) Abort(seriesID, reason string) error {
	return errNotImplemented
}

// SoftReset lowers the season scores by the soft reset step and reports how
// many players moved.
func (s *PromotionSeries) SoftReset(seasonID string, ratings map[string]int) (int, error) {
	return 0, errNotImplemented
}

// Series reads one series.
func (s *PromotionSeries) Series(seriesID string) (Series, bool) {
	return Series{}, false
}

// SeriesGames lists the booked games of one series in booking order.
func (s *PromotionSeries) SeriesGames(seriesID string) []GameRecord {
	return nil
}

// Tier reads the current tier of a player.
func (s *PromotionSeries) Tier(playerID string) string {
	return ""
}

// Rating reads the current rating of a player.
func (s *PromotionSeries) Rating(playerID string) int {
	return 0
}

// Snapshot serialises every series, game record, tier and season reset mark.
func (s *PromotionSeries) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole book with a snapshot, all or nothing.
func (s *PromotionSeries) Restore(data []byte) error {
	return errNotImplemented
}
