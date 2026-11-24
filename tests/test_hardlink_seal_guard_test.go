package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-b-10"
)

func sealOptions() impl.Options {
	return impl.Options{
		StickyEnforced: true,
		MaxProtected:   16,
		Protected:      []string{"/data/secret"},
	}
}

func TestCheckLinkRejectsBlankInfo(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	if _, err := g.CheckLink(impl.Info{}, impl.Info{Path: "/data", IsDir: true}, 0); !errors.Is(err, impl.ErrBadInfo) {
		t.Fatalf("blank source path must fail with ErrBadInfo, got %v", err)
	}
	if _, err := g.CheckLink(impl.Info{Path: "/data/a"}, impl.Info{}, 0); !errors.Is(err, impl.ErrBadInfo) {
		t.Fatalf("blank destination path must fail with ErrBadInfo, got %v", err)
	}
	if _, err := g.CheckUnlink(impl.Info{}, impl.Info{Path: "/data"}, 0); !errors.Is(err, impl.ErrBadInfo) {
		t.Fatalf("blank unlink target must fail with ErrBadInfo, got %v", err)
	}
}

func TestCheckLinkRejectsSymlinkSource(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	src := impl.Info{Path: "/home/u/link", UID: 1000, IsSymlink: true, LinkTarget: "/data/secret"}
	dst := impl.Info{Path: "/home/u", UID: 1000, IsDir: true}
	v, err := g.CheckLink(src, dst, 1000)
	if err != nil {
		t.Fatalf("check link: %v", err)
	}
	if v.Allow {
		t.Fatalf("hardlink of a symlink must be denied")
	}
	if v.Code != impl.CodeLinkSymlink {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeLinkSymlink)
	}
}

func TestCheckLinkRejectsForeignSource(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	src := impl.Info{Path: "/home/u/data.bin", UID: 1000}
	dst := impl.Info{Path: "/home/other", UID: 2000, IsDir: true}
	v, err := g.CheckLink(src, dst, 2000)
	if err != nil {
		t.Fatalf("check link: %v", err)
	}
	if v.Allow {
		t.Fatalf("source owned by another uid must not be linked")
	}
	if v.Code != impl.CodeCrossOwner {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeCrossOwner)
	}
}

func TestCheckLinkAllowsOwnedFileDeterministically(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	src := impl.Info{Path: "/home/u/data.bin", UID: 1000, Mode: 0o644, Inode: 501, Nlink: 1}
	dst := impl.Info{Path: "/home/u", UID: 1000, IsDir: true, Mode: 0o755}
	first, err := g.CheckLink(src, dst, 1000)
	if err != nil {
		t.Fatalf("check link: %v", err)
	}
	if !first.Allow {
		t.Fatalf("owned hardlink must be allowed, code = %q", first.Code)
	}
	if first.Code != impl.CodeAllow {
		t.Fatalf("verdict code = %q, want %q", first.Code, impl.CodeAllow)
	}
	second, err := g.CheckLink(src, dst, 1000)
	if err != nil {
		t.Fatalf("second check link: %v", err)
	}
	if first != second {
		t.Fatalf("repeated check must be bit identical: %+v vs %+v", first, second)
	}
}

func TestCheckLinkRejectsProtectedSource(t *testing.T) {
	opts := sealOptions()
	opts.Protected = []string{"/home/u/data.bin"}
	g := impl.NewHardlinkSealGuard(opts)
	src := impl.Info{Path: "/home/u/data.bin", UID: 1000}
	dst := impl.Info{Path: "/home/u", UID: 1000, IsDir: true}
	v, err := g.CheckLink(src, dst, 1000)
	if err != nil {
		t.Fatalf("check link: %v", err)
	}
	if v.Allow {
		t.Fatalf("protected source must not be linked")
	}
	if v.Code != impl.CodeProtectedPath {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeProtectedPath)
	}
}

func TestCheckUnlinkRejectsProtectedTarget(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	target := impl.Info{Path: "/data/secret", UID: 1000}
	dir := impl.Info{Path: "/data", UID: 1000, IsDir: true}
	v, err := g.CheckUnlink(target, dir, 1000)
	if err != nil {
		t.Fatalf("check unlink: %v", err)
	}
	if v.Allow {
		t.Fatalf("protected target must not be unlinked")
	}
	if v.Code != impl.CodeProtectedPath {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeProtectedPath)
	}
}

func TestCheckUnlinkAllowsPlainFile(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	target := impl.Info{Path: "/data/scratch.txt", UID: 1000}
	dir := impl.Info{Path: "/data", UID: 1000, IsDir: true}
	v, err := g.CheckUnlink(target, dir, 1000)
	if err != nil {
		t.Fatalf("check unlink: %v", err)
	}
	if !v.Allow {
		t.Fatalf("plain owned file must be unlinkable, code = %q", v.Code)
	}
}

func TestCheckRenameAllowsOwnedRegularFile(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	src := impl.Info{Path: "/data/a.txt", UID: 1000}
	dst := impl.Info{Path: "/data/b.txt", UID: 1000}
	v, err := g.CheckRename(src, dst, false)
	if err != nil {
		t.Fatalf("check rename: %v", err)
	}
	if !v.Allow {
		t.Fatalf("owned rename must be allowed, code = %q", v.Code)
	}
}

func TestCheckRenameRejectsProtectedDestination(t *testing.T) {
	opts := sealOptions()
	opts.Protected = []string{"/data/locked"}
	g := impl.NewHardlinkSealGuard(opts)
	src := impl.Info{Path: "/data/a.txt", UID: 1000}
	dst := impl.Info{Path: "/data/locked", UID: 1000}
	v, err := g.CheckRename(src, dst, false)
	if err != nil {
		t.Fatalf("check rename: %v", err)
	}
	if v.Allow {
		t.Fatalf("rename onto a protected destination must be denied")
	}
	if v.Code != impl.CodeProtectedPath {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeProtectedPath)
	}
}

func TestCheckRenameRejectsExchangeTypeMismatch(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	src := impl.Info{Path: "/data/a.txt", UID: 1000}
	dst := impl.Info{Path: "/data/sub", UID: 1000, IsDir: true}
	v, err := g.CheckRename(src, dst, true)
	if err != nil {
		t.Fatalf("check rename: %v", err)
	}
	if v.Allow {
		t.Fatalf("exchange between a file and a directory must be denied")
	}
	if v.Code != impl.CodeExchangeType {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeExchangeType)
	}
}

func TestCheckRenameAllowsExchangeOfRegularFiles(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	src := impl.Info{Path: "/data/a.txt", UID: 1000}
	dst := impl.Info{Path: "/data/b.txt", UID: 1000}
	v, err := g.CheckRename(src, dst, true)
	if err != nil {
		t.Fatalf("check rename: %v", err)
	}
	if !v.Allow {
		t.Fatalf("exchange of two regular files must be allowed, code = %q", v.Code)
	}
	if v.Code != impl.CodeAllow {
		t.Fatalf("verdict code = %q, want %q", v.Code, impl.CodeAllow)
	}
}

func TestExplainIncludesReasonCode(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	denied := g.Explain(impl.Verdict{Allow: false, Code: impl.CodeCrossOwner})
	if denied == "" {
		t.Fatalf("explanation for a denial must not be empty")
	}
	if !strings.Contains(denied, impl.CodeCrossOwner) {
		t.Fatalf("explanation %q does not name the reason code %q", denied, impl.CodeCrossOwner)
	}
	allowed := g.Explain(impl.Verdict{Allow: true, Code: impl.CodeAllow})
	if allowed == "" {
		t.Fatalf("explanation for an allow must not be empty")
	}
}

func TestSetProtectedRejectsBlankPath(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	if err := g.SetProtected([]string{"/data/new", "   "}); !errors.Is(err, impl.ErrBadProtected) {
		t.Fatalf("blank protected path must fail with ErrBadProtected, got %v", err)
	}
	if !g.IsProtected("/data/secret") {
		t.Fatalf("rejected replacement must keep the previous protected set")
	}
}

func TestSetProtectedReplacesWholeSet(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	if err := g.SetProtected([]string{"/data/old"}); err != nil {
		t.Fatalf("first replacement: %v", err)
	}
	if err := g.SetProtected([]string{"/data/fresh"}); err != nil {
		t.Fatalf("second replacement: %v", err)
	}
	if g.IsProtected("/data/old") {
		t.Fatalf("old path still protected after a full replacement")
	}
	if !g.IsProtected("/data/fresh") {
		t.Fatalf("new path missing from the protected set")
	}
	src := impl.Info{Path: "/data/old", UID: 1000}
	dst := impl.Info{Path: "/data", UID: 1000, IsDir: true}
	old, err := g.CheckLink(src, dst, 1000)
	if err != nil {
		t.Fatalf("check old path: %v", err)
	}
	if !old.Allow {
		t.Fatalf("released path must be linkable again, code = %q", old.Code)
	}
	fresh, err := g.CheckLink(impl.Info{Path: "/data/fresh", UID: 1000}, dst, 1000)
	if err != nil {
		t.Fatalf("check fresh path: %v", err)
	}
	if fresh.Allow || fresh.Code != impl.CodeProtectedPath {
		t.Fatalf("new protected path verdict = allow %v code %q", fresh.Allow, fresh.Code)
	}
}

func TestSnapshotRestoreKeepsProtectedSetAndRejectsCorrupt(t *testing.T) {
	g := impl.NewHardlinkSealGuard(sealOptions())
	if err := g.SetProtected([]string{"/opt/sealed"}); err != nil {
		t.Fatalf("set protected: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewHardlinkSealGuard(impl.Options{StickyEnforced: true, MaxProtected: 16})
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !other.IsProtected("/opt/sealed") {
		t.Fatalf("restored guard lost the protected path")
	}
	if got := other.Protected(); len(got) != 1 || got[0] != "/opt/sealed" {
		t.Fatalf("restored protected set = %v, want [/opt/sealed]", got)
	}
	if err := other.Restore([]byte("broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	if !other.IsProtected("/opt/sealed") {
		t.Fatalf("rejected restore must keep the live protected set")
	}
}
