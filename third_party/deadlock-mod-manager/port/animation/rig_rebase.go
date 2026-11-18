// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation/rig_rebase.rs,
// rig_rebase/bind_frames.rs and rig_rebase/preservation.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

var modelSkeletonFields = [...]string{
	"m_boneName",
	"m_nParent",
	"m_bonePosParent",
	"m_boneRotParent",
	"m_boneScaleParent",
	"m_boneSphere",
	"m_nFlag",
}

var modelMeshBlock = [4]byte{'M', 'D', 'A', 'T'}

// RebaseModelRig reconciles the mod's deformation hierarchy with the current
// game hierarchy without changing the authored skin binding. It is atomic:
// every mesh, camera attachment and hitbox must be proven.
func RebaseModelRig(source, base []byte, skeleton *kv3.Value) ([]byte, bool) {
	format, old, encoding, err := camera.DecodeCompiled(source)
	if err != nil {
		return nil, false
	}
	_, current, _, err := camera.DecodeCompiled(base)
	if err != nil {
		return nil, false
	}
	for _, key := range []string{"m_refMeshes", "m_refPhysicsData", "m_refPhysicsHitboxData"} {
		value, ok := old.Get(key)
		if !ok {
			return nil, false
		}
		items, ok := value.AsArray()
		if !ok {
			return nil, false
		}
		for i := range items {
			name, ok := items[i].AsString()
			if !ok || name != "" {
				return nil, false
			}
		}
	}
	for _, key := range []string{"m_boneFlexDrivers", "m_ExtParts"} {
		value, ok := old.Get(key)
		if !ok {
			return nil, false
		}
		items, ok := value.AsArray()
		if !ok || len(items) != 0 {
			return nil, false
		}
	}
	oldWorlds, ok := modelWorlds(&old)
	if !ok {
		return nil, false
	}
	if _, ok := modelWorlds(&current); !ok {
		return nil, false
	}
	oldIndices, ok := modelIndices(&old)
	if !ok {
		return nil, false
	}
	currentIndices, ok := modelIndices(&current)
	if !ok {
		return nil, false
	}
	merged := make(map[string]int, len(oldIndices))
	for name, index := range oldIndices {
		merged[name] = index
	}
	rigValue, ok := old.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	baseRig, ok := current.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	rigFields, ok := rigValue.AsObject()
	if !ok {
		return nil, false
	}
	baseFields, ok := baseRig.AsObject()
	if !ok || len(rigFields) != len(modelSkeletonFields) || len(baseFields) != len(modelSkeletonFields) {
		return nil, false
	}
	originalCount := len(oldIndices)
	for _, field := range modelSkeletonFields {
		items, ok := skeletonArray(rigValue, field)
		if !ok || len(items) != originalCount {
			return nil, false
		}
	}
	required := make(map[string]struct{})
	for name := range currentIndices {
		if _, shared := oldIndices[name]; shared {
			required[name] = struct{}{}
		}
	}
	skeletonNamesValue, ok := skeleton.Get("m_boneIDs")
	if !ok {
		return nil, false
	}
	skeletonNames, ok := skeletonNamesValue.AsArray()
	if !ok {
		return nil, false
	}
	for i := range skeletonNames {
		name, ok := skeletonNames[i].AsString()
		if !ok {
			return nil, false
		}
		name = strings.ToLower(name)
		if _, exists := currentIndices[name]; exists {
			required[name] = struct{}{}
		}
	}
	baseResource, err := resource.Parse(base)
	if err != nil {
		return nil, false
	}
	meshBytes, ok := baseResource.FindBlock(modelMeshBlock)
	if !ok {
		return nil, false
	}
	mesh, err := kv3.Decode(meshBytes)
	if err != nil {
		return nil, false
	}
	cameraBones, ok := camera.RequiredBones(&current, &mesh)
	if !ok {
		return nil, false
	}
	for name := range cameraBones {
		required[name] = struct{}{}
	}
	for _, name := range slices.Sorted(maps.Keys(required)) {
		index, ok := currentIndices[name]
		if !ok {
			return nil, false
		}
		for {
			baseNames, ok := skeletonArray(baseRig, "m_boneName")
			if !ok || index < 0 || index >= len(baseNames) {
				return nil, false
			}
			parentName, ok := baseNames[index].AsString()
			if !ok {
				return nil, false
			}
			required[strings.ToLower(parentName)] = struct{}{}
			parent, ok := arrayItem(baseRig, "m_nParent", index)
			if !ok {
				return nil, false
			}
			parentIndex, ok := parent.AsInt()
			if !ok {
				return nil, false
			}
			if parentIndex == -1 {
				break
			}
			if parentIndex < 0 || parentIndex >= int64(len(baseNames)) {
				return nil, false
			}
			index = int(parentIndex)
		}
	}
	value := rig.Clone(&old)
	valueRig, ok := value.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	valueEncoding, ok := encoding.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	baseNames, ok := skeletonArray(baseRig, "m_boneName")
	if !ok {
		return nil, false
	}
	for index := range baseNames {
		name, ok := baseNames[index].AsString()
		if !ok {
			return nil, false
		}
		key := strings.ToLower(name)
		if _, needed := required[key]; !needed {
			continue
		}
		if _, exists := merged[key]; exists {
			continue
		}
		target := len(merged)
		merged[key] = target
		for _, field := range modelSkeletonFields {
			item, ok := arrayItem(baseRig, field, index)
			if !ok {
				return nil, false
			}
			if field == "m_nFlag" {
				flags, ok := skeletonArray(rigValue, field)
				if !ok || len(flags) == 0 {
					return nil, false
				}
				switch flags[0].Kind {
				case kv3.KindInt:
					number, ok := item.AsInt()
					if !ok {
						return nil, false
					}
					item = kv3.Int(number)
				case kv3.KindUInt:
					number, ok := item.AsUInt()
					if !ok {
						return nil, false
					}
					item = kv3.UInt(number)
				default:
					return nil, false
				}
			}
			if !appendArrayItem(valueRig, field, rig.Clone(&item)) {
				return nil, false
			}
			fieldEncoding, ok := valueEncoding.Get(field)
			if !ok {
				return nil, false
			}
			schema, ok := fieldEncoding.AsArray()
			if !ok || len(schema) == 0 {
				return nil, false
			}
			fieldEncoding.Children.Array = append(fieldEncoding.Children.Array,
				*cloneEncoding(&schema[0]))
		}
	}
	for name, currentIndex := range currentIndices {
		if _, needed := required[name]; !needed {
			continue
		}
		target, ok := merged[name]
		if !ok {
			return nil, false
		}
		for _, field := range []string{"m_bonePosParent", "m_boneRotParent", "m_boneScaleParent"} {
			item, ok := arrayItem(baseRig, field, currentIndex)
			if !ok {
				return nil, false
			}
			slot, ok := arrayItemMut(valueRig, field, target)
			if !ok {
				return nil, false
			}
			*slot = rig.Clone(&item)
		}
		parentItem, ok := arrayItem(baseRig, "m_nParent", currentIndex)
		if !ok {
			return nil, false
		}
		parentIndex, ok := parentItem.AsInt()
		if !ok {
			return nil, false
		}
		parent := int64(-1)
		if parentIndex >= 0 {
			if parentIndex >= int64(len(baseNames)) {
				return nil, false
			}
			parentName, ok := baseNames[parentIndex].AsString()
			if !ok {
				return nil, false
			}
			mergedParent, ok := merged[strings.ToLower(parentName)]
			if !ok {
				return nil, false
			}
			parent = int64(mergedParent)
		}
		slot, ok := arrayItemMut(valueRig, "m_nParent", target)
		if !ok {
			return nil, false
		}
		*slot = kv3.Int(parent)
	}
	if _, err := sampledContract(&value, &current, skeleton); err != nil {
		return nil, false
	}
	if !preserveBindFrames(&value, oldWorlds) {
		return nil, false
	}
	if _, err := sampledContract(&value, &current, skeleton); err != nil {
		return nil, false
	}
	if err := kv3.NormalizeNumericArrayEncoding(&value, &encoding); err != nil {
		return nil, false
	}
	payload, err := kv3.EncodePreserving(&value, &encoding, format)
	if err != nil {
		return nil, false
	}
	sourceResource, err := resource.Parse(source)
	if err != nil {
		return nil, false
	}
	rebuilt, err := sourceResource.RebuildWithDataPreserving(payload)
	if err != nil {
		return nil, false
	}
	meshIndices := make([]int, 0)
	for i, block := range sourceResource.Blocks() {
		if block.Kind == modelMeshBlock {
			meshIndices = append(meshIndices, i)
		}
	}
	if len(meshIndices) == 0 {
		return nil, false
	}
	for _, index := range meshIndices {
		bytes, ok := sourceResource.GetBlockByIndex(index)
		if !ok {
			return nil, false
		}
		mesh, err := kv3.Decode(bytes)
		if err != nil {
			return nil, false
		}
		if !validateMeshBinding(&mesh, oldIndices, oldWorlds) {
			return nil, false
		}
	}
	actualFormat, actual, actualEncoding, err := camera.DecodeCompiled(rebuilt)
	if err != nil {
		return nil, false
	}
	if actualFormat != format || !camera.RuntimeValuesEqual(&value, &actual) ||
		!kv3.EncodingPreserved(&value, &encoding, &actualEncoding) {
		return nil, false
	}
	if !verifyRebasedModel(source, rebuilt) {
		return nil, false
	}
	return rebuilt, true
}

func modelIndices(model *kv3.Value) (map[string]int, bool) {
	rigValue, ok := model.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	names, ok := skeletonArray(rigValue, "m_boneName")
	if !ok {
		return nil, false
	}
	indices := make(map[string]int, len(names))
	for i := range names {
		name, ok := names[i].AsString()
		if !ok {
			return nil, false
		}
		name = strings.ToLower(name)
		if _, duplicate := indices[name]; duplicate {
			return nil, false
		}
		indices[name] = i
	}
	return indices, true
}

func appendArrayItem(value *kv3.Value, field string, item kv3.Value) bool {
	fieldValue, ok := value.Get(field)
	if !ok || fieldValue.Kind != kv3.KindArray {
		return false
	}
	fieldValue.Array = append(fieldValue.Array, item)
	return true
}

// preserveBindFrames keeps the authored model-space bind frames and derives
// local frames for game-added controls from the retained authored parent.
func preserveBindFrames(model *kv3.Value, authored []transform) bool {
	canonical, ok := modelWorlds(model)
	if !ok {
		return false
	}
	rigValue, ok := model.Get("m_modelSkeleton")
	if !ok {
		return false
	}
	parents, ok := skeletonArray(rigValue, "m_nParent")
	if !ok {
		return false
	}
	target := make([]*transform, len(parents))
	for i := range target {
		if i < len(authored) {
			value := authored[i]
			target[i] = &value
		}
	}
	for start := len(authored); start < len(parents); start++ {
		var chain []int
		index := start
		for target[index] == nil {
			chain = append(chain, index)
			parent, ok := parents[index].AsInt()
			if !ok {
				return false
			}
			if parent < 0 {
				break
			}
			if parent >= int64(len(parents)) {
				return false
			}
			index = int(parent)
		}
		for i := len(chain) - 1; i >= 0; i-- {
			index := chain[i]
			parent, ok := parents[index].AsInt()
			if !ok {
				return false
			}
			before, after := identityTransform, identityTransform
			if parent >= 0 {
				if parent >= int64(len(target)) || target[parent] == nil {
					return false
				}
				before = canonical[parent]
				after = *target[parent]
			}
			frame := after.compose(before.inverse().compose(canonical[index]))
			target[index] = &frame
		}
	}
	locals := make([]transform, len(parents))
	for i := range parents {
		if target[i] == nil {
			return false
		}
		parent, ok := parents[i].AsInt()
		if !ok {
			return false
		}
		parentWorld := identityTransform
		if parent >= 0 {
			if parent >= int64(len(target)) || target[parent] == nil {
				return false
			}
			parentWorld = *target[parent]
		}
		locals[i] = parentWorld.inverse().compose(*target[i])
	}
	for i, local := range locals {
		position, ok := arrayItemMut(rigValue, "m_bonePosParent", i)
		if !ok || !replaceNumbers(position, local.position[:]) {
			return false
		}
		rotation, ok := arrayItemMut(rigValue, "m_boneRotParent", i)
		if !ok || !replaceNumbers(rotation, local.rotation[:]) {
			return false
		}
		scale, ok := arrayItemMut(rigValue, "m_boneScaleParent", i)
		if !ok {
			return false
		}
		number, ok := sourceFloat(local.scale)
		if !ok {
			return false
		}
		*scale = number
	}
	actual, ok := modelWorlds(model)
	if !ok {
		return false
	}
	for i := range authored {
		a := authored[i].matrix()
		b := actual[i].matrix()
		for j := range a {
			if math.Abs(a[j]-b[j]) > 0.001 {
				return false
			}
		}
	}
	return true
}

func validateMeshBinding(mesh *kv3.Value, indices map[string]int, worlds []transform) bool {
	constraints, ok := mesh.Get("m_constraints")
	if !ok {
		return false
	}
	items, ok := constraints.AsArray()
	if !ok || len(items) != 0 {
		return false
	}
	if groom, ok := mesh.Get("m_pGroomData"); ok && groom.Kind != kv3.KindNull {
		return false
	}
	skeletonValue, ok := mesh.Get("m_skeleton")
	if !ok {
		return false
	}
	bonesValue, ok := skeletonValue.Get("m_bones")
	if !ok {
		return false
	}
	bones, ok := bonesValue.AsArray()
	if !ok {
		return false
	}
	seen := make(map[string]struct{}, len(bones))
	for i := range bones {
		nameValue, ok := bones[i].Get("m_boneName")
		if !ok {
			return false
		}
		name, ok := nameValue.AsString()
		if !ok {
			return false
		}
		name = strings.ToLower(name)
		if _, duplicate := seen[name]; duplicate {
			return false
		}
		seen[name] = struct{}{}
		index, ok := indices[name]
		if !ok || index >= len(worlds) {
			return false
		}
		world := worlds[index].matrix()
		inverseValue, ok := bones[i].Get("m_invBindPose")
		if !ok {
			return false
		}
		inverse, ok := clipNumbers(inverseValue, 12)
		if !ok {
			return false
		}
		for row := range 3 {
			for col := range 4 {
				sum := 0.0
				for k := range 3 {
					sum += world[row*4+k] * inverse[k*4+col]
				}
				if col == 3 {
					sum += world[row*4+3]
				}
				expected := 0.0
				if row == col {
					expected = 1
				}
				if math.Abs(sum-expected) > 0.003 {
					return false
				}
			}
		}
	}
	return true
}

func verifyRebasedModel(source, rebuilt []byte) bool {
	before, err := resource.Parse(source)
	if err != nil {
		return false
	}
	after, err := resource.Parse(rebuilt)
	if err != nil {
		return false
	}
	_, old, _, err := camera.DecodeCompiled(source)
	if err != nil {
		return false
	}
	_, current, _, err := camera.DecodeCompiled(rebuilt)
	if err != nil {
		return false
	}
	oldIndices, ok := modelIndices(&old)
	if !ok {
		return false
	}
	newIndices, ok := modelIndices(&current)
	if !ok {
		return false
	}
	for name, index := range oldIndices {
		if other, ok := newIndices[name]; !ok || other != index {
			return false
		}
	}
	for _, field := range []string{"m_remappingTable", "m_remappingTableStarts", "m_refMeshes"} {
		left, leftOK := old.Get(field)
		right, rightOK := current.Get(field)
		if leftOK != rightOK || (leftOK && !camera.RuntimeValuesEqual(left, right)) {
			return false
		}
	}
	oldWorlds, ok := modelWorlds(&old)
	if !ok {
		return false
	}
	newWorlds, ok := modelWorlds(&current)
	if !ok {
		return false
	}
	for i := range oldWorlds {
		if i >= len(newWorlds) {
			return false
		}
		a := oldWorlds[i].inverse().matrix()
		b := newWorlds[i].inverse().matrix()
		for j := range a {
			if math.Abs(a[j]-b[j]) > 0.003 {
				return false
			}
		}
	}
	beforeBlocks := before.Blocks()
	afterBlocks := after.Blocks()
	if len(beforeBlocks) != len(afterBlocks) {
		return false
	}
	for index, block := range beforeBlocks {
		if block.Kind != afterBlocks[index].Kind {
			return false
		}
		a, ok := before.GetBlockByIndex(index)
		if !ok {
			return false
		}
		b, ok := after.GetBlockByIndex(index)
		if !ok {
			return false
		}
		if block.Kind == modelMeshBlock {
			oldMesh, err := kv3.Decode(a)
			if err != nil {
				return false
			}
			newMesh, err := kv3.Decode(b)
			if err != nil {
				return false
			}
			if !verifyRebasedMesh(&oldMesh, &newMesh, oldIndices, oldWorlds, newWorlds) {
				return false
			}
			continue
		}
		if block.Kind != [4]byte{'D', 'A', 'T', 'A'} && block.Kind != [4]byte{'R', 'E', 'R', 'L'} &&
			!slices.Equal(a, b) {
			return false
		}
	}
	return true
}

func verifyRebasedMesh(old, current *kv3.Value, indices map[string]int,
	oldWorlds, newWorlds []transform) bool {
	oldSkeleton, ok := old.Get("m_skeleton")
	if !ok {
		return false
	}
	newSkeleton, ok := current.Get("m_skeleton")
	if !ok {
		return false
	}
	oldBones, ok := skeletonArray(oldSkeleton, "m_bones")
	if !ok {
		return false
	}
	newBones, ok := skeletonArray(newSkeleton, "m_bones")
	if !ok || len(oldBones) != len(newBones) {
		return false
	}
	for _, field := range []string{"m_sceneObjects"} {
		left, leftOK := old.Get(field)
		right, rightOK := current.Get(field)
		if leftOK != rightOK || (leftOK && !camera.RuntimeValuesEqual(left, right)) {
			return false
		}
	}
	oldParents, oldParentsOK := oldSkeleton.Get("m_boneParents")
	newParents, newParentsOK := newSkeleton.Get("m_boneParents")
	if oldParentsOK != newParentsOK || (oldParentsOK && !camera.RuntimeValuesEqual(oldParents, newParents)) {
		return false
	}
	for i := range oldBones {
		oldName, _ := oldBones[i].Get("m_boneName")
		newName, _ := newBones[i].Get("m_boneName")
		if oldName == nil || newName == nil || !camera.RuntimeValuesEqual(oldName, newName) {
			return false
		}
		oldParent, _ := oldBones[i].Get("m_parentName")
		newParent, _ := newBones[i].Get("m_parentName")
		if (oldParent == nil) != (newParent == nil) ||
			(oldParent != nil && !camera.RuntimeValuesEqual(oldParent, newParent)) {
			return false
		}
		name, ok := oldName.AsString()
		if !ok {
			return false
		}
		index, ok := indices[strings.ToLower(name)]
		if !ok || index >= len(oldWorlds) || index >= len(newWorlds) {
			return false
		}
		oldInverse, ok := oldBones[i].Get("m_invBindPose")
		if !ok {
			return false
		}
		newInverse, ok := newBones[i].Get("m_invBindPose")
		if !ok {
			return false
		}
		oldNumbers, ok := clipNumbers(oldInverse, 12)
		if !ok {
			return false
		}
		newNumbers, ok := clipNumbers(newInverse, 12)
		if !ok {
			return false
		}
		a := skinMatrix(oldWorlds[index], oldNumbers)
		b := skinMatrix(newWorlds[index], newNumbers)
		for j := range a {
			if math.Abs(a[j]-b[j]) > 0.003 {
				return false
			}
		}
	}
	oldAttachments, ok := skeletonArray(old, "m_attachments")
	if !ok {
		return false
	}
	newAttachments, ok := skeletonArray(current, "m_attachments")
	if !ok {
		return false
	}
	for i := range oldAttachments {
		oldValue, ok := oldAttachments[i].Get("value")
		if !ok {
			return false
		}
		oldName, ok := oldValue.Get("m_name")
		if !ok {
			return false
		}
		newValue, ok := findAttachmentValue(newAttachments, oldName)
		if !ok {
			return false
		}
		for _, key := range []string{
			"m_nInfluences",
			"m_influenceNames",
			"m_influenceWeights",
			"m_bInfluenceRootTransform",
			"m_bIgnoreRotation",
		} {
			left, leftOK := oldValue.Get(key)
			right, rightOK := newValue.Get(key)
			if !leftOK || !rightOK || !camera.RuntimeValuesEqual(left, right) {
				return false
			}
		}
		countValue, ok := oldValue.Get("m_nInfluences")
		if !ok {
			return false
		}
		count, ok := countValue.AsInt()
		if !ok || count < 0 {
			return false
		}
		rootValues, ok := skeletonArray(oldValue, "m_bInfluenceRootTransform")
		if !ok || int(count) > len(rootValues) {
			return false
		}
		influenceNames, ok := skeletonArray(oldValue, "m_influenceNames")
		if !ok || int(count) > len(influenceNames) {
			return false
		}
		ignoreValue, ok := oldValue.Get("m_bIgnoreRotation")
		if !ok {
			return false
		}
		ignoreRotation, ok := ignoreValue.AsBool()
		if !ok {
			return false
		}
		for j := 0; j < int(count); j++ {
			root, ok := rootValues[j].AsBool()
			if !ok {
				return false
			}
			oldWorld, newWorld := identityTransform, identityTransform
			if !root {
				name, ok := influenceNames[j].AsString()
				if !ok {
					return false
				}
				index, ok := indices[strings.ToLower(name)]
				if !ok || index >= len(oldWorlds) || index >= len(newWorlds) {
					return false
				}
				oldWorld = oldWorlds[index]
				newWorld = newWorlds[index]
			}
			oldFrame, ok := influenceFrame(oldValue, j)
			if !ok {
				return false
			}
			newFrame, ok := influenceFrame(newValue, j)
			if !ok {
				return false
			}
			a := oldWorld.compose(oldFrame)
			b := newWorld.compose(newFrame)
			if !closePosition(a.position, b.position) {
				return false
			}
			if !ignoreRotation {
				for _, axis := range [][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
					if !closePosition(a.vector(axis), b.vector(axis)) {
						return false
					}
				}
			}
		}
	}
	oldSets, ok := old.Get("m_hitboxsets")
	if !ok {
		return false
	}
	newSets, ok := current.Get("m_hitboxsets")
	if !ok {
		return false
	}
	oldSetItems, ok := oldSets.AsArray()
	if !ok {
		return false
	}
	newSetItems, ok := newSets.AsArray()
	if !ok || len(oldSetItems) != len(newSetItems) {
		return false
	}
	for i := range oldSetItems {
		oldSet, ok := oldSetItems[i].Get("value")
		if !ok {
			return false
		}
		newSet, ok := newSetItems[i].Get("value")
		if !ok {
			return false
		}
		oldBoxes, ok := skeletonArray(oldSet, "m_HitBoxes")
		if !ok {
			return false
		}
		newBoxes, ok := skeletonArray(newSet, "m_HitBoxes")
		if !ok || len(oldBoxes) != len(newBoxes) {
			return false
		}
		for j := range oldBoxes {
			nameValue, ok := oldBoxes[j].Get("m_sBoneName")
			if !ok {
				return false
			}
			name, ok := nameValue.AsString()
			if !ok {
				return false
			}
			index, ok := indices[strings.ToLower(name)]
			if !ok || index >= len(oldWorlds) || index >= len(newWorlds) {
				return false
			}
			for _, field := range []string{
				"m_sBoneName", "m_nShapeType", "m_bTranslationOnly", "m_nGroupId", "m_name",
			} {
				left, leftOK := oldBoxes[j].Get(field)
				right, rightOK := newBoxes[j].Get(field)
				if !leftOK || !rightOK || !camera.RuntimeValuesEqual(left, right) {
					return false
				}
			}
			translationValue, ok := oldBoxes[j].Get("m_bTranslationOnly")
			if !ok {
				return false
			}
			translationOnly, ok := translationValue.AsBool()
			if !ok {
				return false
			}
			oldWorld, newWorld := oldWorlds[index], newWorlds[index]
			if translationOnly {
				oldWorld.rotation = identityTransform.rotation
				oldWorld.scale = 1
				newWorld.rotation = identityTransform.rotation
				newWorld.scale = 1
			}
			for _, field := range []string{"m_vMinBounds", "m_vMaxBounds"} {
				oldValue, ok := oldBoxes[j].Get(field)
				if !ok {
					return false
				}
				newValue, ok := newBoxes[j].Get(field)
				if !ok {
					return false
				}
				oldNumbers, ok := clipNumbers(oldValue, 3)
				if !ok {
					return false
				}
				newNumbers, ok := clipNumbers(newValue, 3)
				if !ok {
					return false
				}
				if !closePosition(oldWorld.point([3]float64{oldNumbers[0], oldNumbers[1], oldNumbers[2]}),
					newWorld.point([3]float64{newNumbers[0], newNumbers[1], newNumbers[2]})) {
					return false
				}
			}
			oldRadius, ok := boxRadius(&oldBoxes[j])
			if !ok {
				return false
			}
			newRadius, ok := boxRadius(&newBoxes[j])
			if !ok || math.Abs(oldRadius*oldWorld.scale-newRadius*newWorld.scale) > 0.003 {
				return false
			}
		}
	}
	return true
}

func findAttachmentValue(attachments []kv3.Value, name *kv3.Value) (*kv3.Value, bool) {
	for i := range attachments {
		value, ok := attachments[i].Get("value")
		if !ok {
			continue
		}
		candidate, ok := value.Get("m_name")
		if ok && camera.RuntimeValuesEqual(candidate, name) {
			return value, true
		}
	}
	return nil, false
}

func influenceFrame(value *kv3.Value, index int) (transform, bool) {
	offsets, ok := skeletonArray(value, "m_vInfluenceOffsets")
	if !ok || index >= len(offsets) {
		return transform{}, false
	}
	rotations, ok := skeletonArray(value, "m_vInfluenceRotations")
	if !ok || index >= len(rotations) {
		return transform{}, false
	}
	position, ok := clipNumbers(&offsets[index], 3)
	if !ok {
		return transform{}, false
	}
	rotation, ok := clipNumbers(&rotations[index], 4)
	if !ok {
		return transform{}, false
	}
	return newTransform([3]float64{position[0], position[1], position[2]},
		[4]float64{rotation[0], rotation[1], rotation[2], rotation[3]}, 1)
}

func boxRadius(box *kv3.Value) (float64, bool) {
	value, ok := box.Get("m_flShapeRadius")
	if !ok {
		return 0, false
	}
	return value.AsFloat()
}

func skinMatrix(world transform, inverse []float64) [12]float64 {
	m := world.matrix()
	var result [12]float64
	for i := range result {
		row, col := i/4, i%4
		sum := 0.0
		for k := range 3 {
			sum += m[row*4+k] * inverse[k*4+col]
		}
		if col == 3 {
			sum += m[row*4+3]
		}
		result[i] = sum
	}
	return result
}

func closePosition(a, b [3]float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 0.003 {
			return false
		}
	}
	return true
}
