// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_clips/tests.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"strconv"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func clipArray(values ...float64) kv3.Value {
	items := make([]kv3.Value, len(values))
	for i := range values {
		items[i] = kv3.Double(values[i])
	}
	return kv3.Array(items...)
}

func clipPose(x float64) kv3.Value {
	return clipArray(x, 0, 0, 1, 0, 0, 0, 1)
}

func clipRange(x float64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_flRangeStart", Value: kv3.Double(x)},
		kv3.Pair{Key: "m_flRangeLength", Value: kv3.Double(0.1)},
	)
}

func clipTrack(x float64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_bIsRotationStatic", Value: kv3.Bool(true)},
		kv3.Pair{Key: "m_bIsTranslationStatic", Value: kv3.Bool(true)},
		kv3.Pair{Key: "m_bIsScaleStatic", Value: kv3.Bool(true)},
		kv3.Pair{Key: "m_constantRotation", Value: clipArray(0, 0, 0, 1)},
		kv3.Pair{Key: "m_nTrackReadOffset", Value: kv3.UInt(0)},
		kv3.Pair{Key: "m_translationRangeX", Value: clipRange(x)},
		kv3.Pair{Key: "m_translationRangeY", Value: clipRange(0)},
		kv3.Pair{Key: "m_translationRangeZ", Value: clipRange(0)},
		kv3.Pair{Key: "m_scaleRange", Value: clipRange(1)},
	)
}

func clipFixture(t *testing.T) (kv3.Value, kv3.Value, kv3.Value) {
	t.Helper()
	names := []string{"root_motion"}
	for i := range 12 {
		names = append(names, "bone_"+strconv.Itoa(i))
	}
	nameValues := make([]kv3.Value, len(names))
	positions := make([]kv3.Value, len(names))
	rotations := make([]kv3.Value, len(names))
	scales := make([]kv3.Value, len(names))
	parents := make([]kv3.Value, len(names))
	for i, name := range names {
		nameValues[i] = kv3.String(name)
		positions[i] = kv3.Array(kv3.Double(float64(i)), kv3.Double(0), kv3.Double(0))
		rotations[i] = clipArray(0, 0, 0, 1)
		scales[i] = kv3.Double(1)
		parents[i] = kv3.Int(-1)
		if i > 0 {
			parents[i] = kv3.Int(0)
		}
	}
	model := kv3.Object(kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
		kv3.Pair{Key: "m_boneName", Value: kv3.Array(nameValues...)},
		kv3.Pair{Key: "m_nParent", Value: kv3.Array(parents...)},
		kv3.Pair{Key: "m_bonePosParent", Value: kv3.Array(positions...)},
		kv3.Pair{Key: "m_boneRotParent", Value: kv3.Array(rotations...)},
		kv3.Pair{Key: "m_boneScaleParent", Value: kv3.Array(scales...)},
	)})
	current := append([]string{names[0], "inserted_helper"}, names[1:]...)
	currentNames := make([]kv3.Value, len(current))
	currentParents := make([]kv3.Value, len(current))
	currentPoses := make([]kv3.Value, len(current))
	for i, name := range current {
		currentNames[i] = kv3.String(name)
		parent := int64(-1)
		switch i {
		case 1:
			parent = 0
		default:
			if i > 1 {
				parent = 1
			}
		}
		currentParents[i] = kv3.Int(parent)
		pose := float64(0)
		if i > 1 {
			pose = float64(i - 1)
		}
		currentPoses[i] = clipPose(pose)
	}
	skeleton := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(currentNames...)},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(currentParents...)},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(currentPoses...)},
	)
	tracks := make([]kv3.Value, len(names))
	for i := range tracks {
		tracks[i] = clipTrack(float64(i))
	}
	clip := kv3.Object(
		kv3.Pair{Key: "m_trackCompressionSettings", Value: kv3.Array(tracks...)},
		kv3.Pair{Key: "m_nNumFrames", Value: kv3.UInt(2)},
		kv3.Pair{Key: "m_compressedPoseOffsets", Value: kv3.Array(kv3.UInt(0), kv3.UInt(0))},
		kv3.Pair{Key: "m_compressedPoseData", Value: kv3.Binary(nil)},
		kv3.Pair{Key: "m_bIsAdditive", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_secondaryAnimations", Value: kv3.Array()},
		kv3.Pair{Key: "m_modelSpaceBoneSamplingIndices", Value: kv3.Array()},
		kv3.Pair{Key: "m_modelSpaceSamplingChain", Value: kv3.Array()},
		kv3.Pair{Key: "m_floatChannelData", Value: kv3.Array()},
		kv3.Pair{Key: "m_events", Value: kv3.Array()},
	)
	return model, skeleton, clip
}

func TestMappingInsertsIdentityHelperTrack(t *testing.T) {
	model, skeleton, clip := clipFixture(t)
	tracksValue, _ := clip.Get("m_trackCompressionSettings")
	old, _ := tracksValue.AsArray()
	layout, ok := Mapping(&skeleton, &model, old)
	if !ok {
		t.Fatal("mapping declined")
	}
	if layout[1] != -1 {
		t.Fatalf("inserted helper should be unsampled: %v", layout)
	}
	for additive := range 2 {
		added := additive == 1
		if !replaceValueField(&clip, "m_bIsAdditive", kv3.Bool(added)) {
			t.Fatal("additive flag missing")
		}
		rebuilt, ok := ExpandClip(resourceValue(t, clip), &skeleton, layout)
		if !ok {
			t.Fatal("expand declined")
		}
		_, value, _, err := camera.DecodeCompiled(rebuilt)
		if err != nil {
			t.Fatal(err)
		}
		valuesValue, _ := value.Get("m_trackCompressionSettings")
		tracks, _ := valuesValue.AsArray()
		for i, oldIndex := range layout {
			if oldIndex != -1 && !camera.RuntimeValuesEqual(&tracks[i], &old[oldIndex]) {
				t.Fatalf("track %d changed", i)
			}
		}
		if words, ok := FrameWords(&value, tracks); !ok || words != 0 {
			t.Fatalf("frame words = %d", words)
		}
		scaleValue, _ := tracks[1].Get("m_scaleRange")
		startValue, _ := scaleValue.Get("m_flRangeStart")
		start, _ := startValue.AsFloat()
		expected := 1.0
		if added {
			expected = 0
		}
		if start != expected {
			t.Fatalf("inserted scale = %f", start)
		}
		for _, field := range []string{"m_compressedPoseData", "m_compressedPoseOffsets", "m_events"} {
			left, _ := value.Get(field)
			right, _ := clip.Get(field)
			if !camera.RuntimeValuesEqual(left, right) {
				t.Fatalf("%s changed", field)
			}
		}
	}
}

func TestMappingRefusesNonIdentityInsertedAncestor(t *testing.T) {
	model, skeleton, clip := clipFixture(t)
	posesValue, _ := skeleton.Get("m_parentSpaceReferencePose")
	poses, _ := posesValue.AsArray()
	poses[1] = clipPose(10)
	tracksValue, _ := clip.Get("m_trackCompressionSettings")
	tracks, _ := tracksValue.AsArray()
	if _, ok := Mapping(&skeleton, &model, tracks); ok {
		t.Fatal("non-identity inserted ancestor accepted")
	}
}

func TestMappingRefusesUnanchoredTracks(t *testing.T) {
	model, skeleton, clip := clipFixture(t)
	tracksValue, _ := clip.Get("m_trackCompressionSettings")
	tracks, _ := tracksValue.AsArray()
	for i := range tracks {
		replaceValueField(&tracks[i], "m_bIsTranslationStatic", kv3.Bool(false))
	}
	if _, ok := Mapping(&skeleton, &model, tracks); ok {
		t.Fatal("unanchored layout accepted")
	}
}

func TestFrameWordsRefusesTruncatedFramesAndBoneChannels(t *testing.T) {
	_, _, clip := clipFixture(t)
	tracksValue, _ := clip.Get("m_trackCompressionSettings")
	tracks, _ := tracksValue.AsArray()
	replaceValueField(&clip, "m_compressedPoseData", kv3.Binary([]byte{0, 1}))
	if _, ok := FrameWords(&clip, tracks); ok {
		t.Fatal("truncated frames accepted")
	}
	replaceValueField(&clip, "m_compressedPoseData", kv3.Binary(nil))
	replaceValueField(&clip, "m_modelSpaceBoneSamplingIndices",
		kv3.Array(kv3.UInt(1)))
	if _, ok := FrameWords(&clip, tracks); ok {
		t.Fatal("bone-indexed channel accepted")
	}
}
