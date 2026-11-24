package task

import (
	"errors"
	"testing"
	"time"

	impl "gsb/batch48-go-game-a-13"
)

const testGrace = 30 * time.Second

func newLedger() *impl.ReconnectLedger {
	return impl.NewReconnectLedger(testGrace, 5)
}

func mustJoin(t *testing.T, l *impl.ReconnectLedger, playerID, team string) {
	t.Helper()
	if err := l.Join(playerID, team, impl.PhasePlaying); err != nil {
		t.Fatalf("join %s: %v", playerID, err)
	}
}

func mustDetach(t *testing.T, l *impl.ReconnectLedger, playerID string) impl.ResumeToken {
	t.Helper()
	token, err := l.Detach(playerID)
	if err != nil {
		t.Fatalf("detach %s: %v", playerID, err)
	}
	if token == "" {
		t.Fatalf("detach %s returned an empty resume token", playerID)
	}
	return token
}

func TestJoinRejectsEmptyPlayerID(t *testing.T) {
	l := newLedger()
	err := l.Join("", "red", impl.PhasePlaying)
	if !errors.Is(err, impl.ErrInvalidPlayerID) {
		t.Fatalf("join with empty id: got %v, want ErrInvalidPlayerID", err)
	}
	if l.Pending() != 0 {
		t.Fatalf("pending after rejected join = %d, want 0", l.Pending())
	}
}

func TestJoinRejectsDuplicatePlayer(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	err := l.Join("p1", "blue", impl.PhasePlaying)
	if !errors.Is(err, impl.ErrAlreadyJoined) {
		t.Fatalf("second join of p1: got %v, want ErrAlreadyJoined", err)
	}
	s, ok := l.Lookup("p1")
	if !ok {
		t.Fatalf("p1 must still be registered after the rejected join")
	}
	if s.Team != "red" {
		t.Fatalf("team = %q after the rejected join, want the original red", s.Team)
	}
}

func TestDetachIssuesTokenAndEntersGrace(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	if err := l.SetCamera("p1", impl.Camera{X: 4, Y: 5, Z: 6}); err != nil {
		t.Fatalf("set camera: %v", err)
	}
	if err := l.Confirm("p1", 40); err != nil {
		t.Fatalf("confirm frame 40: %v", err)
	}
	mustDetach(t, l, "p1")
	if got := l.Pending(); got != 1 {
		t.Fatalf("pending = %d after detach, want 1", got)
	}
	s, ok := l.Lookup("p1")
	if !ok {
		t.Fatalf("p1 must stay reserved during the grace period")
	}
	if !s.InGrace {
		t.Fatalf("session must be marked in grace after detach")
	}
	if s.LastFrame != 40 {
		t.Fatalf("last frame = %d in grace, want 40 kept for the resume", s.LastFrame)
	}
}

func TestDetachRejectsUnknownPlayer(t *testing.T) {
	l := newLedger()
	_, err := l.Detach("ghost")
	if !errors.Is(err, impl.ErrPlayerNotFound) {
		t.Fatalf("detach of unknown player: got %v, want ErrPlayerNotFound", err)
	}
}

func TestDetachTwiceRejected(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	mustDetach(t, l, "p1")
	_, err := l.Detach("p1")
	if !errors.Is(err, impl.ErrPlayerInGrace) {
		t.Fatalf("second detach: got %v, want ErrPlayerInGrace", err)
	}
	if got := l.Pending(); got != 1 {
		t.Fatalf("pending = %d after the rejected second detach, want 1", got)
	}
}

func TestResumeRestoresSessionState(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	if err := l.SetCamera("p1", impl.Camera{X: 1, Y: 2, Z: 3}); err != nil {
		t.Fatalf("set camera: %v", err)
	}
	if err := l.Confirm("p1", 40); err != nil {
		t.Fatalf("confirm frame 40: %v", err)
	}
	token := mustDetach(t, l, "p1")
	s, err := l.Resume(token, impl.Conn{ID: "conn-7", Remote: "gw-b"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if s == nil {
		t.Fatalf("resume returned a nil session")
	}
	if s.PlayerID != "p1" || s.ConnID != "conn-7" {
		t.Fatalf("resumed session = player %q conn %q, want p1 and conn-7", s.PlayerID, s.ConnID)
	}
	if s.Team != "red" || s.Phase != impl.PhasePlaying {
		t.Fatalf("resumed team/phase = %q/%v, want red/playing", s.Team, s.Phase)
	}
	if s.Camera != (impl.Camera{X: 1, Y: 2, Z: 3}) {
		t.Fatalf("resumed camera = %+v, want the stored {1 2 3}", s.Camera)
	}
	if s.LastFrame != 40 {
		t.Fatalf("resumed last frame = %d, want 40", s.LastFrame)
	}
	if s.ResendFrom != 41 {
		t.Fatalf("resume must ask for input from frame 41, got %d", s.ResendFrom)
	}
	if s.InGrace {
		t.Fatalf("resumed session must not be marked in grace")
	}
	if got := l.Pending(); got != 0 {
		t.Fatalf("pending after resume = %d, want 0", got)
	}
}

func TestResumeRejectsUnknownToken(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	_, err := l.Resume(impl.ResumeToken("rt-unknown"), impl.Conn{ID: "conn-1"})
	if !errors.Is(err, impl.ErrTokenInvalid) {
		t.Fatalf("resume with unknown token: got %v, want ErrTokenInvalid", err)
	}
}

func TestResumeTokenIsSingleUse(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	token := mustDetach(t, l, "p1")
	if _, err := l.Resume(token, impl.Conn{ID: "conn-1"}); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	_, err := l.Resume(token, impl.Conn{ID: "conn-2"})
	if !errors.Is(err, impl.ErrTokenUsed) {
		t.Fatalf("second resume with the same token: got %v, want ErrTokenUsed", err)
	}
	s, ok := l.Lookup("p1")
	if !ok || s.ConnID != "conn-1" {
		t.Fatalf("session after the rejected second resume = %+v ok=%v, want conn-1 still bound", s, ok)
	}
}

func TestResumeRejectsExpiredToken(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	token := mustDetach(t, l, "p1")
	released := l.Expire(time.Now().Add(2 * testGrace))
	if len(released) != 1 || released[0] != "p1" {
		t.Fatalf("expire released %v, want [p1]", released)
	}
	_, err := l.Resume(token, impl.Conn{ID: "conn-1"})
	if !errors.Is(err, impl.ErrTokenExpired) {
		t.Fatalf("resume after expiry: got %v, want ErrTokenExpired", err)
	}
}

func TestExpireReturnsReleasedPlayersSortedByID(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p2", "blue")
	mustJoin(t, l, "p1", "red")
	mustDetach(t, l, "p2")
	mustDetach(t, l, "p1")
	released := l.Expire(time.Now().Add(2 * testGrace))
	if len(released) != 2 || released[0] != "p1" || released[1] != "p2" {
		t.Fatalf("expire released %v, want [p1 p2]", released)
	}
	if got := l.Pending(); got != 0 {
		t.Fatalf("pending after expire = %d, want 0", got)
	}
	if _, ok := l.Lookup("p1"); ok {
		t.Fatalf("p1 must be released after the grace period")
	}
}

func TestAbandonReleasesPlayerImmediately(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	mustDetach(t, l, "p1")
	l.Abandon("p1")
	if got := l.Pending(); got != 0 {
		t.Fatalf("pending after abandon = %d, want 0", got)
	}
	if _, ok := l.Lookup("p1"); ok {
		t.Fatalf("p1 must be gone after abandon")
	}
}

func TestAcceptResendWithinWindow(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	if err := l.Confirm("p1", 10); err != nil {
		t.Fatalf("confirm frame 10: %v", err)
	}
	if got := l.ResendFrom("p1"); got != 11 {
		t.Fatalf("resend from = %d, want 11", got)
	}
	for _, frame := range []uint64{11, 12} {
		if err := l.AcceptResend("p1", frame, impl.Input{Action: "move"}); err != nil {
			t.Fatalf("accept resend of frame %d: %v", frame, err)
		}
	}
	s, ok := l.Lookup("p1")
	if !ok {
		t.Fatalf("p1 missing after accepting resends")
	}
	if s.Resent != 2 {
		t.Fatalf("resent = %d, want 2 accepted frames", s.Resent)
	}
}

func TestAcceptResendOutsideWindowRejected(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	if err := l.Confirm("p1", 10); err != nil {
		t.Fatalf("confirm frame 10: %v", err)
	}
	err := l.AcceptResend("p1", 10, impl.Input{})
	if !errors.Is(err, impl.ErrResendOutOfWindow) {
		t.Fatalf("resend of the confirmed frame 10: got %v, want ErrResendOutOfWindow", err)
	}
	err = l.AcceptResend("p1", 16, impl.Input{})
	if !errors.Is(err, impl.ErrResendOutOfWindow) {
		t.Fatalf("resend beyond the 5 frame window: got %v, want ErrResendOutOfWindow", err)
	}
	if err := l.AcceptResend("p1", 15, impl.Input{}); err != nil {
		t.Fatalf("resend at the window edge: %v", err)
	}
}

func TestConfirmRejectsRegression(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	if err := l.Confirm("p1", 10); err != nil {
		t.Fatalf("confirm frame 10: %v", err)
	}
	err := l.Confirm("p1", 4)
	if !errors.Is(err, impl.ErrConfirmRegressed) {
		t.Fatalf("confirm of an older frame: got %v, want ErrConfirmRegressed", err)
	}
	s, _ := l.Lookup("p1")
	if s.LastFrame != 10 {
		t.Fatalf("last frame = %d after the rejected confirm, want 10", s.LastFrame)
	}
	if err := l.Confirm("p1", 12); err != nil {
		t.Fatalf("confirm frame 12: %v", err)
	}
}

func TestSnapshotRestoreKeepsGraceAndUsedToken(t *testing.T) {
	l := newLedger()
	mustJoin(t, l, "p1", "red")
	if err := l.Confirm("p1", 30); err != nil {
		t.Fatalf("confirm frame 30: %v", err)
	}
	used := mustDetach(t, l, "p1")
	if _, err := l.Resume(used, impl.Conn{ID: "conn-1"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	mustJoin(t, l, "p2", "blue")
	parked := mustDetach(t, l, "p2")
	data, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	mustJoin(t, l, "p3", "red")
	if err := l.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, ok := l.Lookup("p3"); ok {
		t.Fatalf("p3 joined after the snapshot must be gone after restore")
	}
	if got := l.Pending(); got != 1 {
		t.Fatalf("pending after restore = %d, want 1 parked player", got)
	}
	if _, err := l.Resume(used, impl.Conn{ID: "conn-2"}); !errors.Is(err, impl.ErrTokenUsed) {
		t.Fatalf("resume with a token used before the snapshot: got %v, want ErrTokenUsed", err)
	}
	released := l.Expire(time.Now().Add(2 * testGrace))
	if len(released) != 1 || released[0] != "p2" {
		t.Fatalf("expire after restore released %v, want [p2]", released)
	}
	if _, err := l.Resume(parked, impl.Conn{ID: "conn-3"}); !errors.Is(err, impl.ErrTokenExpired) {
		t.Fatalf("resume of the parked token after restore and expiry: got %v, want ErrTokenExpired", err)
	}
	if err := l.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restore of garbage: got %v, want ErrBadSnapshot", err)
	}
}
