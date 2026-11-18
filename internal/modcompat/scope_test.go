package modcompat

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func TestScopesCollectOtherModelsAndRegistryOverrides(t *testing.T) {
	first := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/first.vmdl_c", Data: []byte("a")},
	})
	second := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/second.vmdl_c", Data: []byte("b")},
		{Path: "animgraphs/second.vnmgraph_c", Data: []byte("graph")},
		{Path: "scripts/heroes.vdata_c", Data: []byte("registry")},
	})
	scopes, err := Scopes([]ModFiles{
		{ID: "first", VPKs: []string{first}},
		{ID: "second", VPKs: []string{second}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scopes["first"].OtherModelPaths["models/hero/second.vmdl_c"]; !ok {
		t.Fatalf("first scope = %+v", scopes["first"])
	}
	if _, ok := scopes["first"].OtherAnimationPaths["animgraphs/second.vnmgraph_c"]; !ok {
		t.Fatalf("first animation scope = %+v", scopes["first"])
	}
	if !scopes["first"].RegistryModified {
		t.Fatal("registry override was not reported")
	}
	if _, ok := scopes["second"].OtherModelPaths["models/hero/first.vmdl_c"]; !ok {
		t.Fatalf("second scope = %+v", scopes["second"])
	}
	if scopes["second"].RegistryModified {
		t.Fatal("a mod's own registry override was attributed to another mod")
	}
}

func TestScopesRejectUnreadableArchives(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.vpk")
	if _, err := Scopes([]ModFiles{{ID: "mod", VPKs: []string{missing}}}); err == nil {
		t.Fatal("missing archive accepted")
	}
	if _, err := ProvidesPath([]string{missing}, "models/hero/hero.vmdl"); err == nil {
		t.Fatal("missing archive accepted")
	}
}

func TestProvidesPathNormalizesResourceNames(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: []byte("a")},
		{Path: "scripts/heroes.vdata_c", Data: []byte("registry")},
	})
	for _, name := range []string{
		"models/hero/hero.vmdl",
		"models/hero/hero.vmdl_c",
		`MODELS\Hero\Hero.VMDL`,
	} {
		provided, err := ProvidesPath([]string{mod}, name)
		if err != nil {
			t.Fatal(err)
		}
		if !provided {
			t.Fatalf("%s was not found", name)
		}
	}
	provided, err := ProvidesPath([]string{mod}, "scripts/heroes.vdata_c")
	if err != nil || !provided {
		t.Fatalf("registry provided = %v, err = %v", provided, err)
	}
	provided, err = ProvidesPath([]string{mod}, "models/other/other.vmdl")
	if err != nil || provided {
		t.Fatalf("unexpected model provided = %v, err = %v", provided, err)
	}
}

func TestRepairScopeProtectsMergedFilesFromNonSelectedMods(t *testing.T) {
	const path = "resource/localization/example/example_english.txt"
	const table = "scripts/heroes.vdata_c"
	selected := packMod(t, []vpk.PackEntry{
		{Path: path, Data: []byte("selected text")},
		{Path: table, Data: []byte("selected table")},
	})
	original := packMod(t, []vpk.PackEntry{
		{Path: path, Data: []byte("original text")},
		{Path: table, Data: []byte("original table")},
	})
	for _, order := range [][]ModFiles{
		{{ID: "selected", VPKs: []string{selected}}, {ID: "original", VPKs: []string{original}}},
		{{ID: "original", VPKs: []string{original}}, {ID: "selected", VPKs: []string{selected}}},
	} {
		scope, err := NewRepairScope(order, map[string]struct{}{"selected": {}})
		if err != nil {
			t.Fatal(err)
		}
		if !scope.Protects(path) || !scope.Protects(table) {
			t.Fatalf("merged paths were not protected: %v", scope.ProtectedPaths())
		}
		if owners := scope.ModIDs(path); len(owners) != 1 || owners[0] != "original" {
			t.Fatalf("owners = %v", owners)
		}
		protected, err := scope.ProtectedFor([]string{selected})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(protected, []string{path, table}) {
			t.Fatalf("protected for selected = %v", protected)
		}
	}
}

func TestRepairScopeProtectsBinaryAssetsOnlyWhenTheOtherModWins(t *testing.T) {
	const path = "models/custom.vmdl_c"
	selected := packMod(t, []vpk.PackEntry{{Path: path, Data: []byte("selected")}})
	original := packMod(t, []vpk.PackEntry{{Path: path, Data: []byte("original")}})

	first := []ModFiles{
		{ID: "selected", VPKs: []string{selected}},
		{ID: "original", VPKs: []string{original}},
	}
	scope, err := NewRepairScope(first, map[string]struct{}{"selected": {}})
	if err != nil {
		t.Fatal(err)
	}
	if scope.Protects(path) {
		t.Fatal("a losing asset copy protected the selected mod's repair")
	}

	second := []ModFiles{
		{ID: "original", VPKs: []string{original}},
		{ID: "selected", VPKs: []string{selected}},
	}
	scope, err = NewRepairScope(second, map[string]struct{}{"selected": {}})
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Protects(path) {
		t.Fatal("the winning original mod did not protect the path")
	}
}

func TestRepairScopeIgnoresUnrelatedMods(t *testing.T) {
	selected := packMod(t, []vpk.PackEntry{
		{Path: "resource/localization/example/example_english.txt", Data: []byte("text")},
	})
	unrelated := packMod(t, []vpk.PackEntry{
		{Path: "scripts/custom.vdata_c", Data: []byte("not parseable")},
		{Path: "sounds/own.vsnd_c", Data: []byte("sound")},
	})
	scope, err := NewRepairScope([]ModFiles{
		{ID: "selected", VPKs: []string{selected}},
		{ID: "unrelated", VPKs: []string{unrelated}},
	}, map[string]struct{}{"selected": {}})
	if err != nil {
		t.Fatal(err)
	}
	if paths, err := scope.ProtectedFor([]string{selected}); err != nil {
		t.Fatal(err)
	} else if len(paths) != 0 {
		t.Fatalf("unrelated files protected for the selected mod: %v", paths)
	}
}

func TestRemoveProtectedDropsFindingsAndFixedBytes(t *testing.T) {
	const path = "resource/localization/example/example_english.txt"
	const model = "models/custom.vmdl_c"
	selected := packMod(t, []vpk.PackEntry{
		{Path: path, Data: []byte("selected")},
		{Path: model, Data: []byte("model")},
	})
	original := packMod(t, []vpk.PackEntry{{Path: path, Data: []byte("original")}})
	scope, err := NewRepairScope([]ModFiles{
		{ID: "selected", VPKs: []string{selected}},
		{ID: "original", VPKs: []string{original}},
	}, map[string]struct{}{"selected": {}})
	if err != nil {
		t.Fatal(err)
	}
	analysis := Analysis{
		Repairs: []Repair{
			{FilePath: path, Kind: RepairRestoredGameData},
			{FilePath: model, Kind: RepairCameraAttachmentFrame},
		},
		Warnings: []Warning{{FilePath: path}},
		DataRepairs: []vdata.DataRepair{
			{FilePath: path, Kind: vdata.RepairRestoredGameData},
		},
		DataWarnings: []vdata.DataWarning{{FilePath: path}},
		Fixed: map[string][]byte{
			path:  []byte("merged"),
			model: []byte("fixed model"),
		},
	}
	analysis.RemoveProtected(scope)
	if len(analysis.Repairs) != 1 || analysis.Repairs[0].FilePath != model {
		t.Fatalf("repairs = %+v", analysis.Repairs)
	}
	if len(analysis.Warnings) != 0 || len(analysis.DataRepairs) != 0 || len(analysis.DataWarnings) != 0 {
		t.Fatalf("protected findings kept: %+v", analysis)
	}
	if _, ok := analysis.Fixed[path]; ok {
		t.Fatal("protected fixed bytes kept")
	}
	if _, ok := analysis.Fixed[model]; !ok {
		t.Fatal("unprotected fixed bytes were dropped")
	}
}
