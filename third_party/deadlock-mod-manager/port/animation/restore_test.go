// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation.rs,
// model_animation/sampled_controls.rs and model_animation/metadata.rs tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"bytes"
	"slices"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

var emptyRERL = []byte{8, 0, 0, 0, 0, 0, 0, 0}

func restoreResource(t *testing.T, value kv3.Value) []byte {
	t.Helper()
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		{Kind: resource.BlockTypeRERL, Payload: emptyRERL},
	}, nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func skeletonPart(names []string, parents []int64) []kv3.Pair {
	nameValues := make([]kv3.Value, len(names))
	parentValues := make([]kv3.Value, len(parents))
	positions := make([]kv3.Value, len(names))
	rotations := make([]kv3.Value, len(names))
	scales := make([]kv3.Value, len(names))
	spheres := make([]kv3.Value, len(names))
	flags := make([]kv3.Value, len(names))
	for i := range names {
		nameValues[i] = kv3.String(names[i])
		parentValues[i] = kv3.Int(parents[i])
		positions[i] = kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0))
		rotations[i] = kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1))
		scales[i] = kv3.Double(1)
		spheres[i] = kv3.Double(0)
		flags[i] = kv3.Int(1)
	}
	return []kv3.Pair{
		{Key: "m_boneName", Value: kv3.Array(nameValues...)},
		{Key: "m_nParent", Value: kv3.Array(parentValues...)},
		{Key: "m_bonePosParent", Value: kv3.Array(positions...)},
		{Key: "m_boneRotParent", Value: kv3.Array(rotations...)},
		{Key: "m_boneScaleParent", Value: kv3.Array(scales...)},
		{Key: "m_boneSphere", Value: kv3.Array(spheres...)},
		{Key: "m_nFlag", Value: kv3.Array(flags...)},
	}
}

func animationModel(t *testing.T, names []string, parents []int64, bindings bool) kv3.Value {
	t.Helper()
	pairs := []kv3.Pair{
		{Key: "authoredAppearance", Value: kv3.String("keep")},
		{Key: "m_modelSkeleton", Value: kv3.Object(skeletonPart(names, parents)...)},
		{Key: "m_refMeshes", Value: kv3.Array()},
	}
	if bindings {
		pairs = append(pairs,
			kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: kv3.Array(kv3.String("models/rig.vnmskel"))},
			kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array(kv3.Object(
				kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("default")},
				kv3.Pair{Key: "m_hGraph", Value: kv3.String("animgraphs/current.vnmgraph")},
			))},
		)
	}
	return kv3.Object(pairs...)
}

func restoreSkeleton(t *testing.T, names []string, parents []int64) []byte {
	t.Helper()
	nameValues := make([]kv3.Value, len(names))
	parentValues := make([]kv3.Value, len(parents))
	poses := make([]kv3.Value, len(names))
	for i := range names {
		nameValues[i] = kv3.String(names[i])
		parentValues[i] = kv3.Int(parents[i])
		poses[i] = identityPoseValue()
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(nameValues...)},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(parentValues...)},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(poses...)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(poses...)},
	)
	return restoreResource(t, value)
}

func resolveAvailable(t *testing.T, files map[string][]byte) (testSource, func(string) bool) {
	t.Helper()
	source := testSource{files: files, mods: map[string]bool{}}
	return source, func(name string) bool { return files[name] != nil }
}

func TestRestoreBindingsCopiesCurrentAnimationFields(t *testing.T) {
	old := animationModel(t, []string{"root", "head"}, []int64{-1, 0}, false)
	current := animationModel(t, []string{"root", "head"}, []int64{-1, 0}, true)
	skeleton := restoreSkeleton(t, []string{"head"}, []int64{-1})
	source, available := resolveAvailable(t, map[string][]byte{
		"models/rig.vnmskel":          skeleton,
		"animgraphs/current.vnmgraph": []byte("available graph"),
	})
	repaired, ok := RestoreBindings(restoreResource(t, old), restoreResource(t, current), source, available)
	if !ok {
		t.Fatal("bindings were not restored")
	}
	_, actual, _, err := camera.DecodeCompiled(repaired)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range bindingFields {
		want, _ := current.Get(field)
		got, ok := actual.Get(field)
		if !ok || !kv3.Equal(got, want) {
			t.Fatalf("%s was not copied", field)
		}
	}
	authored, _ := actual.Get("authoredAppearance")
	if value, _ := authored.AsString(); value != "keep" {
		t.Fatalf("authored field changed: %q", value)
	}
	references, err := resource.ReferencesOf(repaired)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(references))
	for _, reference := range references {
		paths = append(paths, reference.Path)
	}
	for _, want := range []string{"models/rig.vnmskel", "animgraphs/current.vnmgraph"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("missing reference %s in %v", want, paths)
		}
	}
}

func TestRestoreBindingsAppendsMissingSampledControls(t *testing.T) {
	old := animationModel(t, []string{"root", "head"}, []int64{-1, 0}, false)
	current := animationModel(t, []string{"root", "head", "control"}, []int64{-1, 0, 0}, true)
	skeleton := restoreSkeleton(t, []string{"head", "control"}, []int64{-1, 0})
	source, available := resolveAvailable(t, map[string][]byte{
		"models/rig.vnmskel":          skeleton,
		"animgraphs/current.vnmgraph": []byte("available graph"),
	})
	repaired, ok := RestoreBindings(restoreResource(t, old), restoreResource(t, current), source, available)
	if !ok {
		t.Fatal("bindings were not restored")
	}
	_, actual, _, err := camera.DecodeCompiled(repaired)
	if err != nil {
		t.Fatal(err)
	}
	rigValue, _ := actual.Get("m_modelSkeleton")
	names, _ := rigValue.Get("m_boneName")
	items, _ := names.AsArray()
	if len(items) != 3 {
		t.Fatalf("bone count = %d", len(items))
	}
	if name, _ := items[2].AsString(); name != "control" {
		t.Fatalf("appended bone = %q", name)
	}
	parents, _ := rigValue.Get("m_nParent")
	parentValues, _ := parents.AsArray()
	if parent, ok := parentValues[2].AsInt(); !ok || parent != 0 {
		t.Fatalf("appended parent = %v", parentValues[2])
	}
}

func TestRestoreBindingsDeclinesOverriddenSkeleton(t *testing.T) {
	old := animationModel(t, []string{"root", "head"}, []int64{-1, 0}, false)
	current := animationModel(t, []string{"root", "head"}, []int64{-1, 0}, true)
	skeleton := restoreSkeleton(t, []string{"head"}, []int64{-1})
	source, available := resolveAvailable(t, map[string][]byte{
		"models/rig.vnmskel":          skeleton,
		"animgraphs/current.vnmgraph": []byte("available graph"),
	})
	source.mods["models/rig.vnmskel"] = true
	if _, ok := RestoreBindings(restoreResource(t, old), restoreResource(t, current), source, available); ok {
		t.Fatal("restored bindings from an overridden skeleton")
	}
}

func TestMetadataNullableSelectorsDoNotPreventBindingActivation(t *testing.T) {
	old := "{ CCitadelHeroModelGameData_t = { m_sAG2UIAnimGraph = null } }"
	current := "{ CCitadelHeroModelGameData_t = { m_bTurnToFaceVelocity = false } }"
	repaired, ok := restoreMetadata(old, current, nil, func(string) bool { return false })
	if !ok {
		t.Fatal("metadata restore failed")
	}
	if bytes.Contains([]byte(repaired), []byte("m_sAG2UIAnimGraph")) ||
		!bytes.Contains([]byte(repaired), []byte("m_bTurnToFaceVelocity = false")) {
		t.Fatalf("repaired = %s", repaired)
	}
}

func TestMetadataRetiresSelectorsAndInheritsControls(t *testing.T) {
	old := "{ anim_graph_resource = resource:\"old.vanmgrph\" CCitadelHeroModelGameData_t = { m_UIAnimgraph = \"old_ui.vanmgrph\" m_flTurnDuration = 1.0 m_hAmbientParticle = \"custom.vpcf\" } custom = { anim_graph_resource = \"preserved\" } }"
	current := "{ CCitadelHeroModelGameData_t = { m_flTurnDuration = 0.5 m_bTurnToFaceVelocity = false } }"
	repaired, ok := restoreMetadata(old, current, nil, func(string) bool { return false })
	if !ok {
		t.Fatal("metadata restore failed")
	}
	for _, unwanted := range []string{"old.vanmgrph", "old_ui.vanmgrph"} {
		if bytes.Contains([]byte(repaired), []byte(unwanted)) {
			t.Fatalf("selector %s survived: %s", unwanted, repaired)
		}
	}
	for _, wanted := range []string{"m_bTurnToFaceVelocity = false", "m_flTurnDuration = 1.0", "custom.vpcf", "anim_graph_resource = \"preserved\""} {
		if !bytes.Contains([]byte(repaired), []byte(wanted)) {
			t.Fatalf("%s missing: %s", wanted, repaired)
		}
	}
	again, ok := restoreMetadata(repaired, current, nil, func(string) bool { return false })
	if !ok || again != repaired {
		t.Fatalf("metadata restore is not idempotent: %s", again)
	}
	for _, path := range []string{"old.vanmgrph", "old_ui.vanmgrph"} {
		available := func(name string) bool { return name == path }
		if _, ok := restoreMetadata(old, current, nil, available); ok {
			t.Fatalf("available retired selector %s was removed", path)
		}
	}
}

func TestMetadataMigratesOnlyMatchingModernSelectors(t *testing.T) {
	old := "{ CCitadelHeroModelGameData_t = { m_sAG2UIAnimGraph = null m_sAG2HeroPawnAnimGraph = resource:\"hero.vnmgraph\" } }"
	current := "{ CCitadelHeroModelGameData_t = { m_bTurnToFaceVelocity = false } }"
	graphs := map[string]string{"": "hero.vnmgraph"}
	repaired, ok := restoreMetadata(old, current, graphs, func(string) bool { return true })
	if !ok {
		t.Fatal("metadata restore failed")
	}
	if bytes.Contains([]byte(repaired), []byte("m_sAG2HeroPawnAnimGraph")) ||
		!bytes.Contains([]byte(repaired), []byte("m_bTurnToFaceVelocity = false")) {
		t.Fatalf("repaired = %s", repaired)
	}
	if _, ok := restoreMetadata(old, current, map[string]string{"ui": "hero.vnmgraph"}, func(string) bool { return true }); ok {
		t.Fatal("mismatched modern graph was retired")
	}
	if _, ok := restoreMetadata(old, current, map[string]string{"": "different.vnmgraph"}, func(string) bool { return true }); ok {
		t.Fatal("different modern graph was retired")
	}
}
