// Package task evaluates static separation of duties over the permissions a
// subject would hold, including the permissions inherited from its roles.
package task

import (
	"errors"
	"time"
)

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

// SodConflictGuard owns the conflict policy, the grants and the exemptions.
type SodConflictGuard struct {
	cfg SodConfig
}

// NewSodConflictGuard builds an empty guard with the given caps.
func NewSodConflictGuard(cfg SodConfig) *SodConflictGuard {
	return &SodConflictGuard{cfg: cfg}
}

// AddConflict registers one mutually exclusive pair for a tenant.
func (g *SodConflictGuard) AddConflict(tenantID, permA, permB, mode string) error {
	return errNotImplemented
}

// Conflicts lists the pairs of one tenant in registration order.
func (g *SodConflictGuard) Conflicts(tenantID string) []ConflictPair {
	return nil
}

// Check evaluates a proposed addition without applying it.
func (g *SodConflictGuard) Check(tenantID, subjectID string, add PermissionSet) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// GrantWithCheck applies one permission if it would not break separation of duties.
func (g *SodConflictGuard) GrantWithCheck(tenantID, subjectID, perm string) (Grant, error) {
	return Grant{}, errNotImplemented
}

// Revoke removes one direct grant from a subject.
func (g *SodConflictGuard) Revoke(tenantID, subjectID, perm string) error {
	return errNotImplemented
}

// Permissions lists the effective permissions of a subject, sorted.
func (g *SodConflictGuard) Permissions(tenantID, subjectID string) []string {
	return nil
}

// DefineRole creates one named bundle of permissions for a tenant.
func (g *SodConflictGuard) DefineRole(tenantID, roleID string, perms []string) error {
	return errNotImplemented
}

// AssignRole attaches one role to a subject, idempotently.
func (g *SodConflictGuard) AssignRole(tenantID, subjectID, roleID string) error {
	return errNotImplemented
}

// Roles lists the roles attached to a subject, sorted.
func (g *SodConflictGuard) Roles(tenantID, subjectID string) []string {
	return nil
}

// Exempt records a time boxed waiver for one subject and one pair.
func (g *SodConflictGuard) Exempt(tenantID, subjectID, pairID string, until time.Time, reason string) (Exemption, error) {
	return Exemption{}, errNotImplemented
}

// Exemptions lists the live exemptions of one subject, sorted by id.
func (g *SodConflictGuard) Exemptions(tenantID, subjectID string) []Exemption {
	return nil
}

// Expire drops every exemption whose window has closed and reports how many went.
func (g *SodConflictGuard) Expire(now time.Time) int {
	return 0
}

// Audit lists the retained block and alert decisions of one tenant.
func (g *SodConflictGuard) Audit(tenantID string) []AuditRecord {
	return nil
}

// Reviews lists the follow up items raised for blocked grants after an expiry.
func (g *SodConflictGuard) Reviews(tenantID string) []ReviewItem {
	return nil
}

// Snapshot serialises the policy, the grants, the exemptions and the records.
func (g *SodConflictGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the whole guard state with a snapshot, all or nothing.
func (g *SodConflictGuard) Restore(data []byte) error {
	return errNotImplemented
}
