// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (compiled-data
// table model) and localization_overlay/vdata_compatibility.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package vdata

import "github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"

// HeroesVDataPath is the hero table that receives the hero-specific repairs.
const HeroesVDataPath = "scripts/heroes.vdata_c"

// RepairKind names a data repair. The strings match the original's serialized
// camelCase names because overlays record them.
type RepairKind string

const (
	RepairHeroDevelopmentState RepairKind = "heroDevelopmentState"
	RepairInheritedDefinition  RepairKind = "inheritedDefinition"
	RepairRetainedDependency   RepairKind = "retainedDependency"
	RepairCurrentReference     RepairKind = "currentReference"
	RepairEnumMapEntry         RepairKind = "enumMapEntry"
	RepairRestoredGameData     RepairKind = "restoredGameData"
	RepairRestoredGameText     RepairKind = "restoredGameText"
)

// DataRepair is one safe game-data repair.
type DataRepair struct {
	ModID     string
	SourceVPK string
	FilePath  string
	RowName   string
	Kind      RepairKind
	Detail    string
}

// WarningKind names a data problem without a proven repair.
type WarningKind string

const (
	WarningMissingDefinition       WarningKind = "missingDefinition"
	WarningUnverifiedDefinition    WarningKind = "unverifiedDefinition"
	WarningUnverifiedInheritance   WarningKind = "unverifiedInheritance"
	WarningMissingDevelopmentState WarningKind = "missingDevelopmentState"
	WarningOmittedHeroDefinition   WarningKind = "omittedHeroDefinition"
	WarningOmittedEnumMapEntry     WarningKind = "omittedEnumMapEntry"
	WarningUnreadableData          WarningKind = "unreadableData"
)

// WarningReason explains why a data problem could not be repaired.
type WarningReason string

const (
	ReasonDefinitionUnavailable         WarningReason = "definitionUnavailable"
	ReasonCompetingDefinitions          WarningReason = "competingDefinitions"
	ReasonUnrecognizedClassOrEnum       WarningReason = "unrecognizedClassOrEnum"
	ReasonAmbiguousTemplates            WarningReason = "ambiguousTemplates"
	ReasonInheritanceCycle              WarningReason = "inheritanceCycle"
	ReasonVisibilityMigrationUnverified WarningReason = "visibilityMigrationUnverified"
	ReasonUnreadableResource            WarningReason = "unreadableResource"
)

// DataWarning is one game-data problem the analyzer cannot prove safe to fix.
type DataWarning struct {
	ModID      string
	SourceVPK  string
	FilePath   string
	RowName    string
	Kind       WarningKind
	FieldPath  string
	TargetFile string
	TargetRow  string
	Reason     WarningReason
	Detail     string
}

// Candidate identifies the mod and archive a row value came from.
type Candidate struct {
	ModID     string
	SourceVPK string
	Priority  int
}

// Unrebased keeps the authored value when an inherited rebase changed it, so a
// later merge can decline the repair if the parent was edited too.
type Unrebased struct {
	Value    kv3.Value
	Encoding kv3.Encoding
}

// CandidateValue is one candidate patch with the encoding needed to rebuild it.
type CandidateValue struct {
	Candidate Candidate
	Value     kv3.Value
	Encoding  kv3.Encoding
	Unrebased *Unrebased
}

// Group is one conflicting row or field patch across sources.
type Group struct {
	Key              string
	FilePath         string
	RowName          string
	Path             []kv3.Seg
	ExistedInVanilla bool
	Candidates       []CandidateValue
}

// BaselineEvidence describes how an authored table matched recorded history.
type BaselineEvidence struct {
	Status         string
	BuildVersions  []uint32
	ComparableRows int
	ChangedRows    int
}

// BaselineInfo records that an authored table differs from the current one.
type BaselineInfo struct {
	ModID     string
	SourceVPK string
	FilePath  string
	Evidence  BaselineEvidence
}

// TableSources is one mod's contribution to a compiled table, built apart from
// the others so a source that cannot be read adds nothing.
type TableSources struct {
	Files              int
	IgnoredVanillaRows int
	Groups             map[string]*Group
	RetiredRows        map[string][]CandidateValue
	RowRepairs         []DataRepair
	RowWarnings        []DataWarning
	Baselines          []BaselineInfo
	Restorations       []DataRepair
}

func newTableSources() *TableSources {
	return &TableSources{
		Groups:      make(map[string]*Group),
		RetiredRows: make(map[string][]CandidateValue),
	}
}

// Add merges another source's contribution into this one.
func (t *TableSources) Add(source *TableSources) {
	t.Files += source.Files
	t.IgnoredVanillaRows += source.IgnoredVanillaRows
	for key, group := range source.Groups {
		existing := t.Groups[key]
		if existing == nil {
			t.Groups[key] = group
			continue
		}
		existing.Candidates = append(existing.Candidates, group.Candidates...)
	}
	for row, values := range source.RetiredRows {
		t.RetiredRows[row] = append(t.RetiredRows[row], values...)
	}
	t.RowRepairs = append(t.RowRepairs, source.RowRepairs...)
	t.RowWarnings = append(t.RowWarnings, source.RowWarnings...)
	t.Baselines = append(t.Baselines, source.Baselines...)
	t.Restorations = append(t.Restorations, source.Restorations...)
}

// Source is one mod's copy of a compiled table.
type Source struct {
	ModID     string
	SourceVPK string
	Priority  int
	Bytes     []byte
}

// Table is the collected view of one compiled table across every source.
type Table struct {
	FilePath           string
	BaseBytes          []byte
	Format             kv3.Format
	BaseRoot           kv3.Value
	BaseEncoding       kv3.Encoding
	Groups             map[string]*Group
	Files              int
	IgnoredVanillaRows int
	RetiredRows        map[string][]CandidateValue
	RowRepairs         []DataRepair
	RowWarnings        []DataWarning
	Baselines          []BaselineInfo
	Restorations       []DataRepair
	Unreadable         []DataWarning
	// Shadowed marks a table whose winning game copy cannot be read, so no
	// merge may replace it.
	Shadowed bool
}

// Origin records which candidate supplied a row or field.
type Origin struct {
	Path      []kv3.Seg
	Candidate Candidate
}

// Merged is one table after conflict choices were applied.
type Merged struct {
	Root        kv3.Value
	Encoding    kv3.Encoding
	AppliedRows map[string]struct{}
	Origins     map[string][]Origin
}

// Resolution is the user's choice for one conflict.
type Resolution struct {
	ConflictKey     string
	WinnerModID     *string
	WinnerValue     *string
	WinnerSourceVPK *string
	UseVanilla      bool
}

// Prepared is the fully merged game data plus all repairs and warnings.
type Prepared struct {
	Merged   []*Merged
	Repairs  []DataRepair
	Warnings []DataWarning
}

// BaselineMatch is the closest recorded build for an authored table.
type BaselineMatch interface {
	Baseline
	ContainsRow(rowName string) bool
	FieldMatches(rowName string, path []kv3.Seg, value kv3.Value) bool
	Version() uint32
	ComparableCount() int
	ChangedCount() int
}

// HistoryMatcher matches an authored table against the learned and shipped
// history. It is nil when no history is available.
type HistoryMatcher func(filePath string, root kv3.Value) (BaselineMatch, BaselineEvidence)

// HeroIDReassignment records an added hero whose requested ID was taken.
type HeroIDReassignment struct {
	RowName     string
	ModID       string
	RequestedID int64
	AssignedID  int64
}
