// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/vdata_compatibility.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package vdata

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

const developmentStateField = "m_eHeroDevelopmentState"

// Prepare merges every table, rebases inherited rows, restores dependencies and
// reports what could not be proven safe.
func Prepare(tables []*Table, resolutions []Resolution) (*Prepared, error) {
	merged := make([]*Merged, len(tables))
	for index, table := range tables {
		value, err := table.Merge(resolutions)
		if err != nil {
			return nil, err
		}
		merged[index] = value
	}
	resolutionFor := func(group *Group) *Resolution {
		for index := range resolutions {
			if resolutions[index].ConflictKey == group.Key {
				return &resolutions[index]
			}
		}
		return nil
	}
	prepared := &Prepared{Merged: merged}
	var missing []missingReference
	skippedRebases := make(map[defKey]struct{})
	omittedHeroes := make(map[defKey]struct{})
	for index, table := range tables {
		for _, key := range slices.Sorted(maps.Keys(table.Groups)) {
			group := table.Groups[key]
			winner, err := ChooseWinner(group, resolutionFor(group))
			if err != nil {
				return nil, err
			}
			if winner == nil || winner.Unrebased == nil {
				continue
			}
			original := &winner.Unrebased.Value
			originalEncoding := &winner.Unrebased.Encoding
			parent, ok := modifiedInheritedParent(original, table.FilePath, merged[index])
			if !ok {
				continue
			}
			if err := compiled.ObjectSet(&merged[index].Root, group.RowName, *original); err != nil {
				return nil, err
			}
			if err := compiled.EncodingSet(&merged[index].Encoding, group.RowName, *originalEncoding); err != nil {
				return nil, err
			}
			skippedRebases[defKey{index, asciiLower(group.RowName)}] = struct{}{}
			prepared.Warnings = append(prepared.Warnings, DataWarning{
				ModID:      winner.Candidate.ModID,
				SourceVPK:  winner.Candidate.SourceVPK,
				FilePath:   table.FilePath,
				RowName:    group.RowName,
				Kind:       WarningUnverifiedInheritance,
				FieldPath:  compiled.PathLabel(parent.Path),
				TargetFile: parent.Table,
				TargetRow:  parent.Row,
				Reason:     ReasonCompetingDefinitions,
			})
		}
	}
	for index, table := range tables {
		for _, repair := range table.RowRepairs {
			if repair.Kind == RepairInheritedDefinition {
				if _, skipped := skippedRebases[defKey{index, asciiLower(repair.RowName)}]; skipped {
					continue
				}
			}
			var path []kv3.Seg
			if repair.Kind == RepairHeroDevelopmentState {
				path = []kv3.Seg{kv3.KeySeg(developmentStateField)}
			}
			owner := mergedOwner(merged[index], repair.RowName, path)
			if owner == nil || owner.ModID != repair.ModID || owner.SourceVPK != repair.SourceVPK {
				continue
			}
			if repair.Kind == RepairHeroDevelopmentState {
				row, ok := rowOf(&merged[index].Root, repair.RowName)
				if !ok {
					continue
				}
				state, ok := stringField(row, developmentStateField)
				if !ok || state != repair.Detail {
					continue
				}
			}
			prepared.Repairs = append(prepared.Repairs, repair)
		}
		for _, warning := range table.RowWarnings {
			owner := mergedOwner(merged[index], warning.RowName, nil)
			if owner != nil && owner.ModID == warning.ModID && owner.SourceVPK == warning.SourceVPK {
				prepared.Warnings = append(prepared.Warnings, warning)
			}
		}
		if merged[index].Root.Kind != kv3.KindObject {
			continue
		}
		for _, row := range merged[index].Root.Object {
			name := row.Key
			value := &row.Value
			if table.FilePath != HeroesVDataPath {
				if _, exists := rowOf(&table.BaseRoot, name); !exists {
					if _, skipped := skippedRebases[defKey{index, asciiLower(name)}]; !skipped {
						if hasInheritanceReference(value, table.FilePath) {
							if class, ok := stringField(value, "_class"); ok &&
								!KnownNativeClass(value, &table.BaseRoot) {
								if owner := mergedOwner(merged[index], name, nil); owner != nil {
									prepared.Warnings = append(prepared.Warnings, DataWarning{
										ModID: owner.ModID, SourceVPK: owner.SourceVPK,
										FilePath: table.FilePath, RowName: name,
										Kind:   WarningUnverifiedInheritance,
										Reason: ReasonUnrecognizedClassOrEnum,
										Detail: "_class=" + class,
									})
								}
							}
						}
					}
				}
			}
			if table.FilePath == HeroesVDataPath {
				if class, ok := stringField(value, "_class"); ok && class == heroDataClass {
					if disabled, ok := boolField(value, "m_bDisabled"); ok && !disabled {
						if inDevelopment, ok := boolField(value, "m_bInDevelopment"); ok && !inDevelopment {
							if _, hasState := stringField(value, developmentStateField); !hasState {
								if _, baseRow := rowOf(&table.BaseRoot, name); !baseRow && baseHasDevelopmentState(&table.BaseRoot) {
									if owner := mergedOwner(merged[index], name, nil); owner != nil {
										prepared.Warnings = append(prepared.Warnings, DataWarning{
											ModID: owner.ModID, SourceVPK: owner.SourceVPK,
											FilePath: table.FilePath, RowName: name,
											Kind:      WarningMissingDevelopmentState,
											FieldPath: developmentStateField,
											Reason:    ReasonVisibilityMigrationUnverified,
										})
									}
								}
							}
						}
					}
				}
			}
			for _, reference := range References(value, table.FilePath) {
				if targetHasRow(tables, merged, reference.Table, reference.Row) {
					continue
				}
				vanillaReference := false
				if baseRow, ok := rowOf(&table.BaseRoot, name); ok {
					for _, old := range References(baseRow, table.FilePath) {
						if old.PathEqual(reference.Path) && strings.EqualFold(old.Row, reference.Row) {
							vanillaReference = true
							break
						}
					}
				}
				vanillaTarget := false
				for _, target := range tables {
					if target.FilePath != reference.Table {
						continue
					}
					if _, ok := rowOf(&target.BaseRoot, reference.Row); ok {
						vanillaTarget = true
						break
					}
				}
				if vanillaReference && !vanillaTarget {
					continue
				}
				if owner := mergedOwner(merged[index], name, reference.Path); owner != nil {
					missing = append(missing, missingReference{index: index, name: name, reference: reference, owner: *owner})
				}
			}
		}
	}
	for _, item := range missing {
		needed, failure := collectNeeded(item.reference, tables, merged)
		if failure == nil {
			for _, key := range slices.SortedFunc(maps.Keys(needed), compareDefKeys) {
				candidate := needed[key]
				if err := compiled.ObjectSet(&merged[key.index].Root, key.row, candidate.Value); err != nil {
					return nil, err
				}
				if err := compiled.EncodingSet(&merged[key.index].Encoding, key.row, candidate.Encoding); err != nil {
					return nil, err
				}
				merged[key.index].AppliedRows[key.row] = struct{}{}
				prepared.Repairs = append(prepared.Repairs, DataRepair{
					ModID: candidate.Candidate.ModID, SourceVPK: candidate.Candidate.SourceVPK,
					FilePath: tables[key.index].FilePath, RowName: key.row,
					Kind: RepairRetainedDependency,
					Detail: fmt.Sprintf("%s:%s · %s", tables[item.index].FilePath, item.name,
						compiled.PathLabel(item.reference.Path)),
				})
			}
			continue
		}
		unavailable := failure.reason == ReasonDefinitionUnavailable
		if unavailable {
			restored, err := referenceFallbackRestore(tables[item.index], merged[item.index], item.name, item.reference)
			if err != nil {
				return nil, err
			}
			if restored {
				prepared.Repairs = append(prepared.Repairs, DataRepair{
					ModID: item.owner.ModID, SourceVPK: item.owner.SourceVPK,
					FilePath: tables[item.index].FilePath, RowName: item.name,
					Kind: RepairCurrentReference,
					Detail: fmt.Sprintf("%s · %s:%s unavailable; current game binding restored",
						compiled.PathLabel(item.reference.Path), item.reference.Table, item.reference.Row),
				})
				continue
			}
		}
		kind := failure.kind
		if unavailable && unavailableNewHero(tables[item.index], merged[item.index], item.name, item.reference) {
			omittedHeroes[defKey{item.index, asciiLower(item.name)}] = struct{}{}
			kind = WarningOmittedHeroDefinition
		}
		prepared.Warnings = append(prepared.Warnings, DataWarning{
			ModID: item.owner.ModID, SourceVPK: item.owner.SourceVPK,
			FilePath: tables[item.index].FilePath, RowName: item.name,
			Kind: kind, FieldPath: compiled.PathLabel(item.reference.Path),
			TargetFile: item.reference.Table, TargetRow: item.reference.Row,
			Reason: failure.reason,
			Detail: fmt.Sprintf("%s:%s", failure.file, failure.row),
		})
	}
	for _, key := range slices.SortedFunc(maps.Keys(omittedHeroes), compareDefKeys) {
		omitRow(merged[key.index], key.row)
	}
	// A failed chain may subsequently be satisfied by another successful repair.
	kept := prepared.Warnings[:0]
	for _, warning := range prepared.Warnings {
		if warning.Kind == WarningMissingDevelopmentState || warning.Kind == WarningUnverifiedInheritance {
			kept = append(kept, warning)
			continue
		}
		satisfied := false
		for index, table := range tables {
			if table.FilePath != warning.TargetFile {
				continue
			}
			if _, ok := rowOf(&merged[index].Root, warning.TargetRow); ok {
				satisfied = true
				break
			}
		}
		if !satisfied {
			kept = append(kept, warning)
		}
	}
	prepared.Warnings = kept
	return prepared, nil
}

type missingReference struct {
	index     int
	name      string
	reference Reference
	owner     Candidate
}

// pathEqual reports whether two reference paths are identical.
func (r Reference) PathEqual(other []kv3.Seg) bool {
	return slices.EqualFunc(r.Path, other, func(a, b kv3.Seg) bool {
		return a.Kind == b.Kind && a.Key == b.Key && a.Index == b.Index
	})
}

func mergedOwner(merged *Merged, name string, path []kv3.Seg) *Candidate {
	origins := merged.Origins[asciiLower(name)]
	var best *Origin
	for index := range origins {
		patch := origins[index].Path
		if pathHasPrefix(path, patch) || pathHasPrefix(patch, path) {
			if best == nil || len(patch) > len(best.Path) {
				best = &origins[index]
			}
		}
	}
	if best == nil {
		return nil
	}
	return &best.Candidate
}

func pathHasPrefix(path, prefix []kv3.Seg) bool {
	if len(prefix) > len(path) {
		return false
	}
	return slices.EqualFunc(path[:len(prefix)], prefix, func(a, b kv3.Seg) bool {
		return a.Kind == b.Kind && a.Key == b.Key && a.Index == b.Index
	})
}

func rowOf(root *kv3.Value, name string) (*kv3.Value, bool) {
	if root.Kind != kv3.KindObject {
		return nil, false
	}
	return compiledObjectGet(root.Object, name)
}

func hasInheritanceReference(value *kv3.Value, file string) bool {
	for _, reference := range References(value, file) {
		if reference.Inheritance {
			return true
		}
	}
	return false
}

func modifiedInheritedParent(value *kv3.Value, file string, merged *Merged) (Reference, bool) {
	for _, reference := range References(value, file) {
		if !reference.Inheritance {
			continue
		}
		if _, ok := merged.Origins[asciiLower(reference.Row)]; ok {
			return reference, true
		}
	}
	return Reference{}, false
}

func baseHasDevelopmentState(root *kv3.Value) bool {
	if root.Kind != kv3.KindObject {
		return false
	}
	for _, row := range root.Object {
		if _, ok := compiledObjectGet(row.Value.Object, developmentStateField); ok && row.Value.Kind == kv3.KindObject {
			return true
		}
	}
	return false
}

func targetHasRow(tables []*Table, merged []*Merged, file, name string) bool {
	for index, table := range tables {
		if table.FilePath != file {
			continue
		}
		if _, ok := rowOf(&merged[index].Root, name); ok {
			return true
		}
	}
	return false
}

type defKey struct {
	index int
	row   string
}

func compareDefKeys(a, b defKey) int {
	if a.index != b.index {
		return a.index - b.index
	}
	return strings.Compare(a.row, b.row)
}

type dependencyFailure struct {
	kind   WarningKind
	reason WarningReason
	file   string
	row    string
}

func (f *dependencyFailure) Error() string {
	return fmt.Sprintf("%s (%s)", f.kind, f.reason)
}

func newDependencyFailure(reference Reference, kind WarningKind, reason WarningReason) *dependencyFailure {
	return &dependencyFailure{kind: kind, reason: reason, file: reference.Table, row: reference.Row}
}

func collectNeeded(reference Reference, tables []*Table, merged []*Merged) (map[defKey]*CandidateValue, *dependencyFailure) {
	var pending []Reference
	pending = append(pending, reference)
	needed := make(map[defKey]*CandidateValue)
	inheritanceEdges := make(map[defKey][]defKey)
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		index := -1
		for candidate, table := range tables {
			if table.FilePath == current.Table {
				index = candidate
				break
			}
		}
		if index < 0 {
			return nil, newDependencyFailure(current, WarningMissingDefinition, ReasonDefinitionUnavailable)
		}
		if _, ok := rowOf(&merged[index].Root, current.Row); ok {
			continue
		}
		key := defKey{index, asciiLower(current.Row)}
		if _, ok := needed[key]; ok {
			continue
		}
		candidates := tables[index].RetiredRows[key.row]
		if len(candidates) == 0 {
			return nil, newDependencyFailure(current, WarningMissingDefinition, ReasonDefinitionUnavailable)
		}
		candidate := &candidates[0]
		for other := range candidates {
			if !compiled.RuntimeValuesEqual(&candidate.Value, &candidates[other].Value) ||
				!encodingEqual(&candidate.Encoding, &candidates[other].Encoding) {
				return nil, newDependencyFailure(current, WarningUnverifiedDefinition, ReasonCompetingDefinitions)
			}
		}
		if !KnownClassAndEnums(&candidate.Value, &tables[index].BaseRoot) {
			return nil, newDependencyFailure(current, WarningUnverifiedDefinition, ReasonUnrecognizedClassOrEnum)
		}
		needed[key] = candidate
		for _, child := range References(&candidate.Value, tables[index].FilePath) {
			if child.Inheritance {
				if _, ok := merged[index].Origins[asciiLower(child.Row)]; ok {
					return nil, newDependencyFailure(child, WarningUnverifiedDefinition, ReasonCompetingDefinitions)
				}
				inheritanceEdges[key] = append(inheritanceEdges[key], defKey{index, asciiLower(child.Row)})
			}
			pending = append(pending, child)
		}
	}
	// Ability triggers can legitimately refer back to each other; inheritance
	// cannot.
	remaining := make(map[defKey]struct{}, len(needed))
	for key := range needed {
		remaining[key] = struct{}{}
	}
	for len(remaining) > 0 {
		var leaves []defKey
		for key := range remaining {
			leaf := true
			for _, target := range inheritanceEdges[key] {
				if _, waiting := remaining[target]; waiting {
					leaf = false
					break
				}
			}
			if leaf {
				leaves = append(leaves, key)
			}
		}
		if len(leaves) == 0 {
			break
		}
		for _, leaf := range leaves {
			delete(remaining, leaf)
		}
	}
	if len(remaining) > 0 {
		return nil, newDependencyFailure(reference, WarningUnverifiedDefinition, ReasonInheritanceCycle)
	}
	return needed, nil
}

func omitRow(merged *Merged, name string) {
	if merged.Root.Kind == kv3.KindObject {
		merged.Root.Object = slices.DeleteFunc(merged.Root.Object, func(pair kv3.Pair) bool {
			return strings.EqualFold(pair.Key, name)
		})
	}
	if merged.Encoding.Children.Kind == kv3.ChildrenObject {
		merged.Encoding.Children.Object = slices.DeleteFunc(merged.Encoding.Children.Object, func(field kv3.EncodingField) bool {
			return strings.EqualFold(field.Key, name)
		})
	}
	delete(merged.AppliedRows, asciiLower(name))
	delete(merged.Origins, asciiLower(name))
}

func unavailableNewHero(table *Table, merged *Merged, name string, reference Reference) bool {
	if _, ok := rowOf(&table.BaseRoot, name); ok {
		return false
	}
	if table.FilePath != HeroesVDataPath || reference.Inheritance || reference.Table != AbilitiesPath {
		return false
	}
	if len(reference.Path) == 0 || reference.Path[0].Kind != kv3.SegKey || reference.Path[0].Key != "m_mapBoundAbilities" {
		return false
	}
	row, ok := rowOf(&merged.Root, name)
	if !ok {
		return false
	}
	if class, ok := stringField(row, "_class"); !ok || class != heroDataClass {
		return false
	}
	// Never introduce a dangling inheritance edge by removing a shared
	// template.
	if merged.Root.Kind != kv3.KindObject {
		return false
	}
	for _, other := range merged.Root.Object {
		if strings.EqualFold(other.Key, name) {
			continue
		}
		for _, candidate := range References(&other.Value, HeroesVDataPath) {
			if candidate.Table == HeroesVDataPath && strings.EqualFold(candidate.Row, name) {
				return false
			}
		}
	}
	return true
}

func referenceFallbackRestore(table *Table, merged *Merged, name string, reference Reference) (bool, error) {
	if reference.Inheritance {
		return false, nil
	}
	current, ok := rowOf(&table.BaseRoot, name)
	if !ok {
		return false, nil
	}
	authored, ok := rowOf(&merged.Root, name)
	if !ok {
		return false, nil
	}
	currentClass, ok := stringField(current, "_class")
	if !ok {
		return false, nil
	}
	authoredClass, ok := stringField(authored, "_class")
	if !ok || authoredClass != currentClass {
		return false, nil
	}
	// Scope this migration to the known hero ability-binding contract.
	if table.FilePath != HeroesVDataPath || currentClass != heroDataClass ||
		len(reference.Path) == 0 || reference.Path[0].Kind != kv3.SegKey ||
		reference.Path[0].Key != "m_mapBoundAbilities" || reference.Table != AbilitiesPath {
		return false, nil
	}
	if merged.Encoding.Children.Kind != kv3.ChildrenObject {
		return false, nil
	}
	authoredEncoding, ok := compiledEncodingGet(merged.Encoding.Children.Object, name)
	if !ok {
		return false, nil
	}
	currentEncoding, ok := compiledEncodingGet(table.BaseEncoding.Children.Object, name)
	if !ok {
		return false, nil
	}
	value := cloneValue(authored)
	encoding := cloneEncoding(authoredEncoding)
	replacement, ok := compiled.ValueAtPath(current, reference.Path)
	if ok {
		// Reference leaves are strings. Decline a schema-changing container edit.
		if replacement.Kind != kv3.KindString {
			return false, nil
		}
		replacementEncoding, ok := compiled.EncodingAtPath(currentEncoding, reference.Path)
		if !ok {
			return false, nil
		}
		if err := compiled.SetValueAtPath(&value, reference.Path, cloneValue(replacement)); err != nil {
			return false, err
		}
		if err := compiled.SetEncodingAtPath(&encoding, reference.Path, cloneEncoding(replacementEncoding)); err != nil {
			return false, err
		}
	} else if !removeOptionalSlot(&value, &encoding, reference.Path) {
		return false, nil
	}
	if err := compiled.ObjectSet(&merged.Root, name, value); err != nil {
		return false, err
	}
	if err := compiled.EncodingSet(&merged.Encoding, name, encoding); err != nil {
		return false, err
	}
	return true, nil
}

func removeOptionalSlot(value *kv3.Value, encoding *kv3.Encoding, path []kv3.Seg) bool {
	// Only an obsolete direct map entry may be removed. Never shift array
	// indices or erase nested metadata merely because a current parent field
	// is absent.
	if len(path) != 2 || path[0].Kind != kv3.SegKey || path[1].Kind != kv3.SegKey {
		return false
	}
	if value.Kind != kv3.KindObject {
		return false
	}
	values, ok := compiledObjectGet(value.Object, path[0].Key)
	if !ok || values.Kind != kv3.KindObject {
		return false
	}
	if encoding.Children.Kind != kv3.ChildrenObject {
		return false
	}
	mapEncoding, ok := compiledEncodingGet(encoding.Children.Object, path[0].Key)
	if !ok || mapEncoding.Children.Kind != kv3.ChildrenObject {
		return false
	}
	slot := path[1].Key
	valueIndex := -1
	for index, entry := range values.Object {
		if entry.Key == slot {
			valueIndex = index
			break
		}
	}
	encodingIndex := -1
	for index, field := range mapEncoding.Children.Object {
		if field.Key == slot {
			encodingIndex = index
			break
		}
	}
	if valueIndex < 0 || encodingIndex < 0 || values.Object[valueIndex].Value.Kind != kv3.KindString {
		return false
	}
	values.Object = slices.Delete(values.Object, valueIndex, valueIndex+1)
	mapEncoding.Children.Object = slices.Delete(mapEncoding.Children.Object, encodingIndex, encodingIndex+1)
	return true
}
