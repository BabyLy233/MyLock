// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (localization
// scanning and merge) at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"fmt"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/localization"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// applyLocalization reports what a mod's localization files change. Text files
// are merged at profile level, so the repaired bytes are produced by
// FixLocalization rather than this per-mod analysis.
func applyLocalization(analysis *Analysis, modID string, modPaths map[string]struct{},
	modArchives map[string]*vpk.Archive, modVPKs map[string]string,
	game *source2.GameIndex) error {
	sources := make(map[string][]localization.Source)
	for path := range modPaths {
		if !localization.IsLocalizationPath(path) {
			continue
		}
		archive, ok := modArchives[path]
		if !ok {
			continue
		}
		bytes, err := archive.ReadLimited(path, maxModelBytes)
		if err != nil {
			return fmt.Errorf("reading %s from %s: %w", path, modVPKs[path], err)
		}
		sources[path] = append(sources[path], localization.Source{
			ModID: modID, SourceVPK: modVPKs[path], FilePath: path, Bytes: bytes,
		})
	}
	if len(sources) == 0 {
		return nil
	}
	merger, err := localization.BuildLocalization(sources, gameBytes(game), localization.EmbeddedHistory())
	if err != nil {
		return fmt.Errorf("reading game localization: %w", err)
	}
	analysis.AssetsChecked += len(sources)
	analysis.DataRepairs = append(analysis.DataRepairs, merger.Repairs...)
	for _, repair := range merger.Repairs {
		analysis.Repairs = append(analysis.Repairs, Repair{
			ModID: repair.ModID, SourceVPK: repair.SourceVPK,
			FilePath: repair.FilePath, Kind: RepairKind(string(repair.Kind)),
		})
	}
	analysis.DataWarnings = append(analysis.DataWarnings, localizationWarnings(merger)...)
	return nil
}

// FixLocalization merges every enabled mod's localization files against the
// current game files. The merged text is emitted into the overlay. A path in
// protected is left as the game loads it.
func FixLocalization(inputs []DataInput, game *source2.GameIndex, protected map[string]struct{}) (DataResult, error) {
	result := DataResult{Files: make(map[string][]byte)}
	sources := make(map[string][]localization.Source)
	for priority, input := range inputs {
		for _, vpkPath := range input.VPKs {
			archive, err := vpk.Open(vpkPath)
			if err != nil {
				return result, fmt.Errorf("reading %s: %w", vpkPath, err)
			}
			for _, entry := range archive.Entries {
				path := camera.NormalizePath(entry.FullPath)
				if !localization.IsLocalizationPath(path) {
					continue
				}
				if _, kept := protected[path]; kept {
					continue
				}
				bytes, err := archive.ReadLimited(entry.FullPath, maxModelBytes)
				if err != nil {
					return result, fmt.Errorf("reading %s from %s: %w", entry.FullPath, vpkPath, err)
				}
				sources[path] = append(sources[path], localization.Source{
					ModID: input.ModID, SourceVPK: vpkPath, Priority: priority,
					FilePath: entry.FullPath, Bytes: bytes,
				})
			}
		}
	}
	if len(sources) == 0 {
		return result, nil
	}
	merger, err := localization.BuildLocalization(sources, gameBytes(game), localization.EmbeddedHistory())
	if err != nil {
		return result, fmt.Errorf("reading game localization: %w", err)
	}
	files, _, err := merger.Render(nil)
	if err != nil {
		return result, fmt.Errorf("merging localization: %w", err)
	}
	result.Files = files
	result.Repairs = append(result.Repairs, merger.Repairs...)
	result.Warnings = append(result.Warnings, localizationWarnings(merger)...)
	return result, nil
}

func gameBytes(game *source2.GameIndex) func(string) ([]byte, bool) {
	return func(name string) ([]byte, bool) {
		if game == nil {
			return nil, false
		}
		return game.GameBytes(name)
	}
}

func localizationWarnings(merger *localization.Merger) []vdata.DataWarning {
	warnings := make([]vdata.DataWarning, 0, len(merger.ParseWarnings))
	for _, warning := range merger.ParseWarnings {
		detail := warning.Error
		if len(warning.SkippedLines) > 0 && detail == "" {
			detail = fmt.Sprintf("skipped lines %v", warning.SkippedLines)
		}
		warnings = append(warnings, vdata.DataWarning{
			ModID: warning.ModID, FilePath: warning.FilePath,
			Kind:   vdata.WarningUnreadableData,
			Reason: vdata.ReasonUnreadableResource,
			Detail: detail,
		})
	}
	return warnings
}
