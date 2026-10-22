package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/download"
)

// An install started from the store keeps running when the user goes back to
// the list of installed mods, which does not change until it is done. The bar
// across the top is how it stays in sight there.
func TestInstallProgressStaysVisibleOnOtherPages(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.installing = "Bikinidicta"
	a.store.progress = download.Progress{Done: 5 << 20, Total: 10 << 20}
	a.store.mu.Unlock()
	a.router.Push("/mods")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{
		"正在安装 Bikinidicta",
		"50% · 5.0 MB / 10.0 MB",
		"查看",
	} {
		if !tt.HasText(want) {
			t.Errorf("the mods page does not show the install in flight with %q: %q",
				want, tt.Texts())
		}
	}

	// Once the store says the install is over, there is nothing left to show.
	a.store.mu.Lock()
	a.store.installing = ""
	a.store.progress = download.Progress{}
	a.store.installed = "Bikinidicta 已安装并启用。"
	a.store.mu.Unlock()
	tt = ui.NewTester(a.view, 1280, 800)
	if tt.HasText("正在安装") {
		t.Errorf("the bar is still up after the install: %q", tt.Texts())
	}
}

// An update downloads in the mods page's own panel, but the user can leave
// that page while it runs; the bar follows it to every page as well.
func TestUpdateProgressStaysVisibleOnOtherPages(t *testing.T) {
	a := newApp()
	a.mu.Lock()
	a.updating = "554012"
	a.updateIndex, a.updateFileCount = 1, 2
	a.updateProgress = download.Progress{Done: 1 << 20, Total: 4 << 20}
	a.library = map[string]config.ModInfo{
		"554012": {Name: "Bikinidicta"},
	}
	a.mu.Unlock()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{
		"正在更新 Bikinidicta",
		"第 1/2 个文件",
		"25% · 1.0 MB / 4.0 MB",
	} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show the update in flight with %q: %q",
				want, tt.Texts())
		}
	}
}

// A download whose size the server did not give shows how much has come
// without a share of a whole that is not known.
func TestProgressWithoutATotalShowsOnlyWhatHasCome(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.installing = "Bikinidicta"
	a.store.progress = download.Progress{Done: 1536}
	a.store.mu.Unlock()
	a.router.Push("/mods")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("1.5 KB") {
		t.Errorf("the bar does not show how much has come: %q", tt.Texts())
	}
	if tt.HasText("%") {
		t.Errorf("the bar shows a share of a total it does not have: %q", tt.Texts())
	}
}
