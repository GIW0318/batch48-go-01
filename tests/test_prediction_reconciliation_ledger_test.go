package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-a-12"
)

func newLedger() *impl.PredictionLedger {
	return impl.NewPredictionLedger(64)
}

func almost(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

func mustRecord(t *testing.T, l *impl.PredictionLedger, frame uint64, dx float64) {
	t.Helper()
	if err := l.Record(frame, impl.Input{DX: dx}); err != nil {
		t.Fatalf("record frame %d: %v", frame, err)
	}
}

func TestRecordRejectsZeroFrame(t *testing.T) {
	l := newLedger()
	err := l.Record(0, impl.Input{DX: 1})
	if !errors.Is(err, impl.ErrInvalidFrame) {
		t.Fatalf("record with frame 0: got %v, want ErrInvalidFrame", err)
	}
	if got := l.Pending(); got != 0 {
		t.Fatalf("pending after rejected record = %d, want 0", got)
	}
}

func TestRecordRejectsAckedFrame(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 1, 1)
	if err := l.Acknowledge(1, impl.State{X: 1}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	err := l.Record(1, impl.Input{DX: 7})
	if !errors.Is(err, impl.ErrAckedFrame) {
		t.Fatalf("record of confirmed frame: got %v, want ErrAckedFrame", err)
	}
}

func TestRecordRejectsDuplicateFrame(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 5, 1)
	err := l.Record(5, impl.Input{DX: 2})
	if !errors.Is(err, impl.ErrDuplicateFrame) {
		t.Fatalf("duplicate record: got %v, want ErrDuplicateFrame", err)
	}
	if got := l.Pending(); got != 1 {
		t.Fatalf("pending after duplicate = %d, want 1", got)
	}
}

func TestPendingCountsUnconfirmedInputs(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 1, 1)
	mustRecord(t, l, 2, 1)
	mustRecord(t, l, 3, 1)
	if got := l.Pending(); got != 3 {
		t.Fatalf("pending = %d, want 3", got)
	}
	if got := l.LastAcked(); got != 0 {
		t.Fatalf("last acked = %d before any server state, want 0", got)
	}
}

func TestReplayAppliesPendingInputs(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 1, 5)
	mustRecord(t, l, 2, 3)
	st, frames, err := l.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if frames != 2 {
		t.Fatalf("replayed frames = %d, want 2", frames)
	}
	if !almost(st.X, 8) {
		t.Fatalf("replayed x = %v, want 8 from 5+3", st.X)
	}
}

func TestReplayCountsFramesAfterAck(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 1, 1)
	mustRecord(t, l, 2, 2)
	mustRecord(t, l, 3, 3)
	if err := l.Acknowledge(1, impl.State{X: 1}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	st, frames, err := l.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if frames != 2 {
		t.Fatalf("replayed frames = %d, want 2 remaining frames", frames)
	}
	if !almost(st.X, 6) {
		t.Fatalf("replayed x = %v, want 6 from the confirmed base 1 plus 2 and 3", st.X)
	}
}

func TestAcknowledgeRemovesConfirmedInputs(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 1, 1)
	mustRecord(t, l, 2, 2)
	mustRecord(t, l, 3, 3)
	if err := l.Acknowledge(1, impl.State{X: 1}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	if got := l.Pending(); got != 2 {
		t.Fatalf("pending after ack = %d, want 2", got)
	}
	if got := l.LastAcked(); got != 1 {
		t.Fatalf("last acked = %d, want 1", got)
	}
}

func TestAcknowledgeSameFrameIsIdempotent(t *testing.T) {
	l := newLedger()
	mustRecord(t, l, 1, 1)
	mustRecord(t, l, 2, 2)
	mustRecord(t, l, 3, 3)
	if err := l.Acknowledge(1, impl.State{X: 1}); err != nil {
		t.Fatalf("first acknowledge: %v", err)
	}
	if err := l.Acknowledge(1, impl.State{X: 50}); err != nil {
		t.Fatalf("repeated acknowledge must not fail, got %v", err)
	}
	st, frames, err := l.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if frames != 2 || !almost(st.X, 6) {
		t.Fatalf("after repeated ack got x=%v frames=%d, want the unchanged 6 and 2", st.X, frames)
	}
	if got := l.LastAcked(); got != 1 {
		t.Fatalf("last acked = %d, want 1", got)
	}
}

func TestAcknowledgeSnapsOnLargeDivergence(t *testing.T) {
	l := newLedger()
	l.SetSmoothing(0.5)
	l.SetDivergenceThreshold(10)
	mustRecord(t, l, 1, 100)
	if err := l.Acknowledge(1, impl.State{X: 0}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	st, frames, err := l.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if frames != 0 {
		t.Fatalf("replayed frames = %d, want 0", frames)
	}
	if !almost(st.X, 0) {
		t.Fatalf("x = %v after a divergence above the threshold, want a hard snap to 0", st.X)
	}
}

func TestAcknowledgeSmoothsSmallDivergence(t *testing.T) {
	l := newLedger()
	l.SetSmoothing(0.5)
	l.SetDivergenceThreshold(10)
	mustRecord(t, l, 1, 8)
	if err := l.Acknowledge(1, impl.State{X: 0}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	st, _, err := l.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !almost(st.X, 4) {
		t.Fatalf("x = %v after smoothing half of an 8 unit error, want 4", st.X)
	}
}

func TestSmoothingConvergesAcrossAcks(t *testing.T) {
	l := newLedger()
	l.SetSmoothing(0.5)
	l.SetDivergenceThreshold(10)
	mustRecord(t, l, 1, 8)
	if err := l.Acknowledge(1, impl.State{X: 0}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	want := []float64{4, 2, 1}
	for i := 0; i < 3; i++ {
		st, _, err := l.Replay()
		if err != nil {
			t.Fatalf("replay round %d: %v", i, err)
		}
		if !almost(st.X, want[i]) {
			t.Fatalf("round %d: x = %v, want %v", i, st.X, want[i])
		}
		next := uint64(i + 2)
		mustRecord(t, l, next, 0)
		if err := l.Acknowledge(next, impl.State{X: 0}); err != nil {
			t.Fatalf("acknowledge frame %d: %v", next, err)
		}
	}
}

func TestSetSmoothingClampsOutOfRange(t *testing.T) {
	l := newLedger()
	l.SetSmoothing(2.5)
	if got := l.Smoothing(); !almost(got, 1) {
		t.Fatalf("smoothing = %v after setting 2.5, want it clamped to 1", got)
	}
	l.SetSmoothing(-0.25)
	if got := l.Smoothing(); !almost(got, 0) {
		t.Fatalf("smoothing = %v after setting -0.25, want it clamped to 0", got)
	}
}

func TestDivergenceThresholdControlsSnapping(t *testing.T) {
	smooth := newLedger()
	smooth.SetSmoothing(0.2)
	smooth.SetDivergenceThreshold(100)
	mustRecord(t, smooth, 1, 50)
	if err := smooth.Acknowledge(1, impl.State{X: 0}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	st, _, err := smooth.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !almost(st.X, 40) {
		t.Fatalf("x = %v below a 100 unit threshold, want the smoothed 40", st.X)
	}

	snap := newLedger()
	snap.SetSmoothing(0.2)
	snap.SetDivergenceThreshold(10)
	mustRecord(t, snap, 1, 50)
	if err := snap.Acknowledge(1, impl.State{X: 0}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	st, _, err = snap.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !almost(st.X, 0) {
		t.Fatalf("x = %v above a 10 unit threshold, want the snapped 0", st.X)
	}
}

func TestReplayWindowOverflowDropsHistory(t *testing.T) {
	l := impl.NewPredictionLedger(3)
	mustRecord(t, l, 1, 1)
	mustRecord(t, l, 2, 1)
	mustRecord(t, l, 3, 1)
	err := l.Record(4, impl.Input{DX: 1})
	if !errors.Is(err, impl.ErrReplayWindowOverflow) {
		t.Fatalf("record beyond the replay window: got %v, want ErrReplayWindowOverflow", err)
	}
	if got := l.Pending(); got != 0 {
		t.Fatalf("pending after overflow = %d, want 0 because the history is dropped", got)
	}
	st, frames, err := l.Replay()
	if err != nil {
		t.Fatalf("replay after overflow: %v", err)
	}
	if frames != 0 || !almost(st.X, 0) {
		t.Fatalf("after overflow got x=%v frames=%d, want 0 and 0", st.X, frames)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	l := newLedger()
	l.SetSmoothing(0.25)
	l.SetDivergenceThreshold(12)
	if err := l.Record(1, impl.Input{DX: 5}); err != nil {
		t.Fatalf("record frame 1: %v", err)
	}
	if err := l.Acknowledge(1, impl.State{X: 5}); err != nil {
		t.Fatalf("acknowledge frame 1: %v", err)
	}
	mustRecord(t, l, 2, 3)
	data, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	mustRecord(t, l, 3, 100)
	if err := l.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := l.Pending(); got != 1 {
		t.Fatalf("pending after restore = %d, want 1", got)
	}
	if got := l.LastAcked(); got != 1 {
		t.Fatalf("last acked after restore = %d, want 1", got)
	}
	if got := l.Smoothing(); !almost(got, 0.25) {
		t.Fatalf("smoothing after restore = %v, want 0.25", got)
	}
	if got := l.DivergenceThreshold(); !almost(got, 12) {
		t.Fatalf("threshold after restore = %v, want 12", got)
	}
	st, frames, err := l.Replay()
	if err != nil {
		t.Fatalf("replay after restore: %v", err)
	}
	if frames != 1 || !almost(st.X, 8) {
		t.Fatalf("after restore got x=%v frames=%d, want 8 and 1", st.X, frames)
	}
	if err := l.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore of garbage: got %v, want ErrBadSnapshot", err)
	}
	if got := l.Pending(); got != 1 {
		t.Fatalf("pending after rejected restore = %d, want the previous 1", got)
	}
}
