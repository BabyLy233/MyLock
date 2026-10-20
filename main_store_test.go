package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/gb"
)

// catalogueReply is a page of the catalogue, as the API sends it: two mods of
// the kind the store is showing.
func catalogueReply(names ...string) string {
	records := ""
	for i, name := range names {
		if i > 0 {
			records += ","
		}
		records += fmt.Sprintf(`{
			"_idRow": %d,
			"_sModelName": "Mod",
			"_sName": %q,
			"_sVersion": "1.3",
			"_aSubmitter": {"_sName": "Kirill Senzu"},
			"_aRootCategory": {"_sName": "Skins"},
			"_aSubCategory": {"_sName": "Yamato"},
			"_nLikeCount": 569,
			"_nViewCount": 210597,
			"_tsDateAdded": 1749645061,
			"_tsDateModified": 1792372517,
			"_bHasFiles": true
		}`, 599927+i, name)
	}
	return fmt.Sprintf(
		`{"_aMetadata": {"_nRecordCount": 4953, "_nPerpage": %d}, "_aRecords": [%s]}`,
		len(names), records)
}

// fakeCatalogue answers the API's listings and download pages with fixed ones,
// so that what the store shows can be read without asking GameBanana for it.
func fakeCatalogue(t *testing.T, a *app, reply func(path string) (int, string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := reply(r.URL.Path)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	a.catalogue.Base = srv.URL + "/"
	return srv
}

// storeWith opens the store on a page of the catalogue, without a request: what
// a test of what the page shows needs is the page, not the asking for it.
func storeWith(t *testing.T, a *app, items ...gb.Submission) ui.Tester {
	t.Helper()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items, a.store.total, a.store.more = items, len(items), false
	a.store.mu.Unlock()
	a.router.Push("/store")
	return *ui.NewTester(a.view, 1280, 800)
}

// mod is one catalogue entry, as a listing would have handed it over.
func mod(name string) gb.Submission {
	return gb.Submission{
		ID:          599927,
		Kind:        gb.Mod,
		Name:        name,
		Author:      "Kirill Senzu",
		Category:    "Skins",
		Subcategory: "Yamato",
		Likes:       569,
		HasFiles:    true,
	}
}

func TestStoreShowsTheCatalogue(t *testing.T) {
	a := newApp()
	tt := storeWith(t, a, mod("Yamato redesign"), mod("Holliday Remodel"))

	for _, want := range []string{"模组商店", "Yamato redesign", "Holliday Remodel", "Skins / Yamato", "Kirill Senzu"} {
		if !tt.HasText(want) {
			t.Errorf("the store does not show %q: %q", want, tt.Texts())
		}
	}
}

// The catalogue is read the first time the page is shown, which is a request,
// and what it is doing is on screen while it runs.
func TestStoreShowsThatItIsReading(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.loading = true
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("正在读取商店…") {
		t.Errorf("the store does not say it is reading: %q", tt.Texts())
	}
}

// A catalogue that could not be read is reported, and offered again.
func TestStoreShowsWhyItCouldNotReadTheCatalogue(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.err = "gamebanana Mod/Index: 429 Too Many Requests"
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("429 Too Many Requests") {
		t.Errorf("the store does not show the error: %q", tt.Texts())
	}
	if !tt.HasText("重试") {
		t.Errorf("the store does not offer to try again: %q", tt.Texts())
	}
}

func TestStoreShowsNothingFound(t *testing.T) {
	a := newApp()
	tt := storeWith(t, a)

	if !tt.HasText("没有找到模组。") {
		t.Errorf("the store does not show its empty state: %q", tt.Texts())
	}
}

// Opening a mod shows what can be downloaded for it, which is what installing
// it is a choice of.
func TestStoreShowsAModsFiles(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.files = []gb.File{
		{ID: 1, Name: "yamato_red_d0d08.rar", Size: 24525612, Description: "Original mod colors", Scan: "clean"},
		{ID: 2, Name: "yamato_blue_c1f72.rar", Size: 31777079, Scan: "suspicious"},
	}
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{
		"yamato_red_d0d08.rar", "Original mod colors", "23.4 MB",
		"yamato_blue_c1f72.rar", "30.3 MB",
		// A file the API's own scan had something to say about is worth saying.
		"扫描结果 suspicious",
		"安装", "返回",
	} {
		if !tt.HasText(want) {
			t.Errorf("the mod's page does not show %q: %q", want, tt.Texts())
		}
	}
}

// Clicking a card opens the mod, which is the whole of what the grid does.
func TestClickingACardOpensTheMod(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(string) (int, string) {
		return http.StatusOK, `{"_aFiles": [{
			"_idRow": 1, "_sFile": "pak01_dir.vpk", "_nFilesize": 1024,
			"_sDownloadUrl": "https://gamebanana.com/dl/1"
		}]}`
	})
	tt := storeWith(t, a, mod("Yamato redesign"), mod("Holliday Remodel"))

	if err := tt.Click("Yamato redesign"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the mod's files to be read", func() bool {
		state := a.store.shown()
		return state.chosen >= 0 && !state.filesLoading
	})

	// The frame is rebuilt once the reply has landed: what the tester last drew
	// is the one from before it.
	tt.Frame()
	state := a.store.shown()
	if state.chosen != 0 {
		t.Fatalf("the store opened item %d, want the one that was clicked", state.chosen)
	}
	if len(state.files) != 1 || state.files[0].Name != "pak01_dir.vpk" {
		t.Errorf("the mod's files read as %+v", state.files)
	}
	if !tt.HasText("pak01_dir.vpk") {
		t.Errorf("the mod's page did not show: %q", tt.Texts())
	}
}

// Going back is how the user gets from a mod to the catalogue again.
func TestTheStoreGoesBack(t *testing.T) {
	a := newApp()
	tt := storeWith(t, a, mod("Yamato redesign"))
	a.store.mu.Lock()
	a.store.chosen = 0
	a.store.mu.Unlock()
	tt.Frame()

	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.store.shown().chosen; got != -1 {
		t.Errorf("the store is showing item %d after 返回, want the catalogue", got)
	}
}

// The store asks the API for the game's own catalogue and nothing else, which
// is what the tabs are for.
func TestTheStoreAsksForTheKindThatIsChosen(t *testing.T) {
	a := newApp()
	var asked []string
	fakeCatalogue(t, a, func(path string) (int, string) {
		asked = append(asked, path)
		return http.StatusOK, catalogueReply("Yamato redesign")
	})

	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	if err := tt.Click("音效"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sound listing to be read", func() bool {
		return !a.store.shown().loading && len(asked) > 0
	})

	if len(asked) == 0 || asked[len(asked)-1] != "/Sound/Index" {
		t.Errorf("the store asked for %v, want the Sound listing", asked)
	}
}

// Searching asks the API's own search, which is the only way to find a mod by
// name among five thousand.
func TestSearchingAsksTheSearch(t *testing.T) {
	a := newApp()
	var asked []string
	fakeCatalogue(t, a, func(path string) (int, string) {
		asked = append(asked, path)
		return http.StatusOK, catalogueReply("Yamato redesign")
	})

	tt := storeWith(t, a, mod("Yamato redesign"))
	a.store.query = "yamato"
	if err := tt.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the search to be read", func() bool {
		return !a.store.shown().loading && len(asked) > 0
	})

	if asked[len(asked)-1] != "/Util/Search/Results" {
		t.Errorf("the store asked for %v, want the search", asked)
	}
}
