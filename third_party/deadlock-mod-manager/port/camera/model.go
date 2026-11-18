// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_camera.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modeltext"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

// CameraFields are the m_keyValueText fields the camera interface requires.
var CameraFields = [2]string{"CitadelCameraSettings_t", "AttachmentCameraData"}

func modelText(model *kv3.Value) (string, bool) {
	info, ok := fieldObject(model, "m_modelInfo")
	if !ok {
		return "", false
	}
	text, ok := info.Get("m_keyValueText")
	if !ok {
		return "", false
	}
	return text.AsString()
}

// MissingCameraFields reports the camera interface fields the current game
// model requires and the mod model lacks. It returns nil when the diagnosis
// cannot be made.
func MissingCameraFields(model, vanilla *kv3.Value) []string {
	currentText, ok := modelText(vanilla)
	if !ok {
		return nil
	}
	_, currentFields, ok := modeltext.RootFields(currentText)
	if !ok {
		return nil
	}
	for _, name := range CameraFields {
		if _, present := currentFields[name]; !present {
			return nil
		}
	}
	text, ok := modelText(model)
	if !ok {
		return nil
	}
	oldFields := map[string]modeltext.Range{}
	if strings.TrimSpace(text) != "" {
		_, parsed, ok := modeltext.RootFields(text)
		if !ok {
			return nil
		}
		oldFields = parsed
	}
	var missing []string
	for _, name := range CameraFields {
		if _, currentHas := currentFields[name]; !currentHas {
			continue
		}
		if _, oldHas := oldFields[name]; !oldHas {
			missing = append(missing, name)
		}
	}
	return missing
}

// CameraPointRequirements returns the camera points required by the model's
// current camera interface, when the game model has a complete interface.
func CameraPointRequirements(model, current *kv3.Value) ([]string, bool) {
	currentText, ok := modelText(current)
	if !ok {
		return nil, false
	}
	_, fields, ok := modeltext.RootFields(currentText)
	if !ok {
		return nil, false
	}
	for _, name := range CameraFields {
		if _, present := fields[name]; !present {
			return nil, false
		}
	}
	text, ok := modelText(model)
	if !ok {
		return nil, false
	}
	names, ok := CameraDataNames(text)
	if !ok {
		return nil, false
	}
	return sortedNames(names), true
}

// CameraDataNames returns the attachment names listed by AttachmentCameraData.
func CameraDataNames(text string) (map[string]struct{}, bool) {
	_, fields, ok := modeltext.RootFields(text)
	if !ok {
		return nil, false
	}
	r, ok := fields["AttachmentCameraData"]
	if !ok {
		return nil, false
	}
	value, ok := modeltext.AssignedValue(text[r.Start:r.End])
	if !ok {
		return nil, false
	}
	items, ok := modeltext.ArrayItems(value)
	if !ok {
		return nil, false
	}
	names := make(map[string]struct{})
	for _, entry := range items {
		_, entryFields, ok := modeltext.RootFields(entry)
		if !ok {
			return nil, false
		}
		nameRange, ok := entryFields["attachment_name"]
		if !ok {
			return nil, false
		}
		name, ok := modeltext.AssignedValue(entry[nameRange.Start:nameRange.End])
		if !ok {
			return nil, false
		}
		unquoted, ok := stripQuotes(name)
		if !ok || unquoted == "" || strings.ContainsAny(unquoted, "\"\\") {
			return nil, false
		}
		names[unquoted] = struct{}{}
	}
	if len(names) == 0 {
		return nil, false
	}
	return names, true
}

// RequiredBones returns the lowercase non-root influence bones the current
// model's camera points use on one mesh. Missing or duplicated camera points
// fail closed.
func RequiredBones(model, mesh *kv3.Value) (map[string]struct{}, bool) {
	text, ok := modelText(model)
	if !ok {
		return nil, false
	}
	names, ok := CameraDataNames(text)
	if !ok {
		return nil, false
	}
	points, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return nil, false
	}
	for _, pivot := range []string{"standing_pivot", "crouching_pivot"} {
		for i := range points {
			keyValue, ok := points[i].Get("key")
			if !ok {
				continue
			}
			value, ok := keyValue.AsString()
			if ok && value == pivot {
				names[pivot] = struct{}{}
			}
		}
	}
	for name := range names {
		count := 0
		for i := range points {
			key, ok := points[i].Get("key")
			if !ok {
				continue
			}
			if value, ok := key.AsString(); ok && value == name {
				count++
			}
		}
		if count != 1 {
			return nil, false
		}
	}
	bones := make(map[string]struct{})
	for i := range points {
		value, ok := points[i].Get("value")
		if !ok {
			return nil, false
		}
		nameValue, ok := value.Get("m_name")
		if !ok {
			continue
		}
		name, ok := nameValue.AsString()
		if !ok {
			return nil, false
		}
		if _, required := names[name]; !required {
			continue
		}
		countValue, ok := value.Get("m_nInfluences")
		if !ok {
			return nil, false
		}
		count, ok := countValue.AsInt()
		if !ok || count < 0 {
			return nil, false
		}
		roots, ok := fieldArray(value, "m_bInfluenceRootTransform")
		if !ok {
			return nil, false
		}
		influences, ok := fieldArray(value, "m_influenceNames")
		if !ok || int(count) > len(roots) || int(count) > len(influences) {
			return nil, false
		}
		for j := 0; j < int(count); j++ {
			root, ok := roots[j].AsBool()
			if !ok {
				return nil, false
			}
			if root {
				continue
			}
			name, ok := influences[j].AsString()
			if !ok {
				return nil, false
			}
			bones[strings.ToLower(name)] = struct{}{}
		}
	}
	return bones, true
}

func stripQuotes(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	return s[1 : len(s)-1], true
}

func sortedNames(names map[string]struct{}) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func cameraPointNames(text string, mesh *kv3.Value) ([]string, bool) {
	names, ok := CameraDataNames(text)
	if !ok {
		return nil, false
	}
	points, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return nil, false
	}
	hasKey := func(name string) bool {
		for i := range points {
			keyV, ok := points[i].Get("key")
			if !ok {
				continue
			}
			key, ok := keyV.AsString()
			if ok && key == name {
				return true
			}
		}
		return false
	}
	for _, pivot := range pivots {
		if hasKey(pivot) {
			names[pivot] = struct{}{}
		}
	}
	for name := range names {
		count := 0
		for i := range points {
			keyV, ok := points[i].Get("key")
			if !ok {
				continue
			}
			key, ok := keyV.AsString()
			if ok && key == name {
				count++
			}
		}
		if count != 1 {
			return nil, false
		}
	}
	return sortedNames(names), true
}

func hasPoint(mesh *kv3.Value, name string) bool {
	points, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return false
	}
	for i := range points {
		keyV, ok := points[i].Get("key")
		if !ok {
			continue
		}
		key, ok := keyV.AsString()
		if ok && key == name {
			return true
		}
	}
	return false
}

// numericSettings accepts only scalar and vector camera settings. Resource
// references, bone names and non-finite numbers are declined.
func numericSettings(field string) bool {
	_, object, ok := strings.Cut(field, "=")
	if !ok {
		return false
	}
	_, fields, ok := modeltext.RootFields(object)
	if !ok || len(fields) == 0 {
		return false
	}
	for _, r := range fields {
		_, value, ok := strings.Cut(object[r.Start:r.End], "=")
		if !ok {
			return false
		}
		value = strings.TrimSpace(value)
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
		numbers := strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
		if len(numbers) == 0 {
			return false
		}
		for _, n := range numbers {
			f, err := strconv.ParseFloat(n, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return false
			}
		}
	}
	return true
}

// ModelOptions carries the evidence the camera repair may use beyond the two
// model files. AllowScaled accepts a model whose animation bindings were
// restored or rebased, and SampledBones names the bones the current animation
// skeleton supplies, which then need no camera pose proof.
type ModelOptions struct {
	AllowControls func(*kv3.Value) bool
	AllowScaled   bool
	SampledBones  map[string]struct{}
}

// RepairModelCamera adds the camera interface, or the camera points and
// control bones, from the current game model. It returns ok=false when the
// repair cannot be proven safe.
func RepairModelCamera(source, base []byte, allowControls func(*kv3.Value) bool) ([]byte, RepairKind, bool) {
	return RepairModelCameraWith(source, base, ModelOptions{AllowControls: allowControls})
}

// RepairModelCameraWith is RepairModelCamera with animation evidence.
func RepairModelCameraWith(source, base []byte, options ModelOptions) ([]byte, RepairKind, bool) {
	format, model, encoding, err := DecodeCompiled(source)
	if err != nil {
		return nil, "", false
	}
	_, vanilla, vanillaEncoding, err := DecodeCompiled(base)
	if err != nil {
		return nil, "", false
	}
	oldText, ok := modelText(&model)
	if !ok {
		return nil, "", false
	}
	currentText, ok := modelText(&vanilla)
	if !ok {
		return nil, "", false
	}
	closing, oldFields, ok := modeltext.RootFields(oldText)
	if !ok {
		return nil, "", false
	}
	_, currentFields, ok := modeltext.RootFields(currentText)
	if !ok {
		return nil, "", false
	}

	var missing []string
	for _, name := range CameraFields {
		if _, present := oldFields[name]; !present {
			missing = append(missing, name)
		}
	}
	for _, name := range CameraFields {
		if _, present := currentFields[name]; !present {
			return nil, "", false
		}
	}
	if containsString(missing, "CitadelCameraSettings_t") {
		r := currentFields["CitadelCameraSettings_t"]
		if !numericSettings(currentText[r.Start:r.End]) {
			return nil, "", false
		}
	}

	sourceResource, err := resource.Parse(source)
	if err != nil {
		return nil, "", false
	}
	baseResource, err := resource.Parse(base)
	if err != nil {
		return nil, "", false
	}
	var baseMesh *kv3.Value
	var baseEncoding *kv3.Encoding
	if raw, ok := baseResource.FindBlock(mdat); ok {
		if mesh, enc, err := kv3.DecodePreserving(raw); err == nil {
			baseMesh, baseEncoding = &mesh, &enc
		}
	}

	cameraText := oldText
	if containsString(missing, "AttachmentCameraData") {
		cameraText = currentText
	}
	var names []string
	if baseMesh != nil {
		names, _ = cameraPointNames(cameraText, baseMesh)
	}

	type meshEntry struct {
		index    int
		value    kv3.Value
		encoding kv3.Encoding
	}
	var meshes []meshEntry
	for index, block := range sourceResource.Blocks() {
		if block.Kind != mdat {
			continue
		}
		raw, ok := sourceResource.GetBlockByIndex(index)
		if !ok {
			return nil, "", false
		}
		mesh, enc, err := kv3.DecodePreserving(raw)
		if err != nil {
			return nil, "", false
		}
		if _, ok := mesh.Get("m_attachments"); ok {
			meshes = append(meshes, meshEntry{index: index, value: mesh, encoding: enc})
		}
	}
	pointsMissing := false
	if names != nil {
		for i := range meshes {
			for _, name := range names {
				if !hasPoint(&meshes[i].value, name) {
					pointsMissing = true
					break
				}
			}
			if pointsMissing {
				break
			}
		}
	}
	if len(missing) == 0 && !pointsMissing {
		return nil, "", false
	}

	settingsOnly := len(missing) == 1 && missing[0] == "CitadelCameraSettings_t" && !pointsMissing
	compatible := settingsOnly
	if !compatible && options.AllowScaled {
		compatible, _ = compatibleCameraDependencies(&model, &vanilla, base, options.SampledBones)
	}
	if !compatible {
		oldRig, ok1 := model.Get("m_modelSkeleton")
		currentRig, ok2 := vanilla.Get("m_modelSkeleton")
		if ok1 && ok2 {
			compatible, _ = compatibleRigs(oldRig, currentRig, options.AllowScaled)
		}
	}
	authoredAttachments := containsString(missing, "AttachmentCameraData") &&
		contractSatisfied(sourceResource, &model, base, cameraText)
	allow := options.AllowControls != nil && options.AllowControls(&vanilla)
	if !compatible && !authoredAttachments && !allow {
		return nil, "", false
	}

	if len(missing) > 0 {
		var b strings.Builder
		b.WriteString(oldText[:closing])
		b.WriteByte('\n')
		for _, name := range missing {
			r := currentFields[name]
			b.WriteString(currentText[r.Start:r.End])
			b.WriteByte('\n')
		}
		b.WriteString(oldText[closing:])
		text := b.String()
		if _, _, ok := modeltext.RootFields(text); !ok {
			return nil, "", false
		}
		info, ok := fieldObject(&model, "m_modelInfo")
		if !ok {
			return nil, "", false
		}
		objectSetCaseInsensitive(info, "m_keyValueText", kv3.String(text))
	}

	if settingsOnly || authoredAttachments {
		data, err := kv3.EncodePreserving(&model, &encoding, format)
		if err != nil {
			return nil, "", false
		}
		intermediate, err := sourceResource.RebuildWithDataPreserving(data)
		if err != nil {
			return nil, "", false
		}
		decodedFormat, decodedModel, decodedEncoding, err := DecodeCompiled(intermediate)
		if err != nil || !sameFormat(decodedFormat, format) ||
			!RuntimeValuesEqual(&model, &decodedModel) ||
			!kv3.EncodingPreserved(&model, &encoding, &decodedEncoding) {
			return nil, "", false
		}
		return intermediate, RepairInterface, true
	}

	if names == nil || baseMesh == nil || baseEncoding == nil || len(meshes) == 0 {
		return nil, "", false
	}
	points := append([]string(nil), names...)
	kind := RepairInterface
	if !compatible {
		if countKind(sourceResource, mdat) != 1 || countKind(baseResource, mdat) != 1 {
			return nil, "", false
		}
		if !controlsExtend(&model, &encoding, &meshes[0].value, &meshes[0].encoding,
			&vanilla, &vanillaEncoding, baseMesh, baseEncoding, points) {
			return nil, "", false
		}
		kind = RepairControls
	}

	modelBones, ok := boneNames(&model)
	if !ok {
		return nil, "", false
	}
	for i := range meshes {
		for _, label := range invalidAttachmentLabels(&meshes[i].value, modelBones) {
			if containsString(points, label) {
				return nil, "", false
			}
		}
		if !addCameraPoints(&meshes[i].value, &meshes[i].encoding, baseMesh, baseEncoding, points) {
			return nil, "", false
		}
	}

	rebuilt := source
	if len(missing) != 0 || !compatible {
		data, err := kv3.EncodePreserving(&model, &encoding, format)
		if err != nil {
			return nil, "", false
		}
		rebuilt, err = sourceResource.RebuildWithDataPreserving(data)
		if err != nil {
			return nil, "", false
		}
	}
	for i := range meshes {
		res, err := resource.Parse(rebuilt)
		if err != nil {
			return nil, "", false
		}
		raw, ok := res.GetBlockByIndex(meshes[i].index)
		if !ok {
			return nil, "", false
		}
		meshFormat, err := kv3.FormatFromPayload(raw)
		if err != nil {
			return nil, "", false
		}
		data, err := kv3.EncodePreserving(&meshes[i].value, &meshes[i].encoding, meshFormat)
		if err != nil {
			return nil, "", false
		}
		rebuilt, err = res.RebuildWithBlockPreserving(meshes[i].index, data)
		if err != nil {
			return nil, "", false
		}
	}

	decodedFormat, decodedModel, decodedEncoding, err := DecodeCompiled(rebuilt)
	if err != nil || !sameFormat(decodedFormat, format) ||
		!RuntimeValuesEqual(&model, &decodedModel) ||
		!kv3.EncodingPreserved(&model, &encoding, &decodedEncoding) {
		return nil, "", false
	}
	rebuiltResource, err := resource.Parse(rebuilt)
	if err != nil {
		return nil, "", false
	}
	if !contractSatisfied(rebuiltResource, &decodedModel, base, cameraText) {
		return nil, "", false
	}
	for i := range meshes {
		raw, ok := rebuiltResource.GetBlockByIndex(meshes[i].index)
		if !ok {
			return nil, "", false
		}
		decodedMesh, decodedMeshEncoding, err := kv3.DecodePreserving(raw)
		if err != nil || !RuntimeValuesEqual(&meshes[i].value, &decodedMesh) ||
			!kv3.EncodingPreserved(&meshes[i].value, &meshes[i].encoding, &decodedMeshEncoding) {
			return nil, "", false
		}
	}
	return rebuilt, kind, true
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func countKind(res *resource.Resource, kind [4]byte) int {
	count := 0
	for _, block := range res.Blocks() {
		if block.Kind == kind {
			count++
		}
	}
	return count
}

func boneNames(model *kv3.Value) (map[string]struct{}, bool) {
	skeleton, ok := fieldObject(model, "m_modelSkeleton")
	if !ok {
		return nil, false
	}
	names, ok := fieldArray(skeleton, "m_boneName")
	if !ok {
		return nil, false
	}
	out := make(map[string]struct{}, len(names))
	for i := range names {
		name, ok := names[i].AsString()
		if !ok {
			return nil, false
		}
		out[name] = struct{}{}
	}
	return out, true
}

// addCameraPoints appends the missing camera points from the base mesh, keeping
// authored points and their encodings unchanged.
func addCameraPoints(mesh *kv3.Value, meshEncoding *kv3.Encoding, base *kv3.Value, baseEncoding *kv3.Encoding, names []string) bool {
	attachments, ok := mesh.Get("m_attachments")
	if !ok || attachments.Kind != kv3.KindArray {
		return false
	}
	existing := make(map[string]struct{}, len(attachments.Array))
	for i := range attachments.Array {
		keyV, ok := attachments.Array[i].Get("key")
		if !ok {
			return false
		}
		key, ok := keyV.AsString()
		if !ok {
			return false
		}
		existing[key] = struct{}{}
	}
	if len(existing) != len(attachments.Array) {
		return false
	}
	encV, ok := meshEncoding.Get("m_attachments")
	if !ok || encV.Children.Kind != kv3.ChildrenArray {
		return false
	}
	if len(attachments.Array) != len(encV.Children.Array) {
		return false
	}

	basePoints, ok := fieldArray(base, "m_attachments")
	if !ok {
		return false
	}
	baseEncV, ok := baseEncoding.Get("m_attachments")
	if !ok || baseEncV.Children.Kind != kv3.ChildrenArray {
		return false
	}
	baseEncodings := baseEncV.Children.Array

	for _, name := range names {
		if _, present := existing[name]; present {
			continue
		}
		index := -1
		for i := range basePoints {
			keyV, ok := basePoints[i].Get("key")
			if !ok {
				continue
			}
			key, ok := keyV.AsString()
			if ok && key == name {
				index = i
				break
			}
		}
		if index < 0 || index >= len(baseEncodings) {
			return false
		}
		clone := rig.Clone(&basePoints[index])
		attachments.Array = append(attachments.Array, clone)
		encV.Children.Array = append(encV.Children.Array, baseEncodings[index])
	}
	return true
}

// invalidAttachmentLabels returns the labels of attachments whose influences
// are malformed or reference unknown bones.
func invalidAttachmentLabels(mesh *kv3.Value, modelBones map[string]struct{}) []string {
	skeleton, ok := fieldObject(mesh, "m_skeleton")
	if !ok {
		return nil
	}
	bones, ok := fieldArray(skeleton, "m_bones")
	if !ok {
		return nil
	}
	names := make(map[string]struct{}, len(bones)+len(modelBones))
	for i := range bones {
		nameV, ok := bones[i].Get("m_boneName")
		if !ok {
			continue
		}
		name, ok := nameV.AsString()
		if ok {
			names[name] = struct{}{}
		}
	}
	for name := range modelBones {
		names[name] = struct{}{}
	}

	attachments, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return nil
	}
	var invalid []string
	for i := range attachments {
		label := "m_attachments"
		if keyV, ok := attachments[i].Get("key"); ok {
			if key, ok := keyV.AsString(); ok {
				label = key
			}
		}
		valid := false
		if value, ok := attachments[i].Get("value"); ok {
			valid = validAttachment(value, names)
		}
		if !valid {
			invalid = append(invalid, label)
		}
	}
	return invalid
}

func validAttachment(value *kv3.Value, bones map[string]struct{}) bool {
	influencesV, ok := value.Get("m_nInfluences")
	if !ok {
		return false
	}
	count64, ok := influencesV.AsInt()
	if !ok || count64 < 0 || count64 > int64(math.MaxInt) {
		return false
	}
	count := int(count64)
	influences, ok := fieldArray(value, "m_influenceNames")
	if !ok {
		return false
	}
	roots, ok := fieldArray(value, "m_bInfluenceRootTransform")
	if !ok {
		return false
	}
	weights, ok := fieldArray(value, "m_influenceWeights")
	if !ok {
		return false
	}
	if count > len(influences) || count > len(roots) || count > len(weights) {
		return false
	}
	for i := 0; i < count; i++ {
		rooted := roots[i].Kind == kv3.KindBool && roots[i].Bool
		w, ok := weights[i].AsFloat()
		if !ok || !isFinite(w) || w < 0 {
			return false
		}
		if rooted {
			continue
		}
		name, ok := influences[i].AsString()
		if !ok {
			return false
		}
		if _, known := bones[name]; !known {
			return false
		}
	}
	return true
}

// compatibleRigs reports whether the current rig's bones exist in the authored
// rig with the same parents and poses. Extra authored branches are allowed.
func compatibleRigs(old, current *kv3.Value, allowScaled bool) (bool, bool) {
	poseKeys := []rig.PoseKey{
		{Key: "m_bonePosParent", Width: 3},
		{Key: "m_boneRotParent", Width: 4},
		{Key: "m_boneScaleParent", Width: 0},
	}
	if !rig.ValidSkeleton(old, "m_boneName", "m_nParent", poseKeys) ||
		!rig.ValidSkeleton(current, "m_boneName", "m_nParent", poseKeys) {
		return false, true
	}
	names := func(v *kv3.Value) ([]string, bool) {
		items, ok := fieldArray(v, "m_boneName")
		if !ok {
			return nil, false
		}
		out := make([]string, len(items))
		for i := range items {
			name, ok := items[i].AsString()
			if !ok {
				return nil, false
			}
			out[i] = name
		}
		return out, true
	}
	a, ok := names(old)
	if !ok {
		return false, false
	}
	b, ok := names(current)
	if !ok {
		return false, false
	}
	unique := func(values []string) map[string]struct{} {
		out := make(map[string]struct{}, len(values))
		for _, v := range values {
			out[v] = struct{}{}
		}
		return out
	}
	ua, ub := unique(a), unique(b)
	if len(a) == 0 || len(ua) != len(a) || len(ub) != len(b) {
		return false, true
	}
	for name := range ub {
		if _, ok := ua[name]; !ok {
			return false, true
		}
	}
	indices := make(map[string]int, len(a))
	for i, name := range a {
		indices[name] = i
	}
	parent := func(v *kv3.Value, names []string, index int) (string, bool) {
		parents, ok := fieldArray(v, "m_nParent")
		if !ok || index >= len(parents) {
			return "", false
		}
		p, ok := parents[index].AsInt()
		if !ok {
			return "", false
		}
		if p == -1 {
			return "", true
		}
		if p < 0 || p >= int64(len(names)) {
			return "", false
		}
		return names[p], true
	}
	pose := func(v *kv3.Value, index int) (kv3.Value, bool) {
		positionV, ok := fieldArray(v, "m_bonePosParent")
		if !ok || index >= len(positionV) {
			return kv3.Value{}, false
		}
		position, ok := positionV[index].AsArray()
		if !ok {
			return kv3.Value{}, false
		}
		rotationV, ok := fieldArray(v, "m_boneRotParent")
		if !ok || index >= len(rotationV) {
			return kv3.Value{}, false
		}
		rotation, ok := rotationV[index].AsArray()
		if !ok || len(position) != 3 || len(rotation) != 4 {
			return kv3.Value{}, false
		}
		scaleV, ok := fieldArray(v, "m_boneScaleParent")
		if !ok || index >= len(scaleV) {
			return kv3.Value{}, false
		}
		values := make([]kv3.Value, 0, 8)
		values = append(values, position...)
		values = append(values, scaleV[index])
		values = append(values, rotation...)
		return kv3.Value{Kind: kv3.KindArray, Array: values}, true
	}
	allEquivalent := true
	poses := make([][2]kv3.Value, 0, len(b))
	for j, name := range b {
		i, ok := indices[name]
		if !ok {
			return false, true
		}
		oldParent, ok1 := parent(old, a, i)
		currentParent, ok2 := parent(current, b, j)
		if !ok1 || !ok2 || oldParent != currentParent {
			return false, true
		}
		oldPose, ok1 := pose(old, i)
		currentPose, ok2 := pose(current, j)
		if !ok1 || !ok2 {
			return false, false
		}
		poses = append(poses, [2]kv3.Value{oldPose, currentPose})
		if equal, valid := rig.EquivalentPose(&oldPose, &currentPose); !valid || !equal {
			allEquivalent = false
		}
	}
	// A preserved bind is the common case; a uniform size change across the
	// whole sampled chain is the only other accepted difference.
	return allEquivalent || allowScaled && rig.ScaledPosesCompatible(poses), true
}

// compatibleCameraDependencies proves the bones the current camera points
// depend on without comparing the whole rig. It is the scaled-rebase path:
// the authored bind frames stay, while the current animation skeleton supplies
// the sampled world pose.
func compatibleCameraDependencies(source, current *kv3.Value, base []byte,
	sampledBones map[string]struct{}) (bool, bool) {
	resourceValue, err := resource.Parse(base)
	if err != nil {
		return false, false
	}
	raw, ok := resourceValue.FindBlock(mdat)
	if !ok {
		return false, false
	}
	mesh, err := kv3.Decode(raw)
	if err != nil {
		return false, false
	}
	required, ok := RequiredBones(current, &mesh)
	if !ok {
		return false, false
	}
	return CompatibleNamedCameraBones(source, current, required, sampledBones)
}

// CompatibleNamedCameraBones checks the camera dependency chain by bone name.
// A bone the current animation skeleton samples needs no pose proof; every
// other bone, and every shared ancestor, must keep an equivalent pose.
func CompatibleNamedCameraBones(source, current *kv3.Value,
	required, sampledBones map[string]struct{}) (bool, bool) {
	oldRig, ok := rig.Parse(source)
	if !ok {
		return false, false
	}
	currentRig, ok := rig.Parse(current)
	if !ok {
		return false, false
	}
	oldNames := make(map[string]string)
	for _, name := range oldRig.Names() {
		oldNames[strings.ToLower(name)] = name
	}
	currentNames := make(map[string]string)
	for _, name := range currentRig.Names() {
		currentNames[strings.ToLower(name)] = name
	}
	type visited struct {
		name    string
		sampled bool
	}
	checked := make(map[visited]struct{})
	for name := range required {
		a, ok := oldNames[name]
		if !ok {
			return false, false
		}
		b, ok := currentNames[name]
		if !ok {
			return false, false
		}
		_, sampled := sampledBones[name]
		for a != "" {
			key := visited{name: a, sampled: sampled}
			if _, seen := checked[key]; seen {
				break
			}
			checked[key] = struct{}{}
			if !strings.EqualFold(a, b) {
				return false, true
			}
			if !sampled {
				oldPose, ok1 := oldRig.Pose(a)
				currentPose, ok2 := currentRig.Pose(b)
				if !ok1 || !ok2 {
					return false, false
				}
				equal, valid := rig.EquivalentPose(&oldPose, &currentPose)
				if !valid || !equal {
					return false, true
				}
			}
			a, ok = oldRig.Parent(a)
			if !ok {
				return false, false
			}
			b, ok = currentRig.Parent(b)
			if !ok {
				return false, false
			}
			if (a == "") != (b == "") {
				return false, true
			}
		}
	}
	return true, true
}

// SampledBoneIDs returns a skeleton's bone ids lowercased, which is the set
// the animation system supplies by name.
func SampledBoneIDs(skeleton *kv3.Value) (map[string]struct{}, bool) {
	ids, ok := fieldArray(skeleton, "m_boneIDs")
	if !ok {
		return nil, false
	}
	out := make(map[string]struct{}, len(ids))
	for i := range ids {
		name, ok := ids[i].AsString()
		if !ok {
			return nil, false
		}
		out[strings.ToLower(name)] = struct{}{}
	}
	return out, true
}
