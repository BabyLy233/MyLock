// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/vdata_compatibility/
// tests.rs at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

package vdata

import (
	"fmt"
	"slices"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

func compiledResource(t *testing.T, value kv3.Value) []byte {
	t.Helper()
	bytes, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

func definition(extra ...kv3.Pair) kv3.Value {
	pairs := []kv3.Pair{{Key: "_class", Value: kv3.String("TestAbility")}}
	return kv3.Object(append(pairs, extra...)...)
}

func boundHero(target string) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String(heroDataClass)},
		kv3.Pair{Key: "m_mapBoundAbilities", Value: kv3.Object(
			kv3.Pair{Key: "ESlot_Weapon_Primary", Value: kv3.String(target)},
		)},
	)
}

func baseTable(t *testing.T, filePath string, base kv3.Value) *Table {
	t.Helper()
	table, err := BuildTable(filePath, compiledResource(t, base), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func addRetired(t *testing.T, table *Table, rowName string, value kv3.Value) {
	t.Helper()
	decoded, encoding := decodeTest(t, value)
	key := asciiLower(rowName)
	table.RetiredRows[key] = append(table.RetiredRows[key], CandidateValue{
		Candidate: Candidate{ModID: "ability-mod", SourceVPK: "ability-mod.vpk"},
		Value:     decoded,
		Encoding:  encoding,
	})
}

func addHeroGroup(t *testing.T, table *Table, rowName string, value kv3.Value) {
	t.Helper()
	decoded, encoding := decodeTest(t, value)
	key := conflictKey(HeroesVDataPath, rowName, nil)
	table.Groups[key] = &Group{
		Key: key, FilePath: HeroesVDataPath, RowName: rowName,
		Candidates: []CandidateValue{{
			Candidate: Candidate{ModID: "hero-mod", SourceVPK: "hero-mod.vpk"},
			Value:     decoded, Encoding: encoding,
		}},
	}
}

func abilitiesTable(t *testing.T, retired map[string]kv3.Value) *Table {
	t.Helper()
	pairs := []kv3.Pair{{Key: "current_ability", Value: definition()}}
	for index := range 40 {
		pairs = append(pairs, kv3.Pair{Key: fmt.Sprintf("unchanged_%d", index), Value: kv3.Int(int64(index))})
	}
	table := baseTable(t, AbilitiesPath, kv3.Object(pairs...))
	for name, value := range retired {
		addRetired(t, table, name, value)
	}
	return table
}

func heroesTable(t *testing.T, target string) *Table {
	t.Helper()
	table := baseTable(t, HeroesVDataPath, kv3.Object())
	addHeroGroup(t, table, "hero_custom", boundHero(target))
	return table
}

func TestRetainsCompleteDependencyChainsAndPreservesSourceTypes(t *testing.T) {
	child := definition(kv3.Pair{Key: "damage", Value: kv3.Double(17.25)})
	parent := definition(kv3.Pair{Key: "m_AbilityToTrigger", Value: kv3.String("retired_child")})
	tables := []*Table{
		heroesTable(t, "retired_parent"),
		abilitiesTable(t, map[string]kv3.Value{
			"retired_parent": parent,
			"retired_child":  child,
			"unused":         definition(),
		}),
	}
	prepared, err := Prepare(tables, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Warnings) != 0 {
		t.Fatalf("warnings = %+v", prepared.Warnings)
	}
	if len(prepared.Repairs) != 2 {
		t.Fatalf("repairs = %+v", prepared.Repairs)
	}
	for _, test := range []struct {
		row   string
		value kv3.Value
	}{{"retired_parent", parent}, {"retired_child", child}} {
		mergedRow, ok := rowOf(&prepared.Merged[1].Root, test.row)
		if !ok || !kv3.Equal(mergedRow, &test.value) {
			t.Fatalf("%s = %+v", test.row, mergedRow)
		}
	}
	if _, ok := rowOf(&prepared.Merged[1].Root, "unused"); ok {
		t.Fatal("unused retired row was retained")
	}
	expected := &tables[1].RetiredRows["retired_child"][0].Encoding
	actual, ok := compiledEncodingGet(prepared.Merged[1].Encoding.Children.Object, "retired_child")
	if !ok || !encodingEqual(actual, expected) {
		t.Fatal("retained row lost its source encoding")
	}
}

func TestIncompleteChainsDoNotProducePartialRepairs(t *testing.T) {
	parent := definition(kv3.Pair{Key: "m_AbilityToTrigger", Value: kv3.String("missing_child")})
	tables := []*Table{
		heroesTable(t, "retired_parent"),
		abilitiesTable(t, map[string]kv3.Value{"retired_parent": parent}),
	}
	prepared, err := Prepare(tables, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Repairs) != 0 {
		t.Fatalf("repairs = %+v", prepared.Repairs)
	}
	if _, ok := rowOf(&prepared.Merged[0].Root, "hero_custom"); ok {
		t.Fatal("a hero with an unavailable required ability reached the game")
	}
	if _, ok := rowOf(&prepared.Merged[1].Root, "retired_parent"); ok {
		t.Fatal("an incomplete chain was partially repaired")
	}
	if len(prepared.Warnings) != 1 {
		t.Fatalf("warnings = %+v", prepared.Warnings)
	}
	warning := prepared.Warnings[0]
	if warning.Kind != WarningOmittedHeroDefinition || warning.TargetRow != "retired_parent" ||
		warning.Detail != "scripts/abilities.vdata_c:missing_child" ||
		warning.Reason != ReasonDefinitionUnavailable {
		t.Fatalf("warning = %+v", warning)
	}
}

func TestInheritanceCyclesAreRejectedButMutualTriggersCanResolve(t *testing.T) {
	for _, test := range []struct {
		field    string
		repaired bool
	}{{"_base", false}, {"m_AbilityToTrigger", true}} {
		tables := []*Table{
			heroesTable(t, "retired_a"),
			abilitiesTable(t, map[string]kv3.Value{
				"retired_a": definition(kv3.Pair{Key: test.field, Value: kv3.String("retired_b")}),
				"retired_b": definition(kv3.Pair{Key: test.field, Value: kv3.String("retired_a")}),
			}),
		}
		prepared, err := Prepare(tables, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, present := rowOf(&prepared.Merged[1].Root, "retired_a")
		if present != test.repaired {
			t.Fatalf("%s: retired_a present = %t", test.field, present)
		}
		if test.repaired && len(prepared.Repairs) != 2 {
			t.Fatalf("%s: repairs = %+v", test.field, prepared.Repairs)
		}
		if !test.repaired && len(prepared.Warnings) != 1 {
			t.Fatalf("%s: warnings = %+v", test.field, prepared.Warnings)
		}
	}
}

func TestUnsupportedClassesEnumsAndCompetingDefinitionsAreLeftOut(t *testing.T) {
	for _, value := range []kv3.Value{
		kv3.Object(kv3.Pair{Key: "_class", Value: kv3.String("RetiredNativeClass")}),
		definition(kv3.Pair{Key: "m_eMode", Value: kv3.String("UnknownEnum")}),
	} {
		tables := []*Table{
			heroesTable(t, "retired"),
			abilitiesTable(t, map[string]kv3.Value{"retired": value}),
		}
		prepared, err := Prepare(tables, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rowOf(&prepared.Merged[1].Root, "retired"); ok {
			t.Fatal("unsupported definition was retained")
		}
		if len(prepared.Repairs) != 0 {
			t.Fatalf("repairs = %+v", prepared.Repairs)
		}
		if len(prepared.Warnings) == 0 || prepared.Warnings[0].Kind != WarningUnverifiedDefinition {
			t.Fatalf("warnings = %+v", prepared.Warnings)
		}
	}
	target := abilitiesTable(t, map[string]kv3.Value{"retired": definition()})
	target.RetiredRows["retired"] = append(target.RetiredRows["retired"], CandidateValue{
		Candidate: Candidate{ModID: "other-mod", SourceVPK: "other.vpk", Priority: 1},
		Value:     definition(kv3.Pair{Key: "damage", Value: kv3.Int(999)}),
		Encoding:  target.RetiredRows["retired"][0].Encoding,
	})
	prepared, err := Prepare([]*Table{heroesTable(t, "retired"), target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rowOf(&prepared.Merged[1].Root, "retired"); ok {
		t.Fatal("competing definition was retained")
	}
	if len(prepared.Repairs) != 0 {
		t.Fatalf("repairs = %+v", prepared.Repairs)
	}
	if len(prepared.Warnings) == 0 || prepared.Warnings[0].Kind != WarningUnverifiedDefinition {
		t.Fatalf("warnings = %+v", prepared.Warnings)
	}
}

type testBaseline struct {
	rows   map[string]kv3.Value
	fields map[string]kv3.Value
}

func (b testBaseline) RowMatches(rowName string, value kv3.Value) bool {
	known, ok := b.rows[asciiLower(rowName)]
	return ok && kv3.Equal(&known, &value)
}

func (b testBaseline) ContainsRow(rowName string) bool {
	_, ok := b.rows[asciiLower(rowName)]
	return ok
}

func (b testBaseline) FieldMatches(rowName string, path []kv3.Seg, value kv3.Value) bool {
	known, ok := b.fields[asciiLower(rowName)+"|"+compiledPathLabel(path)]
	return ok && kv3.Equal(&known, &value)
}

func (b testBaseline) Version() uint32      { return 1 }
func (b testBaseline) ComparableCount() int { return 40 }
func (b testBaseline) ChangedCount() int    { return 0 }

func compiledPathLabel(path []kv3.Seg) string {
	return fmt.Sprint(path)
}

func TestBuildTableMigratesEnumsAndRebasesCopiedHero(t *testing.T) {
	baseRow := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String(heroDataClass)},
		kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/template.vmdl")},
		kv3.Pair{Key: "m_bDisabled", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_bInDevelopment", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_eState", Value: kv3.String("Ready")},
		kv3.Pair{Key: "m_eHeroDevelopmentState", Value: kv3.String("EHeroDevState_Release")},
		kv3.Pair{Key: "m_flNewField", Value: kv3.Int(5)},
	)
	base := kv3.Object(kv3.Pair{Key: "hero_template", Value: baseRow})
	sourceTemplate := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String(heroDataClass)},
		kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/template.vmdl")},
		kv3.Pair{Key: "m_bDisabled", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_bInDevelopment", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_eState", Value: kv3.String("EState_Ready")},
	)
	copied := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String(heroDataClass)},
		kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/template.vmdl")},
		kv3.Pair{Key: "m_bDisabled", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_bInDevelopment", Value: kv3.Bool(false)},
		kv3.Pair{Key: "m_eState", Value: kv3.String("EState_Ready")},
		kv3.Pair{Key: "m_flCustom", Value: kv3.Int(7)},
	)
	sourceValue := kv3.Object(
		kv3.Pair{Key: "hero_template", Value: sourceTemplate},
		kv3.Pair{Key: "hero_custom", Value: copied},
	)
	source, _ := decodeTest(t, sourceValue)
	baseline := testBaseline{
		rows: map[string]kv3.Value{"hero_template": sourceTemplate},
		fields: map[string]kv3.Value{
			"hero_template|" + compiledPathLabel(nil): sourceTemplate,
		},
	}
	history := func(string, kv3.Value) (BaselineMatch, BaselineEvidence) {
		return baseline, BaselineEvidence{Status: "matched", ComparableRows: 40}
	}
	table, err := BuildTable(HeroesVDataPath, compiledResource(t, base), []Source{{
		ModID: "hero-mod", SourceVPK: "hero-mod.vpk", Priority: 0,
		Bytes: compiledResource(t, source),
	}}, history)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare([]*Table{table}, nil)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := rowOf(&prepared.Merged[0].Root, "hero_custom")
	if !ok {
		t.Fatal("rebased hero is missing")
	}
	if field, ok := compiledObjectGet(row.Object, "m_flNewField"); !ok || field.Int != 5 {
		t.Fatalf("current donor field was not inherited: %+v", field)
	}
	if field, ok := compiledObjectGet(row.Object, "m_flCustom"); !ok || field.Int != 7 {
		t.Fatalf("authored delta was not replayed: %+v", field)
	}
	if state, ok := stringField(row, "m_eHeroDevelopmentState"); !ok || state != "EHeroDevState_Release" {
		t.Fatalf("development state = %q", state)
	}
	found := slices.ContainsFunc(prepared.Repairs, func(repair DataRepair) bool {
		return repair.Kind == RepairHeroDevelopmentState && repair.RowName == "hero_custom"
	})
	if !found {
		t.Fatalf("repairs = %+v", prepared.Repairs)
	}
}

func TestMergeRebuildsNestedValuesAndPreservesWireTypes(t *testing.T) {
	baseRow := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("TestAbility")},
		kv3.Pair{Key: "m_flCooldown", Value: kv3.Double(1.5)},
		kv3.Pair{Key: "m_nLevels", Value: kv3.Int(2)},
		kv3.Pair{Key: "m_vecPoint", Value: kv3.Array(kv3.Double(0.5), kv3.Double(1.25))},
		kv3.Pair{Key: "m_sName", Value: kv3.String("base")},
	)
	authoredRow := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("TestAbility")},
		kv3.Pair{Key: "m_flCooldown", Value: kv3.Double(2.75)},
		kv3.Pair{Key: "m_nLevels", Value: kv3.Int(3)},
		kv3.Pair{Key: "m_vecPoint", Value: kv3.Array(kv3.Double(1), kv3.Double(2), kv3.Double(3))},
		kv3.Pair{Key: "m_sName", Value: kv3.String("authored")},
		kv3.Pair{Key: "m_bEnabled", Value: kv3.Bool(true)},
	)
	base := kv3.Object(
		kv3.Pair{Key: "ability_one", Value: baseRow},
		kv3.Pair{Key: "ability_untouched", Value: kv3.Int(7)},
	)
	sourceTable := kv3.Object(kv3.Pair{Key: "ability_one", Value: authoredRow})
	table, err := BuildTable(AbilitiesPath, compiledResource(t, base), []Source{{
		ModID: "mod", SourceVPK: "mod.vpk", Bytes: compiledResource(t, sourceTable),
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare([]*Table{table}, nil)
	if err != nil {
		t.Fatal(err)
	}
	output, applied, err := table.EncodeMerged(prepared.Merged[0])
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("applied rows = %d", applied)
	}
	_, root, encoding, err := compiled.Decode(output, "rebuilt")
	if err != nil {
		t.Fatal(err)
	}
	row, ok := rowOf(&root, "ability_one")
	if !ok || !kv3.Equal(row, &authoredRow) {
		t.Fatalf("row = %+v", row)
	}
	untouched, _ := rowOf(&root, "ability_untouched")
	if untouched.Int != 7 {
		t.Fatalf("untouched row = %+v", untouched)
	}
	_, expectedEncoding := decodeTest(t, sourceTable)
	rowEncoding, _ := compiledEncodingGet(encoding.Children.Object, "ability_one")
	expectedRowEncoding, _ := compiledEncodingGet(expectedEncoding.Children.Object, "ability_one")
	if !encodingEqual(rowEncoding, expectedRowEncoding) {
		t.Fatal("authored wire types were not preserved")
	}
}
