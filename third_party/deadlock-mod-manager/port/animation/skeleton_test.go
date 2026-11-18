// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_skeleton.rs
// tests at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"bytes"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func withMasks(skeleton kv3.Value, masks ...kv3.Value) kv3.Value {
	skeleton.Object = append(skeleton.Object, kv3.Pair{
		Key: "m_maskDefinitions", Value: kv3.Array(masks...),
	})
	return skeleton
}

func maskValue(id string, bones []string, weights []float64) kv3.Value {
	names := make([]kv3.Value, len(bones))
	values := make([]kv3.Value, len(weights))
	for i := range bones {
		names[i] = kv3.String(bones[i])
		values[i] = kv3.Double(weights[i])
	}
	return kv3.Object(
		kv3.Pair{Key: "m_ID", Value: kv3.String(id)},
		kv3.Pair{Key: "m_primaryWeightList", Value: kv3.Object(
			kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(names...)},
			kv3.Pair{Key: "m_weights", Value: kv3.Array(values...)},
		)},
		kv3.Pair{Key: "m_secondaryWeightLists", Value: kv3.Array()},
	)
}

func skeletonFixture(t *testing.T, names []string, parents []int64, masks ...kv3.Value) []byte {
	t.Helper()
	poses := make([]kv3.Value, len(names))
	for i := range poses {
		poses[i] = identityPoseValue()
	}
	return resourceValue(t, withMasks(
		skeletonValue(t, names, parents, poses, poses), masks...))
}

func TestRefreshSkeletonAcceptsReorderedMasks(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"head", "root"}, []float64{2, 1}),
		maskValue("upper", []string{"head"}, []float64{1}))
	mod := skeletonFixture(t, []string{"head", "root"}, []int64{1, -1},
		maskValue("upper", []string{"head"}, []float64{1}),
		maskValue("DEFAULT", []string{"root", "head"}, []float64{1, 2}))
	fixed, ok := RefreshSkeleton(mod, game, false)
	if !ok {
		t.Fatal("reordered masks should describe the same rig")
	}
	if !bytes.Equal(fixed, game) {
		t.Fatal("the current game skeleton should replace the mod bytes")
	}
}

func TestRefreshSkeletonRejectsChangedMaskWeights(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	mod := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 3}))
	if _, ok := RefreshSkeleton(mod, game, false); ok {
		t.Fatal("changed mask weights must not be refreshed")
	}
}

func TestRefreshSkeletonAcceptsGameExtension(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head", "extra"}, []int64{-1, 0, 1},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	mod := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	if _, ok := RefreshSkeleton(mod, game, true); !ok {
		t.Fatal("a game-added leaf bone is a safe extension")
	}
	if _, ok := RefreshSkeleton(mod, game, false); ok {
		t.Fatal("extension bones need proof when animations override the rig")
	}
}

func TestRefreshSkeletonRejectsChangedReferencePose(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	modPoses := []kv3.Value{identityPoseValue(), identityPoseValue()}
	modPoses[1] = poseValue([3]float64{0.01, 0, 0}, [4]float64{0, 0, 0, 1}, 1)
	mod := resourceValue(t, withMasks(
		skeletonValue(t, []string{"root", "head"}, []int64{-1, 0}, modPoses, modPoses),
		maskValue("default", []string{"root", "head"}, []float64{1, 2})))
	if _, ok := RefreshSkeleton(mod, game, false); ok {
		t.Fatal("a changed reference pose must not be refreshed")
	}
}

func TestRefreshSkeletonAcceptsRemovedTerminalBone(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	mod := skeletonFixture(t, []string{"root", "head", "head_end"}, []int64{-1, 0, 1},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	if _, ok := RefreshSkeleton(mod, game, false); !ok {
		t.Fatal("a removed terminal helper does not change the rig")
	}
}

func TestRefreshSkeletonRejectsRemovedBranch(t *testing.T) {
	game := skeletonFixture(t, []string{"root"}, []int64{-1},
		maskValue("default", []string{"root"}, []float64{1}))
	mod := skeletonFixture(t, []string{"root", "head", "head_end"}, []int64{-1, 0, 1},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	if _, ok := RefreshSkeleton(mod, game, false); ok {
		t.Fatal("removing a hierarchy joint must fail closed")
	}
}

func TestRefreshSkeletonAcceptsAddedWorldAttachment(t *testing.T) {
	game := skeletonFixture(t, []string{"root", "head", "attachWorld"}, []int64{-1, 0, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	mod := skeletonFixture(t, []string{"root", "head"}, []int64{-1, 0},
		maskValue("default", []string{"root", "head"}, []float64{1, 2}))
	if _, ok := RefreshSkeleton(mod, game, false); !ok {
		t.Fatal("attachWorld is a known non-deforming root attachment")
	}
}

func TestRefreshSkeletonRejectsChangedMetadata(t *testing.T) {
	game := skeletonFixture(t, []string{"root"}, []int64{-1},
		maskValue("default", []string{"root"}, []float64{1}))
	mod := skeletonFixture(t, []string{"root"}, []int64{-1},
		maskValue("default", []string{"root"}, []float64{1}))
	gameValue := mustDecodeSkeleton(t, game)
	setObjectField(&gameValue, "m_customMetadata", kv3.Int(2))
	modValue := mustDecodeSkeleton(t, mod)
	setObjectField(&modValue, "m_customMetadata", kv3.Int(1))
	if _, ok := RefreshSkeleton(resourceValue(t, modValue), resourceValue(t, gameValue), false); ok {
		t.Fatal("custom skeleton metadata must match exactly")
	}
}

func mustDecodeSkeleton(t *testing.T, raw []byte) kv3.Value {
	t.Helper()
	_, value, _, err := camera.DecodeCompiled(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
