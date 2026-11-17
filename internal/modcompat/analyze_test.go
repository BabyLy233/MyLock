package modcompat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func packMod(t *testing.T, entries []vpk.PackEntry) string {
	t.Helper()
	data, err := vpk.Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mod_dir.vpk")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeReportsCameraChecksAndEmptyFindings(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "materials/hero.vmat_c", Data: []byte("material")},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Checks) != len(CameraChecks) || analysis.ModelsChecked != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Warnings) != 0 || analysis.Fixed != nil {
		t.Fatalf("unexpected findings: %+v", analysis)
	}
}

func TestAnalyzeRejectsAnUnreadableMod(t *testing.T) {
	if _, err := Analyze("mod", []string{filepath.Join(t.TempDir(), "missing.vpk")}, nil, Detect); err == nil {
		t.Fatal("missing VPK accepted")
	}
}

func TestAnalyzeFixModeReturnsAMap(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "scripts/hero.txt", Data: []byte("data")},
	})
	analysis, err := Analyze("mod", []string{mod}, &source2.GameIndex{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Fixed == nil {
		t.Fatal("fix mode did not initialise Fixed")
	}
}

func TestModelPathsNormalizeAndDeduplicate(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: []byte("a")},
		{Path: "materials/hero.vmat_c", Data: []byte("c")},
	})
	paths, err := ModelPaths([]string{mod})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "models/hero/hero.vmdl_c" {
		t.Fatalf("paths = %v", paths)
	}
	if _, err := ModelPaths([]string{filepath.Join(t.TempDir(), "missing.vpk")}); err == nil {
		t.Fatal("missing VPK accepted")
	}
}
