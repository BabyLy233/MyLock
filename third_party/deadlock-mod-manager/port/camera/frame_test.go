// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager camera tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"errors"
	"math"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

var dataKind = [4]byte{'D', 'A', 'T', 'A'}

func array(values ...kv3.Value) kv3.Value {
	return kv3.Value{Kind: kv3.KindArray, Array: values}
}

func numbers(values ...float64) kv3.Value {
	items := make([]kv3.Value, len(values))
	for i, v := range values {
		items[i] = kv3.Double(v)
	}
	return array(items...)
}

func point(name, parent string, offset [3]float64, q [4]float64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "key", Value: kv3.String(name)},
		kv3.Pair{Key: "value", Value: kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String(name)},
			kv3.Pair{Key: "m_influenceWeights", Value: numbers(1, 0, 0)},
			kv3.Pair{Key: "m_nInfluences", Value: kv3.Int(1)},
			kv3.Pair{Key: "m_influenceNames", Value: array(kv3.String(parent), kv3.String(""), kv3.String(""))},
			kv3.Pair{Key: "m_bInfluenceRootTransform", Value: array(kv3.Bool(false), kv3.Bool(false), kv3.Bool(false))},
			kv3.Pair{Key: "m_vInfluenceOffsets", Value: array(numbers(offset[0], offset[1], offset[2]), numbers(0, 0, 0), numbers(0, 0, 0))},
			kv3.Pair{Key: "m_vInfluenceRotations", Value: array(numbers(q[0], q[1], q[2], q[3]), numbers(0, 0, 0, 1), numbers(0, 0, 0, 1))},
		)},
	)
}

func mesh(attachments ...kv3.Value) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_attachments", Value: array(attachments...)},
		kv3.Pair{Key: "mesh_data", Value: kv3.Binary([]byte{1, 2, 3, 4})},
	)
}

func skeleton(names []string, parents []int64) kv3.Value {
	boneNames := make([]kv3.Value, len(names))
	parentValues := make([]kv3.Value, len(names))
	positions := make([]kv3.Value, len(names))
	rotations := make([]kv3.Value, len(names))
	scales := make([]kv3.Value, len(names))
	for i := range names {
		boneNames[i] = kv3.String(names[i])
		parentValues[i] = kv3.Int(parents[i])
		positions[i] = numbers(0, 0, 0)
		rotations[i] = numbers(0, 0, 0, 1)
		scales[i] = kv3.Double(1)
	}
	return kv3.Object(
		kv3.Pair{Key: "m_boneName", Value: array(boneNames...)},
		kv3.Pair{Key: "m_nParent", Value: array(parentValues...)},
		kv3.Pair{Key: "m_bonePosParent", Value: array(positions...)},
		kv3.Pair{Key: "m_boneRotParent", Value: array(rotations...)},
		kv3.Pair{Key: "m_boneScaleParent", Value: array(scales...)},
	)
}

func modelValue(text string) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_modelInfo", Value: kv3.Object(
			kv3.Pair{Key: "m_keyValueText", Value: kv3.String(text)},
		)},
		kv3.Pair{Key: "m_modelSkeleton", Value: skeleton([]string{"root_motion"}, []int64{-1})},
	)
}

func container(model kv3.Value, meshes ...kv3.Value) []byte {
	format := kv3.Format{}
	blocks := []resource.BuildResult{{Kind: dataKind, Payload: kv3.Encode(&model, format)}}
	for i := range meshes {
		blocks = append(blocks, resource.BuildResult{Kind: mdat, Payload: kv3.Encode(&meshes[i], format)})
	}
	out, err := resource.Build(blocks, nil, 0)
	if err != nil {
		panic(err)
	}
	return out
}

func findAttachment(t *testing.T, raw []byte, blockIndex int, name string) (kv3.Value, kv3.Value) {
	t.Helper()
	res, err := resource.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	block, ok := res.GetBlockByIndex(blockIndex)
	if !ok {
		t.Fatal("missing block")
	}
	meshValue, err := kv3.Decode(block)
	if err != nil {
		t.Fatal(err)
	}
	points, _ := fieldArray(&meshValue, "m_attachments")
	for i := range points {
		keyV, _ := points[i].Get("key")
		key, _ := keyV.AsString()
		if key == name {
			value, _ := points[i].Get("value")
			return meshValue, *value
		}
	}
	t.Fatalf("attachment %s missing", name)
	return kv3.Value{}, kv3.Value{}
}

func attachmentOffset(t *testing.T, value kv3.Value, index int) [3]float64 {
	t.Helper()
	offsets, ok := fieldArray(&value, "m_vInfluenceOffsets")
	if !ok || index >= len(offsets) {
		t.Fatal("missing offsets")
	}
	vec, ok := vectorN(&offsets[index], 3)
	if !ok {
		t.Fatal("bad offset vector")
	}
	return [3]float64{vec[0], vec[1], vec[2]}
}

func attachmentRotationOf(t *testing.T, value kv3.Value) [4]float64 {
	t.Helper()
	rotations, ok := fieldArray(&value, "m_vInfluenceRotations")
	if !ok || len(rotations) == 0 {
		t.Fatal("missing rotations")
	}
	vec, ok := vectorN(&rotations[0], 4)
	if !ok {
		t.Fatal("bad rotation")
	}
	return [4]float64{vec[0], vec[1], vec[2], vec[3]}
}

func TestFrameRepairMigratesWholeMesh(t *testing.T) {
	sourceMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, oldFrame),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, oldFrame),
		point("custom_fx", "root_motion", [3]float64{2, 3, 4}, oldFrame),
		point("aim", "head", [3]float64{7, 8, 9}, oldFrame),
	)
	baseMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
	)
	source := container(modelValue("{}"), sourceMesh)
	base := container(modelValue("{}"), baseMesh)

	if needs, err := PlanFrame(source, base); err != nil || !needs {
		t.Fatalf("PlanFrame = %v, %v", needs, err)
	}
	out, changed, err := RepairFrame(source, base)
	if err != nil || !changed {
		t.Fatalf("RepairFrame = %v, %v", changed, err)
	}
	_, custom := findAttachment(t, out, 1, "custom_fx")
	want := convert(oldFrame)
	if got := attachmentRotationOf(t, custom); math.Abs(got[0]-want[0]) > 1e-6 || math.Abs(got[3]-want[3]) > 1e-6 {
		t.Fatalf("custom rotation = %v, want %v", got, want)
	}
	if got := attachmentOffset(t, custom, 0); got != [3]float64{4, 2, 3} {
		t.Fatalf("custom offset = %v, want [4 2 3]", got)
	}
	_, aim := findAttachment(t, out, 1, "aim")
	if got := attachmentRotationOf(t, aim); got != oldFrame {
		t.Fatalf("non-root attachment rotation changed: %v", got)
	}
	if got := attachmentOffset(t, aim, 0); got != [3]float64{7, 8, 9} {
		t.Fatalf("non-root attachment offset changed: %v", got)
	}

	res, _ := resource.Parse(out)
	data, _ := res.FindBlock(dataKind)
	orig, _ := resource.Parse(source)
	origData, _ := orig.FindBlock(dataKind)
	if string(data) != string(origData) {
		t.Fatal("DATA block changed during frame repair")
	}
}

func TestFrameRepairMigratesOnlyStaleAttachments(t *testing.T) {
	sourceMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
		point("near_00", "root_motion", [3]float64{2, 3, 4}, oldFrame),
		point("custom_old", "root_motion", [3]float64{5, 6, 7}, oldFrame),
	)
	baseMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
		point("near_00", "root_motion", [3]float64{2, 3, 4}, identity),
	)
	text := `{ AttachmentCameraData = [ { attachment_name = "near_00" } ] }`
	source := container(modelValue(text), sourceMesh)
	base := container(modelValue(text), baseMesh)

	out, changed, err := RepairFrame(source, base)
	if err != nil || !changed {
		t.Fatalf("RepairFrame = %v, %v", changed, err)
	}
	_, near := findAttachment(t, out, 1, "near_00")
	if got := attachmentRotationOf(t, near); got != identity {
		t.Fatalf("near_00 rotation = %v, want identity", got)
	}
	if got := attachmentOffset(t, near, 0); got != [3]float64{4, 2, 3} {
		t.Fatalf("near_00 offset = %v", got)
	}
	_, custom := findAttachment(t, out, 1, "custom_old")
	if got := attachmentRotationOf(t, custom); got != oldFrame {
		t.Fatalf("custom_old should stay in the old frame: %v", got)
	}
	_, pivot := findAttachment(t, out, 1, "standing_pivot")
	if got := attachmentRotationOf(t, pivot); got != identity {
		t.Fatalf("pivot changed: %v", got)
	}
}

func TestFrameRepairDeclinesMixedFrames(t *testing.T) {
	sourceMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, oldFrame),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
	)
	baseMesh := mesh(
		point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
		point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
	)
	source := container(modelValue("{}"), sourceMesh)
	base := container(modelValue("{}"), baseMesh)
	if _, err := PlanFrame(source, base); !errors.Is(err, ErrMixedFrame) {
		t.Fatalf("PlanFrame error = %v, want ErrMixedFrame", err)
	}
	if _, _, err := RepairFrame(source, base); !errors.Is(err, ErrMixedFrame) {
		t.Fatalf("RepairFrame error = %v, want ErrMixedFrame", err)
	}
}

func TestFrameRepairRequiresMatchingRootAxes(t *testing.T) {
	sourceModel := modelValue("{}")
	baseModel := modelValue("{}")
	baseRig, _ := baseModel.Get("m_modelSkeleton")
	scales, _ := baseRig.Get("m_boneScaleParent")
	scales.Array[0] = kv3.Double(2)
	source := container(sourceModel,
		mesh(
			point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, oldFrame),
			point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, oldFrame),
		))
	base := container(baseModel,
		mesh(
			point("standing_pivot", "root_motion", [3]float64{1, 2, 3}, identity),
			point("crouching_pivot", "root_motion", [3]float64{4, 5, 6}, identity),
		))
	if _, _, err := RepairFrame(source, base); err == nil {
		t.Fatal("root scale mismatch was repaired")
	}
}
