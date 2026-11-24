// Package task evaluates static separation of duties over the permissions a
// subject would hold, including the permissions inherited from its roles.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Conflict modes.
const (
	ModeBlock = "block"
	ModeAlert = "alert"
)

// Verdict actions.
const (
	ActionAllow = "allow"
	ActionBlock = "block"
	ActionAlert = "alert"
)

// Verdict reasons.
const (
	ReasonClear    = "clear"
	ReasonSodBlock = "sod_block"
	ReasonSodAlert = "sod_alert"
	ReasonExempted = "exempted"
)

// Errors returned by the guard.
var (
	ErrBlankArg         = errors.New("tenant, subject, permission and reason must not be blank")
	ErrSamePairPerms    = errors.New("the two sides of a conflict pair must differ")
	ErrBadMode          = errors.New("conflict mode must be block or alert")
	ErrPairLimit        = errors.New("conflict pair table is full for the tenant")
	ErrGrantBlocked     = errors.New("grant blocked by a separation of duties conflict")
	ErrInvalidExemption = errors.New("exemption expiry must be in the future")
	ErrPairNotFound     = errors.New("conflict pair not found in the tenant")
	ErrExemptionLimit   = errors.New("exemption table is full for the subject")
	ErrRoleExists       = errors.New("role already defined in the tenant")
	ErrRoleLimit        = errors.New("role table is full for the tenant")
	ErrUnknownRole      = errors.New("role not defined in the tenant")
	ErrInheritedPerm    = errors.New("permission is inherited from a role and cannot be revoked directly")
	ErrNotHeld          = errors.New("permission is not held by the subject")
	ErrBadSnapshot      = errors.New("snapshot payload is invalid")
)

// PermissionSet is a batch of permissions proposed for addition.
type PermissionSet []string

// SodConfig bounds the policy tables and supplies the clock.
type SodConfig struct {
	MaxPairsPerTenant       int
	MaxExemptionsPerSubject int
	MaxAuditPerTenant       int
	MaxRolesPerTenant       int
	Now                     func() time.Time
}

// ConflictPair is one mutually exclusive pair of permissions.
type ConflictPair struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	PermA    string `json:"perm_a"`
	PermB    string `json:"perm_b"`
	Mode     string `json:"mode"`
}

// Verdict is the outcome of evaluating a proposed addition.
type Verdict struct {
	Allowed   bool     `json:"allowed"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason"`
	Conflicts []string `json:"conflicts"`
}

// Grant reports the state of one permission after a checked grant.
type Grant struct {
	Applied bool   `json:"applied"`
	Perm    string `json:"perm"`
}

// Exemption is a time boxed waiver for one subject and one pair.
type Exemption struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	SubjectID string `json:"subject_id"`
	PairID    string `json:"pair_id"`
	Until     int64  `json:"until"`
	Reason    string `json:"reason"`
}

// AuditRecord is one block or alert decision.
type AuditRecord struct {
	Seq       uint64   `json:"seq"`
	TenantID  string   `json:"tenant_id"`
	SubjectID string   `json:"subject_id"`
	Perm      string   `json:"perm"`
	PairIDs   []string `json:"pair_ids"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason"`
	At        int64    `json:"at"`
}

// ReviewItem is a follow up left behind when an expired exemption no longer
// shields a subject and a fresh grant is blocked.
type ReviewItem struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	SubjectID string `json:"subject_id"`
	PairID    string `json:"pair_id"`
	Perm      string `json:"perm"`
	Note      string `json:"note"`
	At        int64  `json:"at"`
}

type tenantState struct {
	pairs        []ConflictPair
	pairByKey    map[string]string
	roles        map[string]map[string]struct{}
	grants       map[string]map[string]struct{}
	subjectRoles map[string]map[string]struct{}
	exemptions   []Exemption
	expired      map[string]struct{}
	audit        []AuditRecord
	auditSeq     uint64
	reviews      []ReviewItem
}

type guardState struct {
	tenants         map[string]*tenantState
	nextPairID      uint64
	nextExemptionID uint64
	nextReviewID    uint64
}

// SodConflictGuard owns the conflict policy, the grants and the exemptions.
type SodConflictGuard struct {
	mu  sync.RWMutex
	cfg SodConfig
	st  guardState
}

func newTenantState() *tenantState {
	return &tenantState{
		pairByKey:    make(map[string]string),
		roles:        make(map[string]map[string]struct{}),
		grants:       make(map[string]map[string]struct{}),
		subjectRoles: make(map[string]map[string]struct{}),
		expired:      make(map[string]struct{}),
	}
}

// NewSodConflictGuard builds an empty guard with the given caps.
func NewSodConflictGuard(cfg SodConfig) *SodConflictGuard {
	return &SodConflictGuard{
		cfg: cfg,
		st: guardState{
			tenants: make(map[string]*tenantState),
		},
	}
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

func expiredKey(subjectID, pairID string) string {
	return subjectID + "\x00" + pairID
}

func (g *SodConflictGuard) nowTime() time.Time {
	if g.cfg.Now != nil {
		return g.cfg.Now()
	}
	return time.Now()
}

func (g *SodConflictGuard) tenantLocked(tenantID string) *tenantState {
	t := g.st.tenants[tenantID]
	if t == nil {
		t = newTenantState()
		g.st.tenants[tenantID] = t
	}
	return t
}

func (g *SodConflictGuard) limitReached(limit, n int) bool {
	return limit >= 0 && n >= limit
}

func (t *tenantState) effective(subjectID string) map[string]struct{} {
	out := make(map[string]struct{})
	for perm := range t.grants[subjectID] {
		out[perm] = struct{}{}
	}
	for roleID := range t.subjectRoles[subjectID] {
		for perm := range t.roles[roleID] {
			out[perm] = struct{}{}
		}
	}
	return out
}

func (t *tenantState) inherited(subjectID, perm string) bool {
	for roleID := range t.subjectRoles[subjectID] {
		if _, ok := t.roles[roleID][perm]; ok {
			return true
		}
	}
	return false
}

func (t *tenantState) findPair(pairID string) (ConflictPair, bool) {
	for _, pair := range t.pairs {
		if pair.ID == pairID {
			return pair, true
		}
	}
	return ConflictPair{}, false
}

func (t *tenantState) liveExemption(subjectID, pairID string, now time.Time) bool {
	nowNano := now.UnixNano()
	for _, ex := range t.exemptions {
		if ex.SubjectID == subjectID && ex.PairID == pairID && ex.Until > nowNano {
			return true
		}
	}
	return false
}

func (t *tenantState) hadExpiredExemption(subjectID, pairID string, now time.Time) bool {
	if _, ok := t.expired[expiredKey(subjectID, pairID)]; ok {
		return true
	}
	nowNano := now.UnixNano()
	for _, ex := range t.exemptions {
		if ex.SubjectID == subjectID && ex.PairID == pairID && ex.Until <= nowNano {
			return true
		}
	}
	return false
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// AddConflict registers one mutually exclusive pair for a tenant.
func (g *SodConflictGuard) AddConflict(tenantID, permA, permB, mode string) error {
	tenantID = strings.TrimSpace(tenantID)
	permA = strings.TrimSpace(permA)
	permB = strings.TrimSpace(permB)
	mode = strings.TrimSpace(mode)
	if blank(tenantID) || blank(permA) || blank(permB) {
		return ErrBlankArg
	}
	if permA == permB {
		return ErrSamePairPerms
	}
	if mode != ModeBlock && mode != ModeAlert {
		return ErrBadMode
	}

	key := pairKey(permA, permB)
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.tenantLocked(tenantID)
	if _, ok := t.pairByKey[key]; ok {
		return nil
	}
	if g.limitReached(g.cfg.MaxPairsPerTenant, len(t.pairs)) {
		return ErrPairLimit
	}
	g.st.nextPairID++
	pair := ConflictPair{
		ID:       fmt.Sprintf("pair-%06d", g.st.nextPairID),
		TenantID: tenantID,
		PermA:    permA,
		PermB:    permB,
		Mode:     mode,
	}
	t.pairs = append(t.pairs, pair)
	t.pairByKey[key] = pair.ID
	return nil
}

// Conflicts lists the pairs of one tenant in registration order.
func (g *SodConflictGuard) Conflicts(tenantID string) []ConflictPair {
	tenantID = strings.TrimSpace(tenantID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil || len(t.pairs) == 0 {
		return nil
	}
	out := make([]ConflictPair, len(t.pairs))
	copy(out, t.pairs)
	return out
}

func (g *SodConflictGuard) evaluateLocked(t *tenantState, subjectID string, add map[string]struct{}, now time.Time) Verdict {
	effective := t.effective(subjectID)
	for perm := range add {
		effective[perm] = struct{}{}
	}

	conflicts := make([]string, 0)
	blocking := false
	exempted := false
	for _, pair := range t.pairs {
		_, hasA := effective[pair.PermA]
		_, hasB := effective[pair.PermB]
		if !hasA || !hasB {
			continue
		}
		if t.liveExemption(subjectID, pair.ID, now) {
			exempted = true
			continue
		}
		conflicts = append(conflicts, pair.ID)
		if pair.Mode == ModeBlock {
			blocking = true
		}
	}

	if len(conflicts) == 0 {
		if exempted {
			return Verdict{Allowed: true, Action: ActionAllow, Reason: ReasonExempted}
		}
		return Verdict{Allowed: true, Action: ActionAllow, Reason: ReasonClear}
	}
	if blocking {
		return Verdict{
			Allowed:   false,
			Action:    ActionBlock,
			Reason:    ReasonSodBlock,
			Conflicts: conflicts,
		}
	}
	return Verdict{
		Allowed:   true,
		Action:    ActionAlert,
		Reason:    ReasonSodAlert,
		Conflicts: conflicts,
	}
}

// Check evaluates a proposed addition without applying it.
func (g *SodConflictGuard) Check(tenantID, subjectID string, add PermissionSet) (Verdict, error) {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	if blank(tenantID) || blank(subjectID) {
		return Verdict{}, ErrBlankArg
	}
	addSet := make(map[string]struct{}, len(add))
	for _, perm := range add {
		perm = strings.TrimSpace(perm)
		if blank(perm) {
			return Verdict{}, ErrBlankArg
		}
		addSet[perm] = struct{}{}
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil {
		return Verdict{Allowed: true, Action: ActionAllow, Reason: ReasonClear}, nil
	}
	return g.evaluateLocked(t, subjectID, addSet, g.nowTime()), nil
}

func (g *SodConflictGuard) appendAuditLocked(t *tenantState, tenantID, subjectID, perm string, pairIDs []string, action, reason string, now time.Time) {
	t.auditSeq++
	record := AuditRecord{
		Seq:       t.auditSeq,
		TenantID:  tenantID,
		SubjectID: subjectID,
		Perm:      perm,
		PairIDs:   copyStrings(pairIDs),
		Action:    action,
		Reason:    reason,
		At:        now.UnixNano(),
	}
	limit := g.cfg.MaxAuditPerTenant
	if limit < 0 {
		t.audit = append(t.audit, record)
		return
	}
	for limit == 0 || len(t.audit) >= limit {
		if len(t.audit) == 0 {
			return
		}
		t.audit = t.audit[1:]
	}
	t.audit = append(t.audit, record)
}

func (g *SodConflictGuard) maybeReviewLocked(t *tenantState, tenantID, subjectID, perm string, pairIDs []string, now time.Time) {
	for _, pairID := range pairIDs {
		pair, ok := t.findPair(pairID)
		if !ok || pair.Mode != ModeBlock {
			continue
		}
		if !t.hadExpiredExemption(subjectID, pairID, now) {
			continue
		}
		t.expired[expiredKey(subjectID, pairID)] = struct{}{}

		duplicate := false
		for _, item := range t.reviews {
			if item.SubjectID == subjectID && item.PairID == pairID && item.Perm == perm {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		g.st.nextReviewID++
		t.reviews = append(t.reviews, ReviewItem{
			ID:        fmt.Sprintf("review-%06d", g.st.nextReviewID),
			TenantID:  tenantID,
			SubjectID: subjectID,
			PairID:    pairID,
			Perm:      perm,
			Note:      "exemption expired before a blocked grant",
			At:        now.UnixNano(),
		})
	}
}

// GrantWithCheck applies one permission if it would not break separation of duties.
func (g *SodConflictGuard) GrantWithCheck(tenantID, subjectID, perm string) (Grant, error) {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	perm = strings.TrimSpace(perm)
	if blank(tenantID) || blank(subjectID) || blank(perm) {
		return Grant{Perm: perm}, ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.nowTime()
	t := g.st.tenants[tenantID]
	held := false
	if t != nil {
		_, held = t.grants[subjectID][perm]
	}
	verdict := Verdict{Allowed: true, Action: ActionAllow, Reason: ReasonClear}
	if t != nil {
		verdict = g.evaluateLocked(t, subjectID, map[string]struct{}{perm: {}}, now)
	}

	if verdict.Action == ActionBlock {
		if t != nil {
			g.appendAuditLocked(t, tenantID, subjectID, perm, verdict.Conflicts, ActionBlock, ReasonSodBlock, now)
			g.maybeReviewLocked(t, tenantID, subjectID, perm, verdict.Conflicts, now)
		}
		return Grant{Applied: false, Perm: perm}, ErrGrantBlocked
	}
	if held {
		return Grant{Applied: false, Perm: perm}, nil
	}

	t = g.tenantLocked(tenantID)
	if t.grants[subjectID] == nil {
		t.grants[subjectID] = make(map[string]struct{})
	}
	t.grants[subjectID][perm] = struct{}{}
	if verdict.Action == ActionAlert {
		g.appendAuditLocked(t, tenantID, subjectID, perm, verdict.Conflicts, ActionAlert, ReasonSodAlert, now)
	}
	return Grant{Applied: true, Perm: perm}, nil
}

// Revoke removes one direct grant from a subject.
func (g *SodConflictGuard) Revoke(tenantID, subjectID, perm string) error {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	perm = strings.TrimSpace(perm)
	if blank(tenantID) || blank(subjectID) || blank(perm) {
		return ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.st.tenants[tenantID]
	if t == nil {
		return ErrNotHeld
	}
	if direct := t.grants[subjectID]; direct != nil {
		if _, ok := direct[perm]; ok {
			delete(direct, perm)
			if len(direct) == 0 {
				delete(t.grants, subjectID)
			}
			return nil
		}
	}
	if t.inherited(subjectID, perm) {
		return ErrInheritedPerm
	}
	return ErrNotHeld
}

// Permissions lists the effective permissions of a subject, sorted.
func (g *SodConflictGuard) Permissions(tenantID, subjectID string) []string {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil {
		return nil
	}
	effective := t.effective(subjectID)
	out := make([]string, 0, len(effective))
	for perm := range effective {
		out = append(out, perm)
	}
	sort.Strings(out)
	return out
}

// DefineRole creates one named bundle of permissions for a tenant.
func (g *SodConflictGuard) DefineRole(tenantID, roleID string, perms []string) error {
	tenantID = strings.TrimSpace(tenantID)
	roleID = strings.TrimSpace(roleID)
	if blank(tenantID) || blank(roleID) {
		return ErrBlankArg
	}
	set := make(map[string]struct{}, len(perms))
	for _, perm := range perms {
		perm = strings.TrimSpace(perm)
		if blank(perm) {
			return ErrBlankArg
		}
		set[perm] = struct{}{}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.tenantLocked(tenantID)
	if _, ok := t.roles[roleID]; ok {
		return ErrRoleExists
	}
	if g.limitReached(g.cfg.MaxRolesPerTenant, len(t.roles)) {
		return ErrRoleLimit
	}
	t.roles[roleID] = set
	return nil
}

// AssignRole attaches one role to a subject, idempotently.
func (g *SodConflictGuard) AssignRole(tenantID, subjectID, roleID string) error {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	roleID = strings.TrimSpace(roleID)
	if blank(tenantID) || blank(subjectID) || blank(roleID) {
		return ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.st.tenants[tenantID]
	if t == nil {
		return ErrUnknownRole
	}
	if _, ok := t.roles[roleID]; !ok {
		return ErrUnknownRole
	}
	if t.subjectRoles[subjectID] == nil {
		t.subjectRoles[subjectID] = make(map[string]struct{})
	}
	t.subjectRoles[subjectID][roleID] = struct{}{}
	return nil
}

// Roles lists the roles attached to a subject, sorted.
func (g *SodConflictGuard) Roles(tenantID, subjectID string) []string {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.subjectRoles[subjectID]))
	for roleID := range t.subjectRoles[subjectID] {
		out = append(out, roleID)
	}
	sort.Strings(out)
	return out
}

// Exempt records a time boxed waiver for one subject and one pair.
func (g *SodConflictGuard) Exempt(tenantID, subjectID, pairID string, until time.Time, reason string) (Exemption, error) {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	pairID = strings.TrimSpace(pairID)
	reason = strings.TrimSpace(reason)
	if blank(tenantID) || blank(subjectID) || blank(pairID) || blank(reason) {
		return Exemption{}, ErrBlankArg
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.nowTime()
	if until.IsZero() || !until.After(now) {
		return Exemption{}, ErrInvalidExemption
	}
	t := g.st.tenants[tenantID]
	if t == nil {
		return Exemption{}, ErrPairNotFound
	}
	if _, ok := t.findPair(pairID); !ok {
		return Exemption{}, ErrPairNotFound
	}
	count := 0
	for _, ex := range t.exemptions {
		if ex.SubjectID == subjectID {
			count++
		}
	}
	if g.limitReached(g.cfg.MaxExemptionsPerSubject, count) {
		return Exemption{}, ErrExemptionLimit
	}

	g.st.nextExemptionID++
	exemption := Exemption{
		ID:        fmt.Sprintf("exemption-%06d", g.st.nextExemptionID),
		TenantID:  tenantID,
		SubjectID: subjectID,
		PairID:    pairID,
		Until:     until.UnixNano(),
		Reason:    reason,
	}
	t.exemptions = append(t.exemptions, exemption)
	return exemption, nil
}

// Exemptions lists the live exemptions of one subject, sorted by id.
func (g *SodConflictGuard) Exemptions(tenantID, subjectID string) []Exemption {
	tenantID = strings.TrimSpace(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil {
		return nil
	}
	nowNano := g.nowTime().UnixNano()
	out := make([]Exemption, 0)
	for _, ex := range t.exemptions {
		if ex.SubjectID == subjectID && ex.Until > nowNano {
			out = append(out, ex)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Expire drops every exemption whose window has closed and reports how many went.
func (g *SodConflictGuard) Expire(now time.Time) int {
	nowNano := now.UnixNano()
	g.mu.Lock()
	defer g.mu.Unlock()

	removed := 0
	for _, t := range g.st.tenants {
		kept := t.exemptions[:0]
		for _, ex := range t.exemptions {
			if ex.Until <= nowNano {
				t.expired[expiredKey(ex.SubjectID, ex.PairID)] = struct{}{}
				removed++
				continue
			}
			kept = append(kept, ex)
		}
		t.exemptions = kept
	}
	return removed
}

// Audit lists the retained block and alert decisions of one tenant.
func (g *SodConflictGuard) Audit(tenantID string) []AuditRecord {
	tenantID = strings.TrimSpace(tenantID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil || len(t.audit) == 0 {
		return nil
	}
	out := make([]AuditRecord, len(t.audit))
	for i, record := range t.audit {
		record.PairIDs = copyStrings(record.PairIDs)
		out[i] = record
	}
	return out
}

// Reviews lists the follow up items raised for blocked grants after an expiry.
func (g *SodConflictGuard) Reviews(tenantID string) []ReviewItem {
	tenantID = strings.TrimSpace(tenantID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := g.st.tenants[tenantID]
	if t == nil || len(t.reviews) == 0 {
		return nil
	}
	out := make([]ReviewItem, len(t.reviews))
	copy(out, t.reviews)
	return out
}

const snapshotVersion = 1

type snapshotExpired struct {
	SubjectID string `json:"subject_id"`
	PairID    string `json:"pair_id"`
}

type snapshotTenant struct {
	Pairs        []ConflictPair      `json:"pairs"`
	Roles        map[string][]string `json:"roles"`
	Grants       map[string][]string `json:"grants"`
	SubjectRoles map[string][]string `json:"subject_roles"`
	Exemptions   []Exemption         `json:"exemptions"`
	Expired      []snapshotExpired   `json:"expired,omitempty"`
	Audit        []AuditRecord       `json:"audit"`
	AuditSeq     uint64              `json:"audit_seq"`
	Reviews      []ReviewItem        `json:"reviews"`
}

type snapshotPayload struct {
	Version         int                       `json:"version"`
	NextPairID      uint64                    `json:"next_pair_id"`
	NextExemptionID uint64                    `json:"next_exemption_id"`
	NextReviewID    uint64                    `json:"next_review_id"`
	Tenants         map[string]snapshotTenant `json:"tenants"`
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

// Snapshot serialises the policy, the grants, the exemptions and the records.
func (g *SodConflictGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	payload := snapshotPayload{
		Version:         snapshotVersion,
		NextPairID:      g.st.nextPairID,
		NextExemptionID: g.st.nextExemptionID,
		NextReviewID:    g.st.nextReviewID,
		Tenants:         make(map[string]snapshotTenant, len(g.st.tenants)),
	}
	for tenantID, t := range g.st.tenants {
		st := snapshotTenant{
			Pairs:        append([]ConflictPair(nil), t.pairs...),
			Roles:        make(map[string][]string, len(t.roles)),
			Grants:       make(map[string][]string, len(t.grants)),
			SubjectRoles: make(map[string][]string, len(t.subjectRoles)),
			Exemptions:   append([]Exemption(nil), t.exemptions...),
			AuditSeq:     t.auditSeq,
			Reviews:      append([]ReviewItem(nil), t.reviews...),
		}
		for roleID, perms := range t.roles {
			st.Roles[roleID] = sortedSet(perms)
		}
		for subjectID, perms := range t.grants {
			st.Grants[subjectID] = sortedSet(perms)
		}
		for subjectID, roles := range t.subjectRoles {
			st.SubjectRoles[subjectID] = sortedSet(roles)
		}
		for _, record := range t.audit {
			record.PairIDs = copyStrings(record.PairIDs)
			st.Audit = append(st.Audit, record)
		}
		for key := range t.expired {
			parts := strings.SplitN(key, "\x00", 2)
			if len(parts) != 2 {
				continue
			}
			st.Expired = append(st.Expired, snapshotExpired{SubjectID: parts[0], PairID: parts[1]})
		}
		sort.Slice(st.Expired, func(i, j int) bool {
			if st.Expired[i].SubjectID == st.Expired[j].SubjectID {
				return st.Expired[i].PairID < st.Expired[j].PairID
			}
			return st.Expired[i].SubjectID < st.Expired[j].SubjectID
		})
		payload.Tenants[tenantID] = st
	}
	return json.Marshal(payload)
}

func idCounter(ids []string, prefix string) uint64 {
	var max uint64
	for _, id := range ids {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(id, prefix), 10, 64)
		if err == nil && n > max {
			max = n
		}
	}
	return max
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// Restore replaces the whole guard state with a snapshot, all or nothing.
func (g *SodConflictGuard) Restore(data []byte) error {
	var payload snapshotPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ErrBadSnapshot
	}
	if payload.Version != snapshotVersion || payload.Tenants == nil {
		return ErrBadSnapshot
	}

	next := guardState{
		tenants:         make(map[string]*tenantState, len(payload.Tenants)),
		nextPairID:      payload.NextPairID,
		nextExemptionID: payload.NextExemptionID,
		nextReviewID:    payload.NextReviewID,
	}
	var pairIDs []string
	var exemptionIDs []string
	var reviewIDs []string

	for tenantID, st := range payload.Tenants {
		tenantID = strings.TrimSpace(tenantID)
		if blank(tenantID) {
			return ErrBadSnapshot
		}
		t := newTenantState()
		seenPairIDs := make(map[string]struct{}, len(st.Pairs))
		for _, pair := range st.Pairs {
			pair.ID = strings.TrimSpace(pair.ID)
			pair.TenantID = strings.TrimSpace(pair.TenantID)
			pair.PermA = strings.TrimSpace(pair.PermA)
			pair.PermB = strings.TrimSpace(pair.PermB)
			pair.Mode = strings.TrimSpace(pair.Mode)
			if blank(pair.ID) || pair.TenantID != tenantID || blank(pair.PermA) || blank(pair.PermB) || pair.PermA == pair.PermB {
				return ErrBadSnapshot
			}
			if pair.Mode != ModeBlock && pair.Mode != ModeAlert {
				return ErrBadSnapshot
			}
			if _, ok := seenPairIDs[pair.ID]; ok {
				return ErrBadSnapshot
			}
			key := pairKey(pair.PermA, pair.PermB)
			if _, ok := t.pairByKey[key]; ok {
				return ErrBadSnapshot
			}
			seenPairIDs[pair.ID] = struct{}{}
			t.pairs = append(t.pairs, pair)
			t.pairByKey[key] = pair.ID
			pairIDs = append(pairIDs, pair.ID)
		}
		if g.cfg.MaxPairsPerTenant >= 0 && len(t.pairs) > g.cfg.MaxPairsPerTenant {
			return ErrBadSnapshot
		}

		for roleID, perms := range st.Roles {
			roleID = strings.TrimSpace(roleID)
			if blank(roleID) {
				return ErrBadSnapshot
			}
			set := make(map[string]struct{}, len(perms))
			for _, perm := range perms {
				perm = strings.TrimSpace(perm)
				if blank(perm) {
					return ErrBadSnapshot
				}
				set[perm] = struct{}{}
			}
			t.roles[roleID] = set
		}
		if g.cfg.MaxRolesPerTenant >= 0 && len(t.roles) > g.cfg.MaxRolesPerTenant {
			return ErrBadSnapshot
		}

		for subjectID, roles := range st.SubjectRoles {
			subjectID = strings.TrimSpace(subjectID)
			if blank(subjectID) {
				return ErrBadSnapshot
			}
			set := make(map[string]struct{}, len(roles))
			for _, roleID := range roles {
				roleID = strings.TrimSpace(roleID)
				if _, ok := t.roles[roleID]; !ok {
					return ErrBadSnapshot
				}
				set[roleID] = struct{}{}
			}
			t.subjectRoles[subjectID] = set
		}

		for subjectID, perms := range st.Grants {
			subjectID = strings.TrimSpace(subjectID)
			if blank(subjectID) {
				return ErrBadSnapshot
			}
			set := make(map[string]struct{}, len(perms))
			for _, perm := range perms {
				perm = strings.TrimSpace(perm)
				if blank(perm) {
					return ErrBadSnapshot
				}
				set[perm] = struct{}{}
			}
			t.grants[subjectID] = set
		}

		seenExemptionIDs := make(map[string]struct{}, len(st.Exemptions))
		exemptionCount := make(map[string]int)
		for _, ex := range st.Exemptions {
			ex.ID = strings.TrimSpace(ex.ID)
			ex.TenantID = strings.TrimSpace(ex.TenantID)
			ex.SubjectID = strings.TrimSpace(ex.SubjectID)
			ex.PairID = strings.TrimSpace(ex.PairID)
			ex.Reason = strings.TrimSpace(ex.Reason)
			if blank(ex.ID) || ex.TenantID != tenantID || blank(ex.SubjectID) || blank(ex.PairID) || blank(ex.Reason) || ex.Until == 0 {
				return ErrBadSnapshot
			}
			if _, ok := seenPairIDs[ex.PairID]; !ok {
				return ErrBadSnapshot
			}
			if _, ok := seenExemptionIDs[ex.ID]; ok {
				return ErrBadSnapshot
			}
			seenExemptionIDs[ex.ID] = struct{}{}
			exemptionCount[ex.SubjectID]++
			t.exemptions = append(t.exemptions, ex)
			exemptionIDs = append(exemptionIDs, ex.ID)
		}
		for _, count := range exemptionCount {
			if g.cfg.MaxExemptionsPerSubject >= 0 && count > g.cfg.MaxExemptionsPerSubject {
				return ErrBadSnapshot
			}
		}

		for _, ref := range st.Expired {
			ref.SubjectID = strings.TrimSpace(ref.SubjectID)
			ref.PairID = strings.TrimSpace(ref.PairID)
			if blank(ref.SubjectID) || blank(ref.PairID) {
				return ErrBadSnapshot
			}
			if _, ok := seenPairIDs[ref.PairID]; !ok {
				return ErrBadSnapshot
			}
			t.expired[expiredKey(ref.SubjectID, ref.PairID)] = struct{}{}
		}

		var previousSeq uint64
		for _, record := range st.Audit {
			record.TenantID = strings.TrimSpace(record.TenantID)
			record.SubjectID = strings.TrimSpace(record.SubjectID)
			record.Perm = strings.TrimSpace(record.Perm)
			record.Action = strings.TrimSpace(record.Action)
			record.Reason = strings.TrimSpace(record.Reason)
			if record.Seq == 0 || record.Seq <= previousSeq || record.TenantID != tenantID || blank(record.SubjectID) || blank(record.Perm) {
				return ErrBadSnapshot
			}
			if record.Action != ActionBlock && record.Action != ActionAlert {
				return ErrBadSnapshot
			}
			for _, pairID := range record.PairIDs {
				if _, ok := seenPairIDs[pairID]; !ok {
					return ErrBadSnapshot
				}
			}
			previousSeq = record.Seq
			record.PairIDs = copyStrings(record.PairIDs)
			t.audit = append(t.audit, record)
		}
		if g.cfg.MaxAuditPerTenant >= 0 && len(t.audit) > g.cfg.MaxAuditPerTenant {
			return ErrBadSnapshot
		}
		if st.AuditSeq > t.auditSeq {
			t.auditSeq = st.AuditSeq
		}
		if previousSeq > t.auditSeq {
			t.auditSeq = previousSeq
		}

		seenReviewIDs := make(map[string]struct{}, len(st.Reviews))
		for _, item := range st.Reviews {
			item.ID = strings.TrimSpace(item.ID)
			item.TenantID = strings.TrimSpace(item.TenantID)
			item.SubjectID = strings.TrimSpace(item.SubjectID)
			item.PairID = strings.TrimSpace(item.PairID)
			item.Perm = strings.TrimSpace(item.Perm)
			item.Note = strings.TrimSpace(item.Note)
			if blank(item.ID) || item.TenantID != tenantID || blank(item.SubjectID) || blank(item.PairID) || blank(item.Perm) {
				return ErrBadSnapshot
			}
			if _, ok := seenPairIDs[item.PairID]; !ok {
				return ErrBadSnapshot
			}
			if _, ok := seenReviewIDs[item.ID]; ok {
				return ErrBadSnapshot
			}
			seenReviewIDs[item.ID] = struct{}{}
			t.reviews = append(t.reviews, item)
			reviewIDs = append(reviewIDs, item.ID)
		}

		next.tenants[tenantID] = t
	}

	next.nextPairID = maxUint64(next.nextPairID, idCounter(pairIDs, "pair-"))
	next.nextExemptionID = maxUint64(next.nextExemptionID, idCounter(exemptionIDs, "exemption-"))
	next.nextReviewID = maxUint64(next.nextReviewID, idCounter(reviewIDs, "review-"))

	g.mu.Lock()
	g.st = next
	g.mu.Unlock()
	return nil
}
