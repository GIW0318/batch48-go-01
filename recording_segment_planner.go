package task

import "errors"

// Errors reported by the segment planner.
var (
	ErrLimitsNotUsable  = errors.New("segment limits are not usable")
	ErrNonMonotonicPTS  = errors.New("frame pts is not increasing")
	ErrFrameTooLarge    = errors.New("frame exceeds the byte cap")
	ErrKeyframeRequired = errors.New("recording must start on a keyframe")
	ErrUnknownSegment   = errors.New("segment key is unknown")
	ErrUploadInProgress = errors.New("segment already has an upload in flight")
	ErrInvalidKeep      = errors.New("retention count is negative")
)

// Limits caps the segment durations and the upload ledger.
type Limits struct {
	MinDurMs      int64
	MaxDurMs      int64
	KeepUploaded  int
	MaxAttempts   int
	MaxFrameBytes int
}

// Frame is one frame of the recording stream.
type Frame struct {
	PTSMs    int64
	Keyframe bool
	Size     int
}

// Segment is one planned recording segment.
type Segment struct {
	Key       string
	Seq       int
	StartMs   int64
	EndMs     int64
	Bytes     int
	Forced    bool
	Uploading bool
	Uploaded  bool
	Attempts  int
}

// PlannerStats counts planner activity.
type PlannerStats struct {
	Frames    int
	Segments  int
	Uploaded  int
	Evicted   int
	Replanned int
	Bytes     int
}

// PlannerState is the restorable state of one planner.
type PlannerState struct {
	NextSeq     int
	LastPTSMs   int64
	OpenStartMs int64
	OpenFrames  []Frame
	Segments    []Segment
	Limits      Limits
	Stats       PlannerStats
}

// SegmentPlanner cuts a recording stream on keyframe boundaries and tracks the
// upload state of every segment.
type SegmentPlanner struct {
	limits Limits
}

// NewSegmentPlanner builds a planner with the given limits.
func NewSegmentPlanner(limits Limits) (*SegmentPlanner, error) {
	return &SegmentPlanner{limits: limits}, nil
}

// Push feeds one frame of the recording stream.
func (p *SegmentPlanner) Push(f Frame) error {
	return nil
}

// Plan reports the segments cut since the previous call.
func (p *SegmentPlanner) Plan() ([]Segment, error) {
	return nil, nil
}

// MarkUploading marks one segment as being uploaded.
func (p *SegmentPlanner) MarkUploading(key string) error {
	return nil
}

// MarkUploaded records a finished upload.
func (p *SegmentPlanner) MarkUploaded(key string) error {
	return nil
}

// Replan hands the listed segments back for another upload attempt.
func (p *SegmentPlanner) Replan(keys []string) ([]Segment, error) {
	return nil, nil
}

// Retention evicts the oldest uploaded segments beyond the keep count.
func (p *SegmentPlanner) Retention(keep int) ([]string, error) {
	return nil, nil
}

// Segments reports every known segment ordered by sequence.
func (p *SegmentPlanner) Segments() []Segment {
	return nil
}

// Pending reports the segments that are not uploaded yet.
func (p *SegmentPlanner) Pending() []Segment {
	return nil
}

// NextSeq reports the next sequence number to assign.
func (p *SegmentPlanner) NextSeq() int {
	return 0
}

// Snapshot captures the restorable state.
func (p *SegmentPlanner) Snapshot() (PlannerState, error) {
	return PlannerState{}, nil
}

// Restore loads a previously captured state.
func (p *SegmentPlanner) Restore(st PlannerState) error {
	return nil
}

// Stats reports the planner counters.
func (p *SegmentPlanner) Stats() PlannerStats {
	return PlannerStats{}
}
