package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-b-03"
)

func mountData(t *testing.T) (impl.Roots, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	return impl.Roots{{Virtual: "/data", Path: dir, Tenant: "t1"}}, dir
}

func mustJail(t *testing.T, roots impl.Roots) *impl.PathJailGuard {
	t.Helper()
	j, err := impl.NewPathJailGuard(roots)
	if err != nil {
		t.Fatalf("NewPathJailGuard: unexpected error: %v", err)
	}
	if j == nil {
		t.Fatal("NewPathJailGuard returned a nil jail")
	}
	return j
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestNewRejectsBadRoots(t *testing.T) {
	base := t.TempDir()
	if _, err := impl.NewPathJailGuard(impl.Roots{{Virtual: "/data", Path: "relative"}}); !errors.Is(err, impl.ErrBadRoots) {
		t.Fatalf("relative root path error = %v, want ErrBadRoots", err)
	}
	if _, err := impl.NewPathJailGuard(impl.Roots{{Virtual: "/data", Path: filepath.Join(base, "missing")}}); !errors.Is(err, impl.ErrBadRoots) {
		t.Fatalf("missing root directory error = %v, want ErrBadRoots", err)
	}
	if _, err := impl.NewPathJailGuard(nil); !errors.Is(err, impl.ErrBadRoots) {
		t.Fatalf("empty root set error = %v, want ErrBadRoots", err)
	}
}

func TestNewRejectsDuplicateAndNestedVirtuals(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	duplicate := impl.Roots{
		{Virtual: "/data", Path: first},
		{Virtual: "/data", Path: second},
	}
	if _, err := impl.NewPathJailGuard(duplicate); !errors.Is(err, impl.ErrBadRoots) {
		t.Fatalf("duplicate virtual root error = %v, want ErrBadRoots", err)
	}
	nested := impl.Roots{
		{Virtual: "/data", Path: first},
		{Virtual: "/data/sub", Path: second},
	}
	if _, err := impl.NewPathJailGuard(nested); !errors.Is(err, impl.ErrBadRoots) {
		t.Fatalf("nested virtual root error = %v, want ErrBadRoots", err)
	}
}

func TestResolveFoldsRelativeAndDotSegments(t *testing.T) {
	roots, dir := mountData(t)
	j := mustJail(t, roots)
	writeFile(t, dir, "b.txt", "content")
	got, err := j.Resolve("data/./a/../b.txt", impl.OpRead)
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	if got.Virtual != "/data/b.txt" {
		t.Fatalf("resolved virtual path = %q, want /data/b.txt", got.Virtual)
	}
	if got.Real != filepath.Join(dir, "b.txt") {
		t.Fatalf("resolved real path = %q, want %q", got.Real, filepath.Join(dir, "b.txt"))
	}
	if !got.Exists {
		t.Fatal("resolved existing file reported Exists=false")
	}
}

func TestResolveRejectsUnmappedPath(t *testing.T) {
	roots, _ := mountData(t)
	j := mustJail(t, roots)
	if _, err := j.Resolve("/etc/passwd", impl.OpRead); !errors.Is(err, impl.ErrNoRoot) {
		t.Fatalf("Resolve outside every mount error = %v, want ErrNoRoot", err)
	}
	if _, err := j.Resolve("/data/../etc/passwd", impl.OpRead); !errors.Is(err, impl.ErrNoRoot) {
		t.Fatalf("Resolve after dot-dot escape error = %v, want ErrNoRoot", err)
	}
}

func TestResolveRejectsExcludedSubtree(t *testing.T) {
	roots, dir := mountData(t)
	roots[0].Exclude = []string{"secret"}
	j := mustJail(t, roots)
	writeFile(t, dir, "secret/token.txt", "classified")
	if _, err := j.Resolve("/data/secret/token.txt", impl.OpRead); !errors.Is(err, impl.ErrExcluded) {
		t.Fatalf("Resolve in an excluded subtree error = %v, want ErrExcluded", err)
	}
}

func TestResolveReadOfMissingTargetFails(t *testing.T) {
	roots, _ := mountData(t)
	j := mustJail(t, roots)
	if _, err := j.Resolve("/data/missing.txt", impl.OpRead); !errors.Is(err, impl.ErrNotExist) {
		t.Fatalf("Resolve of a missing read target error = %v, want ErrNotExist", err)
	}
}

func TestResolveWriteFallsBackToLastExistingParent(t *testing.T) {
	roots, dir := mountData(t)
	j := mustJail(t, roots)
	got, err := j.Resolve("/data/new.txt", impl.OpWrite)
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	if got.Exists {
		t.Fatal("a not yet created target reported Exists=true")
	}
	if got.Parent != dir {
		t.Fatalf("Parent = %q, want the last existing directory %q", got.Parent, dir)
	}
	if got.Real != filepath.Join(dir, "new.txt") {
		t.Fatalf("Real = %q, want %q", got.Real, filepath.Join(dir, "new.txt"))
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	roots, dir := mountData(t)
	j := mustJail(t, roots)
	outside := t.TempDir()
	outsideFile := writeFile(t, outside, "x.txt", "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if _, err := j.Resolve("/data/out/x.txt", impl.OpRead); !errors.Is(err, impl.ErrEscaped) {
		t.Fatalf("Resolve through an escaping directory link error = %v, want ErrEscaped", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(dir, "pw")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	if _, err := j.Resolve("/data/pw", impl.OpRead); !errors.Is(err, impl.ErrEscaped) {
		t.Fatalf("Resolve through an escaping file link error = %v, want ErrEscaped", err)
	}
}

func TestResolveFollowsInternalSymlink(t *testing.T) {
	roots, dir := mountData(t)
	j := mustJail(t, roots)
	writeFile(t, dir, "real.txt", "inside")
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := j.Resolve("/data/link.txt", impl.OpRead)
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	if got.Real != filepath.Join(dir, "real.txt") {
		t.Fatalf("Real = %q, want the link target %q", got.Real, filepath.Join(dir, "real.txt"))
	}
	if !got.Exists {
		t.Fatal("internal symlink target reported Exists=false")
	}
}

func TestResolveWriteRejectsReadOnlyRoot(t *testing.T) {
	dataRoot, dataDir := mountData(t)
	roDir := t.TempDir()
	roots := append(dataRoot, impl.Root{Virtual: "/ro", Path: roDir, ReadOnly: true})
	j := mustJail(t, roots)
	writeFile(t, roDir, "x.txt", "frozen")
	if _, err := j.Resolve("/ro/x.txt", impl.OpWrite); !errors.Is(err, impl.ErrReadOnly) {
		t.Fatalf("write into a read only root error = %v, want ErrReadOnly", err)
	}
	got, err := j.Resolve("/ro/x.txt", impl.OpRead)
	if err != nil {
		t.Fatalf("read of a read only root: unexpected error: %v", err)
	}
	if !got.ReadOnly {
		t.Fatal("Resolved.ReadOnly = false for a read only mount")
	}
	writeFile(t, dataDir, "y.txt", "writable")
	if _, err := j.Resolve("/data/y.txt", impl.OpWrite); err != nil {
		t.Fatalf("write into the writable mount: unexpected error: %v", err)
	}
}

func TestReadOnlyReportsMountState(t *testing.T) {
	dataRoot, _ := mountData(t)
	roDir := t.TempDir()
	j := mustJail(t, append(dataRoot, impl.Root{Virtual: "/ro", Path: roDir, ReadOnly: true}))
	if j.ReadOnly("/data/file.txt") {
		t.Fatal("ReadOnly(/data/file.txt) = true, want false for the writable mount")
	}
	if !j.ReadOnly("/ro/file.txt") {
		t.Fatal("ReadOnly(/ro/file.txt) = false, want true for the read only mount")
	}
	if !j.ReadOnly("/elsewhere/file.txt") {
		t.Fatal("ReadOnly for an unmapped path = false, want deny by default")
	}
}

func TestQuotaChargeEnforcesBudget(t *testing.T) {
	roots, _ := mountData(t)
	j := mustJail(t, roots)
	if err := j.SetQuota("t1", 500); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if err := j.Charge("t1", 200); err != nil {
		t.Fatalf("Charge 200: %v", err)
	}
	left, err := j.Quota("t1")
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if left != 300 {
		t.Fatalf("remaining quota = %d, want 300", left)
	}
	if err := j.Charge("t1", 301); !errors.Is(err, impl.ErrQuota) {
		t.Fatalf("over budget charge error = %v, want ErrQuota", err)
	}
	if left, err = j.Quota("t1"); err != nil || left != 300 {
		t.Fatalf("quota after a rejected charge = %d (err %v), want 300", left, err)
	}
	if err := j.Charge("t1", -1); !errors.Is(err, impl.ErrBadCharge) {
		t.Fatalf("negative charge error = %v, want ErrBadCharge", err)
	}
	if _, err := j.Quota("ghost"); !errors.Is(err, impl.ErrBadTenant) {
		t.Fatalf("quota of an unknown tenant error = %v, want ErrBadTenant", err)
	}
}

func TestSnapshotRestoreKeepsQuota(t *testing.T) {
	roots, dir := mountData(t)
	j := mustJail(t, roots)
	if err := j.SetQuota("t1", 500); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if err := j.Charge("t1", 200); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	writeFile(t, dir, "kept.txt", "x")
	snap, err := j.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("Snapshot returned an empty payload")
	}

	restored := mustJail(t, roots)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	left, err := restored.Quota("t1")
	if err != nil || left != 300 {
		t.Fatalf("restored quota = %d (err %v), want 300", left, err)
	}
	got, err := restored.Resolve("/data/kept.txt", impl.OpRead)
	if err != nil {
		t.Fatalf("Resolve after restore: %v", err)
	}
	if got.Real != filepath.Join(dir, "kept.txt") {
		t.Fatalf("real path after restore = %q", got.Real)
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	roots, _ := mountData(t)
	j := mustJail(t, roots)
	if err := j.SetQuota("t1", 500); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if err := j.Restore([]byte("{broken")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot error = %v, want ErrBadSnapshot", err)
	}
	left, err := j.Quota("t1")
	if err != nil || left != 500 {
		t.Fatalf("quota after a rejected snapshot = %d (err %v), want 500", left, err)
	}
}

func TestSetRootsSwapKeepsResolvesConsistent(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	writeFile(t, first, "f.txt", "one")
	writeFile(t, second, "f.txt", "two")

	j := mustJail(t, impl.Roots{{Virtual: "/data", Path: first, Tenant: "t1"}})
	oldReal := filepath.Join(first, "f.txt")
	newReal := filepath.Join(second, "f.txt")

	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				got, err := j.Resolve("/data/f.txt", impl.OpRead)
				if err != nil {
					errs <- err
					return
				}
				if got.Real != oldReal && got.Real != newReal {
					errs <- fmt.Errorf("in flight resolve saw %q", got.Real)
					return
				}
			}
		}()
	}
	if err := j.SetRoots([]impl.Root{{Virtual: "/data", Path: second, Tenant: "t1"}}); err != nil {
		t.Fatalf("SetRoots: %v", err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	got, err := j.Resolve("/data/f.txt", impl.OpRead)
	if err != nil {
		t.Fatalf("Resolve after swap: %v", err)
	}
	if got.Real != newReal {
		t.Fatalf("resolve after swap = %q, want %q", got.Real, newReal)
	}
}
