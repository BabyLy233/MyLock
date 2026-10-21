package main

import (
	"os"
	"testing"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/config"
)

// The game holds the files a mod is made of, so the page offers no switch
// while it runs. The switch is checked again where the files would move: one
// that was already asked for cannot be undone by the game having started.
func TestTheModsPageStopsSwitchingWhileTheGameRuns(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	a.running = func() bool { return true }
	tt := show(t, a, install, profile)

	a.observeGame(true)
	tt.Frame()
	if !tt.HasText("游戏正在运行，先退出游戏再装卸模组。") {
		t.Errorf("the page does not say why the mods are left alone: %q", tt.Texts())
	}

	a.setEnabled("snd-42", true)
	waitFor(t, "the refused switch", func() bool {
		return !a.installed().reading && a.state().blocked != ""
	})

	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Enabled {
		t.Errorf("the mod was switched while the game was running: %+v", mods)
	}
}

// Starting the game writes the game's configuration, so it is not offered
// while the game is already running either.
func TestLaunchingIsRefusedWhileTheGameRuns(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	root, record := fakeSteam(t, `printf '%s' "$@" > RECORD`)
	install.SteamRoot = root
	a.running = func() bool { return true }
	tt := show(t, a, install, profile)
	a.observeGame(true)
	tt.Frame()

	a.launch(false)
	waitFor(t, "the refused launch", func() bool { return !a.state().launching })
	tt.Frame()

	if !tt.HasText("游戏正在运行，先退出游戏再启动。") {
		t.Errorf("the page does not say why the game was not started: %q", tt.Texts())
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("Steam was asked to start the game while it was already running")
	}
}

// A launch without mods is followed until its game has come and gone, and then
// the mods are put back, so the next launch from Steam loads them again.
func TestAVanillaSessionPutsTheModsBackWhenItEnds(t *testing.T) {
	a, install, tt := launchable(t)
	a.running = func() bool { return false }

	launch(t, a, tt, "启动原版游戏")
	if info, err := install.ReadGameInfo(); err != nil || info.ModPaths {
		t.Fatalf("the vanilla launch did not turn the mods off: %+v, %v", info, err)
	}

	// The game comes up, and the mods stay off for as long as it runs.
	a.observeGame(true)
	tt.Frame()
	if info, err := install.ReadGameInfo(); err != nil || info.ModPaths {
		t.Errorf("the mods were put back while the vanilla game was running: %+v, %v", info, err)
	}

	// It closes, and they are put back without the user having to ask.
	a.observeGame(false)
	tt.Frame()
	info, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModPaths || !info.Markers {
		t.Errorf("the mods were not put back after the vanilla session: %+v", info)
	}
	if !tt.HasText("游戏已关闭，已恢复加载模组。") {
		t.Errorf("the page does not say the mods are back: %q", tt.Texts())
	}
	if tt.HasText("游戏没有在加载模组目录") {
		t.Errorf("the page still warns about the mods not loading: %q", tt.Texts())
	}
}

// Closing the app in the middle of a vanilla session does not lose track of it:
// the next run puts the mods back once the game is gone.
func TestAVanillaSessionIsRememberedAcrossARestart(t *testing.T) {
	a, install, tt := launchable(t)
	path := settingsPath(t, a)
	a.running = func() bool { return false }

	launch(t, a, tt, "启动原版游戏")
	a.observeGame(true)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VanillaPending || !cfg.VanillaSeen {
		t.Fatalf("the session in flight was not written down: %+v", cfg)
	}

	// The app is closed and started again after the game has closed.
	cfg.GamePath = install.GamePath
	b := newApp()
	b.configPath = path
	b.running = func() bool { return false }
	b.start(cfg, nil)
	waitForLook(t, b)
	b.observeGame(false)

	info, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModPaths {
		t.Error("a vanilla session that ended while the app was closed was not put back")
	}
}

// A launch Steam has taken may still be about to start its game, so finding
// nothing running does not put the mods back: doing that would load them in
// the very launch that asked for none.
func TestAVanillaLaunchWaitsForItsGame(t *testing.T) {
	a, install, tt := launchable(t)
	a.running = func() bool { return false }

	launch(t, a, tt, "启动原版游戏")

	a.observeGame(false)
	if info, err := install.ReadGameInfo(); err != nil || info.ModPaths {
		t.Fatalf("the mods were put back before the vanilla launch's game ran: %+v, %v", info, err)
	}

	a.observeGame(true)
	a.observeGame(false)
	if info, err := install.ReadGameInfo(); err != nil || !info.ModPaths {
		t.Errorf("the mods were not put back after the session ended: %+v, %v", info, err)
	}
}
