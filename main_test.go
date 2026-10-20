package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/config"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/game"
)

// The view runs without a window in tests, which click as a user would.

// waitFor polls until cond holds, which it must: the lookups read the disk on
// a goroutine of their own, and the click that starts one only takes effect in
// a frame after it.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForLook waits for the running lookup to finish, and reports what it left.
func waitForLook(t *testing.T, a *app) state {
	t.Helper()
	waitFor(t, "the lookup to finish", func() bool { return !a.state().looking })
	return a.state()
}

// deadlockDir makes a directory Validate accepts as a Deadlock installation.
func deadlockDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Deadlock")
	if err := os.MkdirAll(filepath.Join(dir, "game", "citadel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "game", "citadel", "gameinfo.gi"), []byte("// gameinfo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// settingsPath points the app at a settings file of its own, so a test never
// reads or writes the one the user runs with.
func settingsPath(t *testing.T, a *app) string {
	t.Helper()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	return a.configPath
}

func TestShellShowsTheSidebar(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"已安装模组", "设置"} {
		if !tt.HasText(want) {
			t.Errorf("the sidebar does not list %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("还没有找到游戏目录，先到设置里指定。") {
		t.Errorf("the mods page does not show its empty state: %q", tt.Texts())
	}
}

// Choosing a page in the sidebar pushes it, and the router shows it.
func TestSidebarNavigates(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.router.Path(); got != "/settings" {
		t.Fatalf("the router is at %q after clicking 设置, want /settings", got)
	}
	if !tt.HasText("游戏目录") {
		t.Errorf("the settings page did not show: %q", tt.Texts())
	}
}

// The mods page points at the directory the game loads mods from, so the
// user can check it before any mod is read.
func TestModsPageShowsTheAddonsDirectory(t *testing.T) {
	a := newApp()
	a.install = &game.Install{GamePath: "/games/Deadlock"}
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText(a.install.AddonsDir()) {
		t.Errorf("the mods page does not show %q: %q", a.install.AddonsDir(), tt.Texts())
	}
}

func TestSettingsShowsTheInstall(t *testing.T) {
	a := newApp()
	a.install = &game.Install{
		GamePath:    "/games/Deadlock",
		SteamRoot:   "/steam",
		LibraryPath: "/mnt/games/SteamLibrary",
		BuildID:     "20260101",
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"/games/Deadlock", "/mnt/games/SteamLibrary", "/steam", "20260101", "自动检测"} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show %q: %q", want, tt.Texts())
		}
	}
}

// A directory the user pinned is named as such, and the Steam fields, which
// were never read, are left out.
func TestSettingsNamesAPinnedDirectory(t *testing.T) {
	a := newApp()
	a.pinned = "/games/Deadlock"
	a.install = &game.Install{GamePath: "/games/Deadlock"}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("手动指定") {
		t.Errorf("the settings page does not say the directory was pinned: %q", tt.Texts())
	}
	if tt.HasText("自动检测") {
		t.Errorf("the settings page calls a pinned directory detected: %q", tt.Texts())
	}
}

func TestSettingsShowsTheLookingState(t *testing.T) {
	a := newApp()
	a.looking = true
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("正在查找…") {
		t.Errorf("the settings page does not say it is looking: %q", tt.Texts())
	}
}

func TestSettingsShowsTheError(t *testing.T) {
	a := newApp()
	a.lookErr = "no Steam installation: not found"
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("no Steam installation") {
		t.Errorf("the settings page does not show the error: %q", tt.Texts())
	}
}

// A directory that is not a Deadlock installation is refused, and does not
// become the one the app uses.
func TestUsePathRejectsADirectoryThatIsNotDeadlock(t *testing.T) {
	a := newApp()
	settingsPath(t, a)
	a.pathInput = t.TempDir()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("使用此目录"); err != nil {
		t.Fatal(err)
	}
	s := waitForLook(t, a)
	if s.lookErr == "" {
		t.Error("the settings page accepted a directory that is not a Deadlock installation")
	}
	if s.pinned != "" || s.install != nil {
		t.Errorf("the app took a directory it refused: pinned=%q install=%+v", s.pinned, s.install)
	}
}

// A directory the user picks is used, and remembered for the next run.
func TestUsePathAcceptsADeadlockDirectory(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	a.pathInput = gamePath
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("使用此目录"); err != nil {
		t.Fatal(err)
	}
	s := waitForLook(t, a)
	if s.lookErr != "" {
		t.Fatalf("the settings page refused a Deadlock directory: %s", s.lookErr)
	}
	if s.install == nil || s.install.GamePath != gamePath {
		t.Fatalf("install = %+v, want the game at %q", s.install, gamePath)
	}
	if s.pinned != gamePath {
		t.Errorf("pinned = %q, want %q", s.pinned, gamePath)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.GamePath != gamePath {
		t.Errorf("the saved settings hold %q, want %q", saved.GamePath, gamePath)
	}
}

// The field is the only place a directory comes from, so an empty one is a
// mistake to point out rather than a request to detect the game.
func TestUsePathRefusesAnEmptyField(t *testing.T) {
	a := newApp()
	settingsPath(t, a)
	a.pathInput = "   "
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("使用此目录"); err != nil {
		t.Fatal(err)
	}
	if s := a.state(); s.lookErr == "" {
		t.Error("an empty directory was accepted")
	}
	if s := a.state(); s.looking {
		t.Error("an empty directory started a lookup")
	}
}

// 重新检测 is the way back from a pinned directory to detection.
func TestDetectDropsThePinnedDirectory(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	a.pinned = gamePath
	a.pathInput = gamePath
	a.install = &game.Install{GamePath: gamePath}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("重新检测"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pinned directory to be dropped", func() bool { return a.state().pinned == "" })
	waitForLook(t, a)

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.GamePath != "" {
		t.Errorf("the saved settings still hold %q after 重新检测", saved.GamePath)
	}
}

// The directory the user chose last time is the one the app starts with.
func TestStartUsesTheSavedDirectory(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	if err := (config.Config{GamePath: gamePath}).Save(path); err != nil {
		t.Fatal(err)
	}

	a.start()
	s := waitForLook(t, a)
	if s.lookErr != "" {
		t.Fatalf("starting with a saved directory failed: %s", s.lookErr)
	}
	if s.install == nil || s.install.GamePath != gamePath {
		t.Fatalf("install = %+v, want the game at %q", s.install, gamePath)
	}
	if a.pathInput != gamePath {
		t.Errorf("the field holds %q, want the saved %q", a.pathInput, gamePath)
	}
}

// A settings file that cannot be read is reported rather than passed over: the
// directory the user chose last time is not in effect.
func TestStartReportsSettingsItCannotRead(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.start()
	s := waitForLook(t, a)
	if s.lookErr == "" {
		t.Error("starting with an unreadable settings file said nothing")
	}
}
