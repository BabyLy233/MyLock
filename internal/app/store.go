package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"github.com/BabyLy233/mylock/internal/download"
	"github.com/BabyLy233/mylock/internal/gb"
)

var (
	// kinds are the three listings the catalogue keeps Deadlock's mods in, with
	// what the tabs call them. They are listed apart rather than together
	// because the API numbers them apart: a mod and a sound pack can both be 42.
	kinds      = []gb.Kind{gb.Mod, gb.Sound, gb.Wip}
	kindLabels = []string{"模组", "音效", "制作中"}

	// orders are how a listing can be sorted, with what the tabs call them.
	orders      = []gb.Order{gb.Updated, gb.Liked, gb.Downloaded}
	orderLabels = []string{"最近更新", "最多点赞", "最多下载"}
)

// store is the mod store: GameBanana's catalogue of Deadlock mods, which is
// where every mod the app installs comes from.
//
// The controls are bound to directly, and only the main thread touches them.
// Everything a request brings back is guarded by mu, because requests run on
// goroutines of their own.
type store struct {
	app *app

	// query, kind, order and page are what the controls ask for. Only the main
	// thread touches them, and load reads them before it hands the request to a
	// goroutine.
	query string
	kind  int
	order int
	page  int
	// category and hero are what the filters ask for, by the names a person
	// reads: the id the API wants is looked up in the tree that was read for
	// the kind. An empty category is the whole listing, and an empty hero is
	// every section under the chosen category.
	category string
	hero     string
	// started reports whether the catalogue has been read at all, which happens
	// the first time the page is shown rather than at startup: a user who never
	// opens the store never asks the API for anything.
	started bool
	// grid keeps the grid's place across the rebuilds a reply causes. What is
	// chosen is not kept in it: opening a mod is a click on its card, which is
	// the gesture everywhere else in the app.
	grid ui.GridState

	mu sync.Mutex
	// seq counts the requests made, so that a reply can be told apart from the
	// one the page is waiting for.
	seq     int
	loading bool
	err     string
	// raw is the page as the API sent it, and items the part of it the filters
	// let through. The adult filter is applied to raw again when it changes,
	// so that hiding something does not need another request.
	raw    []gb.Submission
	filter pageFilter
	items  []gb.Submission
	total  int
	more   bool
	// hideNSFW is whether the submissions the catalogue marks for adults are
	// left out. The app opens showing them, which is what the site's own
	// listings do.
	hideNSFW   bool
	hiddenNSFW int
	// chosen is the item the detail panel is showing, -1 for none.
	chosen int

	// categories is the tree of the kind being shown, and subcategories the
	// sections under the chosen root. Both are read from the API, so both can
	// be on their way or have failed.
	categories        []gb.Category
	categoriesBusy    bool
	categoriesErr     string
	categoriesSeq     int
	subcategories     []gb.Category
	subcategoriesBusy bool
	subcategoriesErr  string
	subcategoriesSeq  int
	// subsCache keeps a root's sections once read, so that going back to a
	// category does not ask again.
	subsCache map[int][]gb.Category

	// The chosen mod's own page -- what its author wrote, its pictures and its
	// files -- which is read when it is opened.
	profile     gb.Profile
	profileBusy bool
	profileErr  string
	// writeUp is that page's write-up, as text: the API gives it as HTML, and
	// turning it into something to read is work of its own, so a reply does it
	// once rather than every frame.
	writeUp string
	// picked is the downloads of the chosen mod the user means to install, by
	// file id. An author often publishes several -- variants of a skin, a base
	// file and its extras -- and they are installed together as one mod.
	picked map[int]bool

	// viewer is the screenshot the user opened out of the row of them, if any.
	viewer viewer

	// The install in flight, if there is one: the file being fetched, how far
	// it has got, and what the last one left behind.
	installing string
	progress   download.Progress
	installErr string
	installed  string
}

func newStore(a *app) *store {
	return &store{app: a, chosen: -1, page: 1, subsCache: make(map[int][]gb.Category)}
}

// pageFilter is what narrowed the page that is up. A listing is narrowed by
// the server itself, which is what keeps its count and its pages right; a
// search is narrowed here, because the search endpoint accepts a filter and
// then ignores it.
type pageFilter struct {
	// search reports that the page came from the search rather than a listing.
	search bool
	// category and hero are the names the filters chose, empty for every one.
	category string
	hero     string
}

// The option that stands for "no filter" in the two selection controls. It is
// a word rather than an empty string because a control shows what it was
// given, and an empty option reads as a broken one.
const (
	allCategories    = "全部分类"
	allSubcategories = "全部"
)

// shown is what the view reads about the last requests, taken in one lock.
type shown struct {
	loading bool
	err     string
	items   []gb.Submission
	total   int
	more    bool
	chosen  int

	profile     gb.Profile
	profileBusy bool
	profileErr  string
	writeUp     string
	picked      map[int]bool

	installing string
	progress   download.Progress
	installErr string
	installed  string
	// gameRunning is whether Deadlock is running, in which case nothing may be
	// installed into the files it holds.
	gameRunning bool

	// The filters: what is chosen, what the adult filter took out of the page,
	// and the category tree the selections are offered from.
	category          string
	hero              string
	hideNSFW          bool
	hiddenNSFW        int
	categories        []gb.Category
	categoriesBusy    bool
	categoriesErr     string
	subcategories     []gb.Category
	subcategoriesBusy bool
	subcategoriesErr  string
}

// view is the store: a search box over the catalogue, the catalogue itself, and
// what the user has opened out of it.
func (s *store) view(c *ui.Context) {
	if !s.started {
		s.started = true
		s.load()
		// The tree is read when the page is opened rather than when a filter
		// is first reached for, so that the controls are ready by then.
		s.loadCategories()
	}
	state := s.shown()

	ui.Column(c).Fill().Padding(24).Gap(16).Justify(ui.Start).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "模组商店").FontSize(22).Bold()
			ui.TextInput(c, &s.query).
				Width(280).
				Placeholder("搜索模组…").
				OnSubmit(s.search)
			ui.Button(c, "搜索").OnClick(s.search)
		})

		ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Center).Children(func() {
			kind := s.kind
			ui.Tabs(c, &kind, kindLabels...).OnChange(func() {
				// The three kinds have category trees of their own, so what
				// was chosen for one means nothing for another.
				s.kind = kind
				s.category, s.hero = "", ""
				s.resetCategories()
				s.loadCategories()
				s.search()
			})
			order := s.order
			ui.Tabs(c, &order, orderLabels...).OnChange(func() {
				s.order = order
				s.search()
			})
		})

		s.filterRow(c, state)

		if state.chosen >= 0 && state.chosen < len(state.items) {
			s.detail(c, state)
			return
		}
		s.list(c, state)
	})
}

// filterRow is what narrows the listing: the category tree, the sections under
// the chosen category, and the adult filter.
func (s *store) filterRow(c *ui.Context, state shown) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
			s.categorySelect(c, state)
			s.subcategorySelect(c, state)
			hide := state.hideNSFW
			ui.Checkbox(c, &hide, "隐藏 NSFW").OnChange(func() {
				s.setHideNSFW(hide)
			})
			if state.hiddenNSFW > 0 {
				ui.Text(c, fmt.Sprintf("本页隐藏了 %d 个", state.hiddenNSFW)).
					FontSize(12).TextColor(t.TextMuted)
			}
		})
		// A tree that could not be read leaves the listing usable and says so
		// here, rather than taking the whole page down with it.
		if state.categoriesErr != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "读不到分类："+state.categoriesErr).
					FontSize(12).TextColor(t.Danger)
				ui.Button(c, "重试").OnClick(s.loadCategories)
			})
		}
		if state.subcategoriesErr != "" {
			ui.Text(c, "读不到子分类："+state.subcategoriesErr).
				FontSize(12).TextColor(t.Danger)
		}
	})
}

// categorySelect offers the roots of the chosen kind. It is left out until they
// have been read, which is a moment after the store opens.
func (s *store) categorySelect(c *ui.Context, state shown) {
	t := c.Theme()
	switch {
	case state.categoriesBusy && len(state.categories) == 0:
		ui.Text(c, "正在读取分类…").FontSize(12).TextColor(t.TextMuted)
		return
	case len(state.categories) == 0:
		return
	}

	options := make([]string, 0, len(state.categories)+1)
	options = append(options, allCategories)
	for _, category := range state.categories {
		options = append(options, category.Name)
	}

	ui.Text(c, "分类").FontSize(12).TextColor(t.TextMuted)
	chosen := state.category
	if chosen == "" {
		chosen = allCategories
	}
	ui.Select(c, &chosen, options).Label("分类").OnChange(func() {
		if chosen == allCategories {
			chosen = ""
		}
		s.chooseCategory(chosen)
	})
}

// subcategorySelect offers the sections under the chosen root. Under Skins
// these are the heroes; under HUD or Model Replacement they are the parts of
// the game the mod changes. It is only there when the root has sections.
func (s *store) subcategorySelect(c *ui.Context, state shown) {
	if state.category == "" {
		return
	}
	t := c.Theme()
	if state.subcategoriesBusy && len(state.subcategories) == 0 {
		ui.Text(c, "正在读取子分类…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if len(state.subcategories) == 0 {
		return
	}

	options := make([]string, 0, len(state.subcategories)+1)
	options = append(options, allSubcategories)
	for _, subcategory := range state.subcategories {
		options = append(options, subcategory.Name)
	}

	ui.Text(c, "子分类").FontSize(12).TextColor(t.TextMuted)
	chosen := state.hero
	if chosen == "" {
		chosen = allSubcategories
	}
	ui.Select(c, &chosen, options).Label("子分类").OnChange(func() {
		if chosen == allSubcategories {
			chosen = ""
		}
		s.chooseHero(chosen)
	})
}

// chooseCategory narrows the listing to one root category, and forgets the
// section chosen under the one before: a hero of the old tree is not a section
// of the new one.
func (s *store) chooseCategory(name string) {
	s.category, s.hero = name, ""
	s.loadSubcategories(name)
	s.search()
}

// chooseHero narrows the listing to one section of the chosen category. The API
// takes a section's id on its own, without the root beside it.
func (s *store) chooseHero(name string) {
	s.hero = name
	s.search()
}

// setHideNSFW leaves the adult-marked submissions out, or puts them back. The
// page the API sent is kept, so this is a redraw rather than a request.
func (s *store) setHideNSFW(hide bool) {
	s.mu.Lock()
	if s.hideNSFW == hide {
		s.mu.Unlock()
		return
	}
	s.hideNSFW = hide
	s.filterLocked()
	// What was open was an item of the list that just changed.
	s.chosen = -1
	s.profile, s.profileErr, s.writeUp = gb.Profile{}, "", ""
	s.mu.Unlock()
	s.closePicture()
	s.app.invalidate()
}

// loadCategories reads the roots of the kind being shown.
func (s *store) loadCategories() {
	kind := kinds[s.kind]
	s.mu.Lock()
	s.categoriesSeq++
	seq := s.categoriesSeq
	s.categoriesBusy = true
	s.categoriesErr = ""
	s.mu.Unlock()
	s.app.invalidate()

	go func() {
		categories, err := s.app.catalogue.Categories(context.Background(), kind, 0)
		var message string
		if err != nil {
			message = err.Error()
		}
		s.app.publish(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// A reply for a kind the tabs have left is not this page's.
			if seq != s.categoriesSeq {
				return
			}
			s.categoriesBusy = false
			s.categoriesErr = message
			if err != nil {
				return
			}
			s.categories = categories
		})
	}()
}

// resetCategories forgets the tree of the kind that was shown. What is in
// flight for it is dropped when it lands.
func (s *store) resetCategories() {
	s.mu.Lock()
	s.categories, s.categoriesErr, s.categoriesBusy = nil, "", false
	s.categoriesSeq++
	s.subcategories, s.subcategoriesErr, s.subcategoriesBusy = nil, "", false
	s.subcategoriesSeq++
	s.mu.Unlock()
}

// loadSubcategories reads the sections under the named root, from the cache
// when it has them. A root without sections, and no root at all, clear what
// was there.
func (s *store) loadSubcategories(rootName string) {
	kind := kinds[s.kind]

	s.mu.Lock()
	s.subcategoriesSeq++
	seq := s.subcategoriesSeq
	s.subcategories, s.subcategoriesErr = nil, ""
	s.subcategoriesBusy = false

	if rootName == "" {
		s.mu.Unlock()
		return
	}
	root, children := 0, 0
	for _, category := range s.categories {
		if category.Name == rootName {
			root, children = category.ID, category.Children
			break
		}
	}
	if root == 0 || children == 0 {
		s.mu.Unlock()
		return
	}
	if cached, ok := s.subsCache[root]; ok {
		s.subcategories = cached
		s.mu.Unlock()
		return
	}
	s.subcategoriesBusy = true
	s.mu.Unlock()
	s.app.invalidate()

	go func() {
		subcategories, err := s.app.catalogue.Categories(context.Background(), kind, root)
		var message string
		if err != nil {
			message = err.Error()
		}
		s.app.publish(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// Another category was chosen while this was on its way.
			if seq != s.subcategoriesSeq {
				return
			}
			s.subcategoriesBusy = false
			s.subcategoriesErr = message
			if err != nil {
				return
			}
			s.subsCache[root] = subcategories
			s.subcategories = subcategories
		})
	}()
}

// filterCategoryID is the id the listing is narrowed by, zero for the whole
// listing. A chosen section is more specific than its root, and the API takes
// it on its own.
func (s *store) filterCategoryID(category, hero string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hero != "" {
		for _, subcategory := range s.subcategories {
			if subcategory.Name == hero {
				return subcategory.ID
			}
		}
	}
	for _, root := range s.categories {
		if root.Name == category {
			return root.ID
		}
	}
	return 0
}

// filterLocked puts the part of the page the filters let through into items.
// The adult filter and a search's category are applied here; a listing's
// category was applied by the server.
func (s *store) filterLocked() {
	items := make([]gb.Submission, 0, len(s.raw))
	hidden := 0
	for _, submission := range s.raw {
		if s.hideNSFW && submission.NSFW {
			hidden++
			continue
		}
		if s.filter.search && !matchesFilter(submission, s.filter) {
			continue
		}
		items = append(items, submission)
	}
	s.items, s.hiddenNSFW = items, hidden
}

// matchesFilter reports whether a search result is filed under the chosen
// category. A section's name is only meaningful beside its root, so both have
// to match.
func matchesFilter(submission gb.Submission, filter pageFilter) bool {
	switch {
	case filter.hero != "":
		return submission.Category == filter.category && submission.Subcategory == filter.hero
	case filter.category != "":
		return submission.Category == filter.category
	default:
		return true
	}
}

// list shows the catalogue, or why it could not be read.
func (s *store) list(c *ui.Context, state shown) {
	t := c.Theme()
	switch {
	case state.loading && len(state.items) == 0:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "正在读取商店…").TextColor(t.TextMuted)
		})
		return
	case state.err != "" && len(state.items) == 0:
		ui.Text(c, state.err).TextColor(t.Danger)
		ui.Row(c).Children(func() {
			ui.Button(c, "重试").OnClick(s.load)
		})
		return
	case len(state.items) == 0:
		ui.Text(c, "没有找到模组。").TextColor(t.TextMuted)
		return
	}

	// A failure beside a listing that is still up is worth showing: what is
	// below is the listing from before.
	if state.err != "" {
		ui.Text(c, state.err).TextColor(t.Danger)
	}

	ui.GridView(c, &s.grid, len(state.items), 200, 250, func(i int) {
		s.card(c, state.items[i], i)
	}).Grow(1)

	s.pager(c, state)
}

// cardPicture is the room a card's picture is shown in, which is also what it
// is fetched for. The grid's cells are at least as wide as this, and a picture
// fetched for the smallest of them is no worse for the wider ones, which scale
// it down.
var cardPicture = box{W: 200, H: 130}

// card is one mod in the grid: its picture, its name, and enough about it to
// tell it from the ones beside it. Clicking it opens it.
func (s *store) card(c *ui.Context, mod gb.Submission, item int) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(6).OnClick(func() { s.open(item) }).Children(func() {
		ui.Row(c).WidthPercent(100).Height(float32(cardPicture.H)).Background(t.Surface).Radius(8).Clip().Children(func() {
			// A picture that has not arrived yet is nothing at all: the card is
			// drawn without it, and the window is redrawn when it comes.
			if bitmap := s.app.thumbnails.get(mod.Thumb, cardPicture, s.app.invalidate); bitmap != nil {
				ui.Image(c, bitmap).Fit(ui.Cover).Fill()
			}
		})
		ui.Text(c, mod.Name).Bold().MaxLines(1).Ellipsis("…")
		ui.Text(c, summary(mod)).FontSize(12).TextColor(t.TextMuted).MaxLines(1).Ellipsis("…")
	})
}

// summary says what a mod is and how it has been received, in the terms the
// catalogue gives: where it is filed, who made it, and how many people liked
// it.
func summary(mod gb.Submission) string {
	var parts []string
	if mod.Category != "" {
		category := mod.Category
		if mod.Subcategory != "" {
			category += " / " + mod.Subcategory
		}
		parts = append(parts, category)
	}
	if mod.Author != "" {
		parts = append(parts, mod.Author)
	}
	if mod.Likes > 0 {
		parts = append(parts, fmt.Sprintf("赞 %d", mod.Likes))
	}
	return strings.Join(parts, " · ")
}

// pager moves through the listing. The API's own count is shown rather than
// paged by: it counts what the filters matched, not what it will hand over.
func (s *store) pager(c *ui.Context, state shown) {
	t := c.Theme()
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Button(c, "上一页").Disabled(s.page <= 1 || state.loading).OnClick(func() {
			s.goTo(s.page - 1)
		})
		ui.Text(c, fmt.Sprintf("第 %d 页", s.page)).TextColor(t.TextMuted)
		ui.Button(c, "下一页").Disabled(!state.more || state.loading).OnClick(func() {
			s.goTo(s.page + 1)
		})
		if state.total > 0 {
			ui.Text(c, fmt.Sprintf("共约 %d 个", state.total)).FontSize(12).TextColor(t.TextMuted)
		}
		if state.loading {
			ui.Spinner(c)
		}
	})
}

// search runs the listing again from the first page, which is what changing
// what is being looked for means.
func (s *store) search() {
	s.page = 1
	s.back()
	s.load()
}

// goTo moves to another page of the same listing.
func (s *store) goTo(page int) {
	if page < 1 {
		return
	}
	s.page = page
	s.back()
	s.load()
}

// load reads the page the controls ask for. It is a request, so it runs off the
// main thread.
func (s *store) load() {
	s.mu.Lock()
	if s.loading {
		s.mu.Unlock()
		return
	}
	s.loading = true
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	s.app.invalidate()

	query := strings.TrimSpace(s.query)
	kind, order, page := kinds[s.kind], orders[s.order], s.page
	// What the page is narrowed by is settled here, on the main thread: the
	// controls can change while the request is on its way, and the reply has
	// to be filtered by what it was asked for.
	category, hero := s.category, s.hero
	filter := pageFilter{search: query != "", category: category, hero: hero}
	// The search endpoint ignores this, so only a listing is given it.
	categoryID := s.filterCategoryID(category, hero)

	go func() {
		var result gb.Page
		var err error
		if query == "" {
			result, err = s.app.catalogue.BrowseFiltered(context.Background(), kind, page, order, gb.Filter{Category: categoryID})
		} else {
			result, err = s.app.catalogue.Search(context.Background(), kind, query, page)
		}

		var message string
		if err != nil {
			message = err.Error()
		}
		s.app.publish(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// A reply is kept only if it is the one the page is waiting for.
			// Paging quickly leaves earlier requests in flight, and an older
			// page landing last would put itself up under the newer one's
			// number.
			if seq != s.seq {
				return
			}
			s.loading = false
			s.err = message
			if err != nil {
				return
			}
			s.filter = filter
			s.raw, s.total, s.more = result.Items, result.Total, result.More
			s.filterLocked()
			s.chosen = -1
		})
	}()
}

// open shows a mod's own page: what its author wrote about it, its pictures, and
// what can be downloaded for it.
func (s *store) open(item int) {
	mods := s.shown().items
	if item < 0 || item >= len(mods) {
		return
	}
	mod := mods[item]

	// What the mod was installed from, if it was installed from here: opening a
	// mod that is already installed shows the same files it has.
	s.app.mu.Lock()
	installed := slices.Clone(s.app.library[mod.Slug()].FileIDs)
	s.app.mu.Unlock()

	s.mu.Lock()
	s.chosen = item
	// The mod that was open before is not this one: nothing of it is left to be
	// drawn while this one's page is on its way.
	s.profile, s.profileErr, s.writeUp = gb.Profile{}, "", ""
	s.picked = nil
	s.profileBusy = true
	s.installErr, s.installed = "", ""
	s.mu.Unlock()
	s.app.invalidate()

	go func() {
		profile, err := s.app.catalogue.Profile(context.Background(), mod.Kind, mod.ID)
		var message string
		if err != nil {
			message = err.Error()
		}
		// The write-up is HTML, and turning it into something to read is work of
		// its own, so it is done here rather than in every frame that shows it.
		writeUp := richtext.ParseHTML(profile.Text).PlainText()
		s.app.publish(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// The user may have gone back, or opened another mod, while this
			// was in flight.
			if s.chosen < 0 || s.chosen >= len(s.items) || s.items[s.chosen].ID != mod.ID {
				return
			}
			s.profileBusy = false
			s.profile, s.profileErr = profile, message
			s.writeUp = writeUp
			s.picked = defaultPicked(profile.Files, installed)
		})
	}()
}

// defaultPicked is what a mod's page starts with selected: the files it was
// installed from, so a second install keeps the variants the user chose, or the
// newest current one when there is no such record. Variants of one mod replace
// each other, so every one of them being selected would be a mod that fights
// itself; the user picks any others they want by hand.
func defaultPicked(files []gb.File, installed []int) map[int]bool {
	picked := make(map[int]bool, len(installed))
	for _, file := range files {
		if !file.Archived && slices.Contains(installed, file.ID) {
			picked[file.ID] = true
		}
	}
	if len(picked) > 0 {
		return picked
	}
	if file, ok := newestOffered(files); ok {
		picked[file.ID] = true
	}
	return picked
}

// back returns to the catalogue.
func (s *store) back() {
	s.mu.Lock()
	s.chosen = -1
	s.profile, s.profileErr, s.writeUp = gb.Profile{}, "", ""
	s.picked = nil
	s.mu.Unlock()
	// A picture of the mod that was open is not a picture of anything once the
	// mod is not.
	s.closePicture()
	s.app.invalidate()
}

// shown reads what the last requests brought back, in one lock.
func (s *store) shown() shown {
	// Read before this store's lock, because the app's is never taken while
	// that one is held.
	s.app.mu.Lock()
	running := s.app.gameRunning
	s.app.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	return shown{
		loading:     s.loading,
		err:         s.err,
		items:       s.items,
		total:       s.total,
		more:        s.more,
		chosen:      s.chosen,
		profile:     s.profile,
		profileBusy: s.profileBusy,
		profileErr:  s.profileErr,
		writeUp:     s.writeUp,
		picked:      maps.Clone(s.picked),
		installing:  s.installing,
		progress:    s.progress,
		installErr:  s.installErr,
		installed:   s.installed,
		gameRunning: running,

		category:          s.category,
		hero:              s.hero,
		hideNSFW:          s.hideNSFW,
		hiddenNSFW:        s.hiddenNSFW,
		categories:        s.categories,
		categoriesBusy:    s.categoriesBusy,
		categoriesErr:     s.categoriesErr,
		subcategories:     s.subcategories,
		subcategoriesBusy: s.subcategoriesBusy,
		subcategoriesErr:  s.subcategoriesErr,
	}
}
