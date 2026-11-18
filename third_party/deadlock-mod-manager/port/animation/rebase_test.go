// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_skeleton.rs
// tests at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func decodeSkeleton(t *testing.T, raw []byte) kv3.Value {
	t.Helper()
	_, value, _, err := camera.DecodeCompiled(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func maskWeights(t *testing.T, skeleton kv3.Value, index int) map[string]float64 {
	t.Helper()
	masks, ok := skeleton.Get("m_maskDefinitions")
	if !ok {
		t.Fatal("missing masks")
	}
	items, ok := masks.AsArray()
	if !ok || index >= len(items) {
		t.Fatalf("masks = %+v", items)
	}
	weights, ok := items[index].Get("m_primaryWeightList")
	if !ok {
		t.Fatal("missing primary weights")
	}
	names, ok := weights.Get("m_boneIDs")
	if !ok {
		t.Fatal("missing bone ids")
	}
	boneNames, _ := names.AsArray()
	values, ok := weights.Get("m_weights")
	if !ok {
		t.Fatal("missing weights")
	}
	numbers, ok := values.AsArray()
	if !ok || len(boneNames) != len(numbers) {
		t.Fatal("weight columns disagree")
	}
	result := make(map[string]float64, len(boneNames))
	for i := range boneNames {
		name, _ := boneNames[i].AsString()
		value, _ := numbers[i].AsFloat()
		result[name] = value
	}
	return result
}

func TestRebaseSkeletonPreservesAuthoredWeightsAndPoses(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{0.5, 0.5}))
	modPoses := []kv3.Value{identityPoseValue(),
		poseValue([3]float64{0.0005, 0, 0}, [4]float64{0, 0, 0, 1}, 1)}
	mod := resourceValue(t, withMasks(
		skeletonValue(t, []string{"root", "head"}, []int64{-1, 0}, modPoses, modPoses),
		maskValue("default", []string{"root", "head"}, []float64{1, 2})))
	if _, ok := RefreshSkeleton(mod, game, false); ok {
		t.Fatal("changed weights must need a rebase")
	}
	repaired, ok := RebaseSkeleton(mod, game, false)
	if !ok {
		t.Fatal("rebase declined")
	}
	value := decodeSkeleton(t, repaired)
	weights := maskWeights(t, value, 0)
	if weights["root"] != 1 || weights["head"] != 2 {
		t.Fatalf("weights = %v", weights)
	}
	poses, _ := value.Get("m_parentSpaceReferencePose")
	poseItems, _ := poses.AsArray()
	head, _ := poseItems[1].AsArray()
	if head[0].Double != 0.0005 {
		t.Fatalf("authored pose not preserved: %v", head)
	}
}

func TestRebaseSkeletonInheritsWeightsForGameAddedBones(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head", "extra"}, []int64{-1, 0, 1},
		maskValue("default", []string{"root", "head", "extra"}, []float64{0.5, 0.5, 3}))
	mod := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	if _, ok := RebaseSkeleton(mod, game, false); ok {
		t.Fatal("game-added bones need extension proof")
	}
	repaired, ok := RebaseSkeleton(mod, game, true)
	if !ok {
		t.Fatal("rebase declined")
	}
	weights := maskWeights(t, decodeSkeleton(t, repaired), 0)
	if weights["root"] != 1 || weights["head"] != 2 || weights["extra"] != 3 {
		t.Fatalf("weights = %v", weights)
	}
}

func TestRebaseSkeletonKeepsExtraMasks(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{0.5, 0.5}))
	mod := skeletonFixture(t, []string{"root", "head", "head_end"}, []int64{-1, 0, 1},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}),
		maskValue("helper", []string{"head_end"}, []float64{5}))
	repaired, ok := RebaseSkeleton(mod, game, false)
	if !ok {
		t.Fatal("rebase declined")
	}
	value := decodeSkeleton(t, repaired)
	masks, _ := value.Get("m_maskDefinitions")
	items, _ := masks.AsArray()
	if len(items) != 2 {
		t.Fatalf("masks = %d", len(items))
	}
	helper := maskWeights(t, value, 1)
	if len(helper) != 0 {
		t.Fatalf("obsolete helper weights = %v", helper)
	}
}

func TestRebaseSkeletonRejectsSecondaryWeightLists(t *testing.T) {
	game := skeletonFixture(t, []string{"root"}, []int64{-1},
		maskValue("default", []string{"root"}, []float64{0.5}))
	mod := skeletonFixture(t, []string{"root"}, []int64{-1},
		maskValue("default", []string{"root"}, []float64{1}))
	modValue := decodeSkeleton(t, mod)
	masks, _ := modValue.Get("m_maskDefinitions")
	items, _ := masks.AsArray()
	weights, _ := items[0].Get("m_primaryWeightList")
	secondary := kv3.Array(kv3.Object(kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(kv3.String("root"))}))
	items[0] = kv3.Object(
		kv3.Pair{Key: "m_ID", Value: kv3.String("default")},
		kv3.Pair{Key: "m_primaryWeightList", Value: *weights},
		kv3.Pair{Key: "m_secondaryWeightLists", Value: secondary},
	)
	if _, ok := RebaseSkeleton(resourceValue(t, modValue), game, false); ok {
		t.Fatal("secondary weight lists must be declined")
	}
}
