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
	if err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
	}
	if fileExists(overlayPath) {
		t.Fatal("merged text overrode a mod that had not opted in")
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err == nil ||
		!strings.Contains(err.Error(), "original") {
		t.Fatalf("shared fix error = %v", err)
	}

	// Once the other owner opts in too, both are merged and the fix proceeds.
	if err := profile.SetCompatibility([]string{"original"}, true); err != nil {
		t.Fatal(err)
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err != nil {
		t.Fatalf("shared fix error = %v", err)
	}
	if err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
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
	if err := a.rebuildOverlayLocked(install, profile); err != nil {
		t.Fatal(err)
	}
	if fileExists(overlayVPKPath(install, profile)) {
		t.Fatal("merged text overrode a manually installed archive")
	}
	if err := sharedFixConflict(profile, []string{"selected"}); err == nil ||
		!strings.Contains(err.Error(), "unmanaged:") {
		t.Fatalf("shared fix error = %v", err)
	}
}
