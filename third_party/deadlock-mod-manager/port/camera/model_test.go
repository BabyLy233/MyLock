// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager model_camera.rs tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

func TestPartialCameraInterfaceRestoresSettings(t *testing.T) {
	oldText := `{ AttachmentCameraData = [ { attachment_name = "near_00" } ] Custom = { value = 7 } }`
	baseText := `{ CitadelCameraSettings_t = { m_flCameraHeightStanding = 80.0 m_vCameraParrotOffset = [ -10.0, -10.0, 10.0 ] } AttachmentCameraData = [] }`
	sourceModel := modelValue(oldText)
	baseModel := modelValue(baseText)
	source := container(sourceModel)
	base := container(baseModel)

	repaired, kind, ok := RepairModelCamera(source, base, nil)
	if !ok || kind != RepairInterface {
		t.Fatalf("repair = %v, kind = %s", ok, kind)
	}
	_, original, _, err := DecodeCompiled(source)
	if err != nil {
		t.Fatal(err)
	}
	_, updated, _, err := DecodeCompiled(repaired)
	if err != nil {
		t.Fatal(err)
	}
	originalSkeleton, _ := original.Get("m_modelSkeleton")
	updatedSkeleton, _ := updated.Get("m_modelSkeleton")
	if !RuntimeValuesEqual(originalSkeleton, updatedSkeleton) {
		t.Fatal("skeleton changed")
	}
	text, ok := FindCameraText(&updated)
	if !ok {
		t.Fatal("repaired text missing")
	}
	_, fields, ok := ParseRootFields(text)
	if !ok {
		t.Fatal("repaired text does not parse")
	}
	_, oldFields, _ := ParseRootFields(oldText)
	for _, field := range []string{"AttachmentCameraData", "Custom"} {
		if text[fields[field].Start:fields[field].End] != oldText[oldFields[field].Start:oldFields[field].End] {
			t.Fatalf("field %s changed", field)
		}
	}
	if _, present := fields["CitadelCameraSettings_t"]; !present {
		t.Fatal("CitadelCameraSettings_t not inserted")
	}
	if _, _, ok := RepairModelCamera(repaired, base, nil); ok {
		t.Fatal("repair is not idempotent")
	}
}

func TestMissingCameraPointsAreCopiedFromTheGameModel(t *testing.T) {
	text := `{ CitadelCameraSettings_t = { offset = 80 } AttachmentCameraData = [ { attachment_name = "near_00" } ] }`
	sourceModel := modelValue(text)
	baseModel := modelValue(text)
	sourceMesh := mesh(point("custom_mount", "root_motion", [3]float64{9, 8, 7}, identity))
	baseMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
		point("near_00", "root_motion", [3]float64{2, 3, 4}, identity),
	)
	source := container(sourceModel, sourceMesh)
	base := container(baseModel, baseMesh)

	repaired, kind, ok := RepairModelCamera(source, base, nil)
	if !ok || kind != RepairInterface {
		t.Fatalf("repair = %v, kind = %s", ok, kind)
	}
	_, custom := findAttachment(t, repaired, 1, "custom_mount")
	if got := attachmentOffset(t, custom, 0); got != [3]float64{9, 8, 7} {
		t.Fatalf("authored attachment changed: %v", got)
	}
	_, near := findAttachment(t, repaired, 1, "near_00")
	if got := attachmentOffset(t, near, 0); got != [3]float64{2, 3, 4} {
		t.Fatalf("copied near_00 offset = %v", got)
	}
	res, _ := resource.Parse(repaired)
	raw, _ := res.GetBlockByIndex(1)
	meshValue, err := kv3.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	points, _ := fieldArray(&meshValue, "m_attachments")
	keys := make(map[string]int)
	for i := range points {
		keyV, _ := points[i].Get("key")
		key, _ := keyV.AsString()
		keys[key]++
	}
	for _, name := range []string{"custom_mount", "standing_pivot", "crouching_pivot", "near_00"} {
		if keys[name] != 1 {
			t.Fatalf("point %s count = %d", name, keys[name])
		}
	}
}

func TestAnalyzeReportsRepairableFrame(t *testing.T) {
	source := container(modelValue("{}"), mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, oldFrame),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, oldFrame),
	))
	base := container(modelValue("{}"), mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
	))
	analysis := Analyze(source, base, ModelOptions{})
	if !analysis.Repairable {
		t.Fatalf("analysis = %+v", analysis)
	}
	found := false
	for _, kind := range analysis.RepairKinds {
		if kind == RepairAttachmentFrame {
			found = true
		}
	}
	if !found {
		t.Fatalf("repair kinds = %v", analysis.RepairKinds)
	}
}

func TestNumericCameraSettingsDeclineResourceAndBoneSchemas(t *testing.T) {
	if !numericSettings("Camera = { offset = -32.0 vector = [ 1, 2, 3 ] }") {
		t.Fatal("numeric settings declined")
	}
	for _, value := range []string{
		`resource:"models/camera.vmdl"`,
		`"head"`,
		"NaN",
		"[ inf, 0 ]",
		"{}",
		"[]",
	} {
		if numericSettings("Camera = { value = " + value + " }") {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestCameraDataNamesComeFromTheInterface(t *testing.T) {
	text := `{ AttachmentCameraData = [ { attachment_name = "alternate_form_camera" fov = 90 } { attachment_name = "near_02" fov = 75 } ] }`
	names, ok := CameraDataNames(text)
	if !ok || len(names) != 2 {
		t.Fatalf("names = %v, %v", names, ok)
	}
	if _, ok := names["near_02"]; !ok {
		t.Fatalf("near_02 missing: %v", names)
	}
	if _, ok := CameraDataNames(`{ AttachmentCameraData = [] }`); ok {
		t.Fatal("empty camera data accepted")
	}
	if _, ok := CameraDataNames(`{ AttachmentCameraData = [ { attachment_name = "bad\\name" } ] }`); ok {
		t.Fatal("bad name accepted")
	}
	if !strings.Contains(text, "alternate_form_camera") {
		t.Fatal("test fixture broken")
	}
}

func rigModel(names []string, parents []int64, rootPosition [3]float64) kv3.Value {
	skeletonValue := skeleton(names, parents)
	for i := range skeletonValue.Object {
		if skeletonValue.Object[i].Key != "m_bonePosParent" {
			continue
		}
		positions := append([]kv3.Value(nil), skeletonValue.Object[i].Value.Array...)
		positions[0] = numbers(rootPosition[0], rootPosition[1], rootPosition[2])
		skeletonValue.Object[i].Value = kv3.Array(positions...)
	}
	return kv3.Object(kv3.Pair{Key: "m_modelSkeleton", Value: skeletonValue})
}

func TestCompatibleNamedCameraBonesTrustsSampledBones(t *testing.T) {
	authored := rigModel([]string{"root", "head"}, []int64{-1, 0}, [3]float64{1, 0, 0})
	current := rigModel([]string{"root", "head"}, []int64{-1, 0}, [3]float64{0, 0, 0})
	head := map[string]struct{}{"head": {}}
	both := map[string]struct{}{"head": {}, "root": {}}

	if ok, valid := CompatibleNamedCameraBones(&authored, &current, head, nil); !valid || ok {
		t.Fatal("an unsampled pose difference was accepted")
	}
	if ok, valid := CompatibleNamedCameraBones(&authored, &current, head, head); !valid || !ok {
		t.Fatal("a sampled head did not satisfy its own chain")
	}
	// A sampled head must not exempt an unsampled shared root.
	if ok, _ := CompatibleNamedCameraBones(&authored, &current, both, head); ok {
		t.Fatal("an unsampled shared ancestor was exempted")
	}
	if ok, _ := CompatibleNamedCameraBones(&authored, &current, both, both); !ok {
		t.Fatal("a fully sampled chain was rejected")
	}
}

func TestScaledPoseCameraDependencyAcceptsAUniformResize(t *testing.T) {
	authored := rigModel([]string{"root", "head"}, []int64{-1, 0}, [3]float64{1, 0, 0})
	current := rigModel([]string{"root", "head"}, []int64{-1, 0}, [3]float64{0.99, 0, 0})
	required := map[string]struct{}{"head": {}}
	if ok, _ := CompatibleNamedCameraBones(&authored, &current, required, nil); ok {
		t.Fatal("a plain pose difference was accepted")
	}
	oldRig, ok := rig.Parse(&authored)
	if !ok {
		t.Fatal("authored rig does not parse")
	}
	currentRig, ok := rig.Parse(&current)
	if !ok {
		t.Fatal("current rig does not parse")
	}
	oldPose, _ := oldRig.Pose("root")
	currentPose, _ := currentRig.Pose("root")
	if !rig.ScaledPosesCompatible([][2]kv3.Value{{oldPose, currentPose}}) {
		t.Fatal("the scaled rig evidence was rejected")
	}
}
