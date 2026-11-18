// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (compile_source,
// table merge, hero IDs) at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../NOTICE.md.

package vdata

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

const developmentState = "m_eHeroDevelopmentState"

// BuildTable collects every source's copy of one compiled table. An unreadable
// source that wins in game shadows the whole table, so no merge replaces it.
func BuildTable(filePath string, baseBytes []byte, sources []Source, history HistoryMatcher) (*Table, error) {
	format, baseRoot, baseEncoding, err := compiled.Decode(baseBytes, "base game:"+filePath)
	if err != nil {
		return nil, err
	}
	if baseRoot.Kind != kv3.KindObject {
		return nil, errCompiled("Base " + filePath + " root is not an object")
	}
	if baseEncoding.Children.Kind != kv3.ChildrenObject {
		return nil, errCompiled("Base " + filePath + " encoding root is not an object")
	}
	baseRows := baseRoot.Object
	baseEncodings := baseEncoding.Children.Object
	total := newTableSources()
	unreadable := make([]DataWarning, 0)
	unreadablePriorities := make([]int, 0)
	readablePriority := -1
	enumMaps := LearnEnumMaps(baseRows)
	for _, source := range sources {
		contribution, err := compileSource(filePath, &baseRoot, baseRows, baseEncodings, enumMaps, history, source)
		if err != nil {
			unreadable = append(unreadable, DataWarning{
				ModID:     source.ModID,
				SourceVPK: source.SourceVPK,
				FilePath:  filePath,
				Kind:      WarningUnreadableData,
				Reason:    ReasonUnreadableResource,
				Detail:    err.Error(),
			})
			unreadablePriorities = append(unreadablePriorities, source.Priority)
			continue
		}
		if readablePriority < 0 || source.Priority < readablePriority {
			readablePriority = source.Priority
		}
		total.Add(contribution)
	}
	// An unreadable file that wins in game must not be replaced by a merge of
	// the others.
	shadowed := len(unreadable) > 0
	if shadowed && readablePriority >= 0 {
		shadowed = false
		for _, priority := range unreadablePriorities {
			if priority <= readablePriority {
				shadowed = true
				break
			}
		}
	}
	return &Table{
		FilePath:           filePath,
		BaseBytes:          baseBytes,
		Format:             format,
		BaseRoot:           baseRoot,
		BaseEncoding:       baseEncoding,
		Groups:             total.Groups,
		Files:              total.Files,
		IgnoredVanillaRows: total.IgnoredVanillaRows,
		RetiredRows:        total.RetiredRows,
		RowRepairs:         total.RowRepairs,
		RowWarnings:        total.RowWarnings,
		Baselines:          total.Baselines,
		Restorations:       total.Restorations,
		Unreadable:         unreadable,
		Shadowed:           shadowed,
	}, nil
}

func compileSource(filePath string, baseRoot *kv3.Value, baseRows []kv3.Pair,
	baseEncodings []kv3.EncodingField, enumMaps *EnumMapCompatibility,
	history HistoryMatcher, source Source) (*TableSources, error) {
	contribution := newTableSources()
	contribution.Files = 1
	_, modRoot, modEncoding, err := compiled.Decode(source.Bytes,
		fmt.Sprintf("%s:%s:%s", source.ModID, source.SourceVPK, filePath))
	if err != nil {
		return nil, err
	}
	if omitsGameContent(baseRoot, &modRoot) {
		contribution.Restorations = append(contribution.Restorations, DataRepair{
			ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
			Kind: RepairRestoredGameData,
		})
	}
	var baseline BaselineMatch
	var evidence BaselineEvidence
	if history != nil {
		baseline, evidence = history(filePath, modRoot)
	} else {
		evidence = BaselineEvidence{Status: "unavailable"}
	}
	if !compiled.RuntimeValuesEqual(&modRoot, baseRoot) {
		contribution.Baselines = append(contribution.Baselines, BaselineInfo{
			ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
			Evidence: evidence,
		})
	}
	if modRoot.Kind != kv3.KindObject {
		return nil, errCompiled(fmt.Sprintf("%s:%s:%s root is not an object", source.ModID, source.SourceVPK, filePath))
	}
	if modEncoding.Children.Kind != kv3.ChildrenObject {
		return nil, errCompiled(fmt.Sprintf("%s:%s:%s encoding root is not an object", source.ModID, source.SourceVPK, filePath))
	}
	modRows := modRoot.Object
	modEncodings := modEncoding.Children.Object
	enumCompatibility := LearnEnumCompatibility(baseRows, baseEncodings, modRows)
	heroRebaser := NewHeroRebaser(baseRows, baseEncodings, modRows, baseline)

	for _, modRow := range modRows {
		rowName := modRow.Key
		original := cloneValue(&modRow.Value)
		value := cloneValue(&modRow.Value)
		rowEncodingPtr, ok := compiledEncodingGet(modEncodings, rowName)
		if !ok {
			return nil, errCompiled(fmt.Sprintf("%s:%s:%s has no encoding for row %s", source.ModID, source.SourceVPK, filePath, rowName))
		}
		rowEncoding := cloneEncoding(rowEncodingPtr)
		vanilla, hasVanilla := compiledObjectGet(baseRows, rowName)
		if !hasVanilla && baseline != nil && baseline.RowMatches(rowName, original) {
			if _, err := enumCompatibility.Normalize(&value, &rowEncoding); err != nil {
				return nil, err
			}
			rebased, err := RebaseInherited(&value, &rowEncoding, baseRows, baseEncodings,
				modRows, modEncodings, baseline, enumCompatibility)
			if err != nil {
				return nil, err
			}
			if rebased.Kind == InheritedRebased {
				value = rebased.Value
				rowEncoding = rebased.Encoding
			}
			key := asciiLower(rowName)
			contribution.RetiredRows[key] = append(contribution.RetiredRows[key], CandidateValue{
				Candidate: Candidate{ModID: source.ModID, SourceVPK: source.SourceVPK, Priority: source.Priority},
				Value:     value,
				Encoding:  rowEncoding,
			})
			contribution.IgnoredVanillaRows++
			continue
		}
		if filePath == HeroesVDataPath && !hasVanilla {
			rebased, rebasedEncoding, ok, err := heroRebaser.Rebase(&value, &rowEncoding)
			if err != nil {
				return nil, err
			}
			if ok {
				value = rebased
				rowEncoding = rebasedEncoding
			}
			if err := heroRebaser.RestoreDevelopmentState(&value, &rowEncoding); err != nil {
				return nil, err
			}
			if _, hadState := stringField(&original, developmentState); !hadState {
				if state, hasState := stringField(&value, developmentState); hasState {
					contribution.RowRepairs = append(contribution.RowRepairs, DataRepair{
						ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
						RowName: rowName, Kind: RepairHeroDevelopmentState, Detail: state,
					})
				}
			}
		}
		if _, err := enumCompatibility.Normalize(&value, &rowEncoding); err != nil {
			return nil, err
		}
		if filePath == HeroesVDataPath {
			removed, err := enumMaps.Normalize(&value, &rowEncoding)
			if err != nil {
				return nil, err
			}
			for _, key := range removed {
				fieldPath := enumMapField + "." + key
				contribution.RowRepairs = append(contribution.RowRepairs, DataRepair{
					ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
					RowName: rowName, Kind: RepairEnumMapEntry, Detail: fieldPath,
				})
				if !originalMapValueZero(&original, key) {
					contribution.RowWarnings = append(contribution.RowWarnings, DataWarning{
						ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
						RowName: rowName, Kind: WarningOmittedEnumMapEntry,
						FieldPath: fieldPath, Reason: ReasonUnrecognizedClassOrEnum,
						Detail: "Omitted value; no mapping was proven in the current game table",
					})
				}
			}
		}
		var unrebased *Unrebased
		if filePath != HeroesVDataPath && !hasVanilla && baseline != nil {
			rebased, err := RebaseInherited(&value, &rowEncoding, baseRows, baseEncodings,
				modRows, modEncodings, baseline, enumCompatibility)
			if err != nil {
				return nil, err
			}
			switch rebased.Kind {
			case InheritedRebased:
				if !compiled.RuntimeValuesEqual(&value, &rebased.Value) || !encodingEqual(&rowEncoding, &rebased.Encoding) {
					unrebased = &Unrebased{Value: value, Encoding: rowEncoding}
					contribution.RowRepairs = append(contribution.RowRepairs, DataRepair{
						ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
						RowName: rowName, Kind: RepairInheritedDefinition,
					})
				}
				value = rebased.Value
				rowEncoding = rebased.Encoding
			case InheritedAmbiguous:
				contribution.RowWarnings = append(contribution.RowWarnings, DataWarning{
					ModID: source.ModID, SourceVPK: source.SourceVPK, FilePath: filePath,
					RowName: rowName, Kind: WarningUnverifiedInheritance,
					FieldPath: "_base / _multibase", Reason: ReasonAmbiguousTemplates,
				})
			}
		}
		var vanillaEncoding *kv3.Encoding
		if hasVanilla {
			vanillaEncoding, _ = compiledEncodingGet(baseEncodings, rowName)
		}
		if hasVanilla && compiled.RuntimeValuesEqual(vanilla, &value) {
			contribution.IgnoredVanillaRows++
			continue
		}
		var patches []compiled.Patch
		if hasVanilla {
			switch {
			case baseline != nil && baseline.ContainsRow(rowName):
				var fields []compiled.Patch
				if err := compiled.CollectFields(&value, &rowEncoding, nil, &fields); err != nil {
					return nil, err
				}
				for _, patch := range fields {
					if baseline.FieldMatches(rowName, patch.Path, patch.Value) {
						continue
					}
					if current, ok := compiled.ValueAtPath(vanilla, patch.Path); ok &&
						compiled.RuntimeValuesEqual(current, &patch.Value) {
						continue
					}
					patches = append(patches, patch)
				}
			default:
				if err := compiled.CollectPatches(vanilla, &value, &rowEncoding, nil, &patches); err != nil {
					return nil, err
				}
			}
		} else {
			patches = []compiled.Patch{{Value: value, Encoding: rowEncoding}}
		}
		if hasVanilla && vanillaEncoding != nil {
			for index := range patches {
				promoted, err := compiled.PromotePatch(vanilla, vanillaEncoding, &value, &rowEncoding, patches[index])
				if err != nil {
					return nil, err
				}
				patches[index] = promoted
			}
		}
		if len(patches) == 0 {
			contribution.IgnoredVanillaRows++
			continue
		}
		distinct := make(map[string]compiled.Patch)
		var order []string
		for _, patch := range patches {
			key := conflictKey(filePath, rowName, patch.Path)
			if _, exists := distinct[key]; !exists {
				distinct[key] = patch
				order = append(order, key)
			}
		}
		for _, key := range order {
			patch := distinct[key]
			group := contribution.Groups[key]
			if group == nil {
				group = &Group{
					Key: key, FilePath: filePath, RowName: rowName,
					Path: patch.Path, ExistedInVanilla: hasVanilla,
				}
				contribution.Groups[key] = group
			}
			group.Candidates = append(group.Candidates, CandidateValue{
				Candidate: Candidate{ModID: source.ModID, SourceVPK: source.SourceVPK, Priority: source.Priority},
				Value:     patch.Value,
				Encoding:  patch.Encoding,
				Unrebased: unrebased,
			})
		}
	}
	return contribution, nil
}

func conflictKey(filePath, rowName string, path []kv3.Seg) string {
	var builder strings.Builder
	builder.WriteString("compiled\x1f")
	builder.WriteString(filePath)
	builder.WriteString("\x1f")
	builder.WriteString(asciiLower(rowName))
	builder.WriteString("\x1f")
	for index, segment := range path {
		if index > 0 {
			builder.WriteByte('/')
		}
		if segment.Kind == kv3.SegIndex {
			fmt.Fprintf(&builder, "i:%d", segment.Index)
			continue
		}
		fmt.Fprintf(&builder, "k:%d:%s", len(segment.Key), segment.Key)
	}
	return builder.String()
}

// omitsGameContent reports game rows or object fields the authored copy lacks.
// Arrays are authored as a whole and are not compared.
func omitsGameContent(game, authored *kv3.Value) bool {
	if game.Kind != kv3.KindObject || authored.Kind != kv3.KindObject {
		return false
	}
	for _, field := range game.Object {
		if strings.EqualFold(field.Key, "_editor") || strings.EqualFold(field.Key, "_include") {
			continue
		}
		authoredValue, ok := compiledObjectGet(authored.Object, field.Key)
		if !ok || omitsGameContent(&field.Value, authoredValue) {
			return true
		}
	}
	return false
}

func originalMapValueZero(original *kv3.Value, key string) bool {
	if original.Kind != kv3.KindObject {
		return true
	}
	field, ok := compiledObjectGet(original.Object, enumMapField)
	if !ok || field.Kind != kv3.KindObject {
		return true
	}
	value, ok := compiledObjectGet(field.Object, key)
	if !ok {
		return true
	}
	switch value.Kind {
	case kv3.KindDouble:
		return value.Double == 0
	case kv3.KindInt:
		return value.Int == 0
	case kv3.KindUInt:
		return value.UInt == 0
	}
	return false
}

// ChooseWinner applies a user choice to one conflict group.
func ChooseWinner(group *Group, resolution *Resolution) (*CandidateValue, error) {
	if resolution == nil {
		if len(group.Candidates) == 0 {
			return nil, nil
		}
		return &group.Candidates[0], nil
	}
	if resolution.UseVanilla {
		return nil, nil
	}
	if resolution.WinnerModID == nil {
		if len(group.Candidates) == 0 {
			return nil, nil
		}
		return &group.Candidates[0], nil
	}
	for index := range group.Candidates {
		candidate := &group.Candidates[index]
		if candidate.Candidate.ModID != *resolution.WinnerModID {
			continue
		}
		if resolution.WinnerSourceVPK != nil && candidate.Candidate.SourceVPK != *resolution.WinnerSourceVPK {
			continue
		}
		return candidate, nil
	}
	return nil, fmt.Errorf("Compatibility choice for %s:%s no longer matches an enabled mod. Reopen compatibility review and choose again", group.FilePath, group.RowName)
}

// Merge applies the chosen candidate for every group to the current game table.
func (t *Table) Merge(resolutions []Resolution) (*Merged, error) {
	byKey := make(map[string]*Resolution, len(resolutions))
	for index := range resolutions {
		byKey[resolutions[index].ConflictKey] = &resolutions[index]
	}
	root := cloneValue(&t.BaseRoot)
	encoding := cloneEncoding(&t.BaseEncoding)
	if root.Kind != kv3.KindObject {
		return nil, errCompiled("Base " + t.FilePath + " root is not an object")
	}
	if encoding.Children.Kind != kv3.ChildrenObject {
		return nil, errCompiled("Base " + t.FilePath + " encoding root is not an object")
	}
	applied := make(map[string]struct{})
	origins := make(map[string][]Origin)
	var addedHeroes []heroIDRequest
	for _, key := range slices.Sorted(maps.Keys(t.Groups)) {
		group := t.Groups[key]
		winner, err := ChooseWinner(group, byKey[group.Key])
		if err != nil {
			return nil, err
		}
		if winner == nil {
			continue
		}
		if len(group.Path) == 0 {
			if err := compiled.ObjectSet(&root, group.RowName, winner.Value); err != nil {
				return nil, err
			}
			if err := compiled.EncodingSet(&encoding, group.RowName, winner.Encoding); err != nil {
				return nil, err
			}
		} else {
			row, ok := compiledObjectGet(root.Object, group.RowName)
			if !ok {
				return nil, errCompiled(fmt.Sprintf("Base %s has no row %s required by a field patch", t.FilePath, group.RowName))
			}
			if err := compiled.SetValueAtPath(row, group.Path, winner.Value); err != nil {
				return nil, err
			}
			rowEncoding, ok := compiledEncodingGet(encoding.Children.Object, group.RowName)
			if !ok {
				return nil, errCompiled(fmt.Sprintf("Base %s has no encoding for row %s required by a field patch", t.FilePath, group.RowName))
			}
			if err := compiled.SetEncodingAtPath(rowEncoding, group.Path, winner.Encoding); err != nil {
				return nil, err
			}
		}
		if t.FilePath == HeroesVDataPath && !group.ExistedInVanilla {
			if requested, ok := compiledHeroID(&winner.Value); ok {
				addedHeroes = append(addedHeroes, heroIDRequest{
					RowName: group.RowName, ModID: winner.Candidate.ModID,
					Priority: winner.Candidate.Priority, RequestedID: requested,
				})
			}
		}
		lower := asciiLower(group.RowName)
		origins[lower] = append(origins[lower], Origin{
			Path:      group.Path,
			Candidate: winner.Candidate,
		})
		applied[lower] = struct{}{}
	}
	if t.FilePath == HeroesVDataPath {
		reassignments, err := PlanUniqueAddedHeroIDs(&t.BaseRoot, addedHeroes)
		if err != nil {
			return nil, err
		}
		for _, reassignment := range reassignments {
			row, ok := compiledObjectGet(root.Object, reassignment.RowName)
			if !ok {
				continue
			}
			if err := setCompiledHeroID(row, reassignment.AssignedID); err != nil {
				return nil, err
			}
		}
	}
	return &Merged{Root: root, Encoding: encoding, AppliedRows: applied, Origins: origins}, nil
}

// HeroIDReassignments reports the ID changes an added hero would need.
func HeroIDReassignments(tables []*Table, resolutions []Resolution) ([]HeroIDReassignment, error) {
	byKey := make(map[string]*Resolution, len(resolutions))
	for index := range resolutions {
		byKey[resolutions[index].ConflictKey] = &resolutions[index]
	}
	var reassignments []HeroIDReassignment
	for _, table := range tables {
		if table.FilePath != HeroesVDataPath {
			continue
		}
		var requests []heroIDRequest
		for _, key := range slices.Sorted(maps.Keys(table.Groups)) {
			group := table.Groups[key]
			if group.ExistedInVanilla {
				continue
			}
			winner, err := ChooseWinner(group, byKey[group.Key])
			if err != nil {
				return nil, err
			}
			if winner == nil {
				continue
			}
			if requested, ok := compiledHeroID(&winner.Value); ok {
				requests = append(requests, heroIDRequest{
					RowName: group.RowName, ModID: winner.Candidate.ModID,
					Priority: winner.Candidate.Priority, RequestedID: requested,
				})
			}
		}
		planned, err := PlanUniqueAddedHeroIDs(&table.BaseRoot, requests)
		if err != nil {
			return nil, err
		}
		reassignments = append(reassignments, planned...)
	}
	return reassignments, nil
}

type heroIDRequest struct {
	RowName     string
	ModID       string
	Priority    int
	RequestedID int64
}

// PlanUniqueAddedHeroIDs assigns new IDs to added heroes whose requested ID is
// already used by the current game or another added hero.
func PlanUniqueAddedHeroIDs(baseRoot *kv3.Value, requests []heroIDRequest) ([]HeroIDReassignment, error) {
	if baseRoot.Kind != kv3.KindObject {
		return nil, errCompiled("Base heroes.vdata root is not an object")
	}
	used := make(map[int64]struct{})
	for _, row := range baseRoot.Object {
		if id, ok := compiledHeroID(&row.Value); ok {
			used[id] = struct{}{}
		}
	}
	highest := int64(0)
	for _, request := range requests {
		if request.RequestedID > highest {
			highest = request.RequestedID
		}
	}
	for id := range used {
		if id > highest {
			highest = id
		}
	}
	if highest == math.MaxInt64 {
		return nil, errCompiled("Cannot allocate another hero ID because the ID range is exhausted")
	}
	next := highest + 1
	slices.SortStableFunc(requests, func(left, right heroIDRequest) int {
		if left.Priority != right.Priority {
			return left.Priority - right.Priority
		}
		return strings.Compare(asciiLower(left.RowName), asciiLower(right.RowName))
	})
	var reassignments []HeroIDReassignment
	for _, request := range requests {
		if _, taken := used[request.RequestedID]; !taken {
			used[request.RequestedID] = struct{}{}
			continue
		}
		for {
			if _, taken := used[next]; !taken {
				break
			}
			if next == math.MaxInt64 {
				return nil, errCompiled("Cannot allocate another hero ID because the ID range is exhausted")
			}
			next++
		}
		reassignments = append(reassignments, HeroIDReassignment{
			RowName: request.RowName, ModID: request.ModID,
			RequestedID: request.RequestedID, AssignedID: next,
		})
		used[next] = struct{}{}
		if next == math.MaxInt64 {
			return nil, errCompiled("Cannot allocate another hero ID because the ID range is exhausted")
		}
		next++
	}
	return reassignments, nil
}

func compiledHeroID(value *kv3.Value) (int64, bool) {
	if value.Kind != kv3.KindObject {
		return 0, false
	}
	field, ok := compiledObjectGet(value.Object, "m_HeroID")
	if !ok {
		return 0, false
	}
	switch field.Kind {
	case kv3.KindInt:
		return field.Int, true
	case kv3.KindUInt:
		if field.UInt > math.MaxInt64 {
			return 0, false
		}
		return int64(field.UInt), true
	}
	return 0, false
}

func setCompiledHeroID(value *kv3.Value, id int64) error {
	if value.Kind != kv3.KindObject {
		return errCompiled("Hero row is not an object")
	}
	field, ok := compiledObjectGet(value.Object, "m_HeroID")
	if !ok {
		return errCompiled("Hero row has no m_HeroID")
	}
	switch field.Kind {
	case kv3.KindInt:
		field.Int = id
	case kv3.KindUInt:
		if id < 0 {
			return errCompiled("Hero ID cannot be negative")
		}
		field.UInt = uint64(id)
	default:
		return errCompiled("Hero row has a non-numeric m_HeroID")
	}
	return nil
}

// EncodeMerged rebuilds the table resource from the merged values and verifies
// that only the applied rows changed.
func (t *Table) EncodeMerged(merged *Merged) ([]byte, int, error) {
	if t.Files == 0 && len(merged.AppliedRows) == 0 {
		return nil, 0, nil
	}
	if len(merged.AppliedRows) == 0 {
		// The original mod still overrides this table through the addons search
		// path. Emit current bytes even when all stale data was ignored.
		return t.BaseBytes, 0, nil
	}
	newData, err := kv3.EncodePreserving(&merged.Root, &merged.Encoding, t.Format)
	if err != nil {
		return nil, 0, errCompiled(fmt.Sprintf("Failed to encode merged %s while preserving Source 2 types: %v", t.FilePath, err))
	}
	base, err := resource.Parse(t.BaseBytes)
	if err != nil {
		return nil, 0, errCompiled("Failed to parse base compiled data: " + err.Error())
	}
	rebuilt, err := base.RebuildWithDataPreserving(newData)
	if err != nil {
		return nil, 0, errCompiled("Failed to rebuild compiled data: " + err.Error())
	}
	verifiedFormat, verifiedRoot, verifiedEncoding, err := compiled.Decode(rebuilt, "rebuilt overlay")
	if err != nil {
		return nil, 0, err
	}
	if verifiedFormat != t.Format ||
		!compiled.RuntimeValuesEqual(&verifiedRoot, &merged.Root) ||
		!kv3.EncodingPreserved(&merged.Root, &merged.Encoding, &verifiedEncoding) {
		return nil, 0, errCompiled("Rebuilt " + t.FilePath + " changed its values or Source 2 wire types")
	}
	if verifiedRoot.Kind != kv3.KindObject {
		return nil, 0, errCompiled("Rebuilt " + t.FilePath + " root is not an object")
	}
	for name := range merged.AppliedRows {
		expected, ok := compiledObjectGet(merged.Root.Object, name)
		if !ok {
			continue
		}
		actual, ok := compiledObjectGet(verifiedRoot.Object, name)
		if !ok || !compiled.RuntimeValuesEqual(actual, expected) {
			return nil, 0, errCompiled("Rebuilt " + t.FilePath + " lost row " + name)
		}
	}
	return rebuilt, len(merged.AppliedRows), nil
}
