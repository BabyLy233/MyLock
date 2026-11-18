// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_clips/layout.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"math"
	"slices"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

const minStaticAnchors = 8

// Mapping recovers the authored track order for a skeleton with more bones
// than the clip reaches. The result has one entry per skeleton bone: the old
// track index, or -1 when the clip has no track for that bone.
func Mapping(skeleton, model *kv3.Value, tracks []kv3.Value) ([]int, bool) {
	modelRig, ok := rig.Parse(model)
	if !ok {
		return nil, false
	}
	namesValue, ok := skeleton.Get("m_boneIDs")
	if !ok {
		return nil, false
	}
	nameValues, ok := namesValue.AsArray()
	if !ok {
		return nil, false
	}
	names := make([]string, len(nameValues))
	for i := range nameValues {
		name, ok := nameValues[i].AsString()
		if !ok {
			return nil, false
		}
		names[i] = name
	}
	if len(slices.Compact(slices.Sorted(slices.Values(names)))) != len(names) {
		return nil, false
	}
	parentsValue, ok := skeleton.Get("m_parentIndices")
	if !ok {
		return nil, false
	}
	parents, ok := parentsValue.AsArray()
	if !ok {
		return nil, false
	}
	posesValue, ok := skeleton.Get("m_parentSpaceReferencePose")
	if !ok {
		return nil, false
	}
	poses, ok := posesValue.AsArray()
	if !ok || len(parents) != len(names) || len(poses) != len(names) {
		return nil, false
	}
	present := make([]bool, len(names))
	sampled := 0
	for i, name := range names {
		_, parentOK := modelRig.Parent(name)
		present[i] = parentOK
		if parentOK {
			sampled++
		}
	}
	if sampled != len(tracks) || len(tracks) >= len(names) {
		return nil, false
	}
	identity := kv3.Array(
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
	)
	children := make([][]int, len(names))
	root := -1
	for index, name := range names {
		if _, ok := poseNumbers(&poses[index]); !ok {
			return nil, false
		}
		parentValue, ok := parents[index].AsInt()
		if !ok || parentValue < -1 || parentValue >= int64(index) {
			return nil, false
		}
		if !present[index] {
			continue
		}
		parent := int(parentValue)
		for parent >= 0 && !present[parent] {
			if match, comparable := rig.EquivalentPose(&poses[parent], &identity); !comparable || !match {
				return nil, false
			}
			next, ok := parents[parent].AsInt()
			if !ok || next < -1 || next >= int64(parent) {
				return nil, false
			}
			parent = int(next)
		}
		parentName := ""
		if parent >= 0 {
			parentName = names[parent]
		}
		if modelParent, ok := modelRig.Parent(name); !ok || modelParent != parentName {
			return nil, false
		}
		if parent >= 0 {
			children[parent] = append(children[parent], index)
		} else if root != -1 {
			return nil, false
		} else {
			root = index
		}
	}
	if root != 0 {
		return nil, false
	}
	modelField, ok := model.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	authoredValue, ok := modelField.Get("m_boneName")
	if !ok {
		return nil, false
	}
	authored, ok := authoredValue.AsArray()
	if !ok {
		return nil, false
	}
	for node := range children {
		slices.SortStableFunc(children[node], func(left, right int) int {
			leftPos := authoredPosition(authored, names[left])
			rightPos := authoredPosition(authored, names[right])
			switch {
			case leftPos < rightPos:
				return -1
			case leftPos > rightPos:
				return 1
			}
			return 0
		})
	}
	anchorOrder, ok := anchors(modelRig, names, present, tracks)
	if !ok {
		return nil, false
	}
	anchored := 0
	for _, anchor := range anchorOrder {
		if anchor != -1 {
			anchored++
		}
	}
	threshold := max(minStaticAnchors, (len(tracks)+1)/2)
	if anchored < threshold {
		return nil, false
	}
	lowCount, ok := lowBoneCount(skeleton, len(names))
	if !ok {
		return nil, false
	}
	oldLowCount := 0
	for _, sampled := range present[:lowCount] {
		if sampled {
			oldLowCount++
		}
	}
	order, ok := newLayoutTree(children, anchorOrder, lowCount).solve(root, 0, oldLowCount)
	if !ok {
		return nil, false
	}
	result := make([]int, len(names))
	for i := range result {
		result[i] = -1
	}
	for old, bone := range order {
		result[bone] = old
	}
	return result, true
}

func authoredPosition(authored []kv3.Value, name string) int {
	for i := range authored {
		if value, ok := authored[i].AsString(); ok && value == name {
			return i
		}
	}
	return -1
}

func lowBoneCount(skeleton *kv3.Value, bones int) (int, bool) {
	value, ok := skeleton.Get("m_numBonesToSampleAtLowLOD")
	if !ok {
		return bones, true
	}
	low, ok := value.AsUInt()
	if !ok || low == 0 || low > uint64(bones) {
		return 0, false
	}
	return int(low), true
}

func anchors(modelRig *rig.ModelRig, names []string, present []bool, tracks []kv3.Value) ([]int, bool) {
	poses := make([]*[8]float64, len(names))
	for i, name := range names {
		if !present[i] {
			continue
		}
		pose, ok := modelRig.Pose(name)
		if !ok {
			return nil, false
		}
		numbers, ok := poseNumbers(&pose)
		if !ok {
			return nil, false
		}
		poses[i] = new(numbers)
	}
	candidates := make([][]int, len(names))
	for old := range tracks {
		static := true
		for _, field := range []string{
			"m_bIsTranslationStatic",
			"m_bIsRotationStatic",
			"m_bIsScaleStatic",
		} {
			value, ok := tracks[old].Get(field)
			if !ok {
				return nil, false
			}
			flag, ok := value.AsBool()
			if !ok || !flag {
				static = false
				break
			}
		}
		if !static {
			continue
		}
		var pose []float64
		for _, axis := range []string{"X", "Y", "Z"} {
			start, ok := rangeStart(&tracks[old], "m_translationRange"+axis)
			if !ok {
				return nil, false
			}
			pose = append(pose, start)
		}
		if math.Abs(pose[0]) < 0.0001 && math.Abs(pose[1]) < 0.0001 && math.Abs(pose[2]) < 0.0001 {
			continue
		}
		scale, ok := rangeStart(&tracks[old], "m_scaleRange")
		if !ok {
			return nil, false
		}
		pose = append(pose, scale)
		rotationValue, ok := tracks[old].Get("m_constantRotation")
		if !ok {
			return nil, false
		}
		rotation, ok := clipNumbers(rotationValue, 4)
		if !ok {
			return nil, false
		}
		pose = append(pose, rotation...)
		matching := make([]int, 0, 1)
		for bone := range poses {
			if poses[bone] != nil && poseMatches(pose, poses[bone][:]) {
				matching = append(matching, bone)
			}
		}
		if len(matching) == 1 {
			candidates[matching[0]] = append(candidates[matching[0]], old)
		}
	}
	result := make([]int, len(names))
	for i := range result {
		result[i] = -1
		if len(candidates[i]) == 1 {
			result[i] = candidates[i][0]
		}
	}
	// NM clips always start with the root; its animation frame can differ from
	// the deformation skeleton's coordinate frame.
	result[0] = 0
	return result, true
}

func rangeStart(track *kv3.Value, field string) (float64, bool) {
	rangeValue, ok := track.Get(field)
	if !ok {
		return 0, false
	}
	startValue, ok := rangeValue.Get("m_flRangeStart")
	if !ok {
		return 0, false
	}
	return startValue.AsFloat()
}

func poseMatches(a, b []float64) bool {
	if len(a) != 8 || len(b) != 8 {
		return false
	}
	for _, values := range [][]float64{a, b} {
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
		}
	}
	for i := 0; i < 3; i++ {
		if math.Abs(a[i]-b[i]) > 0.005 {
			return false
		}
	}
	if math.Abs(a[3]-b[3]) > 0.001 {
		return false
	}
	norm := func(values []float64) float64 {
		sum := 0.0
		for _, value := range values {
			sum += value * value
		}
		return math.Sqrt(sum)
	}
	denominator := norm(a[4:]) * norm(b[4:])
	if denominator <= 0.0001 {
		return false
	}
	dot := 0.0
	for i := 4; i < 8; i++ {
		dot += a[i] * b[i]
	}
	cosine := math.Abs(dot / denominator)
	if cosine > 1 {
		cosine = 1
	}
	return 2*math.Acos(cosine) <= 0.002
}
