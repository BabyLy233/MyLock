package main

import (
	"time"

	"github.com/BabyLy233/mylock/internal/game"
)

// gamePoll is how often the process list is looked at. The game takes far
// longer than this to start or close, and a look is one pass over the process
// list, so the window follows it closely enough to keep its controls off.
const gamePoll = 2 * time.Second

// isGameRunning reports whether Deadlock is running right now. It is asked
// again where files are about to move rather than trusting the last look,
// because the game can start in the interval between the two.
func (a *app) isGameRunning() bool {
	if a.running == nil {
		return false
	}
	return a.running()
}

// watchGame follows the game for as long as the app runs. It is the only
// reader of the process list, so what the window shows and what a launch is
// followed by come from the same series of looks.
func (a *app) watchGame() {
	for range time.Tick(gamePoll) {
		a.observeGame(a.isGameRunning())
	}
}

// observeGame takes one look at whether the game is running and does what a
// change in that calls for: the mods page stops offering to switch anything
// while it runs, and a session started without mods puts them back when it
// ends.
func (a *app) observeGame(running bool) {
	a.mu.Lock()
	wasRunning := a.gameRunning
	a.gameRunning = running
	newlySeen := running && a.vanillaPending && !a.vanillaSeen
	if newlySeen {
		a.vanillaSeen = true
	}
	// Putting the mods back waits until the vanilla session's game has been
	// seen. A launch Steam has taken but not started yet is still coming, and
	// restoring the mods before it would load them in that launch. Once it has
	// been seen, the game being gone is the session ending -- including one
	// that ended while the app was closed.
	restore := a.vanillaPending && a.vanillaSeen && !running
	install := a.install
	if restore {
		// Forgotten before the write rather than after it, so that a look
		// landing while that write is in flight cannot start a second one.
		a.vanillaPending, a.vanillaSeen = false, false
	}
	if !running {
		a.blocked = ""
	}
	a.mu.Unlock()

	switch {
	case newlySeen:
		// Written down so that a restart knows the game was really running.
		a.rememberSession()
	case restore && install == nil:
		// Nothing to restore without an installation, but the session is over
		// and no longer worth remembering.
		a.rememberSession()
	case restore:
		a.restoreMods(install)
		a.rememberSession()
	}

	if wasRunning != running {
		a.invalidate()
	}
}

// restoreMods puts the current profile's search paths back into the game's
// configuration after a session without mods has ended, so that the next
// launch, from the app or from Steam, loads them again.
func (a *app) restoreMods(install *game.Install) {
	info, err := a.patchGameInfo(install)

	a.publish(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil {
			a.restoreErr = "游戏已关闭，但没能恢复加载模组：" + err.Error()
			a.restored = ""
			return
		}
		a.gameInfo, a.gameInfoErr = info, ""
		a.restoreErr = ""
		a.restored = "游戏已关闭，已恢复加载模组。"
	})
}
