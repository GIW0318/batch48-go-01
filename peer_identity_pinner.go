package task

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

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

type connectionState struct {
	pin             Pin
	pending         *Pin
	pendingRequests int
	draining        bool
	drainDeadline   time.Time
}

// PeerIdentityPinner pins peer identities on long lived mTLS connections.
type PeerIdentityPinner struct {
	mu       sync.Mutex
	maxConns int
	maxPend  int
	now      func() time.Time
	conns    map[string]*connectionState
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
		conns:    make(map[string]*connectionState),
	}
}

// Pin binds the first peer identity seen on a connection.
func (p *PeerIdentityPinner) Pin(connID, principal, certFingerprint string) (Pin, error) {
	if connID == "" || principal == "" {
		return Pin{}, ErrInvalidPin
	}
	fingerprint, err := normaliseFingerprint(certFingerprint)
	if err != nil {
		return Pin{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.conns[connID]; ok {
		return Pin{}, ErrAlreadyPinned
	}
	if p.maxConns > 0 && len(p.conns) >= p.maxConns {
		return Pin{}, ErrConnLimit
	}

	pin := Pin{
		ConnID:      connID,
		Principal:   principal,
		Fingerprint: fingerprint,
		Generation:  1,
		PinnedAt:    p.now(),
	}
	p.conns[connID] = &connectionState{pin: pin}
	return pin, nil
}

// Check reports the identity currently pinned on a connection.
func (p *PeerIdentityPinner) Check(connID string) (Pin, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return Pin{}, ErrUnknownConn
	}
	return state.pin, nil
}

// Authorize decides whether a request principal matches the pinned identity.
func (p *PeerIdentityPinner) Authorize(connID, principal string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return false, ErrUnknownConn
	}
	return samePrincipal(state.pin.Principal, principal), nil
}

// Rotate starts a certificate renegotiation on a pinned connection.
func (p *PeerIdentityPinner) Rotate(connID, newPrincipal, newFingerprint string) (uint64, error) {
	if connID == "" || newPrincipal == "" {
		return 0, ErrInvalidPin
	}
	fingerprint, err := normaliseFingerprint(newFingerprint)
	if err != nil {
		return 0, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return 0, ErrUnknownConn
	}
	if state.pending != nil {
		return 0, ErrRotationPending
	}

	generation := state.pin.Generation + 1
	pending := Pin{
		ConnID:      connID,
		Principal:   newPrincipal,
		Fingerprint: fingerprint,
		Generation:  generation,
		PinnedAt:    p.now(),
	}
	state.pending = &pending
	return generation, nil
}

// Commit applies the pending renegotiated identity of a connection.
func (p *PeerIdentityPinner) Commit(connID string, generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return ErrUnknownConn
	}
	if state.pending == nil || state.pending.Generation != generation ||
		state.pin.Generation+1 != generation {
		return ErrStaleGeneration
	}

	state.pin = *state.pending
	state.pin.PinnedAt = p.now()
	state.pending = nil
	return nil
}

// Drain marks a connection as draining at the given deadline.
func (p *PeerIdentityPinner) Drain(connID string, deadline time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return ErrUnknownConn
	}
	state.draining = true
	state.drainDeadline = deadline
	return nil
}

// BeginRequest tracks one in flight request of a connection.
func (p *PeerIdentityPinner) BeginRequest(connID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return ErrUnknownConn
	}
	if state.draining && !p.now().Before(state.drainDeadline) {
		return ErrDraining
	}
	if p.maxPend > 0 && state.pendingRequests >= p.maxPend {
		return ErrPendingLimit
	}
	state.pendingRequests++
	return nil
}

// EndRequest completes one in flight request of a connection.
func (p *PeerIdentityPinner) EndRequest(connID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return ErrUnknownConn
	}
	if state.pendingRequests <= 0 {
		return ErrNoPending
	}
	state.pendingRequests--
	return nil
}

// Pending reports the in flight requests of a connection.
func (p *PeerIdentityPinner) Pending(connID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.conns[connID]
	if !ok {
		return 0
	}
	return state.pendingRequests
}

// Close drops a connection together with its pinned identity.
func (p *PeerIdentityPinner) Close(connID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.conns[connID]; !ok {
		return ErrUnknownConn
	}
	delete(p.conns, connID)
	return nil
}

// ConnectionCount reports how many connections are pinned.
func (p *PeerIdentityPinner) ConnectionCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

const snapshotVersion = 1

type snapshotConnection struct {
	Pin             Pin        `json:"pin"`
	Pending         *Pin       `json:"pending,omitempty"`
	PendingRequests int        `json:"pending_requests"`
	Draining        bool       `json:"draining,omitempty"`
	DrainDeadline   *time.Time `json:"drain_deadline,omitempty"`
}

type pinnerSnapshot struct {
	Version         int                           `json:"version"`
	ConnectionCount int                           `json:"connection_count"`
	Connections     map[string]snapshotConnection `json:"connections"`
}

// Snapshot serialises the whole pinner state.
func (p *PeerIdentityPinner) Snapshot() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := pinnerSnapshot{
		Version:         snapshotVersion,
		ConnectionCount: len(p.conns),
		Connections:     make(map[string]snapshotConnection, len(p.conns)),
	}
	for connID, state := range p.conns {
		connection := snapshotConnection{
			Pin:             state.pin,
			PendingRequests: state.pendingRequests,
			Draining:        state.draining,
		}
		if state.pending != nil {
			pending := *state.pending
			connection.Pending = &pending
		}
		if state.draining {
			deadline := state.drainDeadline
			connection.DrainDeadline = &deadline
		}
		snapshot.Connections[connID] = connection
	}
	return json.Marshal(snapshot)
}

// Restore rebuilds the pinner state from a snapshot.
func (p *PeerIdentityPinner) Restore(data []byte) error {
	var snapshot pinnerSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrBadSnapshot
	}
	if snapshot.Version != snapshotVersion || snapshot.Connections == nil {
		return ErrBadSnapshot
	}

	connections := make(map[string]*connectionState, len(snapshot.Connections))
	for connID, saved := range snapshot.Connections {
		state, ok := restoreConnection(connID, saved)
		if !ok {
			return ErrBadSnapshot
		}
		connections[connID] = state
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns = connections
	return nil
}

func restoreConnection(connID string, saved snapshotConnection) (*connectionState, bool) {
	if connID == "" || saved.PendingRequests < 0 {
		return nil, false
	}

	pin, ok := restorePin(connID, saved.Pin)
	if !ok || pin.Generation == 0 {
		return nil, false
	}

	state := &connectionState{
		pin:             pin,
		pendingRequests: saved.PendingRequests,
		draining:        saved.Draining,
	}
	if saved.Draining && saved.DrainDeadline != nil {
		state.drainDeadline = *saved.DrainDeadline
	}
	if saved.Pending != nil {
		pending, ok := restorePin(connID, *saved.Pending)
		if !ok || pending.Generation != pin.Generation+1 {
			return nil, false
		}
		state.pending = &pending
	}
	return state, true
}

func restorePin(connID string, pin Pin) (Pin, bool) {
	if pin.ConnID != connID || pin.Principal == "" || pin.Generation == 0 {
		return Pin{}, false
	}
	fingerprint, err := normaliseFingerprint(pin.Fingerprint)
	if err != nil {
		return Pin{}, false
	}
	pin.Fingerprint = fingerprint
	return pin, true
}

func normaliseFingerprint(raw string) (string, error) {
	var normalised strings.Builder
	normalised.Grow(len(raw))

	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= '0' && c <= '9':
			normalised.WriteByte(c)
		case c >= 'a' && c <= 'f':
			normalised.WriteByte(c)
		case c >= 'A' && c <= 'F':
			normalised.WriteByte(c + ('a' - 'A'))
		case c == ':' || c == '-' || c == ' ' || c == '\t' || c == '\n' || c == '\r':
			continue
		default:
			return "", ErrInvalidFingerprint
		}
	}

	if normalised.Len() != 64 {
		return "", ErrInvalidFingerprint
	}
	return normalised.String(), nil
}

func samePrincipal(a, b string) bool {
	return canonicalPrincipal(a) == canonicalPrincipal(b)
}

func canonicalPrincipal(principal string) string {
	if unescaped, err := url.PathUnescape(principal); err == nil {
		principal = unescaped
	}
	return strings.ToLower(principal)
}
