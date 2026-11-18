// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/material_repair/tests.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package material

import (
	"bytes"
	"math"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

func testResource(t *testing.T, blocks ...resource.BuildResult) []byte {
	t.Helper()
	raw, err := resource.Build(blocks, nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func param(name, key string, value kv3.Value) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_name", Value: kv3.String(name)},
		kv3.Pair{Key: key, Value: value},
	)
}

func testMaterial(t *testing.T, tint float64, shader string) []byte {
	t.Helper()
	value := kv3.Object(
		kv3.Pair{Key: "m_materialName", Value: kv3.String("current.vmat")},
		kv3.Pair{Key: "m_shaderName", Value: kv3.String(shader)},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array(
			param("F_SELF_ILLUM", "m_nValue", kv3.Int(1)),
		)},
		kv3.Pair{Key: "m_floatParams", Value: kv3.Array(
			param("g_flSelfIllumScale1", "m_flValue", kv3.Double(tint)),
			param("g_flSelfIllumAlbedoFactor1", "m_flValue", kv3.Double(0.4)),
			param("g_flUnrelated", "m_flValue", kv3.Double(42.0)),
		)},
		kv3.Pair{Key: "m_vectorParams", Value: kv3.Array(
			param("g_vSelfIllumTint1", "m_value", kv3.Array(
				kv3.Double(tint), kv3.Double(tint), kv3.Double(tint), kv3.Double(tint),
			)),
		)},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array(
			param("TextureColor1", "m_pValue", kv3.String("current_mask.vtex")),
		)},
		kv3.Pair{Key: "m_dynamicParams", Value: kv3.Array(
			param("g_vColorTint1", "m_value", kv3.Binary([]byte{1, 2})),
			param("g_flSelfIllumScale1", "m_value", kv3.Binary([]byte{3, 4})),
			param("g_flUnrelated", "m_value", kv3.Binary([]byte{5, 6})),
		)},
		kv3.Pair{Key: "m_renderAttributesUsed", Value: kv3.Array(
			kv3.String("$ent_health"), kv3.String("$unrelated"),
		)},
	)
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		resource.BuildResult{Kind: [4]byte{'R', 'E', 'D', '2'}, Payload: []byte{9, 8, 7}},
	)
}

func testModel(t *testing.T) []byte {
	t.Helper()
	groups := kv3.Array(kv3.Object(
		kv3.Pair{Key: "m_name", Value: kv3.String("default")},
		kv3.Pair{Key: "m_materials", Value: kv3.Array(
			kv3.String("body.vmat"), kv3.String("wax.vmat"),
		)},
	))
	value := kv3.Object(
		kv3.Pair{Key: "m_materialGroups", Value: groups},
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Binary([]byte{20, 21, 22})},
	)
	mesh := kv3.Object(
		kv3.Pair{Key: "m_sceneObjects", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_drawCalls", Value: kv3.Array(
				kv3.Object(kv3.Pair{Key: "m_material", Value: kv3.String("body.vmat")}),
				kv3.Object(kv3.Pair{Key: "m_material", Value: kv3.String("wax.vmat")}),
			)},
		))},
	)
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		resource.BuildResult{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: kv3.Encode(&mesh, kv3.Format{})},
		resource.BuildResult{Kind: [4]byte{'M', 'V', 'T', 'X'}, Payload: []byte{15, 16, 17}},
	)
}

func decodeValue(t *testing.T, data []byte) kv3.Value {
	t.Helper()
	value, _, _, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustField(t *testing.T, value *kv3.Value, name string) *kv3.Value {
	t.Helper()
	field, err := Field(value, name)
	if err != nil {
		t.Fatal(err)
	}
	return field
}

func TestMaterialVariantPortsGlowWithoutCopyingTexturesOrExpressions(t *testing.T) {
	current := testMaterial(t, 0, "pbr.vfx")
	output, err := MaterialVariant(current, testMaterial(t, 0.75, "pbr.vfx"), "new.vmat")
	if err != nil {
		t.Fatal(err)
	}
	original := decodeValue(t, current)
	actual := decodeValue(t, output)
	scale, err := named(&actual, "m_floatParams", "g_flSelfIllumScale1")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := scale.AsFloat(); !ok || value != 0.75 {
		t.Fatalf("scale = %#v", scale)
	}
	unrelated, err := named(&actual, "m_floatParams", "g_flUnrelated")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := unrelated.AsFloat(); !ok || value != 42 {
		t.Fatalf("unrelated = %#v", unrelated)
	}
	actualTextures, _ := actual.Get("m_textureParams")
	originalTextures, _ := original.Get("m_textureParams")
	if !Equal(actualTextures, originalTextures) {
		t.Fatal("texture parameters changed")
	}
	actualAttributes, _ := actual.Get("m_renderAttributesUsed")
	originalAttributes, _ := original.Get("m_renderAttributesUsed")
	if !Equal(actualAttributes, originalAttributes) {
		t.Fatal("render attributes changed")
	}
	expressions := mustField(t, &actual, "m_dynamicParams")
	items, _ := expressions.AsArray()
	if len(items) != 1 {
		t.Fatalf("expressions = %d", len(items))
	}
	value, _ := items[0].Get("m_value")
	expected := kv3.Binary([]byte{5, 6})
	if !Equal(value, &expected) {
		t.Fatalf("unrelated expression = %#v", value)
	}
	if err := VerifyBlocks(current, output, resource.BlockTypeData); err != nil {
		t.Fatal(err)
	}
}

func TestMaterialVariantRejectsShaderMismatchAndNonFiniteValues(t *testing.T) {
	current := testMaterial(t, 0, "pbr.vfx")
	if _, err := MaterialVariant(current, testMaterial(t, 1, "other.vfx"), "new.vmat"); err == nil {
		t.Fatal("shader mismatch accepted")
	}
	if _, err := MaterialVariant(current, testMaterial(t, math.Inf(1), "pbr.vfx"), "new.vmat"); err == nil {
		t.Fatal("non-finite value accepted")
	}
}

func TestMaterialVariantRejectsDuplicateAuthoredParameters(t *testing.T) {
	authored := testMaterial(t, 0.75, "pbr.vfx")
	value, _, format, err := Decode(authored)
	if err != nil {
		t.Fatal(err)
	}
	paramsValue := mustField(t, &value, "m_floatParams")
	params, _ := paramsValue.AsArray()
	params = append(params, params[0])
	paramsValue.Array = params
	parsed, err := resource.Parse(authored)
	if err != nil {
		t.Fatal(err)
	}
	changedBytes, err := parsed.RebuildWithDataPreserving(kv3.Encode(&value, format))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MaterialVariant(testMaterial(t, 0, "pbr.vfx"), changedBytes, "new.vmat"); err == nil {
		t.Fatal("duplicate authored parameters accepted")
	}
}

func TestBindVariantsPreservesRigAndOtherMaterials(t *testing.T) {
	current := testModel(t)
	roles := [][2]string{{"FriendlyMedic", "green.vmat"}, {"EnemyMedic", "red.vmat"}}
	ids := map[string]uint64{"green.vmat": 101, "red.vmat": 102}
	output, err := BindVariants(current, current, "wax.vmat", roles, ids)
	if err != nil {
		t.Fatal(err)
	}
	original := decodeValue(t, current)
	actual := decodeValue(t, output)
	originalSkeleton, _ := original.Get("m_modelSkeleton")
	actualSkeleton, _ := actual.Get("m_modelSkeleton")
	if !Equal(actualSkeleton, originalSkeleton) {
		t.Fatal("model skeleton changed")
	}
	groupsValue := mustField(t, &actual, "m_materialGroups")
	groups, _ := groupsValue.AsArray()
	originalGroupsValue := mustField(t, &original, "m_materialGroups")
	originalGroups, _ := originalGroupsValue.AsArray()
	originalMaterials, _ := originalGroups[0].Get("m_materials")
	defaultMaterials, _ := groups[0].Get("m_materials")
	if !Equal(defaultMaterials, originalMaterials) {
		t.Fatal("default material group changed")
	}
	name, _ := groups[1].Get("m_name")
	if text, _ := name.AsString(); text != "FriendlyMedic" {
		t.Fatalf("group name = %q", text)
	}
	friendlyMaterials, _ := groups[1].Get("m_materials")
	items, _ := friendlyMaterials.AsArray()
	want := []kv3.Value{kv3.String("body.vmat"), kv3.String("green.vmat")}
	if len(items) != 2 || !Equal(&items[0], &want[0]) || !Equal(&items[1], &want[1]) {
		t.Fatalf("friendly materials = %#v", items)
	}
	references, err := resource.ReferencesOf(output)
	if err != nil {
		t.Fatal(err)
	}
	wantRefs := []resource.Reference{{ID: 101, Path: "green.vmat"}, {ID: 102, Path: "red.vmat"}}
	if len(references) != 2 || references[0] != wantRefs[0] || references[1] != wantRefs[1] {
		t.Fatalf("references = %#v", references)
	}
	if err := VerifyBlocks(current, output, resource.BlockTypeData, resource.BlockTypeRERL); err != nil {
		t.Fatal(err)
	}
}

func TestBindVariantsRefusesInactiveTargetUnknownIDAndAuthoredVariants(t *testing.T) {
	current := testModel(t)
	roles := [][2]string{{"Friendly", "green.vmat"}}
	ids := map[string]uint64{"green.vmat": 101}
	if _, err := BindVariants(current, current, "obsolete.vmat", roles, ids); err == nil {
		t.Fatal("inactive target accepted")
	}
	if _, err := BindVariants(current, current, "wax.vmat", roles, map[string]uint64{}); err == nil {
		t.Fatal("unknown resource ID accepted")
	}

	value, _, format, err := Decode(current)
	if err != nil {
		t.Fatal(err)
	}
	groupsValue := mustField(t, &value, "m_materialGroups")
	groups, _ := groupsValue.AsArray()
	custom := cloneValue(groups[0])
	if err := setField(&custom, "m_name", kv3.String("Friendly")); err != nil {
		t.Fatal(err)
	}
	if err := setField(&custom, "m_materials", kv3.Array(kv3.String("custom.vmat"))); err != nil {
		t.Fatal(err)
	}
	groups = append(groups, custom)
	groupsValue.Array = groups
	parsed, err := resource.Parse(current)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := parsed.RebuildWithDataPreserving(kv3.Encode(&value, format))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindVariants(changed, current, "wax.vmat", roles, ids); err == nil {
		t.Fatal("existing authored variants were discarded")
	}
}

func sharedHeadModel(t *testing.T) []byte {
	t.Helper()
	data := kv3.Object(
		kv3.Pair{Key: "m_meshGroups", Value: kv3.Array(kv3.String("body"), kv3.String("head"))},
		kv3.Pair{Key: "m_refMeshGroupMasks", Value: kv3.Array(kv3.UInt(1), kv3.UInt(2))},
	)
	control := kv3.Object(
		kv3.Pair{Key: "embedded_meshes", Value: kv3.Array(
			kv3.Object(
				kv3.Pair{Key: "m_nMeshIndex", Value: kv3.Int(0)},
				kv3.Pair{Key: "m_nDataBlock", Value: kv3.Int(3)},
			),
			kv3.Object(
				kv3.Pair{Key: "m_nMeshIndex", Value: kv3.Int(1)},
				kv3.Pair{Key: "m_nDataBlock", Value: kv3.Int(5)},
			),
		)},
	)
	mesh := kv3.Object(
		kv3.Pair{Key: "m_sceneObjects", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_drawCalls", Value: kv3.Array(kv3.Object(
				kv3.Pair{Key: "m_material", Value: kv3.String("shared.vmat")},
				kv3.Pair{Key: "m_nIndexCount", Value: kv3.Int(123)},
			))},
		))},
	)
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&data, kv3.Format{})},
		resource.BuildResult{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		resource.BuildResult{Kind: [4]byte{'C', 'T', 'R', 'L'}, Payload: kv3.Encode(&control, kv3.Format{})},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: kv3.Encode(&mesh, kv3.Format{})},
		resource.BuildResult{Kind: [4]byte{'M', 'V', 'T', 'X'}, Payload: []byte{1, 2, 3, 4}},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: kv3.Encode(&mesh, kv3.Format{})},
		resource.BuildResult{Kind: [4]byte{'M', 'I', 'D', 'X'}, Payload: []byte{5, 6, 7, 8}},
	)
}

func TestIsolateMeshMaterialSplitsHeadOnlyWithoutTouchingBody(t *testing.T) {
	current := sharedHeadModel(t)
	output, changed, err := IsolateMeshMaterial(current, []string{"head"}, "shared.vmat",
		resource.Reference{ID: 101, Path: "head.vmat"})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d", changed)
	}
	before, err := resource.Parse(current)
	if err != nil {
		t.Fatal(err)
	}
	after, err := resource.Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{3, 4, 6} {
		beforeBytes, _ := before.GetBlockByIndex(index)
		afterBytes, _ := after.GetBlockByIndex(index)
		if !bytes.Equal(beforeBytes, afterBytes) {
			t.Fatalf("block %d changed", index)
		}
	}
	expectedRaw, _ := before.GetBlockByIndex(5)
	expected, err := kv3.Decode(expectedRaw)
	if err != nil {
		t.Fatal(err)
	}
	scenesValue := mustField(t, &expected, "m_sceneObjects")
	scenes, _ := scenesValue.AsArray()
	drawsValue := mustField(t, &scenes[0], "m_drawCalls")
	draws, _ := drawsValue.AsArray()
	if err := setField(&draws[0], "m_material", kv3.String("head.vmat")); err != nil {
		t.Fatal(err)
	}
	afterRaw, _ := after.GetBlockByIndex(5)
	actual, err := kv3.Decode(afterRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !kv3.Equal(&expected, &actual) {
		t.Fatal("selected mesh differs from the expected edit")
	}
	references, err := resource.ReferencesOf(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0] != (resource.Reference{ID: 101, Path: "head.vmat"}) {
		t.Fatalf("references = %#v", references)
	}
	if err := VerifyBlocks(current, output, [4]byte{'M', 'D', 'A', 'T'}, resource.BlockTypeRERL); err != nil {
		t.Fatal(err)
	}
}

func TestIsolateMeshMaterialRefusesMissingGroupWrongTargetAndMixedScope(t *testing.T) {
	current := sharedHeadModel(t)
	replacement := resource.Reference{ID: 101, Path: "head.vmat"}
	if _, _, err := IsolateMeshMaterial(current, []string{"missing"}, "shared.vmat", replacement); err == nil {
		t.Fatal("missing group accepted")
	}
	if _, _, err := IsolateMeshMaterial(current, []string{"head"}, "wrong.vmat", replacement); err == nil {
		t.Fatal("wrong target accepted")
	}
	value, encoding, format, err := Decode(current)
	if err != nil {
		t.Fatal(err)
	}
	masksValue := mustField(t, &value, "m_refMeshGroupMasks")
	masks, _ := masksValue.AsArray()
	masks[1] = kv3.UInt(3)
	masksValue.Array = masks
	mixed, err := rebuild(current, &value, &encoding, format)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := IsolateMeshMaterial(mixed, []string{"head"}, "shared.vmat", replacement); err == nil {
		t.Fatal("mixed body/head scope accepted")
	}
}

func TestIsolateMeshMaterialRefusesDeclaredGroupWithoutEmbeddedSurface(t *testing.T) {
	current := sharedHeadModel(t)
	value, encoding, format, err := Decode(current)
	if err != nil {
		t.Fatal(err)
	}
	masksValue := mustField(t, &value, "m_refMeshGroupMasks")
	masks, _ := masksValue.AsArray()
	masks[1] = kv3.UInt(1)
	masksValue.Array = masks
	changed, err := rebuild(current, &value, &encoding, format)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := IsolateMeshMaterial(changed, []string{"head"}, "shared.vmat",
		resource.Reference{ID: 101, Path: "head.vmat"}); err == nil {
		t.Fatal("group without an embedded surface accepted")
	}
}

func TestReplaceTextureParameterChangesOnlyTheRequestedSlot(t *testing.T) {
	original := testMaterial(t, 0, "pbr.vfx")
	value, _, format, err := Decode(original)
	if err != nil {
		t.Fatal(err)
	}
	paramsValue := mustField(t, &value, "m_textureParams")
	params, _ := paramsValue.AsArray()
	if err := setField(&params[0], "m_name", kv3.String("g_tSelfIllumMask")); err != nil {
		t.Fatal(err)
	}
	params = append(params, param("g_tColor", "m_pValue", kv3.String("current_color.vtex")))
	paramsValue.Array = params
	current := testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, format)},
		resource.BuildResult{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		resource.BuildResult{Kind: [4]byte{'R', 'E', 'D', '2'}, Payload: []byte{9, 8, 7}},
	)
	replacement := resource.Reference{ID: 101, Path: "white.vtex"}
	output, err := ReplaceTextureParameter(current, "g_tSelfIllumMask", replacement)
	if err != nil {
		t.Fatal(err)
	}
	expected := decodeValue(t, current)
	expectedParamsValue := mustField(t, &expected, "m_textureParams")
	expectedParams, _ := expectedParamsValue.AsArray()
	if err := setField(&expectedParams[0], "m_pValue", kv3.String("white.vtex")); err != nil {
		t.Fatal(err)
	}
	actual := decodeValue(t, output)
	if !Equal(&expected, &actual) {
		t.Fatal("unexpected material change")
	}
	references, err := resource.ReferencesOf(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0] != replacement {
		t.Fatalf("references = %#v", references)
	}
	if err := VerifyBlocks(current, output, resource.BlockTypeData, resource.BlockTypeRERL); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaceTextureParameter(current, "missing", resource.Reference{ID: 102, Path: "white.vtex"}); err == nil {
		t.Fatal("missing texture parameter accepted")
	}
}
