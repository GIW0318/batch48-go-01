// Package task arbitrates a fixed video memory budget between resource
// categories, borrowing idle quota and evicting by age and priority.
package task

import (
	"errors"
	"sort"
	"sync"
)

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

type allocation struct {
	id        string
	size      Bytes
	category  Category
	handles   map[Handle]struct{}
	pinned    int
	lastUsed  int
	priority  int
	minHandle Handle
}

type handleEntry struct {
	resource Resource
	alloc    *allocation
	pinned   bool
}

// GpuMemoryBudgetArbiter decides which resources stay resident.
type GpuMemoryBudgetArbiter struct {
	mu sync.Mutex

	total   Bytes
	quotas  map[Category]Bytes
	current int

	nextHandle Handle
	handles    map[Handle]*handleEntry
	names      map[string]Handle
	allocByID  map[string]*allocation

	used        Bytes
	pinnedBytes Bytes
	byCategory  map[Category]Bytes
}

var allCategories = [...]Category{CatTexture, CatBuffer, CatTarget}

func validCategory(c Category) bool {
	switch c {
	case CatTexture, CatBuffer, CatTarget:
		return true
	default:
		return false
	}
}

// NewGpuMemoryBudgetArbiter builds an arbiter with a total byte budget.
func NewGpuMemoryBudgetArbiter(total Bytes) *GpuMemoryBudgetArbiter {
	if total < 0 {
		total = 0
	}
	share := Bytes(0)
	if total > 0 {
		share = total / 3
	}
	byCategory := make(map[Category]Bytes, len(allCategories))
	for _, c := range allCategories {
		byCategory[c] = 0
	}
	return &GpuMemoryBudgetArbiter{
		total: total,
		quotas: map[Category]Bytes{
			CatTexture: share,
			CatBuffer:  share,
			CatTarget:  total - 2*share,
		},
		nextHandle: 1,
		handles:    make(map[Handle]*handleEntry),
		names:      make(map[string]Handle),
		allocByID:  make(map[string]*allocation),
		byCategory: byCategory,
	}
}

// SetTotal changes the total budget without touching resident resources.
func (a *GpuMemoryBudgetArbiter) SetTotal(b Bytes) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if b <= 0 {
		return ErrBadTotal
	}
	a.total = b
	return nil
}

// SetQuota changes the quota of one category.
func (a *GpuMemoryBudgetArbiter) SetQuota(c Category, b Bytes) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validCategory(c) {
		return ErrBadCategory
	}
	if b < 0 {
		return ErrBadQuota
	}
	a.quotas[c] = b
	return nil
}

// Quota returns the quota currently assigned to a category.
func (a *GpuMemoryBudgetArbiter) Quota(c Category) Bytes {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validCategory(c) {
		return 0
	}
	return a.quotas[c]
}

func (a *GpuMemoryBudgetArbiter) borrowableFor(c Category, used Bytes) bool {
	quota := a.quotas[c]
	if used <= quota {
		return true
	}
	over := used - quota
	var slack Bytes
	for _, other := range allCategories {
		if other == c {
			continue
		}
		if a.byCategory[other] < a.quotas[other] {
			slack += a.quotas[other] - a.byCategory[other]
		}
	}
	return over <= slack
}

// Request admits a resource if quota, borrowing or total budget allow it.
func (a *GpuMemoryBudgetArbiter) Request(r Resource) (Handle, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if r.Size <= 0 {
		return 0, ErrBadSize
	}
	if !validCategory(r.Category) {
		return 0, ErrBadCategory
	}
	if r.Name != "" {
		if _, ok := a.names[r.Name]; ok {
			return 0, ErrDuplicateResource
		}
	}

	var alloc *allocation
	if r.AllocationID != "" {
		alloc = a.allocByID[r.AllocationID]
	}

	delta := r.Size
	category := r.Category
	if alloc != nil {
		delta = 0
		category = alloc.category
	}

	if a.used > a.total || delta > a.total-a.used {
		return 0, ErrBudgetExceeded
	}
	if !a.borrowableFor(category, a.byCategory[category]+delta) {
		return 0, ErrQuotaExceeded
	}

	h := a.nextHandle
	a.nextHandle++

	if alloc == nil {
		alloc = &allocation{
			id:        r.AllocationID,
			size:      r.Size,
			category:  r.Category,
			handles:   make(map[Handle]struct{}),
			lastUsed:  a.current,
			priority:  r.Priority,
			minHandle: h,
		}
		if r.AllocationID != "" {
			a.allocByID[r.AllocationID] = alloc
		}
		a.used += r.Size
		a.byCategory[r.Category] += r.Size
	} else {
		alloc.lastUsed = a.current
		if r.Priority < alloc.priority {
			alloc.priority = r.Priority
		}
	}

	alloc.handles[h] = struct{}{}
	a.handles[h] = &handleEntry{resource: r, alloc: alloc}
	if r.Name != "" {
		a.names[r.Name] = h
	}
	return h, nil
}

func (a *GpuMemoryBudgetArbiter) deleteName(h Handle, e *handleEntry) {
	if e.resource.Name == "" {
		return
	}
	if current, ok := a.names[e.resource.Name]; ok && current == h {
		delete(a.names, e.resource.Name)
	}
}

// Release drops a resident resource and frees its budget.
func (a *GpuMemoryBudgetArbiter) Release(h Handle) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	e, ok := a.handles[h]
	if !ok {
		return ErrUnknownHandle
	}
	a.releaseLocked(h, e)
	return nil
}

func (a *GpuMemoryBudgetArbiter) releaseLocked(h Handle, e *handleEntry) {
	alloc := e.alloc
	a.deleteName(h, e)
	if e.pinned {
		e.pinned = false
		alloc.pinned--
		if alloc.pinned == 0 {
			a.pinnedBytes -= alloc.size
		}
	}
	delete(alloc.handles, h)
	delete(a.handles, h)

	if len(alloc.handles) != 0 {
		if alloc.minHandle == h {
			first := true
			for candidate := range alloc.handles {
				if first || candidate < alloc.minHandle {
					alloc.minHandle = candidate
					first = false
				}
			}
		}
		return
	}

	a.used -= alloc.size
	a.byCategory[alloc.category] -= alloc.size
	if alloc.id != "" {
		if current, ok := a.allocByID[alloc.id]; ok && current == alloc {
			delete(a.allocByID, alloc.id)
		}
	}
}

// Pin protects a resource from eviction.
func (a *GpuMemoryBudgetArbiter) Pin(h Handle) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	e, ok := a.handles[h]
	if !ok {
		return ErrUnknownHandle
	}
	if e.pinned {
		return ErrAlreadyPinned
	}
	e.pinned = true
	e.alloc.pinned++
	if e.alloc.pinned == 1 {
		a.pinnedBytes += e.alloc.size
	}
	return nil
}

// Unpin removes the eviction protection of a resource.
func (a *GpuMemoryBudgetArbiter) Unpin(h Handle) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	e, ok := a.handles[h]
	if !ok {
		return ErrUnknownHandle
	}
	if !e.pinned {
		return ErrNotPinned
	}
	e.pinned = false
	e.alloc.pinned--
	if e.alloc.pinned == 0 {
		a.pinnedBytes -= e.alloc.size
	}
	return nil
}

// Touch marks a resource as used on the current frame.
func (a *GpuMemoryBudgetArbiter) Touch(h Handle) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	e, ok := a.handles[h]
	if !ok {
		return ErrUnknownHandle
	}
	e.alloc.lastUsed = a.current
	return nil
}

// AdvanceFrame moves the aging clock forward.
func (a *GpuMemoryBudgetArbiter) AdvanceFrame(frame int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if frame < a.current {
		return ErrFrameRegression
	}
	a.current = frame
	return nil
}

type evictionCandidate struct {
	alloc    *allocation
	lastUsed int
	priority int
	handle   Handle
}

// Evict frees at least need bytes and returns the evicted handles in order.
func (a *GpuMemoryBudgetArbiter) Evict(need Bytes) ([]Handle, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if need <= 0 {
		return nil, nil
	}

	seen := make(map[*allocation]struct{})
	candidates := make([]evictionCandidate, 0)
	for _, e := range a.handles {
		alloc := e.alloc
		if _, ok := seen[alloc]; ok {
			continue
		}
		seen[alloc] = struct{}{}
		if alloc.pinned != 0 {
			continue
		}
		candidates = append(candidates, evictionCandidate{
			alloc:    alloc,
			lastUsed: alloc.lastUsed,
			priority: alloc.priority,
			handle:   alloc.minHandle,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.lastUsed != right.lastUsed {
			return left.lastUsed < right.lastUsed
		}
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		return left.handle < right.handle
	})

	var freed Bytes
	selected := make([]*allocation, 0, len(candidates))
	for _, candidate := range candidates {
		selected = append(selected, candidate.alloc)
		freed += candidate.alloc.size
		if freed >= need {
			break
		}
	}
	if freed < need {
		return nil, ErrAllPinned
	}

	evicted := make([]Handle, 0, len(selected))
	for _, alloc := range selected {
		handles := make([]Handle, 0, len(alloc.handles))
		for h := range alloc.handles {
			handles = append(handles, h)
		}
		sort.Slice(handles, func(i, j int) bool { return handles[i] < handles[j] })

		for _, h := range handles {
			e := a.handles[h]
			a.deleteName(h, e)
			delete(a.handles, h)
		}
		a.used -= alloc.size
		a.byCategory[alloc.category] -= alloc.size
		if alloc.id != "" {
			if current, ok := a.allocByID[alloc.id]; ok && current == alloc {
				delete(a.allocByID, alloc.id)
			}
		}
		evicted = append(evicted, handles...)
	}
	return evicted, nil
}

// Usage returns a snapshot of the budget.
func (a *GpuMemoryBudgetArbiter) Usage() Usage {
	a.mu.Lock()
	defer a.mu.Unlock()

	u := Usage{
		Total:      a.total,
		Used:       a.used,
		Pinned:     a.pinnedBytes,
		Handles:    len(a.handles),
		ByCategory: make(map[Category]Bytes, len(allCategories)),
	}
	for _, c := range allCategories {
		used := a.byCategory[c]
		u.ByCategory[c] = used
		if used > a.quotas[c] {
			u.Borrowed += used - a.quotas[c]
		}
	}
	return u
}

// HandleCount reports how many resources are resident.
func (a *GpuMemoryBudgetArbiter) HandleCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.handles)
}

// Resolve returns the resource behind a live handle.
func (a *GpuMemoryBudgetArbiter) Resolve(h Handle) (Resource, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.handles[h]
	if !ok {
		return Resource{}, false
	}
	return e.resource, true
}
