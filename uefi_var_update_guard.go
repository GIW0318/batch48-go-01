package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("not implemented")

// Update failures.
var (
	ErrUnknownVariable    = errors.New("unknown variable name")
	ErrEmptyBatch         = errors.New("batch carries no operation")
	ErrInvalidMode        = errors.New("unknown operation mode")
	ErrBadSignature       = errors.New("signature chain is missing or malformed")
	ErrStaleTimestamp     = errors.New("payload timestamp is not newer than the stored one")
	ErrAlreadyRevoked     = errors.New("entry hash is present in the revocation list")
	ErrQuotaExceeded      = errors.New("variable quota would be exceeded")
	ErrAuthRequired       = errors.New("trust anchor change requires an authorisation token")
	ErrBatchNotStaged     = errors.New("batch was not staged or not validated")
	ErrRollbackRegression = errors.New("rollback counter would move backwards")
	ErrBadSnapshot        = errors.New("snapshot payload is corrupt")
	ErrEntryNotFound      = errors.New("entry to delete is not present")
	ErrDuplicateEntry     = errors.New("entry is already present in the variable")
	ErrBadAnchor          = errors.New("trust anchor payload is incomplete")
)

// Mode selects how one operation treats the variable.
type Mode string

// The two operation modes.
const (
	ModeAppend Mode = "append"
	ModeDelete Mode = "delete"
)

// Limits bounds the number of entries kept per variable.
type Limits struct {
	MaxDbEntries  int
	MaxDbxEntries int
	MaxKekEntries int
}

// Entry is one signature list entry.
type Entry struct {
	Owner string
	Hash  string
	Name  string
}

// Op is a single append or delete inside a batch.
type Op struct {
	Mode  Mode
	Entry Entry
}

// Chain is the detached signature chain that travels with an update payload.
type Chain struct {
	Signer    string
	Signature string
	IssuedAt  int64
}

// Auth carries the extra authorisation needed for trust anchor changes.
type Auth struct {
	Token string
}

// Batch is one staged update of a variable.
type Batch struct {
	Variable  string
	Ops       []Op
	Chain     Chain
	Counter   uint64
	Validated bool
}

// Report summarises a validated batch.
type Report struct {
	Appended int
	Removed  int
	Checked  int
}

// Stats counts guard activity.
type Stats struct {
	Staged    int
	Committed int
	Rejected  int
	Revoked   int
}

// UefiVarGuard maintains the db, dbx and KEK variables of one machine.
type UefiVarGuard struct {
	limits     Limits
	secureBoot bool
	vars       map[string][]Entry
	dbx        map[string]bool
	lastIssued int64
	counter    uint64
	staged     *Batch
	stagedOK   bool
	stats      Stats
}

// NewUefiVarGuard builds a guard with the given quotas.
func NewUefiVarGuard(limits Limits) *UefiVarGuard {
	return &UefiVarGuard{limits: limits}
}

// SetSecureBoot flips the secure boot state that gates destructive deletes.
func (g *UefiVarGuard) SetSecureBoot(enabled bool) error {
	return errNotImplemented
}

// SecureBoot reports the current secure boot state.
func (g *UefiVarGuard) SecureBoot() bool {
	return false
}

// StageUpdate keeps one update payload for later validation.
func (g *UefiVarGuard) StageUpdate(variable string, ops []Op, chain Chain, counter uint64) (Batch, error) {
	return Batch{}, errNotImplemented
}

// Validate checks the signature chain, revocation list, quota and counter.
func (g *UefiVarGuard) Validate(batch Batch) (Report, error) {
	return Report{}, errNotImplemented
}

// Commit applies the validated batch to the variable.
func (g *UefiVarGuard) Commit(batch Batch) error {
	return errNotImplemented
}

// Revoke appends one hash to the revocation list.
func (g *UefiVarGuard) Revoke(hash string) error {
	return errNotImplemented
}

// SetTrustAnchor replaces the trust anchor with proper authorisation.
func (g *UefiVarGuard) SetTrustAnchor(anchor Entry, auth Auth) error {
	return errNotImplemented
}

// Entries returns a copy of one variable's entries.
func (g *UefiVarGuard) Entries(variable string) []Entry {
	return nil
}

// RollbackCounter returns the monotonic anti-rollback counter.
func (g *UefiVarGuard) RollbackCounter() uint64 {
	return 0
}

// Snapshot serialises the guard state.
func (g *UefiVarGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot without lowering the rollback counter.
func (g *UefiVarGuard) Restore(data []byte) error {
	return errNotImplemented
}

// Stats returns the activity counters.
func (g *UefiVarGuard) Stats() Stats {
	return Stats{}
}
