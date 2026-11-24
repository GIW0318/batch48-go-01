package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-21"
)

func key21(material string, blend impl.BlendMode, page int) impl.BatchKey {
	return impl.BatchKey{Material: material, Blend: blend, AtlasPage: page}
}

func TestEmitRejectsNonPositiveCount(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	if _, err := c.Emit(impl.EmitRequest{Key: key21("smoke", impl.BlendAlpha, 0), Count: 0, Frame: 1}); !errors.Is(err, impl.ErrBadCount) {
		t.Fatalf("count 0: got %v, want ErrBadCount", err)
	}
	if _, err := c.Emit(impl.EmitRequest{Key: key21("smoke", impl.BlendAlpha, 0), Count: -3, Frame: 1}); !errors.Is(err, impl.ErrBadCount) {
		t.Fatalf("count -3: got %v, want ErrBadCount", err)
	}
	if c.ActiveCount() != 0 {
		t.Fatalf("rejected emissions must not create particles, active=%d", c.ActiveCount())
	}
}

func TestEmitReturnsStrictlyIncreasingIDs(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	ids, err := c.Emit(impl.EmitRequest{Key: key21("spark", impl.BlendAdd, 1), Count: 4, Frame: 2})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	if len(ids) != 4 {
		t.Fatalf("expected 4 particle ids, got %d", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ids must increase: ids[%d]=%d is not greater than ids[%d]=%d", i, ids[i], i-1, ids[i-1])
		}
	}
}

func TestResolveReportsLiveSlotRef(t *testing.T) {
	c := impl.NewParticleBatchCompactor(4)
	ids, err := c.Emit(impl.EmitRequest{Key: key21("fire", impl.BlendAdd, 0), Count: 2, Frame: 1})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	ref, ok := c.Resolve(ids[1])
	if !ok {
		t.Fatalf("id %d must resolve while the particle is alive", ids[1])
	}
	if ref.ID != ids[1] {
		t.Fatalf("resolved id = %d, want %d", ref.ID, ids[1])
	}
	if ref.Slot < 0 || ref.Slot >= 4 {
		t.Fatalf("resolved slot = %d, want a value in [0,4)", ref.Slot)
	}
	if ref.Serial < 1 {
		t.Fatalf("resolved serial = %d, want at least 1", ref.Serial)
	}
	if c.ActiveCount() != 2 {
		t.Fatalf("active particles = %d, want 2", c.ActiveCount())
	}
	if _, ok := c.Resolve(424242); ok {
		t.Fatalf("unknown id must not resolve")
	}
}

func TestReleaseFreesSlotAndRejectsSecondRelease(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	if got := c.SlotCount(); got != 8 {
		t.Fatalf("free slots before emission = %d, want 8", got)
	}
	ids, err := c.Emit(impl.EmitRequest{Key: key21("dust", impl.BlendAlpha, 2), Count: 2, Frame: 1})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	if got := c.SlotCount(); got != 6 {
		t.Fatalf("free slots after emitting 2 = %d, want 6", got)
	}
	if err := c.Release(ids[:1]); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if got := c.SlotCount(); got != 7 {
		t.Fatalf("free slots after releasing 1 = %d, want 7", got)
	}
	if c.ActiveCount() != 1 {
		t.Fatalf("active particles = %d, want 1", c.ActiveCount())
	}
	if err := c.Release(ids[:1]); !errors.Is(err, impl.ErrUnknownID) {
		t.Fatalf("second release: got %v, want ErrUnknownID", err)
	}
}

func TestStaleReleaseDoesNotFreeReusedParticle(t *testing.T) {
	c := impl.NewParticleBatchCompactor(4)
	first, err := c.Emit(impl.EmitRequest{Key: key21("ember", impl.BlendAdd, 0), Count: 1, Frame: 1})
	if err != nil {
		t.Fatalf("first emit failed: %v", err)
	}
	stale := first[0]
	if err := c.Release([]int{stale}); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	second, err := c.Emit(impl.EmitRequest{Key: key21("ember", impl.BlendAdd, 0), Count: 1, Frame: 1})
	if err != nil {
		t.Fatalf("second emit failed: %v", err)
	}
	if second[0] == stale {
		t.Fatalf("new particle reused stale id %d", stale)
	}
	if err := c.Release([]int{stale}); !errors.Is(err, impl.ErrUnknownID) {
		t.Fatalf("stale release: got %v, want ErrUnknownID", err)
	}
	if c.ActiveCount() != 1 {
		t.Fatalf("stale release must not free the new particle, active=%d", c.ActiveCount())
	}
	if _, ok := c.Resolve(second[0]); !ok {
		t.Fatalf("new particle %d must still resolve", second[0])
	}
}

func TestBudgetCapsEmissionPerFrame(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	if err := c.SetBudget(impl.Budget{MaxPerFrame: 2, MaxQueued: 4, MaxSlots: 8}); err != nil {
		t.Fatalf("set budget failed: %v", err)
	}
	ids, err := c.Emit(impl.EmitRequest{Key: key21("blast", impl.BlendAdd, 0), Count: 5, Frame: 3})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("emitted %d particles on a 2 particle frame budget, want 2", len(ids))
	}
	if got := c.QueueDepth(); got != 3 {
		t.Fatalf("queue depth = %d, want the 3 over budget particles queued", got)
	}
	if c.ActiveCount() != 2 {
		t.Fatalf("active particles = %d, want 2", c.ActiveCount())
	}
}

func TestQueuedEmissionDrainsOnLaterFrames(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	if err := c.SetBudget(impl.Budget{MaxPerFrame: 2, MaxQueued: 8, MaxSlots: 8}); err != nil {
		t.Fatalf("set budget failed: %v", err)
	}
	if _, err := c.Emit(impl.EmitRequest{Key: key21("trail", impl.BlendMultiply, 1), Count: 5, Frame: 3}); err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	same, err := c.DrainQueue(3)
	if err != nil {
		t.Fatalf("drain same frame failed: %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("drained %d particles on an already exhausted frame, want 0", len(same))
	}
	next, err := c.DrainQueue(4)
	if err != nil {
		t.Fatalf("drain next frame failed: %v", err)
	}
	if len(next) != 2 {
		t.Fatalf("drained %d particles on the next frame, want 2", len(next))
	}
	if got := c.QueueDepth(); got != 1 {
		t.Fatalf("queue depth after draining = %d, want 1", got)
	}
	if c.ActiveCount() != 4 {
		t.Fatalf("active particles = %d, want 4", c.ActiveCount())
	}
}

func TestCompactMergesRepeatedKey(t *testing.T) {
	c := impl.NewParticleBatchCompactor(16)
	k := key21("glass", impl.BlendAdd, 3)
	if _, err := c.Emit(impl.EmitRequest{Key: k, Count: 2, Frame: 1}); err != nil {
		t.Fatalf("first emit failed: %v", err)
	}
	if _, err := c.Emit(impl.EmitRequest{Key: k, Count: 3, Frame: 1}); err != nil {
		t.Fatalf("second emit failed: %v", err)
	}
	batches, err := c.Compact()
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("compact produced %d batches for one key, want 1", len(batches))
	}
	if batches[0].Count != 5 {
		t.Fatalf("batch count = %d, want 5", batches[0].Count)
	}
	if len(batches[0].IDs) != 5 {
		t.Fatalf("batch holds %d ids, want 5", len(batches[0].IDs))
	}
	if c.BatchCount() != 1 {
		t.Fatalf("batch count accessor = %d, want 1", c.BatchCount())
	}
}

func TestCompactOrdersBatchesByKey(t *testing.T) {
	c := impl.NewParticleBatchCompactor(16)
	iron := key21("iron", impl.BlendAdd, 1)
	woodAdd := key21("wood", impl.BlendAdd, 0)
	woodMul := key21("wood", impl.BlendMultiply, 0)
	for _, k := range []impl.BatchKey{iron, woodMul, woodAdd} {
		if _, err := c.Emit(impl.EmitRequest{Key: k, Count: 1, Frame: 1}); err != nil {
			t.Fatalf("emit %v failed: %v", k, err)
		}
	}
	batches, err := c.Compact()
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(batches) != 3 {
		t.Fatalf("compact produced %d batches, want 3", len(batches))
	}
	want := []impl.BatchKey{woodAdd, woodMul, iron}
	for i, w := range want {
		if batches[i].Key != w {
			t.Fatalf("batch %d key = %+v, want %+v", i, batches[i].Key, w)
		}
	}
}

func TestCompactKeepsIDsAscending(t *testing.T) {
	c := impl.NewParticleBatchCompactor(16)
	k := key21("plasma", impl.BlendAdd, 2)
	first, err := c.Emit(impl.EmitRequest{Key: k, Count: 2, Frame: 1})
	if err != nil {
		t.Fatalf("first emit failed: %v", err)
	}
	second, err := c.Emit(impl.EmitRequest{Key: k, Count: 2, Frame: 1})
	if err != nil {
		t.Fatalf("second emit failed: %v", err)
	}
	batches, err := c.Compact()
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("batch count = %d, want 1", len(batches))
	}
	want := []int{first[0], first[1], second[0], second[1]}
	if len(batches[0].IDs) != len(want) {
		t.Fatalf("batch holds %d ids, want %d", len(batches[0].IDs), len(want))
	}
	for i, w := range want {
		if batches[0].IDs[i] != w {
			t.Fatalf("batch id %d = %d, want %d", i, batches[0].IDs[i], w)
		}
	}
}

func TestCompactEnforcesBatchLimit(t *testing.T) {
	c := impl.NewParticleBatchCompactor(16)
	if err := c.SetBatchLimit(1); err != nil {
		t.Fatalf("set batch limit failed: %v", err)
	}
	if _, err := c.Emit(impl.EmitRequest{Key: key21("a", impl.BlendAlpha, 0), Count: 1, Frame: 1}); err != nil {
		t.Fatalf("emit a failed: %v", err)
	}
	if _, err := c.Emit(impl.EmitRequest{Key: key21("b", impl.BlendAlpha, 0), Count: 1, Frame: 1}); err != nil {
		t.Fatalf("emit b failed: %v", err)
	}
	if _, err := c.Compact(); !errors.Is(err, impl.ErrBatchLimit) {
		t.Fatalf("compact with 2 keys above a limit of 1: got %v, want ErrBatchLimit", err)
	}
	if err := c.SetBatchLimit(0); !errors.Is(err, impl.ErrBadBatchLimit) {
		t.Fatalf("limit 0: got %v, want ErrBadBatchLimit", err)
	}
}

func TestCompactRebuildsAfterFullRelease(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	old, err := c.Emit(impl.EmitRequest{Key: key21("old", impl.BlendAlpha, 0), Count: 2, Frame: 1})
	if err != nil {
		t.Fatalf("emit old failed: %v", err)
	}
	if err := c.Release(old); err != nil {
		t.Fatalf("release old failed: %v", err)
	}
	fresh := key21("fresh", impl.BlendMultiply, 4)
	if _, err := c.Emit(impl.EmitRequest{Key: fresh, Count: 1, Frame: 2}); err != nil {
		t.Fatalf("emit fresh failed: %v", err)
	}
	batches, err := c.Compact()
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("compact produced %d batches after full release, want 1", len(batches))
	}
	if batches[0].Key != fresh {
		t.Fatalf("batch key = %+v, want %+v", batches[0].Key, fresh)
	}
	if batches[0].Count != 1 {
		t.Fatalf("batch count = %d, want 1", batches[0].Count)
	}
}

func TestBudgetShrinkBelowActiveRejected(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	if _, err := c.Emit(impl.EmitRequest{Key: key21("ash", impl.BlendAlpha, 1), Count: 4, Frame: 1}); err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	err := c.SetBudget(impl.Budget{MaxPerFrame: 8, MaxQueued: 8, MaxSlots: 2})
	if !errors.Is(err, impl.ErrBudgetTooSmall) {
		t.Fatalf("shrinking max slots to 2 with 4 live particles: got %v, want ErrBudgetTooSmall", err)
	}
	if got := c.Budget().MaxSlots; got != 8 {
		t.Fatalf("rejected budget must not be installed, MaxSlots=%d", got)
	}
}

func TestSetBudgetRejectsNegativeValues(t *testing.T) {
	c := impl.NewParticleBatchCompactor(8)
	err := c.SetBudget(impl.Budget{MaxPerFrame: -1, MaxQueued: 4, MaxSlots: 8})
	if !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("negative frame budget: got %v, want ErrBadBudget", err)
	}
	err = c.SetBudget(impl.Budget{MaxPerFrame: 4, MaxQueued: -2, MaxSlots: 8})
	if !errors.Is(err, impl.ErrBadBudget) {
		t.Fatalf("negative queue budget: got %v, want ErrBadBudget", err)
	}
}

func TestConcurrentEmitNeverDuplicateIDs(t *testing.T) {
	c := impl.NewParticleBatchCompactor(4096)
	var mu sync.Mutex
	var ids []int
	var failures int
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				got, err := c.Emit(impl.EmitRequest{Key: key21("spark", impl.BlendAdd, 0), Count: 1, Frame: 1})
				mu.Lock()
				if err != nil {
					failures++
				} else {
					ids = append(ids, got...)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("%d concurrent emissions failed", failures)
	}
	if len(ids) != 160 {
		t.Fatalf("collected %d ids from 160 emissions", len(ids))
	}
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("id %d handed out twice under concurrency", id)
		}
		seen[id] = true
	}
	if c.ActiveCount() != 160 {
		t.Fatalf("active particles = %d, want 160", c.ActiveCount())
	}
}
