package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Sentinels reported by the tick budget governor.
var (
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

const (
	maxDegradeLevel   = 3
	badTicksToDegrade = 2
	okTicksToRecover  = 3
	snapshotVersion   = 1
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

type systemState struct {
	name            string
	weight          float64
	minMs           int
	quota           int
	spent           int
	borrowed        int
	overrunMs       int
	overruns        int
	streakBad       int
	streakOK        int
	skipped         int
	skippedThisTick bool
}

// TickBudgetGovernor hands out per tick time budgets and manages degradation.
type TickBudgetGovernor struct {
	mu                 sync.Mutex
	totalMs            int
	tickSeq            int
	totalQuota         int
	open               bool
	registrationClosed bool
	hasLastTick        bool
	lastTickAt         time.Time
	level              int
	okStreak           int
	tickLevel          int
	degradedThisTick   []string
	skippedThisTick    []string
	systems            map[string]*systemState
	order              []string
	totalSpent         atomic.Int64
}

// NewTickBudgetGovernor builds a governor over a fixed per tick budget.
func NewTickBudgetGovernor(totalMs int) *TickBudgetGovernor {
	if totalMs < 0 {
		totalMs = 0
	}
	return &TickBudgetGovernor{
		totalMs: totalMs,
		systems: make(map[string]*systemState),
	}
}

// Register adds a subsystem with a weight and a reserved minimum budget.
func (g *TickBudgetGovernor) Register(system string, weight float64, minMs int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.registrationClosed {
		return ErrRegistrationClosed
	}
	if _, exists := g.systems[system]; exists {
		return ErrSystemExists
	}
	if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return ErrInvalidWeight
	}
	if minMs < 0 {
		return ErrInvalidMin
	}

	g.systems[system] = &systemState{
		name:   system,
		weight: weight,
		minMs:  minMs,
	}
	g.order = append(g.order, system)
	return nil
}

// BeginTick opens a new tick at the given monotonic timestamp.
func (g *TickBudgetGovernor) BeginTick(now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.open {
		return ErrTickInProgress
	}
	if g.hasLastTick && now.Before(g.lastTickAt) {
		return ErrClockBackwards
	}

	quotas, totalQuota := g.allocateQuotasLocked()
	g.tickSeq++
	g.registrationClosed = true
	g.open = true
	g.tickLevel = g.level
	g.totalQuota = totalQuota
	g.totalSpent.Store(0)
	g.degradedThisTick = nil
	g.skippedThisTick = nil

	for _, name := range g.order {
		state := g.systems[name]
		state.quota = quotas[name]
		state.spent = 0
		state.borrowed = 0
		state.skippedThisTick = false

		if g.level > 0 && state.minMs <= 0 {
			g.degradedThisTick = append(g.degradedThisTick, name)
			if shouldSkipSystem(g.level, g.tickSeq) {
				state.skippedThisTick = true
				state.skipped = saturatingAdd(state.skipped, 1)
				g.skippedThisTick = append(g.skippedThisTick, name)
			}
		}
	}

	g.lastTickAt = now
	g.hasLastTick = true
	return nil
}

// Spend charges time to a subsystem inside the open tick.
func (g *TickBudgetGovernor) Spend(system string, ms int) error {
	if ms <= 0 {
		return ErrInvalidSpend
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.open {
		return ErrTickNotStarted
	}
	state, ok := g.systems[system]
	if !ok {
		return ErrSystemUnknown
	}
	if state.skippedThisTick {
		return ErrSystemSkipped
	}

	amount := int64(ms)
	spent := g.totalSpent.Load()
	if amount > int64(g.totalMs)-spent || amount > int64(g.totalQuota)-spent {
		return ErrBudgetExceeded
	}

	newTotal := g.totalSpent.Add(amount)
	if newTotal > int64(g.totalMs) || newTotal > int64(g.totalQuota) {
		g.totalSpent.Add(-amount)
		return ErrBudgetExceeded
	}

	state.spent += ms
	state.borrowed = maxInt(0, state.spent-state.quota)
	return nil
}

// EndTick closes the open tick and returns its plan.
func (g *TickBudgetGovernor) EndTick() (TickPlan, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.open {
		return TickPlan{}, ErrTickNotStarted
	}

	plan := TickPlan{
		Tick:       g.tickSeq,
		Budget:     g.totalMs,
		Allocated:  make(map[string]int, len(g.order)),
		OverBudget: make([]string, 0),
		Degraded:   append(make([]string, 0, len(g.degradedThisTick)), g.degradedThisTick...),
		Skipped:    append(make([]string, 0, len(g.skippedThisTick)), g.skippedThisTick...),
	}

	anyOverBudget := false
	shouldDegrade := false
	for _, name := range g.order {
		state := g.systems[name]
		plan.Allocated[name] = state.quota

		overrun := state.spent - state.quota
		if overrun > 0 {
			anyOverBudget = true
			plan.OverBudget = append(plan.OverBudget, name)
			state.overruns = saturatingAdd(state.overruns, 1)
			state.overrunMs = saturatingAdd(state.overrunMs, overrun)
			state.streakBad = saturatingAdd(state.streakBad, 1)
			state.streakOK = 0
			if state.streakBad%badTicksToDegrade == 0 {
				shouldDegrade = true
			}
			continue
		}

		state.streakBad = 0
		state.streakOK = saturatingAdd(state.streakOK, 1)
	}

	if anyOverBudget {
		g.okStreak = 0
	} else {
		g.okStreak = saturatingAdd(g.okStreak, 1)
		if g.okStreak >= okTicksToRecover {
			if g.level > 0 {
				g.level--
			}
			g.okStreak = 0
		}
	}

	if shouldDegrade && g.level < maxDegradeLevel {
		g.level++
	}

	g.open = false
	return plan, nil
}

// SetDegradeLevel forces the degradation level within its legal range.
func (g *TickBudgetGovernor) SetDegradeLevel(n int) error {
	if n < 0 || n > maxDegradeLevel {
		return ErrInvalidLevel
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.level = n
	return nil
}

// DegradeLevel reports the current degradation level.
func (g *TickBudgetGovernor) DegradeLevel() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.level
}

// Remaining reports the unspent quota of a subsystem in the open tick.
func (g *TickBudgetGovernor) Remaining(system string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.systems[system]
	if !ok {
		return 0
	}
	return state.quota - state.spent
}

// Stats reports the counters of one subsystem.
func (g *TickBudgetGovernor) Stats(system string) (SystemStats, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.systems[system]
	if !ok {
		return SystemStats{}, ErrSystemUnknown
	}
	return SystemStats{
		Quota:     state.quota,
		Spent:     state.spent,
		Borrowed:  state.borrowed,
		Overruns:  state.overruns,
		StreakBad: state.streakBad,
		StreakOK:  state.streakOK,
		Skipped:   state.skipped,
	}, nil
}

// Snapshot serialises the governor state.
func (g *TickBudgetGovernor) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	payload := snapshotPayload{
		Version:            snapshotVersion,
		TotalMs:            g.totalMs,
		TickSeq:            g.tickSeq,
		TotalQuota:         g.totalQuota,
		Open:               g.open,
		RegistrationClosed: g.registrationClosed,
		HasLastTick:        g.hasLastTick,
		Level:              g.level,
		OKStreak:           g.okStreak,
		TickLevel:          g.tickLevel,
		DegradedThisTick:   append([]string(nil), g.degradedThisTick...),
		SkippedThisTick:    append([]string(nil), g.skippedThisTick...),
		Order:              append([]string(nil), g.order...),
		Systems:            make([]systemSnapshot, 0, len(g.order)),
	}
	if g.hasLastTick {
		payload.LastTickUnixNano = g.lastTickAt.UnixNano()
	}

	for _, name := range g.order {
		state := g.systems[name]
		payload.Systems = append(payload.Systems, systemSnapshot{
			Name:            state.name,
			Weight:          state.weight,
			MinMs:           state.minMs,
			Quota:           state.quota,
			Spent:           state.spent,
			Borrowed:        state.borrowed,
			OverrunMs:       state.overrunMs,
			Overruns:        state.overruns,
			StreakBad:       state.streakBad,
			StreakOK:        state.streakOK,
			Skipped:         state.skipped,
			SkippedThisTick: state.skippedThisTick,
		})
	}
	payload.TotalSpent = g.totalSpent.Load()

	data, err := json.Marshal(&payload)
	if err != nil {
		return nil, fmt.Errorf("snapshot tick budget governor: %w", err)
	}
	return data, nil
}

// Restore rebuilds the governor state from a snapshot payload.
func (g *TickBudgetGovernor) Restore(payload []byte) error {
	var snapshot snapshotPayload
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty payload", ErrBadSnapshot)
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrBadSnapshot, err)
	}
	if err := validateSnapshot(&snapshot); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	systems := make(map[string]*systemState, len(snapshot.Systems))
	order := make([]string, 0, len(snapshot.Systems))
	for _, item := range snapshot.Systems {
		systems[item.Name] = &systemState{
			name:            item.Name,
			weight:          item.Weight,
			minMs:           item.MinMs,
			quota:           item.Quota,
			spent:           item.Spent,
			borrowed:        item.Borrowed,
			overrunMs:       item.OverrunMs,
			overruns:        item.Overruns,
			streakBad:       item.StreakBad,
			streakOK:        item.StreakOK,
			skipped:         item.Skipped,
			skippedThisTick: item.SkippedThisTick,
		}
		order = append(order, item.Name)
	}

	g.totalMs = snapshot.TotalMs
	g.tickSeq = snapshot.TickSeq
	g.totalQuota = snapshot.TotalQuota
	g.open = snapshot.Open
	g.registrationClosed = snapshot.RegistrationClosed
	g.hasLastTick = snapshot.HasLastTick
	if snapshot.HasLastTick {
		g.lastTickAt = time.Unix(0, snapshot.LastTickUnixNano)
	} else {
		g.lastTickAt = time.Time{}
	}
	g.level = snapshot.Level
	g.okStreak = snapshot.OKStreak
	g.tickLevel = snapshot.TickLevel
	g.degradedThisTick = append([]string(nil), snapshot.DegradedThisTick...)
	g.skippedThisTick = append([]string(nil), snapshot.SkippedThisTick...)
	g.systems = systems
	g.order = order
	g.totalSpent.Store(snapshot.TotalSpent)
	return nil
}

type systemSnapshot struct {
	Name            string  `json:"name"`
	Weight          float64 `json:"weight"`
	MinMs           int     `json:"min_ms"`
	Quota           int     `json:"quota"`
	Spent           int     `json:"spent"`
	Borrowed        int     `json:"borrowed"`
	OverrunMs       int     `json:"overrun_ms"`
	Overruns        int     `json:"overruns"`
	StreakBad       int     `json:"streak_bad"`
	StreakOK        int     `json:"streak_ok"`
	Skipped         int     `json:"skipped"`
	SkippedThisTick bool    `json:"skipped_this_tick"`
}

type snapshotPayload struct {
	Version            int              `json:"version"`
	TotalMs            int              `json:"total_ms"`
	TickSeq            int              `json:"tick_seq"`
	TotalQuota         int              `json:"total_quota"`
	Open               bool             `json:"open"`
	RegistrationClosed bool             `json:"registration_closed"`
	HasLastTick        bool             `json:"has_last_tick"`
	LastTickUnixNano   int64            `json:"last_tick_unix_nano"`
	Level              int              `json:"level"`
	OKStreak           int              `json:"ok_streak"`
	TickLevel          int              `json:"tick_level"`
	DegradedThisTick   []string         `json:"degraded_this_tick"`
	SkippedThisTick    []string         `json:"skipped_this_tick"`
	Order              []string         `json:"order"`
	Systems            []systemSnapshot `json:"systems"`
	TotalSpent         int64            `json:"total_spent"`
}

func (g *TickBudgetGovernor) allocateQuotasLocked() (map[string]int, int) {
	quotas := make(map[string]int, len(g.order))
	minSum := 0
	weightSum := 0.0
	for _, name := range g.order {
		state := g.systems[name]
		minSum += state.minMs
		weightSum += state.weight
	}

	freeBudget := g.totalMs - minSum
	if freeBudget < 0 {
		freeBudget = 0
	}

	totalQuota := 0
	for _, name := range g.order {
		state := g.systems[name]
		quota := state.minMs
		if freeBudget > 0 && weightSum > 0 {
			share := math.Floor(float64(freeBudget)*state.weight/weightSum + 1e-9)
			if share > float64(freeBudget) {
				share = float64(freeBudget)
			}
			quota += int(share)
		}
		if quota < 0 {
			quota = 0
		}
		if quota > g.totalMs {
			quota = g.totalMs
		}
		quotas[name] = quota
		totalQuota += quota
	}

	for index := len(g.order) - 1; index >= 0 && totalQuota > g.totalMs; index-- {
		name := g.order[index]
		excess := totalQuota - g.totalMs
		reduction := minInt(quotas[name], excess)
		quotas[name] -= reduction
		totalQuota -= reduction
	}
	if totalQuota > g.totalMs {
		totalQuota = g.totalMs
	}
	return quotas, totalQuota
}

func shouldSkipSystem(level, tick int) bool {
	if level >= maxDegradeLevel {
		return true
	}
	if level <= 0 {
		return false
	}
	interval := level + 1
	return (tick-1)%interval != 0
}

func validateSnapshot(payload *snapshotPayload) error {
	if payload.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, payload.Version)
	}
	if payload.TotalMs < 0 || payload.TickSeq < 0 || payload.TotalQuota < 0 || payload.TotalQuota > payload.TotalMs {
		return fmt.Errorf("%w: invalid budget counters", ErrBadSnapshot)
	}
	if payload.Level < 0 || payload.Level > maxDegradeLevel || payload.TickLevel < 0 || payload.TickLevel > maxDegradeLevel {
		return fmt.Errorf("%w: invalid degrade level", ErrBadSnapshot)
	}
	if payload.OKStreak < 0 || payload.OKStreak > okTicksToRecover {
		return fmt.Errorf("%w: invalid recovery streak", ErrBadSnapshot)
	}
	if payload.TotalSpent < 0 || payload.TotalSpent > int64(payload.TotalMs) || payload.TotalSpent > int64(payload.TotalQuota) {
		return fmt.Errorf("%w: invalid spent total", ErrBadSnapshot)
	}
	if len(payload.Systems) != len(payload.Order) {
		return fmt.Errorf("%w: system order mismatch", ErrBadSnapshot)
	}

	seen := make(map[string]struct{}, len(payload.Systems))
	quotaSum := int64(0)
	spentSum := int64(0)
	for index, system := range payload.Systems {
		if system.Name != payload.Order[index] {
			return fmt.Errorf("%w: system order mismatch", ErrBadSnapshot)
		}
		if _, duplicate := seen[system.Name]; duplicate {
			return fmt.Errorf("%w: duplicate system", ErrBadSnapshot)
		}
		seen[system.Name] = struct{}{}
		if system.Weight <= 0 || math.IsNaN(system.Weight) || math.IsInf(system.Weight, 0) || system.MinMs < 0 {
			return fmt.Errorf("%w: invalid system configuration", ErrBadSnapshot)
		}
		if system.Quota < 0 || system.Quota > payload.TotalMs || system.Spent < 0 || system.Borrowed < 0 || system.OverrunMs < 0 {
			return fmt.Errorf("%w: invalid system budget", ErrBadSnapshot)
		}
		if system.Overruns < 0 || system.StreakBad < 0 || system.StreakOK < 0 || system.Skipped < 0 {
			return fmt.Errorf("%w: invalid system counters", ErrBadSnapshot)
		}
		quotaSum += int64(system.Quota)
		spentSum += int64(system.Spent)
	}
	if quotaSum != int64(payload.TotalQuota) || quotaSum > int64(payload.TotalMs) {
		return fmt.Errorf("%w: quota total mismatch", ErrBadSnapshot)
	}
	if spentSum != payload.TotalSpent {
		return fmt.Errorf("%w: spent total mismatch", ErrBadSnapshot)
	}
	return nil
}

func saturatingAdd(current, delta int) int {
	if delta > 0 && current > math.MaxInt-delta {
		return math.MaxInt
	}
	return current + delta
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
