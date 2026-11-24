// Package task guards the lifecycle of one battle room: seat occupancy,
// reservations with a deadline, host succession and the restart snapshot.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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
}

// NewRoomLifecycleGuard builds a guard that admits at most maxRooms rooms.
func NewRoomLifecycleGuard(maxRooms int) *RoomLifecycleGuard {
	return &RoomLifecycleGuard{maxRooms: maxRooms, clock: time.Now}
}

// SetClock replaces the time source used for reservation deadlines.
func (g *RoomLifecycleGuard) SetClock(now func() time.Time) {
}

// Create opens a room whose owner already sits in seat zero.
func (g *RoomLifecycleGuard) Create(ownerID string, seats int) (Room, error) {
	return Room{}, errNotImplemented
}

// Join gives a free seat to the player and appends it to the join order.
func (g *RoomLifecycleGuard) Join(roomID, playerID string) (Seat, error) {
	return Seat{}, errNotImplemented
}

// Reserve holds a free seat for a player for the given ttl.
func (g *RoomLifecycleGuard) Reserve(roomID, playerID string, ttl time.Duration) (Seat, error) {
	return Seat{}, errNotImplemented
}

// Ready flips the ready flag of one seated player.
func (g *RoomLifecycleGuard) Ready(roomID, playerID string, ready bool) error {
	return errNotImplemented
}

// Start moves a waiting room to running once every seat is ready.
func (g *RoomLifecycleGuard) Start(roomID string) error {
	return errNotImplemented
}

// Leave releases the player's seat and keeps them in the historical order.
func (g *RoomLifecycleGuard) Leave(roomID, playerID string) error {
	return errNotImplemented
}

// TransferHost hands the host seat to the next live player in join order.
func (g *RoomLifecycleGuard) TransferHost(roomID string) (string, error) {
	return "", errNotImplemented
}

// Host reports the current host of a room.
func (g *RoomLifecycleGuard) Host(roomID string) (string, error) {
	return "", errNotImplemented
}

// Room reads the current view of one room.
func (g *RoomLifecycleGuard) Room(roomID string) (Room, bool) {
	return Room{}, false
}

// Seats reads every seat of one room, free seats included.
func (g *RoomLifecycleGuard) Seats(roomID string) []Seat {
	return nil
}

// Finish moves a running room into settlement.
func (g *RoomLifecycleGuard) Finish(roomID string) error {
	return errNotImplemented
}

// Destroy drops a room from the registry and frees its room slot.
func (g *RoomLifecycleGuard) Destroy(roomID string) error {
	return errNotImplemented
}

// Tick expires every reservation whose deadline has been reached.
func (g *RoomLifecycleGuard) Tick() []ExpiredReservation {
	return nil
}

// Snapshot serialises every room, seat table and reservation deadline.
func (g *RoomLifecycleGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole registry with a snapshot, all or nothing.
func (g *RoomLifecycleGuard) Restore(data []byte) error {
	return errNotImplemented
}
