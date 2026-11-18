// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/vdata_history.rs tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package history

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func (b *builder) changes(changes []FingerprintChange) {
	b.u32(uint32(len(changes)))
	for _, change := range changes {
		b.change(change.Key, change.Value)
	}
}

func abilityRoot(cooldown, stamina int64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "ability_celeste", Value: kv3.Object(
			kv3.Pair{Key: "cooldown", Value: kv3.Int(cooldown)},
		)},
		kv3.Pair{Key: "upgrade_improved_stamina", Value: kv3.Object(
			kv3.Pair{Key: "value", Value: kv3.Int(stamina)},
		)},
	)
}

func paddedRoot(value float64) kv3.Value {
	pairs := []kv3.Pair{
		{Key: "authored", Value: kv3.Object(
			kv3.Pair{Key: "scale", Value: kv3.Double(value)},
		)},
	}
	for index := range 40 {
		pairs = append(pairs, kv3.Pair{Key: fmt.Sprintf("padding_%d", index), Value: kv3.Int(int64(index))})
	}
	return kv3.Object(pairs...)
}

func padRows(t *testing.T, value kv3.Value) kv3.Value {
	t.Helper()
	pairs := append([]kv3.Pair(nil), value.Object...)
	for index := range 40 {
		pairs = append(pairs, kv3.Pair{Key: fmt.Sprintf("unchanged_%d", index), Value: kv3.Int(int64(index))})
	}
	return kv3.Object(pairs...)
}

func mustEncode(t *testing.T, tables []HistoricalTable) []byte {
	t.Helper()
	bytes, err := EncodeHistoryIndex(tables)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

func TestStringBindingsSurviveCompactionRoundTripAndRemainScoped(t *testing.T) {
	const (
		path = "scripts/heroes.vdata_c"
		row  = "hero_copper"
	)
	var history Index
	for version := 1; version <= 70; version++ {
		root := kv3.Object(kv3.Pair{Key: row, Value: kv3.Object(
			kv3.Pair{Key: "m_strModelName", Value: kv3.String(fmt.Sprintf("models/version_%d.vmdl", version))},
		)})
		changed, err := history.Record(path, uint32(version), root)
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			t.Fatalf("version %d was not recorded", version)
		}
	}
	encoded, err := history.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"models/version_1.vmdl", "models/version_70.vmdl"} {
		if !restored.ObservedStringField(path, row, "m_strModelName", value) {
			t.Fatalf("%s is missing after compaction", value)
		}
	}
	for _, test := range []struct{ file, row, value string }{
		{"scripts/other.vdata_c", row, "models/version_1.vmdl"},
		{path, "hero_other", "models/version_1.vmdl"},
		{path, row, "models/version_71.vmdl"},
	} {
		if restored.ObservedStringField(test.file, test.row, "m_strModelName", test.value) {
			t.Fatalf("binding leaked to %+v", test)
		}
	}
}

func TestExactV2BindingsUpgradeToDurableHistory(t *testing.T) {
	const path = "scripts/heroes.vdata_c"
	root := kv3.Object(kv3.Pair{Key: "hero_copper", Value: kv3.Object(
		kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/original.vmdl")},
	)})
	encoded := mustEncode(t, []HistoricalTable{{Path: path, Builds: []HistoricalBuild{{Version: 1, Root: root}}}})
	// Drop the one v3 ledger entry: count, key, value count, value.
	encoded = encoded[:len(encoded)-72]
	copy(encoded[:8], exactMagic[:])
	history, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := history.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Parse(upgraded)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.ObservedStringField(path, "hero_copper", "m_strModelName", "models/original.vmdl") {
		t.Fatal("legacy field binding was lost during upgrade")
	}
	if !bytes.Equal(upgraded[:8], magic[:]) {
		t.Fatalf("upgraded magic = %q", upgraded[:8])
	}
}

func TestRoundTripsAndMatchesClosestBaseline(t *testing.T) {
	const path = "scripts/abilities.vdata_c"
	candidate := padRows(t, abilityRoot(32, 100))
	var builds []HistoricalBuild
	for _, snapshot := range []struct {
		version  uint32
		cooldown int64
	}{{100, 30}, {101, 32}, {102, 34}} {
		builds = append(builds, HistoricalBuild{
			Version: snapshot.version,
			Root:    padRows(t, abilityRoot(snapshot.cooldown, 1)),
		})
	}
	index, err := Parse(mustEncode(t, []HistoricalTable{{Path: path, Builds: builds}}))
	if err != nil {
		t.Fatal(err)
	}
	baseline := index.MatchBaseline(path, candidate)
	if baseline == nil {
		t.Fatal("baseline was not found")
	}
	if baseline.BuildVersion != 101 || baseline.ChangedRows != 1 {
		t.Fatalf("baseline = v%d changed %d", baseline.BuildVersion, baseline.ChangedRows)
	}
	if !baseline.FieldMatches("ability_celeste", []Seg{KeySeg("cooldown")}, kv3.Int(32)) {
		t.Fatal("unchanged field was not matched")
	}
	if baseline.FieldMatches("upgrade_improved_stamina", []Seg{KeySeg("value")}, kv3.Int(100)) {
		t.Fatal("changed field was reported as unchanged")
	}
}

func TestDiscoveryToleranceNeverDiscardsSmallDoubleEdits(t *testing.T) {
	const path = "scripts/example.vdata_c"
	index, err := Parse(mustEncode(t, []HistoricalTable{{
		Path:   path,
		Builds: []HistoricalBuild{{Version: 1, Root: paddedRoot(1.001)}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	baseline, evidence := index.MatchWithEvidence(path, paddedRoot(1.004))
	if evidence.Status != BaselineMatched {
		t.Fatalf("status = %s", evidence.Status)
	}
	if baseline == nil {
		t.Fatal("baseline was not found")
	}
	candidate := paddedRoot(1.004)
	authored, _ := candidate.Get("authored")
	if baseline.RowMatches("authored", *authored) {
		t.Fatal("edited row was reported unchanged")
	}
	if baseline.FieldMatches("authored", []Seg{KeySeg("scale")}, kv3.Double(1.004)) {
		t.Fatal("edited double was reported unchanged")
	}
	if !baseline.FieldMatches("authored", []Seg{KeySeg("scale")}, kv3.Double(1.001)) {
		t.Fatal("recorded double was not matched")
	}
}

func TestTiedBuildsAreAcceptedOnlyWhenTheyAgreeOnAuthoredChanges(t *testing.T) {
	const path = "scripts/example.vdata_c"
	index, err := Parse(mustEncode(t, []HistoricalTable{{
		Path: path,
		Builds: []HistoricalBuild{
			{Version: 1, Root: paddedRoot(1.001)},
			{Version: 2, Root: paddedRoot(1.004)},
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	baseline, evidence := index.MatchWithEvidence(path, paddedRoot(1.004))
	if baseline != nil {
		t.Fatal("disagreeing tied builds produced a baseline")
	}
	if evidence.Status != BaselineAmbiguous || fmt.Sprint(evidence.BuildVersions) != "[1 2]" {
		t.Fatalf("evidence = %+v", evidence)
	}
	baseline, evidence = index.MatchWithEvidence(path, paddedRoot(1.003))
	if baseline == nil {
		t.Fatal("agreeing tied builds were rejected")
	}
	if evidence.Status != BaselineMatched || fmt.Sprint(evidence.BuildVersions) != "[1 2]" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestLegacyHistoryCanDiscoverBuildsButNotProveDoubles(t *testing.T) {
	const path = "scripts/example.vdata_c"
	root := paddedRoot(1.001)
	var b builder
	b.bytes = append(b.bytes, legacyMagic[:]...)
	b.u32(1)
	b.string(path)
	b.u32(1)
	b.u32(1)
	rows, _ := rowFingerprints(root, modeDiscovery)
	b.changes(mapChanges(nil, rows))
	fields, _ := fieldFingerprints(root, modeDiscovery)
	b.changes(mapChanges(nil, fields))
	index, err := Parse(b.bytes)
	if err != nil {
		t.Fatal(err)
	}
	baseline, evidence := index.MatchWithEvidence(path, root)
	if evidence.Status != BaselineLegacyApproximate {
		t.Fatalf("status = %s", evidence.Status)
	}
	if baseline == nil {
		t.Fatal("legacy baseline was not found")
	}
	authored, _ := root.Get("authored")
	if baseline.RowMatches("authored", *authored) {
		t.Fatal("legacy history proved a double row")
	}
	if baseline.FieldMatches("authored", []Seg{KeySeg("scale")}, kv3.Double(1.001)) {
		t.Fatal("legacy history proved a double field")
	}
	if !baseline.RowMatches("padding_1", kv3.Int(1)) {
		t.Fatal("legacy history did not match an integer row")
	}
	if len(Embedded().tables) < 2 {
		t.Fatalf("embedded history has %d tables", len(Embedded().tables))
	}
}

func TestMalformedHistoryIsRejectedWithoutLargeAllocations(t *testing.T) {
	encoded := append(append([]byte(nil), magic[:]...), 0xff, 0xff, 0xff, 0xff)
	if _, err := Parse(encoded); err == nil {
		t.Fatal("oversized table count accepted")
	}
	for length := range 8 {
		if _, err := Parse(magic[:length]); err == nil {
			t.Fatalf("truncated history at %d accepted", length)
		}
	}
}

func TestObservedHistoryRoundTripsAndCompactsRetainedDeltas(t *testing.T) {
	const path = "scripts/example.vdata_c"
	var history Index
	for version := range 70 {
		changed, err := history.Record(path, uint32(version), paddedRoot(float64(version)))
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			t.Fatalf("version %d was not recorded", version)
		}
	}
	changed, err := history.Record(path, 70, paddedRoot(69))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("an unchanged snapshot was recorded")
	}
	encoded, err := history.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if builds := len(restored.tables[path].builds); builds != 64 {
		t.Fatalf("retained %d builds", builds)
	}
	for version := 6; version < 70; version++ {
		baseline := restored.MatchBaseline(path, paddedRoot(float64(version)))
		if baseline == nil || baseline.BuildVersion != uint32(version) {
			t.Fatalf("version %d matched %+v", version, baseline)
		}
		if !baseline.FieldMatches("authored", []Seg{KeySeg("scale")}, kv3.Double(float64(version))) {
			t.Fatalf("version %d field was not matched", version)
		}
	}
	if _, err := Embedded().ToBytes(); err == nil {
		t.Fatal("approximate embedded history was written as exact")
	}
}

func TestCombinedHistoryAnswersStringBindingsFromEitherHistory(t *testing.T) {
	const path = "scripts/heroes.vdata_c"
	hero := func(model string) kv3.Value {
		return kv3.Object(kv3.Pair{Key: "hero_copper", Value: kv3.Object(
			kv3.Pair{Key: "m_strModelName", Value: kv3.String(model)},
		)})
	}
	var shipped Index
	if _, err := shipped.Record(path, 1, hero("models/shipped.vmdl")); err != nil {
		t.Fatal(err)
	}
	var learned Index
	if _, err := learned.Record(path, 2, hero("models/learned.vmdl")); err != nil {
		t.Fatal(err)
	}
	combined := shipped.WithObserved(&learned)
	for _, test := range []struct {
		model    string
		observed bool
	}{
		{"models/shipped.vmdl", true},
		{"models/learned.vmdl", true},
		{"models/unseen.vmdl", false},
	} {
		if got := combined.ObservedStringField(path, "hero_copper", "m_strModelName", test.model); got != test.observed {
			t.Fatalf("%s observed = %t", test.model, got)
		}
	}
}

func TestObservedAndEmbeddedCandidatesCannotDisagreeSilently(t *testing.T) {
	const path = "scripts/example.vdata_c"
	var older Index
	if _, err := older.Record(path, 1, paddedRoot(1.001)); err != nil {
		t.Fatal(err)
	}
	var newer Index
	if _, err := newer.Record(path, 2, paddedRoot(1.004)); err != nil {
		t.Fatal(err)
	}
	combined := older.WithObserved(&newer)
	baseline, evidence := combined.MatchWithEvidence(path, paddedRoot(1.004))
	if baseline != nil {
		t.Fatal("disagreeing histories produced a baseline")
	}
	if evidence.Status != BaselineAmbiguous || fmt.Sprint(evidence.BuildVersions) != "[1 2]" {
		t.Fatalf("evidence = %+v", evidence)
	}
	baseline, evidence = combined.MatchWithEvidence(path, paddedRoot(1.003))
	if baseline == nil || baseline.BuildVersion != 2 {
		t.Fatalf("baseline = %+v", baseline)
	}
	if evidence.Status != BaselineMatched || fmt.Sprint(evidence.BuildVersions) != "[1 2]" {
		t.Fatalf("evidence = %+v", evidence)
	}
	var empty Index
	combined = older.WithObserved(&empty)
	if baseline := combined.MatchBaseline(path, paddedRoot(1.001)); baseline == nil || baseline.BuildVersion != 1 {
		t.Fatalf("baseline = %+v", baseline)
	}
	if _, err := older.Record(path, 2, paddedRoot(1.004)); err != nil {
		t.Fatal(err)
	}
	var current Index
	if _, err := current.Record(path, 3, paddedRoot(50)); err != nil {
		t.Fatal(err)
	}
	baseline, evidence = older.WithObserved(&current).MatchWithEvidence(path, paddedRoot(1.004))
	if baseline != nil {
		t.Fatal("ambiguous combined history produced a baseline")
	}
	if evidence.Status != BaselineAmbiguous || fmt.Sprint(evidence.BuildVersions) != "[1 2]" {
		t.Fatalf("evidence = %+v", evidence)
	}
}
