package modhealth

import (
	"sync"
	"time"

	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/source2"
)

// Analyzer is the per-mod analysis function; tests can replace it.
type Analyzer func(modID string, vpks []string, game *source2.GameIndex, mode modcompat.Mode) (modcompat.Analysis, error)

// CheckMod is one installed mod to check.
type CheckMod struct {
	ID     string
	VPKs   []string
	Choice Choice
}

// CheckOptions controls one check run.
type CheckOptions struct {
	Force      bool
	Build      *uint32
	OnProgress func(checked, total int)
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
func (c *Checker) Check(profile string, mods []CheckMod, game *source2.GameIndex, opts CheckOptions) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	installed := make([]Installed, 0, len(mods))
	entries := make(map[string]Cached)
	total := len(mods)
	checked := 0
	for _, mod := range mods {
		key, err := Stamp(mod.VPKs, opts.Build, RepairRevision)
		if err != nil {
			checked++
			if opts.OnProgress != nil {
				opts.OnProgress(checked, total)
			}
			continue
		}
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
		analysis, err := c.analyze(mod.ID, mod.VPKs, game, modcompat.Detect)
		if err != nil {
			checked++
			if opts.OnProgress != nil {
				opts.OnProgress(checked, total)
			}
			continue
		}
		restamped, err := Stamp(mod.VPKs, opts.Build, RepairRevision)
		if err != nil || !key.Equal(restamped) {
			_ = c.store.Forget(profile, mod.ID)
			checked++
			if opts.OnProgress != nil {
				opts.OnProgress(checked, total)
			}
			continue
		}
		entries[mod.ID] = Cached{
			Key:       key,
			Findings:  FindingsFromAnalysis(analysis),
			CheckedAt: time.Now().UTC(),
			Source:    SourceStandalone,
		}
		checked++
		if opts.OnProgress != nil {
			opts.OnProgress(checked, total)
		}
	}
	if err := c.store.Record(profile, opts.Build, entries); err != nil {
		return Report{}, err
	}
	return c.store.Report(profile, opts.Build, installed), nil
}
