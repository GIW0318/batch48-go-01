package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-23"
)

func newWarden() *impl.ReplayWarden {
	return impl.NewReplayWarden(impl.DefaultLimits())
}

func acceptOK(t *testing.T, w *impl.ReplayWarden, epoch uint32, seq uint64, now time.Time) impl.Verdict {
	t.Helper()
	v, err := w.Accept(epoch, seq, now)
	if err != nil {
		t.Fatalf("accept epoch %d seq %d: %v", epoch, seq, err)
	}
	return v
}

func TestFirstDatagramEstablishesWindow(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)

	if got := acceptOK(t, w, 0, 500, t0); got != impl.VerdictAccept {
		t.Fatalf("first datagram verdict = %v, want accept", got)
	}
	highest, ok := w.Highest(0)
	if !ok || highest != 500 {
		t.Fatalf("highest = %d/%v, want 500/true", highest, ok)
	}
	if got := w.Accepted(); got != 1 {
		t.Fatalf("accepted counter = %d, want 1", got)
	}
	if got := acceptOK(t, w, 0, 501, t0); got != impl.VerdictAccept {
		t.Fatalf("next datagram verdict = %v, want accept", got)
	}
	if highest, _ := w.Highest(0); highest != 501 {
		t.Fatalf("highest after advancing = %d, want 501", highest)
	}
}

func TestDuplicateSequenceIsRejectedAndCounted(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 100, t0)

	got := acceptOK(t, w, 0, 100, t0)
	if got != impl.VerdictDuplicate {
		t.Fatalf("repeated sequence verdict = %v, want duplicate", got)
	}
	if w.Duplicates() != 1 {
		t.Fatalf("duplicate counter = %d, want 1", w.Duplicates())
	}
	if w.Accepted() != 1 {
		t.Fatalf("accepted counter = %d, want 1", w.Accepted())
	}
}

func TestOutOfOrderWithinWindowIsAcceptedOnce(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 100, t0)

	for _, seq := range []uint64{95, 97, 83} {
		if got := acceptOK(t, w, 0, seq, t0); got != impl.VerdictAccept {
			t.Fatalf("out of order seq %d verdict = %v, want accept", seq, got)
		}
	}
	if got := acceptOK(t, w, 0, 95, t0); got != impl.VerdictDuplicate {
		t.Fatalf("repeated seq 95 verdict = %v, want duplicate", got)
	}
	if got := acceptOK(t, w, 0, 36, t0); got != impl.VerdictTooOld {
		t.Fatalf("seq 36 at 64 behind verdict = %v, want too old", got)
	}
	if got := acceptOK(t, w, 0, 37, t0); got != impl.VerdictAccept {
		t.Fatalf("seq 37 at 63 behind verdict = %v, want accept", got)
	}
}

func TestWindowShiftsWhenHighestJumps(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 10, t0)
	if got := acceptOK(t, w, 0, 70, t0); got != impl.VerdictAccept {
		t.Fatalf("jump to 70 verdict = %v, want accept", got)
	}
	if got := acceptOK(t, w, 0, 7, t0); got != impl.VerdictAccept {
		t.Fatalf("seq 7 at 63 behind verdict = %v, want accept", got)
	}
	if got := acceptOK(t, w, 0, 6, t0); got != impl.VerdictTooOld {
		t.Fatalf("seq 6 at 64 behind verdict = %v, want too old", got)
	}
	if highest, _ := w.Highest(0); highest != 70 {
		t.Fatalf("highest = %d, want 70", highest)
	}
}

func TestFutureEpochIsRejected(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	if err := w.AdvanceEpoch(4); err != nil {
		t.Fatalf("advance to epoch 4: %v", err)
	}
	if _, err := w.Accept(5, 1, t0); !errors.Is(err, impl.ErrFutureEpoch) {
		t.Fatalf("future epoch = %v, want ErrFutureEpoch", err)
	}
	if got := acceptOK(t, w, 4, 1, t0); got != impl.VerdictAccept {
		t.Fatalf("current epoch verdict = %v, want accept", got)
	}
	if got := w.CurrentEpoch(); got != 4 {
		t.Fatalf("current epoch = %d, want 4", got)
	}
}

func TestAdvanceEpochMustIncrease(t *testing.T) {
	w := newWarden()
	if err := w.AdvanceEpoch(2); err != nil {
		t.Fatalf("advance to epoch 2: %v", err)
	}
	if err := w.AdvanceEpoch(2); !errors.Is(err, impl.ErrBadEpoch) {
		t.Fatalf("same epoch = %v, want ErrBadEpoch", err)
	}
	if err := w.AdvanceEpoch(1); !errors.Is(err, impl.ErrBadEpoch) {
		t.Fatalf("older epoch = %v, want ErrBadEpoch", err)
	}
	if got := w.CurrentEpoch(); got != 2 {
		t.Fatalf("current epoch after rejected advances = %d, want 2", got)
	}
}

func TestFarAheadSequenceEntersResyncAndStashes(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	limit := impl.DefaultLimits()
	acceptOK(t, w, 0, 100, t0)

	far := uint64(100) + limit.MaxSkip + 1
	got := acceptOK(t, w, 0, far, t0)
	if got != impl.VerdictStashed {
		t.Fatalf("far ahead verdict = %v, want stashed", got)
	}
	if !w.Resyncing() {
		t.Fatalf("warden must be resynchronising after a far ahead datagram")
	}
	if w.Stashed() != 1 {
		t.Fatalf("stash size = %d, want 1", w.Stashed())
	}
	if got := acceptOK(t, w, 0, far+1, t0); got != impl.VerdictStashed {
		t.Fatalf("second far ahead verdict = %v, want stashed", got)
	}
	if w.Stashed() != 2 {
		t.Fatalf("stash size = %d, want 2", w.Stashed())
	}
	if highest, _ := w.Highest(0); highest != 100 {
		t.Fatalf("stashing must not move the window, highest = %d", highest)
	}
}

func TestRequestResyncValidatesAndIsIdempotent(t *testing.T) {
	w := newWarden()
	if err := w.RequestResync(0); !errors.Is(err, impl.ErrBadResync) {
		t.Fatalf("zero base = %v, want ErrBadResync", err)
	}
	if err := w.RequestResync(100); err != nil {
		t.Fatalf("request resync: %v", err)
	}
	if err := w.RequestResync(100); err != nil {
		t.Fatalf("repeated request must be a no-op, got %v", err)
	}
	if !w.Resyncing() {
		t.Fatalf("warden must be resynchronising")
	}
}

func TestApplyResyncRequiresHandshakeState(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 100, t0)

	if err := w.ApplyResync("session-a", 500); !errors.Is(err, impl.ErrNotResyncing) {
		t.Fatalf("apply without request = %v, want ErrNotResyncing", err)
	}
	if err := w.RequestResync(100); err != nil {
		t.Fatalf("request resync: %v", err)
	}
	if err := w.ApplyResync("", 500); !errors.Is(err, impl.ErrBadSession) {
		t.Fatalf("blank session = %v, want ErrBadSession", err)
	}
	if err := w.ApplyResync("session-a", 50); !errors.Is(err, impl.ErrBadResync) {
		t.Fatalf("base behind the window = %v, want ErrBadResync", err)
	}
	if err := w.ApplyResync("session-a", 0); !errors.Is(err, impl.ErrBadResync) {
		t.Fatalf("zero base = %v, want ErrBadResync", err)
	}
	if err := w.ApplyResync("session-a", 500); err != nil {
		t.Fatalf("apply resync: %v", err)
	}
	if w.Resyncing() {
		t.Fatalf("resynchronisation must be finished")
	}
	if got := acceptOK(t, w, 0, 500, t0); got != impl.VerdictDuplicate {
		t.Fatalf("base sequence verdict = %v, want duplicate", got)
	}
	if got := acceptOK(t, w, 0, 501, t0); got != impl.VerdictAccept {
		t.Fatalf("sequence above the base verdict = %v, want accept", got)
	}
}

func TestStashIsReleasedAfterResync(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	limit := impl.DefaultLimits()
	acceptOK(t, w, 0, 100, t0)
	acceptOK(t, w, 0, uint64(100)+limit.MaxSkip+1, t0)
	if w.Stashed() != 1 {
		t.Fatalf("stash size = %d, want 1", w.Stashed())
	}
	if err := w.RequestResync(100); err != nil {
		t.Fatalf("request resync: %v", err)
	}
	if err := w.ApplyResync("session-a", 1000); err != nil {
		t.Fatalf("apply resync: %v", err)
	}
	if w.Stashed() != 0 {
		t.Fatalf("stash size after resync = %d, want 0", w.Stashed())
	}
	if highest, _ := w.Highest(0); highest != 1000 {
		t.Fatalf("highest after resync = %d, want 1000", highest)
	}
	if got := acceptOK(t, w, 0, 1001, t0); got != impl.VerdictAccept {
		t.Fatalf("datagram above the resync base verdict = %v, want accept", got)
	}
}

func TestWindowSeenReflectsTheBitmap(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 100, t0)
	acceptOK(t, w, 0, 95, t0)

	if !w.WindowSeen(0, 100) || !w.WindowSeen(0, 95) {
		t.Fatalf("accepted sequences must be visible in the bitmap")
	}
	if w.WindowSeen(0, 96) {
		t.Fatalf("sequence 96 was never received")
	}
	if w.WindowSeen(0, 37) {
		t.Fatalf("sequence 37 is outside the 64 bit window")
	}
	if w.WindowSeen(1, 100) {
		t.Fatalf("epoch 1 has no window")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	if err := w.AdvanceEpoch(2); err != nil {
		t.Fatalf("advance epoch: %v", err)
	}
	acceptOK(t, w, 2, 100, t0)
	acceptOK(t, w, 2, 95, t0)

	payload, err := w.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := newWarden()
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.CurrentEpoch(); got != 2 {
		t.Fatalf("restored epoch = %d, want 2", got)
	}
	if highest, ok := restored.Highest(2); !ok || highest != 100 {
		t.Fatalf("restored highest = %d/%v, want 100/true", highest, ok)
	}
	if got := acceptOK(t, restored, 2, 95, t0); got != impl.VerdictDuplicate {
		t.Fatalf("restored bitmap verdict for 95 = %v, want duplicate", got)
	}
	if got := restored.Duplicates(); got != 1 {
		t.Fatalf("restored duplicate counter = %d, want 1", got)
	}
	if got := acceptOK(t, restored, 2, 101, t0); got != impl.VerdictAccept {
		t.Fatalf("new sequence after restore verdict = %v, want accept", got)
	}
}

func TestRestoreRejectsWrongVersionAndKeepsState(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 100, t0)

	if err := w.Restore([]byte(`{"version":2,"current":9}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("future version = %v, want ErrBadSnapshot", err)
	}
	if highest, ok := w.Highest(0); !ok || highest != 100 {
		t.Fatalf("state after rejected restore = %d/%v, want 100/true", highest, ok)
	}
	if got := acceptOK(t, w, 0, 100, t0); got != impl.VerdictDuplicate {
		t.Fatalf("bitmap after rejected restore verdict = %v, want duplicate", got)
	}
}

func TestRestoreRejectsMalformedPayloadAtomically(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)
	acceptOK(t, w, 0, 42, t0)
	if err := w.RequestResync(42); err != nil {
		t.Fatalf("request resync: %v", err)
	}

	for _, payload := range [][]byte{nil, []byte(""), []byte("{"), []byte("null")} {
		if err := w.Restore(payload); !errors.Is(err, impl.ErrBadSnapshot) {
			t.Fatalf("payload %q = %v, want ErrBadSnapshot", payload, err)
		}
	}
	if highest, ok := w.Highest(0); !ok || highest != 42 {
		t.Fatalf("window after rejected payloads = %d/%v, want 42/true", highest, ok)
	}
	if !w.Resyncing() {
		t.Fatalf("resync flag after rejected payloads must stay set")
	}
}

func TestConcurrentAcceptsKeepExactCounts(t *testing.T) {
	w := newWarden()
	t0 := time.Unix(1700000000, 0)

	const packets = 48
	verdicts := make(chan impl.Verdict, packets)
	errs := make(chan error, packets)
	var wg sync.WaitGroup
	for i := 0; i < packets; i++ {
		wg.Add(1)
		go func(seq uint64) {
			defer wg.Done()
			v, err := w.Accept(0, seq, t0)
			if err != nil {
				errs <- err
				return
			}
			verdicts <- v
		}(uint64(i + 1))
	}
	wg.Wait()
	close(verdicts)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent accept failed: %v", err)
	}
	count := 0
	for v := range verdicts {
		if v != impl.VerdictAccept {
			t.Fatalf("concurrent verdict = %v, want accept", v)
		}
		count++
	}
	if count != packets {
		t.Fatalf("verdicts = %d, want %d", count, packets)
	}
	if got := w.Accepted(); got != packets {
		t.Fatalf("accepted counter = %d, want %d", got, packets)
	}
	if got := w.Duplicates(); got != 0 {
		t.Fatalf("duplicate counter = %d, want 0", got)
	}
	if highest, _ := w.Highest(0); highest != packets {
		t.Fatalf("highest = %d, want %d", highest, packets)
	}
}
