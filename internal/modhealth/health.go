// Package modhealth stores per-mod compatibility verdicts and the cache that
// keeps them between runs.
package modhealth

import (
	"fmt"
	"strings"

	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
)

// Status is the verdict shown for a mod.
type Status string

const (
	StatusWorks         Status = "works"
	StatusFixable       Status = "fixable"
	StatusPartlyFixable Status = "partlyFixable"
	StatusFixed         Status = "fixed"
	StatusOriginalFiles Status = "originalFiles"
	StatusNeedsUpdate   Status = "needsUpdate"
	StatusNotCovered    Status = "notCovered"
)

// Symptom is what a player notices in game.
type Symptom string

const (
	SymptomCameraAim  Symptom = "cameraAim"
	SymptomAnimations Symptom = "animations"
	SymptomAppearance Symptom = "appearance"
	SymptomModel      Symptom = "model"
	SymptomGameData   Symptom = "gameData"
	SymptomCrashRisk  Symptom = "crashRisk"
)

// AllSymptoms lists every category the health model understands. Categories
// not in a mod's Checks are not covered yet.
var AllSymptoms = []Symptom{
	SymptomCameraAim, SymptomAnimations, SymptomAppearance,
	SymptomModel, SymptomGameData, SymptomCrashRisk,
}

// Source says whether the verdict came from a standalone check or the combined
// launch analysis. Only standalone is implemented today.
type Source string

const (
	SourceStandalone Source = "standalone"
	SourceLaunch     Source = "launch"
)

// Choice is the player's compatibility decision stored in the manifest.
type Choice int

const (
	ChoiceUnset Choice = iota
	ChoiceEnabled
	ChoiceOriginalFiles
)

// Findings are the cached details behind a verdict.
type Findings struct {
	Repairs           int                  `json:"repairs"`
	Checks            []string             `json:"checks,omitempty"`
	Warnings          int                  `json:"warnings"`
	NeedsUpdate       bool                 `json:"needsUpdate"`
	FixSymptoms       []Symptom            `json:"fixSymptoms,omitempty"`
	RemainingSymptoms []Symptom            `json:"remainingSymptoms,omitempty"`
	Covered           bool                 `json:"covered"`
	ScopeIncomplete   bool                 `json:"scopeIncomplete,omitempty"`
	HudChecks         []modcompat.HudCheck `json:"hudChecks,omitempty"`
}

// Status applies the original branch order: repairs first, then unresolved
// warnings, then coverage.
func (f Findings) Status(choice Choice) Status {
	if f.Repairs > 0 {
		switch choice {
		case ChoiceEnabled:
			return StatusFixed
		case ChoiceOriginalFiles:
			return StatusOriginalFiles
		default:
			if f.NeedsUpdate {
				return StatusPartlyFixable
			}
			return StatusFixable
		}
	}
	if f.NeedsUpdate {
		return StatusNeedsUpdate
	}
	if !f.Covered || f.ScopeIncomplete {
		return StatusNotCovered
	}
	return StatusWorks
}

// Health is one mod's verdict.
type Health struct {
	ModID             string               `json:"modId"`
	Status            Status               `json:"status"`
	Symptoms          []Symptom            `json:"symptoms"`
	FixSymptoms       []Symptom            `json:"fixSymptoms"`
	RemainingSymptoms []Symptom            `json:"remainingSymptoms"`
	Repairs           int                  `json:"repairs"`
	Warnings          int                  `json:"warnings"`
	Checks            []string             `json:"checks,omitempty"`
	ScopeIncomplete   bool                 `json:"scopeIncomplete,omitempty"`
	HudChecks         []modcompat.HudCheck `json:"hudChecks,omitempty"`
	OverlapDetails    []string             `json:"overlapDetails,omitempty"`
	GameBuild         *uint32              `json:"gameBuild,omitempty"`
	CheckedAt         string               `json:"checkedAt"`
	Source            Source               `json:"source"`
}

// Report is the list of verdicts for one profile.
type Report struct {
	GameBuild         *uint32  `json:"gameBuild,omitempty"`
	PreviousGameBuild *uint32  `json:"previousGameBuild,omitempty"`
	Mods              []Health `json:"mods"`
}

// FindingsFromAnalysis maps the camera analyzer's findings to health symptoms.
func FindingsFromAnalysis(analysis modcompat.Analysis) Findings {
	fixSet := make(map[Symptom]struct{})
	remaining := make(map[Symptom]struct{})
	unloaded := make(map[string]struct{}, len(analysis.UnloadedModels))
	for _, path := range analysis.UnloadedModels {
		unloaded[path] = struct{}{}
	}
	props := make(map[string]struct{}, len(analysis.PropModels))
	for _, path := range analysis.PropModels {
		props[path] = struct{}{}
	}
	for _, repair := range analysis.Repairs {
		if symptom := repairSymptom(repair.Kind); symptom != "" {
			fixSet[symptom] = struct{}{}
		}
	}
	warnings := 0
	for range analysis.DataRepairs {
		fixSet[SymptomGameData] = struct{}{}
	}
	for range analysis.DataWarnings {
		warnings++
		remaining[SymptomGameData] = struct{}{}
	}
	for _, warning := range analysis.Warnings {
		if _, skipped := unloaded[camera.NormalizePath(warning.FilePath)]; skipped {
			continue
		}
		warnings++
		if _, prop := props[camera.NormalizePath(warning.FilePath)]; prop && heroOnlyCamera(warning.Kind) {
			continue
		}
		if symptom := warningSymptom(warning.Kind); symptom != "" {
			remaining[symptom] = struct{}{}
		}
	}
	for _, warning := range analysis.AssetWarnings {
		path := camera.NormalizePath(warning.FilePath)
		if _, skipped := unloaded[path]; skipped {
			continue
		}
		warnings++
		if informationalAssetWarning(warning) {
			continue
		}
		if _, prop := props[path]; prop && heroOnlyAsset(warning.Kind) {
			continue
		}
		if symptom := assetWarningSymptom(warning.Kind); symptom != "" {
			remaining[symptom] = struct{}{}
		}
	}
	return Findings{
		Repairs:           len(analysis.Repairs),
		Checks:            append([]string(nil), analysis.Checks...),
		Warnings:          warnings,
		NeedsUpdate:       len(remaining) > 0,
		FixSymptoms:       sortedSymptoms(fixSet),
		RemainingSymptoms: sortedSymptoms(remaining),
		Covered:           analysis.ModelsChecked > 0 || analysis.AssetsChecked > 0,
		ScopeIncomplete:   analysis.ScopeIncomplete,
		HudChecks:         analysis.HudChecks,
	}
}

// heroOnlyCamera reports camera findings that do not apply to a non-hero model.
func heroOnlyCamera(kind camera.WarningKind) bool {
	switch kind {
	case camera.WarningMissingInterface, camera.WarningMissingAttachments,
		camera.WarningOutdatedAttachments:
		return true
	}
	return false
}

// heroOnlyAsset reports hero interface and mapping findings that do not apply
// to a non-hero model, such as a particle's model.
func heroOnlyAsset(kind compat.WarningKind) bool {
	switch kind {
	case compat.WarningMissingCameraInterface, compat.WarningMissingCameraAttachments,
		compat.WarningOutdatedCameraAttachments, compat.WarningMissingAnimationInterface,
		compat.WarningModelMapping:
		return true
	}
	return false
}

// informationalAssetWarning reports differences that only change how a mod
// looks or that merely describe retired references. They stay listed in review
// but do not mark the mod as needing an update.
func informationalAssetWarning(warning modcompat.AssetWarning) bool {
	detail := strings.ToLower(warning.Detail)
	switch warning.Kind {
	case compat.WarningMaterialInterface:
		return true
	case compat.WarningMissingResource:
		return strings.HasSuffix(detail, ".vtex") || strings.HasSuffix(detail, ".vtex_c") ||
			strings.HasSuffix(detail, ".vanmgrph")
	case compat.WarningInvalidAttachment, compat.WarningMissingCameraAttachments:
		return strings.HasPrefix(warning.Detail, "portrait_")
	}
	return false
}

func repairSymptom(kind modcompat.RepairKind) Symptom {
	switch kind {
	case modcompat.RepairCameraAttachmentFrame, modcompat.RepairCameraInterface, modcompat.RepairCameraControls:
		return SymptomCameraAim
	case modcompat.RepairTaggedSounds, modcompat.RepairAnimationInterface,
		modcompat.RepairAnimationSkeleton, modcompat.RepairAnimationSkeletonRebased,
		modcompat.RepairAnimationClipTracks, modcompat.RepairAnimationRigRebase:
		return SymptomAnimations
	case modcompat.RepairMaterialBindings:
		return SymptomAppearance
	case modcompat.RepairModelRelocation:
		return SymptomModel
	case modcompat.RepairHeroDevelopmentState, modcompat.RepairInheritedDefinition,
		modcompat.RepairRetainedDependency, modcompat.RepairCurrentReference,
		modcompat.RepairEnumMapEntry, modcompat.RepairRestoredGameData:
		return SymptomGameData
	}
	return ""
}

// assetWarningSymptom follows the original health mapping: structural faults
// and missing references are crash risks; camera and mapping warnings belong
// to their own categories.
func assetWarningSymptom(kind compat.WarningKind) Symptom {
	switch kind {
	case compat.WarningUnverifiedSkeleton, compat.WarningMissingAnimationInterface,
		compat.WarningAnimationMapping, compat.WarningMissingSoundBindings:
		return SymptomAnimations
	case compat.WarningOutdatedCameraAttachments, compat.WarningMissingCameraAttachments:
		return SymptomCameraAim
	case compat.WarningMaterialMapping, compat.WarningMaterialInterface:
		return SymptomAppearance
	case compat.WarningModelMapping, compat.WarningOverlappingModel:
		return SymptomModel
	case compat.WarningInvalidSkeleton, compat.WarningInvalidBoneRemapping,
		compat.WarningInvalidAttachment, compat.WarningInvalidHitbox,
		compat.WarningMissingResource, compat.WarningUnreadableResource,
		compat.WarningMissingCameraInterface:
		return SymptomCrashRisk
	}
	return ""
}

func warningSymptom(kind camera.WarningKind) Symptom {
	switch kind {
	case camera.WarningOutdatedAttachments, camera.WarningMissingAttachments:
		return SymptomCameraAim
	case camera.WarningMissingInterface:
		return SymptomCrashRisk
	}
	return ""
}

func sortedSymptoms(set map[Symptom]struct{}) []Symptom {
	out := make([]Symptom, 0, len(set))
	for _, symptom := range AllSymptoms {
		if _, ok := set[symptom]; ok {
			out = append(out, symptom)
		}
	}
	return out
}

// LimitedSymptoms returns the categories not checked for a mod.
func LimitedSymptoms(checks []string) []Symptom {
	checked := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		checked[check] = struct{}{}
	}
	var out []Symptom
	for _, symptom := range AllSymptoms {
		if _, ok := checked[string(symptom)]; !ok {
			out = append(out, symptom)
		}
	}
	return out
}

// ValidateStatus is used by tests to catch typos.
func ValidateStatus(status Status) error {
	switch status {
	case StatusWorks, StatusFixable, StatusPartlyFixable, StatusFixed,
		StatusOriginalFiles, StatusNeedsUpdate, StatusNotCovered:
		return nil
	}
	return fmt.Errorf("unknown status %q", status)
}
