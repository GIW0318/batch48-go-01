package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	impl "gsb/batch48-go-safety-b-04"
)

// Self contained helpers (x prefix) so this file never depends on helpers
// defined in the provided test file.

func xWriteExec(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
	return p
}

func xNewGuard(dirs ...string) *impl.ExecAllowlistGuard {
	if len(dirs) == 0 {
		dirs = []string{}
	}
	return impl.NewExecAllowlistGuard(impl.Options{PATH: dirs})
}

func xReloadPaths(t *testing.T, g *impl.ExecAllowlistGuard, paths ...string) {
	t.Helper()
	entries := make([]impl.Entry, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, impl.Entry{Path: p})
	}
	if err := g.Reload(entries); err != nil {
		t.Fatalf("Reload(%v): %v", paths, err)
	}
}

func xCheckAllowed(t *testing.T, g *impl.ExecAllowlistGuard, p string) impl.Decision {
	t.Helper()
	dec, err := g.Check(impl.ExecRequest{Path: p, Argv: []string{p}})
	if err != nil {
		t.Fatalf("Check(%q) unexpected error: %v", p, err)
	}
	if !dec.Allowed {
		t.Fatalf("Check(%q) denied with reason %q detail %q", p, dec.Reason, dec.Detail)
	}
	return dec
}

func xFileDigest(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func xInode(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	return xInodeOf(fi)
}

func xInodeOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func TestExtraInPlaceRewriteRejectedByDigest(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "version-one")
	if err := g.Allow(impl.Entry{Path: bin}); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	listed, _ := g.Lookup(bin)
	before := xInode(t, bin)
	if before != listed.Inode {
		t.Fatal("test setup: live inode differs from listed inode")
	}

	// In place truncate+write keeps path and inode, changes content.
	if err := os.WriteFile(bin, []byte("version-two-tampered"), 0o755); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	after := xInode(t, bin)
	if after != before {
		t.Fatalf("test setup changed inode %d -> %d; wanted stable inode", before, after)
	}

	dec, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonMetadataDrift {
		t.Fatalf("decision = %+v, want metadata-drift denial", dec)
	}
	if dec.Detail != impl.DetailDigestMismatch {
		t.Fatalf("detail = %q, want %q", dec.Detail, impl.DetailDigestMismatch)
	}
	if dec.Version != listed.Version || dec.Entry.Inode != listed.Inode {
		t.Fatalf("decision must carry the hit entry/version, got %+v", dec)
	}
}

func TestExtraRenameSameContentRejectedByInode(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)

	listedPath := xWriteExec(t, dir, "tool", "identical-bytes")
	if err := g.Allow(impl.Entry{Path: listedPath}); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	entry, _ := g.Lookup(listedPath)

	staged := xWriteExec(t, dir, "staged-copy", "identical-bytes")
	if xInode(t, staged) == entry.Inode {
		t.Fatal("test setup: staged file unexpectedly shares the inode")
	}
	if xFileDigest(t, staged) != entry.Digest {
		t.Fatal("test setup: staged content must have the same digest")
	}

	// Rename the same content object onto the listed path.
	if err := os.Rename(staged, listedPath); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if xInode(t, listedPath) == entry.Inode {
		t.Fatal("test setup: rename did not replace the inode")
	}
	if xFileDigest(t, listedPath) != entry.Digest {
		t.Fatal("test setup: digest should still match for same content")
	}

	dec, err := g.Check(impl.ExecRequest{Path: listedPath, Argv: []string{listedPath}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonMetadataDrift {
		t.Fatalf("decision = %+v, want metadata-drift denial", dec)
	}
	if dec.Detail != impl.DetailInodeMismatch {
		t.Fatalf("detail = %q, want independent inode cause %q", dec.Detail, impl.DetailInodeMismatch)
	}

	// Admission path reports its own sentinel cause for the stale inode.
	err = g.Allow(impl.Entry{Path: listedPath, Inode: entry.Inode, Digest: entry.Digest})
	if !errors.Is(err, impl.ErrEntryMismatch) {
		t.Fatalf("Allow with stale inode error = %v, want ErrEntryMismatch", err)
	}
	current, ok := g.Lookup(listedPath)
	if !ok || current.Version != entry.Version {
		t.Fatalf("failed mismatch Allow must not touch the table, got %+v ok=%v", current, ok)
	}
}

func TestExtraExplicitMetadataMismatchReported(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "metadata-bytes")

	if err := g.Allow(impl.Entry{Path: bin, Inode: 999999}); !errors.Is(err, impl.ErrEntryMismatch) {
		t.Fatalf("bogus inode error = %v, want ErrEntryMismatch", err)
	}
	if _, ok := g.Lookup(bin); ok {
		t.Fatal("entry must not be registered after an inode mismatch")
	}
	if err := g.Allow(impl.Entry{Path: bin, Digest: fmt.Sprintf("%064x", 1)}); !errors.Is(err, impl.ErrEntryMismatch) {
		t.Fatalf("bogus digest error = %v, want ErrEntryMismatch", err)
	}
	if _, ok := g.Lookup(bin); ok {
		t.Fatal("entry must not be registered after a digest mismatch")
	}

	realInode := xInode(t, bin)
	realDigest := xFileDigest(t, bin)
	if err := g.Allow(impl.Entry{Path: bin, Inode: realInode, Digest: realDigest}); err != nil {
		t.Fatalf("Allow with correct explicit metadata: %v", err)
	}
	got, ok := g.Lookup(bin)
	if !ok || got.Inode != realInode || got.Digest != realDigest {
		t.Fatalf("stored entry = %+v ok=%v, want matching metadata", got, ok)
	}
}

func TestExtraSymlinkAndDirectoryNotEntries(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "link-target")
	link := filepath.Join(dir, "tool-link")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := g.Allow(impl.Entry{Path: link}); !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("Allow(symlink) = %v, want ErrEntryInvalid", err)
	}
	if err := g.Allow(impl.Entry{Path: dir}); !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("Allow(directory) = %v, want ErrEntryInvalid", err)
	}
}

func TestExtraBareNameRequiresResolvedArgv0(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "bare-bytes")
	if err := g.Allow(impl.Entry{Path: bin}); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	dec, err := g.Check(impl.ExecRequest{Path: "tool", Argv: []string{"tool"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonArgvMismatch {
		t.Fatalf("bare argv0 decision = %+v, want argv0-mismatch against resolved path", dec)
	}

	dec = xCheckAllowed(t, g, bin)
	if dec.Entry.Path != bin {
		t.Fatalf("absolute check matched %q, want %q", dec.Entry.Path, bin)
	}
}

func TestExtraUnlistedExistingInterpreterRejected(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	interp := xWriteExec(t, dir, "interp", "interp-bytes")
	script := xWriteExec(t, dir, "job.sh", "#!"+interp+"\n")
	if err := g.Allow(impl.Entry{Path: script}); err != nil {
		t.Fatalf("Allow script: %v", err)
	}

	dec, err := g.Check(impl.ExecRequest{Path: script, Argv: []string{script}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonInterpreterMissing {
		t.Fatalf("decision = %+v, want interpreter-missing", dec)
	}
	if dec.Detail != impl.DetailInterpreterNotListed {
		t.Fatalf("detail = %q, want %q", dec.Detail, impl.DetailInterpreterNotListed)
	}
}

func TestExtraEnvTwoStageInterpreterChain(t *testing.T) {
	serverDir := t.TempDir()
	evilDir := t.TempDir()
	g := xNewGuard(serverDir)

	envLauncher := xWriteExec(t, serverDir, "env", "env-launcher-bytes")
	python := xWriteExec(t, serverDir, "xzpython", "real-interpreter")
	evilPython := xWriteExec(t, evilDir, "xzpython", "attacker-interpreter")

	absTargetScript := xWriteExec(t, serverDir, "abs-job.sh",
		"#!"+envLauncher+" "+python+"\n")
	bareTargetScript := xWriteExec(t, serverDir, "bare-job.sh",
		"#!"+envLauncher+" xzpython\n")

	if err := g.Allow(impl.Entry{Path: envLauncher}); err != nil {
		t.Fatalf("Allow env: %v", err)
	}
	if err := g.Allow(impl.Entry{Path: python}); err != nil {
		t.Fatalf("Allow python: %v", err)
	}
	if err := g.Allow(impl.Entry{Path: absTargetScript}); err != nil {
		t.Fatalf("Allow abs script: %v", err)
	}
	if err := g.Allow(impl.Entry{Path: bareTargetScript}); err != nil {
		t.Fatalf("Allow bare script: %v", err)
	}

	xCheckAllowed(t, g, absTargetScript)

	// Caller controlled PATH must not redirect the env target resolution.
	dec, err := g.Check(impl.ExecRequest{
		Path: bareTargetScript,
		Argv: []string{bareTargetScript},
		Env:  []string{"PATH=" + evilDir},
	})
	if err != nil {
		t.Fatalf("Check bare env script: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("env two stage script denied: reason %q detail %q", dec.Reason, dec.Detail)
	}

	// Validating env alone is not enough: revoking the real target denies.
	if err := g.Revoke(python); err != nil {
		t.Fatalf("Revoke python: %v", err)
	}
	dec, err = g.Check(impl.ExecRequest{Path: absTargetScript, Argv: []string{absTargetScript}})
	if err != nil {
		t.Fatalf("Check after revoke: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonInterpreterMissing {
		t.Fatalf("decision after revoking real interpreter = %+v, want interpreter-missing", dec)
	}

	// The evil caller PATH copy was never registered.
	if _, ok := g.Lookup(evilPython); ok {
		t.Fatal("evil interpreter should not be registered")
	}
}

func TestExtraEnvWithoutTargetIsRejected(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	envLauncher := xWriteExec(t, dir, "env", "env-bytes")
	script := xWriteExec(t, dir, "job.sh", "#!"+envLauncher+"\n")
	if err := g.Reload([]impl.Entry{{Path: envLauncher}, {Path: script}}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	dec, err := g.Check(impl.ExecRequest{Path: script, Argv: []string{script}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonInterpreterMissing || dec.Detail != impl.DetailEnvTargetMissing {
		t.Fatalf("decision = %+v, want interpreter-missing/%s", dec, impl.DetailEnvTargetMissing)
	}
}

func TestExtraInterpreterDepthBound(t *testing.T) {
	dir := t.TempDir()
	base := xWriteExec(t, dir, "xz_i0", "base-binary")
	i1 := xWriteExec(t, dir, "xz_i1", "#!"+base+"\n")

	shallowTop := xWriteExec(t, dir, "xz_top_shallow", "#!"+i1+"\n")

	// depth 1: top -> i1 -> base needs two interpreter edges, rejected.
	strict := impl.NewExecAllowlistGuard(impl.Options{PATH: []string{dir}, MaxInterpreterDepth: 1})
	xReloadPaths(t, strict, shallowTop, i1, base)
	dec, err := strict.Check(impl.ExecRequest{Path: shallowTop, Argv: []string{shallowTop}})
	if err != nil {
		t.Fatalf("strict Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonInterpreterMissing || dec.Detail != impl.DetailInterpreterDepth {
		t.Fatalf("strict decision = %+v, want interpreter-missing/depth", dec)
	}

	// depth 2 accepts exactly those two edges.
	loose := impl.NewExecAllowlistGuard(impl.Options{PATH: []string{dir}, MaxInterpreterDepth: 2})
	xReloadPaths(t, loose, shallowTop, i1, base)
	xCheckAllowed(t, loose, shallowTop)

	// A longer chain blows past depth 2.
	i2 := xWriteExec(t, dir, "xz_i2", "#!"+i1+"\n")
	i3 := xWriteExec(t, dir, "xz_i3", "#!"+i2+"\n")
	deepTop := xWriteExec(t, dir, "xz_top_deep", "#!"+i3+"\n")
	bounded := impl.NewExecAllowlistGuard(impl.Options{PATH: []string{dir}, MaxInterpreterDepth: 2})
	xReloadPaths(t, bounded, deepTop, i3, i2, i1, base)
	dec, err = bounded.Check(impl.ExecRequest{Path: deepTop, Argv: []string{deepTop}})
	if err != nil {
		t.Fatalf("bounded Check: %v", err)
	}
	if dec.Allowed || dec.Reason != impl.ReasonInterpreterMissing || dec.Detail != impl.DetailInterpreterDepth {
		t.Fatalf("deep decision = %+v, want interpreter-missing/depth", dec)
	}
}

func TestExtraReplacedInterpreterDeniesScript(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	interp := xWriteExec(t, dir, "interp", "interpreter-v1")
	script := xWriteExec(t, dir, "job.sh", "#!"+interp+"\n")
	if err := g.Reload([]impl.Entry{{Path: interp}, {Path: script}}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	xCheckAllowed(t, g, script)

	if err := os.WriteFile(interp, []byte("interpreter-v2-evil"), 0o755); err != nil {
		t.Fatalf("replace interpreter: %v", err)
	}
	if err := os.Chmod(interp, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	dec, err := g.Check(impl.ExecRequest{Path: script, Argv: []string{script}})
	if err != nil {
		t.Fatalf("Check after interpreter replacement: %v", err)
	}
	if dec.Allowed {
		t.Fatalf("script allowed after its interpreter changed: %+v", dec)
	}
	if dec.Reason != impl.ReasonMetadataDrift {
		t.Fatalf("reason = %q detail %q, want metadata-drift for the drifted interpreter", dec.Reason, dec.Detail)
	}
	if dec.Detail != impl.DetailInterpreterDigestDrift {
		t.Fatalf("detail = %q, want %q", dec.Detail, impl.DetailInterpreterDigestDrift)
	}
}

func TestExtraRevokeAndSortedEntries(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	c := xWriteExec(t, dir, "c-tool", "c")
	a := xWriteExec(t, dir, "a-tool", "a")
	b := xWriteExec(t, dir, "b-tool", "b")
	if err := g.Reload([]impl.Entry{{Path: c}, {Path: a}, {Path: b}}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	entries := g.Entries()
	if len(entries) != 3 || entries[0].Path != a || entries[1].Path != b || entries[2].Path != c {
		t.Fatalf("Entries not path sorted: %+v", entries)
	}

	if err := g.Revoke(b); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, ok := g.Lookup(b); ok {
		t.Fatal("Lookup still finds revoked entry")
	}
	if got := g.Entries(); len(got) != 2 || got[0].Path != a || got[1].Path != c {
		t.Fatalf("Entries after revoke = %+v", got)
	}
	if err := g.Revoke(b); !errors.Is(err, impl.ErrEntryNotFound) {
		t.Fatalf("second Revoke error = %v, want ErrEntryNotFound", err)
	}
}

func TestExtraReloadHotSwapDecisionConsistent(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "stable-bytes")
	if err := g.Allow(impl.Entry{Path: bin}); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	before := xCheckAllowed(t, g, bin)
	if before.Version != 1 {
		t.Fatalf("initial version = %d, want 1", before.Version)
	}

	// Hot reload of the same on disk binary bumps the table/entry version but
	// keeps the decision allowed.
	if err := g.Reload([]impl.Entry{{Path: bin}}); err != nil {
		t.Fatalf("Reload same: %v", err)
	}
	after, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}})
	if err != nil {
		t.Fatalf("Check after reload: %v", err)
	}
	if !after.Allowed || after.Reason != impl.ReasonAllowed {
		t.Fatalf("post reload decision = %+v, want allowed", after)
	}
	listed, ok := g.Lookup(bin)
	if !ok || after.Version != listed.Version {
		t.Fatalf("decision version %d must match current Lookup version %+v", after.Version, listed)
	}
	if after.Version <= before.Version {
		t.Fatalf("reloaded version %d must exceed %d", after.Version, before.Version)
	}

	// Reload to a different table immediately invalidates the old entry.
	other := xWriteExec(t, dir, "other", "other-bytes")
	if err := g.Reload([]impl.Entry{{Path: other}}); err != nil {
		t.Fatalf("Reload other: %v", err)
	}
	stale, _ := g.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}})
	if stale.Allowed || stale.Reason != impl.ReasonNotListed {
		t.Fatalf("old entry after replacement = %+v, want not-listed", stale)
	}
	fresh := xCheckAllowed(t, g, other)
	if fresh.Version <= after.Version {
		t.Fatalf("new table version %d should keep increasing over %d", fresh.Version, after.Version)
	}
}

func TestExtraSnapshotOnNewGuardContinuesDecisions(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	a := xWriteExec(t, dir, "a", "aaa")
	b := xWriteExec(t, dir, "b", "bbb")
	if err := g.Allow(impl.Entry{Path: a}); err != nil {
		t.Fatalf("Allow a: %v", err)
	}
	if err := g.Allow(impl.Entry{Path: b}); err != nil {
		t.Fatalf("Allow b: %v", err)
	}
	if _, err := g.Check(impl.ExecRequest{Path: a, Argv: []string{a}}); err != nil {
		t.Fatalf("Check a: %v", err)
	}
	if err := g.Record(impl.ExecRequest{Path: a, Argv: []string{a}}); err != nil {
		t.Fatalf("Record a: %v", err)
	}

	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restarted := xNewGuard(dir)
	if err := restarted.Restore(snap); err != nil {
		t.Fatalf("Restore on new guard: %v", err)
	}

	wantEntries := g.Entries()
	gotEntries := restarted.Entries()
	if len(gotEntries) != len(wantEntries) {
		t.Fatalf("restored entries = %+v, want %+v", gotEntries, wantEntries)
	}
	for i := range wantEntries {
		if gotEntries[i] != wantEntries[i] {
			t.Fatalf("entry %d mismatch: got %+v want %+v", i, gotEntries[i], wantEntries[i])
		}
	}

	xCheckAllowed(t, restarted, a)
	xCheckAllowed(t, restarted, b)

	// Version sequence continues after restore instead of reusing 1/2.
	c := xWriteExec(t, dir, "c", "ccc")
	if err := restarted.Allow(impl.Entry{Path: c}); err != nil {
		t.Fatalf("Allow c after restore: %v", err)
	}
	gotC, ok := restarted.Lookup(c)
	if !ok || gotC.Version != 3 {
		t.Fatalf("post restore entry version = %+v ok=%v, want version 3", gotC, ok)
	}
	xCheckAllowed(t, restarted, c)

	// A second snapshot/restore hop keeps the same decisions.
	snap2, err := restarted.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot2: %v", err)
	}
	third := xNewGuard(dir)
	if err := third.Restore(snap2); err != nil {
		t.Fatalf("Restore2: %v", err)
	}
	xCheckAllowed(t, third, c)
	// 1 check on g, 3 on the restarted guard (a, b, c) and 1 more on the
	// third generation; the recorded call travelled through both snapshots.
	if st := third.Stats(); st.Recorded != 1 || st.Checks != 5 || st.Allowed != 5 {
		t.Fatalf("restored stats = %+v, want 5 checks/allows and 1 recorded", st)
	}
}

func TestExtraRestoreRejectsMultipleBadPayloads(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "keep-me")
	if err := g.Allow(impl.Entry{Path: bin}); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	bad := [][]byte{
		nil,
		[]byte("not json at all"),
		[]byte("[]not-json"),
		[]byte("{}"),
		[]byte(`{"format":"wrong","entries":[]}`),
		[]byte(`{"format":"exec-allowlist-guard/snapshot-v1","entries":[{"Path":"relative","Inode":1,"Digest":"` +
			"0000000000000000000000000000000000000000000000000000000000000000" + `","Version":1}]}`),
	}
	for i, payload := range bad {
		if err := g.Restore(payload); !errors.Is(err, impl.ErrBadSnapshot) {
			t.Fatalf("bad payload %d error = %v, want ErrBadSnapshot", i, err)
		}
		if _, ok := g.Lookup(bin); !ok {
			t.Fatalf("table disturbed by bad payload %d", i)
		}
	}
	xCheckAllowed(t, g, bin)
}

func TestExtraTamperedSnapshotRejected(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "snapshot-tamper")
	if err := g.Allow(impl.Entry{Path: bin}); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(snap, &doc); err != nil {
		t.Fatalf("unmarshal own snapshot: %v", err)
	}
	entries, ok := doc["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("snapshot entries shape: %#v", doc["entries"])
	}
	first := entries[0].(map[string]any)
	first["Digest"] = "0000000000000000000000000000000000000000000000000000000000000000"
	tampered, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}

	restarted := xNewGuard(dir)
	if err := restarted.Restore(tampered); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("Restore(tampered) = %v, want ErrBadSnapshot", err)
	}
	if _, ok := restarted.Lookup(bin); ok {
		t.Fatal("failed restore must leave the new guard table empty, not half populated")
	}

	// Original snapshot still restores cleanly.
	if err := restarted.Restore(snap); err != nil {
		t.Fatalf("Restore(original): %v", err)
	}
	xCheckAllowed(t, restarted, bin)
}

func TestExtraFailedReloadDoesNotCountOrBump(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)
	bin := xWriteExec(t, dir, "tool", "reload-me")
	if err := g.Allow(impl.Entry{Path: bin}); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	before, _ := g.Lookup(bin)

	err := g.Reload([]impl.Entry{{Path: filepath.Join(dir, "missing")}})
	if !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("bad Reload error = %v, want ErrEntryInvalid", err)
	}
	if st := g.Stats(); st.Reloads != 0 {
		t.Fatalf("Reloads = %d after rejected reload, want 0", st.Reloads)
	}
	after, _ := g.Lookup(bin)
	if after.Version != before.Version {
		t.Fatal("rejected reload changed the live entry version")
	}
	xCheckAllowed(t, g, bin)
}

func TestExtraConcurrentCheckReloadSnapshot(t *testing.T) {
	dir := t.TempDir()
	g := xNewGuard(dir)

	const k = 4
	setA := make([]string, k)
	setB := make([]string, k)
	for i := 0; i < k; i++ {
		setA[i] = xWriteExec(t, dir, fmt.Sprintf("xa%d", i), fmt.Sprintf("a-content-%d", i))
		setB[i] = xWriteExec(t, dir, fmt.Sprintf("xb%d", i), fmt.Sprintf("b-content-%d", i))
	}

	isA := func(p string) bool {
		return len(filepath.Base(p)) >= 2 && filepath.Base(p)[:2] == "xa"
	}

	xReloadPaths(t, g, setA...)

	start := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: whole table swaps between two fixed size revisions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 150; i++ {
			paths := setA
			if i%2 == 0 {
				paths = setB
			}
			entries := make([]impl.Entry, 0, k)
			for _, p := range paths {
				entries = append(entries, impl.Entry{Path: p})
			}
			if err := g.Reload(entries); err != nil {
				t.Errorf("concurrent Reload: %v", err)
				return
			}
		}
	}()

	// Readers: every observed decision must be internally consistent.
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < 300; i++ {
				p := setA[(id+i)%k]
				if (id+i)%2 == 0 {
					p = setB[(id+i)%k]
				}
				dec, err := g.Check(impl.ExecRequest{Path: p, Argv: []string{p}})
				if err != nil {
					t.Errorf("concurrent Check: %v", err)
					return
				}
				if dec.Entry.Version != dec.Version {
					t.Errorf("decision version drift: %+v", dec)
					return
				}
				if dec.Allowed {
					if dec.Reason != impl.ReasonAllowed {
						t.Errorf("allowed decision with reason %q", dec.Reason)
						return
					}
					continue
				}
				// Files never change, so the only safe denial across a swap is
				// that the path belongs to the other complete revision.
				if dec.Reason != impl.ReasonNotListed {
					t.Errorf("unexpected concurrent denial %q/%q for %s", dec.Reason, dec.Detail, p)
					return
				}
			}
		}(worker)
	}

	// Listers: a loaded table is either pure A or pure B, never a half mix.
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 300; i++ {
				entries := g.Entries()
				if len(entries) != k {
					t.Errorf("observed half table size %d, want %d", len(entries), k)
					return
				}
				wantA := isA(entries[0].Path)
				for _, e := range entries {
					if isA(e.Path) != wantA {
						t.Errorf("observed mixed half table: %+v", entries)
						return
					}
					if e.Version < 1 {
						t.Errorf("entry without version: %+v", e)
						return
					}
				}
			}
		}()
	}

	// Snapshotters exercise mutual exclusion with all swaps.
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				payload, err := g.Snapshot()
				if err != nil || len(payload) == 0 {
					t.Errorf("concurrent Snapshot: %v len=%d", err, len(payload))
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	st := g.Stats()
	if st.Checks != st.Allowed+st.Denied {
		t.Fatalf("stats calibre broken: %+v", st)
	}
	if st.Reloads != 151 {
		t.Fatalf("Reloads = %d, want initial reload plus 150 iterations (151)", st.Reloads)
	}
}
