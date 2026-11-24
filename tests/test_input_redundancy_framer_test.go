package task

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	impl "gsb/batch48-go-game-a-19"
)

func TestEncodeProducesConfiguredCopies(t *testing.T) {
	f := impl.NewInputRedundancyFramer(3)
	if err := f.AddPlayer(7, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	pkts, err := f.Encode(10, impl.Input{Player: 7, MoveX: 1, MoveY: 2, Actions: 4})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(pkts) != 3 {
		t.Fatalf("packet count = %d, want 3 redundant copies", len(pkts))
	}
	for i, p := range pkts {
		if p.Frame != 10 || p.Copy != i || p.Player != 7 {
			t.Fatalf("packet %d = %+v, want frame 10 copy %d player 7", i, p, i)
		}
		if p.Input.MoveX != 1 || p.Input.MoveY != 2 || p.Input.Actions != 4 {
			t.Fatalf("packet %d input = %+v, want the encoded input", i, p.Input)
		}
	}
}

func TestEncodeRejectsUnknownPlayer(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if _, err := f.Encode(10, impl.Input{Player: 7, MoveX: 1}); !errors.Is(err, impl.ErrPlayerUnknown) {
		t.Fatalf("encode unknown player error = %v, want ErrPlayerUnknown", err)
	}
}

func TestSetRedundancyRejectsOutOfRange(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.SetRedundancy(0); !errors.Is(err, impl.ErrInvalidRedundancy) {
		t.Fatalf("zero redundancy error = %v, want ErrInvalidRedundancy", err)
	}
	if err := f.SetRedundancy(9); !errors.Is(err, impl.ErrInvalidRedundancy) {
		t.Fatalf("oversized redundancy error = %v, want ErrInvalidRedundancy", err)
	}
	if err := f.SetRedundancy(4); err != nil {
		t.Fatalf("valid redundancy rejected: %v", err)
	}
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	pkts, err := f.Encode(3, impl.Input{Player: 1})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(pkts) != 4 {
		t.Fatalf("packet count = %d, want 4 after changing redundancy", len(pkts))
	}
}

func TestIngestDeduplicatesSamePacket(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(7, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	p := impl.Packet{Player: 7, Frame: 5, Copy: 0, Input: impl.Input{Player: 7, MoveX: 1}}
	frame, in, ok, err := f.Ingest(p)
	if err != nil || !ok {
		t.Fatalf("first ingest: frame=%d ok=%v err=%v", frame, ok, err)
	}
	if frame != 5 || in.MoveX != 1 {
		t.Fatalf("first ingest result = frame %d input %+v, want frame 5 input move 1", frame, in)
	}
	if _, _, dupOK, dupErr := f.Ingest(p); dupErr != nil || dupOK {
		t.Fatalf("duplicate ingest: ok=%v err=%v, want ignored copy", dupOK, dupErr)
	}
	if got := f.Stats().Duplicates; got != 1 {
		t.Fatalf("duplicates = %d, want 1", got)
	}
}

func TestIngestRejectsConflictingInput(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(7, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 7, Frame: 5, Input: impl.Input{Player: 7, MoveX: 1}}); err != nil || !ok {
		t.Fatalf("first ingest failed: ok=%v err=%v", ok, err)
	}
	if _, _, _, err := f.Ingest(impl.Packet{Player: 7, Frame: 5, Input: impl.Input{Player: 7, MoveX: 2}}); !errors.Is(err, impl.ErrInputConflict) {
		t.Fatalf("conflicting ingest error = %v, want ErrInputConflict", err)
	}
}

func TestIngestRejectsUnknownPlayer(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if _, _, _, err := f.Ingest(impl.Packet{Player: 99, Frame: 3, Input: impl.Input{Player: 99}}); !errors.Is(err, impl.ErrPlayerUnknown) {
		t.Fatalf("ingest unknown player error = %v, want ErrPlayerUnknown", err)
	}
}

func TestReadyRequiresEveryPlayer(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add first player: %v", err)
	}
	if err := f.AddPlayer(2, 0); err != nil {
		t.Fatalf("add second player: %v", err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 2, Frame: 4, Input: impl.Input{Player: 2}}); err != nil || !ok {
		t.Fatalf("seed second player frame 4: ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 1, Frame: 5, Input: impl.Input{Player: 1}}); err != nil || !ok {
		t.Fatalf("ingest first player: ok=%v err=%v", ok, err)
	}
	if f.Ready(5) {
		t.Fatalf("frame 5 became ready before the second player delivered")
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 2, Frame: 5, Input: impl.Input{Player: 2}}); err != nil || !ok {
		t.Fatalf("ingest second player: ok=%v err=%v", ok, err)
	}
	if !f.Ready(5) {
		t.Fatalf("frame 5 must be ready once every player delivered")
	}
}

func TestReadyIsPerFrame(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 1, Frame: 5, Input: impl.Input{Player: 1}}); err != nil || !ok {
		t.Fatalf("ingest frame 5: ok=%v err=%v", ok, err)
	}
	if !f.Ready(5) {
		t.Fatalf("frame 5 must be ready")
	}
	if f.Ready(6) {
		t.Fatalf("frame 6 must not be ready before its input arrived")
	}
}

func TestDropBeforeClearsPendingFrames(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	for _, frame := range []uint64{5, 6} {
		if _, _, ok, err := f.Ingest(impl.Packet{Player: 1, Frame: frame, Input: impl.Input{Player: 1}}); err != nil || !ok {
			t.Fatalf("ingest frame %d: ok=%v err=%v", frame, ok, err)
		}
	}
	if got := f.PendingFrames(1); got != 2 {
		t.Fatalf("pending frames = %d, want 2", got)
	}
	f.DropBefore(7)
	if got := f.PendingFrames(1); got != 0 {
		t.Fatalf("pending frames after drop = %d, want 0", got)
	}
	if got := f.Stats().Pending; got != 0 {
		t.Fatalf("stats pending = %d, want 0", got)
	}
}

func TestPendingFramesCountsDistinctFrames(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	for _, frame := range []uint64{5, 5, 7} {
		if _, _, _, err := f.Ingest(impl.Packet{Player: 1, Frame: frame, Input: impl.Input{Player: 1}}); err != nil {
			t.Fatalf("ingest frame %d: %v", frame, err)
		}
	}
	if got := f.PendingFrames(1); got != 2 {
		t.Fatalf("pending frames = %d, want 2 distinct frames", got)
	}
}

func TestBufferLimitRejectsNewFrame(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.SetBufferLimit(1); err != nil {
		t.Fatalf("set buffer limit: %v", err)
	}
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 1, Frame: 5, Input: impl.Input{Player: 1}}); err != nil || !ok {
		t.Fatalf("ingest first frame: ok=%v err=%v", ok, err)
	}
	if _, _, _, err := f.Ingest(impl.Packet{Player: 1, Frame: 7, Input: impl.Input{Player: 1}}); !errors.Is(err, impl.ErrBufferFull) {
		t.Fatalf("buffer overflow error = %v, want ErrBufferFull", err)
	}
	if got := f.PendingFrames(1); got != 1 {
		t.Fatalf("pending frames = %d, want 1", got)
	}
}

func TestRemovePlayerStopsReadyRequirement(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add first player: %v", err)
	}
	if err := f.AddPlayer(2, 0); err != nil {
		t.Fatalf("add second player: %v", err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 2, Frame: 4, Input: impl.Input{Player: 2}}); err != nil || !ok {
		t.Fatalf("seed second player frame 4: ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 1, Frame: 5, Input: impl.Input{Player: 1}}); err != nil || !ok {
		t.Fatalf("ingest first player: ok=%v err=%v", ok, err)
	}
	if f.Ready(5) {
		t.Fatalf("frame 5 must wait for the second player")
	}
	if err := f.RemovePlayer(2); err != nil {
		t.Fatalf("remove player: %v", err)
	}
	if !f.Ready(5) {
		t.Fatalf("frame 5 must be ready after the missing player left")
	}
	if got := f.Stats().Players; got != 1 {
		t.Fatalf("player count = %d, want 1", got)
	}
}

func TestAddPlayerRejectsDuplicate(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if err := f.AddPlayer(1, 0); !errors.Is(err, impl.ErrPlayerExists) {
		t.Fatalf("duplicate player error = %v, want ErrPlayerExists", err)
	}
}

func TestSnapshotRestoreKeepsPendingInputs(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add first player: %v", err)
	}
	if err := f.AddPlayer(2, 0); err != nil {
		t.Fatalf("add second player: %v", err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 2, Frame: 4, Input: impl.Input{Player: 2}}); err != nil || !ok {
		t.Fatalf("seed second player frame 4: ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := f.Ingest(impl.Packet{Player: 1, Frame: 5, Input: impl.Input{Player: 1}}); err != nil || !ok {
		t.Fatalf("ingest first player: ok=%v err=%v", ok, err)
	}
	payload, err := f.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewInputRedundancyFramer(2)
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.PendingFrames(1); got != 1 {
		t.Fatalf("pending frames after restore = %d, want 1", got)
	}
	if restored.Ready(5) {
		t.Fatalf("restored frame 5 must still wait for the second player")
	}
	if _, _, ok, err := restored.Ingest(impl.Packet{Player: 2, Frame: 5, Input: impl.Input{Player: 2}}); err != nil || !ok {
		t.Fatalf("ingest second player after restore: ok=%v err=%v", ok, err)
	}
	if !restored.Ready(5) {
		t.Fatalf("frame 5 must be ready once both restored players delivered")
	}
	if got := restored.Stats().Players; got != 2 {
		t.Fatalf("player count after restore = %d, want 2", got)
	}
}

func TestConcurrentIngestDeliversOnce(t *testing.T) {
	f := impl.NewInputRedundancyFramer(2)
	if err := f.AddPlayer(1, 0); err != nil {
		t.Fatalf("add player: %v", err)
	}
	p := impl.Packet{Player: 1, Frame: 5, Input: impl.Input{Player: 1, MoveX: 3}}
	var (
		wg        sync.WaitGroup
		successes int32
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, ok, err := f.Ingest(p)
			if err != nil {
				t.Errorf("concurrent ingest failed: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&successes); got != 1 {
		t.Fatalf("successful ingests = %d, want exactly 1", got)
	}
	if got := f.Stats().Duplicates; got != 7 {
		t.Fatalf("duplicates = %d, want 7", got)
	}
}
