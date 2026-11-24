package task

import (
	"errors"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-game-b-05"
)

type fakeUploader struct {
	uploads  []string
	releases []string
	failOn   map[string]error
}

func (f *fakeUploader) Upload(r impl.Resource) error {
	if err, ok := f.failOn[r.Key]; ok {
		return err
	}
	f.uploads = append(f.uploads, r.Key)
	return nil
}

func (f *fakeUploader) Release(r impl.Resource) error {
	f.releases = append(f.releases, r.Key)
	return nil
}

func newLoader(maxTx, maxPerTx, maxKeys int, lease time.Duration) (*impl.ResourceLoadTx, *fakeUploader) {
	up := &fakeUploader{failOn: make(map[string]error)}
	l := impl.NewResourceLoadTx(up, impl.Limits{
		MaxTx:    maxTx,
		MaxPerTx: maxPerTx,
		MaxKeys:  maxKeys,
		Lease:    lease,
	})
	return l, up
}

func beginScene(t *testing.T, l *impl.ResourceLoadTx, scene string) string {
	t.Helper()
	id, err := l.Begin(scene)
	if err != nil {
		t.Fatalf("begin scene %q: %v", scene, err)
	}
	return id
}

func tex(key string, bytes int) impl.Resource {
	return impl.Resource{Key: key, Kind: "texture", Bytes: bytes}
}

func TestBeginValidatesSceneUploaderAndLimits(t *testing.T) {
	l, _ := newLoader(2, 4, 8, time.Minute)
	if _, err := l.Begin(""); !errors.Is(err, impl.ErrBlankScene) {
		t.Fatalf("blank scene = %v, want ErrBlankScene", err)
	}

	bad, _ := newLoader(0, 4, 8, time.Minute)
	if _, err := bad.Begin("scene-a"); !errors.Is(err, impl.ErrBadLimits) {
		t.Fatalf("zero transaction limit = %v, want ErrBadLimits", err)
	}

	noUploader := impl.NewResourceLoadTx(nil, impl.Limits{MaxTx: 2, MaxPerTx: 4, MaxKeys: 8, Lease: time.Minute})
	if _, err := noUploader.Begin("scene-a"); !errors.Is(err, impl.ErrNoUploader) {
		t.Fatalf("missing uploader = %v, want ErrNoUploader", err)
	}
}

func TestBeginEnforcesTransactionCapAndSceneOwnership(t *testing.T) {
	l, _ := newLoader(2, 4, 8, time.Minute)
	first := beginScene(t, l, "scene-a")
	beginScene(t, l, "scene-b")

	if _, err := l.Begin("scene-c"); !errors.Is(err, impl.ErrTooManyTx) {
		t.Fatalf("third open transaction = %v, want ErrTooManyTx", err)
	}
	if _, err := l.Begin("scene-a"); !errors.Is(err, impl.ErrSceneBusy) {
		t.Fatalf("duplicate scene = %v, want ErrSceneBusy", err)
	}
	if got := l.ActiveTx(); got != 2 {
		t.Fatalf("active transactions = %d, want 2", got)
	}

	if err := l.Stage(first, tex("k1", 10)); err != nil {
		t.Fatalf("stage first scene: %v", err)
	}
	if err := l.Commit(first); err != nil {
		t.Fatalf("commit first scene: %v", err)
	}
	if _, err := l.Begin("scene-c"); err != nil {
		t.Fatalf("begin after commit freed a slot: %v", err)
	}
}

func TestStageValidatesTransactionAndResource(t *testing.T) {
	l, _ := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")

	if err := l.Stage("missing", tex("k1", 10)); !errors.Is(err, impl.ErrUnknownTx) {
		t.Fatalf("stage on unknown transaction = %v, want ErrUnknownTx", err)
	}
	if err := l.Stage(id, impl.Resource{Key: "", Kind: "texture", Bytes: 10}); !errors.Is(err, impl.ErrBadResource) {
		t.Fatalf("blank content key = %v, want ErrBadResource", err)
	}
	if err := l.Stage(id, impl.Resource{Key: "k1", Kind: "texture", Bytes: 0}); !errors.Is(err, impl.ErrBadResource) {
		t.Fatalf("zero byte resource = %v, want ErrBadResource", err)
	}
	if got := l.KeyCount(); got != 0 {
		t.Fatalf("rejected resources left %d keys resident, want 0", got)
	}
}

func TestStageIsIdempotentForIdenticalContent(t *testing.T) {
	l, up := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")

	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("stage k1: %v", err)
	}
	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("restage identical k1: %v", err)
	}
	if len(up.uploads) != 1 {
		t.Fatalf("uploads for one content key = %d, want 1", len(up.uploads))
	}
	if got := l.Progress(id); got != 0 {
		t.Fatalf("progress before commit = %v, want 0", got)
	}
	infos := l.TxInfos()
	if len(infos) != 1 || infos[0].Staged != 1 {
		t.Fatalf("staged resources = %+v, want exactly 1", infos)
	}
}

func TestStageRejectsSameKeyWithDifferentContent(t *testing.T) {
	l, up := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")

	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("stage k1: %v", err)
	}
	if err := l.Stage(id, tex("k1", 12)); !errors.Is(err, impl.ErrKeyConflict) {
		t.Fatalf("same key different size = %v, want ErrKeyConflict", err)
	}
	if len(up.uploads) != 1 {
		t.Fatalf("conflict uploaded %d times, want 1", len(up.uploads))
	}
	if got := l.RefCount("k1"); got != 1 {
		t.Fatalf("ref count after conflict = %d, want 1", got)
	}
}

func TestStageEnforcesPerTransactionResourceCap(t *testing.T) {
	l, _ := newLoader(2, 2, 16, time.Minute)
	id := beginScene(t, l, "scene-a")

	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("stage k1: %v", err)
	}
	if err := l.Stage(id, tex("k2", 10)); err != nil {
		t.Fatalf("stage k2: %v", err)
	}
	if err := l.Stage(id, tex("k3", 10)); !errors.Is(err, impl.ErrTxLimit) {
		t.Fatalf("third resource = %v, want ErrTxLimit", err)
	}
	if got := l.KeyCount(); got != 2 {
		t.Fatalf("resident keys = %d, want 2", got)
	}
}

func TestCommitPublishesOnceAndRequiresResources(t *testing.T) {
	l, _ := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")
	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("stage k1: %v", err)
	}
	if err := l.Commit(id); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := l.Progress(id); got != 1 {
		t.Fatalf("progress after commit = %v, want 1", got)
	}
	if state, err := l.State(id); err != nil || state != impl.TxCommitted {
		t.Fatalf("state after commit = %v/%v, want committed", state, err)
	}
	if err := l.Commit(id); !errors.Is(err, impl.ErrAlreadyCommitted) {
		t.Fatalf("second commit = %v, want ErrAlreadyCommitted", err)
	}
	if err := l.Stage(id, tex("k2", 10)); !errors.Is(err, impl.ErrTxClosed) {
		t.Fatalf("stage after commit = %v, want ErrTxClosed", err)
	}

	empty := beginScene(t, l, "scene-b")
	if err := l.Commit(empty); !errors.Is(err, impl.ErrEmptyTx) {
		t.Fatalf("commit without resources = %v, want ErrEmptyTx", err)
	}
}

func TestRollbackReleasesEveryResourceAndIsSingleShot(t *testing.T) {
	l, up := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")
	for _, key := range []string{"k1", "k2", "k3"} {
		if err := l.Stage(id, tex(key, 10)); err != nil {
			t.Fatalf("stage %s: %v", key, err)
		}
	}
	if err := l.Commit(id); err != nil {
		t.Fatalf("commit: %v", err)
	}

	n, err := l.Rollback(id)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if n != 3 {
		t.Fatalf("rollback released %d resources, want 3", n)
	}
	if len(up.releases) != 3 {
		t.Fatalf("uploader saw %d releases, want 3", len(up.releases))
	}
	if got := l.KeyCount(); got != 0 {
		t.Fatalf("resident keys after rollback = %d, want 0", got)
	}
	if got := l.Progress(id); got != -1 {
		t.Fatalf("progress after rollback = %v, want -1", got)
	}
	if _, err := l.Rollback(id); !errors.Is(err, impl.ErrAlreadyRolledBack) {
		t.Fatalf("second rollback = %v, want ErrAlreadyRolledBack", err)
	}
}

func TestSharedContentKeyUploadsOnceAndKeepsPerTransactionRefs(t *testing.T) {
	l, up := newLoader(4, 8, 16, time.Minute)

	one := beginScene(t, l, "scene-a")
	if err := l.Stage(one, tex("shared", 32)); err != nil {
		t.Fatalf("stage shared in scene-a: %v", err)
	}
	if err := l.Commit(one); err != nil {
		t.Fatalf("commit scene-a: %v", err)
	}

	two := beginScene(t, l, "scene-b")
	if err := l.Stage(two, tex("shared", 32)); err != nil {
		t.Fatalf("stage shared in scene-b: %v", err)
	}
	if err := l.Commit(two); err != nil {
		t.Fatalf("commit scene-b: %v", err)
	}
	if len(up.uploads) != 1 {
		t.Fatalf("uploads of one content key = %d, want 1", len(up.uploads))
	}
	if got := l.RefCount("shared"); got != 2 {
		t.Fatalf("ref count = %d, want 2", got)
	}

	if n, err := l.Rollback(one); err != nil || n != 1 {
		t.Fatalf("rollback scene-a = %d/%v, want 1/nil", n, err)
	}
	if len(up.releases) != 0 {
		t.Fatalf("shared key released while still referenced, releases = %d", len(up.releases))
	}
	if got := l.RefCount("shared"); got != 1 {
		t.Fatalf("ref count after first rollback = %d, want 1", got)
	}

	if n, err := l.Rollback(two); err != nil || n != 1 {
		t.Fatalf("rollback scene-b = %d/%v, want 1/nil", n, err)
	}
	if len(up.releases) != 1 {
		t.Fatalf("last rollback releases = %d, want 1", len(up.releases))
	}
}

func TestUploadFailurePoisonsTransaction(t *testing.T) {
	l, up := newLoader(2, 8, 16, time.Minute)
	boom := errors.New("device lost")
	up.failOn["bad"] = boom
	id := beginScene(t, l, "scene-a")

	if err := l.Stage(id, tex("good", 10)); err != nil {
		t.Fatalf("stage good: %v", err)
	}
	if err := l.Stage(id, tex("bad", 10)); !errors.Is(err, boom) {
		t.Fatalf("failed upload = %v, want device error", err)
	}
	if err := l.Stage(id, tex("later", 10)); !errors.Is(err, impl.ErrTxFailed) {
		t.Fatalf("stage after failure = %v, want ErrTxFailed", err)
	}
	if err := l.Commit(id); !errors.Is(err, impl.ErrStageFailed) {
		t.Fatalf("commit after failure = %v, want ErrStageFailed", err)
	}
	n, err := l.Rollback(id)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if n != 1 {
		t.Fatalf("rollback released %d resources, want only the 1 created before the failure", n)
	}
	if len(up.releases) != 1 {
		t.Fatalf("uploader releases = %d, want 1", len(up.releases))
	}
	if got := l.KeyCount(); got != 0 {
		t.Fatalf("keys left after poisoned rollback = %d, want 0", got)
	}
}

func TestCancelAbandonsOpenTransaction(t *testing.T) {
	l, up := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")
	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := l.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if state, err := l.State(id); err != nil || state != impl.TxCanceled {
		t.Fatalf("state after cancel = %v/%v, want canceled", state, err)
	}
	if err := l.Stage(id, tex("k2", 10)); !errors.Is(err, impl.ErrTxClosed) {
		t.Fatalf("stage after cancel = %v, want ErrTxClosed", err)
	}
	if err := l.Commit(id); !errors.Is(err, impl.ErrTxClosed) {
		t.Fatalf("commit after cancel = %v, want ErrTxClosed", err)
	}
	if err := l.Cancel(id); err != nil {
		t.Fatalf("repeated cancel must be a no-op, got %v", err)
	}
	if len(up.releases) != 1 {
		t.Fatalf("cancel releases = %d, want 1", len(up.releases))
	}
	if got := l.ActiveTx(); got != 0 {
		t.Fatalf("active transactions after cancel = %d, want 0", got)
	}
	if err := l.Cancel("missing"); !errors.Is(err, impl.ErrUnknownTx) {
		t.Fatalf("cancel unknown = %v, want ErrUnknownTx", err)
	}
}

func TestReclaimReleasesAbandonedTransactions(t *testing.T) {
	l, up := newLoader(4, 8, 16, 100*time.Millisecond)
	stale := beginScene(t, l, "scene-a")
	if err := l.Stage(stale, tex("k1", 10)); err != nil {
		t.Fatalf("stage stale: %v", err)
	}
	committed := beginScene(t, l, "scene-b")
	if err := l.Stage(committed, tex("k2", 10)); err != nil {
		t.Fatalf("stage committed: %v", err)
	}
	if err := l.Commit(committed); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n, err := l.Reclaim(time.Now()); err != nil || n != 0 {
		t.Fatalf("reclaim before the lease expired = %d/%v, want 0/nil", n, err)
	}
	if n, err := l.Reclaim(time.Now().Add(2 * time.Second)); err != nil || n != 1 {
		t.Fatalf("reclaim after the lease expired = %d/%v, want 1/nil", n, err)
	}
	if got := l.ActiveTx(); got != 0 {
		t.Fatalf("active transactions after reclaim = %d, want 0", got)
	}
	if state, err := l.State(stale); err != nil || state != impl.TxCanceled {
		t.Fatalf("stale state = %v/%v, want canceled", state, err)
	}
	if len(up.releases) != 1 {
		t.Fatalf("reclaim releases = %d, want 1", len(up.releases))
	}
	if got := l.RefCount("k2"); got != 1 {
		t.Fatalf("committed resource after reclaim = %d refs, want 1", got)
	}
}

func TestSnapshotRestoreKeepsCommittedAndOpenWork(t *testing.T) {
	l, up := newLoader(4, 8, 16, time.Minute)
	done := beginScene(t, l, "scene-a")
	if err := l.Stage(done, tex("k1", 10)); err != nil {
		t.Fatalf("stage k1: %v", err)
	}
	if err := l.Commit(done); err != nil {
		t.Fatalf("commit k1: %v", err)
	}
	open := beginScene(t, l, "scene-b")
	if err := l.Stage(open, tex("k2", 20)); err != nil {
		t.Fatalf("stage k2: %v", err)
	}

	payload, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored, up2 := newLoader(4, 8, 16, time.Minute)
	if err := restored.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := restored.RefCount("k1"); got != 1 {
		t.Fatalf("restored k1 refs = %d, want 1", got)
	}
	if got := restored.RefCount("k2"); got != 1 {
		t.Fatalf("restored k2 refs = %d, want 1", got)
	}
	if got := restored.KeyCount(); got != 2 {
		t.Fatalf("restored keys = %d, want 2", got)
	}
	if got := restored.ActiveTx(); got != 1 {
		t.Fatalf("restored open transactions = %d, want 1", got)
	}
	if got := restored.Progress(done); got != 1 {
		t.Fatalf("restored committed progress = %v, want 1", got)
	}
	if got := restored.Progress(open); got != 0 {
		t.Fatalf("restored open progress = %v, want 0", got)
	}
	if err := restored.Commit(open); err != nil {
		t.Fatalf("commit restored open transaction: %v", err)
	}
	if n, err := restored.Rollback(done); err != nil || n != 1 {
		t.Fatalf("rollback restored committed transaction = %d/%v, want 1/nil", n, err)
	}
	if len(up2.releases) != 1 {
		t.Fatalf("restored loader releases = %d, want 1", len(up2.releases))
	}

	_ = up
}

func TestRestoreRejectsBadPayloadAndKeepsCurrentState(t *testing.T) {
	l, _ := newLoader(2, 8, 16, time.Minute)
	id := beginScene(t, l, "scene-a")
	if err := l.Stage(id, tex("k1", 10)); err != nil {
		t.Fatalf("stage: %v", err)
	}

	if err := l.Restore([]byte(`{"version":99,"txs":[],"keys":[]}`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("future snapshot version = %v, want ErrBadSnapshot", err)
	}
	if err := l.Restore([]byte(`{"version":1,"txs":[{"id":"tx-9"`)); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("truncated snapshot = %v, want ErrBadSnapshot", err)
	}
	if got := l.RefCount("k1"); got != 1 {
		t.Fatalf("ref count after rejected restores = %d, want 1", got)
	}
	if got := l.ActiveTx(); got != 1 {
		t.Fatalf("open transactions after rejected restores = %d, want 1", got)
	}
	if err := l.SetLease(0); !errors.Is(err, impl.ErrBadLease) {
		t.Fatalf("zero lease = %v, want ErrBadLease", err)
	}
}

func TestConcurrentTransactionsAndStagesKeepExactCounts(t *testing.T) {
	l, up := newLoader(8, 32, 128, time.Minute)

	const scenes = 8
	ids := make([]string, scenes)
	errs := make(chan error, scenes)
	var wg sync.WaitGroup
	for i := 0; i < scenes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := l.Begin("scene-" + string(rune('a'+i)))
			if err != nil {
				errs <- err
				return
			}
			ids[i] = id
			if err := l.Stage(id, tex("scene-key-"+string(rune('a'+i)), 10+i)); err != nil {
				errs <- err
				return
			}
			errs <- l.Commit(id)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent transaction failed: %v", err)
		}
	}
	if got := l.KeyCount(); got != scenes {
		t.Fatalf("keys after concurrent commits = %d, want %d", got, scenes)
	}
	if got := l.ActiveTx(); got != 0 {
		t.Fatalf("open transactions after concurrent commits = %d, want 0", got)
	}

	shared := beginScene(t, l, "scene-shared")
	const parts = 24
	stageErrs := make(chan error, parts)
	var wg2 sync.WaitGroup
	for i := 0; i < parts; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			stageErrs <- l.Stage(shared, tex("part-"+string(rune('a'+i)), 100+i))
		}(i)
	}
	wg2.Wait()
	close(stageErrs)
	for err := range stageErrs {
		if err != nil {
			t.Fatalf("concurrent stage failed: %v", err)
		}
	}
	if got := l.RefCount("part-a"); got != 1 {
		t.Fatalf("ref count of one concurrent part = %d, want 1", got)
	}
	if got := len(up.uploads); got != scenes+parts {
		t.Fatalf("uploads = %d, want %d", got, scenes+parts)
	}
	if err := l.Commit(shared); err != nil {
		t.Fatalf("commit shared transaction: %v", err)
	}
	n, err := l.Rollback(shared)
	if err != nil || n != parts {
		t.Fatalf("rollback shared transaction = %d/%v, want %d/nil", n, err, parts)
	}
	if got := len(up.releases); got != parts {
		t.Fatalf("releases after rollback = %d, want %d", got, parts)
	}
	if got := l.KeyCount(); got != scenes {
		t.Fatalf("remaining keys = %d, want %d", got, scenes)
	}
	_ = ids
}
