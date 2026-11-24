// Package task arbitrates a fixed video memory budget between resource
// categories, borrowing idle quota and evicting by age and priority.
package task

import "errors"

// errNotImplemented marks behaviour that still has to be provided.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the memory budget arbiter.
var (
	ErrBadTotal          = errors.New("total budget must be positive")
	ErrBadQuota          = errors.New("quota must not be negative")
	ErrBadSize           = errors.New("resource size must be positive")
	ErrBadCategory       = errors.New("unknown resource category")
	ErrDuplicateResource = errors.New("a live resource already uses this name")
	ErrUnknownHandle     = errors.New("handle is unknown or already released")
	ErrBudgetExceeded    = errors.New("total budget would be exceeded")
	ErrQuotaExceeded     = errors.New("category quota and borrowable budget are exhausted")
	ErrAllPinned         = errors.New("every eviction candidate is pinned")
	ErrAlreadyPinned     = errors.New("resource is already pinned")
	ErrNotPinned         = errors.New("resource is not pinned")
	ErrFrameRegression   = errors.New("frame must not move backwards")
)

// Category is one class of resident video memory.
type Category int

// Resource categories.
const (
	CatTexture Category = iota
	CatBuffer
	CatTarget
)

// Bytes is a size in bytes.
type Bytes int64

// Handle identifies one resident resource.
type Handle int

// Resource describes one residency request.
type Resource struct {
	Name         string
	Category     Category
	Size         Bytes
	Priority     int
	AllocationID string
}

// Usage is a point in time view of the budget.
type Usage struct {
	Total      Bytes
	Used       Bytes
	Pinned     Bytes
	Handles    int
	Borrowed   Bytes
	ByCategory map[Category]Bytes
}

// GpuMemoryBudgetArbiter decides which resources stay resident.
type GpuMemoryBudgetArbiter struct {
	total   Bytes
	quotas  map[Category]Bytes
	current int
}

// NewGpuMemoryBudgetArbiter builds an arbiter with a total byte budget.
func NewGpuMemoryBudgetArbiter(total Bytes) *GpuMemoryBudgetArbiter {
	share := Bytes(0)
	if total > 0 {
		share = total / 3
	}
	return &GpuMemoryBudgetArbiter{
		total: total,
		quotas: map[Category]Bytes{
			CatTexture: share,
			CatBuffer:  share,
			CatTarget:  total - 2*share,
		},
	}
}

// SetTotal changes the total budget without touching resident resources.
func (a *GpuMemoryBudgetArbiter) SetTotal(b Bytes) error { return errNotImplemented }

// SetQuota changes the quota of one category.
func (a *GpuMemoryBudgetArbiter) SetQuota(c Category, b Bytes) error { return errNotImplemented }

// Quota returns the quota currently assigned to a category.
func (a *GpuMemoryBudgetArbiter) Quota(c Category) Bytes { return 0 }

// Request admits a resource if quota, borrowing or total budget allow it.
func (a *GpuMemoryBudgetArbiter) Request(r Resource) (Handle, error) {
	return 0, errNotImplemented
}

// Release drops a resident resource and frees its budget.
func (a *GpuMemoryBudgetArbiter) Release(h Handle) error { return errNotImplemented }

// Pin protects a resource from eviction.
func (a *GpuMemoryBudgetArbiter) Pin(h Handle) error { return errNotImplemented }

// Unpin removes the eviction protection of a resource.
func (a *GpuMemoryBudgetArbiter) Unpin(h Handle) error { return errNotImplemented }

// Touch marks a resource as used on the current frame.
func (a *GpuMemoryBudgetArbiter) Touch(h Handle) error { return errNotImplemented }

// AdvanceFrame moves the aging clock forward.
func (a *GpuMemoryBudgetArbiter) AdvanceFrame(frame int) error { return errNotImplemented }

// Evict frees at least need bytes and returns the evicted handles in order.
func (a *GpuMemoryBudgetArbiter) Evict(need Bytes) ([]Handle, error) {
	return nil, errNotImplemented
}

// Usage returns a snapshot of the budget.
func (a *GpuMemoryBudgetArbiter) Usage() Usage { return Usage{} }

// HandleCount reports how many resources are resident.
func (a *GpuMemoryBudgetArbiter) HandleCount() int { return 0 }

// Resolve returns the resource behind a live handle.
func (a *GpuMemoryBudgetArbiter) Resolve(h Handle) (Resource, bool) {
	return Resource{}, false
}
