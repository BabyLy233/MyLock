// Package modcompat analyses installed mods against the current game files and
// produces compatibility findings and repaired bytes.
package modcompat

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/animation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/material"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modelsounds"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
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

// RepairKind identifies a compatibility repair. The camera kinds keep their
// upstream names because receipts store the kind as a string.
type RepairKind string

const (
	RepairCameraAttachmentFrame    RepairKind = RepairKind(camera.RepairAttachmentFrame)
	RepairCameraInterface          RepairKind = RepairKind(camera.RepairInterface)
	RepairCameraControls           RepairKind = RepairKind(camera.RepairControls)
	RepairTaggedSounds             RepairKind = "TaggedSounds"
	RepairMaterialBindings         RepairKind = "MaterialBindings"
	RepairModelRelocation          RepairKind = "ModelRelocation"
	RepairAnimationInterface       RepairKind = "AnimationInterface"
	RepairAnimationSkeleton        RepairKind = "AnimationSkeleton"
	RepairAnimationSkeletonRebased RepairKind = "AnimationSkeletonRebased"
	RepairAnimationClipTracks      RepairKind = "AnimationClipTracks"
	RepairAnimationRigRebase       RepairKind = "AnimationRigRebase"
	// Data repairs keep the original serialized names.
	RepairHeroDevelopmentState RepairKind = RepairKind(vdata.RepairHeroDevelopmentState)
	RepairInheritedDefinition  RepairKind = RepairKind(vdata.RepairInheritedDefinition)
	RepairRetainedDependency   RepairKind = RepairKind(vdata.RepairRetainedDependency)
	RepairCurrentReference     RepairKind = RepairKind(vdata.RepairCurrentReference)
	RepairEnumMapEntry         RepairKind = RepairKind(vdata.RepairEnumMapEntry)
	RepairRestoredGameData     RepairKind = RepairKind(vdata.RepairRestoredGameData)
)

// Repair is one safe repair the analyzer can apply.
type Repair struct {
	ModID     string
	SourceVPK string
	FilePath  string
	Kind      RepairKind
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
	// AssetsChecked is the number of model, mesh, skeleton, material and
	// game-data table resources that received the structural checks.
	AssetsChecked int
	// ScopeIncomplete marks a mod whose files another enabled mod also
	// provides: its findings cannot be repaired on its own.
	ScopeIncomplete bool
	// UnloadedModels are mod-only models nothing loaded references. Their
	// warnings are left out of review and mod health.
	UnloadedModels []string
	// PropModels are warned models that are not a hero's model. Hero camera,
	// animation and model-mapping warnings on them do not affect mod health.
	PropModels []string
	// DataRepairs are merged-table repairs; DataWarnings are table problems
	// without a proven repair. Both map to the gameData health category.
	DataRepairs  []vdata.DataRepair
	DataWarnings []vdata.DataWarning
	// HudChecks compares mod Panorama layouts with the installed game's. They
	// are advisory: a removed panel can be intentional.
	HudChecks []HudCheck
}

// SupportedChecks are the health categories the analyzer covers today.
var SupportedChecks = []string{"cameraAim", "crashRisk", "animations", "appearance", "model", "gameData"}

type modelSource struct {
	path    string
	vpkPath string
	archive *vpk.Archive
}

// effectiveModel is the bytes the game will load for a path after repairs.
type effectiveModel struct {
	data    []byte
	vpkPath string
}

type materialCandidate struct {
	path    string
	vpkPath string
}

// Analyze compares every model in the mod's VPKs with the current game model.
// VPK order is load order: the first VPK providing a path wins.
func Analyze(modID string, vpkPaths []string, game *source2.GameIndex, scope Scope, mode Mode) (Analysis, error) {
	var analysis Analysis
	analysis.Checks = append(analysis.Checks, SupportedChecks...)
	if mode == Fix {
		analysis.Fixed = make(map[string][]byte)
	}

	seen := make(map[string]struct{})
	modPaths := make(map[string]struct{})
	modArchives := make(map[string]*vpk.Archive)
	modVPKs := make(map[string]string)
	var materialCandidates []materialCandidate
	seenMaterials := make(map[string]struct{})
	var models []modelSource
	var assets []modelSource
	effective := make(map[string]effectiveModel)
	hasAnimationEntries := false
	hasAnimationOverrides := false
	for _, vpkPath := range vpkPaths {
		archive, err := vpk.Open(vpkPath)
		if err != nil {
			return Analysis{}, fmt.Errorf("reading %s: %w", vpkPath, err)
		}
		for _, entry := range archive.Entries {
			path := entry.FullPath
			normalized := camera.NormalizePath(path)
			modPaths[normalized] = struct{}{}
			if _, ok := modArchives[normalized]; !ok {
				modArchives[normalized] = archive
				modVPKs[normalized] = vpkPath
			}
			if _, ok := material.SourceRole(normalized); ok {
				if _, dup := seenMaterials[normalized]; !dup {
					seenMaterials[normalized] = struct{}{}
					materialCandidates = append(materialCandidates, materialCandidate{
						path: normalized, vpkPath: vpkPath,
					})
				}
			}
			if isAnimationPath(path) {
				hasAnimationEntries = true
				if !strings.HasSuffix(normalized, ".vnmskel_c") ||
					strings.HasPrefix(normalized, "animations/") || strings.HasPrefix(normalized, "animgraphs/") {
					hasAnimationOverrides = true
				}
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
	exists := func(name string) bool {
		if resourceExists(name, modPaths, game) {
			return true
		}
		path, ok := source2.ResourcePath(name)
		if !ok {
			return false
		}
		if _, ok := scope.OtherModelPaths[path]; ok {
			return true
		}
		_, ok = scope.OtherAnimationPaths[path]
		return ok
	}
	// readResource resolves resources the way the load order does: the mod's
	// first VPK wins, then the game. A missing resource is (nil, nil).
	readResource := func(name string) ([]byte, error) {
		path, ok := source2.ResourcePath(name)
		if !ok {
			return nil, nil
		}
		if archive, ok := modArchives[strings.ToLower(path)]; ok {
			return archive.ReadLimited(path, maxModelBytes)
		}
		if game != nil {
			if data, ok := game.GameBytes(path); ok {
				return data, nil
			}
		}
		return nil, nil
	}

	repairedPaths := make(map[string]struct{})
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
		fixed := modBytes
		changed := false
		if hasBase {
			analysis.ModelsChecked++
			allowControls := func(vanilla *kv3.Value) bool {
				return !hasAnimationEntries && gameSkeletonReference(vanilla)
			}
			result := camera.Analyze(modBytes, base, camera.ModelOptions{AllowControls: allowControls})
			for _, kind := range result.RepairKinds {
				analysis.Repairs = append(analysis.Repairs, Repair{
					ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path,
					Kind: RepairKind(kind),
				})
			}
			for _, kind := range result.Warnings {
				analysis.Warnings = append(analysis.Warnings, Warning{
					ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path, Kind: kind, Detail: warningDetail(kind),
				})
			}
			// The analyzers inspect the repaired bytes, as the upstream overlay
			// does: warnings must describe what the game will actually load.
			if result.Repairable {
				if repaired, _, _, ok := camera.Repair(modBytes, base, camera.ModelOptions{AllowControls: allowControls}); ok {
					fixed = repaired
					changed = true
				}
			}
			soundFixed, soundChanged, err := modelsounds.Repair(fixed, base, readResource)
			if err != nil {
				return Analysis{}, fmt.Errorf("reading tagged sounds for %s: %w", source.path, err)
			}
			if soundChanged {
				fixed = soundFixed
				changed = true
				analysis.Repairs = append(analysis.Repairs, Repair{
					ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path, Kind: RepairTaggedSounds,
				})
			}
		}
		effective[camera.NormalizePath(source.path)] = effectiveModel{data: fixed, vpkPath: source.vpkPath}
		analysis.AssetWarnings = append(analysis.AssetWarnings, structuralModelWarnings(
			modID, source.vpkPath, source.path, fixed, base, hasBase, exists)...)
		if changed {
			repairedPaths[source.path] = struct{}{}
		}
		if mode == Fix && changed {
			analysis.Fixed[source.path] = fixed
		}
	}

	relocated := applyRelocation(&analysis, modID, models, game, modPaths, modArchives,
		readResource, exists, hasAnimationEntries, scope, repairedPaths, effective, mode)
	applyMaterialBindings(&analysis, modID, materialCandidates, readResource, game, repairedPaths, effective, mode)
	if err := applyData(&analysis, modID, modPaths, modArchives, modVPKs, game, scope, mode); err != nil {
		return Analysis{}, err
	}
	if err := applyLocalization(&analysis, modID, modPaths, modArchives, modVPKs, game); err != nil {
		return Analysis{}, err
	}
	if err := applyHud(&analysis, modID, modPaths, modArchives, modVPKs, game); err != nil {
		return Analysis{}, err
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
			_, competing := scope.OtherAnimationPaths[path]
			data = repairSkeleton(&analysis, modID, source, data, game,
				!hasAnimationOverrides && len(scope.OtherAnimationPaths) == 0,
				scope.Unknown || competing, mode, effective, repairedPaths)
			analysis.AssetWarnings = append(analysis.AssetWarnings,
				skeletonWarnings(modID, source.vpkPath, source.path, data)...)
		case strings.HasSuffix(path, ".vmesh_c"):
			analysis.AssetWarnings = append(analysis.AssetWarnings,
				meshAssetWarnings(modID, source.vpkPath, source.path, data)...)
		default:
			analysis.AssetWarnings = append(analysis.AssetWarnings,
				materialAssetWarnings(modID, source.vpkPath, source.path, data, game, exists)...)
		}
	}
	if err := applyClipTracks(&analysis, modID, models, modVPKs, readResource, game,
		scope, mode, effective, repairedPaths); err != nil {
		return Analysis{}, err
	}
	applyAnimationContract(&analysis, modID, effective, readResource, game, modArchives, scope)
	if unloaded, ok := unloadedModels(game, modPaths, modArchives, readResource, relocated); ok {
		analysis.UnloadedModels = unloaded
	}
	warnedPaths := make([]string, 0, len(analysis.AssetWarnings)+len(analysis.Warnings))
	for _, warning := range analysis.AssetWarnings {
		warnedPaths = append(warnedPaths, warning.FilePath)
	}
	for _, warning := range analysis.Warnings {
		warnedPaths = append(warnedPaths, warning.FilePath)
	}
	analysis.PropModels = propModels(game, readResource, warnedPaths, relocated)
	return analysis, nil
}

// repairSkeleton refreshes a skeleton whose rig is semantically equal to the
// current game skeleton. The game bytes replace the mod's compiler-layout
// differences; skeletons that need a rebase are left to the caller's warning.
// The evidence path is not ported: without proof that animation overrides do
// not sample this skeleton, only a proof-free structural refresh is safe.
func repairSkeleton(analysis *Analysis, modID string, source modelSource, data []byte,
	game *source2.GameIndex, extension, blocked bool, mode Mode, effective map[string]effectiveModel,
	repairedPaths map[string]struct{}) []byte {
	if game == nil || !refreshableSkeletonPath(source.path) {
		return data
	}
	base, ok := game.GameBytes(source.path)
	if !ok {
		return data
	}
	if blocked || !extension {
		if skeletonBonesDiffer(data, base) {
			skeletonUnverifiedWarning(analysis, modID, source)
		}
		return data
	}
	fixed, ok := animation.RefreshSkeleton(data, base, extension)
	kind := RepairAnimationSkeleton
	if !ok {
		fixed, ok = animation.RebaseSkeleton(data, base, extension)
		kind = RepairAnimationSkeletonRebased
	}
	if ok && bytes.Equal(fixed, data) {
		return data
	}
	if ok {
		path := camera.NormalizePath(source.path)
		effective[path] = effectiveModel{data: fixed, vpkPath: source.vpkPath}
		repairedPaths[source.path] = struct{}{}
		analysis.Repairs = append(analysis.Repairs, Repair{
			ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path,
			Kind: kind,
		})
		if mode == Fix {
			analysis.Fixed[source.path] = fixed
		}
		return fixed
	}
	if skeletonBonesDiffer(data, base) {
		skeletonUnverifiedWarning(analysis, modID, source)
	}
	return data
}

func skeletonUnverifiedWarning(analysis *Analysis, modID string, source modelSource) {
	analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
		ModID: modID, SourceVPK: source.vpkPath, FilePath: source.path,
		Kind:   compat.WarningUnverifiedSkeleton,
		Detail: "Bone hierarchy, reference poses, metadata, or competing mod settings cannot be safely rebased",
	})
}

// refreshableSkeletonPath mirrors the upstream skeleton source filter.
func refreshableSkeletonPath(name string) bool {
	path := camera.NormalizePath(name)
	if !strings.HasPrefix(path, "models/") || !strings.HasSuffix(path, ".vnmskel_c") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// skeletonBonesDiffer reports the only skeleton mismatch the upstream repair
// reports on its own: a changed bone name list cannot be refreshed without
// proving the animation dependencies against it.
func skeletonBonesDiffer(source, base []byte) bool {
	_, old, _, err := camera.DecodeCompiled(source)
	if err != nil {
		return false
	}
	_, current, _, err := camera.DecodeCompiled(base)
	if err != nil {
		return false
	}
	oldIDs, oldOK := old.Get("m_boneIDs")
	currentIDs, currentOK := current.Get("m_boneIDs")
	if !oldOK || !currentOK {
		return oldOK != currentOK
	}
	return !camera.RuntimeValuesEqual(oldIDs, currentIDs)
}

// materialSnapshot resolves resources in the mod's load order for the material
// recipe.
type materialSnapshot struct {
	game *source2.GameIndex
	read func(string) ([]byte, error)
}

func (s materialSnapshot) GameBytes(name string) ([]byte, bool, error) {
	if s.game == nil {
		return nil, false, nil
	}
	data, ok := s.game.GameBytes(name)
	return data, ok, nil
}

func (s materialSnapshot) Resolve(name string) ([]byte, error) {
	return s.read(name)
}

// applyMaterialBindings runs the reviewed static-glow migration. A partial role
// set, split source archives or any failed precondition yields warnings on the
// authored materials and never half a repair.
func applyMaterialBindings(analysis *Analysis, modID string, candidates []materialCandidate, read func(string) ([]byte, error), game *source2.GameIndex, repairedPaths map[string]struct{}, effective map[string]effectiveModel, mode Mode) {
	if len(candidates) == 0 {
		return
	}
	warn := func(detail string) {
		for _, candidate := range candidates {
			analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
				ModID: modID, SourceVPK: candidate.vpkPath, FilePath: candidate.path,
				Kind: compat.WarningMaterialMapping, Detail: detail,
			})
		}
	}
	if len(candidates) != 4 {
		warn("The four material roles must resolve from one source archive; partial or competing overrides cannot be migrated")
		return
	}
	sourceVPK := candidates[0].vpkPath
	for _, candidate := range candidates[1:] {
		if candidate.vpkPath != sourceVPK {
			warn("The four material roles must resolve from one source archive; partial or competing overrides cannot be migrated")
			return
		}
	}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.path)
	}
	generated, err := material.BuildMinionGlow(
		materialSnapshot{game: game, read: read}, paths)
	if err != nil {
		warn(err.Error())
		return
	}
	for path := range generated {
		if _, taken := repairedPaths[path]; taken {
			warn("Another compatibility repair owns " + path)
			return
		}
	}
	for path, data := range generated {
		if mode == Fix {
			analysis.Fixed[path] = data
		}
		if strings.HasSuffix(path, ".vmdl_c") {
			effective[path] = effectiveModel{data: data, vpkPath: sourceVPK}
			analysis.Repairs = append(analysis.Repairs, Repair{
				ModID: modID, SourceVPK: sourceVPK, FilePath: path,
				Kind: RepairMaterialBindings,
			})
		}
	}
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
			if modelsounds.Missing(&value, &vanilla) {
				add(compat.WarningMissingSoundBindings, modelsounds.Settings)
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
// current game material and checks that every RERL dependency still resolves.
// It reports differences only; nothing is rewritten.
func materialAssetWarnings(modID, vpkPath, path string, data []byte, game *source2.GameIndex, exists func(string) bool) []AssetWarning {
	var warnings []AssetWarning
	add := func(kind compat.WarningKind, detail string) {
		warnings = append(warnings, AssetWarning{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: kind, Detail: detail})
	}
	_, source, _, err := camera.DecodeCompiled(data)
	if err != nil {
		return []AssetWarning{{ModID: modID, SourceVPK: vpkPath, FilePath: path,
			Kind: compat.WarningUnreadableResource, Detail: err.Error()}}
	}
	if game != nil {
		if base, ok := game.GameBytes(path); ok {
			_, current, _, err := camera.DecodeCompiled(base)
			if err != nil {
				add(compat.WarningMaterialInterface,
					"Current material interface could not be decoded: "+err.Error())
			} else {
				details, err := compat.MaterialDifferences(&source, &current)
				if err != nil {
					add(compat.WarningMaterialInterface, err.Error())
				}
				for _, detail := range details {
					add(compat.WarningMaterialInterface, detail)
				}
			}
		}
	}
	// A mod-only material has no current reference to compare against, but its
	// own dependencies are still resolved in the mod's load order.
	if parsed, err := resource.Parse(data); err == nil {
		if _, ok := parsed.FindBlock(resource.BlockTypeRERL); ok {
			references, err := parsed.References()
			if err != nil {
				add(compat.WarningUnreadableResource, err.Error())
			}
			for _, reference := range references {
				if !exists(reference.Path) {
					add(compat.WarningMissingResource, reference.Path)
				}
			}
		}
	}
	return dedupeAssetWarnings(warnings)
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
	p := camera.NormalizePath(path)
	if strings.HasPrefix(p, "animations/") || strings.HasPrefix(p, "animgraphs/") {
		return true
	}
	for _, suffix := range []string{
		".vanim_c", ".vnmskel_c", ".vnmgraph_c", ".vnmclip_c", ".vagrp_c", ".vseq_c", ".vanmgrph_c",
	} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
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
