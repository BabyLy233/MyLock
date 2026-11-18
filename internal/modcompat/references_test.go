package modcompat

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func TestAnalyzeReportsUnloadedModels(t *testing.T) {
	table := compiledBlock(t, new(kv3.Object(
		kv3.Pair{Key: "entry", Value: kv3.Object(
			kv3.Pair{Key: "model", Value: kv3.String("models/a.vmdl")},
		)},
	)))
	mod := packMod(t, []vpk.PackEntry{
		{Path: "scripts/foo.vdata_c", Data: table},
		{Path: "models/a.vmdl_c", Data: compiledBlock(t, new(kv3.Object()))},
		{Path: "models/b.vmdl_c", Data: compiledBlock(t, new(kv3.Object()))},
	})
	game := gameIndexWith(t, []vpk.PackEntry{{Path: "scripts/foo.vdata_c", Data: table}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.UnloadedModels) != 1 || analysis.UnloadedModels[0] != "models/b.vmdl_c" {
		t.Fatalf("unloaded = %v", analysis.UnloadedModels)
	}
}

func TestPropModelsExcludeRegisteredHeroesAndRelocatedModels(t *testing.T) {
	registry := compiledBlock(t, new(kv3.Object(
		kv3.Pair{Key: "hero_a", Value: kv3.Object(
			kv3.Pair{Key: "m_strModelName", Value: kv3.String("models/heroes/a/a.vmdl")},
		)},
	)))
	game := gameIndexWith(t, []vpk.PackEntry{{Path: "scripts/heroes.vdata_c", Data: registry}})
	props := propModels(game, func(string) ([]byte, error) { return nil, nil },
		[]string{"models/heroes/a/a.vmdl_c", "models/props/particle.vmdl_c",
			"models/heroes/old/old.vmdl_c"},
		map[string]struct{}{"models/heroes/old/old.vmdl_c": {}})
	if len(props) != 1 || props[0] != "models/props/particle.vmdl_c" {
		t.Fatalf("props = %v", props)
	}
}
