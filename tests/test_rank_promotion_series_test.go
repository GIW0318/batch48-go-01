package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-07"
)

func openTestSeries(t *testing.T, s *impl.PromotionSeries, playerID, tier string) impl.Series {
	t.Helper()
	if err := s.Register(playerID, tier); err != nil {
		t.Fatalf("register %s at %s failed: %v", playerID, tier, err)
	}
	series, err := s.OpenSeries(playerID, "season-1")
	if err != nil {
		t.Fatalf("open series for %s failed: %v", playerID, err)
	}
	return series
}

func TestRegisterValidatesTier(t *testing.T) {
	s := impl.NewPromotionSeries()
	if err := s.Register("p1", "mythic"); !errors.Is(err, impl.ErrUnknownTier) {
		t.Fatalf("register with an unknown tier = %v, want ErrUnknownTier", err)
	}
	if err := s.Register("p1", "gold"); err != nil {
		t.Fatalf("register at gold failed: %v", err)
	}
	if got := s.Tier("p1"); got != "gold" {
		t.Fatalf("registered tier = %q, want gold", got)
	}
}

func TestOpenSeriesFreezesTier(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if series.State != impl.SeriesRunning {
		t.Fatalf("new series state = %q, want %q", series.State, impl.SeriesRunning)
	}
	if series.Tier != "gold" {
		t.Fatalf("series tier = %q, want the opening tier gold", series.Tier)
	}
	if series.Wins != 0 || series.Losses != 0 || series.Games != 0 {
		t.Fatalf("new series starts with %d/%d in %d games, want a clean 0/0 in 0", series.Wins, series.Losses, series.Games)
	}
	if series.PlayerID != "p1" || series.SeasonID != "season-1" {
		t.Fatalf("series bound to %q/%q, want p1/season-1", series.PlayerID, series.SeasonID)
	}
}

func TestOpenSeriesRequiresRegisteredPlayer(t *testing.T) {
	s := impl.NewPromotionSeries()
	_, err := s.OpenSeries("ghost", "season-1")
	if !errors.Is(err, impl.ErrPlayerNotRegistered) {
		t.Fatalf("open series for an unregistered player = %v, want ErrPlayerNotRegistered", err)
	}
}

func TestRecordGameDeduplicatesMatch(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-7", true); err != nil {
		t.Fatalf("first record failed: %v", err)
	}
	_, err := s.RecordGame(series.ID, "match-7", true)
	if !errors.Is(err, impl.ErrDuplicateMatch) {
		t.Fatalf("duplicate match id = %v, want ErrDuplicateMatch", err)
	}
	got, ok := s.Series(series.ID)
	if !ok {
		t.Fatalf("series lookup failed")
	}
	if got.Wins != 1 || got.Games != 1 {
		t.Fatalf("after duplicate booking wins=%d games=%d, want 1/1", got.Wins, got.Games)
	}
}

func TestRecordGameCompletesAtTwoWins(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("first win failed: %v", err)
	}
	got, err := s.RecordGame(series.ID, "match-2", true)
	if err != nil {
		t.Fatalf("second win failed: %v", err)
	}
	if got.State != impl.SeriesComplete {
		t.Fatalf("series state after two wins = %q, want %q", got.State, impl.SeriesComplete)
	}
	if got.Wins != impl.WinsToClose || got.Games != 2 {
		t.Fatalf("series shows %d wins in %d games, want %d wins in 2", got.Wins, got.Games, impl.WinsToClose)
	}
}

func TestRecordGameRejectsClosedSeries(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("first win failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-2", true); err != nil {
		t.Fatalf("second win failed: %v", err)
	}
	_, err := s.RecordGame(series.ID, "match-3", true)
	if !errors.Is(err, impl.ErrSeriesClosed) {
		t.Fatalf("record on a closed series = %v, want ErrSeriesClosed", err)
	}
}

func TestResolvePromotesOnTwoWins(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("first win failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-2", true); err != nil {
		t.Fatalf("second win failed: %v", err)
	}
	out, err := s.Resolve(series.ID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if out.Result != impl.ResultPromoted {
		t.Fatalf("outcome = %q, want %q", out.Result, impl.ResultPromoted)
	}
	if out.Tier != "gold" || out.NewTier != "platinum" {
		t.Fatalf("outcome moved %q to %q, want gold to platinum", out.Tier, out.NewTier)
	}
	if got := s.Tier("p1"); got != "platinum" {
		t.Fatalf("player tier after promotion = %q, want platinum", got)
	}
}

func TestResolveRetainsAtOneWinTwoLosses(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("win failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-2", false); err != nil {
		t.Fatalf("first loss failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-3", false); err != nil {
		t.Fatalf("second loss failed: %v", err)
	}
	out, err := s.Resolve(series.ID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if out.Result != impl.ResultRetained {
		t.Fatalf("outcome = %q, want %q", out.Result, impl.ResultRetained)
	}
	if out.Wins != 1 || out.Losses != 2 {
		t.Fatalf("outcome recorded %d wins and %d losses, want 1 and 2", out.Wins, out.Losses)
	}
	if got := s.Tier("p1"); got != "gold" {
		t.Fatalf("player tier after retention = %q, want gold", got)
	}
}

func TestResolveDemotesOnTwoLosses(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", false); err != nil {
		t.Fatalf("first loss failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-2", false); err != nil {
		t.Fatalf("second loss failed: %v", err)
	}
	out, err := s.Resolve(series.ID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if out.Result != impl.ResultDemoted {
		t.Fatalf("outcome = %q, want %q", out.Result, impl.ResultDemoted)
	}
	if out.NewTier != "silver" {
		t.Fatalf("new tier after demotion = %q, want silver", out.NewTier)
	}
	if got := s.Tier("p1"); got != "silver" {
		t.Fatalf("player tier after demotion = %q, want silver", got)
	}
}

func TestResolveBeforeCompletionFails(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("win failed: %v", err)
	}
	if _, err := s.Resolve(series.ID); !errors.Is(err, impl.ErrSeriesIncomplete) {
		t.Fatalf("resolve of an incomplete series = %v, want ErrSeriesIncomplete", err)
	}
	if got := s.Tier("p1"); got != "gold" {
		t.Fatalf("tier moved to %q before the series completed", got)
	}
}

func TestResolveAppliesOnce(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("first win failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-2", true); err != nil {
		t.Fatalf("second win failed: %v", err)
	}
	if _, err := s.Resolve(series.ID); err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	if _, err := s.Resolve(series.ID); !errors.Is(err, impl.ErrAlreadyResolved) {
		t.Fatalf("second resolve = %v, want ErrAlreadyResolved", err)
	}
	if got := s.Tier("p1"); got != "platinum" {
		t.Fatalf("player tier after a repeated resolve = %q, want platinum without a second step", got)
	}
}

func TestAbortStopsSeries(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("win failed: %v", err)
	}
	if err := s.Abort(series.ID, "player swapped out"); err != nil {
		t.Fatalf("abort failed: %v", err)
	}
	got, ok := s.Series(series.ID)
	if !ok || got.State != impl.SeriesAborted {
		t.Fatalf("series state after abort = %q (found=%v), want %q", got.State, ok, impl.SeriesAborted)
	}
	if _, err := s.RecordGame(series.ID, "match-2", true); !errors.Is(err, impl.ErrSeriesAborted) {
		t.Fatalf("record after abort = %v, want ErrSeriesAborted", err)
	}
	if _, err := s.Resolve(series.ID); !errors.Is(err, impl.ErrSeriesAborted) {
		t.Fatalf("resolve after abort = %v, want ErrSeriesAborted", err)
	}
	if got := s.Tier("p1"); got != "gold" {
		t.Fatalf("tier after abort = %q, want gold unchanged", got)
	}
}

func TestAbortAfterCompletionRejected(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("first win failed: %v", err)
	}
	if _, err := s.RecordGame(series.ID, "match-2", true); err != nil {
		t.Fatalf("second win failed: %v", err)
	}
	err := s.Abort(series.ID, "late abort")
	if !errors.Is(err, impl.ErrSeriesClosed) {
		t.Fatalf("abort of a completed series = %v, want ErrSeriesClosed", err)
	}
}

func TestSoftResetDropsRatingAndTier(t *testing.T) {
	s := impl.NewPromotionSeries()
	if err := s.Register("p1", "diamond"); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	moved, err := s.SoftReset("season-2", map[string]int{"p1": 2200})
	if err != nil {
		t.Fatalf("soft reset failed: %v", err)
	}
	if moved != 1 {
		t.Fatalf("soft reset moved %d players, want 1", moved)
	}
	if got := s.Rating("p1"); got != 2200-impl.SoftResetStep {
		t.Fatalf("rating after soft reset = %d, want %d", got, 2200-impl.SoftResetStep)
	}
	if got := s.Tier("p1"); got != "platinum" {
		t.Fatalf("tier after soft reset = %q, want platinum", got)
	}
}

func TestSnapshotRestoreKeepsSeriesProgress(t *testing.T) {
	s := impl.NewPromotionSeries()
	series := openTestSeries(t, s, "p1", "gold")
	if _, err := s.RecordGame(series.ID, "match-1", true); err != nil {
		t.Fatalf("win failed: %v", err)
	}
	data, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := impl.NewPromotionSeries()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	got, ok := restored.Series(series.ID)
	if !ok {
		t.Fatalf("series missing after restore")
	}
	if got.Wins != 1 || got.State != impl.SeriesRunning {
		t.Fatalf("restored series shows %d wins in state %q, want 1 win still running", got.Wins, got.State)
	}
	games := restored.SeriesGames(series.ID)
	if len(games) != 1 || games[0].MatchID != "match-1" {
		t.Fatalf("restored game record = %+v, want the single match-1 booking", games)
	}
	if got := restored.Tier("p1"); got != "gold" {
		t.Fatalf("restored tier = %q, want gold", got)
	}
	if got := restored.Rating("p1"); got != 1400 {
		t.Fatalf("restored rating = %d, want the gold baseline 1400", got)
	}
}
