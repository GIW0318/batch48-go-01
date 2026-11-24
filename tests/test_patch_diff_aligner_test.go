package task

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-04"
)

func aligner() *impl.PatchDiffAligner {
	return impl.NewPatchDiffAligner(impl.Options{MaxShift: 64, MinContext: 1})
}

func baseLines() []impl.Line {
	return []impl.Line{
		{No: 1, Text: "func alpha() {"},
		{No: 2, Text: "    x := 1"},
		{No: 3, Text: "    if x > 0 {"},
		{No: 4, Text: "        use(x)"},
		{No: 5, Text: "    }"},
		{No: 6, Text: "}"},
		{No: 7, Text: "func beta() {"},
		{No: 8, Text: "    y := 2"},
		{No: 9, Text: "    return y"},
		{No: 10, Text: "}"},
	}
}

func targetLines() []impl.Line {
	return []impl.Line{
		{No: 1, Text: "// upstream header"},
		{No: 2, Text: "// second header"},
		{No: 3, Text: "// third header"},
		{No: 4, Text: "func alpha() {"},
		{No: 5, Text: "    x := 1"},
		{No: 6, Text: "    if x > 0 {"},
		{No: 7, Text: "        use(x)"},
		{No: 8, Text: "    }"},
		{No: 9, Text: "}"},
		{No: 10, Text: "func beta() {"},
		{No: 11, Text: "    y := 2"},
		{No: 12, Text: "    return y"},
		{No: 13, Text: "}"},
	}
}

func hunkWithContext(index int, oldStart int, context ...string) impl.Hunk {
	lines := make([]impl.Line, 0, len(context))
	for i, text := range context {
		lines = append(lines, impl.Line{No: oldStart + i, Text: text})
	}
	return impl.Hunk{
		Index:    index,
		OldStart: oldStart,
		OldCount: len(lines),
		NewStart: oldStart,
		NewCount: len(lines),
		Context:  lines,
	}
}

func loadHunks(t *testing.T, a *impl.PatchDiffAligner, hunks ...impl.Hunk) {
	t.Helper()
	var b strings.Builder
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		for _, line := range h.Context {
			b.WriteString(" " + line.Text + "\n")
		}
		for _, line := range h.Removed {
			b.WriteString("-" + line.Text + "\n")
		}
		for _, line := range h.Added {
			b.WriteString("+" + line.Text + "\n")
		}
	}
	if _, err := a.Parse([]byte(b.String())); err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
}

func TestParseRejectsBadHeader(t *testing.T) {
	a := aligner()
	_, err := a.Parse([]byte("@@ -1 +1 @@\n context\n"))
	if !errors.Is(err, impl.ErrBadHeader) {
		t.Fatalf("bad header error = %v, want ErrBadHeader", err)
	}
}

func TestParseRejectsCountMismatch(t *testing.T) {
	a := aligner()
	src := "@@ -10,2 +10,2 @@\n context line\n-old line\n"
	_, err := a.Parse([]byte(src))
	if !errors.Is(err, impl.ErrCountMismatch) {
		t.Fatalf("count mismatch error = %v, want ErrCountMismatch", err)
	}
}

func TestParseRejectsHunkWithoutContext(t *testing.T) {
	a := aligner()
	_, err := a.Parse([]byte("@@ -1,0 +1,1 @@\n+only line\n"))
	if !errors.Is(err, impl.ErrEmptyContext) {
		t.Fatalf("empty context error = %v, want ErrEmptyContext", err)
	}
}

func TestParseReadsHunkFields(t *testing.T) {
	a := aligner()
	src := "@@ -12,2 +12,2 @@\n ctx one\n-old line\n+new line\n"
	hunks, err := a.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if h.Index != 0 || h.OldStart != 12 || h.OldCount != 2 || h.NewStart != 12 || h.NewCount != 2 {
		t.Fatalf("header fields = %+v", h)
	}
	if len(h.Context) != 1 || h.Context[0].Text != "ctx one" || h.Context[0].No != 12 {
		t.Fatalf("context = %+v", h.Context)
	}
	if len(h.Removed) != 1 || h.Removed[0].Text != "old line" || h.Removed[0].No != 13 {
		t.Fatalf("removed = %+v", h.Removed)
	}
	if len(h.Added) != 1 || h.Added[0].Text != "new line" || h.Added[0].No != 12 {
		t.Fatalf("added = %+v", h.Added)
	}
}

func TestAlignFollowsDriftedContext(t *testing.T) {
	a := aligner()
	loadHunks(t, a, hunkWithContext(0, 20, "    x := 1", "    if x > 0 {"))
	maps, err := a.Align(baseLines(), targetLines())
	if err != nil {
		t.Fatalf("Align: unexpected error: %v", err)
	}
	want := []impl.Mapping{{Hunk: 0, Function: "alpha", BaseStart: 2, BaseEnd: 3, TargetStart: 5, Shift: 3}}
	if !reflect.DeepEqual(maps, want) {
		t.Fatalf("mappings = %+v, want %+v", maps, want)
	}
}

func TestAlignRejectsAmbiguousContext(t *testing.T) {
	a := aligner()
	base := []impl.Line{
		{No: 1, Text: "func same() {"},
		{No: 2, Text: "// marker"},
		{No: 3, Text: "return nil"},
		{No: 4, Text: "}"},
		{No: 5, Text: "func other() {"},
		{No: 6, Text: "// marker"},
		{No: 7, Text: "return nil"},
		{No: 8, Text: "}"},
	}
	target := append([]impl.Line(nil), base...)
	loadHunks(t, a, hunkWithContext(0, 2, "// marker", "return nil"))
	if _, err := a.Align(base, target); !errors.Is(err, impl.ErrAmbiguousHunk) {
		t.Fatalf("ambiguous context error = %v, want ErrAmbiguousHunk", err)
	}
}

func TestAlignRejectsMissingContext(t *testing.T) {
	a := aligner()
	loadHunks(t, a, hunkWithContext(0, 2, "    missing line"))
	if _, err := a.Align(baseLines(), targetLines()); !errors.Is(err, impl.ErrNoMatch) {
		t.Fatalf("missing context error = %v, want ErrNoMatch", err)
	}
}

func TestAlignAttributesHunkToFunction(t *testing.T) {
	a := aligner()
	loadHunks(t, a, hunkWithContext(0, 5, "    y := 2", "    return y"))
	maps, err := a.Align(baseLines(), targetLines())
	if err != nil {
		t.Fatalf("Align: unexpected error: %v", err)
	}
	if len(maps) != 1 || maps[0].Function != "beta" {
		t.Fatalf("mappings = %+v, want the hunk attributed to beta", maps)
	}
	if maps[0].BaseStart != 8 || maps[0].TargetStart != 11 || maps[0].Shift != 3 {
		t.Fatalf("mapping = %+v, want base 8, target 11, shift 3", maps[0])
	}
}

func TestAlignRejectsHunkSpanningTwoFunctions(t *testing.T) {
	a := aligner()
	loadHunks(t, a, hunkWithContext(0, 6, "}", "func beta() {"))
	if _, err := a.Align(baseLines(), targetLines()); !errors.Is(err, impl.ErrSpanningFunction) {
		t.Fatalf("spanning error = %v, want ErrSpanningFunction", err)
	}
}

func TestAlignOrderIsStable(t *testing.T) {
	a := aligner()
	loadHunks(t, a,
		hunkWithContext(0, 20, "    y := 2", "    return y"),
		hunkWithContext(1, 30, "    x := 1", "    if x > 0 {"),
	)
	first, err := a.Align(baseLines(), targetLines())
	if err != nil {
		t.Fatalf("Align: unexpected error: %v", err)
	}
	if len(first) != 2 || first[0].Hunk != 0 || first[0].Function != "beta" || first[1].Hunk != 1 || first[1].Function != "alpha" {
		t.Fatalf("mappings = %+v, want hunk order 0 then 1", first)
	}
	second, err := a.Align(baseLines(), targetLines())
	if err != nil {
		t.Fatalf("second Align: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second Align = %+v, want identical to %+v", second, first)
	}
}

func TestFingerprintIgnoresSymbolRenames(t *testing.T) {
	a := aligner()
	one := impl.Hunk{Index: 0, Added: []impl.Line{{Text: "if count > 64 {"}}}
	two := impl.Hunk{Index: 1, Added: []impl.Line{{Text: "if limit > 64 {"}}}
	first, err := a.Fingerprint(one)
	if err != nil {
		t.Fatalf("Fingerprint(one): %v", err)
	}
	second, err := a.Fingerprint(two)
	if err != nil {
		t.Fatalf("Fingerprint(two): %v", err)
	}
	if len(first.Checks) != 1 {
		t.Fatalf("Checks = %v, want exactly the renamed guard", first.Checks)
	}
	if !slices.Equal(first.Checks, second.Checks) || first.Digest != second.Digest {
		t.Fatalf("rename changed the fingerprint: %+v vs %+v", first, second)
	}
}

func TestFingerprintIgnoresNonConditionalLines(t *testing.T) {
	a := aligner()
	withLog := impl.Hunk{Added: []impl.Line{
		{Text: "if n > 0 {"},
		{Text: `log.Printf("start")`},
	}}
	guardOnly := impl.Hunk{Added: []impl.Line{{Text: "if n > 0 {"}}}
	first, err := a.Fingerprint(withLog)
	if err != nil {
		t.Fatalf("Fingerprint(withLog): %v", err)
	}
	second, err := a.Fingerprint(guardOnly)
	if err != nil {
		t.Fatalf("Fingerprint(guardOnly): %v", err)
	}
	if first.Digest != second.Digest || !slices.Equal(first.Checks, second.Checks) {
		t.Fatalf("non conditional line changed the fingerprint: %+v vs %+v", first, second)
	}
}

func TestFingerprintIsStableAcrossRuns(t *testing.T) {
	a := aligner()
	h := impl.Hunk{Added: []impl.Line{{Text: "    if off >= len(buf) {"}}}
	first, err := a.Fingerprint(h)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	second, err := a.Fingerprint(h)
	if err != nil {
		t.Fatalf("second Fingerprint: %v", err)
	}
	if first.Digest != second.Digest || !slices.Equal(first.Checks, second.Checks) {
		t.Fatalf("fingerprint is not stable: %+v vs %+v", first, second)
	}
	combined, err := a.Digest([]impl.Hunk{h})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if combined.Digest != first.Digest {
		t.Fatalf("Digest of one hunk = %x, want %x", combined.Digest, first.Digest)
	}
}

func TestClassifyGuardChange(t *testing.T) {
	a := aligner()
	h := impl.Hunk{Added: []impl.Line{{Text: "if off >= len(buf) {"}}}
	kind, err := a.Classify(h)
	if err != nil {
		t.Fatalf("Classify: unexpected error: %v", err)
	}
	if kind != impl.KindGuard {
		t.Fatalf("kind = %v, want KindGuard", kind)
	}
}

func TestClassifyCommentOnlyRefactor(t *testing.T) {
	a := aligner()
	h := impl.Hunk{Added: []impl.Line{{Text: "// explain why the bound holds"}}}
	kind, err := a.Classify(h)
	if err != nil {
		t.Fatalf("Classify: unexpected error: %v", err)
	}
	if kind != impl.KindRefactor {
		t.Fatalf("kind = %v, want KindRefactor", kind)
	}
	if _, err := a.Classify(impl.Hunk{}); !errors.Is(err, impl.ErrNoChange) {
		t.Fatalf("empty hunk error = %v, want ErrNoChange", err)
	}
}
