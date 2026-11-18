// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation.rs,
// model_animation/scaled_rig.rs and animation_poses.rs tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

type testSource struct {
	files map[string][]byte
	mods  map[string]bool
}

func (s testSource) Resolve(path string) ([]byte, error) {
	return s.files[path], nil
}

func (s testSource) FromMod(path string) bool {
	return s.mods[path]
}

func resourceValue(t *testing.T, value kv3.Value) []byte {
	t.Helper()
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
	}, nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func poseValue(position [3]float64, rotation [4]float64, scaleValue float64) kv3.Value {
	return kv3.Array(
		kv3.Double(position[0]), kv3.Double(position[1]), kv3.Double(position[2]),
		kv3.Double(scaleValue),
		kv3.Double(rotation[0]), kv3.Double(rotation[1]),
		kv3.Double(rotation[2]), kv3.Double(rotation[3]),
	)
}

func identityPoseValue() kv3.Value {
	return poseValue([3]float64{}, [4]float64{0, 0, 0, 1}, 1)
}

func modelValue(t *testing.T, bones []string, skeletonRefs []string, graph string) kv3.Value {
	t.Helper()
	parents := make([]kv3.Value, len(bones))
	positions := make([]kv3.Value, len(bones))
	rotations := make([]kv3.Value, len(bones))
	scales := make([]kv3.Value, len(bones))
	for i := range bones {
		parents[i] = kv3.Int(-1)
		if i > 0 {
			parents[i] = kv3.Int(0)
		}
		positions[i] = kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0))
		rotations[i] = kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1))
		scales[i] = kv3.Double(1)
	}
	names := make([]kv3.Value, len(bones))
	for i, name := range bones {
		names[i] = kv3.String(name)
	}
	refs := make([]kv3.Value, len(skeletonRefs))
	for i, ref := range skeletonRefs {
		refs[i] = kv3.String(ref)
	}
	var graphs kv3.Value
	if graph == "" {
		graphs = kv3.Array()
	} else {
		graphs = kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("default")},
			kv3.Pair{Key: "m_hGraph", Value: kv3.String(graph)},
		))
	}
	return kv3.Object(
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.Array(names...)},
			kv3.Pair{Key: "m_nParent", Value: kv3.Array(parents...)},
			kv3.Pair{Key: "m_bonePosParent", Value: kv3.Array(positions...)},
			kv3.Pair{Key: "m_boneRotParent", Value: kv3.Array(rotations...)},
			kv3.Pair{Key: "m_boneScaleParent", Value: kv3.Array(scales...)},
		)},
		kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: kv3.Array(refs...)},
		kv3.Pair{Key: "m_animGraph2Refs", Value: graphs},
	)
}

func skeletonValue(t *testing.T, names []string, parents []int64, local, world []kv3.Value) kv3.Value {
	t.Helper()
	nameValues := make([]kv3.Value, len(names))
	for i, name := range names {
		nameValues[i] = kv3.String(name)
	}
	parentValues := make([]kv3.Value, len(parents))
	for i, parent := range parents {
		parentValues[i] = kv3.Int(parent)
	}
	return kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(nameValues...)},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(parentValues...)},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(local...)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(world...)},
	)
}

func proofFixture(t *testing.T) (*kv3.Value, *kv3.Value, testSource) {
	t.Helper()
	old := modelValue(t, []string{"root", "head"},
		[]string{"models/rig.vnmskel"}, "animgraphs/current.vnmgraph")
	current := modelValue(t, []string{"root", "head"},
		[]string{"models/rig.vnmskel"}, "animgraphs/current.vnmgraph")
	skeleton := skeletonValue(t,
		[]string{"head"}, []int64{-1},
		[]kv3.Value{identityPoseValue()}, []kv3.Value{identityPoseValue()})
	source := testSource{
		files: map[string][]byte{
			"models/rig.vnmskel":          resourceValue(t, skeleton),
			"animgraphs/current.vnmgraph": []byte("available graph"),
		},
		mods: map[string]bool{},
	}
	return &old, &current, source
}

func TestProveCurrentBindingsAcceptsMatchingModels(t *testing.T) {
	old, current, source := proofFixture(t)
	if err := ProveCurrentBindings(old, current, false, source); err != nil {
		t.Fatal(err)
	}
}

func TestProveCurrentBindingsRejectsBlockedAnimationInputs(t *testing.T) {
	old, current, source := proofFixture(t)
	if err := ProveCurrentBindings(old, current, true, source); err == nil {
		t.Fatal("expected blocked animation inputs to fail")
	}
}

func TestProveCurrentBindingsRejectsChangedGraphs(t *testing.T) {
	old, _, source := proofFixture(t)
	changed := modelValue(t, []string{"root", "head"},
		[]string{"models/rig.vnmskel"}, "animgraphs/other.vnmgraph")
	if err := ProveCurrentBindings(old, &changed, false, source); err == nil {
		t.Fatal("expected changed graph bindings to fail")
	}
}

func TestProveCurrentBindingsRejectsOverriddenGameResources(t *testing.T) {
	old, current, source := proofFixture(t)
	source.mods["models/rig.vnmskel"] = true
	if err := ProveCurrentBindings(old, current, false, source); err == nil {
		t.Fatal("expected mod skeleton override to fail")
	}
	source.mods["models/rig.vnmskel"] = false
	source.mods["animgraphs/current.vnmgraph"] = true
	if err := ProveCurrentBindings(old, current, false, source); err == nil {
		t.Fatal("expected mod graph override to fail")
	}
}

func setBonePosition(t *testing.T, model *kv3.Value, bone string, position [3]float64) {
	t.Helper()
	skeleton, ok := model.Get("m_modelSkeleton")
	if !ok {
		t.Fatal("missing skeleton")
	}
	namesValue, ok := skeleton.Get("m_boneName")
	if !ok {
		t.Fatal("missing bone names")
	}
	names, ok := namesValue.AsArray()
	if !ok {
		t.Fatal("invalid bone names")
	}
	index := -1
	for i := range names {
		name, _ := names[i].AsString()
		if name == bone {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("missing bone %s", bone)
	}
	positionsValue, ok := skeleton.Get("m_bonePosParent")
	if !ok {
		t.Fatal("missing bone positions")
	}
	positions, ok := positionsValue.AsArray()
	if !ok || index >= len(positions) {
		t.Fatal("invalid bone positions")
	}
	positions[index] = kv3.Array(
		kv3.Double(position[0]), kv3.Double(position[1]), kv3.Double(position[2]),
	)
}

func TestProveCurrentBindingsRejectsChangedPose(t *testing.T) {
	old, current, source := proofFixture(t)
	setBonePosition(t, current, "head", [3]float64{5, 0, 0})
	if err := ProveCurrentBindings(old, current, false, source); err == nil {
		t.Fatal("expected changed sampled pose to fail")
	}
}

func TestSelfConsistentRejectsBrokenTransform(t *testing.T) {
	bad := skeletonValue(t,
		[]string{"root", "head"}, []int64{-1, 0},
		[]kv3.Value{identityPoseValue(), identityPoseValue()},
		[]kv3.Value{identityPoseValue(), poseValue([3]float64{3, 0, 0}, [4]float64{0, 0, 0, 1}, 1)})
	if consistent, known := SelfConsistent(&bad); !known || consistent {
		t.Fatalf("expected inconsistent transform, got known=%v consistent=%v", known, consistent)
	}
}

func TestUnmappedReferenceControlsSkipsUnmappedBranches(t *testing.T) {
	skeleton := skeletonValue(t,
		[]string{"spine", "head", "tail"}, []int64{-1, 0, 0},
		[]kv3.Value{identityPoseValue(), identityPoseValue(), identityPoseValue()},
		[]kv3.Value{identityPoseValue(), identityPoseValue(), identityPoseValue()})
	unused, ok := unmappedReferenceControls(&skeleton,
		map[string]string{"spine": "spine", "head": "head"},
		map[string]string{"spine": "spine", "head": "head"})
	if !ok {
		t.Fatal("expected valid skeleton")
	}
	if _, present := unused["tail"]; !present {
		t.Fatalf("expected tail to be unused, got %v", unused)
	}
}

func TestScaledPosesCompatible(t *testing.T) {
	pose := func(x float64) kv3.Value {
		return poseValue([3]float64{x, 0, 0}, [4]float64{0, 0, 0, 1}, 1)
	}
	pairs := []Pose{
		{Name: "one", Old: pose(10.08), Current: pose(10.0)},
		{Name: "two", Old: pose(20.16), Current: pose(20.0)},
	}
	if !ScaledPosesCompatible(pairs) {
		t.Fatal("expected uniform scale to be compatible")
	}
	nonuniform := append([]Pose(nil), pairs...)
	nonuniform[1].Old = pose(20.0)
	if ScaledPosesCompatible(nonuniform) {
		t.Fatal("expected non-uniform scale to be rejected")
	}
	large := []Pose{{Name: "one", Old: pose(10.3), Current: pose(10.0)}}
	if ScaledPosesCompatible(large) {
		t.Fatal("expected large scale change to be rejected")
	}
	rotated := append([]Pose(nil), pairs...)
	rotated[0].Old = poseValue([3]float64{10.08, 0, 0}, [4]float64{0.1, 0, 0, 1}, 1)
	if ScaledPosesCompatible(rotated) {
		t.Fatal("expected changed rotation to be rejected")
	}
}

func TestGraphsRejectsDuplicateIdentifiers(t *testing.T) {
	model := modelValue(t, []string{"root"}, []string{"models/rig.vnmskel"}, "")
	duplicate := kv3.Object(
		kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array(
			kv3.Object(
				kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("x")},
				kv3.Pair{Key: "m_hGraph", Value: kv3.String("animgraphs/a.vnmgraph")},
			),
			kv3.Object(
				kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("X")},
				kv3.Pair{Key: "m_hGraph", Value: kv3.String("animgraphs/b.vnmgraph")},
			),
		)},
	)
	if _, ok := Graphs(&duplicate); ok {
		t.Fatal("expected duplicate identifiers to be rejected")
	}
	if _, ok := Graphs(&model); ok {
		t.Fatal("expected empty graph list to be rejected")
	}
	valid := kv3.Object(
		kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array(
			kv3.Object(
				kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("Default")},
				kv3.Pair{Key: "m_hGraph", Value: kv3.String("AnimGraphs\\A.vnmgraph")},
			),
		)},
	)
	got, ok := Graphs(&valid)
	if !ok || got["default"] != "animgraphs/a.vnmgraph" {
		t.Fatalf("unexpected graphs: %v %v", got, ok)
	}
}

func TestLegacyAnimationGraphRejectsEscapedNames(t *testing.T) {
	build := func(text string) kv3.Value {
		return kv3.Object(kv3.Pair{Key: "m_modelInfo", Value: kv3.Object(
			kv3.Pair{Key: "m_keyValueText", Value: kv3.String(text)},
		)})
	}
	model := build("{ anim_graph_resource = resource:\"animgraphs/old.vanmgrph\" }\n")
	graph, ok := LegacyAnimationGraph(&model)
	if !ok || graph != "animgraphs/old.vanmgrph" {
		t.Fatalf("unexpected legacy graph: %q %v", graph, ok)
	}
	escaped := build("{ anim_graph_resource = resource:\"animgraphs/old\\\\name.vanmgrph\" }\n")
	if _, ok := LegacyAnimationGraph(&escaped); ok {
		t.Fatal("expected escaped graph name to be rejected")
	}
}
