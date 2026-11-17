package app

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
)

// The mods page can show the installed mods as a grid of cards, and remembers
// which layout was chosen for the next run.
func TestModsPageShowsTheModsAsAGrid(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	settingsPath(t, a)
	tt := show(t, a, install, profile)

	if err := tt.Click("网格"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := a.state().modsLayout; got != modsLayoutGrid {
		t.Fatalf("the layout is %q after clicking 网格, want %q", got, modsLayoutGrid)
	}
	if !tt.HasText("商店") {
		t.Errorf("the grid does not offer the store on a card: %q", tt.Texts())
	}
	waitFor(t, "the layout to be saved", func() bool {
		cfg, err := config.Load(a.configPath)
		return err == nil && cfg.ModsLayout == modsLayoutGrid
	})
}

// A grid card's switch is the same switch a list row has.
func TestAGridCardsSwitchEnablesTheMod(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	a.modsLayout = modsLayoutGrid
	tt := show(t, a, install, profile)

	if err := tt.Click("启用 snd-42"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })

	if got := a.installed().list; len(got) != 1 || !got[0].Enabled {
		t.Errorf("the grid shows %+v, want the mod enabled", got)
	}
}

// Every mod offers its own page in the store, where the author's write-up and
// its files are, rather than sending the user to a browser.
func TestAModsStoreButtonOpensTheStore(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "599927", "yamato.vpk")
	a.library = map[string]config.ModInfo{
		"599927": {Name: "Yamato Cyber", Category: "Skins", Subcategory: "Yamato"},
	}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		if strings.HasSuffix(path, "/ProfilePage") {
			return http.StatusOK, `{
				"_idRow": 599927,
				"_sName": "Yamato Cyber",
				"_sText": "A new look for Yamato",
				"_sProfileUrl": "https://gamebanana.com/mods/599927",
				"_aSuperCategory": {"_sName": "Skins"},
				"_aCategory": {"_sName": "Yamato"},
				"_aFiles": []
			}`
		}
		return http.StatusOK, `{"_aMetadata": {"_nRecordCount": 0, "_nPerpage": 50}, "_aRecords": []}`
	})
	tt := show(t, a, install, profile)

	if err := tt.Click("在商店中查看 Yamato Cyber"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the mod's page to be read", func() bool {
		state := a.store.shown()
		return state.opened && !state.profileBusy && state.mod.ID == 599927
	})
	tt.Frame()

	if got := a.router.Path(); got != "/store" {
		t.Fatalf("the router is at %q after clicking 商店, want /store", got)
	}
	if urls := tt.OpenedURLs(); len(urls) > 0 {
		t.Errorf("商店 opened %v in a browser", urls)
	}
	if !tt.HasText("A new look for Yamato") {
		t.Errorf("the mod's page did not open: %q", tt.Texts())
	}
}

// modIDs returns the ids of mods in the order they are in.
func modIDs(mods []addons.Mod) []string {
	ids := make([]string, len(mods))
	for i, mod := range mods {
		ids[i] = mod.ID
	}
	return ids
}

// The page orders the mods by the name it shows them under, which is the
// library's name or the id, rather than by the game's load order.
func TestSortedModsUsesTheShownName(t *testing.T) {
	mods := []addons.Mod{{ID: "zulu"}, {ID: "599927"}, {ID: "alpha"}}
	infos := map[string]config.ModInfo{
		"zulu":   {Name: "Zeta"},
		"599927": {Name: "Beta"},
	}

	got := modIDs(sortedMods(mods, infos, modsSortName))
	want := []string{"alpha", "599927", "zulu"}
	if !slices.Equal(got, want) {
		t.Errorf("the name order is %v, want %v", got, want)
	}
	if mods[0].ID != "zulu" {
		t.Errorf("sortedMods reordered the slice it was given: %v", modIDs(mods))
	}
}

// The empty order a first run saves, and one this version does not know, are
// both the name's.
func TestModsSortDefaultsToTheName(t *testing.T) {
	for _, saved := range []string{"", "something-old"} {
		if got := modsSorts[modsSortIndex(saved)]; got != modsSortName {
			t.Errorf("the order saved as %q shows %q, want %q", saved, got, modsSortName)
		}
	}
}

// By install time the newest mod comes first, and a mod with no files on disk
// -- so no install time -- comes last rather than first.
func TestSortedModsByInstallTimePutsTheNewestFirst(t *testing.T) {
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mods := []addons.Mod{
		{ID: "old", Modified: old},
		{ID: "missing"},
		{ID: "new", Modified: old.Add(48 * time.Hour)},
		{ID: "middle", Modified: old.Add(24 * time.Hour)},
	}

	got := modIDs(sortedMods(mods, nil, modsSortInstalled))
	want := []string{"new", "middle", "old", "missing"}
	if !slices.Equal(got, want) {
		t.Errorf("the install-time order is %v, want %v", got, want)
	}
}

// Turning a mod on or off leaves it where it was. The page used to show the
// enabled mods first, so flipping a switch moved the row that was clicked, and
// the click after it could land on another mod.
func TestSwitchingAModKeepsItsPlaceInTheList(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	addMod(t, profile, "skin-pack", "skin.vpk")
	tt := show(t, a, install, profile)

	top, ok := tt.Find("skin-pack")
	if !ok {
		t.Fatalf("the page does not show skin-pack: %q", tt.Texts())
	}
	bottom, ok := tt.Find("snd-42")
	if !ok {
		t.Fatalf("the page does not show snd-42: %q", tt.Texts())
	}
	if top.Y >= bottom.Y {
		t.Fatalf("the name order puts snd-42 above skin-pack: %.1f and %.1f", bottom.Y, top.Y)
	}

	if err := tt.Click("启用 snd-42"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })
	tt.Frame()

	top, ok = tt.Find("skin-pack")
	if !ok {
		t.Fatalf("the page stopped showing skin-pack: %q", tt.Texts())
	}
	bottom, ok = tt.Find("snd-42")
	if !ok {
		t.Fatalf("the page stopped showing snd-42: %q", tt.Texts())
	}
	if top.Y >= bottom.Y {
		t.Errorf("enabling snd-42 moved it above skin-pack: %.1f and %.1f", bottom.Y, top.Y)
	}
}

// The page can order the mods by install time instead, newest first, and
// remembers the choice for the next run. Enabling a mod does not change when
// it was installed, so it does not move in this order either.
func TestModsPageCanOrderByInstallTime(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "alpha", "a.vpk")
	addMod(t, profile, "zulu", "z.vpk")
	settingsPath(t, a)
	// The name order puts alpha first; the files' times put zulu first.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(profile.Dir, "alpha_a.vpk"), base, base); err != nil {
		t.Fatal(err)
	}
	newer := base.Add(24 * time.Hour)
	if err := os.Chtimes(filepath.Join(profile.Dir, "zulu_z.vpk"), newer, newer); err != nil {
		t.Fatal(err)
	}
	tt := show(t, a, install, profile)

	if err := tt.Click("按安装时间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	zulu, ok := tt.Find("zulu")
	if !ok {
		t.Fatalf("the page does not show zulu: %q", tt.Texts())
	}
	alpha, ok := tt.Find("alpha")
	if !ok {
		t.Fatalf("the page does not show alpha: %q", tt.Texts())
	}
	if zulu.Y >= alpha.Y {
		t.Errorf("the install-time order puts alpha above zulu: %.1f and %.1f", alpha.Y, zulu.Y)
	}

	if err := tt.Click("启用 zulu"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })
	tt.Frame()

	zulu, _ = tt.Find("zulu")
	alpha, _ = tt.Find("alpha")
	if zulu.Y >= alpha.Y {
		t.Errorf("enabling zulu moved it below alpha in the install-time order: %.1f and %.1f", zulu.Y, alpha.Y)
	}

	waitFor(t, "the order to be saved", func() bool {
		cfg, err := config.Load(a.configPath)
		return err == nil && cfg.ModsSort == modsSortInstalled
	})
}
