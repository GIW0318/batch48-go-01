package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Errors reported by the fragmented MP4 muxer.
var (
	ErrTrackRejected   = errors.New("track configuration is not usable")
	ErrSampleRejected  = errors.New("sample is not usable")
	ErrDtsRegressed    = errors.New("sample decode time regressed")
	ErrTimescaleLocked = errors.New("track timescale is locked after the first sample")
	ErrEmptyFragment   = errors.New("fragment has no samples")
	ErrLimitRejected   = errors.New("fragment byte limit is not usable")
	ErrMuxerSnapshot   = errors.New("fmp4 muxer snapshot is not readable")
)

// TrackConfig describes one track of the output file.
type TrackConfig struct {
	ID        uint32
	Timescale uint32
	Handler   string
}

// Sample is one encoded frame handed to the muxer.
type Sample struct {
	TrackID  uint32
	DTS      int64
	Duration int64
	Size     int
	Keyframe bool
}

// Fragment is one moof plus mdat pair.
type Fragment struct {
	Sequence    uint32
	TrackIDs    []uint32
	Offsets     []int
	Bytes       int
	SampleCount int
	BaseTime    map[uint32]int64
}

// MuxConfig bounds the size of a fragment.
type MuxConfig struct {
	FragmentLimit int
}

// MuxStats reports the counters kept by the muxer.
type MuxStats struct {
	Samples     uint64
	Fragments   uint64
	Duplicates  uint64
	Bytes       uint64
	LimitSplits uint64
	Rejects     uint64
}

// Fmp4Muxer packs encoded frames into fragmented MP4.
type Fmp4Muxer struct {
	mu sync.Mutex
}

// NewFmp4Muxer validates the configuration and builds a muxer.
func NewFmp4Muxer(cfg MuxConfig) (*Fmp4Muxer, error) {
	return nil, errNotImplemented
}

// AddTrack registers one track with its decode time base.
func (m *Fmp4Muxer) AddTrack(cfg TrackConfig) error {
	return errNotImplemented
}

// WriteSample appends one encoded frame to the pending fragment.
func (m *Fmp4Muxer) WriteSample(s Sample) error {
	return errNotImplemented
}

// FlushFragment closes the pending fragment and returns it.
func (m *Fmp4Muxer) FlushFragment() (*Fragment, error) {
	return nil, errNotImplemented
}

// SetFragmentLimit replaces the byte limit of one fragment.
func (m *Fmp4Muxer) SetFragmentLimit(bytes int) error {
	return errNotImplemented
}

// SetTrackTimescale changes the decode time base of a track that has not
// received any sample yet.
func (m *Fmp4Muxer) SetTrackTimescale(trackID uint32, timescale uint32) error {
	return errNotImplemented
}

// PendingBytes reports how many sample bytes wait in the open fragment.
func (m *Fmp4Muxer) PendingBytes() int {
	return 0
}

// Sequence reports the sequence number the next fragment will carry.
func (m *Fmp4Muxer) Sequence() uint32 {
	return 0
}

// BaseDecodeTime reports the base decode time of one track in the open
// fragment.
func (m *Fmp4Muxer) BaseDecodeTime(trackID uint32) (int64, error) {
	return 0, errNotImplemented
}

// TrackCount reports how many tracks are registered.
func (m *Fmp4Muxer) TrackCount() int {
	return 0
}

// Snapshot serialises the muxer state.
func (m *Fmp4Muxer) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the muxer state from a snapshot.
func (m *Fmp4Muxer) Restore(data []byte) error {
	return errNotImplemented
}

// Stats reports the counters kept by the muxer.
func (m *Fmp4Muxer) Stats() MuxStats {
	return MuxStats{}
}
