package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

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

const (
	maxInt = int(^uint(0) >> 1)

	snapshotFormat = "fmp4-muxer-v1"
)

var snapshotMagic = []byte(snapshotFormat + ":")

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

type trackState struct {
	id        uint32
	timescale uint32
	handler   string

	locked bool

	hasDTS  bool
	lastDTS int64

	hasLast bool
	last    Sample
}

// Fmp4Muxer packs encoded frames into fragmented MP4.
type Fmp4Muxer struct {
	mu sync.Mutex

	limit  int
	tracks map[uint32]*trackState
	order  []uint32

	pending      []Sample
	pendingBytes int

	// fragmentTracks is the track set captured when the open fragment
	// receives its first sample. Tracks added later start in the next
	// fragment.
	fragmentTracks []uint32

	nextSequence uint32
	stats        MuxStats
}

// NewFmp4Muxer validates the configuration and builds a muxer.
func NewFmp4Muxer(cfg MuxConfig) (*Fmp4Muxer, error) {
	if cfg.FragmentLimit < 0 {
		return nil, ErrLimitRejected
	}
	return &Fmp4Muxer{
		limit:        cfg.FragmentLimit,
		tracks:       make(map[uint32]*trackState),
		nextSequence: 1,
	}, nil
}

// AddTrack registers one track with its decode time base.
func (m *Fmp4Muxer) AddTrack(cfg TrackConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.ID == 0 || cfg.Timescale == 0 {
		m.stats.Rejects++
		return ErrTrackRejected
	}
	if _, exists := m.tracks[cfg.ID]; exists {
		m.stats.Rejects++
		return ErrTrackRejected
	}

	m.tracks[cfg.ID] = &trackState{
		id:        cfg.ID,
		timescale: cfg.Timescale,
		handler:   cfg.Handler,
	}
	m.order = append(m.order, cfg.ID)
	return nil
}

// WriteSample appends one encoded frame to the pending fragment.
func (m *Fmp4Muxer) WriteSample(s Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	track, ok := m.tracks[s.TrackID]
	if !ok {
		m.stats.Rejects++
		return ErrTrackRejected
	}
	if s.Duration <= 0 || s.Size <= 0 {
		m.stats.Rejects++
		return ErrSampleRejected
	}
	if m.limit > 0 && s.Size > m.limit {
		// A single frame cannot be made smaller than one fragment.
		m.stats.Rejects++
		return ErrSampleRejected
	}

	if track.hasLast && sameSample(track.last, s) {
		m.stats.Duplicates++
		return nil
	}
	if track.hasDTS && s.DTS < track.lastDTS {
		m.stats.Rejects++
		return ErrDtsRegressed
	}
	if m.limit == 0 && m.pendingBytes > maxInt-s.Size {
		m.stats.Rejects++
		return ErrSampleRejected
	}

	// A track added after the current fragment started cannot contribute
	// samples to it. Close the old track set and start a new fragment.
	if len(m.pending) > 0 && !containsTrack(m.fragmentTracks, s.TrackID) {
		m.closePrefix(len(m.pending), false)
	}

	// The first sample of a fragment fixes its complete track list.
	if len(m.pending) == 0 {
		m.fragmentTracks = cloneTracks(m.order)
	}

	if m.limit > 0 && m.exceedsLimit(s.Size) {
		m.splitForLimit(s.Size)
		if len(m.pending) == 0 {
			m.fragmentTracks = cloneTracks(m.order)
		}
	}

	m.pending = append(m.pending, s)
	m.pendingBytes += s.Size

	track.locked = true
	track.hasDTS = true
	track.lastDTS = s.DTS
	track.hasLast = true
	track.last = s

	m.stats.Samples++
	m.stats.Bytes += uint64(s.Size)
	return nil
}

// FlushFragment closes the pending fragment and returns it.
func (m *Fmp4Muxer) FlushFragment() (*Fragment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.pending) == 0 {
		m.stats.Rejects++
		return nil, ErrEmptyFragment
	}

	frag := m.makeFragment(m.pending)
	m.pending = nil
	m.pendingBytes = 0
	m.fragmentTracks = nil
	return frag, nil
}

// SetFragmentLimit replaces the byte limit of one fragment.
func (m *Fmp4Muxer) SetFragmentLimit(limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit <= 0 {
		m.stats.Rejects++
		return ErrLimitRejected
	}
	if limit == m.limit {
		return nil
	}

	// Lowering the limit below an already pending fragment must not leave
	// an over-limit fragment in place. Split it first; if a single pending
	// sample is itself larger than the new limit, the change is rejected.
	if len(m.pending) > 0 {
		for _, s := range m.pending {
			if s.Size > limit {
				m.stats.Rejects++
				return ErrLimitRejected
			}
		}

		oldLimit := m.limit
		m.limit = limit
		for m.pendingBytes > m.limit {
			split := m.prefixForLimit()
			if split <= 0 || split >= len(m.pending) {
				m.limit = oldLimit
				m.stats.Rejects++
				return ErrLimitRejected
			}
			m.closePrefix(split, true)
		}
		return nil
	}

	m.limit = limit
	return nil
}

// SetTrackTimescale changes the decode time base of a track that has not
// received any sample yet.
func (m *Fmp4Muxer) SetTrackTimescale(trackID uint32, timescale uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	track, ok := m.tracks[trackID]
	if !ok {
		m.stats.Rejects++
		return ErrTrackRejected
	}
	if track.locked {
		m.stats.Rejects++
		return ErrTimescaleLocked
	}
	if timescale == 0 {
		m.stats.Rejects++
		return ErrTrackRejected
	}
	track.timescale = timescale
	return nil
}

// PendingBytes reports how many sample bytes wait in the open fragment.
func (m *Fmp4Muxer) PendingBytes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pendingBytes
}

// Sequence reports the sequence number the next fragment will carry.
func (m *Fmp4Muxer) Sequence() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nextSequence
}

// BaseDecodeTime reports the base decode time of one track in the open
// fragment.
func (m *Fmp4Muxer) BaseDecodeTime(trackID uint32) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tracks[trackID]; !ok {
		return 0, ErrTrackRejected
	}
	for _, s := range m.pending {
		if s.TrackID == trackID {
			return s.DTS, nil
		}
	}
	if track := m.tracks[trackID]; track.hasDTS {
		return track.lastDTS, nil
	}
	return 0, nil
}

// TrackCount reports how many tracks are registered.
func (m *Fmp4Muxer) TrackCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tracks)
}

type snapshotTrack struct {
	ID        uint32 `json:"id"`
	Timescale uint32 `json:"timescale"`
	Handler   string `json:"handler"`

	Locked bool `json:"locked"`

	HasDTS  bool  `json:"has_dts"`
	LastDTS int64 `json:"last_dts"`

	HasLast bool   `json:"has_last"`
	Last    Sample `json:"last"`
}

type snapshotState struct {
	Format string `json:"format"`

	Limit        int    `json:"limit"`
	NextSequence uint32 `json:"next_sequence"`

	Tracks         []snapshotTrack `json:"tracks"`
	FragmentTracks []uint32        `json:"fragment_tracks"`
	Pending        []Sample        `json:"pending"`

	Stats MuxStats `json:"stats"`
}

// Snapshot serialises the muxer state.
func (m *Fmp4Muxer) Snapshot() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := snapshotState{
		Format:         snapshotFormat,
		Limit:          m.limit,
		NextSequence:   m.nextSequence,
		Tracks:         make([]snapshotTrack, 0, len(m.order)),
		FragmentTracks: cloneTracks(m.fragmentTracks),
		Pending:        append([]Sample(nil), m.pending...),
		Stats:          m.stats,
	}
	for _, id := range m.order {
		t := m.tracks[id]
		state.Tracks = append(state.Tracks, snapshotTrack{
			ID:        t.id,
			Timescale: t.timescale,
			Handler:   t.handler,
			Locked:    t.locked,
			HasDTS:    t.hasDTS,
			LastDTS:   t.lastDTS,
			HasLast:   t.hasLast,
			Last:      t.last,
		})
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	header := append(append([]byte(nil), snapshotMagic...), []byte(hex.EncodeToString(digest[:])+":")...)
	out := make([]byte, 0, len(header)+len(payload))
	out = append(out, header...)
	out = append(out, payload...)
	return out, nil
}

// Restore replaces the muxer state from a snapshot.
func (m *Fmp4Muxer) Restore(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := decodeSnapshot(data)
	if err != nil {
		return ErrMuxerSnapshot
	}

	tracks := make(map[uint32]*trackState, len(state.Tracks))
	order := make([]uint32, 0, len(state.Tracks))
	for _, t := range state.Tracks {
		if _, exists := tracks[t.ID]; exists {
			return ErrMuxerSnapshot
		}
		tracks[t.ID] = &trackState{
			id:        t.ID,
			timescale: t.Timescale,
			handler:   t.Handler,
			locked:    t.Locked,
			hasDTS:    t.HasDTS,
			lastDTS:   t.LastDTS,
			hasLast:   t.HasLast,
			last:      t.Last,
		}
		order = append(order, t.ID)
	}

	// Compute the pending byte count defensively rather than trusting a
	// serialised total.
	pendingBytes64 := int64(0)
	fragSet := make(map[uint32]struct{}, len(state.FragmentTracks))
	for _, id := range state.FragmentTracks {
		if _, ok := tracks[id]; !ok {
			return ErrMuxerSnapshot
		}
		if _, dup := fragSet[id]; dup {
			return ErrMuxerSnapshot
		}
		fragSet[id] = struct{}{}
	}
	if len(state.Pending) == 0 && len(state.FragmentTracks) != 0 {
		return ErrMuxerSnapshot
	}
	if len(state.Pending) > 0 && len(state.FragmentTracks) == 0 {
		return ErrMuxerSnapshot
	}

	pendingDTS := make(map[uint32]int64, len(tracks))
	for _, s := range state.Pending {
		if _, ok := tracks[s.TrackID]; !ok || s.Duration <= 0 || s.Size <= 0 {
			return ErrMuxerSnapshot
		}
		if _, ok := fragSet[s.TrackID]; !ok {
			return ErrMuxerSnapshot
		}
		if prev, ok := pendingDTS[s.TrackID]; ok && s.DTS < prev {
			return ErrMuxerSnapshot
		}
		pendingDTS[s.TrackID] = s.DTS
		pendingBytes64 += int64(s.Size)
		if pendingBytes64 > int64(maxInt) {
			return ErrMuxerSnapshot
		}
	}
	if state.Limit > 0 && pendingBytes64 > int64(state.Limit) {
		return ErrMuxerSnapshot
	}

	// Commit only after the complete snapshot has been validated.
	m.limit = state.Limit
	m.nextSequence = state.NextSequence
	m.tracks = tracks
	m.order = order
	m.fragmentTracks = cloneTracks(state.FragmentTracks)
	m.pending = append([]Sample(nil), state.Pending...)
	m.pendingBytes = int(pendingBytes64)
	m.stats = state.Stats
	return nil
}

// Stats reports the counters kept by the muxer.
func (m *Fmp4Muxer) Stats() MuxStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

func (m *Fmp4Muxer) exceedsLimit(incoming int) bool {
	if m.limit <= 0 {
		return false
	}
	return int64(m.pendingBytes)+int64(incoming) > int64(m.limit)
}

// splitForLimit closes at least one sample before the incoming sample is
// appended. It prefers starting the next fragment on a keyframe, but the
// byte limit always wins and may therefore produce a short fragment.
func (m *Fmp4Muxer) splitForLimit(incoming int) {
	if m.limit <= 0 || len(m.pending) == 0 {
		return
	}

	needed := int64(m.pendingBytes) + int64(incoming) - int64(m.limit)
	if needed <= 0 {
		return
	}

	split := len(m.pending)
	prefix := int64(0)
	for i, s := range m.pending {
		prefix += int64(s.Size)
		if prefix < needed {
			continue
		}
		if prefix > int64(m.limit) {
			break
		}
		next := i + 1
		if next < len(m.pending) && m.pending[next].Keyframe {
			split = next
			break
		}
	}
	m.closePrefix(split, true)
}

// prefixForLimit chooses a non-empty prefix that fits under the limit,
// preferring a keyframe at the start of the remaining fragment.
func (m *Fmp4Muxer) prefixForLimit() int {
	split := 0
	keyframeSplit := 0
	prefix := int64(0)
	for i, s := range m.pending {
		prefix += int64(s.Size)
		if prefix > int64(m.limit) {
			break
		}
		next := i + 1
		if next < len(m.pending) {
			split = next
			if m.pending[next].Keyframe {
				keyframeSplit = next
			}
		}
	}
	if keyframeSplit > 0 {
		return keyframeSplit
	}
	return split
}

func (m *Fmp4Muxer) closePrefix(n int, limitSplit bool) *Fragment {
	if n <= 0 || n > len(m.pending) {
		return nil
	}

	frag := m.makeFragment(m.pending[:n])
	if limitSplit {
		m.stats.LimitSplits++
	}

	m.pending = append([]Sample(nil), m.pending[n:]...)
	m.pendingBytes = 0
	for _, s := range m.pending {
		m.pendingBytes += s.Size
	}
	if len(m.pending) == 0 {
		m.fragmentTracks = nil
		m.pending = nil
	} else {
		// The remainder is a fresh fragment, so tracks registered while
		// the previous fragment was open now belong to its track table.
		m.fragmentTracks = cloneTracks(m.order)
	}
	return frag
}

func (m *Fmp4Muxer) makeFragment(samples []Sample) *Fragment {
	trackIDs := cloneTracks(m.fragmentTracks)
	if len(trackIDs) == 0 {
		trackIDs = cloneTracks(m.order)
	}

	base := make(map[uint32]int64, len(trackIDs))
	for _, id := range trackIDs {
		if track, ok := m.tracks[id]; ok && track.hasDTS {
			base[id] = track.lastDTS
		} else {
			base[id] = 0
		}
	}
	seenBase := make(map[uint32]struct{}, len(trackIDs))

	offsets := make([]int, 0, len(samples))
	total := 0
	for _, s := range samples {
		offsets = append(offsets, total)
		if _, seen := seenBase[s.TrackID]; !seen {
			base[s.TrackID] = s.DTS
			seenBase[s.TrackID] = struct{}{}
		}
		total += s.Size
	}

	frag := &Fragment{
		Sequence:    m.nextSequence,
		TrackIDs:    trackIDs,
		Offsets:     offsets,
		Bytes:       total,
		SampleCount: len(samples),
		BaseTime:    base,
	}
	m.nextSequence++
	m.stats.Fragments++
	return frag
}

func sameSample(a, b Sample) bool {
	return a.TrackID == b.TrackID &&
		a.DTS == b.DTS &&
		a.Duration == b.Duration &&
		a.Size == b.Size &&
		a.Keyframe == b.Keyframe
}

func containsTrack(ids []uint32, id uint32) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func cloneTracks(ids []uint32) []uint32 {
	if len(ids) == 0 {
		return nil
	}
	return append([]uint32(nil), ids...)
}

func decodeSnapshot(data []byte) (snapshotState, error) {
	var state snapshotState

	if !bytes.HasPrefix(data, snapshotMagic) {
		return state, ErrMuxerSnapshot
	}
	rest := data[len(snapshotMagic):]
	if len(rest) < 65 || rest[64] != ':' {
		return state, ErrMuxerSnapshot
	}
	digestText := string(rest[:64])
	payload := rest[65:]
	digest := sha256.Sum256(payload)
	if digestText != hex.EncodeToString(digest[:]) {
		return state, ErrMuxerSnapshot
	}

	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return snapshotState{}, ErrMuxerSnapshot
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return snapshotState{}, ErrMuxerSnapshot
	}

	if state.Format != snapshotFormat || state.Limit < 0 || state.NextSequence == 0 {
		return snapshotState{}, ErrMuxerSnapshot
	}
	for _, t := range state.Tracks {
		if t.ID == 0 || t.Timescale == 0 {
			return snapshotState{}, ErrMuxerSnapshot
		}
		if t.Locked != t.HasDTS || t.HasDTS != t.HasLast {
			return snapshotState{}, ErrMuxerSnapshot
		}
		if t.HasLast {
			if t.Last.TrackID != t.ID || t.Last.DTS != t.LastDTS ||
				t.Last.Duration <= 0 || t.Last.Size <= 0 {
				return snapshotState{}, ErrMuxerSnapshot
			}
		}
	}
	return state, nil
}
