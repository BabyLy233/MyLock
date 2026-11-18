// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (localization
// merge) at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package localization

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
)

const staleSnapshotChangeThreshold = 50

var protectedPaths = []string{
	"resource/localization/citadel_gc_hero_names/citadel_gc_hero_names_english.txt",
	"resource/localization/citadel_heroes/citadel_heroes_english.txt",
}

// IsLocalizationPath reports whether a path is a localization text file.
func IsLocalizationPath(filePath string) bool {
	normalized := NormalizePath(filePath)
	return strings.HasPrefix(normalized, "resource/localization/") && strings.HasSuffix(normalized, ".txt")
}

// IsProtectedPath reports whether stale historical tokens must be dropped from
// this file.
func IsProtectedPath(filePath string) bool {
	normalized := NormalizePath(filePath)
	return slices.Contains(protectedPaths, normalized)
}

// Source is one mod's copy of a localization file.
type Source struct {
	ModID     string
	SourceVPK string
	Priority  int
	// FilePath is the authored entry path used in findings; it may differ in
	// case from the normalized path the merge keys on.
	FilePath string
	Bytes    []byte
}

// Candidate is one mod's value for a token.
type Candidate struct {
	ModID     string
	SourceVPK string
	Value     string
	Priority  int
}

// Group is one token authored by at least one mod with a value that differs
// from the current game file.
type Group struct {
	Key          string
	FilePath     string
	Token        string
	VanillaValue *string
	Candidates   []Candidate
}

// ParseWarning reports a file that could not be parsed, or lines the fallback
// parser skipped.
type ParseWarning struct {
	ModID        string
	FilePath     string
	SkippedLines []int
	Error        string
}

// SnapshotWarning reports a mod file that looks like a stale full copy of a
// game table: it changed most of its tokens.
type SnapshotWarning struct {
	ModID         string
	FilePath      string
	ChangedTokens int
	TotalTokens   int
}

// Merger is the collected localization input for one profile.
type Merger struct {
	Bases                   map[string]*File
	Groups                  map[string]*Group
	Seeded                  map[string]struct{}
	Repairs                 []vdata.DataRepair
	ParseWarnings           []ParseWarning
	SnapshotWarnings        []SnapshotWarning
	LocalizationFiles       int
	IgnoredVanillaTokens    int
	IgnoredHistoricalTokens int
}

// BuildLocalization parses every mod copy and compares it with the current
// game file. Sources may contain several mods per path in load order.
func BuildLocalization(sources map[string][]Source, game func(string) ([]byte, bool),
	history *HistoryIndex) (*Merger, error) {
	merger := &Merger{
		Bases:  make(map[string]*File),
		Groups: make(map[string]*Group),
		Seeded: make(map[string]struct{}),
	}
	gameText := make(map[string]struct{})
	readText := make(map[string]struct{})
	unreadableText := make(map[string]struct{})
	for _, path := range slices.Sorted(maps.Keys(sources)) {
		base := &File{Language: InferLanguage(path), Tokens: make(map[string]string)}
		if game != nil {
			if bytes, ok := game(path); ok {
				gameText[path] = struct{}{}
				parsed, err := ParseBytes(bytes, "game:"+path)
				if err != nil {
					return nil, err
				}
				base = &parsed.File
			}
		}
		merger.Bases[path] = base
		baseTokens := tokenIndex(base)
		for _, source := range sources[path] {
			if _, skip := unreadableText[path]; skip {
				continue
			}
			parsed, err := ParseBytes(source.Bytes,
				fmt.Sprintf("%s:%s", source.ModID, source.FilePath))
			if err != nil {
				if _, read := readText[path]; !read {
					unreadableText[path] = struct{}{}
				}
				merger.ParseWarnings = append(merger.ParseWarnings, ParseWarning{
					ModID: source.ModID, FilePath: slashPath(source.FilePath), Error: err.Error(),
				})
				continue
			}
			readText[path] = struct{}{}
			if len(parsed.SkippedLines) > 0 {
				merger.ParseWarnings = append(merger.ParseWarnings, ParseWarning{
					ModID: source.ModID, FilePath: slashPath(source.FilePath),
					SkippedLines: parsed.SkippedLines,
				})
			}
			localization := parsed.File
			merger.LocalizationFiles++
			totalTokens := len(localization.Tokens)
			modTokens := make(map[string]struct{}, totalTokens)
			for token := range localization.Tokens {
				modTokens[asciiLower(token)] = struct{}{}
			}
			omitsGameText := false
			if _, isGameText := gameText[path]; isGameText {
				for token := range baseTokens {
					if _, ok := modTokens[token]; !ok {
						omitsGameText = true
						break
					}
				}
			}
			changedInFile := 0
			staleInFile := 0
			for _, token := range localization.Keys() {
				value := localization.Tokens[token]
				vanilla, hasVanilla := baseTokens[asciiLower(token)]
				if hasVanilla && vanilla == value {
					merger.IgnoredVanillaTokens++
					continue
				}
				if IsProtectedPath(path) && history.Contains(path, token, value) {
					merger.IgnoredHistoricalTokens++
					staleInFile++
					continue
				}
				changedInFile++
				key := conflictKey(path, token)
				group := merger.Groups[key]
				if group == nil {
					group = &Group{
						Key: key, FilePath: slashPath(source.FilePath), Token: token,
					}
					if hasVanilla {
						vanillaValue := vanilla
						group.VanillaValue = &vanillaValue
					}
					merger.Groups[key] = group
				}
				group.Candidates = append(group.Candidates, Candidate{
					ModID: source.ModID, SourceVPK: source.SourceVPK,
					Value: value, Priority: source.Priority,
				})
			}
			if _, isGameText := gameText[path]; isGameText {
				merger.Seeded[path] = struct{}{}
				if omitsGameText || staleInFile > 0 {
					merger.Repairs = append(merger.Repairs, vdata.DataRepair{
						ModID: source.ModID, SourceVPK: source.SourceVPK,
						FilePath: slashPath(source.FilePath), Kind: vdata.RepairRestoredGameText,
					})
				}
			}
			if changedInFile >= staleSnapshotChangeThreshold &&
				len(base.Tokens) > 0 && totalTokens >= staleSnapshotChangeThreshold {
				merger.SnapshotWarnings = append(merger.SnapshotWarnings, SnapshotWarning{
					ModID: source.ModID, FilePath: slashPath(source.FilePath),
					ChangedTokens: changedInFile, TotalTokens: totalTokens,
				})
			}
		}
	}
	return merger, nil
}

// ChooseWinner applies a review choice to one token group.
func ChooseWinner(group *Group, resolution *vdata.Resolution) (*Candidate, error) {
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
		if candidate.ModID != *resolution.WinnerModID {
			continue
		}
		if resolution.WinnerSourceVPK != nil && candidate.SourceVPK != *resolution.WinnerSourceVPK {
			continue
		}
		if resolution.WinnerValue != nil && candidate.Value != *resolution.WinnerValue {
			continue
		}
		return candidate, nil
	}
	return nil, fmt.Errorf("Compatibility choice for %s:%s no longer matches an enabled mod. Reopen compatibility review and choose again", group.FilePath, group.Token)
}

// Render produces the merged files. Every seeded game file is emitted so a
// mod copy cannot hide game tokens it lacks.
func (m *Merger) Render(resolutions []vdata.Resolution) (map[string][]byte, int, error) {
	byKey := make(map[string]*vdata.Resolution, len(resolutions))
	for index := range resolutions {
		byKey[resolutions[index].ConflictKey] = &resolutions[index]
	}
	outputFiles := make(map[string]*File)
	for path := range m.Seeded {
		if base, ok := m.Bases[path]; ok {
			copy := File{Language: base.Language, Tokens: make(map[string]string, len(base.Tokens))}
			maps.Copy(copy.Tokens, base.Tokens)
			outputFiles[path] = &copy
		}
	}
	applied := 0
	for _, key := range slices.Sorted(maps.Keys(m.Groups)) {
		group := m.Groups[key]
		winner, err := ChooseWinner(group, byKey[group.Key])
		if err != nil {
			return nil, 0, err
		}
		if winner == nil {
			continue
		}
		path := NormalizePath(group.FilePath)
		output := outputFiles[path]
		if output == nil {
			if base, ok := m.Bases[path]; ok {
				copy := File{Language: base.Language, Tokens: make(map[string]string, len(base.Tokens))}
				maps.Copy(copy.Tokens, base.Tokens)
				output = &copy
			} else {
				output = &File{Language: InferLanguage(group.FilePath), Tokens: make(map[string]string)}
			}
			outputFiles[path] = output
		}
		output.Set(group.Token, winner.Value)
		applied++
	}
	files := make(map[string][]byte, len(outputFiles))
	for path, file := range outputFiles {
		files[path] = Render(file)
	}
	return files, applied, nil
}

// TokenStats reports how many authored tokens changed or added game rows.
func (m *Merger) TokenStats() (changed, added int) {
	for _, group := range m.Groups {
		if group.VanillaValue != nil {
			changed++
		} else {
			added++
		}
	}
	return changed, added
}

func tokenIndex(file *File) map[string]string {
	index := make(map[string]string, len(file.Tokens))
	for _, key := range file.Keys() {
		lower := asciiLower(key)
		if _, exists := index[lower]; !exists {
			index[lower] = file.Tokens[key]
		}
	}
	return index
}

func conflictKey(filePath, token string) string {
	return filePath + "\x1f" + asciiLower(token)
}

func slashPath(filePath string) string {
	return replaceBackslashes(filePath)
}
