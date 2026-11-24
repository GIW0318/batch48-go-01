// Package task drives a root started service down a fixed privilege ladder and
// keeps the observed credentials of every completed stage.
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Errors returned by the guard.
var (
	ErrPlanInvalid      = errors.New("privilege stage guard: plan invalid")
	ErrUnknownStage     = errors.New("privilege stage guard: unknown stage")
	ErrOutOfOrder       = errors.New("privilege stage guard: stage out of order")
	ErrReadbackMismatch = errors.New("privilege stage guard: readback mismatch")
	ErrStagePending     = errors.New("privilege stage guard: stage pending")
	ErrBadSnapshot      = errors.New("privilege stage guard: bad snapshot")
)

// StageKind identifies what a single descent step does.
type StageKind int

// Supported stage kinds, in the only order a ladder may declare them.
const (
	StageGroups StageKind = iota
	StageDropGID
	StageDropUID
	StageNoNewPrivs
)

// Credentials is the observed credential set of the service process.
type Credentials struct {
	RealUID    int
	RealGID    int
	EUID       int
	EGID       int
	Groups     []int
	NoNewPrivs bool
	CapMask    uint64
}

// StageSpec declares one step of the descent ladder.
type StageSpec struct {
	Name         string
	Kind         StageKind
	Value        int
	Groups       []int
	DependsOn    []string
	Irreversible bool
}

// Plan is the whole ladder plus the readback hook used after every step.
type Plan struct {
	Stages []StageSpec
	Probe  func(stage string) Credentials
}

// StageResult is returned for one stage attempt.
type StageResult struct {
	Stage       string
	Applied     bool
	Credentials Credentials
	Readback    Credentials
}

// JournalEntry records one applied stage in completion order.
type JournalEntry struct {
	Sequence    int
	Stage       string
	Credentials Credentials
	Readback    Credentials
}

type stageReadback struct {
	Attempted   bool        `json:"attempted"`
	Credentials Credentials `json:"credentials"`
}

const snapshotVersion = 1

type snapshotState struct {
	Version      int             `json:"version"`
	PlanID       string          `json:"plan_id"`
	Current      Credentials     `json:"current"`
	Completed    []bool          `json:"completed"`
	Readbacks    []stageReadback `json:"readbacks"`
	Journal      []JournalEntry  `json:"journal"`
	NextSequence int             `json:"next_sequence"`
}

// PrivilegeStageGuard serialises a single descent ladder.
type PrivilegeStageGuard struct {
	mu sync.RWMutex

	stages       []StageSpec
	index        map[string]int
	probe        func(string) Credentials
	planID       string
	current      Credentials
	completed    []bool
	readbacks    []stageReadback
	journal      []JournalEntry
	nextSequence int
}

// NewPrivilegeStageGuard validates the ladder and returns a fresh guard.
func NewPrivilegeStageGuard(plan Plan) (*PrivilegeStageGuard, error) {
	if len(plan.Stages) != 4 {
		return nil, ErrPlanInvalid
	}

	stages := make([]StageSpec, len(plan.Stages))
	index := make(map[string]int, len(plan.Stages))
	for i, stage := range plan.Stages {
		if stage.Kind != StageKind(i) {
			return nil, ErrPlanInvalid
		}
		if stage.Name == "" {
			return nil, ErrPlanInvalid
		}
		if _, exists := index[stage.Name]; exists {
			return nil, ErrPlanInvalid
		}
		stages[i] = cloneStageSpec(stage)
		index[stage.Name] = i
	}

	for i, stage := range stages {
		if stage.Irreversible != (i != 0) {
			return nil, ErrPlanInvalid
		}
		for _, dependency := range stage.DependsOn {
			j, exists := index[dependency]
			if !exists || j >= i {
				return nil, ErrPlanInvalid
			}
		}
	}

	return &PrivilegeStageGuard{
		stages:       stages,
		index:        index,
		probe:        plan.Probe,
		planID:       planSignature(stages),
		current:      initialCredentials(),
		completed:    make([]bool, len(stages)),
		readbacks:    make([]stageReadback, len(stages)),
		nextSequence: 1,
	}, nil
}

// Run advances exactly one stage and reads the real credentials back.
func (g *PrivilegeStageGuard) Run(stage string) (StageResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	idx, ok := g.index[stage]
	if !ok {
		return StageResult{}, ErrUnknownStage
	}

	if g.completed[idx] {
		for later := idx + 1; later < len(g.completed); later++ {
			if g.completed[later] {
				return StageResult{
					Stage:       stage,
					Applied:     false,
					Credentials: cloneCredentials(g.current),
					Readback:    cloneCredentials(g.readbacks[idx].Credentials),
				}, ErrOutOfOrder
			}
		}
		return StageResult{
			Stage:       stage,
			Applied:     false,
			Credentials: cloneCredentials(g.current),
			Readback:    cloneCredentials(g.readbacks[idx].Credentials),
		}, nil
	}

	for earlier := 0; earlier < idx; earlier++ {
		if !g.completed[earlier] {
			return StageResult{
				Stage:       stage,
				Applied:     false,
				Credentials: cloneCredentials(g.current),
			}, ErrOutOfOrder
		}
	}

	spec := g.stages[idx]
	applied := applyStage(g.current, spec)
	observed := applied
	if g.probe != nil {
		observed = cloneCredentials(g.probe(stage))
	}

	g.readbacks[idx] = stageReadback{
		Attempted:   true,
		Credentials: cloneCredentials(observed),
	}

	differences := stageDifferences(spec, observed)
	if len(differences) != 0 {
		return StageResult{
			Stage:       stage,
			Applied:     false,
			Credentials: cloneCredentials(g.current),
			Readback:    cloneCredentials(observed),
		}, fmt.Errorf("%w: %s: %s", ErrReadbackMismatch, stage, strings.Join(differences, "; "))
	}

	g.current = cloneCredentials(observed)
	g.completed[idx] = true
	entry := JournalEntry{
		Sequence:    g.nextSequence,
		Stage:       stage,
		Credentials: cloneCredentials(applied),
		Readback:    cloneCredentials(observed),
	}
	g.nextSequence++
	g.journal = append(g.journal, entry)

	return StageResult{
		Stage:       stage,
		Applied:     true,
		Credentials: cloneCredentials(applied),
		Readback:    cloneCredentials(observed),
	}, nil
}

// Current returns the credentials the service is modelled to hold now.
func (g *PrivilegeStageGuard) Current() Credentials {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return cloneCredentials(g.current)
}

// Verify checks the named stage against the plan and the current readback.
func (g *PrivilegeStageGuard) Verify(stage string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	idx, ok := g.index[stage]
	if !ok {
		return ErrUnknownStage
	}

	if !g.completed[idx] {
		if !g.readbacks[idx].Attempted {
			return ErrStagePending
		}
		differences := stageDifferences(g.stages[idx], g.readbacks[idx].Credentials)
		if len(differences) == 0 {
			return ErrStagePending
		}
		return fmt.Errorf("%w: %s: %s", ErrStagePending, stage, strings.Join(differences, "; "))
	}

	differences := stageDifferences(g.stages[idx], g.readbacks[idx].Credentials)
	if len(differences) != 0 {
		return fmt.Errorf("%w: %s: %s", ErrReadbackMismatch, stage, strings.Join(differences, "; "))
	}
	return nil
}

// Completed lists the completed stage names in plan order.
func (g *PrivilegeStageGuard) Completed() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	completed := make([]string, 0, len(g.stages))
	for i, stage := range g.stages {
		if g.completed[i] {
			completed = append(completed, stage.Name)
		}
	}
	return completed
}

// Pending lists the not yet completed stage names in plan order.
func (g *PrivilegeStageGuard) Pending() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	pending := make([]string, 0, len(g.stages))
	for i, stage := range g.stages {
		if !g.completed[i] {
			pending = append(pending, stage.Name)
		}
	}
	return pending
}

// Journal returns the audit trail in completion order.
func (g *PrivilegeStageGuard) Journal() []JournalEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return cloneJournal(g.journal)
}

// StageNames returns every declared stage name in plan order.
func (g *PrivilegeStageGuard) StageNames() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, len(g.stages))
	for i, stage := range g.stages {
		names[i] = stage.Name
	}
	return names
}

// Stage looks up one declared stage.
func (g *PrivilegeStageGuard) Stage(name string) (StageSpec, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	idx, ok := g.index[name]
	if !ok {
		return StageSpec{}, false
	}
	return cloneStageSpec(g.stages[idx]), true
}

// Snapshot serialises the completion bits and the readback results.
func (g *PrivilegeStageGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	state := snapshotState{
		Version:      snapshotVersion,
		PlanID:       g.planID,
		Current:      cloneCredentials(g.current),
		Completed:    append([]bool(nil), g.completed...),
		Readbacks:    cloneReadbacks(g.readbacks),
		Journal:      cloneJournal(g.journal),
		NextSequence: g.nextSequence,
	}
	return json.Marshal(state)
}

// Restore loads a snapshot produced by Snapshot.
func (g *PrivilegeStageGuard) Restore(b []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var state snapshotState
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return ErrBadSnapshot
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrBadSnapshot
	}
	if !g.validSnapshot(state) {
		return ErrBadSnapshot
	}

	g.current = cloneCredentials(state.Current)
	g.completed = append([]bool(nil), state.Completed...)
	g.readbacks = cloneReadbacks(state.Readbacks)
	g.journal = cloneJournal(state.Journal)
	g.nextSequence = state.NextSequence
	return nil
}

func (g *PrivilegeStageGuard) validSnapshot(state snapshotState) bool {
	if state.Version != snapshotVersion || state.PlanID != g.planID {
		return false
	}
	if len(state.Completed) != len(g.stages) || len(state.Readbacks) != len(g.stages) {
		return false
	}

	completedCount := 0
	pendingSeen := false
	for i, done := range state.Completed {
		if done {
			if pendingSeen || !state.Readbacks[i].Attempted {
				return false
			}
			if len(stageDifferences(g.stages[i], state.Readbacks[i].Credentials)) != 0 {
				return false
			}
			completedCount++
			continue
		}
		pendingSeen = true
	}

	if len(state.Journal) != completedCount {
		return false
	}
	expectedCurrent := initialCredentials()
	if completedCount != 0 {
		expectedCurrent = state.Readbacks[completedCount-1].Credentials
	}
	if !sameCredentials(state.Current, expectedCurrent) {
		return false
	}
	lastSequence := 0
	for i, entry := range state.Journal {
		if entry.Stage != g.stages[i].Name || entry.Sequence <= lastSequence {
			return false
		}
		if len(stageDifferences(g.stages[i], entry.Credentials)) != 0 ||
			len(stageDifferences(g.stages[i], entry.Readback)) != 0 {
			return false
		}
		lastSequence = entry.Sequence
	}
	if state.NextSequence <= lastSequence || state.NextSequence < 1 {
		return false
	}
	return true
}

func initialCredentials() Credentials {
	return Credentials{CapMask: ^uint64(0)}
}

func applyStage(current Credentials, stage StageSpec) Credentials {
	next := cloneCredentials(current)
	switch stage.Kind {
	case StageGroups:
		next.Groups = normalizeGroups(stage.Groups)
	case StageDropGID:
		next.EGID = stage.Value
	case StageDropUID:
		next.EUID = stage.Value
		next.CapMask = 0
	case StageNoNewPrivs:
		next.NoNewPrivs = true
	}
	return next
}

func stageDifferences(stage StageSpec, observed Credentials) []string {
	var differences []string
	switch stage.Kind {
	case StageGroups:
		expected := normalizeGroups(stage.Groups)
		if !sameGroups(expected, observed.Groups) {
			differences = append(differences, fmt.Sprintf("groups: want %v, got %v", expected, observed.Groups))
		}
	case StageDropGID:
		if observed.EGID != stage.Value {
			differences = append(differences, fmt.Sprintf("egid: want %d, got %d", stage.Value, observed.EGID))
		}
	case StageDropUID:
		if observed.EUID != stage.Value {
			differences = append(differences, fmt.Sprintf("euid: want %d, got %d", stage.Value, observed.EUID))
		}
		if observed.CapMask != 0 {
			differences = append(differences, fmt.Sprintf("capmask: want 0, got %#x", observed.CapMask))
		}
	case StageNoNewPrivs:
		if !observed.NoNewPrivs {
			differences = append(differences, "nonewprivs: want true, got false")
		}
	}
	return differences
}

func normalizeGroups(groups []int) []int {
	if len(groups) == 0 {
		return nil
	}
	normalized := append([]int(nil), groups...)
	sort.Ints(normalized)
	out := normalized[:0]
	for _, group := range normalized {
		if len(out) == 0 || out[len(out)-1] != group {
			out = append(out, group)
		}
	}
	return out
}

func sameGroups(a, b []int) bool {
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

func sameCredentials(a, b Credentials) bool {
	return a.RealUID == b.RealUID &&
		a.RealGID == b.RealGID &&
		a.EUID == b.EUID &&
		a.EGID == b.EGID &&
		a.NoNewPrivs == b.NoNewPrivs &&
		a.CapMask == b.CapMask &&
		sameGroups(a.Groups, b.Groups)
}

func cloneStageSpec(stage StageSpec) StageSpec {
	stage.Groups = cloneInts(stage.Groups)
	stage.DependsOn = cloneStrings(stage.DependsOn)
	return stage
}

func cloneCredentials(credentials Credentials) Credentials {
	credentials.Groups = cloneInts(credentials.Groups)
	return credentials
}

func cloneReadbacks(readbacks []stageReadback) []stageReadback {
	out := make([]stageReadback, len(readbacks))
	for i, readback := range readbacks {
		out[i] = stageReadback{
			Attempted:   readback.Attempted,
			Credentials: cloneCredentials(readback.Credentials),
		}
	}
	return out
}

func cloneJournal(journal []JournalEntry) []JournalEntry {
	out := make([]JournalEntry, len(journal))
	for i, entry := range journal {
		out[i] = JournalEntry{
			Sequence:    entry.Sequence,
			Stage:       entry.Stage,
			Credentials: cloneCredentials(entry.Credentials),
			Readback:    cloneCredentials(entry.Readback),
		}
	}
	return out
}

func cloneInts(values []int) []int {
	if values == nil {
		return nil
	}
	out := make([]int, len(values))
	copy(out, values)
	return out
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func planSignature(stages []StageSpec) string {
	type signatureStage struct {
		Name         string
		Kind         StageKind
		Value        int
		Groups       []int
		DependsOn    []string
		Irreversible bool
	}

	signature := make([]signatureStage, len(stages))
	for i, stage := range stages {
		dependencies := cloneStrings(stage.DependsOn)
		if len(dependencies) == 0 {
			dependencies = nil
		} else {
			sort.Strings(dependencies)
		}
		signature[i] = signatureStage{
			Name:         stage.Name,
			Kind:         stage.Kind,
			Value:        stage.Value,
			Groups:       normalizeGroups(stage.Groups),
			DependsOn:    dependencies,
			Irreversible: stage.Irreversible,
		}
	}
	encoded, _ := json.Marshal(signature)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
