// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_skeleton.rs
// (repair_with_evidence rebase path, added_bones) and animation_poses.rs
// (preserve) at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

// RebaseSkeleton keeps the mod's authored masks and reference poses, maps them
// onto the current skeleton, and returns the rebuilt resource. It is the
// fallback after the byte-free semantic refresh fails. No model evidence is
// used; extension permits game-added bones as leaves or whole branches.
func RebaseSkeleton(source, base []byte, extension bool) ([]byte, bool) {
	format, old, oldEncoding, err := camera.DecodeCompiled(source)
	if err != nil {
		return nil, false
	}
	_, current, currentEncoding, err := camera.DecodeCompiled(base)
	if err != nil {
		return nil, false
	}
	if EquivalentRigs(&old, &current, extension) {
		return base, true
	}
	if !equivalentLayout(&old, &current, extension) {
		return nil, false
	}
	added, ok := addedBones(&old, &current)
	if !ok {
		return nil, false
	}
	masks, maskEncoding, ok := rebaseMasks(&old, &current, &oldEncoding, &currentEncoding)
	if !ok {
		return nil, false
	}
	if !replaceValueField(&current, "m_maskDefinitions", masks) ||
		!replaceEncodingField(&currentEncoding, "m_maskDefinitions", maskEncoding) {
		return nil, false
	}
	if !preservePoses(&old, &oldEncoding, &current, &currentEncoding) {
		return nil, false
	}
	if extension && added {
		if consistent, known := SelfConsistent(&current); !known || !consistent {
			return nil, false
		}
	}
	payload, err := kv3.EncodePreserving(&current, &currentEncoding, format)
	if err != nil {
		return nil, false
	}
	parsed, err := resource.Parse(base)
	if err != nil {
		return nil, false
	}
	rebuilt, err := parsed.RebuildWithDataPreserving(payload)
	if err != nil {
		return nil, false
	}
	actualFormat, actual, actualEncoding, err := camera.DecodeCompiled(rebuilt)
	if err != nil {
		return nil, false
	}
	if actualFormat != format || !camera.RuntimeValuesEqual(&current, &actual) ||
		!kv3.EncodingPreserved(&current, &currentEncoding, &actualEncoding) {
		return nil, false
	}
	return rebuilt, true
}

// addedBones reports whether the current skeleton has bones the old one lacks,
// other than the known root attachment.
func addedBones(old, current *kv3.Value) (bool, bool) {
	names, ok := skeletonArray(old, "m_boneIDs")
	if !ok {
		return false, false
	}
	oldNames := make(map[string]struct{}, len(names))
	for i := range names {
		name, ok := names[i].AsString()
		if !ok {
			return false, false
		}
		oldNames[name] = struct{}{}
	}
	currentNames, ok := skeletonArray(current, "m_boneIDs")
	if !ok {
		return false, false
	}
	for i := range currentNames {
		name, ok := currentNames[i].AsString()
		if ok && name != "attachWorld" {
			if _, exists := oldNames[name]; !exists {
				return true, true
			}
		}
	}
	return false, true
}

// preservePoses copies the mod's accepted reference poses onto the same bone
// names in the current skeleton, keeping their original encodings.
func preservePoses(old *kv3.Value, oldEncoding *kv3.Encoding,
	current *kv3.Value, currentEncoding *kv3.Encoding) bool {
	oldNames, ok := skeletonArray(old, "m_boneIDs")
	if !ok {
		return false
	}
	indices := make(map[string]int, len(oldNames))
	for i := range oldNames {
		name, ok := oldNames[i].AsString()
		if !ok {
			return false
		}
		indices[name] = i
	}
	names, ok := skeletonArray(current, "m_boneIDs")
	if !ok {
		return false
	}
	for _, field := range []string{"m_parentSpaceReferencePose", "m_modelSpaceReferencePose"} {
		oldPoses, ok := skeletonArray(old, field)
		if !ok {
			return false
		}
		oldField, ok := oldEncoding.Get(field)
		if !ok {
			return false
		}
		oldEncodings, ok := oldField.AsArray()
		if !ok {
			return false
		}
		currentField, ok := current.Get(field)
		if !ok || currentField.Kind != kv3.KindArray {
			return false
		}
		currentEncodingField, ok := currentEncoding.Get(field)
		if !ok {
			return false
		}
		encodings, ok := currentEncodingField.AsArray()
		if !ok || len(oldPoses) != len(indices) || len(oldEncodings) != len(indices) ||
			len(currentField.Array) != len(names) || len(encodings) != len(names) {
			return false
		}
		for index := range names {
			name, ok := names[index].AsString()
			if !ok {
				return false
			}
			oldIndex, exists := indices[name]
			if !exists {
				continue
			}
			currentField.Array[index] = rig.Clone(&oldPoses[oldIndex])
			encodings[index] = *cloneEncoding(&oldEncodings[oldIndex])
		}
		currentEncodingField.Children.Array = encodings
	}
	return true
}

func replaceValueField(value *kv3.Value, key string, replacement kv3.Value) bool {
	if value.Kind != kv3.KindObject {
		return false
	}
	for i := range value.Object {
		if value.Object[i].Key == key {
			value.Object[i].Value = replacement
			return true
		}
	}
	return false
}

func replaceEncodingField(encoding *kv3.Encoding, key string, replacement kv3.Encoding) bool {
	if encoding.Children.Kind != kv3.ChildrenObject {
		return false
	}
	for i := range encoding.Children.Object {
		if encoding.Children.Object[i].Key == key {
			encoding.Children.Object[i].Value = replacement
			return true
		}
	}
	return false
}
