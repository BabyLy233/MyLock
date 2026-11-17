// Package modhealth stores per-mod compatibility verdicts and the cache that
// keeps them between runs.
package modhealth

import (
	"fmt"

	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
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
	Repairs           int       `json:"repairs"`
	Checks            []string  `json:"checks,omitempty"`
	Warnings          int       `json:"warnings"`
	NeedsUpdate       bool      `json:"needsUpdate"`
	FixSymptoms       []Symptom `json:"fixSymptoms,omitempty"`
	RemainingSymptoms []Symptom `json:"remainingSymptoms,omitempty"`
	Covered           bool      `json:"covered"`
	ScopeIncomplete   bool      `json:"scopeIncomplete,omitempty"`
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
	ModID             string    `json:"modId"`
	Status            Status    `json:"status"`
	Symptoms          []Symptom `json:"symptoms"`
	FixSymptoms       []Symptom `json:"fixSymptoms"`
	RemainingSymptoms []Symptom `json:"remainingSymptoms"`
	Repairs           int       `json:"repairs"`
	Warnings          int       `json:"warnings"`
	Checks            []string  `json:"checks,omitempty"`
	GameBuild         *uint32   `json:"gameBuild,omitempty"`
	CheckedAt         string    `json:"checkedAt"`
	Source            Source    `json:"source"`
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
	for _, repair := range analysis.Repairs {
		if symptom := repairSymptom(repair.Kind); symptom != "" {
			fixSet[symptom] = struct{}{}
		}
	}
	for _, warning := range analysis.Warnings {
		if symptom := warningSymptom(warning.Kind); symptom != "" {
			remaining[symptom] = struct{}{}
		}
	}
	return Findings{
		Repairs:           len(analysis.Repairs),
		Checks:            append([]string(nil), analysis.Checks...),
		Warnings:          len(analysis.Warnings),
		NeedsUpdate:       len(remaining) > 0,
		FixSymptoms:       sortedSymptoms(fixSet),
		RemainingSymptoms: sortedSymptoms(remaining),
		Covered:           analysis.ModelsChecked > 0,
	}
}

func repairSymptom(kind camera.RepairKind) Symptom {
	switch kind {
	case camera.RepairAttachmentFrame, camera.RepairInterface, camera.RepairControls:
		return SymptomCameraAim
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
