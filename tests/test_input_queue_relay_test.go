package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-10"
)

func pushInput(t *testing.T, q *impl.InputQueueRelay, playerID string, frame uint64) impl.PushResult {
	t.Helper()
	res, err := q.Push(playerID, frame, impl.Input{Priority: 1, MoveX: 1, MoveY: 2, Action: "move"})
	if err != nil {
		t.Fatalf("push of frame %d failed: %v", frame, err)
	}
	return res
}

func TestPushAcceptsInOrder(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	res := pushInput(t, q, "p1", 2)
	if !res.Accepted {
		t.Fatalf("first push was not accepted: %+v", res)
	}
	if res.Buffered != 1 {
		t.Fatalf("buffered after first push = %d, want 1", res.Buffered)
	}
	res = pushInput(t, q, "p1", 3)
	if res.Buffered != 2 {
		t.Fatalf("buffered after second push = %d, want 2", res.Buffered)
	}
	if got := q.Pending("p1"); got != 2 {
		t.Fatalf("pending inputs = %d, want 2", got)
	}
}

func TestPushDeduplicatesSameFrame(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	if res := pushInput(t, q, "p1", 5); !res.Accepted {
		t.Fatalf("first push was not accepted: %+v", res)
	}
	res := pushInput(t, q, "p1", 5)
	if res.Accepted || !res.Duplicate {
		t.Fatalf("repeated frame result = %+v, want a rejected duplicate", res)
	}
	if got := q.Pending("p1"); got != 1 {
		t.Fatalf("pending after a duplicate = %d, want the single original input", got)
	}
}

func TestFlushReturnsFramesInOrder(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	pushInput(t, q, "p1", 3)
	pushInput(t, q, "p1", 1)
	pushInput(t, q, "p1", 2)
	got := q.Flush("p1", 10)
	if len(got) != 3 {
		t.Fatalf("flushed %d inputs, want 3", len(got))
	}
	for i, want := range []uint64{1, 2, 3} {
		if got[i].Frame != want {
			t.Fatalf("flushed[%d] frame = %d, want %d", i, got[i].Frame, want)
		}
	}
}

func TestFlushHonorsMax(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	for _, frame := range []uint64{1, 2, 3} {
		pushInput(t, q, "p1", frame)
	}
	first := q.Flush("p1", 2)
	if len(first) != 2 || first[0].Frame != 1 || first[1].Frame != 2 {
		t.Fatalf("first flush = %+v, want frames 1 and 2", first)
	}
	second := q.Flush("p1", 10)
	if len(second) != 1 || second[0].Frame != 3 {
		t.Fatalf("second flush = %+v, want the remaining frame 3", second)
	}
}

func TestFlushEmptyQueue(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	pushInput(t, q, "p1", 1)
	if got := q.Flush("p1", 10); len(got) != 1 {
		t.Fatalf("flush of the single frame returned %d inputs, want 1", len(got))
	}
	if got := q.Flush("p1", 10); len(got) != 0 {
		t.Fatalf("second flush returned %d inputs, want an empty result", len(got))
	}
}

func TestAckRemovesConsumedFrame(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	pushInput(t, q, "p1", 1)
	pushInput(t, q, "p1", 2)
	if err := q.Ack("p1", 1); err != nil {
		t.Fatalf("ack failed: %v", err)
	}
	if got := q.HighestAcked("p1"); got != 1 {
		t.Fatalf("highest acked frame = %d, want 1", got)
	}
	left := q.Flush("p1", 10)
	if len(left) != 1 || left[0].Frame != 2 {
		t.Fatalf("flush after ack = %+v, want only frame 2", left)
	}
}

func TestAckWatermarkNeverRegresses(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	for _, frame := range []uint64{1, 2, 3} {
		pushInput(t, q, "p1", frame)
	}
	if err := q.Ack("p1", 3); err != nil {
		t.Fatalf("ack of frame 3 failed: %v", err)
	}
	if err := q.Ack("p1", 1); err != nil {
		t.Fatalf("late ack of frame 1 returned error: %v", err)
	}
	if got := q.HighestAcked("p1"); got != 3 {
		t.Fatalf("watermark after a late ack = %d, want 3", got)
	}
}

func TestLateFrameBeyondWindowDropped(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	pushInput(t, q, "p1", 100)
	res, err := q.Push("p1", 89, impl.Input{Priority: 1})
	if err != nil {
		t.Fatalf("late push returned error: %v", err)
	}
	if res.Accepted {
		t.Fatalf("frame 89 with a window of 10 and head 100 was accepted")
	}
	if res.Duplicate {
		t.Fatalf("a too late frame must be counted as dropped, not as a duplicate: %+v", res)
	}
	if got := q.Dropped("p1"); got != 1 {
		t.Fatalf("dropped count = %d, want 1", got)
	}
}

func TestLateFrameInsideWindowAccepted(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	pushInput(t, q, "p1", 100)
	res, err := q.Push("p1", 95, impl.Input{Priority: 1})
	if err != nil {
		t.Fatalf("push returned error: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("frame 95 inside the late window was rejected: %+v", res)
	}
	if got := q.Pending("p1"); got != 2 {
		t.Fatalf("pending after an in-window late frame = %d, want 2", got)
	}
	if got := q.Dropped("p1"); got != 0 {
		t.Fatalf("dropped count after an in-window frame = %d, want 0", got)
	}
}

func TestPerPlayerQueuesIsolated(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	pushInput(t, q, "p1", 1)
	pushInput(t, q, "p2", 2)
	one := q.Flush("p1", 10)
	if len(one) != 1 || one[0].PlayerID != "p1" || one[0].Frame != 1 {
		t.Fatalf("flush of p1 = %+v, want only p1 frame 1", one)
	}
	two := q.Flush("p2", 10)
	if len(two) != 1 || two[0].PlayerID != "p2" || two[0].Frame != 2 {
		t.Fatalf("flush of p2 = %+v, want only p2 frame 2", two)
	}
}

func TestQueueCapacityRejectsOverflow(t *testing.T) {
	q := impl.NewInputQueueRelay(2, 10)
	pushInput(t, q, "p1", 1)
	pushInput(t, q, "p1", 2)
	_, err := q.Push("p1", 3, impl.Input{Priority: 1})
	if !errors.Is(err, impl.ErrQueueFull) {
		t.Fatalf("push beyond the queue cap = %v, want ErrQueueFull", err)
	}
	if got := q.Pending("p1"); got != 2 {
		t.Fatalf("pending after a rejected push = %d, want the two old inputs", got)
	}
	left := q.Flush("p1", 10)
	if len(left) != 2 || left[0].Frame != 1 || left[1].Frame != 2 {
		t.Fatalf("flush after overflow = %+v, want the two old frames", left)
	}
}

func TestWindowFollowsConfirmedAndHighest(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	for _, frame := range []uint64{1, 2, 3} {
		pushInput(t, q, "p1", frame)
	}
	if err := q.Ack("p1", 2); err != nil {
		t.Fatalf("ack failed: %v", err)
	}
	confirmed, highest := q.Window()
	if confirmed != 2 || highest != 3 {
		t.Fatalf("window = (%d,%d), want (2,3)", confirmed, highest)
	}
	if got := q.Pending("p1"); got != 1 {
		t.Fatalf("pending after acking frame 2 = %d, want the single frame 3", got)
	}
}

func TestWindowUsesSlowestPlayer(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	for _, frame := range []uint64{1, 2, 3} {
		pushInput(t, q, "p1", frame)
	}
	for _, frame := range []uint64{1, 2} {
		pushInput(t, q, "p2", frame)
	}
	if err := q.Ack("p1", 3); err != nil {
		t.Fatalf("ack of p1 failed: %v", err)
	}
	if err := q.Ack("p2", 1); err != nil {
		t.Fatalf("ack of p2 failed: %v", err)
	}
	confirmed, highest := q.Window()
	if confirmed != 1 || highest != 3 {
		t.Fatalf("window = (%d,%d), want the slowest player to hold the watermark at (1,3)", confirmed, highest)
	}
}

func TestInvalidFrameRejected(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	if _, err := q.Push("p1", 0, impl.Input{Priority: 1}); !errors.Is(err, impl.ErrInvalidFrame) {
		t.Fatalf("push of frame zero = %v, want ErrInvalidFrame", err)
	}
	if _, err := q.Push("", 1, impl.Input{Priority: 1}); !errors.Is(err, impl.ErrInvalidPlayerID) {
		t.Fatalf("push without a player id = %v, want ErrInvalidPlayerID", err)
	}
}

func TestSnapshotRestoreKeepsPendingAndWatermark(t *testing.T) {
	q := impl.NewInputQueueRelay(8, 10)
	for _, frame := range []uint64{1, 2, 3} {
		pushInput(t, q, "p1", frame)
	}
	if got := q.Flush("p1", 2); len(got) != 2 {
		t.Fatalf("first flush returned %d inputs, want 2", len(got))
	}
	if err := q.Ack("p1", 1); err != nil {
		t.Fatalf("ack failed: %v", err)
	}
	data, err := q.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	restored := impl.NewInputQueueRelay(8, 10)
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := restored.HighestAcked("p1"); got != 1 {
		t.Fatalf("restored watermark = %d, want 1", got)
	}
	if got := restored.Pending("p1"); got != 1 {
		t.Fatalf("restored pending = %d, want the single unconfirmed frame", got)
	}
	left := restored.Flush("p1", 10)
	if len(left) != 1 || left[0].Frame != 3 {
		t.Fatalf("flush after restore = %+v, want only frame 3 without replaying delivered frames", left)
	}
	confirmed, highest := restored.Window()
	if confirmed != 1 || highest != 3 {
		t.Fatalf("restored window = (%d,%d), want (1,3)", confirmed, highest)
	}
}
