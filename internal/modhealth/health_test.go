package modhealth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func TestStatusBranches(t *testing.T) {
	cases := []struct {
		findings Findings
		choice   Choice
		want     Status
	}{
		{Findings{Repairs: 1}, ChoiceUnset, StatusFixable},
		{Findings{Repairs: 1, NeedsUpdate: true}, ChoiceUnset, StatusPartlyFixable},
		{Findings{Repairs: 1}, ChoiceEnabled, StatusFixed},
		{Findings{Repairs: 1}, ChoiceOriginalFiles, StatusOriginalFiles},
		{Findings{NeedsUpdate: true}, ChoiceUnset, StatusNeedsUpdate},
		{Findings{Covered: false}, ChoiceUnset, StatusNotCovered},
		{Findings{Covered: true, ScopeIncomplete: true}, ChoiceUnset, StatusNotCovered},
		{Findings{Covered: true}, ChoiceUnset, StatusWorks},
	}
	for i, tc := range cases {
		if got := tc.findings.Status(tc.choice); got != tc.want {
			t.Fatalf("case %d: status = %s, want %s", i, got, tc.want)
		}
	}
}

func TestFindingsFromAnalysisMapsCameraSymptoms(t *testing.T) {
	findings := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked: 1,
		Checks:        []string{"cameraAim", "crashRisk"},
		Repairs: []modcompat.Repair{
			{Kind: modcompat.RepairCameraAttachmentFrame},
			{Kind: modcompat.RepairCameraInterface},
		},
		Warnings: []modcompat.Warning{{Kind: camera.WarningMissingInterface}},
	})
	if findings.Repairs != 2 || findings.Warnings != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	if len(findings.FixSymptoms) != 1 || findings.FixSymptoms[0] != SymptomCameraAim {
		t.Fatalf("fix symptoms = %v", findings.FixSymptoms)
	}
	if len(findings.RemainingSymptoms) != 1 || findings.RemainingSymptoms[0] != SymptomCrashRisk {
		t.Fatalf("remaining = %v", findings.RemainingSymptoms)
	}
	if !findings.NeedsUpdate || !findings.Covered {
		t.Fatalf("flags = %+v", findings)
	}
	if len(findings.Checks) != 2 || findings.Checks[0] != "cameraAim" {
		t.Fatalf("checks = %v", findings.Checks)
	}
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStampTracksFilesAndBuild(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.vpk", []byte("one"))
	build := uint32(1)
	key, err := Stamp([]string{path}, &build, RepairRevision)
	if err != nil {
		t.Fatal(err)
	}
	same, err := Stamp([]string{path}, &build, RepairRevision)
	if err != nil || !key.Equal(same) {
		t.Fatal("unchanged stamp differs")
	}
	if err := os.WriteFile(path, []byte("two!"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Stamp([]string{path}, &build, RepairRevision)
	if err != nil {
		t.Fatal(err)
	}
	if key.Equal(changed) {
		t.Fatal("size change did not invalidate")
	}
	build2 := uint32(2)
	otherBuild, _ := Stamp([]string{path}, &build2, RepairRevision)
	if changed.Equal(otherBuild) {
		t.Fatal("build change did not invalidate")
	}
	if key.Equal(HealthKey{}) {
		t.Fatal("zero key equals a stamped key")
	}
}

func TestCheckerInvalidatesWhenTheEnabledScopeChanges(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	pack := func(name, model string) string {
		data, err := vpk.Pack([]vpk.PackEntry{{Path: model, Data: []byte("model")}})
		if err != nil {
			t.Fatal(err)
		}
		return writeFile(t, dir, name, data)
	}
	first := pack("first_dir.vpk", "models/hero/first.vmdl_c")
	second := pack("second_dir.vpk", "models/hero/second.vmdl_c")
	calls := make(map[string]int)
	checker := NewCheckerWithAnalyzer(store, func(modID string, _ []string, _ *source2.GameIndex, _ modcompat.Scope, _ modcompat.Mode) (modcompat.Analysis, error) {
		calls[modID]++
		return modcompat.Analysis{ModelsChecked: 1}, nil
	})
	both := []CheckMod{
		{ID: "first", VPKs: []string{first}, Enabled: true},
		{ID: "second", VPKs: []string{second}, Enabled: true},
	}
	if _, err := checker.Check("profile", both, nil, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls["first"] != 1 || calls["second"] != 1 {
		t.Fatalf("calls = %v", calls)
	}
	if _, err := checker.Check("profile", both[:1], nil, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls["first"] != 2 {
		t.Fatalf("first mod was not rechecked after the scope changed: %v", calls)
	}
}

func TestCheckerCachesSkipsAndForces(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "mod-health.json")
	store, err := Open(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	vpk := writeFile(t, dir, "mod.vpk", []byte("mod"))
	build := uint32(100)
	calls := 0
	checker := NewCheckerWithAnalyzer(store, func(string, []string, *source2.GameIndex, modcompat.Scope, modcompat.Mode) (modcompat.Analysis, error) {
		calls++
		return modcompat.Analysis{
			ModelsChecked: 1,
			Repairs:       []modcompat.Repair{{Kind: modcompat.RepairCameraAttachmentFrame}},
			Checks:        []string{"cameraAim", "crashRisk"},
		}, nil
	})
	mods := []CheckMod{{ID: "a", VPKs: []string{vpk}}}
	progress := 0
	report, err := checker.Check("profile", mods, nil, CheckOptions{Build: &build, OnProgress: func(checked, total int) {
		if total != 1 {
			t.Fatalf("total = %d", total)
		}
		progress = checked
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || progress != 1 {
		t.Fatalf("calls = %d, progress = %d", calls, progress)
	}
	if len(report.Mods) != 1 || report.Mods[0].Status != StatusFixable {
		t.Fatalf("report = %+v", report.Mods)
	}
	if _, err := checker.Check("profile", mods, nil, CheckOptions{Build: &build}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cached check re-analysed: %d calls", calls)
	}
	if _, err := checker.Check("profile", mods, nil, CheckOptions{Build: &build, Force: true}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("force did not re-analyse: %d calls", calls)
	}
	if err := os.WriteFile(vpk, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check("profile", mods, nil, CheckOptions{Build: &build}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("file change did not re-analyse: %d calls", calls)
	}
	if err := os.WriteFile(cachePath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get("profile", "a", HealthKey{}); ok {
		t.Fatal("corrupt cache was not discarded")
	}
}

func TestCheckerDropsResultsWhenFilesChangeDuringAnalysis(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	vpk := writeFile(t, dir, "mod.vpk", []byte("mod"))
	checker := NewCheckerWithAnalyzer(store, func(_ string, vpks []string, _ *source2.GameIndex, _ modcompat.Scope, _ modcompat.Mode) (modcompat.Analysis, error) {
		if err := os.WriteFile(vpks[0], []byte("changed during analysis"), 0o644); err != nil {
			t.Fatal(err)
		}
		return modcompat.Analysis{ModelsChecked: 1, Repairs: []modcompat.Repair{{Kind: modcompat.RepairCameraAttachmentFrame}}}, nil
	})
	mods := []CheckMod{{ID: "a", VPKs: []string{vpk}}}
	report, err := checker.Check("profile", mods, nil, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Mods) != 0 {
		t.Fatalf("unstable result was published: %+v", report.Mods)
	}
}

func TestCheckerSkipsAnalysisErrorsAndAdvancesBuild(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	vpk := writeFile(t, dir, "mod.vpk", []byte("mod"))
	checker := NewCheckerWithAnalyzer(store, func(string, []string, *source2.GameIndex, modcompat.Scope, modcompat.Mode) (modcompat.Analysis, error) {
		return modcompat.Analysis{}, errors.New("bad mod")
	})
	build := uint32(100)
	report, err := checker.Check("profile", []CheckMod{{ID: "a", VPKs: []string{vpk}}}, nil, CheckOptions{Build: &build})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Mods) != 0 {
		t.Fatalf("failed mod was reported: %+v", report.Mods)
	}
	if !equalBuild(report.PreviousGameBuild, nil) {
		t.Fatalf("previous build = %v", report.PreviousGameBuild)
	}
}

func TestAssetWarningsMapToSymptoms(t *testing.T) {
	cases := map[compat.WarningKind]Symptom{
		compat.WarningInvalidSkeleton:           SymptomCrashRisk,
		compat.WarningInvalidBoneRemapping:      SymptomCrashRisk,
		compat.WarningInvalidAttachment:         SymptomCrashRisk,
		compat.WarningInvalidHitbox:             SymptomCrashRisk,
		compat.WarningMissingResource:           SymptomCrashRisk,
		compat.WarningUnreadableResource:        SymptomCrashRisk,
		compat.WarningMissingCameraInterface:    SymptomCrashRisk,
		compat.WarningOutdatedCameraAttachments: SymptomCameraAim,
		compat.WarningMissingCameraAttachments:  SymptomCameraAim,
		compat.WarningUnverifiedSkeleton:        SymptomAnimations,
		compat.WarningMissingAnimationInterface: SymptomAnimations,
		compat.WarningMissingSoundBindings:      SymptomAnimations,
		compat.WarningMaterialMapping:           SymptomAppearance,
		compat.WarningMaterialInterface:         SymptomAppearance,
		compat.WarningModelMapping:              SymptomModel,
		compat.WarningOverlappingModel:          SymptomModel,
	}
	for kind, want := range cases {
		if got := assetWarningSymptom(kind); got != want {
			t.Fatalf("%s = %s, want %s", kind, got, want)
		}
	}
}

func TestFindingsCountStructuralWarningsAndCoverage(t *testing.T) {
	findings := FindingsFromAnalysis(modcompat.Analysis{
		AssetsChecked: 1,
		AssetWarnings: []modcompat.AssetWarning{
			{Kind: compat.WarningInvalidSkeleton},
			{Kind: compat.WarningMissingResource},
		},
	})
	if findings.Warnings != 2 || !findings.Covered || !findings.NeedsUpdate {
		t.Fatalf("findings = %+v", findings)
	}
	if len(findings.RemainingSymptoms) != 1 || findings.RemainingSymptoms[0] != SymptomCrashRisk {
		t.Fatalf("remaining = %v", findings.RemainingSymptoms)
	}
	if findings.Status(ChoiceUnset) != StatusNeedsUpdate {
		t.Fatalf("status = %s", findings.Status(ChoiceUnset))
	}
}

func TestOverlappingModelsMarkBothModsIncomplete(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "mod-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	packModel := func(name string) string {
		data, err := vpk.Pack([]vpk.PackEntry{
			{Path: "models/hero/hero.vmdl_c", Data: []byte("model")},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	first := packModel("first_dir.vpk")
	second := packModel("second_dir.vpk")
	checker := NewCheckerWithAnalyzer(store, func(string, []string, *source2.GameIndex, modcompat.Scope, modcompat.Mode) (modcompat.Analysis, error) {
		return modcompat.Analysis{ModelsChecked: 1}, nil
	})
	mods := []CheckMod{
		{ID: "first", VPKs: []string{first}, Enabled: true},
		{ID: "second", VPKs: []string{second}, Enabled: true},
	}
	report, err := checker.Check("profile", mods, nil, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Mods) != 2 {
		t.Fatalf("report = %+v", report.Mods)
	}
	for _, health := range report.Mods {
		if !health.ScopeIncomplete || health.Status != StatusNeedsUpdate {
			t.Fatalf("overlap health = %+v", health)
		}
		if len(health.Symptoms) != 1 || health.Symptoms[0] != SymptomModel {
			t.Fatalf("symptoms = %v", health.Symptoms)
		}
		if health.Warnings != 1 {
			t.Fatalf("warnings = %d", health.Warnings)
		}
	}

	// Disabling the second mod removes the ambiguity without changing the
	// first mod's cached bytes; the next report must not keep the old flag.
	only := []CheckMod{{ID: "first", VPKs: []string{first}, Enabled: true}}
	report, err = checker.Check("profile", only, nil, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Mods) != 1 || report.Mods[0].ScopeIncomplete {
		t.Fatalf("stale overlap = %+v", report.Mods)
	}
}

func TestFindingsIgnoreUnloadedModelsAndHeroChecksOnProps(t *testing.T) {
	findings := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked:  2,
		UnloadedModels: []string{"models/unloaded.vmdl_c"},
		PropModels:     []string{"models/prop.vmdl_c"},
		AssetWarnings: []modcompat.AssetWarning{
			{FilePath: "models/unloaded.vmdl_c", Kind: compat.WarningMissingResource},
			{FilePath: "models/prop.vmdl_c", Kind: compat.WarningMissingCameraInterface},
			{FilePath: "models/prop.vmdl_c", Kind: compat.WarningInvalidHitbox},
		},
	})
	if findings.Warnings != 2 {
		t.Fatalf("warnings = %d", findings.Warnings)
	}
	if len(findings.RemainingSymptoms) != 1 || findings.RemainingSymptoms[0] != SymptomCrashRisk {
		t.Fatalf("remaining = %v", findings.RemainingSymptoms)
	}
	if !findings.NeedsUpdate {
		t.Fatal("invalid hitbox should need an update")
	}
	unloadedOnly := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked:  1,
		UnloadedModels: []string{"models/unloaded.vmdl_c"},
		AssetWarnings: []modcompat.AssetWarning{
			{FilePath: "models/unloaded.vmdl_c", Kind: compat.WarningMissingResource},
		},
	})
	if unloadedOnly.Warnings != 0 || unloadedOnly.NeedsUpdate {
		t.Fatalf("unloaded findings = %+v", unloadedOnly)
	}
}

func TestFindingsFromAnalysisMapsTaggedSoundRepairToAnimations(t *testing.T) {
	findings := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked: 1,
		Repairs: []modcompat.Repair{
			{Kind: modcompat.RepairTaggedSounds},
			{Kind: modcompat.RepairAnimationInterface},
			{Kind: modcompat.RepairAnimationSkeleton},
			{Kind: modcompat.RepairAnimationSkeletonRebased},
			{Kind: modcompat.RepairAnimationClipTracks},
			{Kind: modcompat.RepairAnimationRigRebase},
		},
		AssetWarnings: []modcompat.AssetWarning{{Kind: compat.WarningMissingSoundBindings}},
	})
	if len(findings.FixSymptoms) != 1 || findings.FixSymptoms[0] != SymptomAnimations {
		t.Fatalf("fix symptoms = %v", findings.FixSymptoms)
	}
	if len(findings.RemainingSymptoms) != 1 || findings.RemainingSymptoms[0] != SymptomAnimations {
		t.Fatalf("remaining symptoms = %v", findings.RemainingSymptoms)
	}
}

func TestFindingsFromAnalysisMapsModelRelocationToModel(t *testing.T) {
	findings := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked: 1,
		Repairs:       []modcompat.Repair{{Kind: modcompat.RepairModelRelocation}},
		AssetWarnings: []modcompat.AssetWarning{{Kind: compat.WarningModelMapping}},
	})
	if len(findings.FixSymptoms) != 1 || findings.FixSymptoms[0] != SymptomModel {
		t.Fatalf("fix symptoms = %v", findings.FixSymptoms)
	}
	if len(findings.RemainingSymptoms) != 1 || findings.RemainingSymptoms[0] != SymptomModel {
		t.Fatalf("remaining symptoms = %v", findings.RemainingSymptoms)
	}
}

func TestInformationalAssetWarningsDoNotRequireAnUpdate(t *testing.T) {
	findings := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked: 1,
		AssetWarnings: []modcompat.AssetWarning{
			{Kind: compat.WarningMaterialInterface, Detail: "Authored shader differs"},
			{Kind: compat.WarningMissingResource, Detail: "materials/a.vtex"},
			{Kind: compat.WarningMissingCameraAttachments, Detail: "portrait_camera"},
		},
	})
	if findings.Warnings != 3 || findings.NeedsUpdate || len(findings.RemainingSymptoms) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	if findings.Status(ChoiceUnset) != StatusWorks {
		t.Fatalf("status = %s", findings.Status(ChoiceUnset))
	}

	crash := FindingsFromAnalysis(modcompat.Analysis{
		ModelsChecked: 1,
		AssetWarnings: []modcompat.AssetWarning{
			{Kind: compat.WarningMissingResource, Detail: "models/a.vmdl"},
		},
	})
	if !crash.NeedsUpdate || len(crash.RemainingSymptoms) != 1 ||
		crash.RemainingSymptoms[0] != SymptomCrashRisk {
		t.Fatalf("crash findings = %+v", crash)
	}
}
