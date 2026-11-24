// Package task turns a set of SRV records into a backend selector: weighted
// round robin inside the preferred priority, health suppression with a single
// half open probe, TTL staleness while waiting for a new record set and sticky
// keys that survive as long as the record set does.
package task

import (
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Errors reported by the balancer.
var (
	ErrEmptyRecordSet   = errors.New("empty record set")
	ErrDuplicateTarget  = errors.New("duplicate target")
	ErrInvalidRecord    = errors.New("invalid srv record")
	ErrInvalidPolicy    = errors.New("invalid balancer policy")
	ErrNoRecords        = errors.New("no record set loaded")
	ErrNoHealthyBackend = errors.New("no healthy backend")
	ErrUnknownTarget    = errors.New("unknown target")
	ErrBadSnapshot      = errors.New("bad snapshot")
)

// Target is one backend endpoint.
type Target struct {
	Host string
	Port uint16
}

// String renders the target as host:port.
func (t Target) String() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(int(t.Port)))
}

// SRVRecord is one record of the service discovery set.
type SRVRecord struct {
	Host     string
	Port     uint16
	Priority uint16
	Weight   uint16
	TTL      time.Duration
}

// Policy controls health handling and the sticky key table.
type Policy struct {
	FailThreshold int
	Cooldown      time.Duration
	MaxKeys       int
}

// BalancerStats counts what the balancer did so far.
type BalancerStats struct {
	Records         int
	Picks           int
	Updates         int
	Stale           bool
	Suppressed      int
	Probes          int
	Migrations      int
	StickyKeys      int
	WeightZeroPicks int
}

// entry is the mutable side of one record.
type entry struct {
	target        Target
	priority      uint16
	weight        uint16
	ttl           time.Duration
	failCount     int
	suppressed    bool
	cooldownUntil time.Time
	probing       bool
	current       int64
}

// priorityGroup holds the entries of one priority in stable order.
type priorityGroup struct {
	priority   uint16
	entries    []*entry
	zeroCursor int
}

// SRVBalancer serialises every selection of one record set.
type SRVBalancer struct {
	mu sync.Mutex

	policy Policy
	now    time.Time

	loaded    bool
	entries   []*entry
	byTarget  map[Target]*entry
	groups    []*priorityGroup
	expiresAt time.Time
	stale     bool

	sticky      map[string]Target
	stickyOrder []string

	stats BalancerStats
}

const snapshotVersion = 1

func validatePolicy(p Policy) error {
	if p.FailThreshold <= 0 || p.Cooldown <= 0 || p.MaxKeys <= 0 {
		return ErrInvalidPolicy
	}
	return nil
}

func validateRecord(r SRVRecord) error {
	if r.Host == "" || r.Port == 0 || r.TTL <= 0 {
		return ErrInvalidRecord
	}
	return nil
}

// NewSRVBalancer validates the policy and builds the balancer.
func NewSRVBalancer(p Policy) (*SRVBalancer, error) {
	if err := validatePolicy(p); err != nil {
		return nil, err
	}
	return &SRVBalancer{
		policy:      p,
		byTarget:    make(map[Target]*entry),
		sticky:      make(map[string]Target),
		stickyOrder: make([]string, 0, p.MaxKeys),
	}, nil
}

// Update replaces the record set with a validated one.
func (b *SRVBalancer) Update(records []SRVRecord) error {
	if len(records) == 0 {
		return ErrEmptyRecordSet
	}

	seen := make(map[Target]struct{}, len(records))
	minTTL := records[0].TTL
	for _, r := range records {
		if err := validateRecord(r); err != nil {
			return err
		}
		tgt := Target{Host: r.Host, Port: r.Port}
		if _, ok := seen[tgt]; ok {
			return ErrDuplicateTarget
		}
		seen[tgt] = struct{}{}
		if r.TTL < minTTL {
			minTTL = r.TTL
		}
	}

	sorted := append([]SRVRecord(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		if sorted[i].Host != sorted[j].Host {
			return sorted[i].Host < sorted[j].Host
		}
		return sorted[i].Port < sorted[j].Port
	})

	b.mu.Lock()
	defer b.mu.Unlock()

	newEntries := make([]*entry, 0, len(sorted))
	newByTarget := make(map[Target]*entry, len(sorted))
	for _, r := range sorted {
		e := &entry{
			target:   Target{Host: r.Host, Port: r.Port},
			priority: r.Priority,
			weight:   r.Weight,
			ttl:      r.TTL,
		}
		if old, ok := b.byTarget[e.target]; ok {
			e.failCount = old.failCount
			e.suppressed = old.suppressed
			e.cooldownUntil = old.cooldownUntil
			e.probing = old.probing
		}
		newEntries = append(newEntries, e)
		newByTarget[e.target] = e
	}

	newGroups := buildGroups(newEntries)
	oldGroups := make(map[uint16]*priorityGroup, len(b.groups))
	for _, g := range b.groups {
		oldGroups[g.priority] = g
	}
	for _, g := range newGroups {
		old, ok := oldGroups[g.priority]
		if !ok || !sameGroup(old, g) {
			continue
		}
		for _, e := range g.entries {
			if oldEntry, ok := b.byTarget[e.target]; ok {
				e.current = oldEntry.current
			}
		}
		g.zeroCursor = old.zeroCursor
	}

	newSticky := make(map[string]Target, len(b.sticky))
	newOrder := make([]string, 0, len(b.stickyOrder))
	for _, key := range b.stickyOrder {
		tgt, ok := b.sticky[key]
		if !ok {
			continue
		}
		if _, exists := newByTarget[tgt]; !exists {
			b.stats.Migrations++
			continue
		}
		newSticky[key] = tgt
		newOrder = append(newOrder, key)
	}

	b.loaded = true
	b.entries = newEntries
	b.byTarget = newByTarget
	b.groups = newGroups
	b.expiresAt = b.now.Add(minTTL)
	b.stale = false
	b.sticky = newSticky
	b.stickyOrder = newOrder
	b.stats.Updates++
	b.trimStickyLocked()

	return nil
}

func buildGroups(entries []*entry) []*priorityGroup {
	groups := make([]*priorityGroup, 0)
	for _, e := range entries {
		if len(groups) == 0 || groups[len(groups)-1].priority != e.priority {
			groups = append(groups, &priorityGroup{priority: e.priority})
		}
		g := groups[len(groups)-1]
		g.entries = append(g.entries, e)
	}
	return groups
}

func sameGroup(a, b *priorityGroup) bool {
	if a == nil || len(a.entries) != len(b.entries) {
		return false
	}
	for i := range a.entries {
		if a.entries[i].target != b.entries[i].target ||
			a.entries[i].weight != b.entries[i].weight {
			return false
		}
	}
	return true
}

// Pick selects one backend for the caller key.
func (b *SRVBalancer) Pick(key string) (Target, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.loaded || len(b.entries) == 0 {
		return Target{}, ErrNoRecords
	}

	if key != "" {
		if tgt, ok := b.sticky[key]; ok {
			if b.stickySelectableLocked(tgt) {
				b.touchStickyLocked(key)
				b.stats.Picks++
				return tgt, nil
			}
		}
	}

	e, err := b.selectLocked()
	if err != nil {
		return Target{}, err
	}
	if key != "" {
		b.rememberStickyLocked(key, e.target)
	}
	b.stats.Picks++
	return e.target, nil
}

func (b *SRVBalancer) stickySelectableLocked(tgt Target) bool {
	e, ok := b.byTarget[tgt]
	if !ok || len(b.groups) == 0 {
		return false
	}
	if e.priority != b.groups[0].priority {
		return false
	}
	return !e.suppressed
}

func (b *SRVBalancer) selectLocked() (*entry, error) {
	group := b.groups[0]

	// A cooled down target gets one half-open probe before normal traffic.
	for _, e := range group.entries {
		if e.suppressed && !e.probing && !b.now.Before(e.cooldownUntil) {
			e.probing = true
			b.stats.Probes++
			return e, nil
		}
	}

	positive := make([]*entry, 0, len(group.entries))
	for _, e := range group.entries {
		if e.weight > 0 && !e.suppressed {
			positive = append(positive, e)
		}
	}
	if len(positive) > 0 {
		return pickSmooth(positive), nil
	}

	zero := make([]*entry, 0, len(group.entries))
	for _, e := range group.entries {
		if e.weight == 0 {
			zero = append(zero, e)
		}
	}
	if len(zero) > 0 {
		for i := 0; i < len(zero); i++ {
			idx := (group.zeroCursor + i) % len(zero)
			e := zero[idx]
			if e.suppressed {
				continue
			}
			group.zeroCursor = (idx + 1) % len(zero)
			b.stats.WeightZeroPicks++
			return e, nil
		}
	}

	return nil, ErrNoHealthyBackend
}

func pickSmooth(entries []*entry) *entry {
	var total int64
	for _, e := range entries {
		total += int64(e.weight)
	}

	var best *entry
	for _, e := range entries {
		e.current += int64(e.weight)
		if best == nil || e.current > best.current {
			best = e
		}
	}
	best.current -= total
	return best
}

func (b *SRVBalancer) touchStickyLocked(key string) {
	for i, k := range b.stickyOrder {
		if k != key {
			continue
		}
		copy(b.stickyOrder[i:], b.stickyOrder[i+1:])
		b.stickyOrder[len(b.stickyOrder)-1] = key
		return
	}
	b.stickyOrder = append(b.stickyOrder, key)
}

func (b *SRVBalancer) rememberStickyLocked(key string, tgt Target) {
	b.sticky[key] = tgt
	b.touchStickyLocked(key)
	b.trimStickyLocked()
}

func (b *SRVBalancer) trimStickyLocked() {
	for len(b.stickyOrder) > b.policy.MaxKeys {
		oldest := b.stickyOrder[0]
		b.stickyOrder = b.stickyOrder[1:]
		delete(b.sticky, oldest)
	}
}

// ReportHealth records the outcome of one request against a target.
func (b *SRVBalancer) ReportHealth(target Target, ok bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, exists := b.byTarget[target]
	if !exists {
		return ErrUnknownTarget
	}

	if ok {
		e.failCount = 0
		e.suppressed = false
		e.probing = false
		e.cooldownUntil = time.Time{}
		return nil
	}

	if e.probing {
		e.probing = false
		e.suppressed = true
		e.cooldownUntil = b.now.Add(b.policy.Cooldown)
		return nil
	}
	if e.suppressed {
		return nil
	}

	e.failCount++
	if e.failCount >= b.policy.FailThreshold {
		e.suppressed = true
		e.cooldownUntil = b.now.Add(b.policy.Cooldown)
		b.stats.Suppressed++
	}
	return nil
}

// Expire advances the balancer clock and marks the record set stale when its
// TTL has passed.
func (b *SRVBalancer) Expire(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.now = now
	if b.loaded && !b.stale && !now.Before(b.expiresAt) {
		b.stale = true
	}
}

// SetPolicy replaces the health policy.
func (b *SRVBalancer) SetPolicy(p Policy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.policy = p
	for _, e := range b.entries {
		if !e.suppressed && e.failCount >= p.FailThreshold {
			e.suppressed = true
			e.cooldownUntil = b.now.Add(p.Cooldown)
			b.stats.Suppressed++
		}
	}
	b.trimStickyLocked()
	return nil
}

// Targets lists the loaded targets ordered by priority, host and port.
func (b *SRVBalancer) Targets() []Target {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.entries) == 0 {
		return nil
	}
	out := make([]Target, len(b.entries))
	for i, e := range b.entries {
		out[i] = e.target
	}
	return out
}

// Healthy reports whether the target is currently selectable.
func (b *SRVBalancer) Healthy(target Target) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.byTarget[target]
	return ok && !e.suppressed
}

// Stats returns a copy of the balancer counters.
func (b *SRVBalancer) Stats() BalancerStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := b.stats
	s.Records = len(b.entries)
	s.Stale = b.stale
	s.StickyKeys = len(b.sticky)
	return s
}

type snapshotEntry struct {
	Host          string        `json:"host"`
	Port          uint16        `json:"port"`
	Priority      uint16        `json:"priority"`
	Weight        uint16        `json:"weight"`
	TTL           time.Duration `json:"ttl"`
	FailCount     int           `json:"fail_count"`
	Suppressed    bool          `json:"suppressed"`
	CooldownUntil time.Time     `json:"cooldown_until"`
	Probing       bool          `json:"probing"`
	Current       int64         `json:"current"`
}

type snapshotGroup struct {
	Priority   uint16 `json:"priority"`
	ZeroCursor int    `json:"zero_cursor"`
}

type snapshotSticky struct {
	Key    string `json:"key"`
	Target Target `json:"target"`
}

type snapshotState struct {
	Version   int              `json:"version"`
	Policy    Policy           `json:"policy"`
	Now       time.Time        `json:"now"`
	Loaded    bool             `json:"loaded"`
	ExpiresAt time.Time        `json:"expires_at"`
	Stale     bool             `json:"stale"`
	Stats     BalancerStats    `json:"stats"`
	Entries   []snapshotEntry  `json:"entries"`
	Groups    []snapshotGroup  `json:"groups"`
	Sticky    []snapshotSticky `json:"sticky"`
}

// Snapshot serialises records, rotation state, health state and sticky keys.
func (b *SRVBalancer) Snapshot() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := snapshotState{
		Version:   snapshotVersion,
		Policy:    b.policy,
		Now:       b.now,
		Loaded:    b.loaded,
		ExpiresAt: b.expiresAt,
		Stale:     b.stale,
		Stats:     b.stats,
	}
	s.Stats.Records = len(b.entries)
	s.Stats.Stale = b.stale
	s.Stats.StickyKeys = len(b.sticky)

	s.Entries = make([]snapshotEntry, 0, len(b.entries))
	for _, e := range b.entries {
		s.Entries = append(s.Entries, snapshotEntry{
			Host:          e.target.Host,
			Port:          e.target.Port,
			Priority:      e.priority,
			Weight:        e.weight,
			TTL:           e.ttl,
			FailCount:     e.failCount,
			Suppressed:    e.suppressed,
			CooldownUntil: e.cooldownUntil,
			Probing:       e.probing,
			Current:       e.current,
		})
	}

	s.Groups = make([]snapshotGroup, 0, len(b.groups))
	for _, g := range b.groups {
		s.Groups = append(s.Groups, snapshotGroup{
			Priority:   g.priority,
			ZeroCursor: g.zeroCursor,
		})
	}

	s.Sticky = make([]snapshotSticky, 0, len(b.stickyOrder))
	for _, key := range b.stickyOrder {
		tgt, ok := b.sticky[key]
		if !ok {
			continue
		}
		s.Sticky = append(s.Sticky, snapshotSticky{Key: key, Target: tgt})
	}

	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces the balancer state with a snapshot.
func (b *SRVBalancer) Restore(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var s snapshotState
	if err := json.Unmarshal(data, &s); err != nil {
		return ErrBadSnapshot
	}
	if s.Version != snapshotVersion || validatePolicy(s.Policy) != nil {
		return ErrBadSnapshot
	}
	if !s.Loaded && len(s.Entries) != 0 {
		return ErrBadSnapshot
	}
	if s.Loaded && len(s.Entries) == 0 {
		return ErrBadSnapshot
	}

	entries := make([]*entry, 0, len(s.Entries))
	byTarget := make(map[Target]*entry, len(s.Entries))
	minTTL := time.Duration(0)
	for i, se := range s.Entries {
		rec := SRVRecord{
			Host:     se.Host,
			Port:     se.Port,
			Priority: se.Priority,
			Weight:   se.Weight,
			TTL:      se.TTL,
		}
		if err := validateRecord(rec); err != nil {
			return ErrBadSnapshot
		}
		tgt := Target{Host: se.Host, Port: se.Port}
		if _, ok := byTarget[tgt]; ok {
			return ErrBadSnapshot
		}
		if se.FailCount < 0 {
			return ErrBadSnapshot
		}
		e := &entry{
			target:        tgt,
			priority:      se.Priority,
			weight:        se.Weight,
			ttl:           se.TTL,
			failCount:     se.FailCount,
			suppressed:    se.Suppressed,
			cooldownUntil: se.CooldownUntil,
			probing:       se.Probing,
			current:       se.Current,
		}
		entries = append(entries, e)
		byTarget[tgt] = e
		if i == 0 || se.TTL < minTTL {
			minTTL = se.TTL
		}
	}
	if s.Loaded && minTTL <= 0 {
		return ErrBadSnapshot
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].priority != entries[j].priority {
			return entries[i].priority < entries[j].priority
		}
		if entries[i].target.Host != entries[j].target.Host {
			return entries[i].target.Host < entries[j].target.Host
		}
		return entries[i].target.Port < entries[j].target.Port
	})

	groups := buildGroups(entries)
	cursors := make(map[uint16]int, len(s.Groups))
	for _, sg := range s.Groups {
		if sg.ZeroCursor < 0 {
			return ErrBadSnapshot
		}
		if _, ok := cursors[sg.Priority]; ok {
			return ErrBadSnapshot
		}
		cursors[sg.Priority] = sg.ZeroCursor
	}
	knownPriority := make(map[uint16]struct{}, len(groups))
	for _, g := range groups {
		knownPriority[g.priority] = struct{}{}
		if cursor, ok := cursors[g.priority]; ok {
			g.zeroCursor = cursor
		}
	}
	for priority := range cursors {
		if _, ok := knownPriority[priority]; !ok {
			return ErrBadSnapshot
		}
	}

	sticky := make(map[string]Target, len(s.Sticky))
	order := make([]string, 0, len(s.Sticky))
	for _, ss := range s.Sticky {
		if ss.Key == "" {
			return ErrBadSnapshot
		}
		if _, ok := sticky[ss.Key]; ok {
			return ErrBadSnapshot
		}
		if _, ok := byTarget[ss.Target]; !ok {
			return ErrBadSnapshot
		}
		sticky[ss.Key] = ss.Target
		order = append(order, ss.Key)
	}

	expiresAt := s.ExpiresAt
	if s.Loaded && expiresAt.IsZero() {
		expiresAt = s.Now.Add(minTTL)
	}

	b.policy = s.Policy
	b.now = s.Now
	b.loaded = s.Loaded
	b.entries = entries
	b.byTarget = byTarget
	b.groups = groups
	b.expiresAt = expiresAt
	b.stale = s.Stale
	b.sticky = sticky
	b.stickyOrder = order
	b.stats = s.Stats
	b.trimStickyLocked()

	return nil
}
