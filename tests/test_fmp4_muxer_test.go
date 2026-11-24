package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-c-14"
)

func mustMuxer(t *testing.T, limit int) *impl.Fmp4Muxer {
	t.Helper()
	m, err := impl.NewFmp4Muxer(impl.MuxConfig{FragmentLimit: limit})
	if err != nil {
		t.Fatalf("build muxer with limit %d: %v", limit, err)
	}
	return m
}

func mustTrack(t *testing.T, m *impl.Fmp4Muxer, id uint32, timescale uint32) {
	t.Helper()
	if err := m.AddTrack(impl.TrackConfig{ID: id, Timescale: timescale, Handler: "vide"}); err != nil {
		t.Fatalf("add track %d: %v", id, err)
	}
}

func TestNewMuxerRejectsNegativeLimit(t *testing.T) {
	if _, err := impl.NewFmp4Muxer(impl.MuxConfig{FragmentLimit: -5}); !errors.Is(err, impl.ErrLimitRejected) {
		t.Fatalf("negative fragment limit returned %v, want ErrLimitRejected", err)
	}
}

func TestAddTrackRejectsZeroTimescale(t *testing.T) {
	m := mustMuxer(t, 0)
	if err := m.AddTrack(impl.TrackConfig{ID: 1, Timescale: 0}); !errors.Is(err, impl.ErrTrackRejected) {
		t.Fatalf("zero timescale returned %v, want ErrTrackRejected", err)
	}
	if got := m.TrackCount(); got != 0 {
		t.Fatalf("track count = %d after rejected track, want 0", got)
	}
}

func TestAddTrackRejectsDuplicateID(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.AddTrack(impl.TrackConfig{ID: 1, Timescale: 90000}); !errors.Is(err, impl.ErrTrackRejected) {
		t.Fatalf("duplicate track id returned %v, want ErrTrackRejected", err)
	}
	if got := m.TrackCount(); got != 1 {
		t.Fatalf("track count = %d after duplicate track, want 1", got)
	}
}

func TestWriteSampleRequiresKnownTrack(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 2, DTS: 0, Duration: 1000, Size: 100}); !errors.Is(err, impl.ErrTrackRejected) {
		t.Fatalf("unknown track sample returned %v, want ErrTrackRejected", err)
	}
	if got := m.PendingBytes(); got != 0 {
		t.Fatalf("pending bytes = %d after rejected sample, want 0", got)
	}
}

func TestWriteSampleRejectsNonPositiveDuration(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 0, Duration: 0, Size: 100}); !errors.Is(err, impl.ErrSampleRejected) {
		t.Fatalf("zero duration sample returned %v, want ErrSampleRejected", err)
	}
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 0, Duration: -1000, Size: 100}); !errors.Is(err, impl.ErrSampleRejected) {
		t.Fatalf("negative duration sample returned %v, want ErrSampleRejected", err)
	}
	if got := m.PendingBytes(); got != 0 {
		t.Fatalf("pending bytes = %d after rejected samples, want 0", got)
	}
}

func TestDuplicateSampleIsIdempotent(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	s := impl.Sample{TrackID: 1, DTS: 0, Duration: 1000, Size: 100}
	if err := m.WriteSample(s); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := m.WriteSample(s); err != nil {
		t.Fatalf("duplicate write: %v", err)
	}
	if got := m.PendingBytes(); got != 100 {
		t.Fatalf("pending bytes = %d after duplicate sample, want 100", got)
	}
	stats := m.Stats()
	if stats.Duplicates != 1 {
		t.Fatalf("duplicate counter = %d, want 1", stats.Duplicates)
	}
	if stats.Samples != 1 {
		t.Fatalf("sample counter = %d after duplicate, want 1", stats.Samples)
	}
}

func TestDTSRegressionRejected(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 1000, Duration: 1000, Size: 100}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 500, Duration: 1000, Size: 100}); !errors.Is(err, impl.ErrDtsRegressed) {
		t.Fatalf("regressed decode time returned %v, want ErrDtsRegressed", err)
	}
	if got := m.PendingBytes(); got != 100 {
		t.Fatalf("pending bytes = %d after regression, want 100 kept", got)
	}
}

func TestEmptyFlushRejected(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if _, err := m.FlushFragment(); !errors.Is(err, impl.ErrEmptyFragment) {
		t.Fatalf("empty flush returned %v, want ErrEmptyFragment", err)
	}
	if got := m.Sequence(); got != 1 {
		t.Fatalf("sequence after empty flush = %d, want 1", got)
	}
}

func TestFragmentSequenceStartsAtOne(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 0, Duration: 1000, Size: 100, Keyframe: true}); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	frag, err := m.FlushFragment()
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if frag.Sequence != 1 {
		t.Fatalf("first fragment sequence = %d, want 1", frag.Sequence)
	}
	if got := m.Sequence(); got != 2 {
		t.Fatalf("next fragment sequence = %d, want 2", got)
	}
}

func TestFragmentOffsetsCumulative(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	for i, size := range []int{10, 20, 30} {
		s := impl.Sample{TrackID: 1, DTS: int64(i) * 1000, Duration: 1000, Size: size}
		if err := m.WriteSample(s); err != nil {
			t.Fatalf("write sample %d: %v", i, err)
		}
	}
	frag, err := m.FlushFragment()
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	want := []int{0, 10, 30}
	if len(frag.Offsets) != len(want) {
		t.Fatalf("offset count = %d, want %d", len(frag.Offsets), len(want))
	}
	for i := range want {
		if frag.Offsets[i] != want[i] {
			t.Fatalf("offset %d = %d, want %d", i, frag.Offsets[i], want[i])
		}
	}
	if frag.Bytes != 60 || frag.SampleCount != 3 {
		t.Fatalf("fragment bytes=%d samples=%d, want 60 and 3", frag.Bytes, frag.SampleCount)
	}
	if len(frag.TrackIDs) != 1 || frag.TrackIDs[0] != 1 {
		t.Fatalf("fragment track ids = %v, want [1]", frag.TrackIDs)
	}
	if got := frag.BaseTime[1]; got != 0 {
		t.Fatalf("base decode time = %d, want 0", got)
	}
}

func TestFragmentLimitSplitsAtKeyframe(t *testing.T) {
	m := mustMuxer(t, 50)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 0, Duration: 1000, Size: 30, Keyframe: true}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 1000, Duration: 1000, Size: 30, Keyframe: true}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if got := m.PendingBytes(); got != 30 {
		t.Fatalf("pending bytes after split = %d, want 30", got)
	}
	if got := m.Sequence(); got != 2 {
		t.Fatalf("sequence after split = %d, want 2", got)
	}
	stats := m.Stats()
	if stats.LimitSplits != 1 || stats.Fragments != 1 {
		t.Fatalf("limit splits=%d fragments=%d, want 1 and 1", stats.LimitSplits, stats.Fragments)
	}
	frag, err := m.FlushFragment()
	if err != nil {
		t.Fatalf("flush remaining: %v", err)
	}
	if frag.Sequence != 2 || frag.Bytes != 30 || frag.SampleCount != 1 {
		t.Fatalf("second fragment = seq %d bytes %d samples %d, want 2/30/1", frag.Sequence, frag.Bytes, frag.SampleCount)
	}
}

func TestSetFragmentLimitRejectsNonPositive(t *testing.T) {
	m := mustMuxer(t, 100)
	if err := m.SetFragmentLimit(0); !errors.Is(err, impl.ErrLimitRejected) {
		t.Fatalf("zero limit returned %v, want ErrLimitRejected", err)
	}
	if err := m.SetFragmentLimit(-1); !errors.Is(err, impl.ErrLimitRejected) {
		t.Fatalf("negative limit returned %v, want ErrLimitRejected", err)
	}
}

func TestTimescaleChangeLockedAfterSamples(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 0, Duration: 1000, Size: 100}); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	if err := m.SetTrackTimescale(1, 2000); !errors.Is(err, impl.ErrTimescaleLocked) {
		t.Fatalf("timescale change returned %v, want ErrTimescaleLocked", err)
	}
	if _, err := m.BaseDecodeTime(1); err != nil {
		t.Fatalf("base decode time: %v", err)
	}
}

func TestSnapshotRestoreKeepsSequenceAndPending(t *testing.T) {
	m := mustMuxer(t, 0)
	mustTrack(t, m, 1, 1000)
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 0, Duration: 1000, Size: 20, Keyframe: true}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := m.WriteSample(impl.Sample{TrackID: 1, DTS: 1000, Duration: 1000, Size: 30}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	snap, err := m.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := impl.NewFmp4Muxer(impl.MuxConfig{})
	if err != nil {
		t.Fatalf("build second muxer: %v", err)
	}
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.PendingBytes(); got != 50 {
		t.Fatalf("pending bytes after restore = %d, want 50", got)
	}
	if got := restored.Sequence(); got != 1 {
		t.Fatalf("sequence after restore = %d, want 1", got)
	}
	if got := restored.TrackCount(); got != 1 {
		t.Fatalf("track count after restore = %d, want 1", got)
	}
	frag, err := restored.FlushFragment()
	if err != nil {
		t.Fatalf("flush after restore: %v", err)
	}
	if frag.SampleCount != 2 || frag.Bytes != 50 {
		t.Fatalf("fragment after restore = samples %d bytes %d, want 2/50", frag.SampleCount, frag.Bytes)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	m := mustMuxer(t, 0)
	if err := m.Restore([]byte("broken muxer state")); !errors.Is(err, impl.ErrMuxerSnapshot) {
		t.Fatalf("corrupt restore returned %v, want ErrMuxerSnapshot", err)
	}
	if got := m.TrackCount(); got != 0 {
		t.Fatalf("track count after corrupt restore = %d, want 0 untouched", got)
	}
}
