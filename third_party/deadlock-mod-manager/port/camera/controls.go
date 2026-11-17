// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/camera_controls.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"maps"
	"math"
	"sort"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

var boneFields = [7]string{
	"m_boneName",
	"m_nParent",
	"m_bonePosParent",
	"m_boneRotParent",
	"m_boneScaleParent",
	"m_boneSphere",
	"m_nFlag",
}

func graphs(model *kv3.Value) (map[string]string, bool) {
	refs, ok := fieldArray(model, "m_animGraph2Refs")
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(refs))
	for i := range refs {
		idV, ok := refs[i].Get("m_sIdentifier")
		if !ok {
			return nil, false
		}
		id, ok := idV.AsString()
		if !ok {
			return nil, false
		}
		pathV, ok := refs[i].Get("m_hGraph")
		if !ok {
			return nil, false
		}
		path, ok := pathV.AsString()
		if !ok {
			return nil, false
		}
		path = NormalizePath(path)
		if path == "" {
			return nil, false
		}
		key := stringsLower(id)
		if _, dup := out[key]; dup {
			return nil, false
		}
		out[key] = path
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func stringsLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func controlsNames(rigValue *kv3.Value) (map[string]int, bool) {
	if !rig.ValidSkeleton(rigValue, "m_boneName", "m_nParent", []rig.PoseKey{
		{Key: "m_bonePosParent", Width: 3},
		{Key: "m_boneRotParent", Width: 4},
		{Key: "m_boneScaleParent", Width: 0},
	}) {
		return nil, false
	}
	values, ok := fieldArray(rigValue, "m_boneName")
	if !ok {
		return nil, false
	}
	fields, ok := rigValue.AsObject()
	if !ok || len(fields) != len(boneFields) {
		return nil, false
	}
	for _, field := range boneFields {
		items, ok := fieldArray(rigValue, field)
		if !ok || len(items) != len(values) {
			return nil, false
		}
	}
	out := make(map[string]int, len(values))
	for i := range values {
		name, ok := values[i].AsString()
		if !ok {
			return nil, false
		}
		out[name] = i
	}
	return out, true
}

func rootLeaf(rigValue *kv3.Value, index int) bool {
	parents, ok := fieldArray(rigValue, "m_nParent")
	if !ok || index < 0 || index >= len(parents) {
		return false
	}
	parent, ok := parents[index].AsInt()
	if !ok || parent != -1 {
		return false
	}
	spheres, ok := fieldArray(rigValue, "m_boneSphere")
	if !ok || index >= len(spheres) {
		return false
	}
	sphere, ok := spheres[index].AsFloat()
	if !ok || sphere != 0 {
		return false
	}
	for i := range parents {
		p, ok := parents[i].AsInt()
		if ok && p == int64(index) {
			return false
		}
	}
	return true
}

func controlPose(rigValue *kv3.Value, index int) (kv3.Value, bool) {
	positions, ok := fieldArray(rigValue, "m_bonePosParent")
	if !ok || index >= len(positions) {
		return kv3.Value{}, false
	}
	position, ok := positions[index].AsArray()
	if !ok {
		return kv3.Value{}, false
	}
	scales, ok := fieldArray(rigValue, "m_boneScaleParent")
	if !ok || index >= len(scales) {
		return kv3.Value{}, false
	}
	rotations, ok := fieldArray(rigValue, "m_boneRotParent")
	if !ok || index >= len(rotations) {
		return kv3.Value{}, false
	}
	rotation, ok := rotations[index].AsArray()
	if !ok {
		return kv3.Value{}, false
	}
	values := make([]kv3.Value, 0, len(position)+1+len(rotation))
	values = append(values, position...)
	values = append(values, scales[index])
	values = append(values, rotation...)
	return kv3.Value{Kind: kv3.KindArray, Array: values}, true
}

func appendValue(value *kv3.Value, encoding *kv3.Encoding, item kv3.Value, itemEncoding kv3.Encoding) bool {
	if value.Kind != kv3.KindArray || encoding.Children.Kind != kv3.ChildrenArray {
		return false
	}
	if len(value.Array) != len(encoding.Children.Array) {
		return false
	}
	value.Array = append(value.Array, rig.Clone(&item))
	encoding.Children.Array = append(encoding.Children.Array, itemEncoding)
	return true
}

func appendField(value *kv3.Value, encoding *kv3.Encoding, key string, item kv3.Value, itemEncoding kv3.Encoding) bool {
	field, ok := value.Get(key)
	if !ok {
		return false
	}
	enc, ok := encoding.Get(key)
	if !ok {
		return false
	}
	return appendValue(field, enc, item, itemEncoding)
}

func appendNested(value *kv3.Value, encoding *kv3.Encoding, parentKey, key string, item kv3.Value, itemEncoding kv3.Encoding) bool {
	parent, ok := fieldObject(value, parentKey)
	if !ok {
		return false
	}
	parentEncoding, ok := encoding.Get(parentKey)
	if !ok {
		return false
	}
	child, ok := parent.Get(key)
	if !ok {
		return false
	}
	childEncoding, ok := parentEncoding.Get(key)
	if !ok {
		return false
	}
	return appendValue(child, childEncoding, item, itemEncoding)
}

// controlsExtend adds the camera control bones to a single embedded mesh. It
// only runs under the strict conditions in the upstream rule: matching
// animation graphs, no reference meshes or animations, a single remapping
// table, and root-leaf control bones.
func controlsExtend(
	model *kv3.Value, modelEncoding *kv3.Encoding,
	mesh *kv3.Value, meshEncoding *kv3.Encoding,
	current *kv3.Value, currentEncoding *kv3.Encoding,
	baseMesh *kv3.Value, baseEncoding *kv3.Encoding,
	points []string,
) bool {
	modelGraphs, ok := graphs(model)
	if !ok {
		return false
	}
	currentGraphs, ok := graphs(current)
	if !ok || !maps.Equal(modelGraphs, currentGraphs) {
		return false
	}
	modelRefs, ok := model.Get("m_vecNmSkeletonRefs")
	if !ok {
		return false
	}
	currentRefs, ok := current.Get("m_vecNmSkeletonRefs")
	if !ok || !kv3.Equal(modelRefs, currentRefs) {
		return false
	}
	refs, ok := modelRefs.AsArray()
	if !ok || len(refs) == 0 {
		return false
	}
	for _, field := range []string{
		"m_refMeshes",
		"m_refAnimGroups",
		"m_refAnimIncludeModels",
		"m_refSequenceGroups",
		"m_ExtParts",
		"m_boneFlexDrivers",
	} {
		items, ok := fieldArray(model, field)
		if !ok || len(items) != 0 {
			return false
		}
	}
	for _, value := range []*kv3.Value{mesh, baseMesh} {
		items, ok := fieldArray(value, "m_constraints")
		if !ok || len(items) != 0 {
			return false
		}
	}

	oldRig, ok := fieldObject(model, "m_modelSkeleton")
	if !ok {
		return false
	}
	oldNames, ok := controlsNames(oldRig)
	if !ok {
		return false
	}
	newRig, ok := fieldObject(current, "m_modelSkeleton")
	if !ok {
		return false
	}
	newNames, ok := controlsNames(newRig)
	if !ok {
		return false
	}
	meshSkeleton, ok := fieldObject(mesh, "m_skeleton")
	if !ok {
		return false
	}
	bones, ok := fieldArray(meshSkeleton, "m_bones")
	if !ok {
		return false
	}
	table, ok := fieldArray(model, "m_remappingTable")
	if !ok {
		return false
	}
	starts, ok := fieldArray(model, "m_remappingTableStarts")
	if !ok || len(starts) != 1 {
		return false
	}
	start, ok := starts[0].AsInt()
	if !ok || start != 0 || len(table) != len(bones) {
		return false
	}
	seen := make(map[string]struct{}, len(bones))
	for i := range bones {
		nameV, ok := bones[i].Get("m_boneName")
		if !ok {
			return false
		}
		name, ok := nameV.AsString()
		if !ok {
			return false
		}
		if _, dup := seen[name]; dup {
			return false
		}
		seen[name] = struct{}{}
		oldIndex, ok := oldNames[name]
		if !ok || i >= len(table) {
			return false
		}
		tableIndex, ok := table[i].AsInt()
		if !ok || tableIndex < 0 || int(tableIndex) != oldIndex {
			return false
		}
	}

	baseSkeleton, ok := fieldObject(baseMesh, "m_skeleton")
	if !ok {
		return false
	}
	baseBones, ok := fieldArray(baseSkeleton, "m_bones")
	if !ok {
		return false
	}
	baseAttachments, ok := fieldArray(baseMesh, "m_attachments")
	if !ok {
		return false
	}

	required := make(map[string]struct{})
	for _, pointName := range points {
		var point *kv3.Value
		for i := range baseAttachments {
			keyV, ok := baseAttachments[i].Get("key")
			if !ok {
				continue
			}
			key, ok := keyV.AsString()
			if ok && key == pointName {
				point, _ = baseAttachments[i].Get("value")
				break
			}
		}
		if point == nil {
			return false
		}
		countV, ok := point.Get("m_nInfluences")
		if !ok {
			return false
		}
		count64, ok := countV.AsInt()
		if !ok || count64 <= 0 || count64 > math.MaxInt {
			return false
		}
		roots, ok := fieldArray(point, "m_bInfluenceRootTransform")
		if !ok {
			return false
		}
		influences, ok := fieldArray(point, "m_influenceNames")
		if !ok {
			return false
		}
		for i := 0; i < int(count64); i++ {
			if i >= len(roots) || i >= len(influences) {
				return false
			}
			rooted, ok := roots[i].AsBool()
			if !ok {
				return false
			}
			if rooted {
				continue
			}
			name, ok := influences[i].AsString()
			if !ok {
				return false
			}
			required[name] = struct{}{}
		}
	}

	type control struct {
		name      string
		newIndex  int
		meshIndex int
	}
	var controls []control
	requiredNames := make([]string, 0, len(required))
	for name := range required {
		requiredNames = append(requiredNames, name)
	}
	sort.Strings(requiredNames)
	for _, name := range requiredNames {
		newIndex, ok := newNames[name]
		if !ok || !rootLeaf(newRig, newIndex) {
			return false
		}
		meshIndex := -1
		var baseBone *kv3.Value
		for i := range baseBones {
			nameV, ok := baseBones[i].Get("m_boneName")
			if !ok {
				continue
			}
			boneName, ok := nameV.AsString()
			if ok && boneName == name {
				meshIndex = i
				baseBone = &baseBones[i]
				break
			}
		}
		if baseBone == nil {
			return false
		}
		parentV, ok := baseBone.Get("m_parentName")
		if !ok {
			return false
		}
		parentName, ok := parentV.AsString()
		if !ok || parentName != "" {
			return false
		}
		sphereV, ok := baseBone.Get("m_flSphereRadius")
		if !ok {
			return false
		}
		sphere, ok := sphereV.AsFloat()
		if !ok || sphere != 0 {
			return false
		}
		bbox, ok := fieldObject(baseBone, "m_bbox")
		if !ok {
			return false
		}
		sizes, ok := fieldArray(bbox, "m_vecSize")
		if !ok {
			return false
		}
		for i := range sizes {
			size, ok := sizes[i].AsFloat()
			if !ok || size != 0 {
				return false
			}
		}
		if oldIndex, exists := oldNames[name]; exists {
			if !rootLeaf(oldRig, oldIndex) {
				return false
			}
			oldPose, ok1 := controlPose(oldRig, oldIndex)
			newPose, ok2 := controlPose(newRig, newIndex)
			if !ok1 || !ok2 {
				return false
			}
			if equal, valid := rig.EquivalentPose(&oldPose, &newPose); !valid || !equal {
				return false
			}
		}
		if _, already := seen[name]; !already {
			controls = append(controls, control{name: name, newIndex: newIndex, meshIndex: meshIndex})
		}
	}
	if len(controls) == 0 {
		return false
	}

	nextIndex := len(oldNames)
	for _, control := range controls {
		modelIndex := 0
		if index, ok := oldNames[control.name]; ok {
			modelIndex = index
		} else {
			for _, field := range boneFields {
				newValues, ok := fieldArray(newRig, field)
				if !ok || control.newIndex >= len(newValues) {
					return false
				}
				currentEnc, ok := currentEncoding.Get("m_modelSkeleton")
				if !ok {
					return false
				}
				encs, ok := currentEnc.Get(field)
				if !ok || encs.Children.Kind != kv3.ChildrenArray || control.newIndex >= len(encs.Children.Array) {
					return false
				}
				if !appendNested(model, modelEncoding, "m_modelSkeleton", field, newValues[control.newIndex],
					encs.Children.Array[control.newIndex]) {
					return false
				}
			}
			modelIndex = nextIndex
			nextIndex++
		}

		baseSkeletonEnc, ok := baseEncoding.Get("m_skeleton")
		if !ok {
			return false
		}
		baseBonesEnc, ok := baseSkeletonEnc.Get("m_bones")
		if !ok || baseBonesEnc.Children.Kind != kv3.ChildrenArray || control.meshIndex >= len(baseBonesEnc.Children.Array) {
			return false
		}
		if !appendNested(mesh, meshEncoding, "m_skeleton", "m_bones", baseBones[control.meshIndex],
			baseBonesEnc.Children.Array[control.meshIndex]) {
			return false
		}

		table, ok = fieldArray(model, "m_remappingTable")
		if !ok || len(table) == 0 {
			return false
		}
		var indexValue kv3.Value
		switch table[0].Kind {
		case kv3.KindInt:
			indexValue = kv3.Int(int64(modelIndex))
		case kv3.KindUInt:
			indexValue = kv3.UInt(uint64(modelIndex))
		default:
			return false
		}
		tableEnc, ok := modelEncoding.Get("m_remappingTable")
		if !ok || tableEnc.Children.Kind != kv3.ChildrenArray || len(tableEnc.Children.Array) == 0 {
			return false
		}
		if !appendField(model, modelEncoding, "m_remappingTable", indexValue, tableEnc.Children.Array[0]) {
			return false
		}
	}
	return true
}
