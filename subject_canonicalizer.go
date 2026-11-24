// Package task canonicalises tenant scoped subject identifiers, keeps a per
// tenant reservation table and reports positional differences against the
// canonical form.
package task

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Errors returned by the canonicalizer.
var (
	ErrBlankTenant      = errors.New("blank tenant")
	ErrBlankIdentity    = errors.New("blank identity")
	ErrUnknownKind      = errors.New("unknown kind")
	ErrKeyTooLong       = errors.New("key too long")
	ErrEmptySubject     = errors.New("empty subject after normalization")
	ErrMalformedEmail   = errors.New("malformed email")
	ErrReservationLimit = errors.New("reservation limit")
	ErrBadSnapshot      = errors.New("bad snapshot")
)

// Subject kinds accepted by the canonicalizer.
const (
	KindAccount = "account"
	KindEmail   = "email"
	KindGroup   = "group"
)

// Reservation actions kept in the short history of one key.
const (
	ActionReserve = "reserve"
	ActionRelease = "release"
)

const snapshotVersion = 1

// CanonRules bounds the reservation table and the key length.
type CanonRules struct {
	MaxReservationsPerTenant int
	HistoryDepth             int
	MaxKeyLen                int
}

// Key is the canonical form of a subject identifier.
type Key struct {
	TenantID string
	Kind     string
	Value    string
	Digest   string
}

// Conflict is one positional difference between the raw and canonical form.
type Conflict struct {
	Position int
	Want     rune
	Got      rune
	Reason   string
}

// Reservation is one entry of the short history kept for a reserved key.
type Reservation struct {
	Key       string
	AccountID string
	At        time.Time
	Action    string
}

// SubjectCanonicalizer keeps reservations and history isolated per tenant.
type SubjectCanonicalizer struct {
	mu       sync.Mutex
	rules    CanonRules
	reserved map[string]map[string]Reservation
	history  map[string]map[string][]Reservation
}

type canonSnapshot struct {
	Version  int                                 `json:"version"`
	Reserved map[string]map[string]Reservation   `json:"reserved"`
	History  map[string]map[string][]Reservation `json:"history"`
}

// NewSubjectCanonicalizer builds a canonicalizer with the supplied rules.
func NewSubjectCanonicalizer(rules CanonRules) *SubjectCanonicalizer {
	return &SubjectCanonicalizer{
		rules:    rules,
		reserved: make(map[string]map[string]Reservation),
		history:  make(map[string]map[string][]Reservation),
	}
}

// Canonicalize folds the raw identifier of one kind into its canonical key.
func (c *SubjectCanonicalizer) Canonicalize(tenantID, kind, raw string) (Key, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Key{}, ErrBlankTenant
	}

	switch kind {
	case KindAccount, KindEmail, KindGroup:
	default:
		return Key{}, ErrUnknownKind
	}

	if raw == "" {
		return Key{}, ErrBlankIdentity
	}
	if c.rules.MaxKeyLen > 0 && utf8.RuneCountInString(raw) > c.rules.MaxKeyLen {
		return Key{}, ErrKeyTooLong
	}

	var value string
	var err error
	if kind == KindEmail {
		value, err = canonicalEmail(raw)
		if err != nil {
			return Key{}, err
		}
	} else {
		value = normalizeText(raw, true)
	}

	if value == "" {
		return Key{}, ErrEmptySubject
	}
	if c.rules.MaxKeyLen > 0 && utf8.RuneCountInString(value) > c.rules.MaxKeyLen {
		return Key{}, ErrKeyTooLong
	}

	return Key{
		TenantID: tenantID,
		Kind:     kind,
		Value:    value,
		Digest:   digestFor(tenantID, kind, value),
	}, nil
}

// Reserve claims one canonical key for an account inside a tenant.
func (c *SubjectCanonicalizer) Reserve(tenantID, key, accountID string) (bool, error) {
	if strings.TrimSpace(tenantID) == "" {
		return false, ErrBlankTenant
	}
	if strings.TrimSpace(key) == "" {
		return false, ErrBlankIdentity
	}
	if strings.TrimSpace(accountID) == "" {
		return false, ErrBlankIdentity
	}
	if c.rules.MaxKeyLen > 0 && utf8.RuneCountInString(key) > c.rules.MaxKeyLen {
		return false, ErrKeyTooLong
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reserved == nil {
		c.reserved = make(map[string]map[string]Reservation)
	}
	tenant := c.reserved[tenantID]
	if _, exists := tenant[key]; exists {
		return false, nil
	}
	if c.rules.MaxReservationsPerTenant > 0 && len(tenant) >= c.rules.MaxReservationsPerTenant {
		return false, ErrReservationLimit
	}
	if tenant == nil {
		tenant = make(map[string]Reservation)
		c.reserved[tenantID] = tenant
	}

	reservation := Reservation{
		Key:       key,
		AccountID: accountID,
		At:        time.Now().UTC().Round(0),
		Action:    ActionReserve,
	}
	tenant[key] = reservation
	c.appendHistoryLocked(tenantID, key, reservation)
	return true, nil
}

// Release drops the reservation of one canonical key.
func (c *SubjectCanonicalizer) Release(tenantID, key string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrBlankTenant
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	tenant := c.reserved[tenantID]
	reservation, exists := tenant[key]
	if !exists {
		return nil
	}

	delete(tenant, key)
	if len(tenant) == 0 {
		delete(c.reserved, tenantID)
	}
	c.appendHistoryLocked(tenantID, key, Reservation{
		Key:       key,
		AccountID: reservation.AccountID,
		At:        time.Now().UTC().Round(0),
		Action:    ActionRelease,
	})
	return nil
}

// Lookup returns the account that owns a canonical key inside a tenant.
func (c *SubjectCanonicalizer) Lookup(tenantID, key string) (string, bool) {
	if strings.TrimSpace(tenantID) == "" || key == "" {
		return "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	reservation, exists := c.reserved[tenantID][key]
	if !exists {
		return "", false
	}
	return reservation.AccountID, true
}

// Conflicts reports where the raw identifier differs from its canonical form.
func (c *SubjectCanonicalizer) Conflicts(tenantID, kind, raw string) ([]Conflict, error) {
	key, err := c.Canonicalize(tenantID, kind, raw)
	if err != nil {
		return nil, err
	}
	return diffRawCanonical(raw, key.Value), nil
}

// History returns the short reservation history of one key, oldest first.
func (c *SubjectCanonicalizer) History(tenantID, key string) []Reservation {
	if strings.TrimSpace(tenantID) == "" || key == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entries := c.history[tenantID][key]
	if len(entries) == 0 {
		return nil
	}
	out := make([]Reservation, len(entries))
	copy(out, entries)
	return out
}

// ReserveCount reports how many keys the tenant currently holds.
func (c *SubjectCanonicalizer) ReserveCount(tenantID string) int {
	if strings.TrimSpace(tenantID) == "" {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reserved[tenantID])
}

// Snapshot serialises reservations and history of every tenant.
func (c *SubjectCanonicalizer) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(canonSnapshot{
		Version:  snapshotVersion,
		Reserved: c.reserved,
		History:  c.history,
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces reservations and history with a snapshot.
func (c *SubjectCanonicalizer) Restore(data []byte) error {
	if len(data) == 0 {
		return ErrBadSnapshot
	}

	var snapshot canonSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ErrBadSnapshot
	}
	if snapshot.Version != snapshotVersion {
		return ErrBadSnapshot
	}

	reserved, history := c.rebuildState(snapshot)
	c.mu.Lock()
	c.reserved = reserved
	c.history = history
	c.mu.Unlock()
	return nil
}

func (c *SubjectCanonicalizer) rebuildState(snapshot canonSnapshot) (map[string]map[string]Reservation, map[string]map[string][]Reservation) {
	reserved := make(map[string]map[string]Reservation)
	for tenantID, entries := range snapshot.Reserved {
		tenant := make(map[string]Reservation, len(entries))
		for key, reservation := range entries {
			tenant[key] = reservation
		}
		tenant = c.trimReservations(tenant)
		if len(tenant) > 0 {
			reserved[tenantID] = tenant
		}
	}

	history := make(map[string]map[string][]Reservation)
	if c.rules.HistoryDepth > 0 {
		for tenantID, byKey := range snapshot.History {
			tenant := make(map[string][]Reservation)
			for key, entries := range byKey {
				entries = trimHistoryEntries(entries, c.rules.HistoryDepth)
				if len(entries) > 0 {
					tenant[key] = entries
				}
			}
			if len(tenant) > 0 {
				history[tenantID] = tenant
			}
		}
	}
	return reserved, history
}

func (c *SubjectCanonicalizer) trimReservations(tenant map[string]Reservation) map[string]Reservation {
	limit := c.rules.MaxReservationsPerTenant
	if limit <= 0 || len(tenant) <= limit {
		return tenant
	}

	type entry struct {
		key string
		at  time.Time
	}
	entries := make([]entry, 0, len(tenant))
	for key, reservation := range tenant {
		entries = append(entries, entry{key: key, at: reservation.At})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].at.Equal(entries[j].at) {
			return entries[i].key < entries[j].key
		}
		return entries[i].at.After(entries[j].at)
	})

	trimmed := make(map[string]Reservation, limit)
	for _, item := range entries[:limit] {
		trimmed[item.key] = tenant[item.key]
	}
	return trimmed
}

func (c *SubjectCanonicalizer) appendHistoryLocked(tenantID, key string, reservation Reservation) {
	if c.rules.HistoryDepth <= 0 {
		return
	}

	if c.history == nil {
		c.history = make(map[string]map[string][]Reservation)
	}
	byKey := c.history[tenantID]
	if byKey == nil {
		byKey = make(map[string][]Reservation)
		c.history[tenantID] = byKey
	}
	byKey[key] = trimHistoryEntries(append(byKey[key], reservation), c.rules.HistoryDepth)
}

func trimHistoryEntries(entries []Reservation, depth int) []Reservation {
	if depth <= 0 {
		return nil
	}
	if len(entries) <= depth {
		out := make([]Reservation, len(entries))
		copy(out, entries)
		return out
	}
	out := make([]Reservation, depth)
	copy(out, entries[len(entries)-depth:])
	return out
}

func canonicalEmail(raw string) (string, error) {
	cleaned := normalizeText(raw, false)
	if cleaned == "" {
		return "", nil
	}
	if strings.Count(cleaned, "@") != 1 {
		return "", ErrMalformedEmail
	}

	parts := strings.SplitN(cleaned, "@", 2)
	local := normalizeText(parts[0], false)
	domain := normalizeText(parts[1], true)
	if local == "" || domain == "" {
		return "", ErrMalformedEmail
	}
	return local + "@" + domain, nil
}

func normalizeText(s string, foldCase bool) string {
	var builder strings.Builder
	builder.Grow(len(s))

	wrote := false
	pendingSpace := false
	for _, r := range s {
		r = foldWidth(r)
		if isInvisibleRune(r) {
			continue
		}
		if unicode.IsSpace(r) {
			if wrote {
				pendingSpace = true
			}
			continue
		}
		if unicode.IsControl(r) {
			continue
		}

		if pendingSpace {
			builder.WriteByte(' ')
			pendingSpace = false
		}
		if foldCase {
			r = unicode.ToLower(r)
		}
		builder.WriteRune(r)
		wrote = true
	}
	return builder.String()
}

func foldWidth(r rune) rune {
	switch {
	case r == '\u3000':
		return ' '
	case r >= '\uFF01' && r <= '\uFF5E':
		return r - 0xFEE0
	case r == '\uFFE0':
		return '\u00A2'
	case r == '\uFFE1':
		return '\u00A3'
	case r == '\uFFE2':
		return '\u00AC'
	case r == '\uFFE3':
		return '\u00AF'
	case r == '\uFFE4':
		return '\u00A6'
	case r == '\uFFE5':
		return '\u00A5'
	case r == '\uFFE6':
		return '\u20A9'
	default:
		return r
	}
}

func isInvisibleRune(r rune) bool {
	switch {
	case r == '\u00AD', r == '\u034F', r == '\u061C':
		return true
	case r >= '\u115F' && r <= '\u1160':
		return true
	case r >= '\u17B4' && r <= '\u17B5':
		return true
	case r >= '\u180B' && r <= '\u180E':
		return true
	case r >= '\u200B' && r <= '\u200F':
		return true
	case r >= '\u202A' && r <= '\u202E':
		return true
	case r >= '\u2060' && r <= '\u206F':
		return true
	case r == '\u3164', r == '\uFFA0':
		return true
	case r >= '\uFE00' && r <= '\uFE0F':
		return true
	case r >= '\uFFF0' && r <= '\uFFF8':
		return true
	case r >= '\U000E0000' && r <= '\U000E007F':
		return true
	case r >= '\U000E0100' && r <= '\U000E01EF':
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

func diffRawCanonical(raw, canonical string) []Conflict {
	rawRunes := []rune(raw)
	wantRunes := []rune(canonical)
	var conflicts []Conflict
	wantIndex := 0

	for position, rawRune := range rawRunes {
		folded := foldWidth(rawRune)
		if isInvisibleRune(folded) {
			conflicts = append(conflicts, Conflict{
				Position: position,
				Want:     runeAt(wantRunes, wantIndex),
				Got:      rawRune,
				Reason:   "invisible",
			})
			continue
		}

		if unicode.IsSpace(folded) {
			if wantIndex < len(wantRunes) && wantRunes[wantIndex] == ' ' {
				if folded != wantRunes[wantIndex] {
					reason := "whitespace"
					if folded != rawRune {
						reason = "fullwidth"
					}
					conflicts = append(conflicts, Conflict{
						Position: position,
						Want:     wantRunes[wantIndex],
						Got:      rawRune,
						Reason:   reason,
					})
				}
				wantIndex++
				continue
			}
			reason := "whitespace"
			if folded != rawRune {
				reason = "fullwidth"
			}
			conflicts = append(conflicts, Conflict{
				Position: position,
				Want:     runeAt(wantRunes, wantIndex),
				Got:      rawRune,
				Reason:   reason,
			})
			continue
		}

		if unicode.IsControl(folded) {
			conflicts = append(conflicts, Conflict{
				Position: position,
				Want:     runeAt(wantRunes, wantIndex),
				Got:      rawRune,
				Reason:   "invisible",
			})
			continue
		}

		if wantIndex >= len(wantRunes) {
			conflicts = append(conflicts, Conflict{
				Position: position,
				Want:     0,
				Got:      rawRune,
				Reason:   "canonical",
			})
			continue
		}

		want := wantRunes[wantIndex]
		if folded == want {
			wantIndex++
			continue
		}

		reason := "canonical"
		switch {
		case folded != rawRune:
			reason = "fullwidth"
		case unicode.ToLower(folded) == want || unicode.ToUpper(folded) == want:
			reason = "case"
		}
		conflicts = append(conflicts, Conflict{
			Position: position,
			Want:     want,
			Got:      rawRune,
			Reason:   reason,
		})
		wantIndex++
	}

	for ; wantIndex < len(wantRunes); wantIndex++ {
		conflicts = append(conflicts, Conflict{
			Position: len(rawRunes),
			Want:     wantRunes[wantIndex],
			Got:      0,
			Reason:   "canonical",
		})
	}
	return conflicts
}

func runeAt(runes []rune, index int) rune {
	if index < 0 || index >= len(runes) {
		return 0
	}
	return runes[index]
}

func digestFor(tenantID, kind, value string) string {
	hash := sha256.New()
	for _, part := range []string{tenantID, kind, value} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		hash.Write(size[:])
		hash.Write([]byte(part))
	}
	sum := hash.Sum(nil)
	return hex.EncodeToString(sum[:8])
}
