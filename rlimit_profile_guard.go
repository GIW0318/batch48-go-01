// Package task stacks named rlimit profiles onto processes and recomputes the
// ceilings a child keeps across exec.
package task

import "errors"

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

// Resource numbers used by the guard, following the Linux x86-64 order.
const (
	ResourceCPU    = 0
	ResourceStack  = 3
	ResourceCore   = 4
	ResourceNProc  = 6
	ResourceNoFile = 7
	ResourceAS     = 9
)

// Errors returned by the guard.
var (
	ErrBlankName        = errors.New("profile name is required")
	ErrDuplicateProfile = errors.New("profile already defined")
	ErrProfileLimit     = errors.New("profile limit reached")
	ErrStackLimit       = errors.New("profile stack limit reached")
	ErrSoftAboveHard    = errors.New("soft limit above hard limit")
	ErrHardRaise        = errors.New("hard limit cannot be raised")
	ErrProfileNotFound  = errors.New("unknown rlimit profile")
	ErrEmptyStack       = errors.New("at least one profile name is required")
	ErrUnknownPID       = errors.New("process has no applied profile")
	ErrBadSnapshot      = errors.New("snapshot payload is invalid")
)

// Limits holds the soft and hard ceilings per resource number. A resource that
// is absent from a map keeps the value the process already carries.
type Limits struct {
	Soft    map[int]uint64 `json:"soft,omitempty"`
	Hard    map[int]uint64 `json:"hard,omitempty"`
	OpenFDs uint64         `json:"open_fds,omitempty"`
}

// Options bounds the profile table and the stack depth.
type Options struct {
	MaxProfiles int `json:"max_profiles"`
	MaxStack    int `json:"max_stack"`
}

// Applied is the state a process carries after one or more profiles landed.
type Applied struct {
	PID    int      `json:"pid"`
	Names  []string `json:"names"`
	Limits Limits   `json:"limits"`
}

// Request is what a child asks for right before exec.
type Request struct {
	Names     []string       `json:"names"`
	RaiseSoft map[int]uint64 `json:"raise_soft,omitempty"`
	RaiseHard map[int]uint64 `json:"raise_hard,omitempty"`
}

// RlimitProfileGuard owns the profile table and the per-process state.
type RlimitProfileGuard struct {
	opts Options
}

// NewRlimitProfileGuard builds a guard with the given table bounds.
func NewRlimitProfileGuard(opts Options) *RlimitProfileGuard {
	return &RlimitProfileGuard{opts: opts}
}

// Define registers a named profile.
func (g *RlimitProfileGuard) Define(name string, limits Limits) error {
	return errNotImplemented
}

// Apply stacks the named profiles onto a process and returns the new state.
func (g *RlimitProfileGuard) Apply(pid int, names []string) (Applied, error) {
	return Applied{}, errNotImplemented
}

// Inherit recomputes the limits a child keeps across exec.
func (g *RlimitProfileGuard) Inherit(parent Applied, child Request) (Applied, error) {
	return Applied{}, errNotImplemented
}

// Effective reports the limits currently in force for a process.
func (g *RlimitProfileGuard) Effective(pid int) (Limits, error) {
	return Limits{}, errNotImplemented
}

// Reset drops the guard state for a process.
func (g *RlimitProfileGuard) Reset(pid int) error {
	return errNotImplemented
}

// Snapshot serialises the profile table and every per-process state.
func (g *RlimitProfileGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot.
func (g *RlimitProfileGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Profile returns one stored profile.
func (g *RlimitProfileGuard) Profile(name string) (Limits, bool) {
	return Limits{}, false
}

// Profiles lists defined profile names in definition order.
func (g *RlimitProfileGuard) Profiles() []string {
	return nil
}

// TrackOpenFDs records how many descriptors a process actually holds.
func (g *RlimitProfileGuard) TrackOpenFDs(pid int, open uint64) error {
	return errNotImplemented
}
