package task

import (
	"encoding/json"
	"errors"
	"sync"
)

// ClockSource names the timeline used as the sync master.
type ClockSource string

// Supported master clocks.
const (
	MasterAudio    ClockSource = "audio"
	MasterVideo    ClockSource = "video"
	MasterExternal ClockSource = "external"
)

// Decision actions returned by Decide.
const (
	ActionKeep          = "keep"
	ActionDropVideo     = "drop_video"
	ActionRepeatVideo   = "repeat_video"
	ActionResampleAudio = "resample_audio"
)

// Errors reported by the sync guard.
var (
	ErrClockUnsupported  = errors.New("master clock is not supported")
	ErrNoMasterClock     = errors.New("master clock is not set")
	ErrVideoOrder        = errors.New("video presentation time is not increasing")
	ErrAudioBlock        = errors.New("audio block is not usable")
	ErrThresholdsInvalid = errors.New("sync thresholds are not usable")
	ErrStateCorrupt      = errors.New("sync guard state is corrupt")
)

// VideoFrame is one decoded video frame waiting for presentation.
type VideoFrame struct {
	PTS      int64
	Keyframe bool
	Seq      int64
}

// AudioBlock is one audio block and the playback position the device reports.
type AudioBlock struct {
	PTS      int64
	Duration int64
	Position int64
}

// Thresholds configures drift estimation and compensation.
type Thresholds struct {
	SyncWindow       int64
	MaxStepPerCall   int64
	SmoothingSamples int
	AudioQuantum     int64
	MaxQueue         int
}

// SyncDecision is the outcome of one Decide call.
type SyncDecision struct {
	Action        string
	Frame         VideoFrame
	RepeatCount   int
	ResampleRatio float64
	Drift         int64
	DriftAfter    int64
}

// SyncStats reports the counters of the guard.
type SyncStats struct {
	Decides       uint64
	Drops         uint64
	Repeats       uint64
	Resamples     uint64
	KeyframeDrops uint64
	PushedVideo   uint64
	PushedAudio   uint64
	Rejected      uint64
	ResetSamples  uint64
	InSync        bool
}

// AvSyncGuard keeps video presentation aligned with the master clock.
type AvSyncGuard struct {
	mu sync.Mutex

	master     ClockSource
	thresholds Thresholds

	video []VideoFrame
	audio []AudioBlock

	samples      []int64
	compensation int64

	lastVideoPTS int64
	hasVideoPTS  bool

	stats SyncStats
}

type guardSnapshot struct {
	Master       ClockSource  `json:"master"`
	Thresholds   Thresholds   `json:"thresholds"`
	Video        []VideoFrame `json:"video"`
	Audio        []AudioBlock `json:"audio"`
	Samples      []int64      `json:"samples"`
	Compensation int64        `json:"compensation"`
	LastVideoPTS int64        `json:"last_video_pts"`
	HasVideoPTS  bool         `json:"has_video_pts"`
	Stats        SyncStats    `json:"stats"`
}

// NewAvSyncGuard builds a guard with the given thresholds.
func NewAvSyncGuard(th Thresholds) *AvSyncGuard {
	if th.SmoothingSamples < 1 {
		th.SmoothingSamples = 1
	}
	return &AvSyncGuard{thresholds: th}
}

func validClock(src ClockSource) bool {
	switch src {
	case MasterAudio, MasterVideo, MasterExternal:
		return true
	default:
		return false
	}
}

func validThresholds(th Thresholds) bool {
	return th.SyncWindow >= 0 && th.MaxStepPerCall > 0 && th.SmoothingSamples >= 1
}

// SetMasterClock selects the timeline every correction is measured against.
func (g *AvSyncGuard) SetMasterClock(src ClockSource) error {
	if !validClock(src) {
		return ErrClockUnsupported
	}
	g.mu.Lock()
	g.master = src
	g.mu.Unlock()
	return nil
}

// MasterClock reports the selected master clock.
func (g *AvSyncGuard) MasterClock() ClockSource {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.master
}

// PushVideo queues one decoded video frame.
func (g *AvSyncGuard) PushVideo(frame VideoFrame) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.hasVideoPTS && frame.PTS <= g.lastVideoPTS {
		g.stats.Rejected++
		return ErrVideoOrder
	}

	if g.thresholds.MaxQueue > 0 && len(g.video) >= g.thresholds.MaxQueue {
		dropped := g.video[0]
		g.video = g.video[1:]
		g.stats.Drops++
		if dropped.Keyframe {
			g.stats.KeyframeDrops++
		}
	}

	g.video = append(g.video, frame)
	g.lastVideoPTS = frame.PTS
	g.hasVideoPTS = true
	g.stats.PushedVideo++
	return nil
}

// PushAudio queues one audio block with the reported playback position.
func (g *AvSyncGuard) PushAudio(block AudioBlock) error {
	if block.Duration <= 0 {
		g.mu.Lock()
		g.stats.Rejected++
		g.mu.Unlock()
		return ErrAudioBlock
	}

	g.mu.Lock()
	if block.Position < 0 {
		block.Position = block.PTS
	}
	g.audio = append(g.audio, block)
	g.stats.PushedAudio++
	g.mu.Unlock()
	return nil
}

func (g *AvSyncGuard) currentVideoPTSLocked() (int64, bool) {
	if len(g.video) > 0 {
		return g.video[len(g.video)-1].PTS, true
	}
	if g.hasVideoPTS {
		return g.lastVideoPTS, true
	}
	return 0, false
}

func (g *AvSyncGuard) driftVideoPTSLocked(audioPos int64) (int64, bool) {
	newest, ok := g.currentVideoPTSLocked()
	if !ok {
		return 0, false
	}
	if g.master == MasterAudio && g.compensation == 0 && len(g.video) > 0 {
		head := g.video[0].PTS
		span := newest - head
		if span < g.thresholds.MaxStepPerCall && audioPos-newest > g.thresholds.SyncWindow {
			return head, true
		}
	}
	return newest, true
}

func (g *AvSyncGuard) appendSampleLocked(sample int64) int64 {
	g.samples = append(g.samples, sample)
	if n := g.thresholds.SmoothingSamples; n > 0 && len(g.samples) > n {
		g.samples = append([]int64(nil), g.samples[len(g.samples)-n:]...)
	}
	return g.meanLocked()
}

func (g *AvSyncGuard) meanLocked() int64 {
	if len(g.samples) == 0 {
		return 0
	}
	var total int64
	for _, sample := range g.samples {
		total += sample
	}
	return total / int64(len(g.samples))
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func sign64(v int64) int64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

// dropVideoLocked removes one normal frame before considering a keyframe.
func (g *AvSyncGuard) dropVideoLocked() (VideoFrame, bool) {
	if len(g.video) == 0 {
		return VideoFrame{}, false
	}

	index := -1
	for i, frame := range g.video {
		if !frame.Keyframe {
			index = i
			break
		}
	}
	if index < 0 {
		index = 0
	}

	dropped := g.video[index]
	copy(g.video[index:], g.video[index+1:])
	g.video = g.video[:len(g.video)-1]
	g.stats.Drops++
	if dropped.Keyframe {
		g.stats.KeyframeDrops++
	}
	return dropped, true
}

// Decide estimates the drift and returns the next compensation step.
func (g *AvSyncGuard) Decide() (SyncDecision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.master == "" {
		return SyncDecision{}, ErrNoMasterClock
	}

	g.stats.Decides++
	decision := SyncDecision{Action: ActionKeep, Drift: g.meanLocked(), DriftAfter: g.meanLocked()}
	if len(g.video) > 0 {
		decision.Frame = g.video[0]
	}

	videoPTS, hasVideo := g.currentVideoPTSLocked()
	if !hasVideo || len(g.audio) == 0 {
		g.stats.InSync = true
		return decision, nil
	}

	audioPos := g.audio[len(g.audio)-1].Position
	if g.thresholds.AudioQuantum > 0 && audioPos >= 0 {
		audioPos -= audioPos % g.thresholds.AudioQuantum
	}
	videoPTS, _ = g.driftVideoPTSLocked(audioPos)
	estimate := g.appendSampleLocked(audioPos - videoPTS)
	decision.Drift = estimate
	decision.DriftAfter = estimate

	if abs64(estimate) <= g.thresholds.SyncWindow {
		g.stats.InSync = true
		return decision, nil
	}

	residual := estimate - g.compensation
	if abs64(residual) <= g.thresholds.SyncWindow {
		decision.DriftAfter = residual
		g.stats.InSync = true
		return decision, nil
	}

	step := abs64(residual)
	if step > g.thresholds.MaxStepPerCall {
		step = g.thresholds.MaxStepPerCall
	}
	step *= sign64(residual)

	switch g.master {
	case MasterAudio:
		if residual > 0 {
			dropped, ok := g.dropVideoLocked()
			if !ok {
				g.stats.InSync = false
				return decision, nil
			}
			decision.Action = ActionDropVideo
			decision.Frame = dropped
		} else {
			if len(g.video) == 0 {
				g.stats.InSync = false
				return decision, nil
			}
			decision.Action = ActionRepeatVideo
			decision.Frame = g.video[0]
			decision.RepeatCount = 1
			g.stats.Repeats++
		}
	case MasterVideo, MasterExternal:
		decision.Action = ActionResampleAudio
		g.stats.Resamples++
	default:
		return SyncDecision{}, ErrNoMasterClock
	}

	g.compensation += step
	decision.DriftAfter = estimate - g.compensation
	if decision.Action == ActionResampleAudio {
		decision.ResampleRatio = 1 - float64(step)/1_000_000
	}
	g.stats.InSync = abs64(decision.DriftAfter) <= g.thresholds.SyncWindow
	return decision, nil
}

// SetThresholds replaces the estimation and compensation thresholds.
func (g *AvSyncGuard) SetThresholds(th Thresholds) error {
	if !validThresholds(th) {
		return ErrThresholdsInvalid
	}
	g.mu.Lock()
	g.thresholds = th
	if len(g.samples) > th.SmoothingSamples {
		g.samples = append([]int64(nil), g.samples[len(g.samples)-th.SmoothingSamples:]...)
	}
	g.mu.Unlock()
	return nil
}

// ResetSource clears the samples, the compensation offset and the queues.
func (g *AvSyncGuard) ResetSource() error {
	g.mu.Lock()
	g.video = nil
	g.audio = nil
	g.samples = nil
	g.compensation = 0
	g.lastVideoPTS = 0
	g.hasVideoPTS = false
	g.stats.ResetSamples++
	g.stats.InSync = false
	g.mu.Unlock()
	return nil
}

// DriftEstimate reports the smoothed drift in microseconds.
func (g *AvSyncGuard) DriftEstimate() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.meanLocked()
}

// QueueLen reports the queued video frames and audio blocks.
func (g *AvSyncGuard) QueueLen() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.video), len(g.audio)
}

// PendingPTS reports the presentation times of the queued video frames.
func (g *AvSyncGuard) PendingPTS() []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	pts := make([]int64, len(g.video))
	for i, frame := range g.video {
		pts[i] = frame.PTS
	}
	return pts
}

// Snapshot serialises the clocks, thresholds, samples, queues and counters.
func (g *AvSyncGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	state := guardSnapshot{
		Master:       g.master,
		Thresholds:   g.thresholds,
		Video:        append([]VideoFrame(nil), g.video...),
		Audio:        append([]AudioBlock(nil), g.audio...),
		Samples:      append([]int64(nil), g.samples...),
		Compensation: g.compensation,
		LastVideoPTS: g.lastVideoPTS,
		HasVideoPTS:  g.hasVideoPTS,
		Stats:        g.stats,
	}
	g.mu.Unlock()
	return json.Marshal(state)
}

func validateSnapshot(state *guardSnapshot) bool {
	if state.Master != "" && !validClock(state.Master) {
		return false
	}
	if !validThresholds(state.Thresholds) {
		return false
	}
	if state.Thresholds.MaxQueue > 0 && len(state.Video) > state.Thresholds.MaxQueue {
		return false
	}
	if len(state.Video) > 0 && !state.HasVideoPTS {
		return false
	}
	for i, frame := range state.Video {
		if i > 0 && frame.PTS <= state.Video[i-1].PTS {
			return false
		}
	}
	for _, block := range state.Audio {
		if block.Duration <= 0 {
			return false
		}
	}
	if len(state.Samples) > state.Thresholds.SmoothingSamples {
		return false
	}
	if state.HasVideoPTS && len(state.Video) > 0 && state.LastVideoPTS < state.Video[len(state.Video)-1].PTS {
		return false
	}
	return true
}

// Restore rebuilds the guard state from a snapshot.
func (g *AvSyncGuard) Restore(payload []byte) error {
	var state guardSnapshot
	if err := json.Unmarshal(payload, &state); err != nil || !validateSnapshot(&state) {
		return ErrStateCorrupt
	}

	g.mu.Lock()
	g.master = state.Master
	g.thresholds = state.Thresholds
	g.video = append([]VideoFrame(nil), state.Video...)
	g.audio = append([]AudioBlock(nil), state.Audio...)
	g.samples = append([]int64(nil), state.Samples...)
	g.compensation = state.Compensation
	g.lastVideoPTS = state.LastVideoPTS
	g.hasVideoPTS = state.HasVideoPTS
	g.stats = state.Stats
	g.mu.Unlock()
	return nil
}

// Stats reports the guard counters.
func (g *AvSyncGuard) Stats() SyncStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}
