package task

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-13"
)

const (
	leakTokenA = "AKIAZ7Q2XWPLMN3RT4VY"
	leakTokenB = "AKIAB8R3YTNKM5QWXZ2CD"
	leakTokenC = "AKIAC9S4ZUPLN6RXVY3DE"
)

func leakOptions() impl.ScannerOptions {
	return impl.ScannerOptions{
		MaxFileBytes:  1 << 20,
		MaxCandidates: 64,
		MinEntropy:    3.0,
		Prefixes:      []string{"AKIA"},
	}
}

func TestEntropyOfUniformAndPeriodicStrings(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	if got := s.Entropy("aaaaaaaa"); got != 0 {
		t.Fatalf("entropy of a uniform string: want 0, got %v", got)
	}
	if got := s.Entropy("abab"); got != 1.0 {
		t.Fatalf("entropy of two equiprobable symbols: want 1, got %v", got)
	}
}

func TestEntropyOfFourDistinctSymbols(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	if got := s.Entropy("abcd"); got != 2.0 {
		t.Fatalf("entropy of four equiprobable symbols: want 2, got %v", got)
	}
}

func TestScanFileDetectsPrefixedToken(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	data := []byte("token = \"" + leakTokenA + "\"\n")
	findings, err := s.ScanFile("config.yaml", data)
	if err != nil {
		t.Fatalf("scan file: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings: want 1, got %d", len(findings))
	}
	got := findings[0]
	if got.RuleID != "prefix:AKIA" {
		t.Fatalf("rule id: want prefix:AKIA, got %q", got.RuleID)
	}
	if got.Value != leakTokenA {
		t.Fatalf("value: want %q, got %q", leakTokenA, got.Value)
	}
	if len(got.Occurrences) != 1 {
		t.Fatalf("occurrences: want 1, got %d", len(got.Occurrences))
	}
	wantColumn := strings.Index(string(data), leakTokenA) + 1
	if got.Occurrences[0].Line != 1 || got.Occurrences[0].Column != wantColumn {
		t.Fatalf("location: want 1:%d, got %d:%d", wantColumn, got.Occurrences[0].Line, got.Occurrences[0].Column)
	}
	if got.Entropy < 3.0 {
		t.Fatalf("finding entropy: want at least 3, got %v", got.Entropy)
	}
}

func TestScanFileDedupesSameValueWithPositions(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	lines := []string{
		"key1 = \"" + leakTokenA + "\"",
		"unrelated = \"short\"",
		"    key2 = \"" + leakTokenA + "\"",
	}
	data := []byte(strings.Join(lines, "\n"))
	findings, err := s.ScanFile("deploy.yaml", data)
	if err != nil {
		t.Fatalf("scan file: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("same value twice in one file must yield one finding, got %d", len(findings))
	}
	occ := findings[0].Occurrences
	if len(occ) != 2 {
		t.Fatalf("occurrences: want 2, got %d", len(occ))
	}
	want1 := strings.Index(lines[0], leakTokenA) + 1
	want2 := strings.Index(lines[2], leakTokenA) + 1
	if occ[0].Line != 1 || occ[0].Column != want1 {
		t.Fatalf("first occurrence: want 1:%d, got %d:%d", want1, occ[0].Line, occ[0].Column)
	}
	if occ[1].Line != 3 || occ[1].Column != want2 {
		t.Fatalf("second occurrence: want 3:%d, got %d:%d", want2, occ[1].Line, occ[1].Column)
	}
	if got := s.Stats().Candidates; got != 2 {
		t.Fatalf("candidate counter: want 2, got %d", got)
	}
}

func TestScanFileRejectsOversizedFile(t *testing.T) {
	opts := leakOptions()
	opts.MaxFileBytes = 8
	s := impl.NewSecretLeakScanner(opts)
	data := []byte("token = \"" + leakTokenA + "\"\n")
	if _, err := s.ScanFile("big.yaml", data); !errors.Is(err, impl.ErrFileTooLarge) {
		t.Fatalf("oversized file: want ErrFileTooLarge, got %v", err)
	}
	if len(s.Findings()) != 0 {
		t.Fatalf("rejected file must not leave findings behind")
	}
}

func TestScanFileRejectsCandidateOverflow(t *testing.T) {
	opts := leakOptions()
	opts.MaxCandidates = 2
	s := impl.NewSecretLeakScanner(opts)
	data := []byte("a = \"" + leakTokenA + "\"\nb = \"" + leakTokenB + "\"\nc = \"" + leakTokenC + "\"\n")
	if _, err := s.ScanFile("many.yaml", data); !errors.Is(err, impl.ErrCandidateLimit) {
		t.Fatalf("candidate overflow: want ErrCandidateLimit, got %v", err)
	}
	if len(s.Findings()) != 0 {
		t.Fatalf("an over-limit scan must be discarded whole, got %d findings", len(s.Findings()))
	}
}

func TestScanFileSkipsLowEntropyToken(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	data := []byte("key = \"" + strings.Repeat("a", 20) + "\"\n")
	findings, err := s.ScanFile("weak.yaml", data)
	if err != nil {
		t.Fatalf("scan file: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("low entropy token must be skipped, got %d findings", len(findings))
	}
}

func TestScanTreeReturnsFindingsInPathOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("k = \""+leakTokenB+"\"\n"), 0o600); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("k = \""+leakTokenA+"\"\n"), 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	s := impl.NewSecretLeakScanner(leakOptions())
	findings, err := s.ScanTree(root)
	if err != nil {
		t.Fatalf("scan tree: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings: want 2, got %d", len(findings))
	}
	if !strings.HasSuffix(findings[0].Path, "a.txt") || !strings.HasSuffix(findings[1].Path, "b.txt") {
		t.Fatalf("tree scan order: want a.txt before b.txt, got %q then %q", findings[0].Path, findings[1].Path)
	}
	if findings[0].Value != leakTokenA || findings[1].Value != leakTokenB {
		t.Fatalf("tree scan values: got %q then %q", findings[0].Value, findings[1].Value)
	}
	if got := s.Stats().Files; got != 2 {
		t.Fatalf("file counter: want 2, got %d", got)
	}
}

func TestAllowPathSuffixSuppressesFindings(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	if err := s.Allow(impl.Context{PathSuffix: "_test.go", Reason: "fixture"}); err != nil {
		t.Fatalf("allow context: %v", err)
	}
	data := []byte("token = \"" + leakTokenA + "\"\n")
	findings, err := s.ScanFile("auth_test.go", data)
	if err != nil {
		t.Fatalf("scan allowlisted file: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("allowlisted path must not report findings, got %d", len(findings))
	}
	if got := s.Stats().Suppressed; got != 1 {
		t.Fatalf("suppressed counter: want 1, got %d", got)
	}
	findings, err = s.ScanFile("auth.go", data)
	if err != nil {
		t.Fatalf("scan normal file: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("normal path must still report the secret, got %d findings", len(findings))
	}
}

func TestSetBaselineHidesKnownFindings(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	data := []byte("a = \"" + leakTokenA + "\"\nb = \"" + leakTokenB + "\"\n")
	first, err := s.ScanFile("creds.yaml", data)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first scan findings: want 2, got %d", len(first))
	}
	var known, fresh impl.Finding
	for _, f := range first {
		if f.Value == leakTokenA {
			known = f
		} else {
			fresh = f
		}
	}
	knownFP := s.Fingerprint(known)
	freshFP := s.Fingerprint(fresh)
	if err := s.SetBaseline([]string{knownFP}); err != nil {
		t.Fatalf("set baseline: %v", err)
	}
	second, err := s.ScanFile("creds.yaml", data)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(second) != 1 || second[0].Value != leakTokenB {
		t.Fatalf("baseline finding must be hidden: got %+v", second)
	}
	if got := s.Stats().Suppressed; got != 1 {
		t.Fatalf("suppressed counter: want 1, got %d", got)
	}
	diff, err := s.BaselineDiff()
	if err != nil {
		t.Fatalf("baseline diff: %v", err)
	}
	if len(diff.Added) != 1 || diff.Added[0] != freshFP {
		t.Fatalf("added: want [%s], got %v", freshFP, diff.Added)
	}
	if len(diff.Removed) != 0 {
		t.Fatalf("removed: want none, got %v", diff.Removed)
	}
}

func TestBaselineDiffReportsRemoved(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	two := []byte("a = \"" + leakTokenA + "\"\nb = \"" + leakTokenB + "\"\n")
	first, err := s.ScanFile("creds.yaml", two)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	var known, fresh impl.Finding
	for _, f := range first {
		if f.Value == leakTokenA {
			known = f
		} else {
			fresh = f
		}
	}
	knownFP := s.Fingerprint(known)
	freshFP := s.Fingerprint(fresh)
	if err := s.SetBaseline([]string{knownFP}); err != nil {
		t.Fatalf("set baseline: %v", err)
	}
	onlyKnown := []byte("a = \"" + leakTokenA + "\"\n")
	findings, err := s.ScanFile("creds.yaml", onlyKnown)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("only the baseline token remains: want 0 findings, got %d", len(findings))
	}
	diff, err := s.BaselineDiff()
	if err != nil {
		t.Fatalf("baseline diff: %v", err)
	}
	if len(diff.Added) != 0 {
		t.Fatalf("added: want none, got %v", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != freshFP {
		t.Fatalf("removed: want [%s], got %v", freshFP, diff.Removed)
	}
}

func TestSetBaselineRejectsInvalidFingerprint(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	if err := s.SetBaseline([]string{"not-a-fingerprint"}); !errors.Is(err, impl.ErrInvalidBaseline) {
		t.Fatalf("bad fingerprint: want ErrInvalidBaseline, got %v", err)
	}
}

func TestFingerprintIsStableHexDigest(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	f := impl.Finding{RuleID: "prefix:AKIA", Value: leakTokenA}
	fp := s.Fingerprint(f)
	if len(fp) != 64 {
		t.Fatalf("fingerprint length: want 64, got %d (%q)", len(fp), fp)
	}
	if _, err := hex.DecodeString(fp); err != nil {
		t.Fatalf("fingerprint must be hexadecimal: %v", err)
	}
	if again := s.Fingerprint(f); again != fp {
		t.Fatalf("fingerprint must be stable across calls: %q vs %q", fp, again)
	}
	other := s.Fingerprint(impl.Finding{RuleID: "prefix:AKIA", Value: leakTokenB})
	if other == fp {
		t.Fatalf("different values must have different fingerprints")
	}
}

func TestScanReplacesPreviousFindings(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	if _, err := s.ScanFile("a.yaml", []byte("k = \""+leakTokenA+"\"\n")); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	findings, err := s.ScanFile("b.yaml", []byte("k = \""+leakTokenB+"\"\n"))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(findings) != 1 || findings[0].Value != leakTokenB {
		t.Fatalf("findings must be replaced by the latest scan, got %+v", findings)
	}
	if got := s.Stats().Files; got != 2 {
		t.Fatalf("file counter: want 2, got %d", got)
	}
}

func TestSnapshotRestoreKeepsFindingsAndBaseline(t *testing.T) {
	s := impl.NewSecretLeakScanner(leakOptions())
	data := []byte("a = \"" + leakTokenA + "\"\nb = \"" + leakTokenB + "\"\n")
	first, err := s.ScanFile("creds.yaml", data)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var known impl.Finding
	for _, f := range first {
		if f.Value == leakTokenA {
			known = f
		}
	}
	if err := s.SetBaseline([]string{s.Fingerprint(known)}); err != nil {
		t.Fatalf("set baseline: %v", err)
	}
	findings, err := s.ScanFile("creds.yaml", data)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	data2, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored := impl.NewSecretLeakScanner(leakOptions())
	if err := restored.Restore(data2); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !reflect.DeepEqual(findings, restored.Findings()) {
		t.Fatalf("findings after restore: want %+v, got %+v", findings, restored.Findings())
	}
	if want, got := s.Stats(), restored.Stats(); want != got {
		t.Fatalf("stats after restore: want %+v, got %+v", want, got)
	}
	if err := restored.Restore([]byte("not-a-snapshot")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("bad snapshot: want ErrBadSnapshot, got %v", err)
	}
}
