package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-c-04"
)

func c04Config() impl.Config {
	return impl.Config{ThrottleMs: 500, TimeoutMs: 1000, MaxPendingPerStream: 4, MaxPendingTotal: 16}
}

func c04Request(subscriber, stream, key string, startSeq uint64, now int64) impl.Request {
	return impl.Request{Subscriber: subscriber, Stream: stream, Key: key, StartSeq: startSeq, NowMs: now}
}

func TestRequestValidatesFields(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("", "s1", "k1", 0, 1000)); !errors.Is(err, impl.ErrRequestField) {
		t.Fatalf("empty subscriber error = %v, want ErrRequestField", err)
	}
	if err := r.Request(c04Request("sub", "", "k1", 0, 1000)); !errors.Is(err, impl.ErrRequestField) {
		t.Fatalf("empty stream error = %v, want ErrRequestField", err)
	}
	if err := r.Request(c04Request("sub", "s1", "", 0, 1000)); !errors.Is(err, impl.ErrKeyRequired) {
		t.Fatalf("empty key error = %v, want ErrKeyRequired", err)
	}
	if got := r.Pending(); got != 0 {
		t.Fatalf("pending = %d, want no state written for rejected requests", got)
	}
	if got := r.Stats().Requests; got != 0 {
		t.Fatalf("requests = %d, want 0", got)
	}
}

func TestRequestCreatesPendingItem(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("sub", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request: %v", err)
	}
	if got := r.Pending(); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
	if got := r.PendingFor("s1"); got != 1 {
		t.Fatalf("pending for s1 = %d, want 1", got)
	}
	if got := r.Stats().Requests; got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestIdempotencyKeyCoalesces(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("sub", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := r.Request(c04Request("sub", "s1", "k1", 0, 1050)); err != nil {
		t.Fatalf("repeated request: %v", err)
	}
	if got := r.Pending(); got != 1 {
		t.Fatalf("pending = %d, want the repeated key folded", got)
	}
	if got := r.Stats().Coalesced; got != 1 {
		t.Fatalf("coalesced = %d, want 1", got)
	}
	if got := r.Stats().Requests; got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestThrottleRejectsSecondRequestInWindow(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("sub", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := r.Request(c04Request("sub", "s1", "k2", 0, 1200)); !errors.Is(err, impl.ErrThrottled) {
		t.Fatalf("second request error = %v, want ErrThrottled", err)
	}
	if got := r.Pending(); got != 1 {
		t.Fatalf("pending = %d, want the rejected request to take no slot", got)
	}
	if got := r.Stats().Throttled; got != 1 {
		t.Fatalf("throttled = %d, want 1", got)
	}
}

func TestThrottleIsPerSubscriber(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("alice", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("alice request: %v", err)
	}
	if err := r.Request(c04Request("bob", "s1", "k2", 0, 1100)); err != nil {
		t.Fatalf("bob request: %v", err)
	}
	if got := r.Pending(); got != 2 {
		t.Fatalf("pending = %d, want both subscribers queued", got)
	}
	if got := r.Stats().Throttled; got != 0 {
		t.Fatalf("throttled = %d, want 0", got)
	}
}

func TestUpstreamCoalescesSubscribers(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	for _, sub := range []string{"a", "b", "c"} {
		if err := r.Request(c04Request(sub, "s1", "key-"+sub, 0, 1000)); err != nil {
			t.Fatalf("request %s: %v", sub, err)
		}
	}
	up, ok := r.Upstream(1000)
	if !ok {
		t.Fatalf("a coalesced upstream request must be produced")
	}
	if up.Stream != "s1" || up.Requesters != 3 {
		t.Fatalf("upstream stream=%q requesters=%d, want s1 and 3", up.Stream, up.Requesters)
	}
	if up.Seq != 1 || up.Priority != 1 || up.Reason != impl.ReasonCoalesced {
		t.Fatalf("upstream seq=%d priority=%d reason=%q, want 1, 1 and coalesced", up.Seq, up.Priority, up.Reason)
	}
	if _, ok := r.Upstream(1000); ok {
		t.Fatalf("the same stream must not be dispatched twice before its timeout")
	}
	if got := r.Pending(); got != 3 {
		t.Fatalf("pending = %d, want the requests still waiting for a keyframe", got)
	}
}

func TestUpstreamSeparatesStreams(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request s1: %v", err)
	}
	if err := r.Request(c04Request("b", "s2", "k2", 0, 1000)); err != nil {
		t.Fatalf("request s2: %v", err)
	}
	first, ok := r.Upstream(1000)
	if !ok {
		t.Fatalf("first upstream request missing")
	}
	second, ok := r.Upstream(1000)
	if !ok {
		t.Fatalf("second stream must produce its own upstream request")
	}
	if first.Stream == second.Stream {
		t.Fatalf("streams were merged, both %q", first.Stream)
	}
	if first.Stream != "s1" || second.Stream != "s2" {
		t.Fatalf("stream order = %q then %q, want s1 then s2", first.Stream, second.Stream)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("dispatch seq = %d then %d, want an increasing sequence", first.Seq, second.Seq)
	}
	if _, ok := r.Upstream(1000); ok {
		t.Fatalf("no third stream is pending")
	}
}

func TestUpstreamEscalatesAfterTimeout(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request: %v", err)
	}
	first, ok := r.Upstream(1000)
	if !ok {
		t.Fatalf("initial upstream request missing")
	}
	if first.Priority != 1 {
		t.Fatalf("initial priority = %d, want 1", first.Priority)
	}
	for _, at := range []int64{1500, 1999} {
		if _, ok := r.Upstream(at); ok {
			t.Fatalf("upstream at %d must stay silent before the timeout", at)
		}
	}
	escalated, ok := r.Upstream(2000)
	if !ok {
		t.Fatalf("the timed out request must be dispatched again")
	}
	if escalated.Priority != 2 || escalated.Reason != impl.ReasonEscalated {
		t.Fatalf("escalated priority=%d reason=%q, want 2 and escalated", escalated.Priority, escalated.Reason)
	}
	if escalated.Seq != 2 {
		t.Fatalf("escalated seq = %d, want the next dispatch sequence", escalated.Seq)
	}
	if got := r.Stats().Escalated; got != 1 {
		t.Fatalf("escalated = %d, want 1", got)
	}
}

func TestOnKeyframeSatisfiesMatchingStreamOnly(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("a", "s1", "k1", 10, 1000)); err != nil {
		t.Fatalf("request s1: %v", err)
	}
	if err := r.Request(c04Request("b", "s2", "k2", 10, 1000)); err != nil {
		t.Fatalf("request s2: %v", err)
	}
	if got := r.OnKeyframe("s1", 50); got != 1 {
		t.Fatalf("satisfied by keyframe = %d, want 1", got)
	}
	if got := r.Pending(); got != 1 {
		t.Fatalf("pending = %d, want the other stream untouched", got)
	}
	if got := r.PendingFor("s2"); got != 1 {
		t.Fatalf("pending for s2 = %d, want 1", got)
	}
	if got := r.Stats().Satisfied; got != 1 {
		t.Fatalf("satisfied = %d, want 1", got)
	}
	if got := r.OnKeyframe("s9", 99); got != 0 {
		t.Fatalf("satisfied for unknown stream = %d, want 0", got)
	}
}

func TestSubscriberLeaveCancelsPending(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request a: %v", err)
	}
	if err := r.Request(c04Request("b", "s1", "k2", 0, 1010)); err != nil {
		t.Fatalf("request b: %v", err)
	}
	if got := r.OnSubscriberLeave("a"); got != 1 {
		t.Fatalf("cancelled items = %d, want 1", got)
	}
	if got := r.Pending(); got != 1 {
		t.Fatalf("pending = %d, want only bob left", got)
	}
	st := r.Stats()
	if st.Cancelled != 1 || st.Satisfied != 0 {
		t.Fatalf("cancelled=%d satisfied=%d, want 1 and 0", st.Cancelled, st.Satisfied)
	}
	if got := r.OnKeyframe("s1", 100); got != 1 {
		t.Fatalf("satisfied after leave = %d, want only the remaining subscriber", got)
	}
	if got := r.OnSubscriberLeave("ghost"); got != 0 {
		t.Fatalf("leave for unknown subscriber = %d, want 0", got)
	}
}

func TestPendingLimitPerStream(t *testing.T) {
	cfg := c04Config()
	cfg.MaxPendingPerStream = 2
	r := impl.NewKeyframeRequester(cfg)
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request a: %v", err)
	}
	if err := r.Request(c04Request("b", "s1", "k2", 0, 1010)); err != nil {
		t.Fatalf("request b: %v", err)
	}
	if err := r.Request(c04Request("c", "s1", "k3", 0, 1020)); !errors.Is(err, impl.ErrPendingCap) {
		t.Fatalf("third request error = %v, want ErrPendingCap", err)
	}
	if got := r.PendingFor("s1"); got != 2 {
		t.Fatalf("pending for s1 = %d, want the cap of 2", got)
	}
	if got := r.Stats().LimitRejected; got != 1 {
		t.Fatalf("limit rejected = %d, want 1", got)
	}
}

func TestPendingLimitTotal(t *testing.T) {
	cfg := c04Config()
	cfg.MaxPendingTotal = 3
	r := impl.NewKeyframeRequester(cfg)
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("request a: %v", err)
	}
	if err := r.Request(c04Request("b", "s1", "k2", 0, 1010)); err != nil {
		t.Fatalf("request b: %v", err)
	}
	if err := r.Request(c04Request("c", "s2", "k3", 0, 1020)); err != nil {
		t.Fatalf("request c: %v", err)
	}
	if err := r.Request(c04Request("d", "s3", "k4", 0, 1030)); !errors.Is(err, impl.ErrPendingCap) {
		t.Fatalf("request over the total cap error = %v, want ErrPendingCap", err)
	}
	if got := r.Pending(); got != 3 {
		t.Fatalf("pending = %d, want the total cap of 3", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("a", "s1", "k1", 10, 1000)); err != nil {
		t.Fatalf("request s1: %v", err)
	}
	if err := r.Request(c04Request("b", "s2", "k2", 10, 1000)); err != nil {
		t.Fatalf("request s2: %v", err)
	}
	first, ok := r.Upstream(1000)
	if !ok || first.Stream != "s1" {
		t.Fatalf("initial upstream = %+v ok=%v, want s1", first, ok)
	}
	payload, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	h := impl.NewKeyframeRequester(c04Config())
	if err := h.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := h.Pending(); got != 2 {
		t.Fatalf("restored pending = %d, want 2", got)
	}
	if got := h.PendingFor("s2"); got != 1 {
		t.Fatalf("restored pending for s2 = %d, want 1", got)
	}
	if got := h.Stats().Requests; got != 2 {
		t.Fatalf("restored requests = %d, want 2", got)
	}
	next, ok := h.Upstream(1900)
	if !ok {
		t.Fatalf("the untouched stream must still be dispatchable after restore")
	}
	if next.Stream != "s2" || next.Seq != 2 {
		t.Fatalf("restored upstream stream=%q seq=%d, want s2 and the persisted sequence", next.Stream, next.Seq)
	}
	escalated, ok := h.Upstream(2000)
	if !ok || escalated.Stream != "s1" || escalated.Priority != 2 {
		t.Fatalf("restored escalation = %+v ok=%v, want s1 at priority 2", escalated, ok)
	}
	if got := h.Stats().Escalated; got != 1 {
		t.Fatalf("restored escalated counter = %d, want 1", got)
	}
	if err := h.Restore([]byte("junk")); !errors.Is(err, impl.ErrStateCorrupt) {
		t.Fatalf("bad snapshot error = %v, want ErrStateCorrupt", err)
	}
	if got := h.Pending(); got != 2 {
		t.Fatalf("bad restore changed pending to %d", got)
	}
}

func TestConcurrentRequestsAreSerialised(t *testing.T) {
	cfg := c04Config()
	cfg.MaxPendingPerStream = 32
	cfg.MaxPendingTotal = 64
	r := impl.NewKeyframeRequester(cfg)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sub := "sub-" + string(rune('a'+i))
			_ = r.Request(c04Request(sub, "s1", sub+"-key", 0, 1000))
		}(i)
	}
	wg.Wait()
	if got := r.Pending(); got != 10 {
		t.Fatalf("pending = %d, want every subscriber queued once", got)
	}
	if got := r.Stats().Requests; got != 10 {
		t.Fatalf("requests = %d, want 10", got)
	}
	up, ok := r.Upstream(1000)
	if !ok {
		t.Fatalf("coalesced upstream request missing")
	}
	if up.Requesters != 10 {
		t.Fatalf("requesters = %d, want the whole batch merged", up.Requesters)
	}
}

func TestStatsCountersAfterMixedCalls(t *testing.T) {
	r := impl.NewKeyframeRequester(c04Config())
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1000)); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := r.Request(c04Request("a", "s1", "k2", 0, 1100)); !errors.Is(err, impl.ErrThrottled) {
		t.Fatalf("throttled request error = %v, want ErrThrottled", err)
	}
	if err := r.Request(c04Request("a", "s1", "k1", 0, 1200)); err != nil {
		t.Fatalf("repeated key must be folded even inside the throttle window: %v", err)
	}
	if err := r.Request(c04Request("b", "s1", "k3", 0, 1300)); err != nil {
		t.Fatalf("second subscriber request: %v", err)
	}
	if got := r.OnKeyframe("s1", 100); got != 2 {
		t.Fatalf("satisfied = %d, want both pending subscribers", got)
	}
	st := r.Stats()
	if st.Requests != 2 || st.Throttled != 1 || st.Coalesced != 1 || st.Satisfied != 2 || st.Pending != 0 {
		t.Fatalf("stats requests=%d throttled=%d coalesced=%d satisfied=%d pending=%d, want 2,1,1,2,0",
			st.Requests, st.Throttled, st.Coalesced, st.Satisfied, st.Pending)
	}
}
