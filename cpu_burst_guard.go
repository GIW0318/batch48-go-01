// Package task arbitrates CPU quotas and burst credit between tenants.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Errors reported by the guard.
var (
	ErrUnknownTenant = errors.New("tenant is not tracked")
	ErrBadShare      = errors.New("share weight is out of range")
	ErrBadQuota      = errors.New("quota or period is out of range")
	ErrBadBorrow     = errors.New("borrow request is not positive")
	ErrTenantLimit   = errors.New("tenant limit reached")
	ErrLedgerCorrupt = errors.New("quota ledger is corrupt")
)

const (
	maxShareValue       = 10000
	snapshotVersion     = 1
	defaultPeriodMs     = 100
	defaultMaxTenants   = 64
	minExpressibleQuota = 1
)

// Usage is the current accounting row for one tenant.
type Usage struct {
	Tenant     string `json:"tenant"`
	PeriodMs   int    `json:"period_ms"`
	QuotaMs    int    `json:"quota_ms"`
	UsedMs     int    `json:"used_ms"`
	BorrowedMs int    `json:"borrowed_ms"`
	Throttled  int    `json:"throttled"`
}

// Options configures the common period, quota floor and borrow ceiling.
type Options struct {
	PeriodMs    int `json:"period_ms"`
	MinQuotaMs  int `json:"min_quota_ms"`
	MaxBorrowMs int `json:"max_borrow_ms"`
	MaxTenants  int `json:"max_tenants"`
}

type tenantLedger struct {
	Name            string `json:"tenant"`
	Share           int    `json:"share"`
	PeriodMs        int    `json:"period_ms"`
	QuotaMs         int    `json:"quota_ms"`
	UsedMs          int    `json:"used_ms"`
	BorrowedMs      int    `json:"borrowed_ms"`
	Throttled       int    `json:"throttled"`
	LastThrottledAt int64  `json:"last_throttled_at"`
}

type snapshotData struct {
	Version int            `json:"version"`
	Options Options        `json:"options"`
	Tenants []tenantLedger `json:"tenants"`
}

// CpuBurstGuard owns the per tenant ledger.
type CpuBurstGuard struct {
	opts         Options
	mu           sync.Mutex
	tenants      map[string]*tenantLedger
	balanceRound uint64
}

// NewCpuBurstGuard builds a guard with the given policy.
func NewCpuBurstGuard(opts Options) *CpuBurstGuard {
	if opts.PeriodMs <= 0 {
		opts.PeriodMs = defaultPeriodMs
	}
	if opts.MinQuotaMs <= 0 {
		opts.MinQuotaMs = minExpressibleQuota
	}
	if opts.MaxBorrowMs < 0 {
		opts.MaxBorrowMs = 0
	}
	if opts.MaxTenants <= 0 {
		opts.MaxTenants = defaultMaxTenants
	}

	return &CpuBurstGuard{
		opts:    opts,
		tenants: make(map[string]*tenantLedger),
	}
}

// SetShare assigns a weight and recomputes proportional quotas.
func (g *CpuBurstGuard) SetShare(tenant string, share int) error {
	if share < 0 || share > maxShareValue {
		return ErrBadShare
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	row, err := g.ensureTenantLocked(tenant)
	if err != nil {
		return err
	}

	row.Share = share
	g.rebalanceLocked()
	return nil
}

// SetQuota fixes an explicit period and quota for a tenant.
func (g *CpuBurstGuard) SetQuota(tenant string, periodMs, quotaMs int) error {
	if periodMs <= 0 || quotaMs < 0 {
		return ErrBadQuota
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	row, err := g.ensureTenantLocked(tenant)
	if err != nil {
		return err
	}

	if quotaMs < row.QuotaMs && row.QuotaMs > 0 && row.BorrowedMs > 0 {
		row.BorrowedMs = row.BorrowedMs * quotaMs / row.QuotaMs
	}
	if row.UsedMs < 0 {
		row.UsedMs = 0
	}

	row.PeriodMs = periodMs
	row.QuotaMs = quotaMs
	return nil
}

// Throttled records one throttle event for a tenant.
func (g *CpuBurstGuard) Throttled(tenant string, at int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	row, ok := g.tenants[tenant]
	if !ok {
		return ErrUnknownTenant
	}

	row.Throttled++
	row.LastThrottledAt = at
	return nil
}

// Borrow grants up to the idle capacity and the borrow ceiling.
func (g *CpuBurstGuard) Borrow(tenant string, ms int) (int, error) {
	if ms <= 0 {
		return 0, ErrBadBorrow
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	row, ok := g.tenants[tenant]
	if !ok {
		return 0, ErrUnknownTenant
	}

	granted := ms
	if idle := g.idleLocked(); granted > idle {
		granted = idle
	}
	if remainingCap := g.opts.MaxBorrowMs - row.BorrowedMs; granted > remainingCap {
		granted = remainingCap
	}
	if granted < 0 {
		granted = 0
	}

	row.BorrowedMs += granted
	return granted, nil
}

// Usage returns the accounting row for one tenant.
func (g *CpuBurstGuard) Usage(tenant string) (Usage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	row, ok := g.tenants[tenant]
	if !ok {
		return Usage{}, ErrUnknownTenant
	}

	return Usage{
		Tenant:     row.Name,
		PeriodMs:   row.PeriodMs,
		QuotaMs:    row.QuotaMs,
		UsedMs:     maxInt(row.UsedMs, 0),
		BorrowedMs: maxInt(row.BorrowedMs, 0),
		Throttled:  row.Throttled,
	}, nil
}

// Rebalance recomputes all quotas from the stored share weights.
func (g *CpuBurstGuard) Rebalance() (map[string]int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.rebalanceLocked(), nil
}

// Snapshot serialises every tenant row.
func (g *CpuBurstGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	names := g.sortedNamesLocked()
	payload := snapshotData{
		Version: snapshotVersion,
		Options: g.opts,
		Tenants: make([]tenantLedger, 0, len(names)),
	}
	for _, name := range names {
		payload.Tenants = append(payload.Tenants, *g.tenants[name])
	}
	g.mu.Unlock()

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("snapshot cpu burst ledger: %w", err)
	}
	return data, nil
}

// Restore replaces every tenant row from a snapshot.
func (g *CpuBurstGuard) Restore(payload []byte) error {
	var snapshot snapshotData
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrLedgerCorrupt, err)
	}
	if snapshot.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrLedgerCorrupt, snapshot.Version)
	}
	if snapshot.Tenants == nil {
		return fmt.Errorf("%w: missing tenants", ErrLedgerCorrupt)
	}
	if len(snapshot.Tenants) > g.opts.MaxTenants {
		return fmt.Errorf("%w: too many tenants", ErrLedgerCorrupt)
	}

	rows := make(map[string]*tenantLedger, len(snapshot.Tenants))
	seen := make(map[string]struct{}, len(snapshot.Tenants))
	borrowedTotal := 0
	for i := range snapshot.Tenants {
		row := snapshot.Tenants[i]
		if row.Name == "" {
			return fmt.Errorf("%w: empty tenant name", ErrLedgerCorrupt)
		}
		if _, duplicate := seen[row.Name]; duplicate {
			return fmt.Errorf("%w: duplicate tenant %q", ErrLedgerCorrupt, row.Name)
		}
		if row.Share < 0 || row.Share > maxShareValue {
			return fmt.Errorf("%w: bad share for %q", ErrLedgerCorrupt, row.Name)
		}
		if row.PeriodMs <= 0 || row.QuotaMs < 0 || row.UsedMs < 0 ||
			row.BorrowedMs < 0 || row.Throttled < 0 {
			return fmt.Errorf("%w: negative or invalid ledger field for %q", ErrLedgerCorrupt, row.Name)
		}
		if row.BorrowedMs > g.opts.MaxBorrowMs {
			return fmt.Errorf("%w: borrow cap exceeded for %q", ErrLedgerCorrupt, row.Name)
		}

		borrowedTotal += row.BorrowedMs
		if borrowedTotal > g.opts.PeriodMs {
			return fmt.Errorf("%w: borrowed total exceeds period", ErrLedgerCorrupt)
		}

		seen[row.Name] = struct{}{}
		rowCopy := row
		rows[row.Name] = &rowCopy
	}

	g.mu.Lock()
	g.tenants = rows
	g.balanceRound = 0
	g.mu.Unlock()
	return nil
}

// Tenants lists tracked tenants in stable name order.
func (g *CpuBurstGuard) Tenants() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sortedNamesLocked()
}

// Idle reports the capacity not assigned as quota or borrowed credit.
func (g *CpuBurstGuard) Idle() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.idleLocked()
}

// Borrowed reports the burst credit currently held by one tenant.
func (g *CpuBurstGuard) Borrowed(tenant string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	row, ok := g.tenants[tenant]
	if !ok {
		return 0, ErrUnknownTenant
	}
	return maxInt(row.BorrowedMs, 0), nil
}

func (g *CpuBurstGuard) ensureTenantLocked(tenant string) (*tenantLedger, error) {
	row, ok := g.tenants[tenant]
	if ok {
		return row, nil
	}
	if len(g.tenants) >= g.opts.MaxTenants {
		return nil, ErrTenantLimit
	}

	row = &tenantLedger{
		Name:     tenant,
		PeriodMs: g.opts.PeriodMs,
	}
	g.tenants[tenant] = row
	return row, nil
}

func (g *CpuBurstGuard) sortedNamesLocked() []string {
	names := make([]string, 0, len(g.tenants))
	for name := range g.tenants {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (g *CpuBurstGuard) idleLocked() int {
	idle := g.opts.PeriodMs
	for _, row := range g.tenants {
		idle -= maxInt(row.QuotaMs, 0)
		idle -= maxInt(row.BorrowedMs, 0)
	}
	return maxInt(idle, 0)
}

func (g *CpuBurstGuard) rebalanceLocked() map[string]int {
	g.balanceRound++
	names := g.sortedNamesLocked()
	quotas := make(map[string]int, len(names))

	borrowedTotal := 0
	for _, name := range names {
		borrowedTotal += maxInt(g.tenants[name].BorrowedMs, 0)
	}

	budget := g.opts.PeriodMs - borrowedTotal
	if budget < 0 {
		budget = 0
	}

	weights := make([]int, len(names))
	totalWeight := 0
	participants := 0
	for i, name := range names {
		row := g.tenants[name]
		quotas[name] = 0
		if row.Share > 0 {
			weights[i] = row.Share
			totalWeight += row.Share
			participants++
		}
	}

	if totalWeight == 0 || budget == 0 {
		for _, name := range names {
			g.tenants[name].QuotaMs = 0
		}
		return quotas
	}

	minQuota := g.opts.MinQuotaMs
	if minQuota < minExpressibleQuota {
		minQuota = minExpressibleQuota
	}
	if participants*minQuota > budget {
		minQuota = minExpressibleQuota
	}

	allocated := make([]int, len(names))
	type remainder struct {
		index int
		value int64
	}
	remainders := make([]remainder, 0, participants)

	for i := range names {
		if weights[i] == 0 {
			continue
		}
		numerator := int64(budget) * int64(weights[i])
		allocated[i] = int(numerator / int64(totalWeight))
		rem := numerator % int64(totalWeight)
		if allocated[i] < minQuota {
			allocated[i] = minQuota
		}
		remainders = append(remainders, remainder{index: i, value: rem})
	}

	sum := 0
	for _, q := range allocated {
		sum += q
	}

	for sum > budget {
		donationIndex := -1
		largest := 0
		for i := range names {
			floor := 0
			if weights[i] > 0 {
				floor = minQuota
			}
			if allocated[i] > floor && allocated[i] > largest {
				largest = allocated[i]
				donationIndex = i
			}
		}
		if donationIndex < 0 {
			break
		}
		allocated[donationIndex]--
		sum--
	}

	if sum > budget {
		allocated = fairFallback(weights, budget)
		sum = budget
	}

	leftover := budget - sum
	if leftover > 0 && len(remainders) > 0 {
		offset := int((g.balanceRound - 1) % uint64(len(names)))
		tieRank := func(index int) int {
			return (index - offset + len(names)) % len(names)
		}
		sort.SliceStable(remainders, func(i, j int) bool {
			if remainders[i].value != remainders[j].value {
				return remainders[i].value > remainders[j].value
			}
			return tieRank(remainders[i].index) < tieRank(remainders[j].index)
		})

		for i := 0; i < leftover && i < len(remainders); i++ {
			allocated[remainders[i].index]++
		}
	}

	for i, name := range names {
		q := allocated[i]
		if q < 0 {
			q = 0
		}
		g.tenants[name].QuotaMs = q
		quotas[name] = q
	}
	return quotas
}

func fairFallback(weights []int, budget int) []int {
	allocated := make([]int, len(weights))
	if budget <= 0 {
		return allocated
	}

	totalWeight := 0
	for _, weight := range weights {
		if weight > 0 {
			totalWeight += weight
		}
	}
	if totalWeight == 0 {
		return allocated
	}

	type remainder struct {
		index int
		value int64
	}
	remainders := make([]remainder, 0, len(weights))
	for i, weight := range weights {
		if weight <= 0 {
			continue
		}
		numerator := int64(budget) * int64(weight)
		allocated[i] = int(numerator / int64(totalWeight))
		remainders = append(remainders, remainder{index: i, value: numerator % int64(totalWeight)})
	}

	sort.SliceStable(remainders, func(i, j int) bool {
		if remainders[i].value != remainders[j].value {
			return remainders[i].value > remainders[j].value
		}
		return remainders[i].index < remainders[j].index
	})

	used := 0
	for _, q := range allocated {
		used += q
	}
	for i := 0; i < budget-used && i < len(remainders); i++ {
		allocated[remainders[i].index]++
	}
	return allocated
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
