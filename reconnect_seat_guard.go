// Package task keeps a dropped player's seat during a hold window, hands out
// one-shot resume tokens and frees the seat when the window ends.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// SeatState is the occupancy state of one chair.
type SeatState string

// Seat states.
const (
	SeatActive   SeatState = "active"
	SeatHeld     SeatState = "held"
	SeatReleased SeatState = "released"
)

// MaxSeatIndex is the highest seat number a room may hand out.
const MaxSeatIndex = 15

// MaxResumeTokens caps the number of live resume tokens kept in memory.
const MaxResumeTokens = 4096

// Errors returned by the reconnect seat guard.
var (
	ErrRoomNotFound    = errors.New("room not found")
	ErrPlayerNotFound  = errors.New("player is not seated in this room")
	ErrSeatTaken       = errors.New("seat already occupied")
	ErrAlreadySeated   = errors.New("player already holds a seat")
	ErrAlreadyHeld     = errors.New("seat is already held after a drop")
	ErrSeatNotHeld     = errors.New("seat is not held")
	ErrInvalidSeat     = errors.New("seat index is out of range")
	ErrInvalidRoomID   = errors.New("room id is required")
	ErrInvalidPlayerID = errors.New("player id is required")
	ErrInvalidHoldWin  = errors.New("hold window must be positive")
	ErrInvalidTokenTTL = errors.New("token ttl must be positive")
	ErrTokenNotFound   = errors.New("resume token is unknown")
	ErrTokenExpired    = errors.New("resume token has expired")
	ErrTokenUsed       = errors.New("resume token was already used")
	ErrTokenTableFull  = errors.New("resume token table is full")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Seat is one chair of a room.
type Seat struct {
	RoomID    string    `json:"room_id"`
	PlayerID  string    `json:"player_id"`
	SeatIndex int       `json:"seat_index"`
	State     SeatState `json:"state"`
	HeldUntil time.Time `json:"held_until"`
}

// SeatHold describes the hold that a drop created for one seat.
type SeatHold struct {
	HoldID    string    `json:"hold_id"`
	RoomID    string    `json:"room_id"`
	PlayerID  string    `json:"player_id"`
	SeatIndex int       `json:"seat_index"`
	HeldUntil time.Time `json:"held_until"`
}

// ResumeToken is the one-shot credential that lets a dropped player return.
type ResumeToken struct {
	Value     string    `json:"value"`
	RoomID    string    `json:"room_id"`
	PlayerID  string    `json:"player_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ReconnectSeatGuard owns the seat tables, holds and resume tokens.
type ReconnectSeatGuard struct {
	mu         sync.Mutex
	holdWindow time.Duration
	tokenTTL   time.Duration
	clock      func() time.Time
}

// NewReconnectSeatGuard builds a guard with an independent hold window and
// token lifetime.
func NewReconnectSeatGuard(holdWindow, tokenTTL time.Duration) *ReconnectSeatGuard {
	return &ReconnectSeatGuard{holdWindow: holdWindow, tokenTTL: tokenTTL, clock: time.Now}
}

// SetClock replaces the time source used for hold deadlines and token expiry.
func (g *ReconnectSeatGuard) SetClock(now func() time.Time) {
}

// Join seats a player at the requested index while the seat is free.
func (g *ReconnectSeatGuard) Join(roomID, playerID string, seatIndex int) (Seat, error) {
	return Seat{}, errNotImplemented
}

// OnDrop moves an active seat into the hold state and returns its deadline.
func (g *ReconnectSeatGuard) OnDrop(roomID, playerID string) (SeatHold, error) {
	return SeatHold{}, errNotImplemented
}

// IssueResume mints a one-shot resume token for a held seat.
func (g *ReconnectSeatGuard) IssueResume(roomID, playerID string) (ResumeToken, error) {
	return ResumeToken{}, errNotImplemented
}

// Resume redeems a token and returns the seat to its player.
func (g *ReconnectSeatGuard) Resume(token string) (Seat, error) {
	return Seat{}, errNotImplemented
}

// Expire releases every held seat whose deadline has been reached.
func (g *ReconnectSeatGuard) Expire(now time.Time) []Seat {
	return nil
}

// Release frees a seat immediately without any hold.
func (g *ReconnectSeatGuard) Release(roomID, playerID string) error {
	return errNotImplemented
}

// Seat reads one seat of a room.
func (g *ReconnectSeatGuard) Seat(roomID, playerID string) (Seat, bool) {
	return Seat{}, false
}

// HeldSeats lists every seat currently in the hold state, ordered by room and
// then by seat index.
func (g *ReconnectSeatGuard) HeldSeats() []Seat {
	return nil
}

// Snapshot serialises the seat tables, hold deadlines and token digests.
func (g *ReconnectSeatGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole guard state with a snapshot, all or nothing.
func (g *ReconnectSeatGuard) Restore(data []byte) error {
	return errNotImplemented
}
