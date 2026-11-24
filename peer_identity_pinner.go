package task

import (
	"errors"
	"sync"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs a fix.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by PeerIdentityPinner.
var (
	ErrInvalidPin         = errors.New("invalid pin request")
	ErrInvalidFingerprint = errors.New("invalid certificate fingerprint")
	ErrAlreadyPinned      = errors.New("connection already pinned")
	ErrUnknownConn        = errors.New("unknown connection")
	ErrConnLimit          = errors.New("connection limit reached")
	ErrPendingLimit       = errors.New("pending request limit reached")
	ErrStaleGeneration    = errors.New("stale pin generation")
	ErrRotationPending    = errors.New("a renegotiation is already pending")
	ErrDraining           = errors.New("connection is draining")
	ErrNoPending          = errors.New("no pending request to complete")
	ErrBadSnapshot        = errors.New("bad snapshot")
)

// Pin is the identity pinned on one long lived TLS connection.
type Pin struct {
	ConnID      string    `json:"conn_id"`
	Principal   string    `json:"principal"`
	Fingerprint string    `json:"fingerprint"`
	Generation  uint64    `json:"generation"`
	PinnedAt    time.Time `json:"pinned_at"`
}

// PinOptions bounds the pinner.
type PinOptions struct {
	MaxConnections    int
	MaxPendingPerConn int
	Now               func() time.Time
}

// PeerIdentityPinner pins peer identities on long lived mTLS connections.
type PeerIdentityPinner struct {
	mu       sync.Mutex
	maxConns int
	maxPend  int
	now      func() time.Time
}

// NewPeerIdentityPinner builds an empty pinner.
func NewPeerIdentityPinner(opts PinOptions) *PeerIdentityPinner {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &PeerIdentityPinner{
		maxConns: opts.MaxConnections,
		maxPend:  opts.MaxPendingPerConn,
		now:      now,
	}
}

// Pin binds the first peer identity seen on a connection.
func (p *PeerIdentityPinner) Pin(connID, principal, certFingerprint string) (Pin, error) {
	return Pin{}, errNotImplemented
}

// Check reports the identity currently pinned on a connection.
func (p *PeerIdentityPinner) Check(connID string) (Pin, error) {
	return Pin{}, errNotImplemented
}

// Authorize decides whether a request principal matches the pinned identity.
func (p *PeerIdentityPinner) Authorize(connID, principal string) (bool, error) {
	return false, errNotImplemented
}

// Rotate starts a certificate renegotiation on a pinned connection.
func (p *PeerIdentityPinner) Rotate(connID, newPrincipal, newFingerprint string) (uint64, error) {
	return 0, errNotImplemented
}

// Commit applies the pending renegotiated identity of a connection.
func (p *PeerIdentityPinner) Commit(connID string, generation uint64) error {
	return errNotImplemented
}

// Drain marks a connection as draining at the given deadline.
func (p *PeerIdentityPinner) Drain(connID string, deadline time.Time) error {
	return errNotImplemented
}

// BeginRequest tracks one in flight request of a connection.
func (p *PeerIdentityPinner) BeginRequest(connID string) error {
	return errNotImplemented
}

// EndRequest completes one in flight request of a connection.
func (p *PeerIdentityPinner) EndRequest(connID string) error {
	return errNotImplemented
}

// Pending reports the in flight requests of a connection.
func (p *PeerIdentityPinner) Pending(connID string) int {
	return 0
}

// Close drops a connection together with its pinned identity.
func (p *PeerIdentityPinner) Close(connID string) error {
	return errNotImplemented
}

// ConnectionCount reports how many connections are pinned.
func (p *PeerIdentityPinner) ConnectionCount() int {
	return 0
}

// Snapshot serialises the whole pinner state.
func (p *PeerIdentityPinner) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the pinner state from a snapshot.
func (p *PeerIdentityPinner) Restore(data []byte) error {
	return errNotImplemented
}
