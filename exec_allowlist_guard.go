// Package task decides which executables may be launched inside a sandbox by
// binding the real path, the inode and the content digest together.
package task

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

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

// Fine grained cause codes carried in Decision.Detail. The top level
// Decision.Reason still follows the documented reason vocabulary; Detail only
// distinguishes independent root causes (for example inode drift versus digest
// drift when both surface as ReasonMetadataDrift).
const (
	DetailInodeMismatch          = "inode-mismatch"
	DetailDigestMismatch         = "digest-mismatch"
	DetailEntryUnreadable        = "entry-unreadable"
	DetailArgv0Missing           = "argv0-missing"
	DetailInterpreterNotListed   = "interpreter-not-listed"
	DetailInterpreterUnresolved  = "interpreter-unresolved"
	DetailInterpreterDepth       = "interpreter-depth-exceeded"
	DetailInterpreterCycle       = "interpreter-cycle"
	DetailInterpreterBadShebang  = "interpreter-bad-shebang"
	DetailEnvTargetMissing       = "env-target-missing"
	DetailInterpreterInodeDrift  = "interpreter-inode-mismatch"
	DetailInterpreterDigestDrift = "interpreter-digest-mismatch"
	DetailInterpreterUnreadable  = "interpreter-unreadable"
)

const (
	defaultMaxInterpreterDepth = 8
	snapshotFormatV1           = "exec-allowlist-guard/snapshot-v1"
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
	// Detail is an optional, finer grained cause code. It never replaces
	// Reason and stays empty on the allowed path.
	Detail  string
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

// allowTable is an immutable, atomically published revision of the list.
type allowTable struct {
	entries map[string]Entry
	sorted  []Entry
}

// ExecAllowlistGuard owns the current allowlist revision.
type ExecAllowlistGuard struct {
	opts Options

	// mu serialises writers (Allow/Reload/Revoke/Restore) and coherent
	// snapshots. The table itself is swapped atomically so Check never blocks
	// on mu and only ever observes a complete old or new table.
	mu          sync.Mutex
	current     atomic.Pointer[allowTable]
	nextVersion int

	checks   atomic.Int64
	allowed  atomic.Int64
	denied   atomic.Int64
	recorded atomic.Int64
	reloads  atomic.Int64
}

// NewExecAllowlistGuard builds a guard with the documented defaults.
func NewExecAllowlistGuard(opts Options) *ExecAllowlistGuard {
	serverPATH := opts.PATH
	if len(serverPATH) == 0 {
		// Fixed at construction time from the server process environment;
		// per request Env PATH is never consulted.
		serverPATH = filepath.SplitList(os.Getenv("PATH"))
	} else {
		serverPATH = append([]string(nil), opts.PATH...)
	}
	g := &ExecAllowlistGuard{
		opts:        Options{PATH: serverPATH, MaxInterpreterDepth: opts.MaxInterpreterDepth},
		nextVersion: 1,
	}
	g.current.Store(emptyTable())
	return g
}

func emptyTable() *allowTable {
	return &allowTable{entries: map[string]Entry{}, sorted: []Entry{}}
}

func cloneTable(t *allowTable) *allowTable {
	next := make(map[string]Entry)
	if t != nil {
		for path, entry := range t.entries {
			next[path] = entry
		}
	}
	return &allowTable{entries: next}
}

func sortedEntries(entries map[string]Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (g *ExecAllowlistGuard) table() *allowTable {
	t := g.current.Load()
	if t == nil {
		return emptyTable()
	}
	return t
}

func (g *ExecAllowlistGuard) allocateVersionLocked() int {
	version := g.nextVersion
	if version < 1 {
		version = 1
	}
	g.nextVersion = version + 1
	return version
}

type fileMetadata struct {
	Inode  uint64
	Digest string
}

var errNotRegularExec = errors.New("not a regular executable file")

// inspectExecutable enforces the entry file contract: it has to be a regular
// file (symlinks and directories are rejected) carrying at least one execute
// bit. The digest is taken from an open file descriptor and paired with the
// fstat inode so both describe the same underlying object.
func inspectExecutable(path string) (fileMetadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileMetadata{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fileMetadata{}, errNotRegularExec
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fileMetadata{}, errNotRegularExec
	}

	file, err := os.Open(path)
	if err != nil {
		return fileMetadata{}, err
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return fileMetadata{}, err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return fileMetadata{}, errNotRegularExec
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fileMetadata{}, err
	}

	return fileMetadata{
		Inode:  inodeOf(fi),
		Digest: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func inodeOf(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}

func isRegularExecutable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// completeEntry validates the path/file contract and fills blank inode/digest
// fields. Explicit metadata is verified, never silently overwritten.
func completeEntry(entry Entry) (Entry, error) {
	if !filepath.IsAbs(entry.Path) {
		return Entry{}, fmt.Errorf("%w: path %q is not absolute", ErrEntryInvalid, entry.Path)
	}
	entry.Path = filepath.Clean(entry.Path)

	meta, err := inspectExecutable(entry.Path)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: path %q: %v", ErrEntryInvalid, entry.Path, err)
	}

	if entry.Inode != 0 && entry.Inode != meta.Inode {
		return Entry{}, fmt.Errorf("%w: path %q recorded inode %d, actual %d",
			ErrEntryMismatch, entry.Path, entry.Inode, meta.Inode)
	}
	if entry.Digest != "" && !strings.EqualFold(entry.Digest, meta.Digest) {
		return Entry{}, fmt.Errorf("%w: path %q recorded digest %s, actual %s",
			ErrEntryMismatch, entry.Path, entry.Digest, meta.Digest)
	}

	entry.Inode = meta.Inode
	entry.Digest = meta.Digest
	return entry, nil
}

// Allow validates and appends one entry to the current table.
func (g *ExecAllowlistGuard) Allow(e Entry) error {
	completed, err := completeEntry(e)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	completed.Version = g.allocateVersionLocked()

	next := cloneTable(g.current.Load())
	next.entries[completed.Path] = completed
	next.sorted = sortedEntries(next.entries)
	g.current.Store(next)
	return nil
}

// resolveTokenPath maps an executable token to the absolute path used as the
// table key. Bare names are looked up exclusively in the server fixed PATH.
// Request Env is intentionally unavailable here.
func (g *ExecAllowlistGuard) resolveTokenPath(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name), true
	}
	if strings.ContainsRune(name, filepath.Separator) {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return "", false
		}
		return filepath.Clean(absolute), true
	}

	for _, dir := range g.opts.PATH {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if isRegularExecutable(candidate) {
			return filepath.Clean(candidate), true
		}
	}
	return "", false
}

// Check decides whether the request may be launched. Every call is counted
// exactly once in Stats.Checks and then once in either Allowed or Denied.
func (g *ExecAllowlistGuard) Check(req ExecRequest) (Decision, error) {
	g.checks.Add(1)
	decision := g.evaluate(req)
	if decision.Allowed {
		g.allowed.Add(1)
	} else {
		g.denied.Add(1)
	}
	return decision, nil
}

func (g *ExecAllowlistGuard) evaluate(req ExecRequest) Decision {
	table := g.table()

	resolved, resolvedOK := g.resolveTokenPath(req.Path)
	if !resolvedOK {
		return Decision{Allowed: false, Reason: ReasonNotListed}
	}

	registered, hit := table.entries[resolved]
	if !hit {
		return Decision{Allowed: false, Reason: ReasonNotListed}
	}

	decision := Decision{Entry: registered, Version: registered.Version}

	// argv[0] is an identity assertion, not a display label. It must name the
	// exact resolved executable path.
	if len(req.Argv) == 0 {
		decision.Reason = ReasonArgvMismatch
		decision.Detail = DetailArgv0Missing
		return decision
	}
	if req.Argv[0] != resolved {
		decision.Reason = ReasonArgvMismatch
		return decision
	}

	meta, err := inspectExecutable(resolved)
	if err != nil {
		decision.Reason = ReasonMetadataDrift
		decision.Detail = DetailEntryUnreadable
		return decision
	}
	if meta.Inode != registered.Inode {
		// Catches a same content object renamed over the listed path: the
		// digest still matches but the inode is a different object.
		decision.Reason = ReasonMetadataDrift
		decision.Detail = DetailInodeMismatch
		return decision
	}
	if !strings.EqualFold(meta.Digest, registered.Digest) {
		// Catches an in place rewrite that kept path and inode stable.
		decision.Reason = ReasonMetadataDrift
		decision.Detail = DetailDigestMismatch
		return decision
	}

	if reason, detail := g.verifyInterpreterChain(resolved, table); reason != "" {
		decision.Reason = reason
		decision.Detail = detail
		return decision
	}

	decision.Allowed = true
	decision.Reason = ReasonAllowed
	return decision
}

// verifyInterpreterChain follows shebang lines recursively. A normal shebang
// names the next interpreter. An env style shebang ("#!/.../env cmd") has two
// stages: the env launcher itself and the real command it dispatches to; both
// must be trusted. Failures to locate/list an interpreter collapse to
// ReasonInterpreterMissing, while a listed interpreter whose metadata drifted
// keeps the independent ReasonMetadataDrift classification.
func (g *ExecAllowlistGuard) verifyInterpreterChain(top string, table *allowTable) (string, string) {
	maxDepth := g.opts.MaxInterpreterDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxInterpreterDepth
	}

	visited := map[string]struct{}{top: {}}
	current := top
	hops := 0

	for {
		line, isScript := readShebang(current)
		if !isScript {
			return "", ""
		}
		if hops >= maxDepth {
			return ReasonInterpreterMissing, DetailInterpreterDepth
		}
		hops++

		program, envCommand, isEnv := parseShebang(line)
		if program == "" {
			return ReasonInterpreterMissing, DetailInterpreterBadShebang
		}

		programPath, ok := g.resolveTokenPath(program)
		if !ok {
			return ReasonInterpreterMissing, DetailInterpreterUnresolved
		}
		if reason, detail := verifyInterpreterNode(programPath, table); reason != "" {
			return reason, detail
		}
		next := programPath

		if isEnv {
			if envCommand == "" {
				return ReasonInterpreterMissing, DetailEnvTargetMissing
			}
			commandPath, ok := g.resolveTokenPath(envCommand)
			if !ok {
				return ReasonInterpreterMissing, DetailInterpreterUnresolved
			}
			if reason, detail := verifyInterpreterNode(commandPath, table); reason != "" {
				return reason, detail
			}
			next = commandPath
		}

		if _, repeated := visited[next]; repeated {
			return ReasonInterpreterMissing, DetailInterpreterCycle
		}
		visited[next] = struct{}{}
		current = next
	}
}

// verifyInterpreterNode applies the same path/inode/digest binding as a top
// level Check against one interpreter executable.
func verifyInterpreterNode(path string, table *allowTable) (string, string) {
	registered, listed := table.entries[path]
	if !listed {
		return ReasonInterpreterMissing, DetailInterpreterNotListed
	}
	meta, err := inspectExecutable(path)
	if err != nil {
		return ReasonMetadataDrift, DetailInterpreterUnreadable
	}
	if meta.Inode != registered.Inode {
		return ReasonMetadataDrift, DetailInterpreterInodeDrift
	}
	if !strings.EqualFold(meta.Digest, registered.Digest) {
		return ReasonMetadataDrift, DetailInterpreterDigestDrift
	}
	return "", ""
}

// readShebang returns the trimmed shebang payload and true when path starts
// with "#!".
func readShebang(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	reader := bufio.NewReader(io.LimitReader(file, 4096))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false
	}
	if !strings.HasPrefix(line, "#!") {
		return "", false
	}
	line = strings.TrimPrefix(line, "#!")
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, true
}

// parseShebang splits a shebang payload. When the interpreter is env it also
// extracts the real command, skipping common env options and NAME=value
// assignments.
func parseShebang(line string) (program string, envCommand string, isEnv bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", false
	}
	program = fields[0]
	if filepath.Base(program) != "env" {
		return program, "", false
	}

	for i := 1; i < len(fields); i++ {
		arg := fields[i]

		if arg == "-S" || arg == "--split-string" {
			if i+1 < len(fields) {
				sub := strings.Fields(fields[i+1])
				if len(sub) > 0 {
					return program, sub[0], true
				}
			}
			return program, "", true
		}
		if strings.HasPrefix(arg, "--split-string=") {
			sub := strings.Fields(strings.TrimPrefix(arg, "--split-string="))
			if len(sub) > 0 {
				return program, sub[0], true
			}
			return program, "", true
		}

		if arg == "--" {
			if i+1 < len(fields) {
				return program, fields[i+1], true
			}
			return program, "", true
		}

		if strings.HasPrefix(arg, "-") && arg != "-" {
			if envOptionConsumesValue(arg) && !strings.Contains(arg, "=") && i+1 < len(fields) {
				i++
			}
			continue
		}

		// Leading NAME=VALUE assignments precede the command.
		if name, _, ok := strings.Cut(arg, "="); ok && name != "" {
			continue
		}

		return program, arg, true
	}
	return program, "", true
}

func envOptionConsumesValue(arg string) bool {
	switch arg {
	case "-u", "--unset", "-C", "--chdir", "-S", "--split-string":
		return true
	default:
		return false
	}
}

// Record audits a request that must already be allowed. It deliberately runs
// the decision without touching Check/Allowed/Denied counters so the metrics
// calibres stay independent.
func (g *ExecAllowlistGuard) Record(req ExecRequest) error {
	decision := g.evaluate(req)
	if !decision.Allowed {
		return fmt.Errorf("%w: reason %s", ErrNotAllowed, decision.Reason)
	}
	g.recorded.Add(1)
	return nil
}

// Reload validates the whole table first and only then swaps in the completed
// revision atomically. A rejected batch leaves the previous table untouched.
func (g *ExecAllowlistGuard) Reload(entries []Entry) error {
	completed := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		done, err := completeEntry(entry)
		if err != nil {
			return err
		}
		completed = append(completed, done)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	next := emptyTable()
	for _, entry := range completed {
		entry.Version = g.allocateVersionLocked()
		next.entries[entry.Path] = entry
	}
	next.sorted = sortedEntries(next.entries)

	g.current.Store(next)
	g.reloads.Add(1)
	return nil
}

// Stats returns a snapshot of the lifetime counters.
func (g *ExecAllowlistGuard) Stats() Stats {
	return Stats{
		Checks:   int(g.checks.Load()),
		Allowed:  int(g.allowed.Load()),
		Denied:   int(g.denied.Load()),
		Recorded: int(g.recorded.Load()),
		Reloads:  int(g.reloads.Load()),
	}
}

// Entries returns a copy of the current table ordered by path.
func (g *ExecAllowlistGuard) Entries() []Entry {
	current := g.table()
	out := make([]Entry, len(current.sorted))
	copy(out, current.sorted)
	return out
}

// Lookup returns the entry stored for one path.
func (g *ExecAllowlistGuard) Lookup(path string) (Entry, bool) {
	key := path
	if filepath.IsAbs(key) {
		key = filepath.Clean(key)
	}
	entry, ok := g.table().entries[key]
	return entry, ok
}

// Revoke removes one entry from the current table.
func (g *ExecAllowlistGuard) Revoke(path string) error {
	key := path
	if filepath.IsAbs(key) {
		key = filepath.Clean(key)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	next := cloneTable(g.current.Load())
	if _, ok := next.entries[key]; !ok {
		return fmt.Errorf("%w: %q", ErrEntryNotFound, path)
	}
	delete(next.entries, key)
	next.sorted = sortedEntries(next.entries)
	g.current.Store(next)
	return nil
}

type snapshotEnvelope struct {
	Format      string  `json:"format"`
	NextVersion int     `json:"next_version"`
	Entries     []Entry `json:"entries"`
	Stats       Stats   `json:"stats"`
	Checksum    string  `json:"checksum"`
}

// snapshotChecksum hashes the canonical envelope content while leaving the
// checksum field itself empty. It detects corruption/tampering of otherwise
// valid JSON payloads.
func snapshotChecksum(payload snapshotEnvelope) string {
	payload.Checksum = ""
	bytes, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

// Snapshot serialises the current table, its version sequence and counters.
func (g *ExecAllowlistGuard) Snapshot() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	current := g.table()
	entries := make([]Entry, len(current.sorted))
	copy(entries, current.sorted)

	payload := snapshotEnvelope{
		Format:      snapshotFormatV1,
		NextVersion: g.nextVersion,
		Entries:     entries,
		Stats:       g.Stats(),
	}
	payload.Checksum = snapshotChecksum(payload)
	bytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("exec allowlist guard: encode snapshot: %w", err)
	}
	return bytes, nil
}

// Restore loads a snapshot produced by Snapshot. The payload is fully parsed
// and validated before the table is swapped, so a bad snapshot never disturbs
// the live state.
func (g *ExecAllowlistGuard) Restore(b []byte) error {
	var payload snapshotEnvelope
	if err := json.Unmarshal(b, &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if payload.Format != snapshotFormatV1 {
		return fmt.Errorf("%w: unsupported snapshot format", ErrBadSnapshot)
	}
	if payload.Checksum == "" || subtle.ConstantTimeCompare(
		[]byte(payload.Checksum), []byte(snapshotChecksum(payload))) != 1 {
		return fmt.Errorf("%w: checksum mismatch", ErrBadSnapshot)
	}

	seen := make(map[string]struct{}, len(payload.Entries))
	maxVersion := 0
	for _, entry := range payload.Entries {
		if entry.Path == "" || !filepath.IsAbs(entry.Path) {
			return fmt.Errorf("%w: entry with invalid path", ErrBadSnapshot)
		}
		if entry.Version < 1 || entry.Inode == 0 || len(entry.Digest) != 64 {
			return fmt.Errorf("%w: entry %q is incomplete", ErrBadSnapshot, entry.Path)
		}
		if _, err := hex.DecodeString(entry.Digest); err != nil {
			return fmt.Errorf("%w: entry %q has a non hex digest", ErrBadSnapshot, entry.Path)
		}
		key := filepath.Clean(entry.Path)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate path %q", ErrBadSnapshot, key)
		}
		seen[key] = struct{}{}
		if entry.Version > maxVersion {
			maxVersion = entry.Version
		}
	}

	nextVersion := payload.NextVersion
	if nextVersion < maxVersion+1 {
		nextVersion = maxVersion + 1
	}
	if nextVersion < 1 {
		nextVersion = 1
	}

	table := emptyTable()
	for _, entry := range payload.Entries {
		entry.Path = filepath.Clean(entry.Path)
		table.entries[entry.Path] = entry
	}
	table.sorted = sortedEntries(table.entries)

	g.mu.Lock()
	defer g.mu.Unlock()

	g.current.Store(table)
	g.nextVersion = nextVersion
	g.checks.Store(int64(payload.Stats.Checks))
	g.allowed.Store(int64(payload.Stats.Allowed))
	g.denied.Store(int64(payload.Stats.Denied))
	g.recorded.Store(int64(payload.Stats.Recorded))
	g.reloads.Store(int64(payload.Stats.Reloads))
	return nil
}
