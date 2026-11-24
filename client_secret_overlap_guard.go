// Package task keeps two client secret generations usable at the same time
// during rotation, distinguishes expired from revoked secrets and guarantees a
// single winning rotation per client and instant.
package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

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
	ClientID  string    `json:"client_id"`
	Gen       int       `json:"gen"`
	Secret    string    `json:"secret"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Verdict is the outcome of one verification attempt.
type Verdict struct {
	OK     bool
	Gen    int
	Reason string
}

type clientSecrets struct {
	gens       map[int]Generation
	live       []int
	current    int
	lastRotate time.Time
	counter    int
}

// ClientSecretOverlapGuard keeps the generation table of every client.
type ClientSecretOverlapGuard struct {
	mu         sync.Mutex
	limits     SecretLimits
	clients    map[string]*clientSecrets
	lastExpire time.Time
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
	if strings.TrimSpace(clientID) == "" {
		return Generation{}, ErrBlankClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.clients[clientID]; exists {
		return Generation{}, ErrClientExists
	}
	if g.limits.MaxClients > 0 && len(g.clients) >= g.limits.MaxClients {
		return Generation{}, ErrClientLimit
	}

	secret, err := g.mintSecretLocked(nil)
	if err != nil {
		return Generation{}, err
	}

	gen := Generation{
		ClientID:  clientID,
		Gen:       1,
		Secret:    secret,
		Status:    StatusActive,
		CreatedAt: now,
		ExpiresAt: now.Add(g.limits.OverlapWindow),
	}
	g.clients[clientID] = &clientSecrets{
		gens:    map[int]Generation{1: gen},
		live:    []int{1},
		current: 1,
		counter: 1,
	}
	return gen, nil
}

// Rotate opens the next generation and moves the previous one into overlap.
func (g *ClientSecretOverlapGuard) Rotate(clientID string, now time.Time) (Generation, error) {
	if strings.TrimSpace(clientID) == "" {
		return Generation{}, ErrBlankClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	cs, ok := g.clients[clientID]
	if !ok {
		return Generation{}, ErrClientNotFound
	}

	// A replayed request at the same instant is idempotent: callers observe
	// exactly one new generation even when several workers race.
	if !cs.lastRotate.IsZero() && cs.lastRotate.Equal(now) {
		return cs.gens[cs.current], nil
	}

	// Retire the oldest live generation the moment the new one is registered
	// so the usable set never holds more than MaxGenerations entries.
	for len(cs.live) >= g.limits.MaxGenerations && len(cs.live) > 0 {
		oldest := cs.live[0]
		cs.live = cs.live[1:]
		old := cs.gens[oldest]
		old.Status = StatusRetired
		cs.gens[oldest] = old
		if cs.current == oldest {
			cs.current = 0
		}
	}

	cs.counter++
	nextGen := cs.counter
	secret, err := g.mintSecretLocked(cs)
	if err != nil {
		cs.counter--
		return Generation{}, err
	}

	if cs.current != 0 {
		current := cs.gens[cs.current]
		current.Status = StatusPrevious
		cs.gens[cs.current] = current
	}

	gen := Generation{
		ClientID:  clientID,
		Gen:       nextGen,
		Secret:    secret,
		Status:    StatusActive,
		CreatedAt: now,
		ExpiresAt: now.Add(g.limits.OverlapWindow),
	}
	cs.gens[nextGen] = gen
	cs.live = append(cs.live, nextGen)
	cs.current = nextGen
	cs.lastRotate = now
	return gen, nil
}

// Activate makes one generation the active member of the overlap window.
func (g *ClientSecretOverlapGuard) Activate(clientID string, gen int) error {
	if strings.TrimSpace(clientID) == "" {
		return ErrBlankClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	cs, ok := g.clients[clientID]
	if !ok {
		return ErrClientNotFound
	}
	target, ok := cs.gens[gen]
	if !ok || !g.isLiveLocked(cs, gen) {
		return ErrGenerationNotFound
	}

	if cs.current != 0 && cs.current != gen {
		previous := cs.gens[cs.current]
		previous.Status = StatusPrevious
		cs.gens[cs.current] = previous
	}
	target.Status = StatusActive
	cs.gens[gen] = target
	cs.current = gen
	return nil
}

// Verify checks one presented client secret against the usable window.
func (g *ClientSecretOverlapGuard) Verify(clientID, secret string, now time.Time) (Verdict, error) {
	if strings.TrimSpace(clientID) == "" {
		return Verdict{}, ErrBlankClient
	}
	if strings.TrimSpace(secret) == "" {
		return Verdict{}, ErrBadSecret
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	cs, ok := g.clients[clientID]
	if !ok {
		return Verdict{OK: false, Reason: ReasonClientUnknown}, ErrClientNotFound
	}

	matchedGen := 0
	var matched Generation
	for _, record := range cs.gens {
		if record.Secret == secret {
			matched = record
			matchedGen = record.Gen
			break
		}
	}
	if matchedGen == 0 {
		return Verdict{OK: false, Gen: 0, Reason: ReasonNoMatch}, ErrSecretUnknown
	}

	switch matched.Status {
	case StatusRevoked:
		return Verdict{OK: false, Gen: matchedGen, Reason: ReasonSecretRevoked}, ErrSecretRevoked
	case StatusRetired:
		return Verdict{OK: false, Gen: matchedGen, Reason: ReasonSecretRetired}, ErrSecretRetired
	case StatusExpired:
		return Verdict{OK: false, Gen: matchedGen, Reason: ReasonSecretExpired}, ErrSecretExpired
	}

	if !now.Before(matched.ExpiresAt) {
		matched.Status = StatusExpired
		cs.gens[matchedGen] = matched
		cs.removeLiveLocked(matchedGen)
		return Verdict{OK: false, Gen: matchedGen, Reason: ReasonSecretExpired}, ErrSecretExpired
	}

	return Verdict{OK: true, Gen: matchedGen, Reason: ReasonOK}, nil
}

// RevokeOne retires a single generation before its overlap window ends.
func (g *ClientSecretOverlapGuard) RevokeOne(clientID string, gen int) error {
	if strings.TrimSpace(clientID) == "" {
		return ErrBlankClient
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	cs, ok := g.clients[clientID]
	if !ok {
		return ErrClientNotFound
	}
	record, ok := cs.gens[gen]
	if !ok {
		return ErrGenerationNotFound
	}
	if !g.isLiveLocked(cs, gen) {
		return ErrGenerationRetired
	}

	record.Status = StatusRevoked
	cs.gens[gen] = record
	cs.removeLiveLocked(gen)
	if cs.current == gen {
		cs.current = 0
	}
	return nil
}

// Expire marks every generation whose overlap window elapsed and reports how
// many were swept.
func (g *ClientSecretOverlapGuard) Expire(now time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	swept := 0
	for _, cs := range g.clients {
		for _, gen := range append([]int(nil), cs.live...) {
			record := cs.gens[gen]
			if now.Before(record.ExpiresAt) {
				continue
			}
			record.Status = StatusExpired
			cs.gens[gen] = record
			cs.removeLiveLocked(gen)
			if cs.current == gen {
				cs.current = 0
			}
			swept++
		}
	}
	g.lastExpire = now
	return swept
}

// Generations lists the generations that were not swept yet, oldest first.
func (g *ClientSecretOverlapGuard) Generations(clientID string) []Generation {
	g.mu.Lock()
	defer g.mu.Unlock()

	cs, ok := g.clients[clientID]
	if !ok {
		return []Generation{}
	}
	out := make([]Generation, 0, len(cs.live))
	for _, gen := range cs.live {
		out = append(out, cs.gens[gen])
	}
	return out
}

// Snapshot serialises every client generation table.
func (g *ClientSecretOverlapGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := snapshotEnvelope{
		Limits:     g.limits,
		LastExpire: g.lastExpire,
		Clients:    make(map[string]clientSnapshot, len(g.clients)),
	}
	for clientID, cs := range g.clients {
		gens := make([]Generation, 0, len(cs.gens))
		for _, record := range cs.gens {
			gens = append(gens, record)
		}
		snap.Clients[clientID] = clientSnapshot{
			Gens:       gens,
			Live:       append([]int(nil), cs.live...),
			Current:    cs.current,
			LastRotate: cs.lastRotate,
			Counter:    cs.counter,
		}
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	return data, nil
}

// Restore replaces every client generation table with a snapshot.
func (g *ClientSecretOverlapGuard) Restore(data []byte) error {
	var snap snapshotEnvelope
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}

	clients := make(map[string]*clientSecrets, len(snap.Clients))
	for clientID, entry := range snap.Clients {
		if strings.TrimSpace(clientID) == "" {
			return fmt.Errorf("%w: blank client id", ErrBadSnapshot)
		}
		gens := make(map[int]Generation, len(entry.Gens))
		for _, record := range entry.Gens {
			if record.Gen <= 0 || strings.TrimSpace(record.Secret) == "" || record.Status == "" {
				return fmt.Errorf("%w: malformed generation for client %q", ErrBadSnapshot, clientID)
			}
			gens[record.Gen] = record
		}
		clients[clientID] = &clientSecrets{
			gens:       gens,
			live:       append([]int(nil), entry.Live...),
			current:    entry.Current,
			lastRotate: entry.LastRotate,
			counter:    entry.Counter,
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.clients = clients
	g.limits = snap.Limits
	g.lastExpire = snap.LastExpire
	return nil
}

type snapshotEnvelope struct {
	Limits     SecretLimits              `json:"limits"`
	LastExpire time.Time                 `json:"last_expire"`
	Clients    map[string]clientSnapshot `json:"clients"`
}

type clientSnapshot struct {
	Gens       []Generation `json:"gens"`
	Live       []int        `json:"live"`
	Current    int          `json:"current"`
	LastRotate time.Time    `json:"last_rotate"`
	Counter    int          `json:"counter"`
}

func (g *ClientSecretOverlapGuard) mintSecretLocked(cs *clientSecrets) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("mint secret: %w", err)
		}
		secret := hex.EncodeToString(buf)
		collision := false
		if cs != nil {
			for _, record := range cs.gens {
				if record.Secret == secret {
					collision = true
					break
				}
			}
		}
		if !collision {
			return secret, nil
		}
	}
	return "", errors.New("mint secret: could not derive a unique secret")
}

func (g *ClientSecretOverlapGuard) isLiveLocked(cs *clientSecrets, gen int) bool {
	for _, live := range cs.live {
		if live == gen {
			return true
		}
	}
	return false
}

func (cs *clientSecrets) removeLiveLocked(gen int) {
	kept := cs.live[:0]
	for _, live := range cs.live {
		if live != gen {
			kept = append(kept, live)
		}
	}
	cs.live = kept
}
