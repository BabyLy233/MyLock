package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/about"
)

// The sidebar says which build is running and opens the project's page from
// the mark beside it, on every page, not only in the settings.
func TestSidebarShowsTheBuildAndItsRepository(t *testing.T) {
	a := newApp()
	old := about.Version
	t.Cleanup(func() { about.Version = old })
	about.Version = "9.9.9"
	a.router.Push("/mods")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"MyLock 9.9.9", about.Commit(), "在 GitHub 上打开 MyLock"} {
		if !tt.HasText(want) {
			t.Errorf("the sidebar does not show %q: %q", want, tt.Texts())
		}
	}

	if err := tt.Click("在 GitHub 上打开 MyLock"); err != nil {
		t.Fatalf("clicking the repository mark: %v", err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 1 || urls[0] != projectURL {
		t.Errorf("the mark opened %v, want %s", urls, projectURL)
	}
}
