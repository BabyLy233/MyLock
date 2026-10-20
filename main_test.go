package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/game"
)

// The view runs without a window in tests, which click as a user would.

func TestShellShowsTheSidebar(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"已安装模组", "设置"} {
		if !tt.HasText(want) {
			t.Errorf("the sidebar does not list %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("还没有找到游戏目录，先到设置里检测。") {
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

	for _, want := range []string{"/games/Deadlock", "/mnt/games/SteamLibrary", "/steam", "20260101"} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show %q: %q", want, tt.Texts())
		}
	}
}

func TestSettingsShowsTheDetectingState(t *testing.T) {
	a := newApp()
	a.detecting = true
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("正在检测…") {
		t.Errorf("the settings page does not say it is detecting: %q", tt.Texts())
	}
}

func TestSettingsShowsTheError(t *testing.T) {
	a := newApp()
	a.detectErr = "no Steam installation: not found"
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("no Steam installation") {
		t.Errorf("the settings page does not show the error: %q", tt.Texts())
	}
}

// The button starts a detection, and the result reaches the app.
func TestDetectButtonStartsADetection(t *testing.T) {
	a := newApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("重新检测"); err != nil {
		t.Fatal(err)
	}

	// The detection reads the disk on a goroutine of its own, so wait for it
	// rather than assume it finished. It either finds the game or records
	// why it did not, so one of the two is set once it is done.
	deadline := time.Now().Add(30 * time.Second)
	for {
		a.mu.Lock()
		done := !a.detecting && (a.install != nil || a.detectErr != "")
		a.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the detection never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
