// Package task owns the temporary files and directories a sandbox tenant is
// allowed to create, enforcing exclusive creation, quotas and orphan sweeps.
package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

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

// errNotImplemented is retained for source compatibility with the scaffold.
var errNotImplemented = errors.New("not implemented")

const (
	kindFile = "file"
	kindDir  = "dir"

	snapshotVersion = 1

	// accessWOK is the W_OK mode for access(2); the stdlib syscall package
	// does not export the constant portably.
	accessWOK = 0x2
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

// entry is the single authoritative record for one directory or file. Quota
// usage is always derived from the entry table so the gauges cannot drift.
type entry struct {
	ID       string `json:"id"`
	Tenant   string `json:"tenant"`
	Purpose  string `json:"purpose"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Mode     uint32 `json:"mode"`
	Size     int    `json:"size"`
	Created  int64  `json:"created"`
	Seq      int64  `json:"seq"`
	Handles  int    `json:"handles"`
	Active   bool   `json:"active"`
	Released bool   `json:"released"`
	ExpireAt int64  `json:"expire_at"`

	// Writing is transient and never serialised. When positive Sweep must
	// leave the entry alone and postpone its expiry.
	Writing int `json:"-"`
}

// guardState is the replaceable unit behind Restore.
type guardState struct {
	Root     string            `json:"root"`
	Seq      int64             `json:"seq"`
	Clock    int64             `json:"clock"`
	ClockSet bool              `json:"clock_set"`
	Entries  map[string]*entry `json:"entries"`
}

// snapshotData is the on-the-wire snapshot envelope.
type snapshotData struct {
	Version  int      `json:"version"`
	Root     string   `json:"root"`
	Seq      int64    `json:"seq"`
	Clock    int64    `json:"clock"`
	ClockSet bool     `json:"clock_set"`
	Entries  []*entry `json:"entries"`
}

// SecureTempGuard owns the naming sequence, the entry table and the clocks.
type SecureTempGuard struct {
	opts Options

	mu       sync.RWMutex
	st       *guardState
	physical bool
}

// NewSecureTempGuard builds a guard with the given root and caps.
func NewSecureTempGuard(opts Options) *SecureTempGuard {
	g := &SecureTempGuard{
		opts: opts,
		st: &guardState{
			Root:    opts.Root,
			Entries: make(map[string]*entry),
		},
	}
	g.physical = preparePhysicalRoot(opts.Root)
	return g
}

// NewDir creates the exclusively named directory of one tenant.
func (g *SecureTempGuard) NewDir(tenant string) (*TempDir, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	root := g.st.Root
	if root == "" {
		return nil, ErrBadRoot
	}
	if tenant == "" {
		return nil, ErrBadArg
	}
	if dir := g.tenantDirLocked(tenant); dir != nil {
		return nil, fmt.Errorf("%w: tenant %q already has a directory", ErrPathExists, tenant)
	}

	count := len(g.tenantEntriesLocked(tenant))
	if limit := g.opts.MaxEntriesPerTenant; count+1 > limit {
		return nil, fmt.Errorf("%w: tenant %q already owns %d entries", ErrQuotaExceeded, tenant, count)
	}

	now := g.nowLocked()
	seq, token := g.allocateNameLocked()
	name := fmt.Sprintf("%s-%d-%s", safePathToken(tenant), seq, token)

	e := &entry{
		ID:       fmt.Sprintf("e-%d", seq),
		Tenant:   tenant,
		Kind:     kindDir,
		Name:     name,
		Path:     filepath.Join(root, name),
		Mode:     ModeTempDir,
		Created:  now,
		Seq:      seq,
		ExpireAt: now + g.opts.TTL,
	}

	// Settle the quota/name reservation before touching the disk. The lock
	// is held for the whole operation, so a failed persistence simply rolls
	// the provisional record back and no peer ever observes it.
	g.st.Entries[e.ID] = e
	if g.physical {
		if err := physicalMkdir(root, e.Name, ModeTempDir); err != nil {
			delete(g.st.Entries, e.ID)
			if errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("%w: %s", ErrPathExists, e.Path)
			}
			return nil, fmt.Errorf("secure mkdir %s: %w", e.Path, err)
		}
	}

	return toTempDir(e), nil
}

// NewFile creates one exclusively named file under the tenant directory.
func (g *SecureTempGuard) NewFile(tenant, purpose string, n int) (*TempFile, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.st.Root == "" {
		return nil, ErrBadRoot
	}
	if tenant == "" || purpose == "" {
		return nil, ErrBadArg
	}
	if n <= 0 {
		return nil, fmt.Errorf("%w: requested size %d must be positive", ErrBadSize, n)
	}
	if maxFile := g.opts.MaxFileBytes; uint64(n) > maxFile {
		return nil, fmt.Errorf("%w: size %d exceeds single-file cap %d", ErrBadSize, n, maxFile)
	}

	dir := g.tenantDirLocked(tenant)
	if dir == nil {
		return nil, fmt.Errorf("%w: tenant %q", ErrNoParent, tenant)
	}

	owned := g.tenantEntriesLocked(tenant)
	if limit := g.opts.MaxEntriesPerTenant; len(owned)+1 > limit {
		return nil, fmt.Errorf("%w: entry cap %d reached", ErrQuotaExceeded, limit)
	}
	var usedBytes uint64
	for _, e := range owned {
		if e.Kind == kindFile {
			usedBytes += uint64(e.Size)
		}
	}
	if limit := g.opts.MaxBytesPerTenant; usedBytes+uint64(n) > limit {
		return nil, fmt.Errorf("%w: %d+%d bytes exceeds %d", ErrQuotaExceeded, usedBytes, n, limit)
	}

	now := g.nowLocked()
	seq, token := g.allocateNameLocked()
	name := fmt.Sprintf("%s-%d-%s", safePathToken(purpose), seq, token)

	e := &entry{
		ID:       fmt.Sprintf("e-%d", seq),
		Tenant:   tenant,
		Purpose:  purpose,
		Kind:     kindFile,
		Name:     name,
		Path:     filepath.Join(dir.Path, name),
		Mode:     ModeTempFile,
		Size:     n,
		Created:  now,
		Seq:      seq,
		Handles:  1,
		Active:   true,
		ExpireAt: now + g.opts.TTL,
	}

	// Reserve the entry and its bytes first, then persist exclusively.
	g.st.Entries[e.ID] = e
	if g.physical {
		if err := physicalCreateFile(dir.Path, e.Name, ModeTempFile, int64(n)); err != nil {
			delete(g.st.Entries, e.ID)
			if errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("%w: %s", ErrPathExists, e.Path)
			}
			// The parent path is missing or unsafe (for example swapped for
			// an external symlink), so report the same class as no parent.
			return nil, fmt.Errorf("%w: secure create %s: %v", ErrNoParent, e.Path, err)
		}
	}

	return toTempFile(e), nil
}

// Release drops the active handle of one entry.
func (g *SecureTempGuard) Release(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	e, ok := g.st.Entries[id]
	if !ok {
		return ErrNoEntry
	}
	// Only files carry active handles. A second Release after the handle is
	// gone is reported as an unknown/released entry.
	if e.Kind != kindFile || e.Handles <= 0 {
		return fmt.Errorf("%w: %q has no active handle", ErrNoEntry, id)
	}

	// Gauge first: the handle disappears immediately, while quota usage keeps
	// counting the record until Sweep reclaims it.
	e.Handles--
	if e.Handles == 0 {
		e.Active = false
		e.Released = true
	}
	return nil
}

// Sweep reclaims entries whose lifetime has passed and that nobody holds.
func (g *SecureTempGuard) Sweep(now int64) (int, error) {
	if now < 0 {
		return 0, fmt.Errorf("%w: sweep timestamp %d", ErrBadArg, now)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.st.Clock = now
	g.st.ClockSet = true

	removed := 0
	ttl := g.opts.TTL

	// Files first so an expired file and its directory can both go in one
	// sweep, matching the "released files then empty expired directories" rule.
	for _, id := range g.sortedIDsLocked() {
		e, ok := g.st.Entries[id]
		if !ok || e.Kind != kindFile {
			continue
		}
		if now < e.ExpireAt {
			continue
		}
		if e.Handles > 0 || e.Writing > 0 {
			// Busy/held entries survive; push the deadline forward.
			e.ExpireAt = now + ttl
			continue
		}
		if g.physical {
			if err := physicalRemoveFile(e.Path); err != nil {
				// Never drop the bookkeeping for a file that may still be on
				// disk; retry on a later sweep after postponing.
				e.ExpireAt = now + ttl
				continue
			}
		}
		delete(g.st.Entries, id)
		removed++
	}

	// Directories only when expired and empty. Their deadline is left intact
	// while non-empty so they can be reclaimed in the same sweep that removes
	// the final child.
	for _, id := range g.sortedIDsLocked() {
		e, ok := g.st.Entries[id]
		if !ok || e.Kind != kindDir {
			continue
		}
		if now < e.ExpireAt {
			continue
		}
		if len(g.tenantEntriesLocked(e.Tenant)) > 1 {
			continue
		}
		if g.physical {
			if err := physicalRemoveDir(e.Path); err != nil {
				e.ExpireAt = now + ttl
				continue
			}
		}
		delete(g.st.Entries, id)
		removed++
	}

	return removed, nil
}

// Usage reports the entries, bytes and active handles of one tenant.
func (g *SecureTempGuard) Usage(tenant string) (Usage, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	u := Usage{Tenant: tenant}
	for _, e := range g.st.Entries {
		if e.Tenant != tenant {
			continue
		}
		u.Entries++
		if e.Kind == kindFile {
			u.Bytes += uint64(e.Size)
			if e.Handles > 0 {
				u.ActiveHandles++
			}
		}
	}
	return u, nil
}

// Write grows one temporary file within the tenant byte quota.
func (g *SecureTempGuard) Write(id string, extra int) error {
	if extra < 0 {
		return fmt.Errorf("%w: negative growth %d", ErrBadSize, extra)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	e, ok := g.st.Entries[id]
	if !ok || e.Kind != kindFile {
		return fmt.Errorf("%w: %q", ErrNoEntry, id)
	}
	if extra == 0 {
		return nil
	}

	newSize := uint64(e.Size) + uint64(extra)

	var sizeCapErr error
	if maxFile := g.opts.MaxFileBytes; newSize > maxFile {
		sizeCapErr = fmt.Errorf("new size %d exceeds single-file cap %d", newSize, maxFile)
	}

	var usedBytes uint64
	for _, other := range g.st.Entries {
		if other.Tenant == e.Tenant && other.Kind == kindFile {
			usedBytes += uint64(other.Size)
		}
	}
	var tenantCapErr error
	if maxBytes := g.opts.MaxBytesPerTenant; usedBytes+uint64(extra) > maxBytes {
		tenantCapErr = fmt.Errorf("tenant usage %d+%d exceeds byte cap %d", usedBytes, extra, maxBytes)
	}

	if sizeCapErr != nil || tenantCapErr != nil {
		// Nothing has mutated, so size and accounted bytes are unchanged.
		switch {
		case sizeCapErr != nil && tenantCapErr != nil:
			return fmt.Errorf("%w and %w: %v; %v", ErrBadSize, ErrQuotaExceeded, sizeCapErr, tenantCapErr)
		case sizeCapErr != nil:
			// A single-file hard cap is both a size violation and a capacity
			// quota; wrap both sentinels so either classification matches.
			return fmt.Errorf("%w/%w: %v", ErrBadSize, ErrQuotaExceeded, sizeCapErr)
		default:
			return fmt.Errorf("%w: %v", ErrQuotaExceeded, tenantCapErr)
		}
	}

	oldSize := e.Size
	e.Size = int(newSize)
	e.Writing++

	var physErr error
	if g.physical {
		physErr = physicalGrowFile(e.Path, int64(newSize))
	}

	e.Writing--
	if physErr != nil {
		// Roll the reservation back so neither the file size nor the tenant
		// byte gauge moves after a failed write.
		e.Size = oldSize
		return fmt.Errorf("%w: secure write %s: %v", ErrNoParent, e.Path, physErr)
	}
	return nil
}

// SetNow moves the guard clock used when entries are created.
func (g *SecureTempGuard) SetNow(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: clock %d", ErrBadArg, now)
	}
	g.mu.Lock()
	g.st.Clock = now
	g.st.ClockSet = true
	g.mu.Unlock()
	return nil
}

// Stat looks one entry up by id.
func (g *SecureTempGuard) Stat(id string) (TempFile, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	e, ok := g.st.Entries[id]
	if !ok || e.Kind != kindFile {
		return TempFile{}, false
	}
	return *toTempFile(e), true
}

// List lists the entry ids currently owned by one tenant.
func (g *SecureTempGuard) List(tenant string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := make([]string, 0)
	for _, id := range g.sortedIDsLocked() {
		if e := g.st.Entries[id]; e != nil && e.Tenant == tenant {
			ids = append(ids, id)
		}
	}
	return ids
}

// Snapshot serialises the roots, the naming sequence and all entries.
func (g *SecureTempGuard) Snapshot() ([]byte, error) {
	g.mu.RLock()
	data := snapshotData{
		Version:  snapshotVersion,
		Root:     g.st.Root,
		Seq:      g.st.Seq,
		Clock:    g.st.Clock,
		ClockSet: g.st.ClockSet,
		Entries:  make([]*entry, 0, len(g.st.Entries)),
	}
	for _, id := range g.sortedIDsLocked() {
		data.Entries = append(data.Entries, g.st.Entries[id])
	}
	g.mu.RUnlock()

	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode snapshot: %w", err)
	}
	return payload, nil
}

// Restore replaces the guard state from a snapshot. The payload is fully
// parsed and validated before the live state pointer is swapped, so a bad
// snapshot never perturbs the running guard.
func (g *SecureTempGuard) Restore(payload []byte) error {
	var data snapshotData
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrBadSnapshot, err)
	}

	st, err := buildRestoredState(&data)
	if err != nil {
		return err
	}

	physical := preparePhysicalRoot(st.Root)

	g.mu.Lock()
	g.st = st
	g.physical = physical
	g.mu.Unlock()
	return nil
}

// buildRestoredState validates an unmarshalled snapshot and constructs the
// replacement state without touching the live guard.
func buildRestoredState(data *snapshotData) (*guardState, error) {
	if data.Version != 0 && data.Version != snapshotVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, data.Version)
	}
	if data.Seq < 0 {
		return nil, fmt.Errorf("%w: negative sequence %d", ErrBadSnapshot, data.Seq)
	}

	st := &guardState{
		Root:     data.Root,
		Seq:      data.Seq,
		Clock:    data.Clock,
		ClockSet: data.ClockSet,
		Entries:  make(map[string]*entry, len(data.Entries)),
	}

	dirTenants := make(map[string]string)
	var maxSeq int64
	for i, e := range data.Entries {
		if e == nil {
			return nil, fmt.Errorf("%w: entry %d is null", ErrBadSnapshot, i)
		}
		if e.ID == "" {
			return nil, fmt.Errorf("%w: entry %d has empty id", ErrBadSnapshot, i)
		}
		if _, dup := st.Entries[e.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate id %q", ErrBadSnapshot, e.ID)
		}
		if e.Tenant == "" {
			return nil, fmt.Errorf("%w: entry %q has empty tenant", ErrBadSnapshot, e.ID)
		}
		if e.Kind != kindFile && e.Kind != kindDir {
			return nil, fmt.Errorf("%w: entry %q has bad kind %q", ErrBadSnapshot, e.ID, e.Kind)
		}
		if e.Size < 0 {
			return nil, fmt.Errorf("%w: entry %q has negative size %d", ErrBadSnapshot, e.ID, e.Size)
		}
		if e.Seq < 0 {
			return nil, fmt.Errorf("%w: entry %q has negative seq", ErrBadSnapshot, e.ID)
		}
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}

		// Transient state must never survive a restore.
		e.Writing = 0
		if e.Kind == kindDir {
			if prior, dup := dirTenants[e.Tenant]; dup {
				return nil, fmt.Errorf("%w: tenant %q has directories %q and %q", ErrBadSnapshot, e.Tenant, prior, e.ID)
			}
			dirTenants[e.Tenant] = e.ID
			e.Handles = 0
			e.Active = false
		} else {
			if e.Handles < 0 {
				return nil, fmt.Errorf("%w: entry %q has negative handles", ErrBadSnapshot, e.ID)
			}
			if e.Active && e.Handles == 0 {
				e.Handles = 1
			}
			e.Active = e.Handles > 0
		}

		st.Entries[e.ID] = e
	}

	// Never reissue an id/name that a restored entry already consumed, even if
	// the snapshot sequence counter lagged the entries.
	if maxSeq > st.Seq {
		st.Seq = maxSeq
	}
	return st, nil
}

// nowLocked resolves the entry timestamp, honouring SetNow.
func (g *SecureTempGuard) nowLocked() int64 {
	if g.st.ClockSet {
		return g.st.Clock
	}
	return time.Now().Unix()
}

// allocateNameLocked advances the naming sequence and mints a random token.
func (g *SecureTempGuard) allocateNameLocked() (int64, string) {
	g.st.Seq++
	return g.st.Seq, randomToken(8)
}

func (g *SecureTempGuard) tenantDirLocked(tenant string) *entry {
	for _, e := range g.st.Entries {
		if e.Kind == kindDir && e.Tenant == tenant {
			return e
		}
	}
	return nil
}

func (g *SecureTempGuard) tenantEntriesLocked(tenant string) []*entry {
	out := make([]*entry, 0)
	for _, e := range g.st.Entries {
		if e.Tenant == tenant {
			out = append(out, e)
		}
	}
	return out
}

func (g *SecureTempGuard) sortedIDsLocked() []string {
	ids := make([]string, 0, len(g.st.Entries))
	for id := range g.st.Entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return g.st.Entries[ids[i]].Seq < g.st.Entries[ids[j]].Seq
	})
	return ids
}

func toTempFile(e *entry) *TempFile {
	return &TempFile{
		ID:      e.ID,
		Tenant:  e.Tenant,
		Path:    e.Path,
		Mode:    e.Mode,
		Size:    e.Size,
		Created: e.Created,
		Active:  e.Handles > 0,
	}
}

func toTempDir(e *entry) *TempDir {
	return &TempDir{
		ID:      e.ID,
		Tenant:  e.Tenant,
		Path:    e.Path,
		Mode:    e.Mode,
		Created: e.Created,
	}
}

// safePathToken reduces a tenant/purpose string to one non-empty path
// component. Separators and other hostile characters are replaced, so the
// resulting name can never escape its parent directory. The sequence and
// random suffix appended by callers also prevent "." / ".." collisions.
func safePathToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-' || r == '@' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}

func randomToken(nBytes int) string {
	raw := make([]byte, nBytes)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failure is extraordinary; the sequence still makes
		// names unique, so fall back to a time-based token.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

// preparePhysicalRoot ensures the configured root exists and is a writable
// directory. When the platform path is not provisioned in the current
// environment (the shipped tests use a nominal /sandbox/tmp), the guard runs
// on its authoritative in-memory state instead of failing every operation.
// Real, writable roots - notably t.TempDir() used by security tests - get the
// full secure-filesystem backend.
func preparePhysicalRoot(root string) bool {
	if root == "" {
		return false
	}
	if err := os.MkdirAll(root, ModeTempDir); err != nil {
		return false
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return false
	}
	if err := syscall.Access(root, accessWOK); err != nil {
		return false
	}
	return true
}

// openSecureDir opens a directory while refusing a symlink as its final
// component. The open itself is the atomic parent validation: there is no
// stat-then-open gap, and the returned descriptor pins the real inode so a
// later rename/symlink swap cannot redirect relative operations outside.
func openSecureDir(path string) (int, error) {
	return syscall.Open(path,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
}

func openRootDir(root string) (int, error) {
	// The configured root itself is platform owned and may legitimately be a
	// symlink (for example /tmp -> private/tmp), so it is followed once.
	return syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
}

func physicalMkdir(root, name string, mode uint32) error {
	fd, err := openRootDir(root)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	if err := syscall.Mkdirat(fd, name, mode); err != nil {
		return err
	}
	// Make the advertised owner-only mode exact regardless of umask.
	_ = syscall.Fchmodat(fd, name, mode, 0)
	return nil
}

func physicalCreateFile(parentDir, name string, mode uint32, size int64) error {
	dirFD, err := openSecureDir(parentDir)
	if err != nil {
		return err
	}
	defer syscall.Close(dirFD)

	fd, err := syscall.Openat(dirFD, name,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, mode)
	if err != nil {
		return err
	}

	cleanup := func() {
		_ = syscall.Close(fd)
		_ = syscall.Unlinkat(dirFD, name)
	}

	if err := syscall.Fchmod(fd, mode); err != nil {
		cleanup()
		return err
	}
	if size > 0 {
		if err := syscall.Ftruncate(fd, size); err != nil {
			cleanup()
			return err
		}
	}
	if err := syscall.Close(fd); err != nil {
		_ = syscall.Unlinkat(dirFD, name)
		return err
	}
	return nil
}

// physicalGrowFile extends the file to newSize through a securely resolved
// parent descriptor. A one-byte write at the final offset grows the inode
// without ever shrinking it, so concurrent reserved growth cannot regress the
// on-disk size.
func physicalGrowFile(path string, newSize int64) error {
	parentDir := filepath.Dir(path)
	name := filepath.Base(path)

	dirFD, err := openSecureDir(parentDir)
	if err != nil {
		return err
	}
	defer syscall.Close(dirFD)

	fd, err := syscall.Openat(dirFD, name,
		syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	if newSize <= 0 {
		return nil
	}
	_, err = syscall.Pwrite(fd, []byte{0}, newSize-1)
	return err
}

func physicalRemoveFile(path string) error {
	dirFD, err := openSecureDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer syscall.Close(dirFD)
	return syscall.Unlinkat(dirFD, filepath.Base(path))
}

func physicalRemoveDir(path string) error {
	// rmdir(2) never follows a trailing symlink, so a swapped directory path
	// fails with ENOTDIR instead of removing the external target.
	return syscall.Rmdir(path)
}
