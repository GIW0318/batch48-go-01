// Package task stacks named rlimit profiles onto processes and recomputes the
// ceilings a child keeps across exec.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

// Resource numbers used by the guard, following the Linux x86-64 order.
const (
	ResourceCPU    = 0
	ResourceStack  = 3
	ResourceCore   = 4
	ResourceNProc  = 6
	ResourceNoFile = 7
	ResourceAS     = 9
)

// Errors returned by the guard.
var (
	ErrBlankName        = errors.New("profile name is required")
	ErrDuplicateProfile = errors.New("profile already defined")
	ErrProfileLimit     = errors.New("profile limit reached")
	ErrStackLimit       = errors.New("profile stack limit reached")
	ErrSoftAboveHard    = errors.New("soft limit above hard limit")
	ErrHardRaise        = errors.New("hard limit cannot be raised")
	ErrProfileNotFound  = errors.New("unknown rlimit profile")
	ErrEmptyStack       = errors.New("at least one profile name is required")
	ErrUnknownPID       = errors.New("process has no applied profile")
	ErrBadSnapshot      = errors.New("snapshot payload is invalid")
)

// Limits holds the soft and hard ceilings per resource number. A resource that
// is absent from a map keeps the value the process already carries.
type Limits struct {
	Soft    map[int]uint64 `json:"soft,omitempty"`
	Hard    map[int]uint64 `json:"hard,omitempty"`
	OpenFDs uint64         `json:"open_fds,omitempty"`
}

// Options bounds the profile table and the stack depth.
type Options struct {
	MaxProfiles int `json:"max_profiles"`
	MaxStack    int `json:"max_stack"`
}

// Applied is the state a process carries after one or more profiles landed.
type Applied struct {
	PID    int      `json:"pid"`
	Names  []string `json:"names"`
	Limits Limits   `json:"limits"`
}

// Request is what a child asks for right before exec.
type Request struct {
	Names     []string       `json:"names"`
	RaiseSoft map[int]uint64 `json:"raise_soft,omitempty"`
	RaiseHard map[int]uint64 `json:"raise_hard,omitempty"`
}

// RlimitProfileGuard owns the profile table and the per-process state.
type RlimitProfileGuard struct {
	mu       sync.RWMutex
	opts     Options
	profiles []profileRecord
	process  map[int]Applied
}

// profileRecord keeps a named profile next to its definition order.
type profileRecord struct {
	Name   string `json:"name"`
	Limits Limits `json:"limits"`
}

// snapshotPayload is the on-disk shape of Snapshot/Restore.
type snapshotPayload struct {
	Options   Options         `json:"options"`
	Profiles  []profileRecord `json:"profiles"`
	Processes []Applied       `json:"processes"`
}

// NewRlimitProfileGuard builds a guard with the given table bounds.
func NewRlimitProfileGuard(opts Options) *RlimitProfileGuard {
	return &RlimitProfileGuard{
		opts:     opts,
		profiles: make([]profileRecord, 0),
		process:  make(map[int]Applied),
	}
}

// Define registers a named profile.
func (g *RlimitProfileGuard) Define(name string, limits Limits) error {
	if strings.TrimSpace(name) == "" {
		return ErrBlankName
	}
	if err := validateSoftHard(limits); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, record := range g.profiles {
		if record.Name == name {
			return ErrDuplicateProfile
		}
	}
	if g.opts.MaxProfiles > 0 && len(g.profiles) >= g.opts.MaxProfiles {
		return ErrProfileLimit
	}

	record := profileRecord{Name: name, Limits: cloneLimits(limits)}
	g.profiles = append(g.profiles, record)
	return nil
}

// Apply stacks the named profiles onto a process and returns the new state.
func (g *RlimitProfileGuard) Apply(pid int, names []string) (Applied, error) {
	if len(names) == 0 {
		return Applied{}, ErrEmptyStack
	}
	if g.opts.MaxStack > 0 && len(names) > g.opts.MaxStack {
		return Applied{}, ErrStackLimit
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	stacked := make([]Limits, 0, len(names))
	for _, name := range names {
		record, ok := g.lookupProfileLocked(name)
		if !ok {
			return Applied{}, ErrProfileNotFound
		}
		stacked = append(stacked, record.Limits)
	}

	current := g.process[pid]
	next := Limits{
		Soft:    cloneU64Map(current.Limits.Soft),
		Hard:    cloneU64Map(current.Limits.Hard),
		OpenFDs: current.Limits.OpenFDs,
	}

	// First fold the whole stack into one intersection. Profiles within a
	// single Apply never raise each other; only the final result is checked
	// against the ceilings the process already carries.
	stackSoft := map[int]uint64{}
	stackHard := map[int]uint64{}
	for _, profile := range stacked {
		for resource, requested := range profile.Hard {
			active, exists := stackHard[resource]
			stackHard[resource] = minU64(active, requested, !exists)
		}
		for resource, requested := range profile.Soft {
			active, exists := stackSoft[resource]
			stackSoft[resource] = minU64(active, requested, !exists)
		}
	}

	for resource, requested := range stackHard {
		if active, exists := next.Hard[resource]; exists && requested > active {
			return Applied{}, ErrHardRaise
		}
	}

	for resource, requested := range stackHard {
		active, exists := next.Hard[resource]
		next.Hard[resource] = minU64(active, requested, !exists)
	}
	for resource, requested := range stackSoft {
		active, exists := next.Soft[resource]
		next.Soft[resource] = minU64(active, requested, !exists)
	}

	if err := validateSoftHard(next); err != nil {
		return Applied{}, err
	}

	applied := Applied{
		PID:    pid,
		Names:  cloneNames(names),
		Limits: next,
	}
	g.process[pid] = applied
	return cloneApplied(applied), nil
}

// Inherit recomputes the limits a child keeps across exec.
func (g *RlimitProfileGuard) Inherit(parent Applied, child Request) (Applied, error) {
	if len(child.Names) == 0 {
		return Applied{}, ErrEmptyStack
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.opts.MaxStack > 0 && len(child.Names) > g.opts.MaxStack {
		return Applied{}, ErrStackLimit
	}

	stacked := make([]Limits, 0, len(child.Names))
	for _, name := range child.Names {
		record, ok := g.lookupProfileLocked(name)
		if !ok {
			return Applied{}, ErrProfileNotFound
		}
		stacked = append(stacked, record.Limits)
	}

	next := Limits{
		Soft:    cloneU64Map(parent.Limits.Soft),
		Hard:    cloneU64Map(parent.Limits.Hard),
		OpenFDs: parent.Limits.OpenFDs,
	}

	for _, profile := range stacked {
		for resource, requested := range profile.Hard {
			active, exists := next.Hard[resource]
			next.Hard[resource] = minU64(active, requested, !exists)
		}
		for resource, requested := range profile.Soft {
			active, exists := next.Soft[resource]
			next.Soft[resource] = minU64(active, requested, !exists)
		}
	}

	for resource, requestedSoft := range child.RaiseSoft {
		if hard, ok := next.Hard[resource]; ok && requestedSoft > hard {
			return Applied{}, ErrSoftAboveHard
		}
		next.Soft[resource] = requestedSoft
	}
	for resource, requestedHard := range child.RaiseHard {
		if parentHard, ok := parent.Limits.Hard[resource]; ok && requestedHard > parentHard {
			return Applied{}, ErrHardRaise
		}
		next.Hard[resource] = requestedHard
	}

	if err := validateSoftHard(next); err != nil {
		return Applied{}, err
	}

	return Applied{
		PID:    parent.PID,
		Names:  cloneNames(child.Names),
		Limits: next,
	}, nil
}

// Effective reports the limits currently in force for a process.
func (g *RlimitProfileGuard) Effective(pid int) (Limits, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	applied, ok := g.process[pid]
	if !ok {
		return Limits{}, ErrUnknownPID
	}
	return cloneLimits(applied.Limits), nil
}

// Reset drops the guard state for a process.
func (g *RlimitProfileGuard) Reset(pid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.process[pid]; !ok {
		return ErrUnknownPID
	}
	delete(g.process, pid)
	return nil
}

// Snapshot serialises the profile table and every per-process state.
func (g *RlimitProfileGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	payload := snapshotPayload{
		Options:   g.opts,
		Profiles:  make([]profileRecord, len(g.profiles)),
		Processes: make([]Applied, 0, len(g.process)),
	}
	for i, record := range g.profiles {
		payload.Profiles[i] = profileRecord{
			Name:   record.Name,
			Limits: cloneLimits(record.Limits),
		}
	}
	for _, applied := range g.process {
		payload.Processes = append(payload.Processes, cloneApplied(applied))
	}
	return json.Marshal(payload)
}

// Restore replaces the guard state from a snapshot.
func (g *RlimitProfileGuard) Restore(payload []byte) error {
	if len(bytes.TrimSpace(payload)) == 0 {
		return ErrBadSnapshot
	}

	var decoded snapshotPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ErrBadSnapshot
	}

	seen := make(map[string]struct{}, len(decoded.Profiles))
	for _, record := range decoded.Profiles {
		if strings.TrimSpace(record.Name) == "" {
			return ErrBadSnapshot
		}
		if _, dup := seen[record.Name]; dup {
			return ErrBadSnapshot
		}
		seen[record.Name] = struct{}{}
		if err := validateSoftHard(record.Limits); err != nil {
			return ErrBadSnapshot
		}
	}
	for _, applied := range decoded.Processes {
		if err := validateSoftHard(applied.Limits); err != nil {
			return ErrBadSnapshot
		}
	}

	profiles := make([]profileRecord, len(decoded.Profiles))
	for i, record := range decoded.Profiles {
		profiles[i] = profileRecord{
			Name:   record.Name,
			Limits: cloneLimits(record.Limits),
		}
	}
	processes := make(map[int]Applied, len(decoded.Processes))
	for _, applied := range decoded.Processes {
		processes[applied.PID] = cloneApplied(applied)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if decoded.Options != (Options{}) {
		g.opts = decoded.Options
	}
	g.profiles = profiles
	g.process = processes
	return nil
}

// Profile returns one stored profile.
func (g *RlimitProfileGuard) Profile(name string) (Limits, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	record, ok := g.lookupProfileLocked(name)
	if !ok {
		return Limits{}, false
	}
	return cloneLimits(record.Limits), true
}

// Profiles lists defined profile names in definition order.
func (g *RlimitProfileGuard) Profiles() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, len(g.profiles))
	for i, record := range g.profiles {
		names[i] = record.Name
	}
	return names
}

// TrackOpenFDs records how many descriptors a process actually holds.
func (g *RlimitProfileGuard) TrackOpenFDs(pid int, open uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	applied, ok := g.process[pid]
	if !ok {
		return ErrUnknownPID
	}
	applied.Limits.OpenFDs = open
	g.process[pid] = applied
	return nil
}

// lookupProfileLocked returns a profile by name; the caller must hold a lock.
func (g *RlimitProfileGuard) lookupProfileLocked(name string) (profileRecord, bool) {
	for _, record := range g.profiles {
		if record.Name == name {
			return record, true
		}
	}
	return profileRecord{}, false
}

// validateSoftHard rejects any resource whose soft ceiling exceeds its hard.
func validateSoftHard(limits Limits) error {
	for resource, soft := range limits.Soft {
		if hard, ok := limits.Hard[resource]; ok && soft > hard {
			return ErrSoftAboveHard
		}
	}
	return nil
}

// minU64 folds a profile value into an active ceiling. When the process does
// not carry a value yet, the profile value becomes the new ceiling instead of
// being intersected with zero.
func minU64(active, requested uint64, activeMissing bool) uint64 {
	if activeMissing {
		return requested
	}
	if requested < active {
		return requested
	}
	return active
}

func cloneU64Map(in map[int]uint64) map[int]uint64 {
	if in == nil {
		return map[int]uint64{}
	}
	out := make(map[int]uint64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneLimits(in Limits) Limits {
	return Limits{
		Soft:    cloneU64Map(in.Soft),
		Hard:    cloneU64Map(in.Hard),
		OpenFDs: in.OpenFDs,
	}
}

func cloneApplied(in Applied) Applied {
	return Applied{
		PID:    in.PID,
		Names:  cloneNames(in.Names),
		Limits: cloneLimits(in.Limits),
	}
}
