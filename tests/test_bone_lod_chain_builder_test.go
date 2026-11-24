package task

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-19"
)

func bone(id, parent string, weight float64) impl.Bone {
	return impl.Bone{ID: id, Parent: parent, Weight: weight}
}

func lodBase() impl.Skeleton {
	return impl.Skeleton{
		Root: "root",
		Bones: []impl.Bone{
			bone("root", "", 10),
			bone("leaf_a", "root", 5),
			bone("leaf_b", "root", 1),
			bone("leaf_c", "root", 3),
		},
	}
}

func curveBase() impl.Skeleton {
	return impl.Skeleton{
		Root: "root",
		Bones: []impl.Bone{
			bone("root", "", 1),
			bone("arm", "root", 1),
			bone("hand", "root", 1),
		},
	}
}

func TestBuildLodRejectsBadLevel(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := lodBase()
	for _, level := range []int{0, -1, 9} {
		if _, err := r.BuildLod(base, level, 3); !errors.Is(err, impl.ErrBadLevel) {
			t.Fatalf("level %d: got %v, want ErrBadLevel", level, err)
		}
	}
	if err := r.SetMaxLevels(0); !errors.Is(err, impl.ErrBadLevel) {
		t.Fatalf("zero max levels: got %v, want ErrBadLevel", err)
	}
	if err := r.SetMaxLevels(-3); !errors.Is(err, impl.ErrBadLevel) {
		t.Fatalf("negative max levels: got %v, want ErrBadLevel", err)
	}
	if err := r.SetMaxLevels(2); err != nil {
		t.Fatalf("SetMaxLevels(2): %v", err)
	}
	if got := r.MaxLevels(); got != 2 {
		t.Fatalf("max levels = %d, want 2", got)
	}
	if _, err := r.BuildLod(base, 3, 2); !errors.Is(err, impl.ErrBadLevel) {
		t.Fatalf("level above cap: got %v, want ErrBadLevel", err)
	}
}

func TestBuildLodRejectsBadMaxBones(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := lodBase()
	for _, maxBones := range []int{0, -1} {
		if _, err := r.BuildLod(base, 1, maxBones); !errors.Is(err, impl.ErrBadMaxBones) {
			t.Fatalf("max bones %d: got %v, want ErrBadMaxBones", maxBones, err)
		}
	}
	if _, err := r.BuildLod(base, 1, len(base.Bones)); !errors.Is(err, impl.ErrCannotSimplify) {
		t.Fatalf("max bones equal to bone count: got %v, want ErrCannotSimplify", err)
	}
	if _, err := r.BuildLod(base, 1, len(base.Bones)+2); !errors.Is(err, impl.ErrCannotSimplify) {
		t.Fatalf("max bones above bone count: got %v, want ErrCannotSimplify", err)
	}
	if _, err := r.BuildLod(base, 1, 3); err != nil {
		t.Fatalf("valid build: %v", err)
	}
}

func TestBuildLodRejectsMalformedSkeletons(t *testing.T) {
	base := lodBase()
	cases := []struct {
		name string
		skel impl.Skeleton
	}{
		{name: "no bones", skel: impl.Skeleton{}},
		{name: "root not in bones", skel: impl.Skeleton{Root: "ghost", Bones: append([]impl.Bone(nil), base.Bones...)}},
		{name: "duplicate id", skel: impl.Skeleton{Root: "root", Bones: append(append([]impl.Bone(nil), base.Bones...), bone("leaf_a", "root", 2))}},
		{name: "unknown parent", skel: func() impl.Skeleton {
			s := base
			s.Bones = append([]impl.Bone(nil), base.Bones...)
			s.Bones[1].Parent = "ghost"
			return s
		}()},
		{name: "cycle", skel: func() impl.Skeleton {
			s := base
			s.Bones = append([]impl.Bone(nil), base.Bones...)
			s.Bones[0].Parent = "leaf_a"
			return s
		}()},
		{name: "negative weight", skel: func() impl.Skeleton {
			s := base
			s.Bones = append([]impl.Bone(nil), base.Bones...)
			s.Bones[2].Weight = -1
			return s
		}()},
	}
	for _, tc := range cases {
		r := impl.NewBoneLodChainBuilder()
		if _, err := r.BuildLod(tc.skel, 1, 3); !errors.Is(err, impl.ErrBadSkeleton) {
			t.Fatalf("%s: got %v, want ErrBadSkeleton", tc.name, err)
		}
		if _, ok := r.LevelBones(1); ok {
			t.Fatalf("%s: failed build cached a level", tc.name)
		}
	}
}

func TestBuildLodMergesLightestLeaf(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := lodBase()
	out, err := r.BuildLod(base, 1, 3)
	if err != nil {
		t.Fatalf("BuildLod: %v", err)
	}
	if len(out.Bones) != 3 {
		t.Fatalf("level 1 bone count = %d, want 3", len(out.Bones))
	}
	if got, err := r.MapBone(1, "leaf_b"); err != nil || got != "root" {
		t.Fatalf("MapBone(leaf_b) = (%q,%v), want root", got, err)
	}
	if got, _ := r.MapBone(1, "leaf_a"); got != "leaf_a" {
		t.Fatalf("light leaf_a was merged into %q, want it to survive", got)
	}
	if got, _ := r.MapBone(1, "leaf_c"); got != "leaf_c" {
		t.Fatalf("leaf_c mapped to %q, want it to survive", got)
	}
	if got := r.Levels(); got != 1 {
		t.Fatalf("levels = %d, want 1", got)
	}
	if lvl, ok := r.LevelBones(1); !ok || len(lvl.Bones) != 3 {
		t.Fatalf("LevelBones(1) = (%d bones, %v), want 3 bones cached", len(lvl.Bones), ok)
	}
}

func TestBuildLodTieBreakByBoneID(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := impl.Skeleton{
		Root: "root",
		Bones: []impl.Bone{
			bone("root", "", 10),
			bone("leaf_b", "root", 1),
			bone("leaf_a", "root", 1),
			bone("leaf_c", "root", 3),
		},
	}
	if _, err := r.BuildLod(base, 1, 3); err != nil {
		t.Fatalf("BuildLod: %v", err)
	}
	if got, _ := r.MapBone(1, "leaf_a"); got != "root" {
		t.Fatalf("leaf_a mapped to %q, want root via the id tie break", got)
	}
	if got, _ := r.MapBone(1, "leaf_b"); got != "leaf_b" {
		t.Fatalf("leaf_b mapped to %q, want it to survive", got)
	}
}

func TestBuildLodKeepsProtectedBones(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := impl.Skeleton{
		Root: "root",
		Bones: []impl.Bone{
			bone("root", "", 10),
			{ID: "tip", Parent: "root", Weight: 0.1, Terminal: true},
			{ID: "hitbox", Parent: "root", Weight: 0.2, Collider: true},
			bone("free", "root", 5),
		},
	}
	if _, err := r.BuildLod(base, 1, 2); !errors.Is(err, impl.ErrCannotSimplify) {
		t.Fatalf("build that must merge protected bones: got %v, want ErrCannotSimplify", err)
	}
	if _, ok := r.LevelBones(1); ok {
		t.Fatalf("failed build cached a level")
	}
	if _, err := r.MapBone(1, "root"); !errors.Is(err, impl.ErrLevelNotBuilt) {
		t.Fatalf("MapBone after failed build: got %v, want ErrLevelNotBuilt", err)
	}
}

func TestBuildLodLevelUsesPreviousLevel(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := impl.Skeleton{
		Root: "root",
		Bones: []impl.Bone{
			bone("root", "", 10),
			bone("leaf_a", "root", 1),
			bone("leaf_b", "root", 2),
			bone("leaf_c", "root", 3),
			bone("leaf_d", "root", 4),
		},
	}
	if _, err := r.BuildLod(base, 1, 4); err != nil {
		t.Fatalf("level 1: %v", err)
	}
	if _, err := r.BuildLod(base, 2, 3); err != nil {
		t.Fatalf("level 2: %v", err)
	}
	lvl1, _ := r.LevelBones(1)
	lvl2, _ := r.LevelBones(2)
	if len(lvl1.Bones) != 4 || len(lvl2.Bones) != 3 {
		t.Fatalf("level sizes = (%d,%d), want (4,3)", len(lvl1.Bones), len(lvl2.Bones))
	}
	if got := r.Levels(); got != 2 {
		t.Fatalf("levels = %d, want 2", got)
	}
	if got, _ := r.MapBone(2, "leaf_a"); got != "root" {
		t.Fatalf("MapBone(2, leaf_a) = %q, want root", got)
	}
	if got, _ := r.MapBone(2, "leaf_b"); got != "root" {
		t.Fatalf("MapBone(2, leaf_b) = %q, want root", got)
	}
	if got, _ := r.MapBone(1, "leaf_b"); got != "leaf_b" {
		t.Fatalf("level 1 leaf_b mapped to %q, want it to survive there", got)
	}
}

func TestMapBoneRejectsUnbuiltLevelAndUnknownBone(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	if _, err := r.MapBone(1, "root"); !errors.Is(err, impl.ErrLevelNotBuilt) {
		t.Fatalf("MapBone before build: got %v, want ErrLevelNotBuilt", err)
	}
	base := lodBase()
	if _, err := r.BuildLod(base, 1, 3); err != nil {
		t.Fatalf("BuildLod: %v", err)
	}
	if _, err := r.MapBone(1, "ghost"); !errors.Is(err, impl.ErrUnknownBone) {
		t.Fatalf("MapBone unknown id: got %v, want ErrUnknownBone", err)
	}
	if got, err := r.MapBone(1, "root"); err != nil || got != "root" {
		t.Fatalf("MapBone(root) = (%q,%v), want root", got, err)
	}
	if _, err := r.MapBone(2, "root"); !errors.Is(err, impl.ErrLevelNotBuilt) {
		t.Fatalf("MapBone of an unbuilt level: got %v, want ErrLevelNotBuilt", err)
	}
	if _, ok := r.LevelBones(2); ok {
		t.Fatalf("LevelBones(2) reported a level that was never built")
	}
}

func TestMigrateCurvesMapsMergedBoneToParent(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	src := curveBase()
	dst := impl.Skeleton{Root: "root", Bones: []impl.Bone{bone("root", "", 1)}}
	out, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "arm", Weight: 1, Samples: []float64{1, 2, 3}},
	})
	if err != nil {
		t.Fatalf("MigrateCurves: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("migrated %d tracks, want 1", len(out))
	}
	if out[0].BoneID != "root" {
		t.Fatalf("merged track bone = %q, want root", out[0].BoneID)
	}
	if !reflect.DeepEqual(out[0].Samples, []float64{1, 2, 3}) {
		t.Fatalf("samples = %v, want [1 2 3]", out[0].Samples)
	}
	if out[0].Weight != 1 {
		t.Fatalf("migrated weight = %v, want 1", out[0].Weight)
	}
}

func TestMigrateCurvesMergesWeightsToUnity(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	src := curveBase()
	dst := impl.Skeleton{Root: "root", Bones: []impl.Bone{bone("root", "", 1)}}
	out, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "arm", Weight: 1, Samples: []float64{1, 3}},
		{BoneID: "hand", Weight: 3, Samples: []float64{5, 3}},
	})
	if err != nil {
		t.Fatalf("MigrateCurves: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("migrated %d tracks, want one merged track", len(out))
	}
	if out[0].BoneID != "root" {
		t.Fatalf("merged track bone = %q, want root", out[0].BoneID)
	}
	want := []float64{4, 3}
	if !reflect.DeepEqual(out[0].Samples, want) {
		t.Fatalf("weighted samples = %v, want %v", out[0].Samples, want)
	}
	if out[0].Weight != 1 {
		t.Fatalf("merged weight = %v, want 1 so the group sums to unity", out[0].Weight)
	}
}

func TestMigrateCurvesKeepsSurvivingTracks(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	src := curveBase()
	dst := impl.Skeleton{Root: "root", Bones: []impl.Bone{
		bone("root", "", 1),
		bone("arm", "root", 1),
	}}
	out, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "hand", Weight: 2, Samples: []float64{2, 4}},
		{BoneID: "arm", Weight: 1, Samples: []float64{1, 1}},
	})
	if err != nil {
		t.Fatalf("MigrateCurves: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("migrated %d tracks, want 2", len(out))
	}
	if out[0].BoneID != "arm" || !reflect.DeepEqual(out[0].Samples, []float64{1, 1}) {
		t.Fatalf("first track = %+v, want the surviving arm track", out[0])
	}
	if out[0].Weight != 1 {
		t.Fatalf("surviving track weight = %v, want 1", out[0].Weight)
	}
	if out[1].BoneID != "root" || !reflect.DeepEqual(out[1].Samples, []float64{2, 4}) {
		t.Fatalf("second track = %+v, want the hand track moved to root", out[1])
	}
}

func TestMigrateCurvesRejectsUnknownBone(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	src := curveBase()
	dst := impl.Skeleton{Root: "root", Bones: []impl.Bone{bone("root", "", 1)}}
	if _, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "ghost", Weight: 1, Samples: []float64{1}},
	}); !errors.Is(err, impl.ErrUnknownBone) {
		t.Fatalf("unknown track bone: got %v, want ErrUnknownBone", err)
	}
}

func TestMigrateCurvesRejectsBadTracks(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	src := curveBase()
	dst := impl.Skeleton{Root: "root", Bones: []impl.Bone{bone("root", "", 1)}}
	if _, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "arm", Weight: -1, Samples: []float64{1}},
	}); !errors.Is(err, impl.ErrBadTrack) {
		t.Fatalf("negative weight: got %v, want ErrBadTrack", err)
	}
	if _, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "arm", Weight: 1, Samples: nil},
	}); !errors.Is(err, impl.ErrBadTrack) {
		t.Fatalf("empty samples: got %v, want ErrBadTrack", err)
	}
	if _, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "arm", Weight: 1, Samples: []float64{1, 2}},
		{BoneID: "hand", Weight: 1, Samples: []float64{1}},
	}); !errors.Is(err, impl.ErrBadTrack) {
		t.Fatalf("mismatched sample lengths: got %v, want ErrBadTrack", err)
	}
	if _, err := r.MigrateCurves(src, dst, []impl.Track{
		{BoneID: "arm", Weight: 0, Samples: []float64{1}},
		{BoneID: "hand", Weight: 0, Samples: []float64{1}},
	}); !errors.Is(err, impl.ErrZeroWeight) {
		t.Fatalf("zero weight group: got %v, want ErrZeroWeight", err)
	}
}

func TestBuildLodIsDeterministic(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := lodBase()
	first, err := r.BuildLod(base, 1, 3)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	second, err := r.BuildLod(base, 1, 3)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated build differs:\nfirst  %+v\nsecond %+v", first, second)
	}
	fresh := impl.NewBoneLodChainBuilder()
	third, err := fresh.BuildLod(base, 1, 3)
	if err != nil {
		t.Fatalf("fresh build: %v", err)
	}
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("two builders produced different levels")
	}
	if got, _ := r.MapBone(1, "leaf_b"); got != "root" {
		t.Fatalf("MapBone after rebuild = %q, want root", got)
	}
}

func TestConcurrentBuildAndMap(t *testing.T) {
	r := impl.NewBoneLodChainBuilder()
	base := lodBase()
	baseline, err := r.BuildLod(base, 1, 3)
	if err != nil {
		t.Fatalf("baseline build: %v", err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				got, err := r.BuildLod(base, 1, 3)
				if err != nil {
					errCh <- err
					return
				}
				if !reflect.DeepEqual(got, baseline) {
					errCh <- errors.New("concurrent BuildLod returned a different skeleton")
					return
				}
				if merged, err := r.MapBone(1, "leaf_b"); err != nil || merged != "root" {
					errCh <- errors.New("concurrent MapBone lost the merge relation")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent build/map failed: %v", err)
	}
}
