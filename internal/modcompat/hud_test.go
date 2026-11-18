// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only

package modcompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const hudLayoutPath = "panorama/layout/hud/test.vxml_c"

func hudLayout(t *testing.T, ids ...string) []byte {
	t.Helper()
	children := make([]kv3.Value, 0, len(ids))
	for _, id := range ids {
		children = append(children, kv3.Object(
			kv3.Pair{Key: "eType", Value: kv3.String("PANEL_ATTRIBUTE")},
			kv3.Pair{Key: "name", Value: kv3.String("id")},
			kv3.Pair{Key: "child", Value: kv3.Object(
				kv3.Pair{Key: "name", Value: kv3.String(id)},
			)},
		))
	}
	root := kv3.Object(
		kv3.Pair{Key: "eType", Value: kv3.String("PANEL")},
		kv3.Pair{Key: "name", Value: kv3.String("Root")},
		kv3.Pair{Key: "vecChildren", Value: kv3.Array(children...)},
	)
	ast := kv3.Object(kv3.Pair{Key: "m_AST", Value: kv3.Object(
		kv3.Pair{Key: "m_pRoot", Value: root},
	)})
	data, err := resource.Build([]resource.BuildResult{{
		Kind:    [4]byte{'L', 'a', 'C', 'o'},
		Payload: kv3.Encode(&ast, kv3.Format{}),
	}}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnalyzeReportsHudPanelsTheGameHasAndTheModLost(t *testing.T) {
	game := gameIndexWith(t, []vpk.PackEntry{{
		Path: hudLayoutPath, Data: hudLayout(t, "HealthBar", "BuyButton"),
	}})
	console := filepath.Join(game.Directories()[0], "console.log")
	if err := os.WriteFile(console, []byte(
		"Unable to find child 'BuyButton' in layout\nnothing to see\nlayout error elsewhere\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := packMod(t, []vpk.PackEntry{{
		Path: hudLayoutPath, Data: hudLayout(t, "HealthBar"),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.HudChecks) != 1 {
		t.Fatalf("hud checks = %+v", analysis.HudChecks)
	}
	check := analysis.HudChecks[0]
	if check.ModID != "mod" || check.FilePath != hudLayoutPath || check.Status != HudCompared {
		t.Fatalf("check = %+v", check)
	}
	if len(check.MissingPanels) != 1 || check.MissingPanels[0] != "BuyButton" {
		t.Fatalf("missing panels = %v", check.MissingPanels)
	}
	if len(check.Console.Lines) != 1 || !strings.Contains(check.Console.Lines[0], "BuyButton") {
		t.Fatalf("console = %+v", check.Console)
	}
	if check.Console.ModifiedAt == nil {
		t.Fatal("console modification time was not recorded")
	}
}

func TestHudWithoutABaselineOrLayoutsIsAdvisory(t *testing.T) {
	// A layout with no game counterpart is recorded, not treated as an error.
	game := gameIndexWith(t, []vpk.PackEntry{{Path: "models/hero.vmdl_c", Data: []byte("model")}})
	mod := packMod(t, []vpk.PackEntry{{
		Path: hudLayoutPath, Data: hudLayout(t, "HealthBar"),
	}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.HudChecks) != 1 || analysis.HudChecks[0].Status != HudNoBaseline {
		t.Fatalf("hud checks = %+v", analysis.HudChecks)
	}

	// A file that is not a readable layout is unsupported, not a repair.
	rawMod := packMod(t, []vpk.PackEntry{{Path: hudLayoutPath, Data: []byte("not a resource")}})
	rawGame := gameIndexWith(t, []vpk.PackEntry{{Path: hudLayoutPath, Data: []byte("not a resource")}})
	analysis, err = Analyze("mod", []string{rawMod}, rawGame, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.HudChecks) != 1 || analysis.HudChecks[0].Status != HudUnsupported {
		t.Fatalf("hud checks = %+v", analysis.HudChecks)
	}
}
