// Package task admits path shaped requests inside a sandbox, resolving every
// symbolic link and enforcing per root read only flags and tenant quotas.
package task

import "errors"

// errNotImplemented is returned by every behaviour that still needs work.
var errNotImplemented = errors.New("path jail guard: not implemented")

// Errors returned by the guard.
var (
	ErrBadRoots     = errors.New("path jail guard: bad roots")
	ErrBadPath      = errors.New("path jail guard: bad path")
	ErrNoRoot       = errors.New("path jail guard: no matching root")
	ErrExcluded     = errors.New("path jail guard: excluded subtree")
	ErrEscaped      = errors.New("path jail guard: resolved outside the root")
	ErrTooManyLinks = errors.New("path jail guard: too many symlink hops")
	ErrNotExist     = errors.New("path jail guard: target does not exist")
	ErrReadOnly     = errors.New("path jail guard: root is read only")
	ErrQuota        = errors.New("path jail guard: quota exceeded")
	ErrBadTenant    = errors.New("path jail guard: unknown tenant")
	ErrBadCharge    = errors.New("path jail guard: bad charge")
	ErrBadSnapshot  = errors.New("path jail guard: bad snapshot")
)

// MaxSymlinkHops is the number of link expansions allowed per resolution.
const MaxSymlinkHops = 8

// Op is the kind of access a request asks for.
type Op int

// Supported operations.
const (
	OpRead Op = iota
	OpWrite
	OpExecute
)

// Root mounts one virtual subtree on a host directory.
type Root struct {
	Virtual  string
	Path     string
	ReadOnly bool
	Tenant   string
	Exclude  []string
}

// Roots is the ordered set of mounts a jail is built from.
type Roots []Root

// Resolved is the outcome of a successful admission.
type Resolved struct {
	Virtual  string
	Real     string
	Root     string
	Tenant   string
	ReadOnly bool
	Exists   bool
	Parent   string
}

// PathJailGuard admits or rejects path shaped requests.
type PathJailGuard struct {
}

// NewPathJailGuard validates the mounts and returns a jail.
func NewPathJailGuard(roots Roots) (*PathJailGuard, error) {
	return nil, errNotImplemented
}

// Resolve folds the request, follows symbolic links and applies the mounts.
func (j *PathJailGuard) Resolve(raw string, op Op) (Resolved, error) {
	return Resolved{}, errNotImplemented
}

// SetRoots swaps the mount set atomically.
func (j *PathJailGuard) SetRoots(roots []Root) error {
	return errNotImplemented
}

// Roots returns a copy of the current mounts.
func (j *PathJailGuard) Roots() []Root {
	return nil
}

// ReadOnly reports whether the request path lands in a deny-by-default mount.
func (j *PathJailGuard) ReadOnly(raw string) bool {
	return false
}

// SetQuota sets the writable byte budget of one tenant.
func (j *PathJailGuard) SetQuota(tenant string, bytes int) error {
	return errNotImplemented
}

// Charge deducts writable bytes from one tenant budget.
func (j *PathJailGuard) Charge(tenant string, bytes int) error {
	return errNotImplemented
}

// Quota reports the remaining writable bytes of one tenant.
func (j *PathJailGuard) Quota(tenant string) (int, error) {
	return 0, errNotImplemented
}

// Snapshot serialises the mounts and the remaining quotas.
func (j *PathJailGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore loads a snapshot produced by Snapshot.
func (j *PathJailGuard) Restore(b []byte) error {
	return errNotImplemented
}
