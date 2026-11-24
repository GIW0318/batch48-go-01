package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Guard level failures.
var (
	ErrBadTarget       = errors.New("target process is not usable")
	ErrBadWhitelist    = errors.New("group whitelist is empty or invalid")
	ErrGroupQuota      = errors.New("supplementary group quota exceeded")
	ErrGroupNotAllowed = errors.New("group is outside the registered whitelist")
	ErrThreadCount     = errors.New("multithreaded target cannot receive a revertible plan")
	ErrNonRevertible   = errors.New("plan crosses the user switch and needs explicit approval")
	ErrAlreadyApplied  = errors.New("plan has already been applied")
	ErrIrreversible    = errors.New("cannot roll back past the user switch")
	ErrNotPlanned      = errors.New("no plan is staged for apply or rollback")
	ErrBadSnapshot     = errors.New("snapshot payload is corrupt")
)

// Step is one stage of the fixed credential transition order.
type Step string

// The fixed transition order, from the most recoverable step to the last one.
const (
	StepGroups       Step = "groups"
	StepPrimaryGroup Step = "primary_group"
	StepUser         Step = "user"
	StepEnvCleanup   Step = "env_cleanup"
	StepNoNewPrivs   Step = "no_new_privs"
)

// Limits bounds one guard instance.
type Limits struct {
	MaxGroups  int
	MaxEnvVars int
}

// Creds is a credential triple: user, primary group and supplementary groups.
type Creds struct {
	Uid    int
	Gid    int
	Groups []int
}

// Target identifies the process a transition is planned for.
type Target struct {
	Pid     int
	Threads int
}

// Plan is the ordered list of steps for one transition.
type Plan struct {
	ID         int
	Target     Creds
	Steps      []Step
	Revertible bool
}

// Result reports what an apply call performed.
type Result struct {
	PlanID    int
	Steps     []Step
	EnvPurged int
}

// State is the guard state visible to operators.
type State struct {
	PlanID      int
	Credentials Creds
	Applied     []Step
	Revertible  bool
	NoNewPrivs  bool
}

// Stats counts guard activity.
type Stats struct {
	Planned    int
	Applied    int
	RolledBack int
	Rejected   int
}

// CredTransitionGuard orchestrates one credential drop of a privileged parent.
type CredTransitionGuard struct {
	limits    Limits
	whitelist map[int]bool
	current   Creds
	planID    int
	planned   Plan
	hasPlan   bool
	applied   bool
	steps     []Step
	audit     []string
	stats     Stats
}

// NewCredTransitionGuard builds a guard for the given limits.
func NewCredTransitionGuard(limits Limits) *CredTransitionGuard {
	return &CredTransitionGuard{limits: limits}
}

// StepOrder returns the fixed order in which the five steps run.
func (g *CredTransitionGuard) StepOrder() []Step {
	return nil
}

// SetGroupWhitelist registers the only supplementary groups a plan may request.
func (g *CredTransitionGuard) SetGroupWhitelist(groups []int) error {
	return errNotImplemented
}

// Plan builds an ordered transition plan for the target process.
func (g *CredTransitionGuard) Plan(target Target, want Creds) (Plan, error) {
	return Plan{}, errNotImplemented
}

// Apply performs the plan in order, optionally approving an irreversible one.
func (g *CredTransitionGuard) Apply(plan Plan, allowIrreversible bool) (Result, error) {
	return Result{}, errNotImplemented
}

// Rollback undoes the last applied plan in reverse order when it is revertible.
func (g *CredTransitionGuard) Rollback() error {
	return errNotImplemented
}

// Inspect returns the current progress of the guard.
func (g *CredTransitionGuard) Inspect() State {
	return State{}
}

// Snapshot serialises the guard state.
func (g *CredTransitionGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot without replaying completed steps.
func (g *CredTransitionGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *CredTransitionGuard) Stats() Stats {
	return Stats{}
}

// AuditLog returns the ordered audit records.
func (g *CredTransitionGuard) AuditLog() []string {
	return nil
}
