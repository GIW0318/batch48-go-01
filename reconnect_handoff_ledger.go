// Package task hands a disconnected player over to the reconnect path: the
// session is parked for a grace period, the resume token is single use and the
// entity stays reserved until the grace period expires.
package task

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// DefaultResendWindow is how many frames behind the last confirmed frame a
// client may resend after a reconnect.
const DefaultResendWindow = 16

// snapshotVersion is the serialised ledger format understood by Restore.
const snapshotVersion = 1

// errNotImplemented was returned by every behaviour that needed work in the
// scaffold; it is retained for source compatibility.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the reconnect ledger.
var (
	ErrInvalidPlayerID   = errors.New("player id is required")
	ErrAlreadyJoined     = errors.New("player already has a session")
	ErrPlayerNotFound    = errors.New("player session does not exist")
	ErrPlayerInGrace     = errors.New("player session is already parked in grace")
	ErrTokenInvalid      = errors.New("resume token is unknown")
	ErrTokenUsed         = errors.New("resume token was already used")
	ErrTokenExpired      = errors.New("resume token has expired")
	ErrMatchEnded        = errors.New("match already ended")
	ErrResendOutOfWindow = errors.New("frame is outside the resend window")
	ErrConfirmRegressed  = errors.New("confirmed frame must not move backwards")
	ErrBadSnapshot       = errors.New("snapshot payload is invalid")
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
//
// Every map and the denial counter are advanced under mu only. Resume performs
// token validation and entity re-occupation in a single critical section so two
// gateway connections can never walk away with the same live session.
type ReconnectLedger struct {
	mu           sync.Mutex
	grace        time.Duration
	resendWindow int
	seq          uint64
	sessions     map[string]*Session
	deadlines    map[string]time.Time
	tokens       map[ResumeToken]string
	used         map[ResumeToken]bool
	expired      map[ResumeToken]bool
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
		expired:      map[ResumeToken]bool{},
	}
}

// Join registers a player for a match.
func (l *ReconnectLedger) Join(playerID, team string, phase MatchPhase) error {
	if playerID == "" {
		return ErrInvalidPlayerID
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.sessions[playerID]; exists {
		return ErrAlreadyJoined
	}

	l.sessions[playerID] = &Session{
		PlayerID:   playerID,
		Team:       team,
		Phase:      phase,
		ResendFrom: 1,
	}
	return nil
}

// Detach parks a player session and hands out a resume token.
func (l *ReconnectLedger) Detach(playerID string) (ResumeToken, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok {
		return "", ErrPlayerNotFound
	}
	if s.InGrace {
		return "", ErrPlayerInGrace
	}

	token := l.issueTokenLocked(playerID)
	s.InGrace = true
	s.ConnID = ""
	l.deadlines[playerID] = time.Now().Add(l.grace)
	return token, nil
}

// Resume restores a parked session for the given token.
func (l *ReconnectLedger) Resume(token ResumeToken, conn Conn) (*Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// deny accounts for one rejected resume attempt. The denial counter is the
	// only state a rejected call advances; sessions and tokens are untouched
	// apart from explicitly mandated terminal/expiry bookkeeping.
	deny := func(err error) (*Session, error) {
		l.denials++
		return nil, err
	}

	playerID, known := l.tokens[token]
	if !known {
		return deny(ErrTokenInvalid)
	}
	if l.used[token] {
		return deny(ErrTokenUsed)
	}
	if l.expired[token] {
		return deny(ErrTokenExpired)
	}

	s, ok := l.sessions[playerID]
	if !ok || s == nil {
		// Dangling, non-terminal token (for example left by a hand crafted
		// snapshot). It can never resume anything.
		return deny(ErrTokenInvalid)
	}

	// The timer may not have reaped the park yet, but the deadline itself has
	// passed: classify the ticket read only as expired. Rejected resumes do not
	// mutate session state; Expire remains the single entity release path.
	if s.InGrace {
		if deadline, ok := l.deadlines[playerID]; ok && !time.Now().Before(deadline) {
			return deny(ErrTokenExpired)
		}
	}

	if s.Phase == PhaseEnded {
		// The match is over. Report the terminal state and drop the ticket so
		// it can never pull the player back into the finished match.
		l.deleteTokenLocked(token)
		return deny(ErrMatchEnded)
	}

	if !s.InGrace {
		// The account/session is already occupied by a winning resume, possibly
		// presented with a different ticket. Fence the loser out atomically
		// without rebinding or otherwise mutating the live session.
		return deny(ErrTokenUsed)
	}

	// Winning transition: consume the one time token and re-occupy the entity
	// in the same critical section.
	l.used[token] = true
	s.InGrace = false
	s.ConnID = conn.ID
	s.ResendFrom = s.LastFrame + 1
	delete(l.deadlines, playerID)

	clone := *s
	return &clone, nil
}

// Abandon drops a session and releases its slot immediately.
func (l *ReconnectLedger) Abandon(playerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.removePlayerLocked(playerID)
}

// Expire releases every session whose grace period ended at the given instant.
func (l *ReconnectLedger) Expire(now time.Time) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	due := make([]string, 0)
	for playerID, s := range l.sessions {
		if s == nil || !s.InGrace {
			continue
		}
		deadline, ok := l.deadlines[playerID]
		if !ok {
			continue
		}
		if !now.Before(deadline) {
			due = append(due, playerID)
		}
	}

	sort.Strings(due)
	for _, playerID := range due {
		l.releaseForExpiryLocked(playerID)
	}
	return due
}

// Confirm advances the last confirmed frame of a player.
func (l *ReconnectLedger) Confirm(playerID string, frame uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok {
		return ErrPlayerNotFound
	}
	if frame < s.LastFrame {
		return ErrConfirmRegressed
	}

	s.LastFrame = frame
	s.ResendFrom = frame + 1
	return nil
}

// AcceptResend accepts one resent input inside the resend window.
func (l *ReconnectLedger) AcceptResend(playerID string, frame uint64, in Input) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok {
		return ErrPlayerNotFound
	}

	// Only frames strictly after the last confirmed frame are eligible. The
	// upper window edge (confirmed + resendWindow) is accepted as well.
	if frame <= s.LastFrame {
		return ErrResendOutOfWindow
	}
	if frame-s.LastFrame > uint64(l.resendWindow) {
		return ErrResendOutOfWindow
	}

	s.Resent++
	return nil
}

// SetCamera stores the camera of a player session.
func (l *ReconnectLedger) SetCamera(playerID string, c Camera) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok {
		return ErrPlayerNotFound
	}
	s.Camera = c
	return nil
}

// SetPhase stores the match phase of a player session.
func (l *ReconnectLedger) SetPhase(playerID string, phase MatchPhase) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok {
		return ErrPlayerNotFound
	}
	s.Phase = phase
	return nil
}

// Lookup returns a copy of the session of a player.
func (l *ReconnectLedger) Lookup(playerID string) (Session, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok || s == nil {
		return Session{}, false
	}
	return *s, true
}

// ResendFrom reports the first frame a reconnecting client must resend.
func (l *ReconnectLedger) ResendFrom(playerID string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.sessions[playerID]
	if !ok || s == nil {
		return 0
	}
	return s.LastFrame + 1
}

// Pending reports how many players are parked inside their grace period.
func (l *ReconnectLedger) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	pending := 0
	for _, s := range l.sessions {
		if s != nil && s.InGrace {
			pending++
		}
	}
	return pending
}

// Denials reports how many resume attempts were rejected.
func (l *ReconnectLedger) Denials() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.denials
}

// Grace reports the configured grace period.
func (l *ReconnectLedger) Grace() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.grace
}

// issueTokenLocked mints a unique opaque token and binds it to playerID.
func (l *ReconnectLedger) issueTokenLocked(playerID string) ResumeToken {
	for {
		l.seq++

		var raw [16]byte
		tail := ""
		if _, err := rand.Read(raw[:]); err == nil {
			tail = hex.EncodeToString(raw[:])
		} else {
			tail = fmt.Sprintf("%016x", time.Now().UnixNano())
		}
		token := ResumeToken(fmt.Sprintf("rt-%d-%s", l.seq, tail))
		if _, exists := l.tokens[token]; exists {
			continue
		}

		l.tokens[token] = playerID
		l.used[token] = false
		l.expired[token] = false
		return token
	}
}

// deleteTokenLocked removes every trace of one ticket.
func (l *ReconnectLedger) deleteTokenLocked(token ResumeToken) {
	delete(l.tokens, token)
	delete(l.used, token)
	delete(l.expired, token)
}

// removePlayerLocked releases an entity and invalidates all of its tickets.
func (l *ReconnectLedger) removePlayerLocked(playerID string) {
	delete(l.sessions, playerID)
	delete(l.deadlines, playerID)
	for token, owner := range l.tokens {
		if owner == playerID {
			l.deleteTokenLocked(token)
		}
	}
}

// releaseForExpiryLocked frees the entity while retaining its tickets marked as
// expired, so a later Resume can report ErrTokenExpired instead of
// ErrTokenInvalid.
func (l *ReconnectLedger) releaseForExpiryLocked(playerID string) {
	for token, owner := range l.tokens {
		if owner == playerID {
			l.expired[token] = true
		}
	}
	delete(l.sessions, playerID)
	delete(l.deadlines, playerID)
}

// snapshotData is the serialisable ledger state.
type snapshotData struct {
	Version      int                  `json:"version"`
	GraceNanos   int64                `json:"grace_nanos"`
	ResendWindow int                  `json:"resend_window"`
	Seq          uint64               `json:"seq"`
	Denials      int                  `json:"denials"`
	Sessions     []Session            `json:"sessions"`
	Deadlines    map[string]time.Time `json:"deadlines"`
	Tokens       map[string]string    `json:"tokens"`
	Used         map[string]bool      `json:"used"`
	Expired      map[string]bool      `json:"expired"`
}

// snapshotEnvelope guards the payload against corruption.
type snapshotEnvelope struct {
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Snapshot serialises the sessions, the deadlines and the token marks.
func (l *ReconnectLedger) Snapshot() ([]byte, error) {
	l.mu.Lock()

	data := snapshotData{
		Version:      snapshotVersion,
		GraceNanos:   int64(l.grace),
		ResendWindow: l.resendWindow,
		Seq:          l.seq,
		Denials:      l.denials,
		Sessions:     make([]Session, 0, len(l.sessions)),
		Deadlines:    make(map[string]time.Time, len(l.deadlines)),
		Tokens:       make(map[string]string, len(l.tokens)),
		Used:         make(map[string]bool, len(l.used)),
		Expired:      make(map[string]bool, len(l.expired)),
	}
	for _, s := range l.sessions {
		if s != nil {
			data.Sessions = append(data.Sessions, *s)
		}
	}
	for playerID, deadline := range l.deadlines {
		data.Deadlines[playerID] = deadline
	}
	for token, playerID := range l.tokens {
		data.Tokens[string(token)] = playerID
	}
	for token, marked := range l.used {
		if marked {
			data.Used[string(token)] = true
		}
	}
	for token, marked := range l.expired {
		if marked {
			data.Expired[string(token)] = true
		}
	}
	l.mu.Unlock()

	// Emit sessions in a stable order so equivalent ledgers snapshot to the
	// same bytes.
	sort.Slice(data.Sessions, func(i, j int) bool {
		return data.Sessions[i].PlayerID < data.Sessions[j].PlayerID
	})

	payload, err := json.Marshal(&data)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)

	envelope := snapshotEnvelope{
		Checksum: hex.EncodeToString(sum[:]),
		Payload:  payload,
	}
	out, err := json.Marshal(&envelope)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Restore replaces the whole ledger state with a payload.
func (l *ReconnectLedger) Restore(data []byte) error {
	snap, err := decodeSnapshot(data)
	if err != nil {
		return ErrBadSnapshot
	}

	sessions := make(map[string]*Session, len(snap.Sessions))
	for i := range snap.Sessions {
		s := snap.Sessions[i]
		if s.PlayerID == "" {
			return ErrBadSnapshot
		}
		if _, dup := sessions[s.PlayerID]; dup {
			return ErrBadSnapshot
		}
		if s.Phase < PhaseLobby || s.Phase > PhaseEnded {
			return ErrBadSnapshot
		}
		// Keep the resend watermark consistent with the confirmed frame.
		s.ResendFrom = s.LastFrame + 1
		clone := s
		sessions[s.PlayerID] = &clone
	}

	deadlines := make(map[string]time.Time, len(snap.Deadlines))
	for playerID, deadline := range snap.Deadlines {
		if playerID == "" || deadline.IsZero() {
			return ErrBadSnapshot
		}
		deadlines[playerID] = deadline
	}

	tokens := make(map[ResumeToken]string, len(snap.Tokens))
	for token, playerID := range snap.Tokens {
		if token == "" || playerID == "" {
			return ErrBadSnapshot
		}
		tokens[ResumeToken(token)] = playerID
	}

	used := make(map[ResumeToken]bool, len(snap.Used))
	for token, marked := range snap.Used {
		if marked {
			used[ResumeToken(token)] = true
		}
	}

	expired := make(map[ResumeToken]bool, len(snap.Expired))
	for token, marked := range snap.Expired {
		if marked {
			expired[ResumeToken(token)] = true
		}
	}

	grace := time.Duration(snap.GraceNanos)
	resendWindow := snap.ResendWindow
	if resendWindow <= 0 {
		resendWindow = DefaultResendWindow
	}

	// All decoding and validation succeeded: only now swap the live state under
	// the lock, so a bad payload leaves the running ledger untouched.
	l.mu.Lock()
	l.grace = grace
	l.resendWindow = resendWindow
	l.seq = snap.Seq
	l.denials = snap.Denials
	l.sessions = sessions
	l.deadlines = deadlines
	l.tokens = tokens
	l.used = used
	l.expired = expired
	l.mu.Unlock()
	return nil
}

// decodeSnapshot authenticates and decodes a snapshot without touching ledger
// state.
func decodeSnapshot(raw []byte) (snapshotData, error) {
	var data snapshotData
	if len(raw) == 0 {
		return data, ErrBadSnapshot
	}

	var envelope snapshotEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return data, ErrBadSnapshot
	}
	if envelope.Checksum == "" || len(envelope.Payload) == 0 {
		return data, ErrBadSnapshot
	}

	sum := sha256.Sum256(envelope.Payload)
	if !equalFold(envelope.Checksum, hex.EncodeToString(sum[:])) {
		return data, ErrBadSnapshot
	}

	if err := json.Unmarshal(envelope.Payload, &data); err != nil {
		return snapshotData{}, ErrBadSnapshot
	}
	if data.Version != snapshotVersion {
		return snapshotData{}, ErrBadSnapshot
	}
	if data.Denials < 0 {
		return snapshotData{}, ErrBadSnapshot
	}
	return data, nil
}

// equalFold compares two lowercase hex checksums without leaking timing
// information.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x := a[i]
		y := b[i]
		if 'A' <= x && x <= 'F' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'F' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
