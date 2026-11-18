// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/material_repair.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package material ports the reviewed material and model-binding edits used by
// the compatibility overlay. Callers supply an evidence-backed target mapping;
// this package does not infer intent.
package material

import (
	"bytes"
	"fmt"
	"math"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

var dataKind = resource.BlockTypeData

// Field returns a required object member.
func Field(value *kv3.Value, name string) (*kv3.Value, error) {
	field, ok := value.Get(name)
	if !ok {
		return nil, fmt.Errorf("missing %s", name)
	}
	return field, nil
}

func named(value *kv3.Value, table, name string) (*kv3.Value, error) {
	entriesValue, err := Field(value, table)
	if err != nil {
		return nil, err
	}
	entries, ok := entriesValue.AsArray()
	if !ok {
		return nil, fmt.Errorf("expected parameter array")
	}
	var match *kv3.Value
	for i := range entries {
		entry := &entries[i]
		entryName, ok := entry.Get("m_name")
		if !ok {
			continue
		}
		text, ok := entryName.AsString()
		if !ok || text != name {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("duplicate parameter %s", name)
		}
		match = entry
	}
	if match == nil {
		return nil, fmt.Errorf("missing parameter %s", name)
	}
	key, err := parameterValueKey(table)
	if err != nil {
		return nil, err
	}
	return Field(match, key)
}

func parameterValueKey(table string) (string, error) {
	switch table {
	case "m_intParams":
		return "m_nValue", nil
	case "m_floatParams":
		return "m_flValue", nil
	case "m_vectorParams":
		return "m_value", nil
	}
	return "", fmt.Errorf("unsupported parameter table")
}

// Decode parses a compiled material or model resource.
func Decode(bytes []byte) (kv3.Value, kv3.Encoding, kv3.Format, error) {
	parsed, err := resource.Parse(bytes)
	if err != nil {
		return kv3.Value{}, kv3.Encoding{}, kv3.Format{}, err
	}
	data, err := parsed.DataBlock()
	if err != nil {
		return kv3.Value{}, kv3.Encoding{}, kv3.Format{}, err
	}
	value, encoding, err := kv3.DecodePreserving(data)
	if err != nil {
		return kv3.Value{}, kv3.Encoding{}, kv3.Format{}, err
	}
	format, err := kv3.FormatFromPayload(data)
	if err != nil {
		return kv3.Value{}, kv3.Encoding{}, kv3.Format{}, err
	}
	return value, encoding, format, nil
}

// Equal compares value trees with the tolerance the upstream repair uses for
// floating-point parameters.
func Equal(left, right *kv3.Value) bool {
	if left.Kind != right.Kind {
		return false
	}
	switch left.Kind {
	case kv3.KindDouble:
		return math.Abs(left.Double-right.Double) < 0.000001
	case kv3.KindArray:
		if len(left.Array) != len(right.Array) {
			return false
		}
		for i := range left.Array {
			if !Equal(&left.Array[i], &right.Array[i]) {
				return false
			}
		}
		return true
	case kv3.KindObject:
		if len(left.Object) != len(right.Object) {
			return false
		}
		for i := range left.Object {
			if left.Object[i].Key != right.Object[i].Key ||
				!Equal(&left.Object[i].Value, &right.Object[i].Value) {
				return false
			}
		}
		return true
	}
	return kv3.Equal(left, right)
}

func rebuild(bytes []byte, value *kv3.Value, encoding *kv3.Encoding, format kv3.Format) ([]byte, error) {
	data, err := kv3.EncodePreserving(value, encoding, format)
	if err != nil {
		return nil, err
	}
	parsed, err := resource.Parse(bytes)
	if err != nil {
		return nil, err
	}
	output, err := parsed.RebuildWithDataPreserving(data)
	if err != nil {
		return nil, err
	}
	actual, actualEncoding, actualFormat, err := Decode(output)
	if err != nil {
		return nil, err
	}
	if actualFormat != format || !Equal(value, &actual) ||
		!kv3.EncodingPreserved(value, encoding, &actualEncoding) {
		return nil, fmt.Errorf("rebuilt DATA failed value/encoding verification")
	}
	if err := VerifyBlocks(bytes, output, dataKind); err != nil {
		return nil, err
	}
	return output, nil
}

// VerifyBlocks checks that only the allowed block kinds changed.
func VerifyBlocks(before, after []byte, allowed ...[4]byte) error {
	beforeResource, err := resource.Parse(before)
	if err != nil {
		return err
	}
	afterResource, err := resource.Parse(after)
	if err != nil {
		return err
	}
	if len(beforeResource.Blocks()) != len(afterResource.Blocks()) {
		return fmt.Errorf("block registry changed")
	}
	for index, block := range beforeResource.Blocks() {
		other := afterResource.Blocks()[index]
		if block.Kind != other.Kind {
			return fmt.Errorf("unexpected change to block %d", index)
		}
		permitted := false
		for _, kind := range allowed {
			if block.Kind == kind {
				permitted = true
				break
			}
		}
		if permitted {
			continue
		}
		beforeBytes, _ := beforeResource.GetBlockByIndex(index)
		afterBytes, _ := afterResource.GetBlockByIndex(index)
		if !bytes.Equal(beforeBytes, afterBytes) {
			return fmt.Errorf("unexpected change to block %d", index)
		}
	}
	return nil
}

func cloneValue(value kv3.Value) kv3.Value {
	if value.Binary != nil {
		value.Binary = append([]byte(nil), value.Binary...)
	}
	if value.Array != nil {
		items := make([]kv3.Value, len(value.Array))
		for i := range value.Array {
			items[i] = cloneValue(value.Array[i])
		}
		value.Array = items
	}
	if value.Object != nil {
		pairs := make([]kv3.Pair, len(value.Object))
		for i := range value.Object {
			pairs[i] = kv3.Pair{Key: value.Object[i].Key, Value: cloneValue(value.Object[i].Value)}
		}
		value.Object = pairs
	}
	return value
}

func cloneEncoding(encoding kv3.Encoding) kv3.Encoding {
	switch encoding.Children.Kind {
	case kv3.ChildrenArray:
		items := make([]kv3.Encoding, len(encoding.Children.Array))
		for i := range encoding.Children.Array {
			items[i] = cloneEncoding(encoding.Children.Array[i])
		}
		encoding.Children.Array = items
	case kv3.ChildrenObject:
		fields := make([]kv3.EncodingField, len(encoding.Children.Object))
		for i := range encoding.Children.Object {
			fields[i] = kv3.EncodingField{
				Key:   encoding.Children.Object[i].Key,
				Value: cloneEncoding(encoding.Children.Object[i].Value),
			}
		}
		encoding.Children.Object = fields
	}
	return encoding
}

func setField(value *kv3.Value, key string, replacement kv3.Value) error {
	field, ok := value.Get(key)
	if !ok {
		return fmt.Errorf("missing %s", key)
	}
	*field = replacement
	return nil
}

func encodingSet(encoding *kv3.Encoding, key string, replacement kv3.Encoding) error {
	if encoding.Children.Kind != kv3.ChildrenObject {
		return fmt.Errorf("missing %s", key)
	}
	for i := range encoding.Children.Object {
		if encoding.Children.Object[i].Key == key {
			encoding.Children.Object[i].Value = replacement
			return nil
		}
	}
	return fmt.Errorf("missing %s", key)
}

// MaterialVariant applies only the reviewed static glow edits. Current masks,
// shader features, resource references and attribute indices stay intact.
// Health tint expressions are removed because this mod explicitly requests
// static colors.
func MaterialVariant(current, authored []byte, path string) ([]byte, error) {
	target, encoding, format, err := Decode(current)
	if err != nil {
		return nil, err
	}
	source, _, _, err := Decode(authored)
	if err != nil {
		return nil, err
	}
	targetShader, err := Field(&target, "m_shaderName")
	if err != nil {
		return nil, err
	}
	sourceShader, err := Field(&source, "m_shaderName")
	if err != nil {
		return nil, err
	}
	targetShaderName, targetShaderOK := targetShader.AsString()
	sourceShaderName, sourceShaderOK := sourceShader.AsString()
	targetFeature, err := named(&target, "m_intParams", "F_SELF_ILLUM")
	if err != nil {
		return nil, err
	}
	sourceFeature, err := named(&source, "m_intParams", "F_SELF_ILLUM")
	if err != nil {
		return nil, err
	}
	targetFeatureValue, targetOK := targetFeature.AsInt()
	sourceFeatureValue, sourceOK := sourceFeature.AsInt()
	if !targetShaderOK || targetShaderName != "pbr.vfx" ||
		!sourceShaderOK || sourceShaderName != targetShaderName ||
		!targetOK || targetFeatureValue != 1 || !sourceOK || sourceFeatureValue != 1 {
		return nil, fmt.Errorf("incompatible emissive shader interface")
	}
	for _, parameter := range [][2]string{
		{"m_floatParams", "g_flSelfIllumScale1"},
		{"m_floatParams", "g_flSelfIllumAlbedoFactor1"},
		{"m_vectorParams", "g_vSelfIllumTint1"},
	} {
		replacement, err := named(&source, parameter[0], parameter[1])
		if err != nil {
			return nil, err
		}
		original, err := named(&target, parameter[0], parameter[1])
		if err != nil {
			return nil, err
		}
		valid := false
		switch replacement.Kind {
		case kv3.KindDouble:
			valid = original.Kind == kv3.KindDouble &&
				!math.IsNaN(replacement.Double) && !math.IsInf(replacement.Double, 0)
		case kv3.KindArray:
			valid = len(replacement.Array) == 4 && original.Kind == kv3.KindArray &&
				len(original.Array) == 4
			if valid {
				for i := range replacement.Array {
					value, ok := replacement.Array[i].AsFloat()
					if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
						valid = false
						break
					}
				}
			}
		}
		if !valid {
			return nil, fmt.Errorf("incompatible parameter %s", parameter[1])
		}
		table, ok := target.Get(parameter[0])
		if !ok {
			return nil, fmt.Errorf("missing parameter table")
		}
		entries, ok := table.AsArray()
		if !ok {
			return nil, fmt.Errorf("invalid parameter table")
		}
		key, err := parameterValueKey(parameter[0])
		if err != nil {
			return nil, err
		}
		found := false
		for i := range entries {
			nameValue, ok := entries[i].Get("m_name")
			if !ok {
				continue
			}
			name, ok := nameValue.AsString()
			if !ok || name != parameter[1] {
				continue
			}
			if err := setField(&entries[i], key, cloneValue(*replacement)); err != nil {
				return nil, err
			}
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("missing target parameter")
		}
	}

	expressionsValue, err := Field(&target, "m_dynamicParams")
	if err != nil {
		return nil, err
	}
	expressions, ok := expressionsValue.AsArray()
	if !ok {
		return nil, fmt.Errorf("invalid expressions")
	}
	expressionEncoding, ok := encoding.Get("m_dynamicParams")
	if !ok {
		return nil, fmt.Errorf("missing expression encoding")
	}
	if expressionEncoding.Children.Kind != kv3.ChildrenArray ||
		len(expressions) != len(expressionEncoding.Children.Array) {
		return nil, fmt.Errorf("expression encoding count mismatch")
	}
	for index := len(expressions) - 1; index >= 0; index-- {
		nameValue, ok := expressions[index].Get("m_name")
		if !ok {
			continue
		}
		name, _ := nameValue.AsString()
		if name != "g_vColorTint1" && name != "g_flSelfIllumScale1" {
			continue
		}
		expressions = append(expressions[:index], expressions[index+1:]...)
		expressionEncoding.Children.Array = append(
			expressionEncoding.Children.Array[:index],
			expressionEncoding.Children.Array[index+1:]...)
	}
	if err := setField(&target, "m_dynamicParams", kv3.Array(expressions...)); err != nil {
		return nil, err
	}
	if err := setField(&target, "m_materialName", kv3.String(path)); err != nil {
		return nil, err
	}
	return rebuild(current, &target, &encoding, format)
}

// DrawnMaterials lists the materials named by a model's embedded draw calls.
func DrawnMaterials(bytes []byte) ([]string, error) {
	parsed, err := resource.Parse(bytes)
	if err != nil {
		return nil, err
	}
	var names []string
	for index, block := range parsed.Blocks() {
		if block.Kind != [4]byte{'M', 'D', 'A', 'T'} {
			continue
		}
		raw, ok := parsed.GetBlockByIndex(index)
		if !ok {
			return nil, fmt.Errorf("missing mesh block")
		}
		value, err := kv3.Decode(raw)
		if err != nil {
			return nil, err
		}
		scenesValue, err := Field(&value, "m_sceneObjects")
		if err != nil {
			return nil, err
		}
		scenes, ok := scenesValue.AsArray()
		if !ok {
			return nil, fmt.Errorf("invalid scene objects")
		}
		for i := range scenes {
			drawsValue, err := Field(&scenes[i], "m_drawCalls")
			if err != nil {
				return nil, err
			}
			draws, ok := drawsValue.AsArray()
			if !ok {
				return nil, fmt.Errorf("invalid draw calls")
			}
			for j := range draws {
				material, ok := draws[j].Get("m_material")
				if !ok {
					material, ok = draws[j].Get("m_pMaterial")
				}
				if !ok {
					return nil, fmt.Errorf("missing draw material")
				}
				name, stringOK := material.AsString()
				if !stringOK {
					return nil, fmt.Errorf("missing draw material")
				}
				known := false
				for _, existing := range names {
					if existing == name {
						known = true
						break
					}
				}
				if !known {
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no embedded draw materials")
	}
	return names, nil
}

// BindVariants writes a default material group plus the requested role groups
// into a model, keeping unrelated groups and the rest of the model untouched.
func BindVariants(current, template []byte, target string, roles [][2]string, ids map[string]uint64) ([]byte, error) {
	names, err := DrawnMaterials(current)
	if err != nil {
		return nil, err
	}
	position := -1
	for i, name := range names {
		if name == target {
			position = i
			break
		}
	}
	if position < 0 {
		return nil, fmt.Errorf("target is not an active draw material")
	}
	model, encoding, format, err := Decode(current)
	if err != nil {
		return nil, err
	}
	existingValue, err := Field(&model, "m_materialGroups")
	if err != nil {
		return nil, err
	}
	existing, ok := existingValue.AsArray()
	if !ok {
		return nil, fmt.Errorf("invalid existing material groups")
	}
	if len(existing) > 0 {
		defaultValue, ok := existing[0].Get("m_name")
		if !ok {
			return nil, fmt.Errorf("missing default group")
		}
		defaultName, _ := defaultValue.AsString()
		if defaultName != "default" {
			return nil, fmt.Errorf("first material group is not default")
		}
		defaultMaterials, err := Field(&existing[0], "m_materials")
		if err != nil {
			return nil, err
		}
		for i := range existing {
			nameValue, err := Field(&existing[i], "m_name")
			if err != nil {
				return nil, err
			}
			name, ok := nameValue.AsString()
			if !ok {
				return nil, fmt.Errorf("invalid group name")
			}
			materials, err := Field(&existing[i], "m_materials")
			if err != nil {
				return nil, err
			}
			known := false
			if name == "default" {
				known = true
			}
			for _, role := range roles {
				if role[0] == name {
					known = true
					break
				}
			}
			if !known || !Equal(materials, defaultMaterials) {
				return nil, fmt.Errorf("existing material variants require a reviewed merge")
			}
		}
	}

	old, oldEncoding, _, err := Decode(template)
	if err != nil {
		return nil, err
	}
	groupsValue, err := Field(&old, "m_materialGroups")
	if err != nil {
		return nil, err
	}
	groups, ok := groupsValue.AsArray()
	if !ok || len(groups) == 0 {
		return nil, fmt.Errorf("invalid group template")
	}
	templateGroup := groups[0]
	oldGroupsEncoding, ok := oldEncoding.Get("m_materialGroups")
	if !ok {
		return nil, fmt.Errorf("missing groups encoding")
	}
	groupEncodings, ok := oldGroupsEncoding.AsArray()
	if !ok || len(groupEncodings) == 0 {
		return nil, fmt.Errorf("missing group template encoding")
	}
	groupEncoding := groupEncodings[0]
	materialsEncoding, ok := groupEncoding.Get("m_materials")
	if !ok {
		return nil, fmt.Errorf("missing material encoding")
	}
	materialEncodings, ok := materialsEncoding.AsArray()
	if !ok || len(materialEncodings) == 0 {
		return nil, fmt.Errorf("missing material encoding")
	}
	materialEncoding := materialEncodings[0]

	type replacementGroup struct {
		name   string
		member *string
	}
	order := []replacementGroup{{name: "default"}}
	for _, role := range roles {
		path := role[1]
		order = append(order, replacementGroup{name: role[0], member: &path})
	}
	var values []kv3.Value
	var encodings []kv3.Encoding
	var additions []resource.Reference
	for _, group := range order {
		materials := append([]string(nil), names...)
		if group.member != nil {
			path := *group.member
			id, ok := ids[path]
			if !ok {
				return nil, fmt.Errorf("no resource ID for %s", path)
			}
			additions = append(additions, resource.Reference{ID: id, Path: path})
			materials[position] = path
		}
		value := cloneValue(templateGroup)
		if err := setField(&value, "m_name", kv3.String(group.name)); err != nil {
			return nil, fmt.Errorf("missing group name")
		}
		items := make([]kv3.Value, len(materials))
		for i, name := range materials {
			items[i] = kv3.String(name)
		}
		if err := setField(&value, "m_materials", kv3.Array(items...)); err != nil {
			return nil, fmt.Errorf("missing group materials")
		}
		meta := cloneEncoding(groupEncoding)
		materialsMeta, ok := meta.Get("m_materials")
		if !ok {
			return nil, fmt.Errorf("missing material list encoding")
		}
		materialsMeta.Children = kv3.EncodingChildren{
			Kind:  kv3.ChildrenArray,
			Array: repeatEncoding(materialEncoding, len(names)),
		}
		values = append(values, value)
		encodings = append(encodings, meta)
	}
	if err := setField(&model, "m_materialGroups", kv3.Array(values...)); err != nil {
		return nil, fmt.Errorf("missing model material groups")
	}
	groupMeta := cloneEncoding(*oldGroupsEncoding)
	groupMeta.Children = kv3.EncodingChildren{Kind: kv3.ChildrenArray, Array: encodings}
	if err := encodingSet(&encoding, "m_materialGroups", groupMeta); err != nil {
		return nil, fmt.Errorf("missing model groups encoding")
	}
	output, err := rebuild(current, &model, &encoding, format)
	if err != nil {
		return nil, err
	}
	output, err = resource.AddReferencesOf(output, additions)
	if err != nil {
		return nil, err
	}
	if err := VerifyBlocks(current, output, dataKind, resource.BlockTypeRERL); err != nil {
		return nil, err
	}
	return output, nil
}

func repeatEncoding(encoding kv3.Encoding, count int) []kv3.Encoding {
	out := make([]kv3.Encoding, count)
	for i := range out {
		out[i] = cloneEncoding(encoding)
	}
	return out
}

// IsolateMeshMaterial redirects the target material only on the draw calls of
// the selected mesh groups. Unselected meshes and buffers stay byte-for-byte.
func IsolateMeshMaterial(current []byte, groups []string, target string, replacement resource.Reference) ([]byte, int, error) {
	model, _, _, err := Decode(current)
	if err != nil {
		return nil, 0, err
	}
	if len(groups) == 0 {
		return nil, 0, fmt.Errorf("empty mesh selection")
	}
	namesValue, err := Field(&model, "m_meshGroups")
	if err != nil {
		return nil, 0, err
	}
	names, ok := namesValue.AsArray()
	if !ok {
		return nil, 0, fmt.Errorf("invalid mesh groups")
	}
	masksValue, err := Field(&model, "m_refMeshGroupMasks")
	if err != nil {
		return nil, 0, err
	}
	masks, ok := masksValue.AsArray()
	if !ok {
		return nil, 0, fmt.Errorf("invalid mesh group masks")
	}
	var selected uint64
	for _, group := range groups {
		matches := 0
		index := 0
		for i := range names {
			name, ok := names[i].AsString()
			if ok && name == group {
				matches++
				index = i
			}
		}
		if matches != 1 {
			return nil, 0, fmt.Errorf("missing or ambiguous group %s", group)
		}
		if index >= 64 {
			return nil, 0, fmt.Errorf("too many mesh groups")
		}
		selected |= uint64(1) << uint(index)
	}

	original, err := resource.Parse(current)
	if err != nil {
		return nil, 0, err
	}
	controlBytes, ok := original.FindBlock([4]byte{'C', 'T', 'R', 'L'})
	if !ok {
		return nil, 0, fmt.Errorf("missing mesh registry")
	}
	control, err := kv3.Decode(controlBytes)
	if err != nil {
		return nil, 0, err
	}
	registryValue, err := Field(&control, "embedded_meshes")
	if err != nil {
		return nil, 0, err
	}
	registry, ok := registryValue.AsArray()
	if !ok {
		return nil, 0, fmt.Errorf("invalid mesh registry")
	}
	output := append([]byte(nil), current...)
	changed := 0
	var covered uint64
	seenMeshes := make(map[int]bool)
	seen := make(map[int]bool)
	for i := range registry {
		indexValue, err := Field(&registry[i], "m_nMeshIndex")
		if err != nil {
			return nil, 0, err
		}
		mesh, ok := indexValue.AsInt()
		if !ok || mesh < 0 || mesh >= int64(len(masks)) {
			return nil, 0, fmt.Errorf("invalid mesh index")
		}
		maskValue := masks[mesh]
		mask, ok := maskValue.AsUInt()
		if !ok {
			return nil, 0, fmt.Errorf("invalid mesh mask")
		}
		if mask&selected == 0 {
			continue
		}
		if mask&^selected != 0 {
			return nil, 0, fmt.Errorf("selected mesh also belongs to an unselected group")
		}
		if seenMeshes[int(mesh)] {
			return nil, 0, fmt.Errorf("duplicate selected mesh index")
		}
		seenMeshes[int(mesh)] = true
		covered |= mask & selected
		blockIndexValue, err := Field(&registry[i], "m_nDataBlock")
		if err != nil {
			return nil, 0, err
		}
		blockIndex, ok := blockIndexValue.AsInt()
		if !ok || blockIndex < 0 {
			return nil, 0, fmt.Errorf("invalid data block")
		}
		index := int(blockIndex)
		if seen[index] {
			return nil, 0, fmt.Errorf("duplicate selected mesh block")
		}
		seen[index] = true
		if index >= len(original.Blocks()) || original.Blocks()[index].Kind != [4]byte{'M', 'D', 'A', 'T'} {
			return nil, 0, fmt.Errorf("selected block is not MDAT")
		}
		block, ok := original.GetBlockByIndex(index)
		if !ok {
			return nil, 0, fmt.Errorf("missing selected mesh")
		}
		format, err := kv3.FormatFromPayload(block)
		if err != nil {
			return nil, 0, err
		}
		expected, beforeEncoding, err := kv3.DecodePreserving(block)
		if err != nil {
			return nil, 0, err
		}
		scenesValue, err := Field(&expected, "m_sceneObjects")
		if err != nil {
			return nil, 0, err
		}
		scenes, ok := scenesValue.AsArray()
		if !ok {
			return nil, 0, fmt.Errorf("invalid scene objects")
		}
		edits := 0
		for sceneIndex := range scenes {
			drawsValue, err := Field(&scenes[sceneIndex], "m_drawCalls")
			if err != nil {
				return nil, 0, err
			}
			draws, ok := drawsValue.AsArray()
			if !ok {
				return nil, 0, fmt.Errorf("invalid draw calls")
			}
			for drawIndex := range draws {
				key := "m_pMaterial"
				if _, ok := draws[drawIndex].Get("m_material"); ok {
					key = "m_material"
				}
				value, ok := draws[drawIndex].Get(key)
				if !ok {
					return nil, 0, fmt.Errorf("missing material")
				}
				name, ok := value.AsString()
				if !ok || name != target {
					continue
				}
				*value = kv3.String(replacement.Path)
				edits++
			}
			drawsValue.Array = draws
		}
		if edits == 0 {
			return nil, 0, fmt.Errorf("selected mesh does not draw the expected material")
		}
		scenesValue.Array = scenes
		edited, err := kv3.EncodePreserving(&expected, &beforeEncoding, format)
		if err != nil {
			return nil, 0, err
		}
		actual, actualEncoding, err := kv3.DecodePreserving(edited)
		if err != nil {
			return nil, 0, err
		}
		actualFormat, err := kv3.FormatFromPayload(edited)
		if err != nil {
			return nil, 0, err
		}
		if !Equal(&expected, &actual) ||
			!kv3.EncodingPreserved(&expected, &beforeEncoding, &actualEncoding) ||
			actualFormat != format {
			return nil, 0, fmt.Errorf("mesh binding edit changed unrelated values or encodings")
		}
		parsedOutput, err := resource.Parse(output)
		if err != nil {
			return nil, 0, err
		}
		output, err = parsedOutput.RebuildWithBlockPreserving(index, edited)
		if err != nil {
			return nil, 0, err
		}
		changed++
	}
	if changed == 0 {
		return nil, 0, fmt.Errorf("no matching selected meshes")
	}
	if covered != selected {
		return nil, 0, fmt.Errorf("selected mesh groups are not fully represented by embedded meshes")
	}
	for index := range masks {
		mask, ok := masks[index].AsUInt()
		if ok && mask&selected != 0 && !seenMeshes[index] {
			return nil, 0, fmt.Errorf("selected mesh groups are not fully represented by embedded meshes")
		}
	}
	output, err = resource.AddReferencesOf(output, []resource.Reference{replacement})
	if err != nil {
		return nil, 0, err
	}
	if err := VerifyBlocks(current, output, [4]byte{'M', 'D', 'A', 'T'}, resource.BlockTypeRERL); err != nil {
		return nil, 0, err
	}
	after, err := resource.Parse(output)
	if err != nil {
		return nil, 0, err
	}
	for index, block := range original.Blocks() {
		if seen[index] || block.Kind != [4]byte{'M', 'D', 'A', 'T'} {
			continue
		}
		beforeBytes, _ := original.GetBlockByIndex(index)
		afterBytes, _ := after.GetBlockByIndex(index)
		if !bytes.Equal(beforeBytes, afterBytes) {
			return nil, 0, fmt.Errorf("unselected mesh changed")
		}
	}
	return output, changed, nil
}

// RenameMaterial changes only the authored material name.
func RenameMaterial(current []byte, path string) ([]byte, error) {
	material, encoding, format, err := Decode(current)
	if err != nil {
		return nil, err
	}
	if err := setField(&material, "m_materialName", kv3.String(path)); err != nil {
		return nil, err
	}
	return rebuild(current, &material, &encoding, format)
}

// ReplaceTextureParameter changes one named texture slot and registers the new
// dependency in RERL.
func ReplaceTextureParameter(current []byte, name string, replacement resource.Reference) ([]byte, error) {
	material, encoding, format, err := Decode(current)
	if err != nil {
		return nil, err
	}
	paramsValue, err := Field(&material, "m_textureParams")
	if err != nil {
		return nil, fmt.Errorf("missing texture parameters")
	}
	params, ok := paramsValue.AsArray()
	if !ok {
		return nil, fmt.Errorf("invalid texture parameters")
	}
	index := -1
	for i := range params {
		entry, ok := params[i].Get("m_name")
		if !ok {
			continue
		}
		entryName, ok := entry.AsString()
		if !ok || entryName != name {
			continue
		}
		if index >= 0 {
			return nil, fmt.Errorf("missing or ambiguous texture parameter")
		}
		index = i
	}
	if index < 0 {
		return nil, fmt.Errorf("missing or ambiguous texture parameter")
	}
	value, ok := params[index].Get("m_pValue")
	if !ok {
		return nil, fmt.Errorf("missing texture path")
	}
	if _, ok := value.AsString(); !ok {
		return nil, fmt.Errorf("invalid texture path")
	}
	*value = kv3.String(replacement.Path)
	paramsValue.Array = params
	output, err := rebuild(current, &material, &encoding, format)
	if err != nil {
		return nil, err
	}
	return resource.AddReferencesOf(output, []resource.Reference{replacement})
}
