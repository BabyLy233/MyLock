// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/asset_compatibility.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

// Package compat holds the structural asset checks that do not repair
// anything: invalid skeletons, bone remapping tables, mesh attachments,
// hitboxes and missing resource references.
package compat

import (
	"math"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// WarningKind identifies a structural asset problem.
type WarningKind string

const (
	WarningUnverifiedSkeleton        WarningKind = "UnverifiedSkeleton"
	WarningInvalidSkeleton           WarningKind = "InvalidSkeleton"
	WarningInvalidBoneRemapping      WarningKind = "InvalidBoneRemapping"
	WarningInvalidAttachment         WarningKind = "InvalidAttachment"
	WarningInvalidHitbox             WarningKind = "InvalidHitbox"
	WarningMissingResource           WarningKind = "MissingResource"
	WarningMissingCameraInterface    WarningKind = "MissingCameraInterface"
	WarningMissingCameraAttachments  WarningKind = "MissingCameraAttachments"
	WarningOutdatedCameraAttachments WarningKind = "OutdatedCameraAttachments"
	WarningMissingSoundBindings      WarningKind = "MissingSoundBindings"
	WarningMissingAnimationInterface WarningKind = "MissingAnimationInterface"
	WarningAnimationMapping          WarningKind = "AnimationMapping"
	WarningUnreadableResource        WarningKind = "UnreadableResource"
	WarningMaterialMapping           WarningKind = "MaterialMapping"
	WarningMaterialInterface         WarningKind = "MaterialInterface"
	WarningModelMapping              WarningKind = "ModelMapping"
	WarningOverlappingModel          WarningKind = "OverlappingModel"
)

// Warning is one structural problem at one resource path.
type Warning struct {
	Kind   WarningKind
	Detail string
}

// PoseKey names a reference-pose field and its expected width. Width 0 means a
// scalar; a positive width means an array of that many finite numbers.
type PoseKey struct {
	Key   string
	Width int
}

// IsMeshPath reports whether path is a models/**.vmesh_c path with no empty,
// dot or parent segment.
func IsMeshPath(path string) bool {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	if !strings.HasPrefix(path, "models/") || !strings.HasSuffix(path, ".vmesh_c") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// ValidSkeleton checks a rig: unique non-empty names, one parent per bone, in
// range or -1, no cycles, and complete finite reference poses.
func ValidSkeleton(rig *kv3.Value, namesKey, parentsKey string, poses []PoseKey) bool {
	namesValue, ok := rig.Get(namesKey)
	if !ok {
		return false
	}
	names, ok := namesValue.AsArray()
	if !ok {
		return false
	}
	parentsValue, ok := rig.Get(parentsKey)
	if !ok {
		return false
	}
	parents, ok := parentsValue.AsArray()
	if !ok {
		return false
	}
	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		value, ok := name.AsString()
		if !ok {
			// Upstream filters non-strings out, which makes the count disagree
			// with the array length and fails the check below.
			continue
		}
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	if len(unique) != len(names) || len(parents) != len(names) {
		return false
	}
	for _, pose := range poses {
		valuesValue, ok := rig.Get(pose.Key)
		if !ok {
			return false
		}
		values, ok := valuesValue.AsArray()
		if !ok || len(values) != len(names) {
			return false
		}
		for _, value := range values {
			if pose.Width == 0 {
				if number, ok := value.AsFloat(); !ok || !finite(number) {
					return false
				}
				continue
			}
			items, ok := value.AsArray()
			if !ok || len(items) != pose.Width {
				return false
			}
			for _, item := range items {
				number, ok := item.AsFloat()
				if !ok || !finite(number) {
					return false
				}
			}
		}
	}
	finished := make([]bool, len(names))
	for start := range names {
		visiting := make(map[int]struct{})
		node := start
		for !finished[node] {
			if _, seen := visiting[node]; seen {
				return false
			}
			visiting[node] = struct{}{}
			parent, ok := parents[node].AsInt()
			if !ok {
				return false
			}
			if parent == -1 {
				break
			}
			if parent < 0 || parent >= int64(len(names)) {
				return false
			}
			node = int(parent)
		}
		for node := range visiting {
			finished[node] = true
		}
	}
	return true
}

// ValidRemapping checks that the remapping table indexes the model's bones and
// that the starts are a sorted set of offsets into it.
func ValidRemapping(model, rig *kv3.Value) bool {
	table := []kv3.Value(nil)
	if value, ok := model.Get("m_remappingTable"); ok {
		var isArray bool
		table, isArray = value.AsArray()
		if !isArray {
			return false
		}
	}
	namesValue, ok := rig.Get("m_boneName")
	if !ok {
		return false
	}
	names, ok := namesValue.AsArray()
	if !ok {
		return false
	}
	for _, value := range table {
		index, ok := value.AsInt()
		if !ok || index < 0 || index >= int64(len(names)) {
			return false
		}
	}
	startsValue, ok := model.Get("m_remappingTableStarts")
	if !ok {
		return len(table) == 0
	}
	starts, ok := startsValue.AsArray()
	if !ok {
		return false
	}
	previous := int64(0)
	for _, value := range starts {
		start, ok := value.AsInt()
		if !ok || start < previous || start > int64(len(table)) {
			return false
		}
		previous = start
	}
	return true
}

// MeshWarnings checks one decoded mesh against the model's bone names.
func MeshWarnings(mesh *kv3.Value, modelBones map[string]struct{}) []Warning {
	var warnings []Warning
	bones, ok := meshBones(mesh)
	if !ok {
		return warnings
	}
	names := make(map[string]struct{}, len(bones)+len(modelBones))
	for _, bone := range bones {
		if name, ok := bone.Get("m_boneName"); ok {
			if value, ok := name.AsString(); ok {
				names[value] = struct{}{}
			}
		}
	}
	uniqueMeshBones := len(names) == len(bones)
	for name := range modelBones {
		names[name] = struct{}{}
	}
	invalid := !uniqueMeshBones
	if !invalid {
		for _, bone := range bones {
			parent, ok := bone.Get("m_parentName")
			if !ok {
				invalid = true
				break
			}
			name, ok := parent.AsString()
			if !ok || (name != "" && !contains(names, name)) {
				invalid = true
				break
			}
		}
	}
	if invalid {
		warnings = append(warnings, Warning{Kind: WarningInvalidSkeleton, Detail: "m_skeleton.m_bones"})
	}

	if attachmentsValue, ok := mesh.Get("m_attachments"); ok {
		if attachments, ok := attachmentsValue.AsArray(); ok {
			for _, attachment := range attachments {
				label := "m_attachments"
				if key, ok := attachment.Get("key"); ok {
					if value, ok := key.AsString(); ok {
						label = value
					}
				}
				if !validAttachment(&attachment, names) {
					warnings = append(warnings, Warning{Kind: WarningInvalidAttachment, Detail: label})
				}
			}
		}
	}

	if setsValue, ok := mesh.Get("m_hitboxsets"); ok {
		if sets, ok := setsValue.AsArray(); ok {
			for _, set := range sets {
				value, ok := set.Get("value")
				if !ok {
					continue
				}
				hitboxesValue, ok := value.Get("m_HitBoxes")
				if !ok {
					continue
				}
				hitboxes, ok := hitboxesValue.AsArray()
				if !ok {
					continue
				}
				for _, hitbox := range hitboxes {
					nameValue, ok := hitbox.Get("m_sBoneName")
					if !ok {
						continue
					}
					name, ok := nameValue.AsString()
					if !ok || name == "" {
						continue
					}
					if !contains(names, name) {
						warnings = append(warnings, Warning{Kind: WarningInvalidHitbox, Detail: name})
					}
				}
			}
		}
	}
	return warnings
}

func meshBones(mesh *kv3.Value) ([]kv3.Value, bool) {
	skeletonValue, ok := mesh.Get("m_skeleton")
	if !ok {
		return nil, false
	}
	bonesValue, ok := skeletonValue.Get("m_bones")
	if !ok {
		return nil, false
	}
	bones, ok := bonesValue.AsArray()
	if !ok {
		return nil, false
	}
	return bones, true
}

func validAttachment(attachment *kv3.Value, names map[string]struct{}) bool {
	value, ok := attachment.Get("value")
	if !ok {
		return false
	}
	countValue, ok := value.Get("m_nInfluences")
	if !ok {
		return false
	}
	count, ok := countValue.AsInt()
	if !ok || count < 0 {
		return false
	}
	influencesValue, ok := value.Get("m_influenceNames")
	if !ok {
		return false
	}
	influences, ok := influencesValue.AsArray()
	if !ok {
		return false
	}
	rootsValue, ok := value.Get("m_bInfluenceRootTransform")
	if !ok {
		return false
	}
	roots, ok := rootsValue.AsArray()
	if !ok {
		return false
	}
	weightsValue, ok := value.Get("m_influenceWeights")
	if !ok {
		return false
	}
	weights, ok := weightsValue.AsArray()
	if !ok {
		return false
	}
	if count > int64(len(influences)) || count > int64(len(roots)) || count > int64(len(weights)) {
		return false
	}
	for i := 0; i < int(count); i++ {
		rooted, _ := roots[i].AsBool()
		weight, ok := weights[i].AsFloat()
		if !ok || !finite(weight) || weight < 0 {
			return false
		}
		if rooted {
			continue
		}
		name, ok := influences[i].AsString()
		if !ok || !contains(names, name) {
			return false
		}
	}
	return true
}

// MissingAnimationFields lists animation interfaces the current game model has
// and the mod model lacks. A missing list has no safe repair of its own.
func MissingAnimationFields(source, current *kv3.Value) []string {
	var missing []string
	for _, field := range []string{"m_animGraph2Refs", "m_vecNmSkeletonRefs"} {
		currentValue, ok := current.Get(field)
		if !ok {
			continue
		}
		currentItems, ok := currentValue.AsArray()
		if !ok || len(currentItems) == 0 {
			continue
		}
		sourceValue, ok := source.Get(field)
		if !ok {
			missing = append(missing, field)
			continue
		}
		sourceItems, ok := sourceValue.AsArray()
		if !ok || len(sourceItems) == 0 {
			missing = append(missing, field)
		}
	}
	return missing
}

func contains(set map[string]struct{}, value string) bool {
	_, ok := set[value]
	return ok
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
