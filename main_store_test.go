package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/gb"
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

// categoryReply is the roots of a kind's category tree, as the API sends them.
const categoryReply = `[{"_idRow": 33295, "_sName": "Skins", "_nItemCount": 3364, "_nCategoryCount": 44}]`

// searchReply is a page of search results holding submissions of two
// categories, which is what the search endpoint sends when it was asked to
// narrow by one: it accepts the filter and ignores it.
const searchReply = `{"_aMetadata": {"_nRecordCount": 2, "_nPerpage": 2}, "_aRecords": [
	{"_idRow": 1, "_sModelName": "Mod", "_sName": "Yamato skin",
	 "_aRootCategory": {"_sName": "Skins"}, "_aSubCategory": {"_sName": "Yamato"}},
	{"_idRow": 2, "_sModelName": "Mod", "_sName": "Cursor pack",
	 "_aRootCategory": {"_sName": "HUD"}, "_aSubCategory": {"_sName": "Cursors"}}
]}`

// fakeCatalogue answers the API's listings and submissions' pages with fixed
// ones, so that what the store shows can be read without asking GameBanana for
// it. The category tree is answered here rather than by each test: the store
// asks for it when it opens and when the kind changes, and a test is not about
// that request.
func fakeCatalogue(t *testing.T, a *app, reply func(path string, query url.Values) (int, string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/Categories") {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, categoryReply)
			return
		}
		status, body := reply(r.URL.Path, r.URL.Query())
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	a.catalogue.Base = srv.URL + "/"
	return srv
}

// askedPaths records what a fake catalogue was asked for. The requests run on
// goroutines of their own, and the store can have more than one in flight.
type askedPaths struct {
	mu      sync.Mutex
	paths   []string
	queries []url.Values
}

func (a *askedPaths) add(path string, query url.Values) {
	a.mu.Lock()
	a.paths = append(a.paths, path)
	a.queries = append(a.queries, query)
	a.mu.Unlock()
}

func (a *askedPaths) has(path string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.paths, path)
}

func (a *askedPaths) all() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.paths)
}

// lastQuery is what a path was asked for the last time, or nil when it was
// never asked for.
func (a *askedPaths) lastQuery(path string) url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.paths) - 1; i >= 0; i-- {
		if a.paths[i] == path {
			return a.queries[i]
		}
	}
	return nil
}

// storeWith opens the store on a page of the catalogue, without a request: what
// a test of what the page shows needs is the page, not the asking for it.
func storeWith(t *testing.T, a *app, items ...gb.Submission) ui.Tester {
	t.Helper()
	a.store.started = true
	a.store.mu.Lock()
	a.store.raw = items
	a.store.total, a.store.more = len(items), false
	a.store.filterLocked()
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

// shot is one picture a submission carries, as the API hands it over: the small
// one the row of them shows and the whole one behind it.
func shot(n int) gb.Image {
	return gb.Image{
		Thumb: fmt.Sprintf("https://images.gamebanana.com/img/ss/mods/220-90_%d.jpg", n),
		URL:   fmt.Sprintf("https://images.gamebanana.com/img/ss/mods/%d.jpg", n),
	}
}

// storeShowing is the store showing one mod's own page, before a frame has been
// built. A test that answers the pictures itself has to take this rather than
// modPage: it is the first frame that starts a fetch, so what the pictures are
// fetched with has to be in place before it.
func storeShowing(t *testing.T, images ...gb.Image) *app {
	t.Helper()
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.profile = gb.Profile{Images: images}
	a.store.mu.Unlock()
	a.router.Push("/store")
	return a
}

// modPage is that page with a frame built, which is where a picture is opened
// from.
func modPage(t *testing.T, images ...gb.Image) (*app, *ui.Tester) {
	t.Helper()
	a := storeShowing(t, images...)
	return a, ui.NewTester(a.view, 1280, 800)
}

// Clicking a picture opens it whole. What the row of them shows is a thumbnail
// of a screenshot that was published at a size the row has no room for, and
// seeing that is what opening one is for.
func TestClickingAScreenshotOpensItWhole(t *testing.T) {
	a, tt := modPage(t, shot(1), shot(2))

	if err := tt.Click("第 1 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !a.store.viewer.open {
		t.Fatal("the picture did not open")
	}
	if got := a.store.viewer.index; got != 0 {
		t.Errorf("the viewer opened at picture %d, want the one that was clicked", got)
	}
	if !tt.HasText("第 1 / 共 2 张") {
		t.Errorf("the viewer does not say which picture it is showing: %q", tt.Texts())
	}
	// The whole one and not the row's small one.
	if asked := asked(a.thumbnails); !asked["https://images.gamebanana.com/img/ss/mods/1.jpg"] {
		t.Errorf("the viewer asked for %v, want the picture as it was published", asked)
	}
}

// The pictures of a submission are read through one after another, and the last
// one leads back to the first.
func TestTheViewerStepsThroughThePictures(t *testing.T) {
	a, tt := modPage(t, shot(1), shot(2))

	if err := tt.Click("第 2 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.store.viewer.index; got != 1 {
		t.Fatalf("the viewer opened at picture %d, want the one that was clicked", got)
	}

	if err := tt.Click("下一张"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.store.viewer.index; got != 0 {
		t.Errorf("the picture after the last is %d, want the first", got)
	}

	if err := tt.Click("上一张"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.store.viewer.index; got != 1 {
		t.Errorf("the picture before the first is %d, want the last", got)
	}
}

// A picture is put away again, and the page it was opened from is what is left.
func TestTheViewerCloses(t *testing.T) {
	a, tt := modPage(t, shot(1), shot(2))

	if err := tt.Click("第 1 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.store.viewer.open {
		t.Fatal("the picture did not open")
	}

	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.store.viewer.open {
		t.Error("the picture is still up")
	}
	if tt.HasText("第 1 / 共 2 张") {
		t.Errorf("the viewer is still drawn: %q", tt.Texts())
	}
	if !tt.HasText("在浏览器打开原帖") {
		t.Errorf("the mod's page is not what is left: %q", tt.Texts())
	}
}

// Escape puts the picture away as well, which is how a dialog is dismissed
// everywhere else.
func TestEscapePutsThePictureAway(t *testing.T) {
	a, tt := modPage(t, shot(1), shot(2))

	if err := tt.Click("第 1 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.store.viewer.open {
		t.Fatal("the picture did not open")
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.store.viewer.open {
		t.Error("the picture is still up after Escape")
	}
}

// The viewer zooms in and out, as far as it goes each way and no further.
func TestTheViewerZooms(t *testing.T) {
	a, tt := modPage(t, shot(1))

	if err := tt.Click("第 1 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.store.viewer.zoom; got != zoomWhole {
		t.Fatalf("the viewer opened at %v, want the picture whole", got)
	}

	if err := tt.Click("放大"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.store.viewer.zoom; got <= zoomWhole {
		t.Errorf("放大 left the picture at %v", got)
	}

	// As far in as it goes: one more click is not one more step.
	for range 20 {
		if err := tt.Click("放大"); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	if got := a.store.viewer.zoom; got != zoomMost {
		t.Errorf("the viewer zoomed to %v, want it to stop at %v", got, zoomMost)
	}

	for range 20 {
		if err := tt.Click("缩小"); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	if got := a.store.viewer.zoom; got != zoomWhole {
		t.Errorf("the viewer zoomed out to %v, want it to stop at %v", got, zoomWhole)
	}
}

// openPicture is the picture at index opened, which is what every test of the
// viewer starts with.
func openPicture(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// The viewer draws the picture it fetched, and draws it larger when it is
// zoomed, which is the whole of what zooming is.
func TestTheViewerDrawsThePictureItFetched(t *testing.T) {
	a := storeShowing(t, shot(1))
	a.thumbnails.client = &http.Client{Transport: pictureStub{body: jpegOf(t, 1920, 1080)}}
	tt := ui.NewTester(a.view, 1280, 800)
	openPicture(t, tt, "第 1 张截图")

	// A screenshot is megabytes, so until it arrives there is nothing to draw
	// but that it is on its way.
	if !tt.HasText("正在读取大图") {
		t.Errorf("the viewer does not say the picture is on its way: %q", tt.Texts())
	}
	settled(t, a.thumbnails)
	tt.Frame()

	if tt.HasText("正在读取大图") {
		t.Errorf("the viewer still waits for a picture that arrived: %q", tt.Texts())
	}
	if !tt.HasText("整张截图") {
		t.Fatalf("the viewer does not draw the picture: %q", tt.Texts())
	}
	// A picture drawn in a room the size of the one it is given is the whole of
	// it: there is nothing beyond its edges to scroll to.
	if got := a.store.viewer.scroll; got.MaxX != 0 || got.MaxY != 0 {
		t.Errorf("the picture is drawn %v×%v past its room, want it whole", got.MaxX, got.MaxY)
	}

	if err := tt.Click("放大"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Zoomed in, there is more of it than the room holds, which is what there
	// is to read along.
	if got := a.store.viewer.scroll.MaxX; got <= 0 {
		t.Error("zooming in left the picture whole, with nothing to scroll to")
	}
}

// The wheel over the picture zooms it, which is how one is looked at closely
// without going to the buttons for every step.
func TestTheWheelZoomsThePicture(t *testing.T) {
	a, tt := modPage(t, shot(1))
	openPicture(t, tt, "第 1 张截图")

	picture, ok := tt.Find("大图")
	if !ok {
		t.Fatal("the viewer does not show the picture")
	}
	x, y := picture.X+picture.W/2, picture.Y+picture.H/2

	// Scrolling up is zooming in, as it is everywhere a picture is zoomed.
	tt.Scroll(x, y, 0, -60)
	tt.Frame()
	if got := a.store.viewer.zoom; got <= zoomWhole {
		t.Errorf("scrolling up left the picture at %v", got)
	}

	in := a.store.viewer.zoom
	tt.Scroll(x, y, 0, 60)
	tt.Frame()
	if got := a.store.viewer.zoom; got >= in {
		t.Errorf("scrolling down left the picture at %v, want it smaller than %v", got, in)
	}
}

// A zoomed picture is read along by pressing it and moving, which is what the
// room it is drawn in cannot show at once.
func TestDraggingMovesTheZoomedPicture(t *testing.T) {
	a, tt := modPage(t, shot(1))
	openPicture(t, tt, "第 1 张截图")

	// Somewhere to move to: a picture shown whole has nothing beyond its edges.
	for range 3 {
		if err := tt.Click("放大"); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()

	picture, ok := tt.Find("大图")
	if !ok {
		t.Fatal("the viewer does not show the picture")
	}
	x, y := picture.X+picture.W/2, picture.Y+picture.H/2
	before := a.store.viewer.scroll.X

	tt.Press(x, y)
	tt.Move(x-40, y)
	tt.Frame()
	tt.Move(x-80, y)
	tt.Frame()
	tt.Release(x-80, y)
	tt.Frame()

	if got := a.store.viewer.scroll.X; got <= before {
		t.Errorf("dragging left left the picture scrolled to %v, want it past %v", got, before)
	}
}

// The viewer is drawn inside the window, controls and picture both: what falls
// outside it is a part of it nobody can reach, and a picture that cannot be put
// away is worse than one that cannot be opened.
func TestTheViewerIsDrawnInsideTheWindow(t *testing.T) {
	const width, height = 1280, 800
	a, tt := modPage(t, shot(1), shot(2))

	if err := tt.Click("第 1 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.store.viewer.open {
		t.Fatal("the picture did not open")
	}

	for _, name := range []string{
		"第 1 / 共 2 张", "大图", "缩小", "放大", "上一张", "下一张", "关闭",
	} {
		r, ok := tt.Find(name)
		if !ok {
			t.Errorf("the viewer does not show %q", name)
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

// A picture belongs to the mod it was opened from, and is not a picture of
// anything once the mod is not.
func TestGoingBackPutsThePictureAway(t *testing.T) {
	a, tt := modPage(t, shot(1), shot(2))

	if err := tt.Click("第 1 张截图"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.store.viewer.open {
		t.Fatal("the picture did not open")
	}

	// 返回 is behind the picture, as everything on the page is while a dialog
	// is over it, so what the button does is done rather than clicked.
	a.store.back()
	tt.Frame()
	if a.store.viewer.open {
		t.Error("the picture is still up over the catalogue")
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
	fakeCatalogue(t, a, func(string, url.Values) (int, string) {
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
	asked := &askedPaths{}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		asked.add(path, query)
		return http.StatusOK, catalogueReply("Yamato redesign")
	})

	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	if err := tt.Click("音效"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sound listing to be read", func() bool {
		return !a.store.shown().loading && asked.has("/Sound/Index")
	})

	if !asked.has("/Sound/Index") {
		t.Errorf("the store asked for %v, want the Sound listing", asked.all())
	}
}

// Searching asks the API's own search, which is the only way to find a mod by
// name among five thousand.
func TestSearchingAsksTheSearch(t *testing.T) {
	a := newApp()
	asked := &askedPaths{}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		asked.add(path, query)
		return http.StatusOK, catalogueReply("Yamato redesign")
	})

	tt := storeWith(t, a, mod("Yamato redesign"))
	a.store.query = "yamato"
	if err := tt.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the search to be read", func() bool {
		return !a.store.shown().loading && asked.has("/Util/Search/Results")
	})

	if !asked.has("/Util/Search/Results") {
		t.Errorf("the store asked for %v, want the search", asked.all())
	}
}

// Choosing a category narrows the listing by the API's own category filter,
// which takes the section's id rather than its name.
func TestChoosingACategoryAsksForItsSection(t *testing.T) {
	a := newApp()
	asked := &askedPaths{}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		asked.add(path, query)
		return http.StatusOK, catalogueReply("Yamato redesign")
	})
	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	// The tree is read when the store opens; a test of the control is not
	// about that request, so it is put there rather than waited for.
	a.store.mu.Lock()
	a.store.categories = []gb.Category{{ID: 33295, Name: "Skins", Items: 3364, Children: 44}}
	a.store.mu.Unlock()
	tt.Frame()

	if err := tt.Click("全部分类"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Skins"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the Skins listing to be read", func() bool {
		query := asked.lastQuery("/Mod/Index")
		return query != nil && query.Get("_aFilters[Generic_Category]") == "33295" && !a.store.shown().loading
	})

	if got := a.store.shown().category; got != "Skins" {
		t.Errorf("the store is filtered by %q, want Skins", got)
	}

	// Going back to every category drops the filter from the request.
	tt.Frame()
	if err := tt.Click("Skins"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("全部分类"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the whole listing to be read again", func() bool {
		query := asked.lastQuery("/Mod/Index")
		return query != nil && query.Get("_aFilters[Generic_Category]") == "" && !a.store.shown().loading
	})
	if got := a.store.shown().category; got != "" {
		t.Errorf("the store is still filtered by %q", got)
	}
}

// Choosing a section under the chosen category narrows the listing to that
// section's own id, without the root beside it.
func TestChoosingAHeroAsksForTheSectionUnderIt(t *testing.T) {
	a := newApp()
	asked := &askedPaths{}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		asked.add(path, query)
		return http.StatusOK, catalogueReply("Yamato redesign")
	})
	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	a.store.mu.Lock()
	a.store.categories = []gb.Category{{ID: 33295, Name: "Skins", Children: 44}}
	a.store.subcategories = []gb.Category{{ID: 33328, Name: "Yamato"}}
	a.store.mu.Unlock()
	a.store.category = "Skins"
	tt.Frame()

	// The section is chosen from its own control, which is only there once the
	// root's sections have been read.
	if err := tt.Click("全部"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Yamato"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the Yamato listing to be read", func() bool {
		query := asked.lastQuery("/Mod/Index")
		return query != nil && query.Get("_aFilters[Generic_Category]") == "33328" && !a.store.shown().loading
	})
	if got := a.store.shown().hero; got != "Yamato" {
		t.Errorf("the store is filtered by the section %q, want Yamato", got)
	}
}

// A search is narrowed here rather than by the API, which accepts the category
// filter and then ignores it, so its page holds submissions of every category.
func TestASearchIsNarrowedByTheCategoryHere(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(string, url.Values) (int, string) {
		return http.StatusOK, searchReply
	})
	tt := storeWith(t, a)
	a.store.query = "skin"
	a.store.category = "Skins"
	a.store.search()
	waitFor(t, "the search to be read", func() bool { return !a.store.shown().loading })
	tt.Frame()

	if tt.HasText("Cursor pack") {
		t.Errorf("the search shows a submission of another category: %q", tt.Texts())
	}
	if !tt.HasText("Yamato skin") {
		t.Errorf("the search does not show the submission of the chosen category: %q", tt.Texts())
	}

	// A section narrows it the same way, by the section's own name beside its
	// root's: Cursors is a section of HUD, not of Skins.
	a.store.category, a.store.hero = "HUD", "Cursors"
	a.store.search()
	waitFor(t, "the search under the other section to be read", func() bool { return !a.store.shown().loading })
	tt.Frame()

	if tt.HasText("Yamato skin") {
		t.Errorf("the search under Cursors shows a Yamato submission: %q", tt.Texts())
	}
	if !tt.HasText("Cursor pack") {
		t.Errorf("the search under Cursors shows nothing of it: %q", tt.Texts())
	}
}

// The adult filter leaves the submissions the catalogue marks for adults out
// of the page, and says how many it took, without asking the API again.
func TestHidingNSFWLeavesThemOutOfThePage(t *testing.T) {
	a := newApp()
	adult := mod("Yamato NSFW")
	adult.ID = 599928
	adult.NSFW = true
	tt := storeWith(t, a, mod("Yamato redesign"), adult)
	tt.Frame()
	if !tt.HasText("Yamato NSFW") {
		t.Fatalf("the page does not show the adult submission to begin with: %q", tt.Texts())
	}

	if err := tt.Click("隐藏 NSFW"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Yamato NSFW") {
		t.Errorf("the adult submission is still on the page: %q", tt.Texts())
	}
	if !tt.HasText("Yamato redesign") {
		t.Errorf("hiding what is for adults took the rest with it: %q", tt.Texts())
	}
	if !tt.HasText("本页隐藏了 1 个") {
		t.Errorf("the page does not say what it hid: %q", tt.Texts())
	}

	// Putting them back is a redraw of the same page, not another request.
	if err := tt.Click("隐藏 NSFW"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Yamato NSFW") {
		t.Errorf("the adult submission did not come back: %q", tt.Texts())
	}
	if tt.HasText("本页隐藏了") {
		t.Errorf("the page still says it hid something: %q", tt.Texts())
	}
}

// The three kinds have trees of their own, so changing the kind forgets what
// was chosen from the one before.
func TestChangingTheKindForgetsTheChosenCategory(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(string, url.Values) (int, string) {
		return http.StatusOK, catalogueReply("Yamato redesign")
	})
	tt := storeWith(t, a, mod("Yamato redesign"))
	waitFor(t, "the first page to be read", func() bool { return !a.store.shown().loading })

	a.store.mu.Lock()
	a.store.categories = []gb.Category{{ID: 33295, Name: "Skins", Children: 44}}
	a.store.subcategories = []gb.Category{{ID: 33328, Name: "Yamato"}}
	a.store.mu.Unlock()
	a.store.category, a.store.hero = "Skins", "Yamato"
	tt.Frame()

	if err := tt.Click("音效"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the kind to change", func() bool {
		state := a.store.shown()
		return state.category == "" && state.hero == ""
	})
	if got := a.store.shown().hero; got != "" {
		t.Errorf("the section %q survived the change of kind", got)
	}
}

// A tree that could not be read leaves the listing up and says so, with a way
// to ask again.
func TestTheStoreSaysWhenTheCategoriesCouldNotBeRead(t *testing.T) {
	a := newApp()
	tt := storeWith(t, a, mod("Yamato redesign"))
	a.store.mu.Lock()
	a.store.categoriesErr = "network is down"
	a.store.mu.Unlock()
	tt.Frame()

	if !tt.HasText("读不到分类：network is down") {
		t.Errorf("the store does not say why the tree is missing: %q", tt.Texts())
	}

	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the tree to be read", func() bool {
		state := a.store.shown()
		return !state.categoriesBusy && state.categoriesErr == "" && len(state.categories) > 0
	})
	if !tt.HasText("Yamato redesign") {
		t.Errorf("reading the tree took the listing with it: %q", tt.Texts())
	}
}
