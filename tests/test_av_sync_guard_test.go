package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-c-02"
)

func c02Guard() *impl.AvSyncGuard {
	return impl.NewAvSyncGuard(impl.Thresholds{SyncWindow: 5000, MaxStepPerCall: 2000, SmoothingSamples: 1, AudioQuantum: 0, MaxQueue: 8})
}

func c02Video(pts int64) impl.VideoFrame {
	return impl.VideoFrame{PTS: pts}
}

func c02Audio(duration, position int64) impl.AudioBlock {
	return impl.AudioBlock{PTS: 0, Duration: duration, Position: position}
}

func TestSetMasterClockValidation(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.ClockSource("ntp")); !errors.Is(err, impl.ErrClockUnsupported) {
		t.Fatalf("unknown clock error = %v, want ErrClockUnsupported", err)
	}
	if _, err := g.Decide(); !errors.Is(err, impl.ErrNoMasterClock) {
		t.Fatalf("decide without master error = %v, want ErrNoMasterClock", err)
	}
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if got := g.MasterClock(); got != impl.MasterAudio {
		t.Fatalf("master clock = %q, want audio", got)
	}
}

func TestPushVideoRequiresIncreasingPTS(t *testing.T) {
	g := c02Guard()
	if err := g.PushVideo(c02Video(1000)); err != nil {
		t.Fatalf("push first frame: %v", err)
	}
	if err := g.PushVideo(c02Video(900)); !errors.Is(err, impl.ErrVideoOrder) {
		t.Fatalf("backwards presentation time error = %v, want ErrVideoOrder", err)
	}
	if err := g.PushVideo(c02Video(1000)); !errors.Is(err, impl.ErrVideoOrder) {
		t.Fatalf("repeated presentation time error = %v, want ErrVideoOrder", err)
	}
	v, a := g.QueueLen()
	if v != 1 || a != 0 {
		t.Fatalf("queue = (%d,%d), want one video frame and no audio block", v, a)
	}
}

func TestPushAudioRejectsEmptyBlock(t *testing.T) {
	g := c02Guard()
	if err := g.PushAudio(c02Audio(0, 1000)); !errors.Is(err, impl.ErrAudioBlock) {
		t.Fatalf("zero duration error = %v, want ErrAudioBlock", err)
	}
	if _, a := g.QueueLen(); a != 0 {
		t.Fatalf("rejected audio block must not be queued, queue length %d", a)
	}
	if err := g.PushAudio(c02Audio(20, 1000)); err != nil {
		t.Fatalf("push valid audio block: %v", err)
	}
	if _, a := g.QueueLen(); a != 1 {
		t.Fatalf("audio queue length = %d, want 1", a)
	}
}

func TestDriftEstimateUsesSlidingMean(t *testing.T) {
	g := impl.NewAvSyncGuard(impl.Thresholds{SyncWindow: 100000, MaxStepPerCall: 2000, SmoothingSamples: 3, AudioQuantum: 0, MaxQueue: 8})
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(0)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 20000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	if _, err := g.Decide(); err != nil {
		t.Fatalf("first decide: %v", err)
	}
	if err := g.PushVideo(c02Video(9000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if _, err := g.Decide(); err != nil {
		t.Fatalf("second decide: %v", err)
	}
	if err := g.PushVideo(c02Video(12000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if _, err := g.Decide(); err != nil {
		t.Fatalf("third decide: %v", err)
	}
	if got := g.DriftEstimate(); got != 13000 {
		t.Fatalf("smoothed drift = %d, want the mean of 20000, 11000 and 8000", got)
	}
}

func TestInSyncDecisionKeepsTimeline(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(1000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 3000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	d, err := g.Decide()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Action != impl.ActionKeep {
		t.Fatalf("action = %q, want keep inside the sync window", d.Action)
	}
	if d.Drift != 2000 || d.DriftAfter != 2000 {
		t.Fatalf("drift=%d after=%d, want 2000 and 2000", d.Drift, d.DriftAfter)
	}
	if d.Frame.PTS != 1000 {
		t.Fatalf("decision frame pts = %d, want the queued frame", d.Frame.PTS)
	}
	if got := g.Stats().Drops; got != 0 {
		t.Fatalf("drops = %d, want none for an in sync stream", got)
	}
	if v, _ := g.QueueLen(); v != 1 {
		t.Fatalf("video queue = %d, want the frame kept", v)
	}
}

func TestVideoBehindDropsFrames(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(0)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushVideo(c02Video(1000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 30000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	d, err := g.Decide()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Action != impl.ActionDropVideo {
		t.Fatalf("action = %q, want drop_video when video lags the audio master", d.Action)
	}
	if d.Drift != 30000 || d.DriftAfter != 28000 {
		t.Fatalf("drift=%d after=%d, want 30000 and one capped step of 2000", d.Drift, d.DriftAfter)
	}
	if got := g.Stats().Drops; got != 1 {
		t.Fatalf("drops = %d, want 1", got)
	}
	if v, _ := g.QueueLen(); v != 1 {
		t.Fatalf("video queue = %d, want one frame consumed", v)
	}
}

func TestVideoAheadRepeatsFrame(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(20000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 10000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	d, err := g.Decide()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Action != impl.ActionRepeatVideo {
		t.Fatalf("action = %q, want repeat_video when video runs ahead", d.Action)
	}
	if d.Drift != -10000 || d.DriftAfter != -8000 {
		t.Fatalf("drift=%d after=%d, want -10000 and one capped step", d.Drift, d.DriftAfter)
	}
	if d.RepeatCount != 1 {
		t.Fatalf("repeat count = %d, want 1", d.RepeatCount)
	}
	if got := g.Stats().Repeats; got != 1 {
		t.Fatalf("repeats = %d, want 1", got)
	}
	if v, _ := g.QueueLen(); v != 1 {
		t.Fatalf("video queue = %d, want the frame kept for repeat", v)
	}
}

func TestVideoMasterResamplesAudio(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterVideo); err != nil {
		t.Fatalf("set video master: %v", err)
	}
	if err := g.PushVideo(c02Video(20000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 10000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	d, err := g.Decide()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Action != impl.ActionResampleAudio {
		t.Fatalf("action = %q, want resample_audio when video is the master", d.Action)
	}
	if d.DriftAfter != -8000 {
		t.Fatalf("drift after = %d, want the capped step applied to the audio side", d.DriftAfter)
	}
	if d.ResampleRatio <= 1.0 || d.ResampleRatio >= 1.01 {
		t.Fatalf("resample ratio = %v, want a small speed up above 1", d.ResampleRatio)
	}
	if got := g.Stats().Resamples; got != 1 {
		t.Fatalf("resamples = %d, want 1", got)
	}
}

func TestCompensationStepIsCapped(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	for _, pts := range []int64{0, 1000, 2000} {
		if err := g.PushVideo(c02Video(pts)); err != nil {
			t.Fatalf("push video: %v", err)
		}
	}
	if err := g.PushAudio(c02Audio(20, 42000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	want := []int64{38000, 36000, 34000}
	for i, w := range want {
		d, err := g.Decide()
		if err != nil {
			t.Fatalf("decide %d: %v", i, err)
		}
		if d.DriftAfter != w {
			t.Fatalf("decision %d drift after = %d, want %d", i, d.DriftAfter, w)
		}
	}
}

func TestSetThresholdsValidates(t *testing.T) {
	g := c02Guard()
	bad := impl.Thresholds{SyncWindow: -1, MaxStepPerCall: 2000, SmoothingSamples: 1, MaxQueue: 8}
	if err := g.SetThresholds(bad); !errors.Is(err, impl.ErrThresholdsInvalid) {
		t.Fatalf("negative window error = %v, want ErrThresholdsInvalid", err)
	}
	bad = impl.Thresholds{SyncWindow: 5000, MaxStepPerCall: 0, SmoothingSamples: 1, MaxQueue: 8}
	if err := g.SetThresholds(bad); !errors.Is(err, impl.ErrThresholdsInvalid) {
		t.Fatalf("zero step error = %v, want ErrThresholdsInvalid", err)
	}
	bad = impl.Thresholds{SyncWindow: 5000, MaxStepPerCall: 2000, SmoothingSamples: 0, MaxQueue: 8}
	if err := g.SetThresholds(bad); !errors.Is(err, impl.ErrThresholdsInvalid) {
		t.Fatalf("zero smoothing error = %v, want ErrThresholdsInvalid", err)
	}
	if err := g.SetThresholds(impl.Thresholds{SyncWindow: 1000, MaxStepPerCall: 500, SmoothingSamples: 2, MaxQueue: 4}); err != nil {
		t.Fatalf("valid thresholds rejected: %v", err)
	}
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(0)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 5000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	d, err := g.Decide()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.DriftAfter != 4500 {
		t.Fatalf("drift after = %d, want the new 500 microsecond step applied", d.DriftAfter)
	}
}

func TestResetSourceClearsHistory(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(1000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 10000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	if _, err := g.Decide(); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got := g.DriftEstimate(); got != 9000 {
		t.Fatalf("drift estimate = %d, want 9000 before the source switch", got)
	}
	if err := g.ResetSource(); err != nil {
		t.Fatalf("reset source: %v", err)
	}
	if got := g.DriftEstimate(); got != 0 {
		t.Fatalf("drift estimate = %d, want 0 after the source switch", got)
	}
	if v, a := g.QueueLen(); v != 0 || a != 0 {
		t.Fatalf("queues = (%d,%d), want both cleared", v, a)
	}
	if got := g.Stats().ResetSamples; got != 1 {
		t.Fatalf("reset samples = %d, want 1", got)
	}
	if err := g.PushVideo(c02Video(500)); err != nil {
		t.Fatalf("push after reset must be accepted: %v", err)
	}
	if err := g.PushVideo(c02Video(600)); err != nil {
		t.Fatalf("push after reset must accept a new timeline: %v", err)
	}
}

func TestQueueOverloadDropsOldestFrame(t *testing.T) {
	g := impl.NewAvSyncGuard(impl.Thresholds{SyncWindow: 5000, MaxStepPerCall: 2000, SmoothingSamples: 1, MaxQueue: 3})
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	for _, pts := range []int64{1000, 2000, 3000, 4000} {
		if err := g.PushVideo(c02Video(pts)); err != nil {
			t.Fatalf("push video %d: %v", pts, err)
		}
	}
	if v, _ := g.QueueLen(); v != 3 {
		t.Fatalf("video queue = %d, want the configured cap of 3", v)
	}
	pts := g.PendingPTS()
	want := []int64{2000, 3000, 4000}
	if len(pts) != len(want) {
		t.Fatalf("queued frames = %d, want 3", len(pts))
	}
	for i, w := range want {
		if pts[i] != w {
			t.Fatalf("queued frame %d pts = %d, want %d after dropping the oldest", i, pts[i], w)
		}
	}
	if got := g.Stats().Drops; got != 1 {
		t.Fatalf("drops = %d, want the overloaded frame counted", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(1000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushVideo(c02Video(3000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushAudio(c02Audio(20, 20000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	if _, err := g.Decide(); err != nil {
		t.Fatalf("decide: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	h := c02Guard()
	if err := h.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if h.DriftEstimate() != g.DriftEstimate() {
		t.Fatalf("restored drift = %d, want %d", h.DriftEstimate(), g.DriftEstimate())
	}
	hv, ha := h.QueueLen()
	gv, ga := g.QueueLen()
	if hv != gv || ha != ga {
		t.Fatalf("restored queues = (%d,%d), want (%d,%d)", hv, ha, gv, ga)
	}
	want, err := h.Decide()
	if err != nil {
		t.Fatalf("decide after restore: %v", err)
	}
	got, err := g.Decide()
	if err != nil {
		t.Fatalf("decide on original: %v", err)
	}
	if want.Action != got.Action || want.Drift != got.Drift || want.DriftAfter != got.DriftAfter {
		t.Fatalf("restored decision %+v differs from original %+v", want, got)
	}
	hv, ha = h.QueueLen()
	if err := h.Restore([]byte("junk")); !errors.Is(err, impl.ErrStateCorrupt) {
		t.Fatalf("bad snapshot error = %v, want ErrStateCorrupt", err)
	}
	hv2, ha2 := h.QueueLen()
	if hv2 != hv || ha2 != ha {
		t.Fatalf("bad restore changed live queues from (%d,%d) to (%d,%d)", hv, ha, hv2, ha2)
	}
}

func TestConcurrentPushAndDecide(t *testing.T) {
	g := impl.NewAvSyncGuard(impl.Thresholds{SyncWindow: 5000, MaxStepPerCall: 2000, SmoothingSamples: 1, MaxQueue: 64})
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.PushAudio(c02Audio(20, 1000))
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.Decide()
		}()
	}
	wg.Wait()
	st := g.Stats()
	if st.PushedAudio != 25 {
		t.Fatalf("pushed audio = %d, want every concurrent block counted once", st.PushedAudio)
	}
	if st.Decides != 5 {
		t.Fatalf("decides = %d, want 5", st.Decides)
	}
}

func TestStatsAfterRejections(t *testing.T) {
	g := c02Guard()
	if err := g.SetMasterClock(impl.MasterAudio); err != nil {
		t.Fatalf("set audio master: %v", err)
	}
	if err := g.PushVideo(c02Video(1000)); err != nil {
		t.Fatalf("push video: %v", err)
	}
	if err := g.PushVideo(c02Video(1000)); !errors.Is(err, impl.ErrVideoOrder) {
		t.Fatalf("duplicate video pts error = %v, want ErrVideoOrder", err)
	}
	if err := g.PushAudio(c02Audio(0, 0)); !errors.Is(err, impl.ErrAudioBlock) {
		t.Fatalf("empty audio block error = %v, want ErrAudioBlock", err)
	}
	if err := g.PushAudio(c02Audio(20, 1000)); err != nil {
		t.Fatalf("push audio: %v", err)
	}
	if _, err := g.Decide(); err != nil {
		t.Fatalf("decide: %v", err)
	}
	st := g.Stats()
	if st.PushedVideo != 1 || st.PushedAudio != 1 || st.Rejected != 2 || st.Decides != 1 {
		t.Fatalf("stats video=%d audio=%d rejected=%d decides=%d, want 1,1,2,1",
			st.PushedVideo, st.PushedAudio, st.Rejected, st.Decides)
	}
}
