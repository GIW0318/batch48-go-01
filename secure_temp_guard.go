// Package task owns the temporary files and directories a sandbox tenant is
// allowed to create, enforcing exclusive creation, quotas and orphan sweeps.
package task

import "errors"

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

// Permission bits applied to every entry the guard creates.
const (
	ModeTempFile = 0o600
	ModeTempDir  = 0o700
)

// Errors returned by the guard.
var (
	ErrBadRoot       = errors.New("temp root is not configured")
	ErrBadArg        = errors.New("tenant, purpose or timestamp is invalid")
	ErrPathExists    = errors.New("path already exists")
	ErrNoParent      = errors.New("tenant directory does not exist")
	ErrBadSize       = errors.New("requested size is out of range")
	ErrQuotaExceeded = errors.New("tenant quota exceeded")
	ErrNoEntry       = errors.New("temporary entry not found")
	ErrBadSnapshot   = errors.New("snapshot payload is invalid")
)

// Options configures the sandbox root, the naming seed and every hard cap.
type Options struct {
	Root                string `json:"root"`
	Seed                int64  `json:"seed"`
	MaxEntriesPerTenant int    `json:"max_entries_per_tenant"`
	MaxBytesPerTenant   uint64 `json:"max_bytes_per_tenant"`
	MaxFileBytes        uint64 `json:"max_file_bytes"`
	TTL                 int64  `json:"ttl"`
}

// TempFile is one file the guard handed out.
type TempFile struct {
	ID      string `json:"id"`
	Tenant  string `json:"tenant"`
	Path    string `json:"path"`
	Mode    uint32 `json:"mode"`
	Size    int    `json:"size"`
	Created int64  `json:"created"`
	Active  bool   `json:"active"`
}

// TempDir is the per-tenant directory the guard handed out.
type TempDir struct {
	ID      string `json:"id"`
	Tenant  string `json:"tenant"`
	Path    string `json:"path"`
	Mode    uint32 `json:"mode"`
	Created int64  `json:"created"`
}

// Usage is the per-tenant accounting view.
type Usage struct {
	Tenant        string `json:"tenant"`
	Entries       int    `json:"entries"`
	Bytes         uint64 `json:"bytes"`
	ActiveHandles int    `json:"active_handles"`
}

// SecureTempGuard owns the naming sequence, the entry table and the clocks.
type SecureTempGuard struct {
	opts Options
}

// NewSecureTempGuard builds a guard with the given root and caps.
func NewSecureTempGuard(opts Options) *SecureTempGuard {
	return &SecureTempGuard{opts: opts}
}

// NewFile creates one exclusively named file under the tenant directory.
func (g *SecureTempGuard) NewFile(tenant, purpose string, n int) (*TempFile, error) {
	return nil, errNotImplemented
}

// NewDir creates the exclusively named directory of one tenant.
func (g *SecureTempGuard) NewDir(tenant string) (*TempDir, error) {
	return nil, errNotImplemented
}

// Release drops the active handle of one entry.
func (g *SecureTempGuard) Release(id string) error {
	return errNotImplemented
}

// Sweep reclaims entries whose lifetime has passed and that nobody holds.
func (g *SecureTempGuard) Sweep(now int64) (int, error) {
	return 0, errNotImplemented
}

// Usage reports the entries, bytes and active handles of one tenant.
func (g *SecureTempGuard) Usage(tenant string) (Usage, error) {
	return Usage{}, errNotImplemented
}

// Write grows one temporary file within the tenant byte quota.
func (g *SecureTempGuard) Write(id string, extra int) error {
	return errNotImplemented
}

// SetNow moves the guard clock used when entries are created.
func (g *SecureTempGuard) SetNow(now int64) error {
	return errNotImplemented
}

// Stat looks one entry up by id.
func (g *SecureTempGuard) Stat(id string) (TempFile, bool) {
	return TempFile{}, false
}

// List lists the entry ids currently owned by one tenant.
func (g *SecureTempGuard) List(tenant string) []string {
	return nil
}

// Snapshot serialises the roots, the naming sequence and all entries.
func (g *SecureTempGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the guard state from a snapshot.
func (g *SecureTempGuard) Restore(payload []byte) error {
	return errNotImplemented
}
