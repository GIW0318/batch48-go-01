package task

import "errors"

// Errors reported by the cue scheduler.
var (
	ErrInvalidCue    = errors.New("cue is missing its id or track")
	ErrInvalidRange  = errors.New("cue time range is not usable")
	ErrInvalidLayer  = errors.New("cue layer is negative")
	ErrDuplicateCue  = errors.New("cue id already exists")
	ErrCueLimit      = errors.New("cue count limit reached")
	ErrTrackLimit    = errors.New("track count limit reached")
	ErrUnknownCue    = errors.New("cue id is unknown")
	ErrLimitRejected = errors.New("scheduler limits are not usable")
	ErrInvalidTime   = errors.New("clock position is negative")
)

// Cue is one subtitle entry on one track.
type Cue struct {
	ID      string
	Track   string
	StartMs int64
	EndMs   int64
	Layer   int
	Text    string
}

// Limits caps the cues kept and the text shown.
type Limits struct {
	MaxCues    int
	MaxTracks  int
	MaxVisible int
	MaxRows    int
	MaxCols    int
}

// Rendered is one cue as it should be drawn at the current clock.
type Rendered struct {
	CueID     string
	Track     string
	Layer     int
	Text      string
	Truncated bool
}

// CueStats counts scheduler activity.
type CueStats struct {
	Added          int
	Removed        int
	ActivePeak     int
	DroppedTotal   int
	TruncatedTotal int
}

// SchedulerState is the restorable state of one scheduler.
type SchedulerState struct {
	Cues    []Cue
	Limits  Limits
	ClockMs int64
	Stats   CueStats
}

// CueScheduler keeps the subtitle cues of a playback session and resolves the
// active set at the current clock.
type CueScheduler struct {
	limits Limits
}

// NewCueScheduler builds a scheduler with the given limits.
func NewCueScheduler(limits Limits) (*CueScheduler, error) {
	return &CueScheduler{limits: limits}, nil
}

// AddCue registers one cue.
func (s *CueScheduler) AddCue(c Cue) error {
	return nil
}

// RemoveCue drops one cue by id.
func (s *CueScheduler) RemoveCue(id string) error {
	return nil
}

// Active reports the cues to draw at the current clock.
func (s *CueScheduler) Active() []Rendered {
	return nil
}

// SeekTo moves the playback clock.
func (s *CueScheduler) SeekTo(atMs int64) error {
	return nil
}

// SetLimits replaces the scheduler limits.
func (s *CueScheduler) SetLimits(limits Limits) error {
	return nil
}

// Clock reports the current playback clock.
func (s *CueScheduler) Clock() int64 {
	return 0
}

// CueCount reports how many cues are registered.
func (s *CueScheduler) CueCount() int {
	return 0
}

// Tracks reports the tracks that own at least one cue.
func (s *CueScheduler) Tracks() []string {
	return nil
}

// Snapshot captures the restorable state.
func (s *CueScheduler) Snapshot() (SchedulerState, error) {
	return SchedulerState{}, nil
}

// Restore loads a previously captured state.
func (s *CueScheduler) Restore(st SchedulerState) error {
	return nil
}

// Stats reports the scheduler counters.
func (s *CueScheduler) Stats() CueStats {
	return CueStats{}
}
