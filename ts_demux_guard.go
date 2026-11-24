package task

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
)

// Table kinds accepted by PushTable.
const (
	TablePAT = "PAT"
	TablePMT = "PMT"
)

// Errors reported by the demux guard.
var (
	ErrUnknownTable     = errors.New("psi table kind is unknown")
	ErrInvalidVersion   = errors.New("psi table version is out of range")
	ErrTableCRC         = errors.New("psi table checksum does not match")
	ErrDuplicateVersion = errors.New("psi table version already installed")
	ErrPIDConflict      = errors.New("two programs share one pmt pid")
	ErrInvalidProgram   = errors.New("pmt refers to an unknown program")
	ErrProgramLimit     = errors.New("program limit reached")
	ErrStreamLimit      = errors.New("elementary stream limit reached")
	ErrUnreferencedPID  = errors.New("pid is not mapped by any pmt")
	ErrBufferFull       = errors.New("segment buffer limit reached")
	ErrScrambledStream  = errors.New("stream is scrambled")
	ErrBadPacket        = errors.New("ts packet is not usable")
	ErrBadState         = errors.New("demux state is not usable")
	ErrInvalidOptions   = errors.New("demux options are not usable")
)

// Options caps the guard resources.
type Options struct {
	SegmentBytes int
	MaxPrograms  int
	MaxStreams   int
}

// StreamSpec is one elementary stream announced by a PMT.
type StreamSpec struct {
	PID       int
	Type      int
	Scrambled bool
}

// Table is one PAT or PMT section handed to the guard.
type Table struct {
	Kind    string
	Program int
	Version int
	PIDs    []int
	Streams []StreamSpec
	CRC     uint32
}

// Packet is one payload unit pushed for an elementary stream pid.
type Packet struct {
	PID     int
	CC      int
	Payload []byte
}

// ProgramInfo is one installed program mapping.
type ProgramInfo struct {
	Number  int
	PMTPID  int
	Version int
}

// StreamInfo is one installed elementary stream.
type StreamInfo struct {
	PID        int
	Program    int
	StreamType int
	Scrambled  bool
	Version    int
}

// Tables is the readable view of the installed tables.
type Tables struct {
	PATVersion  int
	Programs    map[int]ProgramInfo
	PMTVersions map[int]int
	Streams     map[int]StreamInfo
}

// DemuxStats counts demux activity.
type DemuxStats struct {
	Tables        int
	Duplicates    int
	CRCFailures   int
	Conflicts     int
	Packets       int
	Unreferenced  int
	Scrambled     int
	BufferFull    int
	BytesBuffered int
}

// DemuxState is the restorable state of one guard.
type DemuxState struct {
	PATVersion  int
	Programs    map[int]ProgramInfo
	PMTVersions map[int]int
	Streams     map[int]StreamInfo
	Buffered    map[int][]byte
	Stats       DemuxStats
	Epoch       int
}

// versionUnset marks a PAT version memory that has never been installed (or
// was cleared by Resync). PAT versions themselves are the five bit range
// 0..31, so -1 never collides with a real version.
const versionUnset = -1

// TsDemuxGuard tracks the PSI tables of one transport stream and buffers
// elementary stream segments.
type TsDemuxGuard struct {
	mu   sync.RWMutex
	opts Options

	// patVersion is versionUnset when no PAT version is remembered and the
	// installed five bit version otherwise.
	patVersion int

	programs    map[int]ProgramInfo
	pmtVersions map[int]int
	streams     map[int]StreamInfo

	buffers map[int][]byte

	// Continuity counter bookkeeping. Presence in ccLast means a counter was
	// seen for the pid and ccGap latches a discontinuity marker. Neither
	// changes whether payload is accepted.
	ccLast map[int]int
	ccGap  map[int]bool

	epoch int
	stats DemuxStats
}

// NewTsDemuxGuard builds a guard with the given resource caps.
func NewTsDemuxGuard(opts Options) (*TsDemuxGuard, error) {
	if !validOptions(opts) {
		return nil, ErrInvalidOptions
	}
	g := &TsDemuxGuard{
		opts:        opts,
		patVersion:  versionUnset,
		programs:    make(map[int]ProgramInfo),
		pmtVersions: make(map[int]int),
		streams:     make(map[int]StreamInfo),
		buffers:     make(map[int][]byte),
		ccLast:      make(map[int]int),
		ccGap:       make(map[int]bool),
	}
	return g, nil
}

func validOptions(opts Options) bool {
	return opts.SegmentBytes > 0 && opts.MaxPrograms > 0 && opts.MaxStreams > 0
}

// SetOptions replaces the resource caps. Invalid options are rejected and the
// previously installed options stay in effect.
func (g *TsDemuxGuard) SetOptions(opts Options) error {
	if !validOptions(opts) {
		return ErrInvalidOptions
	}
	g.mu.Lock()
	g.opts = opts
	g.mu.Unlock()
	return nil
}

// tableChecksum reproduces the PSI checksum input: table type byte, version
// byte, program number, the flattened PID list and the stream entries, all
// integers encoded as big endian 16 bit words, followed by IEEE CRC32.
func tableChecksum(t Table) uint32 {
	buf := make([]byte, 0, 16+2*len(t.PIDs)+2+4*len(t.Streams))
	switch t.Kind {
	case TablePAT:
		buf = append(buf, 0x00)
	case TablePMT:
		buf = append(buf, 0x02)
	default:
		buf = append(buf, 0xFF)
	}
	buf = append(buf, byte(t.Version))

	var word [2]byte
	append16 := func(v int) {
		binary.BigEndian.PutUint16(word[:], uint16(v))
		buf = append(buf, word[0], word[1])
	}

	append16(t.Program)
	append16(len(t.PIDs))
	for _, pid := range t.PIDs {
		append16(pid)
	}
	append16(len(t.Streams))
	for _, s := range t.Streams {
		append16(s.PID)
		buf = append(buf, byte(s.Type))
		if s.Scrambled {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	return crc32.ChecksumIEEE(buf)
}

// PushTable installs one PAT or PMT section.
func (g *TsDemuxGuard) PushTable(t Table) error {
	if t.Kind != TablePAT && t.Kind != TablePMT {
		return ErrUnknownTable
	}
	if t.Version < 0 || t.Version > 31 {
		return ErrInvalidVersion
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if tableChecksum(t) != t.CRC {
		g.stats.CRCFailures++
		return ErrTableCRC
	}

	if t.Kind == TablePAT {
		return g.installPATLocked(t)
	}
	return g.installPMTLocked(t)
}

// patPairs decodes the (program, PMT PID) pairs carried by a PAT Table whose
// flattened PIDs slice stores them back to back.
func patPairs(t Table) ([][2]int, bool) {
	if len(t.PIDs)%2 != 0 {
		return nil, false
	}
	pairs := make([][2]int, 0, len(t.PIDs)/2)
	for i := 0; i < len(t.PIDs); i += 2 {
		pairs = append(pairs, [2]int{t.PIDs[i], t.PIDs[i+1]})
	}
	return pairs, true
}

func (g *TsDemuxGuard) installPATLocked(t Table) error {
	// A retransmitted version already installed is ignored wholesale.
	if g.patVersion == t.Version {
		g.stats.Duplicates++
		return ErrDuplicateVersion
	}

	pairs, ok := patPairs(t)
	if !ok {
		return ErrBadPacket
	}

	// Two distinct programs sharing one PMT PID make the PAT unusable. The
	// check runs against the candidate table only, so a rejected PAT leaves
	// the installed mapping untouched.
	pmtOwner := make(map[int]int, len(pairs))
	for _, pair := range pairs {
		program, pmtPID := pair[0], pair[1]
		if owner, seen := pmtOwner[pmtPID]; seen && owner != program {
			g.stats.Conflicts++
			return ErrPIDConflict
		}
		pmtOwner[pmtPID] = program
	}

	next := make(map[int]ProgramInfo, len(pairs))
	for _, pair := range pairs {
		program, pmtPID := pair[0], pair[1]
		info := ProgramInfo{Number: program, PMTPID: pmtPID}
		// A refreshed PAT must not discard the PMT that is still serving a
		// program until the replacement PMT actually arrives.
		if pmtVersion, seen := g.pmtVersions[program]; seen {
			info.Version = pmtVersion
		}
		next[program] = info
	}

	if len(next) > g.opts.MaxPrograms {
		return ErrProgramLimit
	}

	g.programs = next
	g.patVersion = t.Version
	g.stats.Tables++
	return nil
}

func (g *TsDemuxGuard) installPMTLocked(t Table) error {
	// The PMT can only describe a program the current PAT announces.
	if _, known := g.programs[t.Program]; !known {
		return ErrInvalidProgram
	}

	// Same program, same five bit version is a retransmission.
	if installed, seen := g.pmtVersions[t.Program]; seen && installed == t.Version {
		g.stats.Duplicates++
		return ErrDuplicateVersion
	}

	// Replacing one program's PMT rebuilds that program's stream entries
	// wholesale while leaving every other program's streams intact.
	next := make(map[int]StreamInfo, len(g.streams)+len(t.Streams))
	for pid, info := range g.streams {
		if info.Program != t.Program {
			next[pid] = info
		}
	}
	for _, spec := range t.Streams {
		next[spec.PID] = StreamInfo{
			PID:        spec.PID,
			Program:    t.Program,
			StreamType: spec.Type,
			Scrambled:  spec.Scrambled,
			Version:    t.Version,
		}
	}

	if len(next) > g.opts.MaxStreams {
		return ErrStreamLimit
	}

	g.streams = next
	g.pmtVersions[t.Program] = t.Version

	info := g.programs[t.Program]
	info.Version = t.Version
	g.programs[t.Program] = info

	g.stats.Tables++
	return nil
}

// PushES feeds one payload unit of an elementary stream.
func (g *TsDemuxGuard) PushES(p Packet) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.stats.Packets++

	info, referenced := g.streams[p.PID]
	if !referenced {
		g.stats.Unreferenced++
		return ErrUnreferencedPID
	}
	if info.Scrambled {
		g.stats.Scrambled++
		return ErrScrambledStream
	}

	// Continuity gaps are latched as markers but never cause the ordered
	// payload to be discarded.
	g.noteContinuityLocked(p.PID, p.CC)

	if len(g.buffers[p.PID])+len(p.Payload) > g.opts.SegmentBytes {
		g.stats.BufferFull++
		return ErrBufferFull
	}

	g.buffers[p.PID] = append(g.buffers[p.PID], p.Payload...)
	return nil
}

// noteContinuityLocked records the continuity counter and flags a jump. The
// MPEG-TS counter is four bits wide and wraps 15 -> 0; raw monotonically
// increasing integers are accepted as well.
func (g *TsDemuxGuard) noteContinuityLocked(pid, cc int) {
	if last, seen := g.ccLast[pid]; seen && !ccContinues(last, cc) {
		g.ccGap[pid] = true
	}
	g.ccLast[pid] = cc
}

func ccContinues(prev, cur int) bool {
	if cur == prev+1 || cur == prev {
		// Next counter, or a duplicated packet carrying the same counter.
		return true
	}
	prevMod := ((prev % 16) + 16) % 16
	curMod := ((cur % 16) + 16) % 16
	return curMod == (prevMod+1)%16
}

// Tables reports an independent copy of the installed tables. Programs and
// streams are copied under one read lock so callers always observe one
// complete, consistent table generation.
func (g *TsDemuxGuard) Tables() Tables {
	g.mu.RLock()
	defer g.mu.RUnlock()

	programs := make(map[int]ProgramInfo, len(g.programs))
	for number, info := range g.programs {
		programs[number] = info
	}
	versions := make(map[int]int, len(g.pmtVersions))
	for program, version := range g.pmtVersions {
		versions[program] = version
	}
	streams := make(map[int]StreamInfo, len(g.streams))
	for pid, info := range g.streams {
		streams[pid] = info
	}

	return Tables{
		PATVersion:  g.patVersionViewLocked(),
		Programs:    programs,
		PMTVersions: versions,
		Streams:     streams,
	}
}

func (g *TsDemuxGuard) patVersionViewLocked() int {
	if g.patVersion == versionUnset {
		return 0
	}
	return g.patVersion
}

// Stream reports one installed elementary stream.
func (g *TsDemuxGuard) Stream(pid int) (StreamInfo, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	info, ok := g.streams[pid]
	return info, ok
}

// TableVersion reports the installed PMT version of one program.
func (g *TsDemuxGuard) TableVersion(program int) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.pmtVersions[program]
}

// BufferedBytes reports the bytes held by all segment buffers.
func (g *TsDemuxGuard) BufferedBytes() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.bufferedBytesLocked()
}

func (g *TsDemuxGuard) bufferedBytesLocked() int {
	total := 0
	for _, segment := range g.buffers {
		total += len(segment)
	}
	return total
}

// Resync drops all partial buffers and forgets installed table versions so
// the same versions can install again. Mappings, options and cumulative
// statistics are preserved.
func (g *TsDemuxGuard) Resync() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.buffers = make(map[int][]byte)
	g.ccLast = make(map[int]int)
	g.ccGap = make(map[int]bool)

	g.patVersion = versionUnset
	g.pmtVersions = make(map[int]int)
}

// Stats reports the demux counters. BytesBuffered is always derived from the
// live buffers so it matches BufferedBytes.
func (g *TsDemuxGuard) Stats() DemuxStats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	stats := g.stats
	stats.BytesBuffered = g.bufferedBytesLocked()
	return stats
}

// Snapshot captures a deep, restorable copy of the guard state.
func (g *TsDemuxGuard) Snapshot() (DemuxState, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	programs := make(map[int]ProgramInfo, len(g.programs))
	for number, info := range g.programs {
		programs[number] = info
	}
	versions := make(map[int]int, len(g.pmtVersions))
	for program, version := range g.pmtVersions {
		versions[program] = version
	}
	streams := make(map[int]StreamInfo, len(g.streams))
	for pid, info := range g.streams {
		streams[pid] = info
	}
	buffered := make(map[int][]byte, len(g.buffers))
	total := 0
	for pid, segment := range g.buffers {
		dup := append([]byte(nil), segment...)
		buffered[pid] = dup
		total += len(dup)
	}

	stats := g.stats
	stats.BytesBuffered = total

	return DemuxState{
		PATVersion:  g.patVersionViewLocked(),
		Programs:    programs,
		PMTVersions: versions,
		Streams:     streams,
		Buffered:    buffered,
		Stats:       stats,
		Epoch:       g.epoch,
	}, nil
}

// Restore loads a state previously produced by Snapshot, replacing the
// current table, stream and buffer state wholesale. Resource options are not
// part of DemuxState and stay with the receiver.
func (g *TsDemuxGuard) Restore(state DemuxState) error {
	programs := make(map[int]ProgramInfo, len(state.Programs))
	for number, info := range state.Programs {
		programs[number] = info
	}
	versions := make(map[int]int, len(state.PMTVersions))
	for program, version := range state.PMTVersions {
		versions[program] = version
	}
	streams := make(map[int]StreamInfo, len(state.Streams))
	for pid, info := range state.Streams {
		streams[pid] = info
	}
	buffered := make(map[int][]byte, len(state.Buffered))
	for pid, segment := range state.Buffered {
		buffered[pid] = append([]byte(nil), segment...)
	}

	patVersion := state.PATVersion
	switch {
	case patVersion < 0:
		patVersion = versionUnset
	case patVersion == 0 && len(programs) == 0 && len(versions) == 0:
		// The zero value of DemuxState denotes an empty guard rather than a
		// PAT at version 0 with no installed program.
		patVersion = versionUnset
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.patVersion = patVersion
	g.programs = programs
	g.pmtVersions = versions
	g.streams = streams
	g.buffers = buffered
	g.ccLast = make(map[int]int)
	g.ccGap = make(map[int]bool)
	g.epoch = state.Epoch
	g.stats = state.Stats

	return nil
}
