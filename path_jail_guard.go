// Package task admits path shaped requests inside a sandbox, resolving every
// symbolic link and enforcing per root read only flags and tenant quotas.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
)

// errNotImplemented is retained as a stable internal sentinel.
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

const snapshotVersion = 1

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

type compiledRoot struct {
	spec            Root
	virtual         string
	virtualParts    []string
	real            string
	excludeParts    [][]string
	caseInsensitive bool
}

// PathJailGuard admits or rejects path shaped requests.
type PathJailGuard struct {
	mu     sync.RWMutex
	roots  []compiledRoot
	quotas map[string]int
}

type snapshotPayload struct {
	Version int            `json:"version"`
	Roots   []Root         `json:"roots"`
	Quotas  map[string]int `json:"quotas"`
}

type linkHop struct {
	From   string
	Target string
}

type linkLoopError struct {
	hops []linkHop
}

func (e *linkLoopError) Error() string {
	if len(e.hops) == 0 {
		return ErrTooManyLinks.Error()
	}

	start := 0
	seen := make(map[string]int)
	for i, hop := range e.hops {
		if prev, ok := seen[hop.From]; ok {
			start = prev
			break
		}
		seen[hop.From] = i
	}

	var builder strings.Builder
	builder.WriteString(ErrTooManyLinks.Error())
	builder.WriteString(": cycle fragment ")
	for i := start; i < len(e.hops); i++ {
		if i > start {
			builder.WriteString("; ")
		}
		fmt.Fprintf(&builder, "%s -> %s", path.Base(filepath.ToSlash(e.hops[i].From)), e.hops[i].Target)
	}
	return builder.String()
}

func (e *linkLoopError) Unwrap() error {
	return ErrTooManyLinks
}

// NewPathJailGuard validates the mounts and returns a jail.
func NewPathJailGuard(roots Roots) (*PathJailGuard, error) {
	compiled, err := compileRoots(roots)
	if err != nil {
		return nil, err
	}
	return &PathJailGuard{
		roots:  compiled,
		quotas: make(map[string]int),
	}, nil
}

// Resolve folds the request, follows symbolic links and applies the mounts.
func (j *PathJailGuard) Resolve(raw string, op Op) (Resolved, error) {
	if strings.IndexByte(raw, 0) >= 0 {
		return Resolved{}, fmt.Errorf("%w: NUL byte in path", ErrBadPath)
	}

	virtual := foldVirtual(raw)

	j.mu.RLock()
	defer j.mu.RUnlock()

	cr, relParts, err := matchRoot(j.roots, virtual)
	if err != nil {
		return Resolved{}, err
	}
	if excludedByRoot(relParts, cr) {
		return Resolved{}, fmt.Errorf("%w: %s", ErrExcluded, virtual)
	}
	if op == OpWrite && cr.spec.ReadOnly {
		return Resolved{}, fmt.Errorf("%w: %s", ErrReadOnly, cr.virtual)
	}

	real, exists, parent, info, err := resolvePhysical(cr.real, relParts, op)
	if err != nil {
		return Resolved{}, err
	}

	if op == OpWrite && exists && info != nil && info.Mode().IsRegular() {
		external, err := hasExternalHardLink(cr.real, info)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: inspect hard links: %v", ErrEscaped, err)
		}
		if external {
			return Resolved{}, fmt.Errorf("%w: %s has a hard link outside its root", ErrEscaped, real)
		}
	}

	if parent == "" && exists {
		parent = filepath.Dir(real)
	}

	return Resolved{
		Virtual:  virtual,
		Real:     real,
		Root:     cr.real,
		Tenant:   cr.spec.Tenant,
		ReadOnly: cr.spec.ReadOnly,
		Exists:   exists,
		Parent:   parent,
	}, nil
}

// SetRoots swaps the mount set atomically.
func (j *PathJailGuard) SetRoots(roots []Root) error {
	compiled, err := compileRoots(Roots(roots))
	if err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	j.roots = compiled
	return nil
}

// Roots returns a copy of the current mounts.
func (j *PathJailGuard) Roots() []Root {
	j.mu.RLock()
	defer j.mu.RUnlock()

	out := make([]Root, 0, len(j.roots))
	for i := range j.roots {
		out = append(out, cloneRoot(j.roots[i].spec))
	}
	return out
}

// ReadOnly reports whether the request path lands in a deny-by-default mount.
func (j *PathJailGuard) ReadOnly(raw string) bool {
	if strings.IndexByte(raw, 0) >= 0 {
		return true
	}

	virtual := foldVirtual(raw)
	j.mu.RLock()
	defer j.mu.RUnlock()

	cr, relParts, err := matchRoot(j.roots, virtual)
	if err != nil {
		return true
	}
	if excludedByRoot(relParts, cr) {
		return true
	}
	return cr.spec.ReadOnly
}

// SetQuota sets the writable byte budget of one tenant.
func (j *PathJailGuard) SetQuota(tenant string, bytes int) error {
	if bytes < 0 {
		return fmt.Errorf("%w: quota %d", ErrBadCharge, bytes)
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.quotas == nil {
		j.quotas = make(map[string]int)
	}
	j.quotas[tenant] = bytes
	return nil
}

// Charge deducts writable bytes from one tenant budget.
func (j *PathJailGuard) Charge(tenant string, bytes int) error {
	if bytes < 0 {
		return fmt.Errorf("%w: charge %d", ErrBadCharge, bytes)
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	balance, ok := j.quotas[tenant]
	if !ok {
		return fmt.Errorf("%w: %q", ErrBadTenant, tenant)
	}
	if bytes > balance {
		return fmt.Errorf("%w: charge %d from balance %d", ErrQuota, bytes, balance)
	}
	j.quotas[tenant] = balance - bytes
	return nil
}

// Quota reports the remaining writable bytes of one tenant.
func (j *PathJailGuard) Quota(tenant string) (int, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	balance, ok := j.quotas[tenant]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrBadTenant, tenant)
	}
	return balance, nil
}

// Snapshot serialises the mounts and the remaining quotas.
func (j *PathJailGuard) Snapshot() ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	payload := snapshotPayload{
		Version: snapshotVersion,
		Roots:   make([]Root, 0, len(j.roots)),
		Quotas:  make(map[string]int, len(j.quotas)),
	}
	for i := range j.roots {
		payload.Roots = append(payload.Roots, cloneRoot(j.roots[i].spec))
	}
	for tenant, balance := range j.quotas {
		payload.Quotas[tenant] = balance
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("path jail guard: encode snapshot: %w", err)
	}
	return data, nil
}

// Restore loads a snapshot produced by Snapshot.
func (j *PathJailGuard) Restore(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("%w: empty payload", ErrBadSnapshot)
	}

	var payload snapshotPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if payload.Version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrBadSnapshot, payload.Version)
	}

	compiled, err := compileRoots(Roots(payload.Roots))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	quotas := make(map[string]int, len(payload.Quotas))
	for tenant, balance := range payload.Quotas {
		if balance < 0 {
			return fmt.Errorf("%w: negative balance for %q", ErrBadSnapshot, tenant)
		}
		quotas[tenant] = balance
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	j.roots = compiled
	j.quotas = quotas
	return nil
}

func compileRoots(roots Roots) ([]compiledRoot, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: empty root set", ErrBadRoots)
	}

	compiled := make([]compiledRoot, 0, len(roots))
	for i, root := range roots {
		if strings.IndexByte(root.Path, 0) >= 0 || strings.IndexByte(root.Virtual, 0) >= 0 {
			return nil, fmt.Errorf("%w: root %d contains a NUL byte", ErrBadRoots, i)
		}
		if !filepath.IsAbs(root.Path) {
			return nil, fmt.Errorf("%w: root %d path %q is not absolute", ErrBadRoots, i, root.Path)
		}

		info, err := os.Stat(root.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: root %d path %q: %v", ErrBadRoots, i, root.Path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: root %d path %q is not a directory", ErrBadRoots, i, root.Path)
		}

		real, err := filepath.EvalSymlinks(root.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: evaluate root %d path %q: %v", ErrBadRoots, i, root.Path, err)
		}
		real = filepath.Clean(real)
		realInfo, err := os.Stat(real)
		if err != nil || !realInfo.IsDir() {
			return nil, fmt.Errorf("%w: canonical root %d path %q is not a directory", ErrBadRoots, i, real)
		}

		spec := cloneRoot(root)
		virtual := foldVirtual(spec.Virtual)
		excludeParts := make([][]string, 0, len(spec.Exclude))
		for _, exclude := range spec.Exclude {
			excludeParts = append(excludeParts, splitVirtual(foldVirtual(exclude)))
		}

		compiled = append(compiled, compiledRoot{
			spec:            spec,
			virtual:         virtual,
			virtualParts:    splitVirtual(virtual),
			real:            real,
			excludeParts:    excludeParts,
			caseInsensitive: detectCaseInsensitive(real),
		})
	}

	for i := 0; i < len(compiled); i++ {
		for k := i + 1; k < len(compiled); k++ {
			if compiledRootsOverlap(compiled[i], compiled[k]) {
				return nil, fmt.Errorf("%w: virtual prefixes %q and %q overlap",
					ErrBadRoots, compiled[i].virtual, compiled[k].virtual)
			}
		}
	}

	return compiled, nil
}

func cloneRoot(root Root) Root {
	copyRoot := root
	if root.Exclude != nil {
		copyRoot.Exclude = make([]string, len(root.Exclude))
		copy(copyRoot.Exclude, root.Exclude)
	}
	return copyRoot
}

func foldVirtual(raw string) string {
	return path.Clean("/" + raw)
}

func splitVirtual(virtual string) []string {
	virtual = path.Clean(virtual)
	if virtual == "/" || virtual == "." {
		return nil
	}
	return strings.Split(strings.TrimPrefix(virtual, "/"), "/")
}

func segmentPrefix(parts []string, prefix []string) bool {
	if len(parts) < len(prefix) {
		return false
	}
	for i := range prefix {
		if parts[i] != prefix[i] {
			return false
		}
	}
	return true
}

func segmentPrefixFold(parts []string, prefix []string) bool {
	if len(parts) < len(prefix) {
		return false
	}
	for i := range prefix {
		if !strings.EqualFold(parts[i], prefix[i]) {
			return false
		}
	}
	return true
}

func compiledRootsOverlap(a, b compiledRoot) bool {
	exact := segmentPrefix(a.virtualParts, b.virtualParts) ||
		segmentPrefix(b.virtualParts, a.virtualParts)
	if exact || (!a.caseInsensitive && !b.caseInsensitive) {
		return exact
	}
	return segmentPrefixFold(a.virtualParts, b.virtualParts) ||
		segmentPrefixFold(b.virtualParts, a.virtualParts)
}

func matchRoot(roots []compiledRoot, virtual string) (*compiledRoot, []string, error) {
	parts := splitVirtual(virtual)
	best := -1
	for i := range roots {
		if segmentPrefix(parts, roots[i].virtualParts) {
			if best == -1 || len(roots[i].virtualParts) > len(roots[best].virtualParts) {
				best = i
			}
		}
	}
	if best == -1 {
		for i := range roots {
			if !roots[i].caseInsensitive || !segmentPrefixFold(parts, roots[i].virtualParts) {
				continue
			}
			if best == -1 || len(roots[i].virtualParts) > len(roots[best].virtualParts) {
				best = i
			}
		}
	}
	if best == -1 {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoRoot, virtual)
	}

	cr := &roots[best]
	relParts := append([]string(nil), parts[len(cr.virtualParts):]...)
	return cr, relParts, nil
}

func excludedByRoot(relParts []string, cr *compiledRoot) bool {
	for _, exclude := range cr.excludeParts {
		if cr.caseInsensitive {
			if segmentPrefixFold(relParts, exclude) {
				return true
			}
			continue
		}
		if segmentPrefix(relParts, exclude) {
			return true
		}
	}
	return false
}

func detectCaseInsensitive(dir string) bool {
	if runtime.GOOS == "windows" {
		return true
	}

	probe, err := os.MkdirTemp(dir, "caseProbe-*")
	if err != nil {
		return runtime.GOOS == "darwin"
	}
	defer os.RemoveAll(probe)

	alternate := filepath.Join(dir, invertASCIICase(filepath.Base(probe)))
	_, err = os.Stat(alternate)
	return err == nil
}

func invertASCIICase(value string) string {
	bytes := []byte(value)
	for i, char := range bytes {
		switch {
		case char >= 'a' && char <= 'z':
			bytes[i] = char - ('a' - 'A')
		case char >= 'A' && char <= 'Z':
			bytes[i] = char + ('a' - 'A')
		}
	}
	return string(bytes)
}

type rootRelation int

const (
	relationOutside rootRelation = iota
	relationAncestor
	relationInside
)

func resolvePhysical(rootReal string, initialParts []string, op Op) (string, bool, string, os.FileInfo, error) {
	parts := append([]string(nil), initialParts...)
	current := rootReal
	enteredRoot := true
	hops := 0
	trail := make([]linkHop, 0, MaxSymlinkHops+1)

	for idx := 0; idx < len(parts); {
		segment := parts[idx]
		idx++

		if segment == "" || segment == "." {
			continue
		}

		if segment == ".." {
			current = filepath.Dir(current)
			relation, err := compareToRoot(rootReal, current)
			if err != nil {
				return "", false, "", nil, err
			}
			if relation == relationInside {
				enteredRoot = true
				continue
			}
			if !enteredRoot && relation == relationAncestor {
				continue
			}
			return "", false, "", nil, fmt.Errorf("%w: %s", ErrEscaped, current)
		}

		candidate := filepath.Join(current, segment)
		info, err := os.Lstat(candidate)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return "", false, "", nil, fmt.Errorf("lstat %s: %w", candidate, err)
			}

			relation, relationErr := compareToRoot(rootReal, current)
			if relationErr != nil {
				return "", false, "", nil, relationErr
			}
			if relation != relationInside {
				return "", false, "", nil, fmt.Errorf("%w: %s", ErrEscaped, candidate)
			}

			rest := append([]string{segment}, parts[idx:]...)
			real := filepath.Clean(filepath.Join(append([]string{current}, rest...)...))
			if err := ensureExistingAncestorWithin(rootReal, real); err != nil {
				return "", false, "", nil, err
			}
			if op != OpWrite {
				return "", false, "", nil, fmt.Errorf("%w: %s", ErrNotExist, real)
			}
			return real, false, current, nil, nil
		}

		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(candidate)
			if err != nil {
				return "", false, "", nil, fmt.Errorf("readlink %s: %w", candidate, err)
			}

			hops++
			trail = append(trail, linkHop{From: candidate, Target: target})
			if hops > MaxSymlinkHops {
				return "", false, "", nil, &linkLoopError{hops: append([]linkHop(nil), trail...)}
			}

			targetParts, absolute := splitLinkTarget(target)
			remaining := append([]string(nil), parts[idx:]...)
			parts = append(targetParts, remaining...)
			idx = 0
			if absolute {
				current = string(filepath.Separator)
				if volume := filepath.VolumeName(target); volume != "" {
					current = volume + string(filepath.Separator)
				}
				enteredRoot = false
			}
			continue
		}

		current = candidate
		relation, err := compareToRoot(rootReal, current)
		if err != nil {
			return "", false, "", nil, err
		}
		if relation == relationInside {
			enteredRoot = true
			continue
		}
		if !enteredRoot && relation == relationAncestor {
			continue
		}
		return "", false, "", nil, fmt.Errorf("%w: %s", ErrEscaped, current)
	}

	relation, err := compareToRoot(rootReal, current)
	if err != nil {
		return "", false, "", nil, err
	}
	if relation != relationInside {
		return "", false, "", nil, fmt.Errorf("%w: %s", ErrEscaped, current)
	}

	info, err := os.Lstat(current)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if op == OpWrite {
				return current, false, filepath.Dir(current), nil, nil
			}
			return "", false, "", nil, fmt.Errorf("%w: %s", ErrNotExist, current)
		}
		return "", false, "", nil, fmt.Errorf("lstat final path %s: %w", current, err)
	}

	return current, true, filepath.Dir(current), info, nil
}

func splitLinkTarget(target string) ([]string, bool) {
	slashTarget := filepath.ToSlash(target)
	absolute := filepath.IsAbs(target)
	if volume := filepath.VolumeName(target); volume != "" {
		slashTarget = strings.TrimPrefix(slashTarget, filepath.ToSlash(volume))
	}
	slashTarget = strings.TrimPrefix(slashTarget, "/")
	parts := strings.Split(slashTarget, "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil, absolute
	}
	return parts, absolute
}

func ensureExistingAncestorWithin(rootReal, target string) error {
	probe := filepath.Clean(target)
	for {
		info, err := os.Lstat(probe)
		if err == nil {
			relation, compareErr := compareInfoToRoot(rootReal, probe, info)
			if compareErr != nil {
				return compareErr
			}
			if relation != relationInside {
				return fmt.Errorf("%w: %s", ErrEscaped, target)
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("lstat ancestor %s: %w", probe, err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("%w: %s", ErrEscaped, target)
		}
		probe = parent
	}
}

func compareToRoot(rootReal, current string) (rootRelation, error) {
	info, err := os.Lstat(current)
	if err != nil {
		return relationOutside, fmt.Errorf("stat %s: %w", current, err)
	}
	return compareInfoToRoot(rootReal, current, info)
}

func compareInfoToRoot(rootReal, current string, currentInfo os.FileInfo) (rootRelation, error) {
	rootInfo, err := os.Stat(rootReal)
	if err != nil {
		return relationOutside, fmt.Errorf("stat root %s: %w", rootReal, err)
	}
	if os.SameFile(rootInfo, currentInfo) {
		return relationInside, nil
	}

	probe := current
	for {
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		info, err := os.Lstat(parent)
		if err == nil && os.SameFile(rootInfo, info) {
			return relationInside, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return relationOutside, fmt.Errorf("stat parent %s: %w", parent, err)
		}
		probe = parent
	}

	probe = filepath.Dir(rootReal)
	for {
		info, err := os.Lstat(probe)
		if err == nil && os.SameFile(currentInfo, info) {
			return relationAncestor, nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}

	return relationOutside, nil
}

func hasExternalHardLink(rootReal string, targetInfo os.FileInfo) (bool, error) {
	nlink, ok := hardLinkCount(targetInfo)
	if !ok || nlink <= 1 {
		return false, nil
	}

	internalLinks := 0
	walkErr := filepath.WalkDir(rootReal, func(candidate string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if os.SameFile(targetInfo, info) {
			internalLinks++
		}
		return nil
	})
	if walkErr != nil {
		return false, walkErr
	}

	return uint64(internalLinks) < nlink, nil
}

func hardLinkCount(info os.FileInfo) (uint64, bool) {
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0, false
	}
	field := value.FieldByName("Nlink")
	if !field.IsValid() {
		return 0, false
	}

	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int()), true
	default:
		return 0, false
	}
}
