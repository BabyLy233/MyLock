// Package modcompat analyses installed mods against the current game files and
// produces compatibility findings and repaired bytes.
package modcompat

import (
	"fmt"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const maxModelBytes = 32 << 20

// Mode selects whether the analyzer only reports findings or also returns the
// repaired bytes.
type Mode int

const (
	// Detect only reports findings.
	Detect Mode = iota
	// Fix also stores repaired bytes in Analysis.Fixed.
	Fix
)

// Repair is one safe repair the analyzer can apply.
type Repair struct {
	ModID     string
	SourceVPK string
	FilePath  string
	Kind      camera.RepairKind
}

// AssetWarning is a structural asset problem that changes what loads or how it
// looks, but has no safe repair yet.
type AssetWarning struct {
	ModID     string
	SourceVPK string
	FilePath  string
	Kind      compat.WarningKind
	Detail    string
}

// Warning is one issue that could not be repaired safely.
type Warning struct {
	ModID     string
	SourceVPK string
	FilePath  string
	Kind      camera.WarningKind
	Detail    string
}

// Analysis is the result of analysing one mod.
type Analysis struct {
	Repairs       []Repair
	Warnings      []Warning
	AssetWarnings []AssetWarning
	// Fixed holds the repaired resource bytes by path; only populated in Fix mode.
	Fixed map[string][]byte
	// Checks lists the health categories this analysis covered.
	Checks []string
	// ModelsChecked is the number of model files compared with the game.
	ModelsChecked int
	// AssetsChecked is the number of model, mesh, skeleton and material
	// resources that received the structural checks.
	AssetsChecked int
	// ScopeIncomplete marks a mod whose files another enabled mod also
	// provides: its findings cannot be repaired on its own.
	ScopeIncomplete bool
}

// CameraChecks are the health categories the camera analyzer covers today.
var CameraChecks = []string{"cameraAim", "crashRisk"}

type modelSource struct {
	path    string
	vpkPath string
	archive *vpk.Archive
}

// Analyze compares every model in the mod's VPKs with the current game model.
// VPK order is load order: the first VPK providing a path wins.
func Analyze(modID string, vpkPaths []string, game *source2.GameIndex, mode Mode) (Analysis, error) {
	var analysis Analysis
	analysis.Checks = append(analysis.Checks, CameraChecks...)
	if mode == Fix {
		analysis.Fixed = make(map[string][]byte)
	}

	seen := make(map[string]struct{})
	modPaths := make(map[string]struct{})
	var models []modelSource
	var assets []modelSource
	hasAnimationEntries := false
	for _, vpkPath := range vpkPaths {
		archive, err := vpk.Open(vpkPath)
		if err != nil {
			return Analysis{}, fmt.Errorf("reading %s: %w", vpkPath, err)
		}
		for _, entry := range archive.Entries {
			path := entry.FullPath
			normalized := camera.NormalizePath(path)
			modPaths[normalized] = struct{}{}
			if isAnimationPath(path) {
				hasAnimationEntries = true
			}
			if !camera.IsModelPath(path) {
				if strings.HasSuffix(normalized, ".vmesh_c") || strings.HasSuffix(normalized, ".vnmskel_c") ||
					strings.HasSuffix(normalized, ".vmat_c") {
					if _, dup := seen[normalized]; !dup {
						seen[normalized] = struct{}{}
						assets = append(assets, modelSource{path: entry.FullPath, vpkPath: vpkPath, archive: archive})
					}
				}
				continue
			}
			if _, dup := seen[normalized]; dup {
				continue
			}
			seen[normalized] = struct{}{}
			models = append(models, modelSource{path: entry.FullPath, vpkPath: vpkPath, archive: archive})
		}
	}
	exists := func(name string) bool { return resourceExists(name, modPaths, game) }

	for _, source := range models {
		modBytes, err := source.archive.ReadLimited(source.path, maxModelBytes)
		if err != nil {
			return Analysis{}, fmt.Errorf("reading %s from %s: %w", source.path, source.vpkPath, err)
		}
		var base []byte
		hasBase := false
		if game != nil {
			base, hasBase = game.GameBytes(source.path)
		}
		analysis.AssetsChecked++
		analysis.AssetWarnings = append(analysis.AssetWarnings, structuralModelWarnings(
			modID, source.vpkPath, source.path, modBytes, base, hasBase, exists)...)
		if !hasBase {
			continue
		}
		analysis.ModelsChecked++
		allowControls := func(vanilla *kv3.Value) bool {
			return !hasAnimationEntries && gameSkeletonReference(vanilla)
		}
		result := camera.Analyze(modBytes, base, allowControls)
		for _, kind := range result.RepairKinds {
			analysis.Repairs = append(analysis.Repairs, Repair{
				ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path, Kind: kind,
			})
		}
		for _, kind := range result.Warnings {
			analysis.Warnings = append(analysis.Warnings, Warning{
				ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path, Kind: kind, Detail: warningDetail(kind),
			})
		}
		if mode != Fix || !result.Repairable {
			continue
		}
		fixed, _, _, changed := camera.Repair(modBytes, base, allowControls)
		if changed {
			analysis.Fixed[source.path] = fixed
		}
	}

	for _, source := range assets {
		data, err := source.archive.ReadLimited(source.path, maxModelBytes)
		if err != nil {
			return Analysis{}, fmt.Errorf("reading %s from %s: %w", source.path, source.vpkPath, err)
		}
		analysis.AssetsChecked++
		path := camera.NormalizePath(source.path)
		switch {
		case strings.HasSuffix(path, ".vnmskel_c"):
			analysis.AssetWarnings = append(analysis.AssetWarnings,
				skeletonWarnings(modID, source.vpkPath, source.path, data)...)
		case strings.HasSuffix(path, ".vmesh_c"):
			analysis.AssetWarnings = append(analysis.AssetWarnings,
				meshAssetWarnings(modID, source.vpkPath, source.path, data)...)
		default:
			analysis.AssetWarnings = append(analysis.AssetWarnings,
				materialAssetWarnings(modID, source.vpkPath, source.path, data, game)...)
		}
	}
	return analysis, nil
}

var mdatKind = [4]byte{'M', 'D', 'A', 'T'}

// structuralModelWarnings checks the parts of a model that can make the engine
// reject it or render it wrong: rig shape, remapping, resource references,
// animation interfaces and mesh attachments/hitboxes.
func structuralModelWarnings(modID, vpkPath, path string, data, base []byte, hasBase bool, exists func(string) bool) []AssetWarning {
	var warnings []AssetWarning
	add := func(kind compat.WarningKind, detail string) {
		warnings = append(warnings, AssetWarning{
			ModID: modID, SourceVPK: vpkPath, FilePath: path, Kind: kind, Detail: detail,
		})
	}
	_, value, _, err := camera.DecodeCompiled(data)
	if err != nil {
		add(compat.WarningUnreadableResource, "DATA")
		return warnings
	}
	if rigValue, ok := value.Get("m_modelSkeleton"); ok {
		if !compat.ValidSkeleton(rigValue, "m_boneName", "m_nParent",
			[]compat.PoseKey{
				{Key: "m_bonePosParent", Width: 3},
				{Key: "m_boneRotParent", Width: 4},
				{Key: "m_boneScaleParent", Width: 0},
			}) {
			add(compat.WarningInvalidSkeleton, "m_modelSkeleton")
		}
		if !compat.ValidRemapping(&value, rigValue) {
			add(compat.WarningInvalidBoneRemapping, "m_remappingTable / m_remappingTableStarts")
		}
	}
	for _, field := range []string{
		"m_refMeshes",
		"m_refPhysicsData",
		"m_refPhysicsHitboxData",
		"m_vecNmSkeletonRefs",
		"m_refAnimGroups",
		"m_refAnimIncludeModels",
		"m_refSequenceGroups",
	} {
		refsValue, ok := value.Get(field)
		if !ok {
			continue
		}
		refs, ok := refsValue.AsArray()
		if !ok {
			continue
		}
		for _, ref := range refs {
			name, ok := ref.AsString()
			if ok && !exists(name) {
				add(compat.WarningMissingResource, name)
			}
		}
	}
	if graphsValue, ok := value.Get("m_animGraph2Refs"); ok {
		if graphs, ok := graphsValue.AsArray(); ok {
			for _, graph := range graphs {
				nameValue, ok := graph.Get("m_hGraph")
				if !ok {
					continue
				}
				name, ok := nameValue.AsString()
				if ok && !exists(name) {
					add(compat.WarningMissingResource, name)
				}
			}
		}
	}
	if hasBase {
		if _, vanilla, _, err := camera.DecodeCompiled(base); err == nil {
			for _, field := range compat.MissingAnimationFields(&value, &vanilla) {
				add(compat.WarningMissingAnimationInterface, field)
			}
		}
	}
	if parsed, err := resource.Parse(data); err == nil {
		bones := modelBoneSet(&value)
		for index, block := range parsed.Blocks() {
			if block.Kind != mdatKind {
				continue
			}
			raw, ok := parsed.GetBlockByIndex(index)
			if !ok {
				add(compat.WarningUnreadableResource, "MDAT")
				continue
			}
			mesh, err := kv3.Decode(raw)
			if err != nil {
				add(compat.WarningUnreadableResource, "MDAT")
				continue
			}
			for _, warning := range compat.MeshWarnings(&mesh, bones) {
				add(warning.Kind, warning.Detail)
			}
		}
	}
	return dedupeAssetWarnings(warnings)
}

func skeletonWarnings(modID, vpkPath, path string, data []byte) []AssetWarning {
	_, value, _, err := camera.DecodeCompiled(data)
	if err != nil {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningUnreadableResource, Detail: "DATA"}}
	}
	if !compat.ValidSkeleton(&value, "m_boneIDs", "m_parentIndices",
		[]compat.PoseKey{
			{Key: "m_parentSpaceReferencePose", Width: 8},
			{Key: "m_modelSpaceReferencePose", Width: 8},
		}) {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningInvalidSkeleton, Detail: "m_parentIndices / m_boneIDs / referencePose"}}
	}
	return nil
}

func meshAssetWarnings(modID, vpkPath, path string, data []byte) []AssetWarning {
	var warnings []AssetWarning
	parsed, err := resource.Parse(data)
	if err != nil {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningUnreadableResource, Detail: "resource"}}
	}
	for index, block := range parsed.Blocks() {
		if block.Kind != resource.BlockTypeData {
			continue
		}
		raw, ok := parsed.GetBlockByIndex(index)
		if !ok {
			warnings = append(warnings, AssetWarning{ModID: modID, SourceVPK: vpkPath, FilePath: path,
				Kind: compat.WarningUnreadableResource, Detail: "DATA"})
			continue
		}
		mesh, err := kv3.Decode(raw)
		if err != nil {
			warnings = append(warnings, AssetWarning{ModID: modID, SourceVPK: vpkPath, FilePath: path,
				Kind: compat.WarningUnreadableResource, Detail: "DATA"})
			continue
		}
		for _, warning := range compat.MeshWarnings(&mesh, nil) {
			warnings = append(warnings, AssetWarning{ModID: modID, SourceVPK: vpkPath, FilePath: path,
				Kind: warning.Kind, Detail: warning.Detail})
		}
	}
	return dedupeAssetWarnings(warnings)
}

// materialAssetWarnings compares a mod material's shader interface with the
// current game material. It reports differences only; nothing is rewritten.
func materialAssetWarnings(modID, vpkPath, path string, data []byte, game *source2.GameIndex) []AssetWarning {
	_, source, _, err := camera.DecodeCompiled(data)
	if err != nil {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningUnreadableResource, Detail: err.Error()}}
	}
	if game == nil {
		return nil
	}
	base, ok := game.GameBytes(path)
	if !ok {
		// A mod-only material has no current reference to compare against.
		return nil
	}
	_, current, _, err := camera.DecodeCompiled(base)
	if err != nil {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind:   compat.WarningMaterialInterface,
			Detail: "Current material interface could not be decoded: " + err.Error()}}
	}
	details, err := compat.MaterialDifferences(&source, &current)
	if err != nil {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningMaterialInterface, Detail: err.Error()}}
	}
	warnings := make([]AssetWarning, 0, len(details))
	for _, detail := range details {
		warnings = append(warnings, AssetWarning{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningMaterialInterface, Detail: detail})
	}
	return warnings
}

func modelBoneSet(value *kv3.Value) map[string]struct{} {
	rig, ok := value.Get("m_modelSkeleton")
	if !ok {
		return nil
	}
	namesValue, ok := rig.Get("m_boneName")
	if !ok {
		return nil
	}
	names, ok := namesValue.AsArray()
	if !ok {
		return nil
	}
	bones := make(map[string]struct{}, len(names))
	for _, value := range names {
		if name, ok := value.AsString(); ok {
			bones[name] = struct{}{}
		}
	}
	return bones
}

func resourceExists(name string, modPaths map[string]struct{}, game *source2.GameIndex) bool {
	if name == "" || strings.Contains(name, "#") {
		return true
	}
	path, ok := source2.ResourcePath(name)
	if !ok {
		return false
	}
	if _, ok := modPaths[strings.ToLower(path)]; ok {
		return true
	}
	if game == nil {
		return false
	}
	_, ok = game.Provider(name)
	return ok
}

func dedupeAssetWarnings(warnings []AssetWarning) []AssetWarning {
	seen := make(map[[3]string]struct{}, len(warnings))
	out := warnings[:0]
	for _, warning := range warnings {
		key := [3]string{warning.FilePath, string(warning.Kind), warning.Detail}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, warning)
	}
	return out
}

func warningDetail(kind camera.WarningKind) string {
	switch kind {
	case camera.WarningMissingInterface:
		return "the model lacks camera interface fields the current game expects"
	case camera.WarningMissingAttachments:
		return "the model lacks camera points the current game uses"
	case camera.WarningOutdatedAttachments:
		return "the camera attachment frame is outdated or uses a layout this version cannot migrate"
	}
	return ""
}

func isAnimationPath(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".vnmskel_c") ||
		strings.HasSuffix(p, ".vnmgraph_c") ||
		strings.HasSuffix(p, ".vnmclip_c")
}

// ModelPaths lists the normalized model paths a mod provides. Two mods in one
// load order providing the same path cannot be repaired independently: the
// overlay would make one mod's fixed file override the other's original.
func ModelPaths(vpkPaths []string) ([]string, error) {
	seen := make(map[string]struct{})
	var paths []string
	for _, vpkPath := range vpkPaths {
		archive, err := vpk.Open(vpkPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", vpkPath, err)
		}
		for _, entry := range archive.Entries {
			if !camera.IsModelPath(entry.FullPath) {
				continue
			}
			key := camera.NormalizePath(entry.FullPath)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			paths = append(paths, key)
		}
	}
	return paths, nil
}

// gameSkeletonReference reports whether the game model references exactly one
// NV skeleton and no mod-provided animation data is involved.
func gameSkeletonReference(model *kv3.Value) bool {
	refsV, ok := model.Get("m_vecNmSkeletonRefs")
	if !ok {
		return false
	}
	refs, ok := refsV.AsArray()
	if !ok || len(refs) != 1 {
		return false
	}
	name, ok := refs[0].AsString()
	if !ok {
		return false
	}
	normalized, ok := source2.ResourcePath(name)
	return ok && strings.HasSuffix(normalized, ".vnmskel_c")
}
