package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

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

// FrameAssembler reassembles RTP packets into whole video frames.
type FrameAssembler struct {
	mu     sync.Mutex
	limits FrameLimits
}

// NewFrameAssembler builds an assembler with the given limits.
func NewFrameAssembler(limits FrameLimits) *FrameAssembler {
	return &FrameAssembler{limits: withDefaults(limits)}
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

// Push feeds one packet into the reassembly buffer.
func (a *FrameAssembler) Push(pkt RTPPacket) error {
	return errNotImplemented
}

// PollFrame returns the next complete frame in presentation order.
func (a *FrameAssembler) PollFrame() (*VideoFrame, bool) {
	return nil, false
}

// Flush drops every buffered packet of one SSRC and reports how many were freed.
func (a *FrameAssembler) Flush(ssrc uint32) int {
	return 0
}

// SetLimits replaces the buffer limits.
func (a *FrameAssembler) SetLimits(limits FrameLimits) error {
	return errNotImplemented
}

// Pending reports how many packets are buffered for one SSRC.
func (a *FrameAssembler) Pending(ssrc uint32) int {
	return 0
}

// Snapshot serialises the buffers, the delivery watermarks and the counters.
func (a *FrameAssembler) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore rebuilds the assembler state from a snapshot.
func (a *FrameAssembler) Restore(payload []byte) error {
	return errNotImplemented
}

// Stats reports the assembler counters.
func (a *FrameAssembler) Stats() AssemblerStats {
	return AssemblerStats{}
}

// Reset clears every stream buffer and counter.
func (a *FrameAssembler) Reset() error {
	return errNotImplemented
}
