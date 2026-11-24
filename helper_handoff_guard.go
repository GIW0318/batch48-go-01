// Package task brokers one-shot handoffs of already opened file descriptors
// between a privileged helper and a dropped-privilege child process.
package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("helper handoff guard: not implemented")

// Errors returned by the guard.
var (
	ErrPeerInvalid   = errors.New("helper handoff guard: peer invalid")
	ErrPeerMismatch  = errors.New("helper handoff guard: peer mismatch")
	ErrNoFDs         = errors.New("helper handoff guard: no descriptors offered")
	ErrTooManyFDs    = errors.New("helper handoff guard: too many descriptors")
	ErrInvalidFD     = errors.New("helper handoff guard: invalid descriptor")
	ErrPendingFull   = errors.New("helper handoff guard: pending table full")
	ErrUnknownNonce  = errors.New("helper handoff guard: unknown nonce")
	ErrNonceExpired  = errors.New("helper handoff guard: nonce expired")
	ErrNonceConsumed = errors.New("helper handoff guard: nonce consumed")
	ErrBadSnapshot   = errors.New("helper handoff guard: bad snapshot")
)

// PeerCred is the credential set the helper observed for the calling process.
type PeerCred struct {
	PID       int
	UID       int
	GID       int
	StartedAt time.Time
}

// Meta is the request summary carried together with the descriptors.
type Meta struct {
	Method string
	Path   string
	Body   []byte
}

// Options configures peer policy, nonce lifetime and table bounds.
type Options struct {
	AllowedUID       int
	AllowedGID       int
	NonceTTL         time.Duration
	MaxFDsPerRequest int
	MaxPending       int
	Now              func() time.Time
}

// Handoff is the payload returned exactly once for a nonce.
type Handoff struct {
	Nonce    string
	FDs      []int
	Meta     Meta
	Digest   string
	IssuedAt time.Time
}

// OfferInfo describes one pending offer without consuming it.
type OfferInfo struct {
	Nonce    string
	FDs      []int
	Peer     PeerCred
	Digest   string
	IssuedAt time.Time
}

// HandoffStats counts the lifetime activity of the guard.
type HandoffStats struct {
	Offered  int
	Accepted int
	Revoked  int
	Expired  int
}

// HelperHandoffGuard owns the pending handoff table.
type HelperHandoffGuard struct {
}

// NewHelperHandoffGuard builds a guard; zero options use the documented defaults.
func NewHelperHandoffGuard(opts Options) *HelperHandoffGuard {
	return &HelperHandoffGuard{}
}

// Offer validates the caller and parks the descriptors behind a fresh nonce.
func (g *HelperHandoffGuard) Offer(peer PeerCred, fds []int, meta Meta) (string, error) {
	return "", errNotImplemented
}

// Accept consumes a nonce and returns its descriptors exactly once.
func (g *HelperHandoffGuard) Accept(nonce string, peer PeerCred) (Handoff, error) {
	return Handoff{}, errNotImplemented
}

// Revoke drops a pending offer.
func (g *HelperHandoffGuard) Revoke(nonce string) error {
	return errNotImplemented
}

// Pending reports how many offers are currently waiting.
func (g *HelperHandoffGuard) Pending() int {
	return 0
}

// Inspect describes a pending offer without consuming it.
func (g *HelperHandoffGuard) Inspect(nonce string) (OfferInfo, error) {
	return OfferInfo{}, errNotImplemented
}

// Purge drops every expired offer and reports how many were removed.
func (g *HelperHandoffGuard) Purge() int {
	return 0
}

// RevokeAll drops every pending offer and reports how many were removed.
func (g *HelperHandoffGuard) RevokeAll() int {
	return 0
}

// Stats returns a snapshot of the lifetime counters.
func (g *HelperHandoffGuard) Stats() HandoffStats {
	return HandoffStats{}
}

// Snapshot serialises the pending table.
func (g *HelperHandoffGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (g *HelperHandoffGuard) Restore(b []byte) error {
	return errNotImplemented
}
