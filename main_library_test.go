package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/gb"
)

// An install remembers what the store knew, beside the manifest's id.
func TestRememberingAModKeepsItsNameAndDetails(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)

	a.rememberMod(gb.Submission{
		ID:          554012,
		Kind:        gb.Mod,
		Name:        "Bikinidicta",
		Version:     "v2",
		Author:      "Keller Max",
		Category:    "Skins",
		Subcategory: "Vindicta",
		Thumb:       "https://images.gamebanana.com/img/ss/mods/x.jpg",
	})

	info := a.modInfo("554012")
	if info.Name != "Bikinidicta" || info.Author != "Keller Max" {
		t.Errorf("the app remembers %+v, want its name and author", info)
	}
	if info.Category != "Skins" || info.Subcategory != "Vindicta" || info.Version != "v2" {
		t.Errorf("the app remembers %+v, want where it is filed and its version", info)
	}
	if !strings.HasSuffix(info.Page, "/mods/554012") {
		t.Errorf("Page = %q, want the submission's page", info.Page)
	}

	library, err := config.LoadLibrary(config.LibraryPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := library.Mods["554012"]; got.Name != "Bikinidicta" || got.Thumb == "" {
		t.Errorf("the library file holds %+v, want the mod's details", got)
	}
}

// A manifest id is turned back into the listing and number it came from, which
// is what the lookup asks the API for.
func TestReadingAModIdBackIntoTheCatalogue(t *testing.T) {
	for _, tt := range []struct {
		id     string
		kind   gb.Kind
		number int
	}{
		{"554012", gb.Mod, 554012},
		{"snd-42", gb.Sound, 42},
		{"wip-7", gb.Wip, 7},
	} {
		kind, number, ok := submissionRef(tt.id)
		if !ok || kind != tt.kind || number != tt.number {
			t.Errorf("submissionRef(%q) = %q, %d, %v; want %q, %d", tt.id, kind, number, ok, tt.kind, tt.number)
		}
	}
	for _, id := range []string{"", "snd-", "not-a-mod", "0", "-3"} {
		if _, _, ok := submissionRef(id); ok {
			t.Errorf("submissionRef(%q) was taken for a catalogue entry", id)
		}
	}
}

// A mod installed before the library existed, or by hand, has only an id in
// the manifest. The page shows the id first and the looked-up name when it
// arrives.
func TestLookingUpAnInstalledModFillsItsCard(t *testing.T) {
	a, install, profile, path := profileApp(t)
	addMod(t, profile, "554012", "skin.vpk")
	fakeCatalogue(t, a, func(p string, query url.Values) (int, string) {
		if strings.HasSuffix(p, "/Mod/554012/ProfilePage") {
			return 200, `{
				"_idRow": 554012,
				"_sName": "Bikini Vindicta",
				"_sVersion": "v2",
				"_aSubmitter": {"_sName": "Keller Max"},
				"_aSuperCategory": {"_sName": "Skins"},
				"_aCategory": {"_sName": "Vindicta"}
			}`
		}
		return 404, `{}`
	})

	tt := show(t, a, install, profile)
	if !tt.HasText("#554012") {
		t.Errorf("a mod with no details does not show its id: %q", tt.Texts())
	}

	a.fillModInfo(a.installed().list)
	waitFor(t, "the looked-up details", func() bool { return a.modInfo("554012").Name != "" })
	tt.Frame()

	for _, want := range []string{"Bikini Vindicta", "Keller Max", "Skins / Vindicta", "v2", "#554012"} {
		if !tt.HasText(want) {
			t.Errorf("the card does not show %q: %q", want, tt.Texts())
		}
	}
	library, err := config.LoadLibrary(config.LibraryPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := library.Mods["554012"]; got.Name != "Bikini Vindicta" {
		t.Errorf("the library file holds %+v, want the looked-up mod", got)
	}
}

// The card is built from what the library knows: the readable name, the
// details, and the id it is keyed by.
func TestTheCardShowsTheNameAndDetailsOfAKnownMod(t *testing.T) {
	a, install, profile, _ := profileApp(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	a.library = map[string]config.ModInfo{
		"snd-42": {
			Name:        "Voice Pack",
			Author:      "Someone",
			Category:    "Audio",
			Subcategory: "Voice",
			Version:     "1.2",
		},
	}
	tt := show(t, a, install, profile)

	for _, want := range []string{
		"Voice Pack", "Someone", "Audio / Voice", "1.2", "#snd-42",
		"无预览图", "1 个文件 · 已停用", "启用 snd-42", "删除 snd-42",
	} {
		if !tt.HasText(want) {
			t.Errorf("the card does not show %q: %q", want, tt.Texts())
		}
	}
}
