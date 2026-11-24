// Package task issues single use invite, reset and verification tokens and
// keeps only hashed material at rest with per tenant purpose quotas.
package task

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Token purposes.
const (
	PurposeInvite = "invite"
	PurposeReset  = "reset"
	PurposeVerify = "verify"
)

// Token states.
const (
	TokenLive     = "live"
	TokenRedeemed = "redeemed"
	TokenRevoked  = "revoked"
	TokenExpired  = "expired"
)

// Errors returned by the vault.
var (
	ErrBlankArg        = errors.New("tenant and purpose are required")
	ErrInvalidTTL      = errors.New("token ttl must be positive")
	ErrInvalidCount    = errors.New("batch size must be positive")
	ErrBatchTooLarge   = errors.New("batch size above the configured cap")
	ErrQuotaExceeded   = errors.New("issue quota exhausted for this tenant and purpose")
	ErrTokenMalformed  = errors.New("raw token is not in id.secret form")
	ErrTokenNotFound   = errors.New("token not found")
	ErrTokenMismatch   = errors.New("token secret does not match the stored hash")
	ErrTokenExpired    = errors.New("token expired")
	ErrTokenRevoked    = errors.New("token revoked")
	ErrTokenConsumed   = errors.New("token already redeemed")
	ErrTenantMismatch  = errors.New("token belongs to another tenant")
	ErrPurposeMismatch = errors.New("token belongs to another purpose")
	ErrConfirmFailed   = errors.New("issuer confirmation failed")
	ErrBadSnapshot     = errors.New("snapshot payload is invalid")
)

// Internal safety defaults. They only apply when the matching VaultConfig
// knob is left at its zero value.
const (
	tokenIDBytes     = 16
	tokenSecretBytes = 32

	defaultMaxBatch          = 64
	defaultMaxLivePerPurpose = 100000
	fallbackBatchTTL         = 24 * time.Hour

	// maxPurposeFailures is the number of authenticated purpose mismatches a
	// live token tolerates before it is voided early.
	maxPurposeFailures = 5

	// hardMaxRecords is the process wide ceiling on retained entries.
	hardMaxRecords = 1_000_000

	snapshotVersion = 1
)

// Token is the issue time view of a token. Raw is only filled on issue, the
// stored record keeps the hash and never the secret.
type Token struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Purpose   string `json:"purpose"`
	State     string `json:"state"`
	ExpiresAt int64  `json:"expires_at"`
	Raw       string `json:"raw,omitempty"`
	Hash      string `json:"hash"`
}

// Subject is the account that redeemed a token.
type Subject struct {
	TokenID    string `json:"token_id"`
	TenantID   string `json:"tenant_id"`
	Purpose    string `json:"purpose"`
	RedeemedAt int64  `json:"redeemed_at"`
	Seq        int64  `json:"seq"`
}

// Quota is the per tenant per purpose accounting view.
type Quota struct {
	TenantID string `json:"tenant_id"`
	Purpose  string `json:"purpose"`
	Issued   int    `json:"issued"`
	Live     int    `json:"live"`
	Settled  int    `json:"settled"`
}

// VaultConfig carries the caps and the two injected dependencies.
type VaultConfig struct {
	MaxBatch          int
	MaxLivePerPurpose int
	DefaultTTL        time.Duration
	Now               func() time.Time
	Confirm           func(Subject) error
}

// record is the persisted representation. It deliberately has no field for
// the raw token or the plaintext secret.
type record struct {
	Token
	IssuedAt   int64  `json:"issued_at"`
	RedeemedAt int64  `json:"redeemed_at,omitempty"`
	SettledAt  int64  `json:"settled_at,omitempty"`
	RedeemSeq  int64  `json:"redeem_seq,omitempty"`
	Failures   int    `json:"failures,omitempty"`
	Confirmed  bool   `json:"confirmed,omitempty"`
	VoidReason string `json:"void_reason,omitempty"`
}

// OneTimeTokenVault owns the token table and the quota counters.
type OneTimeTokenVault struct {
	cfg     VaultConfig
	mu      sync.Mutex
	records map[string]*record
	// issued is tenant -> purpose -> cumulative issued count. The counter is
	// monotonic: redeem and purge never lower it, so quotas survive cleanup
	// and restart.
	issued map[string]map[string]int
	seq    int64
}

// NewOneTimeTokenVault builds a vault with the given caps.
func NewOneTimeTokenVault(cfg VaultConfig) *OneTimeTokenVault {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = defaultMaxBatch
	}
	if cfg.MaxLivePerPurpose <= 0 {
		cfg.MaxLivePerPurpose = defaultMaxLivePerPurpose
	}
	return &OneTimeTokenVault{
		cfg:     cfg,
		records: make(map[string]*record),
		issued:  make(map[string]map[string]int),
	}
}

func isBlank(value string) bool {
	return strings.TrimSpace(value) == ""
}

// tokenMaterial is the freshly minted id, secret and matching hash.
type tokenMaterial struct {
	id     string
	secret string
	hash   string
	raw    string
}

func mintTokenMaterial() (tokenMaterial, error) {
	idBytes := make([]byte, tokenIDBytes)
	secretBytes := make([]byte, tokenSecretBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return tokenMaterial{}, err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return tokenMaterial{}, err
	}
	id := "tok_" + hex.EncodeToString(idBytes)
	secret := hex.EncodeToString(secretBytes)
	digest := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(digest[:])
	return tokenMaterial{
		id:     id,
		secret: secret,
		hash:   hash,
		raw:    id + "." + secret,
	}, nil
}

func hashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

// parseRaw validates the id.secret wire form.
func parseRaw(raw string) (string, string, error) {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrTokenMalformed
	}
	return parts[0], parts[1], nil
}

// Issue mints one token for the tenant and purpose.
func (v *OneTimeTokenVault) Issue(tenantID, purpose string, ttl time.Duration) (Token, error) {
	if isBlank(tenantID) || isBlank(purpose) {
		return Token{}, ErrBlankArg
	}
	if ttl <= 0 {
		return Token{}, ErrInvalidTTL
	}

	now := v.cfg.Now()

	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.quotaFitsLocked(tenantID, purpose, 1); err != nil {
		return Token{}, err
	}

	material, err := v.uniqueMaterialLocked(nil)
	if err != nil {
		return Token{}, err
	}

	rec := &record{
		Token: Token{
			ID:        material.id,
			TenantID:  tenantID,
			Purpose:   purpose,
			State:     TokenLive,
			ExpiresAt: now.Add(ttl).UnixNano(),
			Hash:      material.hash,
		},
		IssuedAt: now.UnixNano(),
	}
	v.records[material.id] = rec
	v.addIssuedLocked(tenantID, purpose, 1)

	return rec.view(material.raw), nil
}

// IssueBatch mints a batch of tokens or nothing at all.
func (v *OneTimeTokenVault) IssueBatch(tenantID, purpose string, n int) ([]Token, error) {
	if n <= 0 {
		return nil, ErrInvalidCount
	}
	if n > v.cfg.MaxBatch {
		return nil, ErrBatchTooLarge
	}
	if isBlank(tenantID) || isBlank(purpose) {
		return nil, ErrBlankArg
	}

	ttl := v.cfg.DefaultTTL
	if ttl <= 0 {
		ttl = fallbackBatchTTL
	}
	now := v.cfg.Now()

	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.quotaFitsLocked(tenantID, purpose, n); err != nil {
		return nil, err
	}

	// Stage every record before touching the table so a generation failure
	// or a cap rejection leaves no partial batch behind.
	type staged struct {
		rec *record
		raw string
	}
	items := make([]staged, 0, n)
	pending := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		material, err := v.uniqueMaterialLocked(pending)
		if err != nil {
			return nil, err
		}
		pending[material.id] = struct{}{}
		items = append(items, staged{
			raw: material.raw,
			rec: &record{
				Token: Token{
					ID:        material.id,
					TenantID:  tenantID,
					Purpose:   purpose,
					State:     TokenLive,
					ExpiresAt: now.Add(ttl).UnixNano(),
					Hash:      material.hash,
				},
				IssuedAt: now.UnixNano(),
			},
		})
	}

	tokens := make([]Token, 0, n)
	for _, item := range items {
		v.records[item.rec.ID] = item.rec
		tokens = append(tokens, item.rec.view(item.raw))
	}
	v.addIssuedLocked(tenantID, purpose, n)
	return tokens, nil
}

// uniqueMaterialLocked mints material whose id is unused in the table and not
// already present in pending. The odds of a collision are negligible, the
// loop makes the guarantee exact.
func (v *OneTimeTokenVault) uniqueMaterialLocked(pending map[string]struct{}) (tokenMaterial, error) {
	for {
		material, err := mintTokenMaterial()
		if err != nil {
			return tokenMaterial{}, err
		}
		if _, exists := v.records[material.id]; exists {
			continue
		}
		if _, exists := pending[material.id]; exists {
			continue
		}
		return material, nil
	}
}

func (v *OneTimeTokenVault) quotaFitsLocked(tenantID, purpose string, n int) error {
	if len(v.records)+n > hardMaxRecords {
		return ErrQuotaExceeded
	}
	used := 0
	if purposes, ok := v.issued[tenantID]; ok {
		used = purposes[purpose]
	}
	if used+n > v.cfg.MaxLivePerPurpose {
		return ErrQuotaExceeded
	}
	return nil
}

func (v *OneTimeTokenVault) addIssuedLocked(tenantID, purpose string, n int) {
	purposes, ok := v.issued[tenantID]
	if !ok {
		purposes = make(map[string]int)
		v.issued[tenantID] = purposes
	}
	purposes[purpose] += n
}

func (v *OneTimeTokenVault) issuedCountLocked(tenantID, purpose string) int {
	if purposes, ok := v.issued[tenantID]; ok {
		return purposes[purpose]
	}
	return 0
}

// view returns the externally visible token. Raw is only populated by the
// issue paths, never from a stored record.
func (r *record) view(raw string) Token {
	tok := r.Token
	tok.Raw = raw
	return tok
}

// Redeem consumes a token atomically and returns the bound subject.
func (v *OneTimeTokenVault) Redeem(tenantID, purpose, raw string) (Subject, error) {
	if isBlank(tenantID) || isBlank(purpose) {
		return Subject{}, ErrBlankArg
	}
	id, secret, err := parseRaw(raw)
	if err != nil {
		return Subject{}, err
	}
	secretHash := hashSecret(secret)

	v.mu.Lock()
	rec, ok := v.records[id]
	if !ok {
		v.mu.Unlock()
		return Subject{}, ErrTokenNotFound
	}

	// Prove possession of the secret before any binding or state decision so
	// unauthenticated callers cannot count failures against a victim.
	if subtle.ConstantTimeCompare([]byte(secretHash), []byte(rec.Hash)) != 1 {
		v.mu.Unlock()
		return Subject{}, ErrTokenMismatch
	}
	if rec.TenantID != tenantID {
		v.mu.Unlock()
		return Subject{}, ErrTenantMismatch
	}
	if rec.Purpose != purpose {
		if rec.State == TokenLive {
			rec.Failures++
			if rec.Failures >= maxPurposeFailures {
				rec.State = TokenRevoked
				rec.VoidReason = "purpose-mismatch-limit"
				rec.SettledAt = v.cfg.Now().UnixNano()
			}
		}
		v.mu.Unlock()
		return Subject{}, ErrPurposeMismatch
	}

	nowNano := v.cfg.Now().UnixNano()
	switch rec.State {
	case TokenRevoked:
		v.mu.Unlock()
		return Subject{}, ErrTokenRevoked
	case TokenRedeemed:
		v.mu.Unlock()
		return Subject{}, ErrTokenConsumed
	case TokenExpired:
		v.mu.Unlock()
		return Subject{}, ErrTokenExpired
	case TokenLive:
		if nowNano >= rec.ExpiresAt {
			rec.State = TokenExpired
			rec.SettledAt = nowNano
			v.mu.Unlock()
			return Subject{}, ErrTokenExpired
		}
	default:
		v.mu.Unlock()
		return Subject{}, ErrTokenNotFound
	}

	// This compare-and-transition is the single commit point. The mutex makes
	// sure at most one caller ever reaches it for a given record.
	v.seq++
	seq := v.seq
	rec.State = TokenRedeemed
	rec.RedeemedAt = nowNano
	rec.SettledAt = nowNano
	rec.RedeemSeq = seq
	subject := Subject{
		TokenID:    rec.ID,
		TenantID:   rec.TenantID,
		Purpose:    rec.Purpose,
		RedeemedAt: nowNano,
		Seq:        seq,
	}
	confirm := v.cfg.Confirm
	v.mu.Unlock()

	// Run the side effect outside the lock: the token is already consumed, so
	// concurrent or retried redeems cannot revive it. A failed confirmation
	// only voids the record, it never returns it to live.
	if confirm != nil {
		if confirmErr := confirm(subject); confirmErr != nil {
			v.mu.Lock()
			if latest, exists := v.records[id]; exists && latest.State == TokenRedeemed && latest.RedeemSeq == seq {
				latest.State = TokenRevoked
				latest.VoidReason = "confirmation-failed"
			}
			v.mu.Unlock()
			return subject, fmt.Errorf("%w: confirmation callback failed: %w", ErrConfirmFailed, confirmErr)
		}
		v.mu.Lock()
		if latest, exists := v.records[id]; exists && latest.RedeemSeq == seq {
			latest.Confirmed = true
		}
		v.mu.Unlock()
	}

	return subject, nil
}

// Revoke kills a live token and keeps the record for audit.
func (v *OneTimeTokenVault) Revoke(tokenID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	rec, ok := v.records[tokenID]
	if !ok {
		return ErrTokenNotFound
	}
	switch rec.State {
	case TokenRevoked:
		return nil
	case TokenLive:
		rec.State = TokenRevoked
		rec.SettledAt = v.cfg.Now().UnixNano()
		return nil
	case TokenRedeemed:
		return ErrTokenConsumed
	case TokenExpired:
		return ErrTokenExpired
	default:
		return ErrTokenNotFound
	}
}

// Purge drops settled and expired records and reports how many went away.
// Redeemed and revoked records are settled and always removable; live records
// are removed once the purge time reaches their expiry.
func (v *OneTimeTokenVault) Purge(now time.Time) int {
	cutoff := now.UnixNano()

	v.mu.Lock()
	defer v.mu.Unlock()

	removed := 0
	for id, rec := range v.records {
		switch rec.State {
		case TokenRedeemed, TokenRevoked, TokenExpired:
			delete(v.records, id)
			removed++
		case TokenLive:
			if cutoff >= rec.ExpiresAt {
				delete(v.records, id)
				removed++
			}
		}
	}
	return removed
}

// Lookup reads one record without the secret.
func (v *OneTimeTokenVault) Lookup(tokenID string) (Token, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	rec, ok := v.records[tokenID]
	if !ok {
		return Token{}, false
	}
	return rec.view(""), true
}

// Count reports how many records are currently retained.
func (v *OneTimeTokenVault) Count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.records)
}

// Stats reports the quota view of one tenant and purpose.
func (v *OneTimeTokenVault) Stats(tenantID, purpose string) Quota {
	v.mu.Lock()
	defer v.mu.Unlock()

	quota := Quota{
		TenantID: tenantID,
		Purpose:  purpose,
		Issued:   v.issuedCountLocked(tenantID, purpose),
	}
	for _, rec := range v.records {
		if rec.TenantID != tenantID || rec.Purpose != purpose {
			continue
		}
		switch rec.State {
		case TokenLive:
			quota.Live++
		case TokenRedeemed, TokenRevoked:
			quota.Settled++
		}
	}
	return quota
}

// snapshotPayload is the canonical full state of the vault.
type snapshotPayload struct {
	Seq     int64                     `json:"seq"`
	Issued  map[string]map[string]int `json:"issued"`
	Records []record                  `json:"records"`
}

// snapshotEnvelope seals the payload with a checksum so a torn or tampered
// snapshot is rejected as ErrBadSnapshot instead of partially restored.
type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Snapshot serialises records, the quota counters and the sequence.
func (v *OneTimeTokenVault) Snapshot() ([]byte, error) {
	v.mu.Lock()
	payload := snapshotPayload{
		Seq:     v.seq,
		Issued:  make(map[string]map[string]int, len(v.issued)),
		Records: make([]record, 0, len(v.records)),
	}
	for tenant, purposes := range v.issued {
		copied := make(map[string]int, len(purposes))
		for purpose, count := range purposes {
			copied[purpose] = count
		}
		payload.Issued[tenant] = copied
	}
	for _, rec := range v.records {
		payload.Records = append(payload.Records, *rec)
	}
	v.mu.Unlock()

	sort.Slice(payload.Records, func(i, j int) bool {
		return payload.Records[i].ID < payload.Records[j].ID
	})

	rawPayload, err := json.Marshal(&payload)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(rawPayload)
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		Checksum: hex.EncodeToString(digest[:]),
		Payload:  rawPayload,
	}
	return json.Marshal(&envelope)
}

// Restore replaces the vault state with a snapshot, all or nothing.
func (v *OneTimeTokenVault) Restore(data []byte) error {
	next, err := decodeSnapshot(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}

	v.mu.Lock()
	v.records = next.records
	v.issued = next.issued
	v.seq = next.seq
	v.mu.Unlock()
	return nil
}

type restoredState struct {
	records map[string]*record
	issued  map[string]map[string]int
	seq     int64
}

func decodeSnapshot(data []byte) (*restoredState, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("empty snapshot")
	}

	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if envelope.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported version %d", envelope.Version)
	}
	if len(envelope.Checksum) != 64 || len(envelope.Payload) == 0 {
		return nil, errors.New("snapshot seal is missing")
	}
	digest := sha256.Sum256(envelope.Payload)
	wantChecksum := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(wantChecksum), []byte(envelope.Checksum)) != 1 {
		return nil, errors.New("snapshot checksum mismatch")
	}

	var payload snapshotPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, err
	}
	if payload.Seq < 0 {
		return nil, errors.New("negative sequence")
	}

	issued := make(map[string]map[string]int, len(payload.Issued))
	for tenant, purposes := range payload.Issued {
		if isBlank(tenant) {
			return nil, errors.New("blank tenant quota key")
		}
		copied := make(map[string]int, len(purposes))
		for purpose, count := range purposes {
			if isBlank(purpose) {
				return nil, errors.New("blank purpose quota key")
			}
			if count < 0 {
				return nil, errors.New("negative issued counter")
			}
			copied[purpose] = count
		}
		issued[tenant] = copied
	}

	records := make(map[string]*record, len(payload.Records))
	for i := range payload.Records {
		rec := payload.Records[i]
		if err := validateSnapshotRecord(&rec); err != nil {
			return nil, err
		}
		if _, duplicated := records[rec.ID]; duplicated {
			return nil, fmt.Errorf("duplicate record id %q", rec.ID)
		}
		// Never trust serialised raw material even if a payload carries it.
		rec.Raw = ""
		records[rec.ID] = &rec
	}

	return &restoredState{
		records: records,
		issued:  issued,
		seq:     payload.Seq,
	}, nil
}

func validateSnapshotRecord(rec *record) error {
	if rec.ID == "" {
		return errors.New("record missing id")
	}
	if isBlank(rec.TenantID) || isBlank(rec.Purpose) {
		return fmt.Errorf("record %q missing tenant or purpose", rec.ID)
	}
	if rec.Hash == "" {
		return fmt.Errorf("record %q missing hash", rec.ID)
	}
	if rec.ExpiresAt <= 0 {
		return fmt.Errorf("record %q missing expiry", rec.ID)
	}
	if rec.IssuedAt < 0 || rec.RedeemedAt < 0 || rec.SettledAt < 0 || rec.Failures < 0 || rec.RedeemSeq < 0 {
		return fmt.Errorf("record %q has negative timestamps or counters", rec.ID)
	}
	switch rec.State {
	case TokenLive, TokenRedeemed, TokenRevoked, TokenExpired:
	default:
		return fmt.Errorf("record %q has unknown state %q", rec.ID, rec.State)
	}
	return nil
}
