// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation.rs
// (prove_current_bindings and its helpers), animation_poses.rs (pose
// consistency), and model_animation/scaled_rig.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package animation ports the animation-preservation proofs used by the
// compatibility repairs. It verifies that a mod's sampled rig still matches the
// current game model before bindings are moved or restored.
package animation

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modeltext"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

const (
	positionEpsilon         = 0.001
	scaleEpsilon            = 0.001
	rotationEpsilonRadians  = 0.0002
	propagatedPositionBound = 0.003
)

var (
	nMSkeletonRefs  = "m_vecNmSkeletonRefs"
	animationGraphs = "m_animGraph2Refs"
)

// Source resolves resources in the profile's load order. FromMod reports
// whether the effective provider is a mod rather than the game.
type Source interface {
	Resolve(path string) ([]byte, error)
	FromMod(path string) bool
}

// Pose is one bone's authored and current reference pose.
type Pose struct {
	Name    string
	Old     kv3.Value
	Current kv3.Value
}

// Graphs returns the model's NM graph identifiers mapped to normalized graph
// paths.
func Graphs(model *kv3.Value) (map[string]string, bool) {
	graphsValue, ok := model.Get(animationGraphs)
	if !ok {
		return nil, false
	}
	items, ok := graphsValue.AsArray()
	if !ok {
		return nil, false
	}
	graphs := make(map[string]string, len(items))
	for i := range items {
		nameValue, ok := items[i].Get("m_sIdentifier")
		if !ok {
			return nil, false
		}
		name, ok := nameValue.AsString()
		if !ok {
			return nil, false
		}
		pathValue, ok := items[i].Get("m_hGraph")
		if !ok {
			return nil, false
		}
		path, ok := pathValue.AsString()
		if !ok {
			return nil, false
		}
		path = camera.NormalizePath(path)
		name = strings.ToLower(name)
		if path == "" {
			return nil, false
		}
		if _, duplicate := graphs[name]; duplicate {
			return nil, false
		}
		graphs[name] = path
	}
	if len(graphs) == 0 {
		return nil, false
	}
	return graphs, true
}

// LegacyAnimationGraph returns the authored legacy graph reference, when the
// model text still carries one.
func LegacyAnimationGraph(model *kv3.Value) (string, bool) {
	info, ok := model.Get("m_modelInfo")
	if !ok || info.Kind != kv3.KindObject {
		return "", false
	}
	textValue, ok := info.Get("m_keyValueText")
	if !ok {
		return "", false
	}
	text, ok := textValue.AsString()
	if !ok {
		return "", false
	}
	_, fields, ok := modeltext.RootFields(text)
	if !ok {
		return "", false
	}
	field, ok := modeltext.Field(fields, "anim_graph_resource")
	if !ok {
		return "", false
	}
	value, ok := modeltext.AssignedValue(text[field.Start:field.End])
	if !ok {
		return "", false
	}
	name, ok := modeltext.ResourceName(value)
	if !ok || strings.ContainsAny(name, "\"\\") {
		return "", false
	}
	return name, true
}

// ProveCurrentBindings reports whether moving the mod's model to the current
// game model preserves its animation bindings. blocks is true when the mod
// ships animation assets that need a dependency proof.
func ProveCurrentBindings(source, current *kv3.Value, blocks bool, resolve Source) error {
	if blocks {
		return fmt.Errorf("Existing animation bindings do not match the current game")
	}
	sourceGraphs, sourceOK := Graphs(source)
	currentGraphs, currentOK := Graphs(current)
	if !sourceOK || !currentOK || !equalGraphs(sourceGraphs, currentGraphs) {
		return fmt.Errorf("Existing animation bindings do not match the current game")
	}
	if !equalOptional(source, current, nMSkeletonRefs) {
		return fmt.Errorf("Existing animation bindings do not match the current game")
	}
	if _, legacy := LegacyAnimationGraph(source); legacy {
		return fmt.Errorf("Existing animation bindings do not match the current game")
	}
	for _, field := range []string{"m_refAnimGroups", "m_refSequenceGroups"} {
		if value, ok := source.Get(field); ok {
			array, ok := value.AsArray()
			if !ok || len(array) > 0 {
				return fmt.Errorf("Existing animation bindings do not match the current game")
			}
		}
	}
	reference, err := singleSkeletonReference(current)
	if err != nil {
		return err
	}
	skeletonBytes, err := resolve.Resolve(reference)
	if err != nil {
		return err
	}
	if skeletonBytes == nil || resolve.FromMod(reference) {
		return fmt.Errorf("Current sampling skeleton is unavailable or overridden")
	}
	for _, graph := range sortedGraphPaths(currentGraphs) {
		if resolve.FromMod(graph) {
			return fmt.Errorf("Current animation graph is unavailable or overridden: %s", graph)
		}
		bytes, err := resolve.Resolve(graph)
		if err != nil {
			return err
		}
		if bytes == nil {
			return fmt.Errorf("Current animation graph is unavailable or overridden: %s", graph)
		}
	}
	skeleton, err := decodeValue(skeletonBytes)
	if err != nil {
		return err
	}
	return proveSampledRig(source, current, &skeleton)
}

func singleSkeletonReference(model *kv3.Value) (string, error) {
	refsValue, ok := model.Get(nMSkeletonRefs)
	if !ok {
		return "", fmt.Errorf("Current sampling skeleton is unavailable")
	}
	refs, ok := refsValue.AsArray()
	if !ok || len(refs) != 1 {
		return "", fmt.Errorf("Multiple sampling skeletons require additional proof")
	}
	name, ok := refs[0].AsString()
	if !ok {
		return "", fmt.Errorf("Invalid skeleton path")
	}
	return name, nil
}

func equalOptional(left, right *kv3.Value, field string) bool {
	leftValue, leftOK := left.Get(field)
	rightValue, rightOK := right.Get(field)
	if leftOK != rightOK {
		return false
	}
	if !leftOK {
		return true
	}
	return kv3.Equal(leftValue, rightValue)
}

func equalGraphs(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if other, ok := right[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func sortedGraphPaths(graphs map[string]string) []string {
	return slices.Sorted(maps.Values(graphs))
}

func decodeValue(bytes []byte) (kv3.Value, error) {
	_, value, _, err := camera.DecodeCompiled(bytes)
	return value, err
}

func proveSampledRig(old, current, skeleton *kv3.Value) error {
	poses, err := sampledContract(old, current, skeleton)
	if err != nil {
		return err
	}
	different := false
	for i := range poses {
		match, comparable := rig.EquivalentPose(&poses[i].Old, &poses[i].Current)
		if comparable && match {
			continue
		}
		different = true
		break
	}
	if different && !ScaledPosesCompatible(poses) {
		for i := range poses {
			match, comparable := rig.EquivalentPose(&poses[i].Old, &poses[i].Current)
			if !comparable || !match {
				return fmt.Errorf("Changed sampled bone reference pose: %s", poses[i].Name)
			}
		}
	}
	return nil
}

func sampledContract(oldValue, currentValue, skeleton *kv3.Value) ([]Pose, error) {
	oldRig, ok := rig.Parse(oldValue)
	if !ok {
		return nil, fmt.Errorf("Invalid source model rig")
	}
	currentRig, ok := rig.Parse(currentValue)
	if !ok {
		return nil, fmt.Errorf("Invalid current model rig")
	}
	if !compat.ValidSkeleton(skeleton, "m_boneIDs", "m_parentIndices",
		[]compat.PoseKey{
			{Key: "m_parentSpaceReferencePose", Width: 8},
			{Key: "m_modelSpaceReferencePose", Width: 8},
		}) {
		return nil, fmt.Errorf("Invalid current animation rig")
	}
	samplesValue, ok := skeleton.Get("m_boneIDs")
	if !ok {
		return nil, fmt.Errorf("Missing sampled bones")
	}
	samples, ok := samplesValue.AsArray()
	if !ok || len(samples) == 0 {
		return nil, fmt.Errorf("Empty sampled rig")
	}
	oldNames, ok := lowerCaseNames(oldRig)
	if !ok {
		return nil, fmt.Errorf("Ambiguous source bone names")
	}
	currentNames, ok := lowerCaseNames(currentRig)
	if !ok {
		return nil, fmt.Errorf("Ambiguous current bone names")
	}
	unused, ok := unmappedReferenceControls(skeleton, oldNames, currentNames)
	if !ok {
		unused = map[string]struct{}{}
	}
	checked := make(map[string]struct{})
	var poses []Pose
	for i := range samples {
		name, ok := samples[i].AsString()
		if !ok {
			return nil, fmt.Errorf("Invalid sampled bone name")
		}
		lower := strings.ToLower(name)
		currentName, ok := currentNames[lower]
		if !ok {
			if _, skip := unused[lower]; skip {
				continue
			}
			return nil, fmt.Errorf("Current model lacks sampled bone: %s", lower)
		}
		sourceName, ok := oldNames[lower]
		if !ok {
			return nil, fmt.Errorf("Missing sampled bone: %s", lower)
		}
		for sourceName != "" {
			if _, seen := checked[sourceName]; seen {
				break
			}
			checked[sourceName] = struct{}{}
			if !strings.EqualFold(sourceName, currentName) {
				return nil, fmt.Errorf("Changed sampled bone hierarchy: %s", sourceName)
			}
			oldPose, ok := oldRig.Pose(sourceName)
			if !ok {
				return nil, fmt.Errorf("Invalid source pose")
			}
			currentPose, ok := currentRig.Pose(currentName)
			if !ok {
				return nil, fmt.Errorf("Invalid current pose")
			}
			poses = append(poses, Pose{Name: sourceName, Old: oldPose, Current: currentPose})
			sourceName, ok = oldRig.Parent(sourceName)
			if !ok {
				return nil, fmt.Errorf("Invalid source bone parent")
			}
			currentName, ok = currentRig.Parent(currentName)
			if !ok {
				return nil, fmt.Errorf("Invalid current bone parent")
			}
			if (sourceName == "") != (currentName == "") {
				return nil, fmt.Errorf("Changed sampled bone root")
			}
		}
	}
	return poses, nil
}

func lowerCaseNames(modelRig *rig.ModelRig) (map[string]string, bool) {
	names := modelRig.Names()
	mapped := make(map[string]string, len(names))
	for _, name := range names {
		mapped[strings.ToLower(name)] = name
	}
	return mapped, len(mapped) == len(names)
}

func unmappedReferenceControls(skeleton *kv3.Value, sourceNames, currentNames map[string]string) (map[string]struct{}, bool) {
	if consistent, ok := SelfConsistent(skeleton); !ok || !consistent {
		return nil, false
	}
	namesValue, ok := skeleton.Get("m_boneIDs")
	if !ok {
		return nil, false
	}
	namesValues, ok := namesValue.AsArray()
	if !ok {
		return nil, false
	}
	names := make([]string, len(namesValues))
	unique := make(map[string]struct{}, len(namesValues))
	for i := range namesValues {
		name, ok := namesValues[i].AsString()
		if !ok {
			return nil, false
		}
		names[i] = strings.ToLower(name)
		if _, duplicate := unique[names[i]]; duplicate {
			return nil, false
		}
		unique[names[i]] = struct{}{}
	}
	parentsValue, ok := skeleton.Get("m_parentIndices")
	if !ok {
		return nil, false
	}
	parents, ok := parentsValue.AsArray()
	if !ok {
		return nil, false
	}
	required := make(map[int]struct{})
	for index, name := range names {
		_, inSource := sourceNames[name]
		_, inCurrent := currentNames[name]
		if !inSource && !inCurrent {
			continue
		}
		for {
			required[index] = struct{}{}
			if index >= len(parents) {
				return nil, false
			}
			parent, ok := parents[index].AsInt()
			if !ok {
				return nil, false
			}
			if parent < 0 {
				break
			}
			index = int(parent)
		}
	}
	unused := make(map[string]struct{})
	for index, name := range names {
		if _, ok := required[index]; ok {
			continue
		}
		if index >= len(parents) {
			return nil, false
		}
		parent, ok := parents[index].AsInt()
		if !ok {
			return nil, false
		}
		for parent >= 0 {
			parentIndex := int(parent)
			if parentIndex >= len(names) || parentIndex >= len(parents) {
				return nil, false
			}
			if _, ok := currentNames[names[parentIndex]]; ok {
				unused[name] = struct{}{}
				break
			}
			next, ok := parents[parentIndex].AsInt()
			if !ok {
				return nil, false
			}
			parent = next
		}
	}
	return unused, true
}

// SelfConsistent verifies the reference pose tree: local poses must reproduce
// the stored model-space poses within the animation tolerances.
func SelfConsistent(skeleton *kv3.Value) (bool, bool) {
	localValue, ok := skeleton.Get("m_parentSpaceReferencePose")
	if !ok {
		return false, false
	}
	local, ok := localValue.AsArray()
	if !ok {
		return false, false
	}
	worldValue, ok := skeleton.Get("m_modelSpaceReferencePose")
	if !ok {
		return false, false
	}
	world, ok := worldValue.AsArray()
	if !ok {
		return false, false
	}
	parentsValue, ok := skeleton.Get("m_parentIndices")
	if !ok {
		return false, false
	}
	parents, ok := parentsValue.AsArray()
	if !ok {
		return false, false
	}
	if len(local) != len(parents) || len(world) != len(parents) {
		return false, true
	}
	for index := range parents {
		localPose, ok := poseNumbers(&local[index])
		if !ok {
			return false, false
		}
		if localPose[3] <= 0 {
			return false, true
		}
		parent, ok := parents[index].AsInt()
		if !ok {
			return false, false
		}
		var expected [8]float64
		if parent == -1 {
			expected = localPose
		} else {
			if parent < 0 || int(parent) >= len(world) {
				return false, false
			}
			parentPose, ok := poseNumbers(&world[parent])
			if !ok {
				return false, false
			}
			x, y, z, w := parentPose[4], parentPose[5], parentPose[6], parentPose[7]
			cross := [3]float64{
				y*localPose[2] - z*localPose[1],
				z*localPose[0] - x*localPose[2],
				x*localPose[1] - y*localPose[0],
			}
			cross2 := [3]float64{
				y*cross[2] - z*cross[1],
				z*cross[0] - x*cross[2],
				x*cross[1] - y*cross[0],
			}
			a, b, c, d := localPose[4], localPose[5], localPose[6], localPose[7]
			expected = [8]float64{
				parentPose[0] + parentPose[3]*(localPose[0]+2*(w*cross[0]+cross2[0])),
				parentPose[1] + parentPose[3]*(localPose[1]+2*(w*cross[1]+cross2[1])),
				parentPose[2] + parentPose[3]*(localPose[2]+2*(w*cross[2]+cross2[2])),
				parentPose[3] * localPose[3],
				w*a + x*d + y*c - z*b,
				w*b - x*c + y*d + z*a,
				w*c + x*b - y*a + z*d,
				w*d - x*a - y*b - z*c,
			}
		}
		actual, ok := poseNumbers(&world[index])
		if !ok {
			return false, false
		}
		difference, ok := poseDelta(expected, actual)
		if !ok {
			return false, false
		}
		if difference.position > propagatedPositionBound ||
			difference.scale > scaleEpsilon ||
			difference.rotation > rotationEpsilonRadians {
			return false, true
		}
	}
	return true, true
}

type delta struct {
	position float64
	scale    float64
	rotation float64
}

func poseDelta(old, current [8]float64) (delta, bool) {
	a := length(old[4:])
	b := length(current[4:])
	if math.Abs(a-1) > 0.001 || math.Abs(b-1) > 0.001 {
		return delta{}, false
	}
	dot := old[4]*current[4] + old[5]*current[5] + old[6]*current[6] + old[7]*current[7]
	sign := 1.0
	if dot < 0 {
		sign = -1
	}
	quaternionDistance := make([]float64, 4)
	for i := 0; i < 4; i++ {
		quaternionDistance[i] = old[4+i]/a - sign*current[4+i]/b
	}
	return delta{
		position: length([]float64{
			old[0] - current[0],
			old[1] - current[1],
			old[2] - current[2],
		}),
		scale:    math.Abs(old[3] - current[3]),
		rotation: 4 * math.Asin(math.Min(length(quaternionDistance)/2, 1)),
	}, true
}

func poseNumbers(pose *kv3.Value) ([8]float64, bool) {
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

func length(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total = math.Hypot(total, value)
	}
	return total
}

// ScaledPosesCompatible accepts a uniform size change across the whole sampled
// chain when every position and rotation stays consistent with one scale.
func ScaledPosesCompatible(poses []Pose) bool {
	pairs := make([][2][8]float64, 0, len(poses))
	for i := range poses {
		oldPose, ok := poseNumbers(&poses[i].Old)
		if !ok {
			return false
		}
		currentPose, ok := poseNumbers(&poses[i].Current)
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
		oldValue := numbersValue(pair[0])
		modifiedValue := numbersValue(rotationAndScale)
		match, comparable := rig.EquivalentPose(&oldValue, &modifiedValue)
		if !positionsMatch || !comparable || !match {
			return false
		}
	}
	return true
}

func numbersValue(values [8]float64) kv3.Value {
	items := make([]kv3.Value, len(values))
	for i, value := range values {
		items[i] = kv3.Double(value)
	}
	return kv3.Array(items...)
}
