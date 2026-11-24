// Package task binds identity token claims to a login session and grades the
// audience scope, authorized party, authentication age and one-time nonce use
// of every presented identity token.
package task

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors returned by the binder.
var (
	ErrBlankIdentity     = errors.New("blank identity")
	ErrNoAudience        = errors.New("no audience")
	ErrAudienceLimit     = errors.New("audience limit")
	ErrSessionLimit      = errors.New("session limit")
	ErrUnknownSession    = errors.New("unknown session")
	ErrBadToken          = errors.New("bad token")
	ErrAudienceMismatch  = errors.New("audience mismatch")
	ErrSubjectMismatch   = errors.New("subject mismatch")
	ErrAuthorizedParty   = errors.New("authorized party missing")
	ErrAuthTimeMissing   = errors.New("auth time missing")
	ErrAuthAgeExceeded   = errors.New("auth age exceeded")
	ErrClockAhead        = errors.New("clock ahead of token")
	ErrTokenExpired      = errors.New("token expired")
	ErrTokenPairMismatch = errors.New("token pair mismatch")
	ErrNonceRequired     = errors.New("nonce required")
	ErrNonceUnknown      = errors.New("nonce unknown")
	ErrNonceReused       = errors.New("nonce reused")
	ErrBadSnapshot       = errors.New("bad snapshot")
)

// Reasons attached to a validation result and to the re-authentication probe.
const (
	ReasonOK             = "ok"
	ReasonUnknownSession = "unknown_session"
	ReasonBadToken       = "bad_token"
	ReasonSubject        = "subject_mismatch"
	ReasonAudience       = "audience_mismatch"
	ReasonAuthorized     = "authorized_party_missing"
	ReasonNoAuthTime     = "auth_time_missing"
	ReasonAuthAge        = "auth_age_exceeded"
	ReasonClockAhead     = "clock_ahead"
	ReasonExpired        = "token_expired"
	ReasonPair           = "token_pair_mismatch"
	ReasonNonce          = "nonce_reused"
)

const (
	tokenPrefix     = "idc1."
	snapshotVersion = 1
)

// BinderLimits caps sessions, audiences and the accepted authentication age.
type BinderLimits struct {
	MaxSessionsPerUser   int
	MaxAudiencesPerToken int
	MaxAuthAge           time.Duration
	ClockSkew            time.Duration
	NonceTTL             time.Duration
}

// BinderConfig carries the client identity the binder speaks for.
type BinderConfig struct {
	ClientID string
	Limits   BinderLimits
}

// Claims is the decoded claim set of one identity token.
type Claims struct {
	Subject         string
	Audiences       []string
	AuthorizedParty string
	Issuer          string
	IssuedAt        time.Time
	AuthTime        time.Time
	ExpiresAt       time.Time
	Nonce           string
	AccessTokenHash string
}

// Binding is the session side of the claim binding.
type Binding struct {
	SessionID string
	UserID    string
	Audiences []string
	TokenHash string
	Seq       int
	BoundAt   time.Time
	AuthAt    time.Time
}

// Result is the outcome of one validation attempt.
type Result struct {
	Valid     bool
	Reason    string
	SessionID string
	UserID    string
	Audiences []string
	AuthAge   time.Duration
}

type nonceRecord struct {
	SessionID string    `json:"session_id"`
	Nonce     string    `json:"nonce"`
	Consumed  bool      `json:"consumed"`
	IssuedAt  time.Time `json:"issued_at"`
}

type snapshotState struct {
	Version  int           `json:"version"`
	Seq      int           `json:"seq"`
	Sessions []Binding     `json:"sessions"`
	Nonces   []nonceRecord `json:"nonces"`
}

// IdTokenClaimBinder keeps session bindings, nonce state and session ordering.
type IdTokenClaimBinder struct {
	mu       sync.Mutex
	cfg      BinderConfig
	sessions map[string]Binding
	nonces   map[string]nonceRecord
	seq      int
}

// NewIdTokenClaimBinder builds a binder for one relying client.
func NewIdTokenClaimBinder(cfg BinderConfig) *IdTokenClaimBinder {
	return &IdTokenClaimBinder{
		cfg:      cfg,
		sessions: make(map[string]Binding),
		nonces:   make(map[string]nonceRecord),
	}
}

// Digest is the pairing digest kept for the access token of a session.
func (b *IdTokenClaimBinder) Digest(accessToken string) string {
	sum := sha256.Sum256([]byte(accessToken))
	return hex.EncodeToString(sum[:])
}

// Seal encodes a claim set into the opaque identity token string.
func (b *IdTokenClaimBinder) Seal(claims Claims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// Parse decodes an identity token string back into its claim set.
func (b *IdTokenClaimBinder) Parse(raw string) (Claims, error) {
	if !strings.HasPrefix(raw, tokenPrefix) {
		return Claims{}, ErrBadToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw[len(tokenPrefix):])
	if err != nil {
		return Claims{}, ErrBadToken
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, ErrBadToken
	}
	return claims, nil
}

// Bind records the claim binding of one login session.
func (b *IdTokenClaimBinder) Bind(sessionID string, claims Claims) (Binding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(claims.Subject) == "" {
		return Binding{}, ErrBlankIdentity
	}
	if !hasAudience(claims.Audiences) {
		return Binding{}, ErrNoAudience
	}
	if limit := b.cfg.Limits.MaxAudiencesPerToken; limit > 0 && len(claims.Audiences) > limit {
		return Binding{}, ErrAudienceLimit
	}

	existing, exists := b.sessions[sessionID]
	if !exists || existing.UserID != claims.Subject {
		limit := b.cfg.Limits.MaxSessionsPerUser
		if limit > 0 && b.sessionCountLocked(claims.Subject) >= limit {
			return Binding{}, ErrSessionLimit
		}
	}

	b.seq++
	binding := Binding{
		SessionID: sessionID,
		UserID:    claims.Subject,
		Audiences: cloneStrings(claims.Audiences),
		TokenHash: claims.AccessTokenHash,
		Seq:       b.seq,
		BoundAt:   time.Now(),
		AuthAt:    claims.AuthTime,
	}
	b.sessions[sessionID] = binding

	if claims.Nonce != "" {
		key := nonceKey(sessionID, claims.Nonce)
		if _, ok := b.nonces[key]; !ok {
			b.nonces[key] = nonceRecord{
				SessionID: sessionID,
				Nonce:     claims.Nonce,
				IssuedAt:  claims.IssuedAt,
			}
		}
	}

	return cloneBinding(binding), nil
}

// Validate checks a presented identity token against the session binding.
func (b *IdTokenClaimBinder) Validate(sessionID, rawIDToken string, now time.Time) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(sessionID) == "" {
		return failedResult("", "", nil, ReasonBadToken, ErrBlankIdentity)
	}
	binding, ok := b.sessions[sessionID]
	if !ok {
		return failedResult(sessionID, "", nil, ReasonUnknownSession, ErrUnknownSession)
	}

	claims, err := b.Parse(rawIDToken)
	if err != nil {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonBadToken, ErrBadToken)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonBadToken, ErrBlankIdentity)
	}
	if claims.Subject != binding.UserID {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonSubject, ErrSubjectMismatch)
	}
	if !hasAudience(claims.Audiences) {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonAudience, ErrNoAudience)
	}
	if limit := b.cfg.Limits.MaxAudiencesPerToken; limit > 0 && len(claims.Audiences) > limit {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonAudience, ErrAudienceLimit)
	}
	if len(claims.Audiences) > 1 && strings.TrimSpace(claims.AuthorizedParty) == "" {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonAuthorized, ErrAuthorizedParty)
	}
	if !audienceSubset(claims.Audiences, binding.Audiences) {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonAudience, ErrAudienceMismatch)
	}
	if err := b.checkAuthorizedParty(claims); err != nil {
		reason := ReasonAuthorized
		if errors.Is(err, ErrAudienceMismatch) {
			reason = ReasonAudience
		}
		return failedResult(sessionID, binding.UserID, binding.Audiences, reason, err)
	}

	if claims.AuthTime.IsZero() {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonNoAuthTime, ErrAuthTimeMissing)
	}
	authAge := now.Sub(claims.AuthTime)
	if authAge < 0 {
		skew := b.cfg.Limits.ClockSkew
		if skew < 0 {
			skew = 0
		}
		if -authAge > skew {
			return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonClockAhead, ErrClockAhead)
		}
		authAge = 0
	}
	if maxAge := b.cfg.Limits.MaxAuthAge; maxAge > 0 && authAge > maxAge {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonAuthAge, ErrAuthAgeExceeded)
	}
	if claims.ExpiresAt.IsZero() || !now.Before(claims.ExpiresAt) {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonExpired, ErrTokenExpired)
	}
	if subtle.ConstantTimeCompare([]byte(claims.AccessTokenHash), []byte(binding.TokenHash)) != 1 {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonPair, ErrTokenPairMismatch)
	}

	if claims.Nonce == "" {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonNonce, ErrNonceRequired)
	}
	key := nonceKey(sessionID, claims.Nonce)
	record, ok := b.nonces[key]
	if !ok {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonNonce, ErrNonceUnknown)
	}
	if record.Consumed {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonNonce, ErrNonceReused)
	}
	if b.nonceExpiredLocked(record, now) {
		return failedResult(sessionID, binding.UserID, binding.Audiences, ReasonNonce, ErrNonceUnknown)
	}
	record.Consumed = true
	b.nonces[key] = record

	return Result{
		Valid:     true,
		Reason:    ReasonOK,
		SessionID: sessionID,
		UserID:    binding.UserID,
		Audiences: cloneStrings(binding.Audiences),
		AuthAge:   authAge,
	}, nil
}

// IssueNonce mints a session scoped nonce that may be used exactly once.
func (b *IdTokenClaimBinder) IssueNonce(sessionID string, now time.Time) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(sessionID) == "" {
		return "", ErrBlankIdentity
	}
	for {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		nonce := hex.EncodeToString(raw[:])
		key := nonceKey(sessionID, nonce)
		if _, exists := b.nonces[key]; exists {
			continue
		}
		b.nonces[key] = nonceRecord{
			SessionID: sessionID,
			Nonce:     nonce,
			IssuedAt:  now,
		}
		return nonce, nil
	}
}

// ConsumeNonce burns an outstanding nonce of one session.
func (b *IdTokenClaimBinder) ConsumeNonce(sessionID, nonce string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(sessionID) == "" {
		return ErrBlankIdentity
	}
	key := nonceKey(sessionID, nonce)
	record, ok := b.nonces[key]
	if !ok {
		return ErrNonceUnknown
	}
	if record.Consumed {
		return ErrNonceReused
	}
	record.Consumed = true
	b.nonces[key] = record
	return nil
}

// NeedsReauth reports whether the session must re-authenticate and why.
func (b *IdTokenClaimBinder) NeedsReauth(sessionID string, now time.Time) (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(sessionID) == "" {
		return true, ReasonUnknownSession
	}
	binding, ok := b.sessions[sessionID]
	if !ok {
		return true, ReasonUnknownSession
	}
	if binding.AuthAt.IsZero() {
		return true, ReasonNoAuthTime
	}
	if binding.AuthAt.After(now) {
		skew := b.cfg.Limits.ClockSkew
		if skew < 0 {
			skew = 0
		}
		if binding.AuthAt.Sub(now) > skew {
			return true, ReasonClockAhead
		}
		return false, ""
	}
	if maxAge := b.cfg.Limits.MaxAuthAge; maxAge > 0 && now.Sub(binding.AuthAt) > maxAge {
		return true, ReasonAuthAge
	}
	return false, ""
}

// Sessions lists the live session ids of one user in ascending order.
func (b *IdTokenClaimBinder) Sessions(userID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	var sessions []string
	for sessionID, binding := range b.sessions {
		if binding.UserID == userID {
			sessions = append(sessions, sessionID)
		}
	}
	sort.Strings(sessions)
	return sessions
}

// Revoke drops one session binding.
func (b *IdTokenClaimBinder) Revoke(sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if strings.TrimSpace(sessionID) == "" {
		return ErrBlankIdentity
	}
	if _, ok := b.sessions[sessionID]; !ok {
		return ErrUnknownSession
	}
	delete(b.sessions, sessionID)
	return nil
}

// Snapshot serialises bindings, nonce state and the session sequence.
func (b *IdTokenClaimBinder) Snapshot() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	state := snapshotState{
		Version:  snapshotVersion,
		Seq:      b.seq,
		Sessions: make([]Binding, 0, len(b.sessions)),
		Nonces:   make([]nonceRecord, 0, len(b.nonces)),
	}
	for _, binding := range b.sessions {
		state.Sessions = append(state.Sessions, cloneBinding(binding))
	}
	sort.Slice(state.Sessions, func(i, j int) bool {
		if state.Sessions[i].Seq == state.Sessions[j].Seq {
			return state.Sessions[i].SessionID < state.Sessions[j].SessionID
		}
		return state.Sessions[i].Seq < state.Sessions[j].Seq
	})
	for _, record := range b.nonces {
		state.Nonces = append(state.Nonces, record)
	}
	sort.Slice(state.Nonces, func(i, j int) bool {
		if state.Nonces[i].SessionID == state.Nonces[j].SessionID {
			return state.Nonces[i].Nonce < state.Nonces[j].Nonce
		}
		return state.Nonces[i].SessionID < state.Nonces[j].SessionID
	})

	return json.Marshal(state)
}

// Restore replaces the binder state with a snapshot.
func (b *IdTokenClaimBinder) Restore(data []byte) error {
	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return ErrBadSnapshot
	}
	if state.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	sessions := make(map[string]Binding, len(state.Sessions))
	maxSeq := 0
	for _, binding := range state.Sessions {
		if strings.TrimSpace(binding.SessionID) == "" || strings.TrimSpace(binding.UserID) == "" {
			return ErrBadSnapshot
		}
		if _, exists := sessions[binding.SessionID]; exists {
			return ErrBadSnapshot
		}
		binding.Audiences = cloneStrings(binding.Audiences)
		sessions[binding.SessionID] = binding
		if binding.Seq > maxSeq {
			maxSeq = binding.Seq
		}
	}

	nonces := make(map[string]nonceRecord, len(state.Nonces))
	for _, record := range state.Nonces {
		if strings.TrimSpace(record.SessionID) == "" || record.Nonce == "" {
			return ErrBadSnapshot
		}
		key := nonceKey(record.SessionID, record.Nonce)
		if _, exists := nonces[key]; exists {
			return ErrBadSnapshot
		}
		nonces[key] = record
	}

	seq := state.Seq
	if seq < maxSeq {
		seq = maxSeq
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions = sessions
	b.nonces = nonces
	b.seq = seq
	return nil
}

func (b *IdTokenClaimBinder) checkAuthorizedParty(claims Claims) error {
	if len(claims.Audiences) <= 1 {
		return nil
	}
	authorizedParty := strings.TrimSpace(claims.AuthorizedParty)
	if authorizedParty == "" {
		return ErrAuthorizedParty
	}
	if authorizedParty != b.cfg.ClientID {
		return ErrAudienceMismatch
	}
	return nil
}

func (b *IdTokenClaimBinder) sessionCountLocked(userID string) int {
	count := 0
	for _, binding := range b.sessions {
		if binding.UserID == userID {
			count++
		}
	}
	return count
}

func (b *IdTokenClaimBinder) nonceExpiredLocked(record nonceRecord, now time.Time) bool {
	ttl := b.cfg.Limits.NonceTTL
	if ttl <= 0 || record.IssuedAt.IsZero() {
		return false
	}
	return now.Sub(record.IssuedAt) > ttl
}

func nonceKey(sessionID, nonce string) string {
	return sessionID + "\x00" + nonce
}

func hasAudience(audiences []string) bool {
	for _, audience := range audiences {
		if strings.TrimSpace(audience) != "" {
			return true
		}
	}
	return false
}

func audienceSubset(candidate, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, audience := range allowed {
		set[audience] = struct{}{}
	}
	for _, audience := range candidate {
		if _, ok := set[audience]; !ok {
			return false
		}
	}
	return true
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneBinding(binding Binding) Binding {
	binding.Audiences = cloneStrings(binding.Audiences)
	return binding
}

func failedResult(sessionID, userID string, audiences []string, reason string, err error) (Result, error) {
	return Result{
		Valid:     false,
		Reason:    reason,
		SessionID: sessionID,
		UserID:    userID,
		Audiences: cloneStrings(audiences),
	}, err
}
