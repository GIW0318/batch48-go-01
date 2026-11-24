package task

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	impl "gsb/batch48-go-safety-b-04"
)

func writeExec(t *testing.T, dir, name, content string) string {
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

func guardFor(t *testing.T, pathDirs ...string) *impl.ExecAllowlistGuard {
	t.Helper()
	if len(pathDirs) == 0 {
		pathDirs = []string{t.TempDir()}
	}
	return impl.NewExecAllowlistGuard(impl.Options{PATH: pathDirs})
}

func mustAllow(t *testing.T, g *impl.ExecAllowlistGuard, path string) impl.Entry {
	t.Helper()
	if err := g.Allow(impl.Entry{Path: path}); err != nil {
		t.Fatalf("Allow(%q): unexpected error: %v", path, err)
	}
	e, ok := g.Lookup(path)
	if !ok {
		t.Fatalf("Lookup(%q) did not find the accepted entry", path)
	}
	return e
}

func TestAllowRejectsInvalidEntries(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	if err := g.Allow(impl.Entry{Path: ""}); !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("blank entry path error = %v, want ErrEntryInvalid", err)
	}
	if err := g.Allow(impl.Entry{Path: "tool"}); !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("relative entry path error = %v, want ErrEntryInvalid", err)
	}
	if err := g.Allow(impl.Entry{Path: filepath.Join(dir, "absent")}); !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("missing file error = %v, want ErrEntryInvalid", err)
	}
	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte("no exec bit"), 0o644); err != nil {
		t.Fatalf("write plain file: %v", err)
	}
	if err := os.Chmod(plain, 0o644); err != nil {
		t.Fatalf("chmod plain file: %v", err)
	}
	if err := g.Allow(impl.Entry{Path: plain}); !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("non executable entry error = %v, want ErrEntryInvalid", err)
	}
}

func TestAllowFillsMetadataAndVersion(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	entry := mustAllow(t, g, bin)
	if entry.Version != 1 {
		t.Fatalf("first entry version = %d, want 1", entry.Version)
	}
	if entry.Inode == 0 {
		t.Fatal("entry inode was not filled from the file")
	}
	if len(entry.Digest) != 64 {
		t.Fatalf("digest length = %d, want 64 hex characters", len(entry.Digest))
	}
	if _, err := hex.DecodeString(entry.Digest); err != nil {
		t.Fatalf("digest %q is not hex: %v", entry.Digest, err)
	}
	if got := g.Entries(); len(got) != 1 || got[0].Path != bin {
		t.Fatalf("Entries() = %+v, want the accepted binary", got)
	}
}

func TestCheckAllowsListedExecutable(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	entry := mustAllow(t, g, bin)
	dec, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("Check denied the listed binary with reason %q", dec.Reason)
	}
	if dec.Reason != impl.ReasonAllowed {
		t.Fatalf("decision reason = %q, want %q", dec.Reason, impl.ReasonAllowed)
	}
	if dec.Version != entry.Version {
		t.Fatalf("decision version = %d, want the matched entry version %d", dec.Version, entry.Version)
	}
	if dec.Entry.Path != bin {
		t.Fatalf("matched entry path = %q, want %q", dec.Entry.Path, bin)
	}
}

func TestCheckDeniesUnlistedExecutable(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	writeExec(t, dir, "allowed", "allowed")
	rogue := writeExec(t, dir, "rogue", "rogue")
	dec, err := g.Check(impl.ExecRequest{Path: rogue, Argv: []string{rogue}})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Fatal("Check allowed a binary that was never added")
	}
	if dec.Reason != impl.ReasonNotListed {
		t.Fatalf("deny reason = %q, want %q", dec.Reason, impl.ReasonNotListed)
	}
}

func TestCheckRejectsArgv0Mismatch(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	mustAllow(t, g, bin)
	dec, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{"/usr/bin/other"}})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Fatal("Check allowed a request whose argv[0] differs from the real path")
	}
	if dec.Reason != impl.ReasonArgvMismatch {
		t.Fatalf("deny reason = %q, want %q", dec.Reason, impl.ReasonArgvMismatch)
	}
}

func TestCheckUsesServerPATHAndIgnoresCallerEnv(t *testing.T) {
	serverDir := t.TempDir()
	envDir := t.TempDir()
	g := guardFor(t, serverDir)
	serverTool := writeExec(t, serverDir, "tool", "server-copy")
	writeExec(t, envDir, "tool", "env-copy")
	mustAllow(t, g, serverTool)

	dec, err := g.Check(impl.ExecRequest{
		Path: "tool",
		Argv: []string{serverTool},
		Env:  []string{"PATH=" + envDir},
	})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("Check denied the server PATH lookup with reason %q", dec.Reason)
	}
	if dec.Entry.Path != serverTool {
		t.Fatalf("matched entry = %q, want the server PATH copy %q", dec.Entry.Path, serverTool)
	}
}

func TestCheckRejectsScriptWithMissingInterpreter(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	script := writeExec(t, dir, "job.sh", "#!/missing/interpreter\n")
	mustAllow(t, g, script)
	dec, err := g.Check(impl.ExecRequest{Path: script, Argv: []string{script}})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Fatal("Check allowed a script whose interpreter is absent from the allowlist")
	}
	if dec.Reason != impl.ReasonInterpreterMissing {
		t.Fatalf("deny reason = %q, want %q", dec.Reason, impl.ReasonInterpreterMissing)
	}
}

func TestCheckAllowsScriptWithListedInterpreter(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	interp := writeExec(t, dir, "interp", "interpreter-binary")
	script := writeExec(t, dir, "job.sh", "#!"+interp+"\n")
	mustAllow(t, g, interp)
	mustAllow(t, g, script)
	dec, err := g.Check(impl.ExecRequest{Path: script, Argv: []string{script}})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("Check denied a script whose interpreter is listed, reason %q", dec.Reason)
	}
	if dec.Entry.Path != script {
		t.Fatalf("matched entry = %q, want the script %q", dec.Entry.Path, script)
	}
}

func TestCheckDeniesUnresolvableBareName(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	dec, err := g.Check(impl.ExecRequest{Path: "no-such-tool", Argv: []string{"no-such-tool"}})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Fatal("Check allowed a bare name that the server PATH cannot resolve")
	}
	if dec.Reason != impl.ReasonNotListed {
		t.Fatalf("deny reason = %q, want %q", dec.Reason, impl.ReasonNotListed)
	}
}

func TestReloadReplacesTableAndBumpsVersions(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	oldBin := writeExec(t, dir, "old", "old-binary")
	newBin := writeExec(t, dir, "new", "new-binary")
	oldEntry := mustAllow(t, g, oldBin)

	if err := g.Reload([]impl.Entry{{Path: newBin}}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	entries := g.Entries()
	if len(entries) != 1 || entries[0].Path != newBin {
		t.Fatalf("Entries() after reload = %+v, want only the new binary", entries)
	}
	if entries[0].Version <= oldEntry.Version {
		t.Fatalf("reloaded version = %d, want greater than %d", entries[0].Version, oldEntry.Version)
	}
	if got := g.Stats().Reloads; got != 1 {
		t.Fatalf("Stats.Reloads = %d, want 1", got)
	}
	stale, err := g.Check(impl.ExecRequest{Path: oldBin, Argv: []string{oldBin}})
	if err != nil {
		t.Fatalf("Check old binary: %v", err)
	}
	if stale.Allowed {
		t.Fatal("Check still allowed an entry removed by reload")
	}
	fresh, err := g.Check(impl.ExecRequest{Path: newBin, Argv: []string{newBin}})
	if err != nil {
		t.Fatalf("Check new binary: %v", err)
	}
	if !fresh.Allowed {
		t.Fatalf("Check denied the reloaded entry, reason %q", fresh.Reason)
	}
}

func TestReloadRejectsInvalidEntriesAndKeepsOldTable(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	mustAllow(t, g, bin)
	err := g.Reload([]impl.Entry{{Path: filepath.Join(dir, "missing")}})
	if !errors.Is(err, impl.ErrEntryInvalid) {
		t.Fatalf("reload with a missing entry error = %v, want ErrEntryInvalid", err)
	}
	dec, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("old table was lost after a rejected reload, reason %q", dec.Reason)
	}
}

func TestStatsCountsChecksAndDenials(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	rogue := writeExec(t, dir, "rogue", "rogue-binary")
	mustAllow(t, g, bin)

	if _, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}}); err != nil {
		t.Fatalf("Check allowed: %v", err)
	}
	if _, err := g.Check(impl.ExecRequest{Path: bin, Argv: []string{"/bin/other"}}); err != nil {
		t.Fatalf("Check argv mismatch: %v", err)
	}
	if _, err := g.Check(impl.ExecRequest{Path: rogue, Argv: []string{rogue}}); err != nil {
		t.Fatalf("Check rogue: %v", err)
	}
	if err := g.Record(impl.ExecRequest{Path: bin, Argv: []string{bin}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	stats := g.Stats()
	if stats.Checks != 3 || stats.Allowed != 1 || stats.Denied != 2 || stats.Recorded != 1 {
		t.Fatalf("Stats = %+v, want 3 checks, 1 allowed, 2 denied and 1 recorded", stats)
	}
}

func TestRecordRequiresAllowedRequest(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	rogue := writeExec(t, dir, "rogue", "rogue-binary")
	if err := g.Record(impl.ExecRequest{Path: rogue, Argv: []string{rogue}}); !errors.Is(err, impl.ErrNotAllowed) {
		t.Fatalf("Record of an unlisted request error = %v, want ErrNotAllowed", err)
	}
	if got := g.Stats().Recorded; got != 0 {
		t.Fatalf("Stats.Recorded = %d after a rejected record, want 0", got)
	}
}

func TestSnapshotRestoreKeepsEntries(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	entry := mustAllow(t, g, bin)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("Snapshot returned an empty payload")
	}

	restored := guardFor(t, dir)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, ok := restored.Lookup(bin)
	if !ok {
		t.Fatalf("Lookup(%q) missing after restore", bin)
	}
	if got.Version != entry.Version || got.Digest != entry.Digest {
		t.Fatalf("restored entry = %+v, want version %d and the same digest", got, entry.Version)
	}
	dec, err := restored.Check(impl.ExecRequest{Path: bin, Argv: []string{bin}})
	if err != nil {
		t.Fatalf("Check after restore: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("Check denied the restored entry, reason %q", dec.Reason)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	dir := t.TempDir()
	g := guardFor(t, dir)
	bin := writeExec(t, dir, "tool", "tool-binary")
	mustAllow(t, g, bin)
	if err := g.Restore([]byte("[]not-json")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot error = %v, want ErrBadSnapshot", err)
	}
	if _, ok := g.Lookup(bin); !ok {
		t.Fatal("entry table was disturbed by a rejected snapshot")
	}
}
