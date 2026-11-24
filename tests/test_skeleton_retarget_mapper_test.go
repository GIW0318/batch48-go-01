package task

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	impl "gsb/batch48-go-game-b-18"
)

func baseSkeletons() (impl.Skeleton, impl.Skeleton) {
	src := impl.Skeleton{
		Root: "src_root",
		Bones: []impl.Bone{
			{
				Name: "src_root", Parent: "", Role: "root", Length: 2,
				RestPos: impl.Vec3{X: 0, Y: 0, Z: 0}, RestRot: impl.Vec3{X: 0, Y: 0, Z: 0},
				RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
			},
			{
				Name: "Bip01_Spine", Parent: "src_root", Role: "", Length: 2,
				RestPos: impl.Vec3{X: 0, Y: 1, Z: 0}, RestRot: impl.Vec3{X: 10, Y: 0, Z: 0},
				RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
			},
		},
	}
	dst := impl.Skeleton{
		Root: "dst_root",
		Bones: []impl.Bone{
			{
				Name: "dst_root", Parent: "", Role: "root", Length: 4,
				RestPos: impl.Vec3{X: 0, Y: 0, Z: 0}, RestRot: impl.Vec3{X: 0, Y: 0, Z: 0},
				RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
			},
			{
				Name: "spine", Parent: "dst_root", Role: "", Length: 1,
				RestPos: impl.Vec3{X: 0, Y: 2, Z: 0}, RestRot: impl.Vec3{X: 5, Y: 0, Z: 0},
				RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
			},
		},
	}
	return src, dst
}

func buildBase(t *testing.T) *impl.SkeletonRetargeter {
	t.Helper()
	src, dst := baseSkeletons()
	r := impl.NewSkeletonRetargeter(64)
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap on the base skeletons: %v", err)
	}
	return r
}

func mappingFor(t *testing.T, r *impl.SkeletonRetargeter, source string) impl.Mapping {
	t.Helper()
	for _, m := range r.Mappings() {
		if m.Source == source {
			return m
		}
	}
	t.Fatalf("no mapping registered for source bone %q", source)
	return impl.Mapping{}
}

func TestBuildMapMatchesByRoleFirst(t *testing.T) {
	src := impl.Skeleton{
		Root: "root_a",
		Bones: []impl.Bone{
			{Name: "root_a", Parent: "", Role: "root", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}},
			{Name: "SrcArm", Parent: "root_a", Role: "upper_arm_l", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}},
		},
	}
	dst := impl.Skeleton{
		Root: "root_b",
		Bones: []impl.Bone{
			{Name: "root_b", Parent: "", Role: "root", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}},
			{Name: "DstUpperArm", Parent: "root_b", Role: "upper_arm_l", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}},
		},
	}
	r := impl.NewSkeletonRetargeter(16)
	bm, err := r.BuildMap(src, dst)
	if err != nil {
		t.Fatalf("BuildMap: %v", err)
	}
	if len(bm.Pairs) != 2 {
		t.Fatalf("pairs = %d, want 2", len(bm.Pairs))
	}
	arm := mappingFor(t, r, "SrcArm")
	if arm.Target != "DstUpperArm" {
		t.Fatalf("SrcArm target = %q, want DstUpperArm", arm.Target)
	}
	if arm.MatchedBy != "role" {
		t.Fatalf("SrcArm matched by %q, want role", arm.MatchedBy)
	}
	root := mappingFor(t, r, "root_a")
	if root.Target != "root_b" || root.MatchedBy != "role" {
		t.Fatalf("root mapping = %+v, want root_b matched by role", root)
	}
}

func TestBuildMapFallsBackToNormalizedName(t *testing.T) {
	src, dst := baseSkeletons()
	r := impl.NewSkeletonRetargeter(16)
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap: %v", err)
	}
	spine := mappingFor(t, r, "Bip01_Spine")
	if spine.Target != "spine" {
		t.Fatalf("spine target = %q, want spine", spine.Target)
	}
	if spine.MatchedBy != "name" {
		t.Fatalf("spine matched by %q, want name", spine.MatchedBy)
	}
	if got := r.Normalize("Bip01_Spine"); got != "spine" {
		t.Fatalf("Normalize(Bip01_Spine) = %q, want spine", got)
	}
	if got := r.Normalize("mixamorig:Hips"); got != "hips" {
		t.Fatalf("Normalize(mixamorig:Hips) = %q, want hips", got)
	}
}

func TestBuildMapRejectsMalformedSkeletons(t *testing.T) {
	src, dst := baseSkeletons()
	mutate := func(f func(*impl.Skeleton)) (impl.Skeleton, impl.Skeleton) {
		s := src
		d := dst
		copyS := append([]impl.Bone(nil), src.Bones...)
		copyD := append([]impl.Bone(nil), dst.Bones...)
		s.Bones = copyS
		d.Bones = copyD
		f(&s)
		return s, d
	}

	cases := []struct {
		name string
		src  impl.Skeleton
		dst  impl.Skeleton
	}{
		{name: "empty source", src: impl.Skeleton{}, dst: dst},
		{name: "empty target", src: src, dst: impl.Skeleton{}},
		{name: "root not in bones", src: impl.Skeleton{Root: "ghost", Bones: append([]impl.Bone(nil), src.Bones...)}, dst: dst},
		{name: "duplicate bone", src: func() impl.Skeleton {
			s, _ := mutate(func(s *impl.Skeleton) {
				s.Bones = append(s.Bones, impl.Bone{Name: "Bip01_Spine", Parent: "src_root", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}})
			})
			return s
		}(), dst: dst},
		{name: "unknown parent", src: func() impl.Skeleton {
			s, _ := mutate(func(s *impl.Skeleton) { s.Bones[1].Parent = "ghost" })
			return s
		}(), dst: dst},
		{name: "cycle", src: func() impl.Skeleton {
			s, _ := mutate(func(s *impl.Skeleton) { s.Bones[0].Parent = "Bip01_Spine" })
			return s
		}(), dst: dst},
		{name: "zero length", src: func() impl.Skeleton {
			s, _ := mutate(func(s *impl.Skeleton) { s.Bones[1].Length = 0 })
			return s
		}(), dst: dst},
		{name: "zero rest scale", src: func() impl.Skeleton {
			s, _ := mutate(func(s *impl.Skeleton) { s.Bones[1].RestScale = impl.Vec3{X: 0, Y: 1, Z: 1} })
			return s
		}(), dst: dst},
	}

	for _, tc := range cases {
		r := impl.NewSkeletonRetargeter(16)
		if _, err := r.BuildMap(tc.src, tc.dst); !errors.Is(err, impl.ErrBadSkeleton) {
			t.Fatalf("%s: got %v, want ErrBadSkeleton", tc.name, err)
		}
		if _, ok := r.Map(); ok {
			t.Fatalf("%s: failed BuildMap left a map behind", tc.name)
		}
	}
}

func TestBuildMapRegistersMissingBones(t *testing.T) {
	src, dst := baseSkeletons()
	src.Bones = append(src.Bones, impl.Bone{
		Name: "tail", Parent: "Bip01_Spine", Role: "", Length: 1,
		RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
	})
	r := impl.NewSkeletonRetargeter(16)
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap: %v", err)
	}
	fb, ok := r.Missing("tail")
	if !ok {
		t.Fatalf("tail not registered as missing")
	}
	if fb.Source != "tail" || fb.Strategy != "parent" {
		t.Fatalf("tail fallback = %+v, want source tail strategy parent", fb)
	}
	if _, ok := r.Missing("Bip01_Spine"); ok {
		t.Fatalf("mapped bone reported as missing")
	}
	if _, ok := r.Missing("ghost"); ok {
		t.Fatalf("unknown bone reported as missing")
	}
}

func TestBuildMapRegistersOneToManyConflicts(t *testing.T) {
	src, dst := baseSkeletons()
	src.Bones = append(src.Bones, impl.Bone{
		Name: "SrcArm", Parent: "src_root", Role: "arm", Length: 1,
		RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
	})
	dst.Bones = append(dst.Bones,
		impl.Bone{Name: "DstArmA", Parent: "dst_root", Role: "arm", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}},
		impl.Bone{Name: "DstArmB", Parent: "dst_root", Role: "arm", Length: 1, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1}},
	)
	r := impl.NewSkeletonRetargeter(16)
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap: %v", err)
	}
	conflicts := r.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflicts))
	}
	if conflicts[0].Source != "SrcArm" {
		t.Fatalf("conflict source = %q, want SrcArm", conflicts[0].Source)
	}
	want := []string{"DstArmA", "DstArmB"}
	if !reflect.DeepEqual(conflicts[0].Targets, want) {
		t.Fatalf("conflict targets = %v, want %v", conflicts[0].Targets, want)
	}
	for _, m := range r.Mappings() {
		if m.Source == "SrcArm" {
			t.Fatalf("ambiguous source still produced mapping %+v", m)
		}
	}
	if got := len(r.Mappings()); got != 2 {
		t.Fatalf("pairs = %d, want only the two unambiguous bones", got)
	}
}

func TestRetargetRequiresBuiltMap(t *testing.T) {
	r := impl.NewSkeletonRetargeter(16)
	if _, ok := r.Map(); ok {
		t.Fatalf("fresh retargeter already has a map")
	}
	if _, err := r.Retarget(impl.Pose{}); !errors.Is(err, impl.ErrNoMap) {
		t.Fatalf("Retarget without map: got %v, want ErrNoMap", err)
	}
}

func TestRetargetRotationUsesRestDifference(t *testing.T) {
	r := buildBase(t)
	pose := impl.Pose{Rot: map[string]impl.Vec3{
		"src_root":    {X: 0, Y: 0, Z: 0},
		"Bip01_Spine": {X: 40, Y: 0, Z: 0},
	}}
	out, err := r.Retarget(pose)
	if err != nil {
		t.Fatalf("Retarget: %v", err)
	}
	got, ok := out.Rot["spine"]
	if !ok {
		t.Fatalf("retargeted pose has no rotation for spine")
	}
	if got.X != 35 {
		t.Fatalf("spine rotation X = %v, want 35 (5 + (40 - 10))", got.X)
	}
	if got.Y != 0 || got.Z != 0 {
		t.Fatalf("spine rotation = %+v, want only X", got)
	}
	if root := out.Rot["dst_root"]; root.X != 0 {
		t.Fatalf("root rotation X = %v, want 0", root.X)
	}
	if len(out.Fallback) != 0 {
		t.Fatalf("fully mapped pose reported fallbacks: %v", out.Fallback)
	}
}

func TestSetRestDeltaShiftsRotation(t *testing.T) {
	r := buildBase(t)
	if err := r.SetRestDelta(impl.Vec3{X: 0, Y: 10, Z: 0}); err != nil {
		t.Fatalf("SetRestDelta: %v", err)
	}
	if got := r.RestDelta(); got.Y != 10 {
		t.Fatalf("RestDelta Y = %v, want 10", got.Y)
	}
	out, err := r.Retarget(impl.Pose{Rot: map[string]impl.Vec3{
		"Bip01_Spine": {X: 40, Y: 0, Z: 0},
	}})
	if err != nil {
		t.Fatalf("Retarget: %v", err)
	}
	spine := out.Rot["spine"]
	if spine.X != 35 || spine.Y != 10 || spine.Z != 0 {
		t.Fatalf("spine rotation = %+v, want {35 10 0}", spine)
	}
	if err := r.SetRestDelta(impl.Vec3{X: 200, Y: 0, Z: 0}); !errors.Is(err, impl.ErrBadRestDelta) {
		t.Fatalf("out of range rest delta: got %v, want ErrBadRestDelta", err)
	}
	if err := r.SetRestDelta(impl.Vec3{X: math.NaN(), Y: 0, Z: 0}); !errors.Is(err, impl.ErrBadRestDelta) {
		t.Fatalf("NaN rest delta: got %v, want ErrBadRestDelta", err)
	}
	if got := r.RestDelta(); got.Y != 10 || got.X != 0 {
		t.Fatalf("rejected rest delta changed state: %+v", got)
	}
}

func TestRetargetScaleUsesBoneLengthRatio(t *testing.T) {
	r := buildBase(t)
	out, err := r.Retarget(impl.Pose{Scale: map[string]impl.Vec3{
		"Bip01_Spine": {X: 1.5, Y: 1.5, Z: 1.5},
		"src_root":    {X: 1, Y: 1, Z: 1},
	}})
	if err != nil {
		t.Fatalf("Retarget: %v", err)
	}
	spine := out.Scale["spine"]
	if spine.X != 0.75 || spine.Y != 0.75 || spine.Z != 0.75 {
		t.Fatalf("spine scale = %+v, want 0.75 on every component", spine)
	}
	root := out.Scale["dst_root"]
	if root.X != 2 {
		t.Fatalf("root scale X = %v, want 2 (4/2 bone length ratio)", root.X)
	}
}

func TestRetargetPositionUsesRootLengthRatio(t *testing.T) {
	r := buildBase(t)
	out, err := r.Retarget(impl.Pose{Pos: map[string]impl.Vec3{
		"src_root":    {X: 3, Y: 6, Z: 0},
		"Bip01_Spine": {X: 0, Y: 1, Z: 0},
	}})
	if err != nil {
		t.Fatalf("Retarget: %v", err)
	}
	root := out.Pos["dst_root"]
	if root.X != 6 || root.Y != 12 || root.Z != 0 {
		t.Fatalf("root position = %+v, want {6 12 0}", root)
	}
	spine := out.Pos["spine"]
	if spine.Y != 2 {
		t.Fatalf("spine position Y = %v, want rest Y 2 with no delta", spine.Y)
	}
}

func TestUnmappedTargetInheritsParentAndIsMarked(t *testing.T) {
	src, dst := baseSkeletons()
	dst.Bones = append(dst.Bones, impl.Bone{
		Name: "dst_helm", Parent: "spine", Role: "", Length: 1,
		RestPos: impl.Vec3{X: 0, Y: 3, Z: 0}, RestScale: impl.Vec3{X: 1, Y: 1, Z: 1},
	})
	r := impl.NewSkeletonRetargeter(16)
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap: %v", err)
	}
	out, err := r.Retarget(impl.Pose{Rot: map[string]impl.Vec3{
		"Bip01_Spine": {X: 40, Y: 0, Z: 0},
	}})
	if err != nil {
		t.Fatalf("Retarget: %v", err)
	}
	if got := out.Rot["dst_helm"]; got.X != out.Rot["spine"].X || got.X != 35 {
		t.Fatalf("helm rotation = %+v, want it to inherit the spine rotation 35", got)
	}
	if got := out.Pos["dst_helm"]; got.Y != out.Pos["spine"].Y || got.Y != 2 {
		t.Fatalf("helm position = %+v, want it to inherit the spine position", got)
	}
	if len(out.Fallback) != 1 || out.Fallback[0] != "dst_helm" {
		t.Fatalf("fallback list = %v, want [dst_helm]", out.Fallback)
	}
}

func TestRetargetIsDeterministic(t *testing.T) {
	r := buildBase(t)
	pose := impl.Pose{
		Rot: map[string]impl.Vec3{
			"src_root":    {X: 1, Y: 2, Z: 3},
			"Bip01_Spine": {X: 40, Y: 5, Z: 6},
		},
		Pos: map[string]impl.Vec3{
			"src_root": {X: 1, Y: 2, Z: 3},
		},
		Scale: map[string]impl.Vec3{
			"Bip01_Spine": {X: 2, Y: 2, Z: 2},
		},
	}
	first, err := r.Retarget(pose)
	if err != nil {
		t.Fatalf("first Retarget: %v", err)
	}
	second, err := r.Retarget(pose)
	if err != nil {
		t.Fatalf("second Retarget: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated Retarget differs:\nfirst  %+v\nsecond %+v", first, second)
	}
	if !reflect.DeepEqual(r.Mappings(), r.Mappings()) {
		t.Fatalf("Mappings is not stable across calls")
	}
	fresh := impl.NewSkeletonRetargeter(64)
	src, dst := baseSkeletons()
	if _, err := fresh.BuildMap(src, dst); err != nil {
		t.Fatalf("fresh BuildMap: %v", err)
	}
	if !reflect.DeepEqual(r.Mappings(), fresh.Mappings()) {
		t.Fatalf("two builders produced different mappings")
	}
}

func TestMappingCapacityIsEnforced(t *testing.T) {
	src, dst := baseSkeletons()
	r := impl.NewSkeletonRetargeter(64)
	if err := r.SetMaxMappings(1); err != nil {
		t.Fatalf("SetMaxMappings(1): %v", err)
	}
	if _, err := r.BuildMap(src, dst); !errors.Is(err, impl.ErrMappingCapacity) {
		t.Fatalf("map over capacity: got %v, want ErrMappingCapacity", err)
	}
	if _, ok := r.Map(); ok {
		t.Fatalf("failed capacity check stored a partial map")
	}
	if got := len(r.Mappings()); got != 0 {
		t.Fatalf("mappings after rejected build = %d, want 0", got)
	}
	if err := r.SetMaxMappings(0); !errors.Is(err, impl.ErrBadMaxMappings) {
		t.Fatalf("zero mapping limit: got %v, want ErrBadMaxMappings", err)
	}
	if err := r.SetMaxMappings(2); err != nil {
		t.Fatalf("SetMaxMappings(2): %v", err)
	}
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap within capacity: %v", err)
	}
	if got := len(r.Mappings()); got != 2 {
		t.Fatalf("mappings = %d, want 2", got)
	}
}

func TestMissingLookupAndReset(t *testing.T) {
	r := buildBase(t)
	if _, ok := r.Missing("ghost"); ok {
		t.Fatalf("unknown bone reported as missing")
	}
	if err := r.SetRestDelta(impl.Vec3{X: 3, Y: 0, Z: 0}); err != nil {
		t.Fatalf("SetRestDelta: %v", err)
	}
	r.Reset()
	if _, ok := r.Map(); ok {
		t.Fatalf("Reset kept the bone map")
	}
	if got := len(r.Mappings()); got != 0 {
		t.Fatalf("mappings after Reset = %d, want 0", got)
	}
	if got := r.RestDelta(); got.X != 0 {
		t.Fatalf("rest delta after Reset = %+v, want zero", got)
	}
	if _, err := r.Retarget(impl.Pose{}); !errors.Is(err, impl.ErrNoMap) {
		t.Fatalf("Retarget after Reset: got %v, want ErrNoMap", err)
	}
	src, dst := baseSkeletons()
	if _, err := r.BuildMap(src, dst); err != nil {
		t.Fatalf("BuildMap after Reset: %v", err)
	}
	if got := len(r.Mappings()); got != 2 {
		t.Fatalf("mappings after rebuild = %d, want 2", got)
	}
}

func TestConcurrentRetargetIsConsistent(t *testing.T) {
	r := buildBase(t)
	pose := impl.Pose{
		Rot: map[string]impl.Vec3{
			"src_root":    {X: 1, Y: 0, Z: 0},
			"Bip01_Spine": {X: 40, Y: 0, Z: 0},
		},
		Scale: map[string]impl.Vec3{
			"Bip01_Spine": {X: 1.5, Y: 1.5, Z: 1.5},
		},
	}
	baseline, err := r.Retarget(pose)
	if err != nil {
		t.Fatalf("baseline Retarget: %v", err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got, err := r.Retarget(pose)
				if err != nil {
					errCh <- err
					return
				}
				if !reflect.DeepEqual(got, baseline) {
					errCh <- errors.New("concurrent Retarget returned a different pose")
					return
				}
				_ = r.Mappings()
				_, _ = r.Missing("Bip01_Spine")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent retarget failed: %v", err)
	}
}
