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
func fakeCompatibility(modID string, _ []string, _ *source2.GameIndex, mode modcompat.Mode) (modcompat.Analysis, error) {
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
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusFixable, Checks: modcompat.SupportedChecks, Symptoms: []modhealth.Symptom{modhealth.SymptomCameraAim}}); got != "可修复：相机/瞄准（未检查：模型、游戏数据）" {
		t.Fatalf("fixable line = %q", got)
	}
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusFixed, Checks: modcompat.SupportedChecks, Symptoms: []modhealth.Symptom{modhealth.SymptomCameraAim}}); got != "已修复：相机/瞄准（未检查：模型、游戏数据）" {
		t.Fatalf("fixed line = %q", got)
	}
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusNeedsUpdate, Checks: modcompat.SupportedChecks, Symptoms: []modhealth.Symptom{modhealth.SymptomCrashRisk}}); got != "需要更新：崩溃风险（未检查：模型、游戏数据）" {
		t.Fatalf("needs-update line = %q", got)
	}
	if got, _ := healthLine(modhealth.Health{Status: modhealth.StatusOriginalFiles, Checks: modcompat.SupportedChecks}); got != "使用原文件（未检查：模型、游戏数据）" {
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
