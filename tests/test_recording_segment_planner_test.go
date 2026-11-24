package task

import (
	"testing"

	impl "gsb/batch48-go-game-c-09"
)

func segmentLimits() impl.Limits {
	return impl.Limits{MinDurMs: 800, MaxDurMs: 5000, KeepUploaded: 8, MaxAttempts: 5, MaxFrameBytes: 4096}
}

func newSegmentPlanner(t *testing.T) *impl.SegmentPlanner {
	t.Helper()
	p, err := impl.NewSegmentPlanner(segmentLimits())
	if err != nil {
		t.Fatalf("NewSegmentPlanner returned error: %v", err)
	}
	if p == nil {
		t.Fatal("NewSegmentPlanner returned nil")
	}
	return p
}

func pushRecordingFrame(t *testing.T, p *impl.SegmentPlanner, pts int64, keyframe bool, size int) {
	t.Helper()
	if err := p.Push(impl.Frame{PTSMs: pts, Keyframe: keyframe, Size: size}); err != nil {
		t.Fatalf("Push(%d) returned error: %v", pts, err)
	}
}

func threeRecordingSegments(t *testing.T) (*impl.SegmentPlanner, []impl.Segment) {
	t.Helper()
	p := newSegmentPlanner(t)
	for _, pts := range []int64{0, 1000, 2000, 3000} {
		pushRecordingFrame(t, p, pts, true, 100)
	}
	segs, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("planned segments = %d, want 3", len(segs))
	}
	return p, segs
}

func TestNewRejectsInvalidDurationLimits(t *testing.T) {
	bad := impl.Limits{MinDurMs: 800, MaxDurMs: 500, KeepUploaded: 4, MaxAttempts: 3, MaxFrameBytes: 4096}
	if _, err := impl.NewSegmentPlanner(bad); err != impl.ErrLimitsNotUsable {
		t.Fatalf("expected ErrLimitsNotUsable, got %v", err)
	}
	zero := impl.Limits{MinDurMs: 0, MaxDurMs: 500, KeepUploaded: 4, MaxAttempts: 3, MaxFrameBytes: 4096}
	if _, err := impl.NewSegmentPlanner(zero); err != impl.ErrLimitsNotUsable {
		t.Fatalf("expected ErrLimitsNotUsable for a zero minimum duration, got %v", err)
	}
}

func TestPushRejectsNonMonotonicPTS(t *testing.T) {
	p := newSegmentPlanner(t)
	pushRecordingFrame(t, p, 1000, true, 100)
	err := p.Push(impl.Frame{PTSMs: 1000, Keyframe: true, Size: 100})
	if err != impl.ErrNonMonotonicPTS {
		t.Fatalf("expected ErrNonMonotonicPTS, got %v", err)
	}
	if got := p.Stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want the rejected frame not to be counted", got)
	}
}

func TestPushRejectsOversizedFrame(t *testing.T) {
	p := newSegmentPlanner(t)
	err := p.Push(impl.Frame{PTSMs: 0, Keyframe: true, Size: 5000})
	if err != impl.ErrFrameTooLarge {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestPushRejectsNonKeyframeStart(t *testing.T) {
	p := newSegmentPlanner(t)
	err := p.Push(impl.Frame{PTSMs: 0, Keyframe: false, Size: 100})
	if err != impl.ErrKeyframeRequired {
		t.Fatalf("expected ErrKeyframeRequired, got %v", err)
	}
}

func TestKeyframeBeforeMinimumDoesNotCut(t *testing.T) {
	p := newSegmentPlanner(t)
	pushRecordingFrame(t, p, 0, true, 100)
	pushRecordingFrame(t, p, 500, true, 100)
	pushRecordingFrame(t, p, 1200, false, 100)
	pushRecordingFrame(t, p, 2000, true, 100)
	segs, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("planned segments = %d, want 1", len(segs))
	}
	if segs[0].StartMs != 0 || segs[0].EndMs != 2000 {
		t.Fatalf("segment span = %d..%d, want 0..2000", segs[0].StartMs, segs[0].EndMs)
	}
	if segs[0].Forced {
		t.Fatal("a keyframe cut must not be marked as forced")
	}
}

func TestSegmentSequenceIsMonotonic(t *testing.T) {
	_, segs := threeRecordingSegments(t)
	for i, seg := range segs {
		if seg.Seq != i+1 {
			t.Fatalf("segment %d has seq %d, want %d", i, seg.Seq, i+1)
		}
	}
}

func TestSegmentKeysAreUniqueAndResolveUploads(t *testing.T) {
	p, segs := threeRecordingSegments(t)
	if segs[0].Key == "" || segs[0].Key == segs[1].Key {
		t.Fatalf("segment keys %q and %q must be non empty and unique", segs[0].Key, segs[1].Key)
	}
	if err := p.MarkUploaded(segs[0].Key); err != nil {
		t.Fatalf("MarkUploaded returned error: %v", err)
	}
	if got := p.Stats().Uploaded; got != 1 {
		t.Fatalf("uploaded segments = %d, want 1", got)
	}
}

func TestMarkUploadedUnknownKeyRejected(t *testing.T) {
	p := newSegmentPlanner(t)
	if err := p.MarkUploaded("seg-missing"); err != impl.ErrUnknownSegment {
		t.Fatalf("expected ErrUnknownSegment, got %v", err)
	}
}

func TestMarkUploadedIsIdempotent(t *testing.T) {
	p, segs := threeRecordingSegments(t)
	if err := p.MarkUploaded(segs[0].Key); err != nil {
		t.Fatalf("first MarkUploaded returned error: %v", err)
	}
	if err := p.MarkUploaded(segs[0].Key); err != nil {
		t.Fatalf("second MarkUploaded returned error: %v", err)
	}
	if got := p.Stats().Uploaded; got != 1 {
		t.Fatalf("uploaded segments = %d, want the repeat call not to count twice", got)
	}
}

func TestSecondUploadWhileUploadingRejected(t *testing.T) {
	p := newSegmentPlanner(t)
	pushRecordingFrame(t, p, 0, true, 100)
	pushRecordingFrame(t, p, 1000, true, 100)
	segs, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("planned segments = %d, want 1", len(segs))
	}
	key := segs[0].Key
	if err := p.MarkUploading(key); err != nil {
		t.Fatalf("MarkUploading returned error: %v", err)
	}
	if err := p.MarkUploading(key); err != impl.ErrUploadInProgress {
		t.Fatalf("expected ErrUploadInProgress, got %v", err)
	}
	if err := p.MarkUploaded(key); err != nil {
		t.Fatalf("MarkUploaded after the download finished returned error: %v", err)
	}
}

func TestRetentionEvictsOldestUploaded(t *testing.T) {
	p, segs := threeRecordingSegments(t)
	for _, seg := range segs {
		if err := p.MarkUploaded(seg.Key); err != nil {
			t.Fatalf("MarkUploaded(%q) returned error: %v", seg.Key, err)
		}
	}
	evicted, err := p.Retention(1)
	if err != nil {
		t.Fatalf("Retention returned error: %v", err)
	}
	if len(evicted) != 2 {
		t.Fatalf("evicted keys = %d, want 2", len(evicted))
	}
	if evicted[0] != segs[0].Key || evicted[1] != segs[1].Key {
		t.Fatalf("evicted keys = %v, want the two oldest in order", evicted)
	}
	if got := p.Stats().Evicted; got != 2 {
		t.Fatalf("evicted counter = %d, want 2", got)
	}
	if got := len(p.Segments()); got != 1 {
		t.Fatalf("segments after retention = %d, want 1", got)
	}
}

func TestReplanIncrementsAttempts(t *testing.T) {
	p := newSegmentPlanner(t)
	pushRecordingFrame(t, p, 0, true, 100)
	pushRecordingFrame(t, p, 1000, true, 100)
	segs, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("planned segments = %d, want 1", len(segs))
	}
	key := segs[0].Key
	retried, err := p.Replan([]string{key})
	if err != nil {
		t.Fatalf("Replan returned error: %v", err)
	}
	if len(retried) != 1 || retried[0].Attempts != 1 {
		t.Fatalf("replanned = %+v, want one segment with one attempt", retried)
	}
	again, err := p.Replan([]string{key})
	if err != nil {
		t.Fatalf("second Replan returned error: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("second replanned segments = %d, want 1", len(again))
	}
	if again[0].Attempts != 2 {
		t.Fatalf("attempts after second Replan = %d, want 2", again[0].Attempts)
	}
	if got := p.Stats().Replanned; got != 2 {
		t.Fatalf("replanned counter = %d, want 2", got)
	}
}

func TestSnapshotRestoreContinuesSequence(t *testing.T) {
	p := newSegmentPlanner(t)
	pushRecordingFrame(t, p, 0, true, 100)
	pushRecordingFrame(t, p, 1000, true, 100)
	pushRecordingFrame(t, p, 2000, true, 100)
	if _, err := p.Plan(); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if got := p.NextSeq(); got != 3 {
		t.Fatalf("next seq before snapshot = %d, want 3", got)
	}
	st, err := p.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	r := newSegmentPlanner(t)
	if err := r.Restore(st); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if got := r.NextSeq(); got != 3 {
		t.Fatalf("next seq after restore = %d, want 3", got)
	}
	pushRecordingFrame(t, r, 3000, true, 100)
	segs, err := r.Plan()
	if err != nil {
		t.Fatalf("Plan after restore returned error: %v", err)
	}
	if len(segs) != 1 || segs[0].Seq != 3 {
		t.Fatalf("post restore segment = %+v, want seq 3", segs)
	}
}

func TestSegmentsReturnsCopies(t *testing.T) {
	p, _ := threeRecordingSegments(t)
	view := p.Segments()
	if len(view) != 3 {
		t.Fatalf("segments = %d, want 3", len(view))
	}
	view[0].Seq = 99
	view[0].Uploaded = true
	if got := p.Segments()[0].Seq; got != 1 {
		t.Fatalf("sequence after mutating the returned slice = %d, want the internal copy untouched", got)
	}
}

func TestStatsCountFramesAndSegments(t *testing.T) {
	p := newSegmentPlanner(t)
	for _, pts := range []int64{0, 1000, 2000, 3000} {
		pushRecordingFrame(t, p, pts, true, 100)
	}
	if _, err := p.Plan(); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	stats := p.Stats()
	if stats.Frames != 4 {
		t.Fatalf("frames = %d, want 4", stats.Frames)
	}
	if stats.Segments != 3 {
		t.Fatalf("segments = %d, want 3", stats.Segments)
	}
	if stats.Bytes != 400 {
		t.Fatalf("bytes = %d, want 400", stats.Bytes)
	}
}
