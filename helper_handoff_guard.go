// Package task brokers one-shot handoffs of already opened file descriptors
// between a privileged helper and a dropped-privilege child process.
package task

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

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

const (
	defaultNonceTTL         = time.Minute
	defaultMaxFDsPerRequest = 16
	defaultMaxPending       = 256
	snapshotVersion         = 1
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

type pendingOffer struct {
	nonce     string
	fds       []int
	peer      PeerCred
	meta      Meta
	digest    string
	issuedAt  time.Time
	expiresAt time.Time
}

// HelperHandoffGuard owns the pending handoff table.
type HelperHandoffGuard struct {
	mu       sync.Mutex
	opts     Options
	pending  map[string]*pendingOffer
	consumed map[string]struct{}
	stats    HandoffStats
}

// NewHelperHandoffGuard builds a guard; zero options use the documented defaults.
func NewHelperHandoffGuard(opts Options) *HelperHandoffGuard {
	if opts.NonceTTL <= 0 {
		opts.NonceTTL = defaultNonceTTL
	}
	if opts.MaxFDsPerRequest <= 0 {
		opts.MaxFDsPerRequest = defaultMaxFDsPerRequest
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = defaultMaxPending
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &HelperHandoffGuard{
		opts:     opts,
		pending:  make(map[string]*pendingOffer),
		consumed: make(map[string]struct{}),
	}
}

// Offer validates the caller and parks the descriptors behind a fresh nonce.
func (g *HelperHandoffGuard) Offer(peer PeerCred, fds []int, meta Meta) (string, error) {
	if !peerComplete(peer) {
		return "", ErrPeerInvalid
	}
	if peer.UID != g.opts.AllowedUID || peer.GID != g.opts.AllowedGID {
		return "", ErrPeerMismatch
	}
	if err := g.validateFDs(fds); err != nil {
		return "", err
	}

	digest := digestMeta(meta)
	storedMeta := Meta{
		Method: meta.Method,
		Path:   meta.Path,
		Body:   cloneBytes(meta.Body),
	}
	storedFDs := cloneInts(fds)
	issuedAt := g.now()

	for {
		nonce, err := newNonce()
		if err != nil {
			return "", err
		}

		g.mu.Lock()
		if len(g.pending) >= g.opts.MaxPending {
			g.mu.Unlock()
			return "", ErrPendingFull
		}
		if _, exists := g.pending[nonce]; exists {
			g.mu.Unlock()
			continue
		}
		if _, exists := g.consumed[nonce]; exists {
			g.mu.Unlock()
			continue
		}
		g.pending[nonce] = &pendingOffer{
			nonce:     nonce,
			fds:       storedFDs,
			peer:      peer,
			meta:      storedMeta,
			digest:    digest,
			issuedAt:  issuedAt,
			expiresAt: issuedAt.Add(g.opts.NonceTTL),
		}
		g.stats.Offered++
		g.mu.Unlock()
		return nonce, nil
	}
}

// Accept consumes a nonce and returns its descriptors exactly once.
func (g *HelperHandoffGuard) Accept(nonce string, peer PeerCred) (Handoff, error) {
	now := g.now()

	g.mu.Lock()
	offer, ok := g.pending[nonce]
	if !ok {
		_, wasConsumed := g.consumed[nonce]
		g.mu.Unlock()
		if wasConsumed {
			return Handoff{}, ErrNonceConsumed
		}
		return Handoff{}, ErrUnknownNonce
	}
	if now.After(offer.expiresAt) {
		delete(g.pending, nonce)
		g.stats.Expired++
		g.mu.Unlock()
		return Handoff{}, ErrNonceExpired
	}
	if !samePeer(offer.peer, peer) {
		g.mu.Unlock()
		return Handoff{}, ErrPeerMismatch
	}
	delete(g.pending, nonce)
	g.consumed[nonce] = struct{}{}
	g.stats.Accepted++
	handoff := Handoff{
		Nonce:    nonce,
		FDs:      offer.fds,
		Meta:     offer.meta,
		Digest:   offer.digest,
		IssuedAt: offer.issuedAt,
	}
	g.mu.Unlock()

	handoff.FDs = cloneInts(handoff.FDs)
	handoff.Meta.Body = cloneBytes(handoff.Meta.Body)
	return handoff, nil
}

// Revoke drops a pending offer.
func (g *HelperHandoffGuard) Revoke(nonce string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.pending[nonce]; !ok {
		return ErrUnknownNonce
	}
	delete(g.pending, nonce)
	g.stats.Revoked++
	return nil
}

// Pending reports how many offers are currently waiting.
func (g *HelperHandoffGuard) Pending() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pending)
}

// Inspect describes a pending offer without consuming it.
func (g *HelperHandoffGuard) Inspect(nonce string) (OfferInfo, error) {
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()
	offer, ok := g.pending[nonce]
	if !ok {
		return OfferInfo{}, ErrUnknownNonce
	}
	if now.After(offer.expiresAt) {
		delete(g.pending, nonce)
		g.stats.Expired++
		return OfferInfo{}, ErrNonceExpired
	}
	return OfferInfo{
		Nonce:    offer.nonce,
		FDs:      cloneInts(offer.fds),
		Peer:     offer.peer,
		Digest:   offer.digest,
		IssuedAt: offer.issuedAt,
	}, nil
}

// Purge drops every expired offer and reports how many were removed.
func (g *HelperHandoffGuard) Purge() int {
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()
	removed := 0
	for nonce, offer := range g.pending {
		if now.After(offer.expiresAt) {
			delete(g.pending, nonce)
			removed++
		}
	}
	g.stats.Expired += removed
	return removed
}

// RevokeAll drops every pending offer and reports how many were removed.
func (g *HelperHandoffGuard) RevokeAll() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	removed := len(g.pending)
	g.pending = make(map[string]*pendingOffer)
	g.stats.Revoked += removed
	return removed
}

// Stats returns a snapshot of the lifetime counters.
func (g *HelperHandoffGuard) Stats() HandoffStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

type snapshotPeer struct {
	PID       int       `json:"pid"`
	UID       int       `json:"uid"`
	GID       int       `json:"gid"`
	StartedAt time.Time `json:"started_at"`
}

type snapshotMeta struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   []byte `json:"body"`
}

type snapshotEntry struct {
	Nonce     string       `json:"nonce"`
	FDs       []int        `json:"fds"`
	Peer      snapshotPeer `json:"peer"`
	Meta      snapshotMeta `json:"meta"`
	Digest    string       `json:"digest"`
	IssuedAt  time.Time    `json:"issued_at"`
	ExpiresAt time.Time    `json:"expires_at"`
}

type snapshotPayload struct {
	Version int             `json:"version"`
	Entries []snapshotEntry `json:"entries"`
}

type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Entries  []snapshotEntry `json:"entries"`
	Checksum string          `json:"checksum"`
}

// Snapshot serialises the pending table.
func (g *HelperHandoffGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	nonces := make([]string, 0, len(g.pending))
	for nonce := range g.pending {
		nonces = append(nonces, nonce)
	}
	sort.Strings(nonces)

	entries := make([]snapshotEntry, 0, len(nonces))
	for _, nonce := range nonces {
		offer := g.pending[nonce]
		entries = append(entries, snapshotEntry{
			Nonce: offer.nonce,
			FDs:   cloneInts(offer.fds),
			Peer: snapshotPeer{
				PID:       offer.peer.PID,
				UID:       offer.peer.UID,
				GID:       offer.peer.GID,
				StartedAt: offer.peer.StartedAt,
			},
			Meta: snapshotMeta{
				Method: offer.meta.Method,
				Path:   offer.meta.Path,
				Body:   offer.meta.Body,
			},
			Digest:    offer.digest,
			IssuedAt:  offer.issuedAt,
			ExpiresAt: offer.expiresAt,
		})
	}
	g.mu.Unlock()

	payload := snapshotPayload{Version: snapshotVersion, Entries: entries}
	checksum, err := snapshotChecksum(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshotEnvelope{
		Version:  snapshotVersion,
		Entries:  entries,
		Checksum: checksum,
	})
}

// Restore loads a snapshot produced by Snapshot.
func (g *HelperHandoffGuard) Restore(b []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(b, &envelope); err != nil {
		return ErrBadSnapshot
	}
	if envelope.Version != snapshotVersion {
		return ErrBadSnapshot
	}
	expected, err := snapshotChecksum(snapshotPayload{
		Version: envelope.Version,
		Entries: envelope.Entries,
	})
	if err != nil || expected != envelope.Checksum {
		return ErrBadSnapshot
	}

	restored := make(map[string]*pendingOffer, len(envelope.Entries))
	for _, entry := range envelope.Entries {
		offer, err := g.offerFromSnapshot(entry)
		if err != nil {
			return ErrBadSnapshot
		}
		if _, exists := restored[offer.nonce]; exists {
			return ErrBadSnapshot
		}
		restored[offer.nonce] = offer
	}

	g.mu.Lock()
	g.pending = restored
	g.mu.Unlock()
	return nil
}

func (g *HelperHandoffGuard) offerFromSnapshot(entry snapshotEntry) (*pendingOffer, error) {
	if !validNonce(entry.Nonce) {
		return nil, ErrBadSnapshot
	}
	if err := g.validateFDs(entry.FDs); err != nil {
		return nil, ErrBadSnapshot
	}
	peer := PeerCred{
		PID:       entry.Peer.PID,
		UID:       entry.Peer.UID,
		GID:       entry.Peer.GID,
		StartedAt: entry.Peer.StartedAt,
	}
	if !peerComplete(peer) {
		return nil, ErrBadSnapshot
	}
	meta := Meta{
		Method: entry.Meta.Method,
		Path:   entry.Meta.Path,
		Body:   entry.Meta.Body,
	}
	if entry.Digest != digestMeta(meta) {
		return nil, ErrBadSnapshot
	}
	if entry.ExpiresAt.Before(entry.IssuedAt) {
		return nil, ErrBadSnapshot
	}
	return &pendingOffer{
		nonce:     entry.Nonce,
		fds:       entry.FDs,
		peer:      peer,
		meta:      meta,
		digest:    entry.Digest,
		issuedAt:  entry.IssuedAt,
		expiresAt: entry.ExpiresAt,
	}, nil
}

func (g *HelperHandoffGuard) validateFDs(fds []int) error {
	if len(fds) == 0 {
		return ErrNoFDs
	}
	if len(fds) > g.opts.MaxFDsPerRequest {
		return ErrTooManyFDs
	}
	seen := make(map[int]struct{}, len(fds))
	for _, fd := range fds {
		if fd < 0 {
			return ErrInvalidFD
		}
		if _, exists := seen[fd]; exists {
			return ErrInvalidFD
		}
		seen[fd] = struct{}{}
	}
	return nil
}

func (g *HelperHandoffGuard) now() time.Time {
	return g.opts.Now()
}

func peerComplete(peer PeerCred) bool {
	return peer.PID > 0 &&
		peer.UID >= 0 &&
		peer.GID >= 0 &&
		!peer.StartedAt.IsZero()
}

func samePeer(a, b PeerCred) bool {
	return a.PID == b.PID &&
		a.UID == b.UID &&
		a.GID == b.GID &&
		a.StartedAt.Equal(b.StartedAt)
}

func digestMeta(meta Meta) string {
	h := sha256.New()
	h.Write([]byte(meta.Method))
	h.Write([]byte{0})
	h.Write([]byte(meta.Path))
	h.Write([]byte{0})
	h.Write(meta.Body)
	return hex.EncodeToString(h.Sum(nil))
}

func newNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func validNonce(nonce string) bool {
	if len(nonce) != 32 {
		return false
	}
	for i := 0; i < len(nonce); i++ {
		c := nonce[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func snapshotChecksum(payload snapshotPayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneInts(values []int) []int {
	if values == nil {
		return nil
	}
	out := make([]int, len(values))
	copy(out, values)
	return out
}
