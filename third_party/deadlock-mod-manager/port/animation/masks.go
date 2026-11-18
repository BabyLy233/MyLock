// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_masks.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

// rebaseMasks preserves the mod's authored primary weights, maps them onto the
// current bone names, and keeps masks the current skeleton lacks. Secondary
// weight lists and custom mask metadata are not rebased.
func rebaseMasks(old, current *kv3.Value, oldEncoding, currentEncoding *kv3.Encoding) (kv3.Value, kv3.Encoding, bool) {
	oldBones, ok := boneSet(old)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	currentBones, ok := boneSet(current)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	oldMasks, ok := skeletonArray(old, "m_maskDefinitions")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	currentMasks, ok := skeletonArray(current, "m_maskDefinitions")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	oldField, ok := oldEncoding.Get("m_maskDefinitions")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	oldEncodings, ok := oldField.AsArray()
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	currentField, ok := currentEncoding.Get("m_maskDefinitions")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	currentEncodings, ok := currentField.AsArray()
	if !ok || len(oldMasks) != len(oldEncodings) || len(currentMasks) != len(currentEncodings) {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	oldIndices, ok := maskIndices(oldMasks)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	currentIndices, ok := maskIndices(currentMasks)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	var values []kv3.Value
	var encodings []kv3.Encoding
	for index := range currentMasks {
		id, ok := maskID(&currentMasks[index])
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		mask := &currentMasks[index]
		oldIndex, exists := oldIndices[id]
		if !exists {
			values = append(values, rig.Clone(mask))
			encodings = append(encodings, *cloneEncoding(&currentEncodings[index]))
			continue
		}
		source := &oldMasks[oldIndex]
		if !emptySecondary(source) || !emptySecondary(mask) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		if !matchingMetadata(source, mask, "m_ID", "m_primaryWeightList") {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		sourceWeights, ok := source.Get("m_primaryWeightList")
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		maskWeights, ok := mask.Get("m_primaryWeightList")
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		if !matchingMetadata(sourceWeights, maskWeights, "m_boneIDs", "m_weights") {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		value := rig.Clone(mask)
		encoding := *cloneEncoding(&currentEncodings[index])
		if !copyWeights(source, &oldEncodings[oldIndex], &value, &encoding,
			oldBones, currentBones, true) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		values = append(values, value)
		encodings = append(encodings, encoding)
	}
	// Extra mod masks remain available, with obsolete bone references removed.
	for _, id := range slices.Sorted(maps.Keys(oldIndices)) {
		if _, exists := currentIndices[id]; exists {
			continue
		}
		index := oldIndices[id]
		if !emptySecondary(&oldMasks[index]) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		value := rig.Clone(&oldMasks[index])
		encoding := *cloneEncoding(&oldEncodings[index])
		if !copyWeights(&oldMasks[index], &oldEncodings[index], &value, &encoding,
			oldBones, currentBones, false) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		values = append(values, value)
		encodings = append(encodings, encoding)
	}
	encoding := *cloneEncoding(currentField)
	encoding.Children = kv3.EncodingChildren{Kind: kv3.ChildrenArray, Array: encodings}
	return kv3.Array(values...), encoding, true
}

func boneSet(rigValue *kv3.Value) (map[string]struct{}, bool) {
	names, ok := skeletonArray(rigValue, "m_boneIDs")
	if !ok {
		return nil, false
	}
	bones := make(map[string]struct{}, len(names))
	for i := range names {
		name, ok := names[i].AsString()
		if !ok {
			return nil, false
		}
		bones[name] = struct{}{}
	}
	return bones, true
}

func maskIndices(masks []kv3.Value) (map[string]int, bool) {
	indices := make(map[string]int, len(masks))
	for i := range masks {
		id, ok := maskID(&masks[i])
		if !ok {
			return nil, false
		}
		if _, duplicate := indices[id]; duplicate {
			return nil, false
		}
		indices[id] = i
	}
	return indices, true
}

func maskID(mask *kv3.Value) (string, bool) {
	value, ok := mask.Get("m_ID")
	if !ok {
		return "", false
	}
	id, ok := value.AsString()
	if !ok {
		return "", false
	}
	return strings.ToLower(id), true
}

func emptySecondary(mask *kv3.Value) bool {
	value, ok := mask.Get("m_secondaryWeightLists")
	if !ok {
		return false
	}
	items, ok := value.AsArray()
	return ok && len(items) == 0
}

func matchingMetadata(old, current *kv3.Value, excluded ...string) bool {
	fields, ok := old.AsObject()
	if !ok {
		return false
	}
	for _, field := range fields {
		if slices.Contains(excluded, field.Key) {
			continue
		}
		value, ok := current.Get(field.Key)
		if !ok || !camera.RuntimeValuesEqual(&field.Value, value) {
			return false
		}
	}
	return true
}

func copyWeights(source *kv3.Value, sourceEncoding *kv3.Encoding,
	target *kv3.Value, targetEncoding *kv3.Encoding,
	oldBones, currentBones map[string]struct{}, inheritNew bool) bool {
	sourceWeights, ok := source.Get("m_primaryWeightList")
	if !ok {
		return false
	}
	sourceWeightsEncoding, ok := sourceEncoding.Get("m_primaryWeightList")
	if !ok {
		return false
	}
	var names, weights []kv3.Value
	var nameEncodings, weightEncodings []kv3.Encoding
	seen := make(map[string]struct{})
	appendList := func(value *kv3.Value, encoding *kv3.Encoding, newOnly bool) bool {
		boneNames, ok := value.Get("m_boneIDs")
		if !ok {
			return false
		}
		bones, ok := boneNames.AsArray()
		if !ok {
			return false
		}
		boneValues, ok := value.Get("m_weights")
		if !ok {
			return false
		}
		weightList, ok := boneValues.AsArray()
		if !ok {
			return false
		}
		nameFields, ok := encoding.Get("m_boneIDs")
		if !ok {
			return false
		}
		boneNameEncodings, ok := nameFields.AsArray()
		if !ok {
			return false
		}
		weightFields, ok := encoding.Get("m_weights")
		if !ok {
			return false
		}
		boneWeightEncodings, ok := weightFields.AsArray()
		if !ok || len(bones) != len(weightList) || len(bones) != len(boneNameEncodings) ||
			len(bones) != len(boneWeightEncodings) {
			return false
		}
		for i := range bones {
			name, ok := bones[i].AsString()
			if !ok {
				return false
			}
			weight, ok := weightList[i].AsFloat()
			if !ok || math.IsNaN(weight) || math.IsInf(weight, 0) {
				return false
			}
			_, known := currentBones[name]
			_, existed := oldBones[name]
			if !known || (newOnly && existed) {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				return false
			}
			seen[name] = struct{}{}
			names = append(names, rig.Clone(&bones[i]))
			weights = append(weights, rig.Clone(&weightList[i]))
			nameEncodings = append(nameEncodings, *cloneEncoding(&boneNameEncodings[i]))
			weightEncodings = append(weightEncodings, *cloneEncoding(&boneWeightEncodings[i]))
		}
		return true
	}
	if !appendList(sourceWeights, sourceWeightsEncoding, false) {
		return false
	}
	if inheritNew {
		targetWeights, ok := target.Get("m_primaryWeightList")
		if !ok {
			return false
		}
		targetWeightsEncoding, ok := targetEncoding.Get("m_primaryWeightList")
		if !ok {
			return false
		}
		if !appendList(targetWeights, targetWeightsEncoding, true) {
			return false
		}
	}
	targetWeights, ok := target.Get("m_primaryWeightList")
	if !ok || targetWeights.Kind != kv3.KindObject {
		return false
	}
	setObjectField(targetWeights, "m_boneIDs", kv3.Array(names...))
	setObjectField(targetWeights, "m_weights", kv3.Array(weights...))
	targetWeightsEncoding, ok := targetEncoding.Get("m_primaryWeightList")
	if !ok {
		return false
	}
	nameFields, ok := targetWeightsEncoding.Get("m_boneIDs")
	if !ok {
		return false
	}
	nameFields.Children = kv3.EncodingChildren{Kind: kv3.ChildrenArray, Array: nameEncodings}
	weightFields, ok := targetWeightsEncoding.Get("m_weights")
	if !ok {
		return false
	}
	weightFields.Children = kv3.EncodingChildren{Kind: kv3.ChildrenArray, Array: weightEncodings}
	return true
}
