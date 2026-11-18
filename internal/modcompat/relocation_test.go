package modcompat

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const (
	retiredHornet = "models/heroes_staging/hornet_v3/hornet.vmdl_c"
	currentHornet = "models/heroes/hornet/hornet.vmdl_c"
)

func heroPose() kv3.Value {
	return kv3.Array(
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
	)
}

func heroModel(t *testing.T, bindings bool) kv3.Value {
	t.Helper()
	model := kv3.Object(
		kv3.Pair{Key: "authoredAppearance", Value: kv3.String("keep my custom appearance")},
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.Array(kv3.String("root"), kv3.String("head"))},
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
	)
	if bindings {
		pairs, _ := model.AsObject()
		pairs = append(pairs,
			kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: kv3.Array(kv3.String("models/rig.vnmskel"))},
			kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array(kv3.Object(
				kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("default")},
				kv3.Pair{Key: "m_hGraph", Value: kv3.String("animgraphs/current.vnmgraph")},
			))},
		)
		return kv3.Object(pairs...)
	}
	return model
}

func heroSkeleton(t *testing.T) []byte {
	t.Helper()
	skeleton := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(kv3.String("head"))},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(kv3.Int(-1))},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(heroPose())},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(heroPose())},
	)
	return compiledBlock(t, &skeleton)
}

func heroBytes(t *testing.T, value kv3.Value) []byte {
	t.Helper()
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
		{Kind: resource.BlockTypeRERL, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
	}, nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func heroRegistryBytes(t *testing.T, row, model string) []byte {
	t.Helper()
	value := kv3.Object(kv3.Pair{Key: row, Value: kv3.Object(
		kv3.Pair{Key: "m_strModelName", Value: kv3.String(strings.TrimSuffix(model, "_c"))},
	)})
	return compiledBlock(t, &value)
}

func relocationGame(t *testing.T) *source2.GameIndex {
	t.Helper()
	return gameIndexWith(t, []vpk.PackEntry{
		{Path: "scripts/heroes.vdata_c", Data: heroRegistryBytes(t, "hero_hornet", currentHornet)},
		{Path: retiredHornet, Data: heroBytes(t, heroModel(t, true))},
		{Path: currentHornet, Data: heroBytes(t, heroModel(t, true))},
		{Path: "models/rig.vnmskel_c", Data: heroSkeleton(t)},
		{Path: "animgraphs/current.vnmgraph_c", Data: []byte("available graph")},
	})
}

func TestAnalyzeRelocatesRetiredHeroModels(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{{Path: retiredHornet, Data: source}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, repair := range analysis.Repairs {
		if repair.Kind == RepairModelRelocation && repair.FilePath == currentHornet {
			found = true
		}
	}
	if !found {
		t.Fatalf("relocation missing: repairs=%+v warnings=%+v", analysis.Repairs, analysis.AssetWarnings)
	}
	fixed, ok := analysis.Fixed[currentHornet]
	if !ok {
		t.Fatalf("relocated bytes missing: %+v", analysis.Fixed)
	}
	if !bytes.Equal(fixed, source) {
		t.Fatal("relocation changed the authored model bytes")
	}
}

func TestAnalyzeRestoresAnimationBindingsAndRelocates(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, false))
	mod := packMod(t, []vpk.PackEntry{{Path: retiredHornet, Data: source}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	fixed, ok := analysis.Fixed[currentHornet]
	if !ok {
		t.Fatalf("relocation missing: warnings=%+v", analysis.AssetWarnings)
	}
	_, actual, _, err := camera.DecodeCompiled(fixed)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"m_vecNmSkeletonRefs", "m_animGraph2Refs"} {
		if _, ok := actual.Get(field); !ok {
			t.Fatalf("%s missing from the relocated model", field)
		}
	}
	found := false
	for _, repair := range analysis.Repairs {
		if repair.Kind == RepairAnimationInterface && repair.FilePath == currentHornet {
			found = true
		}
	}
	if !found {
		t.Fatalf("animation repair missing: %+v", analysis.Repairs)
	}
}

func TestAnalyzeDeclinesRelocationWhenTheSampledRigDoesNotMatch(t *testing.T) {
	game := relocationGame(t)
	current := heroModel(t, true)
	setRelocationHeadPosition(t, &current, [3]float64{5, 0, 0})
	game = gameIndexWith(t, []vpk.PackEntry{
		{Path: "scripts/heroes.vdata_c", Data: heroRegistryBytes(t, "hero_hornet", currentHornet)},
		{Path: retiredHornet, Data: heroBytes(t, heroModel(t, true))},
		{Path: currentHornet, Data: heroBytes(t, current)},
		{Path: "models/rig.vnmskel_c", Data: heroSkeleton(t)},
		{Path: "animgraphs/current.vnmgraph_c", Data: []byte("available graph")},
	})
	source := heroBytes(t, heroModel(t, false))
	mod := packMod(t, []vpk.PackEntry{{Path: retiredHornet, Data: source}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; ok {
		t.Fatal("model with a mismatched sampled rig was relocated")
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningModelMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func setRelocationHeadPosition(t *testing.T, model *kv3.Value, position [3]float64) {
	t.Helper()
	skeleton, ok := model.Get("m_modelSkeleton")
	if !ok {
		t.Fatal("missing skeleton")
	}
	positionsValue, ok := skeleton.Get("m_bonePosParent")
	if !ok {
		t.Fatal("missing bone positions")
	}
	positions, ok := positionsValue.AsArray()
	if !ok || len(positions) < 2 {
		t.Fatal("invalid bone positions")
	}
	positions[1] = kv3.Array(
		kv3.Double(position[0]), kv3.Double(position[1]), kv3.Double(position[2]),
	)
}

func TestAnalyzeDeclinesRelocationWhenTheModShipsAnimations(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{
		{Path: retiredHornet, Data: source},
		{Path: "animations/custom.vnmclip_c", Data: []byte("clip")},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; ok {
		t.Fatal("model with animation overrides was relocated")
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningModelMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeDeclinesRelocationOntoAnotherModsModel(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{{Path: retiredHornet, Data: source}})
	scope := Scope{OtherModelPaths: map[string]struct{}{currentHornet: {}}}
	analysis, err := Analyze("mod", []string{mod}, game, scope, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; ok {
		t.Fatal("model was relocated onto another mod's model")
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningModelMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeDoesNotRelocateWhenTheModShipsTheCurrentModel(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{
		{Path: retiredHornet, Data: source},
		{Path: currentHornet, Data: heroBytes(t, heroModel(t, true))},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; ok {
		t.Fatal("the old model was relocated over the mod's own current model")
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningModelMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeDeclinesRelocationWithAModifiedHeroRegistry(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{
		{Path: retiredHornet, Data: source},
		{Path: "scripts/heroes.vdata_c", Data: heroRegistryBytes(t, "hero_hornet", "models/other/other.vmdl")},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; ok {
		t.Fatal("model was relocated through a modified registry")
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningModelMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeRelocatesWithAnUnchangedHeroRegistryOverride(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{
		{Path: retiredHornet, Data: source},
		{Path: "scripts/heroes.vdata_c", Data: heroRegistryBytes(t, "hero_hornet", currentHornet)},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; !ok {
		t.Fatalf("relocation missing: warnings=%+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeDeclinesRelocationWhenTheScopeIsUnknown(t *testing.T) {
	game := relocationGame(t)
	source := heroBytes(t, heroModel(t, true))
	mod := packMod(t, []vpk.PackEntry{{Path: retiredHornet, Data: source}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{Unknown: true}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := analysis.Fixed[currentHornet]; ok {
		t.Fatal("model was relocated with an unknown load order")
	}
}

func hasWarning(warnings []AssetWarning, kind compat.WarningKind) bool {
	for _, warning := range warnings {
		if warning.Kind == kind {
			return true
		}
	}
	return false
}
