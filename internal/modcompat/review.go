// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs
// (compiled_data_analysis and the localization conflict scan) at
// 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/localization"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
)

// LocalizationCandidate is one mod's value for a conflicting token.
type LocalizationCandidate struct {
	ModID     string
	SourceVPK string
	Value     string
	Priority  int
}

// LocalizationConflict is a token every contributor rewrites differently.
// The player has to choose the value the game shows; the key matches the
// resolution the merger expects.
type LocalizationConflict struct {
	Key          string
	FilePath     string
	Token        string
	VanillaValue *string
	Candidates   []LocalizationCandidate
}

// CompiledDataCandidate identifies the archive of one compiled-data contender.
type CompiledDataCandidate struct {
	ModID     string
	SourceVPK string
	Priority  int
}

// CompiledDataConflict is a row or field every contributor patches differently.
type CompiledDataConflict struct {
	Key              string
	FilePath         string
	RowName          string
	FieldPath        string
	ExistedInVanilla bool
	Candidates       []CompiledDataCandidate
}

// localizationConflicts lists the token groups whose candidates disagree. A
// group all contributors agree on needs no review.
func localizationConflicts(merger *localization.Merger) []LocalizationConflict {
	var conflicts []LocalizationConflict
	for _, key := range slices.Sorted(maps.Keys(merger.Groups)) {
		group := merger.Groups[key]
		distinct := make(map[string]struct{}, len(group.Candidates))
		for _, candidate := range group.Candidates {
			distinct[candidate.Value] = struct{}{}
		}
		if len(distinct) < 2 {
			continue
		}
		conflict := LocalizationConflict{
			Key: group.Key, FilePath: group.FilePath, Token: group.Token,
		}
		if group.VanillaValue != nil {
			value := *group.VanillaValue
			conflict.VanillaValue = &value
		}
		for _, candidate := range group.Candidates {
			conflict.Candidates = append(conflict.Candidates, LocalizationCandidate{
				ModID: candidate.ModID, SourceVPK: candidate.SourceVPK,
				Value: candidate.Value, Priority: candidate.Priority,
			})
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts
}

// compiledDataConflicts lists the row or field patches whose values disagree.
// Values are compared at runtime so differing wire encodings of the same value
// do not ask the player to choose between identical results.
func compiledDataConflicts(tables []*vdata.Table) []CompiledDataConflict {
	var conflicts []CompiledDataConflict
	for _, table := range tables {
		for _, key := range slices.Sorted(maps.Keys(table.Groups)) {
			group := table.Groups[key]
			var distinct []kv3.Value
			for index := range group.Candidates {
				value := &group.Candidates[index].Value
				duplicate := false
				for distinctIndex := range distinct {
					if compiled.RuntimeValuesEqual(&distinct[distinctIndex], value) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					distinct = append(distinct, *value)
				}
			}
			if len(distinct) < 2 {
				continue
			}
			conflict := CompiledDataConflict{
				Key: group.Key, FilePath: table.FilePath, RowName: group.RowName,
				FieldPath:        compiledPathLabel(group.Path),
				ExistedInVanilla: group.ExistedInVanilla,
			}
			for _, candidate := range group.Candidates {
				conflict.Candidates = append(conflict.Candidates, CompiledDataCandidate{
					ModID: candidate.Candidate.ModID, SourceVPK: candidate.Candidate.SourceVPK,
					Priority: candidate.Candidate.Priority,
				})
			}
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts
}

// compiledPathLabel renders a compiled-data path the way the review shows it:
// object members joined by dots, array elements in brackets.
func compiledPathLabel(path []kv3.Seg) string {
	var label strings.Builder
	for _, segment := range path {
		if segment.Kind == kv3.SegIndex {
			label.WriteByte('[')
			label.WriteString(strconv.FormatUint(segment.Index, 10))
			label.WriteByte(']')
			continue
		}
		if label.Len() > 0 {
			label.WriteByte('.')
		}
		label.WriteString(segment.Key)
	}
	return label.String()
}