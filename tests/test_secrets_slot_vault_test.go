package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-d-01"
)

func mustPutSlot(t *testing.T, v *impl.SecretSlotVault, spec impl.SlotSpec) {
	t.Helper()
	if err := v.PutSlot(spec); err != nil {
		t.Fatalf("put slot %s/%s/%s: %v", spec.Tenant, spec.User, spec.Name, err)
	}
}

func mustSealSlot(t *testing.T, v *impl.SecretSlotVault, tenant, user, name string) impl.SealedRecord {
	t.Helper()
	rec, err := v.Seal(tenant, user, name)
	if err != nil {
		t.Fatalf("seal slot %s/%s/%s: %v", tenant, user, name, err)
	}
	return rec
}

func TestPutSlotRejectsDuplicateSlot(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	spec := impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")}
	mustPutSlot(t, v, spec)
	if err := v.PutSlot(spec); !errors.Is(err, impl.ErrSlotExists) {
		t.Fatalf("duplicate slot error = %v, want %v", err, impl.ErrSlotExists)
	}
}

func TestPutSlotRejectsUnknownKind(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	spec := impl.SlotSpec{Tenant: "acme", User: "u1", Name: "pin", Kind: impl.SlotKind("pin"), Secret: []byte("1234")}
	if err := v.PutSlot(spec); !errors.Is(err, impl.ErrInvalidSlot) {
		t.Fatalf("unknown slot kind error = %v, want %v", err, impl.ErrInvalidSlot)
	}
}

func TestPutSlotEnforcesSlotLimit(t *testing.T) {
	v := impl.NewSecretSlotVault(2, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "totp", Kind: impl.SlotKindTOTP, Secret: []byte("seed")})
	err := v.PutSlot(impl.SlotSpec{Tenant: "acme", User: "u1", Name: "recovery", Kind: impl.SlotKindRecovery, Codes: []string{"c1"}})
	if !errors.Is(err, impl.ErrSlotLimit) {
		t.Fatalf("slot over limit error = %v, want %v", err, impl.ErrSlotLimit)
	}
}

func TestSealBindsGenerationAndKEK(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	rec := mustSealSlot(t, v, "acme", "u1", "password")
	if rec.Tenant != "acme" || rec.User != "u1" || rec.Name != "password" {
		t.Fatalf("sealed record not bound to its slot: %+v", rec)
	}
	if rec.Generation != 1 {
		t.Fatalf("sealed generation = %d, want 1", rec.Generation)
	}
	if rec.KEKID != "kek-1" {
		t.Fatalf("sealed kek id = %q, want %q", rec.KEKID, "kek-1")
	}
	if rec.Algorithm != "aes-256-gcm" {
		t.Fatalf("sealed algorithm = %q, want %q", rec.Algorithm, "aes-256-gcm")
	}
	if rec.Tag == 0 {
		t.Fatalf("sealed tag must not be zero")
	}
	again := mustSealSlot(t, v, "acme", "u1", "password")
	if again.Tag != rec.Tag {
		t.Fatalf("re-sealing unchanged material changed tag from %d to %d", rec.Tag, again.Tag)
	}
}

func TestOpenWrongSecretCountsFailure(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	rec := mustSealSlot(t, v, "acme", "u1", "password")
	if err := v.Open(rec, []byte("wrong")); !errors.Is(err, impl.ErrSecretMismatch) {
		t.Fatalf("open with wrong secret error = %v, want %v", err, impl.ErrSecretMismatch)
	}
	st, err := v.SlotStatus("acme", "u1", "password")
	if err != nil {
		t.Fatalf("slot status: %v", err)
	}
	if st.Failures != 1 {
		t.Fatalf("failure counter = %d, want 1", st.Failures)
	}
	if st.State != impl.SlotActive {
		t.Fatalf("state = %q, want %q", st.State, impl.SlotActive)
	}
}

func TestOpenLocksSlotAtFailureLimit(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	rec := mustSealSlot(t, v, "acme", "u1", "password")
	for i := 0; i < 2; i++ {
		if err := v.Open(rec, []byte("wrong")); !errors.Is(err, impl.ErrSecretMismatch) {
			t.Fatalf("attempt %d error = %v, want %v", i+1, err, impl.ErrSecretMismatch)
		}
	}
	if err := v.Open(rec, []byte("wrong")); !errors.Is(err, impl.ErrSlotLocked) {
		t.Fatalf("attempt at failure limit error = %v, want %v", err, impl.ErrSlotLocked)
	}
	st, err := v.SlotStatus("acme", "u1", "password")
	if err != nil {
		t.Fatalf("slot status: %v", err)
	}
	if st.State != impl.SlotLocked {
		t.Fatalf("state = %q, want %q", st.State, impl.SlotLocked)
	}
	if st.Failures != 3 {
		t.Fatalf("failure counter = %d, want 3", st.Failures)
	}
	if err := v.Open(rec, []byte("pw-1")); !errors.Is(err, impl.ErrSlotLocked) {
		t.Fatalf("open on locked slot with correct secret error = %v, want %v", err, impl.ErrSlotLocked)
	}
}

func TestOpenCorrectSecretClearsFailures(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	rec := mustSealSlot(t, v, "acme", "u1", "password")
	if err := v.Open(rec, []byte("wrong")); !errors.Is(err, impl.ErrSecretMismatch) {
		t.Fatalf("wrong secret error = %v, want %v", err, impl.ErrSecretMismatch)
	}
	if err := v.Open(rec, []byte("pw-1")); err != nil {
		t.Fatalf("open with correct secret: %v", err)
	}
	st, err := v.SlotStatus("acme", "u1", "password")
	if err != nil {
		t.Fatalf("slot status: %v", err)
	}
	if st.Failures != 0 {
		t.Fatalf("failure counter after success = %d, want 0", st.Failures)
	}
}

func TestConsumeRecoveryIsOneTime(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "codes", Kind: impl.SlotKindRecovery, Codes: []string{"c1", "c2"}})
	ok, err := v.ConsumeRecovery("acme", "u1", "c1")
	if err != nil {
		t.Fatalf("consume first code: %v", err)
	}
	if !ok {
		t.Fatalf("first consumption of a fresh code must report true")
	}
	again, err := v.ConsumeRecovery("acme", "u1", "c1")
	if !errors.Is(err, impl.ErrRecoveryUsed) {
		t.Fatalf("second consumption error = %v, want %v", err, impl.ErrRecoveryUsed)
	}
	if again {
		t.Fatalf("second consumption must not report true")
	}
	st, err := v.SlotStatus("acme", "u1", "codes")
	if err != nil {
		t.Fatalf("slot status: %v", err)
	}
	if st.Consumed != 1 {
		t.Fatalf("consumed counter = %d, want 1", st.Consumed)
	}
	if st.Remaining != 1 {
		t.Fatalf("remaining codes = %d, want 1", st.Remaining)
	}
}

func TestConsumeRecoveryUnknownCode(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "codes", Kind: impl.SlotKindRecovery, Codes: []string{"c1"}})
	ok, err := v.ConsumeRecovery("acme", "u1", "zz")
	if !errors.Is(err, impl.ErrUnknownRecovery) {
		t.Fatalf("unknown code error = %v, want %v", err, impl.ErrUnknownRecovery)
	}
	if ok {
		t.Fatalf("unknown code must not be accepted")
	}
}

func TestRotateKEKResealsSealedSlots(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "totp", Kind: impl.SlotKindTOTP, Secret: []byte("seed")})
	mustSealSlot(t, v, "acme", "u1", "password")
	mustSealSlot(t, v, "acme", "u1", "totp")
	rep, err := v.RotateKEK(2, "kek-2")
	if err != nil {
		t.Fatalf("rotate kek: %v", err)
	}
	if rep.FromGeneration != 1 || rep.ToGeneration != 2 {
		t.Fatalf("rotation report = %+v, want 1 -> 2", rep)
	}
	if rep.Resealed != 2 {
		t.Fatalf("resealed count = %d, want 2", rep.Resealed)
	}
	if got := v.Generation(); got != 2 {
		t.Fatalf("vault generation = %d, want 2", got)
	}
	rec, err := v.Record("acme", "u1", "password")
	if err != nil {
		t.Fatalf("record after rotation: %v", err)
	}
	if rec.Generation != 2 || rec.KEKID != "kek-2" {
		t.Fatalf("record after rotation = gen %d kek %q, want gen 2 kek %q", rec.Generation, rec.KEKID, "kek-2")
	}
}

func TestRotateKEKRejectsNonMonotonicGeneration(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	mustSealSlot(t, v, "acme", "u1", "password")
	if _, err := v.RotateKEK(2, "kek-2"); err != nil {
		t.Fatalf("first rotation: %v", err)
	}
	if _, err := v.RotateKEK(2, "kek-2"); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("repeated generation error = %v, want %v", err, impl.ErrStaleGeneration)
	}
	if _, err := v.RotateKEK(4, "kek-4"); !errors.Is(err, impl.ErrStaleGeneration) {
		t.Fatalf("skipped generation error = %v, want %v", err, impl.ErrStaleGeneration)
	}
	if got := v.Generation(); got != 2 {
		t.Fatalf("vault generation after rejected rotations = %d, want 2", got)
	}
}

func TestRotateKEKAbortsOnUnsealedSlot(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u2", Name: "totp", Kind: impl.SlotKindTOTP})
	mustSealSlot(t, v, "acme", "u1", "password")
	if _, err := v.RotateKEK(2, "kek-2"); !errors.Is(err, impl.ErrNotSealed) {
		t.Fatalf("rotation with unsealed slot error = %v, want %v", err, impl.ErrNotSealed)
	}
	if got := v.Generation(); got != 1 {
		t.Fatalf("vault generation after aborted rotation = %d, want 1", got)
	}
	st, err := v.SlotStatus("acme", "u1", "password")
	if err != nil {
		t.Fatalf("slot status after aborted rotation: %v", err)
	}
	if st.Generation != 1 {
		t.Fatalf("sealed slot generation after aborted rotation = %d, want 1", st.Generation)
	}
}

func TestSnapshotRestoreKeepsRotationCounters(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-a")})
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "codes", Kind: impl.SlotKindRecovery, Codes: []string{"c1", "c2"}})
	rec := mustSealSlot(t, v, "acme", "u1", "password")
	mustSealSlot(t, v, "acme", "u1", "codes")
	if err := v.Open(rec, []byte("bad")); !errors.Is(err, impl.ErrSecretMismatch) {
		t.Fatalf("wrong secret error = %v, want %v", err, impl.ErrSecretMismatch)
	}
	if _, err := v.ConsumeRecovery("acme", "u1", "c1"); err != nil {
		t.Fatalf("consume recovery: %v", err)
	}
	if _, err := v.RotateKEK(2, "kek-2"); err != nil {
		t.Fatalf("rotation before snapshot: %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewSecretSlotVault(1, 9)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.Generation(); got != 2 {
		t.Fatalf("restored generation = %d, want 2", got)
	}
	pw, err := restored.SlotStatus("acme", "u1", "password")
	if err != nil {
		t.Fatalf("restored password status: %v", err)
	}
	if pw.State != impl.SlotActive || pw.Failures != 1 || pw.Generation != 2 {
		t.Fatalf("restored password slot = state %q failures %d gen %d, want active 1 2", pw.State, pw.Failures, pw.Generation)
	}
	codes, err := restored.SlotStatus("acme", "u1", "codes")
	if err != nil {
		t.Fatalf("restored recovery status: %v", err)
	}
	if codes.Consumed != 1 || codes.Remaining != 1 {
		t.Fatalf("restored recovery slot = consumed %d remaining %d, want 1 and 1", codes.Consumed, codes.Remaining)
	}
	restoredRec, err := restored.Record("acme", "u1", "password")
	if err != nil {
		t.Fatalf("restored record: %v", err)
	}
	if err := restored.Open(restoredRec, []byte("pw-a")); err != nil {
		t.Fatalf("open after restore: %v", err)
	}
	if got := restored.Stats().Sealed; got != 2 {
		t.Fatalf("restored sealed count = %d, want 2", got)
	}
}

func TestRestoreRejectsBadSnapshot(t *testing.T) {
	v := impl.NewSecretSlotVault(2, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	if err := v.Restore(impl.VaultSnapshot{}); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("empty snapshot error = %v, want %v", err, impl.ErrBadSnapshot)
	}
	if got := v.Stats().Slots; got != 1 {
		t.Fatalf("slot count after rejected restore = %d, want 1", got)
	}
}

func TestRetireBlocksOpen(t *testing.T) {
	v := impl.NewSecretSlotVault(4, 3)
	mustPutSlot(t, v, impl.SlotSpec{Tenant: "acme", User: "u1", Name: "password", Kind: impl.SlotKindPassword, Secret: []byte("pw-1")})
	rec := mustSealSlot(t, v, "acme", "u1", "password")
	if err := v.Retire("acme", "u1", "password"); err != nil {
		t.Fatalf("retire slot: %v", err)
	}
	if err := v.Open(rec, []byte("pw-1")); !errors.Is(err, impl.ErrSlotRetired) {
		t.Fatalf("open retired slot error = %v, want %v", err, impl.ErrSlotRetired)
	}
	st, err := v.SlotStatus("acme", "u1", "password")
	if err != nil {
		t.Fatalf("slot status: %v", err)
	}
	if st.State != impl.SlotRetired {
		t.Fatalf("state = %q, want %q", st.State, impl.SlotRetired)
	}
	if got := v.Stats().Retired; got != 1 {
		t.Fatalf("retired count = %d, want 1", got)
	}
}
