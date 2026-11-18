package modhealth

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
)

// Analyzer is the per-mod analysis function; tests can replace it.
type Analyzer func(modID string, vpks []string, game *source2.GameIndex, scope modcompat.Scope, mode modcompat.Mode) (modcompat.Analysis, error)

// CheckMod is one installed mod to check.
type CheckMod struct {
	ID     string
	VPKs   []string
	Choice Choice
	// Enabled mods take part in the load order, so only they can overlap.
	Enabled bool
}

// CheckOptions controls one check run.
type CheckOptions struct {
	Force      bool
	Build      *uint32
	OnProgress func(checked, total int)
	// HistoryPath is the install's learned game-data history. The checker
	// loads it before analysing, learns the game tables each analysed mod
	// reads, and saves it back when that taught it something new. Empty
	// disables both learning and persistence.
	HistoryPath string
	// MountedFiles lists every archive the game loads, in load order,
	// including archives no mod lists. Nil builds it from the checked mods.
	// It decides which shared files the package may not rewrite.
	MountedFiles []modcompat.ModFiles
}

// CheckResult is one check's report and whether learning changed the saved
// history.
type CheckResult struct {
	Report         Report
	HistoryChanged bool
}

// Checker runs standalone checks and writes their verdicts to the cache.
type Checker struct {
	store   *Store
	analyze Analyzer
	mu      sync.Mutex
}

// NewChecker returns a Checker using the production analyzer.
func NewChecker(store *Store) *Checker {
	return &Checker{store: store, analyze: modcompat.Analyze}
}

// NewCheckerWithAnalyzer is used by tests.
func NewCheckerWithAnalyzer(store *Store, analyze Analyzer) *Checker {
	return &Checker{store: store, analyze: analyze}
}

// Check analyses the mods whose files changed since the cached verdict and
// returns the refreshed report. A mod whose files change during analysis is
// dropped from the cache rather than recorded against the wrong bytes.
func (c *Checker) Check(profile string, mods []CheckMod, game *source2.GameIndex, opts CheckOptions) (CheckResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Loaded lazily: a check that reuses every cached verdict must not touch
	// the history at all.
	var learned *modcompat.History
	history := func() *modcompat.History {
		if learned == nil {
			learned = modcompat.LoadHistory(opts.HistoryPath, opts.Build)
		}
		return learned
	}

	installed := make([]Installed, 0, len(mods))
	entries := make(map[string]Cached)
	files := make([]modcompat.ModFiles, 0, len(mods))
	for _, mod := range mods {
		if mod.Enabled {
			files = append(files, modcompat.ModFiles{ID: mod.ID, VPKs: mod.VPKs})
		}
	}
	scopes, scopeErr := modcompat.Scopes(files)
	protected, protectionErr := sharedProtection(mods, opts.MountedFiles)
	if protectionErr != nil {
		// A package cannot be proven safe without knowing who ships what, so
		// an opted-in mod is left unfixed instead of guessing.
		log.Printf("Could not resolve shared compatibility resources: %v", protectionErr)
		protected = make(map[string][]string)
		for _, mod := range mods {
			if mod.Enabled && mod.Choice == ChoiceEnabled {
				protected[mod.ID] = nil
			}
		}
	}
	type analyzed struct {
		key      HealthKey
		analysis modcompat.Analysis
	}
	analyses := make(map[string]analyzed)
	total := len(mods)
	checked := 0
	for _, mod := range mods {
		scope := scopes[mod.ID]
		if scopeErr != nil {
			scope = modcompat.Scope{Unknown: true}
		}
		key, err := Stamp(mod.VPKs, opts.Build, RepairRevision)
		if err != nil {
			checked++
			if opts.OnProgress != nil {
				opts.OnProgress(checked, total)
			}
			continue
		}
		key.Scope = scope.Fingerprint()
		installed = append(installed, Installed{ID: mod.ID, Key: key, Choice: mod.Choice})
		if !opts.Force {
			if cached, ok := c.store.Get(profile, mod.ID, key); ok {
				entries[mod.ID] = cached
				checked++
				if opts.OnProgress != nil {
					opts.OnProgress(checked, total)
				}
				continue
			}
		}
		scope.History = history()
		analysis, err := c.analyze(mod.ID, mod.VPKs, game, scope, modcompat.Detect)
		if err != nil {
			checked++
			if opts.OnProgress != nil {
				opts.OnProgress(checked, total)
			}
			continue
		}
		restamped, err := Stamp(mod.VPKs, opts.Build, RepairRevision)
		restamped.Scope = key.Scope
		if err != nil || !key.Equal(restamped) {
			_ = c.store.Forget(profile, mod.ID)
			checked++
			if opts.OnProgress != nil {
				opts.OnProgress(checked, total)
			}
			continue
		}
		analyses[mod.ID] = analyzed{key: key, analysis: analysis}
		checked++
		if opts.OnProgress != nil {
			opts.OnProgress(checked, total)
		}
	}
	for id, analyzed := range analyses {
		entries[id] = Cached{
			Key:       analyzed.key,
			Findings:  FindingsFromAnalysis(analyzed.analysis),
			CheckedAt: time.Now().UTC(),
			Source:    SourceStandalone,
		}
	}
	overlaps := modelOverlaps(mods)
	if err := c.store.Record(profile, opts.Build, entries); err != nil {
		return CheckResult{}, err
	}
	report := c.store.Report(profile, opts.Build, installed)
	applyOverlaps(&report, overlaps, protected)
	// Persist once, after the batch, so a crash cannot leave half a lesson.
	// A failure never fails the check: the same tables can be learned again.
	historyChanged := false
	if learned != nil && learned.Changed() && opts.HistoryPath != "" {
		if err := learned.Save(opts.HistoryPath); err != nil {
			log.Printf("Could not persist observed compatibility history: %v", err)
		} else {
			historyChanged = true
		}
	}
	return CheckResult{Report: report, HistoryChanged: historyChanged}, nil
}

// overlapSummary is one mod's view of the models more than one enabled mod
// provides: the paths, plus any animation files the load order would mix in
// from another mod.
type overlapSummary struct {
	paths   []string
	details []string
}

// applyOverlaps adds the cross-mod findings to the report without persisting
// them: whether a path is shared depends on the other installed mods, not on
// the bytes this mod's cached verdict was recorded for.
func applyOverlaps(report *Report, overlaps map[string]overlapSummary, protected map[string][]string) {
	for i := range report.Mods {
		health := &report.Mods[i]
		overlap, hasOverlap := overlaps[health.ModID]
		shared, protectedKnown := protected[health.ModID]
		if len(overlap.paths) == 0 && !protectedKnown {
			continue
		}
		health.ScopeIncomplete = true
		health.Warnings += len(overlap.paths) + len(shared)
		if hasOverlap {
			health.Symptoms = withSymptom(health.Symptoms, SymptomModel)
		}
		if len(overlap.details) > 3 {
			health.OverlapDetails = append(health.OverlapDetails, overlap.details[:3]...)
			health.OverlapDetails = append(health.OverlapDetails,
				fmt.Sprintf("另有 %d 处重叠模型混入了其它模组的动画文件", len(overlap.details)-3))
		} else {
			health.OverlapDetails = append(health.OverlapDetails, overlap.details...)
		}
		for _, path := range shared {
			switch {
			case strings.HasPrefix(path, "resource/localization/") && strings.HasSuffix(path, ".txt"),
				modcompat.IsDataTablePath(path):
				health.Symptoms = withSymptom(health.Symptoms, SymptomGameData)
			case camera.IsModelPath(path):
				health.Symptoms = withSymptom(health.Symptoms, SymptomModel)
			}
		}
		if health.Status == StatusWorks {
			health.Status = StatusNeedsUpdate
		}
	}
}

// sharedProtection reports the paths each opted-in mod may not repair because
// another mounted archive also ships them.
func sharedProtection(mods []CheckMod, mounted []modcompat.ModFiles) (map[string][]string, error) {
	if mounted == nil {
		mounted = make([]modcompat.ModFiles, 0, len(mods))
		for _, mod := range mods {
			if !mod.Enabled || len(mod.VPKs) == 0 {
				continue
			}
			mounted = append(mounted, modcompat.ModFiles{ID: mod.ID, VPKs: mod.VPKs})
		}
	}
	selected := make(map[string]struct{})
	for _, mod := range mods {
		if mod.Enabled && mod.Choice == ChoiceEnabled {
			selected[mod.ID] = struct{}{}
		}
	}
	scope, err := modcompat.NewRepairScope(mounted, selected)
	if err != nil {
		return nil, err
	}
	protected := make(map[string][]string)
	for _, mod := range mods {
		if !mod.Enabled || mod.Choice != ChoiceEnabled || len(mod.VPKs) == 0 {
			continue
		}
		paths, err := scope.ProtectedFor(mod.VPKs)
		if err != nil {
			return nil, err
		}
		if len(paths) > 0 {
			protected[mod.ID] = paths
		}
	}
	return protected, nil
}

// modelOverlaps lists the enabled mods that provide the same model path and,
// for the winning copy, the animation files the load order would take from a
// different mod. The detail is provenance evidence, not a verdict: the model
// can be fine, or can stretch when another mod's animation ships with it.
func modelOverlaps(mods []CheckMod) map[string]overlapSummary {
	enabled := make([]modcompat.ModFiles, 0, len(mods))
	for _, mod := range mods {
		if mod.Enabled && len(mod.VPKs) > 0 {
			enabled = append(enabled, modcompat.ModFiles{ID: mod.ID, VPKs: mod.VPKs})
		}
	}
	if len(enabled) < 2 {
		return nil
	}
	owners := make(map[string][]string)
	winners := make(map[string]string)
	vpks := make(map[string][]string, len(mods))
	for _, mod := range mods {
		if !mod.Enabled || len(mod.VPKs) == 0 {
			continue
		}
		vpks[mod.ID] = mod.VPKs
		paths, err := modcompat.ModelPaths(mod.VPKs)
		if err != nil {
			continue
		}
		for _, path := range paths {
			if _, taken := winners[path]; !taken {
				winners[path] = mod.ID
			}
			owners[path] = append(owners[path], mod.ID)
		}
	}
	overlaps := make(map[string]overlapSummary)
	for path, ids := range owners {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		detail := ""
		if winner := winners[path]; winner != "" {
			owner := modcompat.ModFiles{ID: winner, VPKs: vpks[winner]}
			mixed, err := modcompat.MixedAnimationDependencies(owner, enabled, path)
			if err != nil {
				log.Printf("Could not inspect overlapping model dependencies for %s: %v", path, err)
				detail = "按当前加载顺序，部分动画依赖无法检查；如果模型拉伸或扭曲，先只启用需要的皮肤再测试。"
			} else if len(mixed) > 0 {
				detail = fmt.Sprintf("按当前加载顺序，这个模型还会混入其它模组的动画文件：%s。修复会单独评估；如果模型拉伸或扭曲，先只启用需要的皮肤再测试。",
					modcompat.DescribeMixedDependencies(mixed))
			}
		}
		for _, id := range ids {
			summary := overlaps[id]
			summary.paths = append(summary.paths, path)
			if detail != "" {
				summary.details = append(summary.details, detail)
			}
			overlaps[id] = summary
		}
	}
	for id, summary := range overlaps {
		sort.Strings(summary.paths)
		overlaps[id] = summary
	}
	return overlaps
}

func withSymptom(list []Symptom, symptom Symptom) []Symptom {
	set := make(map[Symptom]struct{}, len(list)+1)
	for _, value := range list {
		set[value] = struct{}{}
	}
	set[symptom] = struct{}{}
	return sortedSymptoms(set)
}
