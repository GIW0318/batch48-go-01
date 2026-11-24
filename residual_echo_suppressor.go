package task

import "errors"

// Errors reported by the echo suppressor.
var (
	ErrFrameSize        = errors.New("frame length does not match the configured frame size")
	ErrInvalidThreshold = errors.New("threshold set is not usable")
	ErrQueueFull        = errors.New("output frame queue is full")
	ErrBadSnapshot      = errors.New("echo suppressor snapshot is not usable")
)

// States reported by State.
const (
	StateFarEnd     = "far_end"
	StateNearEnd    = "near_end"
	StateDoubleTalk = "double_talk"
)

// Defaults applied by NewEchoSuppressor.
const (
	DefaultRefEnter       = 0.5
	DefaultRefRelease     = 0.2
	DefaultNearEnter      = 0.5
	DefaultNearRelease    = 0.2
	DefaultMinDwell       = 2
	DefaultMaxGainStep    = 0.1
	DefaultSuppressGain   = 0.08
	DefaultDoubleTalkGain = 0.6
	DefaultQueueCapacity  = 64
)

// Thresholds carries the energy gates and the gain policy.
type Thresholds struct {
	RefEnter       float64
	RefRelease     float64
	NearEnter      float64
	NearRelease    float64
	MinDwell       int
	MaxGainStep    float64
	SuppressGain   float64
	DoubleTalkGain float64
}

// Stats counts frames handled by the suppressor.
type Stats struct {
	InFrames         int
	OutFrames        int
	FarEndFrames     int
	NearEndFrames    int
	DoubleTalkFrames int
	ComfortFrames    int
	SuppressedFrames int
}

// EchoState is the restorable state of one suppressor.
type EchoState struct {
	State      string
	Gain       float64
	Dwell      int
	Comfort    float64
	NoiseFloor float64
	Stats      Stats
	Queued     int
}

// EchoSuppressor decides one of three talk states per frame and shapes the
// suppression gain of the captured signal.
type EchoSuppressor struct {
	frameSize  int
	thresholds Thresholds
}

// NewEchoSuppressor builds a suppressor for fixed size frames.
func NewEchoSuppressor(frameSize int) (*EchoSuppressor, error) {
	return &EchoSuppressor{frameSize: frameSize, thresholds: Thresholds{
		RefEnter:       DefaultRefEnter,
		RefRelease:     DefaultRefRelease,
		NearEnter:      DefaultNearEnter,
		NearRelease:    DefaultNearRelease,
		MinDwell:       DefaultMinDwell,
		MaxGainStep:    DefaultMaxGainStep,
		SuppressGain:   DefaultSuppressGain,
		DoubleTalkGain: DefaultDoubleTalkGain,
	}}, nil
}

// PushReference feeds one far end reference frame.
func (s *EchoSuppressor) PushReference(frame []float64) error {
	return nil
}

// PushNearEnd feeds one captured near end frame and queues its output.
func (s *EchoSuppressor) PushNearEnd(frame []float64) error {
	return nil
}

// PopFrame dequeues one processed output frame.
func (s *EchoSuppressor) PopFrame() ([]float64, bool) {
	return nil, false
}

// SetThresholds replaces the energy gates and gain policy.
func (s *EchoSuppressor) SetThresholds(t Thresholds) error {
	return nil
}

// State reports the current talk state.
func (s *EchoSuppressor) State() string {
	return ""
}

// Gain reports the current suppression gain.
func (s *EchoSuppressor) Gain() float64 {
	return 0
}

// ComfortNoiseLevel reports the current comfort noise level.
func (s *EchoSuppressor) ComfortNoiseLevel() float64 {
	return 0
}

// Reset returns the suppressor to its initial state.
func (s *EchoSuppressor) Reset() {
}

// Snapshot captures the restorable state.
func (s *EchoSuppressor) Snapshot() (EchoState, error) {
	return EchoState{}, nil
}

// Restore loads a previously captured state.
func (s *EchoSuppressor) Restore(st EchoState) error {
	return nil
}

// Stats reports the frame counters.
func (s *EchoSuppressor) Stats() Stats {
	return Stats{}
}
