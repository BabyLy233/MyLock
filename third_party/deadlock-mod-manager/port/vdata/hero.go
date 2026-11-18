// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/hero_rebase.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

package vdata

import (
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// Baseline answers whether an authored row is unchanged from the recorded
// build. history.BaselineMatch implements it.
type Baseline interface {
	RowMatches(rowName string, value kv3.Value) bool
}

// HeroRebaser replays a copied hero's authored deltas onto current donor data.
// The donor must be the unique closest unchanged historical row with the same
// model and class.
type HeroRebaser struct {
	CurrentRows      []kv3.Pair
	CurrentEncodings []kv3.EncodingField
	SourceRows       []kv3.Pair
	Baseline         Baseline
}

// NewHeroRebaser returns a rebaser for one authored table.
func NewHeroRebaser(currentRows []kv3.Pair, currentEncodings []kv3.EncodingField,
	sourceRows []kv3.Pair, baseline Baseline) *HeroRebaser {
	return &HeroRebaser{
		CurrentRows:      currentRows,
		CurrentEncodings: currentEncodings,
		SourceRows:       sourceRows,
		Baseline:         baseline,
	}
}

// Rebase returns the rebased hero and encoding. The third result is false when
// no unique donor exists.
func (h *HeroRebaser) Rebase(hero *kv3.Value, encoding *kv3.Encoding) (kv3.Value, kv3.Encoding, bool, error) {
	if h.Baseline == nil {
		return kv3.Value{}, kv3.Encoding{}, false, nil
	}
	class, ok := stringField(hero, "_class")
	if !ok || class != heroDataClass {
		return kv3.Value{}, kv3.Encoding{}, false, nil
	}
	model, ok := stringField(hero, "m_strModelName")
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false, nil
	}
	var bestName string
	var bestPatches []compiled.Patch
	bestScore := int(^uint(0) >> 1)
	tied := false
	for _, row := range h.SourceRows {
		if donorClass, ok := stringField(&row.Value, "_class"); !ok || donorClass != class {
			continue
		}
		if donorModel, ok := stringField(&row.Value, "m_strModelName"); !ok || donorModel != model {
			continue
		}
		if !h.Baseline.RowMatches(row.Key, row.Value) {
			continue
		}
		if _, ok := compiledObjectGet(h.CurrentRows, row.Key); !ok {
			continue
		}
		var patches []compiled.Patch
		if err := compiled.CollectPatches(&row.Value, hero, encoding, nil, &patches); err != nil {
			return kv3.Value{}, kv3.Encoding{}, false, err
		}
		switch {
		case len(patches) < bestScore:
			bestScore = len(patches)
			bestName = row.Key
			bestPatches = patches
			tied = false
		case len(patches) == bestScore:
			tied = true
		}
	}
	if bestName == "" {
		return kv3.Value{}, kv3.Encoding{}, false, nil
	}
	// Model reuse is common in development heroes. Equal distances remain
	// ambiguous.
	if tied {
		return kv3.Value{}, kv3.Encoding{}, false, nil
	}
	currentDonor, ok := compiledObjectGet(h.CurrentRows, bestName)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false, nil
	}
	currentEncoding, ok := compiledEncodingGet(h.CurrentEncodings, bestName)
	if !ok {
		return kv3.Value{}, kv3.Encoding{}, false, errCompiled("Missing current hero encoding for " + bestName)
	}
	rebased := cloneValue(currentDonor)
	rebasedEncoding := cloneEncoding(currentEncoding)
	for _, patch := range bestPatches {
		promoted, err := compiled.PromotePatch(currentDonor, currentEncoding, hero, encoding, patch)
		if err != nil {
			return kv3.Value{}, kv3.Encoding{}, false, err
		}
		if len(promoted.Path) == 0 {
			rebased = promoted.Value
			rebasedEncoding = promoted.Encoding
			continue
		}
		if err := compiled.SetValueAtPath(&rebased, promoted.Path, promoted.Value); err != nil {
			return kv3.Value{}, kv3.Encoding{}, false, err
		}
		if err := compiled.SetEncodingAtPath(&rebasedEncoding, promoted.Path, promoted.Encoding); err != nil {
			return kv3.Value{}, kv3.Encoding{}, false, err
		}
	}
	return rebased, rebasedEncoding, true, nil
}

// RestoreDevelopmentState fills the visibility field a released hero row needs
// when every current donor agrees on it. Author choices and ambiguity are left
// untouched.
func (h *HeroRebaser) RestoreDevelopmentState(hero *kv3.Value, encoding *kv3.Encoding) error {
	const field = "m_eHeroDevelopmentState"
	if hero.Kind != kv3.KindObject {
		return nil
	}
	if _, exists := compiledObjectGet(hero.Object, field); exists {
		return nil
	}
	if class, ok := stringField(hero, "_class"); !ok || class != heroDataClass {
		return nil
	}
	if disabled, ok := boolField(hero, "m_bDisabled"); !ok || disabled {
		return nil
	}
	if inDevelopment, ok := boolField(hero, "m_bInDevelopment"); !ok || inDevelopment {
		return nil
	}
	model, ok := stringField(hero, "m_strModelName")
	if !ok || model == "" {
		return nil
	}
	var stateValue string
	var stateEncoding *kv3.Encoding
	hasState := false
	for _, row := range h.CurrentRows {
		if row.Value.Kind != kv3.KindObject {
			continue
		}
		donorClass, ok := stringField(&row.Value, "_class")
		if !ok || donorClass != heroDataClass {
			continue
		}
		disabled, ok := boolField(&row.Value, "m_bDisabled")
		if !ok || disabled {
			continue
		}
		inDevelopment, ok := boolField(&row.Value, "m_bInDevelopment")
		if !ok || inDevelopment {
			continue
		}
		donorModel, ok := stringField(&row.Value, "m_strModelName")
		if !ok || normalizePath(donorModel) != normalizePath(model) {
			continue
		}
		value, ok := stringField(&row.Value, field)
		if !ok {
			continue
		}
		rowEncoding, ok := compiledEncodingGet(h.CurrentEncodings, row.Key)
		if !ok {
			return nil
		}
		wireEncoding, ok := compiledEncodingGet(rowEncoding.Children.Object, field)
		if !ok {
			return nil
		}
		// Shared models are safe only when every matching donor agrees on the
		// field and type.
		if hasState && (stateValue != value || !encodingEqual(stateEncoding, wireEncoding)) {
			return nil
		}
		stateValue = value
		stateEncoding = wireEncoding
		hasState = true
	}
	if !hasState {
		return nil
	}
	if encoding.Children.Kind != kv3.ChildrenObject {
		return errCompiled("Custom hero has no object encoding metadata")
	}
	if err := compiled.ObjectSet(hero, field, kv3.String(stateValue)); err != nil {
		return err
	}
	return compiled.EncodingSet(encoding, field, *stateEncoding)
}
