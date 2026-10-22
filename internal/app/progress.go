package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/download"
)

// progressItem is one install or update in flight, as the bar across the top
// of the window shows it.
type progressItem struct {
	// label says what is being done, and to what.
	label string
	// detail is what the page it was started from would say beside the bar:
	// which of several files is on its way.
	detail string
	// progress is how much of it has come.
	progress download.Progress
	// page is where the rest of it is shown, which the bar offers to open.
	page string
}

// progressItems reads the install and the update in flight, if either is. They
// run on goroutines of their own while the user moves between pages, so the
// page that started one is not necessarily the one that is up.
func (a *app) progressItems() []progressItem {
	items := make([]progressItem, 0, 2)

	// The store's lock is taken before the app's, which is never held while
	// the store's is.
	a.store.mu.Lock()
	installing, installProgress := a.store.installing, a.store.progress
	a.store.mu.Unlock()
	if installing != "" {
		items = append(items, progressItem{
			label:    "正在安装 " + installing,
			progress: installProgress,
			page:     "/store",
		})
	}

	a.mu.Lock()
	updating, index, count := a.updating, a.updateIndex, a.updateFileCount
	updateProgress := a.updateProgress
	name := a.library[updating].Name
	a.mu.Unlock()
	if updating != "" {
		if name == "" {
			name = updating
		}
		item := progressItem{
			label:    "正在更新 " + name,
			progress: updateProgress,
			page:     "/mods",
		}
		if count > 0 {
			item.detail = fmt.Sprintf("第 %d/%d 个文件", index, count)
		}
		items = append(items, item)
	}

	return items
}

// progressBanner is what the pages below are doing in the background, across
// the top of the window. A download outlasts the detail page it was started
// from, and leaves the list of installed mods unchanged until it is done, so
// without it an install could only be watched from the one page that started
// it.
func (a *app) progressBanner(c *ui.Context) {
	items := a.progressItems()
	if len(items) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(8).Padding(12).Background(t.Surface).Children(func() {
		for _, item := range items {
			item := item
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, item.label).Bold().MaxLines(1).Ellipsis("…").Grow(1)
					if item.detail != "" {
						ui.Text(c, item.detail).FontSize(12).TextColor(t.TextMuted)
					}
					ui.Button(c, "查看").OnClick(func() {
						a.router.Push(item.page)
					})
				})
				// A bar of unknown length until the server says how big the
				// file is, which some file servers do not.
				value := float64(-1)
				if item.progress.Total > 0 {
					value = float64(item.progress.Done) / float64(item.progress.Total)
				}
				ui.Progress(c, value).WidthPercent(100)
				if item.progress.Total > 0 {
					ui.Text(c, fmt.Sprintf("%d%% · %s / %s",
						int(value*100+0.5), humanSize(item.progress.Done), humanSize(item.progress.Total))).
						FontSize(12).TextColor(t.TextMuted)
				} else if item.progress.Done > 0 {
					ui.Text(c, humanSize(item.progress.Done)).FontSize(12).TextColor(t.TextMuted)
				}
			})
		}
	})
}
