// Package task implements the takeover ledger of a stateful TCP proxy. A
// client that loses its source port can resume the same upstream session with
// a resume token; the ledger tracks the upstream sent offset, the client
// acknowledged offset and the replay window so the new connection can be
// caught up without gaps or duplicates.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the ledger.
var (
	ErrInvalidArgument = errors.New("session id and resume token must not be empty")
	ErrDuplicateSession = errors.New("session id already registered")
	ErrDuplicateToken   = errors.New("resume token already in use")
	ErrSessionLimit     = errors.New("session limit reached")
	ErrUnknownToken     = errors.New("unknown resume token")
	ErrTokenExpired     = errors.New("resume token expired")
	ErrSessionNotFound  = errors.New("session not found")
	ErrNilConn          = errors.New("nil connection")
	ErrEmptyPayload     = errors.New("empty payload")
	ErrReplayFull       = errors.New("replay buffer full")
	ErrAckBeyondSent    = errors.New("ack offset beyond sent data")
	ErrStaleAck         = errors.New("stale ack offset")
	ErrReplayGap        = errors.New("replay offset beyond sent data")
	ErrFenced           = errors.New("session connection is fenced")
	ErrBadSnapshot      = errors.New("bad snapshot")
)

// Conn is one client transport attached to a session. Implementations are
// supplied by the proxy front end.
type Conn interface {
	ConnID() string
}

// Policy bounds the ledger.
type Policy struct {
	MaxSessions    int
	MaxReplayBytes uint64
	TokenTTL       time.Duration
}

// Session is the handle returned by Takeover. A handle carries the generation
// it was created for: once another connection takes over, the handle becomes
// read only and its write side must be refused.
type Session struct {
	SessionID  string
	Token      string
	Generation uint64
	Sent       uint64
	Acked      uint64
	Conn       Conn
	ReadOnly   bool
	ledger     *TakeoverLedger
}

// LedgerStats counts what the ledger carried so far.
type LedgerStats struct {
	Sessions      int
	Registered    int
	Takeovers     int
	FencedConns   int
	DroppedAcks   int
	StaleAcks     int
	BufferedBytes uint64
}

// TakeoverLedger holds the session table, the token index and the replay
// windows.
type TakeoverLedger struct {
	mu     sync.Mutex
	policy Policy
	stats  LedgerStats
}

// NewTakeoverLedger builds a ledger with the supplied policy.
func NewTakeoverLedger(p Policy) *TakeoverLedger {
	return &TakeoverLedger{policy: p}
}

// Register creates a session and binds a resume token to it.
func (l *TakeoverLedger) Register(sessionID, token string) error {
	return errNotImplemented
}

// Takeover attaches a new connection to the session bound to the token and
// returns a handle for it.
func (l *TakeoverLedger) Takeover(token string, conn Conn) (*Session, error) {
	return nil, errNotImplemented
}

// Ack advances the acknowledged offset of the session.
func (l *TakeoverLedger) Ack(sessionID string, offset uint64) error {
	return errNotImplemented
}

// Fence puts the session into the read only state until a new takeover.
func (l *TakeoverLedger) Fence(sessionID string) {
}

// Append records bytes produced by the upstream side and returns the new sent
// offset.
func (l *TakeoverLedger) Append(sessionID string, payload []byte) (uint64, error) {
	return 0, errNotImplemented
}

// Replay returns the buffered bytes starting at from, clamped to the
// acknowledged offset.
func (l *TakeoverLedger) Replay(sessionID string, from uint64) ([]byte, error) {
	return nil, errNotImplemented
}

// Session returns a handle for the session without attaching a connection.
func (l *TakeoverLedger) Session(sessionID string) (*Session, bool) {
	return nil, false
}

// Sessions lists the registered session ids in ascending order.
func (l *TakeoverLedger) Sessions() []string {
	return nil
}

// Stats returns a copy of the ledger counters.
func (l *TakeoverLedger) Stats() LedgerStats {
	return LedgerStats{}
}

// Snapshot serialises the session table, the replay windows and the counters.
func (l *TakeoverLedger) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the ledger state with a snapshot.
func (l *TakeoverLedger) Restore(data []byte) error {
	return errNotImplemented
}

// Ack advances the acknowledged offset through the handle generation.
func (s *Session) Ack(offset uint64) error {
	return errNotImplemented
}

// Replay returns the replay window through the handle generation.
func (s *Session) Replay(from uint64) ([]byte, error) {
	return nil, errNotImplemented
}
