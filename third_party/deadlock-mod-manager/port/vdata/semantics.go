// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/vdata_compatibility/
// semantics.rs at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

// Package vdata implements the game-data compatibility rules: inherited
// definitions, enum aliases, hero visibility fields and reference fallbacks.
package vdata

import (
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// AbilitiesPath is the ability table that hero and ability rows reference.
const AbilitiesPath = "scripts/abilities.vdata_c"

// Reference is one VData row reference named by a string field.
type Reference struct {
	Path        []kv3.Seg
	Table       string
	Row         string
	Inheritance bool
}

// References lists VData table references in a value. Native `_class` names,
// resource paths and CSS classes have different lookup rules and are not
// table rows.
func References(value *kv3.Value, file string) []Reference {
	var out []Reference
	walkReferences(value, file, nil, &out)
	return out
}

func walkReferences(value *kv3.Value, file string, path []kv3.Seg, out *[]Reference) {
	switch value.Kind {
	case kv3.KindObject:
		for _, field := range value.Object {
			if strings.EqualFold(field.Key, "_editor") {
				continue
			}
			next := append(append([]kv3.Seg(nil), path...), kv3.KeySeg(field.Key))
			inheritance := field.Key == "_base" || field.Key == "_multibase"
			target := ""
			switch {
			case inheritance:
				target = file
			case field.Key == "m_mapBoundAbilities",
				file == AbilitiesPath && abilityReferenceField(field.Key):
				target = AbilitiesPath
			}
			if target == "" {
				walkReferences(&field.Value, file, next, out)
				continue
			}
			collectReferences(&field.Value, target, inheritance, next, out)
		}
	case kv3.KindArray:
		for index := range value.Array {
			next := append(append([]kv3.Seg(nil), path...), kv3.IndexSeg(uint64(index)))
			walkReferences(&value.Array[index], file, next, out)
		}
	}
}

func abilityReferenceField(field string) bool {
	switch field {
	case "m_AbilityToTrigger", "m_NewWeaponAbility", "m_StartingWeaponAbility",
		"m_strBaseSlideAbility", "m_strViperSlideAbility":
		return true
	}
	return false
}

func collectReferences(value *kv3.Value, table string, inheritance bool, path []kv3.Seg, out *[]Reference) {
	switch value.Kind {
	case kv3.KindString:
		if value.String != "" {
			*out = append(*out, Reference{
				Path:        append([]kv3.Seg(nil), path...),
				Table:       table,
				Row:         value.String,
				Inheritance: inheritance,
			})
		}
	case kv3.KindObject:
		for _, field := range value.Object {
			next := append(append([]kv3.Seg(nil), path...), kv3.KeySeg(field.Key))
			collectReferences(&field.Value, table, inheritance, next, out)
		}
	case kv3.KindArray:
		for index := range value.Array {
			next := append(append([]kv3.Seg(nil), path...), kv3.IndexSeg(uint64(index)))
			collectReferences(&value.Array[index], table, inheritance, next, out)
		}
	}
}

// KnownClassAndEnums reports whether a mod definition uses a class and enum
// names that exist in the current game table.
func KnownClassAndEnums(value, current *kv3.Value) bool {
	_, unsupported := UnsupportedDefinition(value, current)
	return !unsupported
}

// KnownNativeClass reports whether any current row uses the value's class.
func KnownNativeClass(value, current *kv3.Value) bool {
	class, ok := stringField(value, "_class")
	if !ok || current.Kind != kv3.KindObject {
		return false
	}
	for _, row := range current.Object {
		if candidate, ok := stringField(&row.Value, "_class"); ok && candidate == class {
			return true
		}
	}
	return false
}

// UnsupportedDefinition names the class or enum value the current table does
// not recognize, or reports false when the definition is known.
func UnsupportedDefinition(value, current *kv3.Value) (string, bool) {
	class, ok := stringField(value, "_class")
	if !ok {
		return "_class", true
	}
	if current.Kind != kv3.KindObject {
		return "_class=" + class, true
	}
	knownClass := false
	for _, row := range current.Object {
		if candidate, ok := stringField(&row.Value, "_class"); ok && candidate == class {
			knownClass = true
			break
		}
	}
	if !knownClass {
		return "_class=" + class, true
	}
	authored := make(map[string]enumEntry)
	enums(value, nil, authored)
	known := make(map[string]enumEntry)
	for _, row := range current.Object {
		enums(&row.Value, nil, known)
	}
	var missing []enumEntry
	for key, entry := range authored {
		if _, ok := known[key]; !ok {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return "", false
	}
	slices.SortFunc(missing, func(a, b enumEntry) int {
		if order := slices.Compare(a.path, b.path); order != 0 {
			return order
		}
		return strings.Compare(a.value, b.value)
	})
	first := missing[0]
	return strings.Join(first.path, ".") + "=" + first.value, true
}

type enumEntry struct {
	path  []string
	value string
}

func enumMapKey(path []string, value string) string {
	return strings.Join(path, "\x00") + "\x1f" + value
}

func enums(value *kv3.Value, path []string, out map[string]enumEntry) {
	switch value.Kind {
	case kv3.KindObject:
		for _, field := range value.Object {
			if strings.EqualFold(field.Key, "_editor") {
				continue
			}
			next := append(append([]string(nil), path...), field.Key)
			if tail, ok := strings.CutPrefix(field.Key, "m_e"); ok &&
				tail != "" && tail[0] >= 'A' && tail[0] <= 'Z' {
				if field.Value.Kind == kv3.KindString {
					for item := range strings.SplitSeq(field.Value.String, "|") {
						item = strings.TrimSpace(item)
						if item != "" {
							out[enumMapKey(next, item)] = enumEntry{path: append([]string(nil), next...), value: item}
						}
					}
				}
			}
			enums(&field.Value, next, out)
		}
	case kv3.KindArray:
		next := append(append([]string(nil), path...), "[]")
		for index := range value.Array {
			enums(&value.Array[index], next, out)
		}
	}
}

func stringField(value *kv3.Value, key string) (string, bool) {
	if value.Kind != kv3.KindObject {
		return "", false
	}
	for _, field := range value.Object {
		if strings.EqualFold(field.Key, key) && field.Value.Kind == kv3.KindString {
			return field.Value.String, true
		}
	}
	return "", false
}

// EnumCompatibility migrates enum aliases demonstrated by matching old and
// current vanilla fields. No prefix is removed from arbitrary strings.
type EnumCompatibility struct {
	aliases map[aliasKey]*EnumReplacement
}

type aliasKey struct {
	field string
	value string
}

// EnumReplacement is the current enum value and its wire encoding.
type EnumReplacement struct {
	Value    string
	Encoding kv3.Encoding
}

// LearnEnumCompatibility compares mod rows with the current table's rows.
func LearnEnumCompatibility(currentRows []kv3.Pair, currentEncodings []kv3.EncodingField,
	sourceRows []kv3.Pair) *EnumCompatibility {
	compatibility := &EnumCompatibility{aliases: make(map[aliasKey]*EnumReplacement)}
	for _, source := range sourceRows {
		current, ok := compiledObjectGet(currentRows, source.Key)
		if !ok {
			continue
		}
		encoding, ok := compiledEncodingGet(currentEncodings, source.Key)
		if !ok {
			continue
		}
		compatibility.compare(current, encoding, &source.Value, "")
	}
	return compatibility
}

func (e *EnumCompatibility) compare(current *kv3.Value, encoding *kv3.Encoding, source *kv3.Value, field string) {
	if current.Kind == kv3.KindObject && source.Kind == kv3.KindObject &&
		encoding.Children.Kind == kv3.ChildrenObject {
		for _, sourceField := range source.Object {
			if strings.EqualFold(sourceField.Key, "_editor") {
				continue
			}
			currentField, ok := compiledObjectGet(current.Object, sourceField.Key)
			if !ok {
				continue
			}
			fieldEncoding, ok := compiledEncodingGet(encoding.Children.Object, sourceField.Key)
			if !ok {
				continue
			}
			e.compare(currentField, fieldEncoding, &sourceField.Value, sourceField.Key)
		}
		return
	}
	if current.Kind == kv3.KindArray && source.Kind == kv3.KindArray &&
		len(current.Array) == len(source.Array) &&
		encoding.Children.Kind == kv3.ChildrenArray {
		limit := min(len(current.Array), len(source.Array), len(encoding.Children.Array))
		for index := range limit {
			e.compare(&current.Array[index], &encoding.Children.Array[index], &source.Array[index], field)
		}
		return
	}
	if current.Kind != kv3.KindString || source.Kind != kv3.KindString ||
		!strings.HasPrefix(field, "m_e") || current.String == "" {
		return
	}
	prefix, ok := strings.CutSuffix(source.String, current.String)
	if !ok || !strings.HasPrefix(prefix, "E") || !strings.HasSuffix(prefix, "_") {
		return
	}
	for _, char := range prefix {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_') {
			return
		}
	}
	key := aliasKey{field: asciiLower(field), value: source.String}
	replacement := &EnumReplacement{Value: current.String, Encoding: *encoding}
	if known, exists := e.aliases[key]; exists {
		if known == nil || !enumReplacementEqual(known, replacement) {
			e.aliases[key] = nil
		}
		return
	}
	e.aliases[key] = replacement
}

func enumReplacementEqual(a, b *EnumReplacement) bool {
	return a.Value == b.Value && encodingEqual(&a.Encoding, &b.Encoding)
}

// Normalize replaces learned enum aliases in place. It returns the number of
// migrated values.
func (e *EnumCompatibility) Normalize(value *kv3.Value, encoding *kv3.Encoding) (int, error) {
	if e == nil || len(e.aliases) == 0 {
		return 0, nil
	}
	return e.normalizeField(value, encoding, "")
}

func (e *EnumCompatibility) normalizeField(value *kv3.Value, encoding *kv3.Encoding, field string) (int, error) {
	switch value.Kind {
	case kv3.KindObject:
		if encoding.Children.Kind != kv3.ChildrenObject {
			return 0, errCompiled("Object lacks enum migration encoding metadata")
		}
		count := 0
		for index := range value.Object {
			child := &value.Object[index]
			if strings.EqualFold(child.Key, "_editor") {
				continue
			}
			childEncoding, ok := compiledEncodingGetMut(encoding.Children.Object, child.Key)
			if !ok {
				return 0, errCompiled("Missing enum migration encoding for " + child.Key)
			}
			migrated, err := e.normalizeField(&child.Value, childEncoding, child.Key)
			if err != nil {
				return 0, err
			}
			count += migrated
		}
		return count, nil
	case kv3.KindArray:
		if encoding.Children.Kind != kv3.ChildrenArray {
			return 0, errCompiled("Array lacks enum migration encoding metadata")
		}
		count := 0
		limit := min(len(value.Array), len(encoding.Children.Array))
		for index := range limit {
			migrated, err := e.normalizeField(&value.Array[index], &encoding.Children.Array[index], field)
			if err != nil {
				return 0, err
			}
			count += migrated
		}
		return count, nil
	case kv3.KindString:
		replacement, ok := e.aliases[aliasKey{field: asciiLower(field), value: value.String}]
		if !ok || replacement == nil {
			return 0, nil
		}
		value.String = replacement.Value
		*encoding = replacement.Encoding
		return 1, nil
	}
	return 0, nil
}

const (
	enumMapField  = "m_mapStandardLevelUpUpgrades"
	heroDataClass = "CitadelHeroData_t"
)

// EnumMapCompatibility removes map entries whose modifier enum keys the
// current game no longer knows. The installed table supplies the domain.
type EnumMapCompatibility struct {
	keys map[string]struct{}
}

// LearnEnumMaps collects the current key domain across all hero rows.
func LearnEnumMaps(rows []kv3.Pair) *EnumMapCompatibility {
	compatibility := &EnumMapCompatibility{keys: make(map[string]struct{})}
	for _, row := range rows {
		if class, ok := stringField(&row.Value, "_class"); !ok || class != heroDataClass {
			continue
		}
		if row.Value.Kind != kv3.KindObject {
			continue
		}
		value, ok := compiledObjectGet(row.Value.Object, enumMapField)
		if !ok || value.Kind != kv3.KindObject {
			continue
		}
		for _, entry := range value.Object {
			compatibility.keys[entry.Key] = struct{}{}
		}
	}
	return compatibility
}

// Normalize drops retired enum-map keys and returns the removed key names.
func (e *EnumMapCompatibility) Normalize(value *kv3.Value, encoding *kv3.Encoding) ([]string, error) {
	if e == nil || len(e.keys) == 0 {
		return nil, nil
	}
	if class, ok := stringField(value, "_class"); !ok || class != heroDataClass {
		return nil, nil
	}
	if value.Kind != kv3.KindObject {
		return nil, nil
	}
	field, ok := compiledObjectGet(value.Object, enumMapField)
	if !ok || field.Kind != kv3.KindObject {
		return nil, nil
	}
	var removed []string
	for _, entry := range field.Object {
		if _, known := e.keys[entry.Key]; !known {
			removed = append(removed, entry.Key)
		}
	}
	if len(removed) == 0 {
		return removed, nil
	}
	if encoding.Children.Kind != kv3.ChildrenObject {
		return nil, errCompiled("Enum map lacks object encoding metadata")
	}
	fieldEncoding, ok := compiledEncodingGetMut(encoding.Children.Object, enumMapField)
	if !ok || fieldEncoding.Children.Kind != kv3.ChildrenObject {
		return nil, errCompiled("Enum map lacks object encoding metadata")
	}
	field.Object = slices.DeleteFunc(field.Object, func(pair kv3.Pair) bool {
		_, known := e.keys[pair.Key]
		return !known
	})
	fieldEncoding.Children.Object = slices.DeleteFunc(fieldEncoding.Children.Object, func(field kv3.EncodingField) bool {
		_, known := e.keys[field.Key]
		return !known
	})
	return removed, nil
}

func compiledObjectGet(rows []kv3.Pair, key string) (*kv3.Value, bool) {
	for index := range rows {
		if strings.EqualFold(rows[index].Key, key) {
			return &rows[index].Value, true
		}
	}
	return nil, false
}

func compiledEncodingGet(fields []kv3.EncodingField, key string) (*kv3.Encoding, bool) {
	for index := range fields {
		if strings.EqualFold(fields[index].Key, key) {
			return &fields[index].Value, true
		}
	}
	return nil, false
}

func compiledEncodingGetMut(fields []kv3.EncodingField, key string) (*kv3.Encoding, bool) {
	for index := range fields {
		if strings.EqualFold(fields[index].Key, key) {
			return &fields[index].Value, true
		}
	}
	return nil, false
}

func asciiLower(value string) string {
	buffer := []byte(value)
	for index, char := range buffer {
		if char >= 'A' && char <= 'Z' {
			buffer[index] = char + ('a' - 'A')
		}
	}
	return string(buffer)
}
