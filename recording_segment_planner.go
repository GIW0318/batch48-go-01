package task

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

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
	mu sync.Mutex

	limits Limits
	batch  string

	nextSeq int

	havePTS bool
	lastPTS int64

	// startMs is the start PTS of the open (not yet cut) segment and open
	// holds the frames that belong to it. The boundary keyframe that closes
	// one segment becomes the first frame of the next open segment.
	startMs int64
	open    []Frame

	// segments is the upload ledger ordered by Seq ascending. keyToSeg is
	// the stable key lookup into the same pointers.
	segments []*Segment
	keyToSeg map[string]*Segment

	// lastPlanSeq is the high-water mark of segments already handed to a
	// Plan caller, giving exactly-once dequeue under concurrency.
	lastPlanSeq int

	stats PlannerStats
}

// defaultBatch is the stable batch/namespace token that, together with the
// monotonic sequence number, composes a segment key. It is constant so a
// restored planner recomputes exactly the same keys after restart.
const defaultBatch = "seg"

// NewSegmentPlanner builds a planner with the given limits.
func NewSegmentPlanner(limits Limits) (*SegmentPlanner, error) {
	if !limits.usable() {
		return nil, ErrLimitsNotUsable
	}
	return &SegmentPlanner{
		limits:   limits,
		batch:    defaultBatch,
		nextSeq:  1,
		keyToSeg: make(map[string]*Segment),
	}, nil
}

// usable reports whether the limits form a usable cutting configuration.
func (l Limits) usable() bool {
	return l.MinDurMs > 0 &&
		l.MaxDurMs > 0 &&
		l.MaxAttempts > 0 &&
		l.MaxFrameBytes > 0 &&
		l.MaxDurMs >= l.MinDurMs
}

// segmentKey composes the unique, stable key of one segment from the batch and
// its monotonic sequence number.
func (p *SegmentPlanner) segmentKey(seq int) string {
	batch := p.batch
	if batch == "" {
		batch = defaultBatch
	}
	return fmt.Sprintf("%s-%d", batch, seq)
}

// Push feeds one frame of the recording stream.
func (p *SegmentPlanner) Push(f Frame) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// A recording has to start on a keyframe so the first segment is
	// decodable instead of showing a garbled first frame.
	if !p.havePTS && !f.Keyframe {
		return ErrKeyframeRequired
	}
	// PTS has to advance strictly; equal or backwards PTS is rejected.
	if p.havePTS && f.PTSMs <= p.lastPTS {
		return ErrNonMonotonicPTS
	}
	if f.Size > p.limits.MaxFrameBytes {
		return ErrFrameTooLarge
	}

	p.havePTS = true
	p.lastPTS = f.PTSMs
	p.stats.Frames++
	p.stats.Bytes += f.Size

	// Decide whether the incoming frame is a cut boundary before appending
	// it: a boundary keyframe belongs to the segment that starts on it.
	if len(p.open) > 0 {
		dur := f.PTSMs - p.startMs
		switch {
		case f.Keyframe && dur >= p.limits.MinDurMs:
			// Minimum duration reached on a keyframe: cut naturally.
			p.closeOpenLocked(f.PTSMs, true)
		case dur >= p.limits.MaxDurMs:
			// Maximum duration reached without a usable keyframe: force
			// cut, continuously subdividing an over-long span.
			p.closeOpenLocked(f.PTSMs, false)
		}
	}

	if len(p.open) == 0 {
		p.startMs = f.PTSMs
	}
	p.open = append(p.open, f)
	return nil
}

// closeOpenLocked cuts the open segment at endMs. When naturalKeyframe is
// true the final piece ends on an eligible keyframe and is not forced. Every
// MaxDurMs boundary introduced to keep pieces compliant is always forced.
// The open buffer is reset; the caller appends the boundary frame next.
func (p *SegmentPlanner) closeOpenLocked(endMs int64, naturalKeyframe bool) {
	frames := p.open
	curStart := p.startMs
	idx := 0

	// Continuously peel off MaxDurMs-sized pieces while the remaining span
	// is still longer than the cap. This keeps every emitted segment
	// compliant even after a large PTS jump.
	for endMs-curStart > p.limits.MaxDurMs {
		boundary := curStart + p.limits.MaxDurMs
		j := idx
		for j < len(frames) && frames[j].PTSMs < boundary {
			j++
		}
		p.emitLocked(curStart, boundary, frames[idx:j], true)
		idx = j
		curStart = boundary
	}

	p.emitLocked(curStart, endMs, frames[idx:], !naturalKeyframe)
	p.open = nil
}

// emitLocked allocates the next monotonic sequence number, builds one segment
// and registers it once in the ordered ledger and key index.
func (p *SegmentPlanner) emitLocked(startMs, endMs int64, frames []Frame, forced bool) {
	seq := p.nextSeq
	p.nextSeq++

	var totalBytes int
	for i := range frames {
		totalBytes += frames[i].Size
	}

	seg := &Segment{
		Key:     p.segmentKey(seq),
		Seq:     seq,
		StartMs: startMs,
		EndMs:   endMs,
		Bytes:   totalBytes,
		Forced:  forced,
	}
	p.segments = append(p.segments, seg)
	if p.keyToSeg == nil {
		p.keyToSeg = make(map[string]*Segment)
	}
	p.keyToSeg[seg.Key] = seg
	p.stats.Segments++
}

// Plan reports the segments cut since the previous call.
func (p *SegmentPlanner) Plan() ([]Segment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Segment, 0)
	highest := p.lastPlanSeq
	for _, seg := range p.segments {
		if seg.Seq > p.lastPlanSeq {
			out = append(out, *seg)
			if seg.Seq > highest {
				highest = seg.Seq
			}
		}
	}
	// Advance the high-water mark atomically so concurrent / repeated Plan
	// callers never observe the same segment twice.
	p.lastPlanSeq = highest
	return out, nil
}

// MarkUploading marks one segment as being uploaded.
func (p *SegmentPlanner) MarkUploading(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	seg, ok := p.keyToSeg[key]
	if !ok {
		return ErrUnknownSegment
	}
	if seg.Uploading {
		return ErrUploadInProgress
	}
	seg.Uploading = true
	return nil
}

// MarkUploaded records a finished upload.
func (p *SegmentPlanner) MarkUploaded(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	seg, ok := p.keyToSeg[key]
	if !ok {
		return ErrUnknownSegment
	}
	// A repeated completion is a successful no-op that only refreshes the
	// ledger entry; the upload counter and the ledger row stay unique.
	if !seg.Uploaded {
		seg.Uploaded = true
		p.stats.Uploaded++
	}
	seg.Uploading = false
	return nil
}

// Replan hands the listed segments back for another upload attempt.
func (p *SegmentPlanner) Replan(keys []string) ([]Segment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Resolve every key first so an unknown key fails the whole request
	// without half-applied retry accounting.
	for _, key := range keys {
		if _, ok := p.keyToSeg[key]; !ok {
			return nil, ErrUnknownSegment
		}
	}

	out := make([]Segment, 0, len(keys))
	for _, key := range keys {
		seg := p.keyToSeg[key]
		// A replan clears a stale in-flight flag left by a dropped
		// connection so the same segment can be claimed once again.
		seg.Uploading = false
		seg.Attempts++
		p.stats.Replanned++
		out = append(out, *seg)
	}
	return out, nil
}

// Retention evicts the oldest uploaded segments beyond the keep count.
func (p *SegmentPlanner) Retention(keep int) ([]string, error) {
	if keep < 0 {
		return nil, ErrInvalidKeep
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Only unreferenced, fully uploaded segments count towards retention and
	// may be deleted. Pending, in-flight and manually parked segments are
	// protected even when they are the oldest entries.
	eligible := make([]*Segment, 0, len(p.segments))
	for _, seg := range p.segments {
		if seg.Uploaded && !seg.Uploading && !p.isManualLocked(seg) {
			eligible = append(eligible, seg)
		}
	}

	evictCount := len(eligible) - keep
	if evictCount <= 0 {
		return []string{}, nil
	}

	evicted := make([]string, 0, evictCount)
	removed := make(map[string]struct{}, evictCount)
	for i := 0; i < evictCount; i++ {
		seg := eligible[i]
		evicted = append(evicted, seg.Key)
		removed[seg.Key] = struct{}{}
		delete(p.keyToSeg, seg.Key)
	}

	kept := make([]*Segment, 0, len(p.segments)-evictCount)
	for _, seg := range p.segments {
		if _, drop := removed[seg.Key]; !drop {
			kept = append(kept, seg)
		}
	}
	p.segments = kept
	p.stats.Evicted += evictCount

	return evicted, nil
}

// isManualLocked reports whether a segment has exhausted its retry budget and
// is therefore parked for manual handling. Such rows are never auto-deleted.
// The state is fully derived from the exported ledger fields, so it survives
// Snapshot/Restore (and even serialization based restarts) without extra
// bookkeeping.
func (p *SegmentPlanner) isManualLocked(seg *Segment) bool {
	return !seg.Uploaded && p.limits.MaxAttempts > 0 && seg.Attempts >= p.limits.MaxAttempts
}

// Segments reports every known segment ordered by sequence.
func (p *SegmentPlanner) Segments() []Segment {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Segment, 0, len(p.segments))
	for _, seg := range p.segments {
		out = append(out, *seg)
	}
	return out
}

// Pending reports the segments that are not uploaded yet.
func (p *SegmentPlanner) Pending() []Segment {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]Segment, 0)
	for _, seg := range p.segments {
		if !seg.Uploaded {
			out = append(out, *seg)
		}
	}
	return out
}

// NextSeq reports the next sequence number to assign.
func (p *SegmentPlanner) NextSeq() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextSeq
}

// Snapshot captures the restorable state.
func (p *SegmentPlanner) Snapshot() (PlannerState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	segs := make([]Segment, 0, len(p.segments))
	for _, seg := range p.segments {
		segs = append(segs, *seg)
	}

	return PlannerState{
		NextSeq:     p.nextSeq,
		LastPTSMs:   p.lastPTS,
		OpenStartMs: p.startMs,
		OpenFrames:  append([]Frame(nil), p.open...),
		Segments:    segs,
		Limits:      p.limits,
		Stats:       p.stats,
	}, nil
}

// Restore loads a previously captured state.
func (p *SegmentPlanner) Restore(st PlannerState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.limits = st.Limits
	if p.batch == "" {
		p.batch = defaultBatch
	}

	p.nextSeq = st.NextSeq
	p.lastPTS = st.LastPTSMs
	p.startMs = st.OpenStartMs
	p.open = append([]Frame(nil), st.OpenFrames...)
	if len(p.open) > 0 {
		p.startMs = p.open[0].PTSMs
	}

	p.stats = st.Stats

	p.segments = make([]*Segment, 0, len(st.Segments))
	p.keyToSeg = make(map[string]*Segment, len(st.Segments))
	maxSeq := 0
	for i := range st.Segments {
		cp := st.Segments[i]
		p.segments = append(p.segments, &cp)
		p.keyToSeg[cp.Key] = &cp
		if cp.Seq > maxSeq {
			maxSeq = cp.Seq
		}
	}
	sort.SliceStable(p.segments, func(i, j int) bool {
		return p.segments[i].Seq < p.segments[j].Seq
	})

	if p.nextSeq <= 0 {
		p.nextSeq = maxSeq + 1
	}
	// A restored ledger is treated as already dequeued by Plan, matching
	// the snapshot/restore continuation contract and avoiding replay.
	p.lastPlanSeq = maxSeq

	// Reconstruct the "have seen a PTS" gate from the persisted activity so
	// a restored stream does not wrongly demand another keyframe at PTS 0.
	p.havePTS = st.Stats.Frames > 0 ||
		len(p.open) > 0 ||
		len(p.segments) > 0

	return nil
}

// Stats reports the planner counters.
func (p *SegmentPlanner) Stats() PlannerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}
