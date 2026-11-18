// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_sounds.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package modelsounds ports the tagged-sound binding repair. Models compiled
// before the game renamed the block keep their own bindings under the old name,
// which the game no longer reads; the repair restores the binding the current
// game model expects without replacing the mod's own sound tables.
package modelsounds

import (
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modeltext"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

const (
	// Settings is the binding name the current game reads.
	Settings = "TaggedSoundSettings_t"
	// LegacySettings is the binding name older compilers wrote.
	LegacySettings = "CitadelTaggedSoundSettings_t"
	tablesPrefix   = "scripts/tagged_sounds/"
)

// Resolve returns the bytes of a resource path. A nil slice with a nil error
// means the resource does not exist. Errors abort the repair: the caller must
// not treat an unreadable dependency as a safe "no repair".
type Resolve func(path string) ([]byte, error)

// Missing reports whether the current game model binds tagged sounds and this
// model has no binding the game reads.
func Missing(model, current *kv3.Value) bool {
	currentText, ok := modelText(current)
	if !ok {
		return false
	}
	_, currentFields, ok := modeltext.RootFields(currentText)
	if !ok {
		return false
	}
	if _, ok := modeltext.Field(currentFields, Settings); !ok {
		return false
	}
	text, ok := modelText(model)
	if !ok || strings.TrimSpace(text) == "" {
		return true
	}
	_, fields, ok := modeltext.RootFields(text)
	return ok && !hasField(fields, Settings)
}

// Repair restores the tagged-sound binding. It returns repaired=false when the
// model is already correct or no safe repair exists.
func Repair(source, base []byte, resolve Resolve) ([]byte, bool, error) {
	format, model, encoding, err := camera.DecodeCompiled(source)
	if err != nil {
		return nil, false, nil
	}
	_, current, _, err := camera.DecodeCompiled(base)
	if err != nil {
		return nil, false, nil
	}
	currentText, ok := modelText(&current)
	if !ok {
		return nil, false, nil
	}
	_, currentFields, ok := modeltext.RootFields(currentText)
	if !ok {
		return nil, false, nil
	}
	currentBinding, ok := modeltext.Field(currentFields, Settings)
	if !ok {
		return nil, false, nil
	}
	text, ok := modelText(&model)
	if !ok {
		return nil, false, nil
	}
	closing, fields, ok := modeltext.RootFields(text)
	if !ok {
		return nil, false, nil
	}
	// An authored binding, even an empty one, may silence these sounds on
	// purpose.
	if hasField(fields, Settings) {
		return nil, false, nil
	}
	// Renaming keeps the mod's own tables, including custom sounds.
	var updated string
	var addedTables []string
	if legacy, ok := modeltext.Field(fields, LegacySettings); ok {
		addedTables, ok = bindingTables(text[legacy.Start:legacy.End])
		if !ok {
			return nil, false, nil
		}
		nameEnd := legacy.Start + len(LegacySettings)
		updated = text[:legacy.Start] + Settings + text[nameEnd:]
	} else {
		binding := currentText[currentBinding.Start:currentBinding.End]
		addedTables, ok = bindingTables(binding)
		if !ok {
			return nil, false, nil
		}
		updated = text[:closing] + "\n" + binding + "\n" + text[closing:]
	}
	if _, fields, ok := modeltext.RootFields(updated); !ok || !hasField(fields, Settings) {
		return nil, false, nil
	}

	gameReferences, err := resource.ReferencesOf(base)
	if err != nil {
		gameReferences = nil
	}
	references := make([]resource.Reference, 0, len(addedTables))
	for _, table := range addedTables {
		bytes, err := resolve(table)
		if err != nil {
			return nil, false, err
		}
		if bytes == nil {
			return nil, false, nil
		}
		_, tableValue, _, err := camera.DecodeCompiled(bytes)
		if err != nil {
			return nil, false, nil
		}
		id, err := resource.ResourceID(table)
		if err != nil {
			return nil, false, nil
		}
		kindValue, ok := tableValue.Get("generic_data_type")
		if !ok {
			return nil, false, nil
		}
		kind, kindOK := kindValue.AsString()
		if !kindOK || kind != "CTaggedSound_t" {
			return nil, false, nil
		}
		for _, reference := range gameReferences {
			if reference.Path == table && reference.ID != id {
				return nil, false, nil
			}
		}
		references = append(references, resource.Reference{ID: id, Path: table})
	}

	info, ok := model.Get("m_modelInfo")
	if !ok || info.Kind != kv3.KindObject {
		return nil, false, nil
	}
	setCaseInsensitive(info, "m_keyValueText", kv3.String(updated))
	return rebuild(source, &model, &encoding, format, references)
}

func rebuild(source []byte, model *kv3.Value, encoding *kv3.Encoding, format kv3.Format, references []resource.Reference) ([]byte, bool, error) {
	data, err := kv3.EncodePreserving(model, encoding, format)
	if err != nil {
		return nil, false, nil
	}
	rebuilt, err := resource.Parse(source)
	if err != nil {
		return nil, false, nil
	}
	rebuiltBytes, err := rebuilt.RebuildWithDataPreserving(data)
	if err != nil {
		return nil, false, nil
	}
	existing, err := resource.ReferencesOf(rebuiltBytes)
	if err != nil {
		return nil, false, nil
	}
	additions := make([]resource.Reference, 0, len(references))
	for _, reference := range references {
		if !slices.Contains(existing, reference) {
			additions = append(additions, reference)
		}
	}
	if len(additions) > 0 {
		rebuiltBytes, err = resource.AddReferencesOf(rebuiltBytes, additions)
		if err != nil {
			return nil, false, nil
		}
	}
	actualFormat, actual, actualEncoding, err := camera.DecodeCompiled(rebuiltBytes)
	if err != nil || actualFormat != format || !camera.RuntimeValuesEqual(model, &actual) ||
		!kv3.EncodingPreserved(model, encoding, &actualEncoding) {
		return nil, false, nil
	}
	return rebuiltBytes, true, nil
}

// bindingTables returns the table paths of a
// `{ m_taggedSounds = [ resource:"…", … ] }` binding. Any other field or
// reference form is an unknown schema.
func bindingTables(assignment string) ([]string, bool) {
	object, ok := modeltext.AssignedValue(assignment)
	if !ok {
		return nil, false
	}
	_, fields, ok := modeltext.RootFields(object)
	if !ok || len(fields) != 1 {
		return nil, false
	}
	var rangeOf modeltext.Range
	for name, r := range fields {
		if name != "m_taggedSounds" {
			return nil, false
		}
		rangeOf = r
	}
	value, ok := modeltext.AssignedValue(object[rangeOf.Start:rangeOf.End])
	if !ok {
		return nil, false
	}
	items, ok := modeltext.ArrayItems(value)
	if !ok {
		return nil, false
	}
	tables := make([]string, 0, len(items))
	for _, item := range items {
		table, ok := modeltext.ResourceName(item)
		if !ok {
			return nil, false
		}
		// Null resource slots are retained in the authored text, but have no
		// dependency to resolve or add to RERL.
		if table == "" {
			continue
		}
		if !strings.HasPrefix(table, tablesPrefix) || !strings.HasSuffix(table, ".vdata") {
			return nil, false
		}
		for part := range strings.SplitSeq(table, "/") {
			if part == "" || part == "." || part == ".." {
				return nil, false
			}
		}
		tables = append(tables, table)
	}
	if len(tables) == 0 {
		return nil, false
	}
	return tables, true
}

func modelText(model *kv3.Value) (string, bool) {
	info, ok := model.Get("m_modelInfo")
	if !ok || info.Kind != kv3.KindObject {
		return "", false
	}
	text, ok := info.Get("m_keyValueText")
	if !ok {
		return "", false
	}
	return text.AsString()
}

func hasField(fields map[string]modeltext.Range, name string) bool {
	_, ok := modeltext.Field(fields, name)
	return ok
}

func setCaseInsensitive(object *kv3.Value, key string, value kv3.Value) {
	for i := range object.Object {
		if strings.EqualFold(object.Object[i].Key, key) {
			object.Object[i].Value = value
			return
		}
	}
	object.Object = append(object.Object, kv3.Pair{Key: key, Value: value})
}
