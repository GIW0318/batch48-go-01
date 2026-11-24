// Package task arbitrates CPU quotas and burst credit between tenants.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the guard.
var (
	ErrUnknownTenant = errors.New("tenant is not tracked")
	ErrBadShare      = errors.New("share weight is out of range")
	ErrBadQuota      = errors.New("quota or period is out of range")
	ErrBadBorrow     = errors.New("borrow request is not positive")
	ErrTenantLimit   = errors.New("tenant limit reached")
	ErrLedgerCorrupt = errors.New("quota ledger is corrupt")
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

// CpuBurstGuard owns the per tenant ledger.
type CpuBurstGuard struct {
	opts Options
	mu   sync.Mutex
}

// NewCpuBurstGuard builds a guard with the given policy.
func NewCpuBurstGuard(opts Options) *CpuBurstGuard {
	return &CpuBurstGuard{opts: opts}
}

// SetShare assigns a weight and recomputes proportional quotas.
func (g *CpuBurstGuard) SetShare(tenant string, share int) error {
	return errNotImplemented
}

// SetQuota fixes an explicit period and quota for a tenant.
func (g *CpuBurstGuard) SetQuota(tenant string, periodMs, quotaMs int) error {
	return errNotImplemented
}

// Throttled records one throttle event for a tenant.
func (g *CpuBurstGuard) Throttled(tenant string, at int64) error {
	return errNotImplemented
}

// Borrow grants up to the idle capacity and the borrow ceiling.
func (g *CpuBurstGuard) Borrow(tenant string, ms int) (int, error) {
	return 0, errNotImplemented
}

// Usage returns the accounting row for one tenant.
func (g *CpuBurstGuard) Usage(tenant string) (Usage, error) {
	return Usage{}, errNotImplemented
}

// Rebalance recomputes all quotas from the stored share weights.
func (g *CpuBurstGuard) Rebalance() (map[string]int, error) {
	return nil, errNotImplemented
}

// Snapshot serialises every tenant row.
func (g *CpuBurstGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces every tenant row from a snapshot.
func (g *CpuBurstGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Tenants lists tracked tenants in stable name order.
func (g *CpuBurstGuard) Tenants() []string {
	return nil
}

// Idle reports the capacity not assigned as quota or borrowed credit.
func (g *CpuBurstGuard) Idle() int {
	return 0
}

// Borrowed reports the burst credit currently held by one tenant.
func (g *CpuBurstGuard) Borrowed(tenant string) (int, error) {
	return 0, errNotImplemented
}
