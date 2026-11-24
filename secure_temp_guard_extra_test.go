package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func uid5506ExtraOpts(root string) Options {
	return Options{
		Root:                root,
		Seed:                7,
		MaxEntriesPerTenant: 16,
		MaxBytesPerTenant:   1 << 30,
		MaxFileBytes:        1 << 20,
		TTL:                 60,
	}
}

func newUID5506Guard(t *testing.T) (*SecureTempGuard, string) {
	t.Helper()
	root := t.TempDir()
	g := NewSecureTempGuard(uid5506ExtraOpts(root))
	if !g.physical {
		t.Fatalf("temp root %q must enable the physical secure backend", root)
	}
	return g, root
}

func TestExtraSizeBoundariesAreInclusive(t *testing.T) {
	g, _ := newUID5506Guard(t)
	g.opts.MaxFileBytes = 8
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}

	file, err := g.NewFile("t", "exact", 8)
	if err != nil {
		t.Fatalf("size exactly at the cap must be allowed: %v", err)
	}
	if _, err := g.NewFile("t", "too-big", 9); !errors.Is(err, ErrBadSize) {
		t.Fatalf("size cap+1 must fail with ErrBadSize, got %v", err)
	}

	if err := g.Write(file.ID, 1); err == nil {
		t.Fatal("write growing past the single-file cap must fail")
	} else if !errors.Is(err, ErrBadSize) {
		t.Fatalf("file-cap write should classify as ErrBadSize, got %v", err)
	}
	stored, ok := g.Stat(file.ID)
	if !ok || stored.Size != 8 {
		t.Fatalf("size after rejected write = %+v ok=%v, want size 8", stored, ok)
	}
}

func TestExtraUnknownTenantUsageIsZero(t *testing.T) {
	g, _ := newUID5506Guard(t)
	u, err := g.Usage("ghost")
	if err != nil {
		t.Fatal(err)
	}
	if u != (Usage{Tenant: "ghost"}) {
		t.Fatalf("unknown tenant usage = %+v, want all zero", u)
	}
}

func TestExtraHostileTenantStaysUnderRoot(t *testing.T) {
	g, root := newUID5506Guard(t)

	dir, err := g.NewDir("../../etc/evil")
	if err != nil {
		t.Fatalf("hostile tenant must be contained, not rejected: %v", err)
	}
	rel, err := filepath.Rel(root, dir.Path)
	if err != nil {
		t.Fatal(err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		t.Fatalf("dir path %q escapes root %q (rel=%q)", dir.Path, root, rel)
	}
	if filepath.Dir(dir.Path) != root {
		t.Fatalf("tenant dir %q must be a single component under %q", dir.Path, root)
	}
	if info, err := os.Stat(dir.Path); err != nil || !info.IsDir() {
		t.Fatalf("contained directory not on disk: %v", err)
	}

	file, err := g.NewFile("../../etc/evil", "../../tmp/x", 3)
	if err != nil {
		t.Fatalf("hostile purpose must be contained: %v", err)
	}
	if filepath.Dir(file.Path) != dir.Path {
		t.Fatalf("file %q escaped tenant dir %q", file.Path, dir.Path)
	}
}

func TestExtraPhysicalOwnerOnlyModesAndSweepRemoval(t *testing.T) {
	g, _ := newUID5506Guard(t)
	g.SetNow(1000)

	dir, err := g.NewDir("t")
	if err != nil {
		t.Fatal(err)
	}
	dInfo, err := os.Stat(dir.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dInfo.Mode().Perm(); perm != os.FileMode(ModeTempDir) {
		t.Fatalf("dir perm = %o, want %o", perm, ModeTempDir)
	}

	file, err := g.NewFile("t", "f", 5)
	if err != nil {
		t.Fatal(err)
	}
	fInfo, err := os.Stat(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fInfo.Mode().Perm(); perm != os.FileMode(ModeTempFile) {
		t.Fatalf("file perm = %o, want %o", perm, ModeTempFile)
	}
	if fInfo.Size() != 5 {
		t.Fatalf("disk size at create = %d, want 5", fInfo.Size())
	}

	if err := g.Release(file.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := g.Sweep(1060)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("swept = %d, want file+dir (2)", removed)
	}
	if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
		t.Fatalf("file should be removed from disk, stat err=%v", err)
	}
	if _, err := os.Stat(dir.Path); !os.IsNotExist(err) {
		t.Fatalf("dir should be removed from disk, stat err=%v", err)
	}
}

func TestExtraSymlinkParentSwapBlocksExternalWrite(t *testing.T) {
	g, _ := newUID5506Guard(t)

	dir, err := g.NewDir("tenant")
	if err != nil {
		t.Fatal(err)
	}
	file, err := g.NewFile("tenant", "spool", 10)
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	moved := dir.Path + ".real"
	if err := os.Rename(dir.Path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir.Path); err != nil {
		t.Fatal(err)
	}

	before, err := g.Usage("tenant")
	if err != nil {
		t.Fatal(err)
	}

	// The swapped parent must make the write fail atomically.
	if err := g.Write(file.ID, 5); err == nil {
		t.Fatal("Write through an external symlinked parent must fail")
	} else {
		t.Logf("write rejected after parent swap: %v", err)
	}
	after, _ := g.Usage("tenant")
	if after.Bytes != before.Bytes {
		t.Fatalf("bytes moved from %d to %d after failed write", before.Bytes, after.Bytes)
	}
	assertUID5506DirEmpty(t, outside)

	// New-file creation must fail too and leave no provisional entry.
	if _, err := g.NewFile("tenant", "more", 3); err == nil {
		t.Fatal("NewFile through an external symlinked parent must fail")
	} else {
		t.Logf("create rejected after parent swap: %v", err)
	}
	afterCreate, _ := g.Usage("tenant")
	if afterCreate.Entries != before.Entries {
		t.Fatalf("entries = %d after failed create, want %d", afterCreate.Entries, before.Entries)
	}
	assertUID5506DirEmpty(t, outside)
}

func assertUID5506DirEmpty(t *testing.T, dir string) {
	t.Helper()
	items, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read external dir: %v", err)
	}
	if len(items) != 0 {
		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name())
		}
		t.Fatalf("external directory must stay empty, found %v", names)
	}
}

func TestExtraConcurrentCreateAtEntryCap(t *testing.T) {
	const cap = 24
	root := t.TempDir()
	opts := uid5506ExtraOpts(root)
	opts.MaxEntriesPerTenant = cap
	g := NewSecureTempGuard(opts)

	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}

	const goroutines = 12
	const attempts = 25
	start := make(chan struct{})
	var wg sync.WaitGroup
	var created int64
	var quotaErrors int64
	var unexpected int64

	for w := 0; w < goroutines; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < attempts; i++ {
				_, err := g.NewFile("t", "p", 1)
				switch {
				case err == nil:
					atomic.AddInt64(&created, 1)
				case errors.Is(err, ErrQuotaExceeded):
					atomic.AddInt64(&quotaErrors, 1)
				default:
					atomic.AddInt64(&unexpected, 1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("got %d unexpected errors", unexpected)
	}
	wantFiles := int64(cap - 1) // the tenant directory consumes one slot
	if created != wantFiles {
		t.Fatalf("successful creates = %d, want %d", created, wantFiles)
	}
	if created+quotaErrors != goroutines*attempts {
		t.Fatalf("created=%d quota=%d do not account for all attempts", created, quotaErrors)
	}

	u, err := g.Usage("t")
	if err != nil {
		t.Fatal(err)
	}
	if u.Entries != cap {
		t.Fatalf("usage entries = %d, want cap %d", u.Entries, cap)
	}
	if len(g.List("t")) != cap {
		t.Fatalf("List len = %d, want %d", len(g.List("t")), cap)
	}

	// No half-created/empty leftovers on disk: exactly one physical file per
	// successful create inside the tenant directory.
	g.mu.RLock()
	dir := g.tenantDirLocked("t")
	dirPath := ""
	if dir != nil {
		dirPath = dir.Path
	}
	g.mu.RUnlock()
	if dirPath == "" {
		t.Fatal("tenant dir vanished")
	}
	items, err := os.ReadDir(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	regular := 0
	for _, item := range items {
		if !item.IsDir() {
			regular++
		}
	}
	if regular != int(wantFiles) {
		t.Fatalf("physical files = %d, want %d (no failed-create leftovers)", regular, wantFiles)
	}
}

func TestExtraSweepWriteCrossInvariants(t *testing.T) {
	g, _ := newUID5506Guard(t)
	g.opts.MaxEntriesPerTenant = 1000
	if err := g.SetNow(1000); err != nil {
		t.Fatal(err)
	}
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}

	const activeFiles = 8
	const releasedFiles = 40
	activeIDs := make([]string, 0, activeFiles)
	for i := 0; i < activeFiles; i++ {
		f, err := g.NewFile("t", "active", 1)
		if err != nil {
			t.Fatal(err)
		}
		activeIDs = append(activeIDs, f.ID)
	}
	for i := 0; i < releasedFiles; i++ {
		f, err := g.NewFile("t", "orphan", 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.Release(f.ID); err != nil {
			t.Fatal(err)
		}
	}

	var writeFailures int64
	var writerWG sync.WaitGroup
	const writers = 8
	const iterations = 100
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < iterations; i++ {
				id := activeIDs[(w*iterations+i)%activeFiles]
				if err := g.Write(id, 1); err != nil {
					atomic.AddInt64(&writeFailures, 1)
				}
			}
		}(w)
	}

	stopSweep := make(chan struct{})
	var sweepWG sync.WaitGroup
	sweepWG.Add(1)
	go func() {
		defer sweepWG.Done()
		ts := int64(1060)
		for {
			select {
			case <-stopSweep:
				return
			default:
			}
			_, _ = g.Sweep(ts)
			ts += 60
			if ts > 10_000_000 {
				ts = 1060
			}
		}
	}()

	stopCheck := make(chan struct{})
	var checkWG sync.WaitGroup
	checkWG.Add(1)
	go func() {
		defer checkWG.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCheck:
				return
			case <-ticker.C:
				// One critical section gives an internally consistent view;
				// calling Usage/List/Stat separately could straddle a Write by
				// design and is not an invariant violation.
				g.mu.RLock()
				var entries int
				var handles int
				for _, e := range g.st.Entries {
					if e.Tenant != "t" {
						continue
					}
					entries++
					if e.Kind == kindFile {
						if e.Size < 1 {
							t.Errorf("entry %s has torn/negative size %d", e.ID, e.Size)
						}
						if e.Handles > 0 {
							handles++
						}
						if e.Active != (e.Handles > 0) {
							t.Errorf("entry %s active=%v handles=%d disagree", e.ID, e.Active, e.Handles)
						}
					}
				}
				listed := 0
				for _, id := range g.sortedIDsLocked() {
					if g.st.Entries[id].Tenant == "t" {
						listed++
					}
				}
				g.mu.RUnlock()

				if listed != entries {
					t.Errorf("List len %d != entries %d", listed, entries)
				}
				if handles != activeFiles {
					t.Errorf("locked handles = %d, want %d", handles, activeFiles)
				}
				u, err := g.Usage("t")
				if err != nil {
					t.Errorf("usage: %v", err)
					return
				}
				if u.ActiveHandles != activeFiles {
					t.Errorf("active handles = %d, want stable %d", u.ActiveHandles, activeFiles)
				}
				if u.Entries < activeFiles+1 {
					t.Errorf("entries = %d, active files/dir must survive", u.Entries)
				}
			}
		}
	}()

	writerWG.Wait()
	close(stopSweep)
	sweepWG.Wait()
	close(stopCheck)
	checkWG.Wait()

	if writeFailures != 0 {
		t.Fatalf("active-file writes failed %d times", writeFailures)
	}

	// A far-future sweep reclaims every released orphan; active files remain
	// held and keep their directory alive.
	for i := 0; i < 3; i++ {
		if _, err := g.Sweep(100_000_000); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := g.Usage("t")
	if u.Entries != activeFiles+1 {
		t.Fatalf("entries after cross sweep = %d, want %d active files + dir", u.Entries, activeFiles+1)
	}
	if u.ActiveHandles != activeFiles {
		t.Fatalf("active handles = %d, want %d", u.ActiveHandles, activeFiles)
	}
	var bytes uint64
	for _, id := range activeIDs {
		f, ok := g.Stat(id)
		if !ok {
			t.Fatalf("active file %s swept while held", id)
		}
		bytes += uint64(f.Size)
	}
	wantBytes := uint64(activeFiles + writers*iterations)
	if bytes != wantBytes || u.Bytes != wantBytes {
		t.Fatalf("bytes stat=%d usage=%d, want %d", bytes, u.Bytes, wantBytes)
	}
}

func TestExtraSweepActiveHandlePostponesDeadline(t *testing.T) {
	g, _ := newUID5506Guard(t)
	if err := g.SetNow(1000); err != nil {
		t.Fatal(err)
	}
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}
	file, err := g.NewFile("t", "f", 10)
	if err != nil {
		t.Fatal(err)
	}

	if n, _ := g.Sweep(1060); n != 0 {
		t.Fatalf("held entry swept at expiry, count=%d", n)
	}
	if n, _ := g.Sweep(1100); n != 0 {
		t.Fatalf("postponed entry swept early, count=%d", n)
	}
	if n, _ := g.Sweep(1120); n != 0 {
		t.Fatalf("held entry swept at postponed deadline, count=%d", n)
	}
	if err := g.Release(file.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := g.Sweep(1179); n != 0 {
		t.Fatalf("still before postponed deadline, swept=%d", n)
	}
	if n, _ := g.Sweep(1180); n != 2 {
		t.Fatalf("at postponed deadline want file+dir=2, got %d", n)
	}
	if u, _ := g.Usage("t"); u.Entries != 0 {
		t.Fatalf("entries after final sweep = %d, want 0", u.Entries)
	}
}

func TestExtraSweepWritingEntrySkippedAndPostponed(t *testing.T) {
	g, _ := newUID5506Guard(t)
	if err := g.SetNow(0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}
	file, err := g.NewFile("t", "f", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Release(file.ID); err != nil {
		t.Fatal(err)
	}

	g.mu.Lock()
	g.st.Entries[file.ID].Writing = 1
	g.mu.Unlock()

	if n, _ := g.Sweep(60); n != 0 {
		t.Fatalf("entry being written must not be swept, count=%d", n)
	}
	if _, ok := g.Stat(file.ID); !ok {
		t.Fatal("writing entry disappeared")
	}
	g.mu.RLock()
	expireAt := g.st.Entries[file.ID].ExpireAt
	g.mu.RUnlock()
	if expireAt != 120 {
		t.Fatalf("expireAt after busy sweep = %d, want 120", expireAt)
	}

	if n, _ := g.Sweep(119); n != 0 {
		t.Fatalf("postponed writing entry swept early, count=%d", n)
	}

	g.mu.Lock()
	g.st.Entries[file.ID].Writing = 0
	g.mu.Unlock()

	if n, _ := g.Sweep(120); n != 2 {
		t.Fatalf("idle entry at postponed deadline want 2, got %d", n)
	}
}

func TestExtraSnapshotRestoreContinueWriteWithSameID(t *testing.T) {
	root := t.TempDir()
	opts := uid5506ExtraOpts(root)
	g := NewSecureTempGuard(opts)
	if err := g.SetNow(500); err != nil {
		t.Fatal(err)
	}
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}
	file, err := g.NewFile("t", "log", 10)
	if err != nil {
		t.Fatal(err)
	}
	other, err := g.NewFile("t", "log2", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Write(file.ID, 5); err != nil {
		t.Fatal(err)
	}

	snapshot, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	restored := NewSecureTempGuard(uid5506ExtraOpts(root))
	if err := restored.Restore(snapshot); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.physical {
		t.Fatal("restored guard should keep using the real temp root")
	}

	// Keep writing the very same id after restart.
	if err := restored.Write(file.ID, 7); err != nil {
		t.Fatalf("write by original id after restore: %v", err)
	}
	stored, ok := restored.Stat(file.ID)
	if !ok || stored.Size != 22 {
		t.Fatalf("restored file = %+v ok=%v, want size 22", stored, ok)
	}
	diskInfo, err := os.Stat(file.Path)
	if err != nil {
		t.Fatalf("physical file missing after restored write: %v", err)
	}
	if diskInfo.Size() != 22 {
		t.Fatalf("disk size = %d, want 22", diskInfo.Size())
	}

	u, _ := restored.Usage("t")
	if u.Entries != 3 {
		t.Fatalf("entries after restore = %d, want 3", u.Entries)
	}
	if u.Bytes != 23 {
		t.Fatalf("bytes after restore+write = %d, want 23", u.Bytes)
	}

	// Newly issued ids must not collide with pre-snapshot ids.
	next, err := restored.NewFile("t", "log3", 1)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == file.ID || next.ID == other.ID {
		t.Fatalf("new id %q reused an old id", next.ID)
	}
	if _, ok := restored.Stat(next.ID); !ok {
		t.Fatal("new entry not stat-able")
	}
	if len(restored.List("t")) != 4 {
		t.Fatalf("list len = %d, want 4", len(restored.List("t")))
	}
}

func TestExtraRestoreBadSnapshotsRejectedAndLiveKept(t *testing.T) {
	g, _ := newUID5506Guard(t)
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}
	file, err := g.NewFile("t", "f", 7)
	if err != nil {
		t.Fatal(err)
	}

	badPayloads := [][]byte{
		nil,
		[]byte("broken-payload"),
		[]byte(`{"version":99}`),
		[]byte(`{"seq":-1}`),
		[]byte(`{"entries":[{"id":""}]}`),
		[]byte(`{"entries":[{"id":"x","tenant":"t","kind":"mystery"}]}`),
		[]byte(`{"entries":[{"id":"x","tenant":"t","kind":"file","size":-2}]}`),
	}
	for i, payload := range badPayloads {
		if err := g.Restore(payload); !errors.Is(err, ErrBadSnapshot) {
			t.Fatalf("payload %d: err=%v, want ErrBadSnapshot", i, err)
		}
		u, _ := g.Usage("t")
		if u.Entries != 2 || u.Bytes != 7 {
			t.Fatalf("payload %d: live state changed: %+v", i, u)
		}
		if _, ok := g.Stat(file.ID); !ok {
			t.Fatalf("payload %d: original file lost", i)
		}
	}

	if _, err := g.NewFile("t", "after", 1); err != nil {
		t.Fatalf("guard must keep working after rejected restores: %v", err)
	}
}

func TestExtraConcurrentUsageSnapshotSweep(t *testing.T) {
	g, _ := newUID5506Guard(t)
	g.opts.MaxEntriesPerTenant = 10000
	if _, err := g.NewDir("t"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		f, err := g.NewFile("t", "p", 1)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if err := g.Release(f.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = g.Usage("t")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = g.Snapshot()
			}
		}()
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, _ = g.Sweep(int64(1_000_000 + n*1000 + i))
			}
		}(w)
	}
	wg.Wait()

	// Snapshot must always unmarshal back into a valid state.
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	other := NewSecureTempGuard(uid5506ExtraOpts(t.TempDir()))
	if err := other.Restore(payload); err != nil {
		t.Fatalf("concurrent snapshot failed to restore: %v\n%s", err, payload)
	}
}
