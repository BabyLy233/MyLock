// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_sounds/tests.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package modelsounds

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

const (
	testHeader     = "<!-- kv3 encoding:text:version{e21c7f3c-8a33-41c5-9977-a76d3a32aa0d} format:vmdlkeys4:version{9f5c1002-f19f-470c-bd42-c3c26c063d33} -->"
	gameTable      = "scripts/tagged_sounds/test_anim_sounds.vdata"
	customTable    = "scripts/tagged_sounds/custom_steps.vdata"
	testModelPath  = "models/heroes/test/test.vmdl_c"
	mdatOpaqueData = "\x04\x05\x06"
)

func binding(name, table string) string {
	return name + " = \n\t{\n\t\tm_taggedSounds = \n\t\t[\n\t\t\tresource:\"" + table + "\",\n\t\t]\n\t}"
}

func text(fields string) string {
	return testHeader + "\n{\n\t// keep this comment\n\tCustom = { value = 7 }\n\t" + fields + "\n}\n"
}

func testModel(t *testing.T, body string, references []resource.Reference) []byte {
	t.Helper()
	value := kv3.Object(
		kv3.Pair{Key: "m_modelInfo", Value: kv3.Object(
			kv3.Pair{Key: "m_keyValueText", Value: kv3.String(body)},
		)},
		kv3.Pair{Key: "authored", Value: kv3.String("mesh and rig")},
	)
	data := kv3.Encode(&value, kv3.Format{})
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: data},
		{Kind: [4]byte{'R', 'E', 'R', 'L'}, Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0}},
		{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: []byte(mdatOpaqueData)},
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

func testTable(t *testing.T, kind string) []byte {
	t.Helper()
	value := kv3.Object(kv3.Pair{Key: "generic_data_type", Value: kv3.String(kind)})
	raw, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func tableID(t *testing.T, path string) uint64 {
	t.Helper()
	id, err := resource.ResourceID(path)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func resolver(tables map[string][]byte) Resolve {
	return func(path string) ([]byte, error) {
		if data, ok := tables[path]; ok {
			return data, nil
		}
		if data, ok := tables[path+"_c"]; ok {
			return data, nil
		}
		return nil, nil
	}
}

func current(t *testing.T) []byte {
	t.Helper()
	return testModel(t, text(binding(Settings, gameTable)),
		[]resource.Reference{{ID: tableID(t, gameTable), Path: gameTable}})
}

func textOf(t *testing.T, data []byte) string {
	t.Helper()
	_, value, _, err := camera.DecodeCompiled(data)
	if err != nil {
		t.Fatal(err)
	}
	body, ok := modelText(&value)
	if !ok {
		t.Fatal("model has no authored text")
	}
	return body
}

func referencesOf(t *testing.T, data []byte) []resource.Reference {
	t.Helper()
	refs, err := resource.ReferencesOf(data)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func assertOnlyTextAndReferencesChanged(t *testing.T, source, repaired []byte) {
	t.Helper()
	before, err := resource.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := resource.Parse(repaired)
	if err != nil {
		t.Fatal(err)
	}
	for index, block := range before.Blocks() {
		if block.Kind != [4]byte{'M', 'D', 'A', 'T'} {
			continue
		}
		beforeBytes, _ := before.GetBlockByIndex(index)
		afterBytes, _ := after.GetBlockByIndex(index)
		if !bytes.Equal(beforeBytes, afterBytes) {
			t.Fatalf("MDAT block %d changed", index)
		}
	}
	_, oldValue, _, err := camera.DecodeCompiled(source)
	if err != nil {
		t.Fatal(err)
	}
	_, newValue, _, err := camera.DecodeCompiled(repaired)
	if err != nil {
		t.Fatal(err)
	}
	oldAuthored, _ := oldValue.Get("authored")
	newAuthored, _ := newValue.Get("authored")
	if oldAuthored == nil || newAuthored == nil || !kv3.Equal(oldAuthored, newAuthored) {
		t.Fatal("authored asset fields changed")
	}
}

func TestLegacyBindingIsRenamedAndKeepsTheModsOwnSoundTables(t *testing.T) {
	custom := testTable(t, "CTaggedSound_t")
	resolve := resolver(map[string][]byte{customTable: custom})
	authored := text(binding(LegacySettings, customTable))
	source := testModel(t, authored, nil)

	_, before, _, err := camera.DecodeCompiled(source)
	if err != nil {
		t.Fatal(err)
	}
	_, game, _, err := camera.DecodeCompiled(current(t))
	if err != nil {
		t.Fatal(err)
	}
	if !Missing(&before, &game) {
		t.Fatal("missing binding not detected")
	}

	repaired, ok, err := Repair(source, current(t), resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("legacy binding not repaired")
	}
	want := strings.Replace(authored, LegacySettings, Settings, 1)
	if got := textOf(t, repaired); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	refs := referencesOf(t, repaired)
	wantRefs := []resource.Reference{{ID: tableID(t, customTable), Path: customTable}}
	if len(refs) != 1 || refs[0] != wantRefs[0] {
		t.Fatalf("references = %#v", refs)
	}
	assertOnlyTextAndReferencesChanged(t, source, repaired)

	_, after, _, err := camera.DecodeCompiled(repaired)
	if err != nil {
		t.Fatal(err)
	}
	if Missing(&after, &game) {
		t.Fatal("repaired model still reports a missing binding")
	}
	if _, again, err := Repair(repaired, current(t), resolve); err != nil || again {
		t.Fatalf("second repair = %v, %v", again, err)
	}
}

func TestAbsentBindingIsCopiedFromTheCurrentModel(t *testing.T) {
	resolve := resolver(map[string][]byte{gameTable: testTable(t, "CTaggedSound_t")})
	authored := text("Other = { flag = true }")
	source := testModel(t, authored, nil)
	repaired, ok, err := Repair(source, current(t), resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("absent binding not copied")
	}
	updated := textOf(t, repaired)
	closing := strings.LastIndex(authored, "}")
	if closing < 0 || updated[:closing] != authored[:closing] {
		t.Fatalf("text before the closing brace changed: %q", updated)
	}
	if !strings.Contains(updated, binding(Settings, gameTable)) {
		t.Fatalf("copied binding missing: %q", updated)
	}
	want := resource.Reference{ID: tableID(t, gameTable), Path: gameTable}
	found := false
	for _, reference := range referencesOf(t, repaired) {
		if reference == want {
			found = true
		}
	}
	if !found {
		t.Fatal("RERL does not reference the copied game table")
	}
	assertOnlyTextAndReferencesChanged(t, source, repaired)
}

func TestAuthoredUnknownOrUnverifiableBindingsAreLeftAlone(t *testing.T) {
	wrong := testTable(t, "CSomethingElse_t")
	resolve := resolver(map[string][]byte{
		gameTable:                                testTable(t, "CTaggedSound_t"),
		"scripts/tagged_sounds/wrong_type.vdata": wrong,
	})
	for _, fields := range []string{
		Settings + " = { }",
		Settings + " = { m_taggedSounds = [ ] }",
		LegacySettings + " = { m_taggedSounds = [ resource:\"" + gameTable + "\" ] unknown = 1 }",
		LegacySettings + " = { m_taggedSounds = [ \"" + gameTable + "\" ] }",
		binding(LegacySettings, "scripts/tagged_sounds/missing.vdata"),
		binding(LegacySettings, "scripts/tagged_sounds/wrong_type.vdata"),
		binding(LegacySettings, "sounds/elsewhere.vdata"),
	} {
		source := testModel(t, text(fields), nil)
		if _, ok, err := Repair(source, current(t), resolve); err != nil || ok {
			t.Fatalf("%s: repaired=%v err=%v", fields, ok, err)
		}
	}
	// A conflicting authored reference to the same table is never overwritten.
	source := testModel(t, text(binding(LegacySettings, gameTable)),
		[]resource.Reference{{ID: tableID(t, gameTable) ^ 1, Path: gameTable}})
	if _, ok, err := Repair(source, current(t), resolve); err != nil || ok {
		t.Fatalf("conflicting reference repaired=%v err=%v", ok, err)
	}
}

func TestLegacySoundListsPreserveEmptySlotsWithoutLosingValidTables(t *testing.T) {
	resolve := resolver(map[string][]byte{gameTable: testTable(t, "CTaggedSound_t")})
	authored := text(LegacySettings + " = { m_taggedSounds = [ resource:\"" + gameTable +
		"\", resource:\"\" ] }")
	source := testModel(t, authored, nil)
	repaired, ok, err := Repair(source, current(t), resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("an empty sound slot must not prevent migration of the valid authored table")
	}
	if got, want := textOf(t, repaired), strings.Replace(authored, LegacySettings, Settings, 1); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	refs := referencesOf(t, repaired)
	want := resource.Reference{ID: tableID(t, gameTable), Path: gameTable}
	if len(refs) != 1 || refs[0] != want {
		t.Fatalf("references = %#v", refs)
	}
	assertOnlyTextAndReferencesChanged(t, source, repaired)
	if _, again, err := Repair(repaired, current(t), resolve); err != nil || again {
		t.Fatalf("second repair = %v, %v", again, err)
	}
	empty := testModel(t, text(binding(LegacySettings, "")), nil)
	if _, ok, err := Repair(empty, current(t), resolve); err != nil || ok {
		t.Fatalf("empty binding repaired=%v err=%v", ok, err)
	}
}

func TestMissingRequiresACurrentBinding(t *testing.T) {
	_, model, _, err := camera.DecodeCompiled(testModel(t, text(""), nil))
	if err != nil {
		t.Fatal(err)
	}
	_, plain, _, err := camera.DecodeCompiled(testModel(t, text("Other = 1"), nil))
	if err != nil {
		t.Fatal(err)
	}
	if Missing(&model, &plain) {
		t.Fatal("missing reported when the current model has no binding")
	}
}
