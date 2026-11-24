package task

import (
	"errors"
	"testing"

	impl "gsb/batch48-go-safety-b-13"
)

func newSealGuard() *impl.FileSealGuard {
	return impl.NewFileSealGuard(impl.Options{MaxFiles: 8, MaxSize: 4096})
}

func TestCreateValidatesNameAndSize(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("", 16); !errors.Is(err, impl.ErrInvalidName) {
		t.Fatalf("blank name = %v, want ErrInvalidName", err)
	}
	if _, err := g.Create("neg", -1); !errors.Is(err, impl.ErrInvalidSize) {
		t.Fatalf("negative size = %v, want ErrInvalidSize", err)
	}
	if _, err := g.Create("big", 8192); !errors.Is(err, impl.ErrInvalidSize) {
		t.Fatalf("oversized file = %v, want ErrInvalidSize", err)
	}
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("ring", 32); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := g.Create("ring", 64); !errors.Is(err, impl.ErrNameTaken) {
		t.Fatalf("duplicate create = %v, want ErrNameTaken", err)
	}
}

func TestCreateReturnsHandleAndState(t *testing.T) {
	g := newSealGuard()
	h, err := g.Create("ctl", 32)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h == nil {
		t.Fatalf("create returned a nil handle")
	}
	if h.Name != "ctl" || h.Size != 32 || h.Seals != 0 {
		t.Fatalf("handle = %+v", h)
	}
	got, err := g.Size("ctl")
	if err != nil || got != 32 {
		t.Fatalf("size = %d err %v, want 32", got, err)
	}
	flags, err := g.Seals("ctl")
	if err != nil || flags != 0 {
		t.Fatalf("seals = %v err %v, want none", flags, err)
	}
}

func TestCreateHonorsFileLimit(t *testing.T) {
	g := impl.NewFileSealGuard(impl.Options{MaxFiles: 1, MaxSize: 4096})
	if _, err := g.Create("first", 16); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := g.Create("second", 16); !errors.Is(err, impl.ErrFileLimit) {
		t.Fatalf("create over limit = %v, want ErrFileLimit", err)
	}
}

func TestWriteOverwritesInPlace(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("data", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Write("data", 0, []byte("abc")); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := g.Contents("data")
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if string(body) != "abc\x00\x00\x00\x00\x00" {
		t.Fatalf("contents = %q", string(body))
	}
	if got, err := g.Size("data"); err != nil || got != 8 {
		t.Fatalf("size = %d err %v, want 8", got, err)
	}
}

func TestWriteAppendGrowsFile(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("log", 4); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Write("log", 0, []byte("head")); err != nil {
		t.Fatalf("write head: %v", err)
	}
	if err := g.Write("log", 4, []byte("tail")); err != nil {
		t.Fatalf("append tail: %v", err)
	}
	if got, err := g.Size("log"); err != nil || got != 8 {
		t.Fatalf("size = %d err %v, want 8", got, err)
	}
	body, err := g.Contents("log")
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if string(body) != "headtail" {
		t.Fatalf("contents = %q, want headtail", string(body))
	}
}

func TestWriteRejectsOutOfRangeOffset(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("range", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Write("range", 9, []byte("x")); !errors.Is(err, impl.ErrWriteRange) {
		t.Fatalf("offset past end = %v, want ErrWriteRange", err)
	}
	if err := g.Write("range", -1, []byte("x")); !errors.Is(err, impl.ErrWriteRange) {
		t.Fatalf("negative offset = %v, want ErrWriteRange", err)
	}
}

func TestSealIsAdditiveAndValidatesBits(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("cfg", 16); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Seal("cfg", impl.SealNoWrite); err != nil {
		t.Fatalf("seal no write: %v", err)
	}
	if err := g.Seal("cfg", impl.SealNoShrink); err != nil {
		t.Fatalf("seal no shrink: %v", err)
	}
	flags, err := g.Seals("cfg")
	if err != nil {
		t.Fatalf("seals: %v", err)
	}
	if flags != impl.SealNoWrite|impl.SealNoShrink {
		t.Fatalf("seals = %v, want %v", flags, impl.SealNoWrite|impl.SealNoShrink)
	}
	if err := g.Seal("cfg", impl.SealFlags(1<<20)); !errors.Is(err, impl.ErrSealBits) {
		t.Fatalf("unknown bits = %v, want ErrSealBits", err)
	}
	flags, err = g.Seals("cfg")
	if err != nil {
		t.Fatalf("seals after reject: %v", err)
	}
	if flags != impl.SealNoWrite|impl.SealNoShrink {
		t.Fatalf("rejected seal changed flags to %v", flags)
	}
}

func TestWriteBlockedByNoWriteSeal(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("ro", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Seal("ro", impl.SealNoWrite); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := g.Write("ro", 0, []byte("x")); !errors.Is(err, impl.ErrSealViolation) {
		t.Fatalf("write under no-write = %v, want ErrSealViolation", err)
	}
	body, err := g.Contents("ro")
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if string(body) != "\x00\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("sealed write mutated contents: %q", string(body))
	}
}

func TestTruncateGrowthBlockedByNoGrow(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("fixed", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Seal("fixed", impl.SealNoGrow); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := g.Truncate("fixed", 16); !errors.Is(err, impl.ErrSealViolation) {
		t.Fatalf("grow under no-grow = %v, want ErrSealViolation", err)
	}
	if got, err := g.Size("fixed"); err != nil || got != 8 {
		t.Fatalf("size = %d err %v, want 8", got, err)
	}
}

func TestTruncateShrinkBlockedByNoShrink(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("grow", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Seal("grow", impl.SealNoShrink); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := g.Truncate("grow", 4); !errors.Is(err, impl.ErrSealViolation) {
		t.Fatalf("shrink under no-shrink = %v, want ErrSealViolation", err)
	}
	if got, err := g.Size("grow"); err != nil || got != 8 {
		t.Fatalf("size = %d err %v, want 8", got, err)
	}
}

func TestTruncateResizesUnsealedFile(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("resize", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Write("resize", 0, []byte("abcdefgh")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := g.Truncate("resize", 4); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	body, err := g.Contents("resize")
	if err != nil {
		t.Fatalf("contents after shrink: %v", err)
	}
	if string(body) != "abcd" {
		t.Fatalf("contents after shrink = %q", string(body))
	}
	if err := g.Truncate("resize", 6); err != nil {
		t.Fatalf("grow: %v", err)
	}
	body, err = g.Contents("resize")
	if err != nil {
		t.Fatalf("contents after grow: %v", err)
	}
	if string(body) != "abcd\x00\x00" {
		t.Fatalf("contents after grow = %q", string(body))
	}
}

func TestReleaseForgetsFile(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("tmp", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Release("tmp"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := g.Size("tmp"); !errors.Is(err, impl.ErrNoSuchFile) {
		t.Fatalf("size after release = %v, want ErrNoSuchFile", err)
	}
	if err := g.Write("tmp", 0, []byte("x")); !errors.Is(err, impl.ErrNoSuchFile) {
		t.Fatalf("write after release = %v, want ErrNoSuchFile", err)
	}
	if err := g.Release("tmp"); !errors.Is(err, impl.ErrNoSuchFile) {
		t.Fatalf("second release = %v, want ErrNoSuchFile", err)
	}
}

func TestSnapshotRestoreKeepsContents(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("snap", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Write("snap", 0, []byte("snapdata")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := g.Seal("snap", impl.SealNoWrite); err != nil {
		t.Fatalf("seal: %v", err)
	}
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	other := newSealGuard()
	if err := other.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	body, err := other.Contents("snap")
	if err != nil {
		t.Fatalf("contents after restore: %v", err)
	}
	if string(body) != "snapdata" {
		t.Fatalf("restored contents = %q", string(body))
	}
	if got, err := other.Size("snap"); err != nil || got != 8 {
		t.Fatalf("restored size = %d err %v, want 8", got, err)
	}
}

func TestRestoreRejectsDamagedPayload(t *testing.T) {
	g := newSealGuard()
	if _, err := g.Create("keep", 8); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := g.Restore([]byte("broken")); !errors.Is(err, impl.ErrSnapshotDamaged) {
		t.Fatalf("damaged restore = %v, want ErrSnapshotDamaged", err)
	}
	if got, err := g.Size("keep"); err != nil || got != 8 {
		t.Fatalf("live file lost after rejected restore: size %d err %v", got, err)
	}
}
