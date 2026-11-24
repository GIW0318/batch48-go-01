package task

import (
	"errors"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-16"
)

func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func capsuleFrame(typ uint64, payload []byte) []byte {
	out := appendUvarint(nil, typ)
	out = appendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}

func datagramFrame(flowID uint64, payload []byte) []byte {
	body := appendUvarint(nil, flowID)
	body = append(body, payload...)
	return capsuleFrame(impl.CapsuleDatagram, body)
}

func splitDatagramFrame(frame []byte) (uint64, []byte, bool) {
	if len(frame) < 2 {
		return 0, nil, false
	}
	flowID, n := uint64(0), 0
	for i := 0; i < len(frame); i++ {
		flowID |= uint64(frame[i]&0x7f) << (7 * uint(n))
		n++
		if frame[i] < 0x80 {
			return flowID, frame[n:], true
		}
	}
	return 0, nil, false
}

func newRouter() *impl.CapsuleRouter {
	return impl.NewCapsuleRouter(impl.CapsuleLimits{
		MaxFlows:         4,
		MaxQueuePerFlow:  2,
		MaxCapsuleBytes:  64,
		MaxDatagramBytes: 8,
	})
}

func TestOpenFlowRejectsZeroID(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(0); !errors.Is(err, impl.ErrZeroFlowID) {
		t.Fatalf("flow id zero must be rejected with ErrZeroFlowID, got %v", err)
	}
	if ids := r.FlowIDs(); len(ids) != 0 {
		t.Fatalf("flow table must stay empty, got %v", ids)
	}
}

func TestOpenFlowRejectsDuplicate(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(7); err != nil {
		t.Fatalf("first open must succeed, got %v", err)
	}
	if err := r.OpenFlow(7); !errors.Is(err, impl.ErrDuplicateFlow) {
		t.Fatalf("second open of the same flow must report ErrDuplicateFlow, got %v", err)
	}
	if ids := r.FlowIDs(); len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("flow table = %v, want exactly [7]", ids)
	}
}

func TestOpenFlowEnforcesFlowLimit(t *testing.T) {
	r := impl.NewCapsuleRouter(impl.CapsuleLimits{
		MaxFlows:         2,
		MaxQueuePerFlow:  2,
		MaxCapsuleBytes:  64,
		MaxDatagramBytes: 8,
	})
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("first open must succeed, got %v", err)
	}
	if err := r.OpenFlow(2); err != nil {
		t.Fatalf("second open must succeed, got %v", err)
	}
	if err := r.OpenFlow(3); !errors.Is(err, impl.ErrFlowLimit) {
		t.Fatalf("open past the flow cap must report ErrFlowLimit, got %v", err)
	}
	ids := r.FlowIDs()
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("flow table = %v, want [1 2] in ascending order", ids)
	}
}

func TestFeedReassemblesCapsuleSplitAcrossChunks(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	frame := datagramFrame(1, []byte("abc"))
	if len(frame) != 6 {
		t.Fatalf("test frame length = %d, want 6", len(frame))
	}
	if err := r.Feed(frame[:3]); err != nil {
		t.Fatalf("feeding the first half must not fail, got %v", err)
	}
	if in, _ := r.Pending(1); in != 0 {
		t.Fatalf("a half frame must not produce a datagram, pending = %d", in)
	}
	if err := r.Feed(frame[3:]); err != nil {
		t.Fatalf("feeding the second half must not fail, got %v", err)
	}
	got := r.TakeInbound(1)
	if len(got) != 1 {
		t.Fatalf("reassembled datagrams = %d, want 1", len(got))
	}
	if got[0].Seq != 1 || got[0].FlowID != 1 || string(got[0].Payload) != "abc" {
		t.Fatalf("reassembled datagram = %+v, want flow 1 seq 1 payload abc", got[0])
	}
}

func TestFeedParsesStickyCapsulesInOrder(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow 1 failed: %v", err)
	}
	if err := r.OpenFlow(2); err != nil {
		t.Fatalf("open flow 2 failed: %v", err)
	}
	chunk := append(datagramFrame(1, []byte("a")), datagramFrame(2, []byte("bb"))...)
	chunk = append(chunk, datagramFrame(1, []byte("ccc"))...)
	if err := r.Feed(chunk); err != nil {
		t.Fatalf("sticky chunk must parse, got %v", err)
	}
	one := r.TakeInbound(1)
	if len(one) != 2 {
		t.Fatalf("flow 1 datagrams = %d, want 2", len(one))
	}
	if one[0].Seq != 1 || one[1].Seq != 2 {
		t.Fatalf("flow 1 sequence = [%d %d], want [1 2]", one[0].Seq, one[1].Seq)
	}
	if string(one[0].Payload) != "a" || string(one[1].Payload) != "ccc" {
		t.Fatalf("flow 1 payloads = [%q %q], want [a ccc] in arrival order", one[0].Payload, one[1].Payload)
	}
	two := r.TakeInbound(2)
	if len(two) != 1 || two[0].Seq != 1 || string(two[0].Payload) != "bb" {
		t.Fatalf("flow 2 datagrams = %+v, want one seq 1 payload bb", two)
	}
}

func TestFeedSkipsUnknownCapsules(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	chunk := capsuleFrame(impl.CapsulePadding, []byte("pad"))
	chunk = append(chunk, capsuleFrame(0x42, []byte("x"))...)
	chunk = append(chunk, datagramFrame(1, []byte("z"))...)
	if err := r.Feed(chunk); err != nil {
		t.Fatalf("unknown capsules must be skipped, got %v", err)
	}
	stats := r.Stats()
	if stats.UnknownCapsules != 1 {
		t.Fatalf("unknown capsule count = %d, want 1", stats.UnknownCapsules)
	}
	if in, _ := r.Pending(1); in != 1 {
		t.Fatalf("datagram behind the unknown capsule must still be queued, pending = %d", in)
	}
}

func TestFeedRejectsOversizeCapsule(t *testing.T) {
	r := impl.NewCapsuleRouter(impl.CapsuleLimits{
		MaxFlows:         4,
		MaxQueuePerFlow:  2,
		MaxCapsuleBytes:  16,
		MaxDatagramBytes: 8,
	})
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	big := make([]byte, 200)
	if err := r.Feed(capsuleFrame(impl.CapsuleDatagram, big)); !errors.Is(err, impl.ErrCapsuleTooLarge) {
		t.Fatalf("capsule above the byte cap must report ErrCapsuleTooLarge, got %v", err)
	}
	if got := r.Stats().ProtocolErrors; got != 1 {
		t.Fatalf("protocol error count = %d, want 1", got)
	}
}

func TestFeedDropsDatagramForUnknownFlow(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Feed(datagramFrame(9, []byte("x"))); err != nil {
		t.Fatalf("datagram for an unknown flow must be dropped without a hard error, got %v", err)
	}
	if in, out := r.Pending(1); in != 0 || out != 0 {
		t.Fatalf("known flow queues = %d/%d, want 0/0", in, out)
	}
	if got := r.Stats().DatagramsDropped; got != 1 {
		t.Fatalf("dropped datagram count = %d, want 1", got)
	}
	if ids := r.FlowIDs(); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("an unknown flow datagram must not create a flow, table = %v", ids)
	}
}

func TestDeliverWritesFlowPrefixedCapsule(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(3); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Deliver(3, []byte("hi")); err != nil {
		t.Fatalf("deliver must succeed, got %v", err)
	}
	frames := r.TakeOutbound(3)
	if len(frames) != 1 {
		t.Fatalf("outbound capsules = %d, want 1", len(frames))
	}
	if frames[0].Type != impl.CapsuleDatagram {
		t.Fatalf("outbound capsule type = %d, want %d", frames[0].Type, impl.CapsuleDatagram)
	}
	flowID, body, ok := splitDatagramFrame(frames[0].Payload)
	if !ok || flowID != 3 || string(body) != "hi" {
		t.Fatalf("outbound capsule payload = %v, want flow 3 body hi", frames[0].Payload)
	}
	if _, out := r.Pending(3); out != 0 {
		t.Fatalf("outbound queue after take = %d, want 0", out)
	}
	if got := r.Stats().DatagramsOut; got != 1 {
		t.Fatalf("outbound datagram count = %d, want 1", got)
	}
}

func TestDeliverEnforcesPerFlowQueueLimit(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Deliver(1, []byte("a")); err != nil {
		t.Fatalf("first deliver must succeed, got %v", err)
	}
	if err := r.Deliver(1, []byte("b")); err != nil {
		t.Fatalf("second deliver must succeed, got %v", err)
	}
	if err := r.Deliver(1, []byte("c")); !errors.Is(err, impl.ErrQueueFull) {
		t.Fatalf("deliver past the per flow queue depth must report ErrQueueFull, got %v", err)
	}
	if _, out := r.Pending(1); out != 2 {
		t.Fatalf("outbound queue = %d, want 2", out)
	}
	if got := r.Stats().DatagramsOut; got != 2 {
		t.Fatalf("queued datagram count = %d, want 2", got)
	}
}

func TestDeliverRejectsOversizePayload(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Deliver(1, make([]byte, 9)); !errors.Is(err, impl.ErrPayloadTooLarge) {
		t.Fatalf("payload above the datagram cap must report ErrPayloadTooLarge, got %v", err)
	}
	if _, out := r.Pending(1); out != 0 {
		t.Fatalf("rejected payload must not be queued, outbound = %d", out)
	}
}

func TestCloseFlowDropsBothQueues(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Feed(datagramFrame(1, []byte("x"))); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	if err := r.Deliver(1, []byte("y")); err != nil {
		t.Fatalf("deliver failed: %v", err)
	}
	r.CloseFlow(1)
	if ids := r.FlowIDs(); len(ids) != 0 {
		t.Fatalf("closed flow must leave the table, got %v", ids)
	}
	if err := r.Deliver(1, []byte("z")); !errors.Is(err, impl.ErrUnknownFlow) {
		t.Fatalf("deliver after close must report ErrUnknownFlow, got %v", err)
	}
	if in, out := r.Pending(1); in != 0 || out != 0 {
		t.Fatalf("closed flow queues = %d/%d, want 0/0", in, out)
	}
	if got := r.TakeInbound(1); len(got) != 0 {
		t.Fatalf("closed flow must drop queued inbound datagrams, got %d", len(got))
	}
	if got := r.TakeOutbound(1); len(got) != 0 {
		t.Fatalf("closed flow must drop queued outbound capsules, got %d", len(got))
	}
}

func TestDrainRejectsNewFlows(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Drain(time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("drain with an empty queue must succeed, got %v", err)
	}
	if err := r.OpenFlow(2); !errors.Is(err, impl.ErrDraining) {
		t.Fatalf("open after drain must report ErrDraining, got %v", err)
	}
	if err := r.Deliver(1, []byte("still")); err != nil {
		t.Fatalf("existing flows must stay deliverable while draining, got %v", err)
	}
}

func TestDrainTimesOutWithBacklog(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow failed: %v", err)
	}
	if err := r.Deliver(1, []byte("a")); err != nil {
		t.Fatalf("deliver failed: %v", err)
	}
	if err := r.Deliver(1, []byte("b")); err != nil {
		t.Fatalf("deliver failed: %v", err)
	}
	past := time.Now().Add(-time.Second)
	if err := r.Drain(past); !errors.Is(err, impl.ErrDrainTimeout) {
		t.Fatalf("drain past the deadline with a backlog must report ErrDrainTimeout, got %v", err)
	}
	if got := r.Stats().DrainTimeouts; got != 1 {
		t.Fatalf("drain timeout count = %d, want 1", got)
	}
	r.TakeOutbound(1)
	if err := r.Drain(past); err != nil {
		t.Fatalf("drain after the backlog cleared must succeed, got %v", err)
	}
}

func TestSnapshotRestoreKeepsQueuesWithoutResend(t *testing.T) {
	r := newRouter()
	if err := r.OpenFlow(1); err != nil {
		t.Fatalf("open flow 1 failed: %v", err)
	}
	if err := r.OpenFlow(2); err != nil {
		t.Fatalf("open flow 2 failed: %v", err)
	}
	if err := r.Feed(datagramFrame(1, []byte("a"))); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	if err := r.Feed(datagramFrame(1, []byte("b"))); err != nil {
		t.Fatalf("feed failed: %v", err)
	}
	if err := r.Deliver(1, []byte("o1")); err != nil {
		t.Fatalf("deliver failed: %v", err)
	}
	if err := r.Deliver(1, []byte("o2")); err != nil {
		t.Fatalf("deliver failed: %v", err)
	}
	sent := r.TakeOutbound(1)
	if len(sent) != 2 {
		t.Fatalf("taking the outbound queue must return both capsules, got %d", len(sent))
	}
	if err := r.Deliver(1, []byte("o3")); err != nil {
		t.Fatalf("deliver after the flush failed: %v", err)
	}
	snap, err := r.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := newRouter()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	ids := restored.FlowIDs()
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("restored flow table = %v, want [1 2]", ids)
	}
	if in, out := restored.Pending(1); in != 2 || out != 1 {
		t.Fatalf("restored queues = %d/%d, want inbound 2 outbound 1 without the already sent capsule", in, out)
	}
	got := restored.TakeInbound(1)
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("restored inbound = %+v, want seq 1 then 2", got)
	}
	if err := restored.Deliver(2, []byte("ok")); err != nil {
		t.Fatalf("restored flow 2 must accept a reply, got %v", err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage snapshot must report ErrBadSnapshot, got %v", err)
	}
}
