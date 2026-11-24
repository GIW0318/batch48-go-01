package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-a-18"
)

func newTracker(t *testing.T) *impl.KeyUpdateTracker {
	t.Helper()
	tr, err := impl.NewKeyUpdateTracker(impl.KeyUpdateConfig{MaxEpochs: 8, EpochTolerance: 2})
	if err != nil {
		t.Fatalf("tracker build failed: %v", err)
	}
	return tr
}

func TestNewTrackerValidatesConfig(t *testing.T) {
	if _, err := impl.NewKeyUpdateTracker(impl.KeyUpdateConfig{MaxEpochs: 1}); !errors.Is(err, impl.ErrInvalidConfig) {
		t.Fatalf("MaxEpochs below two must report ErrInvalidConfig, got %v", err)
	}
	tr, err := impl.NewKeyUpdateTracker(impl.KeyUpdateConfig{MaxEpochs: 2})
	if err != nil || tr == nil {
		t.Fatalf("a two epoch config must build a tracker, got (%v, %v)", tr, err)
	}
}

func TestInitialEpochsAreIndependent(t *testing.T) {
	tr := newTracker(t)
	if got := tr.EpochOf(impl.DirClientToProxy); got != 1 {
		t.Fatalf("client to proxy epoch = %d, want 1", got)
	}
	if got := tr.EpochOf(impl.DirProxyToClient); got != 1 {
		t.Fatalf("proxy to client epoch = %d, want 1", got)
	}
	if got := tr.NextSeq(impl.DirClientToProxy); got != 0 {
		t.Fatalf("client to proxy next sequence = %d, want 0", got)
	}
	if got := tr.NextSeq(impl.DirProxyToClient); got != 0 {
		t.Fatalf("proxy to client next sequence = %d, want 0", got)
	}
}

func TestObserveRecordAdvancesSequence(t *testing.T) {
	tr := newTracker(t)
	for seq := uint64(0); seq < 3; seq++ {
		if err := tr.ObserveRecord(impl.DirClientToProxy, 1, seq); err != nil {
			t.Fatalf("record %d in order must be accepted, got %v", seq, err)
		}
	}
	if got := tr.NextSeq(impl.DirClientToProxy); got != 3 {
		t.Fatalf("next sequence = %d, want 3", got)
	}
	if got := tr.Stats().Records; got != 3 {
		t.Fatalf("record counter = %d, want 3", got)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 1, 2); !errors.Is(err, impl.ErrReplayRecord) {
		t.Fatalf("a replayed record must report ErrReplayRecord, got %v", err)
	}
	if got := tr.Stats().ReplayedRecords; got != 1 {
		t.Fatalf("replayed record counter = %d, want 1", got)
	}
	if got := tr.NextSeq(impl.DirClientToProxy); got != 3 {
		t.Fatalf("next sequence after a replay = %d, want 3", got)
	}
}

func TestObserveRecordRejectsInvalidDirection(t *testing.T) {
	tr := newTracker(t)
	if err := tr.ObserveRecord(impl.Direction(0), 1, 0); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("zero direction must report ErrInvalidDirection, got %v", err)
	}
	if err := tr.ObserveRecord(impl.Direction(9), 1, 0); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("unknown direction must report ErrInvalidDirection, got %v", err)
	}
	if got := tr.Stats().Records; got != 0 {
		t.Fatalf("record counter after invalid directions = %d, want 0", got)
	}
}

func TestObserveRecordRejectsEpochGap(t *testing.T) {
	tr := newTracker(t)
	if err := tr.ObserveRecord(impl.DirClientToProxy, 3, 0); !errors.Is(err, impl.ErrEpochGap) {
		t.Fatalf("an epoch ahead of the current one must report ErrEpochGap, got %v", err)
	}
	if got := tr.Stats().GapRejected; got != 1 {
		t.Fatalf("gap counter = %d, want 1", got)
	}
	if got := tr.EpochOf(impl.DirClientToProxy); got != 1 {
		t.Fatalf("epoch after a gap = %d, want 1", got)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 1, 0); err != nil {
		t.Fatalf("a record in the current epoch must still be accepted, got %v", err)
	}
}

func TestObserveRecordRejectsOldEpochBeyondTolerance(t *testing.T) {
	tr, err := impl.NewKeyUpdateTracker(impl.KeyUpdateConfig{MaxEpochs: 8, EpochTolerance: 0})
	if err != nil {
		t.Fatalf("tracker build failed: %v", err)
	}
	if _, err := tr.OnKeyUpdate(impl.DirClientToProxy, false); err != nil {
		t.Fatalf("key update must succeed, got %v", err)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 1, 0); !errors.Is(err, impl.ErrEpochTooOld) {
		t.Fatalf("an epoch outside the tolerance window must report ErrEpochTooOld, got %v", err)
	}
	if got := tr.Stats().TooOldRejected; got != 1 {
		t.Fatalf("too old counter = %d, want 1", got)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 2, 0); err != nil {
		t.Fatalf("a record in the new epoch must be accepted, got %v", err)
	}
}

func TestOnKeyUpdateAdvancesEpochAndResetsSequence(t *testing.T) {
	tr := newTracker(t)
	respond, err := tr.OnKeyUpdate(impl.DirClientToProxy, false)
	if err != nil || respond {
		t.Fatalf("key update without request = (%v, %v), want (false, nil)", respond, err)
	}
	if got := tr.EpochOf(impl.DirClientToProxy); got != 2 {
		t.Fatalf("epoch after the key update = %d, want 2", got)
	}
	if got := tr.NextSeq(impl.DirClientToProxy); got != 0 {
		t.Fatalf("next sequence after the key update = %d, want 0", got)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 2, 0); err != nil {
		t.Fatalf("first record of the new epoch must be accepted, got %v", err)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 2, 1); err != nil {
		t.Fatalf("second record of the new epoch must be accepted, got %v", err)
	}
	stats := tr.Stats()
	if stats.PeerUpdates != 1 {
		t.Fatalf("peer update counter = %d, want 1", stats.PeerUpdates)
	}
	if stats.EpochRollovers != 1 {
		t.Fatalf("epoch rollover counter = %d, want 1", stats.EpochRollovers)
	}
}

func TestOnKeyUpdateRequestedSchedulesResponse(t *testing.T) {
	tr := newTracker(t)
	respond, err := tr.OnKeyUpdate(impl.DirClientToProxy, true)
	if err != nil || !respond {
		t.Fatalf("requested key update = (%v, %v), want (true, nil)", respond, err)
	}
	if err := tr.MarkResponseSent(impl.DirProxyToClient); err != nil {
		t.Fatalf("marking the response written must succeed, got %v", err)
	}
	if got := tr.Stats().ResponsesWritten; got != 1 {
		t.Fatalf("response counter = %d, want 1", got)
	}
	if err := tr.MarkResponseSent(impl.DirProxyToClient); !errors.Is(err, impl.ErrNoPendingResponse) {
		t.Fatalf("a second response mark must report ErrNoPendingResponse, got %v", err)
	}
}

func TestMarkResponseSentWithoutRequest(t *testing.T) {
	tr := newTracker(t)
	if err := tr.MarkResponseSent(impl.DirProxyToClient); !errors.Is(err, impl.ErrNoPendingResponse) {
		t.Fatalf("marking a response without a request must report ErrNoPendingResponse, got %v", err)
	}
	if got := tr.Stats().ResponsesWritten; got != 0 {
		t.Fatalf("response counter = %d, want 0", got)
	}
}

func TestRequestUpdateCoalesces(t *testing.T) {
	tr := newTracker(t)
	if err := tr.RequestUpdate(impl.DirClientToProxy); err != nil {
		t.Fatalf("first local update request must succeed, got %v", err)
	}
	if got := tr.EpochOf(impl.DirClientToProxy); got != 2 {
		t.Fatalf("epoch after the request = %d, want 2", got)
	}
	if err := tr.RequestUpdate(impl.DirClientToProxy); err != nil {
		t.Fatalf("repeated request must be merged without an error, got %v", err)
	}
	if err := tr.RequestUpdate(impl.DirClientToProxy); err != nil {
		t.Fatalf("third request must be merged without an error, got %v", err)
	}
	if got := tr.EpochOf(impl.DirClientToProxy); got != 2 {
		t.Fatalf("epoch after merged requests = %d, want 2", got)
	}
	stats := tr.Stats()
	if stats.LocalUpdates != 1 || stats.CoalescedRequests != 2 {
		t.Fatalf("local updates/coalesced = %d/%d, want 1/2 without double counting", stats.LocalUpdates, stats.CoalescedRequests)
	}
	if err := tr.MarkUpdateSent(impl.DirClientToProxy); err != nil {
		t.Fatalf("marking the local update written must succeed, got %v", err)
	}
	if got := tr.Stats().UpdatesWritten; got != 1 {
		t.Fatalf("written update counter = %d, want 1", got)
	}
	if err := tr.MarkUpdateSent(impl.DirClientToProxy); !errors.Is(err, impl.ErrNoPendingUpdate) {
		t.Fatalf("a second update mark must report ErrNoPendingUpdate, got %v", err)
	}
}

func TestEpochLimitClosesTracker(t *testing.T) {
	tr, err := impl.NewKeyUpdateTracker(impl.KeyUpdateConfig{MaxEpochs: 3, EpochTolerance: 0})
	if err != nil {
		t.Fatalf("tracker build failed: %v", err)
	}
	if _, err := tr.OnKeyUpdate(impl.DirClientToProxy, false); err != nil {
		t.Fatalf("first rollover must succeed, got %v", err)
	}
	if _, err := tr.OnKeyUpdate(impl.DirClientToProxy, false); err != nil {
		t.Fatalf("second rollover must succeed, got %v", err)
	}
	if got := tr.EpochOf(impl.DirClientToProxy); got != 3 {
		t.Fatalf("epoch after two rollovers = %d, want 3", got)
	}
	if _, err := tr.OnKeyUpdate(impl.DirClientToProxy, false); !errors.Is(err, impl.ErrEpochExhausted) {
		t.Fatalf("rollover past the cap must report ErrEpochExhausted, got %v", err)
	}
	if !tr.Stats().Closing {
		t.Fatalf("tracker must be closing after the epoch cap was hit")
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 3, 0); !errors.Is(err, impl.ErrClosing) {
		t.Fatalf("records on a closing tracker must report ErrClosing, got %v", err)
	}
	if err := tr.RequestUpdate(impl.DirProxyToClient); !errors.Is(err, impl.ErrClosing) {
		t.Fatalf("update requests on a closing tracker must report ErrClosing, got %v", err)
	}
}

func TestOnKeyUpdateWithoutRequestDoesNotScheduleResponse(t *testing.T) {
	tr := newTracker(t)
	respond, err := tr.OnKeyUpdate(impl.DirClientToProxy, false)
	if err != nil || respond {
		t.Fatalf("key update without request = (%v, %v), want (false, nil)", respond, err)
	}
	if err := tr.MarkResponseSent(impl.DirProxyToClient); !errors.Is(err, impl.ErrNoPendingResponse) {
		t.Fatalf("no response may become pending without a request, got %v", err)
	}
	if got := tr.Stats().ResponsesWritten; got != 0 {
		t.Fatalf("response counter = %d, want 0", got)
	}
}

func TestInvalidDirectionRejected(t *testing.T) {
	tr := newTracker(t)
	if err := tr.RequestUpdate(impl.Direction(0)); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("zero direction on request must report ErrInvalidDirection, got %v", err)
	}
	if err := tr.RequestUpdate(impl.Direction(9)); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("unknown direction on request must report ErrInvalidDirection, got %v", err)
	}
	if respond, err := tr.OnKeyUpdate(impl.Direction(0), false); !errors.Is(err, impl.ErrInvalidDirection) || respond {
		t.Fatalf("zero direction on key update = (%v, %v), want (false, ErrInvalidDirection)", respond, err)
	}
	if err := tr.MarkUpdateSent(impl.Direction(0)); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("zero direction on update mark must report ErrInvalidDirection, got %v", err)
	}
	if err := tr.MarkResponseSent(impl.Direction(0)); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("zero direction on response mark must report ErrInvalidDirection, got %v", err)
	}
	if _, err := tr.ResponseEpoch(impl.Direction(0)); !errors.Is(err, impl.ErrInvalidDirection) {
		t.Fatalf("zero direction on response epoch must report ErrInvalidDirection, got %v", err)
	}
}

func TestSnapshotRestoreKeepsEpochsAndSequence(t *testing.T) {
	tr := newTracker(t)
	if err := tr.ObserveRecord(impl.DirClientToProxy, 1, 0); err != nil {
		t.Fatalf("record 0 failed: %v", err)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 1, 1); err != nil {
		t.Fatalf("record 1 failed: %v", err)
	}
	if _, err := tr.OnKeyUpdate(impl.DirClientToProxy, false); err != nil {
		t.Fatalf("key update failed: %v", err)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 2, 0); err != nil {
		t.Fatalf("record in the new epoch failed: %v", err)
	}
	if err := tr.ObserveRecord(impl.DirProxyToClient, 1, 0); err != nil {
		t.Fatalf("opposite direction record failed: %v", err)
	}
	snap, err := tr.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := newTracker(t)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.EpochOf(impl.DirClientToProxy); got != 2 {
		t.Fatalf("restored client to proxy epoch = %d, want 2", got)
	}
	if got := restored.NextSeq(impl.DirClientToProxy); got != 1 {
		t.Fatalf("restored client to proxy sequence = %d, want 1", got)
	}
	if got := restored.EpochOf(impl.DirProxyToClient); got != 1 {
		t.Fatalf("restored proxy to client epoch = %d, want 1", got)
	}
	if got := restored.NextSeq(impl.DirProxyToClient); got != 1 {
		t.Fatalf("restored proxy to client sequence = %d, want 1", got)
	}
	if err := restored.ObserveRecord(impl.DirClientToProxy, 2, 1); err != nil {
		t.Fatalf("record after restore must continue the sequence, got %v", err)
	}
	if got := restored.Stats().Records; got != 5 {
		t.Fatalf("restored record counter = %d, want 5", got)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage snapshot must report ErrBadSnapshot, got %v", err)
	}
	if got := restored.NextSeq(impl.DirClientToProxy); got != 2 {
		t.Fatalf("sequence after a rejected restore = %d, want 2", got)
	}
}

func TestObserveRecordRejectsSequenceAfterEpochReset(t *testing.T) {
	tr := newTracker(t)
	if _, err := tr.OnKeyUpdate(impl.DirClientToProxy, false); err != nil {
		t.Fatalf("key update failed: %v", err)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 2, 5); !errors.Is(err, impl.ErrReplayRecord) {
		t.Fatalf("a new epoch must restart at sequence zero, got %v", err)
	}
	if got := tr.NextSeq(impl.DirClientToProxy); got != 0 {
		t.Fatalf("next sequence after the rejected record = %d, want 0", got)
	}
	if got := tr.Stats().ReplayedRecords; got != 1 {
		t.Fatalf("rejected sequence counter = %d, want 1", got)
	}
	if err := tr.ObserveRecord(impl.DirClientToProxy, 2, 0); err != nil {
		t.Fatalf("sequence zero of the new epoch must be accepted, got %v", err)
	}
	if got := tr.NextSeq(impl.DirClientToProxy); got != 1 {
		t.Fatalf("next sequence after acceptance = %d, want 1", got)
	}
}
