// Package task decides whether hardlink, unlink and rename requests may land,
// based on ownership, sticky bits and an immutable protected path set.
package task

import "errors"

// errNotImplemented marks behaviour that is not wired up yet.
var errNotImplemented = errors.New("not implemented")

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

// HardlinkSealGuard holds the protected path set behind an atomic pointer.
type HardlinkSealGuard struct {
	opts Options
}

// NewHardlinkSealGuard builds a guard with the given policy and protected set.
func NewHardlinkSealGuard(opts Options) *HardlinkSealGuard {
	return &HardlinkSealGuard{opts: opts}
}

// CheckLink decides whether a hardlink may be created.
func (g *HardlinkSealGuard) CheckLink(src Info, dstDir Info, owner uint32) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// CheckUnlink decides whether a deletion may proceed.
func (g *HardlinkSealGuard) CheckUnlink(target Info, dir Info, owner uint32) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// CheckRename decides whether a rename, plain or exchange style, may proceed.
func (g *HardlinkSealGuard) CheckRename(src, dst Info, exchange bool) (Verdict, error) {
	return Verdict{}, errNotImplemented
}

// SetProtected replaces the whole protected path set.
func (g *HardlinkSealGuard) SetProtected(paths []string) error {
	return errNotImplemented
}

// Explain renders a verdict as readable text that names its reason code.
func (g *HardlinkSealGuard) Explain(v Verdict) string {
	return ""
}

// Snapshot serialises the policy and the protected path set.
func (g *HardlinkSealGuard) Snapshot() ([]byte, error) {
	return nil, errNotImplemented
}

// Restore replaces the policy and protected set from a snapshot.
func (g *HardlinkSealGuard) Restore(payload []byte) error {
	return errNotImplemented
}

// IsProtected reports whether one path sits in the protected set.
func (g *HardlinkSealGuard) IsProtected(path string) bool {
	return false
}

// Protected lists the protected paths in stored order.
func (g *HardlinkSealGuard) Protected() []string {
	return nil
}
