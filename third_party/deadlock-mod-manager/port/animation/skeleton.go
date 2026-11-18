// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_skeleton.rs
// (equivalent_rigs, equivalent_layout, canonical_masks and compatible) at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

var indexedSkeletonFields = [...]string{
	"m_boneIDs",
	"m_parentIndices",
	"m_parentSpaceReferencePose",
	"m_modelSpaceReferencePose",
	"m_maskDefinitions",
	"m_numBonesToSampleAtLowLOD",
}

var terminalSuffixes = [...]string{"_end", "_end_L", "_end_R"}

// RefreshSkeleton reports whether the mod skeleton describes the same rig as the
// current game skeleton. When it does, the current skeleton bytes are the safe
// replacement: authored masks and poses are proven equal after canonicalization.
// extension allows bones the game added below retained joints.
func RefreshSkeleton(source, base []byte, extension bool) ([]byte, bool) {
	_, old, _, err := camera.DecodeCompiled(source)
	if err != nil {
		return nil, false
	}
	_, current, _, err := camera.DecodeCompiled(base)
	if err != nil {
		return nil, false
	}
	if !EquivalentRigs(&old, &current, extension) {
		return nil, false
	}
	return base, true
}

// EquivalentRigs compares the semantic rig, ignoring index order and compiler
// float noise. Unknown metadata or a changed hierarchy fails closed.
func EquivalentRigs(old, current *kv3.Value, extension bool) bool {
	if !equivalentLayout(old, current, extension) {
		return false
	}
	_, oldIndices, ok := skeletonNames(old)
	if !ok {
		return false
	}
	currentNames, _, ok := skeletonNames(current)
	if !ok {
		return false
	}
	for currentIndex, name := range currentNames {
		oldIndex, exists := oldIndices[name]
		if !exists {
			continue
		}
		for _, field := range []string{"m_parentSpaceReferencePose", "m_modelSpaceReferencePose"} {
			oldPose, ok := skeletonArrayItem(old, field, oldIndex)
			if !ok {
				return false
			}
			currentPose, ok := skeletonArrayItem(current, field, currentIndex)
			if !ok {
				return false
			}
			match, comparable := rig.EquivalentPose(&oldPose, &currentPose)
			if !comparable || !match {
				return false
			}
		}
	}
	bones := make(map[string]struct{}, len(currentNames))
	for _, name := range currentNames {
		bones[name] = struct{}{}
	}
	oldMasks, ok := canonicalMasks(old, bones)
	if !ok {
		return false
	}
	currentMasks, ok := canonicalMasks(current, bones)
	if !ok {
		return false
	}
	if len(oldMasks) != len(currentMasks) {
		return false
	}
	for id, mask := range oldMasks {
		other, ok := currentMasks[id]
		if !ok || !camera.RuntimeValuesEqual(&mask, &other) {
			return false
		}
	}
	return true
}

func equivalentLayout(old, current *kv3.Value, extension bool) bool {
	oldNames, oldIndices, ok := skeletonNames(old)
	if !ok {
		return false
	}
	currentNames, currentIndices, ok := skeletonNames(current)
	if !ok {
		return false
	}
	for _, value := range []*kv3.Value{old, current} {
		if !compat.ValidSkeleton(value, "m_boneIDs", "m_parentIndices", []compat.PoseKey{
			{Key: "m_parentSpaceReferencePose", Width: 8},
			{Key: "m_modelSpaceReferencePose", Width: 8},
		}) {
			return false
		}
	}
	oldParents, ok := skeletonArray(old, "m_parentIndices")
	if !ok || len(oldParents) != len(oldNames) {
		return false
	}
	currentParents, ok := skeletonArray(current, "m_parentIndices")
	if !ok || len(currentParents) != len(currentNames) {
		return false
	}
	for name, index := range oldIndices {
		if _, exists := currentIndices[name]; exists {
			continue
		}
		parent, ok := oldParents[index].AsInt()
		if !ok {
			return false
		}
		parentName, hasParent := skeletonParentName(oldNames, parent)
		if !allowedRemoval(name, parentName) || !hasParent {
			return false
		}
		if _, exists := currentIndices[parentName]; !exists {
			return false
		}
		if skeletonHasChild(oldParents, index) {
			return false
		}
	}
	for name, currentIndex := range currentIndices {
		oldIndex, exists := oldIndices[name]
		if !exists {
			parent, ok := currentParents[currentIndex].AsInt()
			if !ok || parent < 0 || parent >= int64(len(currentNames)) {
				return false
			}
			root := int(parent)
			parentName := currentNames[root]
			rootParent, rootOK := currentParents[root].AsInt()
			knownRootAttachment := rootOK && name == "attachWorld" && rootParent == -1
			ancestor := root
			for {
				if _, ok := oldIndices[currentNames[ancestor]]; ok {
					break
				}
				next, ok := currentParents[ancestor].AsInt()
				if !ok || next < 0 || next >= int64(len(currentNames)) {
					break
				}
				ancestor = int(next)
			}
			if extension {
				if _, ok := oldIndices[currentNames[ancestor]]; ok {
					continue
				}
			}
			if _, ok := oldIndices[parentName]; !ok || !knownRootAttachment ||
				skeletonHasChild(currentParents, currentIndex) {
				return false
			}
			continue
		}
		oldParent, ok := skeletonParentName(oldNames, oldParents[oldIndex].Int)
		if !ok {
			return false
		}
		currentParent, ok := skeletonParentName(currentNames, currentParents[currentIndex].Int)
		if !ok || oldParent != currentParent {
			return false
		}
		if match, comparable := compatiblePoses(old, current, oldIndex, currentIndex,
			len(oldNames), len(currentNames)); !comparable || !match {
			return false
		}
	}
	fields, ok := old.AsObject()
	if !ok {
		return false
	}
	for _, field := range fields {
		if slices.Contains(indexedSkeletonFields[:], field.Key) {
			continue
		}
		currentValue, ok := current.Get(field.Key)
		if !ok || !camera.RuntimeValuesEqual(&field.Value, currentValue) {
			return false
		}
	}
	return true
}

func skeletonNames(value *kv3.Value) ([]string, map[string]int, bool) {
	values, ok := skeletonArray(value, "m_boneIDs")
	if !ok {
		return nil, nil, false
	}
	names := make([]string, len(values))
	indices := make(map[string]int, len(values))
	for i := range values {
		name, ok := values[i].AsString()
		if !ok {
			return nil, nil, false
		}
		if _, duplicate := indices[name]; duplicate {
			return nil, nil, false
		}
		names[i] = name
		indices[name] = i
	}
	if len(names) == 0 {
		return nil, nil, false
	}
	return names, indices, true
}

func skeletonArray(value *kv3.Value, field string) ([]kv3.Value, bool) {
	fieldValue, ok := value.Get(field)
	if !ok {
		return nil, false
	}
	return fieldValue.AsArray()
}

func skeletonArrayItem(value *kv3.Value, field string, index int) (kv3.Value, bool) {
	items, ok := skeletonArray(value, field)
	if !ok || index < 0 || index >= len(items) {
		return kv3.Value{}, false
	}
	return items[index], true
}

func skeletonParentName(names []string, parent int64) (string, bool) {
	if parent == -1 {
		return "", true
	}
	if parent < 0 || parent >= int64(len(names)) {
		return "", false
	}
	return names[parent], true
}

func skeletonHasChild(parents []kv3.Value, index int) bool {
	for i := range parents {
		if parent, ok := parents[i].AsInt(); ok && parent == int64(index) {
			return true
		}
	}
	return false
}

func allowedRemoval(name, parentName string) bool {
	for _, suffix := range terminalSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	if stem, suffix, ok := strings.Cut(name, "_TWIST"); ok {
		digits := strings.IndexFunc(suffix, func(r rune) bool {
			return r < '0' || r > '9'
		}) < 0
		if digits && parentName == stem {
			return true
		}
	}
	if name == "scapulaAimUp_JNT" && parentName == "spine_3" {
		return true
	}
	for _, side := range []string{"L", "R"} {
		if name == "knee_helper_"+side && parentName == "leg_upper_"+side {
			return true
		}
	}
	return false
}

// compatiblePoses checks one retained bone's local and model-space drift,
// bounding the error that a changed parent transform can propagate.
func compatiblePoses(old, current *kv3.Value, oldIndex, currentIndex,
	oldBones, currentBones int) (bool, bool) {
	for _, field := range []string{"m_parentSpaceReferencePose", "m_modelSpaceReferencePose"} {
		oldItems, ok := skeletonArray(old, field)
		if !ok {
			return false, false
		}
		currentItems, ok := skeletonArray(current, field)
		if !ok || len(oldItems) != oldBones || len(currentItems) != currentBones {
			return false, true
		}
	}
	oldLocal, ok := skeletonArrayItem(old, "m_parentSpaceReferencePose", oldIndex)
	if !ok {
		return false, false
	}
	currentLocal, ok := skeletonArrayItem(current, "m_parentSpaceReferencePose", currentIndex)
	if !ok {
		return false, false
	}
	oldNumbers, ok := poseNumbers(&oldLocal)
	if !ok {
		return false, false
	}
	currentNumbers, ok := poseNumbers(&currentLocal)
	if !ok {
		return false, false
	}
	local, ok := poseDelta(oldNumbers, currentNumbers)
	if !ok {
		return false, false
	}
	if local.position > positionEpsilon || local.scale > scaleEpsilon ||
		local.rotation > rotationEpsilonRadians {
		return false, true
	}
	oldModel, ok := skeletonArrayItem(old, "m_modelSpaceReferencePose", oldIndex)
	if !ok {
		return false, false
	}
	currentModel, ok := skeletonArrayItem(current, "m_modelSpaceReferencePose", currentIndex)
	if !ok {
		return false, false
	}
	oldModelNumbers, ok := poseNumbers(&oldModel)
	if !ok {
		return false, false
	}
	currentModelNumbers, ok := poseNumbers(&currentModel)
	if !ok {
		return false, false
	}
	model, ok := poseDelta(oldModelNumbers, currentModelNumbers)
	if !ok {
		return false, false
	}
	oldParents, ok := skeletonArray(old, "m_parentIndices")
	if !ok || oldIndex >= len(oldParents) {
		return false, false
	}
	currentParents, ok := skeletonArray(current, "m_parentIndices")
	if !ok || currentIndex >= len(currentParents) {
		return false, false
	}
	oldParent, ok := oldParents[oldIndex].AsInt()
	if !ok {
		return false, false
	}
	currentParent, ok := currentParents[currentIndex].AsInt()
	if !ok {
		return false, false
	}
	positionBound := positionEpsilon
	if oldParent != -1 || currentParent != -1 {
		if oldParent < 0 || currentParent < 0 {
			return false, false
		}
		oldParentPose, ok := skeletonArrayItem(old, "m_modelSpaceReferencePose", int(oldParent))
		if !ok {
			return false, false
		}
		currentParentPose, ok := skeletonArrayItem(current, "m_modelSpaceReferencePose", int(currentParent))
		if !ok {
			return false, false
		}
		oldParentNumbers, ok := poseNumbers(&oldParentPose)
		if !ok {
			return false, false
		}
		currentParentNumbers, ok := poseNumbers(&currentParentPose)
		if !ok {
			return false, false
		}
		parent, ok := poseDelta(oldParentNumbers, currentParentNumbers)
		if !ok {
			return false, false
		}
		radius := max(length(oldNumbers[:3]), length(currentNumbers[:3]))
		scale := max(math.Abs(oldParentNumbers[3]), math.Abs(currentParentNumbers[3]))
		positionBound += parent.position + radius*(parent.rotation*scale+parent.scale) +
			local.position*scale
	}
	return model.position <= positionBound && model.scale <= scaleEpsilon &&
		model.rotation <= rotationEpsilonRadians, true
}

func canonicalMasks(value *kv3.Value, bones map[string]struct{}) (map[string]kv3.Value, bool) {
	items, ok := skeletonArray(value, "m_maskDefinitions")
	if !ok {
		return nil, false
	}
	masks := make(map[string]kv3.Value, len(items))
	for i := range items {
		mask := rig.Clone(&items[i])
		idValue, ok := mask.Get("m_ID")
		if !ok {
			return nil, false
		}
		id, ok := idValue.AsString()
		if !ok {
			return nil, false
		}
		id = strings.ToLower(id)
		if mask.Kind != kv3.KindObject {
			return nil, false
		}
		setObjectField(&mask, "m_ID", kv3.String(id))
		weightsValue, ok := mask.Get("m_primaryWeightList")
		if !ok || weightsValue.Kind != kv3.KindObject {
			return nil, false
		}
		weights := rig.Clone(weightsValue)
		names, ok := skeletonArray(&weights, "m_boneIDs")
		if !ok {
			return nil, false
		}
		values, ok := skeletonArray(&weights, "m_weights")
		if !ok || len(names) != len(values) {
			return nil, false
		}
		named := make(map[string]kv3.Value, len(names))
		for j := range names {
			name, ok := names[j].AsString()
			if !ok {
				return nil, false
			}
			if _, known := bones[name]; !known {
				continue
			}
			if _, duplicate := named[name]; duplicate {
				return nil, false
			}
			named[name] = values[j]
		}
		sortedNames := slices.Sorted(maps.Keys(named))
		nameValues := make([]kv3.Value, len(sortedNames))
		weightValues := make([]kv3.Value, len(sortedNames))
		for j, name := range sortedNames {
			nameValues[j] = kv3.String(name)
			weightValues[j] = named[name]
		}
		setObjectField(&weights, "m_boneIDs", kv3.Array(nameValues...))
		setObjectField(&weights, "m_weights", kv3.Array(weightValues...))
		setObjectField(&mask, "m_primaryWeightList", weights)
		canonicalizeObjectOrder(&mask)
		if _, duplicate := masks[id]; duplicate {
			return nil, false
		}
		masks[id] = mask
	}
	return masks, true
}

func canonicalizeObjectOrder(value *kv3.Value) {
	switch value.Kind {
	case kv3.KindObject:
		slices.SortFunc(value.Object, func(left, right kv3.Pair) int {
			return cmp.Compare(left.Key, right.Key)
		})
		for i := range value.Object {
			canonicalizeObjectOrder(&value.Object[i].Value)
		}
	case kv3.KindArray:
		for i := range value.Array {
			canonicalizeObjectOrder(&value.Array[i])
		}
	}
}
