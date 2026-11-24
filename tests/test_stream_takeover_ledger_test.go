package task

import (
	"errors"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-17"
)

type testConn struct {
	id string
}

func (c testConn) ConnID() string { return c.id }

func testPolicy() impl.Policy {
	return impl.Policy{
		MaxSessions:    4,
		MaxReplayBytes: 8,
		TokenTTL:       time.Hour,
	}
}

const resumeToken = "token-abcdef"

func TestRegisterRejectsInvalidArguments(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("", resumeToken); !errors.Is(err, impl.ErrInvalidArgument) {
		t.Fatalf("empty session id must report ErrInvalidArgument, got %v", err)
	}
	if err := l.Register("s1", ""); !errors.Is(err, impl.ErrInvalidArgument) {
		t.Fatalf("empty resume token must report ErrInvalidArgument, got %v", err)
	}
	if ids := l.Sessions(); len(ids) != 0 {
		t.Fatalf("rejected registrations must not create sessions, got %v", ids)
	}
}

func TestRegisterRejectsDuplicateSession(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("first registration must succeed, got %v", err)
	}
	if err := l.Register("s1", "another-token"); !errors.Is(err, impl.ErrDuplicateSession) {
		t.Fatalf("second registration of the same session must report ErrDuplicateSession, got %v", err)
	}
	if ids := l.Sessions(); len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("session table = %v, want exactly [s1]", ids)
	}
	if got := l.Stats().Registered; got != 1 {
		t.Fatalf("registration counter = %d, want 1", got)
	}
}

func TestRegisterEnforcesSessionLimit(t *testing.T) {
	p := testPolicy()
	p.MaxSessions = 2
	l := impl.NewTakeoverLedger(p)
	if err := l.Register("s1", "token-00000001"); err != nil {
		t.Fatalf("first registration must succeed, got %v", err)
	}
	if err := l.Register("s2", "token-00000002"); err != nil {
		t.Fatalf("second registration must succeed, got %v", err)
	}
	if err := l.Register("s3", "token-00000003"); !errors.Is(err, impl.ErrSessionLimit) {
		t.Fatalf("registration past the session cap must report ErrSessionLimit, got %v", err)
	}
	ids := l.Sessions()
	if len(ids) != 2 || ids[0] != "s1" || ids[1] != "s2" {
		t.Fatalf("session table = %v, want [s1 s2] in ascending order", ids)
	}
}

func TestTakeoverRejectsUnknownToken(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Takeover("token-zzzzzzzz", testConn{"c1"}); !errors.Is(err, impl.ErrUnknownToken) {
		t.Fatalf("takeover with an unknown token must report ErrUnknownToken, got %v", err)
	}
	if got := l.Stats().Takeovers; got != 0 {
		t.Fatalf("takeover counter = %d, want 0", got)
	}
}

func TestTakeoverRejectsNilConn(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Takeover(resumeToken, nil); !errors.Is(err, impl.ErrNilConn) {
		t.Fatalf("takeover with a nil connection must report ErrNilConn, got %v", err)
	}
	if s, ok := l.Session("s1"); !ok || s.Conn != nil {
		t.Fatalf("rejected takeover must not attach a connection, session = %+v", s)
	}
}

func TestTakeoverTransfersActiveConnection(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	first, err := l.Takeover(resumeToken, testConn{"c1"})
	if err != nil {
		t.Fatalf("first takeover must succeed, got %v", err)
	}
	if first.Generation != 1 || first.Conn == nil || first.Conn.ConnID() != "c1" || first.ReadOnly {
		t.Fatalf("first handle = %+v, want generation 1 attached to c1 and writable", first)
	}
	second, err := l.Takeover(resumeToken, testConn{"c2"})
	if err != nil {
		t.Fatalf("second takeover must succeed, got %v", err)
	}
	if second.Generation != 2 || second.Conn == nil || second.Conn.ConnID() != "c2" || second.ReadOnly {
		t.Fatalf("second handle = %+v, want generation 2 attached to c2 and writable", second)
	}
	if !first.ReadOnly {
		t.Fatalf("the previous handle must become read only after a takeover")
	}
	stats := l.Stats()
	if stats.Takeovers != 2 {
		t.Fatalf("takeover counter = %d, want 2", stats.Takeovers)
	}
	if stats.FencedConns != 1 {
		t.Fatalf("fenced connection counter = %d, want 1", stats.FencedConns)
	}
}

func TestAppendTracksSentOffsetAndBuffer(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if off, err := l.Append("s1", []byte("abc")); err != nil || off != 3 {
		t.Fatalf("first append = (%d, %v), want (3, nil)", off, err)
	}
	if off, err := l.Append("s1", []byte("de")); err != nil || off != 5 {
		t.Fatalf("second append = (%d, %v), want (5, nil)", off, err)
	}
	s, ok := l.Session("s1")
	if !ok || s.Sent != 5 || s.Acked != 0 {
		t.Fatalf("session = %+v, want sent 5 and acked 0", s)
	}
	got, err := l.Replay("s1", 0)
	if err != nil || string(got) != "abcde" {
		t.Fatalf("replay from zero = (%q, %v), want abcde", got, err)
	}
	if got := l.Stats().BufferedBytes; got != 5 {
		t.Fatalf("buffered bytes = %d, want 5", got)
	}
}

func TestAppendRejectsEmptyPayload(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", nil); !errors.Is(err, impl.ErrEmptyPayload) {
		t.Fatalf("empty append must report ErrEmptyPayload, got %v", err)
	}
	if s, _ := l.Session("s1"); s.Sent != 0 {
		t.Fatalf("sent offset after a rejected append = %d, want 0", s.Sent)
	}
}

func TestAppendEnforcesReplayCap(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", []byte("abcdef")); err != nil {
		t.Fatalf("append of six bytes must fit the eight byte window, got %v", err)
	}
	if _, err := l.Append("s1", []byte("ghij")); !errors.Is(err, impl.ErrReplayFull) {
		t.Fatalf("append past the replay cap must report ErrReplayFull, got %v", err)
	}
	if s, _ := l.Session("s1"); s.Sent != 6 {
		t.Fatalf("sent offset after an overflow = %d, want 6", s.Sent)
	}
	if err := l.Ack("s1", 6); err != nil {
		t.Fatalf("ack of the whole window must succeed, got %v", err)
	}
	if off, err := l.Append("s1", []byte("ghij")); err != nil || off != 10 {
		t.Fatalf("append after the window cleared = (%d, %v), want (10, nil)", off, err)
	}
}

func TestAckAdvancesAndTrimsReplay(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", []byte("abcdef")); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := l.Ack("s1", 4); err != nil {
		t.Fatalf("ack of four bytes must succeed, got %v", err)
	}
	got, err := l.Replay("s1", 0)
	if err != nil || string(got) != "ef" {
		t.Fatalf("replay after ack = (%q, %v), want ef with the window trimmed", got, err)
	}
	s, _ := l.Session("s1")
	if s.Acked != 4 {
		t.Fatalf("acked offset = %d, want 4", s.Acked)
	}
	if got := l.Stats().BufferedBytes; got != 2 {
		t.Fatalf("buffered bytes after ack = %d, want 2", got)
	}
	if err := l.Ack("s1", 4); err != nil {
		t.Fatalf("repeating the same ack must be idempotent, got %v", err)
	}
	if s, _ := l.Session("s1"); s.Acked != 4 {
		t.Fatalf("acked offset after the idempotent ack = %d, want 4", s.Acked)
	}
}

func TestAckValidatesTargetAndOffset(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", []byte("abcd")); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := l.Ack("nope", 1); !errors.Is(err, impl.ErrSessionNotFound) {
		t.Fatalf("ack for an unknown session must report ErrSessionNotFound, got %v", err)
	}
	if err := l.Ack("s1", 5); !errors.Is(err, impl.ErrAckBeyondSent) {
		t.Fatalf("ack beyond the sent offset must report ErrAckBeyondSent, got %v", err)
	}
	if s, _ := l.Session("s1"); s.Acked != 0 {
		t.Fatalf("acked offset after the rejected ack = %d, want 0", s.Acked)
	}
}

func TestAckRejectsStaleOffset(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", []byte("abcdef")); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := l.Ack("s1", 5); err != nil {
		t.Fatalf("ack of five bytes must succeed, got %v", err)
	}
	if err := l.Ack("s1", 3); !errors.Is(err, impl.ErrStaleAck) {
		t.Fatalf("ack below the acknowledged offset must report ErrStaleAck, got %v", err)
	}
	if s, _ := l.Session("s1"); s.Acked != 5 {
		t.Fatalf("acked offset after the stale ack = %d, want 5", s.Acked)
	}
	if got := l.Stats().StaleAcks; got != 1 {
		t.Fatalf("stale ack counter = %d, want 1", got)
	}
	if got, err := l.Replay("s1", 0); err != nil || string(got) != "f" {
		t.Fatalf("replay after the stale ack = (%q, %v), want f", got, err)
	}
}

func TestReplayRejectsGapBeyondSent(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", []byte("abcd")); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if _, err := l.Replay("s1", 5); !errors.Is(err, impl.ErrReplayGap) {
		t.Fatalf("replay beyond the sent offset must report ErrReplayGap, got %v", err)
	}
	got, err := l.Replay("s1", 4)
	if err != nil || len(got) != 0 {
		t.Fatalf("replay at the sent offset = (%q, %v), want an empty window and no error", got, err)
	}
}

func TestFenceBlocksAppendUntilNextTakeover(t *testing.T) {
	l := impl.NewTakeoverLedger(testPolicy())
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Takeover(resumeToken, testConn{"c1"}); err != nil {
		t.Fatalf("first takeover failed: %v", err)
	}
	l.Fence("s1")
	if _, err := l.Append("s1", []byte("x")); !errors.Is(err, impl.ErrFenced) {
		t.Fatalf("append on a fenced session must report ErrFenced, got %v", err)
	}
	handle, err := l.Takeover(resumeToken, testConn{"c2"})
	if err != nil {
		t.Fatalf("takeover after fence must succeed, got %v", err)
	}
	if handle.Generation != 2 || handle.Conn == nil || handle.Conn.ConnID() != "c2" {
		t.Fatalf("handle after fence = %+v, want generation 2 attached to c2", handle)
	}
	if off, err := l.Append("s1", []byte("x")); err != nil || off != 1 {
		t.Fatalf("append after the new takeover = (%d, %v), want (1, nil)", off, err)
	}
}

func TestSnapshotRestoreKeepsOffsetsAndReplay(t *testing.T) {
	p := testPolicy()
	l := impl.NewTakeoverLedger(p)
	if err := l.Register("s1", resumeToken); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if _, err := l.Append("s1", []byte("abcdef")); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := l.Ack("s1", 2); err != nil {
		t.Fatalf("ack failed: %v", err)
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := impl.NewTakeoverLedger(p)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	ids := restored.Sessions()
	if len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("restored session table = %v, want [s1]", ids)
	}
	s, ok := restored.Session("s1")
	if !ok || s.Sent != 6 || s.Acked != 2 {
		t.Fatalf("restored session = %+v, want sent 6 and acked 2", s)
	}
	if got, err := restored.Replay("s1", 0); err != nil || string(got) != "cdef" {
		t.Fatalf("restored replay = (%q, %v), want cdef", got, err)
	}
	if off, err := restored.Append("s1", []byte("g")); err != nil || off != 7 {
		t.Fatalf("append after restore = (%d, %v), want (7, nil)", off, err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("garbage snapshot must report ErrBadSnapshot, got %v", err)
	}
	if s, _ := restored.Session("s1"); s.Sent != 7 {
		t.Fatalf("sent offset after a rejected restore = %d, want 7", s.Sent)
	}
}
