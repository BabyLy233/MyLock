// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/vdata_compatibility/
// inheritance.rs at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

package vdata

import (
	"maps"
	"slices"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// InheritedRebaseKind says whether an inherited definition was rebased.
type InheritedRebaseKind uint8

const (
	// InheritedNotApplicable means the value has no rebaseable parent.
	InheritedNotApplicable InheritedRebaseKind = iota
	// InheritedAmbiguous means several same-class parents disagree.
	InheritedAmbiguous
	// InheritedRebased means Value and Encoding hold the rebased definition.
	InheritedRebased
)

// InheritedRebase is the result of rebasing a copied definition.
type InheritedRebase struct {
	Kind     InheritedRebaseKind
	Value    kv3.Value
	Encoding kv3.Encoding
}

// RebaseInherited replays a copied definition's authored deltas onto the
// current version of its inherited parent.
func RebaseInherited(value *kv3.Value, encoding *kv3.Encoding,
	currentRows []kv3.Pair, currentEncodings []kv3.EncodingField,
	sourceRows []kv3.Pair, sourceEncodings []kv3.EncodingField,
	baseline Baseline, enums *EnumCompatibility) (InheritedRebase, error) {
	class, ok := stringField(value, "_class")
	if !ok {
		return InheritedRebase{Kind: InheritedNotApplicable}, nil
	}
	parents := make(map[string]struct{})
	for _, reference := range References(value, AbilitiesPath) {
		if !reference.Inheritance || len(reference.Path) > 2 {
			continue
		}
		first := reference.Path[0]
		if first.Kind != kv3.SegKey || (first.Key != "_base" && first.Key != "_multibase") {
			continue
		}
		parents[asciiLower(reference.Row)] = struct{}{}
	}
	var donorName string
	var oldRow, newRow *kv3.Value
	var oldEncoding, newEncoding *kv3.Encoding
	for _, name := range slices.Sorted(maps.Keys(parents)) {
		oldCandidate, ok := compiledObjectGet(sourceRows, name)
		if !ok {
			continue
		}
		newCandidate, ok := compiledObjectGet(currentRows, name)
		if !ok {
			continue
		}
		oldClass, ok := stringField(oldCandidate, "_class")
		if !ok || oldClass != class {
			continue
		}
		newClass, ok := stringField(newCandidate, "_class")
		if !ok || newClass != class {
			continue
		}
		if baseline == nil || !baseline.RowMatches(name, *oldCandidate) {
			continue
		}
		// Multiple same-class parents have uncertain precedence in flattened
		// data.
		if donorName != "" {
			return InheritedRebase{Kind: InheritedAmbiguous}, nil
		}
		oldWire, ok := compiledEncodingGet(sourceEncodings, name)
		if !ok {
			return InheritedRebase{Kind: InheritedNotApplicable}, nil
		}
		newWire, ok := compiledEncodingGet(currentEncodings, name)
		if !ok {
			return InheritedRebase{Kind: InheritedNotApplicable}, nil
		}
		donorName = name
		oldRow, newRow = oldCandidate, newCandidate
		oldEncoding, newEncoding = oldWire, newWire
	}
	if donorName == "" {
		return InheritedRebase{Kind: InheritedNotApplicable}, nil
	}
	old := cloneValue(oldRow)
	oldWire := cloneEncoding(oldEncoding)
	if _, err := enums.Normalize(&old, &oldWire); err != nil {
		return InheritedRebase{}, err
	}
	var patches []compiled.Patch
	if err := compiled.CollectPatches(&old, value, encoding, nil, &patches); err != nil {
		return InheritedRebase{}, err
	}
	rebased := cloneValue(newRow)
	rebasedEncoding := cloneEncoding(newEncoding)
	for _, patch := range patches {
		promoted, err := compiled.PromotePatch(newRow, newEncoding, value, encoding, patch)
		if err != nil {
			return InheritedRebase{}, err
		}
		if len(promoted.Path) == 0 {
			return InheritedRebase{Kind: InheritedNotApplicable}, nil
		}
		if err := compiled.SetValueAtPath(&rebased, promoted.Path, promoted.Value); err != nil {
			return InheritedRebase{}, err
		}
		if err := compiled.SetEncodingAtPath(&rebasedEncoding, promoted.Path, promoted.Encoding); err != nil {
			return InheritedRebase{}, err
		}
	}
	return InheritedRebase{Kind: InheritedRebased, Value: rebased, Encoding: rebasedEncoding}, nil
}
