package task

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-17"
)

func newScheduler(t *testing.T, maxLevels int) *impl.MipStreamScheduler {
	t.Helper()
	s := impl.NewMipStreamScheduler(maxLevels)
	if s == nil {
		t.Fatalf("NewMipStreamScheduler returned nil")
	}
	return s
}

func TestRegisterTextureValidatesDimensions(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 0, 64); !errors.Is(err, impl.ErrBadDimensions) {
		t.Fatalf("zero width: got %v, want ErrBadDimensions", err)
	}
	if err := s.RegisterTexture(1, 64, -1); !errors.Is(err, impl.ErrBadDimensions) {
		t.Fatalf("negative height: got %v, want ErrBadDimensions", err)
	}
	if err := s.RegisterTexture(2, 64, 64); err != nil {
		t.Fatalf("register 64x64: %v", err)
	}
	levels, err := s.TextureLevels(2)
	if err != nil {
		t.Fatalf("levels of 64x64: %v", err)
	}
	if levels != 7 {
		t.Fatalf("64x64 levels = %d, want 7", levels)
	}
	if err := s.RegisterTexture(2, 64, 64); err != nil {
		t.Fatalf("re-register same dimensions: %v", err)
	}
	if err := s.RegisterTexture(2, 128, 64); !errors.Is(err, impl.ErrTextureExists) {
		t.Fatalf("re-register with other dimensions: got %v, want ErrTextureExists", err)
	}
}

func TestRegisterTextureCapsLevels(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.SetMaxLevels(3); err != nil {
		t.Fatalf("set max levels: %v", err)
	}
	if err := s.RegisterTexture(1, 1024, 1024); err != nil {
		t.Fatalf("register 1024x1024: %v", err)
	}
	levels, err := s.TextureLevels(1)
	if err != nil {
		t.Fatalf("levels: %v", err)
	}
	if levels != 3 {
		t.Fatalf("capped levels = %d, want 3", levels)
	}
	bytes, err := s.MipBytes(1, 2)
	if err != nil {
		t.Fatalf("mip bytes: %v", err)
	}
	if bytes != 262144 {
		t.Fatalf("1024x1024 mip2 bytes = %d, want 262144", bytes)
	}
	if err := s.SetMaxLevels(0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("zero max levels: got %v, want ErrBadLimit", err)
	}
	if err := s.SetMaxLevels(-2); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("negative max levels: got %v, want ErrBadLimit", err)
	}
}

func TestMipBytesHalvesUntilOnePixel(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 64, 64); err != nil {
		t.Fatalf("register 64x64: %v", err)
	}
	want := map[int]int{0: 16384, 1: 4096, 2: 1024, 6: 4}
	for mip, size := range want {
		got, err := s.MipBytes(1, mip)
		if err != nil {
			t.Fatalf("mip %d bytes: %v", mip, err)
		}
		if got != size {
			t.Fatalf("mip %d bytes = %d, want %d", mip, got, size)
		}
	}
	if _, err := s.MipBytes(1, 7); !errors.Is(err, impl.ErrBadMip) {
		t.Fatalf("mip beyond chain: got %v, want ErrBadMip", err)
	}
	if _, err := s.MipBytes(99, 0); !errors.Is(err, impl.ErrUnknownTexture) {
		t.Fatalf("unknown texture bytes: got %v, want ErrUnknownTexture", err)
	}
	if err := s.RegisterTexture(2, 8, 2); err != nil {
		t.Fatalf("register 8x2: %v", err)
	}
	if got, _ := s.MipBytes(2, 2); got != 8 {
		t.Fatalf("8x2 mip2 bytes = %d, want 8", got)
	}
}

func TestRequestRejectsUnknownTextureAndBadMip(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.Request(99, 0, 1); !errors.Is(err, impl.ErrUnknownTexture) {
		t.Fatalf("unknown texture request: got %v, want ErrUnknownTexture", err)
	}
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, -1, 1); !errors.Is(err, impl.ErrBadMip) {
		t.Fatalf("negative mip: got %v, want ErrBadMip", err)
	}
	if err := s.Request(1, 6, 1); !errors.Is(err, impl.ErrBadMip) {
		t.Fatalf("mip 6 on a 6 level texture: got %v, want ErrBadMip", err)
	}
	if err := s.Request(1, 5, 1); err != nil {
		t.Fatalf("top mip should be requestable: %v", err)
	}
}

func TestRequestRejectsBadPriority(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, 0, math.NaN()); !errors.Is(err, impl.ErrBadPriority) {
		t.Fatalf("NaN priority: got %v, want ErrBadPriority", err)
	}
	if err := s.Request(1, 0, -0.5); !errors.Is(err, impl.ErrBadPriority) {
		t.Fatalf("negative priority: got %v, want ErrBadPriority", err)
	}
	if err := s.Request(1, 0, math.Inf(1)); !errors.Is(err, impl.ErrBadPriority) {
		t.Fatalf("infinite priority: got %v, want ErrBadPriority", err)
	}
	if err := s.Request(1, 0, 0); err != nil {
		t.Fatalf("zero priority should be accepted: %v", err)
	}
}

func TestFirstMipUploadsWithinBudget(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("request: %v", err)
	}
	ups, err := s.Drain(4096)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("drain returned %d uploads, want 1", len(ups))
	}
	if ups[0].TexID != 1 || ups[0].Mip != 0 || ups[0].Bytes != 4096 {
		t.Fatalf("upload = %+v, want tex 1 mip 0 with 4096 bytes", ups[0])
	}
	if ups[0].Priority != 1 || ups[0].WaitFrames != 0 {
		t.Fatalf("upload priority/wait = %v/%d, want 1/0", ups[0].Priority, ups[0].WaitFrames)
	}
	if got, _ := s.Resident(1); got != 0 {
		t.Fatalf("resident level = %d, want 0", got)
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("pending count = %d, want 0", got)
	}
	if got := s.Frame(); got != 1 {
		t.Fatalf("frame = %d, want 1", got)
	}
	if got := s.Stats().Uploaded; got != 1 {
		t.Fatalf("uploaded counter = %d, want 1", got)
	}
	again, err := s.Drain(4096)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second drain returned %d uploads, want 0", len(again))
	}
}

func TestChildMipWaitsForParent(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, 1, 2); err != nil {
		t.Fatalf("request child mip: %v", err)
	}
	ups, err := s.Drain(1 << 20)
	if err != nil {
		t.Fatalf("drain without parent: %v", err)
	}
	if len(ups) != 0 {
		t.Fatalf("child mip uploaded before its parent: %+v", ups)
	}
	if got, _ := s.Resident(1); got != -1 {
		t.Fatalf("resident level = %d, want -1", got)
	}
	if got := s.PendingCount(); got != 1 {
		t.Fatalf("pending count = %d, want 1", got)
	}

	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("request parent mip: %v", err)
	}
	ups, err = s.Drain(1 << 20)
	if err != nil {
		t.Fatalf("drain with parent: %v", err)
	}
	if len(ups) != 1 || ups[0].Mip != 0 {
		t.Fatalf("drain = %+v, want only mip 0 in the frame its parent arrives", ups)
	}
	ups, err = s.Drain(1 << 20)
	if err != nil {
		t.Fatalf("drain after parent resident: %v", err)
	}
	if len(ups) != 1 || ups[0].Mip != 1 {
		t.Fatalf("drain = %+v, want the pending mip 1", ups)
	}
	if got, _ := s.Resident(1); got != 1 {
		t.Fatalf("resident level = %d, want 1", got)
	}
}

func TestDuplicateRequestMergesToHighestPriority(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := s.Request(1, 0, 5); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if got := s.PendingCount(); got != 1 {
		t.Fatalf("pending count after duplicate request = %d, want 1", got)
	}
	ups, err := s.Drain(4096)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("drain returned %d uploads, want 1", len(ups))
	}
	if ups[0].Priority != 5 {
		t.Fatalf("merged priority = %v, want 5", ups[0].Priority)
	}
}

func TestDrainBudgetIsHardCap(t *testing.T) {
	s := newScheduler(t, 8)
	for _, id := range []int{1, 2} {
		if err := s.RegisterTexture(id, 64, 64); err != nil {
			t.Fatalf("register %d: %v", id, err)
		}
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if err := s.Request(2, 0, 2); err != nil {
		t.Fatalf("request 2: %v", err)
	}
	ups, err := s.Drain(20000)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("drain returned %d uploads under a 20000 byte budget, want 1", len(ups))
	}
	if ups[0].TexID != 2 {
		t.Fatalf("higher priority upload = tex %d, want 2", ups[0].TexID)
	}
	for _, u := range ups {
		if u.Bytes > 20000 {
			t.Fatalf("upload of %d bytes exceeds the budget", u.Bytes)
		}
	}
	if got := s.PendingCount(); got != 1 {
		t.Fatalf("pending count = %d, want 1 deferred request", got)
	}
	ups, err = s.Drain(16384)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(ups) != 1 || ups[0].TexID != 1 {
		t.Fatalf("deferred drain = %+v, want tex 1", ups)
	}
	if _, err := s.Drain(0); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("zero budget: got %v, want ErrBadBudget", err)
	}
	if _, err := s.Drain(-4); !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("negative budget: got %v, want ErrBadBudget", err)
	}
}

func TestAgingLiftsDeferredRequest(t *testing.T) {
	s := newScheduler(t, 8)
	for _, id := range []int{1, 2, 3} {
		if err := s.RegisterTexture(id, 32, 32); err != nil {
			t.Fatalf("register %d: %v", id, err)
		}
	}
	if err := s.Request(1, 0, 1.0); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if err := s.Request(2, 0, 2.0); err != nil {
		t.Fatalf("request 2: %v", err)
	}
	ups, err := s.Drain(4096)
	if err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if len(ups) != 1 || ups[0].TexID != 2 {
		t.Fatalf("first drain = %+v, want tex 2", ups)
	}
	if err := s.Request(3, 0, 1.8); err != nil {
		t.Fatalf("request 3: %v", err)
	}
	ups, err = s.Drain(4096)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(ups) != 1 || ups[0].TexID != 3 {
		t.Fatalf("second drain = %+v, want tex 3 ahead of the still young tex 1", ups)
	}
	ups, err = s.Drain(4096)
	if err != nil {
		t.Fatalf("third drain: %v", err)
	}
	if len(ups) != 1 || ups[0].TexID != 1 {
		t.Fatalf("third drain = %+v, want aged tex 1", ups)
	}
	if ups[0].WaitFrames != 2 {
		t.Fatalf("aged request waited %d frames, want 2", ups[0].WaitFrames)
	}
	if ups[0].Priority != 2.0 {
		t.Fatalf("aged effective priority = %v, want 2.0", ups[0].Priority)
	}
}

func TestCancelRemovesPendingRequest(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := s.Cancel(1, 0); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := s.PendingCount(); got != 0 {
		t.Fatalf("pending count after cancel = %d, want 0", got)
	}
	ups, err := s.Drain(1 << 20)
	if err != nil {
		t.Fatalf("drain after cancel: %v", err)
	}
	if len(ups) != 0 {
		t.Fatalf("cancelled request reappeared in drain: %+v", ups)
	}
	if got := s.Stats().Canceled; got != 1 {
		t.Fatalf("canceled counter = %d, want 1", got)
	}
	if err := s.Cancel(1, 0); !errors.Is(err, impl.ErrNotPending) {
		t.Fatalf("second cancel: got %v, want ErrNotPending", err)
	}
}

func TestCancelRejectsResidentLevel(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := s.Drain(4096); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := s.Cancel(1, 0); !errors.Is(err, impl.ErrAlreadyResident) {
		t.Fatalf("cancel resident mip: got %v, want ErrAlreadyResident", err)
	}
	if got, _ := s.Resident(1); got != 0 {
		t.Fatalf("resident level = %d, want 0", got)
	}
}

func TestResidentLevelNeverDecreases(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.RegisterTexture(1, 32, 32); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got, err := s.Resident(1); err != nil || got != -1 {
		t.Fatalf("initial resident = (%d,%v), want (-1,nil)", got, err)
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("request mip 0: %v", err)
	}
	if _, err := s.Drain(1 << 20); err != nil {
		t.Fatalf("drain mip 0: %v", err)
	}
	if err := s.Request(1, 1, 1); err != nil {
		t.Fatalf("request mip 1: %v", err)
	}
	if _, err := s.Drain(1 << 20); err != nil {
		t.Fatalf("drain mip 1: %v", err)
	}
	if got, _ := s.Resident(1); got != 1 {
		t.Fatalf("resident level = %d, want 1", got)
	}
	if err := s.Request(1, 0, 5); !errors.Is(err, impl.ErrAlreadyResident) {
		t.Fatalf("request of a lower resident mip: got %v, want ErrAlreadyResident", err)
	}
	if err := s.Request(1, 1, 5); !errors.Is(err, impl.ErrAlreadyResident) {
		t.Fatalf("request of the resident mip: got %v, want ErrAlreadyResident", err)
	}
	if got, _ := s.Resident(1); got != 1 {
		t.Fatalf("resident level after rejected requests = %d, want 1", got)
	}
}

func TestRequestQueueLimitEnforced(t *testing.T) {
	s := newScheduler(t, 8)
	if err := s.SetMaxPending(1); err != nil {
		t.Fatalf("set max pending: %v", err)
	}
	if err := s.SetMaxPending(0); !errors.Is(err, impl.ErrBadLimit) {
		t.Fatalf("zero pending limit: got %v, want ErrBadLimit", err)
	}
	if err := s.RegisterTexture(1, 16, 16); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if err := s.RegisterTexture(2, 16, 16); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if err := s.Request(1, 0, 1); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := s.Request(2, 0, 1); !errors.Is(err, impl.ErrQueueFull) {
		t.Fatalf("request beyond queue limit: got %v, want ErrQueueFull", err)
	}
	if got := s.PendingCount(); got != 1 {
		t.Fatalf("pending count = %d, want 1", got)
	}
	if err := s.Cancel(1, 0); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := s.Request(2, 0, 1); err != nil {
		t.Fatalf("request after freeing a queue slot: %v", err)
	}
}

func TestConcurrentRequestsAndDrains(t *testing.T) {
	s := newScheduler(t, 8)
	const textures = 8
	for id := 0; id < textures; id++ {
		if err := s.RegisterTexture(id, 16, 16); err != nil {
			t.Fatalf("register %d: %v", id, err)
		}
	}

	var mu sync.Mutex
	seen := make(map[[2]int]int)
	var wg sync.WaitGroup
	errCh := make(chan error, textures)
	for id := 0; id < textures; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := s.Request(id, 0, float64(1+id%3)); err != nil {
				errCh <- fmt.Errorf("request tex %d: %w", id, err)
				return
			}
			for i := 0; i < 16; i++ {
				ups, err := s.Drain(1 << 20)
				if err != nil {
					errCh <- fmt.Errorf("drain tex %d: %w", id, err)
					return
				}
				if len(ups) > 0 {
					mu.Lock()
					for _, u := range ups {
						seen[[2]int{u.TexID, u.Mip}]++
					}
					mu.Unlock()
				}
			}
		}(id)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent request/drain failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != textures {
		t.Fatalf("observed %d distinct uploads, want %d", len(seen), textures)
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("upload %v was drained %d times, want once", key, count)
		}
	}
	for id := 0; id < textures; id++ {
		if got, _ := s.Resident(id); got != 0 {
			t.Fatalf("texture %d resident level = %d, want 0", id, got)
		}
	}
}
