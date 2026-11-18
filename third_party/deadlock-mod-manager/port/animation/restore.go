// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation.rs,
// model_animation/sampled_controls.rs and model_animation/metadata.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modeltext"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const (
	graphsField    = "m_animGraph2Refs"
	skeletonsField = "m_vecNmSkeletonRefs"
)

var bindingFields = [...]string{graphsField, skeletonsField}

var sampleFields = [...]string{
	"m_boneName",
	"m_nParent",
	"m_bonePosParent",
	"m_boneRotParent",
	"m_boneScaleParent",
	"m_boneSphere",
	"m_nFlag",
}

// RestoreBindings gives an old model the animation bindings of the current game
// model when the sampled rig proves the two share a compatible skeleton. The
// authored deformation rig is kept; only missing sampled controls are appended.
func RestoreBindings(source, base []byte, resolve Source, available func(string) bool) ([]byte, bool) {
	format, old, oldEncoding, err := camera.DecodeCompiled(source)
	if err != nil {
		return nil, false
	}
	_, current, currentEncoding, err := camera.DecodeCompiled(base)
	if err != nil || !eligible(&old, &current) {
		return nil, false
	}
	reference, err := singleSkeletonReference(&current)
	if err != nil {
		return nil, false
	}
	if resolve.FromMod(reference) {
		return nil, false
	}
	skeletonBytes, err := resolve.Resolve(reference)
	if err != nil || skeletonBytes == nil {
		return nil, false
	}
	_, skeleton, _, err := camera.DecodeCompiled(skeletonBytes)
	if err != nil {
		return nil, false
	}
	graphs, graphsOK := Graphs(&current)
	if !graphsOK {
		return nil, false
	}
	for _, graph := range sortedGraphPaths(graphs) {
		if resolve.FromMod(graph) {
			return nil, false
		}
		bytes, err := resolve.Resolve(graph)
		if err != nil || bytes == nil {
			return nil, false
		}
	}
	prepared, encoding, samplesOK := appendMissingSamples(&old, &oldEncoding, &current, &currentEncoding, &skeleton)
	if !samplesOK {
		return nil, false
	}
	if err := proveSampledRig(&prepared, &current, &skeleton); err != nil {
		return nil, false
	}
	return restoreBindingsFrom(source, base, format, &prepared, &encoding, resolve, available)
}

func eligible(source, current *kv3.Value) bool {
	for _, field := range bindingFields {
		if value, ok := source.Get(field); ok {
			if value.Kind != kv3.KindNull {
				if items, ok := value.AsArray(); !ok || len(items) > 0 {
					return false
				}
			}
		}
		value, ok := current.Get(field)
		if !ok {
			return false
		}
		items, ok := value.AsArray()
		if !ok || len(items) == 0 {
			return false
		}
	}
	return true
}

func appendMissingSamples(old *kv3.Value, oldEncoding *kv3.Encoding,
	current *kv3.Value, currentEncoding *kv3.Encoding, skeleton *kv3.Value) (kv3.Value, kv3.Encoding, bool) {
	rigValue, ok := old.Get("m_modelSkeleton")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	names, indices, ok := boneNames(rigValue)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	base, ok := current.Get("m_modelSkeleton")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	baseNames, baseIndices, ok := boneNames(base)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	samplesValue, ok := skeleton.Get("m_boneIDs")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	samples, ok := samplesValue.AsArray()
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	model, ok := rig.Parse(current)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	type addition struct {
		index  int
		parent int64
	}
	var additions []addition
	for i := range samples {
		sample, ok := samples[i].AsString()
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		name := strings.ToLower(sample)
		if _, exists := indices[name]; exists {
			continue
		}
		index, exists := baseIndices[name]
		if !exists {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		parentValue, ok := arrayItem(base, "m_nParent", index)
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		parent, ok := parentValue.AsInt()
		if !ok || parent < 0 || parent >= int64(len(baseNames)) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		parentName := strings.ToLower(baseNames[parent])
		oldParent, exists := indices[parentName]
		if !exists {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		oldParentValue, ok := arrayItem(rigValue, "m_nParent", oldParent)
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		oldParentIndex, ok := oldParentValue.AsInt()
		if !ok || oldParentIndex != -1 {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		sphere, ok := arrayItem(base, "m_boneSphere", index)
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		sphereRadius, ok := sphere.AsFloat()
		if !ok || sphereRadius != 0 {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		if hasParentIndex(base, index) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		actualName := baseNames[index]
		pose, ok := model.Pose(actualName)
		if !ok {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		identity := numbersValue([8]float64{0, 0, 0, 1, 0, 0, 0, 1})
		if match, comparable := rig.EquivalentPose(&pose, &identity); !comparable || !match {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		additions = append(additions, addition{index: index, parent: int64(oldParent)})
	}
	if len(additions) == 0 {
		return rig.Clone(old), *cloneEncoding(oldEncoding), true
	}
	oldFields, ok := rigValue.AsObject()
	if !ok || len(oldFields) != len(sampleFields) {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	baseFields, ok := base.AsObject()
	if !ok || len(baseFields) != len(sampleFields) {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	meshesValue, ok := old.Get("m_refMeshes")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	meshes, ok := meshesValue.AsArray()
	if !ok || len(meshes) > 0 {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	for _, field := range sampleFields {
		if values, ok := arrayField(rigValue, field); !ok || len(values) != len(names) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
		if values, ok := arrayField(base, field); !ok || len(values) != len(baseNames) {
			return kv3.Value{}, kv3.Encoding{}, false
		}
	}
	value := rig.Clone(old)
	encoding := cloneEncoding(oldEncoding)
	encodedSkeleton, ok := encoding.Get("m_modelSkeleton")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	currentSkeleton, ok := currentEncoding.Get("m_modelSkeleton")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	oldSkeleton, ok := value.Get("m_modelSkeleton")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false
	}
	for _, addition := range additions {
		for _, field := range sampleFields {
			item, ok := arrayItem(base, field, addition.index)
			if !ok {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			item = rig.Clone(&item)
			if field == "m_nParent" {
				switch item.Kind {
				case kv3.KindInt:
					item = kv3.Int(addition.parent)
				case kv3.KindUInt:
					item = kv3.UInt(uint64(addition.parent))
				default:
					return kv3.Value{}, kv3.Encoding{}, false
				}
			}
			fieldValue, ok := oldSkeleton.Get(field)
			if !ok {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			items, ok := fieldValue.AsArray()
			if !ok {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			fieldValue.Array = append(items, item)

			fieldEncoding, ok := encodedSkeleton.Get(field)
			if !ok {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			encodings, ok := fieldEncoding.AsArray()
			if !ok {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			currentField, ok := currentSkeleton.Get(field)
			if !ok {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			currentArray, ok := currentField.AsArray()
			if !ok || addition.index >= len(currentArray) {
				return kv3.Value{}, kv3.Encoding{}, false
			}
			fieldEncoding.Children.Array = append(encodings, currentArray[addition.index])
		}
	}
	return value, *encoding, true
}

func boneNames(rigValue *kv3.Value) ([]string, map[string]int, bool) {
	values, ok := arrayField(rigValue, "m_boneName")
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
		lower := strings.ToLower(name)
		if _, duplicate := indices[lower]; duplicate {
			return nil, nil, false
		}
		names[i] = name
		indices[lower] = i
	}
	return names, indices, true
}

func arrayField(value *kv3.Value, field string) ([]kv3.Value, bool) {
	fieldValue, ok := value.Get(field)
	if !ok {
		return nil, false
	}
	return fieldValue.AsArray()
}

func arrayItem(value *kv3.Value, field string, index int) (kv3.Value, bool) {
	values, ok := arrayField(value, field)
	if !ok || index < 0 || index >= len(values) {
		return kv3.Value{}, false
	}
	return values[index], true
}

func hasParentIndex(rigValue *kv3.Value, index int) bool {
	parents, ok := arrayField(rigValue, "m_nParent")
	if !ok {
		return true
	}
	for i := range parents {
		parent, ok := parents[i].AsInt()
		if !ok || parent == int64(index) {
			return true
		}
	}
	return false
}

func restoreBindingsFrom(source, base []byte, format kv3.Format, value *kv3.Value, encoding *kv3.Encoding,
	resolve Source, available func(string) bool) ([]byte, bool) {
	_, current, currentEncoding, err := camera.DecodeCompiled(base)
	if err != nil || !eligible(value, &current) {
		return nil, false
	}
	model := rig.Clone(value)
	if oldText, ok := modelText(&model); ok {
		currentText, ok := modelText(&current)
		if !ok {
			return nil, false
		}
		graphs, ok := Graphs(&current)
		if !ok {
			return nil, false
		}
		text, ok := restoreMetadata(oldText, currentText, graphs, available)
		if !ok {
			return nil, false
		}
		info, ok := model.Get("m_modelInfo")
		if !ok {
			return nil, false
		}
		setObjectField(info, "m_keyValueText", kv3.String(text))
	}
	for _, field := range bindingFields {
		replacement, ok := current.Get(field)
		if !ok {
			return nil, false
		}
		cloned := rig.Clone(replacement)
		replacementEncoding, ok := currentEncoding.Get(field)
		if !ok {
			return nil, false
		}
		setObjectField(&model, field, cloned)
		if encoding.Children.Kind != kv3.ChildrenObject {
			return nil, false
		}
		found := false
		for i := range encoding.Children.Object {
			if encoding.Children.Object[i].Key == field {
				encoding.Children.Object[i].Value = *cloneEncoding(replacementEncoding)
				found = true
				break
			}
		}
		if !found {
			encoding.Children.Object = append(encoding.Children.Object, kv3.EncodingField{
				Key:   field,
				Value: *cloneEncoding(replacementEncoding),
			})
		}
	}
	graphs, graphsOK := Graphs(&current)
	if !graphsOK {
		return nil, false
	}
	var additions []resource.Reference
	for _, graph := range sortedGraphPaths(graphs) {
		reference, ok := referenceFor(graph)
		if !ok {
			return nil, false
		}
		additions = append(additions, reference)
	}
	references, ok := current.Get(skeletonsField)
	if !ok {
		return nil, false
	}
	refs, ok := references.AsArray()
	if !ok {
		return nil, false
	}
	for i := range refs {
		name, ok := refs[i].AsString()
		if !ok {
			return nil, false
		}
		reference, ok := referenceFor(name)
		if !ok {
			return nil, false
		}
		additions = append(additions, reference)
	}
	data, err := kv3.EncodePreserving(&model, encoding, format)
	if err != nil {
		return nil, false
	}
	parsed, err := resource.Parse(source)
	if err != nil {
		return nil, false
	}
	rebuilt, err := parsed.RebuildWithDataPreserving(data)
	if err != nil {
		return nil, false
	}
	rebuilt, err = resource.AddReferencesOf(rebuilt, additions)
	if err != nil {
		return nil, false
	}
	actualFormat, actual, actualEncoding, err := camera.DecodeCompiled(rebuilt)
	if err != nil {
		return nil, false
	}
	if actualFormat != format || !camera.RuntimeValuesEqual(&model, &actual) ||
		!kv3.EncodingPreserved(&model, encoding, &actualEncoding) {
		return nil, false
	}
	return rebuilt, true
}

func referenceFor(name string) (resource.Reference, bool) {
	id, err := resource.ResourceID(name)
	if err != nil {
		return resource.Reference{}, false
	}
	return resource.Reference{ID: id, Path: name}, true
}

func modelText(value *kv3.Value) (string, bool) {
	info, ok := value.Get("m_modelInfo")
	if !ok {
		return "", false
	}
	text, ok := info.Get("m_keyValueText")
	if !ok {
		return "", false
	}
	return text.AsString()
}

func setObjectField(object *kv3.Value, key string, value kv3.Value) {
	if object.Kind != kv3.KindObject {
		return
	}
	for i := range object.Object {
		if strings.EqualFold(object.Object[i].Key, key) {
			object.Object[i].Value = value
			return
		}
	}
	object.Object = append(object.Object, kv3.Pair{Key: key, Value: value})
}

func cloneEncoding(encoding *kv3.Encoding) *kv3.Encoding {
	out := *encoding
	out.Children.Array = slices.Clone(encoding.Children.Array)
	for i := range out.Children.Array {
		out.Children.Array[i] = *cloneEncoding(&encoding.Children.Array[i])
	}
	out.Children.Object = slices.Clone(encoding.Children.Object)
	for i := range out.Children.Object {
		out.Children.Object[i].Value = *cloneEncoding(&encoding.Children.Object[i].Value)
	}
	return &out
}

var metadataGraphFields = [...]string{
	"anim_graph_resource",
	"m_UIAnimgraph",
	"m_UIShopAnimgraph",
	"m_sAG2HeroPawnAnimGraph",
	"m_sAG2UIAnimGraph",
}

const heroGameData = "CCitadelHeroModelGameData_t"

type textEdit struct {
	start int
	end   int
	text  string
}

// restoreMetadata retires legacy graph selectors and inherits missing pawn
// controls from the current model. Authored particles and settings survive.
func restoreMetadata(old, current string, modernGraphs map[string]string, available func(string) bool) (string, bool) {
	closing, fields, ok := modeltext.RootFields(old)
	if !ok {
		return "", false
	}
	_, currentFields, ok := modeltext.RootFields(current)
	if !ok {
		return "", false
	}
	var edits []textEdit
	if !retireGraphs(old, fields, modernGraphs, available, &edits) {
		return "", false
	}
	currentRange, hasGameData := modeltext.Field(currentFields, heroGameData)
	if hasGameData {
		currentData := current[currentRange.Start:currentRange.End]
		equals := strings.IndexByte(currentData, '=')
		if equals < 0 {
			return "", false
		}
		currentObject := strings.TrimSpace(currentData[equals+1:])
		_, currentMembers, ok := modeltext.RootFields(currentObject)
		if !ok {
			return "", false
		}
		if oldRange, ok := modeltext.Field(fields, heroGameData); ok {
			oldData := old[oldRange.Start:oldRange.End]
			equals := strings.IndexByte(oldData, '=')
			if equals < 0 {
				return "", false
			}
			objectStart := oldRange.Start + equals + 1
			oldObject := old[objectStart:oldRange.End]
			end, members, ok := modeltext.RootFields(oldObject)
			if !ok {
				return "", false
			}
			var memberEdits []textEdit
			if !retireGraphs(oldObject, members, modernGraphs, available, &memberEdits) {
				return "", false
			}
			for _, edit := range memberEdits {
				edits = append(edits, textEdit{
					start: objectStart + edit.start,
					end:   objectStart + edit.end,
					text:  edit.text,
				})
			}
			var additions strings.Builder
			for _, name := range slices.Sorted(maps.Keys(currentMembers)) {
				if _, exists := members[name]; exists {
					continue
				}
				currentMember := currentMembers[name]
				additions.WriteString("\n")
				additions.WriteString(currentObject[currentMember.Start:currentMember.End])
				additions.WriteString("\n")
			}
			edits = append(edits, textEdit{start: objectStart + end, end: objectStart + end, text: additions.String()})
		} else {
			edits = append(edits, textEdit{start: closing, end: closing, text: "\n" + currentData + "\n"})
		}
	}
	slices.SortFunc(edits, func(left, right textEdit) int {
		return cmp.Compare(right.start, left.start)
	})
	result := old
	for _, edit := range edits {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(result) {
			return "", false
		}
		result = result[:edit.start] + edit.text + result[edit.end:]
	}
	if _, _, ok := modeltext.RootFields(result); !ok {
		return "", false
	}
	return result, true
}

func retireGraphs(text string, fields map[string]modeltext.Range,
	modernGraphs map[string]string, available func(string) bool, edits *[]textEdit) bool {
	for _, name := range metadataGraphFields {
		field, ok := modeltext.Field(fields, name)
		if !ok {
			continue
		}
		assignment := text[field.Start:field.End]
		equals := strings.IndexByte(assignment, '=')
		if equals < 0 {
			return false
		}
		value := strings.TrimSpace(assignment[equals+1:])
		value = strings.TrimPrefix(value, "resource_name:")
		value = strings.TrimPrefix(value, "resource:")
		path, ok := decodeQuoted(strings.TrimSpace(value))
		if !ok {
			return false
		}
		identifier, modern := "", false
		switch name {
		case "m_sAG2HeroPawnAnimGraph":
			modern = true
		case "m_sAG2UIAnimGraph":
			identifier, modern = "ui", true
		}
		matchingModern := false
		if modern {
			graph, known := modernGraphs[identifier]
			matchingModern = known && sameResource(graph, path)
		}
		if path != "" && available(path) && !matchingModern {
			return false
		}
		*edits = append(*edits, textEdit{start: field.Start, end: field.End})
	}
	return true
}

func decodeQuoted(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	if value == "null" {
		return "", true
	}
	name, ok := modeltext.ResourceName("resource:" + value)
	if !ok {
		return "", false
	}
	return name, true
}

// sameResource compares two resource names the way the engine resolves them.
func sameResource(left, right string) bool {
	leftPath, leftOK := canonicalResourcePath(left)
	rightPath, rightOK := canonicalResourcePath(right)
	return leftOK && rightOK && leftPath == rightPath
}

func canonicalResourcePath(name string) (string, bool) {
	normalized, ok := vpk.NormalizeResourcePath(name)
	if !ok {
		return "", false
	}
	path := strings.ToLower(normalized)
	if strings.Contains(path, "#") {
		return "", false
	}
	if !strings.HasSuffix(path, "_c") && !strings.HasSuffix(path, ".txt") {
		path += "_c"
	}
	return path, true
}
