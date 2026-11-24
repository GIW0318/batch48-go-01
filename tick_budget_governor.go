package task

import (
	"errors"
	"time"
)

// Sentinels reported by the tick budget governor.
var (
	errNotImplemented     = errors.New("not implemented")
	ErrSystemUnknown      = errors.New("system not registered")
	ErrSystemExists       = errors.New("system already registered")
	ErrInvalidWeight      = errors.New("weight must be positive")
	ErrInvalidMin         = errors.New("minimum budget must not be negative")
	ErrRegistrationClosed = errors.New("registration closed after the first tick")
	ErrTickInProgress     = errors.New("tick already open")
	ErrTickNotStarted     = errors.New("no tick open")
	ErrInvalidSpend       = errors.New("spend amount must be positive")
	ErrBudgetExceeded     = errors.New("tick budget exceeded")
	ErrInvalidLevel       = errors.New("degrade level must be between 0 and 3")
	ErrSystemSkipped      = errors.New("system is skipped for this tick")
	ErrClockBackwards     = errors.New("tick timestamp moved backwards")
	ErrBadSnapshot        = errors.New("snapshot payload rejected")
)

// TickPlan is the result of one closed tick.
type TickPlan struct {
	Tick       int
	Budget     int
	Allocated  map[string]int
	OverBudget []string
	Degraded   []string
	Skipped    []string
}

// SystemStats is the per system counter block.
type SystemStats struct {
	Quota     int
	Spent     int
	Borrowed  int
	Overruns  int
	StreakBad int
	StreakOK  int
	Skipped   int
}

// TickBudgetGovernor hands out per tick time budgets and manages degradation.
type TickBudgetGovernor struct {
}

// NewTickBudgetGovernor builds a governor over a fixed per tick budget.
func NewTickBudgetGovernor(totalMs int) *TickBudgetGovernor {
	return &TickBudgetGovernor{}
}

// Register adds a subsystem with a weight and a reserved minimum budget.
func (g *TickBudgetGovernor) Register(system string, weight float64, minMs int) error {
	return errNotImplemented
}

// BeginTick opens a new tick at the given monotonic timestamp.
func (g *TickBudgetGovernor) BeginTick(now time.Time) error {
	return errNotImplemented
}

// Spend charges time to a subsystem inside the open tick.
func (g *TickBudgetGovernor) Spend(system string, ms int) error {
	return errNotImplemented
}

// EndTick closes the open tick and returns its plan.
func (g *TickBudgetGovernor) EndTick() (TickPlan, error) {
	return TickPlan{}, errNotImplemented
}

// SetDegradeLevel forces the degradation level within its legal range.
func (g *TickBudgetGovernor) SetDegradeLevel(n int) error {
	return errNotImplemented
}

// DegradeLevel reports the current degradation level.
func (g *TickBudgetGovernor) DegradeLevel() int {
	return 0
}

// Remaining reports the unspent quota of a subsystem in the open tick.
func (g *TickBudgetGovernor) Remaining(system string) int {
	return 0
}

// Stats reports the counters of one subsystem.
func (g *TickBudgetGovernor) Stats(system string) (SystemStats, error) {
	return SystemStats{}, errNotImplemented
}

// Snapshot serialises the governor state.
func (g *TickBudgetGovernor) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the governor state from a snapshot payload.
func (g *TickBudgetGovernor) Restore(payload []byte) error {
	return errNotImplemented
}
