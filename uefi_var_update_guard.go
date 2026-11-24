package task

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
)

const (
	variableDb  = "db"
	variableDbx = "dbx"
	variableKek = "KEK"
)

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
	ErrEmptyHash          = errors.New("entry hash is empty")
	ErrSecureBootActive   = errors.New("secure boot must be disabled before deleting the last db entry")
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

	id uint64
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

type stagedBatch struct {
	batch           Batch
	validated       *Batch
	rejectedCounted bool
}

// UefiVarGuard maintains the db, dbx and KEK variables of one machine.
type UefiVarGuard struct {
	mu sync.Mutex

	limits     Limits
	secureBoot bool
	vars       map[string][]Entry
	revoked    map[string]bool
	lastIssued int64
	counter    uint64
	staged     map[string]*stagedBatch
	nextBatch  uint64
	stats      Stats
}

// NewUefiVarGuard builds a guard with the given quotas.
func NewUefiVarGuard(limits Limits) *UefiVarGuard {
	return &UefiVarGuard{
		limits: limits,
		vars: map[string][]Entry{
			variableDb:  {},
			variableDbx: {},
			variableKek: {},
		},
		revoked: make(map[string]bool),
		staged:  make(map[string]*stagedBatch),
	}
}

// SetSecureBoot flips the secure boot state that gates destructive deletes.
func (g *UefiVarGuard) SetSecureBoot(enabled bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.secureBoot = enabled
	return nil
}

// SecureBoot reports the current secure boot state.
func (g *UefiVarGuard) SecureBoot() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.secureBoot
}

// StageUpdate keeps one update payload for later validation.
func (g *UefiVarGuard) StageUpdate(variable string, ops []Op, chain Chain, counter uint64) (Batch, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !knownVariable(variable) {
		g.stats.Rejected++
		return Batch{}, ErrUnknownVariable
	}
	if len(ops) == 0 {
		g.stats.Rejected++
		return Batch{}, ErrEmptyBatch
	}
	for _, op := range ops {
		if op.Mode != ModeAppend && op.Mode != ModeDelete {
			g.stats.Rejected++
			return Batch{}, ErrInvalidMode
		}
		if op.Entry.Hash == "" {
			g.stats.Rejected++
			return Batch{}, ErrEmptyHash
		}
	}

	g.nextBatch++
	batch := Batch{
		Variable: variable,
		Ops:      cloneOps(ops),
		Chain:    chain,
		Counter:  counter,
		id:       g.nextBatch,
	}
	g.staged[variable] = &stagedBatch{batch: batch}
	g.stats.Staged++
	return batch, nil
}

// Validate checks the signature chain, revocation list, quota and counter.
func (g *UefiVarGuard) Validate(batch Batch) (Report, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := checkStructure(batch); err != nil {
		g.stats.Rejected++
		return Report{}, err
	}

	staged, ok := g.staged[batch.Variable]
	if !ok || staged.batch.id != batch.id {
		g.stats.Rejected++
		return Report{}, ErrBatchNotStaged
	}

	result, err := g.evaluate(batch)
	if err != nil {
		staged.validated = nil
		if !staged.rejectedCounted {
			staged.rejectedCounted = true
			g.stats.Rejected++
		}
		return Report{}, err
	}

	validated := cloneBatch(batch)
	validated.Validated = true
	staged.validated = &validated
	return result.report(), nil
}

// Commit applies the validated batch to the variable.
func (g *UefiVarGuard) Commit(batch Batch) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	staged, ok := g.staged[batch.Variable]
	if !ok {
		g.stats.Rejected++
		return ErrBatchNotStaged
	}
	if staged.batch.id != batch.id {
		g.stats.Rejected++
		return ErrBatchNotStaged
	}
	if staged.validated == nil {
		if !staged.rejectedCounted {
			staged.rejectedCounted = true
			g.stats.Rejected++
		}
		return ErrBatchNotStaged
	}

	validated := *staged.validated
	result, err := g.evaluate(validated)
	if err != nil {
		staged.validated = nil
		if !staged.rejectedCounted {
			staged.rejectedCounted = true
			g.stats.Rejected++
		}
		return err
	}

	g.vars[validated.Variable] = result.entries
	g.revoked = result.revoked
	g.lastIssued = validated.Chain.IssuedAt
	g.counter = validated.Counter
	delete(g.staged, validated.Variable)
	g.stats.Committed++
	return nil
}

// Revoke appends one hash to the revocation list.
func (g *UefiVarGuard) Revoke(hash string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if hash == "" {
		g.stats.Rejected++
		return ErrEmptyHash
	}
	if g.revoked[hash] {
		g.stats.Rejected++
		return ErrAlreadyRevoked
	}
	if len(g.vars[variableDbx]) >= g.limits.MaxDbxEntries {
		g.stats.Rejected++
		return ErrQuotaExceeded
	}

	g.revoked[hash] = true
	g.vars[variableDbx] = append(g.vars[variableDbx], Entry{Hash: hash})
	g.stats.Revoked++
	return nil
}

// SetTrustAnchor replaces the trust anchor with proper authorisation.
func (g *UefiVarGuard) SetTrustAnchor(anchor Entry, auth Auth) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !strings.HasPrefix(auth.Token, "trust:") || auth.Token == "trust:" {
		g.stats.Rejected++
		return ErrAuthRequired
	}
	if anchor.Hash == "" {
		g.stats.Rejected++
		return ErrBadAnchor
	}
	if g.limits.MaxKekEntries < 1 {
		g.stats.Rejected++
		return ErrQuotaExceeded
	}

	g.vars[variableKek] = []Entry{anchor}
	return nil
}

// Entries returns a copy of one variable's entries.
func (g *UefiVarGuard) Entries(variable string) []Entry {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !knownVariable(variable) {
		return nil
	}
	entries := g.vars[variable]
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// RollbackCounter returns the monotonic anti-rollback counter.
func (g *UefiVarGuard) RollbackCounter() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.counter
}

const (
	snapshotVersion = 1
	snapshotMagic   = "UEFIVARGUARD\x00\x01"
)

type snapshotPayload struct {
	Version    int                `json:"version"`
	Vars       map[string][]Entry `json:"vars"`
	Revoked    []string           `json:"revoked"`
	LastIssued int64              `json:"last_issued"`
	Counter    uint64             `json:"counter"`
	SecureBoot bool               `json:"secure_boot"`
}

// Snapshot serialises the guard state at a committed boundary.
func (g *UefiVarGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	revoked := make([]string, 0, len(g.revoked))
	for hash := range g.revoked {
		revoked = append(revoked, hash)
	}
	sort.Strings(revoked)

	payload := snapshotPayload{
		Version:    snapshotVersion,
		Vars:       cloneVars(g.vars),
		Revoked:    revoked,
		LastIssued: g.lastIssued,
		Counter:    g.counter,
		SecureBoot: g.secureBoot,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(snapshotMagic)+len(body)+sha256.Size)
	out = append(out, snapshotMagic...)
	out = append(out, body...)
	sum := sha256.Sum256(out)
	out = append(out, sum[:]...)
	return out, nil
}

// Restore loads a snapshot without lowering the rollback counter or timestamp.
func (g *UefiVarGuard) Restore(data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	payload, err := decodeSnapshot(data)
	if err != nil {
		return ErrBadSnapshot
	}

	vars := map[string][]Entry{
		variableDb:  cloneEntries(payload.Vars[variableDb]),
		variableDbx: cloneEntries(payload.Vars[variableDbx]),
		variableKek: cloneEntries(payload.Vars[variableKek]),
	}

	revoked := make(map[string]bool)
	for _, hash := range payload.Revoked {
		if hash == "" {
			return ErrBadSnapshot
		}
		revoked[hash] = true
	}
	for _, entry := range vars[variableDbx] {
		if entry.Hash == "" {
			return ErrBadSnapshot
		}
		revoked[entry.Hash] = true
	}
	// The revocation list is append-only, so a restore may never drop a hash
	// that is still present in the running guard.
	for hash := range g.revoked {
		revoked[hash] = true
	}
	var missing []string
	for hash := range revoked {
		if !containsHash(vars[variableDbx], hash) {
			missing = append(missing, hash)
		}
	}
	sort.Strings(missing)
	for _, hash := range missing {
		vars[variableDbx] = append(vars[variableDbx], Entry{Hash: hash})
	}

	if payload.Counter > g.counter {
		g.counter = payload.Counter
	}
	if payload.LastIssued > g.lastIssued {
		g.lastIssued = payload.LastIssued
	}
	g.vars = vars
	g.revoked = revoked
	g.secureBoot = payload.SecureBoot
	g.staged = make(map[string]*stagedBatch)
	return nil
}

// Stats returns the activity counters.
func (g *UefiVarGuard) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}

type evaluation struct {
	entries  []Entry
	revoked  map[string]bool
	appended int
	removed  int
	checked  int
}

func (e evaluation) report() Report {
	return Report{Appended: e.appended, Removed: e.removed, Checked: e.checked}
}

func (g *UefiVarGuard) evaluate(batch Batch) (evaluation, error) {
	if err := checkStructure(batch); err != nil {
		return evaluation{}, err
	}
	if batch.Chain.Signer == "" || !strings.HasPrefix(batch.Chain.Signature, "sig:") {
		return evaluation{}, ErrBadSignature
	}
	if batch.Chain.IssuedAt <= g.lastIssued {
		return evaluation{}, ErrStaleTimestamp
	}
	if batch.Counter <= g.counter {
		return evaluation{}, ErrRollbackRegression
	}

	work := cloneEntries(g.vars[batch.Variable])
	revoked := make(map[string]bool, len(g.revoked))
	for hash, present := range g.revoked {
		revoked[hash] = present
	}

	result := evaluation{}
	for _, op := range batch.Ops {
		switch op.Mode {
		case ModeAppend:
			if revoked[op.Entry.Hash] {
				return evaluation{}, ErrAlreadyRevoked
			}
			if containsHash(work, op.Entry.Hash) {
				if batch.Variable == variableDbx {
					return evaluation{}, ErrAlreadyRevoked
				}
				return evaluation{}, ErrDuplicateEntry
			}
			work = append(work, op.Entry)
			if batch.Variable == variableDbx {
				revoked[op.Entry.Hash] = true
			}
			result.appended++
		case ModeDelete:
			if batch.Variable == variableDbx {
				return evaluation{}, ErrInvalidMode
			}
			index := indexOfHash(work, op.Entry.Hash)
			if index < 0 {
				return evaluation{}, ErrEntryNotFound
			}
			work = append(work[:index], work[index+1:]...)
			result.removed++
			if batch.Variable == variableDb && len(work) == 0 && g.secureBoot {
				return evaluation{}, ErrSecureBootActive
			}
		default:
			return evaluation{}, ErrInvalidMode
		}
		result.checked++
	}

	if len(work) > g.quota(batch.Variable) {
		return evaluation{}, ErrQuotaExceeded
	}
	result.entries = work
	result.revoked = revoked
	return result, nil
}

func (g *UefiVarGuard) quota(variable string) int {
	switch variable {
	case variableDb:
		return g.limits.MaxDbEntries
	case variableDbx:
		return g.limits.MaxDbxEntries
	case variableKek:
		return g.limits.MaxKekEntries
	default:
		return 0
	}
}

func checkStructure(batch Batch) error {
	if !knownVariable(batch.Variable) {
		return ErrUnknownVariable
	}
	if len(batch.Ops) == 0 {
		return ErrEmptyBatch
	}
	for _, op := range batch.Ops {
		if op.Mode != ModeAppend && op.Mode != ModeDelete {
			return ErrInvalidMode
		}
		if op.Entry.Hash == "" {
			return ErrEmptyHash
		}
	}
	return nil
}

func knownVariable(variable string) bool {
	switch variable {
	case variableDb, variableDbx, variableKek:
		return true
	default:
		return false
	}
}

func containsHash(entries []Entry, hash string) bool {
	return indexOfHash(entries, hash) >= 0
}

func indexOfHash(entries []Entry, hash string) int {
	for index, entry := range entries {
		if entry.Hash == hash {
			return index
		}
	}
	return -1
}

func cloneEntries(entries []Entry) []Entry {
	if len(entries) == 0 {
		return []Entry{}
	}
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

func cloneOps(ops []Op) []Op {
	if len(ops) == 0 {
		return []Op{}
	}
	out := make([]Op, len(ops))
	copy(out, ops)
	return out
}

func cloneBatch(batch Batch) Batch {
	batch.Ops = cloneOps(batch.Ops)
	return batch
}

func cloneVars(vars map[string][]Entry) map[string][]Entry {
	out := make(map[string][]Entry, len(vars))
	for name, entries := range vars {
		out[name] = cloneEntries(entries)
	}
	return out
}

func decodeSnapshot(data []byte) (snapshotPayload, error) {
	if len(data) < len(snapshotMagic)+sha256.Size {
		return snapshotPayload{}, ErrBadSnapshot
	}
	if string(data[:len(snapshotMagic)]) != snapshotMagic {
		return snapshotPayload{}, ErrBadSnapshot
	}

	bodyEnd := len(data) - sha256.Size
	body := data[len(snapshotMagic):bodyEnd]
	want := data[bodyEnd:]
	got := sha256.Sum256(data[:bodyEnd])
	if subtle.ConstantTimeCompare(want, got[:]) != 1 {
		return snapshotPayload{}, ErrBadSnapshot
	}

	var payload snapshotPayload
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return snapshotPayload{}, ErrBadSnapshot
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return snapshotPayload{}, ErrBadSnapshot
	}
	if payload.Version != snapshotVersion {
		return snapshotPayload{}, ErrBadSnapshot
	}

	for name := range payload.Vars {
		if !knownVariable(name) {
			return snapshotPayload{}, ErrBadSnapshot
		}
	}
	for _, name := range []string{variableDb, variableDbx, variableKek} {
		seen := make(map[string]bool)
		for _, entry := range payload.Vars[name] {
			if entry.Hash == "" || seen[entry.Hash] {
				return snapshotPayload{}, ErrBadSnapshot
			}
			seen[entry.Hash] = true
		}
	}
	return payload, nil
}
