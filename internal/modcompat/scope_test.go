package modcompat

import (
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func TestScopesCollectOtherModelsAndRegistryOverrides(t *testing.T) {
	first := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/first.vmdl_c", Data: []byte("a")},
	})
	second := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/second.vmdl_c", Data: []byte("b")},
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
