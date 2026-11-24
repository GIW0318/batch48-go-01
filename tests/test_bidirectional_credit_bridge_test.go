package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-25"
)

type fakeLink struct {
	pauses  int
	resumes int
}

func (f *fakeLink) Pause() error {
	f.pauses++
	return nil
}

func (f *fakeLink) Resume() error {
	f.resumes++
	return nil
}

func attachedBridge(t *testing.T, pool, maxCredit uint64) (*impl.CreditBridge, *fakeLink, *fakeLink) {
	t.Helper()
	b := impl.NewCreditBridge(impl.Limits{PoolBytes: pool, MaxCredit: maxCredit})
	up, down := &fakeLink{}, &fakeLink{}
	if err := b.Attach(up, down); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return b, up, down
}

func TestAttachValidatesLinksAndRejectsSecondAttach(t *testing.T) {
	b := impl.NewCreditBridge(impl.DefaultLimits())
	if err := b.Grant(impl.DirUp, 10); !errors.Is(err, impl.ErrNotAttached) {
		t.Fatalf("grant before attach = %v, want ErrNotAttached", err)
	}
	up, down := &fakeLink{}, &fakeLink{}
	if err := b.Attach(nil, down); !errors.Is(err, impl.ErrBadLink) {
		t.Fatalf("attach without upstream = %v, want ErrBadLink", err)
	}
	if err := b.Attach(up, nil); !errors.Is(err, impl.ErrBadLink) {
		t.Fatalf("attach without downstream = %v, want ErrBadLink", err)
	}
	if err := b.Attach(up, down); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := b.Attach(up, down); !errors.Is(err, impl.ErrAlreadyAttached) {
		t.Fatalf("second attach = %v, want ErrAlreadyAttached", err)
	}
}

func TestGrantValidatesDirectionAmountAndCeiling(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)

	if err := b.Grant(impl.Direction(7), 1); !errors.Is(err, impl.ErrBadDir) {
		t.Fatalf("unknown direction = %v, want ErrBadDir", err)
	}
	if err := b.Grant(impl.DirUp, 0); !errors.Is(err, impl.ErrBadCredit) {
		t.Fatalf("zero grant = %v, want ErrBadCredit", err)
	}
	if err := b.Grant(impl.DirUp, 1001); !errors.Is(err, impl.ErrCreditOverflow) {
		t.Fatalf("grant above the ceiling = %v, want ErrCreditOverflow", err)
	}
	if err := b.Grant(impl.DirUp, 400); err != nil {
		t.Fatalf("valid grant: %v", err)
	}
	if got := b.Credit(impl.DirUp); got != 400 {
		t.Fatalf("credit = %d, want 400", got)
	}
	if err := b.Grant(impl.DirUp, 700); !errors.Is(err, impl.ErrCreditOverflow) {
		t.Fatalf("grant that would cross the ceiling = %v, want ErrCreditOverflow", err)
	}
}

func TestConsumeWithinCreditUpdatesState(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 100); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 40); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := b.Credit(impl.DirUp); got != 60 {
		t.Fatalf("credit = %d, want 60", got)
	}
	if got := b.Inflight(impl.DirUp); got != 40 {
		t.Fatalf("inflight = %d, want 40", got)
	}
	if got := b.Seq(impl.DirUp); got != 1 {
		t.Fatalf("sequence = %d, want 1", got)
	}
	if !b.Awaiting(impl.DirUp) {
		t.Fatalf("direction must wait for an acknowledgement")
	}
}

func TestConsumeBeyondCreditIsRejected(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 10); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 11); !errors.Is(err, impl.ErrNoCredit) {
		t.Fatalf("consume above credit = %v, want ErrNoCredit", err)
	}
	if got := b.Credit(impl.DirUp); got != 10 {
		t.Fatalf("credit after rejection = %d, want 10", got)
	}
	if got := b.Inflight(impl.DirUp); got != 0 {
		t.Fatalf("inflight after rejection = %d, want 0", got)
	}
}

func TestStopAndWaitBlocksSecondConsume(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 100); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 10); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if err := b.Consume(impl.DirUp, 1); !errors.Is(err, impl.ErrAwaitingAck) {
		t.Fatalf("second consume = %v, want ErrAwaitingAck", err)
	}
	if got := b.Inflight(impl.DirUp); got != 10 {
		t.Fatalf("inflight = %d, want 10", got)
	}
}

func TestAckMatchesOutstandingSequence(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 100); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 10); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := b.Ack(impl.DirUp, 2); !errors.Is(err, impl.ErrBadAck) {
		t.Fatalf("wrong sequence = %v, want ErrBadAck", err)
	}
	if err := b.Ack(impl.DirUp, 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := b.Inflight(impl.DirUp); got != 0 {
		t.Fatalf("inflight after ack = %d, want 0", got)
	}
	if b.Awaiting(impl.DirUp) {
		t.Fatalf("direction must not wait after ack")
	}
	if err := b.Ack(impl.DirUp, 1); !errors.Is(err, impl.ErrNoPending) {
		t.Fatalf("second ack = %v, want ErrNoPending", err)
	}
}

func TestPoolFreeTracksInflight(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 100); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 30); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got := b.PoolFree(); got != 70 {
		t.Fatalf("pool free = %d, want 70", got)
	}
	if err := b.Ack(impl.DirUp, 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := b.PoolFree(); got != 100 {
		t.Fatalf("pool free after ack = %d, want 100", got)
	}
}

func TestFairnessCapsSecondDirection(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 1000); err != nil {
		t.Fatalf("grant up: %v", err)
	}
	if err := b.Grant(impl.DirDown, 1000); err != nil {
		t.Fatalf("grant down: %v", err)
	}
	if err := b.Consume(impl.DirUp, 60); err != nil {
		t.Fatalf("first direction consume: %v", err)
	}
	if err := b.Consume(impl.DirDown, 60); !errors.Is(err, impl.ErrPoolExhausted) {
		t.Fatalf("second direction above its share = %v, want ErrPoolExhausted", err)
	}
	if err := b.Consume(impl.DirDown, 40); err != nil {
		t.Fatalf("second direction within the remainder: %v", err)
	}
	if got := b.PoolFree(); got != 0 {
		t.Fatalf("pool free = %d, want 0", got)
	}
	if err := b.Ack(impl.DirUp, 1); err != nil {
		t.Fatalf("ack up: %v", err)
	}
	if err := b.Ack(impl.DirDown, 1); err != nil {
		t.Fatalf("ack down: %v", err)
	}
	if got := b.PoolFree(); got != 100 {
		t.Fatalf("pool free after both acks = %d, want 100", got)
	}
}

func TestOversizedPoolRequestRejected(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 500); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 200); !errors.Is(err, impl.ErrPoolExhausted) {
		t.Fatalf("consume above the pool = %v, want ErrPoolExhausted", err)
	}
	if err := b.Consume(impl.DirUp, 0); !errors.Is(err, impl.ErrBadData) {
		t.Fatalf("zero byte consume = %v, want ErrBadData", err)
	}
	if got := b.Inflight(impl.DirUp); got != 0 {
		t.Fatalf("inflight after rejections = %d, want 0", got)
	}
}

func TestHalfCloseRejectsDirectionOperations(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 10); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.HalfClose(impl.DirUp); err != nil {
		t.Fatalf("half close: %v", err)
	}
	if !b.HalfClosed(impl.DirUp) {
		t.Fatalf("direction must report half closed")
	}
	if err := b.Grant(impl.DirUp, 1); !errors.Is(err, impl.ErrHalfClosed) {
		t.Fatalf("grant after half close = %v, want ErrHalfClosed", err)
	}
	if err := b.Consume(impl.DirUp, 1); !errors.Is(err, impl.ErrHalfClosed) {
		t.Fatalf("consume after half close = %v, want ErrHalfClosed", err)
	}
	if err := b.HalfClose(impl.DirUp); !errors.Is(err, impl.ErrAlreadyHalfClosed) {
		t.Fatalf("second half close = %v, want ErrAlreadyHalfClosed", err)
	}
	if b.HalfClosed(impl.DirDown) {
		t.Fatalf("the other direction must stay open")
	}
}

func TestPauseAndResumeFollowCredit(t *testing.T) {
	b, _, down := attachedBridge(t, 100, 1000)
	if !b.Paused() || down.pauses != 1 {
		t.Fatalf("attached bridge with no credit must pause the downstream link, paused=%v pauses=%d", b.Paused(), down.pauses)
	}
	if err := b.Grant(impl.DirUp, 10); err != nil {
		t.Fatalf("grant up: %v", err)
	}
	if err := b.Grant(impl.DirDown, 10); err != nil {
		t.Fatalf("grant down: %v", err)
	}
	if b.Paused() || down.resumes != 1 {
		t.Fatalf("bridge with credit on both legs must resume, paused=%v resumes=%d", b.Paused(), down.resumes)
	}
	if err := b.Consume(impl.DirUp, 10); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !b.Paused() || down.pauses != 2 {
		t.Fatalf("exhausted credit must pause again, paused=%v pauses=%d", b.Paused(), down.pauses)
	}
	if err := b.Grant(impl.DirUp, 10); err != nil {
		t.Fatalf("regrant: %v", err)
	}
	if b.Paused() || down.resumes != 2 {
		t.Fatalf("restored credit on both legs must resume, paused=%v resumes=%d", b.Paused(), down.resumes)
	}
}

func TestDrainWaitsForAcks(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirDown, 50); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirDown, 20); err != nil {
		t.Fatalf("consume: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- b.Drain() }()
	select {
	case err := <-done:
		t.Fatalf("drain returned before the acknowledgement: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := b.Ack(impl.DirDown, 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("drain after ack: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("drain did not return after the acknowledgement")
	}
}

func TestSnapshotRestoreKeepsOutstandingState(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 100); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Consume(impl.DirUp, 40); err != nil {
		t.Fatalf("consume: %v", err)
	}
	payload, err := b.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := impl.NewCreditBridge(impl.Limits{PoolBytes: 100, MaxCredit: 1000})
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Credit(impl.DirUp); got != 60 {
		t.Fatalf("restored credit = %d, want 60", got)
	}
	if got := restored.Inflight(impl.DirUp); got != 40 {
		t.Fatalf("restored inflight = %d, want 40", got)
	}
	if got := restored.Seq(impl.DirUp); got != 1 {
		t.Fatalf("restored sequence = %d, want 1", got)
	}
	if !restored.Awaiting(impl.DirUp) {
		t.Fatalf("restored direction must still wait for the acknowledgement")
	}
	if err := restored.Attach(&fakeLink{}, &fakeLink{}); err != nil {
		t.Fatalf("attach restored bridge: %v", err)
	}
	if err := restored.Consume(impl.DirUp, 1); !errors.Is(err, impl.ErrAwaitingAck) {
		t.Fatalf("consume before the restored ack = %v, want ErrAwaitingAck", err)
	}
	if err := restored.Ack(impl.DirUp, 1); err != nil {
		t.Fatalf("ack restored outstanding data: %v", err)
	}
	if got := restored.PoolFree(); got != 100 {
		t.Fatalf("pool free after restored ack = %d, want 100", got)
	}
}

func TestRestoreRejectsBadPayloadAndKeepsState(t *testing.T) {
	b, _, _ := attachedBridge(t, 100, 1000)
	if err := b.Grant(impl.DirUp, 50); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := b.Restore([]byte(`{"version":2}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("future version = %v, want ErrBadSnapshot", err)
	}
	if err := b.Restore([]byte(`{"version":1,"credit":[10,0],"inflight":[5,0],"seq":[1,0],"awaiting":[false,false],"out_bytes":[0,0],"closed":[false,false]}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("inflight without an outstanding sequence = %v, want ErrBadSnapshot", err)
	}
	if err := b.Restore(nil); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("empty payload = %v, want ErrBadSnapshot", err)
	}
	if got := b.Credit(impl.DirUp); got != 50 {
		t.Fatalf("credit after rejected restores = %d, want 50", got)
	}
}

func TestConcurrentGrantsAndAcksAreSerialized(t *testing.T) {
	b, _, _ := attachedBridge(t, 1000, 1000)

	const grants = 16
	var wg sync.WaitGroup
	errs := make(chan error, grants)
	for i := 0; i < grants; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- b.Grant(impl.DirUp, 5)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent grant failed: %v", err)
		}
	}
	if got := b.Credit(impl.DirUp); got != 80 {
		t.Fatalf("credit after concurrent grants = %d, want 80", got)
	}
	if err := b.Consume(impl.DirUp, 80); err != nil {
		t.Fatalf("consume: %v", err)
	}

	const acks = 12
	var wg2 sync.WaitGroup
	ackErrs := make(chan error, acks)
	for i := 0; i < acks; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			ackErrs <- b.Ack(impl.DirUp, 1)
		}()
	}
	wg2.Wait()
	close(ackErrs)
	okCount := 0
	pendingCount := 0
	for err := range ackErrs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, impl.ErrNoPending):
			pendingCount++
		default:
			t.Fatalf("concurrent ack returned %v", err)
		}
	}
	if okCount != 1 || pendingCount != acks-1 {
		t.Fatalf("ack outcomes = %d accepted / %d rejected, want 1/%d", okCount, pendingCount, acks-1)
	}
	if got := b.Inflight(impl.DirUp); got != 0 {
		t.Fatalf("inflight after concurrent acks = %d, want 0", got)
	}
	if got := b.PoolFree(); got != 1000 {
		t.Fatalf("pool free after concurrent acks = %d, want 1000", got)
	}
}
