package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

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
