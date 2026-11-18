// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation/rig_rebase.rs,
// bind_frames.rs and preservation.rs fixtures at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

const rebaseCameraText = `{ AttachmentCameraData = [ { attachment_name = "cam_point" } ] }`

func rebasePose(position [3]float64) kv3.Value {
	return kv3.Array(
		kv3.Double(position[0]), kv3.Double(position[1]), kv3.Double(position[2]),
		kv3.Double(1),
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
	)
}

type rebaseBone struct {
	name     string
	parent   int64
	position [3]float64
}

func rebaseModel(t *testing.T, bones []rebaseBone, cameraText string) kv3.Value {
	t.Helper()
	names := make([]kv3.Value, len(bones))
	parents := make([]kv3.Value, len(bones))
	positions := make([]kv3.Value, len(bones))
	rotations := make([]kv3.Value, len(bones))
	scales := make([]kv3.Value, len(bones))
	spheres := make([]kv3.Value, len(bones))
	flags := make([]kv3.Value, len(bones))
	for i, bone := range bones {
		names[i] = kv3.String(bone.name)
		parents[i] = kv3.Int(bone.parent)
		positions[i] = kv3.Array(kv3.Double(bone.position[0]),
			kv3.Double(bone.position[1]), kv3.Double(bone.position[2]))
		rotations[i] = clipArray(0, 0, 0, 1)
		scales[i] = kv3.Double(1)
		spheres[i] = kv3.Double(1)
		flags[i] = kv3.Int(0)
	}
	model := kv3.Object(
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.Array(names...)},
			kv3.Pair{Key: "m_nParent", Value: kv3.Array(parents...)},
			kv3.Pair{Key: "m_bonePosParent", Value: kv3.Array(positions...)},
			kv3.Pair{Key: "m_boneRotParent", Value: kv3.Array(rotations...)},
			kv3.Pair{Key: "m_boneScaleParent", Value: kv3.Array(scales...)},
			kv3.Pair{Key: "m_boneSphere", Value: kv3.Array(spheres...)},
			kv3.Pair{Key: "m_nFlag", Value: kv3.Array(flags...)},
		)},
		kv3.Pair{Key: "m_refMeshes", Value: kv3.Array()},
		kv3.Pair{Key: "m_refPhysicsData", Value: kv3.Array()},
		kv3.Pair{Key: "m_refPhysicsHitboxData", Value: kv3.Array()},
		kv3.Pair{Key: "m_boneFlexDrivers", Value: kv3.Array()},
		kv3.Pair{Key: "m_ExtParts", Value: kv3.Array()},
	)
	if cameraText != "" {
		model.Object = append(model.Object, kv3.Pair{
			Key: "m_modelInfo", Value: kv3.Object(kv3.Pair{
				Key: "m_keyValueText", Value: kv3.String(cameraText),
			}),
		})
	}
	return model
}

func rebaseMesh(t *testing.T, worlds []transform) kv3.Value {
	t.Helper()
	names := []string{"root", "rider"}
	bones := make([]kv3.Value, len(names))
	for i, name := range names {
		inverse := worlds[i].inverse().matrix()
		values := make([]kv3.Value, len(inverse))
		for j := range inverse {
			values[j] = kv3.Double(inverse[j])
		}
		bones[i] = kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.String(name)},
			kv3.Pair{Key: "m_invBindPose", Value: kv3.Array(values...)},
		)
	}
	cameraPoint := kv3.Object(
		kv3.Pair{Key: "key", Value: kv3.String("cam_point")},
		kv3.Pair{Key: "value", Value: kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("cam_point")},
			kv3.Pair{Key: "m_nInfluences", Value: kv3.Int(1)},
			kv3.Pair{Key: "m_influenceNames", Value: kv3.Array(kv3.String("rider"))},
			kv3.Pair{Key: "m_influenceWeights", Value: kv3.Array(kv3.Double(1))},
			kv3.Pair{Key: "m_bInfluenceRootTransform", Value: kv3.Array(kv3.Bool(false))},
			kv3.Pair{Key: "m_bIgnoreRotation", Value: kv3.Bool(false)},
			kv3.Pair{Key: "m_vInfluenceOffsets", Value: kv3.Array(clipArray(0, 0, 0))},
			kv3.Pair{Key: "m_vInfluenceRotations", Value: kv3.Array(clipArray(0, 0, 0, 1))},
		)},
	)
	return kv3.Object(
		kv3.Pair{Key: "m_constraints", Value: kv3.Array()},
		kv3.Pair{Key: "m_attachments", Value: kv3.Array(cameraPoint)},
		kv3.Pair{Key: "m_hitboxsets", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_HitBoxes", Value: kv3.Array(kv3.Object(
					kv3.Pair{Key: "m_sBoneName", Value: kv3.String("rider")},
					kv3.Pair{Key: "m_nShapeType", Value: kv3.Int(0)},
					kv3.Pair{Key: "m_bTranslationOnly", Value: kv3.Bool(false)},
					kv3.Pair{Key: "m_nGroupId", Value: kv3.Int(0)},
					kv3.Pair{Key: "m_name", Value: kv3.String("box")},
					kv3.Pair{Key: "m_vMinBounds", Value: clipArray(0, 0, 0)},
					kv3.Pair{Key: "m_vMaxBounds", Value: clipArray(0, 0, 0)},
					kv3.Pair{Key: "m_flShapeRadius", Value: kv3.Double(1)},
				))},
			)},
		))},
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_bones", Value: kv3.Array(bones...)},
			kv3.Pair{Key: "m_boneParents", Value: kv3.Array(kv3.Int(-1), kv3.Int(0))},
		)},
	)
}

func rebaseResource(t *testing.T, model, mesh kv3.Value) []byte {
	t.Helper()
	data, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&model, kv3.Format{})},
		{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: kv3.Encode(&mesh, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func rebaseSkeleton() kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(
			kv3.String("root"), kv3.String("rider"), kv3.String("control"))},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(
			kv3.Int(-1), kv3.Int(0), kv3.Int(0))},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(
			clipPose(0), clipPose(0), clipPose(0))},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(
			clipPose(0), clipPose(0), clipPose(0))},
	)
}

func rebaseFixture(t *testing.T) ([]byte, []byte, kv3.Value) {
	t.Helper()
	oldModel := rebaseModel(t, []rebaseBone{
		{name: "root", parent: -1},
		{name: "rider", parent: 0, position: [3]float64{10, 0, 0}},
	}, "")
	oldWorlds, ok := modelWorlds(&oldModel)
	if !ok {
		t.Fatal("old worlds")
	}
	mesh := rebaseMesh(t, oldWorlds)
	currentModel := rebaseModel(t, []rebaseBone{
		{name: "root", parent: -1},
		{name: "rider", parent: 0, position: [3]float64{20, 0, 20}},
		{name: "control", parent: 0, position: [3]float64{1, 0, 0}},
	}, rebaseCameraText)
	currentWorlds, ok := modelWorlds(&currentModel)
	if !ok {
		t.Fatal("current worlds")
	}
	return rebaseResource(t, oldModel, mesh), rebaseResource(t, currentModel, rebaseMesh(t, currentWorlds)), rebaseSkeleton()
}

func TestRebaseModelRigPreservesAuthoredBindFrames(t *testing.T) {
	source, base, skeleton := rebaseFixture(t)
	repaired, ok := RebaseModelRig(source, base, &skeleton)
	if !ok {
		t.Fatal("rebase declined")
	}
	_, value, _, err := camera.DecodeCompiled(repaired)
	if err != nil {
		t.Fatal(err)
	}
	indices, ok := modelIndices(&value)
	if !ok {
		t.Fatal("invalid repaired rig")
	}
	if index, ok := indices["control"]; !ok || index != 2 {
		t.Fatalf("control index = %v", indices)
	}
	rigValue, _ := value.Get("m_modelSkeleton")
	position, ok := arrayItem(rigValue, "m_bonePosParent", indices["rider"])
	if !ok {
		t.Fatal("missing rider position")
	}
	numbers, ok := clipNumbers(&position, 3)
	if !ok || numbers[0] != 10 {
		t.Fatalf("rider position = %v", numbers)
	}
	parent, ok := arrayItem(rigValue, "m_nParent", 2)
	if !ok {
		t.Fatal("missing control parent")
	}
	if value, _ := parent.AsInt(); value != 0 {
		t.Fatalf("control parent = %d", value)
	}
}

func TestRebaseModelRigRejectsExternalMeshes(t *testing.T) {
	source, base, skeleton := rebaseFixture(t)
	before, err := resource.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := before.DataBlock()
	if err != nil {
		t.Fatal(err)
	}
	model, err := kv3.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !replaceValueField(&model, "m_refMeshes", kv3.Array(kv3.String("models/body.vmesh"))) {
		t.Fatal("missing m_refMeshes")
	}
	modified := rebaseResource(t, model, mustMesh(t, source))
	if _, ok := RebaseModelRig(modified, base, &skeleton); ok {
		t.Fatal("external mesh accepted")
	}
}

func TestRebaseModelRigRequiresCameraEvidence(t *testing.T) {
	source, _, skeleton := rebaseFixture(t)
	oldModel := rebaseModel(t, []rebaseBone{
		{name: "root", parent: -1},
		{name: "rider", parent: 0, position: [3]float64{10, 0, 0}},
	}, "")
	oldWorlds, _ := modelWorlds(&oldModel)
	mesh := rebaseMesh(t, oldWorlds)
	withoutCamera := rebaseModel(t, []rebaseBone{
		{name: "root", parent: -1},
		{name: "rider", parent: 0, position: [3]float64{20, 0, 20}},
		{name: "control", parent: 0, position: [3]float64{1, 0, 0}},
	}, "")
	base := rebaseResource(t, withoutCamera, mesh)
	if _, ok := RebaseModelRig(source, base, &skeleton); ok {
		t.Fatal("missing camera evidence accepted")
	}
}

func mustMesh(t *testing.T, source []byte) kv3.Value {
	t.Helper()
	parsed, err := resource.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := parsed.FindBlock([4]byte{'M', 'D', 'A', 'T'})
	if !ok {
		t.Fatal("missing mesh")
	}
	mesh, err := kv3.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	return mesh
}
