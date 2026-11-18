package modcompat

import (
	"strconv"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const (
	clipSkeletonPath = "models/rig.vnmskel_c"
	clipPath         = "animations/old.vnmclip_c"
	clipModelPath    = "models/hero/hero.vmdl_c"
)

func clipNumbers(values ...float64) kv3.Value {
	items := make([]kv3.Value, len(values))
	for i := range values {
		items[i] = kv3.Double(values[i])
	}
	return kv3.Array(items...)
}

func clipRangeValue(start float64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_flRangeStart", Value: kv3.Double(start)},
		kv3.Pair{Key: "m_flRangeLength", Value: kv3.Double(0.1)},
	)
}

func clipTrackValue(x float64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_bIsRotationStatic", Value: kv3.Bool(true)},
		kv3.Pair{Key: "m_bIsTranslationStatic", Value: kv3.Bool(true)},
		kv3.Pair{Key: "m_bIsScaleStatic", Value: kv3.Bool(true)},
		kv3.Pair{Key: "m_constantRotation", Value: clipNumbers(0, 0, 0, 1)},
		kv3.Pair{Key: "m_nTrackReadOffset", Value: kv3.UInt(0)},
		kv3.Pair{Key: "m_translationRangeX", Value: clipRangeValue(x)},
		kv3.Pair{Key: "m_translationRangeY", Value: clipRangeValue(0)},
		kv3.Pair{Key: "m_translationRangeZ", Value: clipRangeValue(0)},
		kv3.Pair{Key: "m_scaleRange", Value: clipRangeValue(1)},
	)
}

func clipTextValue(t *testing.T, additive bool) []byte {
	t.Helper()
	tracks := make([]kv3.Value, 9)
	for i := range tracks {
		tracks[i] = clipTrackValue(float64(i))
	}
	clip := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.String("models/rig.vnmskel")},
		kv3.Pair{Key: "m_trackCompressionSettings", Value: kv3.Array(tracks...)},
		kv3.Pair{Key: "m_nNumFrames", Value: kv3.UInt(2)},
		kv3.Pair{Key: "m_compressedPoseOffsets", Value: kv3.Array(kv3.UInt(0), kv3.UInt(0))},
		kv3.Pair{Key: "m_compressedPoseData", Value: kv3.Binary(nil)},
		kv3.Pair{Key: "m_bIsAdditive", Value: kv3.Bool(additive)},
		kv3.Pair{Key: "m_secondaryAnimations", Value: kv3.Array()},
		kv3.Pair{Key: "m_modelSpaceBoneSamplingIndices", Value: kv3.Array()},
		kv3.Pair{Key: "m_modelSpaceSamplingChain", Value: kv3.Array()},
		kv3.Pair{Key: "m_floatChannelData", Value: kv3.Array()},
		kv3.Pair{Key: "m_events", Value: kv3.Array()},
	)
	return compiledBlock(t, &clip)
}

func clipModelBytes(t *testing.T) []byte {
	t.Helper()
	names := []string{"root_motion"}
	for i := range 8 {
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
		rotations[i] = clipNumbers(0, 0, 0, 1)
		scales[i] = kv3.Double(1)
		parents[i] = kv3.Int(-1)
		if i > 0 {
			parents[i] = kv3.Int(0)
		}
	}
	model := kv3.Object(
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.Array(nameValues...)},
			kv3.Pair{Key: "m_nParent", Value: kv3.Array(parents...)},
			kv3.Pair{Key: "m_bonePosParent", Value: kv3.Array(positions...)},
			kv3.Pair{Key: "m_boneRotParent", Value: kv3.Array(rotations...)},
			kv3.Pair{Key: "m_boneScaleParent", Value: kv3.Array(scales...)},
		)},
		kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: kv3.Array(kv3.String("models/rig.vnmskel"))},
		kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array()},
	)
	return compiledBlock(t, &model)
}

func clipSkeletonBytes(t *testing.T) []byte {
	t.Helper()
	names := []string{"root_motion", "inserted_helper"}
	parents := []int64{-1, 0}
	poses := []kv3.Value{contractPose(), contractPose()}
	for i := range 8 {
		names = append(names, "bone_"+strconv.Itoa(i))
		parents = append(parents, 1)
		poses = append(poses, clipPoseValue(float64(i+1)))
	}
	nameValues := make([]kv3.Value, len(names))
	parentValues := make([]kv3.Value, len(parents))
	for i := range names {
		nameValues[i] = kv3.String(names[i])
		parentValues[i] = kv3.Int(parents[i])
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(nameValues...)},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(parentValues...)},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(poses...)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(poses...)},
	)
	return compiledBlock(t, &value)
}

func clipPoseValue(x float64) kv3.Value {
	return clipNumbers(x, 0, 0, 1, 0, 0, 0, 1)
}

func clipFixture(t *testing.T, additive bool) (string, *source2.GameIndex) {
	t.Helper()
	mod := packMod(t, []vpk.PackEntry{
		{Path: clipModelPath, Data: clipModelBytes(t)},
		{Path: clipPath, Data: clipTextValue(t, additive)},
	})
	game := gameIndexWith(t, []vpk.PackEntry{
		{Path: clipSkeletonPath, Data: clipSkeletonBytes(t)},
	})
	return mod, game
}

func TestAnalyzeExpandsOldAnimationTracks(t *testing.T) {
	mod, game := clipFixture(t, false)
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRepair(analysis.Repairs, RepairAnimationClipTracks, clipPath) {
		t.Fatalf("repairs = %+v warnings = %+v", analysis.Repairs, analysis.AssetWarnings)
	}
	_, value, _, err := camera.DecodeCompiled(analysis.Fixed[clipPath])
	if err != nil {
		t.Fatal(err)
	}
	tracksValue, _ := value.Get("m_trackCompressionSettings")
	tracks, _ := tracksValue.AsArray()
	if len(tracks) != 10 {
		t.Fatalf("tracks = %d", len(tracks))
	}
	static, _ := tracks[1].Get("m_bIsRotationStatic")
	if value, _ := static.AsBool(); !value {
		t.Fatal("inserted track is not static")
	}
	scaleValue, _ := tracks[1].Get("m_scaleRange")
	startValue, _ := scaleValue.Get("m_flRangeStart")
	if start, _ := startValue.AsFloat(); start != 1 {
		t.Fatalf("inserted scale = %v", start)
	}
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningAnimationMapping {
			t.Fatalf("unexpected mapping warning: %+v", warning)
		}
	}
}

func TestAnalyzeWarnsWhenClipLayoutCannotBeInferred(t *testing.T) {
	mod, game := clipFixture(t, true)
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Fixed) != 0 {
		t.Fatalf("fixed = %v", analysis.Fixed)
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningAnimationMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
	mappings := 0
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningAnimationMapping {
			mappings++
		}
	}
	if mappings != 1 {
		t.Fatalf("mapping warnings = %d: %+v", mappings, analysis.AssetWarnings)
	}
}

func TestAnalyzeDefersToAnotherModsClip(t *testing.T) {
	mod, game := clipFixture(t, false)
	scope := Scope{OtherAnimationPaths: map[string]struct{}{clipPath: {}}}
	analysis, err := Analyze("mod", []string{mod}, game, scope, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if hasRepair(analysis.Repairs, RepairAnimationClipTracks, clipPath) {
		t.Fatalf("repairs = %+v", analysis.Repairs)
	}
}
