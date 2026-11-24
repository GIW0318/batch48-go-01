// Package task moves a suspicious process into quarantine, snapshots what it
// had open and closes the case through release, termination or timeout.
package task

import (
	"errors"
	"sync"
)

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

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

// QuarantineGuard owns every quarantine case.
type QuarantineGuard struct {
	opts Options
	mu   sync.Mutex
}

// NewQuarantineGuard builds a guard with the given policy.
func NewQuarantineGuard(opts Options) *QuarantineGuard {
	return &QuarantineGuard{opts: opts}
}

// Quarantine opens a pending case and records where the request came from.
func (g *QuarantineGuard) Quarantine(pid int, reason, by string) (Case, error) {
	return Case{}, errNotImplemented
}

// Collect stops the process and captures its identity and open files.
func (g *QuarantineGuard) Collect(caseID string) (Evidence, error) {
	return Evidence{}, errNotImplemented
}

// Release returns a stopped process to normal operation.
func (g *QuarantineGuard) Release(caseID, by string) error {
	return errNotImplemented
}

// Terminate closes a stopped case by killing the process.
func (g *QuarantineGuard) Terminate(caseID, by string) error {
	return errNotImplemented
}

// Expire advances timer tiers and returns the cases touched by this call.
func (g *QuarantineGuard) Expire(now int64) ([]Case, error) {
	return nil, errNotImplemented
}

// Cases lists cases in one state; an empty state lists every case.
func (g *QuarantineGuard) Cases(state string) []Case {
	return nil
}

// Snapshot serialises every case.
func (g *QuarantineGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces every case from a snapshot.
func (g *QuarantineGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// Lookup returns one case by identifier.
func (g *QuarantineGuard) Lookup(caseID string) (Case, bool) {
	return Case{}, false
}

// Active returns the one active case for a pid, if any.
func (g *QuarantineGuard) Active(pid int) (Case, bool) {
	return Case{}, false
}

// Policy returns the configuration the guard was built with.
func (g *QuarantineGuard) Policy() Options {
	return g.opts
}
