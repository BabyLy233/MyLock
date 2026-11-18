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
	// Learn the game tables this analysis will read before they are matched:
	// the author's table may have been written against the build being
	// observed, and the next game update has to be able to tell. This happens
	// even for a mod that carries only models: camera models make the hero
	// registry and abilities worth knowing.
	sourcePaths := make(map[string]struct{}, len(sources))
	for path := range sources {
		sourcePaths[path] = struct{}{}
	}
	for _, path := range learningPaths(sourcePaths, modPaths) {
		base, ok := game.GameBytes(path)
		if !ok {
			continue
		}
		if _, err := scope.History.Record(path, base); err != nil {
			return fmt.Errorf("reading game table %s: %w", path, err)
		}
	}
	if len(sources) == 0 {
		return nil
	}
	matcher := scope.History.matcher()
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

// learningPaths returns the game tables worth observing. Besides the tables a
// mod merges, a mod that carries camera models makes the hero registry and the
// abilities it references worth learning, even when the mod does not override
// them: a later update still has to recognise what the mod was built against.
func learningPaths(sourcePaths, modPaths map[string]struct{}) []string {
	paths := make(map[string]struct{}, len(sourcePaths)+2)
	maps.Copy(paths, sourcePaths)
	for path := range modPaths {
		if camera.IsModelPath(path) {
			paths[vdata.HeroesVDataPath] = struct{}{}
			break
		}
	}
	if _, ok := paths[vdata.HeroesVDataPath]; ok {
		paths[vdata.AbilitiesPath] = struct{}{}
	}
	if len(paths) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(paths))
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
// The game tables it reads are learned into hist for later game updates.
// A path in protected is left as the game loads it.
func FixData(inputs []DataInput, game *source2.GameIndex, hist *History, protected map[string]struct{}) (DataResult, error) {
	result := DataResult{Files: make(map[string][]byte)}
	if game == nil {
		return result, nil
	}
	sources := make(map[string][]vdata.Source)
	modPaths := make(map[string]struct{})
	heroes := false
	for priority, input := range inputs {
		for _, vpkPath := range input.VPKs {
			archive, err := vpk.Open(vpkPath)
			if err != nil {
				return result, fmt.Errorf("reading %s: %w", vpkPath, err)
			}
			for _, entry := range archive.Entries {
				path := camera.NormalizePath(entry.FullPath)
				modPaths[path] = struct{}{}
				if !IsDataTablePath(path) {
					continue
				}
				if _, kept := protected[path]; kept {
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
	sourcePaths := make(map[string]struct{}, len(sources))
	for path := range sources {
		sourcePaths[path] = struct{}{}
	}
	for _, path := range learningPaths(sourcePaths, modPaths) {
		base, ok := game.GameBytes(path)
		if !ok {
			continue
		}
		if _, err := hist.Record(path, base); err != nil {
			return result, fmt.Errorf("reading game table %s: %w", path, err)
		}
	}
	if len(sources) == 0 {
		return result, nil
	}
	matcher := hist.matcher()
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
