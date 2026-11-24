package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-03"
)

func replayFrame(tick uint64, key bool) impl.Frame {
	return impl.Frame{
		Tick:    tick,
		Time:    time.Duration(tick) * 100 * time.Millisecond,
		Payload: []byte(fmt.Sprintf("frame-%d", tick)),
		Key:     key,
	}
}

func appendRange(t *testing.T, idx *impl.ReplayIndexer, from, to uint64, keys map[uint64]bool) {
	t.Helper()
	for tick := from; tick <= to; tick++ {
		if err := idx.Append(replayFrame(tick, keys[tick])); err != nil {
			t.Fatalf("append tick %d failed: %v", tick, err)
		}
	}
}

func TestNewIndexerValidatesConfig(t *testing.T) {
	if _, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 0, KeyframeGap: 4, MaxBlocks: 8}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero block size must report ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 0, MaxBlocks: 8}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero keyframe gap must report ErrInvalidConfig, got %v", err)
	}
	if _, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 0}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("zero block cap must report ErrInvalidConfig, got %v", err)
	}
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("valid config must build an indexer, got %v", err)
	}
	if blocks := idx.Blocks(); len(blocks) != 0 {
		t.Fatalf("fresh indexer reported %d blocks, want 0", len(blocks))
	}
}

func TestAppendValidatesFrames(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	if err := idx.Append(impl.Frame{Tick: 1, Time: 100 * time.Millisecond}); !errors.Is(err, impl.ErrEmptyPayload) {
		t.Fatalf("empty payload must report ErrEmptyPayload, got %v", err)
	}
	if err := idx.Append(impl.Frame{Tick: 0, Time: 100 * time.Millisecond, Payload: []byte("x")}); !errors.Is(err, impl.ErrInvalidTick) {
		t.Fatalf("tick zero must report ErrInvalidTick, got %v", err)
	}
	if err := idx.Append(replayFrame(1, true)); err != nil {
		t.Fatalf("first frame must be accepted, got %v", err)
	}
	if err := idx.Append(replayFrame(1, false)); !errors.Is(err, impl.ErrNonMonotonicTick) {
		t.Fatalf("repeated tick must report ErrNonMonotonicTick, got %v", err)
	}
	backwards := impl.Frame{Tick: 2, Time: 50 * time.Millisecond, Payload: []byte("late")}
	if err := idx.Append(backwards); !errors.Is(err, impl.ErrTimeRegression) {
		t.Fatalf("earlier time must report ErrTimeRegression, got %v", err)
	}
}

func TestAppendCommitsAFullBlock(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 3, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 3, map[uint64]bool{1: true})
	blocks := idx.Blocks()
	if len(blocks) != 1 {
		t.Fatalf("three frames with a block size of three produced %d blocks, want 1", len(blocks))
	}
	info := blocks[0]
	if info.ID != 0 || info.StartTick != 1 || info.Count != 3 {
		t.Fatalf("block info = %+v, want id 0 starting at tick 1 with 3 frames", info)
	}
	if info.StartTime != 100*time.Millisecond || info.EndTime != 300*time.Millisecond {
		t.Fatalf("block span = %v..%v, want 100ms..300ms", info.StartTime, info.EndTime)
	}
	if len(info.KeyTicks) != 1 || info.KeyTicks[0] != 1 {
		t.Fatalf("block keyframes = %v, want [1]", info.KeyTicks)
	}
	if info.Checksum == 0 {
		t.Fatalf("committed block must carry a checksum")
	}
	if err := idx.Append(replayFrame(4, false)); err != nil {
		t.Fatalf("append tick 4 must succeed, got %v", err)
	}
	if blocks := idx.Blocks(); len(blocks) != 1 {
		t.Fatalf("a partial block must stay invisible, got %d blocks", len(blocks))
	}
}

func TestFlushCommitsPartialBlockIdempotently(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 2, map[uint64]bool{1: true})
	if blocks := idx.Blocks(); len(blocks) != 0 {
		t.Fatalf("a two frame block must not be visible before flush, got %d blocks", len(blocks))
	}
	if err := idx.Flush(); err != nil {
		t.Fatalf("flush must succeed, got %v", err)
	}
	blocks := idx.Blocks()
	if len(blocks) != 1 || blocks[0].Count != 2 {
		t.Fatalf("flush produced %d blocks, want one block of two frames", len(blocks))
	}
	if err := idx.Flush(); err != nil {
		t.Fatalf("second flush must succeed, got %v", err)
	}
	if blocks := idx.Blocks(); len(blocks) != 1 {
		t.Fatalf("flushing an empty buffer must not add a block, got %d blocks", len(blocks))
	}
	if err := idx.Append(replayFrame(3, false)); err != nil {
		t.Fatalf("append tick 3 must succeed, got %v", err)
	}
	if err := idx.Flush(); err != nil {
		t.Fatalf("flush after append must succeed, got %v", err)
	}
	blocks = idx.Blocks()
	if len(blocks) != 2 || blocks[1].Count != 1 || blocks[1].StartTick != 3 {
		t.Fatalf("second block = %+v, want a single frame starting at tick 3", blocks)
	}
}

func TestSeekSnapsToTheNearestKeyframe(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 8, map[uint64]bool{1: true, 5: true})
	cursor, err := idx.Seek(600 * time.Millisecond)
	if err != nil {
		t.Fatalf("seek must succeed, got %v", err)
	}
	if cursor.Tick != 5 || cursor.BlockID != 1 || cursor.Offset != 0 {
		t.Fatalf("cursor = %+v, want the keyframe at tick 5 in block 1", cursor)
	}
	cursor, err = idx.Seek(200 * time.Millisecond)
	if err != nil {
		t.Fatalf("seek must succeed, got %v", err)
	}
	if cursor.Tick != 1 || cursor.BlockID != 0 || cursor.Offset != 0 {
		t.Fatalf("cursor = %+v, want the keyframe at tick 1 in block 0", cursor)
	}
}

func TestLocateScansForwardFromTheKeyframe(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 5, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 10, map[uint64]bool{1: true, 5: true, 9: true})
	cases := []struct {
		at   time.Duration
		want uint64
	}{
		{100 * time.Millisecond, 1},
		{250 * time.Millisecond, 3},
		{800 * time.Millisecond, 8},
		{950 * time.Millisecond, 10},
	}
	for _, tc := range cases {
		got, err := idx.Locate(tc.at)
		if err != nil {
			t.Fatalf("locate %v failed: %v", tc.at, err)
		}
		if got != tc.want {
			t.Fatalf("locate %v = %d, want tick %d", tc.at, got, tc.want)
		}
	}
}

func TestSeekBeforeTheFirstKeyframeStartsAtTheFirstFrame(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 6, KeyframeGap: 10, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 6, map[uint64]bool{3: true})
	cursor, err := idx.Seek(150 * time.Millisecond)
	if err != nil {
		t.Fatalf("seek before the first keyframe must succeed, got %v", err)
	}
	if cursor.Tick != 1 || cursor.BlockID != 0 || cursor.Offset != 0 {
		t.Fatalf("cursor = %+v, want the first frame of the replay", cursor)
	}
}

func TestReadBlockHonoursTheCursorOffset(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 8, map[uint64]bool{1: true, 5: true})
	frames, err := idx.ReadBlock(impl.Cursor{BlockID: 0, Offset: 0, Tick: 1})
	if err != nil {
		t.Fatalf("read block 0 must succeed, got %v", err)
	}
	if len(frames) != 4 {
		t.Fatalf("block 0 returned %d frames, want 4", len(frames))
	}
	for i, f := range frames {
		if f.Tick != uint64(i+1) {
			t.Fatalf("block 0 frame %d has tick %d, want %d", i, f.Tick, i+1)
		}
	}
	frames, err = idx.ReadBlock(impl.Cursor{BlockID: 1, Offset: 2, Tick: 7})
	if err != nil {
		t.Fatalf("read block 1 from offset 2 must succeed, got %v", err)
	}
	if len(frames) != 2 || frames[0].Tick != 7 || frames[1].Tick != 8 {
		t.Fatalf("block 1 from offset 2 = %+v, want ticks 7 and 8", frames)
	}
	if _, err := idx.ReadBlock(impl.Cursor{BlockID: 1, Offset: 9}); !errors.Is(err, impl.ErrOffsetOutOfRange) {
		t.Fatalf("offset past the block must report ErrOffsetOutOfRange, got %v", err)
	}
	if _, err := idx.ReadBlock(impl.Cursor{BlockID: 42}); !errors.Is(err, impl.ErrBlockNotFound) {
		t.Fatalf("unknown block must report ErrBlockNotFound, got %v", err)
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 4, map[uint64]bool{1: true})
	if err := idx.Verify(0); err != nil {
		t.Fatalf("healthy block must verify, got %v", err)
	}
	if err := idx.InjectCorruption(0); err != nil {
		t.Fatalf("corruption injection must succeed, got %v", err)
	}
	if err := idx.Verify(0); !errors.Is(err, impl.ErrBlockCorrupt) {
		t.Fatalf("damaged block must report ErrBlockCorrupt, got %v", err)
	}
	if err := idx.InjectCorruption(42); !errors.Is(err, impl.ErrBlockNotFound) {
		t.Fatalf("corrupting an unknown block must report ErrBlockNotFound, got %v", err)
	}
	if err := idx.Verify(42); !errors.Is(err, impl.ErrBlockNotFound) {
		t.Fatalf("verifying an unknown block must report ErrBlockNotFound, got %v", err)
	}
}

func TestPruneEvictsTheOldestBlocks(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 2, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 8, map[uint64]bool{1: true, 3: true, 5: true, 7: true})
	if blocks := idx.Blocks(); len(blocks) != 4 {
		t.Fatalf("expected 4 committed blocks, got %d", len(blocks))
	}
	evicted, err := idx.Prune(2)
	if err != nil {
		t.Fatalf("prune must succeed, got %v", err)
	}
	if evicted != 2 {
		t.Fatalf("prune evicted %d blocks, want 2", evicted)
	}
	if blocks := idx.Blocks(); len(blocks) != 2 {
		t.Fatalf("after prune %d blocks remain, want 2", len(blocks))
	}
	evicted, err = idx.Prune(5)
	if err != nil {
		t.Fatalf("prune above the block count must succeed, got %v", err)
	}
	if evicted != 0 {
		t.Fatalf("prune above the block count evicted %d blocks, want 0", evicted)
	}
	if _, err := idx.Prune(0); !errors.Is(err, impl.ErrInvalidKeep) {
		t.Fatalf("zero keep must report ErrInvalidKeep, got %v", err)
	}
	if _, err := idx.Prune(-3); !errors.Is(err, impl.ErrInvalidKeep) {
		t.Fatalf("negative keep must report ErrInvalidKeep, got %v", err)
	}
}

func TestBlockCapDropsTheOldestBlock(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 1, KeyframeGap: 4, MaxBlocks: 3})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 5, map[uint64]bool{1: true, 2: true, 3: true, 4: true, 5: true})
	blocks := idx.Blocks()
	if len(blocks) != 3 {
		t.Fatalf("block cap of three kept %d blocks, want 3", len(blocks))
	}
	if blocks[0].ID != 2 || blocks[2].ID != 4 {
		t.Fatalf("retained block ids = %d..%d, want the newest 2..4", blocks[0].ID, blocks[2].ID)
	}
	if err := idx.Verify(0); !errors.Is(err, impl.ErrBlockNotFound) {
		t.Fatalf("evicted block must report ErrBlockNotFound, got %v", err)
	}
	if err := idx.Verify(4); err != nil {
		t.Fatalf("newest block must verify, got %v", err)
	}
}

func TestSnapshotRestoreContinuesTheOpenBlock(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 3, map[uint64]bool{1: true})
	data, err := idx.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	if blocks := restored.Blocks(); len(blocks) != 0 {
		t.Fatalf("restored open block must stay uncommitted, got %d blocks", len(blocks))
	}
	if err := restored.Append(replayFrame(4, false)); err != nil {
		t.Fatalf("continuing the open block must succeed, got %v", err)
	}
	blocks := restored.Blocks()
	if len(blocks) != 1 || blocks[0].Count != 4 {
		t.Fatalf("continued block = %+v, want one block of four frames", blocks)
	}
	if err := restored.Verify(0); err != nil {
		t.Fatalf("continued block must verify, got %v", err)
	}
	frames, err := restored.ReadBlock(impl.Cursor{BlockID: 0})
	if err != nil {
		t.Fatalf("read continued block must succeed, got %v", err)
	}
	if len(frames) != 4 || frames[3].Tick != 4 {
		t.Fatalf("continued block frames = %+v, want ticks 1..4", frames)
	}
	tick, err := restored.Locate(400 * time.Millisecond)
	if err != nil {
		t.Fatalf("locate after restore must succeed, got %v", err)
	}
	if tick != 4 {
		t.Fatalf("locate 400ms = %d, want tick 4", tick)
	}
}

func TestSnapshotRestoreKeepsBlockMetadata(t *testing.T) {
	cfg := impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8}
	idx, err := impl.NewReplayIndexer(cfg)
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	appendRange(t, idx, 1, 8, map[uint64]bool{1: true, 5: true})
	before := idx.Blocks()
	data, err := idx.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored, err := impl.NewReplayIndexer(cfg)
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	after := restored.Blocks()
	if len(after) != 2 || len(before) != 2 {
		t.Fatalf("block count before/after restore = %d/%d, want 2", len(before), len(after))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].StartTick != before[i].StartTick ||
			after[i].Count != before[i].Count || after[i].Checksum != before[i].Checksum {
			t.Fatalf("block %d metadata changed across restore: %+v vs %+v", i, before[i], after[i])
		}
	}
	for _, info := range after {
		if err := restored.Verify(info.ID); err != nil {
			t.Fatalf("restored block %d must verify, got %v", info.ID, err)
		}
	}
	cursor, err := restored.Seek(700 * time.Millisecond)
	if err != nil {
		t.Fatalf("seek after restore must succeed, got %v", err)
	}
	if cursor.Tick != 5 || cursor.BlockID != 1 {
		t.Fatalf("cursor after restore = %+v, want the keyframe at tick 5", cursor)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
}

func TestSeekRejectsEmptyAndOutOfRange(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	if _, err := idx.Seek(0); !errors.Is(err, impl.ErrNoData) {
		t.Fatalf("seek on an empty indexer must report ErrNoData, got %v", err)
	}
	if _, err := idx.Locate(0); !errors.Is(err, impl.ErrNoData) {
		t.Fatalf("locate on an empty indexer must report ErrNoData, got %v", err)
	}
	appendRange(t, idx, 1, 3, map[uint64]bool{1: true})
	if _, err := idx.Seek(200 * time.Millisecond); !errors.Is(err, impl.ErrNoData) {
		t.Fatalf("an unflushed block must stay invisible to seek, got %v", err)
	}
	if err := idx.Flush(); err != nil {
		t.Fatalf("flush must succeed, got %v", err)
	}
	if _, err := idx.Seek(-time.Millisecond); !errors.Is(err, impl.ErrOutOfRange) {
		t.Fatalf("negative time must report ErrOutOfRange, got %v", err)
	}
	if _, err := idx.Seek(500 * time.Millisecond); !errors.Is(err, impl.ErrOutOfRange) {
		t.Fatalf("time past the replay must report ErrOutOfRange, got %v", err)
	}
	tick, err := idx.Locate(150 * time.Millisecond)
	if err != nil {
		t.Fatalf("locate inside the replay must succeed, got %v", err)
	}
	if tick != 2 {
		t.Fatalf("locate 150ms = %d, want tick 2", tick)
	}
}

func TestConcurrentReadersSeeOnlyCommittedBlocks(t *testing.T) {
	idx, err := impl.NewReplayIndexer(impl.ReplayConfig{BlockFrames: 4, KeyframeGap: 4, MaxBlocks: 8})
	if err != nil {
		t.Fatalf("indexer must build, got %v", err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for round := 0; round < 50; round++ {
				blocks := idx.Blocks()
				for _, info := range blocks {
					_ = idx.Verify(info.ID)
				}
				_, _ = idx.Seek(250 * time.Millisecond)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for tick := uint64(1); tick <= 16; tick++ {
			if appendErr := idx.Append(replayFrame(tick, tick%4 == 1)); appendErr != nil {
				t.Errorf("append tick %d failed: %v", tick, appendErr)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	blocks := idx.Blocks()
	if len(blocks) != 4 {
		t.Fatalf("after 16 frames with a block size of four there are %d blocks, want 4", len(blocks))
	}
	for _, info := range blocks {
		if err := idx.Verify(info.ID); err != nil {
			t.Fatalf("block %d must verify, got %v", info.ID, err)
		}
	}
	tick, err := idx.Locate(1600 * time.Millisecond)
	if err != nil {
		t.Fatalf("locate after the writers finished must succeed, got %v", err)
	}
	if tick != 16 {
		t.Fatalf("locate 1600ms = %d, want tick 16", tick)
	}
}
