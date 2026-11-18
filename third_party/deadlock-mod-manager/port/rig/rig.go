// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/asset_compatibility.rs
// (valid_skeleton), animation_model_evidence.rs (ModelRig) and
// animation_skeleton.rs (equivalent_pose) at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package rig exposes the skeleton and pose checks the camera repairs use.
package rig

import (
	"math"
	"sort"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// PoseKey describes a per-bone pose array and its required element width.
type PoseKey struct {
	Key   string
	Width int
}

// ValidSkeleton reports whether rig has unique non-empty bone names, matching
// parent and pose arrays, finite pose values and a cycle-free parent graph.
func ValidSkeleton(rig *kv3.Value, namesKey, parentsKey string, poseKeys []PoseKey) bool {
	namesV, ok := rig.Get(namesKey)
	if !ok {
		return false
	}
	names, ok := namesV.AsArray()
	if !ok {
		return false
	}
	parentsV, ok := rig.Get(parentsKey)
	if !ok {
		return false
	}
	parents, ok := parentsV.AsArray()
	if !ok {
		return false
	}
	unique := make(map[string]struct{}, len(names))
	for i := range names {
		name, ok := names[i].AsString()
		if !ok || name == "" {
			return false
		}
		unique[name] = struct{}{}
	}
	if len(unique) != len(names) || len(parents) != len(names) {
		return false
	}
	for _, pk := range poseKeys {
		posesV, ok := rig.Get(pk.Key)
		if !ok {
			return false
		}
		poses, ok := posesV.AsArray()
		if !ok || len(poses) != len(names) {
			return false
		}
		for i := range poses {
			if pk.Width == 0 {
				v, ok := poses[i].AsFloat()
				if !ok || !isFinite(v) {
					return false
				}
				continue
			}
			values, ok := poses[i].AsArray()
			if !ok || len(values) != pk.Width {
				return false
			}
			for j := range values {
				v, ok := values[j].AsFloat()
				if !ok || !isFinite(v) {
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
			if _, cycle := visiting[node]; cycle {
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

// ModelRig is a validated model skeleton with a name-to-index lookup.
type ModelRig struct {
	value   kv3.Value
	indices map[string]int
}

// Parse builds a ModelRig from a model's m_modelSkeleton.
func Parse(model *kv3.Value) (*ModelRig, bool) {
	valueV, ok := model.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	value := clone(valueV)
	if !ValidSkeleton(&value, "m_boneName", "m_nParent", []PoseKey{
		{Key: "m_bonePosParent", Width: 3},
		{Key: "m_boneRotParent", Width: 4},
		{Key: "m_boneScaleParent", Width: 0},
	}) {
		return nil, false
	}
	namesV, _ := value.Get("m_boneName")
	names, _ := namesV.AsArray()
	indices := make(map[string]int, len(names))
	for i := range names {
		name, ok := names[i].AsString()
		if !ok {
			return nil, false
		}
		indices[name] = i
	}
	if len(indices) == 0 {
		return nil, false
	}
	return &ModelRig{value: value, indices: indices}, true
}

// Names returns the bone names in sorted order.
func (r *ModelRig) Names() []string {
	out := make([]string, 0, len(r.indices))
	for name := range r.indices {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Parent returns the parent bone name, or "" for a root bone.
func (r *ModelRig) Parent(name string) (string, bool) {
	index, ok := r.indices[name]
	if !ok {
		return "", false
	}
	parentsV, ok := r.value.Get("m_nParent")
	if !ok {
		return "", false
	}
	parents, ok := parentsV.AsArray()
	if !ok || index >= len(parents) {
		return "", false
	}
	parent, ok := parents[index].AsInt()
	if !ok {
		return "", false
	}
	if parent == -1 {
		return "", true
	}
	if parent < 0 {
		return "", false
	}
	namesV, ok := r.value.Get("m_boneName")
	if !ok {
		return "", false
	}
	names, ok := namesV.AsArray()
	if !ok || parent >= int64(len(names)) {
		return "", false
	}
	return names[parent].AsString()
}

// Pose returns [position(3), scale, rotation(4)] for a bone.
func (r *ModelRig) Pose(name string) (kv3.Value, bool) {
	index, ok := r.indices[name]
	if !ok {
		return kv3.Value{}, false
	}
	position, ok := arrayAt(&r.value, "m_bonePosParent", index)
	if !ok {
		return kv3.Value{}, false
	}
	rotation, ok := arrayAt(&r.value, "m_boneRotParent", index)
	if !ok {
		return kv3.Value{}, false
	}
	scale, ok := elementAt(&r.value, "m_boneScaleParent", index)
	if !ok {
		return kv3.Value{}, false
	}
	items := make([]kv3.Value, 0, 8)
	items = append(items, position...)
	items = append(items, scale)
	items = append(items, rotation...)
	return kv3.Value{Kind: kv3.KindArray, Array: items}, true
}

func arrayAt(v *kv3.Value, key string, index int) ([]kv3.Value, bool) {
	element, ok := elementAt(v, key, index)
	if !ok {
		return nil, false
	}
	return element.AsArray()
}

func elementAt(v *kv3.Value, key string, index int) (kv3.Value, bool) {
	fieldV, ok := v.Get(key)
	if !ok {
		return kv3.Value{}, false
	}
	field, ok := fieldV.AsArray()
	if !ok || index < 0 || index >= len(field) {
		return kv3.Value{}, false
	}
	return field[index], true
}

// EquivalentPose compares two 8-element poses. Positions tolerate 0.001 and
// rotations tolerate q/-q within 0.00001. The second result is false when the
// values are not numeric arrays at all.
func EquivalentPose(old, current *kv3.Value) (bool, bool) {
	oldArr, ok := old.AsArray()
	if !ok {
		return false, false
	}
	currentArr, ok := current.AsArray()
	if !ok {
		return false, false
	}
	if len(oldArr) != 8 || len(currentArr) != 8 {
		return false, true
	}
	numbers := func(values []kv3.Value) ([]float64, bool) {
		out := make([]float64, len(values))
		for i := range values {
			v, ok := values[i].AsFloat()
			if !ok {
				return nil, false
			}
			out[i] = v
		}
		return out, true
	}
	a, ok := numbers(oldArr)
	if !ok {
		return false, false
	}
	b, ok := numbers(currentArr)
	if !ok {
		return false, false
	}
	for i := range a {
		if !isFinite(a[i]) || !isFinite(b[i]) {
			return false, true
		}
	}
	positionsMatch := true
	for i := 0; i < 4; i++ {
		if math.Abs(a[i]-b[i]) > 0.001 {
			positionsMatch = false
			break
		}
	}
	rotationsMatch := false
	for _, sign := range []float64{1, -1} {
		match := true
		for i := 4; i < 8; i++ {
			if math.Abs(a[i]-sign*b[i]) > 0.00001 {
				match = false
				break
			}
		}
		if match {
			rotationsMatch = true
			break
		}
	}
	return positionsMatch && rotationsMatch, true
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// Clone returns a deep copy, so callers can edit a decoded tree without
// touching the original.
func Clone(v *kv3.Value) kv3.Value { return clone(v) }

func clone(v *kv3.Value) kv3.Value {
	out := *v
	if v.Kind == kv3.KindBinary {
		out.Binary = append([]byte(nil), v.Binary...)
	}
	if v.Kind == kv3.KindArray {
		out.Array = make([]kv3.Value, len(v.Array))
		for i := range v.Array {
			out.Array[i] = clone(&v.Array[i])
		}
	}
	if v.Kind == kv3.KindObject {
		out.Object = make([]kv3.Pair, len(v.Object))
		for i := range v.Object {
			out.Object[i] = kv3.Pair{Key: v.Object[i].Key, Value: clone(&v.Object[i].Value)}
		}
	}
	return out
}

// ScaledPosesCompatible accepts a uniform size change across the whole sampled
// chain when every position and rotation stays consistent with one scale.
func ScaledPosesCompatible(poses [][2]kv3.Value) bool {
	pairs := make([][2][8]float64, 0, len(poses))
	for _, pair := range poses {
		oldPose, ok := poseValues(&pair[0])
		if !ok {
			return false
		}
		currentPose, ok := poseValues(&pair[1])
		if !ok {
			return false
		}
		pairs = append(pairs, [2][8]float64{oldPose, currentPose})
	}
	numerator := 0.0
	denominator := 0.0
	for _, pair := range pairs {
		for i := 0; i < 3; i++ {
			numerator += pair[0][i] * pair[1][i]
			denominator += pair[1][i] * pair[1][i]
		}
	}
	if denominator <= 1e-12 {
		return false
	}
	scale := numerator / denominator
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < 0.98 || scale > 1.02 {
		return false
	}
	for _, pair := range pairs {
		positionsMatch := true
		for i := 0; i < 3; i++ {
			if math.Abs(pair[0][i]-pair[1][i]*scale) > 0.002 {
				positionsMatch = false
				break
			}
		}
		rotationAndScale := pair[1]
		copy(rotationAndScale[:3], pair[0][:3])
		oldValue := poseValue(pair[0])
		modifiedValue := poseValue(rotationAndScale)
		match, comparable := EquivalentPose(&oldValue, &modifiedValue)
		if !positionsMatch || !comparable || !match {
			return false
		}
	}
	return true
}

func poseValues(pose *kv3.Value) ([8]float64, bool) {
	var out [8]float64
	values, ok := pose.AsArray()
	if !ok || len(values) != 8 {
		return out, false
	}
	for i := range values {
		value, ok := values[i].AsFloat()
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return out, false
		}
		out[i] = value
	}
	return out, true
}

func poseValue(values [8]float64) kv3.Value {
	items := make([]kv3.Value, len(values))
	for i, value := range values {
		items[i] = kv3.Double(value)
	}
	return kv3.Array(items...)
}
