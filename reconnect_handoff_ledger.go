// Package task hands a disconnected player over to the reconnect path: the
// session is parked for a grace period, the resume token is single use and the
// entity stays reserved until the grace period expires.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// DefaultResendWindow is how many frames behind the last confirmed frame a
// client may resend after a reconnect.
const DefaultResendWindow = 16

// Errors reported by the reconnect ledger.
var (
	ErrInvalidPlayerID    = errors.New("player id is required")
	ErrAlreadyJoined      = errors.New("player already has a session")
	ErrPlayerNotFound     = errors.New("player session does not exist")
	ErrPlayerInGrace      = errors.New("player session is already parked in grace")
	ErrTokenInvalid       = errors.New("resume token is unknown")
	ErrTokenUsed          = errors.New("resume token was already used")
	ErrTokenExpired       = errors.New("resume token has expired")
	ErrMatchEnded         = errors.New("match already ended")
	ErrResendOutOfWindow  = errors.New("frame is outside the resend window")
	ErrConfirmRegressed   = errors.New("confirmed frame must not move backwards")
	ErrBadSnapshot        = errors.New("snapshot payload is invalid")
)

// MatchPhase is the lifecycle phase of a match.
type MatchPhase int

// Match phases.
const (
	PhaseLobby MatchPhase = iota
	PhasePlaying
	PhaseEnded
)

// String names the match phase.
func (p MatchPhase) String() string {
	switch p {
	case PhaseLobby:
		return "lobby"
	case PhasePlaying:
		return "playing"
	case PhaseEnded:
		return "ended"
	default:
		return "unknown"
	}
}

// ResumeToken is the one time ticket handed out when a player detaches.
type ResumeToken string

// Camera is the client view state that survives a reconnect.
type Camera struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Conn identifies one gateway connection.
type Conn struct {
	ID     string `json:"id"`
	Remote string `json:"remote"`
}

// Input is one frame worth of player input.
type Input struct {
	DX     float64 `json:"dx"`
	DY     float64 `json:"dy"`
	Action string  `json:"action"`
}

// Session is the server side view of one player.
type Session struct {
	PlayerID   string     `json:"player_id"`
	ConnID     string     `json:"conn_id"`
	Team       string     `json:"team"`
	Phase      MatchPhase `json:"phase"`
	Camera     Camera     `json:"camera"`
	LastFrame  uint64     `json:"last_frame"`
	ResendFrom uint64     `json:"resend_from"`
	Resent     int        `json:"resent"`
	InGrace    bool       `json:"in_grace"`
}

// ReconnectLedger owns the sessions, the grace periods and the resume tokens.
type ReconnectLedger struct {
	mu           sync.Mutex
	grace        time.Duration
	resendWindow int
	seq          uint64
	sessions     map[string]*Session
	deadlines    map[string]time.Time
	tokens       map[ResumeToken]string
	used         map[ResumeToken]bool
	denials      int
}

// NewReconnectLedger builds a ledger with the given grace period and resend
// window.
func NewReconnectLedger(grace time.Duration, resendWindow int) *ReconnectLedger {
	if resendWindow <= 0 {
		resendWindow = DefaultResendWindow
	}
	return &ReconnectLedger{
		grace:        grace,
		resendWindow: resendWindow,
		sessions:     map[string]*Session{},
		deadlines:    map[string]time.Time{},
		tokens:       map[ResumeToken]string{},
		used:         map[ResumeToken]bool{},
	}
}

// Join registers a player for a match.
func (l *ReconnectLedger) Join(playerID, team string, phase MatchPhase) error {
	return errNotImplemented
}

// Detach parks a player session and hands out a resume token.
func (l *ReconnectLedger) Detach(playerID string) (ResumeToken, error) {
	return "", errNotImplemented
}

// Resume restores a parked session for the given token.
func (l *ReconnectLedger) Resume(token ResumeToken, conn Conn) (*Session, error) {
	return nil, errNotImplemented
}

// Abandon drops a session and releases its slot immediately.
func (l *ReconnectLedger) Abandon(playerID string) {
}

// Expire releases every session whose grace period ended at the given instant.
func (l *ReconnectLedger) Expire(now time.Time) []string {
	return nil
}

// Confirm advances the last confirmed frame of a player.
func (l *ReconnectLedger) Confirm(playerID string, frame uint64) error {
	return errNotImplemented
}

// AcceptResend accepts one resent input inside the resend window.
func (l *ReconnectLedger) AcceptResend(playerID string, frame uint64, in Input) error {
	return errNotImplemented
}

// SetCamera stores the camera of a player session.
func (l *ReconnectLedger) SetCamera(playerID string, c Camera) error {
	return errNotImplemented
}

// SetPhase stores the match phase of a player session.
func (l *ReconnectLedger) SetPhase(playerID string, phase MatchPhase) error {
	return errNotImplemented
}

// Lookup returns a copy of the session of a player.
func (l *ReconnectLedger) Lookup(playerID string) (Session, bool) {
	return Session{}, false
}

// ResendFrom reports the first frame a reconnecting client must resend.
func (l *ReconnectLedger) ResendFrom(playerID string) uint64 {
	return 0
}

// Pending reports how many players are parked inside their grace period.
func (l *ReconnectLedger) Pending() int {
	return 0
}

// Denials reports how many resume attempts were rejected.
func (l *ReconnectLedger) Denials() int {
	return 0
}

// Grace reports the configured grace period.
func (l *ReconnectLedger) Grace() time.Duration {
	return 0
}

// Snapshot serialises the sessions, the deadlines and the token marks.
func (l *ReconnectLedger) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole ledger state with a payload.
func (l *ReconnectLedger) Restore(data []byte) error {
	return errNotImplemented
}
