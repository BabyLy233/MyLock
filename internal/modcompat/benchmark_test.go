// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Developer benchmarks. They use synthetic resources by default; the real-mod
// benchmark runs only when MYLOCK_TEST_CITADEL and MYLOCK_TEST_MOD_VPK name an
// installed game and mod.

package modcompat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func vpkPackOne(path string, data []byte) ([]byte, error) {
	return vpk.Pack([]vpk.PackEntry{{Path: path, Data: data}})
}

func buildBenchmarkGame(b *testing.B, path string, data []byte) *source2.GameIndex {
	b.Helper()
	dir := filepath.Join(b.TempDir(), "game", "citadel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	packed, err := vpkPackOne(path, data)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pak01_dir.vpk"), packed, 0o644); err != nil {
		b.Fatal(err)
	}
	game, err := source2.OpenGame(dir)
	if err != nil {
		b.Fatal(err)
	}
	return game
}

func benchmarkLocalization(tokens int) []byte {
	var out strings.Builder
	out.WriteString("\"lang\"\r\n{\r\n\t\"Language\"\t\t\"english\"\r\n\t\"Tokens\"\r\n\t{\r\n")
	for i := 0; i < tokens; i++ {
		fmt.Fprintf(&out, "\t\t\"Token_%d\"\t\t\"Value %d\"\r\n", i, i)
	}
	out.WriteString("\t}\r\n}\r\n")
	return []byte(out.String())
}

// BenchmarkLocalizationMerge measures the profile-level merge of a realistic
// token table across three contributors, which is the work a compatibility
// check repeats per game update.
func BenchmarkLocalizationMerge(b *testing.B) {
	const tokens = 500
	gameText := benchmarkLocalization(tokens)
	first := benchmarkLocalization(tokens)
	second := bytesReplaceAll(benchmarkLocalization(tokens), "Value", "Other")
	third := bytesReplaceAll(benchmarkLocalization(tokens), "Value", "Third")
	inputs := make([]DataInput, 0, 3)
	for index, bytes := range [][]byte{first, second, third} {
		data, err := vpkPackOne(reviewTextPath, bytes)
		if err != nil {
			b.Fatal(err)
		}
		path := b.TempDir() + "/mod_dir.vpk"
		if err := os.WriteFile(path, data, 0o644); err != nil {
			b.Fatal(err)
		}
		inputs = append(inputs, DataInput{ModID: fmt.Sprintf("mod%d", index), VPKs: []string{path}})
	}
	game := buildBenchmarkGame(b, reviewTextPath, gameText)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := FixLocalization(inputs, game, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		if len(result.Files) == 0 || len(result.LocalizationConflicts) == 0 {
			b.Fatal("the benchmark fixture produced no merge")
		}
	}
}

// BenchmarkAnalyzeRealMod measures the full per-mod analysis against a real
// installation. It is skipped unless the environment provides one.
func BenchmarkAnalyzeRealMod(b *testing.B) {
	citadel := os.Getenv("MYLOCK_TEST_CITADEL")
	mod := os.Getenv("MYLOCK_TEST_MOD_VPK")
	if citadel == "" || mod == "" {
		b.Skip("set MYLOCK_TEST_CITADEL and MYLOCK_TEST_MOD_VPK")
	}
	game, err := source2.OpenGame(citadel)
	if err != nil {
		b.Fatal(err)
	}
	learned := LoadHistory("", corpusVersion(citadel))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Analyze("benchmark", []string{mod}, game,
			Scope{History: learned}, Detect); err != nil {
			b.Fatal(err)
		}
	}
}

func bytesReplaceAll(data []byte, old, replacement string) []byte {
	return []byte(strings.ReplaceAll(string(data), old, replacement))
}
