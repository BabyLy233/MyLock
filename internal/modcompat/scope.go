package modcompat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/relocation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// ModFiles pairs one mod with its archives in load order.
type ModFiles struct {
	ID   string
	VPKs []string
}

// Scope describes the other enabled mods that share this mod's load order.
// Repairs must not change which mod the game loads.
type Scope struct {
	// OtherModelPaths are normalized model paths provided by other enabled
	// mods. Relocating a model onto one of them would override that mod.
	OtherModelPaths map[string]struct{}
	// OtherAnimationPaths are animation resources provided by other enabled
	// mods. Without their bytes, dependency checks must decline instead of
	// reporting a false missing resource.
	OtherAnimationPaths map[string]struct{}
	// RegistryModified reports that another enabled mod overrides the hero
	// registry, so relocation targets cannot be trusted without its bytes.
	RegistryModified bool
	// Unknown reports that another enabled mod could not be scanned. Repairs
	// that depend on the rest of the load order must decline.
	Unknown bool
}

// Fingerprint returns a stable cache-key value for the scope. The empty scope
// has an empty fingerprint.
func (s Scope) Fingerprint() string {
	if len(s.OtherModelPaths) == 0 && len(s.OtherAnimationPaths) == 0 &&
		!s.RegistryModified && !s.Unknown {
		return ""
	}
	h := sha256.New()
	for _, path := range slices.Sorted(maps.Keys(s.OtherModelPaths)) {
		h.Write([]byte("model:"))
		h.Write([]byte(path))
		h.Write([]byte{0})
	}
	for _, path := range slices.Sorted(maps.Keys(s.OtherAnimationPaths)) {
		h.Write([]byte("animation:"))
		h.Write([]byte(path))
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "registry=%t unknown=%t", s.RegistryModified, s.Unknown)
	return hex.EncodeToString(h.Sum(nil))
}

// Scopes computes the cross-mod scope for every mod in one load order.
func Scopes(mods []ModFiles) (map[string]Scope, error) {
	type scanned struct {
		models     map[string]struct{}
		animations map[string]struct{}
		registry   bool
	}
	entries := make(map[string]scanned, len(mods))
	for _, mod := range mods {
		paths, err := archivePaths(mod.VPKs)
		if err != nil {
			return nil, err
		}
		models := make(map[string]struct{})
		animations := make(map[string]struct{})
		registry := false
		for _, path := range paths {
			if camera.IsModelPath(path) {
				models[path] = struct{}{}
			}
			if isAnimationPath(path) {
				animations[path] = struct{}{}
			}
			if path == relocation.HeroesVDataPath {
				registry = true
			}
		}
		entries[mod.ID] = scanned{models: models, animations: animations, registry: registry}
	}
	scopes := make(map[string]Scope, len(mods))
	for _, mod := range mods {
		scope := Scope{
			OtherModelPaths:     make(map[string]struct{}),
			OtherAnimationPaths: make(map[string]struct{}),
		}
		for _, other := range mods {
			if other.ID == mod.ID {
				continue
			}
			maps.Copy(scope.OtherModelPaths, entries[other.ID].models)
			maps.Copy(scope.OtherAnimationPaths, entries[other.ID].animations)
			if entries[other.ID].registry {
				scope.RegistryModified = true
			}
		}
		scopes[mod.ID] = scope
	}
	return scopes, nil
}

// archivePaths lists every normalized entry path in the archives. The result is
// sorted by archive and entry order.
func archivePaths(vpkPaths []string) ([]string, error) {
	var paths []string
	for _, vpkPath := range vpkPaths {
		archive, err := vpk.Open(vpkPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", vpkPath, err)
		}
		for _, entry := range archive.Entries {
			paths = append(paths, camera.NormalizePath(entry.FullPath))
		}
	}
	return paths, nil
}

// ProvidesPath reports whether any archive provides a resource path.
func ProvidesPath(vpkPaths []string, name string) (bool, error) {
	target, ok := relocation.ResourcePath(name)
	if !ok {
		return false, nil
	}
	paths, err := archivePaths(vpkPaths)
	if err != nil {
		return false, err
	}
	return slices.Contains(paths, target), nil
}
