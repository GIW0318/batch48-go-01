package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-09"
)

var backfillBase = time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC)

func newBackfillGuard(pool int, clk func() time.Time) *impl.BackfillGuard {
	g := impl.NewBackfillGuard(pool, 2*time.Minute, 45*time.Second)
	g.SetClock(clk)
	return g
}

func fixedBackfillClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func openBackfillMatch(t *testing.T, g *impl.BackfillGuard, matchID, region string) {
	t.Helper()
	if err := g.OpenMatch(matchID, region, 8, 10*time.Minute); err != nil {
		t.Fatalf("open match %s failed: %v", matchID, err)
	}
}

func TestRegisterAddsIdleCandidate(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	if err := g.Register("c1", "eu-west", 1520, 38); err != nil {
		t.Fatalf("register returned error: %v", err)
	}
	got, ok := g.Candidate("c1")
	if !ok {
		t.Fatalf("candidate lookup failed after register")
	}
	if got.State != impl.CandidateIdle {
		t.Fatalf("registered candidate state = %q, want %q", got.State, impl.CandidateIdle)
	}
	if got.Region != "eu-west" || got.Rating != 1520 || got.LatencyMS != 38 {
		t.Fatalf("registered candidate = %+v, want eu-west/1520/38", got)
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	if err := g.Register("c1", "eu-west", 1520, 38); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	err := g.Register("c1", "eu-west", 1600, 20)
	if !errors.Is(err, impl.ErrCandidateExists) {
		t.Fatalf("duplicate register = %v, want ErrCandidateExists", err)
	}
}

func TestRegisterRejectsInvalidFields(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	if err := g.Register("", "eu-west", 1500, 30); !errors.Is(err, impl.ErrInvalidCandidateID) {
		t.Fatalf("blank candidate id = %v, want ErrInvalidCandidateID", err)
	}
	if err := g.Register("c1", " ", 1500, 30); !errors.Is(err, impl.ErrInvalidRegion) {
		t.Fatalf("blank region = %v, want ErrInvalidRegion", err)
	}
	if err := g.Register("c1", "eu-west", -1, 30); !errors.Is(err, impl.ErrInvalidRating) {
		t.Fatalf("negative rating = %v, want ErrInvalidRating", err)
	}
	if err := g.Register("c1", "eu-west", 1500, 0); !errors.Is(err, impl.ErrInvalidLatency) {
		t.Fatalf("zero latency = %v, want ErrInvalidLatency", err)
	}
}

func TestRegisterRejectsPoolOverflow(t *testing.T) {
	g := newBackfillGuard(2, fixedBackfillClock(backfillBase))
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := g.Register("c2", "eu-west", 1500, 30); err != nil {
		t.Fatalf("second register failed: %v", err)
	}
	if err := g.Register("c3", "eu-west", 1500, 30); !errors.Is(err, impl.ErrPoolFull) {
		t.Fatalf("register beyond the pool cap = %v, want ErrPoolFull", err)
	}
}

func TestPickOrdersByLatencyThenRating(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	for _, c := range []struct {
		id      string
		rating  int
		latency int
	}{
		{"slow-strong", 1900, 40},
		{"fast-weak", 1200, 30},
		{"fast-strong", 1800, 30},
	} {
		if err := g.Register(c.id, "eu-west", c.rating, c.latency); err != nil {
			t.Fatalf("register %s failed: %v", c.id, err)
		}
	}
	got, err := g.Pick("match-1", 3)
	if err != nil {
		t.Fatalf("pick returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("picked %d candidates, want 3", len(got))
	}
	want := []string{"fast-strong", "fast-weak", "slow-strong"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("pick order[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestPickRejectsNonPositiveNeed(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if _, err := g.Pick("match-1", 0); !errors.Is(err, impl.ErrInvalidNeed) {
		t.Fatalf("pick with need zero = %v, want ErrInvalidNeed", err)
	}
	if _, err := g.Pick("match-1", -2); !errors.Is(err, impl.ErrInvalidNeed) {
		t.Fatalf("pick with a negative need = %v, want ErrInvalidNeed", err)
	}
}

func TestPickReturnsOnlySameRegion(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if err := g.Register("eu-1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := g.Register("ap-1", "ap-south", 1500, 10); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	got, err := g.Pick("match-1", 2)
	if err != nil {
		t.Fatalf("pick returned error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "eu-1" {
		t.Fatalf("pick returned %+v, want only the eu-west candidate", got)
	}
}

func TestPickRequiresOpenMatch(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if _, err := g.Pick("match-404", 1); !errors.Is(err, impl.ErrMatchUnknown) {
		t.Fatalf("pick on an unopened match = %v, want ErrMatchUnknown", err)
	}
	openBackfillMatch(t, g, "match-1", "eu-west")
	if got := g.Remaining("match-1"); got != 10*time.Minute {
		t.Fatalf("remaining time = %v, want 10m", got)
	}
}

func TestClaimMarksCandidateUnavailable(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	openBackfillMatch(t, g, "match-2", "eu-west")
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	ok, err := g.Claim("match-1", "c1")
	if err != nil || !ok {
		t.Fatalf("claim = %v (err=%v), want a win", ok, err)
	}
	got, _ := g.Candidate("c1")
	if got.State != impl.CandidateClaimed || got.MatchID != "match-1" {
		t.Fatalf("claimed candidate = %+v, want claimed by match-1", got)
	}
	other, err := g.Pick("match-2", 1)
	if err != nil {
		t.Fatalf("pick on the second match failed: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("a claimed candidate is still offered to another match: %+v", other)
	}
	if claimed := g.Claimed("match-1"); len(claimed) != 1 || claimed[0].ID != "c1" {
		t.Fatalf("claimed list = %+v, want the single c1 claim", claimed)
	}
}

func TestClaimHasSingleWinner(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]bool, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = g.Claim("match-1", "c1")
		}(i)
	}
	wg.Wait()
	wins := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("claim %d returned error: %v", i, errs[i])
		}
		if results[i] {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent claims produced %d winners, want exactly 1", wins)
	}
}

func TestClaimUnknownCandidate(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if _, err := g.Claim("match-1", "ghost"); !errors.Is(err, impl.ErrCandidateUnknown) {
		t.Fatalf("claim of an unknown candidate = %v, want ErrCandidateUnknown", err)
	}
}

func TestAttachRequiresClaim(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	err := g.Attach("match-1", "c1", "seat-3")
	if !errors.Is(err, impl.ErrNotClaimed) {
		t.Fatalf("attach without a claim = %v, want ErrNotClaimed", err)
	}
}

func TestAttachBindsSeatAndRejectsDuplicateSeat(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	for _, id := range []string{"c1", "c2"} {
		if err := g.Register(id, "eu-west", 1500, 30); err != nil {
			t.Fatalf("register %s failed: %v", id, err)
		}
		if ok, err := g.Claim("match-1", id); err != nil || !ok {
			t.Fatalf("claim %s = %v (err=%v), want a win", id, ok, err)
		}
	}
	if err := g.Attach("match-1", "c1", "seat-3"); err != nil {
		t.Fatalf("attach c1 failed: %v", err)
	}
	if err := g.Attach("match-1", "c2", "seat-3"); !errors.Is(err, impl.ErrSeatTaken) {
		t.Fatalf("attach onto an occupied seat = %v, want ErrSeatTaken", err)
	}
	seats := g.Seats("match-1")
	if len(seats) != 1 || seats["seat-3"] != "c1" {
		t.Fatalf("seat table = %+v, want seat-3 bound to c1", seats)
	}
	got, _ := g.Candidate("c1")
	if got.State != impl.CandidateAttached || got.SeatID != "seat-3" {
		t.Fatalf("attached candidate = %+v, want attached to seat-3", got)
	}
}

func TestAbortReturnsCandidateToPool(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if ok, err := g.Claim("match-1", "c1"); err != nil || !ok {
		t.Fatalf("claim = %v (err=%v), want a win", ok, err)
	}
	if err := g.Abort("match-1", "c1"); err != nil {
		t.Fatalf("abort failed: %v", err)
	}
	got, _ := g.Candidate("c1")
	if got.State != impl.CandidateIdle || got.MatchID != "" {
		t.Fatalf("candidate after abort = %+v, want back in the idle pool", got)
	}
	again, err := g.Pick("match-1", 1)
	if err != nil {
		t.Fatalf("pick after abort failed: %v", err)
	}
	if len(again) != 1 || again[0].ID != "c1" {
		t.Fatalf("pick after abort = %+v, want c1 offered again", again)
	}
}

func TestSnapshotRestoreKeepsClaimedCandidate(t *testing.T) {
	g := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	openBackfillMatch(t, g, "match-1", "eu-west")
	if err := g.Register("c1", "eu-west", 1500, 30); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if ok, err := g.Claim("match-1", "c1"); err != nil || !ok {
		t.Fatalf("claim = %v (err=%v), want a win", ok, err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := newBackfillGuard(8, fixedBackfillClock(backfillBase))
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	got, ok := restored.Candidate("c1")
	if !ok {
		t.Fatalf("candidate missing after restore")
	}
	if got.State != impl.CandidateClaimed || got.MatchID != "match-1" {
		t.Fatalf("restored candidate = %+v, want the claim restored", got)
	}
	offered, err := restored.Pick("match-1", 1)
	if err != nil {
		t.Fatalf("pick after restore failed: %v", err)
	}
	if len(offered) != 0 {
		t.Fatalf("restored claim was offered again: %+v", offered)
	}
}
