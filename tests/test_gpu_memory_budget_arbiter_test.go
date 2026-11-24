package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-b-23"
)

func newArbiter23(t *testing.T, total int64) *impl.GpuMemoryBudgetArbiter {
	t.Helper()
	a := impl.NewGpuMemoryBudgetArbiter(impl.Bytes(total))
	for _, c := range []impl.Category{impl.CatTexture, impl.CatBuffer, impl.CatTarget} {
		if err := a.SetQuota(c, 100); err != nil {
			t.Fatalf("set quota for category %v failed: %v", c, err)
		}
	}
	return a
}

func resource23(name string, c impl.Category, size int64, prio int) impl.Resource {
	return impl.Resource{Name: name, Category: c, Size: impl.Bytes(size), Priority: prio}
}

func TestRequestRejectsNonPositiveSize(t *testing.T) {
	a := impl.NewGpuMemoryBudgetArbiter(300)
	if _, err := a.Request(resource23("zero", impl.CatTexture, 0, 0)); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("size 0: got %v, want ErrBadSize", err)
	}
	if _, err := a.Request(resource23("negative", impl.CatTexture, -16, 0)); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("size -16: got %v, want ErrBadSize", err)
	}
	if got := a.HandleCount(); got != 0 {
		t.Fatalf("rejected requests created %d handles, want 0", got)
	}
}

func TestRequestRejectsUnknownCategory(t *testing.T) {
	a := impl.NewGpuMemoryBudgetArbiter(300)
	_, err := a.Request(resource23("odd", impl.Category(9), 16, 0))
	if !errors.Is(err, impl.ErrBadCategory) {
		t.Fatalf("unknown category: got %v, want ErrBadCategory", err)
	}
}

func TestRequestRejectsDuplicateLiveName(t *testing.T) {
	a := newArbiter23(t, 300)
	first, err := a.Request(resource23("atlas", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if first <= 0 {
		t.Fatalf("handle = %d, want a positive handle", first)
	}
	_, err = a.Request(resource23("atlas", impl.CatTexture, 10, 0))
	if !errors.Is(err, impl.ErrDuplicateResource) {
		t.Fatalf("duplicate name: got %v, want ErrDuplicateResource", err)
	}
}

func TestRequestWithinQuotaCountsAgainstTotal(t *testing.T) {
	a := newArbiter23(t, 300)
	if _, err := a.Request(resource23("atlas", impl.CatTexture, 60, 0)); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	u := a.Usage()
	if u.Total != 300 {
		t.Fatalf("total = %d, want 300", u.Total)
	}
	if u.Used != 60 {
		t.Fatalf("used = %d, want 60", u.Used)
	}
	if u.Handles != 1 {
		t.Fatalf("handles = %d, want 1", u.Handles)
	}
	if u.Pinned != 0 {
		t.Fatalf("pinned = %d, want 0", u.Pinned)
	}
	if u.Borrowed != 0 {
		t.Fatalf("borrowed = %d, want 0", u.Borrowed)
	}
	if got := u.ByCategory[impl.CatTexture]; got != 60 {
		t.Fatalf("texture bytes = %d, want 60", got)
	}
	if got := u.ByCategory[impl.CatBuffer]; got != 0 {
		t.Fatalf("buffer bytes = %d, want 0", got)
	}
}

func TestRequestBorrowsFreeQuotaFromOtherCategory(t *testing.T) {
	a := newArbiter23(t, 300)
	if _, err := a.Request(resource23("atlas", impl.CatTexture, 150, 0)); err != nil {
		t.Fatalf("request above the texture quota but inside idle quota: %v", err)
	}
	u := a.Usage()
	if u.Used != 150 {
		t.Fatalf("used = %d, want 150", u.Used)
	}
	if u.Borrowed != 50 {
		t.Fatalf("borrowed = %d, want 50 bytes above the texture quota", u.Borrowed)
	}
	if got := u.ByCategory[impl.CatTexture]; got != 150 {
		t.Fatalf("texture bytes = %d, want 150", got)
	}
}

func TestRequestRejectsWhenTotalExhausted(t *testing.T) {
	a := newArbiter23(t, 150)
	if _, err := a.Request(resource23("vb", impl.CatBuffer, 80, 0)); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	_, err := a.Request(resource23("albedo", impl.CatTexture, 80, 0))
	if !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("request above the total budget: got %v, want ErrBudgetExceeded", err)
	}
	if got := a.Usage().Used; got != 80 {
		t.Fatalf("used = %d, want the rejected request to leave usage at 80", got)
	}
}

func TestReleaseFreesBudgetAndRejectsUnknownHandle(t *testing.T) {
	a := newArbiter23(t, 300)
	h, err := a.Request(resource23("atlas", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := a.Release(h); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	u := a.Usage()
	if u.Used != 0 || u.Handles != 0 {
		t.Fatalf("after release used=%d handles=%d, want 0 and 0", u.Used, u.Handles)
	}
	if _, ok := a.Resolve(h); ok {
		t.Fatalf("released handle %d must not resolve", h)
	}
	if err := a.Release(h); !errors.Is(err, impl.ErrUnknownHandle) {
		t.Fatalf("second release: got %v, want ErrUnknownHandle", err)
	}
}

func TestPinRejectsSecondPin(t *testing.T) {
	a := newArbiter23(t, 300)
	h, err := a.Request(resource23("shadow", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := a.Pin(h); err != nil {
		t.Fatalf("pin failed: %v", err)
	}
	if got := a.Usage().Pinned; got != 40 {
		t.Fatalf("pinned bytes = %d, want 40", got)
	}
	if err := a.Pin(h); !errors.Is(err, impl.ErrAlreadyPinned) {
		t.Fatalf("second pin: got %v, want ErrAlreadyPinned", err)
	}
	if got := a.Usage().Pinned; got != 40 {
		t.Fatalf("pinned bytes after duplicate pin = %d, want 40", got)
	}
}

func TestUnpinRejectsUnpinnedHandle(t *testing.T) {
	a := newArbiter23(t, 300)
	h, err := a.Request(resource23("lut", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if err := a.Unpin(h); !errors.Is(err, impl.ErrNotPinned) {
		t.Fatalf("unpin without pin: got %v, want ErrNotPinned", err)
	}
}

func TestEvictSkipsPinnedResources(t *testing.T) {
	a := newArbiter23(t, 300)
	pinned, err := a.Request(resource23("shadow", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("request pinned failed: %v", err)
	}
	loose, err := a.Request(resource23("vb", impl.CatBuffer, 40, 0))
	if err != nil {
		t.Fatalf("request loose failed: %v", err)
	}
	if err := a.Pin(pinned); err != nil {
		t.Fatalf("pin failed: %v", err)
	}
	evicted, err := a.Evict(40)
	if err != nil {
		t.Fatalf("evict failed: %v", err)
	}
	if len(evicted) != 1 || evicted[0] != loose {
		t.Fatalf("evicted = %v, want only handle %d", evicted, loose)
	}
	if _, ok := a.Resolve(pinned); !ok {
		t.Fatalf("pinned handle %d must stay resident", pinned)
	}
	if _, ok := a.Resolve(loose); ok {
		t.Fatalf("evicted handle %d must be gone", loose)
	}
	if got := a.Usage().Used; got != 40 {
		t.Fatalf("used after eviction = %d, want 40", got)
	}
}

func TestEvictFailsAtomicallyWhenAllPinned(t *testing.T) {
	a := newArbiter23(t, 300)
	h1, err := a.Request(resource23("shadow", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("request h1 failed: %v", err)
	}
	h2, err := a.Request(resource23("vb", impl.CatBuffer, 40, 0))
	if err != nil {
		t.Fatalf("request h2 failed: %v", err)
	}
	if err := a.Pin(h1); err != nil {
		t.Fatalf("pin h1 failed: %v", err)
	}
	if err := a.Pin(h2); err != nil {
		t.Fatalf("pin h2 failed: %v", err)
	}
	if _, err := a.Evict(10); !errors.Is(err, impl.ErrAllPinned) {
		t.Fatalf("evicting pinned only resources: got %v, want ErrAllPinned", err)
	}
	if got := a.Usage().Used; got != 80 {
		t.Fatalf("failed eviction changed usage to %d, want 80", got)
	}
	if _, ok := a.Resolve(h1); !ok {
		t.Fatalf("h1 must survive a failed eviction")
	}
	if _, ok := a.Resolve(h2); !ok {
		t.Fatalf("h2 must survive a failed eviction")
	}
}

func TestEvictOrdersByAgeThenHandle(t *testing.T) {
	a := newArbiter23(t, 300)
	h1, err := a.Request(resource23("old", impl.CatTexture, 40, 0))
	if err != nil {
		t.Fatalf("request h1 failed: %v", err)
	}
	h2, err := a.Request(resource23("older", impl.CatBuffer, 40, 0))
	if err != nil {
		t.Fatalf("request h2 failed: %v", err)
	}
	if err := a.AdvanceFrame(5); err != nil {
		t.Fatalf("advance frame failed: %v", err)
	}
	h3, err := a.Request(resource23("fresh", impl.CatTarget, 40, 0))
	if err != nil {
		t.Fatalf("request h3 failed: %v", err)
	}
	if err := a.AdvanceFrame(6); err != nil {
		t.Fatalf("advance frame failed: %v", err)
	}
	if err := a.Touch(h1); err != nil {
		t.Fatalf("touch h1 failed: %v", err)
	}
	evicted, err := a.Evict(120)
	if err != nil {
		t.Fatalf("evict failed: %v", err)
	}
	want := []impl.Handle{h2, h3, h1}
	if len(evicted) != len(want) {
		t.Fatalf("evicted %d handles, want %d", len(evicted), len(want))
	}
	for i := range want {
		if evicted[i] != want[i] {
			t.Fatalf("eviction order[%d] = %d, want %d (oldest last-used frame first)", i, evicted[i], want[i])
		}
	}
	if got := a.Usage().Used; got != 0 {
		t.Fatalf("used after evicting everything = %d, want 0", got)
	}
}

func TestEvictPrefersLowerPriority(t *testing.T) {
	a := newArbiter23(t, 300)
	high, err := a.Request(resource23("hero", impl.CatTexture, 40, 5))
	if err != nil {
		t.Fatalf("request high failed: %v", err)
	}
	low, err := a.Request(resource23("prop", impl.CatBuffer, 40, 0))
	if err != nil {
		t.Fatalf("request low failed: %v", err)
	}
	evicted, err := a.Evict(40)
	if err != nil {
		t.Fatalf("evict failed: %v", err)
	}
	if len(evicted) != 1 || evicted[0] != low {
		t.Fatalf("evicted = %v, want the lower priority handle %d", evicted, low)
	}
	if _, ok := a.Resolve(high); !ok {
		t.Fatalf("higher priority handle %d must stay resident", high)
	}
}

func TestAdvanceFrameRejectsRegression(t *testing.T) {
	a := newArbiter23(t, 300)
	if err := a.AdvanceFrame(3); err != nil {
		t.Fatalf("advance to frame 3 failed: %v", err)
	}
	if err := a.AdvanceFrame(2); !errors.Is(err, impl.ErrFrameRegression) {
		t.Fatalf("frame 2 after frame 3: got %v, want ErrFrameRegression", err)
	}
}

func TestSetTotalBelowUsedRejectsNewRequests(t *testing.T) {
	a := newArbiter23(t, 300)
	if _, err := a.Request(resource23("vb", impl.CatBuffer, 80, 0)); err != nil {
		t.Fatalf("request vb failed: %v", err)
	}
	if _, err := a.Request(resource23("albedo", impl.CatTexture, 80, 0)); err != nil {
		t.Fatalf("request albedo failed: %v", err)
	}
	if err := a.SetTotal(150); err != nil {
		t.Fatalf("shrinking the total budget below current usage must succeed: %v", err)
	}
	if got := a.Usage().Total; got != 150 {
		t.Fatalf("total after shrink = %d, want 150", got)
	}
	_, err := a.Request(resource23("probe", impl.CatTarget, 10, 0))
	if !errors.Is(err, impl.ErrBudgetExceeded) {
		t.Fatalf("request after budget shrink: got %v, want ErrBudgetExceeded", err)
	}
	if got := a.Usage().Used; got != 160 {
		t.Fatalf("used after rejected request = %d, want the resident 160", got)
	}
}
