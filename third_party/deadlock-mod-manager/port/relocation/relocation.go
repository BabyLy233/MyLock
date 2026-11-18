// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_relocation.rs
// and assets/hero-model-bindings.json at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.
//
// The upstream module consults a learned VData history before the archived
// bindings. That history has not been ported yet, so this port only uses the
// archived bindings; models whose old binding is not archived are left alone.

// Package relocation maps a mod's retired hero model path to the hero model the
// current registry uses.
package relocation

import (
	_ "embed"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// HeroesVDataPath is the game's hero registry.
const HeroesVDataPath = "scripts/heroes.vdata_c"

//go:embed hero-model-bindings.json
var archiveData []byte

type archivedBindings struct {
	Bindings map[string][]string `json:"bindings"`
}

var archive = sync.OnceValue(func() map[string][]string {
	var parsed archivedBindings
	if err := json.Unmarshal(archiveData, &parsed); err != nil {
		return nil
	}
	return parsed.Bindings
})

// ResourcePath normalizes a resource reference the way the engine resolves it:
// lowercased with a _c suffix (except .txt files) and without fragments.
func ResourcePath(name string) (string, bool) {
	normalized, ok := vpk.NormalizeResourcePath(name)
	if !ok {
		return "", false
	}
	path := strings.ToLower(normalized)
	if strings.Contains(path, "#") {
		return "", false
	}
	if !strings.HasSuffix(path, "_c") && !strings.HasSuffix(path, ".txt") {
		path += "_c"
	}
	return path, true
}

// ArchivedBinding reports whether the archived game history recorded row's
// model as model.
func ArchivedBinding(row, model string) bool {
	target, ok := ResourcePath(model)
	if !ok {
		return false
	}
	for _, name := range archive()[row] {
		path, ok := ResourcePath(name)
		if ok && path == target {
			return true
		}
	}
	return false
}

// ObservedBinding consults the caller's observed history first, then the
// archived bindings. observed may be nil.
func ObservedBinding(observed func(row, model string) bool, row, model string) bool {
	return (observed != nil && observed(row, model)) || ArchivedBinding(row, model)
}

// RegistryTargets returns the current model paths of every registry row that
// historically bound oldModel. The result is sorted and deduplicated.
func RegistryTargets(registry *kv3.Value, oldModel string, observed func(row, model string) bool) []string {
	oldPath, ok := ResourcePath(oldModel)
	if !ok || !strings.HasSuffix(oldPath, "_c") {
		return nil
	}
	oldName := strings.TrimSuffix(oldPath, "_c")
	rows, ok := registry.AsObject()
	if !ok {
		return nil
	}
	targets := make(map[string]struct{})
	for _, row := range rows {
		if !ObservedBinding(observed, row.Key, oldName) {
			continue
		}
		name, ok := row.Value.Get("m_strModelName")
		if !ok {
			continue
		}
		model, ok := name.AsString()
		if !ok {
			continue
		}
		path, ok := ResourcePath(model)
		if !ok || !camera.IsModelPath(path) {
			continue
		}
		targets[path] = struct{}{}
	}
	return slices.Sorted(maps.Keys(targets))
}

// RegistryHeroModels returns every current hero model path in the registry.
func RegistryHeroModels(registry *kv3.Value) map[string]struct{} {
	models := make(map[string]struct{})
	rows, ok := registry.AsObject()
	if !ok {
		return models
	}
	for _, row := range rows {
		name, ok := row.Value.Get("m_strModelName")
		if !ok {
			continue
		}
		model, ok := name.AsString()
		if !ok {
			continue
		}
		if path, ok := ResourcePath(model); ok {
			models[path] = struct{}{}
		}
	}
	return models
}

// RelocationTarget returns the single current model oldModel maps to. Ambiguous
// or unknown bindings return false.
func RelocationTarget(registry *kv3.Value, oldModel string, observed func(row, model string) bool) (string, bool) {
	targets := RegistryTargets(registry, oldModel, observed)
	if len(targets) != 1 {
		return "", false
	}
	return targets[0], true
}
