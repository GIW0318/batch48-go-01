package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-game-c-12"
)

func voiceConfig() impl.DtxConfig {
	return impl.DtxConfig{
		ClockRate:     48000,
		FrameSamples:  960,
		MaxSilenceMs:  1000,
		MaxNoiseLevel: 40,
		SeqMode:       impl.SeqPerFrame,
	}
}

func TestNewRejectsNonIntegralFrameDuration(t *testing.T) {
	cfg := voiceConfig()
	cfg.ClockRate = 44100
	cfg.FrameSamples = 1000
	if _, err := impl.NewDtxTimelineGuard(cfg); !errors.Is(err, impl.ErrTimelineConfig) {
		t.Fatalf("non integral frame duration returned %v, want ErrTimelineConfig", err)
	}
}

func TestNewRejectsUnknownSequenceMode(t *testing.T) {
	cfg := voiceConfig()
	cfg.SeqMode = "auto"
	if _, err := impl.NewDtxTimelineGuard(cfg); !errors.Is(err, impl.ErrTimelineConfig) {
		t.Fatalf("unknown sequence mode returned %v, want ErrTimelineConfig", err)
	}
}

func TestPushVoiceAdvancesTimestampAndSeq(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	first, err := g.PushVoice()
	if err != nil {
		t.Fatalf("first voice frame: %v", err)
	}
	if first.Timestamp != 0 || first.Seq != 0 || first.Samples != 960 || first.Kind != impl.KindVoice {
		t.Fatalf("first voice frame = %+v, want ts 0 seq 0 samples 960 kind voice", first)
	}
	second, err := g.PushVoice()
	if err != nil {
		t.Fatalf("second voice frame: %v", err)
	}
	if second.Timestamp != 960 || second.Seq != 1 {
		t.Fatalf("second voice frame = %+v, want ts 960 seq 1", second)
	}
	if g.Timestamp() != 1920 || g.Seq() != 2 {
		t.Fatalf("timeline after two frames = ts %d seq %d, want 1920 and 2", g.Timestamp(), g.Seq())
	}
	if got := g.Stats().Voices; got != 2 {
		t.Fatalf("voice counter = %d, want 2", got)
	}
}

func TestPushVoiceDuringSilenceRejected(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	if _, err := g.PushVoice(); !errors.Is(err, impl.ErrTimelineInSilence) {
		t.Fatalf("voice frame during silence returned %v, want ErrTimelineInSilence", err)
	}
	if got := g.Stats().Voices; got != 0 {
		t.Fatalf("voice counter = %d, want 0", got)
	}
}

func TestFrameModeAdvancesSeqDuringSilence(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, ok, err := g.SilenceFrame(); err != nil || ok {
			t.Fatalf("silence frame %d returned ok=%v err=%v, want no packet", i, ok, err)
		}
	}
	if got := g.Seq(); got != 3 {
		t.Fatalf("sequence after three frame-mode silence frames = %d, want 3", got)
	}
	if got := g.Stats().Gaps; got != 0 {
		t.Fatalf("gap counter = %d in frame mode, want 0", got)
	}
	if got := g.FrameDurationMs(); got != 20 {
		t.Fatalf("frame duration = %d ms, want 20", got)
	}
}

func TestPacketModeCountsSkippedFrames(t *testing.T) {
	cfg := voiceConfig()
	cfg.SeqMode = impl.SeqPerPacket
	g, err := impl.NewDtxTimelineGuard(cfg)
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := g.SilenceFrame(); err != nil {
			t.Fatalf("silence frame %d: %v", i, err)
		}
	}
	if got := g.Stats().Gaps; got != 3 {
		t.Fatalf("gap counter = %d in packet mode, want 3", got)
	}
	if got := g.Seq(); got != 0 {
		t.Fatalf("sequence = %d in packet mode without packets, want 0", got)
	}
}

func TestSilenceFrameRejectedOutsideSilence(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if _, _, err := g.SilenceFrame(); !errors.Is(err, impl.ErrTimelineNotSilent) {
		t.Fatalf("silence frame outside silence returned %v, want ErrTimelineNotSilent", err)
	}
}

func TestSilenceFrameForcesRefreshAtLimit(t *testing.T) {
	cfg := voiceConfig()
	cfg.MaxSilenceMs = 40
	g, err := impl.NewDtxTimelineGuard(cfg)
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	if _, ok, err := g.SilenceFrame(); err != nil || ok {
		t.Fatalf("first silence frame returned ok=%v err=%v, want no packet", ok, err)
	}
	pkt, ok, err := g.SilenceFrame()
	if err != nil {
		t.Fatalf("second silence frame: %v", err)
	}
	if !ok {
		t.Fatal("second silence frame reached the silence limit but emitted no packet")
	}
	if pkt.Kind != impl.KindRefresh || pkt.Seq != 1 {
		t.Fatalf("forced packet = %+v, want kind refresh seq 1", pkt)
	}
	if got := g.Stats().Refreshes; got != 1 {
		t.Fatalf("refresh counter = %d, want 1", got)
	}
	if got := g.Seq(); got != 2 {
		t.Fatalf("sequence after refresh = %d, want 2", got)
	}
}

func TestComfortNoiseClampsLevelAndAdvancesTimeline(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	pkt, err := g.ComfortNoise(999)
	if err != nil {
		t.Fatalf("comfort noise: %v", err)
	}
	if pkt.Level != 40 {
		t.Fatalf("comfort level = %d, want clamped 40", pkt.Level)
	}
	if pkt.Kind != impl.KindComfort || pkt.Timestamp != 0 || pkt.Seq != 0 {
		t.Fatalf("comfort packet = %+v, want kind comfort ts 0 seq 0", pkt)
	}
	if got := g.Timestamp(); got != 960 {
		t.Fatalf("timestamp after comfort noise = %d, want 960", got)
	}
	if got := g.Stats().Comfort; got != 1 {
		t.Fatalf("comfort counter = %d, want 1", got)
	}
}

func TestLeaveSilenceRejectedOutsideSilence(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if _, err := g.LeaveSilence(1920); !errors.Is(err, impl.ErrTimelineNotSilent) {
		t.Fatalf("leave outside silence returned %v, want ErrTimelineNotSilent", err)
	}
}

func TestLeaveSilenceRealignsToWallClock(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if _, err := g.PushVoice(); err != nil {
		t.Fatalf("voice frame: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err := g.SilenceFrame(); err != nil {
			t.Fatalf("silence frame %d: %v", i, err)
		}
	}
	pkt, err := g.LeaveSilence(4800)
	if err != nil {
		t.Fatalf("leave silence: %v", err)
	}
	if pkt.Timestamp != 4800 {
		t.Fatalf("first frame after silence has ts %d, want realigned 4800", pkt.Timestamp)
	}
	if pkt.Seq != 5 {
		t.Fatalf("first frame after silence has seq %d, want 5", pkt.Seq)
	}
	if got := g.Stats().Aligned; got != 1 {
		t.Fatalf("aligned counter = %d, want 1", got)
	}
	if got := g.Timestamp(); got != 5760 {
		t.Fatalf("next timestamp = %d, want 5760", got)
	}
}

func TestSampleRateChangeResetsTimeline(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if _, err := g.PushVoice(); err != nil {
		t.Fatalf("voice frame: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	if err := g.SetSampleRate(16000); err != nil {
		t.Fatalf("set sample rate: %v", err)
	}
	if g.Timestamp() != 0 || g.Seq() != 0 {
		t.Fatalf("timeline after rate change = ts %d seq %d, want 0 and 0", g.Timestamp(), g.Seq())
	}
	if g.InSilence() {
		t.Fatal("silence state survived a sample rate change")
	}
	if got := g.FrameDurationMs(); got != 60 {
		t.Fatalf("frame duration at 16 kHz = %d ms, want 60", got)
	}
	if got := g.Stats().Resets; got != 1 {
		t.Fatalf("reset counter = %d, want 1", got)
	}
}

func TestSampleRateRejectsBrokenFrameGrid(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if _, err := g.PushVoice(); err != nil {
		t.Fatalf("voice frame: %v", err)
	}
	if err := g.SetSampleRate(44100); !errors.Is(err, impl.ErrTimelineConfig) {
		t.Fatalf("broken frame grid returned %v, want ErrTimelineConfig", err)
	}
	if got := g.Timestamp(); got != 960 {
		t.Fatalf("timestamp after rejected rate = %d, want 960 untouched", got)
	}
	if got := g.FrameDurationMs(); got != 20 {
		t.Fatalf("frame duration after rejected rate = %d ms, want 20", got)
	}
}

func TestSnapshotRestoreKeepsSilenceState(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if _, err := g.PushVoice(); err != nil {
		t.Fatalf("voice frame: %v", err)
	}
	if err := g.EnterSilence(); err != nil {
		t.Fatalf("enter silence: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err := g.SilenceFrame(); err != nil {
			t.Fatalf("silence frame %d: %v", i, err)
		}
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build second guard: %v", err)
	}
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.InSilence() {
		t.Fatal("silence state lost across restore")
	}
	if got := restored.Timestamp(); got != 2880 {
		t.Fatalf("timestamp after restore = %d, want 2880", got)
	}
	if got := restored.Seq(); got != 3 {
		t.Fatalf("sequence after restore = %d, want 3", got)
	}
	if got := restored.Stats().Silences; got != 1 {
		t.Fatalf("silence counter after restore = %d, want 1", got)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	g, err := impl.NewDtxTimelineGuard(voiceConfig())
	if err != nil {
		t.Fatalf("build guard: %v", err)
	}
	if err := g.Restore([]byte("broken timeline")); !errors.Is(err, impl.ErrTimelineSnapshot) {
		t.Fatalf("corrupt restore returned %v, want ErrTimelineSnapshot", err)
	}
	if got := g.Timestamp(); got != 0 {
		t.Fatalf("timestamp after corrupt restore = %d, want 0 untouched", got)
	}
}
