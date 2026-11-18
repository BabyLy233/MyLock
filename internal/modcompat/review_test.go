// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only

package modcompat

import (
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/localization"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const reviewTextPath = "resource/localization/example/example_english.txt"

func reviewText(tokens ...string) []byte {
	var out strings.Builder
	out.WriteString("\"lang\"\r\n{\r\n\t\"Language\"\t\t\"english\"\r\n\t\"Tokens\"\r\n\t{\r\n")
	for index := 0; index+1 < len(tokens); index += 2 {
		out.WriteString("\t\t\"" + tokens[index] + "\"\t\t\"" + tokens[index+1] + "\"\r\n")
	}
	out.WriteString("\t}\r\n}\r\n")
	return []byte(out.String())
}

func reviewTextToken(t *testing.T, bytes []byte, token string) string {
	t.Helper()
	parsed, err := localization.ParseBytes(bytes, reviewTextPath)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.File.Tokens[token]
}

func TestFixLocalizationListsOnlyDisagreeingTokens(t *testing.T) {
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: reviewTextPath, Data: reviewText("Token_A", "Vanilla", "Token_B", "Vanilla"),
	}})
	first := packMod(t, []vpk.PackEntry{{
		Path: reviewTextPath, Data: reviewText("Token_A", "First", "Token_B", "Agreed"),
	}})
	second := packMod(t, []vpk.PackEntry{{
		Path: reviewTextPath, Data: reviewText("Token_A", "Second", "Token_B", "Agreed"),
	}})
	inputs := []DataInput{
		{ModID: "first", VPKs: []string{first}},
		{ModID: "second", VPKs: []string{second}},
	}

	result, err := FixLocalization(inputs, game, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LocalizationConflicts) != 1 {
		t.Fatalf("conflicts = %+v", result.LocalizationConflicts)
	}
	conflict := result.LocalizationConflicts[0]
	if conflict.Token != "Token_A" || conflict.FilePath != reviewTextPath {
		t.Fatalf("conflict = %+v", conflict)
	}
	if conflict.VanillaValue == nil || *conflict.VanillaValue != "Vanilla" {
		t.Fatalf("vanilla = %v", conflict.VanillaValue)
	}
	if len(conflict.Candidates) != 2 || conflict.Candidates[0].ModID != "first" ||
		conflict.Candidates[0].Value != "First" || conflict.Candidates[1].Value != "Second" {
		t.Fatalf("candidates = %+v", conflict.Candidates)
	}
	// Without a review the first enabled mod keeps winning.
	if got := reviewTextToken(t, result.Files[reviewTextPath], "Token_A"); got != "First" {
		t.Fatalf("default winner = %q", got)
	}

	winner := "second"
	result, err = FixLocalization(inputs, game, nil, []vdata.Resolution{{
		ConflictKey: conflict.Key, WinnerModID: &winner,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := reviewTextToken(t, result.Files[reviewTextPath], "Token_A"); got != "Second" {
		t.Fatalf("reviewed winner = %q", got)
	}
}

func TestFixLocalizationTreatsAgreementAsNoConflict(t *testing.T) {
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: reviewTextPath, Data: reviewText("Token_A", "Vanilla"),
	}})
	first := packMod(t, []vpk.PackEntry{{Path: reviewTextPath, Data: reviewText("Token_A", "Same")}})
	second := packMod(t, []vpk.PackEntry{{Path: reviewTextPath, Data: reviewText("Token_A", "Same")}})
	result, err := FixLocalization([]DataInput{
		{ModID: "first", VPKs: []string{first}},
		{ModID: "second", VPKs: []string{second}},
	}, game, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LocalizationConflicts) != 0 {
		t.Fatalf("conflicts = %+v", result.LocalizationConflicts)
	}
	if got := reviewTextToken(t, result.Files[reviewTextPath], "Token_A"); got != "Same" {
		t.Fatalf("value = %q", got)
	}
}

func reviewHeroTable(health float64) kv3.Value {
	row := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CitadelHeroData_t")},
		kv3.Pair{Key: "m_mapStandardLevelUpUpgrades", Value: kv3.Object(
			kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(health)},
		)},
	)
	return heroTable(row)
}

func TestFixDataListsDisagreeingRowsAndAppliesTheReview(t *testing.T) {
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(reviewHeroTable(10))),
	}})
	first := packMod(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(reviewHeroTable(20))),
	}})
	second := packMod(t, []vpk.PackEntry{{
		Path: dataHeroesPath, Data: compiledBlock(t, ptr(reviewHeroTable(30))),
	}})
	inputs := []DataInput{
		{ModID: "first", VPKs: []string{first}},
		{ModID: "second", VPKs: []string{second}},
	}

	result, err := FixData(inputs, game, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DataConflicts) != 1 {
		t.Fatalf("conflicts = %+v", result.DataConflicts)
	}
	conflict := result.DataConflicts[0]
	if conflict.FilePath != dataHeroesPath || conflict.RowName != "hero_one" ||
		conflict.FieldPath != "m_mapStandardLevelUpUpgrades.MODIFIER_VALUE_HEALTH" {
		t.Fatalf("conflict = %+v", conflict)
	}
	if !conflict.ExistedInVanilla {
		t.Fatalf("conflict = %+v", conflict)
	}
	if len(conflict.Candidates) != 2 || conflict.Candidates[1].ModID != "second" {
		t.Fatalf("candidates = %+v", conflict.Candidates)
	}
	if got := mergedHealth(t, result.Files[dataHeroesPath]); got != 20 {
		t.Fatalf("default health = %v", got)
	}

	winner := "second"
	result, err = FixData(inputs, game, nil, nil, []vdata.Resolution{{
		ConflictKey: conflict.Key, WinnerModID: &winner,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := mergedHealth(t, result.Files[dataHeroesPath]); got != 30 {
		t.Fatalf("reviewed health = %v", got)
	}
}

func mergedHealth(t *testing.T, bytes []byte) float64 {
	t.Helper()
	_, root, _, err := camera.DecodeCompiled(bytes)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := root.Get("hero_one")
	mapping, _ := row.Get("m_mapStandardLevelUpUpgrades")
	health, _ := mapping.Get("MODIFIER_VALUE_HEALTH")
	return health.Double
}

func TestReviewSubjectIgnoresLoadOrderAndKeepsStaleChoicesOut(t *testing.T) {
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: reviewTextPath, Data: reviewText("Token_A", "Vanilla"),
	}})
	first := packMod(t, []vpk.PackEntry{{Path: reviewTextPath, Data: reviewText("Token_A", "First")}})
	second := packMod(t, []vpk.PackEntry{{Path: reviewTextPath, Data: reviewText("Token_A", "Second")}})
	result, err := FixLocalization([]DataInput{
		{ModID: "first", VPKs: []string{first}},
		{ModID: "second", VPKs: []string{second}},
	}, game, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	subject := ReviewSubject(result)
	// Priorities follow the load order; the subject must not.
	reordered, err := FixLocalization([]DataInput{
		{ModID: "second", VPKs: []string{second}},
		{ModID: "first", VPKs: []string{first}},
	}, game, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ReviewSubject(reordered) == subject {
		t.Fatal("load order changed the review subject")
	}
	// The candidate order is part of the question (it picks the default), so
	// the two orders are different reviews; the same order stays stable.
	again, err := FixLocalization([]DataInput{
		{ModID: "first", VPKs: []string{first}},
		{ModID: "second", VPKs: []string{second}},
	}, game, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ReviewSubject(again) != subject {
		t.Fatal("the same inputs changed the review subject")
	}

	conflict := result.LocalizationConflicts[0]
	winner := "second"
	missing := "missing"
	valid := []vdata.Resolution{
		{ConflictKey: conflict.Key, WinnerModID: &winner},
		{ConflictKey: conflict.Key, WinnerModID: &missing},
		{ConflictKey: "gone", WinnerModID: &winner},
	}
	applicable := ApplicableResolutions(result, valid)
	if len(applicable) != 1 || applicable[0].WinnerModID == nil ||
		*applicable[0].WinnerModID != "second" {
		t.Fatalf("applicable = %+v", applicable)
	}
	if ids := ReviewModIDs(result); len(ids) != 2 {
		t.Fatalf("involved mods = %v", ids)
	}
}