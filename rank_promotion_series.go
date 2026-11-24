// Package task runs promotion series (best of three), books every game once
// and applies the season soft reset without stacking the same season twice.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

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

// Hard caps for the shared ledger. They bound every growing collection so a
// runaway caller gets an error instead of silently overwriting entries.
const (
	// MaxSeriesLedger bounds the total number of series kept in the book.
	MaxSeriesLedger = 1000
	// MaxSeasonMarkers bounds the number of seasons carrying a reset mark.
	MaxSeasonMarkers = 64
)

// snapshotVersion gates the snapshot payload format accepted by Restore.
const snapshotVersion = 1

// TierLadder lists the tiers from the lowest to the highest.
var TierLadder = []string{"bronze", "silver", "gold", "platinum", "diamond"}

// tierBaseline is the opening rating written by Register for every tier.
// Gold is fixed at 1400; the rest of the ladder steps in even 400 point
// bands, which keeps 2000 inside platinum and 2200 at the diamond floor.
var tierBaseline = map[string]int{
	"bronze":   600,
	"silver":   1000,
	"gold":     1400,
	"platinum": 1800,
	"diamond":  2200,
}

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
	ErrLimitExceeded       = errors.New("hard limit exceeded")
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

// playerState is the current tier/rating pair of one registered player.
type playerState struct {
	tier   string
	rating int
}

// seriesState is the mutable ledger entry behind a Series view.
type seriesState struct {
	id          string
	playerID    string
	seasonID    string
	frozenTier  string
	state       SeriesState
	wins        int
	losses      int
	records     []GameRecord
	matches     map[string]struct{}
	abortReason string
}

// seasonReset marks that SoftReset already ran for one season.
type seasonReset struct {
	affected int
}

// PromotionSeries owns every series, game record and player tier.
type PromotionSeries struct {
	mu           sync.Mutex
	nextSeries   int
	players      map[string]*playerState
	seriesByID   map[string]*seriesState
	resetSeasons map[string]seasonReset
}

// NewPromotionSeries builds an empty promotion book.
func NewPromotionSeries() *PromotionSeries {
	s := &PromotionSeries{}
	s.initLocked()
	return s
}

// initLocked lazily provisions maps so a zero value stays usable. Callers
// must hold s.mu.
func (s *PromotionSeries) initLocked() {
	if s.nextSeries == 0 {
		s.nextSeries = 1
	}
	if s.players == nil {
		s.players = make(map[string]*playerState)
	}
	if s.seriesByID == nil {
		s.seriesByID = make(map[string]*seriesState)
	}
	if s.resetSeasons == nil {
		s.resetSeasons = make(map[string]seasonReset)
	}
}

// Register records a player at the baseline rating of the given tier.
func (s *PromotionSeries) Register(playerID, tier string) error {
	if playerID == "" {
		return ErrInvalidPlayerID
	}
	baseline, ok := tierBaseline[tier]
	if !ok {
		return ErrUnknownTier
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	player, ok := s.players[playerID]
	if !ok {
		player = &playerState{}
		s.players[playerID] = player
	}
	player.tier = tier
	player.rating = baseline
	return nil
}

// OpenSeries starts a best of three for a registered player and freezes the
// tier the series is played at.
func (s *PromotionSeries) OpenSeries(playerID, seasonID string) (Series, error) {
	if playerID == "" {
		return Series{}, ErrInvalidPlayerID
	}
	if seasonID == "" {
		return Series{}, ErrInvalidSeason
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	player, ok := s.players[playerID]
	if !ok {
		return Series{}, ErrPlayerNotRegistered
	}
	for _, candidate := range s.seriesByID {
		if candidate.playerID == playerID && candidate.state == SeriesRunning {
			return Series{}, ErrSeriesExists
		}
	}
	if len(s.seriesByID) >= MaxSeriesLedger {
		return Series{}, fmt.Errorf("%w: series ledger holds %d", ErrLimitExceeded, len(s.seriesByID))
	}

	id := fmt.Sprintf("series-%d", s.nextSeries)
	s.nextSeries++
	run := &seriesState{
		id:         id,
		playerID:   playerID,
		seasonID:   seasonID,
		frozenTier: player.tier,
		state:      SeriesRunning,
		matches:    make(map[string]struct{}),
	}
	s.seriesByID[id] = run
	return seriesView(run), nil
}

// RecordGame books one game result, once per match id.
func (s *PromotionSeries) RecordGame(seriesID, matchID string, win bool) (Series, error) {
	return s.bookGame(seriesID, matchID, win, false)
}

// RecordDisconnect books a disconnect defeat for one match id.
func (s *PromotionSeries) RecordDisconnect(seriesID, matchID string) (Series, error) {
	return s.bookGame(seriesID, matchID, false, true)
}

// bookGame is the single accounting entry for real games and forfeits.
func (s *PromotionSeries) bookGame(seriesID, matchID string, win, forfeit bool) (Series, error) {
	if matchID == "" {
		return Series{}, ErrInvalidMatchID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	run, ok := s.seriesByID[seriesID]
	if !ok {
		return Series{}, ErrSeriesNotFound
	}

	// Exactly-once accounting wins over lifecycle state: a retried match is
	// always the duplicate booking and can never move wins/losses again.
	if _, duplicated := run.matches[matchID]; duplicated {
		return seriesView(run), ErrDuplicateMatch
	}

	switch run.state {
	case SeriesAborted:
		return seriesView(run), ErrSeriesAborted
	case SeriesComplete, SeriesResolved:
		return seriesView(run), ErrSeriesClosed
	}
	if run.state != SeriesRunning {
		return seriesView(run), ErrSeriesClosed
	}
	if len(run.records) >= MaxGamesPerSeries {
		return seriesView(run), ErrSeriesClosed
	}

	record := GameRecord{
		Index:   len(run.records),
		MatchID: matchID,
		Win:     win,
		Forfeit: forfeit,
	}
	run.records = append(run.records, record)
	run.matches[matchID] = struct{}{}
	if win {
		run.wins++
	} else {
		run.losses++
	}
	if run.wins >= WinsToClose || run.losses >= LossesToClose {
		run.state = SeriesComplete
	}
	return seriesView(run), nil
}

// Resolve settles a complete series into a promotion, a retention or a
// demotion. The tier frozen at opening is the only tier used.
func (s *PromotionSeries) Resolve(seriesID string) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	run, ok := s.seriesByID[seriesID]
	if !ok {
		return Outcome{}, ErrSeriesNotFound
	}
	switch run.state {
	case SeriesAborted:
		return Outcome{}, ErrSeriesAborted
	case SeriesResolved:
		return Outcome{}, ErrAlreadyResolved
	case SeriesRunning:
		return Outcome{}, ErrSeriesIncomplete
	}
	if run.state != SeriesComplete {
		return Outcome{}, ErrSeriesIncomplete
	}

	oldTier := run.frozenTier
	newTier := oldTier
	result := ResultRetained
	switch {
	case run.wins >= WinsToClose:
		result = ResultPromoted
		newTier = moveTier(oldTier, 1)
	case run.wins == 1 && run.losses >= LossesToClose:
		result = ResultRetained
		newTier = oldTier
	case run.losses >= LossesToClose:
		result = ResultDemoted
		newTier = moveTier(oldTier, -1)
	default:
		return Outcome{}, ErrSeriesIncomplete
	}

	// Flip the state inside the same critical section that settles the tier,
	// so concurrent resolves can only observe one successful transition.
	run.state = SeriesResolved
	if player, ok := s.players[run.playerID]; ok {
		player.tier = newTier
		// Re-base the score on the settled tier so Tier and Rating never
		// disagree after settlement or restart.
		player.rating = tierBaseline[newTier]
	}

	return Outcome{
		SeriesID: run.id,
		PlayerID: run.playerID,
		SeasonID: run.seasonID,
		Result:   result,
		Tier:     oldTier,
		NewTier:  newTier,
		Wins:     run.wins,
		Losses:   run.losses,
	}, nil
}

// Abort stops a running series without any tier change.
func (s *PromotionSeries) Abort(seriesID, reason string) error {
	if reason == "" {
		return ErrInvalidReason
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	run, ok := s.seriesByID[seriesID]
	if !ok {
		return ErrSeriesNotFound
	}
	switch run.state {
	case SeriesRunning:
		run.state = SeriesAborted
		run.abortReason = reason
		return nil
	case SeriesAborted:
		return ErrSeriesAborted
	default:
		return ErrSeriesClosed
	}
}

// SoftReset lowers the season scores by the soft reset step and reports how
// many players moved. A season is applied at most once.
func (s *PromotionSeries) SoftReset(seasonID string, ratings map[string]int) (int, error) {
	if seasonID == "" {
		return 0, ErrInvalidSeason
	}
	for playerID := range ratings {
		if playerID == "" {
			return 0, ErrInvalidPlayerID
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	if _, alreadyDone := s.resetSeasons[seasonID]; alreadyDone {
		// Replaying the reset script for the same season never compresses
		// ratings a second time and affects no new players.
		return 0, nil
	}
	if len(s.resetSeasons) >= MaxSeasonMarkers {
		return 0, fmt.Errorf("%w: season markers hold %d", ErrLimitExceeded, len(s.resetSeasons))
	}

	for playerID, settlementRating := range ratings {
		newRating := settlementRating - SoftResetStep
		player, ok := s.players[playerID]
		if !ok {
			player = &playerState{}
			s.players[playerID] = player
		}
		player.rating = newRating
		player.tier = tierForRating(newRating)
	}
	s.resetSeasons[seasonID] = seasonReset{affected: len(ratings)}
	return len(ratings), nil
}

// Series reads one series.
func (s *PromotionSeries) Series(seriesID string) (Series, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	run, ok := s.seriesByID[seriesID]
	if !ok {
		return Series{}, false
	}
	return seriesView(run), true
}

// SeriesGames lists the booked games of one series in booking order.
func (s *PromotionSeries) SeriesGames(seriesID string) []GameRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	run, ok := s.seriesByID[seriesID]
	if !ok {
		return nil
	}
	games := make([]GameRecord, len(run.records))
	copy(games, run.records)
	return games
}

// Tier reads the current tier of a player.
func (s *PromotionSeries) Tier(playerID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	player, ok := s.players[playerID]
	if !ok {
		return ""
	}
	return player.tier
}

// Rating reads the current rating of a player.
func (s *PromotionSeries) Rating(playerID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	player, ok := s.players[playerID]
	if !ok {
		return 0
	}
	return player.rating
}

// seriesView builds the immutable caller facing view under the book lock.
func seriesView(run *seriesState) Series {
	return Series{
		ID:       run.id,
		PlayerID: run.playerID,
		SeasonID: run.seasonID,
		State:    run.state,
		Tier:     run.frozenTier,
		Wins:     run.wins,
		Losses:   run.losses,
		Games:    len(run.records),
	}
}

// tierForRating maps a rating onto the highest tier whose baseline it meets.
func tierForRating(rating int) string {
	tier := TierLadder[0]
	for _, candidate := range TierLadder {
		if baseline, ok := tierBaseline[candidate]; ok && rating >= baseline {
			tier = candidate
		}
	}
	return tier
}

// moveTier walks delta steps on the ladder and clamps at both ends.
func moveTier(tier string, delta int) string {
	index := 0
	for i, candidate := range TierLadder {
		if candidate == tier {
			index = i
			break
		}
	}
	index += delta
	if index < 0 {
		index = 0
	}
	if index >= len(TierLadder) {
		index = len(TierLadder) - 1
	}
	return TierLadder[index]
}

// snapshotFile is the versioned, self contained persistence payload.
type snapshotFile struct {
	Version      int              `json:"version"`
	NextSeries   int              `json:"next_series"`
	Players      []playerSnapshot `json:"players"`
	Series       []seriesSnapshot `json:"series"`
	ResetSeasons []seasonSnapshot `json:"reset_seasons"`
}

type playerSnapshot struct {
	ID     string `json:"id"`
	Tier   string `json:"tier"`
	Rating int    `json:"rating"`
}

type gameSnapshot struct {
	Index   int    `json:"index"`
	MatchID string `json:"match_id"`
	Win     bool   `json:"win"`
	Forfeit bool   `json:"forfeit"`
}

type seriesSnapshot struct {
	ID          string         `json:"id"`
	PlayerID    string         `json:"player_id"`
	SeasonID    string         `json:"season_id"`
	FrozenTier  string         `json:"frozen_tier"`
	State       SeriesState    `json:"state"`
	Wins        int            `json:"wins"`
	Losses      int            `json:"losses"`
	AbortReason string         `json:"abort_reason,omitempty"`
	Games       []gameSnapshot `json:"games"`
}

type seasonSnapshot struct {
	SeasonID string `json:"season_id"`
	Affected int    `json:"affected"`
}

// Snapshot serialises every series, game record, tier and season reset mark.
func (s *PromotionSeries) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	payload := snapshotFile{
		Version:    snapshotVersion,
		NextSeries: s.nextSeries,
	}

	for id, player := range s.players {
		payload.Players = append(payload.Players, playerSnapshot{
			ID:     id,
			Tier:   player.tier,
			Rating: player.rating,
		})
	}
	sort.Slice(payload.Players, func(i, j int) bool {
		return payload.Players[i].ID < payload.Players[j].ID
	})

	for id, run := range s.seriesByID {
		entry := seriesSnapshot{
			ID:          id,
			PlayerID:    run.playerID,
			SeasonID:    run.seasonID,
			FrozenTier:  run.frozenTier,
			State:       run.state,
			Wins:        run.wins,
			Losses:      run.losses,
			AbortReason: run.abortReason,
		}
		for _, record := range run.records {
			entry.Games = append(entry.Games, gameSnapshot{
				Index:   record.Index,
				MatchID: record.MatchID,
				Win:     record.Win,
				Forfeit: record.Forfeit,
			})
		}
		payload.Series = append(payload.Series, entry)
	}
	sort.Slice(payload.Series, func(i, j int) bool {
		return payload.Series[i].ID < payload.Series[j].ID
	})

	for season, mark := range s.resetSeasons {
		payload.ResetSeasons = append(payload.ResetSeasons, seasonSnapshot{
			SeasonID: season,
			Affected: mark.affected,
		})
	}
	sort.Slice(payload.ResetSeasons, func(i, j int) bool {
		return payload.ResetSeasons[i].SeasonID < payload.ResetSeasons[j].SeasonID
	})

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// Restore replaces the whole book with a snapshot, all or nothing.
func (s *PromotionSeries) Restore(data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("%w: empty payload", ErrBadSnapshot)
	}

	var payload snapshotFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(&payload); err != nil {
		return err
	}

	players := make(map[string]*playerState, len(payload.Players))
	for _, player := range payload.Players {
		players[player.ID] = &playerState{tier: player.Tier, rating: player.Rating}
	}

	seriesByID := make(map[string]*seriesState, len(payload.Series))
	for _, entry := range payload.Series {
		run := &seriesState{
			id:          entry.ID,
			playerID:    entry.PlayerID,
			seasonID:    entry.SeasonID,
			frozenTier:  entry.FrozenTier,
			state:       entry.State,
			wins:        entry.Wins,
			losses:      entry.Losses,
			abortReason: entry.AbortReason,
			matches:     make(map[string]struct{}, len(entry.Games)),
			records:     make([]GameRecord, 0, len(entry.Games)),
		}
		for _, game := range entry.Games {
			run.records = append(run.records, GameRecord{
				Index:   game.Index,
				MatchID: game.MatchID,
				Win:     game.Win,
				Forfeit: game.Forfeit,
			})
			run.matches[game.MatchID] = struct{}{}
		}
		seriesByID[entry.ID] = run
	}

	resetSeasons := make(map[string]seasonReset, len(payload.ResetSeasons))
	for _, mark := range payload.ResetSeasons {
		resetSeasons[mark.SeasonID] = seasonReset{affected: mark.Affected}
	}

	nextSeries := payload.NextSeries
	for id := range seriesByID {
		if suffix, ok := seriesSuffix(id); ok && suffix >= nextSeries {
			nextSeries = suffix + 1
		}
	}
	if nextSeries < 1 {
		nextSeries = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.players = players
	s.seriesByID = seriesByID
	s.resetSeasons = resetSeasons
	s.nextSeries = nextSeries
	return nil
}

// validateSnapshot fully checks a payload before any live state is replaced.
func validateSnapshot(payload *snapshotFile) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrBadSnapshot, fmt.Sprintf(format, args...))
	}

	if payload.Version != snapshotVersion {
		return bad("unsupported version %d", payload.Version)
	}
	if payload.NextSeries < 1 {
		return bad("next series counter must be positive")
	}
	if len(payload.Series) > MaxSeriesLedger {
		return bad("too many series: %d", len(payload.Series))
	}
	if len(payload.ResetSeasons) > MaxSeasonMarkers {
		return bad("too many season markers: %d", len(payload.ResetSeasons))
	}

	playerIDs := make(map[string]struct{}, len(payload.Players))
	for _, player := range payload.Players {
		if player.ID == "" {
			return bad("player with empty id")
		}
		if _, duplicated := playerIDs[player.ID]; duplicated {
			return bad("duplicate player %q", player.ID)
		}
		playerIDs[player.ID] = struct{}{}
		if _, known := tierBaseline[player.Tier]; !known {
			return bad("player %q has unknown tier %q", player.ID, player.Tier)
		}
		if tierForRating(player.Rating) != player.Tier {
			return bad("player %q tier %q does not match rating %d", player.ID, player.Tier, player.Rating)
		}
	}

	seriesIDs := make(map[string]struct{}, len(payload.Series))
	for _, entry := range payload.Series {
		if entry.ID == "" {
			return bad("series with empty id")
		}
		if _, duplicated := seriesIDs[entry.ID]; duplicated {
			return bad("duplicate series id %q", entry.ID)
		}
		seriesIDs[entry.ID] = struct{}{}
		if entry.PlayerID == "" || entry.SeasonID == "" {
			return bad("series %q missing player or season", entry.ID)
		}
		if _, known := playerIDs[entry.PlayerID]; !known {
			return bad("series %q references missing player %q", entry.ID, entry.PlayerID)
		}
		if _, known := tierBaseline[entry.FrozenTier]; !known {
			return bad("series %q has unknown frozen tier %q", entry.ID, entry.FrozenTier)
		}
		switch entry.State {
		case SeriesRunning, SeriesComplete, SeriesResolved, SeriesAborted:
		default:
			return bad("series %q has unknown state %q", entry.ID, entry.State)
		}
		if entry.Wins < 0 || entry.Wins > WinsToClose || entry.Losses < 0 || entry.Losses > LossesToClose {
			return bad("series %q has impossible score %d-%d", entry.ID, entry.Wins, entry.Losses)
		}
		if len(entry.Games) > MaxGamesPerSeries {
			return bad("series %q has %d games over the cap", entry.ID, len(entry.Games))
		}

		matchIDs := make(map[string]struct{}, len(entry.Games))
		wins, losses := 0, 0
		for i, game := range entry.Games {
			if game.Index != i {
				return bad("series %q game %d has index %d", entry.ID, i, game.Index)
			}
			if game.MatchID == "" {
				return bad("series %q game %d has empty match id", entry.ID, i)
			}
			if _, duplicated := matchIDs[game.MatchID]; duplicated {
				return bad("series %q repeats match %q", entry.ID, game.MatchID)
			}
			matchIDs[game.MatchID] = struct{}{}
			if game.Forfeit && game.Win {
				return bad("series %q match %q is both forfeit and win", entry.ID, game.MatchID)
			}
			if game.Win {
				wins++
			} else {
				losses++
			}
		}
		if wins != entry.Wins || losses != entry.Losses || len(entry.Games) != entry.Wins+entry.Losses {
			return bad("series %q score %d-%d does not match %d games", entry.ID, entry.Wins, entry.Losses, len(entry.Games))
		}

		closed := entry.Wins >= WinsToClose || entry.Losses >= LossesToClose
		switch entry.State {
		case SeriesRunning:
			if closed {
				return bad("series %q is running at the close threshold", entry.ID)
			}
		case SeriesComplete, SeriesResolved:
			if !closed {
				return bad("series %q is %s before the close threshold", entry.ID, entry.State)
			}
		}
	}

	seasons := make(map[string]struct{}, len(payload.ResetSeasons))
	for _, mark := range payload.ResetSeasons {
		if mark.SeasonID == "" {
			return bad("season marker with empty id")
		}
		if _, duplicated := seasons[mark.SeasonID]; duplicated {
			return bad("duplicate season marker %q", mark.SeasonID)
		}
		seasons[mark.SeasonID] = struct{}{}
		if mark.Affected < 0 {
			return bad("season %q has negative affected count", mark.SeasonID)
		}
	}
	return nil
}

// seriesSuffix extracts the numeric counter from a generated series id.
func seriesSuffix(id string) (int, bool) {
	const prefix = "series-"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil || value < 1 {
		return 0, false
	}
	return value, true
}
