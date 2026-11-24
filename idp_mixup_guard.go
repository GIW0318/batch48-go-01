package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Sentinel errors reported by IdpMixupGuard.
var (
	ErrUnknownIdP     = errors.New("unknown identity provider")
	ErrDuplicateIdP   = errors.New("identity provider already registered")
	ErrInvalidRequest = errors.New("invalid authorization request")
	ErrPendingLimit   = errors.New("pending request limit reached")
	ErrUnknownState   = errors.New("unknown authorization state")
	ErrIssuerMismatch = errors.New("callback issuer does not match the initiating provider")
	ErrReplayed       = errors.New("authorization response already consumed")
	ErrExpired        = errors.New("authorization request expired")
	ErrBadSnapshot    = errors.New("bad snapshot")
)

// IdP is one registered identity provider.
type IdP struct {
	ID       string `json:"id"`
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
}

// Request is one hanging authorization request.
type Request struct {
	State       string    `json:"state"`
	SessionID   string    `json:"session_id"`
	IdPID       string    `json:"idp_id"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Result is the outcome of a verified callback.
type Result struct {
	State       string    `json:"state"`
	SessionID   string    `json:"session_id"`
	IdPID       string    `json:"idp_id"`
	Issuer      string    `json:"issuer"`
	Code        string    `json:"code"`
	CompletedAt time.Time `json:"completed_at"`
}

// GuardOptions bounds the authorization ceremony.
type GuardOptions struct {
	MaxPendingPerSession int
	TTL                  time.Duration
	Now                  func() time.Time
}

// IdpMixupGuard tracks hanging authorization requests per session.
type IdpMixupGuard struct {
	mu        sync.Mutex
	ttl       time.Duration
	maxPerSes int
	now       func() time.Time
	idps      map[string]IdP
	pending   map[string]Request
	consumed  map[string]struct{}
	completed int
}

// NewIdpMixupGuard builds an empty guard.
func NewIdpMixupGuard(opts GuardOptions) *IdpMixupGuard {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &IdpMixupGuard{
		ttl:       opts.TTL,
		maxPerSes: opts.MaxPendingPerSession,
		now:       now,
		idps:      make(map[string]IdP),
		pending:   make(map[string]Request),
		consumed:  make(map[string]struct{}),
	}
}

// normalizeIssuer trims trailing slashes so issuers compare equal
// regardless of how the callback spelled them.
func normalizeIssuer(issuer string) string {
	return strings.TrimRight(issuer, "/")
}

func isHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// RegisterIdP registers one identity provider with its issuer and client.
func (g *IdpMixupGuard) RegisterIdP(idpID, issuer, clientID string) error {
	if idpID == "" || clientID == "" || issuer == "" || !isHTTPS(issuer) {
		return ErrInvalidRequest
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.idps[idpID]; ok {
		return ErrDuplicateIdP
	}
	g.idps[idpID] = IdP{ID: idpID, Issuer: normalizeIssuer(issuer), ClientID: clientID}
	return nil
}

// IdP returns one registered provider.
func (g *IdpMixupGuard) IdP(idpID string) (IdP, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	idp, ok := g.idps[idpID]
	if !ok {
		return IdP{}, ErrUnknownIdP
	}
	return idp, nil
}

// pruneExpiredLocked drops every hanging request whose deadline passed.
func (g *IdpMixupGuard) pruneExpiredLocked() {
	if g.ttl <= 0 {
		return
	}
	now := g.now()
	for state, req := range g.pending {
		if now.After(req.ExpiresAt) {
			delete(g.pending, state)
		}
	}
}

// Begin records one hanging authorization request for a session.
func (g *IdpMixupGuard) Begin(sessionID, idpID, redirectURI string) (Request, error) {
	if sessionID == "" || redirectURI == "" || !isHTTPS(redirectURI) {
		return Request{}, ErrInvalidRequest
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.idps[idpID]; !ok {
		return Request{}, ErrUnknownIdP
	}
	g.pruneExpiredLocked()
	if g.maxPerSes > 0 {
		count := 0
		for _, req := range g.pending {
			if req.SessionID == sessionID {
				count++
			}
		}
		if count >= g.maxPerSes {
			return Request{}, ErrPendingLimit
		}
	}
	var state string
	for {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return Request{}, fmt.Errorf("generating state: %w", err)
		}
		state = hex.EncodeToString(buf)
		if _, ok := g.pending[state]; !ok {
			if _, ok := g.consumed[state]; !ok {
				break
			}
		}
	}
	req := Request{
		State:       state,
		SessionID:   sessionID,
		IdPID:       idpID,
		RedirectURI: redirectURI,
		ExpiresAt:   g.now().Add(g.ttl),
	}
	g.pending[state] = req
	return req, nil
}

// Callback consumes one authorization response exactly once.
func (g *IdpMixupGuard) Callback(state, issuer, code string) (Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	req, ok := g.pending[state]
	if !ok {
		if _, used := g.consumed[state]; used {
			return Result{}, ErrReplayed
		}
		return Result{}, ErrUnknownState
	}
	if g.ttl > 0 && g.now().After(req.ExpiresAt) {
		delete(g.pending, state)
		return Result{}, ErrExpired
	}
	if code == "" {
		return Result{}, ErrInvalidRequest
	}
	idp, ok := g.idps[req.IdPID]
	if !ok {
		return Result{}, ErrUnknownIdP
	}
	if normalizeIssuer(issuer) != idp.Issuer {
		return Result{}, ErrIssuerMismatch
	}
	delete(g.pending, state)
	g.consumed[state] = struct{}{}
	g.completed++
	return Result{
		State:       state,
		SessionID:   req.SessionID,
		IdPID:       req.IdPID,
		Issuer:      idp.Issuer,
		Code:        code,
		CompletedAt: g.now(),
	}, nil
}

// Abandon drops one hanging request without consuming a response.
func (g *IdpMixupGuard) Abandon(state string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.pending[state]; !ok {
		return ErrUnknownState
	}
	delete(g.pending, state)
	return nil
}

// Pending reports the hanging requests of one session.
func (g *IdpMixupGuard) Pending(sessionID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneExpiredLocked()
	count := 0
	for _, req := range g.pending {
		if req.SessionID == sessionID {
			count++
		}
	}
	return count
}

// PendingTotal reports every hanging request.
func (g *IdpMixupGuard) PendingTotal() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneExpiredLocked()
	return len(g.pending)
}

// IssuerFor reports the issuer that owns a hanging state.
func (g *IdpMixupGuard) IssuerFor(state string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneExpiredLocked()
	req, ok := g.pending[state]
	if !ok {
		return "", ErrUnknownState
	}
	idp, ok := g.idps[req.IdPID]
	if !ok {
		return "", ErrUnknownIdP
	}
	return idp.Issuer, nil
}

// Completed reports how many callbacks were accepted.
func (g *IdpMixupGuard) Completed() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.completed
}

// snapshot is the serialisable form of the guard state.
type snapshot struct {
	IdPs      []IdP     `json:"idps"`
	Pending   []Request `json:"pending"`
	Consumed  []string  `json:"consumed"`
	Completed int       `json:"completed"`
}

// Snapshot serialises the whole guard state.
func (g *IdpMixupGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := snapshot{
		IdPs:      make([]IdP, 0, len(g.idps)),
		Pending:   make([]Request, 0, len(g.pending)),
		Consumed:  make([]string, 0, len(g.consumed)),
		Completed: g.completed,
	}
	for _, idp := range g.idps {
		snap.IdPs = append(snap.IdPs, idp)
	}
	for _, req := range g.pending {
		snap.Pending = append(snap.Pending, req)
	}
	for state := range g.consumed {
		snap.Consumed = append(snap.Consumed, state)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	return data, nil
}

// Restore rebuilds the guard state from a snapshot.
func (g *IdpMixupGuard) Restore(data []byte) error {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	idps := make(map[string]IdP, len(snap.IdPs))
	for _, idp := range snap.IdPs {
		if idp.ID == "" || idp.Issuer == "" || idp.ClientID == "" {
			return ErrBadSnapshot
		}
		idps[idp.ID] = idp
	}
	pending := make(map[string]Request, len(snap.Pending))
	for _, req := range snap.Pending {
		if req.State == "" || req.SessionID == "" {
			return ErrBadSnapshot
		}
		if _, ok := idps[req.IdPID]; !ok {
			return ErrBadSnapshot
		}
		pending[req.State] = req
	}
	consumed := make(map[string]struct{}, len(snap.Consumed))
	for _, state := range snap.Consumed {
		consumed[state] = struct{}{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.idps = idps
	g.pending = pending
	g.consumed = consumed
	g.completed = snap.Completed
	return nil
}
