// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/observed_data.rs
// tests at 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/history"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func observedRows(sound string) kv3.Value {
	return kv3.Object(kv3.Pair{Key: "hero", Value: kv3.Object(
		kv3.Pair{Key: "sound", Value: kv3.String(sound)},
		kv3.Pair{Key: "scale", Value: kv3.Double(0.123456789)},
	)})
}

func TestRepeatedObservationsPreserveHistoryAndLearnChangedBytes(t *testing.T) {
	path := dataHeroesPath
	var version uint32
	cached := NewHistory(nil, &version)
	direct := &history.Index{}
	for _, step := range []struct {
		version uint32
		sound   string
	}{
		{1, "old"},
		{1, "old"},
		{1, "changed without a build bump"},
		{2, "new"},
		{3, "old"},
	} {
		version = step.version
		resourceBytes := compiledBlock(t, ptr(observedRows(step.sound)))
		got, err := cached.Record(path, resourceBytes)
		if err != nil {
			t.Fatal(err)
		}
		want, err := direct.Record(path, step.version, observedRows(step.sound))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("step %+v: changed = %t, want %t", step, got, want)
		}
		learned, err := cached.learned.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		expected, err := direct.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(learned, expected) {
			t.Fatalf("step %+v: learned history differs from direct recording", step)
		}
	}
	// A fresh history still learns cached data. Reuse never means skipping the
	// observation, and corrupt resources cannot inherit a previous parse.
	fresh := NewHistory(nil, &version)
	resourceBytes := compiledBlock(t, ptr(observedRows("old")))
	version = 1
	if changed, err := fresh.Record(path, resourceBytes); err != nil || !changed {
		t.Fatalf("fresh record = %t, %v", changed, err)
	}
	if !fresh.learned.ObservedStringField(path, "hero", "sound", "old") {
		t.Fatal("fresh history did not learn the string binding")
	}
	if _, err := fresh.Record(path, []byte("not a compiled resource")); err == nil {
		t.Fatal("corrupt resource inherited a previous parse")
	}
}

func TestObservationsAreScopedToTableAndEvictedWhenFull(t *testing.T) {
	root := kv3.Object(kv3.Pair{Key: "hero", Value: kv3.Object(
		kv3.Pair{Key: "sound", Value: kv3.String("voice")},
	)})
	resourceBytes := compiledBlock(t, ptr(root))
	version := uint32(1)
	observed := NewHistory(nil, &version)
	for _, path := range []string{dataHeroesPath, "scripts/abilities.vdata_c"} {
		if _, err := observed.Record(path, resourceBytes); err != nil {
			t.Fatal(err)
		}
		if !observed.learned.ObservedStringField(path, "hero", "sound", "voice") {
			t.Fatalf("%s did not learn its binding", path)
		}
	}
	snapshot, err := history.NewSnapshot(dataHeroesPath, root)
	if err != nil {
		t.Fatal(err)
	}
	var cache History
	version = 1
	cache.version = &version
	for index := range observationMaxEntries {
		cache.insert("table", [32]byte{byte(index)}, snapshot)
	}
	if cache.cached("table", [32]byte{0}) == nil {
		t.Fatal("least recently used entry was evicted too early")
	}
	if cache.cached("different table", [32]byte{0}) != nil {
		t.Fatal("cache answered for a different table")
	}
	cache.insert("table", [32]byte{observationMaxEntries}, snapshot)
	if cache.cached("table", [32]byte{1}) != nil {
		t.Fatal("oldest entry survived the eviction")
	}
	if cache.cached("table", [32]byte{0}) == nil {
		t.Fatal("recently used entry was evicted")
	}
	if cache.bytes > observationMaxBytes {
		t.Fatalf("cache holds %d bytes", cache.bytes)
	}
}

func TestHistorySaveRoundTripsAndIgnoresUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache", "vdata-history.bin")
	version := uint32(7)
	history := NewHistory(nil, &version)
	if _, err := history.Record(dataHeroesPath,
		compiledBlock(t, ptr(observedRows("voice")))); err != nil {
		t.Fatal(err)
	}
	if !history.Changed() {
		t.Fatal("recording did not mark the history changed")
	}
	if err := history.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadHistory(path, &version)
	if !reloaded.learned.ObservedStringField(dataHeroesPath, "hero", "sound", "voice") {
		t.Fatal("saved binding was not loaded")
	}
	if reloaded.Changed() {
		t.Fatal("loading marked the history changed")
	}

	if err := os.WriteFile(path, []byte("not an index"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := LoadHistory(path, &version)
	if broken.learned.ObservedStringField(dataHeroesPath, "hero", "sound", "voice") {
		t.Fatal("corrupt history was trusted")
	}
	missing := LoadHistory(filepath.Join(dir, "absent.bin"), &version)
	if missing.learned.ObservedStringField(dataHeroesPath, "hero", "sound", "voice") {
		t.Fatal("missing history was trusted")
	}
}

func TestModelOnlyModLearnsHeroRegistryEvenWithoutDataTables(t *testing.T) {
	version := uint32(100)
	learned := NewHistory(nil, &version)
	heroes := kv3.Object(kv3.Pair{Key: "hero_one", Value: kv3.Object(
		kv3.Pair{Key: "sound", Value: kv3.String("voice")},
	)})
	abilities := kv3.Object(kv3.Pair{Key: "ability_one", Value: kv3.Object(
		kv3.Pair{Key: "name", Value: kv3.String("blink")},
	)})
	game := gameIndexWith(t, []vpk.PackEntry{
		{Path: dataHeroesPath, Data: compiledBlock(t, ptr(heroes))},
		{Path: vdata.AbilitiesPath, Data: compiledBlock(t, ptr(abilities))},
	})
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("model")},
	})
	if _, err := Analyze("mod", []string{mod}, game, Scope{History: learned}, Detect); err != nil {
		t.Fatal(err)
	}
	if !learned.learned.ObservedStringField(dataHeroesPath, "hero_one", "sound", "voice") {
		t.Fatal("the hero registry was not learned for a model-only mod")
	}
	if !learned.learned.ObservedStringField(vdata.AbilitiesPath, "ability_one", "name", "blink") {
		t.Fatal("the abilities table was not learned for a model-only mod")
	}
}
