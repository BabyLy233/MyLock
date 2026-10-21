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

// fakeCatalogue answers the API's listings and submissions' pages with fixed
// ones, so that what the store shows can be read without asking GameBanana for
// it.
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

// Opening a mod shows what its author wrote, how it has been received, and what
// can be downloaded for it, which is what installing it is a choice of.
func TestStoreShowsAModsPage(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.profile = gb.Profile{
		Text:      "Redesigned Yamato with a new look",
		Downloads: 785695,
		URL:       "https://gamebanana.com/mods/599927",
		Files: []gb.File{
			{ID: 1, Name: "yamato_red_d0d08.rar", Size: 24525612, Description: "Original mod colors", Scan: "clean"},
			{ID: 2, Name: "yamato_blue_c1f72.rar", Size: 31777079, Scan: "suspicious"},
		},
	}
	a.store.writeUp = a.store.profile.Text
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{
		// What the author wrote, which only the mod's own page has.
		"Redesigned Yamato with a new look",
		// A count of hundreds of thousands is read with the separators the site
		// writes it with.
		"下载 785,695",
		"在浏览器打开原帖",
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

// The pictures the author posted are shown in the row the page has for them.
func TestStoreShowsAModsPictures(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.profile = gb.Profile{Images: []gb.Image{
		{Thumb: "https://images.gamebanana.com/img/ss/mods/a.jpg"},
		{Thumb: "https://images.gamebanana.com/img/ss/mods/b.jpg"},
	}}
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	// The row has no text of its own, so it is found by its name. Asking for a
	// picture is what drawing it is, so that the fetch started is what says the
	// pictures are on the page at all.
	if !tt.HasText("截图") {
		t.Errorf("the mod's page shows no pictures: %q", tt.Texts())
	}
	// They are laid out beside each other with room between them, which is what
	// makes a row of pictures read as several rather than as one wide one.
	first, ok := tt.Find("第 1 张截图")
	if !ok {
		t.Fatalf("the mod's page does not show its first picture: %q", tt.Texts())
	}
	second, ok := tt.Find("第 2 张截图")
	if !ok {
		t.Fatalf("the mod's page does not show its second picture: %q", tt.Texts())
	}
	if gap := second.X - (first.X + first.W); gap < 8 {
		t.Errorf("the pictures are drawn %v apart, which is not a gap", gap)
	}

	a.thumbnails.mu.Lock()
	defer a.thumbnails.mu.Unlock()
	asked := map[string]bool{}
	for want := range a.thumbnails.asked {
		asked[want.address] = true
	}
	for _, want := range []string{
		"https://images.gamebanana.com/img/ss/mods/a.jpg",
		"https://images.gamebanana.com/img/ss/mods/b.jpg",
	} {
		if !asked[want] {
			t.Errorf("the page did not ask for %q", want)
		}
	}
}

// A mod's page is drawn inside the window. It holds more than one screen --
// what an author wrote runs to whatever length they wrote, and the files below
// it are as many as they published -- so the parts of it that are on the first
// screen are the parts the user reaches without scrolling, and one of them
// falling outside the window would be a part nobody can reach.
func TestTheModsPageIsDrawnInsideTheWindow(t *testing.T) {
	const width, height = 1280, 800
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.profile = gb.Profile{
		Text:      "Redesigned Yamato with a new look",
		Downloads: 785695,
		Images:    []gb.Image{{Thumb: "https://images.gamebanana.com/img/ss/mods/a.jpg"}},
		Files:     []gb.File{{ID: 1, Name: "yamato_red_d0d08.rar", Size: 24525612}},
	}
	a.store.writeUp = a.store.profile.Text
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, width, height)

	for _, name := range []string{
		"返回",
		"Redesigned Yamato with a new look",
		"截图", "在浏览器打开原帖", "安装",
	} {
		r, ok := tt.Find(name)
		if !ok {
			t.Errorf("the mod's page does not show %q", name)
			continue
		}
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("%q is drawn with no room: %+v", name, r)
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > width || r.Y+r.H > height {
			t.Errorf("%q is drawn outside the window: %+v", name, r)
		}
	}
}

// A write-up runs to whatever length its author wrote, and the page grows with
// it: what follows it is pushed down rather than drawn over. That is what
// drawing it as text of its own height gets, and what a box of a height the page
// has to pick for it would not.
func TestTheWriteUpIsAsTallAsWhatItSays(t *testing.T) {
	page := func(writeUp string) *ui.Tester {
		a := newApp()
		a.store.started = true
		a.store.mu.Lock()
		a.store.items = []gb.Submission{mod("Yamato redesign")}
		a.store.chosen = 0
		a.store.profile = gb.Profile{Files: []gb.File{{ID: 1, Name: "a.vpk"}}}
		a.store.writeUp = writeUp
		a.store.mu.Unlock()
		a.router.Push("/store")
		return ui.NewTester(a.view, 1280, 800)
	}

	short, ok := page("很短的一句话。").Find("在浏览器打开原帖")
	if !ok {
		t.Fatal("the mod's page does not show the link to its own page")
	}

	long := ""
	for range 8 {
		long += "作者写的说明文字，一句一句地写下来。\n\n"
	}
	far, ok := page(long).Find("在浏览器打开原帖")
	if !ok {
		t.Fatal("the mod's page does not show the link to its own page")
	}
	if far.Y <= short.Y {
		t.Errorf("a write-up of eight paragraphs left the link at %v, where one line left it at %v",
			far.Y, short.Y)
	}
}

// A mod whose author posted no picture and wrote nothing is drawn without the
// row and the write-up rather than around an empty one of each: the page is
// what its author published, and there is nothing here to fill.
func TestStoreShowsAModWithNothingWrittenOrPosted(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.profile = gb.Profile{Files: []gb.File{{ID: 1, Name: "a.vpk"}}}
	a.store.mu.Unlock()
	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)

	if tt.HasText("截图") {
		t.Errorf("the mod's page shows a row of pictures it has none for: %q", tt.Texts())
	}
	if got := a.store.shown().writeUp; got != "" {
		t.Errorf("the mod's page holds a write-up of %q", got)
	}
	if !tt.HasText("选择要安装的文件") {
		t.Errorf("the mod's page does not offer its files: %q", tt.Texts())
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
	waitFor(t, "the mod's page to be read", func() bool {
		state := a.store.shown()
		return state.chosen >= 0 && !state.profileBusy
	})

	// The frame is rebuilt once the reply has landed: what the tester last drew
	// is the one from before it.
	tt.Frame()
	state := a.store.shown()
	if state.chosen != 0 {
		t.Fatalf("the store opened item %d, want the one that was clicked", state.chosen)
	}
	if len(state.profile.Files) != 1 || state.profile.Files[0].Name != "pak01_dir.vpk" {
		t.Errorf("the mod's files read as %+v", state.profile.Files)
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
