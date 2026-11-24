package task

import (
	"errors"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-c-05"
)

func c05Layout2() impl.Layout {
	return impl.Layout{Name: "duo", Regions: []impl.Region{
		{Name: "main", X: 0, Y: 0, W: 960, H: 720},
		{Name: "pip", X: 960, Y: 0, W: 320, H: 720},
	}}
}

func c05Layout3() impl.Layout {
	return impl.Layout{Name: "trio", Regions: []impl.Region{
		{Name: "main", X: 0, Y: 0, W: 640, H: 360},
		{Name: "pip", X: 640, Y: 0, W: 320, H: 180},
		{Name: "side", X: 640, Y: 180, W: 320, H: 180},
	}}
}

func c05Layout1() impl.Layout {
	return impl.Layout{Name: "solo", Regions: []impl.Region{
		{Name: "only", X: 0, Y: 0, W: 640, H: 360},
	}}
}

func c05Config() impl.Config {
	return impl.Config{OutputFPS: 60, HysteresisCount: 3}
}

func c05New(t *testing.T, layout impl.Layout, cfg impl.Config) *impl.CompositorGuard {
	t.Helper()
	g, err := impl.NewCompositorGuard(layout, cfg)
	if err != nil {
		t.Fatalf("new compositor guard: %v", err)
	}
	return g
}

func c05Track(id string, fps int, region string) impl.Track {
	return impl.Track{ID: id, Width: 640, Height: 360, FPS: fps, Region: region}
}

func TestNewGuardRejectsInvalidLayout(t *testing.T) {
	overlap := impl.Layout{Name: "bad", Regions: []impl.Region{
		{Name: "a", X: 0, Y: 0, W: 640, H: 360},
		{Name: "b", X: 320, Y: 180, W: 640, H: 360},
	}}
	if _, err := impl.NewCompositorGuard(overlap, c05Config()); !errors.Is(err, impl.ErrRegionOverlap) {
		t.Fatalf("overlapping layout error = %v, want ErrRegionOverlap", err)
	}
	duplicate := impl.Layout{Name: "bad", Regions: []impl.Region{
		{Name: "a", X: 0, Y: 0, W: 640, H: 360},
		{Name: "a", X: 640, Y: 0, W: 320, H: 180},
	}}
	if _, err := impl.NewCompositorGuard(duplicate, c05Config()); !errors.Is(err, impl.ErrLayoutInvalid) {
		t.Fatalf("duplicate region name error = %v, want ErrLayoutInvalid", err)
	}
	zero := impl.Layout{Name: "bad", Regions: []impl.Region{{Name: "a", X: 0, Y: 0, W: 0, H: 360}}}
	if _, err := impl.NewCompositorGuard(zero, c05Config()); !errors.Is(err, impl.ErrLayoutInvalid) {
		t.Fatalf("zero width region error = %v, want ErrLayoutInvalid", err)
	}
}

func TestAddTrackValidates(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("", 30, "")); !errors.Is(err, impl.ErrTrackInvalid) {
		t.Fatalf("empty track id error = %v, want ErrTrackInvalid", err)
	}
	if err := g.AddTrack(c05Track("t1", 0, "")); !errors.Is(err, impl.ErrTrackInvalid) {
		t.Fatalf("zero fps error = %v, want ErrTrackInvalid", err)
	}
	if err := g.AddTrack(impl.Track{ID: "t2", Width: 0, Height: 360, FPS: 30}); !errors.Is(err, impl.ErrTrackInvalid) {
		t.Fatalf("zero width error = %v, want ErrTrackInvalid", err)
	}
	if err := g.AddTrack(c05Track("t1", 30, "")); err != nil {
		t.Fatalf("add valid track: %v", err)
	}
	if err := g.AddTrack(c05Track("t1", 30, "")); !errors.Is(err, impl.ErrTrackExists) {
		t.Fatalf("duplicate track error = %v, want ErrTrackExists", err)
	}
	if got := g.Stats().TrackAdds; got != 1 {
		t.Fatalf("track adds = %d, want 1", got)
	}
}

func TestAddTrackAutoAssignsRegions(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "")); err != nil {
		t.Fatalf("add t2: %v", err)
	}
	r1, ok := g.TrackRegion("t1")
	if !ok || r1 != "main" {
		t.Fatalf("t1 region = %q ok=%v, want main", r1, ok)
	}
	r2, ok := g.TrackRegion("t2")
	if !ok || r2 != "pip" {
		t.Fatalf("t2 region = %q ok=%v, want pip", r2, ok)
	}
}

func TestAddTrackRejectsUnknownOrTakenRegion(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "nowhere")); !errors.Is(err, impl.ErrUnknownRegion) {
		t.Fatalf("unknown region error = %v, want ErrUnknownRegion", err)
	}
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "main")); !errors.Is(err, impl.ErrRegionTaken) {
		t.Fatalf("taken region error = %v, want ErrRegionTaken", err)
	}
	if _, ok := g.TrackRegion("t2"); ok {
		t.Fatalf("rejected track must not be assigned a region")
	}
}

func TestAddTrackWithoutFreeRegion(t *testing.T) {
	g := c05New(t, c05Layout1(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "")); !errors.Is(err, impl.ErrNoRegion) {
		t.Fatalf("second track error = %v, want ErrNoRegion", err)
	}
	if _, ok := g.TrackRegion("t2"); ok {
		t.Fatalf("track without a region must not be stored")
	}
	if got := g.Stats().TrackAdds; got != 1 {
		t.Fatalf("track adds = %d, want only the accepted track", got)
	}
}

func TestRemoveTrackFreesRegion(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.RemoveTrack("ghost"); !errors.Is(err, impl.ErrUnknownTrack) {
		t.Fatalf("remove unknown track error = %v, want ErrUnknownTrack", err)
	}
	if err := g.RemoveTrack("t1"); err != nil {
		t.Fatalf("remove t1: %v", err)
	}
	if _, ok := g.TrackRegion("t1"); ok {
		t.Fatalf("removed track still has a region")
	}
	if err := g.AddTrack(c05Track("t3", 30, "main")); err != nil {
		t.Fatalf("region must be reusable after removal: %v", err)
	}
	if got := g.Stats().TrackRemoves; got != 1 {
		t.Fatalf("track removes = %d, want 1", got)
	}
}

func TestSetLayoutRejectsOverlapAtomically(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	overlap := impl.Layout{Name: "bad", Regions: []impl.Region{
		{Name: "main", X: 0, Y: 0, W: 800, H: 720},
		{Name: "pip", X: 400, Y: 0, W: 400, H: 720},
	}}
	if err := g.SetLayout(overlap); !errors.Is(err, impl.ErrRegionOverlap) {
		t.Fatalf("overlapping layout error = %v, want ErrRegionOverlap", err)
	}
	regions := g.Regions()
	if len(regions) != 2 || regions[0].Name != "main" || regions[1].Name != "pip" {
		t.Fatalf("regions after rejected layout = %+v, want the old template", regions)
	}
}

func TestSetLayoutReassignsOrFailsAtomically(t *testing.T) {
	g := c05New(t, c05Layout3(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "pip")); err != nil {
		t.Fatalf("add t2: %v", err)
	}
	if err := g.SetLayout(c05Layout1()); !errors.Is(err, impl.ErrNoRegion) {
		t.Fatalf("layout without enough regions error = %v, want ErrNoRegion", err)
	}
	if got := len(g.Regions()); got != 3 {
		t.Fatalf("regions after rejected layout = %d, want the old three", got)
	}
	if err := g.RemoveTrack("t2"); err != nil {
		t.Fatalf("remove t2: %v", err)
	}
	if err := g.SetLayout(c05Layout1()); err != nil {
		t.Fatalf("layout after freeing a region: %v", err)
	}
	regions := g.Regions()
	if len(regions) != 1 || regions[0].Name != "only" {
		t.Fatalf("regions after accepted layout = %+v, want the single only region", regions)
	}
}

func TestSetActiveSpeakerHysteresis(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "pip")); err != nil {
		t.Fatalf("add t2: %v", err)
	}
	if got := g.ActiveSpeaker(); got != "t1" {
		t.Fatalf("initial speaker = %q, want the first track", got)
	}
	for i := 0; i < 2; i++ {
		if err := g.SetActiveSpeaker("t2"); err != nil {
			t.Fatalf("nomination %d: %v", i, err)
		}
		if got := g.ActiveSpeaker(); got != "t1" {
			t.Fatalf("speaker switched to %q before the hysteresis count", got)
		}
	}
	if err := g.SetActiveSpeaker("t2"); err != nil {
		t.Fatalf("third nomination: %v", err)
	}
	if got := g.ActiveSpeaker(); got != "t2" {
		t.Fatalf("speaker = %q, want t2 after three nominations", got)
	}
	if got := g.Stats().SpeakerSwitches; got != 1 {
		t.Fatalf("speaker switches = %d, want 1", got)
	}
}

func TestSetActiveSpeakerUnknownAndReset(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "pip")); err != nil {
		t.Fatalf("add t2: %v", err)
	}
	if err := g.SetActiveSpeaker("ghost"); !errors.Is(err, impl.ErrUnknownTrack) {
		t.Fatalf("unknown speaker error = %v, want ErrUnknownTrack", err)
	}
	if got := g.ActiveSpeaker(); got != "t1" {
		t.Fatalf("unknown nomination changed the speaker to %q", got)
	}
	for i := 0; i < 2; i++ {
		if err := g.SetActiveSpeaker("t2"); err != nil {
			t.Fatalf("nomination %d: %v", i, err)
		}
	}
	if err := g.SetActiveSpeaker("t1"); err != nil {
		t.Fatalf("re-nominate the active speaker: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := g.SetActiveSpeaker("t2"); err != nil {
			t.Fatalf("nomination after reset %d: %v", i, err)
		}
		if got := g.ActiveSpeaker(); got != "t1" {
			t.Fatalf("speaker = %q, want the challenger counter restarted", got)
		}
	}
	if err := g.SetActiveSpeaker("t2"); err != nil {
		t.Fatalf("third nomination after reset: %v", err)
	}
	if got := g.ActiveSpeaker(); got != "t2" {
		t.Fatalf("speaker = %q, want t2 after a full new run", got)
	}
	if got := g.Stats().SpeakerSwitches; got != 1 {
		t.Fatalf("speaker switches = %d, want 1", got)
	}
}

func TestComposeRequiresIncreasingFrameNumbers(t *testing.T) {
	g := c05New(t, c05Layout1(), c05Config())
	if err := g.AddTrack(c05Track("t1", 60, "only")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if _, err := g.Compose(0, 0); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("frame zero error = %v, want ErrFrameOrder", err)
	}
	if _, err := g.Compose(1, 0); err != nil {
		t.Fatalf("first compose: %v", err)
	}
	if _, err := g.Compose(1, 16); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("repeated frame number error = %v, want ErrFrameOrder", err)
	}
	if _, err := g.Compose(2, 16); err != nil {
		t.Fatalf("second compose: %v", err)
	}
	if got := g.Stats().Frames; got != 2 {
		t.Fatalf("composed frames = %d, want 2", got)
	}
}

func TestComposeAlignsHalfRateInput(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("slow", 30, "main")); err != nil {
		t.Fatalf("add slow track: %v", err)
	}
	if err := g.AddTrack(c05Track("fast", 60, "pip")); err != nil {
		t.Fatalf("add fast track: %v", err)
	}
	comps := make([]*impl.Composition, 0, 4)
	for i := uint64(1); i <= 4; i++ {
		comp, err := g.Compose(i, int64((i-1)*16))
		if err != nil {
			t.Fatalf("compose %d: %v", i, err)
		}
		comps = append(comps, comp)
	}
	for i, comp := range comps {
		if len(comp.Cells) != 2 {
			t.Fatalf("composition %d has %d cells, want 2", i+1, len(comp.Cells))
		}
		if comp.Cells[0].Region != "main" || comp.Cells[0].TrackID != "slow" {
			t.Fatalf("cell 0 = %+v, want the slow track in main", comp.Cells[0])
		}
		if comp.Cells[1].Region != "pip" || comp.Cells[1].TrackID != "fast" {
			t.Fatalf("cell 1 = %+v, want the fast track in pip", comp.Cells[1])
		}
	}
	if comps[0].Cells[0].Filled {
		t.Fatalf("the first frame of a track must not be a fill")
	}
	if !comps[1].Cells[0].Filled {
		t.Fatalf("frame 2 of a 30 fps track in a 60 fps output must be filled")
	}
	if comps[2].Cells[0].Filled {
		t.Fatalf("frame 3 of a 30 fps track must be fresh")
	}
	if !comps[3].Cells[0].Filled {
		t.Fatalf("frame 4 of a 30 fps track must be filled again")
	}
	for i, comp := range comps {
		if comp.Cells[1].Filled {
			t.Fatalf("composition %d marked the 60 fps track as filled", i+1)
		}
	}
	if got := g.Stats().Filled; got != 2 {
		t.Fatalf("filled cells = %d, want 2", got)
	}
}

func TestComposeDropsExcessHighRateFrames(t *testing.T) {
	g := c05New(t, c05Layout1(), c05Config())
	if err := g.AddTrack(c05Track("turbo", 120, "only")); err != nil {
		t.Fatalf("add turbo track: %v", err)
	}
	for i := uint64(1); i <= 3; i++ {
		comp, err := g.Compose(i, int64((i-1)*16))
		if err != nil {
			t.Fatalf("compose %d: %v", i, err)
		}
		if comp.Cells[0].Filled {
			t.Fatalf("composition %d filled a track that runs faster than the output", i)
		}
	}
	if got := g.Stats().Dropped; got != 2 {
		t.Fatalf("dropped input frames = %d, want the surplus above one per output tick", got)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 30, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	if err := g.AddTrack(c05Track("t2", 30, "pip")); err != nil {
		t.Fatalf("add t2: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := g.SetActiveSpeaker("t2"); err != nil {
			t.Fatalf("nominate t2: %v", err)
		}
	}
	if _, err := g.Compose(1, 0); err != nil {
		t.Fatalf("compose 1: %v", err)
	}
	if _, err := g.Compose(2, 16); err != nil {
		t.Fatalf("compose 2: %v", err)
	}
	payload, err := g.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	h := c05New(t, c05Layout2(), c05Config())
	if err := h.Restore(payload); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := len(h.Regions()); got != 2 {
		t.Fatalf("restored regions = %d, want 2", got)
	}
	if got, ok := h.TrackRegion("t1"); !ok || got != "main" {
		t.Fatalf("restored t1 region = %q ok=%v, want main", got, ok)
	}
	if got := h.ActiveSpeaker(); got != "t2" {
		t.Fatalf("restored speaker = %q, want t2", got)
	}
	if got := h.Stats().Frames; got != 2 {
		t.Fatalf("restored frames = %d, want 2", got)
	}
	if _, err := h.Compose(2, 32); !errors.Is(err, impl.ErrFrameOrder) {
		t.Fatalf("restored frame watermark error = %v, want ErrFrameOrder", err)
	}
	if _, err := h.Compose(3, 32); err != nil {
		t.Fatalf("compose after restore: %v", err)
	}
	if err := h.Restore([]byte("junk")); !errors.Is(err, impl.ErrStateCorrupt) {
		t.Fatalf("bad snapshot error = %v, want ErrStateCorrupt", err)
	}
	if got := len(h.Regions()); got != 2 {
		t.Fatalf("bad restore changed the layout to %d regions", got)
	}
}

func TestConcurrentComposeAndReaders(t *testing.T) {
	g := c05New(t, c05Layout2(), c05Config())
	if err := g.AddTrack(c05Track("t1", 60, "main")); err != nil {
		t.Fatalf("add t1: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = g.Stats()
				_ = g.Regions()
				_ = g.ActiveSpeaker()
				_, _ = g.TrackRegion("t1")
			}
		}()
	}
	for i := uint64(1); i <= 5; i++ {
		if _, err := g.Compose(i, int64((i-1)*16)); err != nil {
			t.Fatalf("compose %d: %v", i, err)
		}
	}
	wg.Wait()
	if got := g.Stats().Frames; got != 5 {
		t.Fatalf("composed frames = %d, want every frame counted while readers run", got)
	}
	if got := g.Stats().Cells; got != 5 {
		t.Fatalf("cells = %d, want one cell per composed frame", got)
	}
}
