package modhealth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// SchemaVersion is bumped when the cache layout changes incompatibly.
	SchemaVersion = 1
	// RulesRevision is bumped when status, symptom or coverage rules change.
	RulesRevision = 13
	// RepairRevision is bumped when the repair rules change.
	RepairRevision = 8
)

// FileStamp identifies a VPK by size and modification time, not by name:
// enabling, disabling or reordering a mod renames its files.
type FileStamp struct {
	Length   int64 `json:"length"`
	Modified int64 `json:"modified"` // UnixNano; 0 when unknown
}

// HealthKey is the cache key for one mod's analysis.
type HealthKey struct {
	Files     []FileStamp `json:"files"`
	GameBuild *uint32     `json:"gameBuild,omitempty"`
	Revision  uint32      `json:"revision"`
	Rules     uint32      `json:"rules"`
	// Scope fingerprints the other enabled mods. Relocation depends on which
	// model paths they provide, so enabling or disabling one must invalidate
	// this mod's cached verdict.
	Scope string `json:"scope,omitempty"`
}

// Stamp builds a HealthKey from the mod's VPKs.
func Stamp(vpks []string, build *uint32, revision uint32) (HealthKey, error) {
	key := HealthKey{GameBuild: build, Revision: revision, Rules: RulesRevision}
	for _, path := range vpks {
		info, err := os.Stat(path)
		if err != nil {
			return HealthKey{}, err
		}
		modified := info.ModTime().UnixNano()
		if info.ModTime().IsZero() {
			modified = 0
		}
		key.Files = append(key.Files, FileStamp{Length: info.Size(), Modified: modified})
	}
	return key, nil
}

// Equal compares two keys.
func (k HealthKey) Equal(other HealthKey) bool {
	if len(k.Files) != len(other.Files) {
		return false
	}
	for i := range k.Files {
		if k.Files[i] != other.Files[i] {
			return false
		}
	}
	return equalBuild(k.GameBuild, other.GameBuild) &&
		k.Revision == other.Revision && k.Rules == other.Rules && k.Scope == other.Scope
}

func equalBuild(a, b *uint32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Cached is one mod's stored verdict.
type Cached struct {
	Key       HealthKey `json:"key"`
	Findings  Findings  `json:"findings"`
	CheckedAt time.Time `json:"checkedAt"`
	Source    Source    `json:"source"`
}

// ProfileHealth is the cache for one profile.
type ProfileHealth struct {
	CheckedBuild  *uint32           `json:"checkedBuild,omitempty"`
	PreviousBuild *uint32           `json:"previousBuild,omitempty"`
	Mods          map[string]Cached `json:"mods"`
}

// Installed is one mod as it is installed now.
type Installed struct {
	ID     string
	Key    HealthKey
	Choice Choice
}

// Store serializes access to mod-health.json. Analysis happens outside its
// lock; only whole-file reads and writes are guarded.
type Store struct {
	mu   sync.Mutex
	path string
	data cacheFile
}

type cacheFile struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Profiles      map[string]ProfileHealth `json:"profiles"`
}

// Open reads the cache at path. A missing, corrupt or unknown-schema file is
// treated as an empty cache.
func Open(path string) (*Store, error) {
	store := &Store{path: path, data: cacheFile{SchemaVersion: SchemaVersion, Profiles: map[string]ProfileHealth{}}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var parsed cacheFile
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.SchemaVersion != SchemaVersion {
		return store, nil
	}
	if parsed.Profiles == nil {
		parsed.Profiles = map[string]ProfileHealth{}
	}
	for name, profile := range parsed.Profiles {
		if profile.Mods == nil {
			profile.Mods = map[string]Cached{}
			parsed.Profiles[name] = profile
		}
	}
	store.data = parsed
	return store, nil
}

// Get returns a fresh cached verdict for a mod.
func (s *Store) Get(profile, modID string, key HealthKey) (Cached, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Profiles[profile]
	if !ok {
		return Cached{}, false
	}
	entry, ok := p.Mods[modID]
	if !ok || !entry.Key.Equal(key) {
		return Cached{}, false
	}
	return entry, true
}

// Record stores a batch of verdicts and advances the profile's build.
func (s *Store) Record(profile string, build *uint32, entries map[string]Cached) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A run that produced no verdicts (every mod failed to analyse, or nothing
	// is installed) must not move the build the previous results belong to.
	if len(entries) == 0 {
		return nil
	}
	p := s.data.Profiles[profile]
	if p.Mods == nil {
		p.Mods = map[string]Cached{}
	}
	if !equalBuild(p.CheckedBuild, build) {
		p.PreviousBuild = p.CheckedBuild
		p.CheckedBuild = build
	}
	for id, entry := range entries {
		existing, ok := p.Mods[id]
		if ok && entry.Source == SourceStandalone && existing.Source == SourceLaunch &&
			existing.Key.Equal(entry.Key) {
			continue
		}
		p.Mods[id] = entry
	}
	s.data.Profiles[profile] = p
	return s.saveLocked()
}

// Forget drops cached entries, used when files changed while analysing.
func (s *Store) Forget(profile string, modIDs ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Profiles[profile]
	if !ok {
		return nil
	}
	for _, id := range modIDs {
		delete(p.Mods, id)
	}
	s.data.Profiles[profile] = p
	return s.saveLocked()
}

// Report computes the current report for a profile.
func (s *Store) Report(profile string, build *uint32, installed []Installed) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Profiles[profile]
	report := Report{GameBuild: build}
	if equalBuild(p.CheckedBuild, build) {
		report.PreviousGameBuild = p.PreviousBuild
	} else {
		report.PreviousGameBuild = p.CheckedBuild
	}
	for _, mod := range installed {
		entry, ok := p.Mods[mod.ID]
		if !ok || !entry.Key.Equal(mod.Key) {
			continue
		}
		findings := entry.Findings
		status := findings.Status(mod.Choice)
		symptoms := unionSymptoms(findings.FixSymptoms, findings.RemainingSymptoms)
		report.Mods = append(report.Mods, Health{
			ModID:             mod.ID,
			Status:            status,
			Symptoms:          symptoms,
			FixSymptoms:       findings.FixSymptoms,
			RemainingSymptoms: findings.RemainingSymptoms,
			Repairs:           findings.Repairs,
			Warnings:          findings.Warnings,
			GameBuild:         entry.Key.GameBuild,
			CheckedAt:         entry.CheckedAt.Format(time.RFC3339),
			Source:            entry.Source,
		})
	}
	return report
}

func unionSymptoms(a, b []Symptom) []Symptom {
	set := make(map[Symptom]struct{}, len(a)+len(b))
	for _, symptom := range a {
		set[symptom] = struct{}{}
	}
	for _, symptom := range b {
		set[symptom] = struct{}{}
	}
	return sortedSymptoms(set)
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
