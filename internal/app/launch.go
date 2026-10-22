package app

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/game"
)

// launchRow starts the game, with the mods or without them.
//
// Which of the two a launch is decides what the game loads, and the game is
// told that in gameinfo.gi rather than by anything handed to Steam: a launch
// with mods writes the directories this profile's mods are in, and one without
// puts back what the game ships with. Both end the same way, in Steam being
// asked to start the game.
func (a *app) launchRow(c *ui.Context, s state) {
	if s.install == nil {
		return
	}
	t := c.Theme()
	mods := a.installed()
	// Nothing is started while the mods are being switched or the game's
	// configuration is being written: both of those write that configuration
	// too, and two writers at once would leave whichever finished last to say
	// what the game loads.
	busy := s.launching || mods.reading || s.patching || s.gameRunning

	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c, "启动原版游戏").
				Tooltip("不加载任何模组：先把游戏配置改回它自带的样子，再交给 Steam。").
				Disabled(busy).
				OnClick(func() { a.launch(true) })
			ui.Button(c, "启动模组版游戏").
				Tooltip(fmt.Sprintf("加载这 %d 个已启用的模组。", enabledCount(mods.list))).
				Disabled(busy).
				OnClick(func() { a.launch(false) })
			if s.launching {
				ui.Spinner(c)
			}
		})
		switch {
		case s.launching:
			ui.Text(c, "正在启动…").FontSize(12).TextColor(t.TextMuted)
		case s.launchErr != "":
			ui.Text(c, s.launchErr).FontSize(12).TextColor(t.Danger)
		// What the page says when the game is in the way comes before what an
		// earlier session left behind: starting again is what the buttons are
		// for, and it is what cannot be done.
		case s.gameRunning:
			ui.Text(c, "游戏正在运行，先退出游戏再启动。").FontSize(12).TextColor(t.TextMuted)
		// What putting the mods back after a vanilla session came to is newer
		// than the launch that session started with.
		case s.restoreErr != "":
			ui.Text(c, s.restoreErr).FontSize(12).TextColor(t.Danger)
		case s.restored != "":
			ui.Text(c, s.restored).FontSize(12).TextColor(t.Success)
		case s.launched != "":
			ui.Text(c, s.launched).FontSize(12).TextColor(t.Success)
		}
	})
}

// launch starts the game, with the mods or without them. It writes the game's
// configuration and runs Steam's launcher, both of which are the disk's and
// another process's, so it runs off the main thread.
func (a *app) launch(vanilla bool) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	// One launch at a time, and none while the mods are being switched: two
	// writers of the game's configuration would leave whichever finished last
	// to say what the game loads.
	busy := install == nil || a.launching || a.reading || a.patching
	if !busy {
		a.launching = true
		a.launchErr, a.launched = "", ""
	}
	a.mu.Unlock()
	if busy {
		return
	}
	a.invalidate()

	go func() {
		var info game.GameInfo
		err := error(nil)
		// The launch writes the game's configuration too, and the running game
		// is the one thing that must not have it changed underneath it. Asked
		// again here rather than trusting the last look, which is up to a poll
		// old.
		if a.isGameRunning() {
			err = errors.New("游戏正在运行，先退出游戏再启动。")
		} else {
			info, err = a.prepareLaunch(install, profileFor(install, folder), vanilla)
			if err == nil {
				err = install.Launch()
			}
		}

		var message string
		if err != nil {
			message = err.Error()
		}
		// Steam is what starts the game, so all there is to say about a request
		// it took is that it took it: whether the game then runs is Steam's to
		// say, and it says it in its own window.
		said := "游戏已交给 Steam 启动。"
		if vanilla {
			said = "游戏已交给 Steam 启动，这次不加载模组。"
		}

		a.publish(func() {
			a.mu.Lock()
			a.launching = false
			a.launchErr = message
			if err != nil {
				a.mu.Unlock()
				return
			}
			// What the launch left the configuration in is what the mods page
			// reads, so it is taken from the file rather than assumed: a launch
			// without mods is the one thing that turns them off.
			a.gameInfo, a.gameInfoErr = info, ""
			a.launched = said
			a.blocked = ""
			a.restored, a.restoreErr = "", ""
			// A launch without mods is followed until its game has come and
			// gone, and then the mods are put back. One with mods is the
			// configuration the game goes back to, so it cancels any waiting.
			a.vanillaPending, a.vanillaSeen = vanilla, false
			a.mu.Unlock()
			// Written down as well, so that closing the app before the game
			// does not lose track of the session.
			a.rememberSession()
		})
	}()
}

// prepareLaunch writes the game's configuration for a launch, and reports what
// state that left it in. The write is one at a time with the switches and the
// settings page's button, which write the same file.
func (a *app) prepareLaunch(install *game.Install, profile addons.Profile, vanilla bool) (game.GameInfo, error) {
	a.gameinfoMu.Lock()
	defer a.gameinfoMu.Unlock()

	// A launch with mods writes what the settings page's button writes, and
	// reports the state it left behind in the same way.
	if !vanilla {
		return patchGameInfo(install, profile)
	}
	if _, err := install.PatchGameInfoVanilla(); err != nil {
		return game.GameInfo{}, err
	}
	return install.ReadGameInfo()
}
