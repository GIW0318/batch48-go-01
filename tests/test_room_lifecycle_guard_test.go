package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-01"
)

func TestCreateRoomSeatsOwnerAndValidatesSeats(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(4)
	room, err := g.Create("owner-1", 4)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if room.ID == "" {
		t.Fatalf("room id must not be empty")
	}
	if room.OwnerID != "owner-1" {
		t.Fatalf("room owner = %q, want owner-1", room.OwnerID)
	}
	if room.SeatCount != 4 {
		t.Fatalf("room seat count = %d, want 4", room.SeatCount)
	}
	if room.State != impl.RoomWaiting {
		t.Fatalf("fresh room state = %q, want %q", room.State, impl.RoomWaiting)
	}
	seats := g.Seats(room.ID)
	if len(seats) != 4 {
		t.Fatalf("expected 4 seats, got %d", len(seats))
	}
	if seats[0].Index != 0 || seats[0].PlayerID != "owner-1" {
		t.Fatalf("seat 0 = %+v, want the owner in index 0", seats[0])
	}
	if seats[0].Reserved {
		t.Fatalf("the owner seat must not be a reservation")
	}
	for i := 1; i < 4; i++ {
		if seats[i].PlayerID != "" {
			t.Fatalf("seat %d must stay free before anyone joins, got %q", i, seats[i].PlayerID)
		}
	}
	if _, err := g.Create("", 4); !errors.Is(err, impl.ErrInvalidOwner) {
		t.Fatalf("empty owner must be rejected with ErrInvalidOwner, got %v", err)
	}
	if _, err := g.Create("owner-2", impl.MinSeatsPerRoom-1); !errors.Is(err, impl.ErrInvalidSeats) {
		t.Fatalf("too few seats must be rejected with ErrInvalidSeats, got %v", err)
	}
	if _, err := g.Create("owner-2", impl.MaxSeatsPerRoom+1); !errors.Is(err, impl.ErrInvalidSeats) {
		t.Fatalf("too many seats must be rejected with ErrInvalidSeats, got %v", err)
	}
}

func TestCreateStopsAtRoomLimit(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	if _, err := g.Create("owner-1", 2); err != nil {
		t.Fatalf("first room must succeed, got %v", err)
	}
	if _, err := g.Create("owner-2", 2); err != nil {
		t.Fatalf("second room must succeed, got %v", err)
	}
	if _, err := g.Create("owner-3", 2); !errors.Is(err, impl.ErrRoomLimitReached) {
		t.Fatalf("third room must be rejected with ErrRoomLimitReached, got %v", err)
	}
}

func TestDestroyFreesRoomSlot(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(1)
	room, err := g.Create("owner-1", 2)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if err := g.Destroy(room.ID); err != nil {
		t.Fatalf("destroy must succeed, got %v", err)
	}
	if _, ok := g.Room(room.ID); ok {
		t.Fatalf("destroyed room must not be readable any more")
	}
	if _, err := g.Create("owner-2", 2); err != nil {
		t.Fatalf("destroy must free the room slot, got %v", err)
	}
	if err := g.Destroy("room-missing"); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("destroying an unknown room must report ErrRoomNotFound, got %v", err)
	}
}

func TestJoinAssignsLowestFreeSeatAndRejectsDuplicates(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 4)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	second, err := g.Join(room.ID, "player-2")
	if err != nil {
		t.Fatalf("first join must succeed, got %v", err)
	}
	if second.Index != 1 {
		t.Fatalf("first join landed on seat %d, want the lowest free seat 1", second.Index)
	}
	if second.PlayerID != "player-2" || second.Reserved || second.Ready {
		t.Fatalf("joined seat = %+v, want a live seat for player-2", second)
	}
	third, err := g.Join(room.ID, "player-3")
	if err != nil {
		t.Fatalf("second join must succeed, got %v", err)
	}
	if third.Index != 2 {
		t.Fatalf("second join landed on seat %d, want 2", third.Index)
	}
	if _, err := g.Join(room.ID, "player-2"); !errors.Is(err, impl.ErrAlreadyJoined) {
		t.Fatalf("double join must report ErrAlreadyJoined, got %v", err)
	}
	if _, err := g.Join("room-missing", "player-9"); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("joining an unknown room must report ErrRoomNotFound, got %v", err)
	}
	if _, err := g.Join(room.ID, ""); !errors.Is(err, impl.ErrInvalidPlayer) {
		t.Fatalf("empty player must report ErrInvalidPlayer, got %v", err)
	}
	view, ok := g.Room(room.ID)
	if !ok {
		t.Fatalf("room must stay readable")
	}
	want := []string{"owner-1", "player-2", "player-3"}
	if len(view.Players) != len(want) {
		t.Fatalf("join order = %v, want %v", view.Players, want)
	}
	for i := range want {
		if view.Players[i] != want[i] {
			t.Fatalf("join order = %v, want %v", view.Players, want)
		}
	}
}

func TestJoinRejectsWhenEverySeatIsTaken(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 2)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-2"); err != nil {
		t.Fatalf("joining the last free seat must succeed, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-3"); !errors.Is(err, impl.ErrRoomFull) {
		t.Fatalf("joining a full room must report ErrRoomFull, got %v", err)
	}
	seats := g.Seats(room.ID)
	if len(seats) != 2 {
		t.Fatalf("expected 2 seats, got %d", len(seats))
	}
	for i := range seats {
		if seats[i].PlayerID == "" {
			t.Fatalf("seat %d must stay occupied after the rejected join", i)
		}
	}
}

func TestReservationExpiresOnDeadline(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	now := base
	g := impl.NewRoomLifecycleGuard(2)
	g.SetClock(func() time.Time { return now })

	room, err := g.Create("owner-1", 4)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	seat, err := g.Reserve(room.ID, "player-2", 10*time.Second)
	if err != nil {
		t.Fatalf("reserve must succeed, got %v", err)
	}
	if !seat.Reserved {
		t.Fatalf("reserved seat must be marked reserved")
	}
	if !seat.ExpiresAt.Equal(base.Add(10 * time.Second)) {
		t.Fatalf("reservation deadline = %v, want %v", seat.ExpiresAt, base.Add(10*time.Second))
	}
	now = base.Add(9 * time.Second)
	if expired := g.Tick(); len(expired) != 0 {
		t.Fatalf("reservation must survive until its deadline, expired %d seats", len(expired))
	}
	if got := g.Seats(room.ID); got[1].PlayerID != "player-2" || !got[1].Reserved {
		t.Fatalf("seat 1 = %+v, reservation must still be held before the deadline", got[1])
	}
	now = base.Add(10*time.Second + time.Nanosecond)
	expired := g.Tick()
	if len(expired) != 1 {
		t.Fatalf("deadline must expire exactly one reservation, got %d", len(expired))
	}
	if expired[0].RoomID != room.ID || expired[0].SeatIndex != 1 || expired[0].PlayerID != "player-2" {
		t.Fatalf("expired record = %+v, want player-2 on seat 1", expired[0])
	}
	if got := g.Seats(room.ID); got[1].PlayerID != "" || got[1].Reserved {
		t.Fatalf("seat 1 = %+v, the expired reservation must be released", got[1])
	}
	if _, err := g.Join(room.ID, "player-2"); err != nil {
		t.Fatalf("a released seat must be joinable again, got %v", err)
	}
}

func TestReserveRejectsBadTTLAndFullRoom(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 2)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-2", 0); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("zero ttl must report ErrInvalidTTL, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-2", -time.Second); !errors.Is(err, impl.ErrInvalidTTL) {
		t.Fatalf("negative ttl must report ErrInvalidTTL, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-2", time.Minute); err != nil {
		t.Fatalf("reserving the last free seat must succeed, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-2", time.Minute); !errors.Is(err, impl.ErrAlreadyJoined) {
		t.Fatalf("reserving twice must report ErrAlreadyJoined, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-3", time.Minute); !errors.Is(err, impl.ErrRoomFull) {
		t.Fatalf("reserving a full room must report ErrRoomFull, got %v", err)
	}
}

func TestReservedSeatIsNotHandedToAnotherPlayer(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 3)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-2", time.Minute); err != nil {
		t.Fatalf("reserve must succeed, got %v", err)
	}
	seat, err := g.Join(room.ID, "player-3")
	if err != nil {
		t.Fatalf("joining a room with a held seat must succeed, got %v", err)
	}
	if seat.Index != 2 {
		t.Fatalf("player-3 landed on seat %d, the reserved seat 1 must stay with player-2", seat.Index)
	}
	seats := g.Seats(room.ID)
	if seats[1].PlayerID != "player-2" || !seats[1].Reserved {
		t.Fatalf("seat 1 = %+v, the reservation must not be moved", seats[1])
	}
	if seats[2].PlayerID != "player-3" {
		t.Fatalf("seat 2 = %+v, want player-3", seats[2])
	}
}

func TestReadyOnlyWorksForSeatedPlayers(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 3)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if err := g.Ready(room.ID, "player-9", true); !errors.Is(err, impl.ErrPlayerNotFound) {
		t.Fatalf("ready for an unseated player must report ErrPlayerNotFound, got %v", err)
	}
	if err := g.Ready("room-missing", "owner-1", true); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("ready on an unknown room must report ErrRoomNotFound, got %v", err)
	}
	if err := g.Ready(room.ID, "owner-1", true); err != nil {
		t.Fatalf("ready for the owner must succeed, got %v", err)
	}
	if seats := g.Seats(room.ID); !seats[0].Ready {
		t.Fatalf("owner seat must be ready after the call")
	}
	if err := g.Ready(room.ID, "owner-1", false); err != nil {
		t.Fatalf("clearing ready must succeed, got %v", err)
	}
	if seats := g.Seats(room.ID); seats[0].Ready {
		t.Fatalf("owner seat must not stay ready after clearing it")
	}
}

func TestStartRequiresEverySeatReady(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 3)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-2"); err != nil {
		t.Fatalf("join must succeed, got %v", err)
	}
	if err := g.Ready(room.ID, "owner-1", true); err != nil {
		t.Fatalf("ready owner must succeed, got %v", err)
	}
	if err := g.Start(room.ID); !errors.Is(err, impl.ErrNotAllReady) {
		t.Fatalf("start with a not ready player must report ErrNotAllReady, got %v", err)
	}
	if err := g.Ready(room.ID, "player-2", true); err != nil {
		t.Fatalf("ready player-2 must succeed, got %v", err)
	}
	if err := g.Start(room.ID); err != nil {
		t.Fatalf("start with every seat ready must succeed, got %v", err)
	}
	view, ok := g.Room(room.ID)
	if !ok || view.State != impl.RoomRunning {
		t.Fatalf("room state after start = %+v, want %q", view, impl.RoomRunning)
	}
	if err := g.Start(room.ID); !errors.Is(err, impl.ErrIllegalTransition) {
		t.Fatalf("starting a running room must report ErrIllegalTransition, got %v", err)
	}
}

func TestStartNeedsTwoPlayers(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 4)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if err := g.Ready(room.ID, "owner-1", true); err != nil {
		t.Fatalf("ready owner must succeed, got %v", err)
	}
	if err := g.Start(room.ID); !errors.Is(err, impl.ErrNotEnoughPlayers) {
		t.Fatalf("a lone player must not start a room, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-2"); err != nil {
		t.Fatalf("join must succeed, got %v", err)
	}
	if err := g.Ready(room.ID, "player-2", true); err != nil {
		t.Fatalf("ready player-2 must succeed, got %v", err)
	}
	if err := g.Start(room.ID); err != nil {
		t.Fatalf("two ready players must start the room, got %v", err)
	}
}

func TestConcurrentJoinOnlyOnePlayerGetsTheSeat(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 2)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	const callers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, joinErr := g.Join(room.ID, "player-"+string(rune('a'+n)))
			mu.Lock()
			errs = append(errs, joinErr)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	winners := 0
	full := 0
	for _, joinErr := range errs {
		switch {
		case joinErr == nil:
			winners++
		case errors.Is(joinErr, impl.ErrRoomFull):
			full++
		default:
			t.Fatalf("unexpected join error %v", joinErr)
		}
	}
	if winners != 1 || full != callers-1 {
		t.Fatalf("concurrent join produced %d winners and %d full errors, want 1 and %d", winners, full, callers-1)
	}
	view, ok := g.Room(room.ID)
	if !ok || len(view.Players) != 2 {
		t.Fatalf("room players = %v, want exactly two players", view.Players)
	}
}

func TestLeaveFreesSeatAndKeepsJoinOrder(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 4)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	for _, player := range []string{"player-2", "player-3", "player-4"} {
		if _, err := g.Join(room.ID, player); err != nil {
			t.Fatalf("join %s must succeed, got %v", player, err)
		}
	}
	if err := g.Leave(room.ID, "player-3"); err != nil {
		t.Fatalf("leave must succeed, got %v", err)
	}
	seats := g.Seats(room.ID)
	if seats[2].PlayerID != "" {
		t.Fatalf("seat 2 = %+v, leaving must free it", seats[2])
	}
	view, ok := g.Room(room.ID)
	if !ok {
		t.Fatalf("room must stay readable")
	}
	want := []string{"owner-1", "player-2", "player-4"}
	if len(view.Players) != len(want) {
		t.Fatalf("join order = %v, want %v", view.Players, want)
	}
	for i := range want {
		if view.Players[i] != want[i] {
			t.Fatalf("join order = %v, want %v", view.Players, want)
		}
	}
	seat, err := g.Join(room.ID, "player-5")
	if err != nil {
		t.Fatalf("joining the freed seat must succeed, got %v", err)
	}
	if seat.Index != 2 {
		t.Fatalf("new player landed on seat %d, want the freed seat 2", seat.Index)
	}
	if err := g.Leave(room.ID, "player-9"); !errors.Is(err, impl.ErrPlayerNotFound) {
		t.Fatalf("leaving as an unseated player must report ErrPlayerNotFound, got %v", err)
	}
	if err := g.Leave("room-missing", "owner-1"); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("leaving an unknown room must report ErrRoomNotFound, got %v", err)
	}
}

func TestTransferHostWalksTheJoinOrder(t *testing.T) {
	g := impl.NewRoomLifecycleGuard(2)
	room, err := g.Create("owner-1", 4)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-2"); err != nil {
		t.Fatalf("join player-2 must succeed, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-3"); err != nil {
		t.Fatalf("join player-3 must succeed, got %v", err)
	}
	next, err := g.TransferHost(room.ID)
	if err != nil {
		t.Fatalf("first transfer must succeed, got %v", err)
	}
	if next != "player-2" {
		t.Fatalf("first successor = %q, want player-2", next)
	}
	host, err := g.Host(room.ID)
	if err != nil || host != "player-2" {
		t.Fatalf("host after first transfer = %q err %v, want player-2", host, err)
	}
	next, err = g.TransferHost(room.ID)
	if err != nil || next != "player-3" {
		t.Fatalf("second successor = %q err %v, want player-3", next, err)
	}
	next, err = g.TransferHost(room.ID)
	if err != nil || next != "owner-1" {
		t.Fatalf("third successor = %q err %v, want the order to wrap back to owner-1", next, err)
	}
	if _, err := g.TransferHost("room-missing"); !errors.Is(err, impl.ErrRoomNotFound) {
		t.Fatalf("transfer on an unknown room must report ErrRoomNotFound, got %v", err)
	}
}

func TestSnapshotRestoreKeepsSeatsAndDeadline(t *testing.T) {
	base := time.Date(2026, 4, 2, 9, 30, 0, 0, time.UTC)
	now := base
	g := impl.NewRoomLifecycleGuard(2)
	g.SetClock(func() time.Time { return now })

	room, err := g.Create("owner-1", 3)
	if err != nil {
		t.Fatalf("create room must succeed, got %v", err)
	}
	if _, err := g.Join(room.ID, "player-2"); err != nil {
		t.Fatalf("join must succeed, got %v", err)
	}
	if err := g.Ready(room.ID, "owner-1", true); err != nil {
		t.Fatalf("ready owner must succeed, got %v", err)
	}
	if _, err := g.Reserve(room.ID, "player-3", 10*time.Second); err != nil {
		t.Fatalf("reserve must succeed, got %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}

	restored := impl.NewRoomLifecycleGuard(2)
	restored.SetClock(func() time.Time { return now })
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	view, ok := restored.Room(room.ID)
	if !ok {
		t.Fatalf("restored guard must know the room")
	}
	if view.State != impl.RoomWaiting || view.OwnerID != "owner-1" || view.SeatCount != 3 {
		t.Fatalf("restored room = %+v, want the waiting room owned by owner-1 with 3 seats", view)
	}
	seats := restored.Seats(room.ID)
	if len(seats) != 3 {
		t.Fatalf("restored seats = %d, want 3", len(seats))
	}
	if seats[0].PlayerID != "owner-1" || !seats[0].Ready {
		t.Fatalf("restored seat 0 = %+v, want the ready owner", seats[0])
	}
	if seats[1].PlayerID != "player-2" || seats[1].Reserved {
		t.Fatalf("restored seat 1 = %+v, want player-2 without a reservation", seats[1])
	}
	if seats[2].PlayerID != "player-3" || !seats[2].Reserved {
		t.Fatalf("restored seat 2 = %+v, want the held seat of player-3", seats[2])
	}
	if !seats[2].ExpiresAt.Equal(base.Add(10 * time.Second)) {
		t.Fatalf("restored deadline = %v, want the original %v", seats[2].ExpiresAt, base.Add(10*time.Second))
	}
	now = base.Add(10 * time.Second)
	expired := restored.Tick()
	if len(expired) != 1 || expired[0].PlayerID != "player-3" {
		t.Fatalf("restored deadline must still fire, expired %+v", expired)
	}
	fresh, err := restored.Create("owner-9", 2)
	if err != nil {
		t.Fatalf("create after restore must succeed, got %v", err)
	}
	if fresh.ID == room.ID {
		t.Fatalf("restored guard reused room id %q, the sequence must survive the restart", fresh.ID)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
}
