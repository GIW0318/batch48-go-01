package task

import (
	"errors"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-14"
)

var (
	fixedInstructions   = []string{"MOV R1, sym:parse_hdr", "CMP R1, 0", "JNE sym:error_path"}
	renamedInstructions = []string{"  mov   r1, sym:parse_header ", "cmp r1,  0", "jne sym:retry_path"}
	changedInstructions = []string{"MOV R1, sym:parse_hdr", "CMP R1, 1", "JNE sym:error_path"}
)

func newFixChecker(t *testing.T) (*impl.PatchPresenceChecker, impl.Fix) {
	t.Helper()
	c := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 4, CacheSize: 8})
	fix := impl.Fix{
		ID:           "CVE-2025-1010",
		Component:    "libfoo",
		IntroducedIn: "1.0.0",
		FixedIn:      "2.5.0",
		Function:     "parse_hdr",
		Digest:       c.Fingerprint(fixedInstructions),
	}
	if err := c.Register(fix); err != nil {
		t.Fatalf("register fix: %v", err)
	}
	return c, fix
}

func refWith(id, version string, instructions []string) impl.ArtifactRef {
	return impl.ArtifactRef{
		ID:        id,
		Component: "libfoo",
		Version:   version,
		Functions: []impl.Function{{Name: "parse_hdr", Instructions: instructions}},
	}
}

func hasEvidence(p impl.Presence, want string) bool {
	for _, e := range p.Evidence {
		if e == want {
			return true
		}
	}
	return false
}

func TestCompareVersionsOrdersNumericSegments(t *testing.T) {
	c := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 2, CacheSize: 2})
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.9", 1},
		{"2", "2.0.0", 0},
		{"1.2", "1.2.1", -1},
	}
	for _, tc := range cases {
		got, err := c.CompareVersions(tc.a, tc.b)
		if err != nil {
			t.Fatalf("compare %q %q: %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Fatalf("compare %q %q: want %d, got %d", tc.a, tc.b, tc.want, got)
		}
	}
}

func TestCompareVersionsRejectsNonNumeric(t *testing.T) {
	c := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 2, CacheSize: 2})
	if _, err := c.CompareVersions("1.x.0", "1.0.0"); !errors.Is(err, impl.ErrInvalidVersion) {
		t.Fatalf("non numeric segment: want ErrInvalidVersion, got %v", err)
	}
	if _, err := c.CompareVersions("", "1.0.0"); !errors.Is(err, impl.ErrInvalidVersion) {
		t.Fatalf("empty version: want ErrInvalidVersion, got %v", err)
	}
}

func TestRegisterRejectsInvalidFix(t *testing.T) {
	c := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 2, CacheSize: 2})
	reversed := impl.Fix{
		ID: "CVE-2025-2000", Component: "libfoo",
		IntroducedIn: "2.0.0", FixedIn: "1.0.0",
		Function: "f", Digest: strings.Repeat("a", 64),
	}
	if err := c.Register(reversed); !errors.Is(err, impl.ErrInvalidFix) {
		t.Fatalf("reversed version range: want ErrInvalidFix, got %v", err)
	}
	badDigest := impl.Fix{
		ID: "CVE-2025-2001", Component: "libfoo",
		IntroducedIn: "1.0.0", FixedIn: "2.0.0",
		Function: "f", Digest: "not-hex",
	}
	if err := c.Register(badDigest); !errors.Is(err, impl.ErrInvalidFix) {
		t.Fatalf("bad digest: want ErrInvalidFix, got %v", err)
	}
}

func TestRegisterRejectsDuplicateFix(t *testing.T) {
	c, fix := newFixChecker(t)
	if err := c.Register(fix); !errors.Is(err, impl.ErrDuplicateFix) {
		t.Fatalf("duplicate fix id: want ErrDuplicateFix, got %v", err)
	}
	if got := c.Stats().Registered; got != 1 {
		t.Fatalf("registered counter: want 1, got %d", got)
	}
}

func TestFingerprintIgnoresSymbolNamesAndCase(t *testing.T) {
	c := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 2, CacheSize: 2})
	a := c.Fingerprint(fixedInstructions)
	b := c.Fingerprint(renamedInstructions)
	if len(a) != 64 {
		t.Fatalf("fingerprint length: want 64, got %d", len(a))
	}
	if a != b {
		t.Fatalf("symbol rename, case and spacing must not change the fingerprint: %q vs %q", a, b)
	}
}

func TestFingerprintChangesWithOpcode(t *testing.T) {
	c := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 2, CacheSize: 2})
	if c.Fingerprint(fixedInstructions) == c.Fingerprint(changedInstructions) {
		t.Fatalf("a changed operand must change the fingerprint")
	}
}

func TestCheckVersionAboveFixedIsFixed(t *testing.T) {
	c, _ := newFixChecker(t)
	p, err := c.Check(refWith("art-1", "2.6.0", fixedInstructions))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if p.Status != "fixed" || p.Confidence != 100 {
		t.Fatalf("verdict: want fixed/100, got %s/%d", p.Status, p.Confidence)
	}
	if !hasEvidence(p, "version") || !hasEvidence(p, "fingerprint") {
		t.Fatalf("verdict evidence: want version and fingerprint, got %v", p.Evidence)
	}
}

func TestCheckVersionInsideAffectedRangeIsVulnerable(t *testing.T) {
	c, _ := newFixChecker(t)
	p, err := c.Check(refWith("art-2", "2.0.0", changedInstructions))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if p.Status != "vulnerable" {
		t.Fatalf("verdict: want vulnerable, got %s", p.Status)
	}
}

func TestCheckBackportFingerprintWinsOverVersion(t *testing.T) {
	c, _ := newFixChecker(t)
	p, err := c.Check(refWith("art-3", "1.4.0", fixedInstructions))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if p.Status != "fixed" || p.Confidence != 70 {
		t.Fatalf("backport verdict: want fixed/70, got %s/%d", p.Status, p.Confidence)
	}
	if len(p.Evidence) != 1 || p.Evidence[0] != "fingerprint" {
		t.Fatalf("backport evidence: want [fingerprint], got %v", p.Evidence)
	}
}

func TestCheckConflictReportsUncertain(t *testing.T) {
	c, _ := newFixChecker(t)
	p, err := c.Check(refWith("art-4", "2.6.0", changedInstructions))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if p.Status != "uncertain" {
		t.Fatalf("conflicting evidence: want uncertain, got %s", p.Status)
	}
	if got := c.Stats().Uncertain; got != 1 {
		t.Fatalf("uncertain counter: want 1, got %d", got)
	}
}

func TestCheckUnknownComponentFails(t *testing.T) {
	c, _ := newFixChecker(t)
	ref := impl.ArtifactRef{ID: "art-5", Component: "libbar", Version: "2.6.0"}
	if _, err := c.Check(ref); !errors.Is(err, impl.ErrUnknownFix) {
		t.Fatalf("unknown component: want ErrUnknownFix, got %v", err)
	}
}

func TestExplainRecordsEvidenceSteps(t *testing.T) {
	c, fix := newFixChecker(t)
	ref := refWith("art-6", "2.6.0", fixedInstructions)
	if _, err := c.Check(ref); err != nil {
		t.Fatalf("check: %v", err)
	}
	exp, err := c.Explain("art-6")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if exp.FixID != fix.ID {
		t.Fatalf("explain fix id: want %q, got %q", fix.ID, exp.FixID)
	}
	if len(exp.Steps) < 2 {
		t.Fatalf("explain steps: want at least 2, got %v", exp.Steps)
	}
	if exp.Steps[0] != "version" || exp.Steps[1] != "fingerprint" {
		t.Fatalf("explain step order: want version then fingerprint, got %v", exp.Steps)
	}
	if _, err := c.Explain("art-ghost"); !errors.Is(err, impl.ErrUnknownArtifact) {
		t.Fatalf("unknown artifact: want ErrUnknownArtifact, got %v", err)
	}
}

func TestBatchDedupesAndSortsKeys(t *testing.T) {
	c, _ := newFixChecker(t)
	fixed := refWith("art-7", "2.6.0", fixedInstructions)
	vulnerable := refWith("art-8", "2.0.0", changedInstructions)
	got, err := c.Batch([]impl.ArtifactRef{fixed, fixed, vulnerable})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("batch size: want 2, got %d", len(got))
	}
	if got["art-7"].Status != "fixed" {
		t.Fatalf("art-7: want fixed, got %s", got["art-7"].Status)
	}
	if got["art-8"].Status != "vulnerable" {
		t.Fatalf("art-8: want vulnerable, got %s", got["art-8"].Status)
	}
	if hits := c.Stats().CacheHits; hits != 1 {
		t.Fatalf("cache hits: want 1 (duplicate artifact id), got %d", hits)
	}
}

func TestBatchRejectsOverLimit(t *testing.T) {
	opts := impl.CheckerOptions{MaxBatch: 1, CacheSize: 4}
	c := impl.NewPatchPresenceChecker(opts)
	fix := impl.Fix{
		ID: "CVE-2025-1010", Component: "libfoo",
		IntroducedIn: "1.0.0", FixedIn: "2.5.0",
		Function: "parse_hdr", Digest: c.Fingerprint(fixedInstructions),
	}
	if err := c.Register(fix); err != nil {
		t.Fatalf("register: %v", err)
	}
	refs := []impl.ArtifactRef{
		refWith("art-9", "2.6.0", fixedInstructions),
		refWith("art-10", "2.6.0", fixedInstructions),
	}
	if _, err := c.Batch(refs); !errors.Is(err, impl.ErrBatchTooLarge) {
		t.Fatalf("batch over limit: want ErrBatchTooLarge, got %v", err)
	}
}

func TestSnapshotRestoreKeepsCache(t *testing.T) {
	c, _ := newFixChecker(t)
	ref := refWith("art-11", "2.6.0", fixedInstructions)
	first, err := c.Check(ref)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewPatchPresenceChecker(impl.CheckerOptions{MaxBatch: 4, CacheSize: 8})
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	cached, ok := restored.Cached("art-11")
	if !ok || cached.Status != first.Status {
		t.Fatalf("cached verdict after restore: ok=%v status=%q", ok, cached.Status)
	}
	again, err := restored.Check(ref)
	if err != nil {
		t.Fatalf("check after restore: %v", err)
	}
	if again.Status != "fixed" {
		t.Fatalf("check after restore: want fixed, got %s", again.Status)
	}
	if got := restored.Stats().CacheHits; got != 1 {
		t.Fatalf("cache hits after restore check: want 1, got %d", got)
	}
	if err := restored.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot: want ErrBadSnapshot, got %v", err)
	}
}
