// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_dependencies.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/animation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// MixedDependency is one animation resource the load order would take from a
// mod other than the model's owner.
type MixedDependency struct {
	Path  string
	ModID string
}

// mixedDependencyProvider is one winning archive entry.
type mixedDependencyProvider struct {
	modID   string
	archive *vpk.Archive
	entry   string
}

// MixedAnimationDependencies follows the model's actual references and reports
// animation files the current load order resolves to another mod with different
// bytes. It explains why an overlapping model may stretch or twist; it is not a
// playback validation, so a decode failure stops that branch silently.
func MixedAnimationDependencies(owner ModFiles, mods []ModFiles, modelPath string) ([]MixedDependency, error) {
	providers, owners, err := mixedDependencyProviders(owner, mods)
	if err != nil {
		return nil, err
	}
	shared := false
	for path, provider := range providers {
		if provider.modID == owner.ID {
			continue
		}
		if isAnimationPath(path) || camera.IsModelPath(path) {
			shared = true
			break
		}
	}
	if !shared {
		return nil, nil
	}
	pending := []string{normalizeScopePath(modelPath)}
	visited := make(map[string]struct{})
	var mixed []MixedDependency
	for len(pending) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := visited[path]; seen {
			continue
		}
		if len(visited) >= 4096 {
			break
		}
		visited[path] = struct{}{}
		provider, ok := providers[path]
		if !ok {
			continue
		}
		bytes, err := provider.archive.ReadLimited(provider.entry, maxModelBytes)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if provider.modID != owner.ID {
			expected, hasExpected, err := ownerBytes(owners, path)
			if err != nil {
				return nil, err
			}
			if !hasExpected || !slices.Equal(expected, bytes) {
				mixed = append(mixed, MixedDependency{Path: path, ModID: provider.modID})
			}
		}
		_, root, _, err := camera.DecodeCompiled(bytes)
		if err != nil {
			continue
		}
		pending = append(pending, mixedDependencyReferences(&root)...)
	}
	slices.SortFunc(mixed, func(left, right MixedDependency) int {
		return strings.Compare(left.Path, right.Path)
	})
	return mixed, nil
}

// mixedDependencyProviders resolves each path to the archive the game loads
// first, and keeps the owner's own copies for standalone-byte comparison.
func mixedDependencyProviders(owner ModFiles, mods []ModFiles) (map[string]mixedDependencyProvider, map[string]mixedDependencyProvider, error) {
	providers := make(map[string]mixedDependencyProvider)
	owners := make(map[string]mixedDependencyProvider)
	for _, mod := range mods {
		for _, vpkPath := range mod.VPKs {
			archive, err := vpk.Open(vpkPath)
			if err != nil {
				return nil, nil, fmt.Errorf("reading %s: %w", vpkPath, err)
			}
			owner := mod.ID == owner.ID
			for _, entry := range archive.Entries {
				path := camera.NormalizePath(entry.FullPath)
				provider := mixedDependencyProvider{
					modID: mod.ID, archive: archive, entry: entry.FullPath,
				}
				if owner {
					if _, taken := owners[path]; !taken {
						owners[path] = provider
					}
				}
				if _, taken := providers[path]; !taken {
					providers[path] = provider
				}
			}
		}
	}
	return providers, owners, nil
}

func ownerBytes(owners map[string]mixedDependencyProvider, path string) ([]byte, bool, error) {
	provider, ok := owners[path]
	if !ok {
		return nil, false, nil
	}
	bytes, err := provider.archive.ReadLimited(provider.entry, maxModelBytes)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	return bytes, true, nil
}

// mixedDependencyReferences lists the animation and included-model resources a
// decoded model, graph or clip points at.
func mixedDependencyReferences(root *kv3.Value) []string {
	var names []string
	for _, field := range []string{"m_vecNmSkeletonRefs", "m_resources", "m_refAnimGroups"} {
		names = append(names, stringArrayField(root, field)...)
	}
	if graphs, ok := root.Get("m_animGraph2Refs"); ok {
		if items, isArray := graphs.AsArray(); isArray {
			for i := range items {
				if graph, ok := items[i].Get("m_hGraph"); ok {
					if name, isString := graph.AsString(); isString {
						names = append(names, name)
					}
				}
			}
		}
	}
	if skeleton, ok := root.Get("m_skeleton"); ok {
		if name, isString := skeleton.AsString(); isString {
			names = append(names, name)
		}
	}
	if legacy, ok := animation.LegacyAnimationGraph(root); ok {
		names = append(names, legacy)
	}
	var references []string
	for _, name := range names {
		path, ok := source2.ResourcePath(name)
		if !ok {
			continue
		}
		if isAnimationPath(path) {
			references = append(references, path)
		}
	}
	for _, name := range stringArrayField(root, "m_refAnimIncludeModels") {
		if path, ok := source2.ResourcePath(name); ok && strings.HasSuffix(path, ".vmdl_c") {
			references = append(references, path)
		}
	}
	return references
}

func stringArrayField(value *kv3.Value, field string) []string {
	items, ok := value.Get(field)
	if !ok {
		return nil
	}
	array, ok := items.AsArray()
	if !ok {
		return nil
	}
	var out []string
	for i := range array {
		if text, ok := array[i].AsString(); ok {
			out = append(out, text)
		}
	}
	return out
}

// DescribeMixedDependencies groups mixed files by the mod that supplies them.
func DescribeMixedDependencies(dependencies []MixedDependency) string {
	if len(dependencies) == 0 {
		return ""
	}
	byMod := make(map[string][]string)
	for _, dependency := range dependencies {
		byMod[dependency.ModID] = append(byMod[dependency.ModID], dependency.Path)
	}
	parts := make([]string, 0, len(byMod))
	for _, modID := range slices.Sorted(maps.Keys(byMod)) {
		paths := byMod[modID]
		examples := paths
		remaining := 0
		if len(examples) > 3 {
			remaining = len(examples) - 3
			examples = examples[:3]
		}
		more := ""
		if remaining > 0 {
			more = fmt.Sprintf("；另有 %d 个", remaining)
		}
		noun := "个依赖"
		parts = append(parts, fmt.Sprintf("模组 %s：%d %s（%s%s）",
			modID, len(paths), noun, strings.Join(examples, "、"), more))
	}
	return strings.Join(parts, "；")
}
