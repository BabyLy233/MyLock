// Package modcompat analyses installed mods against the current game files and
// produces compatibility findings and repaired bytes.
package modcompat

import (
	"fmt"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
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
	Repairs  []Repair
	Warnings []Warning
	// Fixed holds the repaired resource bytes by path; only populated in Fix mode.
	Fixed map[string][]byte
	// Checks lists the health categories this analysis covered.
	Checks []string
	// ModelsChecked is the number of model files compared with the game.
	ModelsChecked int
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
	var models []modelSource
	hasAnimationEntries := false
	for _, vpkPath := range vpkPaths {
		archive, err := vpk.Open(vpkPath)
		if err != nil {
			return Analysis{}, fmt.Errorf("reading %s: %w", vpkPath, err)
		}
		for _, entry := range archive.Entries {
			path := entry.FullPath
			if isAnimationPath(path) {
				hasAnimationEntries = true
			}
			if !camera.IsModelPath(path) {
				continue
			}
			key := camera.NormalizePath(path)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			models = append(models, modelSource{path: entry.FullPath, vpkPath: vpkPath, archive: archive})
		}
	}

	for _, source := range models {
		base, ok := game.GameBytes(source.path)
		if !ok {
			continue
		}
		modBytes, err := source.archive.ReadLimited(source.path, maxModelBytes)
		if err != nil {
			return Analysis{}, fmt.Errorf("reading %s from %s: %w", source.path, source.vpkPath, err)
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
	return analysis, nil
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
