package overlay

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

type fixture struct {
	citadel string
	output  string
	mod     string
}

func setup(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	citadel := filepath.Join(root, "game", "citadel")
	if err := os.MkdirAll(citadel, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(citadel, "pak01_dir.vpk"), []byte("game"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(citadel, "steam.inf"), []byte("ClientVersion=100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(root, "mod_dir.vpk")
	if err := os.WriteFile(mod, []byte("mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fixture{citadel: citadel, output: filepath.Join(citadel, "dmm_localization_default", "pak01_dir.vpk"), mod: mod}
}

func build(t *testing.T, f fixture, sources ...Source) *Plan {
	t.Helper()
	build := uint32(100)
	plan, err := Build(f.citadel, f.output, &build, sources)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestApplyPublishesVerifiedOverlayAndReceipt(t *testing.T) {
	f := setup(t)
	plan := build(t, f, Source{
		ModID:                "a",
		VPKs:                 []string{f.mod},
		CompatibilityEnabled: true,
		Fixed:                map[string][]byte{"models/hero/hero.vmdl_c": []byte("fixed model")},
		Repairs:              []Repair{{ModID: "a", Path: "models/hero/hero.vmdl_c", Kind: "CameraAttachmentFrame"}},
	})
	result, err := Apply(plan, f.output)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasOverlay || result.OutputPath != f.output || result.PackedFiles != 1 {
		t.Fatalf("result = %+v", result)
	}
	archive, err := vpk.Open(f.output)
	if err != nil {
		t.Fatal(err)
	}
	got, err := archive.Read("models/hero/hero.vmdl_c")
	if err != nil || !bytes.Equal(got, []byte("fixed model")) {
		t.Fatalf("overlay entry = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.output), "compatibility.json")); err != nil {
		t.Fatal(err)
	}
	applied, err := Applied(f.output, plan.Key)
	if err != nil || !applied {
		t.Fatalf("applied = %v, %v", applied, err)
	}
	current, err := Current(f.output, plan.Key)
	if err != nil || !current {
		t.Fatalf("current = %v, %v", current, err)
	}

	// A changed overlay invalidates the receipt without a rebuild.
	if err := os.WriteFile(f.output, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if applied, err := Applied(f.output, plan.Key); err != nil || applied {
		t.Fatalf("corrupt overlay applied = %v, %v", applied, err)
	}
	if current, err := Current(f.output, plan.Key); err != nil || current {
		t.Fatalf("corrupt overlay current = %v, %v", current, err)
	}
}

func TestApplyEmptyPlanRemovesStaleOverlayButWritesReceipt(t *testing.T) {
	f := setup(t)
	plan := build(t, f, Source{
		ModID:                "a",
		VPKs:                 []string{f.mod},
		CompatibilityEnabled: true,
		Fixed:                map[string][]byte{"models/hero/hero.vmdl_c": []byte("fixed")},
	})
	if _, err := Apply(plan, f.output); err != nil {
		t.Fatal(err)
	}
	empty := build(t, f, Source{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true})
	result, err := Apply(empty, f.output)
	if err != nil {
		t.Fatal(err)
	}
	if result.HasOverlay {
		t.Fatalf("empty plan published an overlay: %+v", result)
	}
	if _, err := os.Stat(f.output); !os.IsNotExist(err) {
		t.Fatal("stale overlay survived")
	}
	if current, err := Current(f.output, empty.Key); err != nil || !current {
		t.Fatalf("empty receipt current = %v, %v", current, err)
	}
	if applied, err := Applied(f.output, empty.Key); err != nil || applied {
		t.Fatalf("empty plan applied = %v, %v", applied, err)
	}
}

func TestBuildKeyTracksInputsAndCompanions(t *testing.T) {
	f := setup(t)
	chunk := filepath.Join(filepath.Dir(f.mod), "mod_000.vpk")
	if err := os.WriteFile(chunk, []byte("chunk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.mod), "mod_001.vpk"), []byte("chunk2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.mod), "mod_note.vpk"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := build(t, f, Source{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true})
	if len(plan.Key.Mods) != 1 || len(plan.Key.Mods[0].Files) != 3 {
		t.Fatalf("mod stamps = %+v", plan.Key.Mods)
	}
	before := plan.Key.Mods[0].Files
	if err := os.WriteFile(chunk, []byte("changed chunk"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := build(t, f, Source{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true})
	if len(other.Key.Mods[0].Files) != 3 || other.Key.Mods[0].Files[1] == before[1] {
		t.Fatalf("companion change not stamped: %+v", other.Key.Mods[0].Files)
	}
	if err := os.WriteFile(filepath.Join(f.citadel, "steam.inf"), []byte("ClientVersion=101\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newer := build(t, f, Source{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true})
	if len(newer.Key.BaseGame) != len(other.Key.BaseGame) {
		t.Fatal("base stamp count changed")
	}
	last := newer.Key.BaseGame[len(newer.Key.BaseGame)-1]
	previous := other.Key.BaseGame[len(other.Key.BaseGame)-1]
	if last == previous {
		t.Fatalf("steam.inf stamp did not change: %+v", last)
	}
}

func TestBuildRejectsConflictsAndUnsafePaths(t *testing.T) {
	f := setup(t)
	fixed := map[string][]byte{"models/hero/hero.vmdl_c": []byte("a")}
	_, err := Build(f.citadel, f.output, nil, []Source{
		{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true, Fixed: fixed},
		{ModID: "b", VPKs: []string{f.mod}, CompatibilityEnabled: true, Fixed: map[string][]byte{"models/hero/hero.vmdl_c": []byte("b")}},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	if _, err := Build(f.citadel, f.output, nil, []Source{
		{ModID: "a", VPKs: []string{f.mod}}, {ModID: "a", VPKs: []string{f.mod}},
	}); err == nil {
		t.Fatal("duplicate mod accepted")
	}
	if _, err := Build(f.citadel, f.output, nil, []Source{
		{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true, Fixed: map[string][]byte{"../escape.vmdl_c": []byte("x")}},
	}); err == nil {
		t.Fatal("unsafe path accepted")
	}
	if _, err := Build(f.citadel, f.output, nil, []Source{
		{ModID: "a", VPKs: []string{f.mod}, Fixed: fixed},
	}); err == nil {
		t.Fatal("repair without compatibility opt-in accepted")
	}
}

func TestApplyRemovesPackageWhenReceiptCannotBeWritten(t *testing.T) {
	f := setup(t)
	plan := build(t, f, Source{
		ModID:                "a",
		VPKs:                 []string{f.mod},
		CompatibilityEnabled: true,
		Fixed:                map[string][]byte{"models/hero/hero.vmdl_c": []byte("fixed")},
	})
	if err := os.MkdirAll(filepath.Join(filepath.Dir(f.output), "compatibility.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, f.output); err == nil {
		t.Fatal("apply succeeded without a receipt")
	}
	if _, err := os.Stat(f.output); !os.IsNotExist(err) {
		t.Fatal("overlay survived a failed receipt")
	}
}

func TestApplyRejectsMismatchedOutputPath(t *testing.T) {
	f := setup(t)
	plan := build(t, f, Source{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true})
	if _, err := Apply(plan, filepath.Join(f.citadel, "elsewhere", "pak01_dir.vpk")); err == nil {
		t.Fatal("mismatched output accepted")
	}
}

func TestBuildWithDataIncludesMergedTablesAndRefusesCollisions(t *testing.T) {
	f := setup(t)
	build := uint32(100)
	merged := map[string][]byte{"scripts/heroes.vdata_c": []byte("merged table")}
	plan, err := BuildWithData(f.citadel, f.output, &build, []Source{
		{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true},
	}, merged)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plan.Files["scripts/heroes.vdata_c"], merged["scripts/heroes.vdata_c"]) {
		t.Fatalf("merged file = %q", plan.Files["scripts/heroes.vdata_c"])
	}
	_, err = BuildWithData(f.citadel, f.output, &build, []Source{
		{ModID: "a", VPKs: []string{f.mod}, CompatibilityEnabled: true,
			Fixed: map[string][]byte{"scripts/heroes.vdata_c": []byte("mod table")}},
	}, merged)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("collision error = %v", err)
	}
}
