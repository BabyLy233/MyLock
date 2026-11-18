// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Opt-in tests against a locally installed game. They run only when the
// environment names the files, and no game asset is committed. See
// ../../third_party/deadlock-mod-manager/NOTICE.md for the corpus this
// replaces: the upstream runner pins downloaded mods by checksum, which cannot
// be shipped.

package modcompat

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
)

func corpusGame(t *testing.T) (string, *source2.GameIndex, *uint32) {
	t.Helper()
	citadel := os.Getenv("MYLOCK_TEST_CITADEL")
	if citadel == "" {
		t.Skip("set MYLOCK_TEST_CITADEL to a game/citadel directory")
	}
	game, err := source2.OpenGame(citadel)
	if err != nil {
		t.Fatal(err)
	}
	return citadel, game, corpusVersion(citadel)
}

func corpusVersion(citadelDir string) *uint32 {
	data, err := os.ReadFile(filepath.Join(citadelDir, "steam.inf"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "ClientVersion=")
		if !ok {
			continue
		}
		version, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if err != nil {
			return nil
		}
		out := uint32(version)
		return &out
	}
	return nil
}

// TestRealCitadelGameTablesDecode reads the tables the data compatibility path
// depends on, decodes them and learns them. It proves the KV3, resource and
// history paths against a real installation without keeping any of it.
func TestRealCitadelGameTablesDecode(t *testing.T) {
	_, game, version := corpusGame(t)
	learned := NewHistory(nil, version)
	found := 0
	for _, path := range []string{vdata.HeroesVDataPath, vdata.AbilitiesPath} {
		bytes, ok := game.GameBytes(path)
		if !ok {
			continue
		}
		_, root, _, err := camera.DecodeCompiled(bytes)
		if err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		if root.Kind != kv3.KindObject {
			t.Fatalf("%s root is not an object", path)
		}
		if _, err := learned.Record(path, bytes); err != nil {
			t.Fatalf("learning %s: %v", path, err)
		}
		found++
	}
	if found == 0 {
		t.Skip("this citadel has no representative data tables")
	}
	if version != nil && !learned.Changed() {
		t.Fatal("a versioned install learned no table")
	}
}

// TestRealCitadelModAnalysis runs the whole per-mod analysis on one mod VPK
// against the installed game. Every repaired file must still be a valid
// resource; nothing is written.
func TestRealCitadelModAnalysis(t *testing.T) {
	_, game, version := corpusGame(t)
	mod := os.Getenv("MYLOCK_TEST_MOD_VPK")
	if mod == "" {
		t.Skip("set MYLOCK_TEST_MOD_VPK to an installed mod VPK")
	}
	learned := NewHistory(nil, version)
	analysis, err := Analyze("corpus", []string{mod}, game, Scope{History: learned}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	for path, bytes := range analysis.Fixed {
		if _, err := resource.Parse(bytes); err != nil {
			t.Fatalf("repaired %s is not a valid resource: %v", path, err)
		}
	}
	t.Logf("checks %v, repairs %d, warnings %d, assets %d",
		analysis.Checks, len(analysis.Repairs), len(analysis.Warnings), analysis.AssetsChecked)
}
