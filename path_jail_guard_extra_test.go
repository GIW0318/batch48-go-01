package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func extraWriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func extraJail(t *testing.T, roots Roots) *PathJailGuard {
	t.Helper()
	jail, err := NewPathJailGuard(roots)
	if err != nil {
		t.Fatalf("NewPathJailGuard: %v", err)
	}
	return jail
}

func TestExtraSymlinkCycleStopsAtHopLimit(t *testing.T) {
	dir := t.TempDir()
	jail := extraJail(t, Roots{{Virtual: "/data", Path: dir, Tenant: "t1"}})
	if err := os.Symlink("b", filepath.Join(dir, "a")); err != nil {
		t.Fatalf("symlink a: %v", err)
	}
	if err := os.Symlink("a", filepath.Join(dir, "b")); err != nil {
		t.Fatalf("symlink b: %v", err)
	}

	_, err := jail.Resolve("/data/a", OpRead)
	if !errors.Is(err, ErrTooManyLinks) {
		t.Fatalf("cycle error = %v, want ErrTooManyLinks", err)
	}
	if err == nil || !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("cycle error %q does not carry loop fragments a/b", err)
	}
}

func TestExtraSymlinkHopBoundary(t *testing.T) {
	buildChain := func(links int) (*PathJailGuard, string) {
		dir := t.TempDir()
		extraWriteFile(t, dir, "real.txt", "real")
		for i := 0; i < links; i++ {
			name := filepath.Join(dir, fmt.Sprintf("link-%d", i))
			target := "real.txt"
			if i+1 < links {
				target = fmt.Sprintf("link-%d", i+1)
			}
			if err := os.Symlink(target, name); err != nil {
				t.Fatalf("symlink %d: %v", i, err)
			}
		}
		return extraJail(t, Roots{{Virtual: "/data", Path: dir}}), dir
	}

	eight, dir := buildChain(MaxSymlinkHops)
	got, err := eight.Resolve("/data/link-0", OpRead)
	if err != nil {
		t.Fatalf("resolution with exactly %d hops: %v", MaxSymlinkHops, err)
	}
	if want := filepath.Join(dir, "real.txt"); got.Real != want {
		t.Fatalf("Real = %q, want %q", got.Real, want)
	}

	nine, _ := buildChain(MaxSymlinkHops + 1)
	if _, err := nine.Resolve("/data/link-0", OpRead); !errors.Is(err, ErrTooManyLinks) {
		t.Fatalf("resolution with %d hops error = %v, want ErrTooManyLinks", MaxSymlinkHops+1, err)
	}
}

func TestExtraNestedMissingWriteFallsBackToLastExistingParent(t *testing.T) {
	dir := t.TempDir()
	jail := extraJail(t, Roots{{Virtual: "/data", Path: dir, Tenant: "t1"}})
	existing := filepath.Join(dir, "a")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := jail.Resolve("/data/a/missing/tree/file.txt", OpWrite)
	if err != nil {
		t.Fatalf("Resolve nested missing write: %v", err)
	}
	if got.Exists {
		t.Fatal("nested missing target reported Exists=true")
	}
	if got.Parent != existing {
		t.Fatalf("Parent = %q, want last existing directory %q", got.Parent, existing)
	}
	wantReal := filepath.Join(existing, "missing", "tree", "file.txt")
	if got.Real != wantReal {
		t.Fatalf("Real = %q, want %q", got.Real, wantReal)
	}
}

func TestExtraHardlinkOutsideReadAllowedWriteRejected(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := extraWriteFile(t, outsideDir, "outside.txt", "outside")
	insideLink := filepath.Join(dir, "hard.txt")
	if err := os.Link(outsideFile, insideLink); err != nil {
		t.Fatalf("link outside file into jail: %v", err)
	}

	jail := extraJail(t, Roots{{Virtual: "/data", Path: dir, Tenant: "t1"}})
	got, err := jail.Resolve("/data/hard.txt", OpRead)
	if err != nil {
		t.Fatalf("read hard link to outside inode: %v", err)
	}
	if !got.Exists {
		t.Fatal("hard link target reported missing")
	}
	if _, err := jail.Resolve("/data/hard.txt", OpWrite); !errors.Is(err, ErrEscaped) {
		t.Fatalf("write hard link to outside inode error = %v, want ErrEscaped", err)
	}

	internalOriginal := extraWriteFile(t, dir, "original.txt", "inside")
	internalLink := filepath.Join(dir, "internal-hard.txt")
	if err := os.Link(internalOriginal, internalLink); err != nil {
		t.Fatalf("create internal hard link: %v", err)
	}
	if _, err := jail.Resolve("/data/internal-hard.txt", OpWrite); err != nil {
		t.Fatalf("write through an internal hard link: %v", err)
	}

	if err := os.Remove(outsideFile); err != nil {
		t.Fatalf("remove outside directory entry: %v", err)
	}
	if _, err := jail.Resolve("/data/hard.txt", OpWrite); err != nil {
		t.Fatalf("write after external link count disappeared: %v", err)
	}
}

func TestExtraSetRootsRejectsBadSetAndKeepsOld(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	extraWriteFile(t, first, "f.txt", "one")
	extraWriteFile(t, second, "f.txt", "two")
	jail := extraJail(t, Roots{{Virtual: "/data", Path: first, Tenant: "t1"}})

	if err := jail.SetRoots([]Root{{Virtual: "/data", Path: "relative"}}); !errors.Is(err, ErrBadRoots) {
		t.Fatalf("bad SetRoots error = %v, want ErrBadRoots", err)
	}
	if got, err := jail.Resolve("/data/f.txt", OpRead); err != nil || got.Real != filepath.Join(first, "f.txt") {
		t.Fatalf("old root after bad swap = %+v, err %v", got, err)
	}

	if err := jail.SetRoots(nil); !errors.Is(err, ErrBadRoots) {
		t.Fatalf("nil SetRoots error = %v, want ErrBadRoots", err)
	}
	if jail.ReadOnly("/data/f.txt") {
		t.Fatal("old writable root changed after rejected swap")
	}
}

func TestExtraLongestSegmentPrefixDoesNotTreatSiblingAsNested(t *testing.T) {
	dataDir := t.TempDir()
	databaseDir := t.TempDir()
	extraWriteFile(t, databaseDir, "db.txt", "db")
	jail := extraJail(t, Roots{
		{Virtual: "/data", Path: dataDir},
		{Virtual: "/database", Path: databaseDir},
	})

	got, err := jail.Resolve("/database/db.txt", OpRead)
	if err != nil {
		t.Fatalf("Resolve /database: %v", err)
	}
	if got.Real != filepath.Join(databaseDir, "db.txt") {
		t.Fatalf("Real = %q, want database file", got.Real)
	}
}

func TestExtraCaseFoldMatchingFollowsFilesystemFlag(t *testing.T) {
	insensitive := []compiledRoot{{
		virtualParts:    []string{"data"},
		caseInsensitive: true,
		excludeParts:    [][]string{{"secret"}},
	}}
	cr, rel, err := matchRoot(insensitive, "/DATA/SECRET/file.txt")
	if err != nil {
		t.Fatalf("case folded match on insensitive mount: %v", err)
	}
	if !excludedByRoot(rel, cr) {
		t.Fatal("case folded exclusion did not match on insensitive mount")
	}

	sensitive := []compiledRoot{{virtualParts: []string{"data"}}}
	if _, _, err := matchRoot(sensitive, "/DATA/file.txt"); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("uppercase virtual on sensitive FS error = %v, want ErrNoRoot", err)
	}
}

func TestExtraExclusionIsAppliedBeforeSymlinkExpansion(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "secret", "out")); err != nil {
		t.Fatal(err)
	}
	jail := extraJail(t, Roots{{Virtual: "/data", Path: dir, Exclude: []string{"secret"}}})

	if _, err := jail.Resolve("/data/secret/out", OpRead); !errors.Is(err, ErrExcluded) {
		t.Fatalf("excluded symlink error = %v, want ErrExcluded", err)
	}
	if !jail.ReadOnly("/data/secret/out") {
		t.Fatal("excluded subtree must be read-only deny default")
	}
}

func TestExtraSnapshotRestartContinueResolveAndCharge(t *testing.T) {
	dir := t.TempDir()
	extraWriteFile(t, dir, "kept.txt", "kept")
	jail := extraJail(t, Roots{{Virtual: "/data", Path: dir, Tenant: "t1"}})
	if err := jail.SetQuota("t1", 1000); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if err := jail.Charge("t1", 120); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	snapshot, err := jail.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restartDir := t.TempDir()
	restarted := extraJail(t, Roots{{Virtual: "/placeholder", Path: restartDir}})
	if err := restarted.Restore(snapshot); err != nil {
		t.Fatalf("Restore into new jail: %v", err)
	}
	if balance, err := restarted.Quota("t1"); err != nil || balance != 880 {
		t.Fatalf("restored balance = %d, %v; want 880", balance, err)
	}
	if _, err := restarted.Resolve("/data/kept.txt", OpRead); err != nil {
		t.Fatalf("resolve after restart: %v", err)
	}
	if err := restarted.Charge("t1", 80); err != nil {
		t.Fatalf("continue charging after restart: %v", err)
	}
	if balance, _ := restarted.Quota("t1"); balance != 800 {
		t.Fatalf("balance after continued charge = %d, want 800", balance)
	}

	if err := restarted.Restore(snapshot); err != nil {
		t.Fatalf("repeat Restore: %v", err)
	}
	if balance, _ := restarted.Quota("t1"); balance != 880 {
		t.Fatalf("balance after repeated restore = %d, want 880", balance)
	}
	if _, err := restarted.Resolve("/data/kept.txt", OpRead); err != nil {
		t.Fatalf("resolve after repeated restore: %v", err)
	}
}

func TestExtraConcurrentResolveSetRootsAndSnapshot(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	extraWriteFile(t, first, "f.txt", "one")
	extraWriteFile(t, second, "f.txt", "two")
	firstReal, err := filepath.EvalSymlinks(first)
	if err != nil {
		t.Fatal(err)
	}
	secondReal, err := filepath.EvalSymlinks(second)
	if err != nil {
		t.Fatal(err)
	}

	jail := extraJail(t, Roots{{Virtual: "/data", Path: first, Tenant: "t1"}})
	oldReal := filepath.Join(firstReal, "f.txt")
	newReal := filepath.Join(secondReal, "f.txt")

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				got, err := jail.Resolve("/data/f.txt", OpRead)
				if err != nil {
					errs <- err
					return
				}
				if got.Real != oldReal && got.Real != newReal {
					errs <- fmt.Errorf("unexpected real path %q", got.Real)
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 100; n++ {
			next := first
			expected := oldReal
			if n%2 == 0 {
				next = second
				expected = newReal
			}
			if err := jail.SetRoots([]Root{{Virtual: "/data", Path: next, Tenant: "t1"}}); err != nil {
				errs <- err
				return
			}
			roots := jail.Roots()
			if len(roots) != 1 || roots[0].Path != next {
				errs <- fmt.Errorf("Roots() = %+v, want %s", roots, next)
				return
			}
			_ = expected
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 100; n++ {
			if _, err := jail.Snapshot(); err != nil {
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestExtraConcurrentQuotaChargesAreAtomic(t *testing.T) {
	jail := extraJail(t, Roots{{Virtual: "/data", Path: t.TempDir(), Tenant: "t1"}})
	if err := jail.SetQuota("t1", 100); err != nil {
		t.Fatal(err)
	}

	var allowed int64
	var rejected int64
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for worker := 0; worker < 20; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := jail.Charge("t1", 10)
			switch {
			case err == nil:
				atomic.AddInt64(&allowed, 1)
			case errors.Is(err, ErrQuota):
				atomic.AddInt64(&rejected, 1)
			default:
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if allowed != 10 || rejected != 10 {
		t.Fatalf("allowed=%d rejected=%d, want 10/10", allowed, rejected)
	}
	balance, err := jail.Quota("t1")
	if err != nil || balance != 0 {
		t.Fatalf("balance = %d, err %v; want 0", balance, err)
	}
}
