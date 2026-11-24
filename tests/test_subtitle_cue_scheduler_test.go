package task

import (
	"testing"

	impl "gsb/batch48-go-game-c-07"
)

func cueLimits() impl.Limits {
	return impl.Limits{MaxCues: 8, MaxTracks: 4, MaxVisible: 4, MaxRows: 2, MaxCols: 20}
}

func newCueScheduler(t *testing.T, limits impl.Limits) *impl.CueScheduler {
	t.Helper()
	s, err := impl.NewCueScheduler(limits)
	if err != nil {
		t.Fatalf("NewCueScheduler returned error: %v", err)
	}
	if s == nil {
		t.Fatal("NewCueScheduler returned nil")
	}
	return s
}

func addCue(t *testing.T, s *impl.CueScheduler, c impl.Cue) {
	t.Helper()
	if err := s.AddCue(c); err != nil {
		t.Fatalf("AddCue(%q) returned error: %v", c.ID, err)
	}
}

func TestNewRejectsInvalidLimits(t *testing.T) {
	bad := []impl.Limits{
		{MaxCues: 0, MaxTracks: 2, MaxVisible: 2, MaxRows: 2, MaxCols: 20},
		{MaxCues: 4, MaxTracks: 0, MaxVisible: 2, MaxRows: 2, MaxCols: 20},
		{MaxCues: 4, MaxTracks: 2, MaxVisible: 0, MaxRows: 2, MaxCols: 20},
	}
	for i, limits := range bad {
		if _, err := impl.NewCueScheduler(limits); err != impl.ErrLimitRejected {
			t.Fatalf("case %d: expected ErrLimitRejected, got %v", i, err)
		}
	}
}

func TestAddCueRejectsEmptyFields(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	if err := s.AddCue(impl.Cue{ID: "", Track: "en", StartMs: 0, EndMs: 100}); err != impl.ErrInvalidCue {
		t.Fatalf("expected ErrInvalidCue for an empty id, got %v", err)
	}
	if err := s.AddCue(impl.Cue{ID: "c1", Track: "", StartMs: 0, EndMs: 100}); err != impl.ErrInvalidCue {
		t.Fatalf("expected ErrInvalidCue for an empty track, got %v", err)
	}
}

func TestAddCueRejectsInvertedRange(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	if err := s.AddCue(impl.Cue{ID: "c1", Track: "en", StartMs: 900, EndMs: 400}); err != impl.ErrInvalidRange {
		t.Fatalf("expected ErrInvalidRange for an inverted range, got %v", err)
	}
	if err := s.AddCue(impl.Cue{ID: "c2", Track: "en", StartMs: -5, EndMs: 400}); err != impl.ErrInvalidRange {
		t.Fatalf("expected ErrInvalidRange for a negative start, got %v", err)
	}
}

func TestAddCueRejectsNegativeLayer(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	err := s.AddCue(impl.Cue{ID: "c1", Track: "en", StartMs: 0, EndMs: 100, Layer: -1})
	if err != impl.ErrInvalidLayer {
		t.Fatalf("expected ErrInvalidLayer, got %v", err)
	}
}

func TestAddCueRejectsDuplicateID(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 0, EndMs: 100})
	err := s.AddCue(impl.Cue{ID: "c1", Track: "jp", StartMs: 200, EndMs: 300})
	if err != impl.ErrDuplicateCue {
		t.Fatalf("expected ErrDuplicateCue, got %v", err)
	}
}

func TestAddCueEnforcesCueLimit(t *testing.T) {
	limits := cueLimits()
	limits.MaxCues = 2
	s := newCueScheduler(t, limits)
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 0, EndMs: 100})
	addCue(t, s, impl.Cue{ID: "c2", Track: "en", StartMs: 200, EndMs: 300})
	err := s.AddCue(impl.Cue{ID: "c3", Track: "en", StartMs: 400, EndMs: 500})
	if err != impl.ErrCueLimit {
		t.Fatalf("expected ErrCueLimit, got %v", err)
	}
	if got := s.CueCount(); got != 2 {
		t.Fatalf("cue count = %d, want the rejected cue not to be stored (2)", got)
	}
}

func TestAddCueEnforcesTrackLimit(t *testing.T) {
	limits := cueLimits()
	limits.MaxTracks = 1
	s := newCueScheduler(t, limits)
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 0, EndMs: 100})
	err := s.AddCue(impl.Cue{ID: "c2", Track: "jp", StartMs: 200, EndMs: 300})
	if err != impl.ErrTrackLimit {
		t.Fatalf("expected ErrTrackLimit, got %v", err)
	}
	if got := s.CueCount(); got != 1 {
		t.Fatalf("cue count = %d, want 1 after the rejected track", got)
	}
}

func TestActiveReturnsCueAtSeekTime(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 1000, EndMs: 3000, Layer: 1, Text: "hello"})
	if err := s.SeekTo(2000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	active := s.Active()
	if len(active) != 1 {
		t.Fatalf("active cues = %d, want 1", len(active))
	}
	if active[0].CueID != "c1" || active[0].Text != "hello" {
		t.Fatalf("active[0] = %+v, want c1/hello", active[0])
	}
}

func TestActiveExcludesCuesOutsideWindow(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 1000, EndMs: 3000})
	if err := s.SeekTo(500); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	if got := s.Clock(); got != 500 {
		t.Fatalf("clock after Seek = %d, want 500", got)
	}
	if got := len(s.Active()); got != 0 {
		t.Fatalf("active cues before the window = %d, want 0", got)
	}
	if err := s.SeekTo(3500); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	if got := len(s.Active()); got != 0 {
		t.Fatalf("active cues after the window = %d, want 0", got)
	}
}

func TestActiveOrdersByLayerDescending(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "low", Track: "en", StartMs: 1000, EndMs: 3000, Layer: 1})
	addCue(t, s, impl.Cue{ID: "high", Track: "en", StartMs: 1000, EndMs: 3000, Layer: 5})
	if err := s.SeekTo(2000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	active := s.Active()
	if len(active) != 2 {
		t.Fatalf("active cues = %d, want 2", len(active))
	}
	if active[0].CueID != "high" || active[1].CueID != "low" {
		t.Fatalf("active order = %q then %q, want high before low", active[0].CueID, active[1].CueID)
	}
}

func TestActiveTruncatesAtVisibleLimit(t *testing.T) {
	limits := cueLimits()
	limits.MaxVisible = 2
	s := newCueScheduler(t, limits)
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 0, EndMs: 5000, Layer: 9})
	addCue(t, s, impl.Cue{ID: "c2", Track: "jp", StartMs: 0, EndMs: 5000, Layer: 5})
	addCue(t, s, impl.Cue{ID: "c3", Track: "ko", StartMs: 0, EndMs: 5000, Layer: 1})
	if err := s.SeekTo(1000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	active := s.Active()
	if len(active) != 2 {
		t.Fatalf("visible cues = %d, want 2", len(active))
	}
	if active[0].Layer != 9 || active[1].Layer != 5 {
		t.Fatalf("visible layers = %d,%d, want 9,5", active[0].Layer, active[1].Layer)
	}
	if got := s.Stats().DroppedTotal; got != 1 {
		t.Fatalf("dropped total = %d, want 1", got)
	}
}

func TestLongTextTruncatedAndMarked(t *testing.T) {
	limits := cueLimits()
	limits.MaxRows = 2
	limits.MaxCols = 4
	s := newCueScheduler(t, limits)
	addCue(t, s, impl.Cue{ID: "long", Track: "en", StartMs: 0, EndMs: 5000, Layer: 2, Text: "abcdefghij"})
	addCue(t, s, impl.Cue{ID: "short", Track: "jp", StartMs: 0, EndMs: 5000, Layer: 1, Text: "ok"})
	if err := s.SeekTo(1000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	active := s.Active()
	if len(active) != 2 {
		t.Fatalf("active cues = %d, want 2", len(active))
	}
	if active[0].CueID != "long" {
		t.Fatalf("active[0] = %q, want the higher layer cue first", active[0].CueID)
	}
	if active[0].Text != "abcdefgh" {
		t.Fatalf("truncated text = %q, want %q", active[0].Text, "abcdefgh")
	}
	if !active[0].Truncated {
		t.Fatal("long cue was not marked as truncated")
	}
	if active[1].Text != "ok" || active[1].Truncated {
		t.Fatalf("short cue = %+v, want untouched text", active[1])
	}
	if got := s.Stats().TruncatedTotal; got != 1 {
		t.Fatalf("truncated total = %d, want 1", got)
	}
}

func TestSeekRebuildsActiveSet(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 1000, EndMs: 3000})
	if err := s.SeekTo(2000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	if got := len(s.Active()); got != 1 {
		t.Fatalf("active cues at 2000 = %d, want 1", got)
	}
	if err := s.SeekTo(4000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	if got := len(s.Active()); got != 0 {
		t.Fatalf("active cues at 4000 = %d, want 0", got)
	}
	if err := s.SeekTo(2000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	if got := len(s.Active()); got != 1 {
		t.Fatalf("active cues back at 2000 = %d, want the set to be rebuilt", got)
	}
}

func TestSnapshotRestoreKeepsCuesAndClock(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 1000, EndMs: 3000, Layer: 2, Text: "first"})
	addCue(t, s, impl.Cue{ID: "c2", Track: "jp", StartMs: 1500, EndMs: 2500, Layer: 1, Text: "second"})
	if err := s.SeekTo(2000); err != nil {
		t.Fatalf("Seek returned error: %v", err)
	}
	st, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	r := newCueScheduler(t, cueLimits())
	if err := r.Restore(st); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if got := r.CueCount(); got != 2 {
		t.Fatalf("restored cue count = %d, want 2", got)
	}
	if got := r.Clock(); got != 2000 {
		t.Fatalf("restored clock = %d, want 2000", got)
	}
	if got := len(r.Active()); got != 2 {
		t.Fatalf("restored active cues = %d, want 2", got)
	}
}

func TestStatsTrackAddsAndRemoves(t *testing.T) {
	s := newCueScheduler(t, cueLimits())
	addCue(t, s, impl.Cue{ID: "c1", Track: "en", StartMs: 0, EndMs: 100})
	addCue(t, s, impl.Cue{ID: "c2", Track: "jp", StartMs: 200, EndMs: 300})
	if err := s.RemoveCue("c1"); err != nil {
		t.Fatalf("RemoveCue returned error: %v", err)
	}
	stats := s.Stats()
	if stats.Added != 2 {
		t.Fatalf("added = %d, want 2", stats.Added)
	}
	if stats.Removed != 1 {
		t.Fatalf("removed = %d, want 1", stats.Removed)
	}
	if got := s.CueCount(); got != 1 {
		t.Fatalf("cue count = %d, want 1", got)
	}
	if got := s.Tracks(); len(got) != 1 || got[0] != "jp" {
		t.Fatalf("tracks = %v, want only jp left", got)
	}
}
