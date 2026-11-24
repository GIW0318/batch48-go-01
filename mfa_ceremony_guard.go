// Package task keeps the multi factor enrollment ledger and drives one
// authentication ceremony at a time from challenge to single use grant.
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Factor enrollment states.
const (
	FactorPending     = "pending"
	FactorActive      = "active"
	FactorCompromised = "compromised"
	FactorRemoved     = "removed"
)

// Factor kinds.
const (
	KindKnowledge  = "knowledge"
	KindPossession = "possession"
	KindInherence  = "inherence"
)

// Ceremony states.
const (
	CeremonyOpen     = "open"
	CeremonyRedeemed = "redeemed"

	ceremonyInvalid = "invalidated"
)

// Grant states.
const (
	GrantValid = "valid"
)

// Verdict reason codes.
const (
	ReasonOK              = "reason_ok"
	ReasonProofMismatch   = "reason_proof_mismatch"
	ReasonDuplicateFactor = "reason_duplicate_factor"
	ReasonFactorInactive  = "reason_factor_inactive"
	ReasonFactorUnknown   = "reason_factor_unknown"
	ReasonAccountLocked   = "reason_account_locked"
)

// Errors returned by the guard.
var (
	ErrBlankArg           = errors.New("user, factor and session identifiers are required")
	ErrFactorKind         = errors.New("unknown factor kind")
	ErrFactorExists       = errors.New("factor already enrolled")
	ErrFactorNotFound     = errors.New("factor not found")
	ErrFactorLimit        = errors.New("factor limit reached for this account")
	ErrFactorState        = errors.New("factor is not in a state that allows the operation")
	ErrNoActiveFactor     = errors.New("account has no active factor")
	ErrCeremonyInFlight   = errors.New("another ceremony is already in flight")
	ErrCeremonyNotFound   = errors.New("ceremony not found")
	ErrCeremonyConsumed   = errors.New("ceremony already consumed")
	ErrCeremonyIncomplete = errors.New("ceremony has not collected enough factors")
	ErrAccountLocked      = errors.New("account locked after repeated factor failures")
	ErrBadSnapshot        = errors.New("snapshot payload is invalid")
)

// Factor is one registered authentication factor.
type Factor struct {
	UserID   string `json:"user_id"`
	FactorID string `json:"factor_id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Failures int    `json:"failures"`
}

// Ceremony is one in flight multi factor challenge bound to a session.
type Ceremony struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Challenge string `json:"challenge"`
	State     string `json:"state"`
	Verified  int    `json:"verified"`
	Seq       int64  `json:"seq"`
}

// Verdict is the outcome of one factor verification.
type Verdict struct {
	CeremonyID string `json:"ceremony_id"`
	FactorID   string `json:"factor_id"`
	Accepted   bool   `json:"accepted"`
	Reason     string `json:"reason"`
	Verified   int    `json:"verified"`
}

// Grant is the single use artifact minted by a redeemed ceremony.
type Grant struct {
	ID         string   `json:"id"`
	CeremonyID string   `json:"ceremony_id"`
	UserID     string   `json:"user_id"`
	SessionID  string   `json:"session_id"`
	Factors    []string `json:"factors"`
	State      string   `json:"state"`
	Seq        int64    `json:"seq"`
}

// GuardConfig carries the caps, the lock window, the factor target and the
// two injected dependencies of the guard.
type GuardConfig struct {
	MaxFactorsPerUser int
	MaxFailures       int
	LockDuration      time.Duration
	RequiredFactors   int
	Now               func() time.Time
	ProofVerifier     func(factorID, proof string) bool
}

type ceremonyRecord struct {
	Ceremony
	verified map[string]struct{}
}

// MfaCeremonyGuard owns the factor ledger, the ceremony table and the locks.
type MfaCeremonyGuard struct {
	mu sync.Mutex

	cfg GuardConfig

	factors         map[string]map[string]*Factor
	ceremonies      map[string]*ceremonyRecord
	openByUser      map[string]string
	grants          map[string]Grant
	locks           map[string]time.Time
	accountFailures map[string]int
	seq             int64
}

// NewMfaCeremonyGuard builds a guard with the given policy.
func NewMfaCeremonyGuard(cfg GuardConfig) *MfaCeremonyGuard {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ProofVerifier == nil {
		cfg.ProofVerifier = func(string, string) bool { return false }
	}
	return &MfaCeremonyGuard{
		cfg:             cfg,
		factors:         make(map[string]map[string]*Factor),
		ceremonies:      make(map[string]*ceremonyRecord),
		openByUser:      make(map[string]string),
		grants:          make(map[string]Grant),
		locks:           make(map[string]time.Time),
		accountFailures: make(map[string]int),
	}
}

// EnrollFactor registers a factor in the pending state.
func (g *MfaCeremonyGuard) EnrollFactor(userID, factorID, kind string) (Factor, error) {
	if blank(userID) || blank(factorID) {
		return Factor{}, ErrBlankArg
	}
	if !validKind(kind) {
		return Factor{}, ErrFactorKind
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	userFactors := g.factors[userID]
	if userFactors != nil {
		if _, exists := userFactors[factorID]; exists {
			return Factor{}, ErrFactorExists
		}
	}
	if countLiveFactors(userFactors) >= g.cfg.MaxFactorsPerUser {
		return Factor{}, ErrFactorLimit
	}
	if userFactors == nil {
		userFactors = make(map[string]*Factor)
		g.factors[userID] = userFactors
	}

	factor := &Factor{
		UserID:   userID,
		FactorID: factorID,
		Kind:     kind,
		State:    FactorPending,
	}
	userFactors[factorID] = factor
	return *factor, nil
}

// ConfirmFactor promotes a pending factor to active.
func (g *MfaCeremonyGuard) ConfirmFactor(userID, factorID string) error {
	if blank(userID) || blank(factorID) {
		return ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	factor, ok := g.factorLocked(userID, factorID)
	if !ok {
		return ErrFactorNotFound
	}
	if factor.State != FactorPending {
		return ErrFactorState
	}
	factor.State = FactorActive
	return nil
}

// BeginCeremony opens the only allowed challenge for an account.
func (g *MfaCeremonyGuard) BeginCeremony(userID, sessionID string) (Ceremony, error) {
	if blank(userID) || blank(sessionID) {
		return Ceremony{}, ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	if g.lockActiveLocked(userID, now) {
		return Ceremony{}, ErrAccountLocked
	}

	if ceremonyID, ok := g.openByUser[userID]; ok {
		record := g.ceremonies[ceremonyID]
		if record != nil && record.State == CeremonyOpen {
			return Ceremony{}, ErrCeremonyInFlight
		}
		delete(g.openByUser, userID)
	}

	if !g.hasActiveFactorLocked(userID) {
		return Ceremony{}, ErrNoActiveFactor
	}

	g.seq++
	record := &ceremonyRecord{
		Ceremony: Ceremony{
			ID:        fmt.Sprintf("ceremony-%d", g.seq),
			UserID:    userID,
			SessionID: sessionID,
			Challenge: fmt.Sprintf("challenge-%d-%d", g.seq, now.UnixNano()),
			State:     CeremonyOpen,
			Seq:       g.seq,
		},
		verified: make(map[string]struct{}),
	}
	g.ceremonies[record.ID] = record
	g.openByUser[userID] = record.ID
	return record.Ceremony, nil
}

// VerifyFactor checks one distinct factor against the open ceremony.
func (g *MfaCeremonyGuard) VerifyFactor(ceremonyID, factorID, proof string) (Verdict, error) {
	if blank(ceremonyID) || blank(factorID) {
		return Verdict{}, ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.ceremonies[ceremonyID]
	if !ok {
		return Verdict{}, ErrCeremonyNotFound
	}

	now := g.now()
	if g.lockActiveLocked(record.UserID, now) {
		return g.verdictLocked(record, factorID, false, ReasonAccountLocked), nil
	}
	if record.State != CeremonyOpen {
		return Verdict{}, ErrCeremonyConsumed
	}

	factor, ok := g.factorLocked(record.UserID, factorID)
	if !ok {
		return g.verdictLocked(record, factorID, false, ReasonFactorUnknown), nil
	}
	if factor.State != FactorActive {
		return g.verdictLocked(record, factorID, false, ReasonFactorInactive), nil
	}
	if _, seen := record.verified[factorID]; seen {
		return g.verdictLocked(record, factorID, false, ReasonDuplicateFactor), nil
	}

	if !g.cfg.ProofVerifier(factorID, proof) {
		factor.Failures++
		if countsTowardsLock(factor.Kind) {
			g.accountFailures[record.UserID]++
			if g.cfg.MaxFailures > 0 && g.accountFailures[record.UserID] >= g.cfg.MaxFailures {
				g.lockAccountLocked(record.UserID, now.Add(g.cfg.LockDuration))
			}
		}
		return g.verdictLocked(record, factorID, false, ReasonProofMismatch), nil
	}

	factor.Failures = 0
	delete(g.accountFailures, record.UserID)
	record.verified[factorID] = struct{}{}
	record.Verified++
	return g.verdictLocked(record, factorID, true, ReasonOK), nil
}

// RedeemCeremony turns a complete ceremony into one single use grant.
func (g *MfaCeremonyGuard) RedeemCeremony(ceremonyID string) (Grant, error) {
	if blank(ceremonyID) {
		return Grant{}, ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.ceremonies[ceremonyID]
	if !ok {
		return Grant{}, ErrCeremonyNotFound
	}
	now := g.now()
	if g.lockActiveLocked(record.UserID, now) {
		return Grant{}, ErrAccountLocked
	}
	if record.State != CeremonyOpen {
		return Grant{}, ErrCeremonyConsumed
	}
	if record.Verified < g.cfg.RequiredFactors {
		return Grant{}, ErrCeremonyIncomplete
	}

	factorIDs := make([]string, 0, len(record.verified))
	for factorID := range record.verified {
		factor, ok := g.factorLocked(record.UserID, factorID)
		if !ok || factor.State != FactorActive {
			return Grant{}, ErrCeremonyIncomplete
		}
		factorIDs = append(factorIDs, factorID)
	}
	if len(factorIDs) < g.cfg.RequiredFactors {
		return Grant{}, ErrCeremonyIncomplete
	}
	sort.Strings(factorIDs)

	g.seq++
	grant := Grant{
		ID:         fmt.Sprintf("grant-%d", g.seq),
		CeremonyID: record.ID,
		UserID:     record.UserID,
		SessionID:  record.SessionID,
		Factors:    append([]string(nil), factorIDs...),
		State:      GrantValid,
		Seq:        g.seq,
	}
	record.State = CeremonyRedeemed
	delete(g.openByUser, record.UserID)
	stored := grant
	stored.Factors = append([]string(nil), factorIDs...)
	g.grants[stored.ID] = stored
	return grant, nil
}

// RevokeFactor tombstones a factor without reusing its identifier.
func (g *MfaCeremonyGuard) RevokeFactor(userID, factorID string) error {
	if blank(userID) || blank(factorID) {
		return ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	factor, ok := g.factorLocked(userID, factorID)
	if !ok {
		return ErrFactorNotFound
	}
	if factor.State == FactorRemoved {
		return ErrFactorState
	}
	factor.State = FactorRemoved

	if ceremonyID, ok := g.openByUser[userID]; ok {
		record := g.ceremonies[ceremonyID]
		if record != nil && record.State == CeremonyOpen {
			if _, verified := record.verified[factorID]; verified {
				delete(record.verified, factorID)
				record.Verified--
			}
		}
	}
	return nil
}

// Factors lists the ledger of one account in factor id order.
func (g *MfaCeremonyGuard) Factors(userID string) []Factor {
	g.mu.Lock()
	defer g.mu.Unlock()

	userFactors := g.factors[userID]
	factors := make([]Factor, 0, len(userFactors))
	for _, factor := range userFactors {
		factors = append(factors, *factor)
	}
	sort.Slice(factors, func(i, j int) bool { return factors[i].FactorID < factors[j].FactorID })
	return factors
}

// Factor reads one ledger entry.
func (g *MfaCeremonyGuard) Factor(userID, factorID string) (Factor, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	factor, ok := g.factorLocked(userID, factorID)
	if !ok {
		return Factor{}, false
	}
	return *factor, true
}

// Ceremony reads one ceremony record.
func (g *MfaCeremonyGuard) Ceremony(ceremonyID string) (Ceremony, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.ceremonies[ceremonyID]
	if !ok {
		return Ceremony{}, false
	}
	return record.Ceremony, true
}

// LockRemaining reports how long the account stays locked.
func (g *MfaCeremonyGuard) LockRemaining(userID string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.lockRemainingLocked(userID, g.now())
}

type snapshotCeremony struct {
	Ceremony
	VerifiedFactors []string `json:"verified_factors"`
}

type snapshotState struct {
	Seq             int64              `json:"seq"`
	Factors         []Factor           `json:"factors"`
	Ceremonies      []snapshotCeremony `json:"ceremonies"`
	Grants          []Grant            `json:"grants"`
	Locks           map[string]int64   `json:"locks"`
	AccountFailures map[string]int     `json:"account_failures"`
}

type snapshotEnvelope struct {
	Version  int    `json:"version"`
	Payload  []byte `json:"payload"`
	Checksum string `json:"checksum"`
}

const snapshotVersion = 1

// Snapshot serialises the ledger, the ceremonies, the grants and the locks.
func (g *MfaCeremonyGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	state := g.snapshotStateLocked()
	g.mu.Unlock()

	payload, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		Payload:  payload,
		Checksum: checksum(payload),
	}
	return json.Marshal(envelope)
}

// Restore replaces the whole state with a snapshot, all or nothing.
func (g *MfaCeremonyGuard) Restore(data []byte) error {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ErrBadSnapshot
	}
	if envelope.Version != snapshotVersion || len(envelope.Payload) == 0 ||
		envelope.Checksum != checksum(envelope.Payload) {
		return ErrBadSnapshot
	}

	var state snapshotState
	decoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return ErrBadSnapshot
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrBadSnapshot
	}

	factors, ceremonies, openByUser, grants, locks, failures, seq, err :=
		buildRestoredState(state)
	if err != nil {
		return err
	}

	g.mu.Lock()
	g.factors = factors
	g.ceremonies = ceremonies
	g.openByUser = openByUser
	g.grants = grants
	g.locks = locks
	g.accountFailures = failures
	g.seq = seq
	g.mu.Unlock()
	return nil
}

func (g *MfaCeremonyGuard) now() time.Time {
	return g.cfg.Now()
}

func (g *MfaCeremonyGuard) factorLocked(userID, factorID string) (*Factor, bool) {
	userFactors := g.factors[userID]
	if userFactors == nil {
		return nil, false
	}
	factor, ok := userFactors[factorID]
	return factor, ok
}

func (g *MfaCeremonyGuard) hasActiveFactorLocked(userID string) bool {
	for _, factor := range g.factors[userID] {
		if factor.State == FactorActive {
			return true
		}
	}
	return false
}

func (g *MfaCeremonyGuard) lockActiveLocked(userID string, now time.Time) bool {
	until, ok := g.locks[userID]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(g.locks, userID)
	delete(g.accountFailures, userID)
	return false
}

func (g *MfaCeremonyGuard) lockRemainingLocked(userID string, now time.Time) time.Duration {
	until, ok := g.locks[userID]
	if !ok {
		return 0
	}
	remaining := until.Sub(now)
	if remaining <= 0 {
		delete(g.locks, userID)
		delete(g.accountFailures, userID)
		return 0
	}
	return remaining
}

func (g *MfaCeremonyGuard) lockAccountLocked(userID string, until time.Time) {
	g.locks[userID] = until
	g.invalidateOpenCeremoniesLocked(userID)
}

func (g *MfaCeremonyGuard) invalidateOpenCeremoniesLocked(userID string) {
	for _, record := range g.ceremonies {
		if record.UserID == userID && record.State == CeremonyOpen {
			record.State = ceremonyInvalid
		}
	}
	delete(g.openByUser, userID)
}

func (g *MfaCeremonyGuard) verdictLocked(record *ceremonyRecord, factorID string, accepted bool, reason string) Verdict {
	return Verdict{
		CeremonyID: record.ID,
		FactorID:   factorID,
		Accepted:   accepted,
		Reason:     reason,
		Verified:   record.Verified,
	}
}

func (g *MfaCeremonyGuard) snapshotStateLocked() snapshotState {
	state := snapshotState{
		Seq:             g.seq,
		Locks:           make(map[string]int64, len(g.locks)),
		AccountFailures: make(map[string]int, len(g.accountFailures)),
	}

	for _, userFactors := range g.factors {
		for _, factor := range userFactors {
			state.Factors = append(state.Factors, *factor)
		}
	}
	sort.Slice(state.Factors, func(i, j int) bool {
		if state.Factors[i].UserID != state.Factors[j].UserID {
			return state.Factors[i].UserID < state.Factors[j].UserID
		}
		return state.Factors[i].FactorID < state.Factors[j].FactorID
	})

	for _, record := range g.ceremonies {
		verified := make([]string, 0, len(record.verified))
		for factorID := range record.verified {
			verified = append(verified, factorID)
		}
		sort.Strings(verified)
		state.Ceremonies = append(state.Ceremonies, snapshotCeremony{
			Ceremony:        record.Ceremony,
			VerifiedFactors: verified,
		})
	}
	sort.Slice(state.Ceremonies, func(i, j int) bool {
		return state.Ceremonies[i].ID < state.Ceremonies[j].ID
	})

	for _, grant := range g.grants {
		grant.Factors = append([]string(nil), grant.Factors...)
		state.Grants = append(state.Grants, grant)
	}
	sort.Slice(state.Grants, func(i, j int) bool { return state.Grants[i].ID < state.Grants[j].ID })

	for userID, until := range g.locks {
		state.Locks[userID] = until.UnixNano()
	}
	for userID, count := range g.accountFailures {
		state.AccountFailures[userID] = count
	}
	return state
}

func buildRestoredState(state snapshotState) (
	map[string]map[string]*Factor,
	map[string]*ceremonyRecord,
	map[string]string,
	map[string]Grant,
	map[string]time.Time,
	map[string]int,
	int64,
	error,
) {
	if state.Seq < 0 {
		return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
	}

	factors := make(map[string]map[string]*Factor)
	for _, item := range state.Factors {
		if blank(item.UserID) || blank(item.FactorID) || !validKind(item.Kind) ||
			!validFactorState(item.State) || item.Failures < 0 {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		userFactors := factors[item.UserID]
		if userFactors == nil {
			userFactors = make(map[string]*Factor)
			factors[item.UserID] = userFactors
		}
		if _, exists := userFactors[item.FactorID]; exists {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		factor := item
		userFactors[item.FactorID] = &factor
	}

	ceremonies := make(map[string]*ceremonyRecord)
	openByUser := make(map[string]string)
	seq := state.Seq
	for _, item := range state.Ceremonies {
		ceremony := item.Ceremony
		if blank(ceremony.ID) || blank(ceremony.UserID) || blank(ceremony.SessionID) ||
			blank(ceremony.Challenge) || !validCeremonyState(ceremony.State) ||
			ceremony.Verified < 0 || ceremony.Seq < 0 {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		if _, exists := ceremonies[ceremony.ID]; exists {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		verified := make(map[string]struct{}, len(item.VerifiedFactors))
		for _, factorID := range item.VerifiedFactors {
			if blank(factorID) {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
			if _, exists := verified[factorID]; exists {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
			verified[factorID] = struct{}{}
		}
		if len(verified) != ceremony.Verified {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		if ceremony.State == CeremonyOpen {
			if _, exists := openByUser[ceremony.UserID]; exists {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
			openByUser[ceremony.UserID] = ceremony.ID
		}
		for factorID := range verified {
			factor := factors[ceremony.UserID][factorID]
			if factor == nil {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
			if ceremony.State == CeremonyOpen && factor.State != FactorActive {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
		}
		ceremonies[ceremony.ID] = &ceremonyRecord{Ceremony: ceremony, verified: verified}
		if ceremony.Seq > seq {
			seq = ceremony.Seq
		}
	}

	grants := make(map[string]Grant)
	for _, item := range state.Grants {
		if blank(item.ID) || blank(item.CeremonyID) || blank(item.UserID) ||
			blank(item.SessionID) || item.State != GrantValid || item.Seq < 0 {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		if _, exists := grants[item.ID]; exists {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		ceremony := ceremonies[item.CeremonyID]
		if ceremony == nil {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		seen := make(map[string]struct{}, len(item.Factors))
		for _, factorID := range item.Factors {
			if blank(factorID) {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
			if _, exists := seen[factorID]; exists {
				return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
			}
			seen[factorID] = struct{}{}
		}
		grant := item
		grant.Factors = append([]string(nil), item.Factors...)
		grants[item.ID] = grant
		if item.Seq > seq {
			seq = item.Seq
		}
	}

	locks := make(map[string]time.Time, len(state.Locks))
	for userID, until := range state.Locks {
		if blank(userID) {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		locks[userID] = time.Unix(0, until)
	}
	failures := make(map[string]int, len(state.AccountFailures))
	for userID, count := range state.AccountFailures {
		if blank(userID) || count < 0 {
			return nil, nil, nil, nil, nil, nil, 0, ErrBadSnapshot
		}
		failures[userID] = count
	}

	return factors, ceremonies, openByUser, grants, locks, failures, seq, nil
}

func checksum(payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum)
}

func blank(value string) bool {
	return strings.TrimSpace(value) == ""
}

func validKind(kind string) bool {
	switch kind {
	case KindKnowledge, KindPossession, KindInherence:
		return true
	default:
		return false
	}
}

func validFactorState(state string) bool {
	switch state {
	case FactorPending, FactorActive, FactorCompromised, FactorRemoved:
		return true
	default:
		return false
	}
}

func validCeremonyState(state string) bool {
	switch state {
	case CeremonyOpen, CeremonyRedeemed, ceremonyInvalid:
		return true
	default:
		return false
	}
}

func countsTowardsLock(kind string) bool {
	return kind != KindPossession
}

func countLiveFactors(userFactors map[string]*Factor) int {
	count := 0
	for _, factor := range userFactors {
		if factor.State != FactorRemoved {
			count++
		}
	}
	return count
}
