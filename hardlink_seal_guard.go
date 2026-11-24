// Package task decides whether hardlink, unlink and rename requests may land,
// based on ownership, sticky bits and an immutable protected path set.
package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
)

// Reason codes carried by every verdict.
const (
	CodeAllow         = "ok"
	CodeLinkSymlink   = "link_symlink_source"
	CodeCrossOwner    = "cross_owner_source"
	CodeProtectedPath = "protected_path"
	CodeStickyDenied  = "sticky_denied"
	CodeExchangeType  = "exchange_type_mismatch"
	CodeExchangeCycle = "exchange_cycle"
	CodeSelfTarget    = "self_target"
)

// Errors returned by the guard.
var (
	ErrBadInfo      = errors.New("path info is incomplete")
	ErrBadProtected = errors.New("protected path is blank")
	ErrBadSnapshot  = errors.New("snapshot payload is invalid")
)

const (
	stickyModeBit       = 0o1000
	sealSnapshotVersion = 1
)

// Info is the filesystem metadata one decision is computed from.
type Info struct {
	Path       string `json:"path"`
	UID        uint32 `json:"uid"`
	Mode       uint32 `json:"mode"`
	Inode      uint64 `json:"inode"`
	Nlink      int    `json:"nlink"`
	IsSymlink  bool   `json:"is_symlink"`
	LinkTarget string `json:"link_target,omitempty"`
	IsDir      bool   `json:"is_dir"`
}

// Verdict is the decision handed back for one request.
type Verdict struct {
	Allow  bool   `json:"allow"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// Options configures the sticky bit switch, cross owner policy and caps.
type Options struct {
	StickyEnforced  bool     `json:"sticky_enforced"`
	AllowCrossOwner bool     `json:"allow_cross_owner"`
	MaxProtected    int      `json:"max_protected"`
	Protected       []string `json:"protected,omitempty"`
}

// guardState is an immutable policy and protected-set generation. Publishing a
// whole generation through one atomic pointer keeps readers from ever observing
// a half-replaced set.
type guardState struct {
	options   Options
	protected []string
	lookup    map[string]struct{}
}

// HardlinkSealGuard holds the protected path set behind an atomic pointer.
type HardlinkSealGuard struct {
	state   atomic.Pointer[guardState]
	writeMu sync.Mutex
}

var emptyGuardState = &guardState{lookup: map[string]struct{}{}}

// NewHardlinkSealGuard builds a guard with the given policy and protected set.
func NewHardlinkSealGuard(opts Options) *HardlinkSealGuard {
	if opts.MaxProtected < 0 {
		opts.MaxProtected = 0
	}
	state, err := buildGuardState(opts, opts.Protected)
	if err != nil {
		// The constructor cannot report an error. An invalid initial policy is
		// treated as an empty protected set rather than a partially installed
		// one.
		state, _ = buildGuardState(opts, nil)
	}
	g := &HardlinkSealGuard{}
	g.state.Store(state)
	return g
}

// CheckLink decides whether a hardlink may be created.
func (g *HardlinkSealGuard) CheckLink(src Info, dstDir Info, owner uint32) (Verdict, error) {
	if badInfo(src) || badInfo(dstDir) {
		return Verdict{}, ErrBadInfo
	}

	state := g.current()
	if infoIsSymlink(src) {
		return deny(CodeLinkSymlink, "hardlink source is a symbolic link"), nil
	}
	if !state.options.AllowCrossOwner && src.UID != owner {
		return deny(CodeCrossOwner, "hardlink source owner does not match the effective uid"), nil
	}
	if state.isProtected(src.Path) || state.isProtected(dstDir.Path) {
		return deny(CodeProtectedPath, "hardlink touches a protected path"), nil
	}
	return allow(), nil
}

// CheckUnlink decides whether a deletion may proceed.
func (g *HardlinkSealGuard) CheckUnlink(target Info, dir Info, owner uint32) (Verdict, error) {
	if badInfo(target) || badInfo(dir) {
		return Verdict{}, ErrBadInfo
	}

	state := g.current()
	if state.isProtected(target.Path) || state.isProtected(dir.Path) {
		return deny(CodeProtectedPath, "unlink touches a protected path"), nil
	}
	if state.options.StickyEnforced && isSticky(dir) {
		if owner != 0 && owner != target.UID && owner != dir.UID {
			return deny(CodeStickyDenied, "sticky directory allows only the file owner, directory owner or privileged caller"), nil
		}
	}
	return allow(), nil
}

// CheckRename decides whether a rename, plain or exchange style, may proceed.
func (g *HardlinkSealGuard) CheckRename(src, dst Info, exchange bool) (Verdict, error) {
	if badInfo(src) || badInfo(dst) {
		return Verdict{}, ErrBadInfo
	}

	state := g.current()
	if state.isProtected(src.Path) || state.isProtected(dst.Path) {
		return deny(CodeProtectedPath, "rename touches a protected path"), nil
	}
	if !exchange {
		return allow(), nil
	}
	if src.IsDir != dst.IsDir {
		return deny(CodeExchangeType, "exchange rename requires both sides to have the same type"), nil
	}
	if src.IsDir && dst.IsDir && exchangeWouldCycle(src, dst) {
		return deny(CodeExchangeCycle, "directory exchange would create a cycle"), nil
	}
	return allow(), nil
}

// SetProtected replaces the whole protected path set.
func (g *HardlinkSealGuard) SetProtected(paths []string) error {
	g.writeMu.Lock()
	defer g.writeMu.Unlock()

	current := g.current()
	next, err := buildGuardState(current.options, paths)
	if err != nil {
		return err
	}
	g.state.Store(next)
	return nil
}

// Explain renders a verdict as readable text that names its reason code.
func (g *HardlinkSealGuard) Explain(v Verdict) string {
	code := v.Code
	if code == "" {
		if v.Allow {
			code = CodeAllow
		} else {
			code = "unknown"
		}
	}
	outcome := "denied"
	if v.Allow {
		outcome = "allowed"
	}
	if v.Detail != "" {
		return fmt.Sprintf("%s [%s]: %s", outcome, code, v.Detail)
	}
	return fmt.Sprintf("%s [%s]", outcome, code)
}

type snapshotEnvelope struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Snapshot serialises the policy and the protected path set.
func (g *HardlinkSealGuard) Snapshot() ([]byte, error) {
	state := g.current()
	payload := Options{
		StickyEnforced:  state.options.StickyEnforced,
		AllowCrossOwner: state.options.AllowCrossOwner,
		MaxProtected:    state.options.MaxProtected,
		Protected:       copyStrings(state.protected),
	}
	raw, err := json.Marshal(&payload)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	envelope := snapshotEnvelope{
		Version:  sealSnapshotVersion,
		Checksum: hex.EncodeToString(digest[:]),
		Payload:  raw,
	}
	return json.Marshal(&envelope)
}

// Restore replaces the policy and protected set from a snapshot.
func (g *HardlinkSealGuard) Restore(payload []byte) error {
	next, err := decodeSnapshot(payload)
	if err != nil {
		return err
	}

	g.writeMu.Lock()
	g.state.Store(next)
	g.writeMu.Unlock()
	return nil
}

// IsProtected reports whether one path sits in the protected set.
func (g *HardlinkSealGuard) IsProtected(value string) bool {
	return g.current().isProtected(value)
}

// Protected lists the protected paths in stored order.
func (g *HardlinkSealGuard) Protected() []string {
	return copyStrings(g.current().protected)
}

func (g *HardlinkSealGuard) current() *guardState {
	if state := g.state.Load(); state != nil {
		return state
	}
	return emptyGuardState
}

func buildGuardState(options Options, paths []string) (*guardState, error) {
	if options.MaxProtected > 0 && len(paths) > options.MaxProtected {
		return nil, fmt.Errorf("%w: %d entries exceed the limit of %d", ErrBadProtected, len(paths), options.MaxProtected)
	}

	protected := make([]string, len(paths))
	lookup := make(map[string]struct{}, len(paths))
	for i, value := range paths {
		if strings.TrimSpace(value) == "" {
			return nil, ErrBadProtected
		}
		protected[i] = value
		lookup[normalizePath(value)] = struct{}{}
	}

	policy := options
	policy.Protected = nil
	return &guardState{
		options:   policy,
		protected: protected,
		lookup:    lookup,
	}, nil
}

func decodeSnapshot(payload []byte) (*guardState, error) {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrBadSnapshot)
	}

	var envelope snapshotEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if envelope.Version != sealSnapshotVersion || len(envelope.Payload) == 0 || len(envelope.Checksum) != sha256.Size*2 {
		return nil, fmt.Errorf("%w: invalid envelope", ErrBadSnapshot)
	}
	digest := sha256.Sum256(envelope.Payload)
	want := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(want), []byte(envelope.Checksum)) != 1 {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrBadSnapshot)
	}

	var options Options
	if err := json.Unmarshal(envelope.Payload, &options); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	if options.MaxProtected < 0 {
		return nil, fmt.Errorf("%w: negative protected limit", ErrBadSnapshot)
	}
	state, err := buildGuardState(options, options.Protected)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	return state, nil
}

func (s *guardState) isProtected(value string) bool {
	if value == "" {
		return false
	}
	_, ok := s.lookup[normalizePath(value)]
	return ok
}

func badInfo(info Info) bool {
	return strings.TrimSpace(info.Path) == ""
}

func infoIsSymlink(info Info) bool {
	if info.IsSymlink {
		return true
	}
	const unixSymlinkType = 0o120000
	return info.Mode&0o170000 == unixSymlinkType
}

func isSticky(info Info) bool {
	const goModeSticky = 1 << 20
	return info.Mode&stickyModeBit != 0 || info.Mode&goModeSticky != 0
}

func exchangeWouldCycle(src, dst Info) bool {
	if mutualDirectoryLinks(src, dst) {
		return true
	}
	return pathContains(src.Path, dst.Path) || pathContains(dst.Path, src.Path)
}

func mutualDirectoryLinks(src, dst Info) bool {
	if src.LinkTarget == "" || dst.LinkTarget == "" {
		return false
	}
	return samePath(src.LinkTarget, dst.Path) && samePath(dst.LinkTarget, src.Path)
}

func pathContains(parent, child string) bool {
	parent = normalizePath(parent)
	child = normalizePath(child)
	if parent == child {
		return true
	}
	if parent == "/" {
		return strings.HasPrefix(child, "/")
	}
	return strings.HasPrefix(child, parent+"/")
}

func samePath(left, right string) bool {
	return normalizePath(left) == normalizePath(right)
}

func normalizePath(value string) string {
	return path.Clean(value)
}

func copyStrings(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func allow() Verdict {
	return Verdict{Allow: true, Code: CodeAllow, Detail: "request satisfies all seal rules"}
}

func deny(code, detail string) Verdict {
	return Verdict{Allow: false, Code: code, Detail: detail}
}
