package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// Errors reported by the frame assembler.
var (
	ErrMalformedPacket = errors.New("rtp packet is malformed")
	ErrLimitsRejected  = errors.New("frame limits are not usable")
	ErrSnapshotCorrupt = errors.New("frame assembler snapshot is corrupt")
)

// Limits applied when a field is left at zero.
const (
	DefaultMaxPackets    = 512
	DefaultMaxFrameSpan  = 5000
	DefaultMaxFragments  = 64
	DefaultReorderWindow = 64
)

const snapshotVersion = 1

var snapshotMagic = []byte("GSB48FAS1")

// RTPPacket is one received RTP packet of a video stream.
type RTPPacket struct {
	SSRC      uint32
	Seq       uint16
	Timestamp uint32
	Marker    bool
	FragIndex int
	FragCount int
	Payload   []byte
	Keyframe  bool
}

// VideoFrame is one reassembled video frame.
type VideoFrame struct {
	SSRC      uint32
	Timestamp uint32
	LastSeq   uint32
	Keyframe  bool
	Fragments int
	Payload   []byte
}

// FrameLimits bounds the per stream reassembly buffer.
type FrameLimits struct {
	MaxPackets    int
	MaxFrameSpan  int
	MaxFragments  int
	ReorderWindow int
}

// AssemblerStats reports the counters kept by the assembler.
type AssemblerStats struct {
	Pushed       uint64
	Rejected     uint64
	Duplicates   uint64
	Reordered    uint64
	Late         uint64
	Completed    uint64
	Dropped      uint64
	MissingDrops uint64
	Malicious    uint64
	Buffered     int
}

type fragment struct {
	seq     int64
	payload []byte
}

type frameState struct {
	timestamp uint32
	fragCount int
	marker    bool
	keyframe  bool
	completed bool
	fragments map[int]fragment
}

type streamState struct {
	mu sync.Mutex

	frames           map[uint32]*frameState
	droppedTimestamp map[uint32]struct{}
	highestSeq       int64
	hasHighSeq       bool

	maxTimestamp    uint32
	hasMaxTimestamp bool

	deliveredWatermark uint32
	hasDelivered       bool

	needKeyframe bool
	limitEvicted bool

	badTimestamp    uint32
	hasBadTimestamp bool

	discardTimestamp    uint32
	hasDiscardTimestamp bool
}

func newStreamState() *streamState {
	return &streamState{
		frames:           make(map[uint32]*frameState),
		droppedTimestamp: make(map[uint32]struct{}),
	}
}

// FrameAssembler reassembles RTP packets into whole video frames.
type FrameAssembler struct {
	mu      sync.Mutex
	limits  FrameLimits
	streams map[uint32]*streamState

	pushed       atomic.Uint64
	rejected     atomic.Uint64
	duplicates   atomic.Uint64
	reordered    atomic.Uint64
	late         atomic.Uint64
	completed    atomic.Uint64
	dropped      atomic.Uint64
	missingDrops atomic.Uint64
	malicious    atomic.Uint64
}

// NewFrameAssembler builds an assembler with the given limits.
func NewFrameAssembler(limits FrameLimits) *FrameAssembler {
	return &FrameAssembler{
		limits:  withDefaults(limits),
		streams: make(map[uint32]*streamState),
	}
}

func withDefaults(l FrameLimits) FrameLimits {
	if l.MaxPackets <= 0 {
		l.MaxPackets = DefaultMaxPackets
	}
	if l.MaxFrameSpan <= 0 {
		l.MaxFrameSpan = DefaultMaxFrameSpan
	}
	if l.MaxFragments <= 0 {
		l.MaxFragments = DefaultMaxFragments
	}
	if l.ReorderWindow <= 0 {
		l.ReorderWindow = DefaultReorderWindow
	}
	return l
}

func validLimits(l FrameLimits) bool {
	return l.MaxPackets > 0 && l.MaxFrameSpan > 0 &&
		l.MaxFragments > 0 && l.ReorderWindow > 0
}

// Push feeds one packet into the reassembly buffer.
func (a *FrameAssembler) Push(pkt RTPPacket) error {
	if len(pkt.Payload) == 0 || pkt.FragCount < 1 ||
		pkt.FragIndex < 0 || pkt.FragIndex >= pkt.FragCount {
		a.rejected.Add(1)
		return ErrMalformedPacket
	}

	a.mu.Lock()
	stream := a.streams[pkt.SSRC]
	if stream == nil {
		stream = newStreamState()
		a.streams[pkt.SSRC] = stream
	}
	stream.mu.Lock()
	limits := a.limits
	a.mu.Unlock()
	defer stream.mu.Unlock()

	a.pushLocked(stream, pkt, limits)
	return nil
}

func (a *FrameAssembler) pushLocked(stream *streamState, pkt RTPPacket, limits FrameLimits) {
	a.pushed.Add(1)
	ts := pkt.Timestamp

	if stream.hasDelivered && ts <= stream.deliveredWatermark {
		a.late.Add(1)
		return
	}

	if _, dropped := stream.droppedTimestamp[ts]; dropped {
		a.late.Add(1)
		return
	}
	if stream.hasBadTimestamp && stream.badTimestamp == ts {
		return
	}
	if stream.hasDiscardTimestamp && stream.discardTimestamp == ts {
		return
	}

	if frame, exists := stream.frames[ts]; exists {
		if _, duplicate := frame.fragments[pkt.FragIndex]; duplicate {
			a.duplicates.Add(1)
			return
		}
	}

	if pkt.FragCount <= limits.MaxFragments {
		extSeq := extendSequence(stream, pkt.Seq)
		if stream.hasHighSeq && extSeq < stream.highestSeq &&
			stream.highestSeq-extSeq > int64(limits.ReorderWindow) {
			a.late.Add(1)
			return
		}
	}

	if pkt.FragCount <= limits.MaxFragments {
		incoming := pkt.FragCount
		if _, exists := stream.frames[ts]; exists {
			incoming = 1
		}
		a.reserveFrameCapacityLocked(stream, limits, ts, incoming)
	}

	for oldTS, oldFrame := range stream.frames {
		if oldTS >= ts || oldFrame.completed {
			continue
		}
		delete(stream.frames, oldTS)
		a.recordDroppedTimestampLocked(stream, oldTS, limits)
		a.dropped.Add(1)
		if len(oldFrame.fragments) < oldFrame.fragCount {
			a.missingDrops.Add(1)
		}
		stream.needKeyframe = true
	}

	if !stream.hasMaxTimestamp || ts > stream.maxTimestamp {
		stream.maxTimestamp = ts
		stream.hasMaxTimestamp = true
		if stream.hasBadTimestamp && stream.badTimestamp != ts {
			stream.hasBadTimestamp = false
		}
		if stream.hasDiscardTimestamp && stream.discardTimestamp != ts {
			stream.hasDiscardTimestamp = false
		}
	}
	if _, dropped := stream.droppedTimestamp[ts]; dropped {
		return
	}

	frame, frameExists := stream.frames[ts]

	if stream.hasBadTimestamp && stream.badTimestamp == ts {
		return
	}
	if stream.hasDiscardTimestamp && stream.discardTimestamp == ts {
		return
	}

	if pkt.FragCount > limits.MaxFragments {
		if frameExists {
			delete(stream.frames, ts)
			a.recordDroppedTimestampLocked(stream, ts, limits)
		}
		stream.badTimestamp = ts
		stream.hasBadTimestamp = true
		stream.needKeyframe = true
		a.malicious.Add(1)
		a.dropped.Add(1)
		return
	}

	if !frameExists {
		frame = &frameState{
			timestamp: ts,
			fragCount: pkt.FragCount,
			fragments: make(map[int]fragment),
		}
		stream.frames[ts] = frame
	} else if frame.fragCount != pkt.FragCount {
		a.rejected.Add(1)
		return
	}

	if _, duplicate := frame.fragments[pkt.FragIndex]; duplicate {
		a.duplicates.Add(1)
		return
	}

	extSeq := extendSequence(stream, pkt.Seq)
	if stream.hasHighSeq {
		if extSeq < stream.highestSeq {
			if stream.highestSeq-extSeq > int64(limits.ReorderWindow) {
				if len(frame.fragments) == 0 {
					delete(stream.frames, ts)
				}
				a.late.Add(1)
				return
			}
			a.reordered.Add(1)
		} else if extSeq > stream.highestSeq {
			stream.highestSeq = extSeq
		}
	} else {
		stream.highestSeq = extSeq
		stream.hasHighSeq = true
	}

	frame.fragments[pkt.FragIndex] = fragment{
		seq:     extSeq,
		payload: append([]byte(nil), pkt.Payload...),
	}
	frame.marker = frame.marker || pkt.Marker
	frame.keyframe = frame.keyframe || pkt.Keyframe

	if !frame.completed && frame.marker && len(frame.fragments) == frame.fragCount {
		frame.completed = true
		a.completed.Add(1)
		if stream.needKeyframe {
			if frame.keyframe {
				stream.needKeyframe = false
				stream.limitEvicted = false
			} else if stream.limitEvicted {
				stream.needKeyframe = false
				stream.limitEvicted = false
			} else {
				delete(stream.frames, ts)
				stream.discardTimestamp = ts
				stream.hasDiscardTimestamp = true
				a.dropped.Add(1)
				return
			}
		}
	}

	a.enforceLimitsLocked(stream, limits)
}

// extendSequence maps a 16 bit sequence number onto a comparable extended
// sequence. The signed delta handles the wrap boundary. An initial post-wrap
// packet can leave earlier packets at negative values, which still works with
// the reorder window.
func extendSequence(stream *streamState, seq uint16) int64 {
	if !stream.hasHighSeq {
		return int64(seq)
	}
	delta := int64(int16(seq - uint16(stream.highestSeq)))
	return stream.highestSeq + delta
}

func (a *FrameAssembler) enforceLimitsLocked(stream *streamState, limits FrameLimits) {
	for len(stream.frames) > 0 {
		oldestTS, total := stream.oldestFrame()
		if total <= limits.MaxPackets && !stream.spanExceeded(limits.MaxFrameSpan) {
			return
		}
		delete(stream.frames, oldestTS)
		a.recordDroppedTimestampLocked(stream, oldestTS, limits)
		a.dropped.Add(1)
	}
}

func (a *FrameAssembler) reserveFrameCapacityLocked(stream *streamState, limits FrameLimits, currentTS uint32, incoming int) {
	for len(stream.frames) > 0 {
		oldestTS, total := stream.oldestFrame()
		if uint64(total)+uint64(incoming) <= uint64(limits.MaxPackets) {
			return
		}
		oldest := stream.frames[oldestTS]
		delete(stream.frames, oldestTS)
		a.recordDroppedTimestampLocked(stream, oldestTS, limits)
		a.dropped.Add(1)
		if oldestTS < currentTS && len(oldest.fragments) < oldest.fragCount {
			a.missingDrops.Add(1)
			stream.needKeyframe = true
		}
		stream.limitEvicted = true
	}
}

func (a *FrameAssembler) recordDroppedTimestampLocked(stream *streamState, ts uint32, limits FrameLimits) {
	if stream.droppedTimestamp == nil {
		stream.droppedTimestamp = make(map[uint32]struct{})
	}
	if _, exists := stream.droppedTimestamp[ts]; exists {
		return
	}
	stream.droppedTimestamp[ts] = struct{}{}

	maxEntries64 := uint64(limits.MaxPackets)
	for _, value := range []int{limits.MaxFragments, limits.ReorderWindow} {
		if ^uint64(0)-maxEntries64 < uint64(value) {
			maxEntries64 = ^uint64(0)
			break
		}
		maxEntries64 += uint64(value)
	}
	maxEntries := int(^uint(0) >> 1)
	if maxEntries64 < uint64(maxEntries) {
		maxEntries = int(maxEntries64)
	}
	for len(stream.droppedTimestamp) > maxEntries {
		var oldest uint32
		first := true
		for candidate := range stream.droppedTimestamp {
			if first || candidate < oldest {
				oldest = candidate
				first = false
			}
		}
		delete(stream.droppedTimestamp, oldest)
	}
}

func (s *streamState) oldestFrame() (uint32, int) {
	var (
		oldestTS uint32
		total    int
		first    = true
	)
	for ts, frame := range s.frames {
		total += len(frame.fragments)
		if first || ts < oldestTS {
			oldestTS = ts
			first = false
		}
	}
	return oldestTS, total
}

func (s *streamState) spanExceeded(maxSpan int) bool {
	var (
		minTS uint32
		maxTS uint32
		first = true
	)
	for ts := range s.frames {
		if first {
			minTS, maxTS = ts, ts
			first = false
			continue
		}
		if ts < minTS {
			minTS = ts
		}
		if ts > maxTS {
			maxTS = ts
		}
	}
	if first {
		return false
	}
	return uint64(maxTS)-uint64(minTS) > uint64(maxSpan)
}

// PollFrame returns the next complete frame in presentation order.
func (a *FrameAssembler) PollFrame() (*VideoFrame, bool) {
	a.mu.Lock()
	streams := a.lockAllStreamsLocked()
	defer func() {
		unlockStreams(streams)
		a.mu.Unlock()
	}()

	var (
		chosenStream *streamState
		chosenFrame  *frameState
		chosenSSRC   uint32
		found        bool
	)
	for _, ssrc := range sortedStreamKeys(a.streams) {
		stream := a.streams[ssrc]
		for _, frame := range stream.frames {
			if !frame.completed {
				continue
			}
			if !found || frame.timestamp < chosenFrame.timestamp ||
				(frame.timestamp == chosenFrame.timestamp && ssrc < chosenSSRC) {
				chosenStream = stream
				chosenFrame = frame
				chosenSSRC = ssrc
				found = true
			}
		}
	}
	if !found {
		return nil, false
	}

	delete(chosenStream.frames, chosenFrame.timestamp)
	chosenStream.deliveredWatermark = chosenFrame.timestamp
	chosenStream.hasDelivered = true

	payload := make([]byte, 0, payloadSize(chosenFrame))
	var lastSeq int64
	haveSeq := false
	for index := 0; index < chosenFrame.fragCount; index++ {
		part := chosenFrame.fragments[index]
		payload = append(payload, part.payload...)
		if !haveSeq || part.seq > lastSeq {
			lastSeq = part.seq
			haveSeq = true
		}
	}

	return &VideoFrame{
		SSRC:      chosenSSRC,
		Timestamp: chosenFrame.timestamp,
		LastSeq:   uint32(uint16(lastSeq)),
		Keyframe:  chosenFrame.keyframe,
		Fragments: chosenFrame.fragCount,
		Payload:   payload,
	}, true
}

func payloadSize(frame *frameState) int {
	total := 0
	for _, part := range frame.fragments {
		total += len(part.payload)
	}
	return total
}

// Flush drops every buffered packet of one SSRC and reports how many were freed.
func (a *FrameAssembler) Flush(ssrc uint32) int {
	a.mu.Lock()
	stream := a.streams[ssrc]
	if stream == nil {
		a.mu.Unlock()
		return 0
	}
	stream.mu.Lock()
	a.mu.Unlock()
	defer stream.mu.Unlock()

	_, total := stream.oldestFrame()
	if total == 0 {
		return 0
	}

	stream.frames = make(map[uint32]*frameState)
	stream.droppedTimestamp = make(map[uint32]struct{})
	stream.hasMaxTimestamp = false
	stream.hasHighSeq = false
	stream.hasBadTimestamp = false
	stream.hasDiscardTimestamp = false
	stream.needKeyframe = true
	stream.limitEvicted = false
	a.dropped.Add(uint64(total))
	return total
}

// SetLimits replaces the buffer limits.
func (a *FrameAssembler) SetLimits(limits FrameLimits) error {
	if !validLimits(limits) {
		return ErrLimitsRejected
	}
	a.mu.Lock()
	a.limits = limits
	a.mu.Unlock()
	return nil
}

// Pending reports how many packets are buffered for one SSRC.
func (a *FrameAssembler) Pending(ssrc uint32) int {
	a.mu.Lock()
	stream := a.streams[ssrc]
	if stream == nil {
		a.mu.Unlock()
		return 0
	}
	stream.mu.Lock()
	a.mu.Unlock()
	defer stream.mu.Unlock()

	_, total := stream.oldestFrame()
	return total
}

// Snapshot serialises the buffers, the delivery watermarks and the counters.
func (a *FrameAssembler) Snapshot() ([]byte, error) {
	a.mu.Lock()
	streams := a.lockAllStreamsLocked()
	snap := snapshotData{
		Version: snapshotVersion,
		Limits:  a.limits,
		Streams: make([]snapshotStream, 0, len(a.streams)),
	}
	buffered := 0
	for _, ssrc := range sortedStreamKeys(a.streams) {
		stream := a.streams[ssrc]
		snapStream := snapshotStream{
			SSRC:                ssrc,
			HighestSeq:          stream.highestSeq,
			HasHighestSeq:       stream.hasHighSeq,
			MaxTimestamp:        stream.maxTimestamp,
			HasMaxTimestamp:     stream.hasMaxTimestamp,
			DeliveredWatermark:  stream.deliveredWatermark,
			HasDelivered:        stream.hasDelivered,
			NeedKeyframe:        stream.needKeyframe,
			LimitEvicted:        stream.limitEvicted,
			BadTimestamp:        stream.badTimestamp,
			HasBadTimestamp:     stream.hasBadTimestamp,
			DiscardTimestamp:    stream.discardTimestamp,
			HasDiscardTimestamp: stream.hasDiscardTimestamp,
			DroppedTimestamps:   sortedDroppedTimestampKeys(stream.droppedTimestamp),
			Frames:              make([]snapshotFrame, 0, len(stream.frames)),
		}
		for _, ts := range sortedFrameKeys(stream.frames) {
			frame := stream.frames[ts]
			snapFrame := snapshotFrame{
				Timestamp: ts,
				FragCount: frame.fragCount,
				Marker:    frame.marker,
				Keyframe:  frame.keyframe,
				Completed: frame.completed,
				Parts:     make([]snapshotPart, 0, len(frame.fragments)),
			}
			buffered += len(frame.fragments)
			for _, index := range sortedFragmentKeys(frame.fragments) {
				part := frame.fragments[index]
				snapFrame.Parts = append(snapFrame.Parts, snapshotPart{
					Index:   index,
					Seq:     part.seq,
					Payload: append([]byte(nil), part.payload...),
				})
			}
			snapStream.Frames = append(snapStream.Frames, snapFrame)
		}
		snap.Streams = append(snap.Streams, snapStream)
	}
	snap.Stats = a.loadStatsLocked(buffered)
	unlockStreams(streams)
	a.mu.Unlock()

	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(snap); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded.Bytes())
	result := make([]byte, 0, len(snapshotMagic)+sha256.Size+encoded.Len())
	result = append(result, snapshotMagic...)
	result = append(result, sum[:]...)
	result = append(result, encoded.Bytes()...)
	return result, nil
}

// Restore rebuilds the assembler state from a snapshot.
func (a *FrameAssembler) Restore(payload []byte) error {
	if len(payload) < len(snapshotMagic)+sha256.Size {
		return ErrSnapshotCorrupt
	}
	if !bytes.Equal(payload[:len(snapshotMagic)], snapshotMagic) {
		return ErrSnapshotCorrupt
	}
	expectedSum := payload[len(snapshotMagic) : len(snapshotMagic)+sha256.Size]
	data := payload[len(snapshotMagic)+sha256.Size:]
	sum := sha256.Sum256(data)
	if !bytes.Equal(expectedSum, sum[:]) {
		return ErrSnapshotCorrupt
	}

	decoder := gob.NewDecoder(bytes.NewReader(data))
	var snap snapshotData
	if err := decoder.Decode(&snap); err != nil {
		return ErrSnapshotCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrSnapshotCorrupt
	}
	restored, _, ok := validateSnapshot(snap)
	if !ok {
		return ErrSnapshotCorrupt
	}

	a.mu.Lock()
	oldStreams := a.lockAllStreamsLocked()
	a.streams = restored
	a.limits = snap.Limits
	a.pushed.Store(snap.Stats.Pushed)
	a.rejected.Store(snap.Stats.Rejected)
	a.duplicates.Store(snap.Stats.Duplicates)
	a.reordered.Store(snap.Stats.Reordered)
	a.late.Store(snap.Stats.Late)
	a.completed.Store(snap.Stats.Completed)
	a.dropped.Store(snap.Stats.Dropped)
	a.missingDrops.Store(snap.Stats.MissingDrops)
	a.malicious.Store(snap.Stats.Malicious)
	unlockStreams(oldStreams)
	a.mu.Unlock()
	return nil
}

type snapshotData struct {
	Version int
	Limits  FrameLimits
	Stats   AssemblerStats
	Streams []snapshotStream
}

type snapshotStream struct {
	SSRC                uint32
	Frames              []snapshotFrame
	HighestSeq          int64
	HasHighestSeq       bool
	MaxTimestamp        uint32
	HasMaxTimestamp     bool
	DeliveredWatermark  uint32
	HasDelivered        bool
	NeedKeyframe        bool
	LimitEvicted        bool
	BadTimestamp        uint32
	HasBadTimestamp     bool
	DiscardTimestamp    uint32
	HasDiscardTimestamp bool
	DroppedTimestamps   []uint32
}

type snapshotFrame struct {
	Timestamp uint32
	FragCount int
	Marker    bool
	Keyframe  bool
	Completed bool
	Parts     []snapshotPart
}

type snapshotPart struct {
	Index   int
	Seq     int64
	Payload []byte
}

func validateSnapshot(snap snapshotData) (map[uint32]*streamState, int, bool) {
	if snap.Version != snapshotVersion || !validLimits(snap.Limits) {
		return nil, 0, false
	}
	restored := make(map[uint32]*streamState, len(snap.Streams))
	buffered := 0
	for _, snapStream := range snap.Streams {
		if _, duplicate := restored[snapStream.SSRC]; duplicate {
			return nil, 0, false
		}
		stream := newStreamState()
		stream.highestSeq = snapStream.HighestSeq
		stream.hasHighSeq = snapStream.HasHighestSeq
		stream.maxTimestamp = snapStream.MaxTimestamp
		stream.hasMaxTimestamp = snapStream.HasMaxTimestamp
		stream.deliveredWatermark = snapStream.DeliveredWatermark
		stream.hasDelivered = snapStream.HasDelivered
		stream.needKeyframe = snapStream.NeedKeyframe
		stream.limitEvicted = snapStream.LimitEvicted
		stream.badTimestamp = snapStream.BadTimestamp
		stream.hasBadTimestamp = snapStream.HasBadTimestamp
		stream.discardTimestamp = snapStream.DiscardTimestamp
		stream.hasDiscardTimestamp = snapStream.HasDiscardTimestamp
		for _, ts := range snapStream.DroppedTimestamps {
			if _, duplicate := stream.droppedTimestamp[ts]; duplicate {
				return nil, 0, false
			}
			stream.droppedTimestamp[ts] = struct{}{}
		}

		for _, snapFrame := range snapStream.Frames {
			if _, duplicate := stream.frames[snapFrame.Timestamp]; duplicate ||
				snapFrame.FragCount < 1 || len(snapFrame.Parts) > snapFrame.FragCount {
				return nil, 0, false
			}
			frame := &frameState{
				timestamp: snapFrame.Timestamp,
				fragCount: snapFrame.FragCount,
				marker:    snapFrame.Marker,
				keyframe:  snapFrame.Keyframe,
				completed: snapFrame.Completed,
				fragments: make(map[int]fragment, len(snapFrame.Parts)),
			}
			for _, snapPart := range snapFrame.Parts {
				if snapPart.Index < 0 || snapPart.Index >= snapFrame.FragCount ||
					len(snapPart.Payload) == 0 {
					return nil, 0, false
				}
				if _, duplicate := frame.fragments[snapPart.Index]; duplicate {
					return nil, 0, false
				}
				frame.fragments[snapPart.Index] = fragment{
					seq:     snapPart.Seq,
					payload: append([]byte(nil), snapPart.Payload...),
				}
			}
			complete := len(frame.fragments) == frame.fragCount && frame.marker
			if frame.completed != complete {
				return nil, 0, false
			}
			buffered += len(frame.fragments)
			stream.frames[frame.timestamp] = frame
		}
		restored[snapStream.SSRC] = stream
	}
	return restored, buffered, true
}

// Stats reports the assembler counters.
func (a *FrameAssembler) Stats() AssemblerStats {
	a.mu.Lock()
	streams := a.lockAllStreamsLocked()
	buffered := 0
	for _, stream := range a.streams {
		_, total := stream.oldestFrame()
		buffered += total
	}
	stats := a.loadStatsLocked(buffered)
	unlockStreams(streams)
	a.mu.Unlock()
	return stats
}

func (a *FrameAssembler) loadStatsLocked(buffered int) AssemblerStats {
	return AssemblerStats{
		Pushed:       a.pushed.Load(),
		Rejected:     a.rejected.Load(),
		Duplicates:   a.duplicates.Load(),
		Reordered:    a.reordered.Load(),
		Late:         a.late.Load(),
		Completed:    a.completed.Load(),
		Dropped:      a.dropped.Load(),
		MissingDrops: a.missingDrops.Load(),
		Malicious:    a.malicious.Load(),
		Buffered:     buffered,
	}
}

// Reset clears every stream buffer and counter.
func (a *FrameAssembler) Reset() error {
	a.mu.Lock()
	oldStreams := a.lockAllStreamsLocked()
	a.streams = make(map[uint32]*streamState)
	a.pushed.Store(0)
	a.rejected.Store(0)
	a.duplicates.Store(0)
	a.reordered.Store(0)
	a.late.Store(0)
	a.completed.Store(0)
	a.dropped.Store(0)
	a.missingDrops.Store(0)
	a.malicious.Store(0)
	unlockStreams(oldStreams)
	a.mu.Unlock()
	return nil
}

func (a *FrameAssembler) lockAllStreamsLocked() []*streamState {
	streams := make([]*streamState, 0, len(a.streams))
	for _, ssrc := range sortedStreamKeys(a.streams) {
		stream := a.streams[ssrc]
		stream.mu.Lock()
		streams = append(streams, stream)
	}
	return streams
}

func unlockStreams(streams []*streamState) {
	for i := len(streams) - 1; i >= 0; i-- {
		streams[i].mu.Unlock()
	}
}

func sortedStreamKeys(streams map[uint32]*streamState) []uint32 {
	keys := make([]uint32, 0, len(streams))
	for ssrc := range streams {
		keys = append(keys, ssrc)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedFrameKeys(frames map[uint32]*frameState) []uint32 {
	keys := make([]uint32, 0, len(frames))
	for ts := range frames {
		keys = append(keys, ts)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedFragmentKeys(fragments map[int]fragment) []int {
	keys := make([]int, 0, len(fragments))
	for index := range fragments {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	return keys
}

func sortedDroppedTimestampKeys(timestamps map[uint32]struct{}) []uint32 {
	keys := make([]uint32, 0, len(timestamps))
	for ts := range timestamps {
		keys = append(keys, ts)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
