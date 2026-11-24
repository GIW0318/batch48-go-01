package task

import (
	"errors"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-06"
)

var holdBase = time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)

type holdClock struct {
	now time.Time
}

func (c *holdClock) Now() time.Time {
	return c.now
}

func (c *holdClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestJoinGivesActiveSeat(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	seat, err := g.Join("room-1", "p1", 2)
	if err != nil {
		t.Fatalf("join returned error: %v", err)
	}
	if seat.State != impl.SeatActive {
		t.Fatalf("joined seat state = %q, want %q", seat.State, impl.SeatActive)
	}
	if seat.SeatIndex != 2 {
		t.Fatalf("joined seat index = %d, want 2", seat.SeatIndex)
	}
	got, ok := g.Seat("room-1", "p1")
	if !ok {
		t.Fatalf("seat lookup failed for p1")
	}
	if got.PlayerID != "p1" || got.RoomID != "room-1" {
		t.Fatalf("lookup returned player %q in room %q, want p1 in room-1", got.PlayerID, got.RoomID)
	}
}

func TestJoinRejectsDuplicatePlayer(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	if _, err := g.Join("room-1", "p1", 0); err != nil {
		t.Fatalf("first join failed: %v", err)
	}
	_, err := g.Join("room-1", "p1", 1)
	if !errors.Is(err, impl.ErrAlreadySeated) {
		t.Fatalf("second join error = %v, want ErrAlreadySeated", err)
	}
}

func TestJoinRejectsTakenSeat(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	if _, err := g.Join("room-1", "p1", 3); err != nil {
		t.Fatalf("first join failed: %v", err)
	}
	_, err := g.Join("room-1", "p2", 3)
	if !errors.Is(err, impl.ErrSeatTaken) {
		t.Fatalf("join on an occupied seat = %v, want ErrSeatTaken", err)
	}
}

func TestJoinRejectsOutOfRangeSeat(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	if _, err := g.Join("room-1", "p1", -1); !errors.Is(err, impl.ErrInvalidSeat) {
		t.Fatalf("negative seat index error = %v, want ErrInvalidSeat", err)
	}
	if _, err := g.Join("room-1", "p1", impl.MaxSeatIndex+1); !errors.Is(err, impl.ErrInvalidSeat) {
		t.Fatalf("seat index above the boundary error = %v, want ErrInvalidSeat", err)
	}
}

func TestDropHoldsSeatUntilWindowEnds(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 0); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	hold, err := g.OnDrop("room-1", "p1")
	if err != nil {
		t.Fatalf("drop returned error: %v", err)
	}
	if want := holdBase.Add(30 * time.Second); !hold.HeldUntil.Equal(want) {
		t.Fatalf("hold deadline = %v, want %v", hold.HeldUntil, want)
	}
	if hold.PlayerID != "p1" || hold.SeatIndex != 0 {
		t.Fatalf("hold describes %q seat %d, want p1 seat 0", hold.PlayerID, hold.SeatIndex)
	}
	seat, ok := g.Seat("room-1", "p1")
	if !ok || seat.State != impl.SeatHeld {
		t.Fatalf("seat state after drop = %q (found=%v), want %q", seat.State, ok, impl.SeatHeld)
	}
}

func TestDropUnknownPlayerRejected(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	_, err := g.OnDrop("room-1", "ghost")
	if !errors.Is(err, impl.ErrPlayerNotFound) {
		t.Fatalf("drop of an unseated player = %v, want ErrPlayerNotFound", err)
	}
}

func TestIssueResumeUsesOwnTTL(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(60*time.Second, 10*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 1); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := g.OnDrop("room-1", "p1"); err != nil {
		t.Fatalf("drop failed: %v", err)
	}
	tok, err := g.IssueResume("room-1", "p1")
	if err != nil {
		t.Fatalf("issue resume returned error: %v", err)
	}
	if tok.Value == "" {
		t.Fatalf("resume token value must not be empty")
	}
	if tok.RoomID != "room-1" || tok.PlayerID != "p1" {
		t.Fatalf("token bound to %q/%q, want room-1/p1", tok.RoomID, tok.PlayerID)
	}
	if want := holdBase.Add(10 * time.Second); !tok.ExpiresAt.Equal(want) {
		t.Fatalf("token expiry = %v, want %v from the token ttl, not the hold window", tok.ExpiresAt, want)
	}
}

func TestIssueResumeRequiresHeldSeat(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	if _, err := g.Join("room-1", "p1", 1); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	_, err := g.IssueResume("room-1", "p1")
	if !errors.Is(err, impl.ErrSeatNotHeld) {
		t.Fatalf("issue resume on an active seat = %v, want ErrSeatNotHeld", err)
	}
}

func TestResumeIsSingleUse(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(60*time.Second, 30*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 4); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := g.OnDrop("room-1", "p1"); err != nil {
		t.Fatalf("drop failed: %v", err)
	}
	tok, err := g.IssueResume("room-1", "p1")
	if err != nil {
		t.Fatalf("issue resume failed: %v", err)
	}
	seat, err := g.Resume(tok.Value)
	if err != nil {
		t.Fatalf("resume returned error: %v", err)
	}
	if seat.State != impl.SeatActive || seat.SeatIndex != 4 {
		t.Fatalf("resumed seat = %+v, want active seat 4", seat)
	}
	if _, err := g.Resume(tok.Value); !errors.Is(err, impl.ErrTokenUsed) {
		t.Fatalf("second resume with the same token = %v, want ErrTokenUsed", err)
	}
}

func TestResumeUnknownTokenRejected(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	_, err := g.Resume("made-up-token")
	if !errors.Is(err, impl.ErrTokenNotFound) {
		t.Fatalf("resume with an unknown token = %v, want ErrTokenNotFound", err)
	}
}

func TestResumeExpiredTokenKeepsSeatHeld(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(60*time.Second, 10*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 2); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := g.OnDrop("room-1", "p1"); err != nil {
		t.Fatalf("drop failed: %v", err)
	}
	tok, err := g.IssueResume("room-1", "p1")
	if err != nil {
		t.Fatalf("issue resume failed: %v", err)
	}
	clk.Advance(15 * time.Second)
	if _, err := g.Resume(tok.Value); !errors.Is(err, impl.ErrTokenExpired) {
		t.Fatalf("resume after the token ttl = %v, want ErrTokenExpired", err)
	}
	held := g.HeldSeats()
	if len(held) != 1 {
		t.Fatalf("held seats after token expiry = %d, want the seat still held", len(held))
	}
	if held[0].PlayerID != "p1" {
		t.Fatalf("held seat belongs to %q, want p1", held[0].PlayerID)
	}
}

func TestExpireAtDeadlineReleasesOnce(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 0); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := g.OnDrop("room-1", "p1"); err != nil {
		t.Fatalf("drop failed: %v", err)
	}
	released := g.Expire(holdBase.Add(30 * time.Second))
	if len(released) != 1 {
		t.Fatalf("released seats at the deadline = %d, want 1", len(released))
	}
	if released[0].State != impl.SeatReleased || released[0].PlayerID != "p1" {
		t.Fatalf("released seat = %+v, want p1 in the released state", released[0])
	}
	if again := g.Expire(holdBase.Add(30 * time.Second)); len(again) != 0 {
		t.Fatalf("second expire reported %d seats, want no repeated release", len(again))
	}
}

func TestExpireBeforeDeadlineIsNoop(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 0); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := g.OnDrop("room-1", "p1"); err != nil {
		t.Fatalf("drop failed: %v", err)
	}
	if released := g.Expire(holdBase.Add(29 * time.Second)); len(released) != 0 {
		t.Fatalf("expire before the deadline released %d seats, want 0", len(released))
	}
	if held := g.HeldSeats(); len(held) != 1 {
		t.Fatalf("held seats before the deadline = %d, want 1", len(held))
	}
}

func TestSnapshotRestoreKeepsHoldDeadline(t *testing.T) {
	clk := &holdClock{now: holdBase}
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	g.SetClock(clk.Now)
	if _, err := g.Join("room-1", "p1", 1); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if _, err := g.OnDrop("room-1", "p1"); err != nil {
		t.Fatalf("drop failed: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	clk2 := &holdClock{now: holdBase}
	restored := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	restored.SetClock(clk2.Now)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	held := restored.HeldSeats()
	if len(held) != 1 {
		t.Fatalf("restored held seats = %d, want 1", len(held))
	}
	if want := holdBase.Add(30 * time.Second); !held[0].HeldUntil.Equal(want) {
		t.Fatalf("restored hold deadline = %v, want %v", held[0].HeldUntil, want)
	}
	clk2.Advance(30 * time.Second)
	if released := restored.Expire(clk2.Now()); len(released) != 1 {
		t.Fatalf("expire after restore released %d seats, want 1", len(released))
	}
}

func TestRestoreRejectsBadSnapshot(t *testing.T) {
	g := impl.NewReconnectSeatGuard(30*time.Second, 10*time.Second)
	err := g.Restore([]byte("not a snapshot"))
	if !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore of a corrupt snapshot = %v, want ErrBadSnapshot", err)
	}
}
