package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-24"
)

func defaultCollector() *impl.HoleCollector {
	return impl.NewHoleCollector(impl.DefaultLimits())
}

func part(id uint64, index, count uint32, payload string, at time.Time) impl.Fragment {
	return impl.Fragment{
		MessageID:    id,
		SegmentIndex: index,
		SegmentCount: count,
		Payload:      []byte(payload),
		Arrived:      at,
	}
}

func addOK(t *testing.T, c *impl.HoleCollector, f impl.Fragment) (impl.Message, bool) {
	t.Helper()
	msg, ok, err := c.Add(f)
	if err != nil {
		t.Fatalf("add message %d segment %d: %v", f.MessageID, f.SegmentIndex, err)
	}
	return msg, ok
}

func TestAddRejectsMalformedFragments(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	if _, _, err := c.Add(part(1, 0, 0, "a", t0)); !errors.Is(err, impl.ErrBadFragment) {
		t.Fatalf("zero segment count = %v, want ErrBadFragment", err)
	}
	if _, _, err := c.Add(part(1, 2, 2, "a", t0)); !errors.Is(err, impl.ErrBadFragment) {
		t.Fatalf("index outside the count = %v, want ErrBadFragment", err)
	}
	if _, _, err := c.Add(part(1, 0, 2, "", t0)); !errors.Is(err, impl.ErrBadFragment) {
		t.Fatalf("empty payload = %v, want ErrBadFragment", err)
	}
	if _, _, err := c.Add(part(1, 0, 300, "a", t0)); !errors.Is(err, impl.ErrTooManySegments) {
		t.Fatalf("300 segments = %v, want ErrTooManySegments", err)
	}
	if got := c.PendingCount(); got != 0 {
		t.Fatalf("rejected fragments left %d messages pending, want 0", got)
	}
}

func TestSingleSegmentMessageIsDeliveredImmediately(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	msg, ok := addOK(t, c, part(5, 0, 1, "hello", t0))
	if !ok {
		t.Fatalf("a complete single segment message must be delivered")
	}
	if msg.MessageID != 5 || string(msg.Payload) != "hello" {
		t.Fatalf("delivered message = %d/%q, want 5/hello", msg.MessageID, msg.Payload)
	}
	if msg.Segments != 1 || msg.Skipped != 0 {
		t.Fatalf("message shape = %d segments/%d skipped, want 1/0", msg.Segments, msg.Skipped)
	}
	if c.PendingCount() != 0 || c.Delivered() != 1 {
		t.Fatalf("pending/delivered = %d/%d, want 0/1", c.PendingCount(), c.Delivered())
	}
}

func TestOutOfOrderFragmentsAssembleInIndexOrder(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	if _, ok := addOK(t, c, part(9, 2, 3, "c", t0)); ok {
		t.Fatalf("a partial message must not be delivered")
	}
	if _, ok := addOK(t, c, part(9, 0, 3, "a", t0)); ok {
		t.Fatalf("two of three segments must not be delivered")
	}
	msg, ok := addOK(t, c, part(9, 1, 3, "b", t0))
	if !ok {
		t.Fatalf("the last missing segment must complete the message")
	}
	if string(msg.Payload) != "abc" {
		t.Fatalf("payload = %q, want abc in index order", msg.Payload)
	}
	if msg.Segments != 3 {
		t.Fatalf("segments = %d, want 3", msg.Segments)
	}
}

func TestRepeatedIdenticalSegmentIsIdempotent(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(3, 0, 2, "a", t0))
	msg, ok := addOK(t, c, part(3, 0, 2, "a", t0.Add(time.Second)))
	if ok {
		t.Fatalf("repeated segment must not complete the message: %+v", msg)
	}
	info := c.Pending()
	if len(info) != 1 || info[0].Received != 1 || info[0].Bytes != 1 {
		t.Fatalf("pending after duplicate = %+v, want one segment of one byte", info)
	}
	msg, ok = addOK(t, c, part(3, 1, 2, "b", t0.Add(2*time.Second)))
	if !ok || string(msg.Payload) != "ab" {
		t.Fatalf("message after the second distinct segment = %q/%v, want ab/true", msg.Payload, ok)
	}
}

func TestConflictingSegmentInvalidatesWholeMessage(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(4, 0, 2, "a", t0))
	if _, _, err := c.Add(part(4, 0, 2, "zzz", t0)); !errors.Is(err, impl.ErrSegmentConflict) {
		t.Fatalf("conflicting duplicate = %v, want ErrSegmentConflict", err)
	}
	if got := c.PendingCount(); got != 0 {
		t.Fatalf("pending after conflict = %d, want 0", got)
	}
	if _, ok := addOK(t, c, part(4, 0, 2, "fresh", t0)); ok {
		t.Fatalf("a fresh message must start from zero progress")
	}
	info := c.Pending()
	if len(info) != 1 || info[0].Received != 1 || info[0].Bytes != 5 {
		t.Fatalf("fresh message state = %+v, want one five byte segment", info)
	}
}

func TestDeliveryFollowsMessageIDOrder(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(7, 0, 2, "7a", t0))
	if msg, ok := addOK(t, c, part(10, 0, 1, "ten", t0)); ok {
		t.Fatalf("message 10 must wait for the unfinished message 7, got %+v", msg)
	}
	if _, ok := c.Poll(); ok {
		t.Fatalf("poll must not skip ahead of message 7")
	}
	msg, ok := addOK(t, c, part(7, 1, 2, "7b", t0))
	if !ok || msg.MessageID != 7 || string(msg.Payload) != "7a7b" {
		t.Fatalf("first delivered message = %+v/%v, want message 7", msg, ok)
	}
	msg, ok = c.Poll()
	if !ok || msg.MessageID != 10 || string(msg.Payload) != "ten" {
		t.Fatalf("second delivered message = %+v/%v, want message 10", msg, ok)
	}
	if got := c.Delivered(); got != 2 {
		t.Fatalf("delivered counter = %d, want 2", got)
	}
}

func TestCloseDropsProgressAndAllowsRestart(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(5, 0, 2, "a", t0))
	if err := c.Close(5); err != nil {
		t.Fatalf("close message 5: %v", err)
	}
	if got := c.PendingCount(); got != 0 {
		t.Fatalf("pending after close = %d, want 0", got)
	}
	if err := c.Close(5); !errors.Is(err, impl.ErrUnknownMessage) {
		t.Fatalf("second close = %v, want ErrUnknownMessage", err)
	}
	if _, ok := addOK(t, c, part(5, 0, 2, "a", t0)); ok {
		t.Fatalf("a restarted message must not complete on one segment")
	}
	info := c.Pending()
	if len(info) != 1 || info[0].MessageID != 5 || info[0].Received != 1 {
		t.Fatalf("restarted message state = %+v, want message 5 with one segment", info)
	}
}

func TestSegmentCountLimitIsEnforced(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)
	limit := impl.DefaultLimits()

	if _, _, err := c.Add(part(1, 0, uint32(limit.MaxSegmentsPerMessage+1), "a", t0)); !errors.Is(err, impl.ErrTooManySegments) {
		t.Fatalf("declared segments above the limit = %v, want ErrTooManySegments", err)
	}
	if _, ok := addOK(t, c, part(2, 0, uint32(limit.MaxSegmentsPerMessage), "a", t0)); ok {
		t.Fatalf("a message at the segment limit must still be accepted as pending")
	}
}

func TestPendingLimitEvictsLeastRecentlyActive(t *testing.T) {
	lim := impl.DefaultLimits()
	lim.MaxPending = 2
	c := impl.NewHoleCollector(lim)
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(1, 0, 2, "a", t0))
	addOK(t, c, part(2, 0, 2, "b", t0.Add(time.Second)))
	addOK(t, c, part(1, 0, 2, "a", t0.Add(2*time.Second)))
	addOK(t, c, part(3, 0, 2, "c", t0.Add(3*time.Second)))

	pending := c.Pending()
	if len(pending) != 2 || pending[0].MessageID != 1 || pending[1].MessageID != 3 {
		t.Fatalf("pending messages = %+v, want 1 and 3", pending)
	}
	evicted := c.Evicted()
	if len(evicted) != 1 || evicted[0] != 2 {
		t.Fatalf("evicted = %v, want [2]", evicted)
	}
}

func TestByteBudgetEvictsAndRejectsOversizedMessages(t *testing.T) {
	lim := impl.DefaultLimits()
	lim.MaxPending = 8
	lim.MaxBytes = 8
	c := impl.NewHoleCollector(lim)
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(1, 0, 2, "aaaa", t0))
	addOK(t, c, part(2, 0, 2, "bbbb", t0.Add(time.Second)))
	addOK(t, c, part(3, 0, 2, "cccc", t0.Add(2*time.Second)))

	if got := c.PendingBytes(); got != 8 {
		t.Fatalf("pending bytes = %d, want 8", got)
	}
	pending := c.Pending()
	if len(pending) != 2 || pending[0].MessageID != 2 || pending[1].MessageID != 3 {
		t.Fatalf("pending after byte eviction = %+v, want 2 and 3", pending)
	}
	if got := c.Evicted(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("evicted = %v, want [1]", got)
	}
	huge := impl.Fragment{
		MessageID:    9,
		SegmentIndex: 0,
		SegmentCount: 2,
		Payload:      make([]byte, 16),
		Arrived:      t0,
	}
	if _, _, err := c.Add(huge); !errors.Is(err, impl.ErrMessageTooLarge) {
		t.Fatalf("oversized message = %v, want ErrMessageTooLarge", err)
	}
}

func TestSweepEvictsIdleMessages(t *testing.T) {
	lim := impl.DefaultLimits()
	lim.MaxIdle = 100 * time.Millisecond
	c := impl.NewHoleCollector(lim)
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(6, 0, 2, "a", t0))
	if got := c.Sweep(t0.Add(50 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("sweep before the idle limit = %v, want none", got)
	}
	got := c.Sweep(t0.Add(200 * time.Millisecond))
	if len(got) != 1 || got[0] != 6 {
		t.Fatalf("sweep after the idle limit = %v, want [6]", got)
	}
	if c.PendingCount() != 0 {
		t.Fatalf("pending after idle eviction = %d, want 0", c.PendingCount())
	}
	if evicted := c.Evicted(); len(evicted) != 1 || evicted[0] != 6 {
		t.Fatalf("evicted history = %v, want [6]", evicted)
	}
}

func TestSweepKeepsRecentlyActiveMessages(t *testing.T) {
	lim := impl.DefaultLimits()
	lim.MaxIdle = 100 * time.Millisecond
	c := impl.NewHoleCollector(lim)
	t0 := time.Unix(1700000000, 0)

	addOK(t, c, part(6, 0, 2, "a", t0))
	addOK(t, c, part(6, 0, 2, "a", t0.Add(80*time.Millisecond)))
	if got := c.Sweep(t0.Add(150 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("sweep evicted an active message: %v", got)
	}
	if got := c.PendingCount(); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
}

func TestSnapshotRestoreKeepsProgressAndDeliveryOrder(t *testing.T) {
	lim := impl.DefaultLimits()
	c := impl.NewHoleCollector(lim)
	t0 := time.Unix(1700000000, 0)
	addOK(t, c, part(7, 0, 2, "7a", t0))
	addOK(t, c, part(10, 0, 1, "ten", t0))

	payload, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewHoleCollector(lim)
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.PendingCount(); got != 2 {
		t.Fatalf("restored pending = %d, want 2", got)
	}
	if _, ok := restored.Poll(); ok {
		t.Fatalf("restored queue must still wait for message 7")
	}
	msg, ok := addOK(t, restored, part(7, 1, 2, "7b", t0.Add(time.Second)))
	if !ok || msg.MessageID != 7 || string(msg.Payload) != "7a7b" {
		t.Fatalf("restored message 7 = %+v/%v", msg, ok)
	}
	msg, ok = restored.Poll()
	if !ok || msg.MessageID != 10 || string(msg.Payload) != "ten" {
		t.Fatalf("restored message 10 = %+v/%v", msg, ok)
	}
	if got := restored.Delivered(); got != 2 {
		t.Fatalf("restored delivered counter = %d, want 2", got)
	}
}

func TestRestoreRejectsBadPayloadAndKeepsState(t *testing.T) {
	c := defaultCollector()
	t0 := time.Unix(1700000000, 0)
	addOK(t, c, part(1, 0, 2, "a", t0))

	for _, payload := range [][]byte{nil, []byte(""), []byte("{"), []byte(`{"version":2}`)} {
		if err := c.Restore(payload); !errors.Is(err, impl.ErrBadSnapshot) {
			t.Fatalf("payload %q = %v, want ErrBadSnapshot", payload, err)
		}
	}
	if got := c.PendingCount(); got != 1 {
		t.Fatalf("pending after rejected restores = %d, want 1", got)
	}
	msg, ok := addOK(t, c, part(1, 1, 2, "b", t0.Add(time.Second)))
	if !ok || string(msg.Payload) != "ab" {
		t.Fatalf("message after rejected restores = %q/%v, want ab/true", msg.Payload, ok)
	}
}

func TestConcurrentAddsKeepExactCounts(t *testing.T) {
	lim := impl.DefaultLimits()
	lim.MaxPending = 64
	lim.MaxBytes = 1 << 20
	c := impl.NewHoleCollector(lim)
	t0 := time.Unix(1700000000, 0)

	const messages = 32
	errs := make(chan error, messages)
	delivered := make(chan uint64, messages)
	var wg sync.WaitGroup
	for i := 0; i < messages; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			msg, ok, err := c.Add(impl.Fragment{
				MessageID:    id,
				SegmentIndex: 0,
				SegmentCount: 1,
				Payload:      []byte(fmt.Sprintf("payload-%d", id)),
				Arrived:      t0,
			})
			if err != nil {
				errs <- err
				return
			}
			if ok {
				delivered <- msg.MessageID
			}
		}(uint64(i + 1))
	}
	wg.Wait()
	close(errs)
	close(delivered)

	for err := range errs {
		t.Fatalf("concurrent add failed: %v", err)
	}
	ids := make(map[uint64]bool)
	for id := range delivered {
		ids[id] = true
	}
	if len(ids) != messages {
		t.Fatalf("distinct delivered messages = %d, want %d", len(ids), messages)
	}
	if got := c.Delivered(); got != messages {
		t.Fatalf("delivered counter = %d, want %d", got, messages)
	}
	if got := c.PendingCount(); got != 0 {
		t.Fatalf("pending after concurrent adds = %d, want 0", got)
	}
}
