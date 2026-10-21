package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/game"
)

// fakeSteam makes a Steam installation whose launcher writes down what it was
// given, and returns its root together with the file the record lands in.
//
// Starting the game comes to one program being run with the game's address, so
// a launcher of the test's own is how that is read back. body is the launcher's
// own script, with RECORD standing for the file to write to.
func fakeSteam(t *testing.T, body string) (root, record string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("the fake launcher is a steam.sh, which is the launcher on Linux alone")
	}
	root, record = t.TempDir(), filepath.Join(t.TempDir(), "record")
	script := fmt.Sprintf("#!/bin/sh\n%s\n", strings.ReplaceAll(body, "RECORD", record))
	if err := os.WriteFile(filepath.Join(root, "steam.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, record
}

// handedOver reads back what the fake Steam was given.
func handedOver(t *testing.T, record string) string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// launchable is a game with one mod in it, set up to load it, and a Steam to
// hand it to. It is the state both launches are tried from.
func launchable(t *testing.T) (*app, *game.Install, ui.Tester) {
	t.Helper()
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	if err := profile.SetEnabled("snd-42", true); err != nil {
		t.Fatal(err)
	}
	// The game as the app finds it: set up to load the mods, which is what the
	// page reads before either launch is tried.
	info, err := patchGameInfo(install)
	if err != nil {
		t.Fatal(err)
	}
	a.gameInfo = info
	root, _ := fakeSteam(t, `printf '%s' "$@" > RECORD`)
	install.SteamRoot = root
	return a, install, show(t, a, install, profile)
}

// launch clicks one of the two buttons and waits for what it started.
func launch(t *testing.T, a *app, tt ui.Tester, button string) {
	t.Helper()
	if err := tt.Click(button); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the launch to finish", func() bool { return !a.state().launching })
	tt.Frame()
}

// The mods page is where the game is started from, so both ways of starting it
// are offered there.
func TestTheModsPageOffersBothLaunches(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	tt := show(t, a, install, profile)

	for _, want := range []string{"启动原版游戏", "启动模组版游戏"} {
		if !tt.HasText(want) {
			t.Errorf("the mods page does not offer %q: %q", want, tt.Texts())
		}
	}
}

// There is nothing to start without a game directory, so nothing is offered.
func TestTheModsPageOffersNoLaunchWithoutAGameDirectory(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	if tt.HasText("启动模组版游戏") {
		t.Errorf("the mods page offers to start a game it has not found: %q", tt.Texts())
	}
}

// Starting the game with mods asks Steam for the game, and leaves the game
// told to load the directories the profile's mods are in.
func TestLaunchingWithModsWritesTheModPaths(t *testing.T) {
	a, install, tt := launchable(t)

	launch(t, a, tt, "启动模组版游戏")

	if !tt.HasText("游戏已交给 Steam 启动") {
		t.Errorf("the page does not say the game was handed over: %q", tt.Texts())
	}
	info, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModPaths || !info.Markers {
		t.Errorf("the game is not set up to load mods: %+v", info)
	}
	if got := a.state().gameInfo; !got.ModPaths {
		t.Errorf("the app does not report the game as set up: %+v", got)
	}
}

// Starting without mods is the one thing that turns them off: the game is put
// back the way it shipped before Steam is asked for anything.
func TestLaunchingWithoutModsPutsTheGameBackToVanilla(t *testing.T) {
	a, install, tt := launchable(t)

	launch(t, a, tt, "启动原版游戏")

	if !tt.HasText("这次不加载模组") {
		t.Errorf("the page does not say the mods were left out: %q", tt.Texts())
	}
	info, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.ModPaths {
		t.Error("the game is still told to search the mod directories")
	}
	data, err := os.ReadFile(install.GameInfoPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"citadel/addons", "// Deadlock Mod Manager - Start"} {
		if strings.Contains(string(data), gone) {
			t.Errorf("gameinfo.gi still holds %q:\n%s", gone, data)
		}
	}
}

// What starting the game comes to is Steam being handed the game's address,
// which is the whole of what the app asks for.
func TestLaunchingHandsSteamTheGamesAddress(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	root, record := fakeSteam(t, `printf '%s' "$@" > RECORD`)
	install.SteamRoot = root
	tt := show(t, a, install, profile)

	launch(t, a, tt, "启动模组版游戏")

	if got := handedOver(t, record); got != "steam://run/1422450//" {
		t.Errorf("Steam was asked for %q, want the game's address", got)
	}
}

// A launch Steam could not be asked for says why, in the launcher's own words:
// nothing else knows what went wrong.
func TestTheLaunchSaysWhySteamCouldNotBeAsked(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	root, _ := fakeSteam(t, `echo 'no handler for steam://' >&2; exit 3`)
	install.SteamRoot = root
	tt := show(t, a, install, profile)

	launch(t, a, tt, "启动模组版游戏")

	if !tt.HasText("no handler for steam://") {
		t.Errorf("the page does not say why the game did not start: %q", tt.Texts())
	}
	if tt.HasText("游戏已交给 Steam 启动") {
		t.Errorf("a launch that failed is reported as having worked: %q", tt.Texts())
	}
}

// A launch without mods writes the paths out of the game's configuration on
// purpose, which is not a game update having detached them: the page says the
// mods are left out, not that something is broken.
func TestLaunchingWithoutModsDoesNotWarnAboutDetachedMods(t *testing.T) {
	a, _, tt := launchable(t)
	if tt.HasText("游戏没有在加载模组目录") {
		t.Fatalf("the warning is up before the launch: %q", tt.Texts())
	}

	launch(t, a, tt, "启动原版游戏")
	tt.Frame()

	if !tt.HasText("这次不加载模组") {
		t.Errorf("the page does not say the mods were left out: %q", tt.Texts())
	}
	if tt.HasText("游戏没有在加载模组目录") {
		t.Errorf("a launch without mods is reported as mods detached: %q", tt.Texts())
	}
}
