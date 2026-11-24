package task

import (
	"errors"
	"slices"
	"strings"
	"testing"

	impl "gsb/batch48-go-safety-c-02"
)

func scanner() *impl.StreamSignatureScanner {
	return impl.NewStreamSignatureScanner(impl.Options{})
}

func install(t *testing.T, s *impl.StreamSignatureScanner, patterns []impl.Pattern) *impl.AtomIndex {
	t.Helper()
	idx, err := s.BuildAtoms(patterns)
	if err != nil {
		t.Fatalf("BuildAtoms: unexpected error: %v", err)
	}
	if idx == nil {
		t.Fatal("BuildAtoms returned a nil index")
	}
	return idx
}

func TestBuildAtomsRejectsShortPattern(t *testing.T) {
	s := scanner()
	_, err := s.BuildAtoms([]impl.Pattern{{ID: "short", Kind: impl.Exact, Prefix: []byte("ab")}})
	if !errors.Is(err, impl.ErrAtomTooShort) {
		t.Fatalf("short pattern error = %v, want ErrAtomTooShort", err)
	}
	if !strings.Contains(err.Error(), "short") {
		t.Fatalf("error %q must name the pattern", err.Error())
	}
}

func TestBuildAtomsRejectsDuplicateIDs(t *testing.T) {
	s := scanner()
	_, err := s.BuildAtoms([]impl.Pattern{
		{ID: "dup", Kind: impl.Exact, Prefix: []byte("abc")},
		{ID: "dup", Kind: impl.Exact, Prefix: []byte("abd")},
	})
	if !errors.Is(err, impl.ErrPatternInvalid) {
		t.Fatalf("duplicate id error = %v, want ErrPatternInvalid", err)
	}
}

func TestBuildAtomsRejectsAtomLimit(t *testing.T) {
	s := impl.NewStreamSignatureScanner(impl.Options{MaxAtoms: 2})
	_, err := s.BuildAtoms([]impl.Pattern{
		{ID: "p1", Kind: impl.Exact, Prefix: []byte("aaa")},
		{ID: "p2", Kind: impl.Exact, Prefix: []byte("bbb")},
		{ID: "p3", Kind: impl.Exact, Prefix: []byte("ccc")},
	})
	if !errors.Is(err, impl.ErrTooManyAtoms) {
		t.Fatalf("atom limit error = %v, want ErrTooManyAtoms", err)
	}
}

func TestBuildAtomsIndexShape(t *testing.T) {
	s := scanner()
	patterns := []impl.Pattern{
		{ID: "beta", Kind: impl.Exact, Prefix: []byte("bcd")},
		{ID: "alpha", Kind: impl.Exact, Prefix: []byte("abc")},
		{ID: "gamma", Kind: impl.Exact, Prefix: []byte("cde")},
	}
	idx := install(t, s, patterns)
	if got := idx.Size(); got != 3 {
		t.Fatalf("Size = %d, want 3 distinct atoms", got)
	}
	if got, want := idx.Patterns(), []string{"alpha", "beta", "gamma"}; !slices.Equal(got, want) {
		t.Fatalf("Patterns = %v, want %v", got, want)
	}
	if got := idx.Version(); got != 1 {
		t.Fatalf("Version = %d, want 1 for the first build", got)
	}
	second := install(t, s, patterns)
	if got := second.Version(); got != 2 {
		t.Fatalf("Version = %d, want 2 for the second build", got)
	}
}

func TestSetWindowRejectsOutOfRange(t *testing.T) {
	s := scanner()
	if err := s.SetWindow(-1); !errors.Is(err, impl.ErrBadWindow) {
		t.Fatalf("SetWindow(-1) = %v, want ErrBadWindow", err)
	}
	if err := s.SetWindow(1 << 21); !errors.Is(err, impl.ErrBadWindow) {
		t.Fatalf("SetWindow(1<<21) = %v, want ErrBadWindow", err)
	}
	if err := s.SetWindow(3); err != nil {
		t.Fatalf("SetWindow(3): unexpected error: %v", err)
	}
	if got := s.Window(); got != 3 {
		t.Fatalf("Window = %d, want 3", got)
	}
}

func TestFeedWithoutIndex(t *testing.T) {
	s := scanner()
	if _, err := s.Feed([]byte("abcdef"), 0); !errors.Is(err, impl.ErrNoIndex) {
		t.Fatalf("Feed without index = %v, want ErrNoIndex", err)
	}
}

func TestFeedRejectsNonContiguousOffset(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "abc", Kind: impl.Exact, Prefix: []byte("abc")}})
	if _, err := s.Feed([]byte("abcdef"), 0); err != nil {
		t.Fatalf("first Feed: unexpected error: %v", err)
	}
	if _, err := s.Feed([]byte("ghi"), 100); !errors.Is(err, impl.ErrOffsetGap) {
		t.Fatalf("skipped offset error = %v, want ErrOffsetGap", err)
	}
}

func TestFeedRejectsOversizedChunk(t *testing.T) {
	s := impl.NewStreamSignatureScanner(impl.Options{MaxChunk: 4})
	install(t, s, []impl.Pattern{{ID: "abc", Kind: impl.Exact, Prefix: []byte("abc")}})
	if _, err := s.Feed([]byte("abcde"), 0); !errors.Is(err, impl.ErrChunkTooLarge) {
		t.Fatalf("oversized chunk error = %v, want ErrChunkTooLarge", err)
	}
}

func TestExactHitAcrossChunkBoundary(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "needle", Kind: impl.Exact, Prefix: []byte("needle")}})
	if hits, err := s.Feed([]byte("xxnee"), 0); err != nil || len(hits) != 0 {
		t.Fatalf("first chunk = %v, %v; want no hits", hits, err)
	}
	hits, err := s.Feed([]byte("dlezz"), 5)
	if err != nil {
		t.Fatalf("second Feed: unexpected error: %v", err)
	}
	want := []impl.Hit{{Pattern: "needle", Start: 2, End: 8}}
	if !slices.Equal(hits, want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
	if hits, err := s.Feed([]byte("qqqq"), 10); err != nil || len(hits) != 0 {
		t.Fatalf("trailing chunk = %v, %v; want no duplicate hit", hits, err)
	}
}

func TestGappedHitWithinChunk(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "gap4", Kind: impl.Gapped, Prefix: []byte("abc"), Suffix: []byte("xyz"), Gap: 4}})
	hits, err := s.Feed([]byte("abc12xyz!"), 0)
	if err != nil {
		t.Fatalf("Feed: unexpected error: %v", err)
	}
	want := []impl.Hit{{Pattern: "gap4", Start: 0, End: 8}}
	if !slices.Equal(hits, want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
}

func TestGappedGapLimit(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "gap2", Kind: impl.Gapped, Prefix: []byte("abc"), Suffix: []byte("xyz"), Gap: 2}})
	hits, err := s.Feed([]byte("abc12xyzabc999xyz"), 0)
	if err != nil {
		t.Fatalf("Feed: unexpected error: %v", err)
	}
	want := []impl.Hit{{Pattern: "gap2", Start: 0, End: 8}}
	if !slices.Equal(hits, want) {
		t.Fatalf("hits = %+v, want only the in-limit match %+v", hits, want)
	}
}

func TestWindowHoldingReportsOnce(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "abc", Kind: impl.Exact, Prefix: []byte("abc")}})
	if err := s.SetWindow(2); err != nil {
		t.Fatalf("SetWindow: %v", err)
	}
	first, err := s.Feed([]byte("abcXabc"), 0)
	if err != nil {
		t.Fatalf("first Feed: %v", err)
	}
	if len(first) != 1 || first[0].Start != 0 {
		t.Fatalf("first Feed = %+v, want only the fully settled hit at 0", first)
	}
	second, err := s.Feed([]byte("YY"), 7)
	if err != nil {
		t.Fatalf("second Feed: %v", err)
	}
	want := []impl.Hit{{Pattern: "abc", Start: 4, End: 7}}
	if !slices.Equal(second, want) {
		t.Fatalf("second Feed = %+v, want %+v", second, want)
	}
	if extra, err := s.Feed([]byte("ZZ"), 9); err != nil || len(extra) != 0 {
		t.Fatalf("third Feed = %+v, %v; want no repeat of the held hit", extra, err)
	}
}

func TestFlushDoesNotDuplicateReportedHits(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "abc", Kind: impl.Exact, Prefix: []byte("abc")}})
	hits, err := s.Feed([]byte("abcXY"), 0)
	if err != nil {
		t.Fatalf("Feed: unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Feed returned %d hits, want 1", len(hits))
	}
	if pending := s.Flush(); len(pending) != 0 {
		t.Fatalf("Flush returned %+v, want no duplicate of an already reported hit", pending)
	}
	if pending := s.Flush(); len(pending) != 0 {
		t.Fatalf("second Flush returned %+v, want empty", pending)
	}
}

func TestStatsTrackChunksAndHits(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "abc", Kind: impl.Exact, Prefix: []byte("abc")}})
	if _, err := s.Feed([]byte("abc"), 0); err != nil {
		t.Fatalf("first Feed: %v", err)
	}
	if _, err := s.Feed([]byte("XY"), 3); err != nil {
		t.Fatalf("second Feed: %v", err)
	}
	st := s.Stats()
	if st.Chunks != 2 || st.Bytes != 5 || st.Hits != 1 {
		t.Fatalf("Stats = %+v, want 2 chunks, 5 bytes, 1 hit", st)
	}
}

func TestResetDropsIndex(t *testing.T) {
	s := scanner()
	install(t, s, []impl.Pattern{{ID: "abc", Kind: impl.Exact, Prefix: []byte("abc")}})
	if err := s.Reset(); err != nil {
		t.Fatalf("Reset: unexpected error: %v", err)
	}
	if st := s.Stats(); st.Chunks != 0 || st.Bytes != 0 || st.Hits != 0 {
		t.Fatalf("Stats after Reset = %+v, want zero", st)
	}
	if _, err := s.Feed([]byte("abc"), 0); !errors.Is(err, impl.ErrNoIndex) {
		t.Fatalf("Feed after Reset = %v, want ErrNoIndex", err)
	}
}
