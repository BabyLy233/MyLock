// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (build_compiled_data)
// and localization_overlay/vdata_compatibility.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/history"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// IsDataTablePath reports whether a mod file participates in compiled-data
// merging. Localization text is a separate table family.
func IsDataTablePath(path string) bool {
	path = camera.NormalizePath(path)
	return strings.HasPrefix(path, "scripts/") && strings.HasSuffix(path, ".vdata_c")
}

// applyData merges a mod's VData tables with the current game tables. Data
// repairs never rewrite the mod file: the merged table is emitted into the
// overlay instead.
func applyData(analysis *Analysis, modID string, modPaths map[string]struct{},
	modArchives map[string]*vpk.Archive, modVPKs map[string]string,
	game *source2.GameIndex, scope Scope, mode Mode) error {
	if game == nil {
		return nil
	}
	type tableSource struct {
		fromMod bool
		vpkPath string
	}
	sources := make(map[string]tableSource)
	for path := range modPaths {
		if !IsDataTablePath(path) {
			continue
		}
		if _, ok := modArchives[path]; !ok {
			continue
		}
		sources[path] = tableSource{fromMod: true, vpkPath: modVPKs[path]}
	}
	// Hero references must resolve even when no mod overrides the abilities
	// table.
	if _, ok := sources[vdata.HeroesVDataPath]; ok {
		if _, exists := sources[vdata.AbilitiesPath]; !exists {
			sources[vdata.AbilitiesPath] = tableSource{}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	matcher := dataHistoryMatcher()
	var tables []*vdata.Table
	var paths []string
	for _, path := range slices.Sorted(maps.Keys(sources)) {
		base, ok := game.GameBytes(path)
		if !ok {
			// A mod-only compiled file is already visible through its own VPK;
			// there is no game table to merge into.
			continue
		}
		source := sources[path]
		var inputs []vdata.Source
		if source.fromMod {
			archive := modArchives[path]
			bytes, err := archive.ReadLimited(path, maxModelBytes)
			if err != nil {
				return fmt.Errorf("reading %s from %s: %w", path, source.vpkPath, err)
			}
			inputs = append(inputs, vdata.Source{
				ModID: modID, SourceVPK: source.vpkPath, Priority: 0, Bytes: bytes,
			})
		}
		table, err := vdata.BuildTable(path, base, inputs, matcher)
		if err != nil {
			return fmt.Errorf("reading game table %s: %w", path, err)
		}
		analysis.AssetsChecked++
		analysis.DataWarnings = append(analysis.DataWarnings, table.Unreadable...)
		if table.Shadowed {
			continue
		}
		tables = append(tables, table)
		paths = append(paths, path)
	}
	if len(tables) == 0 {
		return nil
	}
	prepared, err := vdata.Prepare(tables, nil)
	if err != nil {
		return fmt.Errorf("merging game data: %w", err)
	}
	analysis.DataRepairs = append(analysis.DataRepairs, prepared.Repairs...)
	analysis.DataWarnings = append(analysis.DataWarnings, prepared.Warnings...)
	for _, repair := range prepared.Repairs {
		analysis.Repairs = append(analysis.Repairs, Repair{
			ModID: repair.ModID, SourceVPK: repair.SourceVPK,
			FilePath: repair.FilePath, Kind: RepairKind(string(repair.Kind)),
		})
	}
	if mode != Fix || scope.ProfileData {
		// The profile-level merger owns the merged bytes; this analysis still
		// reports the repairs and warnings for its own mod.
		return nil
	}
	for index, table := range tables {
		bytes, _, err := table.EncodeMerged(prepared.Merged[index])
		if err != nil {
			return fmt.Errorf("rebuilding %s: %w", table.FilePath, err)
		}
		if bytes == nil {
			continue
		}
		analysis.Fixed[table.FilePath] = bytes
	}
	return nil
}

// dataHistoryMatcher adapts the shipped VData history to the data merger.
func dataHistoryMatcher() vdata.HistoryMatcher {
	index := history.Embedded()
	return func(filePath string, root kv3.Value) (vdata.BaselineMatch, vdata.BaselineEvidence) {
		match, evidence := index.MatchWithEvidence(filePath, root)
		out := vdata.BaselineEvidence{
			Status:         string(evidence.Status),
			BuildVersions:  evidence.BuildVersions,
			ComparableRows: evidence.ComparableRows,
			ChangedRows:    evidence.ChangedRows,
		}
		if match == nil {
			return nil, out
		}
		return match, out
	}
}

// DataInput is one enabled mod's archives in load order.
type DataInput struct {
	ModID string
	VPKs  []string
}

// DataResult is the profile-wide merged game data.
type DataResult struct {
	Files    map[string][]byte
	Repairs  []vdata.DataRepair
	Warnings []vdata.DataWarning
}

// FixData merges every enabled mod's VData tables against the current game
// tables. Tables are merged across mods because the game loads one table per
// path; the first enabled mod's value wins unless a repair proves otherwise.
func FixData(inputs []DataInput, game *source2.GameIndex) (DataResult, error) {
	result := DataResult{Files: make(map[string][]byte)}
	if game == nil {
		return result, nil
	}
	sources := make(map[string][]vdata.Source)
	heroes := false
	for priority, input := range inputs {
		for _, vpkPath := range input.VPKs {
			archive, err := vpk.Open(vpkPath)
			if err != nil {
				return result, fmt.Errorf("reading %s: %w", vpkPath, err)
			}
			for _, entry := range archive.Entries {
				path := camera.NormalizePath(entry.FullPath)
				if !IsDataTablePath(path) {
					continue
				}
				bytes, err := archive.ReadLimited(entry.FullPath, maxModelBytes)
				if err != nil {
					return result, fmt.Errorf("reading %s from %s: %w", entry.FullPath, vpkPath, err)
				}
				sources[path] = append(sources[path], vdata.Source{
					ModID: input.ModID, SourceVPK: vpkPath, Priority: priority, Bytes: bytes,
				})
				if path == vdata.HeroesVDataPath {
					heroes = true
				}
			}
		}
	}
	if heroes {
		if _, ok := sources[vdata.AbilitiesPath]; !ok {
			sources[vdata.AbilitiesPath] = nil
		}
	}
	if len(sources) == 0 {
		return result, nil
	}
	matcher := dataHistoryMatcher()
	var tables []*vdata.Table
	for _, path := range slices.Sorted(maps.Keys(sources)) {
		base, ok := game.GameBytes(path)
		if !ok {
			continue
		}
		table, err := vdata.BuildTable(path, base, sources[path], matcher)
		if err != nil {
			return result, fmt.Errorf("reading game table %s: %w", path, err)
		}
		result.Warnings = append(result.Warnings, table.Unreadable...)
		if table.Shadowed {
			continue
		}
		tables = append(tables, table)
	}
	if len(tables) == 0 {
		return result, nil
	}
	prepared, err := vdata.Prepare(tables, nil)
	if err != nil {
		return result, fmt.Errorf("merging game data: %w", err)
	}
	result.Repairs = append(result.Repairs, prepared.Repairs...)
	result.Warnings = append(result.Warnings, prepared.Warnings...)
	for index, table := range tables {
		bytes, _, err := table.EncodeMerged(prepared.Merged[index])
		if err != nil {
			return result, fmt.Errorf("rebuilding %s: %w", table.FilePath, err)
		}
		if bytes != nil {
			result.Files[table.FilePath] = bytes
		}
	}
	return result, nil
}
