// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

package vdata

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func decodeTest(t *testing.T, value kv3.Value) (kv3.Value, kv3.Encoding) {
	t.Helper()
	encoded := kv3.Encode(&value, kv3.Format{})
	decoded, encoding, err := kv3.DecodePreserving(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded, encoding
}

func rowValue(t *testing.T, rows []kv3.Pair, name string) *kv3.Value {
	t.Helper()
	value, ok := compiledObjectGet(rows, name)
	if !ok {
		t.Fatalf("row %s is missing", name)
	}
	return value
}

func TestEnumMapNormalizeDropsRetiredKeysAndKeepsAuthoredEdits(t *testing.T) {
	hero := func(entries ...kv3.Pair) kv3.Value {
		return kv3.Object(
			kv3.Pair{Key: "_class", Value: kv3.String(heroDataClass)},
			kv3.Pair{Key: enumMapField, Value: kv3.Object(entries...)},
			kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/custom.vmdl")},
		)
	}
	currentRows := []kv3.Pair{
		{Key: "hero_one", Value: hero(kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(10)})},
		{Key: "hero_two", Value: hero(kv3.Pair{Key: "MODIFIER_VALUE_SPIRIT", Value: kv3.Double(2)})},
	}
	compatibility := LearnEnumMaps(currentRows)
	authored := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String(heroDataClass)},
		kv3.Pair{Key: enumMapField, Value: kv3.Object(
			kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(20)},
			kv3.Pair{Key: "MODIFIER_VALUE_SPIRIT", Value: kv3.Double(3)},
			kv3.Pair{Key: "MODIFIER_VALUE_RETIRED", Value: kv3.Double(0)},
			kv3.Pair{Key: "MODIFIER_VALUE_RETIRED_NONZERO", Value: kv3.Double(0.625)},
		)},
		kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/custom.vmdl")},
	)
	authored, encoding := decodeTest(t, authored)
	removed, err := compatibility.Normalize(&authored, &encoding)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0] != "MODIFIER_VALUE_RETIRED" || removed[1] != "MODIFIER_VALUE_RETIRED_NONZERO" {
		t.Fatalf("removed = %v", removed)
	}
	field, _ := compiledObjectGet(authored.Object, enumMapField)
	if field.Kind != kv3.KindObject || len(field.Object) != 2 {
		t.Fatalf("map = %+v", field)
	}
	if health, _ := compiledObjectGet(field.Object, "MODIFIER_VALUE_HEALTH"); health.Double != 20 {
		t.Fatalf("health = %v", health)
	}
	spirit, _ := compiledObjectGet(field.Object, "MODIFIER_VALUE_SPIRIT")
	if spirit.Double != 3 {
		t.Fatalf("spirit = %v", spirit)
	}
	// The field encoding is pruned in the same order.
	fieldEncoding, _ := compiledEncodingGet(encoding.Children.Object, enumMapField)
	if fieldEncoding.Children.Kind != kv3.ChildrenObject || len(fieldEncoding.Children.Object) != 2 {
		t.Fatalf("map encoding = %+v", fieldEncoding)
	}
}

func TestEnumCompatibilityMigratesOnlyProvenAliases(t *testing.T) {
	currentRows := []kv3.Pair{
		{Key: "row", Value: kv3.Object(
			kv3.Pair{Key: "m_eState", Value: kv3.String("Ready")},
		)},
	}
	sourceRows := []kv3.Pair{
		{Key: "row", Value: kv3.Object(
			kv3.Pair{Key: "m_eState", Value: kv3.String("EState_Ready")},
		)},
	}
	currentValue, currentEncoding := decodeTest(t, kv3.Object(currentRows...))
	currentEncodings := currentEncoding.Children.Object
	source, sourceEncoding := decodeTest(t, kv3.Object(sourceRows...))
	compatibility := LearnEnumCompatibility(currentValue.Object, currentEncodings, source.Object)
	normalized, normalizedEncoding := cloneValue(&source), cloneEncoding(&sourceEncoding)
	migrated, err := compatibility.Normalize(&normalized, &normalizedEncoding)
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 1 {
		t.Fatalf("migrated %d values", migrated)
	}
	state, _ := compiledObjectGet(rowValue(t, normalized.Object, "row").Object, "m_eState")
	if state.String != "Ready" {
		t.Fatalf("state = %q", state.String)
	}
	// A value without the demonstrated alias shape is left alone.
	arbitrary := kv3.Object(
		kv3.Pair{Key: "row", Value: kv3.Object(
			kv3.Pair{Key: "m_eState", Value: kv3.String("Arbitrary_Ready")},
		)},
	)
	arbitrary, arbitraryEncoding := decodeTest(t, arbitrary)
	migrated, err = compatibility.Normalize(&arbitrary, &arbitraryEncoding)
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 0 {
		t.Fatalf("arbitrary prefix migrated %d values", migrated)
	}
}

func heroRow(state string, present bool) kv3.Value {
	pairs := []kv3.Pair{
		{Key: "_class", Value: kv3.String(heroDataClass)},
		{Key: "m_strModelName", Value: kv3.String("models/template.vmdl")},
		{Key: "m_bDisabled", Value: kv3.Bool(false)},
		{Key: "m_bInDevelopment", Value: kv3.Bool(false)},
	}
	if present {
		pairs = append(pairs, kv3.Pair{Key: "m_eHeroDevelopmentState", Value: kv3.String(state)})
	}
	return kv3.Object(pairs...)
}

func restoreDevelopmentState(t *testing.T, current []kv3.Pair, source kv3.Value) kv3.Value {
	t.Helper()
	currentValue, currentEncoding := decodeTest(t, kv3.Object(current...))
	restored, restoredEncoding := decodeTest(t, source)
	rebaser := NewHeroRebaser(currentValue.Object, currentEncoding.Children.Object, nil, nil)
	if err := rebaser.RestoreDevelopmentState(&restored, &restoredEncoding); err != nil {
		t.Fatal(err)
	}
	return restored
}

func TestVisibilityMigrationRequiresAgreementAndPreservesAuthorChoices(t *testing.T) {
	donor := heroRow("EHeroDevState_Release", true)
	source := heroRow("", false)
	current := []kv3.Pair{{Key: "hero_template", Value: donor}}
	explicit := heroRow("EHeroDevState_DebugOnly", true)
	if got := restoreDevelopmentState(t, current, explicit); !compiledRuntimeEqual(&got, &explicit) {
		t.Fatalf("explicit state changed: %+v", got)
	}
	for _, test := range []struct {
		field string
		value kv3.Value
	}{
		{"m_bDisabled", kv3.Bool(true)},
		{"m_bInDevelopment", kv3.Bool(true)},
		{"_class", kv3.String("OtherHeroData_t")},
		{"m_strModelName", kv3.String("models/another.vmdl")},
	} {
		authored := cloneValue(&source)
		if err := compiledObjectSet(&authored, test.field, test.value); err != nil {
			t.Fatal(err)
		}
		got := restoreDevelopmentState(t, current, authored)
		if !compiledRuntimeEqual(&got, &authored) {
			t.Fatalf("%s changed the authored row", test.field)
		}
	}
	ambiguous := []kv3.Pair{
		{Key: "hero_template", Value: donor},
		{Key: "hero_other", Value: heroRow("EHeroDevState_PreRelease", true)},
	}
	if got := restoreDevelopmentState(t, ambiguous, source); !compiledRuntimeEqual(&got, &source) {
		t.Fatal("disagreeing donors filled the field")
	}
	agreed := []kv3.Pair{
		{Key: "hero_template", Value: donor},
		{Key: "hero_other", Value: donor},
	}
	if got := restoreDevelopmentState(t, agreed, source); !compiledRuntimeEqual(&got, &donor) {
		t.Fatalf("agreed donors did not fill the field: %+v", got)
	}
}

type fixedBaseline map[string]kv3.Value

func (b fixedBaseline) RowMatches(rowName string, value kv3.Value) bool {
	known, ok := b[rowName]
	return ok && compiledRuntimeEqual(&known, &value)
}

func TestInheritedDefinitionRebasesAuthoredDeltasOntoCurrentParent(t *testing.T) {
	currentParent := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CAbilityData_t")},
		kv3.Pair{Key: "m_flCooldown", Value: kv3.Int(12)},
		kv3.Pair{Key: "m_flDamage", Value: kv3.Int(5)},
	)
	oldParent := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CAbilityData_t")},
		kv3.Pair{Key: "m_flCooldown", Value: kv3.Int(10)},
		kv3.Pair{Key: "m_flDamage", Value: kv3.Int(5)},
	)
	child := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CAbilityData_t")},
		kv3.Pair{Key: "_base", Value: kv3.String("ability_base")},
		kv3.Pair{Key: "m_flDamage", Value: kv3.Int(7)},
	)
	current, currentEncoding := decodeTest(t, kv3.Object(kv3.Pair{Key: "ability_base", Value: currentParent}))
	source, sourceEncoding := decodeTest(t, kv3.Object(
		kv3.Pair{Key: "ability_base", Value: oldParent},
		kv3.Pair{Key: "ability_custom", Value: child},
	))
	childValue, _ := compiledObjectGet(source.Object, "ability_custom")
	childEncoding, _ := compiledEncodingGet(sourceEncoding.Children.Object, "ability_custom")
	result, err := RebaseInherited(childValue, childEncoding,
		current.Object, currentEncoding.Children.Object,
		source.Object, sourceEncoding.Children.Object,
		fixedBaseline{"ability_base": oldParent},
		&EnumCompatibility{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != InheritedRebased {
		t.Fatalf("kind = %v", result.Kind)
	}
	cooldown, _ := compiledObjectGet(result.Value.Object, "m_flCooldown")
	if cooldown.Int != 12 {
		t.Fatalf("current parent field was not inherited: %v", cooldown)
	}
	damage, _ := compiledObjectGet(result.Value.Object, "m_flDamage")
	if damage.Int != 7 {
		t.Fatalf("authored delta was not replayed: %v", damage)
	}
}

func compiledRuntimeEqual(left, right *kv3.Value) bool {
	return kv3.Equal(left, right)
}

func compiledObjectSet(object *kv3.Value, key string, value kv3.Value) error {
	for index := range object.Object {
		if object.Object[index].Key == key {
			object.Object[index].Value = value
			return nil
		}
	}
	object.Object = append(object.Object, kv3.Pair{Key: key, Value: value})
	return nil
}
