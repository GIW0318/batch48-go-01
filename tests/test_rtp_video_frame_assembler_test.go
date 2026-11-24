package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-c-01"
)

func c01Limits() impl.FrameLimits {
	return impl.FrameLimits{MaxPackets: 64, MaxFrameSpan: 5000, MaxFragments: 8, ReorderWindow: 16}
}

func c01Packet(ssrc uint32, seq uint16, ts uint32, fragIndex, fragCount int, marker bool, payload string, keyframe bool) impl.RTPPacket {
	return impl.RTPPacket{
		SSRC:      ssrc,
		Seq:       seq,
		Timestamp: ts,
		Marker:    marker,
		FragIndex: fragIndex,
		FragCount: fragCount,
		Payload:   []byte(payload),
		Keyframe:  keyframe,
	}
}

func TestPushRejectsMalformedPackets(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(1, 1, 100, 0, 1, true, "", false)); !errors.Is(err, impl.ErrMalformedPacket) {
		t.Fatalf("empty payload error = %v, want ErrMalformedPacket", err)
	}
	if err := a.Push(c01Packet(1, 2, 100, 0, 0, true, "x", false)); !errors.Is(err, impl.ErrMalformedPacket) {
		t.Fatalf("zero fragment count error = %v, want ErrMalformedPacket", err)
	}
	if err := a.Push(c01Packet(1, 3, 100, 2, 2, true, "x", false)); !errors.Is(err, impl.ErrMalformedPacket) {
		t.Fatalf("fragment index out of range error = %v, want ErrMalformedPacket", err)
	}
	if got := a.Stats().Rejected; got != 3 {
		t.Fatalf("rejected packets = %d, want 3", got)
	}
	if got := a.Pending(1); got != 0 {
		t.Fatalf("malformed packets must not be buffered, pending = %d", got)
	}
}

func TestFrameDeliveredOnlyAfterMarker(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(7, 100, 9000, 0, 3, false, "AAA", true)); err != nil {
		t.Fatalf("push first fragment: %v", err)
	}
	if err := a.Push(c01Packet(7, 101, 9000, 1, 3, false, "BBB", true)); err != nil {
		t.Fatalf("push second fragment: %v", err)
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("fragments without marker must not be delivered")
	}
	if err := a.Push(c01Packet(7, 102, 9000, 2, 3, true, "CCC", true)); err != nil {
		t.Fatalf("push marking fragment: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok {
		t.Fatalf("marked frame must be delivered")
	}
	if f.Timestamp != 9000 || f.Fragments != 3 {
		t.Fatalf("frame timestamp=%d fragments=%d, want 9000 and 3", f.Timestamp, f.Fragments)
	}
	if string(f.Payload) != "AAABBBCCC" {
		t.Fatalf("payload = %q, want fragments joined in order", string(f.Payload))
	}
	if !f.Keyframe {
		t.Fatalf("keyframe flag was lost during reassembly")
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("a delivered frame must be removed from the buffer")
	}
}

func TestCompleteFragmentsWithoutMarkerStayBuffered(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	for i := 0; i < 3; i++ {
		if err := a.Push(c01Packet(2, uint16(10+i), 4000, i, 3, false, "zz", false)); err != nil {
			t.Fatalf("push fragment %d: %v", i, err)
		}
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("a frame without marker must not be delivered even when all fragments arrived")
	}
	if got := a.Pending(2); got != 3 {
		t.Fatalf("pending packets = %d, want 3 waiting for the marker", got)
	}
	if got := a.Stats().Completed; got != 0 {
		t.Fatalf("completed frames = %d, want 0", got)
	}
}

func TestReorderInsideWindowReassembles(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(3, 102, 7000, 2, 3, true, "ccc", false)); err != nil {
		t.Fatalf("push marker fragment first: %v", err)
	}
	if err := a.Push(c01Packet(3, 100, 7000, 0, 3, false, "aaa", false)); err != nil {
		t.Fatalf("push late first fragment: %v", err)
	}
	if err := a.Push(c01Packet(3, 101, 7000, 1, 3, false, "bbb", false)); err != nil {
		t.Fatalf("push late middle fragment: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok {
		t.Fatalf("out of order fragments must still complete the frame")
	}
	if string(f.Payload) != "aaabbbccc" {
		t.Fatalf("payload = %q, want fragments joined by fragment index", string(f.Payload))
	}
	st := a.Stats()
	if st.Reordered != 2 {
		t.Fatalf("reordered packets = %d, want 2", st.Reordered)
	}
	if st.Completed != 1 {
		t.Fatalf("completed frames = %d, want 1", st.Completed)
	}
}

func TestDuplicateFragmentCountedOnce(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	p := c01Packet(4, 200, 3000, 0, 3, false, "111", false)
	if err := a.Push(p); err != nil {
		t.Fatalf("push fragment: %v", err)
	}
	if err := a.Push(p); err != nil {
		t.Fatalf("repeat fragment: %v", err)
	}
	if err := a.Push(c01Packet(4, 201, 3000, 1, 3, false, "222", false)); err != nil {
		t.Fatalf("push fragment: %v", err)
	}
	if err := a.Push(c01Packet(4, 202, 3000, 2, 3, true, "333", false)); err != nil {
		t.Fatalf("push marker fragment: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok {
		t.Fatalf("frame must complete once all fragments arrived")
	}
	if string(f.Payload) != "111222333" {
		t.Fatalf("payload = %q, want each fragment exactly once", string(f.Payload))
	}
	if got := a.Stats().Duplicates; got != 1 {
		t.Fatalf("duplicates = %d, want 1", got)
	}
	if got := a.Stats().Completed; got != 1 {
		t.Fatalf("completed frames = %d, want 1", got)
	}
}

func TestMissingFragmentDropsWholeFrame(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(3, 10, 1000, 0, 3, false, "a", false)); err != nil {
		t.Fatalf("push first fragment: %v", err)
	}
	if err := a.Push(c01Packet(3, 12, 1000, 2, 3, true, "c", false)); err != nil {
		t.Fatalf("push last fragment: %v", err)
	}
	if err := a.Push(c01Packet(3, 20, 2000, 0, 1, true, "K", true)); err != nil {
		t.Fatalf("push newer frame: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok {
		t.Fatalf("the newer complete frame must be delivered")
	}
	if f.Timestamp != 2000 {
		t.Fatalf("delivered timestamp = %d, want 2000; the incomplete frame must not be delivered", f.Timestamp)
	}
	if got := a.Stats().MissingDrops; got != 1 {
		t.Fatalf("missing fragment drops = %d, want 1", got)
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("no second frame is expected")
	}
}

func TestRecoveryWaitsForKeyframe(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(4, 10, 1000, 0, 2, false, "a", false)); err != nil {
		t.Fatalf("push incomplete frame: %v", err)
	}
	if err := a.Push(c01Packet(4, 20, 2000, 0, 1, true, "b", false)); err != nil {
		t.Fatalf("push non key frame: %v", err)
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("a non key frame after a lost frame must not be delivered")
	}
	if err := a.Push(c01Packet(4, 30, 3000, 0, 1, true, "k", true)); err != nil {
		t.Fatalf("push key frame: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok {
		t.Fatalf("delivery must resume at the next key frame")
	}
	if f.Timestamp != 3000 || !f.Keyframe {
		t.Fatalf("resumed frame timestamp=%d keyframe=%v, want 3000 and true", f.Timestamp, f.Keyframe)
	}
	if got := a.Stats().Dropped; got < 1 {
		t.Fatalf("dropped frames = %d, want at least the suppressed non key frame", got)
	}
}

func TestLateFragmentAfterDeliveryIsCounted(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(5, 100, 5000, 0, 1, true, "whole", false)); err != nil {
		t.Fatalf("push frame: %v", err)
	}
	if _, ok := a.PollFrame(); !ok {
		t.Fatalf("complete frame must be delivered")
	}
	if err := a.Push(c01Packet(5, 103, 5000, 2, 3, true, "tail", false)); err != nil {
		t.Fatalf("push late fragment: %v", err)
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("a delivered timestamp must not be reassembled again")
	}
	if got := a.Stats().Late; got != 1 {
		t.Fatalf("late fragments = %d, want 1", got)
	}
	if got := a.Stats().Completed; got != 1 {
		t.Fatalf("completed frames = %d, want 1", got)
	}
}

func TestSetLimitsValidatesAndKeepsOldOnFailure(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	bad := impl.FrameLimits{MaxPackets: 0, MaxFrameSpan: 5000, MaxFragments: 8, ReorderWindow: 16}
	if err := a.SetLimits(bad); !errors.Is(err, impl.ErrLimitsRejected) {
		t.Fatalf("zero packet limit error = %v, want ErrLimitsRejected", err)
	}
	bad = impl.FrameLimits{MaxPackets: 8, MaxFrameSpan: -1, MaxFragments: 8, ReorderWindow: 16}
	if err := a.SetLimits(bad); !errors.Is(err, impl.ErrLimitsRejected) {
		t.Fatalf("negative span error = %v, want ErrLimitsRejected", err)
	}
	good := impl.FrameLimits{MaxPackets: 8, MaxFrameSpan: 4000, MaxFragments: 4, ReorderWindow: 8}
	if err := a.SetLimits(good); err != nil {
		t.Fatalf("valid limits rejected: %v", err)
	}
	if err := a.Push(c01Packet(6, 1, 1000, 0, 1, true, "ok", false)); err != nil {
		t.Fatalf("push under new limits: %v", err)
	}
	if _, ok := a.PollFrame(); !ok {
		t.Fatalf("assembler must keep working after a valid limit change")
	}
}

func TestBufferLimitEvictsOldestFrame(t *testing.T) {
	a := impl.NewFrameAssembler(impl.FrameLimits{MaxPackets: 4, MaxFrameSpan: 100000, MaxFragments: 8, ReorderWindow: 16})
	for _, ts := range []uint32{1000, 2000} {
		if err := a.Push(c01Packet(9, uint16(ts/1000*10), ts, 0, 3, false, "a", false)); err != nil {
			t.Fatalf("push first fragment of ts %d: %v", ts, err)
		}
		if err := a.Push(c01Packet(9, uint16(ts/1000*10+1), ts, 1, 3, false, "b", false)); err != nil {
			t.Fatalf("push second fragment of ts %d: %v", ts, err)
		}
	}
	if err := a.Push(c01Packet(9, 40, 3000, 0, 2, false, "c", false)); err != nil {
		t.Fatalf("push packet beyond the packet limit: %v", err)
	}
	if got := a.Pending(9); got > 4 {
		t.Fatalf("pending packets = %d, want at most the configured 4", got)
	}
	if got := a.Stats().Dropped; got < 1 {
		t.Fatalf("dropped frames = %d, want the oldest frame evicted", got)
	}
	if err := a.Push(c01Packet(9, 50, 4000, 0, 1, true, "fresh", false)); err != nil {
		t.Fatalf("push fresh frame: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok || f.Timestamp != 4000 {
		t.Fatalf("eviction must not break later delivery, got ok=%v", ok)
	}
}

func TestPollFrameOrdersByTimestampThenSSRC(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(2, 10, 5000, 0, 1, true, "late", false)); err != nil {
		t.Fatalf("push stream 2 late frame: %v", err)
	}
	if err := a.Push(c01Packet(1, 10, 4000, 0, 1, true, "first", false)); err != nil {
		t.Fatalf("push stream 1 frame: %v", err)
	}
	if err := a.Push(c01Packet(2, 11, 4000, 0, 1, true, "second", false)); err != nil {
		t.Fatalf("push stream 2 early frame: %v", err)
	}
	want := [][2]uint32{{4000, 1}, {4000, 2}, {5000, 2}}
	for i, w := range want {
		f, ok := a.PollFrame()
		if !ok {
			t.Fatalf("frame %d missing", i)
		}
		if f.Timestamp != w[0] || f.SSRC != w[1] {
			t.Fatalf("frame %d = ts %d ssrc %d, want ts %d ssrc %d", i, f.Timestamp, f.SSRC, w[0], w[1])
		}
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("only three complete frames were pushed")
	}
}

func TestFlushDropsPendingPacketsOfOneStream(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(5, 10, 1000, 0, 2, false, "a", false)); err != nil {
		t.Fatalf("push stream 5: %v", err)
	}
	if err := a.Push(c01Packet(5, 11, 1000, 1, 2, false, "b", false)); err != nil {
		t.Fatalf("push stream 5: %v", err)
	}
	if err := a.Push(c01Packet(6, 10, 1000, 0, 2, false, "c", false)); err != nil {
		t.Fatalf("push stream 6: %v", err)
	}
	if err := a.Push(c01Packet(6, 11, 1000, 1, 2, false, "d", false)); err != nil {
		t.Fatalf("push stream 6: %v", err)
	}
	if got := a.Flush(5); got != 2 {
		t.Fatalf("flushed packets = %d, want 2", got)
	}
	if got := a.Pending(5); got != 0 {
		t.Fatalf("stream 5 pending = %d, want 0 after flush", got)
	}
	if got := a.Pending(6); got != 2 {
		t.Fatalf("stream 6 pending = %d, want the other stream untouched", got)
	}
	if got := a.Stats().Dropped; got < 2 {
		t.Fatalf("dropped frames = %d, want the flushed frame counted", got)
	}
	if err := a.Push(c01Packet(5, 20, 9000, 0, 1, true, "after", true)); err != nil {
		t.Fatalf("push after flush: %v", err)
	}
	f, ok := a.PollFrame()
	if !ok || f.Timestamp != 9000 {
		t.Fatalf("the flushed stream must accept new packets, got ok=%v", ok)
	}
}

func TestSnapshotRestoreKeepsBuffers(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(8, 10, 1000, 0, 3, false, "a", false)); err != nil {
		t.Fatalf("push fragment: %v", err)
	}
	if err := a.Push(c01Packet(8, 11, 1000, 1, 3, false, "b", false)); err != nil {
		t.Fatalf("push fragment: %v", err)
	}
	payload, err := a.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b := impl.NewFrameAssembler(c01Limits())
	if err := b.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := b.Pending(8); got != 2 {
		t.Fatalf("restored pending = %d, want 2", got)
	}
	if got := b.Stats().Pushed; got != a.Stats().Pushed {
		t.Fatalf("restored pushed counter = %d, want %d", got, a.Stats().Pushed)
	}
	if err := b.Push(c01Packet(8, 12, 1000, 2, 3, true, "c", false)); err != nil {
		t.Fatalf("finish restored frame: %v", err)
	}
	f, ok := b.PollFrame()
	if !ok {
		t.Fatalf("frame completed after restore must be delivered")
	}
	if string(f.Payload) != "abc" {
		t.Fatalf("restored payload = %q, want fragments preserved across the snapshot", string(f.Payload))
	}
	if err := b.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrSnapshotCorrupt) {
		t.Fatalf("bad snapshot error = %v, want ErrSnapshotCorrupt", err)
	}
	if got := b.Pending(8); got != 0 {
		t.Fatalf("bad restore changed the live state, pending = %d", got)
	}
}

func TestConcurrentPushSameSSRC(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = a.Push(c01Packet(11, uint16(300+i), 8000, i, 8, i == 7, "x", false))
		}(i)
	}
	wg.Wait()
	f, ok := a.PollFrame()
	if !ok {
		t.Fatalf("concurrent pushes must produce one complete frame")
	}
	if f.Fragments != 8 {
		t.Fatalf("frame fragments = %d, want 8", f.Fragments)
	}
	if string(f.Payload) != "xxxxxxxx" {
		t.Fatalf("payload = %q, want every fragment exactly once", string(f.Payload))
	}
	st := a.Stats()
	if st.Pushed != 8 || st.Completed != 1 || st.Duplicates != 0 {
		t.Fatalf("stats pushed=%d completed=%d duplicates=%d, want 8, 1 and 0", st.Pushed, st.Completed, st.Duplicates)
	}
}

func TestStatsAfterMixedPackets(t *testing.T) {
	a := impl.NewFrameAssembler(c01Limits())
	if err := a.Push(c01Packet(12, 1, 100, 0, 1, true, "", false)); err == nil {
		t.Fatalf("malformed packet must be rejected")
	}
	first := c01Packet(12, 2, 1000, 0, 1, true, "one", false)
	if err := a.Push(first); err != nil {
		t.Fatalf("push frame A: %v", err)
	}
	if err := a.Push(first); err != nil {
		t.Fatalf("push duplicate: %v", err)
	}
	if err := a.Push(c01Packet(12, 3, 2000, 0, 1, true, "two", false)); err != nil {
		t.Fatalf("push frame B: %v", err)
	}
	if _, ok := a.PollFrame(); !ok {
		t.Fatalf("frame A must be delivered")
	}
	if _, ok := a.PollFrame(); !ok {
		t.Fatalf("frame B must be delivered")
	}
	if _, ok := a.PollFrame(); ok {
		t.Fatalf("no more frames are buffered")
	}
	st := a.Stats()
	if st.Pushed != 3 || st.Rejected != 1 || st.Duplicates != 1 || st.Completed != 2 || st.Buffered != 0 {
		t.Fatalf("stats pushed=%d rejected=%d duplicates=%d completed=%d buffered=%d, want 3,1,1,2,0",
			st.Pushed, st.Rejected, st.Duplicates, st.Completed, st.Buffered)
	}
}
