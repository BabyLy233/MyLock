// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_model_evidence.rs
// (AnimationInputs and the animation dependency walk) at
// 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/animation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// animationInputs lists the animation overrides the load order supplies. It is
// provenance evidence, not a playback check: an override outside the model's
// graph closure and skeleton cannot change what that model samples.
type animationInputs struct {
	// modIDs are mods that ship any animation override.
	modIDs map[string]struct{}
	// overrides maps an override path to the mod that authored it. Legacy
	// graphs are left out: they cannot replace the active NM graphs.
	overrides map[string]string
}

// animationResolver reads a resource the way the load order does. ok is false
// when nothing provides it; owner is empty for a game resource.
type animationResolver func(path string) (data []byte, ok bool, owner string)

// newAnimationInputs records the analyzed mod's own animation files and the
// files of every other enabled mod in the scope.
func newAnimationInputs(scope Scope, modID string, modPaths map[string]struct{}) animationInputs {
	inputs := animationInputs{
		modIDs:    make(map[string]struct{}),
		overrides: make(map[string]string),
	}
	for path := range modPaths {
		inputs.record(path, modID)
	}
	for path, owner := range scope.OtherAnimationOwners {
		inputs.record(path, owner)
	}
	return inputs
}

// record adds one animation file. Paths outside the animation formats are
// ignored, as in the original.
func (a *animationInputs) record(path, modID string) {
	path = camera.NormalizePath(path)
	if !isAnimationEvidencePath(path) {
		return
	}
	a.modIDs[modID] = struct{}{}
	if strings.HasSuffix(path, ".vanmgrph_c") {
		return
	}
	if _, taken := a.overrides[path]; !taken {
		a.overrides[path] = modID
	}
}

// isAnimationEvidencePath matches the upstream dependency proof's formats.
func isAnimationEvidencePath(path string) bool {
	if strings.HasPrefix(path, "animations/") || strings.HasPrefix(path, "animgraphs/") {
		return true
	}
	for _, suffix := range []string{
		".vanim_c", ".vnmgraph_c", ".vnmclip_c", ".vagrp_c", ".vseq_c", ".vanmgrph_c",
	} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// needsDependencyProof reports whether a skeleton can only be refreshed with
// model evidence. Without that evidence the conservative path declines.
func (a animationInputs) needsDependencyProof(modID string) bool {
	_, own := a.modIDs[modID]
	return len(a.overrides) > 0 || own
}

// blocksModelBindings reports whether restoring this model's animation
// bindings could change what an enabled override plays. An override for another
// hero stays outside the model's closure and does not block.
func (a animationInputs) blocksModelBindings(current *kv3.Value, modID string,
	resolve animationResolver) bool {
	if _, own := a.modIDs[modID]; own {
		return true
	}
	if len(a.overrides) == 0 {
		return false
	}
	return !a.permitsCameraControls(current, modID, resolve)
}

// permitsCameraControls proves that game camera controls can be added to a
// model without changing what any unrelated animation override samples.
func (a animationInputs) permitsCameraControls(model *kv3.Value, modID string,
	resolve animationResolver) bool {
	if _, own := a.modIDs[modID]; own {
		return false
	}
	refsValue, ok := model.Get("m_vecNmSkeletonRefs")
	if !ok {
		return false
	}
	refs, ok := refsValue.AsArray()
	if !ok || len(refs) != 1 {
		return false
	}
	name, ok := refs[0].AsString()
	if !ok {
		return false
	}
	skeleton, ok := source2.ResourcePath(name)
	if !ok || !strings.HasSuffix(skeleton, ".vnmskel_c") {
		return false
	}
	if _, _, owner := resolve(skeleton); owner != "" {
		return false
	}
	return a.unrelated(model, skeleton, resolve)
}

// unrelated proves that effective overrides neither enter this model's graph
// closure nor sample this skeleton through another graph. Unknown animation
// layouts fail closed.
func (a animationInputs) unrelated(model *kv3.Value, skeleton string, resolve animationResolver) bool {
	if len(a.overrides) == 0 {
		return true
	}
	graphs, ok := animation.Graphs(model)
	if !ok {
		return false
	}
	visited := make(map[string]struct{})
	visiting := make(map[string]struct{})
	graphPaths := slices.Sorted(maps.Values(graphs))
	for _, graph := range graphPaths {
		if !walkAnimation(resolve, graph, skeleton, true, visited, visiting) {
			return false
		}
	}
	clear(visited)
	for _, path := range slices.Sorted(maps.Keys(a.overrides)) {
		if !walkAnimation(resolve, path, skeleton, false, visited, visiting) {
			return false
		}
	}
	return true
}

// walkAnimation follows graph and clip children. In the model closure every
// provider must be the game and clip bindings must name this skeleton; outside
// it, overrides must avoid this skeleton.
func walkAnimation(resolve animationResolver, name, skeleton string, modelClosure bool,
	visited, visiting map[string]struct{}) bool {
	path, ok := source2.ResourcePath(name)
	if !ok {
		return false
	}
	if !strings.HasSuffix(path, ".vnmclip_c") && !strings.HasSuffix(path, ".vnmgraph_c") {
		return false
	}
	if _, seen := visited[path]; seen {
		return true
	}
	if len(visiting) >= 64 || len(visited) >= 4096 {
		return false
	}
	if _, cycle := visiting[path]; cycle {
		return false
	}
	visiting[path] = struct{}{}
	defer delete(visiting, path)

	bytes, found, owner := resolve(path)
	if !found {
		return false
	}
	if modelClosure && owner != "" {
		return false
	}
	_, root, _, err := camera.DecodeCompiled(bytes)
	if err != nil {
		return false
	}
	var binding *bool
	if skeletonValue, ok := root.Get("m_skeleton"); ok {
		bound := false
		if name, ok := skeletonValue.AsString(); ok {
			if reference, ok := source2.ResourcePath(name); ok &&
				strings.HasSuffix(reference, ".vnmskel_c") {
				if modelClosure {
					bound = reference == skeleton
				} else {
					bound = reference != skeleton
				}
			}
		}
		binding = &bound
	}
	valid := false
	if strings.HasSuffix(path, ".vnmclip_c") {
		valid = binding != nil && *binding
	} else {
		resources, ok := root.Get("m_resources")
		if !ok {
			return false
		}
		items, ok := resources.AsArray()
		if !ok {
			return false
		}
		bound := binding != nil && *binding
		valid = (binding == nil || bound) && (len(items) > 0 || bound)
		for i := range items {
			if !valid {
				break
			}
			child, ok := items[i].AsString()
			if !ok {
				valid = false
				break
			}
			childPath, ok := source2.ResourcePath(child)
			if ok && modelClosure && bound && strings.HasSuffix(childPath, ".vnmclip_c") {
				// An installed graph may retain an optional clip that is not
				// present; it cannot introduce an override into this closure.
				if _, provided, _ := resolve(childPath); !provided {
					continue
				}
			}
			valid = walkAnimation(resolve, child, skeleton, modelClosure, visited, visiting)
		}
	}
	if valid {
		visited[path] = struct{}{}
	}
	return valid
}

// newAnimationResolver resolves animation resources from the effective load
// order, then the installed game. Other mods' archives are opened lazily and
// shared for the whole analysis.
func newAnimationResolver(scope Scope, modID string, modArchives map[string]*vpk.Archive,
	game *source2.GameIndex) animationResolver {
	opened := make(map[string]*vpk.Archive)
	return func(path string) ([]byte, bool, string) {
		if source, ok := scope.AnimationSources[path]; ok {
			if source.ModID == modID {
				if archive, ok := modArchives[path]; ok {
					data, err := archive.ReadLimited(path, maxModelBytes)
					return data, err == nil, modID
				}
			}
			archive, ok := opened[source.VPK]
			if !ok {
				var err error
				archive, err = vpk.Open(source.VPK)
				if err != nil {
					return nil, false, source.ModID
				}
				opened[source.VPK] = archive
			}
			data, err := archive.ReadLimited(source.Entry, maxModelBytes)
			return data, err == nil, source.ModID
		}
		if archive, ok := modArchives[path]; ok {
			data, err := archive.ReadLimited(path, maxModelBytes)
			return data, err == nil, modID
		}
		if game != nil {
			if data, ok := game.GameBytes(path); ok {
				return data, true, ""
			}
		}
		return nil, false, ""
	}
}
