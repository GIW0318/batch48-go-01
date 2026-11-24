package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Sentinel errors reported by DelegatedGrantLedger.
var (
	ErrInvalidSubject    = errors.New("invalid subject id")
	ErrInvalidDelegation = errors.New("invalid delegation")
	ErrEmptyPermission   = errors.New("empty permission set")
	ErrInvalidExpiry     = errors.New("invalid grant expiry")
	ErrNotSubset         = errors.New("delegated permissions are not a subset of the delegator")
	ErrDepthLimit        = errors.New("delegation depth limit reached")
	ErrGrantLimit        = errors.New("grant limit reached for the subject")
	ErrUnknownGrant      = errors.New("unknown grant")
	ErrAlreadyAccepted   = errors.New("grant already accepted")
	ErrBadSnapshot       = errors.New("bad snapshot")
)

// Permission is one resource and action pair.
type Permission struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// PermissionSet is a collection of permissions.
type PermissionSet []Permission

// Grant is one delegation of permissions.
type Grant struct {
	ID        string        `json:"id"`
	FromID    string        `json:"from_id"`
	ToID      string        `json:"to_id"`
	Perms     PermissionSet `json:"perms"`
	ExpiresAt time.Time     `json:"expires_at"`
	Accepted  bool          `json:"accepted"`
	Revoked   bool          `json:"revoked"`
	Depth     int           `json:"depth"`
	ParentID  string        `json:"parent_id"`
}

// Source explains where one effective permission came from.
type Source struct {
	GrantID    string     `json:"grant_id"`
	Permission Permission `json:"permission"`
	Chain      []string   `json:"chain"`
}

// LedgerOptions bounds the ledger.
type LedgerOptions struct {
	MaxDepth            int
	MaxGrantsPerSubject int
	Now                 func() time.Time
}

// DelegatedGrantLedger registers, evaluates and revokes delegated grants.
type DelegatedGrantLedger struct {
	mu       sync.Mutex
	maxDepth int
	maxCount int
	now      func() time.Time

	seeds  map[string]PermissionSet
	grants map[string]*Grant
	order  []string
	nextID uint64
}

type provenance struct {
	grantID string
	chain   []string
}

type snapshotLedger struct {
	Version             int                      `json:"version"`
	MaxDepth            int                      `json:"max_depth"`
	MaxGrantsPerSubject int                      `json:"max_grants_per_subject"`
	Seeds               map[string]PermissionSet `json:"seeds"`
	Grants              []Grant                  `json:"grants"`
}

// NewDelegatedGrantLedger builds an empty ledger.
func NewDelegatedGrantLedger(opts LedgerOptions) *DelegatedGrantLedger {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &DelegatedGrantLedger{
		maxDepth: opts.MaxDepth,
		maxCount: opts.MaxGrantsPerSubject,
		now:      now,
		seeds:    make(map[string]PermissionSet),
		grants:   make(map[string]*Grant),
	}
}

// Seed gives a subject its own permissions. A later seed replaces the
// subject's previous permissions, which makes delegated permissions derived
// from the removed ones immediately ineffective.
func (l *DelegatedGrantLedger) Seed(subjectID string, perms PermissionSet) error {
	if subjectID == "" {
		return ErrInvalidSubject
	}
	normalized, err := normalizePermissions(perms)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.seeds[subjectID] = normalized
	return nil
}

// Delegate registers a delegation from one subject to another.
func (l *DelegatedGrantLedger) Delegate(fromID, toID string, perms PermissionSet, expiresAt time.Time) (Grant, error) {
	if fromID == "" || toID == "" || fromID == toID {
		return Grant{}, ErrInvalidDelegation
	}
	normalized, err := normalizePermissions(perms)
	if err != nil {
		return Grant{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if !now.Before(expiresAt) {
		return Grant{}, ErrInvalidExpiry
	}
	if l.maxCount > 0 && l.liveGrantCountLocked(toID, now) >= l.maxCount {
		return Grant{}, ErrGrantLimit
	}

	available := l.evaluateLocked(fromID, now)
	for _, perm := range normalized {
		if len(available[perm]) == 0 {
			return Grant{}, ErrNotSubset
		}
	}

	parentDepth, parentID := chooseParent(available, normalized)
	depth := parentDepth + 1
	if l.maxDepth > 0 && depth > l.maxDepth {
		return Grant{}, ErrDepthLimit
	}

	grant := &Grant{
		ID:        l.newGrantIDLocked(),
		FromID:    fromID,
		ToID:      toID,
		Perms:     append(PermissionSet(nil), normalized...),
		ExpiresAt: expiresAt,
		Depth:     depth,
		ParentID:  parentID,
	}
	l.grants[grant.ID] = grant
	l.order = append(l.order, grant.ID)
	return cloneGrant(grant), nil
}

// Accept marks a delegation as accepted by the delegate.
func (l *DelegatedGrantLedger) Accept(grantID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	grant, ok := l.grants[grantID]
	if !ok || grant.Revoked {
		return ErrUnknownGrant
	}
	if grant.Accepted {
		return ErrAlreadyAccepted
	}
	grant.Accepted = true
	return nil
}

// Effective evaluates the permissions of a subject at a point in time.
func (l *DelegatedGrantLedger) Effective(subjectID string, at time.Time) (PermissionSet, error) {
	if subjectID == "" {
		return nil, ErrInvalidSubject
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	available := l.evaluateLocked(subjectID, at)
	out := make(PermissionSet, 0, len(available))
	for perm := range available {
		out = append(out, perm)
	}
	sortPermissions(out)
	return out, nil
}

// Sources explains the origin of every effective permission of a subject.
func (l *DelegatedGrantLedger) Sources(subjectID string, at time.Time) ([]Source, error) {
	if subjectID == "" {
		return nil, ErrInvalidSubject
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	available := l.evaluateLocked(subjectID, at)
	sources := make([]Source, 0, len(available))
	for perm, candidates := range available {
		best := candidates[0]
		for _, candidate := range candidates[1:] {
			if lessProvenance(candidate, best) {
				best = candidate
			}
		}
		chain := append([]string{}, best.chain...)
		sources = append(sources, Source{
			GrantID:    best.grantID,
			Permission: perm,
			Chain:      chain,
		})
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Permission.Resource != sources[j].Permission.Resource {
			return sources[i].Permission.Resource < sources[j].Permission.Resource
		}
		return sources[i].Permission.Action < sources[j].Permission.Action
	})
	return sources, nil
}

// Revoke removes one grant and every delegation derived from it.
func (l *DelegatedGrantLedger) Revoke(grantID string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	grant, ok := l.grants[grantID]
	if !ok || grant.Revoked {
		return 0, ErrUnknownGrant
	}
	return l.revokeLocked(grantID), nil
}

// Cascade revokes every grant issued by one subject.
func (l *DelegatedGrantLedger) Cascade(subjectID string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	direct := make([]string, 0)
	for _, id := range l.order {
		grant := l.grants[id]
		if grant != nil && grant.FromID == subjectID && !grant.Revoked {
			direct = append(direct, id)
		}
	}

	total := 0
	for _, id := range direct {
		total += l.revokeLocked(id)
	}
	return total, nil
}

// GrantCount reports the live grants a subject holds.
func (l *DelegatedGrantLedger) GrantCount(subjectID string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if subjectID == "" {
		return 0, ErrInvalidSubject
	}
	return l.liveGrantCountLocked(subjectID, l.now()), nil
}

// Depth reports how many delegation hops a grant sits from its root.
func (l *DelegatedGrantLedger) Depth(grantID string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	grant, ok := l.grants[grantID]
	if !ok {
		return 0, ErrUnknownGrant
	}
	return grant.Depth, nil
}

// Snapshot serialises the whole ledger state.
func (l *DelegatedGrantLedger) Snapshot() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	seeds := make(map[string]PermissionSet, len(l.seeds))
	for subject, perms := range l.seeds {
		seeds[subject] = append(PermissionSet(nil), perms...)
	}

	grants := make([]Grant, 0, len(l.grants))
	for _, grant := range l.grants {
		grants = append(grants, cloneGrant(grant))
	}
	sort.Slice(grants, func(i, j int) bool {
		return grants[i].ID < grants[j].ID
	})

	state := snapshotLedger{
		Version:             1,
		MaxDepth:            l.maxDepth,
		MaxGrantsPerSubject: l.maxCount,
		Seeds:               seeds,
		Grants:              grants,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore rebuilds the ledger state from a snapshot.
func (l *DelegatedGrantLedger) Restore(data []byte) error {
	var state snapshotLedger
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if state.Version != 0 && state.Version != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, state.Version)
	}

	seeds := make(map[string]PermissionSet, len(state.Seeds))
	for subject, perms := range state.Seeds {
		if subject == "" {
			return fmt.Errorf("%w: empty seed subject", ErrBadSnapshot)
		}
		normalized, err := normalizePermissions(perms)
		if err != nil {
			return fmt.Errorf("%w: seed %q: %v", ErrBadSnapshot, subject, err)
		}
		seeds[subject] = normalized
	}

	grants := make(map[string]*Grant, len(state.Grants))
	for i := range state.Grants {
		restored := state.Grants[i]
		if restored.ID == "" || restored.FromID == "" || restored.ToID == "" ||
			restored.FromID == restored.ToID || restored.Depth < 1 ||
			restored.ExpiresAt.IsZero() {
			return fmt.Errorf("%w: invalid grant %q", ErrBadSnapshot, restored.ID)
		}
		if _, exists := grants[restored.ID]; exists {
			return fmt.Errorf("%w: duplicate grant %q", ErrBadSnapshot, restored.ID)
		}
		normalized, err := normalizePermissions(restored.Perms)
		if err != nil {
			return fmt.Errorf("%w: grant %q: %v", ErrBadSnapshot, restored.ID, err)
		}
		restored.Perms = normalized
		grants[restored.ID] = &restored
	}
	for _, grant := range grants {
		if grant.ParentID == "" {
			continue
		}
		parent, ok := grants[grant.ParentID]
		if !ok {
			return fmt.Errorf("%w: missing parent %q", ErrBadSnapshot, grant.ParentID)
		}
		if parent.ToID != grant.FromID {
			return fmt.Errorf("%w: parent %q does not belong to delegator %q", ErrBadSnapshot, grant.ParentID, grant.FromID)
		}
		if grant.Depth != parent.Depth+1 {
			return fmt.Errorf("%w: invalid depth for grant %q", ErrBadSnapshot, grant.ID)
		}
	}

	order := make([]string, 0, len(grants))
	for id := range grants {
		order = append(order, id)
	}
	sort.Strings(order)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.seeds = seeds
	l.grants = grants
	l.order = order
	l.nextID = 0
	l.maxDepth = state.MaxDepth
	l.maxCount = state.MaxGrantsPerSubject
	return nil
}

func normalizePermissions(perms PermissionSet) (PermissionSet, error) {
	if len(perms) == 0 {
		return nil, ErrEmptyPermission
	}
	out := append(PermissionSet(nil), perms...)
	for _, perm := range out {
		if perm.Resource == "" || perm.Action == "" {
			return nil, ErrInvalidDelegation
		}
	}
	sortPermissions(out)
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, ErrInvalidDelegation
		}
	}
	return out, nil
}

func sortPermissions(perms PermissionSet) {
	sort.Slice(perms, func(i, j int) bool {
		if perms[i].Resource != perms[j].Resource {
			return perms[i].Resource < perms[j].Resource
		}
		return perms[i].Action < perms[j].Action
	})
}

func cloneGrant(grant *Grant) Grant {
	if grant == nil {
		return Grant{}
	}
	out := *grant
	out.Perms = append(PermissionSet(nil), grant.Perms...)
	return out
}

func (l *DelegatedGrantLedger) newGrantIDLocked() string {
	for {
		l.nextID++
		id := fmt.Sprintf("grant-%d", l.nextID)
		if _, exists := l.grants[id]; !exists {
			return id
		}
	}
}

func (l *DelegatedGrantLedger) liveGrantCountLocked(subjectID string, now time.Time) int {
	count := 0
	for _, grant := range l.grants {
		if grant.ToID == subjectID && !grant.Revoked && now.Before(grant.ExpiresAt) {
			count++
		}
	}
	return count
}

// evaluateLocked returns every permission currently available to a subject,
// together with all of the source chains that establish it. Evaluation checks
// the whole chain on every call, so losing a permission at any hop, including
// by re-seeding a delegator, takes effect immediately.
func (l *DelegatedGrantLedger) evaluateLocked(subjectID string, at time.Time) map[Permission][]provenance {
	return l.evaluate(subjectID, at, make(map[string]bool))
}

func (l *DelegatedGrantLedger) evaluate(subjectID string, at time.Time, visiting map[string]bool) map[Permission][]provenance {
	available := make(map[Permission][]provenance)
	for _, perm := range l.seeds[subjectID] {
		addProvenance(available, perm, provenance{})
	}
	if visiting[subjectID] {
		return available
	}

	visiting[subjectID] = true
	defer delete(visiting, subjectID)

	for _, id := range l.order {
		grant := l.grants[id]
		if grant == nil || grant.ToID != subjectID || !grant.Accepted || grant.Revoked ||
			!at.Before(grant.ExpiresAt) {
			continue
		}

		from := l.evaluate(grant.FromID, at, visiting)
		valid := true
		for _, perm := range grant.Perms {
			if len(from[perm]) == 0 {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}

		for _, perm := range grant.Perms {
			for _, parent := range from[perm] {
				chain := append(append([]string(nil), parent.chain...), grant.ID)
				addProvenance(available, perm, provenance{grantID: grant.ID, chain: chain})
			}
		}
	}
	return available
}

func addProvenance(available map[Permission][]provenance, perm Permission, candidate provenance) {
	for _, existing := range available[perm] {
		if existing.grantID == candidate.grantID && equalStrings(existing.chain, candidate.chain) {
			return
		}
	}
	available[perm] = append(available[perm], candidate)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lessProvenance(a, b provenance) bool {
	if len(a.chain) != len(b.chain) {
		return len(a.chain) < len(b.chain)
	}
	for i := range a.chain {
		if a.chain[i] != b.chain[i] {
			return a.chain[i] < b.chain[i]
		}
	}
	return a.grantID < b.grantID
}

// chooseParent finds the deepest source used by a delegation and records its
// immediate parent grant. Own seeded permissions are roots, so they keep an
// empty parent id.
func chooseParent(available map[Permission][]provenance, perms PermissionSet) (int, string) {
	parentDepth := 0
	parentID := ""
	haveParent := false
	var deepest provenance

	for _, perm := range perms {
		candidates := available[perm]
		best := candidates[0]
		for _, candidate := range candidates[1:] {
			if lessProvenance(candidate, best) {
				best = candidate
			}
		}

		depth := len(best.chain)
		if depth > parentDepth || (depth == parentDepth && depth > 0 && lessProvenance(best, deepest)) {
			parentDepth = depth
			deepest = best
			haveParent = depth > 0
		}
	}

	if haveParent {
		parentID = deepest.chain[len(deepest.chain)-1]
	}
	return parentDepth, parentID
}

func (l *DelegatedGrantLedger) revokeLocked(grantID string) int {
	children := make(map[string][]string)
	for id, grant := range l.grants {
		if grant.ParentID != "" {
			children[grant.ParentID] = append(children[grant.ParentID], id)
		}
	}

	revoked := 0
	stack := []string{grantID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		grant := l.grants[id]
		if grant == nil || grant.Revoked {
			continue
		}
		grant.Revoked = true
		revoked++
		stack = append(stack, children[id]...)
	}
	return revoked
}
