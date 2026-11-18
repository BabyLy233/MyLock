// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/material_compatibility/tests.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package material

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

func TestSourceRoleUsesExactResourceRoles(t *testing.T) {
	if role, ok := SourceRole("models/npc/trooper/materials/candle_trooper_wax_enemy.vmat_c"); !ok || role != "enemy" {
		t.Fatalf("role = %q, %v", role, ok)
	}
	for _, path := range []string{
		"models/npc/trooper/materials/candle_trooper_wax_enemy_copy.vmat_c",
		"materials/candle_trooper_wax_enemy.vmat_c",
	} {
		if _, ok := SourceRole(path); ok {
			t.Fatalf("%q accepted", path)
		}
	}
}

func testTexture(t *testing.T, provenance, pixels []byte) []byte {
	t.Helper()
	header := make([]byte, 40)
	binary.LittleEndian.PutUint16(header[0:2], 1)
	copy(header[20:26], []byte{1, 0, 1, 0, 1, 0})
	header[26] = 4 // RGBA8888
	header[27] = 1
	raw := testResource(t,
		resource.BuildResult{Kind: [4]byte{'R', 'E', 'D', 'I'}, Payload: provenance},
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: header},
	)
	return append(raw, pixels...)
}

func TestSameTextureRuntimeAllowsOnlyProvenanceDifferences(t *testing.T) {
	current := testTexture(t, []byte("current compiler"), []byte{255, 255, 255, 255})
	legacy := testTexture(t, []byte("older compiler with another input path"), []byte{255, 255, 255, 255})
	if !SameTextureRuntime(legacy, current) {
		t.Fatal("provenance-only texture difference rejected")
	}
	changedPixels := testTexture(t, []byte("current compiler"), []byte{0, 0, 0, 0})
	if SameTextureRuntime(changedPixels, current) {
		t.Fatal("different pixels accepted")
	}
	changedHeader := append([]byte(nil), legacy...)
	parsed, err := resource.Parse(changedHeader)
	if err != nil {
		t.Fatal(err)
	}
	dataOffset := 0
	for _, block := range parsed.Blocks() {
		if block.Kind == resource.BlockTypeData {
			dataOffset = int(block.Offset)
			break
		}
	}
	changedHeader[dataOffset+2] = 1 // changed clamp flags
	if SameTextureRuntime(changedHeader, current) {
		t.Fatal("different texture header accepted")
	}
	if SameTextureRuntime([]byte("truncated"), current) {
		t.Fatal("truncated texture accepted")
	}
}

// fakeSnapshot resolves in a fixed mod-then-game order and normalizes
// resource names the way the game does.
type fakeSnapshot struct {
	game map[string][]byte
	mod  map[string][]byte
}

func normalize(path string) string {
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	if !strings.HasSuffix(path, "_c") && !strings.HasSuffix(path, ".txt") {
		path += "_c"
	}
	return path
}

func (f fakeSnapshot) GameBytes(path string) ([]byte, bool, error) {
	data, ok := f.game[normalize(path)]
	return data, ok, nil
}

func (f fakeSnapshot) Resolve(path string) ([]byte, error) {
	path = normalize(path)
	if data, ok := f.mod[path]; ok {
		return data, nil
	}
	return f.game[path], nil
}

func glowMaterial(t *testing.T, name string, authored bool) []byte {
	t.Helper()
	dynamic := kv3.Array(
		param("g_vColorTint1", "m_value", kv3.Binary([]byte{1, 2})),
		param("g_flSelfIllumScale1", "m_value", kv3.Binary([]byte{3, 4})),
	)
	if authored {
		dynamic = kv3.Array()
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_materialName", Value: kv3.String(name)},
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("pbr.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array(
			param("F_SELF_ILLUM", "m_nValue", kv3.Int(1)),
		)},
		kv3.Pair{Key: "m_floatParams", Value: kv3.Array(
			param("g_flSelfIllumScale1", "m_flValue", kv3.Double(0.5)),
			param("g_flSelfIllumAlbedoFactor1", "m_flValue", kv3.Double(0.4)),
		)},
		kv3.Pair{Key: "m_vectorParams", Value: kv3.Array(
			param("g_vSelfIllumTint1", "m_value", kv3.Array(
				kv3.Double(1), kv3.Double(1), kv3.Double(1), kv3.Double(1),
			)),
		)},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array(
			param("g_tSelfIllumMask", "m_pValue", kv3.String("materials/default/white.vtex")),
		)},
		kv3.Pair{Key: "m_dynamicParams", Value: dynamic},
	)
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		resource.BuildResult{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
	)
}

func templateModel(t *testing.T) []byte {
	t.Helper()
	value := kv3.Object(
		kv3.Pair{Key: "m_materialGroups", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("default")},
			kv3.Pair{Key: "m_materials", Value: kv3.Array(kv3.String("body.vmat"))},
		))},
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Binary([]byte{1, 2, 3})},
	)
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		resource.BuildResult{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
	)
}

func unitModel(t *testing.T, material string) []byte {
	t.Helper()
	groups := []string{"body"}
	groups = append(groups, headGroups[:]...)
	data := kv3.Object(
		kv3.Pair{Key: "m_materialGroups", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("default")},
			kv3.Pair{Key: "m_materials", Value: kv3.Array(kv3.String("body.vmat"))},
		))},
		kv3.Pair{Key: "m_meshGroups", Value: stringArray(groups)},
		kv3.Pair{Key: "m_refMeshGroupMasks", Value: kv3.Array(
			kv3.UInt(1), kv3.UInt(2), kv3.UInt(4), kv3.UInt(8),
		)},
	)
	control := kv3.Object(kv3.Pair{Key: "embedded_meshes", Value: kv3.Array(
		kv3.Object(
			kv3.Pair{Key: "m_nMeshIndex", Value: kv3.Int(0)},
			kv3.Pair{Key: "m_nDataBlock", Value: kv3.Int(3)},
		),
		kv3.Object(
			kv3.Pair{Key: "m_nMeshIndex", Value: kv3.Int(1)},
			kv3.Pair{Key: "m_nDataBlock", Value: kv3.Int(4)},
		),
		kv3.Object(
			kv3.Pair{Key: "m_nMeshIndex", Value: kv3.Int(2)},
			kv3.Pair{Key: "m_nDataBlock", Value: kv3.Int(5)},
		),
		kv3.Object(
			kv3.Pair{Key: "m_nMeshIndex", Value: kv3.Int(3)},
			kv3.Pair{Key: "m_nDataBlock", Value: kv3.Int(6)},
		),
	)})
	mesh := kv3.Object(kv3.Pair{Key: "m_sceneObjects", Value: kv3.Array(
		kv3.Object(kv3.Pair{Key: "m_drawCalls", Value: kv3.Array(
			kv3.Object(kv3.Pair{Key: "m_material", Value: kv3.String(material)}),
		)}),
	)})
	meshBytes := kv3.Encode(&mesh, kv3.Format{})
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&data, kv3.Format{})},
		resource.BuildResult{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		resource.BuildResult{Kind: [4]byte{'C', 'T', 'R', 'L'}, Payload: kv3.Encode(&control, kv3.Format{})},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: meshBytes},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: meshBytes},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: meshBytes},
		resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: meshBytes},
	)
}

func stringArray(items []string) kv3.Value {
	values := make([]kv3.Value, len(items))
	for i, item := range items {
		values[i] = kv3.String(item)
	}
	return kv3.Array(values...)
}

func unitsTable(t *testing.T) []byte {
	t.Helper()
	unit := func(prefix, amber, sapphire string) kv3.Value {
		return kv3.Object(
			kv3.Pair{Key: "m_sDefaultMaterialGroupName", Value: kv3.String(prefix + "_friendly")},
			kv3.Pair{Key: "m_sEnemyMaterialGroupName", Value: kv3.String(prefix + "_enemy")},
			kv3.Pair{Key: "m_sTeam1MaterialGroupName", Value: kv3.String(prefix + "_amber")},
			kv3.Pair{Key: "m_sTeam2MaterialGroupName", Value: kv3.String(prefix + "_sapphire")},
			kv3.Pair{Key: "m_sAmberModelName", Value: kv3.String(amber)},
			kv3.Pair{Key: "m_sSapphireModelName", Value: kv3.String(sapphire)},
		)
	}
	value := kv3.Object(
		kv3.Pair{Key: "trooper_normal", Value: unit("normal",
			"models/npc_units/troopers/amber_normal.vmdl", "models/npc_units/troopers/sapphire_normal.vmdl")},
		kv3.Pair{Key: "trooper_medic", Value: unit("medic",
			"models/npc_units/troopers/amber_medic.vmdl", "models/npc_units/troopers/sapphire_medic.vmdl")},
		kv3.Pair{Key: "trooper_melee", Value: unit("melee",
			"models/npc_units/troopers/amber_melee.vmdl", "models/npc_units/troopers/sapphire_melee.vmdl")},
	)
	return testResource(t,
		resource.BuildResult{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
	)
}

func glowSnapshot(t *testing.T) fakeSnapshot {
	t.Helper()
	game := map[string][]byte{
		oldModel:             templateModel(t),
		normalize(unitsPath): unitsTable(t),
		normalize(whiteMask): []byte("white mask"),
	}
	mod := make(map[string][]byte)
	for _, target := range glowTargets {
		game[normalize(target.material)] = glowMaterial(t, target.material, false)
	}
	// Only the four legacy roles exist in the mod. Reuse an authored fixture
	// whose tint/parameter layout passes the recipe preconditions.
	authored := glowMaterial(t, "authored.vmat", true)
	for _, role := range sourceRoles {
		mod[normalize(sourcePrefix+role.role+".vmat")] = authored
	}
	for _, name := range []string{
		"models/npc_units/troopers/amber_normal.vmdl",
		"models/npc_units/troopers/amber_medic.vmdl",
		"models/npc_units/troopers/amber_melee.vmdl",
		"models/npc_units/troopers/sapphire_normal.vmdl",
		"models/npc_units/troopers/sapphire_medic.vmdl",
		"models/npc_units/troopers/sapphire_melee.vmdl",
	} {
		game[normalize(name)] = unitModel(t, targetMaterialFor(name))
	}
	return fakeSnapshot{game: game, mod: mod}
}

func targetMaterialFor(model string) string {
	switch {
	case strings.Contains(model, "amber_"):
		return "models/npc_units/troopers/amber_trooper_01/materials/amber_trooper_01_wax.vmat"
	case strings.Contains(model, "sapphire_melee"):
		return "models/npc_units/troopers/sapphire_trooper_01_melee/materials/sapphire_trooper_melee_head.vmat"
	default:
		return "models/npc_units/troopers/sapphire_trooper_01/materials/sapphire_trooper_01.vmat"
	}
}

func TestBuildMinionGlowPublishesPrivateMaterialsAndCurrentModels(t *testing.T) {
	snapshot := glowSnapshot(t)
	paths := make([]string, 0, len(sourceRoles))
	for _, role := range sourceRoles {
		paths = append(paths, normalize(sourcePrefix+role.role+".vmat"))
	}
	assets, err := BuildMinionGlow(snapshot, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 19 {
		t.Fatalf("assets = %d", len(assets))
	}
	models := 0
	for path := range assets {
		if strings.HasSuffix(path, ".vmdl_c") {
			models++
		}
		if strings.HasSuffix(path, ".vmat_c") && !strings.HasPrefix(path, "dmm/compatibility/materials/") {
			t.Fatalf("generated material outside the private namespace: %s", path)
		}
	}
	if models != 6 {
		t.Fatalf("models = %d", models)
	}
}

func TestBuildMinionGlowRefusesPartialRolesAndMissingDependencies(t *testing.T) {
	snapshot := glowSnapshot(t)
	paths := []string{normalize(sourcePrefix + "friendly.vmat")}
	if _, err := BuildMinionGlow(snapshot, paths); err == nil ||
		!strings.Contains(err.Error(), "four material roles") {
		t.Fatalf("partial roles error = %v", err)
	}

	all := make([]string, 0, len(sourceRoles))
	for _, role := range sourceRoles {
		all = append(all, normalize(sourcePrefix+role.role+".vmat"))
	}
	delete(snapshot.game, normalize(oldModel))
	if _, err := BuildMinionGlow(snapshot, all); err == nil ||
		!strings.Contains(err.Error(), "Missing current game dependency") {
		t.Fatalf("missing dependency error = %v", err)
	}
}

func TestBaselineAllowsOnlyTextureProvenanceOverrides(t *testing.T) {
	current := testTexture(t, []byte("current compiler"), []byte{255, 255, 255, 255})
	legacy := testTexture(t, []byte("older compiler"), []byte{255, 255, 255, 255})
	snapshot := fakeSnapshot{
		game: map[string][]byte{normalize("materials/a.vtex"): current},
		mod:  map[string][]byte{normalize("materials/a.vtex"): legacy},
	}
	if _, err := baseline(snapshot, "materials/a.vtex"); err != nil {
		t.Fatalf("provenance-only override rejected: %v", err)
	}
	changed := testTexture(t, []byte("current compiler"), []byte{0, 0, 0, 0})
	snapshot.mod[normalize("materials/a.vtex")] = changed
	if _, err := baseline(snapshot, "materials/a.vtex"); err == nil {
		t.Fatal("rendering-data override accepted")
	}

	snapshot = fakeSnapshot{
		game: map[string][]byte{normalize("models/a.vmdl"): templateModel(t)},
		mod:  map[string][]byte{normalize("models/a.vmdl"): unitModel(t, "body.vmat")},
	}
	if _, err := baseline(snapshot, "models/a.vmdl"); err == nil ||
		!strings.Contains(err.Error(), "Enabled mod overrides") {
		t.Fatalf("model override error = %v", err)
	}
}
