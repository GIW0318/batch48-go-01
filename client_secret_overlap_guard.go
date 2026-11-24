// Package task keeps two client secret generations usable at the same time
// during rotation, distinguishes expired from revoked secrets and guarantees a
// single winning rotation per client and instant.
package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors returned by the guard.
var (
	ErrBlankClient        = errors.New("blank client")
	ErrClientExists       = errors.New("client exists")
	ErrClientNotFound     = errors.New("client not found")
	ErrClientLimit        = errors.New("client limit")
	ErrGenerationNotFound = errors.New("generation not found")
	ErrGenerationRetired  = errors.New("generation retired")
	ErrSecretExpired      = errors.New("secret expired")
	ErrSecretRevoked      = errors.New("secret revoked")
	ErrSecretRetired      = errors.New("secret retired")
	ErrSecretUnknown      = errors.New("secret unknown")
	ErrBadSecret          = errors.New("bad secret")
	ErrBadSnapshot        = errors.New("bad snapshot")
)

// Generation statuses.
const (
	StatusActive   = "active"
	StatusPrevious = "previous"
	StatusRetired  = "retired"
	StatusRevoked  = "revoked"
	StatusExpired  = "expired"
)

// Verdict reasons.
const (
	ReasonOK            = "ok"
	ReasonNoMatch       = "no_match"
	ReasonClientUnknown = "client_unknown"
	ReasonSecretExpired = "secret_expired"
	ReasonSecretRevoked = "secret_revoked"
	ReasonSecretRetired = "secret_retired"
)

// SecretLimits bounds the overlap window and the number of tracked clients.
type SecretLimits struct {
	OverlapWindow  time.Duration
	MaxGenerations int
	MaxClients     int
}

// Generation is one client secret generation.
type Generation struct {
	ClientID  string
	Gen       int
	Secret    string
	Status    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Verdict is the outcome of one verification attempt.
type Verdict struct {
	OK     bool
	Gen    int
	Reason string
}

type clientSecrets struct {
	gens       []Generation
	current    int
	lastRotate time.Time
	counter    int
}

// ClientSecretOverlapGuard keeps the generation table of every client.
type ClientSecretOverlapGuard struct {
	mu      sync.Mutex
	limits  SecretLimits
	clients map[string]*clientSecrets
}

// NewClientSecretOverlapGuard builds a guard with the supplied limits.
func NewClientSecretOverlapGuard(limits SecretLimits) *ClientSecretOverlapGuard {
	return &ClientSecretOverlapGuard{
		limits:  limits,
		clients: make(map[string]*clientSecrets),
	}
}

// Register opens the secret table of one client with its first generation.
func (g *ClientSecretOverlapGuard) Register(clientID string, now time.Time) (Generation, error) {
	return Generation{}, errNotImplemented
}

// Rotate opens the next generation and moves the previous one into overlap.
func (g *ClientSecretOverlapGuard) Rotate(clientID string, now time.Time) (Generation, error) {
	return Generation{}, errNotImplemented
}

// Activate makes one generation the active member of the overlap window.
func (g *ClientSecretOverlapGuard) Activate(clientID string, gen int) error {
	return errNotImplemented
}

// Verify checks one presented client secret against the usable window.
func (g *ClientSecretOverlapGuard) Verify(clientID, secret string, now time.Time) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// RevokeOne retires a single generation before its overlap window ends.
func (g *ClientSecretOverlapGuard) RevokeOne(clientID string, gen int) error {
	return errNotImplemented
}

// Expire marks every generation whose overlap window elapsed and reports how
// many were swept.
func (g *ClientSecretOverlapGuard) Expire(now time.Time) int {
	return 0
}

// Generations lists the generations that were not swept yet, oldest first.
func (g *ClientSecretOverlapGuard) Generations(clientID string) []Generation {
	return nil
}

// Snapshot serialises every client generation table.
func (g *ClientSecretOverlapGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces every client generation table with a snapshot.
func (g *ClientSecretOverlapGuard) Restore(data []byte) error {
	return errNotImplemented
}
