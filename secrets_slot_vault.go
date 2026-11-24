package task

import "errors"

// errNotImplemented is returned by behaviour that still has to be built.
var errNotImplemented = errors.New("not implemented")

// Sentinel errors reported by the slot vault.
var (
	ErrInvalidTenant    = errors.New("secrets_slot_vault: invalid tenant")
	ErrInvalidSlot      = errors.New("secrets_slot_vault: invalid slot")
	ErrSlotExists       = errors.New("secrets_slot_vault: slot already exists")
	ErrSlotNotFound     = errors.New("secrets_slot_vault: slot not found")
	ErrSlotLimit        = errors.New("secrets_slot_vault: slot limit reached")
	ErrSlotLocked       = errors.New("secrets_slot_vault: slot locked")
	ErrSlotRetired      = errors.New("secrets_slot_vault: slot retired")
	ErrNotSealed        = errors.New("secrets_slot_vault: slot carries no sealed record")
	ErrSecretMismatch   = errors.New("secrets_slot_vault: secret mismatch")
	ErrUnknownRecovery  = errors.New("secrets_slot_vault: unknown recovery code")
	ErrRecoveryUsed     = errors.New("secrets_slot_vault: recovery code already consumed")
	ErrRotationRequired = errors.New("secrets_slot_vault: rotation required before open")
	ErrStaleGeneration  = errors.New("secrets_slot_vault: generation must advance by exactly one")
	ErrInvalidKEK       = errors.New("secrets_slot_vault: invalid kek id")
	ErrBadSnapshot      = errors.New("secrets_slot_vault: bad snapshot")
)

// SlotKind is the category of material kept in one slot.
type SlotKind string

// Supported slot kinds.
const (
	SlotKindPassword SlotKind = "password"
	SlotKindTOTP     SlotKind = "totp"
	SlotKindRecovery SlotKind = "recovery"
)

// SlotState is the lifecycle state of one slot.
type SlotState string

// Supported slot states.
const (
	SlotActive  SlotState = "active"
	SlotLocked  SlotState = "locked"
	SlotRetired SlotState = "retired"
)

// SlotSpec is one slot registration request.
type SlotSpec struct {
	Tenant string
	User   string
	Name   string
	Kind   SlotKind
	Secret []byte
	Codes  []string
}

// SealedRecord is the sealed form of one slot, bound to a KEK generation.
type SealedRecord struct {
	Tenant     string
	User       string
	Name       string
	Generation int
	KEKID      string
	Algorithm  string
	Tag        uint64
}

// SlotStatus is the observable state of one slot.
type SlotStatus struct {
	Tenant     string
	User       string
	Name       string
	Kind       SlotKind
	State      SlotState
	Generation int
	Failures   int
	Consumed   int
	Remaining  int
	Grace      int
}

// RotateReport describes one KEK rotation.
type RotateReport struct {
	FromGeneration int
	ToGeneration   int
	Resealed       int
	Skipped        int
}

// SlotSnapshot is the portable form of one slot.
type SlotSnapshot struct {
	Tenant     string
	User       string
	Name       string
	Kind       SlotKind
	State      SlotState
	Generation int
	Failures   int
	Consumed   int
	Sealed     bool
	Secret     []byte
	Codes      []string
	UsedCodes  []string
}

// VaultSnapshot is the portable form of a whole vault.
type VaultSnapshot struct {
	Generation  int
	KEKID       string
	Grace       int
	MaxFailures int
	MaxSlots    int
	Slots       []SlotSnapshot
}

// VaultStats aggregates slot counters.
type VaultStats struct {
	Slots    int
	Sealed   int
	Active   int
	Locked   int
	Retired  int
	Failures int
	Consumed int
}

// SecretSlotVault keeps sealed secrets per tenant and user.
type SecretSlotVault struct {
	maxSlots    int
	maxFailures int
}

// NewSecretSlotVault builds a vault with a slot ceiling and a failure ceiling.
func NewSecretSlotVault(maxSlots, maxFailures int) *SecretSlotVault {
	return &SecretSlotVault{}
}

// Generation reports the KEK generation currently in force.
func (v *SecretSlotVault) Generation() int {
	return 0
}

// GraceOpens reports how many opens an unrenewed record may still serve.
func (v *SecretSlotVault) GraceOpens() int {
	return 0
}

// SetGraceOpens changes the grace allowance for stale records.
func (v *SecretSlotVault) SetGraceOpens(n int) error {
	return errNotImplemented
}

// PutSlot registers one slot for a tenant and user.
func (v *SecretSlotVault) PutSlot(spec SlotSpec) error {
	return errNotImplemented
}

// Seal wraps the slot payload into a record bound to the current generation.
func (v *SecretSlotVault) Seal(tenant, user, name string) (SealedRecord, error) {
	return SealedRecord{}, errNotImplemented
}

// Record returns the current sealed record of one slot.
func (v *SecretSlotVault) Record(tenant, user, name string) (SealedRecord, error) {
	return SealedRecord{}, errNotImplemented
}

// Open verifies a secret against the slot behind the supplied sealed record.
func (v *SecretSlotVault) Open(rec SealedRecord, secret []byte) error {
	return errNotImplemented
}

// ConsumeRecovery spends one recovery code of a recovery slot.
func (v *SecretSlotVault) ConsumeRecovery(tenant, user, code string) (bool, error) {
	return false, errNotImplemented
}

// RotateKEK advances to the next KEK generation and re-seals every slot.
func (v *SecretSlotVault) RotateKEK(nextGeneration int, kekID string) (RotateReport, error) {
	return RotateReport{}, errNotImplemented
}

// Unlock returns a locked slot to active.
func (v *SecretSlotVault) Unlock(tenant, user, name string) error {
	return errNotImplemented
}

// RegenRecovery installs a fresh batch of recovery codes on an unlocked slot.
func (v *SecretSlotVault) RegenRecovery(tenant, user string, codes []string) error {
	return errNotImplemented
}

// Retire takes a slot out of service.
func (v *SecretSlotVault) Retire(tenant, user, name string) error {
	return errNotImplemented
}

// SlotStatus reports one slot.
func (v *SecretSlotVault) SlotStatus(tenant, user, name string) (SlotStatus, error) {
	return SlotStatus{}, errNotImplemented
}

// Snapshot captures generations, counters and sealed payloads.
func (v *SecretSlotVault) Snapshot() (VaultSnapshot, error) {
	return VaultSnapshot{}, errNotImplemented
}

// Restore replaces the vault contents with a snapshot.
func (v *SecretSlotVault) Restore(snap VaultSnapshot) error {
	return errNotImplemented
}

// Stats aggregates slot counters.
func (v *SecretSlotVault) Stats() VaultStats {
	return VaultStats{}
}
