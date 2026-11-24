package task

import (
	"errors"
	"strings"
	"sync"
	"testing"

	impl "gsb/batch48-go-safety-a-06"
)

func TestRegisterSectorNormalisesURI(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "sector-1", "HTTPS://Sector.Example.COM:443/"); err != nil {
		t.Fatalf("registering a valid sector must succeed, got %v", err)
	}
	sec, err := g.AssignClient("tenant-a", "client-1", "sector-1")
	if err != nil {
		t.Fatalf("assigning a registered sector must succeed, got %v", err)
	}
	if sec.ID != "sector-1" {
		t.Fatalf("sector id = %q, want sector-1", sec.ID)
	}
	if sec.URI != "https://sector.example.com" {
		t.Fatalf("sector uri = %q, want the normalised https://sector.example.com", sec.URI)
	}
	if err := g.RegisterSector("tenant-a", "sector-2", "https://sector.example.com/"); !errors.Is(err, impl.ErrDuplicateSectorURI) {
		t.Fatalf("re-registering the same normalised uri = %v, want ErrDuplicateSectorURI", err)
	}
	if err := g.RegisterSector("tenant-a", "sector-1", "https://other.example.com"); !errors.Is(err, impl.ErrInvalidSector) {
		t.Fatalf("re-using a sector id = %v, want ErrInvalidSector", err)
	}
}

func TestRegisterSectorRejectsEmptyFields(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("", "sector-1", "https://a.example.com"); !errors.Is(err, impl.ErrInvalidTenant) {
		t.Fatalf("empty tenant = %v, want ErrInvalidTenant", err)
	}
	if err := g.RegisterSector("tenant-a", "", "https://a.example.com"); !errors.Is(err, impl.ErrInvalidSector) {
		t.Fatalf("empty sector id = %v, want ErrInvalidSector", err)
	}
	if err := g.RegisterSector("tenant-a", "sector-1", "ftp://a.example.com"); !errors.Is(err, impl.ErrInvalidSector) {
		t.Fatalf("non-https sector uri = %v, want ErrInvalidSector", err)
	}
}

func TestSectorLimitEnforced(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{MaxSectorsPerTenant: 2})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("first sector must be accepted, got %v", err)
	}
	if err := g.RegisterSector("tenant-a", "s2", "https://two.example.com"); err != nil {
		t.Fatalf("second sector must be accepted, got %v", err)
	}
	if err := g.RegisterSector("tenant-a", "s3", "https://three.example.com"); !errors.Is(err, impl.ErrSectorLimit) {
		t.Fatalf("third sector over the limit = %v, want ErrSectorLimit", err)
	}
	if got := g.SectorCount("tenant-a"); got != 2 {
		t.Fatalf("sector count = %d, want 2 after the rejected registration", got)
	}
}

func TestClientLimitEnforced(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{MaxClientsPerSector: 2})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	for _, id := range []string{"c1", "c2"} {
		if _, err := g.AssignClient("tenant-a", id, "s1"); err != nil {
			t.Fatalf("assigning %s must succeed, got %v", id, err)
		}
	}
	if _, err := g.AssignClient("tenant-a", "c3", "s1"); !errors.Is(err, impl.ErrClientLimit) {
		t.Fatalf("third client over the sector limit = %v, want ErrClientLimit", err)
	}
	if got := g.ClientCount("tenant-a", "s1"); got != 2 {
		t.Fatalf("client count = %d, want 2", got)
	}
}

func TestSameSectorClientsShareSubject(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	for _, id := range []string{"c1", "c2"} {
		if _, err := g.AssignClient("tenant-a", id, "s1"); err != nil {
			t.Fatalf("assigning %s failed: %v", id, err)
		}
	}
	first, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject for c1 failed: %v", err)
	}
	second, err := g.SubjectFor("tenant-a", "c2", "user-7")
	if err != nil {
		t.Fatalf("subject for c2 failed: %v", err)
	}
	if first == "" || !strings.HasPrefix(first, "ps_") {
		t.Fatalf("subject %q must carry the pairwise prefix ps_", first)
	}
	if first != second {
		t.Fatalf("clients in the same sector got %q and %q, want one shared subject", first, second)
	}
}

func TestCrossSectorSubjectsDiffer(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering s1 failed: %v", err)
	}
	if err := g.RegisterSector("tenant-a", "s2", "https://two.example.com"); err != nil {
		t.Fatalf("registering s2 failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning c1 failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c2", "s2"); err != nil {
		t.Fatalf("assigning c2 failed: %v", err)
	}
	inS1, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject in s1 failed: %v", err)
	}
	inS2, err := g.SubjectFor("tenant-a", "c2", "user-7")
	if err != nil {
		t.Fatalf("subject in s2 failed: %v", err)
	}
	if inS1 == inS2 {
		t.Fatalf("the same user got the same subject %q in two sectors", inS1)
	}
}

func TestSubjectIsOpaqueAndStable(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	first, err := g.SubjectFor("tenant-a", "c1", "alice@example.com")
	if err != nil {
		t.Fatalf("first subject failed: %v", err)
	}
	second, err := g.SubjectFor("tenant-a", "c1", "alice@example.com")
	if err != nil {
		t.Fatalf("second subject failed: %v", err)
	}
	if first != second {
		t.Fatalf("the same local user got %q then %q", first, second)
	}
	if strings.Contains(first, "alice") || strings.Contains(first, "example.com") {
		t.Fatalf("subject %q leaks the local user id", first)
	}
	other, err := g.SubjectFor("tenant-a", "c1", "bob@example.com")
	if err != nil {
		t.Fatalf("subject for another user failed: %v", err)
	}
	if other == first {
		t.Fatalf("two local users collapsed into the same subject %q", first)
	}
}

func TestSubjectForUnknownClient(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.SubjectFor("tenant-a", "never-assigned", "user-7"); !errors.Is(err, impl.ErrUnknownClient) {
		t.Fatalf("subject for an unassigned client = %v, want ErrUnknownClient", err)
	}
	if _, err := g.SubjectFor("tenant-a", "never-assigned", ""); !errors.Is(err, impl.ErrInvalidUser) {
		t.Fatalf("subject for an empty local user = %v, want ErrInvalidUser", err)
	}
}

func TestAssignClientIsIdempotentAndExclusive(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering s1 failed: %v", err)
	}
	if err := g.RegisterSector("tenant-a", "s2", "https://two.example.com"); err != nil {
		t.Fatalf("registering s2 failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("first assignment failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("repeating the same assignment failed: %v", err)
	}
	if got := g.ClientCount("tenant-a", "s1"); got != 1 {
		t.Fatalf("client count after two identical assignments = %d, want 1", got)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s2"); !errors.Is(err, impl.ErrAlreadyAssigned) {
		t.Fatalf("assigning an already attached client to another sector = %v, want ErrAlreadyAssigned", err)
	}
}

func TestRotateSectorKeyKeepsIssuedSubjects(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	before, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject before the rotation failed: %v", err)
	}
	if err := g.RotateSectorKey("tenant-a", "s1"); err != nil {
		t.Fatalf("rotating the sector key failed: %v", err)
	}
	epoch, err := g.KeyEpoch("tenant-a", "s1")
	if err != nil {
		t.Fatalf("reading the key epoch failed: %v", err)
	}
	if epoch != 1 {
		t.Fatalf("key epoch after one rotation = %d, want 1", epoch)
	}
	after, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject after the rotation failed: %v", err)
	}
	if after != before {
		t.Fatalf("rotation changed an issued subject from %q to %q", before, after)
	}
	if err := g.RotateSectorKey("tenant-a", "missing"); !errors.Is(err, impl.ErrUnknownSector) {
		t.Fatalf("rotating an unknown sector = %v, want ErrUnknownSector", err)
	}
}

func TestMoveClientRemapsAndTombstones(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering s1 failed: %v", err)
	}
	if err := g.RegisterSector("tenant-a", "s2", "https://two.example.com"); err != nil {
		t.Fatalf("registering s2 failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	oldSubject, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject before the move failed: %v", err)
	}
	remap, err := g.MoveClient("tenant-a", "c1", "s2")
	if err != nil {
		t.Fatalf("moving the client failed: %v", err)
	}
	if len(remap.Items) != 1 {
		t.Fatalf("remap carried %d items, want 1 for the single known user", len(remap.Items))
	}
	item := remap.Items[0]
	if item.LocalUserID != "user-7" {
		t.Fatalf("remapped user = %q, want user-7", item.LocalUserID)
	}
	if item.OldSubject != oldSubject {
		t.Fatalf("remapped old subject = %q, want %q", item.OldSubject, oldSubject)
	}
	if item.NewSubject == oldSubject || item.NewSubject == "" {
		t.Fatalf("remapped new subject = %q, want a fresh non-empty value", item.NewSubject)
	}
	if remap.FromSector != "s1" || remap.ToSector != "s2" {
		t.Fatalf("remap sectors = %q -> %q, want s1 -> s2", remap.FromSector, remap.ToSector)
	}
	if got := g.TombstoneCount("tenant-a"); got != 1 {
		t.Fatalf("tombstone count after the move = %d, want 1", got)
	}
	resolved, ok := g.Resolve("tenant-a", oldSubject)
	if !ok || resolved != item.NewSubject {
		t.Fatalf("resolving the retired subject = (%q, %v), want (%q, true)", resolved, ok, item.NewSubject)
	}
	if got := g.ClientCount("tenant-a", "s1"); got != 0 {
		t.Fatalf("old sector still holds %d clients after the move", got)
	}
	if got := g.ClientCount("tenant-a", "s2"); got != 1 {
		t.Fatalf("new sector holds %d clients after the move, want 1", got)
	}
}

func TestMoveClientUnknownSectorKeepsAssignment(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	before, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject before the failed move = %v", err)
	}
	if _, err := g.MoveClient("tenant-a", "c1", "s9"); !errors.Is(err, impl.ErrUnknownSector) {
		t.Fatalf("moving to an unregistered sector = %v, want ErrUnknownSector", err)
	}
	after, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject after the failed move = %v", err)
	}
	if after != before {
		t.Fatalf("a rejected move changed the subject from %q to %q", before, after)
	}
	if got := g.ClientCount("tenant-a", "s1"); got != 1 {
		t.Fatalf("rejected move detached the client, sector s1 holds %d clients", got)
	}
}

func TestTombstoneIsIdempotentAndUnresolvable(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	subject, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject failed: %v", err)
	}
	if err := g.Tombstone("tenant-a", subject); err != nil {
		t.Fatalf("retiring a live subject failed: %v", err)
	}
	if err := g.Tombstone("tenant-a", subject); err != nil {
		t.Fatalf("repeating the retirement must be idempotent, got %v", err)
	}
	if got := g.TombstoneCount("tenant-a"); got != 1 {
		t.Fatalf("tombstone count = %d after two retirement calls, want 1", got)
	}
	if resolved, ok := g.Resolve("tenant-a", subject); ok {
		t.Fatalf("a retired subject still resolves to %q", resolved)
	}
	if err := g.Tombstone("tenant-a", "ps_00000000000000000000000000000000"); !errors.Is(err, impl.ErrUnknownSubject) {
		t.Fatalf("retiring an unknown subject = %v, want ErrUnknownSubject", err)
	}
}

func TestSnapshotRestoreKeepsIssuedState(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	subject, err := g.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject failed: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("snapshot must not be empty")
	}
	restored := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := restored.SectorCount("tenant-a"); got != 1 {
		t.Fatalf("restored sector count = %d, want 1", got)
	}
	if got := restored.ClientCount("tenant-a", "s1"); got != 1 {
		t.Fatalf("restored client count = %d, want 1", got)
	}
	after, err := restored.SubjectFor("tenant-a", "c1", "user-7")
	if err != nil {
		t.Fatalf("subject after restore failed: %v", err)
	}
	if after != subject {
		t.Fatalf("restore changed the issued subject from %q to %q", subject, after)
	}
	if err := restored.Restore([]byte("not a snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("restoring garbage = %v, want ErrBadSnapshot", err)
	}
}

func TestConcurrentSubjectForIsConsistent(t *testing.T) {
	g := impl.NewPairwiseSubjectGuard(impl.Options{})
	if err := g.RegisterSector("tenant-a", "s1", "https://one.example.com"); err != nil {
		t.Fatalf("registering the sector failed: %v", err)
	}
	if _, err := g.AssignClient("tenant-a", "c1", "s1"); err != nil {
		t.Fatalf("assigning the client failed: %v", err)
	}
	const workers = 8
	results := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = g.SubjectFor("tenant-a", "c1", "user-7")
		}(i)
	}
	wg.Wait()
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d failed: %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Fatalf("worker %d got %q while worker 0 got %q", i, results[i], results[0])
		}
	}
	if results[0] == "" {
		t.Fatalf("concurrent callers got an empty subject")
	}
}
