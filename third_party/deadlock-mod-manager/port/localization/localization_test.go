// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package localization

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

const testPath = "resource/localization/example/example_english.txt"

func gameText(tokens ...string) []byte {
	var builder strings.Builder
	builder.WriteString("\"lang\"\r\n{\r\n\t\"Language\"\t\t\"english\"\r\n\t\"Tokens\"\r\n\t{\r\n")
	for index := 0; index+1 < len(tokens); index += 2 {
		builder.WriteString("\t\t\"" + tokens[index] + "\"\t\t\"" + tokens[index+1] + "\"\r\n")
	}
	builder.WriteString("\t}\r\n}\r\n")
	return []byte(builder.String())
}

func TestRenderRoundTripsThroughTheParser(t *testing.T) {
	file := File{Language: "english", Tokens: map[string]string{
		"Token_A": "Value \"quoted\"",
		"Token_B": "line\nbreak",
	}}
	rendered := Render(&file)
	if !bytes.HasPrefix(rendered, []byte("\ufeff\"lang\"\r\n{\r\n")) {
		t.Fatalf("rendered prefix = %q", rendered[:24])
	}
	parsed, err := ParseBytes(rendered, testPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.File.Language != "english" || parsed.File.Tokens["Token_A"] != "Value \"quoted\"" ||
		parsed.File.Tokens["Token_B"] != "line\nbreak" {
		t.Fatalf("parsed = %+v", parsed.File)
	}
}

func TestParseAcceptsUTF16AndFallsBackToLines(t *testing.T) {
	text := "\"lang\"\n{\n\t\"Language\"\t\t\"english\"\n\t\"Tokens\"\n\t{\n\t\t\"A\"\t\t\"one\"\n\t}\n}\n"
	units := make([]byte, 0, len(text)*2+2)
	units = append(units, 0xFF, 0xFE)
	for _, char := range text {
		var pair [2]byte
		binary.LittleEndian.PutUint16(pair[:], uint16(char))
		units = append(units, pair[:]...)
	}
	parsed, err := ParseBytes(units, testPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.File.Tokens["A"] != "one" {
		t.Fatalf("tokens = %v", parsed.File.Tokens)
	}

	broken := "\"lang\"\n{\n\t\"Language\"\t\t\"english\"\n\t\"Tokens\"\n\t{\n\t\t\"A\"\t\t\"one\"\n\t\t\"B\"\t\t\"unterminated\n\t}\n}\n"
	parsed, err = ParseBytes([]byte(broken), testPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.SkippedLines) != 1 || parsed.SkippedLines[0] != 7 {
		t.Fatalf("skipped = %v", parsed.SkippedLines)
	}
	if parsed.File.Tokens["A"] != "one" {
		t.Fatalf("tokens = %v", parsed.File.Tokens)
	}
}

func TestMergeRestoresGameTokensAndKeepsAuthoredValues(t *testing.T) {
	base := gameText("Token_A", "Vanilla", "Token_B", "Keep")
	mod := gameText("Token_A", "Authored")
	sources := map[string][]Source{testPath: {{
		ModID: "mod", SourceVPK: "mod.vpk", FilePath: testPath, Bytes: mod,
	}}}
	merger, err := BuildLocalization(sources, func(string) ([]byte, bool) { return base, true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merger.Repairs) != 1 || merger.Repairs[0].Kind != "restoredGameText" {
		t.Fatalf("repairs = %+v", merger.Repairs)
	}
	files, applied, err := merger.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d", applied)
	}
	parsed, err := ParseBytes(files[testPath], testPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.File.Tokens["Token_A"] != "Authored" {
		t.Fatalf("Token_A = %q", parsed.File.Tokens["Token_A"])
	}
	if parsed.File.Tokens["Token_B"] != "Keep" {
		t.Fatalf("Token_B = %q", parsed.File.Tokens["Token_B"])
	}
}

func TestMergeGroupsTokensCaseInsensitivelyByLoadOrder(t *testing.T) {
	base := gameText("Token_A", "Vanilla")
	first := gameText("token_a", "First")
	second := gameText("TOKEN_A", "Second")
	sources := map[string][]Source{testPath: {
		{ModID: "first", SourceVPK: "first.vpk", Priority: 0, FilePath: testPath, Bytes: first},
		{ModID: "second", SourceVPK: "second.vpk", Priority: 1, FilePath: testPath, Bytes: second},
	}}
	merger, err := BuildLocalization(sources, func(string) ([]byte, bool) { return base, true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merger.Groups) != 1 {
		t.Fatalf("groups = %+v", merger.Groups)
	}
	files, _, err := merger.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBytes(files[testPath], testPath)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := parsed.File.Lookup("Token_A"); value != "First" {
		t.Fatalf("token = %q", value)
	}
}

func TestProtectedFileDropsHistoricalTokenValues(t *testing.T) {
	path := "resource/localization/citadel_heroes/citadel_heroes_english.txt"
	value := "Old Hero Name"
	fingerprint := Fingerprint(path, "hero_old", value)
	var history []byte
	history = append(history, []byte("DMMLOC01")...)
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], 1)
	history = append(history, count[:]...)
	history = append(history, fingerprint[:]...)
	index, err := ParseHistory(history)
	if err != nil {
		t.Fatal(err)
	}
	base := gameText("hero_current", "Current")
	mod := gameText("hero_old", value)
	sources := map[string][]Source{path: {{
		ModID: "mod", SourceVPK: "mod.vpk", FilePath: path, Bytes: mod,
	}}}
	merger, err := BuildLocalization(sources, func(string) ([]byte, bool) { return base, true }, index)
	if err != nil {
		t.Fatal(err)
	}
	if len(merger.Groups) != 0 || merger.IgnoredHistoricalTokens != 1 {
		t.Fatalf("merge = %+v", merger)
	}
	if len(merger.Repairs) != 1 || merger.Repairs[0].Kind != "restoredGameText" {
		t.Fatalf("repairs = %+v", merger.Repairs)
	}
}

func TestSnapshotWarningMarksLargeStaleCopies(t *testing.T) {
	tokens := make([]string, 0, 100)
	for index := 0; index < 50; index++ {
		tokens = append(tokens, "Token_"+string(rune('A'+index%26))+"_"+string(rune('a'+index/26)), "Authored")
	}
	base := gameText(tokens...)
	modTokens := append([]string(nil), tokens...)
	for index := 0; index < len(modTokens); index += 2 {
		modTokens[index+1] = "Changed"
	}
	mod := gameText(modTokens...)
	sources := map[string][]Source{testPath: {{
		ModID: "mod", SourceVPK: "mod.vpk", FilePath: testPath, Bytes: mod,
	}}}
	merger, err := BuildLocalization(sources, func(string) ([]byte, bool) { return base, true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merger.SnapshotWarnings) != 1 || merger.SnapshotWarnings[0].TotalTokens != 50 {
		t.Fatalf("snapshot warnings = %+v", merger.SnapshotWarnings)
	}
	changed, added := merger.TokenStats()
	if changed != 50 || added != 0 {
		t.Fatalf("stats = %d changed, %d added", changed, added)
	}
}
