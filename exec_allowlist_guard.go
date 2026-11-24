// Package task decides which executables may be launched inside a sandbox by
// binding the real path, the inode and the content digest together.
package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("exec allowlist guard: not implemented")

// Errors returned by the guard.
var (
	ErrEntryInvalid  = errors.New("exec allowlist guard: invalid entry")
	ErrEntryMismatch = errors.New("exec allowlist guard: entry metadata mismatch")
	ErrEntryNotFound = errors.New("exec allowlist guard: entry not found")
	ErrBadRequest    = errors.New("exec allowlist guard: bad request")
	ErrNotAllowed    = errors.New("exec allowlist guard: request is not allowed")
	ErrBadSnapshot   = errors.New("exec allowlist guard: bad snapshot")
)

// Decision reason codes.
const (
	ReasonAllowed            = "allowed"
	ReasonNotListed          = "not-listed"
	ReasonArgvMismatch       = "argv0-mismatch"
	ReasonInterpreterMissing = "interpreter-missing"
	ReasonMetadataDrift      = "metadata-drift"
	ReasonPathAmbiguous      = "path-ambiguous"
)

// Options carries the server side lookup path and the shebang chain bound.
type Options struct {
	PATH                []string
	MaxInterpreterDepth int
}

// Entry is one allowlisted executable.
type Entry struct {
	Path    string
	Inode   uint64
	Digest  string
	Version int
}

// ExecRequest is one launch attempt observed by the guard.
type ExecRequest struct {
	Path   string
	Argv   []string
	Env    []string
	Tenant string
}

// Decision is the outcome of one Check or Record call.
type Decision struct {
	Allowed bool
	Reason  string
	Entry   Entry
	Version int
}

// Stats counts the lifetime activity of the guard.
type Stats struct {
	Checks   int
	Allowed  int
	Denied   int
	Recorded int
	Reloads  int
}

// ExecAllowlistGuard owns the current allowlist revision.
type ExecAllowlistGuard struct {
}

// NewExecAllowlistGuard builds a guard with the documented defaults.
func NewExecAllowlistGuard(opts Options) *ExecAllowlistGuard {
	return &ExecAllowlistGuard{}
}

// Allow validates and appends one entry to the current table.
func (g *ExecAllowlistGuard) Allow(e Entry) error {
	return errNotImplemented
}

// Check decides whether the request may be launched.
func (g *ExecAllowlistGuard) Check(req ExecRequest) (Decision, error) {
	return Decision{}, errNotImplemented
}

// Record audits a request that must already be allowed.
func (g *ExecAllowlistGuard) Record(req ExecRequest) error {
	return errNotImplemented
}

// Reload validates the whole table and swaps it in atomically.
func (g *ExecAllowlistGuard) Reload(entries []Entry) error {
	return errNotImplemented
}

// Stats returns a snapshot of the lifetime counters.
func (g *ExecAllowlistGuard) Stats() Stats {
	return Stats{}
}

// Entries returns a copy of the current table ordered by path.
func (g *ExecAllowlistGuard) Entries() []Entry {
	return nil
}

// Lookup returns the entry stored for one path.
func (g *ExecAllowlistGuard) Lookup(path string) (Entry, bool) {
	return Entry{}, false
}

// Revoke removes one entry from the current table.
func (g *ExecAllowlistGuard) Revoke(path string) error {
	return errNotImplemented
}

// Snapshot serialises the current table and counters.
func (g *ExecAllowlistGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (g *ExecAllowlistGuard) Restore(b []byte) error {
	return errNotImplemented
}
