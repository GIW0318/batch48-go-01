package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

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
	}
}

// RegisterIdP registers one identity provider with its issuer and client.
func (g *IdpMixupGuard) RegisterIdP(idpID, issuer, clientID string) error {
	return errNotImplemented
}

// IdP returns one registered provider.
func (g *IdpMixupGuard) IdP(idpID string) (IdP, error) {
	return IdP{}, errNotImplemented
}

// Begin records one hanging authorization request for a session.
func (g *IdpMixupGuard) Begin(sessionID, idpID, redirectURI string) (Request, error) {
	return Request{}, errNotImplemented
}

// Callback consumes one authorization response exactly once.
func (g *IdpMixupGuard) Callback(state, issuer, code string) (Result, error) {
	return Result{}, errNotImplemented
}

// Abandon drops one hanging request without consuming a response.
func (g *IdpMixupGuard) Abandon(state string) error {
	return errNotImplemented
}

// Pending reports the hanging requests of one session.
func (g *IdpMixupGuard) Pending(sessionID string) int {
	return 0
}

// PendingTotal reports every hanging request.
func (g *IdpMixupGuard) PendingTotal() int {
	return 0
}

// IssuerFor reports the issuer that owns a hanging state.
func (g *IdpMixupGuard) IssuerFor(state string) (string, error) {
	return "", errNotImplemented
}

// Completed reports how many callbacks were accepted.
func (g *IdpMixupGuard) Completed() int {
	return 0
}

// Snapshot serialises the whole guard state.
func (g *IdpMixupGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard state from a snapshot.
func (g *IdpMixupGuard) Restore(data []byte) error {
	return errNotImplemented
}
