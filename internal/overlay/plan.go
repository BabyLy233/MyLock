// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/manager/localization.rs and
// localization_overlay.rs at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../third_party/deadlock-mod-manager/NOTICE.md.

// Package overlay builds and publishes the compatibility overlay: a separate
// VPK the game mounts before the mods, holding repaired resources so original
// mod files are never rewritten.
package overlay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const (
	// Generator marks receipts written by MyLock. Receipts from the original
	// Tauri app are rebuilt rather than trusted.
	Generator = "mylock"
	// Revision is the receipt revision. Bump it when repair rules or output
	// semantics change, so installed overlays are rebuilt.
	Revision uint32 = 7
)

// ErrConflict is returned when two mods would contribute the same resource
// path with different bytes.
var ErrConflict = errors.New("overlay inputs conflict")

// InputStamp identifies one archive input by size and modification time.
type InputStamp struct {
	Path     string `json:"path"`
	Length   int64  `json:"length"`
	Modified int64  `json:"modified"`
}

// ModStamp lists one mod's input archives in load order.
type ModStamp struct {
	ModID string       `json:"modId"`
	Files []InputStamp `json:"files"`
}

// Key fingerprints everything an overlay package was built from. It is stored
// in the receipt and compared byte-for-byte.
type Key struct {
	OutputPath          string       `json:"outputPath"`
	GameBuild           *uint32      `json:"gameBuild,omitempty"`
	CompatibilityModIDs []string     `json:"compatibilityModIds"`
	Mods                []ModStamp   `json:"mods"`
	BaseGame            []InputStamp `json:"baseGame,omitempty"`
}

// Repair is one fixed resource recorded in the receipt.
type Repair struct {
	ModID string `json:"modId"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
}

// Warning is one problem the overlay does not fix.
type Warning struct {
	ModID  string `json:"modId"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// Source is one enabled mod's overlay input. Fixed holds the repaired bytes
// keyed by the in-VPK resource path.
type Source struct {
	ModID                string
	VPKs                 []string
	CompatibilityEnabled bool
	Fixed                map[string][]byte
	Repairs              []Repair
	Warnings             []Warning
}

// Plan is a built overlay, ready to publish.
type Plan struct {
	Key      Key
	Files    map[string][]byte
	Repairs  []Repair
	Warnings []Warning
}

// Result describes a published overlay.
type Result struct {
	HasOverlay  bool
	OutputPath  string
	PackedFiles int
	Repairs     []Repair
	Warnings    []Warning
}

// Build stamps the inputs and collects repaired files. Duplicate paths across
// mods are refused rather than silently resolved: the combined analysis
// reports them as scope-incomplete.
func Build(citadelDir, outputPath string, build *uint32, sources []Source) (*Plan, error) {
	return BuildWithData(citadelDir, outputPath, build, sources, nil)
}

// BuildWithData is Build plus profile-wide merged files, such as game-data
// tables that no single mod owns. A shared file may not collide with a mod's
// repaired file.
func BuildWithData(citadelDir, outputPath string, build *uint32, sources []Source,
	shared map[string][]byte) (*Plan, error) {
	key := Key{OutputPath: outputPath, GameBuild: build}
	plan := &Plan{Key: key, Files: make(map[string][]byte)}
	owners := make(map[string]string)
	seenMods := make(map[string]struct{}, len(sources))
	compatibility := make([]string, 0, len(sources))

	for _, source := range sources {
		if _, dup := seenMods[source.ModID]; dup {
			return nil, fmt.Errorf("overlay input lists mod %q twice", source.ModID)
		}
		seenMods[source.ModID] = struct{}{}
		stamps, err := StampArchives(source.VPKs)
		if err != nil {
			return nil, fmt.Errorf("mod %s: %w", source.ModID, err)
		}
		plan.Key.Mods = append(plan.Key.Mods, ModStamp{ModID: source.ModID, Files: stamps})
		if source.CompatibilityEnabled {
			compatibility = append(compatibility, source.ModID)
		} else if len(source.Fixed) > 0 {
			return nil, fmt.Errorf("mod %s has repairs but compatibility is disabled", source.ModID)
		}
		for path, data := range source.Fixed {
			normalized, ok := vpk.NormalizeResourcePath(path)
			if !ok {
				return nil, fmt.Errorf("unsafe resource path %q in mod %s", path, source.ModID)
			}
			ownerKey := strings.ToLower(normalized)
			if owner, exists := owners[ownerKey]; exists && owner != source.ModID {
				return nil, fmt.Errorf("%w: mods %s and %s both provide %s", ErrConflict, owner, source.ModID, normalized)
			}
			owners[ownerKey] = source.ModID
			plan.Files[normalized] = data
		}
		plan.Repairs = append(plan.Repairs, source.Repairs...)
		plan.Warnings = append(plan.Warnings, source.Warnings...)
	}
	for path, data := range shared {
		normalized, ok := vpk.NormalizeResourcePath(path)
		if !ok {
			return nil, fmt.Errorf("unsafe resource path %q in merged game data", path)
		}
		ownerKey := strings.ToLower(normalized)
		if owner, exists := owners[ownerKey]; exists {
			return nil, fmt.Errorf("%w: mod %s and merged game data both provide %s", ErrConflict, owner, normalized)
		}
		owners[ownerKey] = ""
		plan.Files[normalized] = data
	}
	sort.Strings(compatibility)
	plan.Key.CompatibilityModIDs = compatibility
	base, err := BaseStamps(citadelDir)
	if err != nil {
		return nil, err
	}
	plan.Key.BaseGame = base
	return plan, nil
}

// StampArchives fingerprints a directory VPK and its numbered companions.
func StampArchives(vpkPaths []string) ([]InputStamp, error) {
	var stamps []InputStamp
	for _, path := range vpkPaths {
		archiveStamps, err := archiveStamps(path)
		if err != nil {
			return nil, err
		}
		stamps = append(stamps, archiveStamps...)
	}
	return stamps, nil
}

func archiveStamps(path string) ([]InputStamp, error) {
	stamp, err := stampFile(path)
	if err != nil {
		return nil, err
	}
	stamps := []InputStamp{stamp}
	prefix := strings.TrimSuffix(filepath.Base(path), "_dir.vpk")
	if prefix == filepath.Base(path) {
		return stamps, nil
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		if os.IsNotExist(err) {
			return stamps, nil
		}
		return nil, err
	}
	var chunks []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		rest, ok := strings.CutPrefix(name, prefix+"_")
		if !ok || !strings.HasSuffix(rest, ".vpk") {
			continue
		}
		number := strings.TrimSuffix(rest, ".vpk")
		if number == "" || !allDigits(number) {
			continue
		}
		chunks = append(chunks, filepath.Join(filepath.Dir(path), name))
	}
	sort.Strings(chunks)
	for _, chunk := range chunks {
		stamp, err := stampFile(chunk)
		if err != nil {
			return nil, err
		}
		stamps = append(stamps, stamp)
	}
	return stamps, nil
}

// BaseStamps fingerprints the game archives and steam.inf under citadelDir.
// A missing citadel or core directory contributes nothing.
func BaseStamps(citadelDir string) ([]InputStamp, error) {
	var stamps []InputStamp
	for _, dir := range []string{citadelDir, filepath.Join(filepath.Dir(citadelDir), "core")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("listing %s: %w", dir, err)
		}
		var archives []string
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_dir.vpk") {
				continue
			}
			archives = append(archives, filepath.Join(dir, entry.Name()))
		}
		sort.Strings(archives)
		for _, archive := range archives {
			archiveStamps, err := archiveStamps(archive)
			if err != nil {
				return nil, err
			}
			stamps = append(stamps, archiveStamps...)
		}
	}
	version := filepath.Join(citadelDir, "steam.inf")
	if info, err := os.Stat(version); err == nil && info.Mode().IsRegular() {
		stamp, err := stampFile(version)
		if err != nil {
			return nil, err
		}
		stamps = append(stamps, stamp)
	}
	return stamps, nil
}

func stampFile(path string) (InputStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return InputStamp{}, err
	}
	if !info.Mode().IsRegular() {
		return InputStamp{}, fmt.Errorf("%s is not a file", path)
	}
	modified := info.ModTime().UnixNano()
	if info.ModTime().IsZero() {
		modified = 0
	}
	return InputStamp{Path: path, Length: info.Size(), Modified: modified}, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// KeyJSON is the receipt encoding of the key.
func (p *Plan) KeyJSON() json.RawMessage {
	data, err := json.Marshal(p.Key)
	if err != nil {
		panic(fmt.Sprintf("overlay key is not encodable: %v", err))
	}
	return data
}
