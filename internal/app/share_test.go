package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/gb"
)

// parseShare is the one place that decides whether what was typed into the
// search box is a search or a share link, so it is read against every shape the
// app copies or accepts.
func TestParseShare(t *testing.T) {
	for _, tt := range []struct {
		text string
		tab  gb.Kind
		kind gb.Kind
		id   int
		ok   bool
	}{
		{"https://gamebanana.com/mods/599927", gb.Sound, gb.Mod, 599927, true},
		{"http://www.gamebanana.com/sounds/42/some-title", gb.Mod, gb.Sound, 42, true},
		// The address an address ends up as when a chat client makes it a link,
		// and the path on its own.
		{"gamebanana.com/wips/7", gb.Mod, gb.Wip, 7, true},
		{"/mods/1", gb.Sound, gb.Mod, 1, true},
		{"mods/2?tab=files", gb.Sound, gb.Mod, 2, true},
		// The app's own slugs, which is what a profile's manifest holds.
		{"snd-42", gb.Mod, gb.Sound, 42, true},
		{"wip-7", gb.Mod, gb.Wip, 7, true},
		// A number alone is the kind of the tab that is up: the three listings
		// number their submissions separately.
		{"599927", gb.Sound, gb.Sound, 599927, true},
		{"   https://gamebanana.com/mods/5   ", gb.Mod, gb.Mod, 5, true},

		{"yamato", gb.Mod, "", 0, false},
		{"https://example.com/mods/1", gb.Mod, "", 0, false},
		{"https://gamebanana.com/games/20948", gb.Mod, "", 0, false},
		{"mods/not-a-number", gb.Mod, "", 0, false},
		{"snd-", gb.Mod, "", 0, false},
		{"0", gb.Mod, "", 0, false},
		{"", gb.Mod, "", 0, false},
	} {
		kind, id, ok := parseShare(tt.text, tt.tab)
		if ok != tt.ok || kind != tt.kind || id != tt.id {
			t.Errorf("parseShare(%q, %q) = %q, %d, %v, want %q, %d, %v",
				tt.text, tt.tab, kind, id, ok, tt.kind, tt.id, tt.ok)
		}
	}
}

// The page copies the mod's address on the site, which is what another user of
// the app pastes into the search box.
func TestTheDetailPageCopiesAShareLink(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("复制分享链接"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got, want := tt.Clipboard(), "https://gamebanana.com/mods/599927"; got != want {
		t.Errorf("the clipboard holds %q, want %q", got, want)
	}
	if !tt.HasText("已复制") {
		t.Errorf("the page does not say the link was copied: %q", tt.Texts())
	}
}

// sharedSoundReply is a sound pack's page, which a share link opens without the
// listing having held it.
const sharedSoundReply = `{
	"_idRow": 42,
	"_sName": "Shared sound",
	"_sText": "A sound pack someone shared",
	"_sProfileUrl": "https://gamebanana.com/sounds/42",
	"_aSubmitter": {"_sName": "Shared Author"},
	"_aSuperCategory": {"_sName": "Audio"},
	"_aCategory": {"_sName": "Voice"},
	"_aFiles": [
		{"_idRow": 7, "_sFile": "shared.zip", "_nFilesize": 1024, "_sDownloadUrl": "https://gamebanana.com/dl/7"}
	]
}`

// A share link pasted into the search box opens the mod it names, even though
// the listing that was up did not hold it, and the tab moves to the link's kind.
func TestAPastedShareLinkOpensItsMod(t *testing.T) {
	a := newApp()
	asked := &askedPaths{}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		asked.add(path, query)
		if strings.HasSuffix(path, "/ProfilePage") {
			return http.StatusOK, sharedSoundReply
		}
		return http.StatusOK, `{"_aMetadata": {"_nRecordCount": 0, "_nPerpage": 50}, "_aRecords": []}`
	})
	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	a.store.query = "https://gamebanana.com/sounds/42"
	if err := tt.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shared mod's page to be read", func() bool {
		state := a.store.shown()
		return state.opened && !state.profileBusy && state.mod.ID == 42 && asked.has("/Sound/42/ProfilePage")
	})
	tt.Frame()

	state := a.store.shown()
	if state.mod.Kind != gb.Sound || state.mod.Name != "Shared sound" {
		t.Errorf("the page shows %+v, want sound 42", state.mod)
	}
	if a.store.query != "" {
		t.Errorf("the search box still holds %q after the jump", a.store.query)
	}
	for _, want := range []string{
		"Shared sound", "作者 Shared Author", "A sound pack someone shared",
		"在浏览器打开原帖", "shared.zip", "安装", "返回",
	} {
		if !tt.HasText(want) {
			t.Errorf("the shared mod's page does not show %q: %q", want, tt.Texts())
		}
	}
	if asked.has("/Util/Search/Results") {
		t.Errorf("the share link was searched for as text: %v", asked.all())
	}
	if !asked.has("/Sound/Index") {
		t.Errorf("the listing behind the page was not read for the link's kind: %v", asked.all())
	}
}

// A profile that cannot be read leaves the page up and says why, rather than
// dropping the user back into the tab they pasted the link from.
func TestASharedModThatCannotBeReadSaysSo(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		if strings.HasSuffix(path, "/ProfilePage") {
			return http.StatusBadGateway, `{"message": "offline"}`
		}
		return http.StatusOK, `{"_aMetadata": {"_nRecordCount": 0, "_nPerpage": 50}, "_aRecords": []}`
	})
	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	a.store.query = "https://gamebanana.com/mods/599927"
	if err := tt.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failed read to be reported", func() bool {
		state := a.store.shown()
		return state.opened && !state.profileBusy && state.profileErr != ""
	})
	tt.Frame()

	if !tt.HasText("在浏览器打开原帖") {
		t.Errorf("the page was left: %q", tt.Texts())
	}
	if !tt.HasText("offline") && !tt.HasText("502") {
		t.Errorf("the page does not say why the mod could not be read: %q", tt.Texts())
	}
}
