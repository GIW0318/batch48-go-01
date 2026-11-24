package task

import "errors"

// errNotImplemented marks behaviour that still has to be filled in.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by MirrorAuditGuard.
var (
	ErrPolicyConfig   = errors.New("mirror: invalid policy configuration")
	ErrPolicyExists   = errors.New("mirror: policy already exists")
	ErrPolicyUnknown  = errors.New("mirror: policy not found")
	ErrSessionExists  = errors.New("mirror: session already exists")
	ErrSessionUnknown = errors.New("mirror: session not found")
	ErrTargetUnknown  = errors.New("mirror: target not registered")
	ErrTargetConfig   = errors.New("mirror: invalid target configuration")
	ErrFlowInvalid    = errors.New("mirror: invalid flow key")
	ErrMirrorSnapshot = errors.New("mirror: corrupt snapshot")
)

// FlowKey is the five tuple plus tenant that selects a mirror policy.
type FlowKey struct {
	SrcIP   string
	DstIP   string
	SrcPort int
	DstPort int
	Proto   string
	Tenant  string
}

// MirrorPolicy describes deterministic sampling and header projection.
type MirrorPolicy struct {
	ID           string
	Tenant       string
	Priority     int
	SampleRate   int
	Granularity  int
	Fields       []string
	Family       string
	HighPriority bool
}

// MirrorSession points one mirror policy at one target.
type MirrorSession struct {
	ID       string
	PolicyID string
	Target   string
	Tenant   string
	Packets  int
	Mirrored int
}

// MirrorTarget is a collector with a packet rate quota.
type MirrorTarget struct {
	Name    string
	MaxPPS  int
	Used    int
	Healthy bool
}

// MirrorStats summarises guard activity.
type MirrorStats struct {
	Policies    int
	Sessions    int
	Matched     int
	Sampled     int
	Projected   int
	Delivered   int
	Dropped     int
	TargetsDown int
}

// MirrorAuditGuard decides which traffic enters mirror sessions.
type MirrorAuditGuard struct{}

// NewMirrorAuditGuard builds an empty guard.
func NewMirrorAuditGuard() *MirrorAuditGuard {
	return &MirrorAuditGuard{}
}

// AddPolicy registers a mirror policy.
func (g *MirrorAuditGuard) AddPolicy(p MirrorPolicy) error {
	return errNotImplemented
}

// DeletePolicy removes a mirror policy by id.
func (g *MirrorAuditGuard) DeletePolicy(id string) error {
	return errNotImplemented
}

// AddSession binds a policy to a mirror target.
func (g *MirrorAuditGuard) AddSession(s MirrorSession) error {
	return errNotImplemented
}

// Match selects the policy for a flow.
func (g *MirrorAuditGuard) Match(f FlowKey) (MirrorPolicy, bool) {
	return MirrorPolicy{}, false
}

// Sample reports whether one packet of a flow is sampled deterministically.
func (g *MirrorAuditGuard) Sample(f FlowKey, p MirrorPolicy, seq int64) bool {
	return false
}

// Project keeps only whitelisted header fields and masks IP fields.
func (g *MirrorAuditGuard) Project(f FlowKey, p MirrorPolicy, header map[string]string) map[string]string {
	return nil
}

// SetTarget registers or updates a mirror target.
func (g *MirrorAuditGuard) SetTarget(t MirrorTarget) error {
	return errNotImplemented
}

// Distribute delivers packets for a session within the target quota.
func (g *MirrorAuditGuard) Distribute(sessionID string, packets int) (int, error) {
	return 0, errNotImplemented
}

// SessionStats returns a session by id.
func (g *MirrorAuditGuard) SessionStats(id string) (MirrorSession, bool) {
	return MirrorSession{}, false
}

// TargetUsed returns the used quota of a target.
func (g *MirrorAuditGuard) TargetUsed(name string) (int, bool) {
	return 0, false
}

// Snapshot serialises policies, targets and sessions.
func (g *MirrorAuditGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the guard from a snapshot.
func (g *MirrorAuditGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *MirrorAuditGuard) Stats() MirrorStats {
	return MirrorStats{}
}
