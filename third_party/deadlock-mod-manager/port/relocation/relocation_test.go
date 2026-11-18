// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_relocation/tests.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa, adapted to the archived
// bindings because the learned VData history is not ported yet. See ../../NOTICE.md.

package relocation

import (
	"slices"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func registry(t *testing.T, rows ...string) kv3.Value {
	t.Helper()
	pairs := make([]kv3.Pair, 0, len(rows)/2)
	for i := 0; i < len(rows); i += 2 {
		pairs = append(pairs, kv3.Pair{
			Key: rows[i],
			Value: kv3.Object(
				kv3.Pair{Key: "m_strModelName", Value: kv3.String(rows[i+1])},
			),
		})
	}
	return kv3.Object(pairs...)
}

func TestArchivedBindingMatchesNormalizedPaths(t *testing.T) {
	const old = "models/heroes_staging/hornet_v3/hornet.vmdl"
	if !ArchivedBinding("hero_hornet", old) {
		t.Fatal("expected archived binding")
	}
	if !ArchivedBinding("hero_hornet", old+"_c") {
		t.Fatal("expected the compiled path to match")
	}
	if !ArchivedBinding("hero_hornet", `MODELS\Heroes_Staging\HORNET_V3\hornet.vmdl`) {
		t.Fatal("expected separators and case to be normalized")
	}
	if ArchivedBinding("hero_hornet", "models/heroes/hornet/hornet.vmdl") {
		t.Fatal("unexpected binding for the current model")
	}
	if ArchivedBinding("hero_unknown", old) {
		t.Fatal("unexpected binding for an unknown row")
	}
}

func TestObservedBindingPrefersObservedHistory(t *testing.T) {
	const old = "models/retired/body.vmdl"
	observed := func(row, model string) bool {
		return row == "hero_copper" && model == old
	}
	if !ObservedBinding(observed, "hero_copper", old) {
		t.Fatal("expected observed binding")
	}
	if ObservedBinding(observed, "hero_other", old) {
		t.Fatal("unexpected binding for another row")
	}
}

func TestRegistryTargetsReturnsNormalizedCurrentModels(t *testing.T) {
	value := registry(t,
		"hero_hornet", "models/heroes/hornet/hornet.vmdl",
		"hero_other", "models/heroes/other/other.vmdl",
	)
	targets := RegistryTargets(&value, "models/heroes_staging/hornet_v3/hornet.vmdl", nil)
	want := []string{"models/heroes/hornet/hornet.vmdl_c"}
	if !slices.Equal(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
}

func TestRegistryTargetsDeduplicatesRowsAndReportsAmbiguity(t *testing.T) {
	const old = "models/retired/body.vmdl_c"
	observed := func(string, string) bool { return true }
	value := registry(t,
		"hero_copper", "models/rebuilt/character.vmdl",
		"hero_other", "models/unrelated/second.vmdl",
	)
	targets := RegistryTargets(&value, old, observed)
	want := []string{
		"models/rebuilt/character.vmdl_c",
		"models/unrelated/second.vmdl_c",
	}
	if !slices.Equal(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	if _, ok := RelocationTarget(&value, old, observed); ok {
		t.Fatal("expected ambiguous bindings to decline relocation")
	}
	single := registry(t, "hero_copper", "models/rebuilt/character.vmdl")
	if target, ok := RelocationTarget(&single, old, observed); !ok ||
		target != "models/rebuilt/character.vmdl_c" {
		t.Fatalf("unexpected target: %q %v", target, ok)
	}
}

func TestRegistryTargetsIgnoresNonModelTargets(t *testing.T) {
	const old = "models/retired/body.vmdl_c"
	observed := func(string, string) bool { return true }
	value := registry(t, "hero_copper", "scripts/not_a_model.vdata")
	if targets := RegistryTargets(&value, old, observed); len(targets) != 0 {
		t.Fatalf("unexpected targets: %v", targets)
	}
}

func TestRegistryHeroModels(t *testing.T) {
	value := registry(t,
		"hero_copper", "models/rebuilt/character.vmdl",
		"hero_other", "models/unrelated/second.vmdl",
	)
	models := RegistryHeroModels(&value)
	for _, want := range []string{
		"models/rebuilt/character.vmdl_c",
		"models/unrelated/second.vmdl_c",
	} {
		if _, ok := models[want]; !ok {
			t.Fatalf("missing hero model %s", want)
		}
	}
}
