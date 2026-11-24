package task

import (
	"math"
	"testing"

	impl "gsb/batch48-go-game-c-06"
)

const echoFrameSize = 160

func echoThresholds() impl.Thresholds {
	return impl.Thresholds{
		RefEnter:       0.10,
		RefRelease:     0.05,
		NearEnter:      0.10,
		NearRelease:    0.05,
		MinDwell:       1,
		MaxGainStep:    0.10,
		SuppressGain:   0.08,
		DoubleTalkGain: 0.60,
	}
}

func echoFrame(v float64) []float64 {
	frame := make([]float64, echoFrameSize)
	for i := range frame {
		frame[i] = v
	}
	return frame
}

func newEchoSuppressor(t *testing.T) *impl.EchoSuppressor {
	t.Helper()
	s, err := impl.NewEchoSuppressor(echoFrameSize)
	if err != nil {
		t.Fatalf("NewEchoSuppressor returned error: %v", err)
	}
	if s == nil {
		t.Fatal("NewEchoSuppressor returned nil")
	}
	return s
}

func TestNewRejectsNonPositiveFrameSize(t *testing.T) {
	if _, err := impl.NewEchoSuppressor(0); err == nil {
		t.Fatal("expected error for zero frame size")
	}
	if _, err := impl.NewEchoSuppressor(-64); err == nil {
		t.Fatal("expected error for negative frame size")
	}
}

func TestPushReferenceRejectsWrongLength(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.PushReference(make([]float64, echoFrameSize-1)); err != impl.ErrFrameSize {
		t.Fatalf("expected ErrFrameSize, got %v", err)
	}
	if got := s.Stats(); got.InFrames != 0 || got.FarEndFrames != 0 {
		t.Fatalf("a rejected reference frame must not advance counters, got %+v", got)
	}
}

func TestPushNearEndRejectsWrongLength(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.PushNearEnd(make([]float64, echoFrameSize+3)); err != impl.ErrFrameSize {
		t.Fatalf("expected ErrFrameSize, got %v", err)
	}
	if got := s.Stats().InFrames; got != 0 {
		t.Fatalf("a rejected near end frame must not be counted, got %d", got)
	}
}

func TestFarEndSingleTalkSuppresses(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 20; i++ {
		if err := s.PushReference(echoFrame(1.0)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(0)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
	}
	if got := s.State(); got != impl.StateFarEnd {
		t.Fatalf("state = %q, want %q", got, impl.StateFarEnd)
	}
	if got := s.Gain(); got > 0.2 {
		t.Fatalf("far end gain = %v, want deep suppression below 0.2", got)
	}
	if got := s.Stats().FarEndFrames; got != 20 {
		t.Fatalf("far end frames = %d, want 20", got)
	}
}

func TestNearEndWithoutReferenceKeepsUnityGain(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0.5)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.State(); got != impl.StateNearEnd {
		t.Fatalf("state without reference = %q, want %q", got, impl.StateNearEnd)
	}
	if got := s.Gain(); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("near end gain = %v, want 1.0", got)
	}
	out, ok := s.PopFrame()
	if !ok || len(out) != echoFrameSize {
		t.Fatalf("PopFrame = (len %d, %v), want a full frame", len(out), ok)
	}
	for i := range out {
		if math.Abs(out[i]-0.5) > 1e-9 {
			t.Fatalf("sample %d = %v, want 0.5 passed through", i, out[i])
		}
	}
}

func TestDoubleTalkUsesDoubleTalkGain(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 20; i++ {
		if err := s.PushReference(echoFrame(1.0)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(1.0)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
	}
	if got := s.State(); got != impl.StateDoubleTalk {
		t.Fatalf("state = %q, want %q", got, impl.StateDoubleTalk)
	}
	if got := s.Gain(); got < 0.55 || got > 0.65 {
		t.Fatalf("double talk gain = %v, want about 0.60", got)
	}
	if got := s.Stats().DoubleTalkFrames; got != 20 {
		t.Fatalf("double talk frames = %d, want 20", got)
	}
}

func TestMinimumDwellDelaysStateSwitch(t *testing.T) {
	s := newEchoSuppressor(t)
	th := echoThresholds()
	th.MinDwell = 4
	th.MaxGainStep = 0.5
	if err := s.SetThresholds(th); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.PushReference(echoFrame(1.0)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(0)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
	}
	if got := s.State(); got != impl.StateFarEnd {
		t.Fatalf("state = %q, want %q before dwell test", got, impl.StateFarEnd)
	}
	for i := 0; i < 3; i++ {
		if err := s.PushReference(echoFrame(1.0)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(1.0)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
	}
	if got := s.State(); got != impl.StateFarEnd {
		t.Fatalf("state = %q, want %q while the dwell timer has not expired", got, impl.StateFarEnd)
	}
	if err := s.PushReference(echoFrame(1.0)); err != nil {
		t.Fatalf("PushReference returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(1.0)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.State(); got != impl.StateDoubleTalk {
		t.Fatalf("state = %q, want %q after the dwell timer expired", got, impl.StateDoubleTalk)
	}
}

func TestGainMovesAtMostOneStepPerFrame(t *testing.T) {
	s := newEchoSuppressor(t)
	th := echoThresholds()
	th.MaxGainStep = 0.05
	if err := s.SetThresholds(th); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	if err := s.PushReference(echoFrame(1.0)); err != nil {
		t.Fatalf("PushReference returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.Gain(); math.Abs(got-0.95) > 1e-9 {
		t.Fatalf("gain after one frame = %v, want 0.95 with a 0.05 step cap", got)
	}
	if err := s.PushReference(echoFrame(1.0)); err != nil {
		t.Fatalf("PushReference returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.Gain(); math.Abs(got-0.90) > 1e-9 {
		t.Fatalf("gain after two frames = %v, want 0.90", got)
	}
}

func TestReferenceHysteresisKeepsFarEnd(t *testing.T) {
	s := newEchoSuppressor(t)
	th := echoThresholds()
	th.RefEnter = 0.5
	th.RefRelease = 0.2
	th.MinDwell = 1
	if err := s.SetThresholds(th); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	if err := s.PushReference(echoFrame(1.0)); err != nil {
		t.Fatalf("PushReference returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.State(); got != impl.StateFarEnd {
		t.Fatalf("state = %q, want %q", got, impl.StateFarEnd)
	}
	if err := s.PushReference(echoFrame(0.3)); err != nil {
		t.Fatalf("PushReference returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.State(); got != impl.StateFarEnd {
		t.Fatalf("state = %q, want the reference to stay active above its release gate", got)
	}
	if err := s.PushReference(echoFrame(0.1)); err != nil {
		t.Fatalf("PushReference returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if got := s.State(); got != impl.StateNearEnd {
		t.Fatalf("state = %q, want %q once the reference fell below its release gate", got, impl.StateNearEnd)
	}
}

func TestComfortNoiseCountsFarEndFrames(t *testing.T) {
	s := newEchoSuppressor(t)
	th := echoThresholds()
	th.NearEnter = 0.2
	th.NearRelease = 0.1
	if err := s.SetThresholds(th); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := s.PushReference(echoFrame(1.5)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(0.05)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
		if _, ok := s.PopFrame(); !ok {
			t.Fatalf("frame %d was not queued for output", i)
		}
	}
	if got := s.Stats().ComfortFrames; got != 10 {
		t.Fatalf("comfort frames = %d, want 10", got)
	}
}

func TestSnapshotRestoreKeepsGainAndState(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := s.PushReference(echoFrame(1.0)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(0)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
	}
	before := s.Gain()
	st, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if st.State != impl.StateFarEnd {
		t.Fatalf("snapshot state = %q, want %q", st.State, impl.StateFarEnd)
	}
	if math.Abs(st.Gain-before) > 1e-9 {
		t.Fatalf("snapshot gain = %v, want %v", st.Gain, before)
	}
	r := newEchoSuppressor(t)
	if err := r.Restore(st); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if got := r.State(); got != impl.StateFarEnd {
		t.Fatalf("restored state = %q, want %q", got, impl.StateFarEnd)
	}
	if got := r.Gain(); math.Abs(got-before) > 1e-9 {
		t.Fatalf("restored gain = %v, want %v", got, before)
	}
	if got := r.Stats().InFrames; got != 5 {
		t.Fatalf("restored input frames = %d, want 5", got)
	}
}

func TestResetRestoresDefaults(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.PushReference(echoFrame(1.0)); err != nil {
			t.Fatalf("PushReference returned error: %v", err)
		}
		if err := s.PushNearEnd(echoFrame(0)); err != nil {
			t.Fatalf("PushNearEnd returned error: %v", err)
		}
	}
	s.Reset()
	if got := s.Stats(); got != (impl.Stats{}) {
		t.Fatalf("stats after Reset = %+v, want all zero", got)
	}
	if got := s.State(); got != impl.StateNearEnd {
		t.Fatalf("state after Reset = %q, want %q", got, impl.StateNearEnd)
	}
	if got := s.Gain(); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("gain after Reset = %v, want 1.0", got)
	}
}

func TestSetThresholdsRejectsInvalidValues(t *testing.T) {
	s := newEchoSuppressor(t)
	bad := []impl.Thresholds{
		{RefEnter: 0, RefRelease: -1, NearEnter: 0.1, NearRelease: 0.05, MinDwell: 1, MaxGainStep: 0.1},
		{RefEnter: 0.1, RefRelease: 0.2, NearEnter: 0.1, NearRelease: 0.05, MinDwell: 1, MaxGainStep: 0.1},
		{RefEnter: 0.1, RefRelease: 0.05, NearEnter: 0.1, NearRelease: 0.05, MinDwell: 0, MaxGainStep: 0.1},
		{RefEnter: 0.1, RefRelease: 0.05, NearEnter: 0.1, NearRelease: 0.05, MinDwell: 1, MaxGainStep: 1.5},
	}
	for i, th := range bad {
		if err := s.SetThresholds(th); err != impl.ErrInvalidThreshold {
			t.Fatalf("case %d: expected ErrInvalidThreshold, got %v", i, err)
		}
	}
}

func TestPopFrameDeliversQueuedFramesInOrder(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0.5)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	if err := s.PushNearEnd(echoFrame(0.25)); err != nil {
		t.Fatalf("PushNearEnd returned error: %v", err)
	}
	first, ok := s.PopFrame()
	if !ok || len(first) != echoFrameSize {
		t.Fatalf("first PopFrame = (len %d, %v), want a full frame", len(first), ok)
	}
	if math.Abs(first[0]-0.5) > 1e-9 {
		t.Fatalf("first frame sample = %v, want 0.5", first[0])
	}
	second, ok := s.PopFrame()
	if !ok || len(second) != echoFrameSize {
		t.Fatalf("second PopFrame = (len %d, %v), want a full frame", len(second), ok)
	}
	if math.Abs(second[0]-0.25) > 1e-9 {
		t.Fatalf("second frame sample = %v, want 0.25", second[0])
	}
	if _, ok := s.PopFrame(); ok {
		t.Fatal("PopFrame returned a frame after the queue was drained")
	}
}

func TestOutputQueueCapRejectsOverflow(t *testing.T) {
	s := newEchoSuppressor(t)
	if err := s.SetThresholds(echoThresholds()); err != nil {
		t.Fatalf("SetThresholds returned error: %v", err)
	}
	for i := 0; i < 64; i++ {
		if err := s.PushNearEnd(echoFrame(0.5)); err != nil {
			t.Fatalf("PushNearEnd %d returned error: %v", i, err)
		}
	}
	if err := s.PushNearEnd(echoFrame(0.5)); err != impl.ErrQueueFull {
		t.Fatalf("expected ErrQueueFull beyond the 64 frame queue, got %v", err)
	}
}
