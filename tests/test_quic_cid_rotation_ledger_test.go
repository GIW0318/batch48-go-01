package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	impl "gsb/batch48-go-safety-a-26"
)

func t1Start() time.Time {
	return time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
}

func newT1Ledger() *impl.CIDRotationLedger {
	return impl.NewCIDRotationLedger(impl.CIDLimits{
		MaxActivePerConn: 4,
		MaxConnections:   8,
		GracePeriod:      5 * time.Second,
	})
}

func cidSet(cids []impl.CID) map[impl.CID]bool {
	out := make(map[impl.CID]bool, len(cids))
	for _, c := range cids {
		out[c] = true
	}
	return out
}

func TestOpenRejectsBlankAndDuplicateConnection(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("   ", false); !errors.Is(err, impl.ErrBlankConn) {
		t.Fatalf("blank connection id must be rejected with ErrBlankConn, got %v", err)
	}
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("opening a fresh connection must succeed, got %v", err)
	}
	if err := l.Open("conn-a", true); !errors.Is(err, impl.ErrConnExists) {
		t.Fatalf("duplicate connection must be rejected with ErrConnExists, got %v", err)
	}
}

func TestZeroLengthConnectionStaysInDefaultBucket(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("zc-1", true); err != nil {
		t.Fatalf("zero length connection must open, got %v", err)
	}
	if _, err := l.Issue("zc-1", 1); !errors.Is(err, impl.ErrZeroLengthCID) {
		t.Fatalf("zero length connection must not receive new CIDs, got %v", err)
	}
	if err := l.Claim("zc-1", impl.CID(500)); !errors.Is(err, impl.ErrZeroLengthCID) {
		t.Fatalf("zero length connection must not claim CIDs, got %v", err)
	}
	defaults := l.DefaultConnections()
	if len(defaults) != 1 || defaults[0] != "zc-1" {
		t.Fatalf("default bucket = %v, want exactly zc-1", defaults)
	}
	if got := len(l.ActiveCIDs("zc-1")); got != 0 {
		t.Fatalf("zero length connection active CIDs = %d, want 0", got)
	}
	if conn, ok := l.Route(impl.ZeroLengthCID); ok || conn != "" {
		t.Fatalf("zero length CID must not resolve through the route table, got %q/%v", conn, ok)
	}
}

func TestIssueReturnsDistinctCIDsAndRoutesThem(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 3)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if len(cids) != 3 {
		t.Fatalf("issued %d CIDs, want 3", len(cids))
	}
	seen := cidSet(cids)
	if len(seen) != 3 {
		t.Fatalf("issued CIDs are not distinct: %v", cids)
	}
	for _, c := range cids {
		if c == impl.ZeroLengthCID {
			t.Fatalf("issued CID must not be the zero length tag")
		}
		conn, ok := l.Route(c)
		if !ok || conn != "conn-a" {
			t.Fatalf("route of %d = %q/%v, want conn-a/true", c, conn, ok)
		}
	}
	active := l.ActiveCIDs("conn-a")
	if len(active) != 3 {
		t.Fatalf("active CIDs = %d, want 3", len(active))
	}
	if len(cidSet(active)) != 3 {
		t.Fatalf("active CIDs contain duplicates: %v", active)
	}
}

func TestIssueEnforcesCountAndActiveLimitAtomically(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	if _, err := l.Issue("conn-a", 0); !errors.Is(err, impl.ErrBadCount) {
		t.Fatalf("zero count must be rejected with ErrBadCount, got %v", err)
	}
	if _, err := l.Issue("conn-a", -2); !errors.Is(err, impl.ErrBadCount) {
		t.Fatalf("negative count must be rejected with ErrBadCount, got %v", err)
	}
	if _, err := l.Issue("conn-a", 3); err != nil {
		t.Fatalf("issue of three CIDs must succeed, got %v", err)
	}
	if _, err := l.Issue("conn-a", 2); !errors.Is(err, impl.ErrActiveLimit) {
		t.Fatalf("over limit issue must be rejected with ErrActiveLimit, got %v", err)
	}
	if got := len(l.ActiveCIDs("conn-a")); got != 3 {
		t.Fatalf("active CIDs after rejected issue = %d, want 3", got)
	}
}

func TestClaimBindsExternalCIDAndIsIdempotent(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	if err := l.Claim("conn-a", impl.CID(9001)); err != nil {
		t.Fatalf("claiming an unused CID must succeed, got %v", err)
	}
	if err := l.Claim("conn-a", impl.CID(9001)); err != nil {
		t.Fatalf("claiming the same CID again for the same connection must be idempotent, got %v", err)
	}
	conn, ok := l.Route(impl.CID(9001))
	if !ok || conn != "conn-a" {
		t.Fatalf("route of claimed CID = %q/%v, want conn-a/true", conn, ok)
	}
	active := l.ActiveCIDs("conn-a")
	if len(active) != 1 || active[0] != impl.CID(9001) {
		t.Fatalf("active CIDs = %v, want exactly the claimed CID", active)
	}
	if err := l.Claim("conn-a", impl.ZeroLengthCID); !errors.Is(err, impl.ErrZeroLengthCID) {
		t.Fatalf("claiming the zero length CID must be rejected, got %v", err)
	}
}

func TestRetireMustBeMonotonic(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	if _, err := l.Issue("conn-a", 3); err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if err := l.Retire("conn-a", 2, t1Start()); err != nil {
		t.Fatalf("first retirement must succeed, got %v", err)
	}
	if err := l.Retire("conn-a", 1, t1Start()); !errors.Is(err, impl.ErrRetireBackwards) {
		t.Fatalf("retire_prior_to moving backwards must report ErrRetireBackwards, got %v", err)
	}
}

func TestRetireMovesCIDsIntoGrace(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 3)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if err := l.Retire("conn-a", 2, t1Start()); err != nil {
		t.Fatalf("retire must succeed, got %v", err)
	}
	active := l.ActiveCIDs("conn-a")
	if len(active) != 2 {
		t.Fatalf("active CIDs after retirement = %v, want two entries", active)
	}
	for _, c := range active {
		if c == cids[0] {
			t.Fatalf("retired CID %d is still listed as active", c)
		}
	}
	retired := l.RetiredCIDs("conn-a")
	if len(retired) != 1 || retired[0] != cids[0] {
		t.Fatalf("retired CIDs = %v, want exactly %d", retired, cids[0])
	}
	conn, ok := l.Route(cids[0])
	if !ok || conn != "conn-a" {
		t.Fatalf("retired CID inside grace must still route to conn-a, got %q/%v", conn, ok)
	}
}

func TestSweepReclaimsRetiredRoutesAfterGrace(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 3)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if err := l.Retire("conn-a", 2, t1Start()); err != nil {
		t.Fatalf("retire must succeed, got %v", err)
	}
	if got := l.Sweep(t1Start().Add(time.Second)); got != 0 {
		t.Fatalf("sweep inside the grace window reclaimed %d routes, want 0", got)
	}
	if got := l.Sweep(t1Start().Add(5*time.Second + time.Millisecond)); got != 1 {
		t.Fatalf("sweep after the grace window reclaimed %d routes, want 1", got)
	}
	if _, ok := l.Route(cids[0]); ok {
		t.Fatalf("retired CID %d must not route after the grace window", cids[0])
	}
	if conn, ok := l.Route(cids[1]); !ok || conn != "conn-a" {
		t.Fatalf("active CID %d must keep routing, got %q/%v", cids[1], conn, ok)
	}
	if got := len(l.RetiredCIDs("conn-a")); got != 0 {
		t.Fatalf("retired CIDs after sweep = %d, want 0", got)
	}
}

func TestLedgerReportsUnknownConnections(t *testing.T) {
	l := newT1Ledger()
	if err := l.Retire("ghost", 2, t1Start()); !errors.Is(err, impl.ErrConnNotFound) {
		t.Fatalf("retire of an unknown connection must report ErrConnNotFound, got %v", err)
	}
	if err := l.Claim("ghost", impl.CID(7)); !errors.Is(err, impl.ErrConnNotFound) {
		t.Fatalf("claim on an unknown connection must report ErrConnNotFound, got %v", err)
	}
	if err := l.Release("ghost", t1Start()); !errors.Is(err, impl.ErrConnNotFound) {
		t.Fatalf("release of an unknown connection must report ErrConnNotFound, got %v", err)
	}
}

func TestReleaseBlocksFurtherIssueAndRepeats(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	if _, err := l.Issue("conn-a", 2); err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if err := l.Release("conn-a", t1Start()); err != nil {
		t.Fatalf("release must succeed, got %v", err)
	}
	if err := l.Release("conn-a", t1Start()); !errors.Is(err, impl.ErrConnReleased) {
		t.Fatalf("releasing twice must report ErrConnReleased, got %v", err)
	}
	if _, err := l.Issue("conn-a", 1); !errors.Is(err, impl.ErrConnReleased) {
		t.Fatalf("issue after release must report ErrConnReleased, got %v", err)
	}
	if err := l.Open("conn-a", false); !errors.Is(err, impl.ErrConnExists) {
		t.Fatalf("reopening a released connection must still report ErrConnExists, got %v", err)
	}
}

func TestRouteReportsKnownAndUnknownCIDs(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 2)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if conn, ok := l.Route(cids[0]); !ok || conn != "conn-a" {
		t.Fatalf("known CID route = %q/%v, want conn-a/true", conn, ok)
	}
	if conn, ok := l.Route(impl.CID(777777)); ok || conn != "" {
		t.Fatalf("unknown CID route = %q/%v, want empty/false", conn, ok)
	}
}

func TestConcurrentIssueKeepsCIDsUnique(t *testing.T) {
	l := impl.NewCIDRotationLedger(impl.CIDLimits{
		MaxActivePerConn: 64,
		MaxConnections:   4,
		GracePeriod:      time.Minute,
	})
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	const workers = 8
	const perWorker = 4
	issued := make([]impl.CID, workers*perWorker)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cids, err := l.Issue("conn-a", perWorker)
			if err != nil {
				errs[i] = err
				return
			}
			copy(issued[i*perWorker:(i+1)*perWorker], cids)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent issue %d failed: %v", i, err)
		}
	}
	unique := cidSet(issued)
	if len(unique) != workers*perWorker {
		t.Fatalf("concurrent issue produced %d unique CIDs, want %d", len(unique), workers*perWorker)
	}
	for _, c := range issued {
		conn, ok := l.Route(c)
		if !ok || conn != "conn-a" {
			t.Fatalf("concurrent CID %d route = %q/%v, want conn-a/true", c, conn, ok)
		}
	}
	if got := len(l.ActiveCIDs("conn-a")); got != workers*perWorker {
		t.Fatalf("active CIDs = %d, want %d", got, workers*perWorker)
	}
}

func TestConcurrentRouteStaysBoundDuringRetire(t *testing.T) {
	l := impl.NewCIDRotationLedger(impl.CIDLimits{
		MaxActivePerConn: 8,
		MaxConnections:   4,
		GracePeriod:      time.Minute,
	})
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 6)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	errCh := make(chan error, len(cids)+1)
	var wg sync.WaitGroup
	for _, c := range cids {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				conn, ok := l.Route(c)
				if !ok || conn != "conn-a" {
					errCh <- fmt.Errorf("route of %d during retire = %q/%v", c, conn, ok)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := l.Retire("conn-a", 3, t1Start()); err != nil {
			errCh <- fmt.Errorf("concurrent retire failed: %w", err)
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if got := len(l.RetiredCIDs("conn-a")); got != 2 {
		t.Fatalf("retired CIDs = %d, want 2", got)
	}
}

func TestSnapshotRestoreKeepsRetiredCIDsRoutable(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 3)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if err := l.Retire("conn-a", 2, t1Start()); err != nil {
		t.Fatalf("retire must succeed, got %v", err)
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	if len(snap) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := newT1Ledger()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if conn, ok := restored.Route(cids[0]); !ok || conn != "conn-a" {
		t.Fatalf("restored retired CID route = %q/%v, want conn-a/true", conn, ok)
	}
	if got := len(restored.ActiveCIDs("conn-a")); got != 2 {
		t.Fatalf("restored active CIDs = %d, want 2", got)
	}
	if got := len(restored.RetiredCIDs("conn-a")); got != 1 {
		t.Fatalf("restored retired CIDs = %d, want 1", got)
	}
}

func TestSnapshotRestorePreservesGraceDeadline(t *testing.T) {
	l := newT1Ledger()
	if err := l.Open("conn-a", false); err != nil {
		t.Fatalf("open must succeed, got %v", err)
	}
	cids, err := l.Issue("conn-a", 3)
	if err != nil {
		t.Fatalf("issue must succeed, got %v", err)
	}
	if err := l.Retire("conn-a", 2, t1Start()); err != nil {
		t.Fatalf("retire must succeed, got %v", err)
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := newT1Ledger()
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore must accept the snapshot, got %v", err)
	}
	if got := restored.Sweep(t1Start().Add(5*time.Second + time.Millisecond)); got != 1 {
		t.Fatalf("restored sweep reclaimed %d routes, want 1", got)
	}
	if _, ok := restored.Route(cids[0]); ok {
		t.Fatalf("restored retired CID must expire at the original grace deadline")
	}
	if conn, ok := restored.Route(cids[1]); !ok || conn != "conn-a" {
		t.Fatalf("restored active CID route = %q/%v, want conn-a/true", conn, ok)
	}
}
