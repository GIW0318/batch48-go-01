package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-d-14"
)

func TestAddSlotNumbersIncrease(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	first, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding the first slot failed: %v", err)
	}
	second, err := g.AddSlot(impl.KindKeyFile, "backup", "keyfile-bytes")
	if err != nil {
		t.Fatalf("adding the second slot failed: %v", err)
	}
	third, err := g.AddSlot(impl.KindRecovery, "recovery", "recovery-bytes")
	if err != nil {
		t.Fatalf("adding the third slot failed: %v", err)
	}
	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("slot numbers are %d,%d,%d, want 1,2,3", first, second, third)
	}
	slots := g.Slots()
	if len(slots) != 3 {
		t.Fatalf("guard reports %d slots, want 3", len(slots))
	}
	for i, slot := range slots {
		if slot.Slot != i+1 {
			t.Fatalf("slot at index %d has number %d, want %d", i, slot.Slot, i+1)
		}
		if slot.Fails != 0 || slot.Locked {
			t.Fatalf("fresh slot %d reports fails=%d locked=%v", slot.Slot, slot.Fails, slot.Locked)
		}
	}
}

func TestAddSlotRejectsQuota(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 2, LockAfter: 3})
	if _, err := g.AddSlot(impl.KindPassphrase, "a", "one"); err != nil {
		t.Fatalf("first slot failed: %v", err)
	}
	if _, err := g.AddSlot(impl.KindPassphrase, "b", "two"); err != nil {
		t.Fatalf("second slot failed: %v", err)
	}
	if _, err := g.AddSlot(impl.KindPassphrase, "c", "three"); !errors.Is(err, impl.ErrSlotQuota) {
		t.Fatalf("expected ErrSlotQuota on the third slot, got %v", err)
	}
	if got := len(g.Slots()); got != 2 {
		t.Fatalf("quota rejection left %d slots, want 2", got)
	}
}

func TestAddSlotRejectsBadSecretAndKind(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	if _, err := g.AddSlot(impl.KindPassphrase, "empty", ""); !errors.Is(err, impl.ErrBadSecret) {
		t.Fatalf("expected ErrBadSecret for an empty secret, got %v", err)
	}
	if _, err := g.AddSlot(impl.Kind("usb-token"), "usb", "material"); !errors.Is(err, impl.ErrBadKind) {
		t.Fatalf("expected ErrBadKind for an unknown kind, got %v", err)
	}
	if got := len(g.Slots()); got != 0 {
		t.Fatalf("rejected additions left %d slots, want 0", got)
	}
}

func TestUnlockSucceedsWithMatchingCredential(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	if err := g.Unlock(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: "s3cret"}); err != nil {
		t.Fatalf("unlock with the matching credential failed: %v", err)
	}
	if err := g.Unlock(99, impl.Credential{Kind: impl.KindPassphrase, Secret: "s3cret"}); !errors.Is(err, impl.ErrNoSuchSlot) {
		t.Fatalf("expected ErrNoSuchSlot for slot 99, got %v", err)
	}
}

func TestUnlockWrongSecretIncrementsFailures(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	wrong := impl.Credential{Kind: impl.KindPassphrase, Secret: "nope"}
	if err := g.Unlock(slot, wrong); !errors.Is(err, impl.ErrBadSecret) {
		t.Fatalf("expected ErrBadSecret on the first wrong attempt, got %v", err)
	}
	if err := g.Unlock(slot, wrong); !errors.Is(err, impl.ErrBadSecret) {
		t.Fatalf("expected ErrBadSecret on the second wrong attempt, got %v", err)
	}
	slots := g.Slots()
	if len(slots) != 1 {
		t.Fatalf("guard reports %d slots, want 1", len(slots))
	}
	if slots[0].Fails != 2 {
		t.Fatalf("slot failure counter is %d, want 2", slots[0].Fails)
	}
	if slots[0].Locked || g.Locked(slot) {
		t.Fatalf("slot must not be locked below the threshold of 3")
	}
}

func TestUnlockLocksAtThreshold(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	wrong := impl.Credential{Kind: impl.KindPassphrase, Secret: "nope"}
	for i := 0; i < 3; i++ {
		g.Unlock(slot, wrong)
	}
	if !g.Locked(slot) {
		t.Fatalf("slot must be locked after 3 failures")
	}
	if err := g.Unlock(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: "s3cret"}); !errors.Is(err, impl.ErrSlotLocked) {
		t.Fatalf("expected ErrSlotLocked for the correct secret on a locked slot, got %v", err)
	}
}

func TestUnlockRejectsKindMismatch(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	if err := g.Unlock(slot, impl.Credential{Kind: impl.KindKeyFile, Secret: "s3cret"}); !errors.Is(err, impl.ErrKindMismatch) {
		t.Fatalf("expected ErrKindMismatch for a keyfile credential, got %v", err)
	}
}

func TestRekeySwitchesActiveSlot(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "old-secret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	res, err := g.Rekey(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: "new-secret"})
	if err != nil {
		t.Fatalf("rekey failed: %v", err)
	}
	if res.OldSlot != 1 || res.NewSlot != 2 || res.Active != 2 {
		t.Fatalf("rekey result is %+v, want old=1 new=2 active=2", res)
	}
	if got := g.Active(); got != 2 {
		t.Fatalf("active slot is %d, want 2", got)
	}
	slots := g.Slots()
	if len(slots) != 1 || slots[0].Slot != 2 || !slots[0].Active {
		t.Fatalf("slots after rekey are %+v, want only slot 2 active", slots)
	}
}

func TestRekeyFailureKeepsOldActive(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "old-secret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	if _, err := g.Rekey(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: ""}); !errors.Is(err, impl.ErrBadSecret) {
		t.Fatalf("expected ErrBadSecret for an empty new credential, got %v", err)
	}
	if got := g.Active(); got != 1 {
		t.Fatalf("failed rekey moved the active slot to %d, want 1", got)
	}
	slots := g.Slots()
	if len(slots) != 1 || slots[0].Slot != 1 {
		t.Fatalf("failed rekey changed the slot set to %+v", slots)
	}
}

func TestRemoveSlotRejectsActiveSlot(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	if err := g.RemoveSlot(slot); !errors.Is(err, impl.ErrActiveSlot) {
		t.Fatalf("expected ErrActiveSlot, got %v", err)
	}
	if got := len(g.Slots()); got != 1 {
		t.Fatalf("rejected removal left %d slots, want 1", got)
	}
}

func TestRemovedSlotNumberIsNotReused(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	if _, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret"); err != nil {
		t.Fatalf("first slot failed: %v", err)
	}
	second, err := g.AddSlot(impl.KindKeyFile, "backup", "keyfile")
	if err != nil {
		t.Fatalf("second slot failed: %v", err)
	}
	if err := g.RemoveSlot(second); err != nil {
		t.Fatalf("removing an inactive slot failed: %v", err)
	}
	next, err := g.AddSlot(impl.KindPassphrase, "third", "third-secret")
	if err != nil {
		t.Fatalf("adding after removal failed: %v", err)
	}
	if next != second+1 {
		t.Fatalf("new slot number is %d, want %d (numbers are never reused)", next, second+1)
	}
}

func TestCredentialPathsAreSeparated(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	pass, err := g.CredentialPath(impl.KindPassphrase)
	if err != nil {
		t.Fatalf("passphrase path failed: %v", err)
	}
	file, err := g.CredentialPath(impl.KindKeyFile)
	if err != nil {
		t.Fatalf("keyfile path failed: %v", err)
	}
	recovery, err := g.CredentialPath(impl.KindRecovery)
	if err != nil {
		t.Fatalf("recovery path failed: %v", err)
	}
	if pass == "" || file == "" || recovery == "" {
		t.Fatalf("credential paths must not be empty: %q %q %q", pass, file, recovery)
	}
	if pass == file || pass == recovery || file == recovery {
		t.Fatalf("credential paths overlap: %q %q %q", pass, file, recovery)
	}
	if _, err := g.CredentialPath(impl.Kind("token")); !errors.Is(err, impl.ErrBadKind) {
		t.Fatalf("expected ErrBadKind for an unknown kind, got %v", err)
	}
}

func TestSnapshotExcludesSecretMaterial(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 3})
	if _, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret"); err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot is empty")
	}
	if strings.Contains(string(snap), "s3cret") {
		t.Fatalf("snapshot leaks credential material")
	}
}

func TestRestoreKeepsFailCountsAndActiveSlot(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 5})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	wrong := impl.Credential{Kind: impl.KindPassphrase, Secret: "nope"}
	g.Unlock(slot, wrong)
	g.Unlock(slot, wrong)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	other := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 5})
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := other.Active(); got != slot {
		t.Fatalf("restored active slot is %d, want %d", got, slot)
	}
	slots := other.Slots()
	if len(slots) != 1 {
		t.Fatalf("restored %d slots, want 1", len(slots))
	}
	if slots[0].Fails != 2 {
		t.Fatalf("restored failure counter is %d, want 2", slots[0].Fails)
	}
	if slots[0].Kind != impl.KindPassphrase || slots[0].Label != "main" {
		t.Fatalf("restored slot metadata is %+v", slots[0])
	}
}

func TestStatsCountsUnlockOutcomes(t *testing.T) {
	g := impl.NewKeyslotGuard(impl.Limits{MaxSlots: 8, LockAfter: 5})
	slot, err := g.AddSlot(impl.KindPassphrase, "main", "s3cret")
	if err != nil {
		t.Fatalf("adding a slot failed: %v", err)
	}
	if err := g.Unlock(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: "s3cret"}); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}
	if err := g.Unlock(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: "nope"}); !errors.Is(err, impl.ErrBadSecret) {
		t.Fatalf("expected ErrBadSecret, got %v", err)
	}
	if _, err := g.Rekey(slot, impl.Credential{Kind: impl.KindPassphrase, Secret: "rotated"}); err != nil {
		t.Fatalf("rekey failed: %v", err)
	}
	stats := g.Stats()
	if stats.Added != 1 || stats.Unlocked != 1 || stats.Failed != 1 || stats.Rekeys != 1 {
		t.Fatalf("stats are %+v, want added=1 unlocked=1 failed=1 rekeys=1", stats)
	}
}
