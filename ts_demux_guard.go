package task

import "errors"

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

// TsDemuxGuard tracks the PSI tables of one transport stream and buffers
// elementary stream segments.
type TsDemuxGuard struct {
	options Options
}

// NewTsDemuxGuard builds a guard with the given resource caps.
func NewTsDemuxGuard(opts Options) (*TsDemuxGuard, error) {
	return &TsDemuxGuard{options: opts}, nil
}

// PushTable installs one PAT or PMT section.
func (g *TsDemuxGuard) PushTable(t Table) error {
	return nil
}

// PushES feeds one payload unit of an elementary stream.
func (g *TsDemuxGuard) PushES(p Packet) error {
	return nil
}

// Tables reports the installed tables.
func (g *TsDemuxGuard) Tables() Tables {
	return Tables{}
}

// Stream reports one installed elementary stream.
func (g *TsDemuxGuard) Stream(pid int) (StreamInfo, bool) {
	return StreamInfo{}, false
}

// TableVersion reports the installed PMT version of one program.
func (g *TsDemuxGuard) TableVersion(program int) int {
	return 0
}

// BufferedBytes reports the bytes held by all segment buffers.
func (g *TsDemuxGuard) BufferedBytes() int {
	return 0
}

// Resync drops all partial buffers and lets the next tables install again.
func (g *TsDemuxGuard) Resync() {
}

// SetOptions replaces the resource caps.
func (g *TsDemuxGuard) SetOptions(opts Options) error {
	return nil
}

// Snapshot captures the restorable state.
func (g *TsDemuxGuard) Snapshot() (DemuxState, error) {
	return DemuxState{}, nil
}

// Restore loads a previously captured state.
func (g *TsDemuxGuard) Restore(st DemuxState) error {
	return nil
}

// Stats reports the demux counters.
func (g *TsDemuxGuard) Stats() DemuxStats {
	return DemuxStats{}
}
