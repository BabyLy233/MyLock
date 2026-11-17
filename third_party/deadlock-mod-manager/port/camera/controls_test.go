// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager camera_controls/tests.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

func controlFixture(control bool) (kv3.Value, kv3.Value) {
	names := []string{"body"}
	if control {
		names = []string{"body", "new_camera_anchor"}
	}
	count := len(names)
	boneNames := make([]kv3.Value, count)
	parents := make([]kv3.Value, count)
	positions := make([]kv3.Value, count)
	rotations := make([]kv3.Value, count)
	scales := make([]kv3.Value, count)
	spheres := make([]kv3.Value, count)
	flags := make([]kv3.Value, count)
	for i, name := range names {
		boneNames[i] = kv3.String(name)
		parents[i] = kv3.Int(-1)
		positions[i] = numbers(0, 0, 0)
		rotations[i] = numbers(0, 0, 0, 1)
		scales[i] = kv3.Double(1)
		if i == 0 {
			spheres[i] = kv3.Double(1)
		} else {
			spheres[i] = kv3.Double(0)
		}
		flags[i] = kv3.Int(0)
	}
	skeletonValue := kv3.Object(
		kv3.Pair{Key: "m_boneName", Value: array(boneNames...)},
		kv3.Pair{Key: "m_nParent", Value: array(parents...)},
		kv3.Pair{Key: "m_bonePosParent", Value: array(positions...)},
		kv3.Pair{Key: "m_boneRotParent", Value: array(rotations...)},
		kv3.Pair{Key: "m_boneScaleParent", Value: array(scales...)},
		kv3.Pair{Key: "m_boneSphere", Value: array(spheres...)},
		kv3.Pair{Key: "m_nFlag", Value: array(flags...)},
	)
	model := kv3.Object(
		kv3.Pair{Key: "m_modelSkeleton", Value: skeletonValue},
		kv3.Pair{Key: "m_animGraph2Refs", Value: array(kv3.Object(
			kv3.Pair{Key: "m_hGraph", Value: kv3.String("graphs/camera.vnmgraph")},
			kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("")},
		))},
		kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: array(kv3.String("models/camera.vnmskel"))},
		kv3.Pair{Key: "m_remappingTable", Value: array(kv3.Int(0))},
		kv3.Pair{Key: "m_remappingTableStarts", Value: array(kv3.Int(0))},
	)
	if count > 1 {
		table := make([]kv3.Value, count)
		for i := range table {
			table[i] = kv3.Int(int64(i))
		}
		remap, _ := model.Get("m_remappingTable")
		remap.Array = table
	}
	for _, field := range []string{
		"m_refMeshes",
		"m_refAnimGroups",
		"m_refAnimIncludeModels",
		"m_refSequenceGroups",
		"m_ExtParts",
		"m_boneFlexDrivers",
	} {
		model.Object = append(model.Object, kv3.Pair{Key: field, Value: array()})
	}

	bones := make([]kv3.Value, count)
	for i, name := range names {
		bones[i] = kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.String(name)},
			kv3.Pair{Key: "m_parentName", Value: kv3.String("")},
			kv3.Pair{Key: "m_flSphereRadius", Value: kv3.Double(0)},
			kv3.Pair{Key: "m_bbox", Value: kv3.Object(
				kv3.Pair{Key: "m_vecSize", Value: numbers(0, 0, 0)},
			)},
		)
	}
	meshValue := kv3.Object(
		kv3.Pair{Key: "m_constraints", Value: array()},
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_bones", Value: array(bones...)},
		)},
		kv3.Pair{Key: "m_attachments", Value: array(kv3.Object(
			kv3.Pair{Key: "key", Value: kv3.String("camera")},
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_nInfluences", Value: kv3.Int(1)},
				kv3.Pair{Key: "m_bInfluenceRootTransform", Value: array(kv3.Bool(false))},
				kv3.Pair{Key: "m_influenceNames", Value: array(kv3.String("new_camera_anchor"))},
			)},
		))},
	)
	return model, meshValue
}

func encoder(v *kv3.Value) kv3.Encoding {
	bytes := kv3.Encode(v, kv3.Format{})
	_, enc, err := kv3.DecodePreserving(bytes)
	if err != nil {
		panic(err)
	}
	return enc
}

func applyControls(model, meshValue *kv3.Value, current, base *kv3.Value) bool {
	modelEnc := encoder(model)
	meshEnc := encoder(meshValue)
	currentEnc := encoder(current)
	baseEnc := encoder(base)
	if !controlsExtend(model, &modelEnc, meshValue, &meshEnc, current, &currentEnc, base, &baseEnc, []string{"camera"}) {
		return false
	}
	for _, pair := range []struct {
		value *kv3.Value
		enc   *kv3.Encoding
	}{{model, &modelEnc}, {meshValue, &meshEnc}} {
		bytes, err := kv3.EncodePreserving(pair.value, pair.enc, kv3.Format{})
		if err != nil {
			return false
		}
		decoded, err := kv3.Decode(bytes)
		if err != nil || !kv3.Equal(&decoded, pair.value) {
			return false
		}
	}
	return true
}

func TestIsolatedCameraControlPreservesAuthoredBonesAndMeshIndices(t *testing.T) {
	model, meshValue := controlFixture(false)
	current, base := controlFixture(true)
	rigValue, _ := model.Get("m_modelSkeleton")
	positions, _ := rigValue.Get("m_bonePosParent")
	positions.Array[0] = numbers(123, 0, 0)
	oldModel := rig.Clone(&model)
	oldMesh := rig.Clone(&meshValue)

	if !applyControls(&model, &meshValue, &current, &base) {
		t.Fatal("controls extension declined")
	}
	newRig, _ := model.Get("m_modelSkeleton")
	oldRig, _ := oldModel.Get("m_modelSkeleton")
	for _, field := range boneFields {
		newValues, _ := fieldArray(newRig, field)
		oldValues, _ := fieldArray(oldRig, field)
		if len(newValues) != 2 || len(oldValues) != 1 {
			t.Fatalf("%s lengths = %d/%d", field, len(newValues), len(oldValues))
		}
		if !kv3.Equal(&newValues[0], &oldValues[0]) {
			t.Fatalf("%s first value changed", field)
		}
	}
	table, _ := fieldArray(&model, "m_remappingTable")
	want := array(kv3.Int(0), kv3.Int(1))
	if !kv3.Equal(&kv3.Value{Kind: kv3.KindArray, Array: table}, &want) {
		t.Fatalf("remapping table = %v", table)
	}
	newMeshSkeleton, _ := meshValue.Get("m_skeleton")
	newBones, _ := fieldArray(newMeshSkeleton, "m_bones")
	oldMeshSkeleton, _ := oldMesh.Get("m_skeleton")
	oldBones, _ := fieldArray(oldMeshSkeleton, "m_bones")
	if len(newBones) != 2 || len(oldBones) != 1 || !kv3.Equal(&newBones[0], &oldBones[0]) {
		t.Fatal("mesh bones changed")
	}
}

func TestCameraControlExtensionDeclinesDeformationAndUnknownLayouts(t *testing.T) {
	for caseIndex := 0; caseIndex < 5; caseIndex++ {
		model, meshValue := controlFixture(false)
		current, base := controlFixture(true)
		switch caseIndex {
		case 0:
			rigValue, _ := current.Get("m_modelSkeleton")
			parents, _ := rigValue.Get("m_nParent")
			parents.Array = []kv3.Value{kv3.Int(-1), kv3.Int(0)}
		case 1:
			rigValue, _ := current.Get("m_modelSkeleton")
			parents, _ := rigValue.Get("m_nParent")
			parents.Array = []kv3.Value{kv3.Int(1), kv3.Int(-1)}
		case 2:
			rigValue, _ := current.Get("m_modelSkeleton")
			spheres, _ := rigValue.Get("m_boneSphere")
			spheres.Array = []kv3.Value{kv3.Double(1), kv3.Double(1)}
		case 3:
			refs, _ := current.Get("m_animGraph2Refs")
			refs.Array = nil
		case 4:
			table, _ := model.Get("m_remappingTable")
			table.Array = []kv3.Value{kv3.Int(9)}
		}
		before := rig.Clone(&model)
		if applyControls(&model, &meshValue, &current, &base) {
			t.Fatalf("case %d was accepted", caseIndex)
		}
		if !kv3.Equal(&model, &before) {
			t.Fatalf("case %d changed the model", caseIndex)
		}
	}
}
