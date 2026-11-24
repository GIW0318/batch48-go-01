// Package task guards the lifecycle of one battle room: seat occupancy,
// reservations with a deadline, host succession and the restart snapshot.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// RoomState is the lifecycle position of a room.
type RoomState string

// Room lifecycle states.
const (
	RoomWaiting  RoomState = "waiting"
	RoomRunning  RoomState = "running"
	RoomSettling RoomState = "settling"
	RoomClosed   RoomState = "closed"
)

// Seat count bounds enforced by Create.
const (
	MinSeatsPerRoom = 2
	MaxSeatsPerRoom = 8
)

// Errors returned by the lifecycle guard.
var (
	ErrRoomNotFound      = errors.New("room not found")
	ErrRoomLimitReached  = errors.New("room limit reached")
	ErrRoomFull          = errors.New("room has no free seat")
	ErrAlreadyJoined     = errors.New("player already holds a seat")
	ErrPlayerNotFound    = errors.New("player is not seated in this room")
	ErrInvalidOwner      = errors.New("owner id is required")
	ErrInvalidPlayer     = errors.New("player id is required")
	ErrInvalidSeats      = errors.New("seat count is out of range")
	ErrInvalidTTL        = errors.New("reservation ttl must be positive")
	ErrNotAllReady       = errors.New("every seated player must be ready")
	ErrNotEnoughPlayers  = errors.New("at least two players are required to start")
	ErrIllegalTransition = errors.New("room state transition is not allowed")
	ErrNoSuccessor       = errors.New("no other player can take the host seat")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
)

// Seat is one chair of a room. A reserved seat is held for a player until
// ExpiresAt passes.
type Seat struct {
	Index     int       `json:"index"`
	PlayerID  string    `json:"player_id"`
	Reserved  bool      `json:"reserved"`
	Ready     bool      `json:"ready"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Room is the caller facing view of one room.
type Room struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	SeatCount int       `json:"seat_count"`
	State     RoomState `json:"state"`
	Players   []string  `json:"players"`
}

// ExpiredReservation reports one seat released by Tick.
type ExpiredReservation struct {
	RoomID    string `json:"room_id"`
	SeatIndex int    `json:"seat_index"`
	PlayerID  string `json:"player_id"`
}

// RoomLifecycleGuard owns every live room and the seat tables behind them.
type RoomLifecycleGuard struct {
	mu       sync.Mutex
	maxRooms int
	clock    func() time.Time
	seq      int
	rooms    map[string]*roomState
}

// roomState is the internal mutable state of one room.
type roomState struct {
	id        string
	host      string
	state     RoomState
	seats     []Seat
	joinOrder []string
}

// NewRoomLifecycleGuard builds a guard that admits at most maxRooms rooms.
func NewRoomLifecycleGuard(maxRooms int) *RoomLifecycleGuard {
	return &RoomLifecycleGuard{
		maxRooms: maxRooms,
		clock:    time.Now,
		rooms:    make(map[string]*roomState),
	}
}

// SetClock replaces the time source used for reservation deadlines.
func (g *RoomLifecycleGuard) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clock = now
}

// Create opens a room whose owner already sits in seat zero.
func (g *RoomLifecycleGuard) Create(ownerID string, seats int) (Room, error) {
	if ownerID == "" {
		return Room{}, ErrInvalidOwner
	}
	if seats < MinSeatsPerRoom || seats > MaxSeatsPerRoom {
		return Room{}, ErrInvalidSeats
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.rooms) >= g.maxRooms {
		return Room{}, ErrRoomLimitReached
	}
	g.seq++
	r := &roomState{
		id:        fmt.Sprintf("room-%d", g.seq),
		host:      ownerID,
		state:     RoomWaiting,
		seats:     make([]Seat, seats),
		joinOrder: []string{ownerID},
	}
	for i := range r.seats {
		r.seats[i].Index = i
	}
	r.seats[0].PlayerID = ownerID
	g.rooms[r.id] = r
	return r.view(), nil
}

// Join gives a free seat to the player and appends it to the join order.
func (g *RoomLifecycleGuard) Join(roomID, playerID string) (Seat, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return Seat{}, ErrRoomNotFound
	}
	if playerID == "" {
		return Seat{}, ErrInvalidPlayer
	}
	if r.state != RoomWaiting {
		return Seat{}, ErrIllegalTransition
	}
	if r.seatOf(playerID) >= 0 {
		return Seat{}, ErrAlreadyJoined
	}
	idx := r.lowestFreeSeat()
	if idx < 0 {
		return Seat{}, ErrRoomFull
	}
	r.seats[idx].PlayerID = playerID
	r.seats[idx].Reserved = false
	r.seats[idx].Ready = false
	r.seats[idx].ExpiresAt = time.Time{}
	r.joinOrder = append(r.joinOrder, playerID)
	return r.seats[idx], nil
}

// Reserve holds a free seat for a player for the given ttl.
func (g *RoomLifecycleGuard) Reserve(roomID, playerID string, ttl time.Duration) (Seat, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return Seat{}, ErrRoomNotFound
	}
	if ttl <= 0 {
		return Seat{}, ErrInvalidTTL
	}
	if playerID == "" {
		return Seat{}, ErrInvalidPlayer
	}
	if r.state != RoomWaiting {
		return Seat{}, ErrIllegalTransition
	}
	if r.seatOf(playerID) >= 0 {
		return Seat{}, ErrAlreadyJoined
	}
	idx := r.lowestFreeSeat()
	if idx < 0 {
		return Seat{}, ErrRoomFull
	}
	r.seats[idx].PlayerID = playerID
	r.seats[idx].Reserved = true
	r.seats[idx].Ready = false
	r.seats[idx].ExpiresAt = g.clock().Add(ttl)
	r.joinOrder = append(r.joinOrder, playerID)
	return r.seats[idx], nil
}

// Ready flips the ready flag of one seated player.
func (g *RoomLifecycleGuard) Ready(roomID, playerID string, ready bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	idx := r.seatOf(playerID)
	if idx < 0 {
		return ErrPlayerNotFound
	}
	r.seats[idx].Ready = ready
	return nil
}

// Start moves a waiting room to running once every seat is ready.
func (g *RoomLifecycleGuard) Start(roomID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	if r.state != RoomWaiting {
		return ErrIllegalTransition
	}
	players := 0
	for i := range r.seats {
		s := &r.seats[i]
		if s.PlayerID == "" || s.Reserved {
			continue
		}
		players++
		if !s.Ready {
			return ErrNotAllReady
		}
	}
	if players < 2 {
		return ErrNotEnoughPlayers
	}
	r.state = RoomRunning
	return nil
}

// Leave releases the player's seat and keeps them in the historical order.
func (g *RoomLifecycleGuard) Leave(roomID, playerID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	if r.state == RoomSettling {
		return ErrIllegalTransition
	}
	idx := r.seatOf(playerID)
	if idx < 0 {
		return ErrPlayerNotFound
	}
	r.seats[idx].PlayerID = ""
	r.seats[idx].Reserved = false
	r.seats[idx].Ready = false
	r.seats[idx].ExpiresAt = time.Time{}
	r.removeFromOrder(playerID)
	if r.host == playerID {
		r.host = ""
		if len(r.joinOrder) > 0 {
			r.host = r.joinOrder[0]
		}
	}
	return nil
}

// TransferHost hands the host seat to the next live player in join order.
func (g *RoomLifecycleGuard) TransferHost(roomID string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return "", ErrRoomNotFound
	}
	n := len(r.joinOrder)
	if n == 0 {
		return "", ErrNoSuccessor
	}
	start := 0
	for i, p := range r.joinOrder {
		if p == r.host {
			start = i
			break
		}
	}
	for step := 1; step <= n; step++ {
		candidate := r.joinOrder[(start+step)%n]
		if candidate == r.host {
			continue
		}
		if r.seatOf(candidate) < 0 {
			continue
		}
		r.host = candidate
		return candidate, nil
	}
	return "", ErrNoSuccessor
}

// Host reports the current host of a room.
func (g *RoomLifecycleGuard) Host(roomID string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return "", ErrRoomNotFound
	}
	return r.host, nil
}

// Room reads the current view of one room.
func (g *RoomLifecycleGuard) Room(roomID string) (Room, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return Room{}, false
	}
	return r.view(), true
}

// Seats reads every seat of one room, free seats included.
func (g *RoomLifecycleGuard) Seats(roomID string) []Seat {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return nil
	}
	out := make([]Seat, len(r.seats))
	copy(out, r.seats)
	return out
}

// Finish moves a running room into settlement.
func (g *RoomLifecycleGuard) Finish(roomID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	if r.state != RoomRunning {
		return ErrIllegalTransition
	}
	r.state = RoomSettling
	return nil
}

// Destroy drops a room from the registry and frees its room slot.
func (g *RoomLifecycleGuard) Destroy(roomID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.rooms[roomID]; !ok {
		return ErrRoomNotFound
	}
	delete(g.rooms, roomID)
	return nil
}

// Tick expires every reservation whose deadline has been reached.
func (g *RoomLifecycleGuard) Tick() []ExpiredReservation {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	ids := make([]string, 0, len(g.rooms))
	for id := range g.rooms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var expired []ExpiredReservation
	for _, id := range ids {
		r := g.rooms[id]
		for i := range r.seats {
			s := &r.seats[i]
			if !s.Reserved || s.PlayerID == "" || s.ExpiresAt.After(now) {
				continue
			}
			expired = append(expired, ExpiredReservation{
				RoomID:    id,
				SeatIndex: s.Index,
				PlayerID:  s.PlayerID,
			})
			r.removeFromOrder(s.PlayerID)
			s.PlayerID = ""
			s.Reserved = false
			s.Ready = false
			s.ExpiresAt = time.Time{}
		}
	}
	return expired
}

// Snapshot serialises every room, seat table and reservation deadline.
func (g *RoomLifecycleGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := snapshot{
		Version: snapshotVersion,
		Seq:     g.seq,
	}
	ids := make([]string, 0, len(g.rooms))
	for id := range g.rooms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := g.rooms[id]
		sr := snapshotRoom{
			ID:        r.id,
			OwnerID:   r.host,
			State:     r.state,
			JoinOrder: append([]string(nil), r.joinOrder...),
			Seats:     append([]Seat(nil), r.seats...),
		}
		snap.Rooms = append(snap.Rooms, sr)
	}
	return json.Marshal(snap)
}

// Restore replaces the whole registry with a snapshot, all or nothing.
func (g *RoomLifecycleGuard) Restore(data []byte) error {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return ErrBadSnapshot
	}
	if snap.Version != snapshotVersion || snap.Seq < 0 {
		return ErrBadSnapshot
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(snap.Rooms) > g.maxRooms {
		return ErrBadSnapshot
	}
	rooms := make(map[string]*roomState, len(snap.Rooms))
	for i := range snap.Rooms {
		sr := &snap.Rooms[i]
		r, err := sr.decode()
		if err != nil {
			return err
		}
		if _, dup := rooms[r.id]; dup {
			return ErrBadSnapshot
		}
		rooms[r.id] = r
	}
	g.rooms = rooms
	g.seq = snap.Seq
	return nil
}

// snapshotVersion tags the on-disk snapshot layout.
const snapshotVersion = 1

// snapshot is the serialisable view of the whole registry.
type snapshot struct {
	Version int            `json:"version"`
	Seq     int            `json:"seq"`
	Rooms   []snapshotRoom `json:"rooms"`
}

// snapshotRoom is the serialisable view of one room.
type snapshotRoom struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	State     RoomState `json:"state"`
	JoinOrder []string  `json:"join_order"`
	Seats     []Seat    `json:"seats"`
}

// decode validates one serialised room and rebuilds its live state.
func (sr *snapshotRoom) decode() (*roomState, error) {
	if sr.ID == "" {
		return nil, ErrBadSnapshot
	}
	switch sr.State {
	case RoomWaiting, RoomRunning, RoomSettling, RoomClosed:
	default:
		return nil, ErrBadSnapshot
	}
	if len(sr.Seats) < MinSeatsPerRoom || len(sr.Seats) > MaxSeatsPerRoom {
		return nil, ErrBadSnapshot
	}
	r := &roomState{
		id:        sr.ID,
		host:      sr.OwnerID,
		state:     sr.State,
		seats:     append([]Seat(nil), sr.Seats...),
		joinOrder: append([]string(nil), sr.JoinOrder...),
	}
	seen := make(map[string]bool, len(r.seats))
	for i := range r.seats {
		s := &r.seats[i]
		if s.Index != i {
			return nil, ErrBadSnapshot
		}
		if s.PlayerID == "" {
			if s.Reserved {
				return nil, ErrBadSnapshot
			}
			continue
		}
		if seen[s.PlayerID] {
			return nil, ErrBadSnapshot
		}
		seen[s.PlayerID] = true
		if s.Reserved && s.ExpiresAt.IsZero() {
			return nil, ErrBadSnapshot
		}
	}
	if r.host == "" {
		if len(seen) > 0 {
			return nil, ErrBadSnapshot
		}
	} else if !seen[r.host] {
		return nil, ErrBadSnapshot
	}
	return r, nil
}

// view builds the caller facing view of one room.
func (r *roomState) view() Room {
	return Room{
		ID:        r.id,
		OwnerID:   r.host,
		SeatCount: len(r.seats),
		State:     r.state,
		Players:   append([]string(nil), r.joinOrder...),
	}
}

// seatOf returns the seat index held by the player, or -1.
func (r *roomState) seatOf(playerID string) int {
	if playerID == "" {
		return -1
	}
	for i := range r.seats {
		if r.seats[i].PlayerID == playerID {
			return i
		}
	}
	return -1
}

// lowestFreeSeat returns the smallest index with no occupant, or -1.
func (r *roomState) lowestFreeSeat() int {
	for i := range r.seats {
		if r.seats[i].PlayerID == "" {
			return i
		}
	}
	return -1
}

// removeFromOrder drops the player from the join order.
func (r *roomState) removeFromOrder(playerID string) {
	for i, p := range r.joinOrder {
		if p == playerID {
			r.joinOrder = append(r.joinOrder[:i], r.joinOrder[i+1:]...)
			return
		}
	}
}
