package modcompat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/localization"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/relocation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// ModFiles pairs one mod with its archives in load order.
type ModFiles struct {
	ID   string
	VPKs []string
}

// AnimationSource is the archive copy the load order resolves for one
// animation resource. ModID is the winning mod.
type AnimationSource struct {
	ModID string
	VPK   string
	Entry string
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
	// OtherAnimationOwners names the mod each of those paths resolves to.
	OtherAnimationOwners map[string]string
	// AnimationSources is the effective animation provider map for the whole
	// load order. It is shared read-only and lets the animation proofs decode
	// the files the game actually loads.
	AnimationSources map[string]AnimationSource
	// RegistryModified reports that another enabled mod overrides the hero
	// registry, so relocation targets cannot be trusted without its bytes.
	RegistryModified bool
	// Unknown reports that another enabled mod could not be scanned. Repairs
	// that depend on the rest of the load order must decline.
	Unknown bool
	// ProfileData reports that game-data tables are merged across the whole
	// profile. The per-mod analysis still reports findings but leaves the merged
	// bytes to the profile-level merger.
	ProfileData bool
	// History is the learned VData history for the installed game. It only
	// improves baseline matching and records the tables this analysis saw; it
	// is deliberately not part of Fingerprint, because learning new tables does
	// not invalidate a verdict about a mod's files.
	History *History
}

// Fingerprint returns a stable cache-key value for the scope. The empty scope
// has an empty fingerprint.
func (s Scope) Fingerprint() string {
	if len(s.OtherModelPaths) == 0 && len(s.OtherAnimationPaths) == 0 &&
		!s.RegistryModified && !s.Unknown && !s.ProfileData {
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
		h.Write([]byte("="))
		h.Write([]byte(s.OtherAnimationOwners[path]))
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "registry=%t unknown=%t profileData=%t", s.RegistryModified, s.Unknown, s.ProfileData)
	return hex.EncodeToString(h.Sum(nil))
}

// RepairScope lists the resources a compatibility package must not rewrite.
// A path is protected when an installed mod that is not part of the package
// also ships it: merged text and data tables combine providers, while a
// binary asset is only protected when the other mod wins the load order.
type RepairScope struct {
	selected  map[string]struct{}
	protected map[string][]string
}

// NewRepairScope builds the protection set for one profile. mods are every
// installed mod in load order, selected are the ones the package includes.
func NewRepairScope(mods []ModFiles, selected map[string]struct{}) (*RepairScope, error) {
	pathsByMod := make(map[string][]string, len(mods))
	winners := make(map[string]string)
	for _, mod := range mods {
		paths, err := archivePaths(mod.VPKs)
		if err != nil {
			return nil, err
		}
		pathsByMod[mod.ID] = paths
		for _, path := range paths {
			if _, taken := winners[path]; !taken {
				winners[path] = mod.ID
			}
		}
	}
	protected := make(map[string]map[string]struct{})
	for _, mod := range mods {
		if _, ok := selected[mod.ID]; ok {
			continue
		}
		for _, path := range pathsByMod[mod.ID] {
			// Merged tables combine every provider, so any other copy makes the
			// path unsafe. A binary asset only matters when this mod is the one
			// the game loads; otherwise the package cannot change that copy.
			if !localization.IsLocalizationPath(path) && !IsDataTablePath(path) &&
				winners[path] != mod.ID {
				continue
			}
			if protected[path] == nil {
				protected[path] = make(map[string]struct{})
			}
			protected[path][mod.ID] = struct{}{}
		}
	}
	scope := &RepairScope{
		selected:  make(map[string]struct{}, len(selected)),
		protected: make(map[string][]string, len(protected)),
	}
	maps.Copy(scope.selected, selected)
	for path, ids := range protected {
		scope.protected[path] = slices.Sorted(maps.Keys(ids))
	}
	return scope, nil
}

// Includes reports whether a mod is part of the package.
func (s *RepairScope) Includes(modID string) bool {
	if s == nil {
		return false
	}
	_, ok := s.selected[modID]
	return ok
}

// Protects reports whether the path must be left as the game loads it.
func (s *RepairScope) Protects(filePath string) bool {
	if s == nil {
		return false
	}
	_, ok := s.protected[normalizeScopePath(filePath)]
	return ok
}

// ModIDs names the installed mods that keep a protected path.
func (s *RepairScope) ModIDs(filePath string) []string {
	if s == nil {
		return nil
	}
	return s.protected[normalizeScopePath(filePath)]
}

// ProtectedPaths returns every protected path in sorted order.
func (s *RepairScope) ProtectedPaths() []string {
	if s == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(s.protected))
}

func normalizeScopePath(filePath string) string {
	return camera.NormalizePath(filePath)
}

// ProtectedFor lists the requested resources that another installed mod keeps.
func (s *RepairScope) ProtectedFor(vpkPaths []string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	paths, err := archivePaths(vpkPaths)
	if err != nil {
		return nil, err
	}
	var protected []string
	for _, path := range paths {
		if s.Protects(path) {
			protected = append(protected, path)
		}
	}
	slices.Sort(protected)
	return slices.Compact(protected), nil
}

// RemoveProtected drops findings and repaired bytes for resources the package
// must leave as the game loads them. Standalone health keeps the findings so
// the player can see what a mod needs; the profile-level build removes them so
// a report never claims a repair the package does not contain.
func (a *Analysis) RemoveProtected(scope *RepairScope) {
	if scope == nil {
		return
	}
	a.Repairs = slices.DeleteFunc(a.Repairs, func(repair Repair) bool {
		return scope.Protects(repair.FilePath)
	})
	a.Warnings = slices.DeleteFunc(a.Warnings, func(warning Warning) bool {
		return scope.Protects(warning.FilePath)
	})
	a.AssetWarnings = slices.DeleteFunc(a.AssetWarnings, func(warning AssetWarning) bool {
		return scope.Protects(warning.FilePath)
	})
	a.DataRepairs = slices.DeleteFunc(a.DataRepairs, func(repair vdata.DataRepair) bool {
		return scope.Protects(repair.FilePath)
	})
	a.DataWarnings = slices.DeleteFunc(a.DataWarnings, func(warning vdata.DataWarning) bool {
		return scope.Protects(warning.FilePath)
	})
	for path := range a.Fixed {
		if scope.Protects(path) {
			delete(a.Fixed, path)
		}
	}
}

// Scopes computes the cross-mod scope for every mod in one load order.
func Scopes(mods []ModFiles) (map[string]Scope, error) {
	type scanned struct {
		models     map[string]struct{}
		animations map[string]struct{}
		sources    map[string]AnimationSource
		registry   bool
	}
	entries := make(map[string]scanned, len(mods))
	for _, mod := range mods {
		scannedMod := scanned{
			models:     make(map[string]struct{}),
			animations: make(map[string]struct{}),
			sources:    make(map[string]AnimationSource),
		}
		for _, vpkPath := range mod.VPKs {
			archive, err := vpk.Open(vpkPath)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", vpkPath, err)
			}
			for _, entry := range archive.Entries {
				path := camera.NormalizePath(entry.FullPath)
				if camera.IsModelPath(path) {
					scannedMod.models[path] = struct{}{}
				}
				if isAnimationPath(path) {
					scannedMod.animations[path] = struct{}{}
					if _, taken := scannedMod.sources[path]; !taken {
						scannedMod.sources[path] = AnimationSource{
							ModID: mod.ID, VPK: vpkPath, Entry: entry.FullPath,
						}
					}
				}
				if path == relocation.HeroesVDataPath {
					scannedMod.registry = true
				}
			}
		}
		entries[mod.ID] = scannedMod
	}
	// The first mod of the load order wins a path, as the game loads it.
	globalSources := make(map[string]AnimationSource)
	for _, mod := range mods {
		for path, source := range entries[mod.ID].sources {
			if _, taken := globalSources[path]; !taken {
				globalSources[path] = source
			}
		}
	}
	scopes := make(map[string]Scope, len(mods))
	for _, mod := range mods {
		scope := Scope{
			OtherModelPaths:      make(map[string]struct{}),
			OtherAnimationPaths:  make(map[string]struct{}),
			OtherAnimationOwners: make(map[string]string),
			AnimationSources:     globalSources,
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
		for path := range scope.OtherAnimationPaths {
			scope.OtherAnimationOwners[path] = globalSources[path].ModID
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
