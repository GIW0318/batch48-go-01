// Package task moves a suspicious process into quarantine, snapshots what it
// had open and closes the case through release, termination or timeout.
package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Case states.
const (
	StatePending    = "pending"
	StateStopped    = "stopped"
	StateReleased   = "released"
	StateTerminated = "terminated"
)

// Errors reported by the guard.
var (
	ErrBadCase         = errors.New("case request is incomplete")
	ErrCaseNotFound    = errors.New("quarantine case not found")
	ErrStateConflict   = errors.New("case is in the wrong state for this action")
	ErrProcUnavailable = errors.New("process cannot be resolved")
	ErrQuarantineFull  = errors.New("quarantine case limit reached")
	ErrBadPayload      = errors.New("quarantine payload is unreadable")
)

// ProcRef is the kernel identity of a process at quarantine time.
type ProcRef struct {
	Pid       int    `json:"pid"`
	StartTime int64  `json:"start_time"`
	Comm      string `json:"comm,omitempty"`
}

// Evidence is what was captured after the process was stopped.
type Evidence struct {
	CaseID      string   `json:"case_id"`
	Pid         int      `json:"pid"`
	StartTime   int64    `json:"start_time"`
	Files       []string `json:"files"`
	CollectedAt int64    `json:"collected_at"`
	Digest      string   `json:"digest"`
}

// Case is one quarantine record.
type Case struct {
	ID        string   `json:"id"`
	Pid       int      `json:"pid"`
	StartTime int64    `json:"start_time"`
	Reason    string   `json:"reason"`
	Sources   []string `json:"sources"`
	State     string   `json:"state"`
	Tier      int      `json:"tier"`
	OpenedAt  int64    `json:"opened_at"`
	StoppedAt int64    `json:"stopped_at"`
	Deadline  int64    `json:"deadline"`
	ClosedBy  string   `json:"closed_by,omitempty"`
	Evidence  Evidence `json:"evidence"`
}

// Options configures hold time, escalation and inspection hooks.
type Options struct {
	HoldMs           int64                         `json:"hold_ms"`
	GraceMs          int64                         `json:"grace_ms"`
	MaxCases         int                           `json:"max_cases"`
	MaxEvidenceFiles int                           `json:"max_evidence_files"`
	Resolve          func(pid int) (ProcRef, bool) `json:"-"`
	Inspect          func(pid int) []string        `json:"-"`
}

const (
	defaultMaxCases         = 64
	defaultMaxEvidenceFiles = 256
	snapshotVersion         = 1
	systemOperator          = "system"
)

var nowMillis = func() int64 {
	return time.Now().UnixMilli()
}

// QuarantineGuard owns every quarantine case.
type QuarantineGuard struct {
	opts   Options
	mu     sync.Mutex
	seq    int64
	cases  map[string]*Case
	order  []string
	active map[int]string
}

// NewQuarantineGuard builds a guard with the given policy.
func NewQuarantineGuard(opts Options) *QuarantineGuard {
	return &QuarantineGuard{
		opts:   opts,
		cases:  make(map[string]*Case),
		active: make(map[int]string),
	}
}

// Quarantine opens a pending case and records where the request came from.
func (g *QuarantineGuard) Quarantine(pid int, reason, by string) (Case, error) {
	reason = trimSpace(reason)
	by = trimSpace(by)
	if pid <= 0 || reason == "" || by == "" {
		return Case{}, ErrBadCase
	}

	ref, ok := g.resolve(pid)
	if !ok || ref.Pid != pid {
		return Case{}, ErrProcUnavailable
	}

	now := nowMillis()

	g.mu.Lock()
	defer g.mu.Unlock()

	if existingID := g.active[pid]; existingID != "" {
		if existing, exists := g.cases[existingID]; exists && isActiveState(existing.State) {
			if existing.StartTime != ref.StartTime {
				return Case{}, fmt.Errorf("%w: pid %d was reused by another process", ErrProcUnavailable, pid)
			}
			appendSource(existing, by)
			return cloneCase(*existing), nil
		}
		delete(g.active, pid)
	}

	if len(g.active) >= g.caseLimit() {
		return Case{}, ErrQuarantineFull
	}

	g.seq++
	id := fmt.Sprintf("q-%d", g.seq)
	record := &Case{
		ID:        id,
		Pid:       pid,
		StartTime: ref.StartTime,
		Reason:    reason,
		Sources:   []string{by},
		State:     StatePending,
		Tier:      0,
		OpenedAt:  now,
		Deadline:  now + g.opts.HoldMs,
		Evidence: Evidence{
			CaseID:    id,
			Pid:       pid,
			StartTime: ref.StartTime,
			Files:     []string{},
		},
	}

	g.cases[id] = record
	g.order = append(g.order, id)
	g.active[pid] = id

	return cloneCase(*record), nil
}

// Collect stops the process and captures its identity and open files.
func (g *QuarantineGuard) Collect(caseID string) (Evidence, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.cases[caseID]
	if !ok {
		return Evidence{}, ErrCaseNotFound
	}
	if record.State != StatePending {
		return Evidence{}, ErrStateConflict
	}

	ref, ok := g.resolve(record.Pid)
	if !ok || ref.Pid != record.Pid || ref.StartTime != record.StartTime {
		return Evidence{}, ErrProcUnavailable
	}

	var observed []string
	if g.opts.Inspect != nil {
		observed = g.opts.Inspect(record.Pid)
	}
	files := normalizeFiles(observed, g.evidenceLimit())
	collectedAt := nowMillis()
	evidence := Evidence{
		CaseID:      record.ID,
		Pid:         record.Pid,
		StartTime:   record.StartTime,
		Files:       files,
		CollectedAt: collectedAt,
	}
	evidence.Digest = digestEvidence(evidence)

	record.Evidence = evidence
	record.StoppedAt = collectedAt
	record.State = StateStopped

	return cloneEvidence(record.Evidence), nil
}

// Release returns a stopped process to normal operation.
func (g *QuarantineGuard) Release(caseID, by string) error {
	by = trimSpace(by)
	if by == "" {
		return ErrBadCase
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if record.State != StateStopped {
		return ErrStateConflict
	}
	if err := g.verifyIdentityLocked(record); err != nil {
		return err
	}

	record.State = StateReleased
	record.ClosedBy = by
	delete(g.active, record.Pid)
	return nil
}

// Terminate closes a stopped case by killing the process.
func (g *QuarantineGuard) Terminate(caseID, by string) error {
	by = trimSpace(by)
	if by == "" {
		return ErrBadCase
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if record.State != StateStopped {
		return ErrStateConflict
	}

	// A missing or reused kernel identity means the original process is
	// already gone. Converge the case instead of signalling a replacement.
	record.State = StateTerminated
	record.Tier = 2
	record.ClosedBy = by
	delete(g.active, record.Pid)
	return nil
}

// Expire advances timer tiers and returns the cases touched by this call.
func (g *QuarantineGuard) Expire(now int64) ([]Case, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	touched := make([]Case, 0)
	for _, id := range g.order {
		record := g.cases[id]
		if record == nil || !isActiveState(record.State) || now < record.Deadline {
			continue
		}

		switch record.Tier {
		case 0:
			record.Tier = 1
			record.Deadline = now + g.opts.GraceMs
			touched = append(touched, cloneCase(*record))
		case 1:
			record.Tier = 2
			record.State = StateTerminated
			record.ClosedBy = systemOperator
			delete(g.active, record.Pid)
			touched = append(touched, cloneCase(*record))
		}
	}

	return touched, nil
}

// Cases lists cases in one state; an empty state lists every case.
func (g *QuarantineGuard) Cases(state string) []Case {
	g.mu.Lock()
	defer g.mu.Unlock()

	result := make([]Case, 0, len(g.order))
	for _, id := range g.order {
		record := g.cases[id]
		if record == nil {
			continue
		}
		if state == "" || record.State == state {
			result = append(result, cloneCase(*record))
		}
	}
	return result
}

// Snapshot serialises every case.
func (g *QuarantineGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	payload := snapshotData{
		Version: snapshotVersion,
		NextSeq: g.seq,
		Cases:   make([]Case, 0, len(g.order)),
	}
	for _, id := range g.order {
		if record := g.cases[id]; record != nil {
			payload.Cases = append(payload.Cases, cloneCase(*record))
		}
	}
	g.mu.Unlock()

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore replaces every case from a snapshot.
func (g *QuarantineGuard) Restore(payload []byte) error {
	var decoded snapshotData
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if decoded.Version != 0 && decoded.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported snapshot version %d", ErrBadPayload, decoded.Version)
	}

	seenCases := make(map[string]struct{}, len(decoded.Cases))
	seenActivePids := make(map[int]string, len(decoded.Cases))
	maxSeq := decoded.NextSeq

	for i := range decoded.Cases {
		record := decoded.Cases[i]
		if record.ID == "" || record.Pid <= 0 || !knownState(record.State) {
			return fmt.Errorf("%w: invalid case at index %d", ErrBadPayload, i)
		}
		if _, duplicated := seenCases[record.ID]; duplicated {
			return fmt.Errorf("%w: duplicate case %s", ErrBadPayload, record.ID)
		}
		seenCases[record.ID] = struct{}{}

		if isActiveState(record.State) {
			if existingID, duplicated := seenActivePids[record.Pid]; duplicated {
				return fmt.Errorf("%w: pid %d has active cases %s and %s", ErrBadPayload, record.Pid, existingID, record.ID)
			}
			seenActivePids[record.Pid] = record.ID
		}

		if idSeq := caseIDSequence(record.ID); idSeq > maxSeq {
			maxSeq = idSeq
		}
	}

	cases := make(map[string]*Case, len(decoded.Cases))
	order := make([]string, 0, len(decoded.Cases))
	active := make(map[int]string, len(seenActivePids))
	for i := range decoded.Cases {
		record := cloneCase(decoded.Cases[i])
		cases[record.ID] = &record
		order = append(order, record.ID)
		if isActiveState(record.State) {
			active[record.Pid] = record.ID
		}
	}

	g.mu.Lock()
	g.seq = maxSeq
	g.cases = cases
	g.order = order
	g.active = active
	g.mu.Unlock()

	return nil
}

// Lookup returns one case by identifier.
func (g *QuarantineGuard) Lookup(caseID string) (Case, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	record, ok := g.cases[caseID]
	if !ok {
		return Case{}, false
	}
	return cloneCase(*record), true
}

// Active returns the one active case for a pid, if any.
func (g *QuarantineGuard) Active(pid int) (Case, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	caseID := g.active[pid]
	record, ok := g.cases[caseID]
	if !ok || !isActiveState(record.State) {
		delete(g.active, pid)
		return Case{}, false
	}
	return cloneCase(*record), true
}

// Policy returns the configuration the guard was built with.
func (g *QuarantineGuard) Policy() Options {
	return g.opts
}

type snapshotData struct {
	Version int    `json:"version"`
	NextSeq int64  `json:"next_seq"`
	Cases   []Case `json:"cases"`
}

func (g *QuarantineGuard) resolve(pid int) (ProcRef, bool) {
	if g.opts.Resolve == nil {
		return ProcRef{}, false
	}
	return g.opts.Resolve(pid)
}

func (g *QuarantineGuard) verifyIdentityLocked(record *Case) error {
	ref, ok := g.resolve(record.Pid)
	if !ok || ref.Pid != record.Pid || ref.StartTime != record.StartTime {
		return ErrProcUnavailable
	}
	return nil
}

func (g *QuarantineGuard) caseLimit() int {
	if g.opts.MaxCases > 0 {
		return g.opts.MaxCases
	}
	return defaultMaxCases
}

func (g *QuarantineGuard) evidenceLimit() int {
	if g.opts.MaxEvidenceFiles > 0 {
		return g.opts.MaxEvidenceFiles
	}
	return defaultMaxEvidenceFiles
}

func appendSource(record *Case, source string) {
	for _, existing := range record.Sources {
		if existing == source {
			return
		}
	}
	record.Sources = append(record.Sources, source)
}

func normalizeFiles(raw []string, limit int) []string {
	seen := make(map[string]struct{}, len(raw))
	files := make([]string, 0, len(raw))
	for _, file := range raw {
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}

	sort.Strings(files)
	if limit > 0 && len(files) > limit {
		files = append([]string(nil), files[:limit]...)
	}
	return files
}

func digestEvidence(evidence Evidence) string {
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "quarantine-evidence-v1\ncase=%s\npid=%d\nstart_time=%d\ncollected_at=%d\n",
		evidence.CaseID, evidence.Pid, evidence.StartTime, evidence.CollectedAt)
	for _, file := range evidence.Files {
		_, _ = fmt.Fprintf(hasher, "file=%s\n", file)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func cloneCase(record Case) Case {
	cloned := record
	cloned.Sources = append([]string(nil), record.Sources...)
	cloned.Evidence = cloneEvidence(record.Evidence)
	return cloned
}

func cloneEvidence(evidence Evidence) Evidence {
	cloned := evidence
	cloned.Files = append([]string(nil), evidence.Files...)
	return cloned
}

func isActiveState(state string) bool {
	return state == StatePending || state == StateStopped
}

func knownState(state string) bool {
	switch state {
	case StatePending, StateStopped, StateReleased, StateTerminated:
		return true
	default:
		return false
	}
}

func trimSpace(value string) string {
	return strings.TrimSpace(value)
}

func caseIDSequence(id string) int64 {
	separator := strings.LastIndex(id, "-")
	if separator < 0 || separator == len(id)-1 {
		return 0
	}
	sequence, err := strconv.ParseInt(id[separator+1:], 10, 64)
	if err != nil {
		return 0
	}
	return sequence
}
