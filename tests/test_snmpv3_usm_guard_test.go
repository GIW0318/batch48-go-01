package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-09"
)

const guardEngineID = "80:00:1f:88:80:4e:54"

func newGuard() *impl.UsmGuard {
	return impl.NewUsmGuard(impl.UsmOptions{
		EngineID:   guardEngineID,
		TimeWindow: 150,
		MaxBoots:   1000,
		MaxUsers:   8,
		MaxTargets: 8,
	})
}

func aliceUser() impl.UsmUser {
	return impl.UsmUser{
		Name:      "alice",
		AuthProto: "sha256",
		PrivProto: "aes128",
		Level:     "authPriv",
		AuthPass:  "auth-pass-alice",
		PrivPass:  "priv-pass-alice",
	}
}

func addUser(t *testing.T, g *impl.UsmGuard, u impl.UsmUser) {
	t.Helper()
	if err := g.AddUser(u); err != nil {
		t.Fatalf("add user %s: %v", u.Name, err)
	}
}

func validMessage() impl.UsmMessage {
	return impl.UsmMessage{
		User:       "alice",
		EngineID:   guardEngineID,
		Boots:      1,
		EngineTime: 0,
		AuthOK:     true,
		PrivOK:     true,
		Generation: 1,
		RequestID:  7,
	}
}

func TestAddUserRejectsInvalidConfiguration(t *testing.T) {
	g := newGuard()
	if err := g.AddUser(impl.UsmUser{Name: "", AuthProto: "sha256", Level: "authNoPriv", AuthPass: "p"}); !errors.Is(err, impl.ErrUserConfig) {
		t.Fatalf("empty user name: want ErrUserConfig, got %v", err)
	}
	if err := g.AddUser(impl.UsmUser{Name: "u2", AuthProto: "md5", Level: "authNoPriv", AuthPass: "p"}); !errors.Is(err, impl.ErrUserConfig) {
		t.Fatalf("md5 auth proto: want ErrUserConfig, got %v", err)
	}
	noPriv := aliceUser()
	noPriv.PrivPass = ""
	if err := g.AddUser(noPriv); !errors.Is(err, impl.ErrUserConfig) {
		t.Fatalf("authPriv without priv pass: want ErrUserConfig, got %v", err)
	}
	badLevel := aliceUser()
	badLevel.Level = "authOnly"
	if err := g.AddUser(badLevel); !errors.Is(err, impl.ErrUserConfig) {
		t.Fatalf("unknown level: want ErrUserConfig, got %v", err)
	}
}

func TestAddUserRejectsDuplicateName(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	if err := g.AddUser(aliceUser()); !errors.Is(err, impl.ErrUserExists) {
		t.Fatalf("duplicate user: want ErrUserExists, got %v", err)
	}
}

func TestAddUserEnforcesUserLimit(t *testing.T) {
	g := impl.NewUsmGuard(impl.UsmOptions{EngineID: guardEngineID, TimeWindow: 150, MaxBoots: 1000, MaxUsers: 1, MaxTargets: 8})
	addUser(t, g, aliceUser())
	bob := aliceUser()
	bob.Name = "bob"
	if err := g.AddUser(bob); !errors.Is(err, impl.ErrUserLimit) {
		t.Fatalf("second user with MaxUsers 1: want ErrUserLimit, got %v", err)
	}
}

func TestLocalizeRejectsUnknownUser(t *testing.T) {
	g := newGuard()
	if _, err := g.Localize("ghost"); !errors.Is(err, impl.ErrUserUnknown) {
		t.Fatalf("localize unknown user: want ErrUserUnknown, got %v", err)
	}
}

func TestLocalizeReturnsStableEngineScopedKeys(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	bob := aliceUser()
	bob.Name = "bob"
	bob.AuthPass = "auth-pass-bob"
	bob.PrivPass = "priv-pass-bob"
	addUser(t, g, bob)
	first, err := g.Localize("alice")
	if err != nil {
		t.Fatalf("localize alice: %v", err)
	}
	second, err := g.Localize("alice")
	if err != nil {
		t.Fatalf("localize alice again: %v", err)
	}
	if first.EngineID != guardEngineID || first.Generation != 1 {
		t.Fatalf("localized keys = %+v, want engine %s generation 1", first, guardEngineID)
	}
	if first.AuthKey == "" || first.PrivKey == "" {
		t.Fatalf("authPriv user must get both keys: %+v", first)
	}
	if first.AuthKey != second.AuthKey || first.PrivKey != second.PrivKey {
		t.Fatalf("localization must be deterministic")
	}
	other, err := g.Localize("bob")
	if err != nil {
		t.Fatalf("localize bob: %v", err)
	}
	if other.AuthKey == first.AuthKey {
		t.Fatalf("different users must not share localized keys")
	}
}

func TestVerifyRejectsEngineIDMismatch(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	msg := validMessage()
	msg.EngineID = "80:00:1f:88:80:99:99"
	if err := g.VerifyMessage(msg); !errors.Is(err, impl.ErrEngineMismatch) {
		t.Fatalf("engine mismatch: want ErrEngineMismatch, got %v", err)
	}
}

func TestVerifyRejectsReplayedBoots(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	msg := validMessage()
	msg.Boots = 0
	if err := g.VerifyMessage(msg); !errors.Is(err, impl.ErrTimeWindow) {
		t.Fatalf("boots below current: want ErrTimeWindow, got %v", err)
	}
}

func TestVerifyRejectsOutsideTimeWindow(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	msg := validMessage()
	msg.EngineTime = 1000
	if err := g.VerifyMessage(msg); !errors.Is(err, impl.ErrTimeWindow) {
		t.Fatalf("engine time far ahead: want ErrTimeWindow, got %v", err)
	}
}

func TestVerifyRejectsMissingAuthProof(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	msg := validMessage()
	msg.AuthOK = false
	if err := g.VerifyMessage(msg); !errors.Is(err, impl.ErrAuthProof) {
		t.Fatalf("missing auth proof: want ErrAuthProof, got %v", err)
	}
}

func TestVerifyRejectsMissingPrivProof(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	msg := validMessage()
	msg.PrivOK = false
	if err := g.VerifyMessage(msg); !errors.Is(err, impl.ErrPrivProof) {
		t.Fatalf("missing priv proof: want ErrPrivProof, got %v", err)
	}
}

func TestVerifyRejectsStaleKeyGeneration(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	if _, err := g.RotateUserKeys("ghost"); !errors.Is(err, impl.ErrUserUnknown) {
		t.Fatalf("rotate unknown user: want ErrUserUnknown, got %v", err)
	}
	gen, err := g.RotateUserKeys("alice")
	if err != nil {
		t.Fatalf("rotate alice: %v", err)
	}
	if gen != 2 {
		t.Fatalf("rotated generation = %d, want 2", gen)
	}
	stale := validMessage()
	if err := g.VerifyMessage(stale); !errors.Is(err, impl.ErrKeyGeneration) {
		t.Fatalf("stale generation: want ErrKeyGeneration, got %v", err)
	}
	fresh := validMessage()
	fresh.Generation = 2
	if err := g.VerifyMessage(fresh); err != nil {
		t.Fatalf("current generation must verify: %v", err)
	}
}

func TestRemoveUserBlocksVerification(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	if err := g.RemoveUser("alice"); err != nil {
		t.Fatalf("remove user: %v", err)
	}
	if err := g.VerifyMessage(validMessage()); !errors.Is(err, impl.ErrUserUnknown) {
		t.Fatalf("verify after removal: want ErrUserUnknown, got %v", err)
	}
	if _, err := g.Localize("alice"); !errors.Is(err, impl.ErrUserUnknown) {
		t.Fatalf("localize after removal: want ErrUserUnknown, got %v", err)
	}
	if err := g.RemoveUser("alice"); !errors.Is(err, impl.ErrUserUnknown) {
		t.Fatalf("remove unknown user: want ErrUserUnknown, got %v", err)
	}
}

func TestAddTrapTargetValidatesViewAndUser(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	if err := g.SetView("full", []impl.ViewEntry{{OID: "1.3.6.1", Included: true}}); err != nil {
		t.Fatalf("set view: %v", err)
	}
	if err := g.AddTrapTarget(impl.TrapTarget{Name: "t1", Address: "10.0.0.9", View: "missing", User: "alice"}); !errors.Is(err, impl.ErrViewUnknown) {
		t.Fatalf("unknown view: want ErrViewUnknown, got %v", err)
	}
	if err := g.AddTrapTarget(impl.TrapTarget{Name: "t1", Address: "10.0.0.9", View: "full", User: "ghost"}); !errors.Is(err, impl.ErrUserUnknown) {
		t.Fatalf("unknown user: want ErrUserUnknown, got %v", err)
	}
	if err := g.AddTrapTarget(impl.TrapTarget{Name: "t1", Address: "10.0.0.9", View: "full", User: "alice"}); err != nil {
		t.Fatalf("valid target: %v", err)
	}
}

func TestProjectTrapFiltersVarbindsAndCountsDropped(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	entries := []impl.ViewEntry{
		{OID: "1.3.6.1", Included: true},
		{OID: "1.3.6.1.4", Included: false},
	}
	if err := g.SetView("audit", entries); err != nil {
		t.Fatalf("set view: %v", err)
	}
	if err := g.AddTrapTarget(impl.TrapTarget{Name: "t1", Address: "10.0.0.9", View: "audit", User: "alice"}); err != nil {
		t.Fatalf("add target: %v", err)
	}
	note, err := g.ProjectTrap("t1", []string{"1.3.6.1.2.1", "1.3.6.1.4.1", "1.3.9"})
	if err != nil {
		t.Fatalf("project trap: %v", err)
	}
	if len(note.Varbinds) != 1 || note.Varbinds[0] != "1.3.6.1.2.1" {
		t.Fatalf("projected varbinds = %v, want [1.3.6.1.2.1]", note.Varbinds)
	}
	if note.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", note.Dropped)
	}
	if !note.Sent {
		t.Fatalf("notification must be marked sent")
	}
	stats := g.Stats()
	if stats.Traps != 1 || stats.Filtered != 2 {
		t.Fatalf("traps/filtered = %d/%d, want 1/2", stats.Traps, stats.Filtered)
	}
	if _, err := g.ProjectTrap("missing", []string{"1.3.6.1.2.1"}); !errors.Is(err, impl.ErrTargetUnknown) {
		t.Fatalf("unknown target: want ErrTargetUnknown, got %v", err)
	}
}

func TestSnapshotRestorePreservesEngineState(t *testing.T) {
	g := newGuard()
	addUser(t, g, aliceUser())
	if got := g.AdvanceEngineTime(30); got != 30 {
		t.Fatalf("engine time = %d, want 30", got)
	}
	blob, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := newGuard()
	if err := restored.Restore(blob); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.EngineBoots(); got != 1 {
		t.Fatalf("restored boots = %d, want 1", got)
	}
	if _, err := restored.Localize("alice"); err != nil {
		t.Fatalf("restored user must localize: %v", err)
	}
	if got := restored.AdvanceEngineTime(5); got != 35 {
		t.Fatalf("restored engine time = %d, want 35", got)
	}
	if err := restored.Restore([]byte("garbage")); !errors.Is(err, impl.ErrUsmSnapshot) {
		t.Fatalf("garbage snapshot: want ErrUsmSnapshot, got %v", err)
	}
	if got := restored.EngineBoots(); got != 1 {
		t.Fatalf("bad snapshot disturbed boots, got %d", got)
	}
}
