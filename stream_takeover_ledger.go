// Package task implements the takeover ledger of a stateful TCP proxy. A
// client that loses its source port can resume the same upstream session with
// a resume token; the ledger tracks the upstream sent offset, the client
// acknowledged offset and the replay window so the new connection can be
// caught up without gaps or duplicates.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Errors reported by the ledger.
var (
	ErrInvalidArgument  = errors.New("session id and resume token must not be empty")
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

	// ErrTokenBusy and ErrTokenInUse are alternate names for the token
	// occupancy rejection. They remain distinct from ErrTokenExpired.
	ErrTokenBusy  = ErrDuplicateToken
	ErrTokenInUse = ErrDuplicateToken
)

// snapshotVersion is the only snapshot format understood by this build.
const snapshotVersion = 1

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

	ledger *TakeoverLedger
	epoch  uint64
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

	// ExpiredTokens counts rejected takeovers caused by an expired token.
	ExpiredTokens int
	// DuplicateTokens counts registrations that tried to bind a token already
	// owned by another session.
	DuplicateTokens int
}

// sessionState is the immutable-by-convention critical-section state for one
// session. replay covers the half-open offset interval [acked, sent).
type sessionState struct {
	id        string
	token     string
	expiresAt time.Time

	sent   uint64
	acked  uint64
	replay []byte

	generation uint64
	conn       Conn
	connID     string
	fenced     bool
	handle     *Session
}

// TakeoverLedger holds the session table, the token index and the replay
// windows. Every map, window and counter is guarded by mu.
type TakeoverLedger struct {
	mu       sync.Mutex
	policy   Policy
	sessions map[string]*sessionState
	tokens   map[string]string
	stats    LedgerStats
	epoch    uint64
}

// NewTakeoverLedger builds a ledger with the supplied policy.
func NewTakeoverLedger(p Policy) *TakeoverLedger {
	if p.MaxSessions < 0 {
		p.MaxSessions = 0
	}
	return &TakeoverLedger{
		policy:   p,
		sessions: make(map[string]*sessionState),
		tokens:   make(map[string]string),
	}
}

// Register creates a session and binds a resume token to it.
func (l *TakeoverLedger) Register(sessionID, token string) error {
	if sessionID == "" || token == "" {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.sessions[sessionID]; exists {
		return ErrDuplicateSession
	}
	if owner, exists := l.tokens[token]; exists && owner != sessionID {
		l.stats.DuplicateTokens++
		return ErrDuplicateToken
	}
	if l.policy.MaxSessions >= 0 && len(l.sessions) >= l.policy.MaxSessions {
		return ErrSessionLimit
	}

	var expiresAt time.Time
	if l.policy.TokenTTL > 0 {
		expiresAt = time.Now().Add(l.policy.TokenTTL)
	}

	state := &sessionState{
		id:        sessionID,
		token:     token,
		expiresAt: expiresAt,
		replay:    make([]byte, 0),
	}
	state.handle = l.newHandleLocked(state, nil)
	l.sessions[sessionID] = state
	l.tokens[token] = sessionID
	l.stats.Registered++
	return nil
}

// Takeover attaches a new connection to the session bound to the token and
// returns a handle for it.
func (l *TakeoverLedger) Takeover(token string, conn Conn) (*Session, error) {
	if conn == nil {
		return nil, ErrNilConn
	}
	connID := conn.ConnID()

	l.mu.Lock()
	defer l.mu.Unlock()

	sessionID, ok := l.tokens[token]
	if !ok {
		return nil, ErrUnknownToken
	}
	state, ok := l.sessions[sessionID]
	if !ok || state == nil {
		delete(l.tokens, token)
		return nil, ErrUnknownToken
	}
	if !state.expiresAt.IsZero() && !time.Now().Before(state.expiresAt) {
		l.stats.ExpiredTokens++
		return nil, ErrTokenExpired
	}

	if state.handle != nil && !state.handle.ReadOnly && state.generation > 0 {
		state.handle.ReadOnly = true
		l.stats.FencedConns++
	}

	state.generation++
	state.conn = conn
	state.connID = connID
	state.fenced = false
	state.handle = l.newHandleLocked(state, conn)
	l.stats.Takeovers++
	return state.handle, nil
}

// Ack advances the acknowledged offset of the session.
func (l *TakeoverLedger) Ack(sessionID string, offset uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.sessions[sessionID]
	if !ok || state == nil {
		return ErrSessionNotFound
	}
	return l.ackLocked(state, offset)
}

// Fence puts the session into the read only state until a new takeover.
func (l *TakeoverLedger) Fence(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.sessions[sessionID]
	if !ok || state == nil {
		return
	}
	state.fenced = true
	if state.handle != nil && !state.handle.ReadOnly {
		state.handle.ReadOnly = true
		l.stats.FencedConns++
	}
}

// Append records bytes produced by the upstream side and returns the new sent
// offset.
func (l *TakeoverLedger) Append(sessionID string, payload []byte) (uint64, error) {
	if len(payload) == 0 {
		return 0, ErrEmptyPayload
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.sessions[sessionID]
	if !ok || state == nil {
		return 0, ErrSessionNotFound
	}
	if state.fenced {
		return state.sent, ErrFenced
	}

	payloadLen := uint64(len(payload))
	windowLen := uint64(len(state.replay))
	if windowLen+payloadLen > l.policy.MaxReplayBytes {
		return state.sent, ErrReplayFull
	}

	state.replay = append(state.replay, payload...)
	state.sent += payloadLen
	l.syncHandleOffsetsLocked(state)
	return state.sent, nil
}

// Replay returns the buffered bytes starting at from, clamped to the
// acknowledged offset.
func (l *TakeoverLedger) Replay(sessionID string, from uint64) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.sessions[sessionID]
	if !ok || state == nil {
		return nil, ErrSessionNotFound
	}
	if from > state.sent {
		return nil, ErrReplayGap
	}

	start := from
	if start < state.acked {
		start = state.acked
	}
	index := start - state.acked
	if index > uint64(len(state.replay)) {
		return nil, ErrReplayGap
	}

	out := make([]byte, len(state.replay[index:]))
	copy(out, state.replay[index:])
	return out, nil
}

// Session returns a handle for the session without attaching a connection.
func (l *TakeoverLedger) Session(sessionID string) (*Session, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.sessions[sessionID]
	if !ok || state == nil {
		return nil, false
	}
	l.syncHandleOffsetsLocked(state)
	handleCopy := *state.handle
	return &handleCopy, true
}

// Sessions lists the registered session ids in ascending order.
func (l *TakeoverLedger) Sessions() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	ids := make([]string, 0, len(l.sessions))
	for id := range l.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Stats returns a copy of the ledger counters.
func (l *TakeoverLedger) Stats() LedgerStats {
	l.mu.Lock()
	defer l.mu.Unlock()

	stats := l.stats
	stats.Sessions = len(l.sessions)

	var buffered uint64
	for _, state := range l.sessions {
		if state != nil {
			buffered += uint64(len(state.replay))
		}
	}
	stats.BufferedBytes = buffered
	return stats
}

// Snapshot serialises the session table, the replay windows and the counters.
func (l *TakeoverLedger) Snapshot() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	policy := l.policy
	data := snapshotData{
		Version:  snapshotVersion,
		Policy:   &policy,
		Stats:    l.statsLocked(),
		Sessions: make([]snapshotSession, 0, len(l.sessions)),
	}

	for _, id := range l.sessionIDsLocked() {
		state := l.sessions[id]
		record := snapshotSession{
			SessionID:  state.id,
			Token:      state.token,
			ExpiresAt:  state.expiresAt,
			Sent:       state.sent,
			Acked:      state.acked,
			Replay:     append([]byte(nil), state.replay...),
			Generation: state.generation,
			Fenced:     state.fenced,
			ConnID:     state.connID,
			HasConn:    state.conn != nil,
		}
		data.Sessions = append(data.Sessions, record)
	}

	raw, err := json.Marshal(&data)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// Restore replaces the ledger state with a snapshot.
func (l *TakeoverLedger) Restore(raw []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var data snapshotData
	if len(raw) == 0 {
		return ErrBadSnapshot
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("%w: decode snapshot: %v", ErrBadSnapshot, err)
	}

	policy := l.policy
	if data.Policy != nil {
		policy = *data.Policy
	}
	if policy.MaxSessions < 0 {
		policy.MaxSessions = 0
	}
	if err := validateSnapshot(&data, policy); err != nil {
		return err
	}

	l.epoch++
	nextSessions := make(map[string]*sessionState, len(data.Sessions))
	nextTokens := make(map[string]string, len(data.Sessions))

	for _, record := range data.Sessions {
		replay := append([]byte(nil), record.Replay...)
		state := &sessionState{
			id:         record.SessionID,
			token:      record.Token,
			expiresAt:  record.ExpiresAt,
			sent:       record.Sent,
			acked:      record.Acked,
			replay:     replay,
			generation: record.Generation,
			connID:     record.ConnID,
			fenced:     record.Fenced,
		}
		if record.HasConn {
			state.conn = restoredConn{id: record.ConnID}
		}
		state.handle = l.newHandleLocked(state, state.conn)
		nextSessions[state.id] = state
		nextTokens[state.token] = state.id
	}

	l.policy = policy
	l.sessions = nextSessions
	l.tokens = nextTokens
	l.stats = data.Stats
	return nil
}

// Ack advances the acknowledged offset through the handle generation.
func (s *Session) Ack(offset uint64) error {
	if s == nil || s.ledger == nil {
		return ErrSessionNotFound
	}

	l := s.ledger
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.sessions[s.SessionID]
	if !ok || state == nil {
		return ErrSessionNotFound
	}
	if s.epoch != l.epoch || s.Generation != state.generation || s.ReadOnly || state.fenced {
		s.ReadOnly = true
		l.stats.DroppedAcks++
		return ErrFenced
	}

	if err := l.ackLocked(state, offset); err != nil {
		return err
	}
	// A successful ack keeps the handle's exported view current.
	s.Acked = state.acked
	s.Sent = state.sent
	return nil
}

// Replay returns the replay window through the handle generation. Reads remain
// available through a read-only/old-generation handle, but are always clamped
// to the current acknowledged point.
func (s *Session) Replay(from uint64) ([]byte, error) {
	if s == nil || s.ledger == nil {
		return nil, ErrSessionNotFound
	}
	return s.ledger.Replay(s.SessionID, from)
}

type snapshotData struct {
	Version  int               `json:"version"`
	Policy   *Policy           `json:"policy,omitempty"`
	Stats    LedgerStats       `json:"stats"`
	Sessions []snapshotSession `json:"sessions"`
}

type snapshotSession struct {
	SessionID  string    `json:"session_id"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Sent       uint64    `json:"sent"`
	Acked      uint64    `json:"acked"`
	Replay     []byte    `json:"replay"`
	Generation uint64    `json:"generation"`
	Fenced     bool      `json:"fenced"`
	ConnID     string    `json:"conn_id,omitempty"`
	HasConn    bool      `json:"has_conn,omitempty"`
}

type restoredConn struct {
	id string
}

func (c restoredConn) ConnID() string { return c.id }

func (l *TakeoverLedger) newHandleLocked(state *sessionState, conn Conn) *Session {
	handle := &Session{
		SessionID:  state.id,
		Token:      state.token,
		Generation: state.generation,
		Sent:       state.sent,
		Acked:      state.acked,
		Conn:       conn,
		ReadOnly:   state.fenced,
		ledger:     l,
		epoch:      l.epoch,
	}
	state.handle = handle
	return handle
}

func (l *TakeoverLedger) syncHandleOffsetsLocked(state *sessionState) {
	if state.handle == nil {
		state.handle = l.newHandleLocked(state, state.conn)
		return
	}
	state.handle.Sent = state.sent
	state.handle.Acked = state.acked
}

func (l *TakeoverLedger) ackLocked(state *sessionState, offset uint64) error {
	if offset > state.sent {
		return ErrAckBeyondSent
	}
	if offset < state.acked {
		l.stats.StaleAcks++
		return ErrStaleAck
	}
	if offset == state.acked {
		return nil
	}

	trim := offset - state.acked
	if trim > uint64(len(state.replay)) {
		// This should be unreachable when the invariant holds. Refuse to move
		// the offset without a contiguous replay window.
		return ErrReplayGap
	}

	remaining := make([]byte, len(state.replay[trim:]))
	copy(remaining, state.replay[trim:])
	state.replay = remaining
	state.acked = offset
	l.syncHandleOffsetsLocked(state)
	return nil
}

func (l *TakeoverLedger) statsLocked() LedgerStats {
	stats := l.stats
	stats.Sessions = len(l.sessions)

	var buffered uint64
	for _, state := range l.sessions {
		if state != nil {
			buffered += uint64(len(state.replay))
		}
	}
	stats.BufferedBytes = buffered
	return stats
}

func (l *TakeoverLedger) sessionIDsLocked() []string {
	ids := make([]string, 0, len(l.sessions))
	for id := range l.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func validateSnapshot(data *snapshotData, policy Policy) error {
	if data.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, data.Version)
	}
	if len(data.Sessions) > policy.MaxSessions {
		return fmt.Errorf("%w: session limit exceeded", ErrBadSnapshot)
	}
	if data.Stats.Registered < 0 || data.Stats.Takeovers < 0 ||
		data.Stats.FencedConns < 0 || data.Stats.DroppedAcks < 0 ||
		data.Stats.StaleAcks < 0 || data.Stats.ExpiredTokens < 0 ||
		data.Stats.DuplicateTokens < 0 {
		return fmt.Errorf("%w: negative counter", ErrBadSnapshot)
	}

	seenIDs := make(map[string]struct{}, len(data.Sessions))
	seenTokens := make(map[string]struct{}, len(data.Sessions))

	for i := range data.Sessions {
		record := &data.Sessions[i]
		if record.SessionID == "" || record.Token == "" {
			return fmt.Errorf("%w: empty session id or token", ErrBadSnapshot)
		}
		if _, dup := seenIDs[record.SessionID]; dup {
			return fmt.Errorf("%w: duplicate session %q", ErrBadSnapshot, record.SessionID)
		}
		if _, dup := seenTokens[record.Token]; dup {
			return fmt.Errorf("%w: duplicate token for %q", ErrBadSnapshot, record.SessionID)
		}
		seenIDs[record.SessionID] = struct{}{}
		seenTokens[record.Token] = struct{}{}

		if record.Acked > record.Sent {
			return fmt.Errorf("%w: acked exceeds sent for %q", ErrBadSnapshot, record.SessionID)
		}
		unacked := record.Sent - record.Acked
		if uint64(len(record.Replay)) != unacked {
			return fmt.Errorf("%w: replay window length mismatch for %q", ErrBadSnapshot, record.SessionID)
		}
		if uint64(len(record.Replay)) > policy.MaxReplayBytes {
			return fmt.Errorf("%w: replay window exceeds policy for %q", ErrBadSnapshot, record.SessionID)
		}
	}
	return nil
}
