// Package task keeps a dropped player's seat during a hold window, hands out
// one-shot resume tokens and frees the seat when the window ends.
package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// Table caps. A seat record exists for every active or held chair, while a hold
// record exists only for a held chair.
const (
	MaxSeatTable = MaxResumeTokens
	MaxHoldTable = MaxResumeTokens
)

// Aliases kept for callers that prefer the shorter table names.
const (
	MaxSeats = MaxSeatTable
	MaxHolds = MaxHoldTable
)

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
	ErrSeatTableFull   = errors.New("seat table is full")
	ErrHoldTableFull   = errors.New("hold table is full")
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

type seatKey struct {
	roomID   string
	playerID string
}

type seatRecord struct {
	roomID    string
	playerID  string
	seatIndex int
	state     SeatState
	heldUntil time.Time
	holdID    string
}

func (s *seatRecord) seat() Seat {
	return Seat{
		RoomID:    s.roomID,
		PlayerID:  s.playerID,
		SeatIndex: s.seatIndex,
		State:     s.state,
		HeldUntil: s.heldUntil,
	}
}

type resumeRecord struct {
	digest    string
	roomID    string
	playerID  string
	seatIndex int
	holdID    string
	issuedAt  time.Time
	expiresAt time.Time
	used      bool
}

const snapshotVersion = 1

type snapshotSeat struct {
	RoomID    string    `json:"room_id"`
	PlayerID  string    `json:"player_id"`
	SeatIndex int       `json:"seat_index"`
	State     SeatState `json:"state"`
	HeldUntil time.Time `json:"held_until"`
	HoldID    string    `json:"hold_id,omitempty"`
}

type snapshotToken struct {
	Digest    string    `json:"digest"`
	RoomID    string    `json:"room_id"`
	PlayerID  string    `json:"player_id"`
	SeatIndex int       `json:"seat_index"`
	HoldID    string    `json:"hold_id,omitempty"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}

type snapshotPayload struct {
	Version int             `json:"version"`
	Seats   []snapshotSeat  `json:"seats"`
	Tokens  []snapshotToken `json:"tokens"`
	Seq     uint64          `json:"seq"`
}

// ReconnectSeatGuard owns the seat tables, holds and resume tokens.
type ReconnectSeatGuard struct {
	mu         sync.Mutex
	holdWindow time.Duration
	tokenTTL   time.Duration
	clock      func() time.Time
	seats      map[string]map[int]*seatRecord
	players    map[string]*seatRecord
	holds      map[seatKey]*seatRecord
	tokens     map[string]*resumeRecord
	seq        uint64
}

// NewReconnectSeatGuard builds a guard with an independent hold window and
// token lifetime.
func NewReconnectSeatGuard(holdWindow, tokenTTL time.Duration) *ReconnectSeatGuard {
	return &ReconnectSeatGuard{
		holdWindow: holdWindow,
		tokenTTL:   tokenTTL,
		clock:      time.Now,
		seats:      make(map[string]map[int]*seatRecord),
		players:    make(map[string]*seatRecord),
		holds:      make(map[seatKey]*seatRecord),
		tokens:     make(map[string]*resumeRecord),
	}
}

// SetClock replaces the time source used for hold deadlines and token expiry.
func (g *ReconnectSeatGuard) SetClock(now func() time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now == nil {
		g.clock = time.Now
		return
	}
	g.clock = now
}

func (g *ReconnectSeatGuard) nowLocked() time.Time {
	if g.clock == nil {
		return time.Now()
	}
	return g.clock()
}

func (g *ReconnectSeatGuard) seatLocked(roomID, playerID string) *seatRecord {
	roomSeats := g.seats[roomID]
	if roomSeats == nil {
		return nil
	}
	for _, rec := range roomSeats {
		if rec.playerID == playerID {
			return rec
		}
	}
	return nil
}

func (g *ReconnectSeatGuard) removeSeatLocked(rec *seatRecord) {
	if roomSeats := g.seats[rec.roomID]; roomSeats != nil {
		delete(roomSeats, rec.seatIndex)
		if len(roomSeats) == 0 {
			delete(g.seats, rec.roomID)
		}
	}
	if cur, ok := g.players[rec.playerID]; ok && cur == rec {
		delete(g.players, rec.playerID)
	}
}

func (g *ReconnectSeatGuard) hasAnyTokenLocked(rec *seatRecord) bool {
	for _, tok := range g.tokens {
		if tok.roomID == rec.roomID && tok.playerID == rec.playerID &&
			tok.seatIndex == rec.seatIndex && tok.holdID == rec.holdID {
			return true
		}
	}
	return false
}

func (g *ReconnectSeatGuard) revokeLiveTokensLocked(rec *seatRecord, now time.Time) {
	for _, tok := range g.tokens {
		if tok.roomID != rec.roomID || tok.playerID != rec.playerID || tok.seatIndex != rec.seatIndex {
			continue
		}
		if !tok.used && now.Before(tok.expiresAt) {
			tok.used = true
		}
	}
}

func (g *ReconnectSeatGuard) revokeAllTokensLocked(rec *seatRecord, now time.Time) {
	for _, tok := range g.tokens {
		if tok.roomID == rec.roomID && tok.playerID == rec.playerID &&
			tok.seatIndex == rec.seatIndex && !tok.used && now.Before(tok.expiresAt) {
			tok.used = true
		}
	}
}

func tokenDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validTokenDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (g *ReconnectSeatGuard) nextTokenLocked(rec *seatRecord, at time.Time) (string, string) {
	for {
		g.seq++
		seed := fmt.Sprintf("resume:%s:%s:%d:%d:%s",
			rec.roomID, rec.playerID, rec.seatIndex, g.seq,
			at.UTC().Format(time.RFC3339Nano))
		sum := sha256.Sum256([]byte(seed))
		value := hex.EncodeToString(sum[:])
		digest := tokenDigest(value)
		if _, exists := g.tokens[digest]; !exists {
			return value, digest
		}
	}
}

// Join seats a player at the requested index while the seat is free.
func (g *ReconnectSeatGuard) Join(roomID, playerID string, seatIndex int) (Seat, error) {
	if strings.TrimSpace(roomID) == "" {
		return Seat{}, ErrInvalidRoomID
	}
	if strings.TrimSpace(playerID) == "" {
		return Seat{}, ErrInvalidPlayerID
	}
	if seatIndex < 0 || seatIndex > MaxSeatIndex {
		return Seat{}, ErrInvalidSeat
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.players[playerID]; exists {
		return Seat{}, ErrAlreadySeated
	}
	if roomSeats := g.seats[roomID]; roomSeats != nil {
		if _, taken := roomSeats[seatIndex]; taken {
			return Seat{}, ErrSeatTaken
		}
	}
	if len(g.players) >= MaxSeatTable {
		return Seat{}, ErrSeatTableFull
	}

	rec := &seatRecord{
		roomID:    roomID,
		playerID:  playerID,
		seatIndex: seatIndex,
		state:     SeatActive,
	}
	roomSeats := g.seats[roomID]
	if roomSeats == nil {
		roomSeats = make(map[int]*seatRecord)
		g.seats[roomID] = roomSeats
	}
	roomSeats[seatIndex] = rec
	g.players[playerID] = rec
	return rec.seat(), nil
}

// OnDrop moves an active seat into the hold state and returns its deadline.
func (g *ReconnectSeatGuard) OnDrop(roomID, playerID string) (SeatHold, error) {
	if strings.TrimSpace(roomID) == "" {
		return SeatHold{}, ErrInvalidRoomID
	}
	if strings.TrimSpace(playerID) == "" {
		return SeatHold{}, ErrInvalidPlayerID
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	rec := g.seatLocked(roomID, playerID)
	if rec == nil || rec.state == SeatReleased {
		return SeatHold{}, ErrPlayerNotFound
	}

	if rec.state == SeatHeld {
		// A repeated drop is accepted only after a resume credential exists;
		// it refreshes the hold and invalidates the previous credential.
		if !g.hasAnyTokenLocked(rec) {
			return SeatHold{}, ErrAlreadyHeld
		}
	} else if rec.state != SeatActive {
		return SeatHold{}, ErrPlayerNotFound
	} else if len(g.holds) >= MaxHoldTable {
		return SeatHold{}, ErrHoldTableFull
	}
	if g.holdWindow <= 0 {
		return SeatHold{}, ErrInvalidHoldWin
	}

	now := g.nowLocked()
	g.revokeLiveTokensLocked(rec, now)
	g.seq++
	rec.state = SeatHeld
	rec.heldUntil = now.Add(g.holdWindow)
	rec.holdID = fmt.Sprintf("hold-%d", g.seq)
	g.holds[seatKey{roomID: roomID, playerID: playerID}] = rec

	return SeatHold{
		HoldID:    rec.holdID,
		RoomID:    rec.roomID,
		PlayerID:  rec.playerID,
		SeatIndex: rec.seatIndex,
		HeldUntil: rec.heldUntil,
	}, nil
}

// IssueResume mints a one-shot resume token for a held seat.
func (g *ReconnectSeatGuard) IssueResume(roomID, playerID string) (ResumeToken, error) {
	if strings.TrimSpace(roomID) == "" {
		return ResumeToken{}, ErrInvalidRoomID
	}
	if strings.TrimSpace(playerID) == "" {
		return ResumeToken{}, ErrInvalidPlayerID
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	rec := g.seatLocked(roomID, playerID)
	if rec == nil {
		return ResumeToken{}, ErrPlayerNotFound
	}
	if rec.state != SeatHeld {
		return ResumeToken{}, ErrSeatNotHeld
	}
	if g.tokenTTL <= 0 {
		return ResumeToken{}, ErrInvalidTokenTTL
	}
	now := g.nowLocked()
	if !now.Before(rec.heldUntil) {
		return ResumeToken{}, ErrSeatNotHeld
	}
	if len(g.tokens) >= MaxResumeTokens {
		return ResumeToken{}, ErrTokenTableFull
	}

	g.revokeLiveTokensLocked(rec, now)
	value, digest := g.nextTokenLocked(rec, now)
	tok := &resumeRecord{
		digest:    digest,
		roomID:    rec.roomID,
		playerID:  rec.playerID,
		seatIndex: rec.seatIndex,
		holdID:    rec.holdID,
		issuedAt:  now,
		expiresAt: now.Add(g.tokenTTL),
	}
	g.tokens[digest] = tok

	return ResumeToken{
		Value:     value,
		RoomID:    tok.roomID,
		PlayerID:  tok.playerID,
		IssuedAt:  tok.issuedAt,
		ExpiresAt: tok.expiresAt,
	}, nil
}

// Resume redeems a token and returns the seat to its player.
func (g *ReconnectSeatGuard) Resume(token string) (Seat, error) {
	if token == "" {
		return Seat{}, ErrTokenNotFound
	}
	digest := tokenDigest(token)

	g.mu.Lock()
	defer g.mu.Unlock()

	tok := g.tokens[digest]
	if tok == nil {
		return Seat{}, ErrTokenNotFound
	}
	if tok.used {
		return Seat{}, ErrTokenUsed
	}
	now := g.nowLocked()
	if !now.Before(tok.expiresAt) {
		return Seat{}, ErrTokenExpired
	}

	rec := g.seatLocked(tok.roomID, tok.playerID)
	if rec == nil || rec.state != SeatHeld || rec.seatIndex != tok.seatIndex || rec.holdID != tok.holdID {
		return Seat{}, ErrSeatNotHeld
	}
	if !now.Before(rec.heldUntil) {
		return Seat{}, ErrSeatNotHeld
	}

	tok.used = true
	rec.state = SeatActive
	rec.heldUntil = time.Time{}
	rec.holdID = ""
	delete(g.holds, seatKey{roomID: rec.roomID, playerID: rec.playerID})
	return rec.seat(), nil
}

// Expire releases every held seat whose deadline has been reached.
func (g *ReconnectSeatGuard) Expire(now time.Time) []Seat {
	g.mu.Lock()
	defer g.mu.Unlock()

	expired := make([]*seatRecord, 0)
	for _, rec := range g.holds {
		if rec.state == SeatHeld && !now.Before(rec.heldUntil) {
			expired = append(expired, rec)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].roomID != expired[j].roomID {
			return expired[i].roomID < expired[j].roomID
		}
		return expired[i].seatIndex < expired[j].seatIndex
	})

	released := make([]Seat, 0, len(expired))
	for _, rec := range expired {
		g.revokeAllTokensLocked(rec, now)
		delete(g.holds, seatKey{roomID: rec.roomID, playerID: rec.playerID})
		g.removeSeatLocked(rec)
		rec.state = SeatReleased
		released = append(released, rec.seat())
	}
	return released
}

// Release frees a seat immediately without any hold.
func (g *ReconnectSeatGuard) Release(roomID, playerID string) error {
	if strings.TrimSpace(roomID) == "" {
		return ErrInvalidRoomID
	}
	if strings.TrimSpace(playerID) == "" {
		return ErrInvalidPlayerID
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	rec := g.seatLocked(roomID, playerID)
	if rec == nil {
		return ErrPlayerNotFound
	}
	g.revokeAllTokensLocked(rec, g.nowLocked())
	delete(g.holds, seatKey{roomID: rec.roomID, playerID: rec.playerID})
	g.removeSeatLocked(rec)
	return nil
}

// Seat reads one seat of a room.
func (g *ReconnectSeatGuard) Seat(roomID, playerID string) (Seat, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rec := g.seatLocked(roomID, playerID)
	if rec == nil {
		return Seat{}, false
	}
	return rec.seat(), true
}

// HeldSeats lists every seat currently in the hold state, ordered by room and
// then by seat index.
func (g *ReconnectSeatGuard) HeldSeats() []Seat {
	g.mu.Lock()
	defer g.mu.Unlock()

	held := make([]Seat, 0, len(g.holds))
	for _, rec := range g.holds {
		if rec.state == SeatHeld {
			held = append(held, rec.seat())
		}
	}
	sort.Slice(held, func(i, j int) bool {
		if held[i].RoomID != held[j].RoomID {
			return held[i].RoomID < held[j].RoomID
		}
		return held[i].SeatIndex < held[j].SeatIndex
	})
	return held
}

// Snapshot serialises the seat tables, hold deadlines and token digests.
func (g *ReconnectSeatGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	payload := snapshotPayload{
		Version: snapshotVersion,
		Seats:   make([]snapshotSeat, 0, len(g.players)),
		Tokens:  make([]snapshotToken, 0, len(g.tokens)),
		Seq:     g.seq,
	}
	for _, roomSeats := range g.seats {
		for _, rec := range roomSeats {
			payload.Seats = append(payload.Seats, snapshotSeat{
				RoomID:    rec.roomID,
				PlayerID:  rec.playerID,
				SeatIndex: rec.seatIndex,
				State:     rec.state,
				HeldUntil: rec.heldUntil,
				HoldID:    rec.holdID,
			})
		}
	}
	sort.Slice(payload.Seats, func(i, j int) bool {
		a, b := payload.Seats[i], payload.Seats[j]
		if a.RoomID != b.RoomID {
			return a.RoomID < b.RoomID
		}
		if a.SeatIndex != b.SeatIndex {
			return a.SeatIndex < b.SeatIndex
		}
		return a.PlayerID < b.PlayerID
	})
	for _, tok := range g.tokens {
		payload.Tokens = append(payload.Tokens, snapshotToken{
			Digest:    tok.digest,
			RoomID:    tok.roomID,
			PlayerID:  tok.playerID,
			SeatIndex: tok.seatIndex,
			HoldID:    tok.holdID,
			IssuedAt:  tok.issuedAt,
			ExpiresAt: tok.expiresAt,
			Used:      tok.used,
		})
	}
	sort.Slice(payload.Tokens, func(i, j int) bool {
		return payload.Tokens[i].Digest < payload.Tokens[j].Digest
	})

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces the whole guard state with a snapshot, all or nothing.
func (g *ReconnectSeatGuard) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}

	var payload snapshotPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ErrBadSnapshot
	}
	if payload.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	if len(payload.Seats) > MaxSeatTable || len(payload.Tokens) > MaxResumeTokens {
		return ErrBadSnapshot
	}

	seats := make(map[string]map[int]*seatRecord)
	players := make(map[string]*seatRecord)
	holds := make(map[seatKey]*seatRecord)
	for _, item := range payload.Seats {
		if strings.TrimSpace(item.RoomID) == "" || strings.TrimSpace(item.PlayerID) == "" {
			return ErrBadSnapshot
		}
		if item.SeatIndex < 0 || item.SeatIndex > MaxSeatIndex {
			return ErrBadSnapshot
		}
		if item.State != SeatActive && item.State != SeatHeld {
			return ErrBadSnapshot
		}
		if _, exists := players[item.PlayerID]; exists {
			return ErrBadSnapshot
		}
		roomSeats := seats[item.RoomID]
		if roomSeats == nil {
			roomSeats = make(map[int]*seatRecord)
			seats[item.RoomID] = roomSeats
		}
		if _, exists := roomSeats[item.SeatIndex]; exists {
			return ErrBadSnapshot
		}

		rec := &seatRecord{
			roomID:    item.RoomID,
			playerID:  item.PlayerID,
			seatIndex: item.SeatIndex,
			state:     item.State,
			heldUntil: item.HeldUntil,
			holdID:    item.HoldID,
		}
		roomSeats[rec.seatIndex] = rec
		players[rec.playerID] = rec
		if rec.state == SeatHeld {
			if len(holds) >= MaxHoldTable {
				return ErrBadSnapshot
			}
			holds[seatKey{roomID: rec.roomID, playerID: rec.playerID}] = rec
		}
	}

	tokens := make(map[string]*resumeRecord)
	for _, item := range payload.Tokens {
		if !validTokenDigest(item.Digest) {
			return ErrBadSnapshot
		}
		if strings.TrimSpace(item.RoomID) == "" || strings.TrimSpace(item.PlayerID) == "" {
			return ErrBadSnapshot
		}
		if item.SeatIndex < 0 || item.SeatIndex > MaxSeatIndex {
			return ErrBadSnapshot
		}
		if _, exists := tokens[item.Digest]; exists {
			return ErrBadSnapshot
		}
		tokens[item.Digest] = &resumeRecord{
			digest:    item.Digest,
			roomID:    item.RoomID,
			playerID:  item.PlayerID,
			seatIndex: item.SeatIndex,
			holdID:    item.HoldID,
			issuedAt:  item.IssuedAt,
			expiresAt: item.ExpiresAt,
			used:      item.Used,
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.seats = seats
	g.players = players
	g.holds = holds
	g.tokens = tokens
	g.seq = payload.Seq
	return nil
}
