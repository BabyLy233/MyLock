package modcompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func packMod(t *testing.T, entries []vpk.PackEntry) string {
	t.Helper()
	data, err := vpk.Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mod_dir.vpk")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeReportsSupportedChecksAndEmptyFindings(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "materials/hero.vmat_c", Data: []byte("material")},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Checks) != len(SupportedChecks) || analysis.ModelsChecked != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Warnings) != 0 || analysis.Fixed != nil {
		t.Fatalf("unexpected findings: %+v", analysis)
	}
}

func TestAnalyzeRejectsAnUnreadableMod(t *testing.T) {
	if _, err := Analyze("mod", []string{filepath.Join(t.TempDir(), "missing.vpk")}, nil, Scope{}, Detect); err == nil {
		t.Fatal("missing VPK accepted")
	}
}

func TestAnalyzeFixModeReturnsAMap(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "scripts/hero.txt", Data: []byte("data")},
	})
	analysis, err := Analyze("mod", []string{mod}, &source2.GameIndex{}, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Fixed == nil {
		t.Fatal("fix mode did not initialise Fixed")
	}
}

func TestModelPathsNormalizeAndDeduplicate(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: []byte("a")},
		{Path: "materials/hero.vmat_c", Data: []byte("c")},
	})
	paths, err := ModelPaths([]string{mod})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "models/hero/hero.vmdl_c" {
		t.Fatalf("paths = %v", paths)
	}
	if _, err := ModelPaths([]string{filepath.Join(t.TempDir(), "missing.vpk")}); err == nil {
		t.Fatal("missing VPK accepted")
	}
}

func compiledBlock(t *testing.T, value *kv3.Value) []byte {
	t.Helper()
	data, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(value, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func compiledWithMesh(t *testing.T, value, mesh *kv3.Value) []byte {
	t.Helper()
	data, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(value, kv3.Format{})},
		{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: kv3.Encode(mesh, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnalyzeReportsStructuralModelWarnings(t *testing.T) {
	model := kv3.Object(
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.Array(kv3.String("bone"), kv3.String("bone"))},
			kv3.Pair{Key: "m_nParent", Value: kv3.Array(kv3.Int(-1), kv3.Int(0))},
			kv3.Pair{Key: "m_bonePosParent", Value: kv3.Array(
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0)),
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0)),
			)},
			kv3.Pair{Key: "m_boneRotParent", Value: kv3.Array(
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
			)},
			kv3.Pair{Key: "m_boneScaleParent", Value: kv3.Array(kv3.Double(1), kv3.Double(1))},
		)},
		kv3.Pair{Key: "m_refMeshes", Value: kv3.Array(kv3.String("models/missing.vmdl"))},
	)
	mesh := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(kv3.Pair{Key: "m_bones", Value: kv3.Array()})},
		kv3.Pair{Key: "m_attachments", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "key", Value: kv3.String("camera")},
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_nInfluences", Value: kv3.Int(1)},
				kv3.Pair{Key: "m_influenceNames", Value: kv3.Array(kv3.String(""))},
				kv3.Pair{Key: "m_bInfluenceRootTransform", Value: kv3.Array(kv3.Bool(false))},
				kv3.Pair{Key: "m_influenceWeights", Value: kv3.Array(kv3.Double(1))},
			)},
		))},
	)
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: compiledWithMesh(t, &model, &mesh)},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 1 {
		t.Fatalf("assets checked = %d", analysis.AssetsChecked)
	}
	kinds := make(map[compat.WarningKind]bool)
	for _, warning := range analysis.AssetWarnings {
		kinds[warning.Kind] = true
	}
	for _, want := range []compat.WarningKind{
		compat.WarningInvalidSkeleton,
		compat.WarningMissingResource,
		compat.WarningInvalidAttachment,
	} {
		if !kinds[want] {
			t.Fatalf("missing %s in %+v", want, analysis.AssetWarnings)
		}
	}
}

func TestAnalyzeReportsSkeletonAndMeshWarnings(t *testing.T) {
	skeleton := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(kv3.String("a"), kv3.String("a"))},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(kv3.Int(-1), kv3.Int(0))},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
		)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
		)},
	)
	mesh := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(kv3.Pair{Key: "m_bones", Value: kv3.Array()})},
		kv3.Pair{Key: "m_hitboxsets", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_HitBoxes", Value: kv3.Array(kv3.Object(
					kv3.Pair{Key: "m_sBoneName", Value: kv3.String("pelvis")},
				))},
			)},
		))},
	)
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vnmskel_c", Data: compiledBlock(t, &skeleton)},
		{Path: "models/hero/hero.vmesh_c", Data: compiledBlock(t, &mesh)},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 2 {
		t.Fatalf("assets checked = %d", analysis.AssetsChecked)
	}
	kinds := make(map[compat.WarningKind]bool)
	for _, warning := range analysis.AssetWarnings {
		kinds[warning.Kind] = true
	}
	if !kinds[compat.WarningInvalidSkeleton] || !kinds[compat.WarningInvalidHitbox] {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func gameIndexWith(t *testing.T, entries []vpk.PackEntry) *source2.GameIndex {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "game", "citadel")
	write := filepath.Join(dir, "pak01_dir.vpk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := vpk.Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(write, data, 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := source2.OpenGame(dir)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestAnalyzeReportsMaterialInterfaceDifferences(t *testing.T) {
	source := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("custom.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	current := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("csgo_environment.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	const path = "materials/hero/hero.vmat_c"
	game := gameIndexWith(t, []vpk.PackEntry{{Path: path, Data: compiledBlock(t, &current)}})
	mod := packMod(t, []vpk.PackEntry{{Path: path, Data: compiledBlock(t, &source)}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 1 || len(analysis.AssetWarnings) != 1 {
		t.Fatalf("analysis = %+v", analysis)
	}
	warning := analysis.AssetWarnings[0]
	if warning.Kind != compat.WarningMaterialInterface || !strings.Contains(warning.Detail, "custom.vfx") {
		t.Fatalf("warning = %+v", warning)
	}

	same := packMod(t, []vpk.PackEntry{{Path: path, Data: compiledBlock(t, &current)}})
	analysis, err = Analyze("mod", []string{same}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.AssetWarnings) != 0 {
		t.Fatalf("identical material warned: %+v", analysis.AssetWarnings)
	}
}

const soundHeader = "<!-- kv3 encoding:text:version{e21c7f3c-8a33-41c5-9977-a76d3a32aa0d} format:vmdlkeys4:version{9f5c1002-f19f-470c-bd42-c3c26c063d33} -->"

func soundBinding(name, table string) string {
	return name + " = \n\t{\n\t\tm_taggedSounds = \n\t\t[\n\t\t\tresource:\"" + table + "\",\n\t\t]\n\t}"
}

func soundText(fields string) string {
	return soundHeader + "\n{\n\tCustom = { value = 7 }\n\t" + fields + "\n}\n"
}

func soundModel(t *testing.T, body string, references []resource.Reference) []byte {
	t.Helper()
	value := kv3.Object(
		kv3.Pair{Key: "m_modelInfo", Value: kv3.Object(
			kv3.Pair{Key: "m_keyValueText", Value: kv3.String(body)},
		)},
		kv3.Pair{Key: "authored", Value: kv3.String("mesh and rig")},
	)
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: []byte{4, 5, 6}},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) > 0 {
		raw, err = resource.AddReferencesOf(raw, references)
		if err != nil {
			t.Fatal(err)
		}
	}
	return raw
}

func soundTable(t *testing.T, kind string) []byte {
	t.Helper()
	value := kv3.Object(kv3.Pair{Key: "generic_data_type", Value: kv3.String(kind)})
	return compiledBlock(t, &value)
}

func TestAnalyzeRepairsTaggedSoundBindings(t *testing.T) {
	const modelPath = "models/hero/hero.vmdl_c"
	const gameTable = "scripts/tagged_sounds/test_anim_sounds.vdata"
	const customTable = "scripts/tagged_sounds/custom_steps.vdata"
	gameID, err := resource.ResourceID(gameTable)
	if err != nil {
		t.Fatal(err)
	}
	customID, err := resource.ResourceID(customTable)
	if err != nil {
		t.Fatal(err)
	}
	game := gameIndexWith(t, []vpk.PackEntry{
		{Path: modelPath, Data: soundModel(t, soundText(soundBinding("TaggedSoundSettings_t", gameTable)),
			[]resource.Reference{{ID: gameID, Path: gameTable}})},
		{Path: gameTable + "_c", Data: soundTable(t, "CTaggedSound_t")},
	})
	mod := packMod(t, []vpk.PackEntry{
		{Path: modelPath, Data: soundModel(t,
			soundText(soundBinding("CitadelTaggedSoundSettings_t", customTable)), nil)},
		{Path: customTable + "_c", Data: soundTable(t, "CTaggedSound_t")},
	})

	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, repair := range analysis.Repairs {
		if repair.Kind == RepairTaggedSounds {
			found = true
		}
	}
	if !found || len(analysis.Fixed) != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningMissingSoundBindings {
			t.Fatalf("repaired binding still warned: %+v", analysis.AssetWarnings)
		}
	}

	analysis, err = Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	fixed, ok := analysis.Fixed[modelPath]
	if !ok {
		t.Fatalf("no fixed bytes: %+v", analysis)
	}
	references, err := resource.ReferencesOf(fixed)
	if err != nil {
		t.Fatal(err)
	}
	want := resource.Reference{ID: customID, Path: customTable}
	if len(references) != 1 || references[0] != want {
		t.Fatalf("references = %#v", references)
	}
}

func TestAnalyzeWarnsWhenTaggedSoundBindingsCannotBeRepaired(t *testing.T) {
	const modelPath = "models/hero/hero.vmdl_c"
	const gameTable = "scripts/tagged_sounds/test_anim_sounds.vdata"
	const missingTable = "scripts/tagged_sounds/missing.vdata"
	gameID, err := resource.ResourceID(gameTable)
	if err != nil {
		t.Fatal(err)
	}
	game := gameIndexWith(t, []vpk.PackEntry{
		{Path: modelPath, Data: soundModel(t, soundText(soundBinding("TaggedSoundSettings_t", gameTable)),
			[]resource.Reference{{ID: gameID, Path: gameTable}})},
		{Path: gameTable + "_c", Data: soundTable(t, "CTaggedSound_t")},
	})
	mod := packMod(t, []vpk.PackEntry{
		{Path: modelPath, Data: soundModel(t,
			soundText(soundBinding("CitadelTaggedSoundSettings_t", missingTable)), nil)},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningMissingSoundBindings {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
	for _, repair := range analysis.Repairs {
		if repair.Kind == RepairTaggedSounds {
			t.Fatal("unresolvable table reported as repairable")
		}
	}
}

func TestAnalyzeWarnsAboutUnresolvedMaterialReferences(t *testing.T) {
	const materialPath = "materials/hero/hero.vmat_c"
	const missingTexture = "materials/hero/missing.vtex"
	missingID, err := resource.ResourceID(missingTexture)
	if err != nil {
		t.Fatal(err)
	}
	source := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("csgo_environment.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&source, kv3.Format{})},
		{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = resource.AddReferencesOf(raw, []resource.Reference{{ID: missingID, Path: missingTexture}})
	if err != nil {
		t.Fatal(err)
	}
	mod := packMod(t, []vpk.PackEntry{{Path: materialPath, Data: raw}})
	analysis, err := Analyze("mod", []string{mod}, nil, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningMissingResource && warning.Detail == missingTexture {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}

	// The same reference resolves through the mod's own VPK, in load order.
	withTexture := packMod(t, []vpk.PackEntry{
		{Path: materialPath, Data: raw},
		{Path: missingTexture + "_c", Data: []byte("texture")},
	})
	analysis, err = Analyze("mod", []string{withTexture}, nil, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningMissingResource && warning.Detail == missingTexture {
			t.Fatalf("resolved reference warned: %+v", analysis.AssetWarnings)
		}
	}
}

func glowMaterialBytes(t *testing.T, name string, authored bool) []byte {
	t.Helper()
	dynamic := kv3.Array(
		kv3.Object(kv3.Pair{Key: "m_name", Value: kv3.String("g_vColorTint1")}),
		kv3.Object(kv3.Pair{Key: "m_name", Value: kv3.String("g_flSelfIllumScale1")}),
	)
	if authored {
		dynamic = kv3.Array()
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_materialName", Value: kv3.String(name)},
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("pbr.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("F_SELF_ILLUM")},
			kv3.Pair{Key: "m_nValue", Value: kv3.Int(1)},
		))},
		kv3.Pair{Key: "m_floatParams", Value: kv3.Array(
			kv3.Object(
				kv3.Pair{Key: "m_name", Value: kv3.String("g_flSelfIllumScale1")},
				kv3.Pair{Key: "m_flValue", Value: kv3.Double(0.5)},
			),
			kv3.Object(
				kv3.Pair{Key: "m_name", Value: kv3.String("g_flSelfIllumAlbedoFactor1")},
				kv3.Pair{Key: "m_flValue", Value: kv3.Double(0.4)},
			),
		)},
		kv3.Pair{Key: "m_vectorParams", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("g_vSelfIllumTint1")},
			kv3.Pair{Key: "m_value", Value: kv3.Array(
				kv3.Double(1), kv3.Double(1), kv3.Double(1), kv3.Double(1),
			)},
		))},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("g_tSelfIllumMask")},
			kv3.Pair{Key: "m_pValue", Value: kv3.String("materials/default/white.vtex")},
		))},
		kv3.Pair{Key: "m_dynamicParams", Value: dynamic},
	)
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func glowUnitModel(t *testing.T, materialName string) []byte {
	t.Helper()
	groups := []kv3.Value{kv3.String("body")}
	for _, group := range []string{
		"head_health_@0_#&high", "head_health_@1_#&mid", "head_health_@2_#&low",
	} {
		groups = append(groups, kv3.String(group))
	}
	data := kv3.Object(
		kv3.Pair{Key: "m_materialGroups", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("default")},
			kv3.Pair{Key: "m_materials", Value: kv3.Array(kv3.String("body.vmat"))},
		))},
		kv3.Pair{Key: "m_meshGroups", Value: kv3.Array(groups...)},
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
	mesh := kv3.Object(kv3.Pair{Key: "m_sceneObjects", Value: kv3.Array(kv3.Object(
		kv3.Pair{Key: "m_drawCalls", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_material", Value: kv3.String(materialName)},
		))},
	))})
	meshBytes := kv3.Encode(&mesh, kv3.Format{})
	blocks := []resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&data, kv3.Format{})},
		{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		{Kind: [4]byte{'C', 'T', 'R', 'L'}, Payload: kv3.Encode(&control, kv3.Format{})},
	}
	for range 4 {
		blocks = append(blocks, resource.BuildResult{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: meshBytes})
	}
	raw, err := resource.Build(blocks, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func glowUnitTable(t *testing.T) []byte {
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
			"models/npc_units/troopers/amber_normal.vmdl",
			"models/npc_units/troopers/sapphire_normal.vmdl")},
		kv3.Pair{Key: "trooper_medic", Value: unit("medic",
			"models/npc_units/troopers/amber_medic.vmdl",
			"models/npc_units/troopers/sapphire_medic.vmdl")},
		kv3.Pair{Key: "trooper_melee", Value: unit("melee",
			"models/npc_units/troopers/amber_melee.vmdl",
			"models/npc_units/troopers/sapphire_melee.vmdl")},
	)
	return compiledBlock(t, &value)
}

func TestAnalyzePublishesMaterialBindings(t *testing.T) {
	const (
		oldModel    = "models/npc/trooper/trooper_humanoid.vmdl_c"
		unitsPath   = "scripts/npc_units.vdata_c"
		whiteMask   = "materials/default/default_mask_tga_344101f8.vtex_c"
		amberMat    = "models/npc_units/troopers/amber_trooper_01/materials/amber_trooper_01_wax.vmat"
		sapphireMat = "models/npc_units/troopers/sapphire_trooper_01/materials/sapphire_trooper_01.vmat"
		meleeMat    = "models/npc_units/troopers/sapphire_trooper_01_melee/materials/sapphire_trooper_melee_head.vmat"
	)
	oldModelValue := kv3.Object(
		kv3.Pair{Key: "m_materialGroups", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("default")},
			kv3.Pair{Key: "m_materials", Value: kv3.Array(kv3.String("body.vmat"))},
		))},
	)
	oldModelMesh := kv3.Object(kv3.Pair{Key: "m_sceneObjects", Value: kv3.Array()})
	gameEntries := []vpk.PackEntry{
		{Path: oldModel, Data: compiledWithMesh(t, &oldModelValue, &oldModelMesh)},
		{Path: unitsPath, Data: glowUnitTable(t)},
		{Path: whiteMask, Data: []byte("white mask")},
		{Path: amberMat + "_c", Data: glowMaterialBytes(t, amberMat, false)},
		{Path: sapphireMat + "_c", Data: glowMaterialBytes(t, sapphireMat, false)},
		{Path: meleeMat + "_c", Data: glowMaterialBytes(t, meleeMat, false)},
	}
	unitModels := []struct{ path, material string }{
		{"models/npc_units/troopers/amber_normal.vmdl", amberMat},
		{"models/npc_units/troopers/amber_medic.vmdl", amberMat},
		{"models/npc_units/troopers/amber_melee.vmdl", amberMat},
		{"models/npc_units/troopers/sapphire_normal.vmdl", sapphireMat},
		{"models/npc_units/troopers/sapphire_medic.vmdl", sapphireMat},
		{"models/npc_units/troopers/sapphire_melee.vmdl", meleeMat},
	}
	for _, unit := range unitModels {
		gameEntries = append(gameEntries, vpk.PackEntry{Path: unit.path + "_c", Data: glowUnitModel(t, unit.material)})
	}
	game := gameIndexWith(t, gameEntries)

	authored := glowMaterialBytes(t, "authored.vmat", true)
	modEntries := make([]vpk.PackEntry, 0, 4)
	for _, role := range []string{"friendly", "enemy", "amber", "sapphire"} {
		modEntries = append(modEntries, vpk.PackEntry{
			Path: "models/npc/trooper/materials/candle_trooper_wax_" + role + ".vmat_c",
			Data: authored,
		})
	}
	mod := packMod(t, modEntries)

	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	repairs := 0
	for _, repair := range analysis.Repairs {
		if repair.Kind == RepairMaterialBindings {
			repairs++
		}
	}
	if repairs != 6 || len(analysis.Fixed) != 0 || len(analysis.AssetWarnings) != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}

	analysis, err = Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Fixed) != 19 {
		t.Fatalf("fixed = %d", len(analysis.Fixed))
	}
}

func TestAnalyzeReportsPartialMaterialRolesWithoutHalfARepair(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{{
		Path: "models/npc/trooper/materials/candle_trooper_wax_friendly.vmat_c",
		Data: []byte("invalid resource"),
	}})
	analysis, err := Analyze("mod", []string{mod}, nil, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range analysis.AssetWarnings {
		if warning.Kind == compat.WarningMaterialMapping &&
			strings.Contains(warning.Detail, "four material roles") {
			found = true
		}
	}
	if !found || len(analysis.Fixed) != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
	for _, repair := range analysis.Repairs {
		if repair.Kind == RepairMaterialBindings {
			t.Fatal("partial roles reported a repair")
		}
	}
}
