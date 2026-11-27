package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/modhealth"
	"github.com/BabyLy233/mylock/internal/overlay"
	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	portlocalization "github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/localization"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func writeGameVPK(t *testing.T, install *game.Install) {
	t.Helper()
	data, err := vpk.Pack([]vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("game model")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "pak01_dir.vpk"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeCompatibility answers every mod with one camera repair, so the app's
// check, fix and restore flow can run without real compiled models.
func fakeCompatibility(modID string, _ []string, _ *source2.GameIndex, _ modcompat.Scope, mode modcompat.Mode) (modcompat.Analysis, error) {
	analysis := modcompat.Analysis{
		ModelsChecked: 1,
		Checks:        append([]string(nil), modcompat.SupportedChecks...),
		Repairs: []modcompat.Repair{{
			ModID:    modID,
			FilePath: "models/heroes/hero/hero.vmdl_c",
			Kind:     modcompat.RepairCameraAttachmentFrame,
		}},
	}
	if mode == modcompat.Fix {
		analysis.Fixed = map[string][]byte{
			"models/heroes/hero/hero.vmdl_c": []byte("fixed model"),
		}
	}
	return analysis, nil
}

func healthTestApp(t *testing.T) (*app, *game.Install, addons.Profile) {
	t.Helper()
	a := newApp()
	a.running = func() bool { return false }
	install, profile := fakeGame(t)
	writeGameVPK(t, install)
	addMod(t, profile, "mod", "hero.vpk")
	if err := profile.SetEnabled("mod", true); err != nil {
		t.Fatal(err)
	}
	data, err := vpk.Pack([]vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("mod model")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.Dir, "pak01_dir.vpk"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	store, err := modhealth.Open(filepath.Join(t.TempDir(), "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.health.checker = modhealth.NewCheckerWithAnalyzer(store, fakeCompatibility)
	a.health.analyze = fakeCompatibility
	a.health.historyPath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "vdata-history.bin"), nil
	}
	return a, install, profile
}

func waitHealth(t *testing.T, a *app, want modhealth.Status) {
	t.Helper()
	waitFor(t, "the compatibility check", func() bool {
		h := a.healthState()
		if h.checking || h.checkedAt.IsZero() {
			return false
		}
		health, ok := h.byID["mod"]
		return ok && health.Status == want
	})
}

func TestHealthCheckPanicIsReportedInsteadOfExiting(t *testing.T) {
	a, _, _ := healthTestApp(t)
	store, err := modhealth.Open(filepath.Join(t.TempDir(), "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.health.checker = modhealth.NewCheckerWithAnalyzer(store,
		func(string, []string, *source2.GameIndex, modcompat.Scope, modcompat.Mode) (modcompat.Analysis, error) {
			panic("malformed mod asset")
		})

	a.checkHealth(false)
	waitFor(t, "the interrupted check", func() bool {
		h := a.healthState()
		return !h.checking && strings.Contains(h.err, "兼容性检查中断")
	})
}

func TestHealthCheckFixesAndRestoresAMod(t *testing.T) {
	a, install, profile := healthTestApp(t)
	settingsPath(t, a)

	a.checkHealth(false)
	waitHealth(t, a, modhealth.StatusFixable)
	if got := a.healthState().byID["mod"].Symptoms; len(got) != 1 || got[0] != modhealth.SymptomCameraAim {
		t.Fatalf("symptoms = %v", got)
	}

	a.fixCompatibility([]string{"mod"})
	overlayPath := overlayVPKPath(install, profile)
	waitFor(t, "the compatibility package", func() bool {
		a.mu.Lock()
		busy := a.health.busy
		a.mu.Unlock()
		return !busy && fileExists(overlayPath)
	})
	waitHealth(t, a, modhealth.StatusFixed)

	manifest, err := addons.Load(profile.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Mods["mod"]
	if !entry.CompatibilityEnabled || entry.CompatibilityOptedOut {
		t.Fatalf("manifest choice = %+v", entry)
	}
	gameinfo, err := os.ReadFile(install.GameInfoPath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(gameinfo)
	overlaySearch := "citadel/dmm_localization_default"
	if !strings.Contains(text, overlaySearch) {
		t.Fatalf("gameinfo does not mount the overlay:\n%s", text)
	}
	if strings.Index(text, overlaySearch) > strings.Index(text, "citadel/addons") {
		t.Fatalf("overlay is not searched before the mods:\n%s", text)
	}

	// A manifest that says opted-in is not enough: tampering with the package
	// must take the "fixed" badge away.
	if err := os.WriteFile(overlayPath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.checkHealth(true)
	waitHealth(t, a, modhealth.StatusFixable)

	a.restoreCompatibility("mod")
	waitFor(t, "the restore", func() bool {
		a.mu.Lock()
		busy := a.health.busy
		a.mu.Unlock()
		return !busy && !fileExists(overlayPath)
	})
	waitHealth(t, a, modhealth.StatusOriginalFiles)

	manifest, err = addons.Load(profile.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	entry = manifest.Mods["mod"]
	if entry.CompatibilityEnabled || !entry.CompatibilityOptedOut {
		t.Fatalf("restored choice = %+v", entry)
	}
	gameinfo, err = os.ReadFile(install.GameInfoPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gameinfo), overlaySearch) {
		t.Fatalf("gameinfo still mounts a removed overlay:\n%s", gameinfo)
	}
}

func TestHealthCheckIsRefusedWhileTheGameRuns(t *testing.T) {
	a, _, profile := healthTestApp(t)
	a.running = func() bool { return true }
	settingsPath(t, a)
	a.fixCompatibility([]string{"mod"})
	waitFor(t, "the refusal", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return !a.health.busy && a.health.compatErr != ""
	})
	if got := a.healthState().compatErr; !strings.Contains(got, "游戏正在运行") {
		t.Fatalf("compatErr = %q", got)
	}
	manifest, err := addons.Load(profile.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Mods["mod"].CompatibilityEnabled {
		t.Fatal("the choice was written while the game was running")
	}
}

func TestHealthStatusBranchesShowEveryState(t *testing.T) {
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusFixable, Checks: modcompat.SupportedChecks, Symptoms: []modhealth.Symptom{modhealth.SymptomCameraAim}}); got != "可修复：相机/瞄准" {
		t.Fatalf("fixable line = %q", got)
	}
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusFixed, Checks: modcompat.SupportedChecks, Symptoms: []modhealth.Symptom{modhealth.SymptomCameraAim}}); got != "已修复：相机/瞄准" {
		t.Fatalf("fixed line = %q", got)
	}
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusNeedsUpdate, Checks: modcompat.SupportedChecks, Symptoms: []modhealth.Symptom{modhealth.SymptomCrashRisk}}); got != "需要更新：崩溃风险" {
		t.Fatalf("needs-update line = %q", got)
	}
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusOriginalFiles, Checks: modcompat.SupportedChecks}); got != "使用原文件" {
		t.Fatalf("original line = %q", got)
	}
}

func TestHealthLineNamesAnOverlappingModel(t *testing.T) {
	checks := make([]string, 0, len(modhealth.AllSymptoms))
	for _, symptom := range modhealth.AllSymptoms {
		checks = append(checks, string(symptom))
	}
	line, _ := healthLine(modhealth.Health{
		Status:          modhealth.StatusFixable,
		Checks:          checks,
		ScopeIncomplete: true,
		Symptoms:        []modhealth.Symptom{modhealth.SymptomModel},
	})
	if !strings.Contains(line, "无法单独修复") {
		t.Fatalf("line = %q", line)
	}
}

func compiledTable(t *testing.T, value kv3.Value) []byte {
	t.Helper()
	data, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(&value, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHealthFixMergesGameDataIntoTheOverlay(t *testing.T) {
	a := newApp()
	a.running = func() bool { return false }
	install, profile := fakeGame(t)
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "steam.inf"),
		[]byte("ClientVersion=100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addMod(t, profile, "mod", "hero.vpk")
	if err := profile.SetEnabled("mod", true); err != nil {
		t.Fatal(err)
	}
	gameRow := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CitadelHeroData_t")},
		kv3.Pair{Key: "m_mapStandardLevelUpUpgrades", Value: kv3.Object(
			kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(10)},
		)},
	)
	authoredRow := kv3.Object(
		kv3.Pair{Key: "_class", Value: kv3.String("CitadelHeroData_t")},
		kv3.Pair{Key: "m_mapStandardLevelUpUpgrades", Value: kv3.Object(
			kv3.Pair{Key: "MODIFIER_VALUE_HEALTH", Value: kv3.Double(20)},
			kv3.Pair{Key: "MODIFIER_VALUE_RETIRED", Value: kv3.Double(0.625)},
		)},
	)
	gameTable := kv3.Object(kv3.Pair{Key: "hero_one", Value: gameRow})
	modTable := kv3.Object(kv3.Pair{Key: "hero_one", Value: authoredRow})
	const textPath = "resource/localization/example/example_english.txt"
	gameData, err := vpk.Pack([]vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("game model")},
		{Path: "scripts/heroes.vdata_c", Data: compiledTable(t, gameTable)},
		{Path: textPath, Data: localizationFile("Token_A", "Vanilla", "Token_B", "Keep")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "pak01_dir.vpk"), gameData, 0o644); err != nil {
		t.Fatal(err)
	}
	modData, err := vpk.Pack([]vpk.PackEntry{
		{Path: "scripts/heroes.vdata_c", Data: compiledTable(t, modTable)},
		{Path: textPath, Data: localizationFile("Token_A", "Authored")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.Dir, "pak01_dir.vpk"), modData, 0o644); err != nil {
		t.Fatal(err)
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	store, err := modhealth.Open(filepath.Join(t.TempDir(), "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.health.checker = modhealth.NewCheckerWithAnalyzer(store, modcompat.Analyze)
	a.health.analyze = modcompat.Analyze
	historyPath := filepath.Join(t.TempDir(), "vdata-history.bin")
	a.health.historyPath = func(string) (string, error) { return historyPath, nil }
	settingsPath(t, a)

	a.checkHealth(false)
	waitHealth(t, a, modhealth.StatusPartlyFixable)
	if info, err := os.Stat(historyPath); err != nil || info.Size() == 0 {
		t.Fatalf("game data was not learned: %v", err)
	}
	health := a.healthState().byID["mod"]
	if !containsSymptom(health.FixSymptoms, modhealth.SymptomGameData) ||
		!containsSymptom(health.RemainingSymptoms, modhealth.SymptomGameData) {
		t.Fatalf("health = %+v", health)
	}

	a.fixCompatibility([]string{"mod"})
	overlayPath := overlayVPKPath(install, profile)
	waitFor(t, "the compatibility package", func() bool {
		a.mu.Lock()
		busy := a.health.busy
		a.mu.Unlock()
		return !busy && fileExists(overlayPath)
	})
	archive, err := vpk.Open(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := archive.Read("scripts/heroes.vdata_c")
	if err != nil {
		t.Fatal(err)
	}
	_, root, _, err := camera.DecodeCompiled(data)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := root.Get("hero_one")
	mapping, _ := row.Get("m_mapStandardLevelUpUpgrades")
	if len(mapping.Object) != 1 {
		t.Fatalf("merged map = %+v", mapping)
	}
	if healthValue, _ := mapping.Get("MODIFIER_VALUE_HEALTH"); healthValue.Double != 20 {
		t.Fatalf("health = %v", healthValue)
	}
	textData, err := archive.Read(textPath)
	if err != nil {
		t.Fatal(err)
	}
	parsedText, err := portlocalization.ParseBytes(textData, textPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsedText.File.Tokens["Token_A"] != "Authored" || parsedText.File.Tokens["Token_B"] != "Keep" {
		t.Fatalf("merged localization = %v", parsedText.File.Tokens)
	}
}

func localizationFile(tokens ...string) []byte {
	out := "\"lang\"\r\n{\r\n\t\"Language\"\t\t\"english\"\r\n\t\"Tokens\"\r\n\t{\r\n"
	for index := 0; index+1 < len(tokens); index += 2 {
		out += "\t\t\"" + tokens[index] + "\"\t\t\"" + tokens[index+1] + "\"\r\n"
	}
	out += "\t}\r\n}\r\n"
	return []byte(out)
}

func containsSymptom(symptoms []modhealth.Symptom, want modhealth.Symptom) bool {
	for _, symptom := range symptoms {
		if symptom == want {
			return true
		}
	}
	return false
}

func TestSharedLocalizationIsLeftToTheGameUnlessEveryOwnerOptsIn(t *testing.T) {
	a := newApp()
	a.running = func() bool { return false }
	install, profile := fakeGame(t)
	a.health.analyze = modcompat.Analyze
	a.health.historyPath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "vdata-history.bin"), nil
	}
	const textPath = "resource/localization/example/example_english.txt"
	gameData, err := vpk.Pack([]vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("game model")},
		{Path: textPath, Data: localizationFile("Token_A", "Vanilla", "Token_B", "Keep")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "pak01_dir.vpk"), gameData, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"selected", "original"} {
		addMod(t, profile, id, "mod.vpk")
		if err := profile.SetEnabled(id, true); err != nil {
			t.Fatal(err)
		}
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range mods {
		if len(mod.VpkPaths) == 0 {
			t.Fatalf("mod %s has no VPK", mod.ID)
		}
		value := "Authored"
		if mod.ID == "original" {
			value = "Original"
		}
		data, err := vpk.Pack([]vpk.PackEntry{{Path: textPath, Data: localizationFile("Token_A", value)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mod.VpkPaths[0], data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.install, a.mods = install, mods
	if err := profile.SetCompatibility([]string{"selected"}, true); err != nil {
		t.Fatal(err)
	}
	overlayPath := overlayVPKPath(install, profile)
	if _, err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
	}
	if fileExists(overlayPath) {
		t.Fatal("merged text overrode a mod that had not opted in")
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err == nil ||
		!strings.Contains(err.Error(), "original") {
		t.Fatalf("shared fix error = %v", err)
	}

	// Once the other owner opts in too, both are merged, but the two different
	// values pause for review instead of silently picking one.
	if err := profile.SetCompatibility([]string{"original"}, true); err != nil {
		t.Fatal(err)
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err != nil {
		t.Fatalf("shared fix error = %v", err)
	}
	prompt, err := a.rebuildOverlayLocked(install, profile)
	if err != nil {
		t.Fatal(err)
	}
	if prompt == nil || len(prompt.conflicts) != 1 {
		t.Fatalf("review = %+v", prompt)
	}
	if fileExists(overlayPath) {
		t.Fatal("a package was written before the conflict was reviewed")
	}
	winner := "selected"
	submitted := submittedReview{
		inputs: prompt.inputs,
		resolutions: []overlay.Resolution{{
			ConflictKey: prompt.conflicts[0].Key, WinnerModID: &winner,
		}},
	}
	if prompt, err = a.rebuildOverlayReview(install, profile, &submitted); err != nil {
		t.Fatal(err)
	}
	if prompt != nil {
		t.Fatalf("review still pending: %+v", prompt)
	}
	archive, err := vpk.Open(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	text, err := archive.Read(textPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := portlocalization.ParseBytes(text, textPath)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := parsed.File.Lookup("Token_A"); value != "Authored" {
		t.Fatalf("merged token = %q", value)
	}
}

// localizationConflictApp makes two opted-in mods that rewrite one game token
// differently, so a build has to ask which value the game should show.
func localizationConflictApp(t *testing.T) (*app, *game.Install, addons.Profile, string, string) {
	t.Helper()
	a := newApp()
	a.running = func() bool { return false }
	install, profile := fakeGame(t)
	a.health.analyze = modcompat.Analyze
	historyPath := filepath.Join(t.TempDir(), "vdata-history.bin")
	a.health.historyPath = func(string) (string, error) { return historyPath, nil }
	const textPath = "resource/localization/example/example_english.txt"
	gameData, err := vpk.Pack([]vpk.PackEntry{{
		Path: textPath, Data: localizationFile("Token_A", "Vanilla"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "pak01_dir.vpk"), gameData, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		addMod(t, profile, id, "mod.vpk")
		if err := profile.SetEnabled(id, true); err != nil {
			t.Fatal(err)
		}
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range mods {
		value := "First"
		if mod.ID == "second" {
			value = "Second"
		}
		data, err := vpk.Pack([]vpk.PackEntry{{Path: textPath, Data: localizationFile("Token_A", value)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mod.VpkPaths[0], data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.install, a.mods = install, mods
	if err := profile.SetCompatibility([]string{"first", "second"}, true); err != nil {
		t.Fatal(err)
	}
	return a, install, profile, overlayVPKPath(install, profile), textPath
}

func TestSavedReviewChoosesTheMergedLocalizationWinner(t *testing.T) {
	a, install, profile, overlayPath, textPath := localizationConflictApp(t)
	gameIndex, err := a.gameIndexFor(install)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	var chosen []addons.Mod
	inputs := make([]modcompat.DataInput, 0, len(selected))
	for _, mod := range selected {
		if mod.Enabled && mod.CompatibilityEnabled && len(mod.VpkPaths) > 0 {
			chosen = append(chosen, mod)
			inputs = append(inputs, modcompat.DataInput{ModID: mod.ID, VPKs: mod.VpkPaths})
		}
	}
	text, err := modcompat.FixLocalization(inputs, gameIndex, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(text.LocalizationConflicts) != 1 {
		t.Fatalf("conflicts = %+v", text.LocalizationConflicts)
	}
	reviewed, err := reviewInputs(install, overlayPath, chosen, "", text)
	if err != nil {
		t.Fatal(err)
	}
	winner := "second"
	if err := overlay.SaveReview(overlayPath, overlay.Review{
		Inputs: reviewed,
		Resolutions: []overlay.Resolution{{
			ConflictKey: text.LocalizationConflicts[0].Key, WinnerModID: &winner,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
	}
	archive, err := vpk.Open(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	value, err := archive.Read(textPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := portlocalization.ParseBytes(value, textPath)
	if err != nil {
		t.Fatal(err)
	}
	if token, _ := parsed.File.Lookup("Token_A"); token != "Second" {
		t.Fatalf("merged token = %q", token)
	}
}

func TestUnmanagedVpkProtectsItsSharedLocalization(t *testing.T) {
	a := newApp()
	a.running = func() bool { return false }
	install, profile := fakeGame(t)
	a.health.analyze = modcompat.Analyze
	a.health.historyPath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "vdata-history.bin"), nil
	}
	const textPath = "resource/localization/example/example_english.txt"
	gameData, err := vpk.Pack([]vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("game model")},
		{Path: textPath, Data: localizationFile("Token_A", "Vanilla", "Token_B", "Keep")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "pak01_dir.vpk"), gameData, 0o644); err != nil {
		t.Fatal(err)
	}
	addMod(t, profile, "selected", "mod.vpk")
	if err := profile.SetEnabled("selected", true); err != nil {
		t.Fatal(err)
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := vpk.Pack([]vpk.PackEntry{
		{Path: textPath, Data: localizationFile("Token_A", "Authored")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mods[0].VpkPaths[0], selected, 0o644); err != nil {
		t.Fatal(err)
	}
	// A manually installed archive the manifest knows nothing about.
	loose, err := vpk.Pack([]vpk.PackEntry{
		{Path: textPath, Data: localizationFile("Token_B", "Loose")},
	})
	if err != nil {
		t.Fatal(err)
	}
	loosePath := filepath.Join(profile.Dir, "pak05_dir.vpk")
	if err := os.WriteFile(loosePath, loose, 0o644); err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	if err := profile.SetCompatibility([]string{"selected"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
	}
	if fileExists(overlayVPKPath(install, profile)) {
		t.Fatal("merged text overrode a manually installed archive")
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err == nil ||
		!strings.Contains(err.Error(), "手动安装的 VPK") {
		t.Fatalf("shared fix error = %v", err)
	}
}

func TestDisabledModDoesNotProtectItsSharedLocalization(t *testing.T) {
	a := newApp()
	a.running = func() bool { return false }
	install, profile := fakeGame(t)
	a.health.analyze = modcompat.Analyze
	a.health.historyPath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "vdata-history.bin"), nil
	}
	const textPath = "resource/localization/example/example_english.txt"
	gameData, err := vpk.Pack([]vpk.PackEntry{
		{Path: "models/heroes/hero/hero.vmdl_c", Data: []byte("game model")},
		{Path: textPath, Data: localizationFile("Token_A", "Vanilla", "Token_B", "Keep")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.CitadelDir(), "pak01_dir.vpk"), gameData, 0o644); err != nil {
		t.Fatal(err)
	}
	addMod(t, profile, "selected", "mod.vpk")
	addMod(t, profile, "disabled", "mod.vpk")
	if err := profile.SetEnabled("selected", true); err != nil {
		t.Fatal(err)
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := vpk.Pack([]vpk.PackEntry{
		{Path: textPath, Data: localizationFile("Token_A", "Authored")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range mods {
		if mod.ID == "selected" {
			if err := os.WriteFile(mod.VpkPaths[0], selected, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.install, a.mods = install, mods
	if err := profile.SetCompatibility([]string{"selected"}, true); err != nil {
		t.Fatal(err)
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err != nil {
		t.Fatalf("a disabled mod blocked the fix: %v", err)
	}
	if _, err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
	}
	archive, err := vpk.Open(overlayVPKPath(install, profile))
	if err != nil {
		t.Fatal(err)
	}
	text, err := archive.Read(textPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := portlocalization.ParseBytes(text, textPath)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := parsed.File.Lookup("Token_A"); value != "Authored" {
		t.Fatalf("merged token = %q", value)
	}
}

func TestReviewChoicesBecomeSavedResolutions(t *testing.T) {
	vanilla := "Vanilla"
	review := &pendingReview{
		conflicts: []modcompat.LocalizationConflict{{
			Key: "text-key", FilePath: "resource/localization/example/example_english.txt",
			Token: "Token_A", VanillaValue: &vanilla,
			Candidates: []modcompat.LocalizationCandidate{
				{ModID: "first", SourceVPK: "a_dir.vpk", Value: "First"},
				{ModID: "second", SourceVPK: "b_dir.vpk", Value: "Second"},
			},
		}},
		dataConflicts: []modcompat.CompiledDataConflict{{
			Key: "data-key", FilePath: "scripts/heroes.vdata_c", RowName: "hero_one",
			FieldPath: "m_mapStandardLevelUpUpgrades.MODIFIER_VALUE_HEALTH",
			Candidates: []modcompat.CompiledDataCandidate{
				{ModID: "first", SourceVPK: "a_dir.vpk"},
				{ModID: "second", SourceVPK: "b_dir.vpk"},
			},
		}},
	}
	names := map[string]string{"first": "First Mod", "second": "Second Mod"}

	textChoices := localizationChoices(review.conflicts[0], names)
	if len(textChoices) != 4 || textChoices[0].resolution != nil ||
		textChoices[1].resolution == nil || !textChoices[1].resolution.UseVanilla {
		t.Fatalf("text choices = %+v", textChoices)
	}
	dataChoices := dataChoices(review.dataConflicts[0], names)
	if len(dataChoices) != 4 || dataChoices[1].resolution == nil || !dataChoices[1].resolution.UseVanilla {
		t.Fatalf("data choices = %+v", dataChoices)
	}

	// Leaving both at the load-order default records no answer at all.
	if resolutions := draftResolutions(review, nil, names); len(resolutions) != 0 {
		t.Fatalf("default resolutions = %+v", resolutions)
	}
	draft := map[string]string{
		"text-key": textChoices[3].label, // the second mod's value
		"data-key": dataChoices[1].label, // the game's own row
	}
	resolutions := draftResolutions(review, draft, names)
	if len(resolutions) != 2 {
		t.Fatalf("resolutions = %+v", resolutions)
	}
	if resolutions[0].ConflictKey != "text-key" || resolutions[0].WinnerModID == nil ||
		*resolutions[0].WinnerModID != "second" || resolutions[0].WinnerValue == nil ||
		*resolutions[0].WinnerValue != "Second" || resolutions[0].UseVanilla {
		t.Fatalf("text resolution = %+v", resolutions[0])
	}
	if resolutions[1].ConflictKey != "data-key" || !resolutions[1].UseVanilla {
		t.Fatalf("data resolution = %+v", resolutions[1])
	}
}

func TestCompatibilityFixPausesForReviewAndAcceptsChoices(t *testing.T) {
	a, _, _, overlayPath, textPath := localizationConflictApp(t)
	settingsPath(t, a)

	a.fixCompatibility([]string{"first", "second"})
	waitFor(t, "the compatibility review", func() bool {
		h := a.healthState()
		return !h.busy && h.review != nil
	})
	if fileExists(overlayPath) {
		t.Fatal("a package was written from unreviewed defaults")
	}

	h := a.healthState()
	winner := "second"
	a.submitReview([]overlay.Resolution{{
		ConflictKey: h.review.conflicts[0].Key, WinnerModID: &winner,
	}})
	waitFor(t, "the reviewed package", func() bool {
		h := a.healthState()
		return !h.busy && h.review == nil && fileExists(overlayPath)
	})
	if review, err := overlay.LoadReview(overlayPath); err != nil || review == nil {
		t.Fatalf("saved review = %+v, %v", review, err)
	}
	archive, err := vpk.Open(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	value, err := archive.Read(textPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := portlocalization.ParseBytes(value, textPath)
	if err != nil {
		t.Fatal(err)
	}
	if token, _ := parsed.File.Lookup("Token_A"); token != "Second" {
		t.Fatalf("merged token = %q", token)
	}
}
