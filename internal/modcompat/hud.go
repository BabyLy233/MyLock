// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/hud_compatibility.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// HudCheckStatus describes how far a HUD layout could be compared.
type HudCheckStatus string

const (
	HudCompared    HudCheckStatus = "compared"
	HudNoBaseline  HudCheckStatus = "noBaseline"
	HudUnsupported HudCheckStatus = "unsupported"
	HudUnreadable  HudCheckStatus = "unreadable"
)

// HudCheck is one advisory comparison between a mod's Panorama layout and the
// installed game's. Removed panel IDs can be intentional, so this never
// changes a mod's health status.
type HudCheck struct {
	ModID         string
	SourceVPK     string
	FilePath      string
	Status        HudCheckStatus
	MissingPanels []string
	Detail        string
	Console       HudConsoleEvidence
}

// HudConsoleEvidence is the game's console.log excerpt that may explain a
// layout failure. Historical lines cannot prove which mod was active, so this
// is evidence only.
type HudConsoleEvidence struct {
	ModifiedAt *float64
	Truncated  bool
	Error      string
	Lines      []string
}

const (
	// hudLogLimit bounds how much of console.log is read, from the end.
	hudLogLimit = 256 << 10
	// hudASTDepth and hudASTNodes bound a layout tree taken from a mod.
	hudASTDepth = 128
	hudASTNodes = 100_000
	// hudLogLines and hudLogLineRunes bound the evidence shown for one check.
	hudLogLines     = 40
	hudLogLineRunes = 500
)

// applyHud compares every Panorama layout a mod ships with the installed game
// copy and records the panels the game has and the mod lost.
func applyHud(analysis *Analysis, modID string, modPaths map[string]struct{},
	modArchives map[string]*vpk.Archive, modVPKs map[string]string,
	game *source2.GameIndex) error {
	if game == nil {
		return nil
	}
	directories := game.Directories()
	if len(directories) == 0 {
		return nil
	}
	console := readHudConsole(filepath.Join(directories[0], "console.log"))
	for _, path := range slices.Sorted(maps.Keys(modPaths)) {
		if !isHudLayoutPath(path) {
			continue
		}
		archive, ok := modArchives[path]
		if !ok {
			continue
		}
		check := HudCheck{
			ModID: modID, SourceVPK: modVPKs[path], FilePath: path, Status: HudCompared,
			Console: console,
		}
		bytes, err := archive.ReadLimited(path, maxModelBytes)
		if err != nil {
			check.Status, check.Detail = HudUnreadable, err.Error()
		} else {
			baseline, ok := game.GameBytes(path)
			if !ok {
				check.Status = HudNoBaseline
				check.Detail = "No installed vanilla layout; custom layout not compared"
			} else if current, currentErr := hudPanelIDs(bytes); currentErr != "" {
				check.Status, check.Detail = HudUnsupported, currentErr
			} else if vanilla, vanillaErr := hudPanelIDs(baseline); vanillaErr != "" {
				check.Status, check.Detail = HudUnsupported, vanillaErr
			} else {
				for _, id := range vanilla {
					if !slices.Contains(current, id) {
						check.MissingPanels = append(check.MissingPanels, id)
					}
				}
			}
		}
		correlateHud(&check)
		analysis.HudChecks = append(analysis.HudChecks, check)
	}
	return nil
}

func isHudLayoutPath(path string) bool {
	normalized := camera.NormalizePath(path)
	return strings.HasPrefix(normalized, "panorama/layout/") && strings.HasSuffix(normalized, ".vxml_c")
}

// hudPanelIDs returns the panel IDs a layout instantiates, sorted. Panels
// declared inside SNIPPET templates are templates, not guaranteed panels.
func hudPanelIDs(bytes []byte) ([]string, string) {
	parsed, err := resource.Parse(bytes)
	if err != nil {
		return nil, err.Error()
	}
	payload, ok := parsed.FindBlock([4]byte{'L', 'a', 'C', 'o'})
	if !ok {
		return nil, "Missing or unreadable LaCo block"
	}
	tree, err := kv3.Decode(payload)
	if err != nil {
		return nil, err.Error()
	}
	ast, ok := tree.Get("m_AST")
	if !ok {
		return nil, "Unsupported layout AST"
	}
	root, ok := ast.Get("m_pRoot")
	if !ok {
		return nil, "Unsupported layout AST"
	}
	ids, errText := collectHudIDs(root)
	if errText != "" {
		return nil, errText
	}
	slices.Sort(ids)
	return ids, ""
}

func collectHudIDs(root *kv3.Value) ([]string, string) {
	if _, ok := root.Get("eType"); !ok {
		return nil, "Unsupported layout root"
	}
	type entry struct {
		node  *kv3.Value
		depth int
	}
	pending := []entry{{node: root}}
	visited := 0
	seen := make(map[string]struct{})
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current.node.Kind == kv3.KindNull {
			continue
		}
		visited++
		if current.depth > hudASTDepth || visited > hudASTNodes {
			return nil, "Layout AST exceeds scan limits"
		}
		kind, _ := hudStringField(current.node, "eType")
		name, hasName := hudStringField(current.node, "name")
		if kind == "" && !hasName {
			return nil, "Unsupported layout node"
		}
		if kind == "SNIPPET" {
			continue
		}
		if kind == "PANEL_ATTRIBUTE" && name == "id" {
			child, ok := current.node.Get("child")
			if !ok {
				return nil, "Unsupported panel ID"
			}
			id, ok := hudStringField(child, "name")
			if !ok {
				return nil, "Unsupported panel ID"
			}
			if id != "" {
				seen[id] = struct{}{}
			}
			continue
		}
		if child, ok := current.node.Get("child"); ok {
			pending = append(pending, entry{node: child, depth: current.depth + 1})
		}
		if children, ok := current.node.Get("vecChildren"); ok {
			items, isArray := children.AsArray()
			if !isArray {
				return nil, "Unsupported layout children"
			}
			for i := range items {
				pending = append(pending, entry{node: &items[i], depth: current.depth + 1})
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, ""
}

func hudStringField(value *kv3.Value, key string) (string, bool) {
	field, ok := value.Get(key)
	if !ok {
		return "", false
	}
	return field.AsString()
}

// readHudConsole reads the tail of the game's console.log and keeps the lines
// that mention a missing panel or layout failure.
func readHudConsole(path string) HudConsoleEvidence {
	var evidence HudConsoleEvidence
	file, err := os.Open(path)
	if err != nil {
		evidence.Error = err.Error()
		return evidence
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		evidence.Error = err.Error()
		return evidence
	}
	if modified := info.ModTime(); !modified.IsZero() {
		value := float64(modified.UnixNano()) / 1e6
		evidence.ModifiedAt = &value
	}
	evidence.Truncated = info.Size() > hudLogLimit
	offset := info.Size() - hudLogLimit
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		evidence.Error = err.Error()
		return evidence
	}
	bytes, err := io.ReadAll(io.LimitReader(file, hudLogLimit))
	if err != nil {
		evidence.Error = err.Error()
		return evidence
	}
	lines := strings.Split(strings.ReplaceAll(string(bytes), "\r\n", "\n"), "\n")
	if evidence.Truncated && len(lines) > 1 {
		// The first line may be a partial one.
		lines = lines[1:]
	}
	var kept []string
	for index := len(lines) - 1; index >= 0 && len(kept) < hudLogLines; index-- {
		line := lines[index]
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "unable to find child") &&
			!strings.Contains(lower, "unable to load layout file") &&
			!(strings.Contains(lower, "layout") &&
				(strings.Contains(lower, "failed") || strings.Contains(lower, "error"))) {
			continue
		}
		if runes := []rune(line); len(runes) > hudLogLineRunes {
			line = string(runes[:hudLogLineRunes])
		}
		kept = append(kept, line)
	}
	evidence.Lines = kept
	return evidence
}

// correlateHud keeps the console lines that name a missing panel or this
// layout file. Matching IDs can occur in several layouts, so every association
// is preserved.
func correlateHud(check *HudCheck) {
	layout := strings.ToLower(strings.ReplaceAll(
		strings.TrimSuffix(check.FilePath, "_c"), "\\", "/"))
	var kept []string
	for _, line := range check.Console.Lines {
		matched := false
		for _, id := range check.MissingPanels {
			if strings.Contains(line, "'"+id+"'") || strings.Contains(line, "\""+id+"\"") {
				matched = true
				break
			}
		}
		if !matched {
			normalized := strings.ToLower(strings.ReplaceAll(line, "\\", "/"))
			matched = strings.Contains(normalized, layout)
		}
		if matched {
			kept = append(kept, line)
		}
	}
	check.Console.Lines = kept
}
