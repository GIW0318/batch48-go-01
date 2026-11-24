package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the pull down planner.
var (
	ErrRateRejected    = errors.New("frame rate is not usable")
	ErrSourceUnderrun  = errors.New("source frame has not arrived yet")
	ErrPolicyRejected  = errors.New("playout policy is not usable")
	ErrFrameOrder      = errors.New("source frame index is not usable")
	ErrPlannerSnapshot = errors.New("pull down planner snapshot is not readable")
)

// Rational is an exact frame rate expressed as a fraction.
type Rational struct {
	Num int64
	Den int64
}

// PlayoutPolicy bounds how many repeats or drops are tolerated in a row.
type PlayoutPolicy struct {
	MaxRepeat int
	MaxDrop   int
}

// Decision is one output slot of the conversion.
type Decision struct {
	Output  int64
	Source  int64
	Repeat  bool
	Dropped int
}

// PlannerStats reports the counters kept by the planner.
type PlannerStats struct {
	Outputs    uint64
	Repeats    uint64
	Drops      uint64
	RepeatOver uint64
	DropOver   uint64
	Resets     uint64
}

// PullDownPlanner maps a source frame rate onto a target frame rate.
type PullDownPlanner struct {
	mu sync.Mutex
}

// NewPullDownPlanner validates both rates and builds a planner.
func NewPullDownPlanner(src, dst Rational) (*PullDownPlanner, error) {
	return nil, errNotImplemented
}

// Configure replaces both the source and the target rate and resets the
// output phase.
func (p *PullDownPlanner) Configure(src, dst Rational) error {
	return errNotImplemented
}

// SetTargetRate replaces the target rate and resets the output phase.
func (p *PullDownPlanner) SetTargetRate(dst Rational) error {
	return errNotImplemented
}

// PushFrame reports that one more source frame has arrived.
func (p *PullDownPlanner) PushFrame(index int64) error {
	return errNotImplemented
}

// Next produces the decision for the next output slot.
func (p *PullDownPlanner) Next() (Decision, error) {
	return Decision{}, errNotImplemented
}

// SetPolicy replaces the repeat and drop tolerances.
func (p *PullDownPlanner) SetPolicy(policy PlayoutPolicy) error {
	return errNotImplemented
}

// ExpectedOutputs reports how many output frames a source frame count turns
// into under the configured rates.
func (p *PullDownPlanner) ExpectedOutputs(sourceFrames int64) int64 {
	return 0
}

// AccumulatedError reports the fractional part left in the source time base
// for the next output slot, in units of 1/src.Den of a source frame.
func (p *PullDownPlanner) AccumulatedError() int64 {
	return 0
}

// Phase reports the index of the next output slot.
func (p *PullDownPlanner) Phase() int64 {
	return 0
}

// FramesAvailable reports how many source frames have been pushed.
func (p *PullDownPlanner) FramesAvailable() int64 {
	return 0
}

// Reset drops every pushed frame and restarts the output phase.
func (p *PullDownPlanner) Reset() error {
	return errNotImplemented
}

// Snapshot serialises the planner state.
func (p *PullDownPlanner) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the planner state from a snapshot.
func (p *PullDownPlanner) Restore(data []byte) error {
	return errNotImplemented
}

// Stats reports the counters kept by the planner.
func (p *PullDownPlanner) Stats() PlannerStats {
	return PlannerStats{}
}
