package modcompat

import (
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const dataHeroesPath = "scripts/heroes.vdata_c"

func heroTable(row kv3.Value) kv3.Value {
	return kv3.Object(kv3.Pair{Key: "hero_one", Value: row})
}

func heroEntry(model string, state string, field kv3.Pair) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CitadelHeroData_t")},
		kv3.Pair{Key: "m_strModelName", Value: kv3.String(model)},
		kv3.Pair{Key: "m_eState", Value: kv3.String(state)},
		field,
	)
}

func TestAnalyzeMigratesEnumMapEntriesAndReportsOmittedValues(t *testing.T) {
	gameRow := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CitadelHeroData_t")},
		kv3.Pair{Key: "m_mapStandardLevelUpUpgrades", Value: kv3.Object(
			kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(10)},
		)},
	)
	authored := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CitadelHeroData_t")},
		kv3.Pair{Key: "m_mapStandardLevelUpUpgrades", Value: kv3.Object(
			kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(20)},
			kv3.Pair{Key: "MODIFIER_VALUE_RETIRED", Value: kv3.Double(0)},
			kv3.Pair{Key: "MODIFIER_VALUE_RETIRED_NONZERO", Value: kv3.Double(0.625)},
		)},
		kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/custom.vmdl")},
	)
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(heroTable(gameRow))),
	}})
	mod := packMod(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(heroTable(authored))),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.DataRepairs) != 2 {
		t.Fatalf("data repairs = %+v", analysis.DataRepairs)
	}
	for _, repair := range analysis.DataRepairs {
		if repair.Kind != vdata.RepairEnumMapEntry {
			t.Fatalf("data repairs = %+v", analysis.DataRepairs)
		}
	}
	if len(analysis.DataWarnings) != 1 || analysis.DataWarnings[0].Kind != vdata.WarningOmittedEnumMapEntry {
		t.Fatalf("data warnings = %+v", analysis.DataWarnings)
	}
	fixed, ok := analysis.Fixed[dataHeroesPath]
	if !ok {
		t.Fatal("merged hero table was not written")
	}
	_, root, _, err := camera.DecodeCompiled(fixed)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := root.Get("hero_one")
	mapping, _ := row.Get("m_mapStandardLevelUpUpgrades")
	if len(mapping.Object) != 1 {
		t.Fatalf("map = %+v", mapping)
	}
	if health, _ := mapping.Get("MODIFIER_VALUE_HEALTH"); health.Double != 20 {
		t.Fatalf("health = %v", health)
	}
	if model, _ := row.Get("m_strModelName"); model.String != "models/custom.vmdl" {
		t.Fatalf("model = %v", model)
	}
}

func TestAnalyzeMigratesProvenEnumAliases(t *testing.T) {
	base := heroTable(heroEntry("models/template.vmdl", "Ready",
		kv3.Pair{Key: "m_flValue", Value: kv3.Int(1)}))
	authored := heroTable(heroEntry("models/template.vmdl", "EState_Ready",
		kv3.Pair{Key: "m_flValue", Value: kv3.Int(2)}))
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(base)),
	}})
	mod := packMod(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(authored)),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	fixed, ok := analysis.Fixed[dataHeroesPath]
	if !ok {
		t.Fatal("merged hero table was not written")
	}
	_, root, _, err := camera.DecodeCompiled(fixed)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := root.Get("hero_one")
	if state, _ := row.Get("m_eState"); state.String != "Ready" {
		t.Fatalf("state = %q", state.String)
	}
	if value, _ := row.Get("m_flValue"); value.Int != 2 {
		t.Fatalf("value = %v", value)
	}
}

func TestAnalyzeReportsMissingInheritedDefinition(t *testing.T) {
	base := kv3.Object(kv3.Pair{Key: "current_ability", Value: kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("TestAbility")},
	)})
	authored := kv3.Object(
		kv3.Pair{Key: "current_ability", Value: kv3.Object(
			kv3.Pair{Key: "_class", Value: kv3.String("TestAbility")},
		)},
		kv3.Pair{Key: "custom_ability", Value: kv3.Object(
			kv3.Pair{Key: "_class", Value: kv3.String("TestAbility")},
			kv3.Pair{Key: "_base", Value: kv3.String("missing_base")},
		)},
	)
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: vdata.AbilitiesPath, Data: compiledBlock(t, ptr(base)),
	}})
	mod := packMod(t, []vpk.PackEntry{{
		Path: vdata.AbilitiesPath, Data: compiledBlock(t, ptr(authored)),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.DataWarnings) != 1 || analysis.DataWarnings[0].Kind != vdata.WarningMissingDefinition {
		t.Fatalf("data warnings = %+v", analysis.DataWarnings)
	}
	if len(analysis.DataRepairs) != 0 {
		t.Fatalf("data repairs = %+v", analysis.DataRepairs)
	}
	// The authored row is kept and warned about; only dependency rows that
	// cannot be proven are withheld.
	fixed, ok := analysis.Fixed[vdata.AbilitiesPath]
	if !ok {
		t.Fatal("authored table was not staged")
	}
	_, root, _, err := camera.DecodeCompiled(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := root.Get("custom_ability"); !ok {
		t.Fatal("authored row was dropped")
	}
}

func TestAnalyzeSkipsModOnlyDataTables(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{{
		Path: "scripts/custom.vdata_c", Data: compiledBlock(t, ptr(kv3.Object())),
	}})
	analysis, err := Analyze("mod", []string{mod}, &source2.GameIndex{}, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.DataRepairs) != 0 || len(analysis.DataWarnings) != 0 {
		t.Fatalf("mod-only table produced findings: %+v", analysis)
	}
}

func ptr(value kv3.Value) *kv3.Value { return &value }

func TestAnalyzeCountsUnchangedDataTablesAsCovered(t *testing.T) {
	table := heroTable(heroEntry("models/template.vmdl", "Ready",
		kv3.Pair{Key: "m_flValue", Value: kv3.Int(1)}))
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(table)),
	}})
	mod := packMod(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(table)),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 1 || len(analysis.DataRepairs) != 0 || len(analysis.DataWarnings) != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
}

func localizationText(tokens ...string) []byte {
	out := "\"lang\"\r\n{\r\n\t\"Language\"\t\t\"english\"\r\n\t\"Tokens\"\r\n\t{\r\n"
	for index := 0; index+1 < len(tokens); index += 2 {
		out += "\t\t\"" + tokens[index] + "\"\t\t\"" + tokens[index+1] + "\"\r\n"
	}
	out += "\t}\r\n}\r\n"
	return []byte(out)
}

func TestAnalyzeReportsRestoredGameText(t *testing.T) {
	const path = "resource/localization/example/example_english.txt"
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: path, Data: localizationText("Token_A", "Vanilla", "Token_B", "Keep"),
	}})
	mod := packMod(t, []vpk.PackEntry{{
		Path: path, Data: localizationText("Token_A", "Authored"),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.DataRepairs) != 1 || analysis.DataRepairs[0].Kind != vdata.RepairRestoredGameText {
		t.Fatalf("data repairs = %+v", analysis.DataRepairs)
	}
	if analysis.AssetsChecked != 1 {
		t.Fatalf("assets checked = %d", analysis.AssetsChecked)
	}
	if _, ok := analysis.Fixed[path]; ok {
		t.Fatal("per-mod analysis wrote merged text")
	}
}
