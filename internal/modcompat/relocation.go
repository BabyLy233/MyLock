package modcompat

import (
	"fmt"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/animation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/history"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/modelsounds"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/relocation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// animationSource resolves animation resources in the mod's load order and
// reports whether a mod provides them. The proof rejects mod-owned skeletons
// and graphs.
type animationSource struct {
	read        func(string) ([]byte, error)
	modArchives map[string]*vpk.Archive
}

func (s animationSource) Resolve(name string) ([]byte, error) {
	return s.read(name)
}

func (s animationSource) FromMod(name string) bool {
	path, ok := source2.ResourcePath(name)
	if !ok {
		return false
	}
	_, ok = s.modArchives[strings.ToLower(path)]
	return ok
}

// applyRelocation moves a mod's retired hero model onto the current hero model
// path when the archived bindings and the animation proof agree. The relocation
// is conservative: any competing model, modified registry, animation asset or
// unverified structural difference leaves the model where it is.
func applyRelocation(analysis *Analysis, modID string, models []modelSource, game *source2.GameIndex,
	modPaths map[string]struct{}, modArchives map[string]*vpk.Archive,
	read func(string) ([]byte, error), exists func(string) bool,
	hasAnimationEntries bool, scope Scope, repairedPaths map[string]struct{}, mode Mode) {
	if game == nil || len(models) == 0 {
		return
	}
	registryBytes, ok := game.GameBytes(relocation.HeroesVDataPath)
	if !ok {
		return
	}
	_, registry, _, err := camera.DecodeCompiled(registryBytes)
	if err != nil {
		return
	}
	registryBlocked := scope.RegistryModified || scope.Unknown
	var overridden map[string]struct{}
	if archive, modified := modArchives[relocation.HeroesVDataPath]; modified {
		modRegistryBytes, err := archive.ReadLimited(relocation.HeroesVDataPath, maxModelBytes)
		if err != nil {
			registryBlocked = true
		} else if _, effective, _, err := camera.DecodeCompiled(modRegistryBytes); err != nil {
			registryBlocked = true
		} else {
			overridden = changedRegistryTargets(&registry, &effective)
		}
	}
	heroModels := relocation.RegistryHeroModels(&registry)
	observed := func(row, model string) bool {
		return history.Embedded().ObservedStringField(relocation.HeroesVDataPath, row, "m_strModelName", model)
	}
	resolve := animationSource{read: read, modArchives: modArchives}

	warn := func(source modelSource, path, detail string) {
		analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
			ModID: modID, SourceVPK: source.vpkPath, FilePath: path,
			Kind: compat.WarningModelMapping, Detail: detail,
		})
	}

	for _, source := range models {
		path := camera.NormalizePath(source.path)
		if _, ok := heroModels[path]; ok {
			continue
		}
		targets := relocation.RegistryTargets(&registry, path, observed)
		if len(targets) == 0 {
			continue
		}
		if len(targets) > 1 {
			warn(source, path, fmt.Sprintf(
				"Historical hero bindings associate this old model with multiple current models: %s",
				strings.Join(targets, ", ")))
			continue
		}
		target := targets[0]
		if _, ok := modPaths[target]; ok {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s is blocked because the mod already ships the current model", target))
			continue
		}
		if registryBlocked {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s is blocked by modified hero model bindings", target))
			continue
		}
		if _, ok := overridden[target]; ok {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s is blocked by modified hero model bindings", target))
			continue
		}
		if _, ok := scope.OtherModelPaths[target]; ok {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s is blocked by a competing model in another mod", target))
			continue
		}
		if _, taken := repairedPaths[target]; taken {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s is blocked by another compatibility repair", target))
			continue
		}
		targetBase, ok := game.GameBytes(target)
		if !ok {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s is blocked because the current hero model is unavailable", target))
			continue
		}
		if hasAnimationEntries {
			warn(source, path, fmt.Sprintf(
				"Historical hero binding maps this model to current hero model %s, but animation preservation could not be verified. Enabled animation overrides require dependency proof before restoring bindings", target))
			continue
		}
		modBytes, err := source.archive.ReadLimited(source.path, maxModelBytes)
		if err != nil {
			analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
				ModID: modID, SourceVPK: source.vpkPath, FilePath: path,
				Kind: compat.WarningUnreadableResource, Detail: "DATA",
			})
			continue
		}
		_, old, _, oldErr := camera.DecodeCompiled(modBytes)
		_, current, _, currentErr := camera.DecodeCompiled(targetBase)
		if oldErr != nil || currentErr != nil {
			warn(source, path, fmt.Sprintf(
				"Historical hero binding maps this model to current hero model %s, but animation preservation could not be verified. Invalid model data", target))
			continue
		}
		available := func(name string) bool {
			bytes, err := resolve.Resolve(name)
			return err == nil && bytes != nil
		}
		restored := false
		beforeAnimation := modBytes
		if prepared, ok := animation.RestoreBindings(modBytes, targetBase, resolve, available); ok {
			beforeAnimation = prepared
			restored = true
		} else if err := animation.ProveCurrentBindings(&old, &current, false, resolve); err != nil {
			warn(source, path, fmt.Sprintf(
				"Historical hero binding maps this model to current hero model %s, but animation preservation could not be verified. %s",
				target, err))
			continue
		}
		fixed := beforeAnimation
		allowControls := func(vanilla *kv3.Value) bool {
			return !hasAnimationEntries && gameSkeletonReference(vanilla)
		}
		cameraResult := camera.Analyze(fixed, targetBase, allowControls)
		if cameraResult.Repairable {
			if repaired, _, _, ok := camera.Repair(fixed, targetBase, allowControls); ok {
				fixed = repaired
			}
		}
		soundFixed, soundChanged, err := modelsounds.Repair(fixed, targetBase, read)
		if err != nil {
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s remains unverified: %s", target, err))
			continue
		}
		if soundChanged {
			fixed = soundFixed
		}
		warnings := structuralModelWarnings(modID, source.vpkPath, target, fixed, targetBase, true, exists)
		var advisories, blocking []AssetWarning
		for _, warning := range warnings {
			switch warning.Kind {
			case compat.WarningMissingSoundBindings, compat.WarningMissingCameraAttachments:
				advisories = append(advisories, warning)
			default:
				blocking = append(blocking, warning)
			}
		}
		if len(blocking) > 0 {
			details := make([]string, 0, len(blocking))
			for _, warning := range blocking {
				details = append(details, warning.Detail)
			}
			warn(source, path, fmt.Sprintf(
				"Model relocation to %s remains unverified: %s", target, strings.Join(details, "; ")))
			continue
		}
		analysis.AssetWarnings = append(analysis.AssetWarnings, advisories...)
		for _, kind := range cameraResult.RepairKinds {
			analysis.Repairs = append(analysis.Repairs, Repair{
				ModID: modID, SourceVPK: source.vpkPath, FilePath: target,
				Kind: RepairKind(kind),
			})
		}
		if soundChanged {
			analysis.Repairs = append(analysis.Repairs, Repair{
				ModID: modID, SourceVPK: source.vpkPath, FilePath: target,
				Kind: RepairTaggedSounds,
			})
		}
		if restored {
			analysis.Repairs = append(analysis.Repairs, Repair{
				ModID: modID, SourceVPK: source.vpkPath, FilePath: target,
				Kind: RepairAnimationInterface,
			})
		}
		analysis.Repairs = append(analysis.Repairs, Repair{
			ModID: modID, SourceVPK: source.vpkPath, FilePath: target,
			Kind: RepairModelRelocation,
		})
		repairedPaths[target] = struct{}{}
		if mode == Fix {
			analysis.Fixed[target] = fixed
		}
	}
}

// changedRegistryTargets lists the game registry targets a mod registry would
// replace. A row that the effective registry no longer defines is included.
func changedRegistryTargets(registry, effective *kv3.Value) map[string]struct{} {
	overridden := make(map[string]struct{})
	rows, ok := registry.AsObject()
	if !ok {
		return overridden
	}
	for _, row := range rows {
		target, ok := registryModel(&row.Value)
		if !ok {
			continue
		}
		effectiveRow, ok := effective.Get(row.Key)
		if !ok {
			overridden[target] = struct{}{}
			continue
		}
		effectiveTarget, ok := registryModel(effectiveRow)
		if !ok || effectiveTarget != target {
			overridden[target] = struct{}{}
		}
	}
	return overridden
}

func registryModel(row *kv3.Value) (string, bool) {
	name, ok := row.Get("m_strModelName")
	if !ok {
		return "", false
	}
	model, ok := name.AsString()
	if !ok {
		return "", false
	}
	return relocation.ResourcePath(model)
}
