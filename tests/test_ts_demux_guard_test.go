package task

import (
	"encoding/binary"
	"hash/crc32"
	"testing"

	impl "gsb/batch48-go-game-c-08"
)

func psiCRC(t impl.Table) uint32 {
	var buf []byte
	switch t.Kind {
	case impl.TablePAT:
		buf = append(buf, 0x00)
	case impl.TablePMT:
		buf = append(buf, 0x02)
	default:
		buf = append(buf, 0xFF)
	}
	buf = append(buf, byte(t.Version))
	var word [2]byte
	binary.BigEndian.PutUint16(word[:], uint16(t.Program))
	buf = append(buf, word[:]...)
	binary.BigEndian.PutUint16(word[:], uint16(len(t.PIDs)))
	buf = append(buf, word[:]...)
	for _, pid := range t.PIDs {
		binary.BigEndian.PutUint16(word[:], uint16(pid))
		buf = append(buf, word[:]...)
	}
	binary.BigEndian.PutUint16(word[:], uint16(len(t.Streams)))
	buf = append(buf, word[:]...)
	for _, s := range t.Streams {
		binary.BigEndian.PutUint16(word[:], uint16(s.PID))
		buf = append(buf, word[:]...)
		buf = append(buf, byte(s.Type))
		if s.Scrambled {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	return crc32.ChecksumIEEE(buf)
}

func patTable(version int, pairs ...int) impl.Table {
	t := impl.Table{Kind: impl.TablePAT, Version: version, PIDs: pairs}
	t.CRC = psiCRC(t)
	return t
}

func pmtTable(program, version int, streams ...impl.StreamSpec) impl.Table {
	t := impl.Table{Kind: impl.TablePMT, Program: program, Version: version, Streams: streams}
	t.CRC = psiCRC(t)
	return t
}

func newDemuxGuard(t *testing.T) *impl.TsDemuxGuard {
	t.Helper()
	g, err := impl.NewTsDemuxGuard(impl.Options{SegmentBytes: 64, MaxPrograms: 4, MaxStreams: 8})
	if err != nil {
		t.Fatalf("NewTsDemuxGuard returned error: %v", err)
	}
	if g == nil {
		t.Fatal("NewTsDemuxGuard returned nil")
	}
	return g
}

func installedDemuxGuard(t *testing.T) *impl.TsDemuxGuard {
	t.Helper()
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100)); err != nil {
		t.Fatalf("PushTable(PAT) returned error: %v", err)
	}
	if err := g.PushTable(pmtTable(1, 1, impl.StreamSpec{PID: 0x101, Type: 0x0F})); err != nil {
		t.Fatalf("PushTable(PMT) returned error: %v", err)
	}
	return g
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	bad := []impl.Options{
		{SegmentBytes: 0, MaxPrograms: 4, MaxStreams: 8},
		{SegmentBytes: 64, MaxPrograms: 0, MaxStreams: 8},
		{SegmentBytes: 64, MaxPrograms: 4, MaxStreams: 0},
	}
	for i, opts := range bad {
		if _, err := impl.NewTsDemuxGuard(opts); err != impl.ErrInvalidOptions {
			t.Fatalf("case %d: expected ErrInvalidOptions, got %v", i, err)
		}
	}
}

func TestPushTableRejectsUnknownKind(t *testing.T) {
	g := newDemuxGuard(t)
	bad := impl.Table{Kind: "SDT", Version: 0, CRC: 1}
	if err := g.PushTable(bad); err != impl.ErrUnknownTable {
		t.Fatalf("expected ErrUnknownTable, got %v", err)
	}
}

func TestPushTableRejectsOutOfRangeVersion(t *testing.T) {
	g := newDemuxGuard(t)
	table := impl.Table{Kind: impl.TablePAT, Version: 32, PIDs: []int{1, 0x100}}
	table.CRC = psiCRC(table)
	if err := g.PushTable(table); err != impl.ErrInvalidVersion {
		t.Fatalf("expected ErrInvalidVersion, got %v", err)
	}
}

func TestPushTableRejectsBadChecksum(t *testing.T) {
	g := newDemuxGuard(t)
	table := patTable(1, 1, 0x100)
	table.CRC++
	if err := g.PushTable(table); err != impl.ErrTableCRC {
		t.Fatalf("expected ErrTableCRC, got %v", err)
	}
	tables := g.Tables()
	if tables.PATVersion != 0 {
		t.Fatalf("PAT version = %d, want the corrupt table to be discarded", tables.PATVersion)
	}
	if len(tables.Programs) != 0 {
		t.Fatalf("programs = %d, want none after a corrupt PAT", len(tables.Programs))
	}
	if got := g.Stats().CRCFailures; got != 1 {
		t.Fatalf("crc failures = %d, want 1", got)
	}
}

func TestPushTableInstallsProgramMapping(t *testing.T) {
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100)); err != nil {
		t.Fatalf("PushTable(PAT) returned error: %v", err)
	}
	tables := g.Tables()
	if tables.PATVersion != 1 {
		t.Fatalf("PAT version = %d, want 1", tables.PATVersion)
	}
	info, ok := tables.Programs[1]
	if !ok {
		t.Fatalf("program 1 missing from %v", tables.Programs)
	}
	if info.PMTPID != 0x100 {
		t.Fatalf("program 1 PMT pid = %d, want 0x100", info.PMTPID)
	}
}

func TestPushTableRejectsDuplicateVersion(t *testing.T) {
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100)); err != nil {
		t.Fatalf("PushTable(PAT) returned error: %v", err)
	}
	if err := g.PushTable(patTable(1, 3, 0x300)); err != impl.ErrDuplicateVersion {
		t.Fatalf("expected ErrDuplicateVersion, got %v", err)
	}
	tables := g.Tables()
	if _, ok := tables.Programs[1]; !ok {
		t.Fatal("the installed program 1 mapping was lost")
	}
	if _, ok := tables.Programs[3]; ok {
		t.Fatal("the duplicate table was applied")
	}
	if got := g.Stats().Duplicates; got != 1 {
		t.Fatalf("duplicates = %d, want 1", got)
	}
}

func TestPushTableRejectsPIDConflict(t *testing.T) {
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100, 2, 0x100)); err != impl.ErrPIDConflict {
		t.Fatalf("expected ErrPIDConflict, got %v", err)
	}
	if got := g.Tables().PATVersion; got != 0 {
		t.Fatalf("PAT version = %d, want the conflicting PAT to be discarded", got)
	}
	if got := g.Stats().Conflicts; got != 1 {
		t.Fatalf("conflicts = %d, want 1", got)
	}
}

func TestPushTableInstallsStreams(t *testing.T) {
	g := installedDemuxGuard(t)
	stream, ok := g.Stream(0x101)
	if !ok {
		t.Fatal("stream 0x101 was not installed")
	}
	if stream.StreamType != 0x0F {
		t.Fatalf("stream type = %d, want 15", stream.StreamType)
	}
	if stream.Program != 1 {
		t.Fatalf("stream program = %d, want 1", stream.Program)
	}
	if got := g.TableVersion(1); got != 1 {
		t.Fatalf("pmt version = %d, want 1", got)
	}
	if got := g.Stats().Tables; got != 2 {
		t.Fatalf("installed tables = %d, want 2", got)
	}
}

func TestPushTableRejectsUnknownProgram(t *testing.T) {
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100)); err != nil {
		t.Fatalf("PushTable(PAT) returned error: %v", err)
	}
	err := g.PushTable(pmtTable(7, 1, impl.StreamSpec{PID: 0x101, Type: 0x0F}))
	if err != impl.ErrInvalidProgram {
		t.Fatalf("expected ErrInvalidProgram, got %v", err)
	}
}

func TestPushESRejectsUnreferencedPID(t *testing.T) {
	g := installedDemuxGuard(t)
	err := g.PushES(impl.Packet{PID: 0x999, CC: 0, Payload: []byte{1, 2, 3, 4}})
	if err != impl.ErrUnreferencedPID {
		t.Fatalf("expected ErrUnreferencedPID, got %v", err)
	}
	if got := g.Stats().Unreferenced; got != 1 {
		t.Fatalf("unreferenced drops = %d, want 1", got)
	}
	if got := g.BufferedBytes(); got != 0 {
		t.Fatalf("buffered bytes = %d, want an unreferenced pid to stay out of the buffers", got)
	}
}

func TestPushESEnforcesSegmentBufferCap(t *testing.T) {
	g := installedDemuxGuard(t)
	first := make([]byte, 40)
	if err := g.PushES(impl.Packet{PID: 0x101, CC: 0, Payload: first}); err != nil {
		t.Fatalf("PushES returned error: %v", err)
	}
	if got := g.BufferedBytes(); got != 40 {
		t.Fatalf("buffered bytes = %d, want 40", got)
	}
	second := make([]byte, 40)
	if err := g.PushES(impl.Packet{PID: 0x101, CC: 1, Payload: second}); err != impl.ErrBufferFull {
		t.Fatalf("expected ErrBufferFull, got %v", err)
	}
	if got := g.BufferedBytes(); got != 40 {
		t.Fatalf("buffered bytes after overflow = %d, want the existing buffer kept at 40", got)
	}
	if got := g.Stats().BufferFull; got != 1 {
		t.Fatalf("buffer full events = %d, want 1", got)
	}
}

func TestPushESRejectsScrambledStream(t *testing.T) {
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100)); err != nil {
		t.Fatalf("PushTable(PAT) returned error: %v", err)
	}
	if err := g.PushTable(pmtTable(1, 1, impl.StreamSpec{PID: 0x102, Type: 0x03, Scrambled: true})); err != nil {
		t.Fatalf("PushTable(PMT) returned error: %v", err)
	}
	err := g.PushES(impl.Packet{PID: 0x102, CC: 0, Payload: []byte{1, 2, 3}})
	if err != impl.ErrScrambledStream {
		t.Fatalf("expected ErrScrambledStream, got %v", err)
	}
	if got := g.Stats().Scrambled; got != 1 {
		t.Fatalf("scrambled drops = %d, want 1", got)
	}
	if got := g.BufferedBytes(); got != 0 {
		t.Fatalf("buffered bytes = %d, want scrambled payload kept out", got)
	}
}

func TestResyncAllowsSameVersionAgain(t *testing.T) {
	g := newDemuxGuard(t)
	if err := g.PushTable(patTable(1, 1, 0x100)); err != nil {
		t.Fatalf("PushTable(PAT) returned error: %v", err)
	}
	if err := g.PushTable(patTable(1, 1, 0x100)); err != impl.ErrDuplicateVersion {
		t.Fatalf("expected ErrDuplicateVersion before Resync, got %v", err)
	}
	g.Resync()
	if err := g.PushTable(patTable(1, 2, 0x200)); err != nil {
		t.Fatalf("PushTable after Resync returned error: %v", err)
	}
	tables := g.Tables()
	if tables.PATVersion != 1 {
		t.Fatalf("PAT version = %d, want 1 after Resync", tables.PATVersion)
	}
	if info, ok := tables.Programs[2]; !ok || info.PMTPID != 0x200 {
		t.Fatalf("program 2 mapping = %+v, want the post Resync table installed", info)
	}
}

func TestSnapshotRestoreKeepsTablesAndBuffers(t *testing.T) {
	g := installedDemuxGuard(t)
	payload := make([]byte, 40)
	if err := g.PushES(impl.Packet{PID: 0x101, CC: 0, Payload: payload}); err != nil {
		t.Fatalf("PushES returned error: %v", err)
	}
	st, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	r := newDemuxGuard(t)
	if err := r.Restore(st); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if _, ok := r.Stream(0x101); !ok {
		t.Fatal("stream 0x101 missing after Restore")
	}
	if got := r.BufferedBytes(); got != 40 {
		t.Fatalf("buffered bytes after Restore = %d, want 40", got)
	}
	if got := r.Stats().Tables; got != 2 {
		t.Fatalf("installed tables after Restore = %d, want 2", got)
	}
}

func TestTablesReturnsIndependentCopy(t *testing.T) {
	g := installedDemuxGuard(t)
	view := g.Tables()
	delete(view.Programs, 1)
	if _, ok := g.Tables().Programs[1]; !ok {
		t.Fatal("deleting a program from the returned map changed the internal table")
	}
}
