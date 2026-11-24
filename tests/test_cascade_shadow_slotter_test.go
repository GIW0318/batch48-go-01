package task

import (
	"errors"
	"math"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-04"
)

func closeTo(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func testCamera() impl.Camera {
	return impl.Camera{Near: 0.5, Far: 32, FovY: 60, Aspect: 1.777}
}

func TestSplitBuildsGeometricRangesWithoutGaps(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	got, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("cascade count = %d, want 4", len(got))
	}
	if !closeTo(got[0].Near, 0.5) {
		t.Fatalf("first cascade starts at %v, want the camera near plane 0.5", got[0].Near)
	}
	if !closeTo(got[3].Far, 32) {
		t.Fatalf("last cascade ends at %v, want the camera far plane 32", got[3].Far)
	}
	ratio := math.Pow(32.0/0.5, 1.0/4.0)
	for i, c := range got {
		if c.ID != i {
			t.Fatalf("cascade %d carries id %d, want %d", i, c.ID, i)
		}
		if !closeTo(c.Far/c.Near, ratio) {
			t.Fatalf("cascade %d ratio = %v, want the constant %v", i, c.Far/c.Near, ratio)
		}
		if i+1 < len(got) && !closeTo(c.Far, got[i+1].Near) {
			t.Fatalf("cascade %d ends at %v but cascade %d starts at %v", i, c.Far, i+1, got[i+1].Near)
		}
	}
}

func TestSplitRejectsImpossibleRanges(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	if _, err := s.Split(testCamera(), 0); !errors.Is(err, impl.ErrBadCascadeCount) {
		t.Fatalf("zero cascades must report ErrBadCascadeCount, got %v", err)
	}
	if _, err := s.Split(testCamera(), impl.MaxCascades+1); !errors.Is(err, impl.ErrBadCascadeCount) {
		t.Fatalf("too many cascades must report ErrBadCascadeCount, got %v", err)
	}
	bad := []impl.Camera{
		{Near: 0, Far: 32, FovY: 60, Aspect: 1.5},
		{Near: 5, Far: 5, FovY: 60, Aspect: 1.5},
		{Near: 1, Far: 32, FovY: 0, Aspect: 1.5},
		{Near: 1, Far: 32, FovY: 200, Aspect: 1.5},
		{Near: 1, Far: 32, FovY: 60, Aspect: 0},
	}
	for i, cam := range bad {
		if _, err := s.Split(cam, 4); !errors.Is(err, impl.ErrBadCamera) {
			t.Fatalf("bad camera %d must report ErrBadCamera, got %v", i, err)
		}
	}
}

func TestSplitWithOneCascadeCoversTheWholeCamera(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	got, err := s.Split(testCamera(), 1)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("cascade count = %d, want 1", len(got))
	}
	if !closeTo(got[0].Near, 0.5) || !closeTo(got[0].Far, 32) {
		t.Fatalf("cascade range = [%v, %v], want [0.5, 32]", got[0].Near, got[0].Far)
	}
}

func TestAssignSlotsNumbersTheCascadesInOrder(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, 4); err != nil {
		t.Fatalf("assigning slots must succeed, got %v", err)
	}
	stored := s.Slots()
	if len(stored) != 4 {
		t.Fatalf("stored cascade count = %d, want 4", len(stored))
	}
	for i, c := range stored {
		if c.Slot != i {
			t.Fatalf("cascade %d sits in slot %d, want slot %d", i, c.Slot, i)
		}
		if !closeTo(c.Near, cascades[i].Near) || !closeTo(c.Far, cascades[i].Far) {
			t.Fatalf("stored cascade %d = [%v, %v], want the split range", i, c.Near, c.Far)
		}
	}
}

func TestAssignSlotsDegradesInsteadOfFailingWhenShort(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, 2); err != nil {
		t.Fatalf("a short slot table must degrade instead of failing, got %v", err)
	}
	stored := s.Slots()
	want := []int{0, 1, -1, -1}
	if len(stored) != len(want) {
		t.Fatalf("stored cascade count = %d, want 4", len(stored))
	}
	for i, slot := range want {
		if stored[i].Slot != slot {
			t.Fatalf("cascade %d sits in slot %d, want %d", i, stored[i].Slot, slot)
		}
	}
}

func TestAssignSlotsValidatesTheTable(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(nil, 2); !errors.Is(err, impl.ErrBadCascadeCount) {
		t.Fatalf("an empty cascade table must report ErrBadCascadeCount, got %v", err)
	}
	if err := s.AssignSlots(cascades, 0); !errors.Is(err, impl.ErrBadSlotCount) {
		t.Fatalf("zero slots must report ErrBadSlotCount, got %v", err)
	}
	if err := s.AssignSlots(cascades, impl.MaxSlots+1); !errors.Is(err, impl.ErrBadSlotCount) {
		t.Fatalf("too many slots must report ErrBadSlotCount, got %v", err)
	}
	tooMany := make([]impl.Cascade, impl.MaxCascades+1)
	if err := s.AssignSlots(tooMany, 2); !errors.Is(err, impl.ErrBadCascadeCount) {
		t.Fatalf("too many cascades must report ErrBadCascadeCount, got %v", err)
	}
}

func TestResolutionHalvesPerCascade(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, 4); err != nil {
		t.Fatalf("assigning slots must succeed, got %v", err)
	}
	want := []int{1024, 512, 256, 128}
	for i, size := range want {
		w, h, err := s.Resolution(i)
		if err != nil {
			t.Fatalf("resolution of cascade %d must succeed, got %v", i, err)
		}
		if w != size || h != size {
			t.Fatalf("cascade %d resolution = %dx%d, want %dx%d", i, w, h, size, size)
		}
	}
	if _, _, err := s.Resolution(4); !errors.Is(err, impl.ErrNoSuchCascade) {
		t.Fatalf("an unknown cascade must report ErrNoSuchCascade, got %v", err)
	}
	if _, _, err := s.Resolution(-1); !errors.Is(err, impl.ErrNoSuchCascade) {
		t.Fatalf("a negative cascade id must report ErrNoSuchCascade, got %v", err)
	}
}

func TestResolutionStopsAtTheFloor(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), impl.MaxCascades)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, impl.MaxSlots); err != nil {
		t.Fatalf("assigning slots must succeed, got %v", err)
	}
	for _, id := range []int{4, 5, 6, 7} {
		w, h, err := s.Resolution(id)
		if err != nil {
			t.Fatalf("resolution of cascade %d must succeed, got %v", id, err)
		}
		if w != impl.MinResolution || h != impl.MinResolution {
			t.Fatalf("cascade %d resolution = %dx%d, want the floor %d", id, w, h, impl.MinResolution)
		}
	}
	if w, _, err := s.Resolution(0); err != nil || w != 1024 {
		t.Fatalf("cascade 0 resolution = %d err %v, want 1024", w, err)
	}
}

func TestSetCameraWaitsForTheFrameBoundary(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	first := testCamera()
	if err := s.SetCamera(first); err != nil {
		t.Fatalf("staging a camera must succeed, got %v", err)
	}
	if err := s.CommitFrame(); err != nil {
		t.Fatalf("committing the frame must succeed, got %v", err)
	}
	if cam, err := s.Camera(); err != nil || cam != first {
		t.Fatalf("camera = %+v err %v, want the staged camera", cam, err)
	}
	if err := s.SetCamera(impl.Camera{Near: -1, Far: 10, FovY: 60, Aspect: 1.5}); !errors.Is(err, impl.ErrBadCamera) {
		t.Fatalf("a broken camera must report ErrBadCamera, got %v", err)
	}
	if cam, err := s.Camera(); err != nil || cam != first {
		t.Fatalf("a rejected camera changed the active one to %+v err %v", cam, err)
	}
	second := impl.Camera{Near: 0.25, Far: 64, FovY: 45, Aspect: 2}
	if err := s.SetCamera(second); err != nil {
		t.Fatalf("staging a camera must succeed, got %v", err)
	}
	if cam, err := s.Camera(); err != nil || cam != first {
		t.Fatalf("camera before the boundary = %+v err %v, want the old camera", cam, err)
	}
	if err := s.CommitFrame(); err != nil {
		t.Fatalf("committing the frame must succeed, got %v", err)
	}
	if cam, err := s.Camera(); err != nil || cam != second {
		t.Fatalf("camera after the boundary = %+v err %v, want the new camera", cam, err)
	}
}

func TestCommitFrameWithoutAStagedCameraIsANoop(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cam := testCamera()
	if err := s.SetCamera(cam); err != nil {
		t.Fatalf("staging a camera must succeed, got %v", err)
	}
	if err := s.CommitFrame(); err != nil {
		t.Fatalf("committing must succeed, got %v", err)
	}
	if err := s.CommitFrame(); err != nil {
		t.Fatalf("a second commit without a staged camera must succeed, got %v", err)
	}
	if got, err := s.Camera(); err != nil || got != cam {
		t.Fatalf("camera = %+v err %v, want the committed camera", got, err)
	}
}

func TestHysteresisValidatesItsThresholds(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	if err := s.Hysteresis(1.0, 0.5); err != nil {
		t.Fatalf("a shrinking pair must be accepted, got %v", err)
	}
	if err := s.Hysteresis(0.5, 1.0); !errors.Is(err, impl.ErrBadHysteresis) {
		t.Fatalf("an inverted pair must report ErrBadHysteresis, got %v", err)
	}
	if err := s.Hysteresis(0.5, 0.5); !errors.Is(err, impl.ErrBadHysteresis) {
		t.Fatalf("equal thresholds must report ErrBadHysteresis, got %v", err)
	}
	if err := s.Hysteresis(-1, -2); !errors.Is(err, impl.ErrBadHysteresis) {
		t.Fatalf("negative thresholds must report ErrBadHysteresis, got %v", err)
	}
}

func TestSelectCascadeFindsTheCoveringRange(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, 4); err != nil {
		t.Fatalf("assigning slots must succeed, got %v", err)
	}
	cases := []struct {
		distance float64
		want     int
	}{
		{0.7, 0},
		{2.0, 1},
		{5.0, 2},
		{20.0, 3},
	}
	for _, c := range cases {
		got, err := s.SelectCascade(c.distance)
		if err != nil {
			t.Fatalf("selecting at %v must succeed, got %v", c.distance, err)
		}
		if got != c.want {
			t.Fatalf("distance %v selected cascade %d, want %d", c.distance, got, c.want)
		}
	}
	if _, err := s.SelectCascade(-0.5); !errors.Is(err, impl.ErrBadDistance) {
		t.Fatalf("a negative distance must report ErrBadDistance, got %v", err)
	}
}

func TestUnconfiguredSlotterRejectsQueries(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	if _, err := s.SelectCascade(1); !errors.Is(err, impl.ErrNotConfigured) {
		t.Fatalf("selecting without a cascade table must report ErrNotConfigured, got %v", err)
	}
	if _, _, err := s.Resolution(0); !errors.Is(err, impl.ErrNotConfigured) {
		t.Fatalf("resolution without a cascade table must report ErrNotConfigured, got %v", err)
	}
	if _, err := s.Camera(); !errors.Is(err, impl.ErrNotConfigured) {
		t.Fatalf("reading the camera without one must report ErrNotConfigured, got %v", err)
	}
}

func TestSnapshotRestoreKeepsCascadesCameraAndHysteresis(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	cascades, err := s.Split(testCamera(), 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, 2); err != nil {
		t.Fatalf("assigning slots must succeed, got %v", err)
	}
	cam := testCamera()
	if err := s.SetCamera(cam); err != nil {
		t.Fatalf("staging a camera must succeed, got %v", err)
	}
	if err := s.CommitFrame(); err != nil {
		t.Fatalf("committing must succeed, got %v", err)
	}
	if err := s.Hysteresis(1.5, 0.75); err != nil {
		t.Fatalf("setting hysteresis must succeed, got %v", err)
	}
	data, err := s.Snapshot()
	if err != nil {
		t.Fatalf("snapshot must succeed, got %v", err)
	}
	restored := impl.NewCascadeShadowSlotter()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("restore must succeed, got %v", err)
	}
	if got, err := restored.Camera(); err != nil || got != cam {
		t.Fatalf("restored camera = %+v err %v, want the committed camera", got, err)
	}
	stored := restored.Slots()
	if len(stored) != 4 || stored[0].Slot != 0 || stored[1].Slot != 1 || stored[2].Slot != -1 {
		t.Fatalf("restored slots = %+v, want 0, 1, -1, -1", stored)
	}
	if id, err := restored.SelectCascade(0.7); err != nil || id != 0 {
		t.Fatalf("restored selection = %d err %v, want cascade 0", id, err)
	}
	if w, h, err := restored.Resolution(1); err != nil || w != 512 || h != 512 {
		t.Fatalf("restored resolution = %dx%d err %v, want 512x512", w, h, err)
	}
	if err := restored.Restore([]byte("{")); !errors.Is(err, impl.ErrBadSnapshot) {
		t.Fatalf("a broken snapshot must report ErrBadSnapshot, got %v", err)
	}
	if w, _, err := restored.Resolution(1); err != nil || w != 512 {
		t.Fatalf("a rejected snapshot changed the table, got %d err %v", w, err)
	}
}

func TestConcurrentReadersNeverSeeAHalfAppliedCamera(t *testing.T) {
	s := impl.NewCascadeShadowSlotter()
	first := impl.Camera{Near: 0.5, Far: 32, FovY: 60, Aspect: 1.5}
	second := impl.Camera{Near: 0.25, Far: 64, FovY: 60, Aspect: 2.5}
	if err := s.SetCamera(first); err != nil {
		t.Fatalf("staging a camera must succeed, got %v", err)
	}
	if err := s.CommitFrame(); err != nil {
		t.Fatalf("committing must succeed, got %v", err)
	}
	cascades, err := s.Split(first, 4)
	if err != nil {
		t.Fatalf("splitting must succeed, got %v", err)
	}
	if err := s.AssignSlots(cascades, 4); err != nil {
		t.Fatalf("assigning slots must succeed, got %v", err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cam, err := s.Camera()
				if err != nil {
					t.Errorf("camera read failed: %v", err)
					return
				}
				if cam != first && cam != second {
					t.Errorf("camera read %+v, want one of the two whole configurations", cam)
					return
				}
				if w, h, err := s.Resolution(3); err != nil || w != 128 || h != 128 {
					t.Errorf("resolution during the switch = %dx%d err %v, want 128x128", w, h, err)
					return
				}
			}
		}()
	}
	for i := 0; i < 60; i++ {
		next := first
		if i%2 == 1 {
			next = second
		}
		if err := s.SetCamera(next); err != nil {
			t.Fatalf("staging a camera must succeed, got %v", err)
		}
		if err := s.CommitFrame(); err != nil {
			t.Fatalf("committing must succeed, got %v", err)
		}
		got, err := s.Split(next, 4)
		if err != nil {
			t.Fatalf("splitting must succeed, got %v", err)
		}
		if !closeTo(got[0].Near, next.Near) || !closeTo(got[3].Far, next.Far) {
			t.Fatalf("split range [%v, %v] does not match the camera [%v, %v]", got[0].Near, got[3].Far, next.Near, next.Far)
		}
	}
	close(stop)
	wg.Wait()
}
