// Package task binds access tokens to the leaf certificate that may present
// them, moves a binding only after re-authentication and keeps token lifetime
// and per client binding counts bounded.
package task

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors returned by the guard.
var (
	ErrBlankClient         = errors.New("blank client")
	ErrBlankFingerprint    = errors.New("blank certificate fingerprint")
	ErrBadFingerprint      = errors.New("bad certificate fingerprint")
	ErrInvalidTTL          = errors.New("invalid token ttl")
	ErrBindingLimit        = errors.New("binding limit")
	ErrUnknownToken        = errors.New("unknown token")
	ErrAlreadyBound        = errors.New("token already bound")
	ErrTokenUnbound        = errors.New("token not bound")
	ErrTokenExpired        = errors.New("token expired")
	ErrFingerprintMismatch = errors.New("leaf certificate fingerprint mismatch")
	ErrMalformedToken      = errors.New("malformed token")
	ErrProofInvalid        = errors.New("reauthentication proof invalid")
	ErrProofMismatch       = errors.New("reauthentication proof mismatch")
	ErrUnknownBinding      = errors.New("unknown binding")
	ErrBadSnapshot         = errors.New("bad snapshot")
)

// Verdict reasons.
const (
	ReasonOK           = "ok"
	ReasonTokenExpired = "token_expired"
	ReasonUnbound      = "token_unbound"
	ReasonMismatch     = "fingerprint_mismatch"
	ReasonForged       = "token_forged"
)

// ProofMethodReauth is the only accepted re-authentication proof method.
const ProofMethodReauth = "reauth"

const (
	tokenPrefix        = "tcb1."
	tokenIDPrefix      = "tcb-"
	snapshotVersion    = 1
	tokenSecretBytes   = 16
	tokenIDFixedDigits = 16
)

// CertLimits bounds token lifetime and the number of live bindings per client.
type CertLimits struct {
	MaxTokensPerClient int
	MaxTokenTTL        time.Duration
	ProofTTL           time.Duration
}

// Token is one access token bound to a leaf certificate fingerprint.
type Token struct {
	TokenID     string
	ClientID    string
	Fingerprint string
	Bound       bool
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

// Proof is the evidence that the subject re-authenticated before a rebind.
type Proof struct {
	TokenID   string
	Subject   string
	Method    string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Verdict is the outcome of one presentation check.
type Verdict struct {
	OK       bool
	TokenID  string
	ClientID string
	Reason   string
}

// TokenCertBindingGuard keeps the token to certificate binding table.
type TokenCertBindingGuard struct {
	mu      sync.RWMutex
	limits  CertLimits
	tokens  map[string]*Token
	secrets map[string]string
	seq     int
}

// NewTokenCertBindingGuard builds a guard with the supplied limits.
func NewTokenCertBindingGuard(limits CertLimits) *TokenCertBindingGuard {
	return &TokenCertBindingGuard{
		limits:  limits,
		tokens:  make(map[string]*Token),
		secrets: make(map[string]string),
	}
}

// NormalizeFingerprint folds a fingerprint into lowercase colon free hex.
func (g *TokenCertBindingGuard) NormalizeFingerprint(raw string) (string, error) {
	fingerprint := strings.TrimSpace(raw)
	if fingerprint == "" {
		return "", ErrBlankFingerprint
	}
	fingerprint = strings.ReplaceAll(fingerprint, ":", "")
	if len(fingerprint) != hex.EncodedLen(32) {
		return "", ErrBadFingerprint
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return "", ErrBadFingerprint
	}
	return strings.ToLower(fingerprint), nil
}

// Issue mints a token already bound to the supplied leaf certificate.
func (g *TokenCertBindingGuard) Issue(clientID, certFingerprint string, ttl time.Duration) (Token, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return Token{}, ErrBlankClient
	}
	fingerprint, err := g.NormalizeFingerprint(certFingerprint)
	if err != nil {
		return Token{}, err
	}
	ttl, err = g.boundedTTL(ttl)
	if err != nil {
		return Token{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.issueLocked(clientID, fingerprint, true, ttl)
}

// IssuePending mints a token that still has to be bound to a certificate.
func (g *TokenCertBindingGuard) IssuePending(clientID string, ttl time.Duration) (Token, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return Token{}, ErrBlankClient
	}
	ttl, err := g.boundedTTL(ttl)
	if err != nil {
		return Token{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.issueLocked(clientID, "", false, ttl)
}

// Bind attaches a pending token to one leaf certificate.
func (g *TokenCertBindingGuard) Bind(tokenID, certFingerprint string) error {
	fingerprint, err := g.NormalizeFingerprint(certFingerprint)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	token, ok := g.tokens[tokenID]
	if !ok {
		return ErrUnknownToken
	}
	if token.Bound {
		return ErrAlreadyBound
	}

	now := time.Now().UTC()
	if !now.Before(token.ExpiresAt) {
		return ErrTokenExpired
	}
	if err := g.checkBindingLimitLocked(token.ClientID, now); err != nil {
		return err
	}

	token.Fingerprint = fingerprint
	token.Bound = true
	return nil
}

// Present builds the opaque token string a client sends on the wire.
func (g *TokenCertBindingGuard) Present(tokenID string) (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	token, ok := g.tokens[tokenID]
	if !ok {
		return "", ErrUnknownToken
	}
	secret, ok := g.secrets[tokenID]
	if !ok {
		return "", ErrUnknownToken
	}
	return tokenPrefix + token.TokenID + "." + secret, nil
}

// VerifyPresented checks the presented token string and leaf fingerprint.
func (g *TokenCertBindingGuard) VerifyPresented(rawToken, presentedFingerprint string, now time.Time) (Verdict, error) {
	tokenID, secret, err := parsePresentedToken(rawToken)
	if err != nil {
		return Verdict{Reason: ReasonForged}, err
	}
	fingerprint, err := g.NormalizeFingerprint(presentedFingerprint)
	if err != nil {
		return Verdict{TokenID: tokenID, Reason: ReasonForged}, err
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	token, ok := g.tokens[tokenID]
	if !ok {
		return Verdict{TokenID: tokenID, Reason: ReasonForged}, ErrUnknownToken
	}
	expectedSecret, ok := g.secrets[tokenID]
	if !ok || subtle.ConstantTimeCompare([]byte(secret), []byte(expectedSecret)) != 1 {
		return Verdict{
			TokenID:  token.TokenID,
			ClientID: token.ClientID,
			Reason:   ReasonForged,
		}, ErrMalformedToken
	}
	if !token.Bound {
		return Verdict{
			TokenID:  token.TokenID,
			ClientID: token.ClientID,
			Reason:   ReasonUnbound,
		}, ErrTokenUnbound
	}
	if !now.Before(token.ExpiresAt) {
		return Verdict{
			TokenID:  token.TokenID,
			ClientID: token.ClientID,
			Reason:   ReasonTokenExpired,
		}, ErrTokenExpired
	}
	if token.Fingerprint != fingerprint {
		return Verdict{
			TokenID:  token.TokenID,
			ClientID: token.ClientID,
			Reason:   ReasonMismatch,
		}, ErrFingerprintMismatch
	}
	return Verdict{
		OK:       true,
		TokenID:  token.TokenID,
		ClientID: token.ClientID,
		Reason:   ReasonOK,
	}, nil
}

// Rebind moves a binding to a new leaf certificate after re-authentication.
func (g *TokenCertBindingGuard) Rebind(tokenID, newFingerprint string, reauthProof Proof) error {
	fingerprint, err := g.NormalizeFingerprint(newFingerprint)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	token, ok := g.tokens[tokenID]
	if !ok {
		return ErrUnknownToken
	}

	now := time.Now().UTC()
	if !now.Before(token.ExpiresAt) {
		return ErrTokenExpired
	}
	if !token.Bound {
		return ErrTokenUnbound
	}
	if err := validateReauthProof(tokenID, reauthProof, g.limits.ProofTTL, now); err != nil {
		return err
	}

	token.Fingerprint = fingerprint
	return nil
}

// RevokeBinding drops one token binding.
func (g *TokenCertBindingGuard) RevokeBinding(tokenID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.tokens[tokenID]; !ok {
		return ErrUnknownToken
	}
	delete(g.tokens, tokenID)
	delete(g.secrets, tokenID)
	return nil
}

// Tokens lists the live tokens of one client ordered by token id.
func (g *TokenCertBindingGuard) Tokens(clientID string) []Token {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	now := time.Now().UTC()
	tokens := make([]Token, 0)
	for _, token := range g.tokens {
		if token.ClientID != clientID || !now.Before(token.ExpiresAt) {
			continue
		}
		tokens = append(tokens, *token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].TokenID < tokens[j].TokenID
	})
	return tokens
}

type snapshotToken struct {
	TokenID     string    `json:"token_id"`
	ClientID    string    `json:"client_id"`
	Fingerprint string    `json:"fingerprint"`
	Bound       bool      `json:"bound"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Secret      string    `json:"secret"`
}

type snapshotDocument struct {
	Version int             `json:"version"`
	Seq     int             `json:"seq"`
	Tokens  []snapshotToken `json:"tokens"`
}

// Snapshot serialises every token binding.
func (g *TokenCertBindingGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	document := snapshotDocument{
		Version: snapshotVersion,
		Seq:     g.seq,
		Tokens:  make([]snapshotToken, 0, len(g.tokens)),
	}
	for _, token := range g.tokens {
		document.Tokens = append(document.Tokens, snapshotToken{
			TokenID:     token.TokenID,
			ClientID:    token.ClientID,
			Fingerprint: token.Fingerprint,
			Bound:       token.Bound,
			IssuedAt:    token.IssuedAt,
			ExpiresAt:   token.ExpiresAt,
			Secret:      g.secrets[token.TokenID],
		})
	}
	sort.Slice(document.Tokens, func(i, j int) bool {
		return document.Tokens[i].TokenID < document.Tokens[j].TokenID
	})

	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return data, nil
}

// Restore replaces every token binding with a snapshot.
func (g *TokenCertBindingGuard) Restore(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return ErrBadSnapshot
	}

	var document snapshotDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if document.Version != snapshotVersion || document.Seq < 0 {
		return ErrBadSnapshot
	}

	tokens := make(map[string]*Token, len(document.Tokens))
	secrets := make(map[string]string, len(document.Tokens))
	maxSeq := 0
	for _, recorded := range document.Tokens {
		if !validSnapshotToken(recorded, tokens) {
			return ErrBadSnapshot
		}
		if recorded.Bound {
			fingerprint, err := g.NormalizeFingerprint(recorded.Fingerprint)
			if err != nil {
				return ErrBadSnapshot
			}
			recorded.Fingerprint = fingerprint
		} else if recorded.Fingerprint != "" {
			return ErrBadSnapshot
		}

		token := &Token{
			TokenID:     recorded.TokenID,
			ClientID:    strings.TrimSpace(recorded.ClientID),
			Fingerprint: recorded.Fingerprint,
			Bound:       recorded.Bound,
			IssuedAt:    recorded.IssuedAt,
			ExpiresAt:   recorded.ExpiresAt,
		}
		tokens[token.TokenID] = token
		secrets[token.TokenID] = recorded.Secret
		if seq, ok := tokenSequence(token.TokenID); ok && seq > maxSeq {
			maxSeq = seq
		}
	}

	if document.Seq < maxSeq {
		document.Seq = maxSeq
	}

	g.mu.Lock()
	g.tokens = tokens
	g.secrets = secrets
	g.seq = document.Seq
	g.mu.Unlock()
	return nil
}

func (g *TokenCertBindingGuard) boundedTTL(ttl time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		return 0, ErrInvalidTTL
	}
	if g.limits.MaxTokenTTL > 0 && ttl > g.limits.MaxTokenTTL {
		return g.limits.MaxTokenTTL, nil
	}
	return ttl, nil
}

func (g *TokenCertBindingGuard) issueLocked(clientID, fingerprint string, bound bool, ttl time.Duration) (Token, error) {
	now := time.Now().UTC()
	if bound {
		if err := g.checkBindingLimitLocked(clientID, now); err != nil {
			return Token{}, err
		}
	}

	secret, err := newTokenSecret()
	if err != nil {
		return Token{}, err
	}
	g.seq++
	tokenID := tokenIDPrefix + fmt.Sprintf("%0*d", tokenIDFixedDigits, g.seq)
	token := &Token{
		TokenID:     tokenID,
		ClientID:    clientID,
		Fingerprint: fingerprint,
		Bound:       bound,
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
	}
	g.tokens[tokenID] = token
	g.secrets[tokenID] = secret
	return *token, nil
}

func (g *TokenCertBindingGuard) checkBindingLimitLocked(clientID string, now time.Time) error {
	limit := g.limits.MaxTokensPerClient
	if limit <= 0 {
		return nil
	}
	active := 0
	for _, token := range g.tokens {
		if token.ClientID == clientID && token.Bound && now.Before(token.ExpiresAt) {
			active++
		}
	}
	if active >= limit {
		return ErrBindingLimit
	}
	return nil
}

func newTokenSecret() (string, error) {
	raw := make([]byte, tokenSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func parsePresentedToken(rawToken string) (tokenID, secret string, err error) {
	if !strings.HasPrefix(rawToken, tokenPrefix) {
		return "", "", ErrMalformedToken
	}
	parts := strings.SplitN(strings.TrimPrefix(rawToken, tokenPrefix), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrMalformedToken
	}
	return parts[0], parts[1], nil
}

func validateReauthProof(tokenID string, proof Proof, maxTTL time.Duration, now time.Time) error {
	if proof.Method != ProofMethodReauth {
		return ErrProofInvalid
	}
	if proof.TokenID != tokenID {
		return ErrProofMismatch
	}
	if strings.TrimSpace(proof.Subject) == "" || proof.IssuedAt.IsZero() {
		return ErrProofInvalid
	}
	if proof.IssuedAt.After(now) {
		return ErrProofInvalid
	}
	if maxTTL > 0 && now.Sub(proof.IssuedAt) > maxTTL {
		return ErrProofInvalid
	}

	expiresAt := proof.ExpiresAt
	if expiresAt.IsZero() {
		if maxTTL <= 0 {
			return ErrProofInvalid
		}
		expiresAt = proof.IssuedAt.Add(maxTTL)
	}
	if !expiresAt.After(proof.IssuedAt) || !now.Before(expiresAt) {
		return ErrProofInvalid
	}
	if maxTTL > 0 && expiresAt.Sub(proof.IssuedAt) > maxTTL {
		return ErrProofInvalid
	}
	return nil
}

func validSnapshotToken(recorded snapshotToken, tokens map[string]*Token) bool {
	if recorded.TokenID == "" || strings.TrimSpace(recorded.ClientID) == "" {
		return false
	}
	if strings.Contains(recorded.TokenID, ".") || recorded.Secret == "" {
		return false
	}
	if _, duplicate := tokens[recorded.TokenID]; duplicate {
		return false
	}
	if recorded.IssuedAt.IsZero() || !recorded.ExpiresAt.After(recorded.IssuedAt) {
		return false
	}
	return true
}

func tokenSequence(tokenID string) (int, bool) {
	if !strings.HasPrefix(tokenID, tokenIDPrefix) {
		return 0, false
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(tokenID, tokenIDPrefix))
	if err != nil || seq <= 0 {
		return 0, false
	}
	return seq, true
}
