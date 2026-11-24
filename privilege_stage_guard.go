// Package task drives a root started service down a fixed privilege ladder and
// keeps the observed credentials of every completed stage.
package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("privilege stage guard: not implemented")

// Errors returned by the guard.
var (
	ErrPlanInvalid      = errors.New("privilege stage guard: plan invalid")
	ErrUnknownStage     = errors.New("privilege stage guard: unknown stage")
	ErrOutOfOrder       = errors.New("privilege stage guard: stage out of order")
	ErrReadbackMismatch = errors.New("privilege stage guard: readback mismatch")
	ErrStagePending     = errors.New("privilege stage guard: stage pending")
	ErrBadSnapshot      = errors.New("privilege stage guard: bad snapshot")
)

// StageKind identifies what a single descent step does.
type StageKind int

// Supported stage kinds, in the only order a ladder may declare them.
const (
	StageGroups StageKind = iota
	StageDropGID
	StageDropUID
	StageNoNewPrivs
)

// Credentials is the observed credential set of the service process.
type Credentials struct {
	RealUID    int
	RealGID    int
	EUID       int
	EGID       int
	Groups     []int
	NoNewPrivs bool
	CapMask    uint64
}

// StageSpec declares one step of the descent ladder.
type StageSpec struct {
	Name         string
	Kind         StageKind
	Value        int
	Groups       []int
	DependsOn    []string
	Irreversible bool
}

// Plan is the whole ladder plus the readback hook used after every step.
type Plan struct {
	Stages []StageSpec
	Probe  func(stage string) Credentials
}

// StageResult is returned for one stage attempt.
type StageResult struct {
	Stage       string
	Applied     bool
	Credentials Credentials
	Readback    Credentials
}

// JournalEntry records one applied stage in completion order.
type JournalEntry struct {
	Sequence    int
	Stage       string
	Credentials Credentials
	Readback    Credentials
}

// PrivilegeStageGuard serialises a single descent ladder.
type PrivilegeStageGuard struct {
}

// NewPrivilegeStageGuard validates the ladder and returns a fresh guard.
func NewPrivilegeStageGuard(plan Plan) (*PrivilegeStageGuard, error) {
	return nil, errNotImplemented
}

// Run advances exactly one stage and reads the real credentials back.
func (g *PrivilegeStageGuard) Run(stage string) (StageResult, error) {
	return StageResult{}, errNotImplemented
}

// Current returns the credentials the service is modelled to hold now.
func (g *PrivilegeStageGuard) Current() Credentials {
	return Credentials{}
}

// Verify checks the named stage against the plan and the current readback.
func (g *PrivilegeStageGuard) Verify(stage string) error {
	return errNotImplemented
}

// Completed lists the completed stage names in plan order.
func (g *PrivilegeStageGuard) Completed() []string {
	return nil
}

// Pending lists the not yet completed stage names in plan order.
func (g *PrivilegeStageGuard) Pending() []string {
	return nil
}

// Journal returns the audit trail in completion order.
func (g *PrivilegeStageGuard) Journal() []JournalEntry {
	return nil
}

// StageNames returns every declared stage name in plan order.
func (g *PrivilegeStageGuard) StageNames() []string {
	return nil
}

// Stage looks up one declared stage.
func (g *PrivilegeStageGuard) Stage(name string) (StageSpec, bool) {
	return StageSpec{}, false
}

// Snapshot serialises the completion bits and the readback results.
func (g *PrivilegeStageGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (g *PrivilegeStageGuard) Restore(b []byte) error {
	return errNotImplemented
}
