package modhealth

import (
	"log"
	"sort"
	"sync"
	"time"

	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/source2"
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
	applyOverlaps(&report, overlaps)
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

// applyOverlaps adds the cross-mod findings to the report without persisting
// them: whether a path is shared depends on the other installed mods, not on
// the bytes this mod's cached verdict was recorded for.
func applyOverlaps(report *Report, overlaps map[string][]string) {
	for i := range report.Mods {
		health := &report.Mods[i]
		paths, ok := overlaps[health.ModID]
		if !ok {
			continue
		}
		health.ScopeIncomplete = true
		health.Warnings += len(paths)
		health.Symptoms = withSymptom(health.Symptoms, SymptomModel)
		if health.Status == StatusWorks {
			health.Status = StatusNeedsUpdate
		}
	}
}

// modelOverlaps lists the enabled mods that provide the same model path.
func modelOverlaps(mods []CheckMod) map[string][]string {
	enabled := 0
	for _, mod := range mods {
		if mod.Enabled && len(mod.VPKs) > 0 {
			enabled++
		}
	}
	if enabled < 2 {
		return nil
	}
	owners := make(map[string][]string)
	for _, mod := range mods {
		if !mod.Enabled || len(mod.VPKs) == 0 {
			continue
		}
		paths, err := modcompat.ModelPaths(mod.VPKs)
		if err != nil {
			continue
		}
		for _, path := range paths {
			owners[path] = append(owners[path], mod.ID)
		}
	}
	overlaps := make(map[string][]string)
	for path, ids := range owners {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		for _, id := range ids {
			overlaps[id] = append(overlaps[id], path)
		}
	}
	for id := range overlaps {
		sort.Strings(overlaps[id])
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
