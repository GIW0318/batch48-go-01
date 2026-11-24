package task

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

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

	// ErrBadPlan is returned when a caller tampers with a plan, including
	// reordering the fixed transition steps.
	ErrBadPlan = errors.New("plan is not in the required fixed order")
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

var fixedStepOrder = []Step{
	StepGroups,
	StepPrimaryGroup,
	StepUser,
	StepEnvCleanup,
	StepNoNewPrivs,
}

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

type appliedTransition struct {
	Plan             Plan
	Before           Creds
	BeforeNoNewPrivs bool
}

// CredTransitionGuard orchestrates one credential drop of a privileged parent.
type CredTransitionGuard struct {
	mu sync.Mutex

	limits Limits

	whitelist    map[int]bool
	whitelistSet bool

	current    Creds
	noNewPrivs bool

	planID int

	planned     Plan
	plannedTgt  Target
	hasPlan     bool
	usedPlanIDs map[int]bool
	transitions []appliedTransition
	audit       []string
	stats       Stats
}

// NewCredTransitionGuard builds a guard for the given limits.
func NewCredTransitionGuard(limits Limits) *CredTransitionGuard {
	return &CredTransitionGuard{
		limits:      limits,
		whitelist:   make(map[int]bool),
		usedPlanIDs: make(map[int]bool),
	}
}

// StepOrder returns the fixed order in which the five steps run.
func (g *CredTransitionGuard) StepOrder() []Step {
	return cloneSteps(fixedStepOrder)
}

// SetGroupWhitelist registers the only supplementary groups a plan may request.
func (g *CredTransitionGuard) SetGroupWhitelist(groups []int) error {
	if len(groups) == 0 {
		return ErrBadWhitelist
	}
	next := make(map[int]bool, len(groups))
	for _, group := range groups {
		if group < 0 {
			return ErrBadWhitelist
		}
		next[group] = true
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.whitelist = next
	g.whitelistSet = true
	return nil
}

// Plan builds an ordered transition plan for the target process.
func (g *CredTransitionGuard) Plan(target Target, want Creds) (Plan, error) {
	if err := validateTarget(target); err != nil {
		g.reject()
		return Plan{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.validateRequestLocked(want); err != nil {
		g.stats.Rejected++
		return Plan{}, err
	}

	revertible := want.Uid == g.current.Uid
	if revertible && target.Threads > 1 {
		g.stats.Rejected++
		return Plan{}, ErrThreadCount
	}
	if g.noNewPrivs && !permissionsSubset(want, g.current) {
		g.stats.Rejected++
		return Plan{}, fmt.Errorf("%w: target credentials are not a subset of the current credentials", ErrGroupNotAllowed)
	}

	g.planID++
	plan := Plan{
		ID:         g.planID,
		Target:     cloneCreds(want),
		Steps:      cloneSteps(fixedStepOrder),
		Revertible: revertible,
	}
	g.planned = clonePlan(plan)
	g.plannedTgt = target
	g.hasPlan = true
	g.stats.Planned++
	return clonePlan(plan), nil
}

// Apply performs the plan in order, optionally approving an irreversible one.
func (g *CredTransitionGuard) Apply(plan Plan, allowIrreversible bool) (Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.usedPlanIDs[plan.ID] {
		g.stats.Rejected++
		return Result{}, ErrAlreadyApplied
	}
	if !g.hasPlan || plan.ID != g.planned.ID {
		g.stats.Rejected++
		return Result{}, ErrNotPlanned
	}
	if !plansEqual(plan, g.planned) {
		g.stats.Rejected++
		return Result{}, ErrBadPlan
	}

	// Re-evaluate the safety properties against the live state. The plan was
	// checked at creation time but another transition may have completed since.
	effectiveRevertible := plan.Target.Uid == g.current.Uid
	if effectiveRevertible != plan.Revertible {
		g.stats.Rejected++
		return Result{}, ErrBadPlan
	}
	if effectiveRevertible && g.plannedTgt.Threads > 1 {
		g.stats.Rejected++
		return Result{}, ErrThreadCount
	}
	if !effectiveRevertible && !allowIrreversible {
		g.stats.Rejected++
		return Result{}, ErrNonRevertible
	}
	if g.noNewPrivs && !permissionsSubset(plan.Target, g.current) {
		g.stats.Rejected++
		return Result{}, fmt.Errorf("%w: target credentials are not a subset of the current credentials", ErrGroupNotAllowed)
	}
	if !stepsEqual(plan.Steps, fixedStepOrder) {
		g.stats.Rejected++
		return Result{}, ErrBadPlan
	}

	before := cloneCreds(g.current)
	beforeNoNewPrivs := g.noNewPrivs
	next := cloneCreds(g.current)
	nextNoNewPrivs := g.noNewPrivs
	envPurged := 0
	records := make([]string, 0, len(plan.Steps))

	// Build the complete next state before publishing any part of it. This
	// keeps apply all-or-nothing even if a future step gains a failure path.
	for _, step := range plan.Steps {
		switch step {
		case StepGroups:
			next.Groups = cloneGroups(plan.Target.Groups)
		case StepPrimaryGroup:
			next.Gid = plan.Target.Gid
		case StepUser:
			next.Uid = plan.Target.Uid
		case StepEnvCleanup:
			envPurged = g.envPurgeCountLocked()
		case StepNoNewPrivs:
			nextNoNewPrivs = true
		default:
			g.stats.Rejected++
			return Result{}, ErrBadPlan
		}
		records = append(records, "apply:"+string(step))
	}

	transition := appliedTransition{
		Plan:             clonePlan(plan),
		Before:           before,
		BeforeNoNewPrivs: beforeNoNewPrivs,
	}
	g.current = next
	g.noNewPrivs = nextNoNewPrivs
	g.transitions = append(g.transitions, transition)
	g.usedPlanIDs[plan.ID] = true
	g.hasPlan = false
	g.audit = append(g.audit, records...)
	g.stats.Applied++

	return Result{
		PlanID:    plan.ID,
		Steps:     cloneSteps(plan.Steps),
		EnvPurged: envPurged,
	}, nil
}

// Rollback undoes the last applied plan in reverse order when it is revertible.
func (g *CredTransitionGuard) Rollback() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.transitions) == 0 {
		g.stats.Rejected++
		return ErrNotPlanned
	}

	last := g.transitions[len(g.transitions)-1]
	if !last.Plan.Revertible {
		g.stats.Rejected++
		return ErrIrreversible
	}

	next := cloneCreds(g.current)
	nextNoNewPrivs := g.noNewPrivs
	records := make([]string, 0, len(last.Plan.Steps))

	for i := len(last.Plan.Steps) - 1; i >= 0; i-- {
		step := last.Plan.Steps[i]
		switch step {
		case StepNoNewPrivs:
			nextNoNewPrivs = last.BeforeNoNewPrivs
		case StepEnvCleanup:
			// The environment is process-local and deliberately not part of
			// the credential snapshot.
		case StepUser:
			next.Uid = last.Before.Uid
		case StepPrimaryGroup:
			next.Gid = last.Before.Gid
		case StepGroups:
			// Restore the exact original slice, including duplicates and order.
			next.Groups = cloneGroups(last.Before.Groups)
		default:
			g.stats.Rejected++
			return ErrBadPlan
		}
		records = append(records, "rollback:"+string(step))
	}

	g.current = next
	g.noNewPrivs = nextNoNewPrivs
	g.transitions = g.transitions[:len(g.transitions)-1]
	g.audit = append(g.audit, records...)
	g.stats.RolledBack++
	if g.hasPlan && g.planned.ID == last.Plan.ID {
		g.hasPlan = false
	}
	return nil
}

// Inspect returns the current progress of the guard.
func (g *CredTransitionGuard) Inspect() State {
	g.mu.Lock()
	defer g.mu.Unlock()

	state := State{
		PlanID:      g.planID,
		Credentials: cloneCreds(g.current),
		NoNewPrivs:  g.noNewPrivs,
	}
	if len(g.transitions) > 0 {
		last := g.transitions[len(g.transitions)-1]
		state.Applied = cloneSteps(last.Plan.Steps)
		state.Revertible = last.Plan.Revertible
	}
	return state
}

type snapshotTransition struct {
	Plan             Plan  `json:"plan"`
	Before           Creds `json:"before"`
	BeforeNoNewPrivs bool  `json:"before_no_new_privs"`
}

type snapshotState struct {
	PlanID       int                  `json:"plan_id"`
	Current      Creds                `json:"current"`
	NoNewPrivs   bool                 `json:"no_new_privs"`
	Planned      Plan                 `json:"planned"`
	PlannedTgt   Target               `json:"planned_target"`
	HasPlan      bool                 `json:"has_plan"`
	Whitelist    []int                `json:"whitelist"`
	WhitelistSet bool                 `json:"whitelist_set"`
	UsedPlanIDs  []int                `json:"used_plan_ids"`
	Transitions  []snapshotTransition `json:"transitions"`
	Audit        []string             `json:"audit"`
	Stats        Stats                `json:"stats"`
}

type snapshotEnvelope struct {
	Version  int    `json:"version"`
	Payload  []byte `json:"payload"`
	Checksum string `json:"checksum"`
}

const snapshotVersion = 1

// Snapshot serialises the guard state.
func (g *CredTransitionGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	state := g.snapshotLocked()
	g.mu.Unlock()

	payload, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	envelope := snapshotEnvelope{
		Version:  snapshotVersion,
		Payload:  payload,
		Checksum: snapshotChecksum(payload),
	}
	return json.Marshal(envelope)
}

// Restore loads a snapshot without replaying completed steps.
func (g *CredTransitionGuard) Restore(data []byte) error {
	// Keep restore, plan, apply and rollback on one serial timeline. In
	// particular no plan may be staged while the state swap is in progress.
	g.mu.Lock()
	defer g.mu.Unlock()

	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ErrBadSnapshot
	}
	if envelope.Version != snapshotVersion || len(envelope.Payload) == 0 {
		return ErrBadSnapshot
	}
	if subtle.ConstantTimeCompare(
		[]byte(envelope.Checksum),
		[]byte(snapshotChecksum(envelope.Payload)),
	) != 1 {
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
	if err := g.validateSnapshotLocked(state); err != nil {
		return ErrBadSnapshot
	}

	// The sequence is a high-water mark: restoring an older image into a live
	// process must never make future plan identifiers go backwards.
	if state.PlanID > g.planID {
		g.planID = state.PlanID
	}
	g.current = cloneCreds(state.Current)
	g.noNewPrivs = state.NoNewPrivs
	g.planned = clonePlan(state.Planned)
	g.plannedTgt = state.PlannedTgt
	g.hasPlan = state.HasPlan
	g.whitelist = make(map[int]bool, len(state.Whitelist))
	for _, group := range state.Whitelist {
		g.whitelist[group] = true
	}
	g.whitelistSet = state.WhitelistSet
	g.usedPlanIDs = make(map[int]bool, len(state.UsedPlanIDs))
	for _, id := range state.UsedPlanIDs {
		g.usedPlanIDs[id] = true
	}
	g.transitions = make([]appliedTransition, 0, len(state.Transitions))
	for _, transition := range state.Transitions {
		g.transitions = append(g.transitions, appliedTransition{
			Plan:             clonePlan(transition.Plan),
			Before:           cloneCreds(transition.Before),
			BeforeNoNewPrivs: transition.BeforeNoNewPrivs,
		})
	}
	g.audit = append([]string(nil), state.Audit...)
	g.stats = state.Stats
	return nil
}

// Stats returns the activity counters.
func (g *CredTransitionGuard) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

// AuditLog returns the ordered audit records.
func (g *CredTransitionGuard) AuditLog() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.audit...)
}

func (g *CredTransitionGuard) reject() {
	g.mu.Lock()
	g.stats.Rejected++
	g.mu.Unlock()
}

func (g *CredTransitionGuard) validateRequestLocked(want Creds) error {
	if want.Uid < 0 || want.Gid < 0 {
		return fmt.Errorf("%w: uid and gid must be non-negative", ErrBadTarget)
	}
	if groupQuotaExceeded(want.Groups, g.limits.MaxGroups) {
		return ErrGroupQuota
	}
	for _, group := range want.Groups {
		if group < 0 {
			return fmt.Errorf("%w: supplementary group %d is negative", ErrBadTarget, group)
		}
		if g.whitelistSet && !g.whitelist[group] {
			return fmt.Errorf("%w: group %d", ErrGroupNotAllowed, group)
		}
	}
	return nil
}

func (g *CredTransitionGuard) validateSnapshotLocked(state snapshotState) error {
	if state.PlanID < 0 || state.Stats.Planned < 0 || state.Stats.Applied < 0 ||
		state.Stats.RolledBack < 0 || state.Stats.Rejected < 0 {
		return ErrBadSnapshot
	}
	if err := validateCreds(state.Current, g.limits.MaxGroups); err != nil {
		return err
	}
	if state.WhitelistSet && len(state.Whitelist) == 0 {
		return ErrBadSnapshot
	}
	if !state.WhitelistSet && len(state.Whitelist) != 0 {
		return ErrBadSnapshot
	}
	whitelist := make(map[int]bool, len(state.Whitelist))
	for _, group := range state.Whitelist {
		if group < 0 || whitelist[group] {
			return ErrBadSnapshot
		}
		whitelist[group] = true
	}

	seenPlans := make(map[int]bool, len(state.UsedPlanIDs))
	for _, id := range state.UsedPlanIDs {
		if id <= 0 || id > state.PlanID || seenPlans[id] {
			return ErrBadSnapshot
		}
		seenPlans[id] = true
	}

	if state.HasPlan {
		if state.Planned.ID <= 0 || state.Planned.ID > state.PlanID ||
			seenPlans[state.Planned.ID] {
			return ErrBadSnapshot
		}
		if err := validateStoredPlan(state.Planned, g.limits.MaxGroups, whitelist, state.WhitelistSet, state.PlannedTgt); err != nil {
			return err
		}
	}

	for _, transition := range state.Transitions {
		if transition.Plan.ID <= 0 || transition.Plan.ID > state.PlanID ||
			!seenPlans[transition.Plan.ID] {
			return ErrBadSnapshot
		}
		if err := validateStoredPlan(transition.Plan, g.limits.MaxGroups, whitelist, state.WhitelistSet, Target{}); err != nil {
			return err
		}
		if err := validateCreds(transition.Before, g.limits.MaxGroups); err != nil {
			return ErrBadSnapshot
		}
		if transition.Plan.Revertible != (transition.Plan.Target.Uid == transition.Before.Uid) {
			return ErrBadSnapshot
		}
	}
	if len(state.Transitions) > 0 {
		last := state.Transitions[len(state.Transitions)-1]
		if !credsEqual(state.Current, last.Plan.Target) {
			return ErrBadSnapshot
		}
	}
	return nil
}

func (g *CredTransitionGuard) snapshotLocked() snapshotState {
	state := snapshotState{
		PlanID:       g.planID,
		Current:      cloneCreds(g.current),
		NoNewPrivs:   g.noNewPrivs,
		Planned:      clonePlan(g.planned),
		PlannedTgt:   g.plannedTgt,
		HasPlan:      g.hasPlan,
		WhitelistSet: g.whitelistSet,
		Stats:        g.stats,
		Audit:        append([]string(nil), g.audit...),
	}
	for group := range g.whitelist {
		state.Whitelist = append(state.Whitelist, group)
	}
	sort.Ints(state.Whitelist)
	for id := range g.usedPlanIDs {
		state.UsedPlanIDs = append(state.UsedPlanIDs, id)
	}
	sort.Ints(state.UsedPlanIDs)
	for _, transition := range g.transitions {
		state.Transitions = append(state.Transitions, snapshotTransition{
			Plan:             clonePlan(transition.Plan),
			Before:           cloneCreds(transition.Before),
			BeforeNoNewPrivs: transition.BeforeNoNewPrivs,
		})
	}
	return state
}

// envPurgeCountLocked models the inherited environment bound to this guard.
// The API has no environment setter, so the configured environment budget is
// the complete inherited set. The count is reported by Apply and is never
// truncated beyond the configured cap.
func (g *CredTransitionGuard) envPurgeCountLocked() int {
	if g.limits.MaxEnvVars <= 0 {
		return 0
	}
	return g.limits.MaxEnvVars
}

func validateTarget(target Target) error {
	if target.Pid <= 0 {
		return ErrBadTarget
	}
	return nil
}

func validateCreds(creds Creds, maxGroups int) error {
	if creds.Uid < 0 || creds.Gid < 0 {
		return ErrBadSnapshot
	}
	if groupQuotaExceeded(creds.Groups, maxGroups) {
		return ErrBadSnapshot
	}
	for _, group := range creds.Groups {
		if group < 0 {
			return ErrBadSnapshot
		}
	}
	return nil
}

func validateStoredPlan(plan Plan, maxGroups int, whitelist map[int]bool, whitelistSet bool, target Target) error {
	if plan.ID <= 0 || !stepsEqual(plan.Steps, fixedStepOrder) {
		return ErrBadSnapshot
	}
	if err := validateCreds(plan.Target, maxGroups); err != nil {
		return err
	}
	if target.Pid < 0 || target.Threads < 0 {
		return ErrBadSnapshot
	}
	for _, group := range plan.Target.Groups {
		if whitelistSet && !whitelist[group] {
			return ErrBadSnapshot
		}
	}
	return nil
}

func groupQuotaExceeded(groups []int, maxGroups int) bool {
	if maxGroups < 0 {
		maxGroups = 0
	}
	return len(groups) > maxGroups
}

func permissionsSubset(target, current Creds) bool {
	// Root owns the whole permission space, so every drop is a subset.
	if current.Uid == 0 {
		return true
	}
	if target.Uid == 0 {
		return false
	}
	allowed := make(map[int]bool, len(current.Groups)+1)
	allowed[current.Gid] = true
	for _, group := range current.Groups {
		allowed[group] = true
	}
	if !allowed[target.Gid] {
		return false
	}
	for _, group := range target.Groups {
		if !allowed[group] {
			return false
		}
	}
	return true
}

func cloneCreds(creds Creds) Creds {
	return Creds{Uid: creds.Uid, Gid: creds.Gid, Groups: cloneGroups(creds.Groups)}
}

func cloneGroups(groups []int) []int {
	if groups == nil {
		return nil
	}
	return append([]int(nil), groups...)
}

func cloneSteps(steps []Step) []Step {
	return append([]Step(nil), steps...)
}

func clonePlan(plan Plan) Plan {
	return Plan{
		ID:         plan.ID,
		Target:     cloneCreds(plan.Target),
		Steps:      cloneSteps(plan.Steps),
		Revertible: plan.Revertible,
	}
}

func plansEqual(a, b Plan) bool {
	return a.ID == b.ID &&
		a.Revertible == b.Revertible &&
		credsEqual(a.Target, b.Target) &&
		stepsEqual(a.Steps, b.Steps)
}

func credsEqual(a, b Creds) bool {
	if a.Uid != b.Uid || a.Gid != b.Gid || len(a.Groups) != len(b.Groups) {
		return false
	}
	for i := range a.Groups {
		if a.Groups[i] != b.Groups[i] {
			return false
		}
	}
	return true
}

func stepsEqual(a, b []Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func snapshotChecksum(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
