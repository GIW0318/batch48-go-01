package task

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
)

// Errors reported by the discontinuous transmission guard.
var (
	ErrTimelineConfig    = errors.New("dtx configuration is not usable")
	ErrTimelineInSilence = errors.New("voice frame cannot be sent during silence")
	ErrTimelineNotSilent = errors.New("timeline guard is not in a silence period")
	ErrTimelineClock     = errors.New("wall clock is behind the timeline")
	ErrTimelineSnapshot  = errors.New("dtx timeline snapshot is not readable")
)

// Sequence advancement policies for the silence period.
const (
	SeqPerFrame  = "frame"
	SeqPerPacket = "packet"
)

// Packet kinds produced by the guard.
const (
	KindVoice   = "voice"
	KindRefresh = "refresh"
	KindComfort = "comfort"
)

// DtxConfig describes the media clock and the silence policy.
type DtxConfig struct {
	ClockRate     int
	FrameSamples  int
	MaxSilenceMs  int
	MaxNoiseLevel int
	SeqMode       string
}

// DtxPacket is one packet emitted on the discontinuous transmission stream.
type DtxPacket struct {
	Seq       uint32
	Timestamp uint32
	Samples   int
	Kind      string
	Level     int
}

// DtxStats reports the counters kept by the guard.
type DtxStats struct {
	Voices    uint64
	Refreshes uint64
	Comfort   uint64
	Gaps      uint64
	Aligned   uint64
	Kept      uint64
	Resets    uint64
	Silences  uint64
}

// DtxTimelineGuard keeps the send timeline coherent across silence periods.
type DtxTimelineGuard struct {
	mu            sync.Mutex
	cfg           DtxConfig
	frameMS       int
	timestamp     uint32
	seq           uint32
	inSilence     bool
	silenceFrames int
	noiseLevel    int
	stats         DtxStats
}

// NewDtxTimelineGuard validates the configuration and builds a guard.
func NewDtxTimelineGuard(cfg DtxConfig) (*DtxTimelineGuard, error) {
	frameMS, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &DtxTimelineGuard{
		cfg:     cfg,
		frameMS: frameMS,
	}, nil
}

// PushVoice emits one voice frame and advances the timeline.
func (g *DtxTimelineGuard) PushVoice() (DtxPacket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.inSilence {
		return DtxPacket{}, ErrTimelineInSilence
	}
	pkt := g.emitVoiceLocked()
	return pkt, nil
}

// EnterSilence marks the start of a silence period.
func (g *DtxTimelineGuard) EnterSilence() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.inSilence = true
	g.silenceFrames = 0
	g.stats.Silences++
	return nil
}

// SilenceFrame advances the silence clock by one frame interval. The second
// result reports whether a forced refresh packet was emitted.
func (g *DtxTimelineGuard) SilenceFrame() (DtxPacket, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.inSilence {
		return DtxPacket{}, false, ErrTimelineNotSilent
	}

	g.silenceFrames++
	due := g.refreshDueLocked()
	pkt := DtxPacket{}
	if due {
		pkt = DtxPacket{
			Seq:       g.seq,
			Timestamp: g.timestamp,
			Samples:   g.cfg.FrameSamples,
			Kind:      KindRefresh,
		}
		g.stats.Refreshes++
	}

	if g.cfg.SeqMode == SeqPerFrame {
		g.seq++
	} else if due {
		g.seq++
	} else {
		g.stats.Gaps++
	}
	g.timestamp += uint32(g.cfg.FrameSamples)
	return pkt, due, nil
}

// LeaveSilence emits the first voice frame after a silence period and aligns
// the timeline to the supplied wall clock position.
func (g *DtxTimelineGuard) LeaveSilence(at uint64) (DtxPacket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.inSilence {
		return DtxPacket{}, ErrTimelineNotSilent
	}

	current := uint64(g.timestamp)
	if at < current {
		return DtxPacket{}, ErrTimelineClock
	}

	drift := at - current
	frameSamples := uint64(g.cfg.FrameSamples)
	frames := drift / frameSamples
	if frames == 0 {
		g.stats.Kept++
	} else {
		skippedSamples := frames * frameSamples
		g.timestamp += uint32(skippedSamples)
		if g.cfg.SeqMode == SeqPerFrame {
			g.seq += uint32(frames)
		} else {
			g.stats.Gaps += frames
		}
		g.stats.Aligned++
	}

	g.inSilence = false
	g.silenceFrames = 0
	return g.emitVoiceLocked(), nil
}

// ComfortNoise emits one comfort noise packet with a bounded level.
func (g *DtxTimelineGuard) ComfortNoise(level int) (DtxPacket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if level < 0 {
		level = 0
	} else if level > g.cfg.MaxNoiseLevel {
		level = g.cfg.MaxNoiseLevel
	}
	g.noiseLevel = level

	pkt := DtxPacket{
		Seq:       g.seq,
		Timestamp: g.timestamp,
		Samples:   g.cfg.FrameSamples,
		Kind:      KindComfort,
		Level:     level,
	}
	g.seq++
	g.timestamp += uint32(g.cfg.FrameSamples)
	g.stats.Comfort++
	return pkt, nil
}

// SetSampleRate switches the media clock and resets the timeline.
func (g *DtxTimelineGuard) SetSampleRate(rate int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	cfg := g.cfg
	cfg.ClockRate = rate
	frameMS, err := validateConfig(cfg)
	if err != nil {
		return err
	}

	g.cfg = cfg
	g.frameMS = frameMS
	g.timestamp = 0
	g.seq = 0
	g.inSilence = false
	g.silenceFrames = 0
	g.stats.Resets++
	return nil
}

// FrameDurationMs reports the duration of one frame in milliseconds.
func (g *DtxTimelineGuard) FrameDurationMs() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.frameMS
}

// Timestamp reports the next timestamp the guard would use.
func (g *DtxTimelineGuard) Timestamp() uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.timestamp
}

// Seq reports the next sequence number the guard would use.
func (g *DtxTimelineGuard) Seq() uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seq
}

// InSilence reports whether the guard is inside a silence period.
func (g *DtxTimelineGuard) InSilence() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inSilence
}

// SilenceFrames reports how many frame intervals the current silence covers.
func (g *DtxTimelineGuard) SilenceFrames() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.silenceFrames
}

// Snapshot serialises the timeline state.
func (g *DtxTimelineGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	data := make([]byte, dtxSnapshotSize)
	copy(data[0:4], dtxSnapshotMagic)
	data[4] = dtxSnapshotVersion
	if g.inSilence {
		data[5] = dtxSnapshotFlagSilence
	}
	binary.BigEndian.PutUint32(data[8:12], g.timestamp)
	binary.BigEndian.PutUint32(data[12:16], g.seq)
	binary.BigEndian.PutUint64(data[16:24], uint64(g.silenceFrames))
	binary.BigEndian.PutUint64(data[24:32], uint64(g.noiseLevel))

	counters := []uint64{
		g.stats.Voices,
		g.stats.Refreshes,
		g.stats.Comfort,
		g.stats.Gaps,
		g.stats.Aligned,
		g.stats.Kept,
		g.stats.Resets,
		g.stats.Silences,
	}
	for i, value := range counters {
		offset := dtxSnapshotStatsOffset + i*8
		binary.BigEndian.PutUint64(data[offset:offset+8], value)
	}

	checksum := crc32.ChecksumIEEE(data[:dtxSnapshotPayloadSize])
	binary.BigEndian.PutUint32(data[dtxSnapshotPayloadSize:], checksum)
	return data, nil
}

// Restore replaces the timeline state from a snapshot.
func (g *DtxTimelineGuard) Restore(data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(data) != dtxSnapshotSize {
		return ErrTimelineSnapshot
	}
	if string(data[0:4]) != dtxSnapshotMagic ||
		data[4] != dtxSnapshotVersion ||
		data[5]&^dtxSnapshotFlagSilence != 0 ||
		data[6] != 0 || data[7] != 0 {
		return ErrTimelineSnapshot
	}
	want := binary.BigEndian.Uint32(data[dtxSnapshotPayloadSize:])
	if crc32.ChecksumIEEE(data[:dtxSnapshotPayloadSize]) != want {
		return ErrTimelineSnapshot
	}

	silence := data[5]&dtxSnapshotFlagSilence != 0
	frames64 := binary.BigEndian.Uint64(data[16:24])
	if frames64 > uint64(^uint(0)>>1) {
		return ErrTimelineSnapshot
	}
	frames := int(frames64)
	if !silence && frames != 0 {
		return ErrTimelineSnapshot
	}

	noise64 := int64(binary.BigEndian.Uint64(data[24:32]))
	if noise64 < 0 {
		return ErrTimelineSnapshot
	}
	noise := int(noise64)
	if noise > g.cfg.MaxNoiseLevel {
		noise = g.cfg.MaxNoiseLevel
	}

	counters := make([]uint64, 8)
	for i := range counters {
		offset := dtxSnapshotStatsOffset + i*8
		counters[i] = binary.BigEndian.Uint64(data[offset : offset+8])
	}

	g.timestamp = binary.BigEndian.Uint32(data[8:12])
	g.seq = binary.BigEndian.Uint32(data[12:16])
	g.inSilence = silence
	g.silenceFrames = frames
	g.noiseLevel = noise
	g.stats = DtxStats{
		Voices:    counters[0],
		Refreshes: counters[1],
		Comfort:   counters[2],
		Gaps:      counters[3],
		Aligned:   counters[4],
		Kept:      counters[5],
		Resets:    counters[6],
		Silences:  counters[7],
	}
	return nil
}

// Stats reports the counters kept by the guard.
func (g *DtxTimelineGuard) Stats() DtxStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

const (
	dtxSnapshotMagic            = "DTXG"
	dtxSnapshotVersion     byte = 1
	dtxSnapshotFlagSilence      = byte(1)
	dtxSnapshotStatsOffset      = 32
	dtxSnapshotPayloadSize      = dtxSnapshotStatsOffset + 8*8
	dtxSnapshotSize             = dtxSnapshotPayloadSize + 4
)

func validateConfig(cfg DtxConfig) (int, error) {
	if cfg.ClockRate <= 0 ||
		cfg.FrameSamples <= 0 ||
		cfg.MaxSilenceMs <= 0 ||
		cfg.MaxNoiseLevel < 0 {
		return 0, ErrTimelineConfig
	}
	if cfg.SeqMode != SeqPerFrame && cfg.SeqMode != SeqPerPacket {
		return 0, ErrTimelineConfig
	}
	frameMS, ok := frameDurationMS(cfg.ClockRate, cfg.FrameSamples)
	if !ok {
		return 0, ErrTimelineConfig
	}
	return frameMS, nil
}

func frameDurationMS(clockRate, frameSamples int) (int, bool) {
	if clockRate <= 0 || frameSamples <= 0 {
		return 0, false
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if int64(frameSamples) > maxInt64/1000 {
		return 0, false
	}
	total := int64(frameSamples) * 1000
	rate := int64(clockRate)
	if total%rate != 0 {
		return 0, false
	}
	ms := total / rate
	if ms <= 0 || uint64(ms) > uint64(^uint(0)>>1) {
		return 0, false
	}
	return int(ms), true
}

func (g *DtxTimelineGuard) refreshDueLocked() bool {
	limitFrames := g.cfg.MaxSilenceMs / g.frameMS
	if limitFrames < 1 {
		limitFrames = 1
	}
	return g.silenceFrames%limitFrames == 0
}

func (g *DtxTimelineGuard) emitVoiceLocked() DtxPacket {
	pkt := DtxPacket{
		Seq:       g.seq,
		Timestamp: g.timestamp,
		Samples:   g.cfg.FrameSamples,
		Kind:      KindVoice,
	}
	g.seq++
	g.timestamp += uint32(g.cfg.FrameSamples)
	g.stats.Voices++
	return pkt
}
