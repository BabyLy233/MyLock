package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/download"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/gb"
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
	items   []gb.Submission
	total   int
	more    bool
	// chosen is the item the detail panel is showing, -1 for none.
	chosen int

	// The chosen mod's own page -- what its author wrote, its pictures and its
	// files -- which is read when it is opened.
	profile     gb.Profile
	profileBusy bool
	profileErr  string
	// writeUp is that page's write-up, as text: the API gives it as HTML, and
	// turning it into something to read is work of its own, so a reply does it
	// once rather than every frame.
	writeUp string

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
	return &store{app: a, chosen: -1, page: 1}
}

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

	installing string
	progress   download.Progress
	installErr string
	installed  string
	// gameRunning is whether Deadlock is running, in which case nothing may be
	// installed into the files it holds.
	gameRunning bool
}

// view is the store: a search box over the catalogue, the catalogue itself, and
// what the user has opened out of it.
func (s *store) view(c *ui.Context) {
	if !s.started {
		s.started = true
		s.load()
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
				s.kind = kind
				s.search()
			})
			order := s.order
			ui.Tabs(c, &order, orderLabels...).OnChange(func() {
				s.order = order
				s.search()
			})
		})

		if state.chosen >= 0 && state.chosen < len(state.items) {
			s.detail(c, state)
			return
		}
		s.list(c, state)
	})
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

	go func() {
		var result gb.Page
		var err error
		if query == "" {
			result, err = s.app.catalogue.Browse(context.Background(), kind, page, order)
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
			s.items, s.total, s.more = result.Items, result.Total, result.More
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

	s.mu.Lock()
	s.chosen = item
	// The mod that was open before is not this one: nothing of it is left to be
	// drawn while this one's page is on its way.
	s.profile, s.profileErr, s.writeUp = gb.Profile{}, "", ""
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
		})
	}()
}

// back returns to the catalogue.
func (s *store) back() {
	s.mu.Lock()
	s.chosen = -1
	s.profile, s.profileErr, s.writeUp = gb.Profile{}, "", ""
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
		installing:  s.installing,
		progress:    s.progress,
		installErr:  s.installErr,
		installed:   s.installed,
		gameRunning: running,
	}
}
