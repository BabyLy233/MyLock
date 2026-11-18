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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// corpusCase mirrors the upstream compatibility corpus manifest. Archives are
// downloaded by the developer and never committed; the checksum pins exactly
// which bytes a run covered.
type corpusCase struct {
	ID      uint64   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Archive string   `json:"archive"`
	SHA256  string   `json:"sha256"`
	VPKs    []string `json:"vpks"`
}

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

// TestPinnedCorpusRepairs runs the analyzer over a local compatibility corpus
// manifest. Every case is pinned by archive SHA-256, and every repaired file
// must remain a valid resource with its block count and order intact, which is
// the invariant the overlay relies on. Set MYLOCK_TEST_CORPUS_ID to run one
// case while iterating.
func TestPinnedCorpusRepairs(t *testing.T) {
	_, game, version := corpusGame(t)
	manifest := os.Getenv("MYLOCK_TEST_CORPUS")
	if manifest == "" {
		t.Skip("set MYLOCK_TEST_CORPUS to a corpus JSON file")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("reading %s: %v", manifest, err)
	}
	if only := os.Getenv("MYLOCK_TEST_CORPUS_ID"); only != "" {
		id, err := strconv.ParseUint(only, 10, 64)
		if err != nil {
			t.Fatalf("MYLOCK_TEST_CORPUS_ID: %v", err)
		}
		cases = slices.DeleteFunc(cases, func(test corpusCase) bool { return test.ID != id })
	}
	if len(cases) == 0 {
		t.Skip("the corpus has no matching case")
	}
	learned := NewHistory(nil, version)
	for _, test := range cases {
		if test.SHA256 == "" {
			t.Fatalf("case %d has no pinned checksum", test.ID)
		}
		if err := verifyCorpusArchive(test); err != nil {
			t.Fatalf("case %d (%s): %v", test.ID, test.Name, err)
		}
		analysis, err := Analyze(strconv.FormatUint(test.ID, 10), test.VPKs, game,
			Scope{History: learned}, Fix)
		if err != nil {
			t.Fatalf("case %d (%s): %v", test.ID, test.Name, err)
		}
		for path, bytes := range analysis.Fixed {
			after, err := resource.Parse(bytes)
			if err != nil {
				t.Fatalf("case %d: repaired %s is not a valid resource: %v", test.ID, path, err)
			}
			original, err := corpusEntry(test.VPKs, path)
			if err != nil || original == nil {
				continue
			}
			before, err := resource.Parse(original)
			if err != nil {
				continue
			}
			if len(before.Blocks()) != len(after.Blocks()) {
				t.Fatalf("case %d: repaired %s changed the block count from %d to %d",
					test.ID, path, len(before.Blocks()), len(after.Blocks()))
			}
		}
		t.Logf("case %d (%s): checks %v, repairs %d, warnings %d",
			test.ID, test.Name, analysis.Checks, len(analysis.Repairs), len(analysis.Warnings))
	}
}

func verifyCorpusArchive(test corpusCase) error {
	data, err := os.ReadFile(test.Archive)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if !strings.EqualFold(actual, test.SHA256) {
		return fmt.Errorf("archive checksum %s does not match the pinned %s", actual, test.SHA256)
	}
	for _, path := range test.VPKs {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return fmt.Errorf("VPK %s is missing", path)
		}
	}
	return nil
}

// corpusEntry reads the first copy of a resource from the case's mod VPKs.
func corpusEntry(vpkPaths []string, path string) ([]byte, error) {
	for _, vpkPath := range vpkPaths {
		archive, err := vpk.Open(vpkPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range archive.Entries {
			if camera.NormalizePath(entry.FullPath) == path {
				return archive.ReadLimited(entry.FullPath, maxModelBytes)
			}
		}
	}
	return nil, nil
}
