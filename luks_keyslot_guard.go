package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Keyslot failures.
var (
	ErrBadKind        = errors.New("credential kind is unknown")
	ErrBadSecret      = errors.New("credential material is empty or wrong")
	ErrSlotQuota      = errors.New("keyslot quota is exhausted")
	ErrNoSuchSlot     = errors.New("keyslot does not exist")
	ErrSlotLocked     = errors.New("keyslot is locked after too many failures")
	ErrKindMismatch   = errors.New("credential kind does not match the slot kind")
	ErrActiveSlot     = errors.New("active keyslot cannot be removed")
	ErrVolumeReadOnly = errors.New("volume is read only while rekeying")
	ErrBadSnapshot    = errors.New("snapshot payload is corrupt")
)

// Kind is one class of unlock credential.
type Kind string

// The three credential classes, each with its own read path.
const (
	KindPassphrase Kind = "passphrase"
	KindKeyFile    Kind = "keyfile"
	KindRecovery   Kind = "recovery"
)

// Credential is one unlock attempt.
type Credential struct {
	Kind   Kind
	Secret string
}

// Limits bounds one keyslot guard.
type Limits struct {
	MaxSlots  int
	LockAfter int
}

// SlotInfo is the metadata of one keyslot.
type SlotInfo struct {
	Slot   int
	Kind   Kind
	Label  string
	Fails  int
	Locked bool
	Active bool
}

// RekeyResult reports the outcome of a rekey.
type RekeyResult struct {
	OldSlot int
	NewSlot int
	Active  int
}

// Stats counts keyslot activity.
type Stats struct {
	Added    int
	Unlocked int
	Failed   int
	Rekeys   int
}

type slotState struct {
	kind   Kind
	label  string
	secret string
	fails  int
}

// KeyslotGuard manages the keyslots of one encrypted volume.
type KeyslotGuard struct {
	limits   Limits
	nextSlot int
	slots    map[int]*slotState
	active   int
	rekeying bool
	stats    Stats
}

// NewKeyslotGuard builds a guard with the given limits.
func NewKeyslotGuard(limits Limits) *KeyslotGuard {
	return &KeyslotGuard{limits: limits}
}

// AddSlot allocates the next keyslot for one credential.
func (g *KeyslotGuard) AddSlot(kind Kind, label, secret string) (int, error) {
	return 0, errNotImplemented
}

// Unlock attempts to unlock one slot with the given credential.
func (g *KeyslotGuard) Unlock(slot int, cred Credential) error {
	return errNotImplemented
}

// Rekey writes a new slot, verifies it and switches the active slot atomically.
func (g *KeyslotGuard) Rekey(slot int, newCred Credential) (RekeyResult, error) {
	return RekeyResult{}, errNotImplemented
}

// RemoveSlot frees a keyslot that is not the active one.
func (g *KeyslotGuard) RemoveSlot(slot int) error {
	return errNotImplemented
}

// SetActive switches the active keyslot.
func (g *KeyslotGuard) SetActive(slot int) error {
	return errNotImplemented
}

// Active returns the active keyslot number.
func (g *KeyslotGuard) Active() int {
	return 0
}

// Slots returns the slot metadata in slot order.
func (g *KeyslotGuard) Slots() []SlotInfo {
	return nil
}

// Locked reports whether a slot is locked.
func (g *KeyslotGuard) Locked(slot int) bool {
	return false
}

// CredentialPath returns the read path reserved for one credential class.
func (g *KeyslotGuard) CredentialPath(kind Kind) (string, error) {
	return "", errNotImplemented
}

// WriteActive accepts volume writes through the active slot.
func (g *KeyslotGuard) WriteActive(payload []byte) (int, error) {
	return 0, errNotImplemented
}

// Snapshot serialises slot metadata and counters, never credential material.
func (g *KeyslotGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot of slot metadata.
func (g *KeyslotGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *KeyslotGuard) Stats() Stats {
	return Stats{}
}
