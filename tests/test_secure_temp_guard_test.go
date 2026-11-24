package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-b-09"
)

func tempOptions() impl.Options {
	return impl.Options{
		Root:                "/sandbox/tmp",
		Seed:                424242,
		MaxEntriesPerTenant: 8,
		MaxBytesPerTenant:   4096,
		MaxFileBytes:        1024,
		TTL:                 60,
	}
}

func mustTempDir(t *testing.T, g *impl.SecureTempGuard, tenant string) *impl.TempDir {
	t.Helper()
	dir, err := g.NewDir(tenant)
	if err != nil {
		t.Fatalf("new dir for tenant %q: %v", tenant, err)
	}
	return dir
}

func TestNewDirRequiresConfiguredRoot(t *testing.T) {
	opts := tempOptions()
	opts.Root = ""
	g := impl.NewSecureTempGuard(opts)
	if _, err := g.NewDir("t1"); !errors.Is(err, impl.ErrBadRoot) {
		t.Fatalf("missing root must fail with ErrBadRoot, got %v", err)
	}
}

func TestNewDirRejectsDuplicateTenant(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	if _, err := g.NewDir("t1"); !errors.Is(err, impl.ErrPathExists) {
		t.Fatalf("second directory for the same tenant must fail with ErrPathExists, got %v", err)
	}
}

func TestNewDirCarriesOwnerOnlyModeAndRandomName(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	dir := mustTempDir(t, g, "t1")
	if dir.Mode != impl.ModeTempDir {
		t.Fatalf("directory mode = %o, want %o", dir.Mode, impl.ModeTempDir)
	}
	if !strings.HasPrefix(dir.Path, "/sandbox/tmp/") {
		t.Fatalf("directory path %q is not under the configured root", dir.Path)
	}
	if !strings.Contains(dir.Path, "t1") {
		t.Fatalf("directory path %q does not mention the tenant", dir.Path)
	}
	if len(dir.Path) <= len("/sandbox/tmp/t1") {
		t.Fatalf("directory name %q has no random suffix", dir.Path)
	}
}

func TestNewFileRequiresTenantDirectory(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	if _, err := g.NewFile("t1", "upload", 10); !errors.Is(err, impl.ErrNoParent) {
		t.Fatalf("file without tenant directory must fail with ErrNoParent, got %v", err)
	}
}

func TestNewFileRejectsBadSizeAndPurpose(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	if _, err := g.NewFile("t1", "", 10); !errors.Is(err, impl.ErrBadArg) {
		t.Fatalf("blank purpose must fail with ErrBadArg, got %v", err)
	}
	if _, err := g.NewFile("t1", "upload", 0); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("zero size must fail with ErrBadSize, got %v", err)
	}
	if _, err := g.NewFile("t1", "upload", -3); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("negative size must fail with ErrBadSize, got %v", err)
	}
	if _, err := g.NewFile("t1", "upload", 2048); !errors.Is(err, impl.ErrBadSize) {
		t.Fatalf("size beyond MaxFileBytes must fail with ErrBadSize, got %v", err)
	}
}

func TestNewFileIsOwnerOnlyAndUniquelyNamed(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	dir := mustTempDir(t, g, "t1")
	first, err := g.NewFile("t1", "upload", 40)
	if err != nil {
		t.Fatalf("first file: %v", err)
	}
	second, err := g.NewFile("t1", "upload", 40)
	if err != nil {
		t.Fatalf("second file: %v", err)
	}
	if first.Mode != impl.ModeTempFile {
		t.Fatalf("file mode = %o, want %o", first.Mode, impl.ModeTempFile)
	}
	if first.Size != 40 {
		t.Fatalf("file size = %d, want 40", first.Size)
	}
	if !first.Active {
		t.Fatalf("fresh file must be held by an active handle")
	}
	if !strings.HasPrefix(first.Path, dir.Path+"/") {
		t.Fatalf("file path %q is not inside the tenant directory %q", first.Path, dir.Path)
	}
	if first.Path == second.Path {
		t.Fatalf("two files of the same purpose landed on the same path %q", first.Path)
	}
}

func TestNewFileQuotaByEntries(t *testing.T) {
	opts := tempOptions()
	opts.MaxEntriesPerTenant = 3
	g := impl.NewSecureTempGuard(opts)
	mustTempDir(t, g, "t1")
	if _, err := g.NewFile("t1", "a", 10); err != nil {
		t.Fatalf("first file: %v", err)
	}
	if _, err := g.NewFile("t1", "b", 10); err != nil {
		t.Fatalf("second file: %v", err)
	}
	if _, err := g.NewFile("t1", "c", 10); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("third file must fail with ErrQuotaExceeded, got %v", err)
	}
	usage, err := g.Usage("t1")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Entries != 3 {
		t.Fatalf("entries after rejected create = %d, want 3", usage.Entries)
	}
	if usage.Bytes != 20 {
		t.Fatalf("bytes after rejected create = %d, want 20", usage.Bytes)
	}
}

func TestNewFileQuotaByBytes(t *testing.T) {
	opts := tempOptions()
	opts.MaxBytesPerTenant = 100
	g := impl.NewSecureTempGuard(opts)
	mustTempDir(t, g, "t1")
	if _, err := g.NewFile("t1", "big", 60); err != nil {
		t.Fatalf("first file: %v", err)
	}
	if _, err := g.NewFile("t1", "more", 50); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("file beyond byte quota must fail with ErrQuotaExceeded, got %v", err)
	}
	usage, err := g.Usage("t1")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Bytes != 60 {
		t.Fatalf("bytes after rejected create = %d, want 60", usage.Bytes)
	}
	if usage.Entries != 2 {
		t.Fatalf("entries after rejected create = %d, want 2", usage.Entries)
	}
}

func TestWriteGrowsBytesAndRespectsQuota(t *testing.T) {
	opts := tempOptions()
	opts.MaxBytesPerTenant = 100
	g := impl.NewSecureTempGuard(opts)
	mustTempDir(t, g, "t1")
	file, err := g.NewFile("t1", "log", 40)
	if err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := g.Write(file.ID, 20); err != nil {
		t.Fatalf("grow file: %v", err)
	}
	stored, ok := g.Stat(file.ID)
	if !ok {
		t.Fatalf("file vanished after write")
	}
	if stored.Size != 60 {
		t.Fatalf("file size after write = %d, want 60", stored.Size)
	}
	if err := g.Write(file.ID, 50); !errors.Is(err, impl.ErrQuotaExceeded) {
		t.Fatalf("write beyond byte quota must fail with ErrQuotaExceeded, got %v", err)
	}
	stored, _ = g.Stat(file.ID)
	if stored.Size != 60 {
		t.Fatalf("file size after rejected write = %d, want 60", stored.Size)
	}
}

func TestWriteRejectsUnknownEntry(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	if err := g.Write("missing-id", 5); !errors.Is(err, impl.ErrNoEntry) {
		t.Fatalf("write to unknown entry must fail with ErrNoEntry, got %v", err)
	}
}

func TestReleaseLowersActiveHandles(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	file, err := g.NewFile("t1", "log", 10)
	if err != nil {
		t.Fatalf("new file: %v", err)
	}
	before, err := g.Usage("t1")
	if err != nil {
		t.Fatalf("usage before release: %v", err)
	}
	if before.ActiveHandles != 1 {
		t.Fatalf("active handles before release = %d, want 1", before.ActiveHandles)
	}
	if err := g.Release(file.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	after, err := g.Usage("t1")
	if err != nil {
		t.Fatalf("usage after release: %v", err)
	}
	if after.ActiveHandles != 0 {
		t.Fatalf("active handles after release = %d, want 0", after.ActiveHandles)
	}
	stored, ok := g.Stat(file.ID)
	if !ok {
		t.Fatalf("released entry must stay accounted until swept")
	}
	if stored.Active {
		t.Fatalf("released entry still reports an active handle")
	}
}

func TestReleaseRejectsDoubleRelease(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	file, err := g.NewFile("t1", "log", 10)
	if err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := g.Release(file.ID); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := g.Release(file.ID); !errors.Is(err, impl.ErrNoEntry) {
		t.Fatalf("second release must fail with ErrNoEntry, got %v", err)
	}
}

func TestSweepRemovesExpiredReleasedEntries(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	if err := g.SetNow(1000); err != nil {
		t.Fatalf("set clock: %v", err)
	}
	mustTempDir(t, g, "t1")
	file, err := g.NewFile("t1", "spool", 10)
	if err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := g.Release(file.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	reclaimed, err := g.Sweep(1000 + 60)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if reclaimed != 2 {
		t.Fatalf("swept entries = %d, want 2 (file and directory)", reclaimed)
	}
	usage, err := g.Usage("t1")
	if err != nil {
		t.Fatalf("usage after sweep: %v", err)
	}
	if usage.Entries != 0 {
		t.Fatalf("entries after sweep = %d, want 0", usage.Entries)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	file, err := g.NewFile("t1", "spool", 30)
	if err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := g.Write(file.ID, 10); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := impl.NewSecureTempGuard(tempOptions())
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	usage, err := other.Usage("t1")
	if err != nil {
		t.Fatalf("usage after restore: %v", err)
	}
	if usage.Entries != 2 {
		t.Fatalf("restored entries = %d, want 2", usage.Entries)
	}
	if usage.Bytes != 40 {
		t.Fatalf("restored bytes = %d, want 40", usage.Bytes)
	}
	stored, ok := other.Stat(file.ID)
	if !ok {
		t.Fatalf("restored guard lost entry %q", file.ID)
	}
	if stored.Size != 40 {
		t.Fatalf("restored entry size = %d, want 40", stored.Size)
	}
}

func TestRestoreRejectsCorruptSnapshotKeepsEntries(t *testing.T) {
	g := impl.NewSecureTempGuard(tempOptions())
	mustTempDir(t, g, "t1")
	if _, err := g.NewFile("t1", "spool", 20); err != nil {
		t.Fatalf("new file: %v", err)
	}
	if err := g.Restore([]byte("broken-payload")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("corrupt snapshot must fail with ErrBadSnapshot, got %v", err)
	}
	usage, err := g.Usage("t1")
	if err != nil {
		t.Fatalf("usage after rejected restore: %v", err)
	}
	if usage.Entries != 2 {
		t.Fatalf("entries after rejected restore = %d, want 2", usage.Entries)
	}
}
