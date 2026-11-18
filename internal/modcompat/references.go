package modcompat

import (
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/relocation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// unloadedModels lists mod-only models that nothing loaded references. The
// second result is false when an unread resource could reference any of them,
// in which case no model may be treated as unloaded.
func unloadedModels(game *source2.GameIndex, modPaths map[string]struct{},
	modArchives map[string]*vpk.Archive, read func(string) ([]byte, error),
	relocated map[string]struct{}) ([]string, bool) {
	if game == nil {
		return nil, false
	}
	candidates := make(map[string]struct{})
	for path := range modPaths {
		if !camera.IsModelPath(path) {
			continue
		}
		if _, ok := game.Provider(path); ok {
			continue
		}
		if _, ok := relocated[path]; ok {
			continue
		}
		candidates[path] = struct{}{}
	}
	if len(candidates) == 0 {
		return nil, true
	}
	var pending []string
	for path := range modPaths {
		_, hasGame := game.Provider(path)
		_, moved := relocated[path]
		if hasGame || moved {
			pending = append(pending, path)
		}
	}
	visited := make(map[string]struct{})
	for len(pending) > 0 && len(candidates) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := visited[path]; seen || !mayReferenceModels(path) {
			continue
		}
		visited[path] = struct{}{}
		bytes, err := read(path)
		if err != nil {
			return nil, false
		}
		if bytes == nil {
			continue
		}
		for _, name := range referencedNames(path, bytes) {
			target, ok := source2.ResourcePath(name)
			if !ok {
				continue
			}
			if _, provided := modArchives[target]; !provided {
				continue
			}
			delete(candidates, target)
			pending = append(pending, target)
		}
	}
	return slices.Sorted(maps.Keys(candidates)), true
}

func mayReferenceModels(path string) bool {
	if !strings.HasSuffix(path, "_c") {
		return false
	}
	for _, suffix := range []string{
		".vtex_c", ".vsnd_c", ".vmat_c", ".vmesh_c", ".vphys_c",
		".vnmclip_c", ".vnmskel_c",
	} {
		if strings.HasSuffix(path, suffix) {
			return false
		}
	}
	return true
}

func referencedNames(path string, bytes []byte) []string {
	var names []string
	if refs, err := resource.ReferencesOf(bytes); err == nil {
		for _, reference := range refs {
			names = append(names, reference.Path)
		}
	}
	if strings.HasSuffix(path, ".vdata_c") {
		if _, root, _, err := camera.DecodeCompiled(bytes); err == nil {
			collectStrings(&root, &names)
		}
	}
	return names
}

func collectStrings(value *kv3.Value, names *[]string) {
	switch value.Kind {
	case kv3.KindString:
		*names = append(*names, value.String)
	case kv3.KindArray:
		for i := range value.Array {
			collectStrings(&value.Array[i], names)
		}
	case kv3.KindObject:
		for i := range value.Object {
			collectStrings(&value.Object[i].Value, names)
		}
	}
}

// propModels lists warned mod models that are not in the hero registry and
// were not relocated from a historical hero binding.
func propModels(game *source2.GameIndex, read func(string) ([]byte, error),
	warnedPaths []string, relocated map[string]struct{}) []string {
	warned := make(map[string]struct{})
	for _, filePath := range warnedPaths {
		path := camera.NormalizePath(filePath)
		if camera.IsModelPath(path) {
			warned[path] = struct{}{}
		}
	}
	if len(warned) == 0 {
		return nil
	}
	heroes := make(map[string]struct{})
	seen := make(map[string]struct{})
	var registries [][]byte
	if game != nil {
		if bytes, ok := game.GameBytes(relocation.HeroesVDataPath); ok {
			registries = append(registries, bytes)
		}
	}
	if bytes, err := read(relocation.HeroesVDataPath); err == nil && bytes != nil {
		registries = append(registries, bytes)
	}
	for _, bytes := range registries {
		key := string(bytes)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		_, root, _, err := camera.DecodeCompiled(bytes)
		if err != nil {
			continue
		}
		rows, ok := root.AsObject()
		if !ok {
			continue
		}
		for _, row := range rows {
			model, ok := row.Value.Get("m_strModelName")
			if !ok {
				continue
			}
			name, ok := model.AsString()
			if !ok {
				continue
			}
			path, ok := source2.ResourcePath(name)
			if ok {
				heroes[path] = struct{}{}
			}
		}
	}
	props := make([]string, 0, len(warned))
	for path := range warned {
		if _, hero := heroes[path]; hero {
			continue
		}
		if _, moved := relocated[path]; moved {
			continue
		}
		props = append(props, path)
	}
	slices.Sort(props)
	return props
}
