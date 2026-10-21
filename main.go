// Command mylock manages mods for Valve's Deadlock. Its window is Mygo's native
// UI: the interface is Go, drawn on the GPU, with no web page and no webview.
package main

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/download"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/gb"
	"github.com/BabyLy233/mylock/internal/imagecache"
)

// app is the state the window shows. The view builds the interface from it on
// the main thread, whenever the window needs a frame.
type app struct {
	router *ui.Router
	win    *mygo.Window

	// configPath is the settings file, empty when the platform has no config
	// directory, in which case the app runs but remembers nothing.
	configPath string

	// pathInput is the text of the settings page's field. The view binds it
	// directly, so that an edit is not lost to the rebuilds of one event. Only
	// the main thread touches it; a goroutine is handed its value instead.
	pathInput string

	// list keeps the mod list's place and its keyboard focus across the
	// rebuilds that a switch being flipped causes.
	list ui.ListState
	// deleteAsk is the mod the confirmation dialog is open for, empty when it
	// is closed. Only the main thread touches it.
	deleteAsk string
	// profilesOpen is whether the profile manager is open, newProfileOpen the
	// dialog for a new one, and profileDeleteAsk the folder whose deletion is
	// being confirmed. Only the main thread touches them.
	profilesOpen     bool
	newProfileOpen   bool
	profileDeleteAsk string
	newProfileName   string
	newProfileDesc   string

	// catalogue reads GameBanana, which is where the store's mods come from.
	catalogue *gb.Client
	// downloads fetches a mod's archive, checking it against what the API says
	// it should be.
	downloads *download.Client
	// thumbnails holds the store's pictures, which are fetched and decoded off
	// the main thread.
	thumbnails *images
	// imageCache is where those pictures are kept between runs, under the
	// platform's cache directory. It is nil when there is no such directory.
	imageCache *imagecache.Cache
	// store is the mod store page's state.
	store *store

	// mu guards what the lookups publish for the view to read, and the
	// appearance, which a save reads from a goroutine.
	mu sync.Mutex
	// settingsMu keeps one write of the settings file at a time: a launch and
	// a path change can both save, and the file is replaced whole.
	settingsMu sync.Mutex
	// profilesMu does the same for the profiles file, which a scan, a switch
	// and a deletion can all write.
	profilesMu sync.Mutex
	// gameinfoMu keeps one read of the profile and write of the game's
	// configuration together: a switch and a session's mods being put back can
	// both do it, and the paths the later write puts down must be the profile
	// as it is then.
	gameinfoMu sync.Mutex
	theme      mygo.ThemeSource
	looking    bool
	browsing   bool
	reading    bool
	patching   bool
	pinned     string
	install    *game.Install
	lookErr    string
	mods       []addons.Mod
	modsErr    string
	// profiles are the named profiles beside the default one, and
	// profileFolder is the one in use, empty for the default profile.
	// profilePresent is which folders the last scan found on disk, so a
	// profile whose folder has gone is shown as missing rather than dropped.
	profiles       []config.Profile
	profileFolder  string
	profilePresent map[string]bool
	profilesErr    string
	gameInfo       game.GameInfo
	gameInfoErr    string
	// The launch in flight, if there is one: why it could not be started, and
	// what came of the last one that was.
	launching bool
	launchErr string
	launched  string

	// running is how the app asks whether Deadlock is running. It is a field
	// so that a test can answer instead of the process list.
	running func() bool
	// gameRunning is whether the last look at the process list found the game,
	// which is what the window is drawn from.
	gameRunning bool
	// vanillaPending and vanillaSeen belong to a launch without mods: the game
	// was last asked to start without them, and whether that launch's process
	// has been seen. Once it has been seen and then goes away, the game is put
	// back to loading the mods.
	vanillaPending bool
	vanillaSeen    bool
	// blocked is why the last switching or installing was not done, which is
	// what a game that started in the meantime leaves behind. restored and
	// restoreErr are what putting the mods back after a vanilla session came to.
	blocked    string
	restored   string
	restoreErr string
	// cacheBusy is set while the picture cache is being cleared, and cacheErr
	// is why that failed.
	cacheBusy bool
	cacheErr  string
}

// state is what the view reads about the game directory, taken in one lock.
type state struct {
	// looking is set while a lookup runs.
	looking bool
	// browsing is set while the system's directory picker is open.
	browsing bool
	// pinned is the directory the user chose, empty when the app detects the
	// game through Steam instead.
	pinned string
	// install is the installation in use, nil until a lookup succeeds.
	install *game.Install
	// lookErr is why the last lookup did not produce one, empty when it did.
	lookErr string
	// gameInfo is the state of the game's configuration, which says whether
	// the game is set up to load mods at all.
	gameInfo game.GameInfo
	// gameInfoErr is why that could not be read or written.
	gameInfoErr string
	// patching is set while the game's configuration is being written.
	patching bool
	// launching is set while the game is being started, and the other two are
	// why it could not be and what came of it.
	launching bool
	launchErr string
	launched  string
	// gameRunning is whether the last look at the process list found the game,
	// which is when the mods must be left alone.
	gameRunning bool
	// vanillaPending reports that the last launch was made without mods, so
	// the paths missing from the game's configuration are on purpose.
	vanillaPending bool
	// blocked is why the last switching or installing was not done, and
	// restored and restoreErr are what putting the mods back after a vanilla
	// session came to.
	blocked    string
	restored   string
	restoreErr string
	// cacheBusy is set while the picture cache is being cleared, and cacheErr
	// is why that failed.
	cacheBusy bool
	cacheErr  string
	// theme is the appearance in effect, which the settings page shows as the
	// chosen segment.
	theme mygo.ThemeSource
}

// themeSources are the appearances the settings page offers, in the order it
// offers them, and themeLabels names them.
var (
	themeSources = []mygo.ThemeSource{mygo.ThemeSystem, mygo.ThemeLight, mygo.ThemeDark}
	themeLabels  = []string{"跟随系统", "浅色", "深色"}
)

// themeSource reads the appearance to run in out of what was saved. A value
// that is not one of them -- a file edited by hand, or one left by a version
// that spelled it differently -- follows the system rather than failing.
func themeSource(saved string) mygo.ThemeSource {
	if source := mygo.ThemeSource(saved); slices.Contains(themeSources, source) {
		return source
	}
	return mygo.ThemeSystem
}

// modsState is what the view reads about the mods of the profile, taken in one
// lock.
type modsState struct {
	// list holds the mods, the enabled ones in the order the game loads them.
	list []addons.Mod
	// err is why the profile could not be read, empty when it could.
	err string
	// reading is set while a read or a switch being flipped is in flight.
	reading bool
}

func newApp() *app {
	a := &app{
		router:     ui.NewRouter("/mods"),
		catalogue:  gb.New(),
		downloads:  download.New(),
		thumbnails: newImages(),
		running:    game.Running,
	}
	a.store = newStore(a)
	return a
}

func (a *app) view(c *ui.Context) {
	a.paint(c)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)
		a.router.View(c, func(r *ui.Route) {
			switch {
			case r.Match("/mods"):
				r.Title("已安装模组")
				a.modsPage(c)
			case r.Match("/store"):
				r.Title("模组商店")
				a.store.view(c)
			case r.Match("/settings"):
				r.Title("设置")
				a.settingsPage(c)
			default:
				ui.Text(c, "没有这个页面").Padding(24)
			}
		})
	})
}

// sidebar lists the pages. It is built before the router builds the page, so
// it stays as the pages change.
func (a *app) sidebar(c *ui.Context) {
	page := a.router.Path()
	ui.Sidebar(c, &page, func() {
		ui.SidebarItem(c, "/mods", nil, "已安装模组")
		ui.SidebarItem(c, "/store", nil, "模组商店")
		ui.SidebarItem(c, "/settings", nil, "设置")
	}).Width(220).OnChange(func() {
		a.router.Push(page)
	})
}

// modsPage lists the mods installed in the game, in the order the game loads
// them, with a switch on each to turn it on or off.
func (a *app) modsPage(c *ui.Context) {
	t := c.Theme()
	s := a.state()
	mods := a.installed()
	profiles := a.profileStateNow()

	a.deleteDialog(c)
	a.profilesDialog(c)
	a.newProfileDialog(c)
	a.profileDeleteDialog(c)

	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Row(c).Fill().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已安装模组").FontSize(22).Bold()
			ui.Spacer(c)
			ui.Text(c, "配置档："+profileDisplayName(profiles.folder, profiles.profiles)).
				FontSize(12).TextColor(t.TextMuted)
			ui.Button(c, "切换 / 管理…").
				Disabled(s.install == nil || s.looking).
				OnClick(a.openProfiles)
		})
		// Above the list rather than below it: starting the game is what the
		// page is for, and it is the one thing here that is about the game
		// rather than about one of the mods.
		a.launchRow(c, s)
		switch {
		case s.install == nil:
			ui.Text(c, "还没有找到游戏目录，先到设置里指定。").TextColor(t.TextMuted)
			return
		case s.looking:
			ui.Text(c, "正在读取…").TextColor(t.TextMuted)
			return
		case mods.err != "":
			ui.Text(c, mods.err).TextColor(t.Danger)
			return
		case len(mods.list) == 0:
			ui.Text(c, "这个目录里还没有模组。").TextColor(t.TextMuted)
			return
		}

		ui.Text(c, s.install.AddonsDir()).FontSize(12).Selectable().TextColor(t.TextMuted)
		// The game holds the files it loaded, so moving them under it is how a
		// mod ends up half read. Nothing here is offered until it is closed.
		if s.gameRunning {
			ui.Text(c, "游戏正在运行，先退出游戏再装卸模组。").FontSize(12).TextColor(t.Danger)
		}
		if s.blocked != "" {
			ui.Text(c, s.blocked).FontSize(12).TextColor(t.Danger)
		}
		if profiles.err != "" {
			ui.Text(c, profiles.err).FontSize(12).TextColor(t.Danger)
		}
		// Enabled mods that the game is not set up to load are the one thing
		// the list cannot show by itself, and the one thing a game update
		// causes without any other sign.
		if enabled := enabledCount(mods.list); enabled > 0 && !s.gameInfo.ModPaths && !s.vanillaPending {
			ui.Text(c, fmt.Sprintf(
				"游戏没有在加载模组目录，这 %d 个已启用的模组不会生效。到设置里点一下「应用模组路径」。",
				enabled)).FontSize(12).TextColor(t.Danger)
		}
		// The list is keyed by mod id rather than by row number, so that a mod
		// changing place as it is enabled takes its focus and its place in the
		// scroll with it instead of handing them to whatever moved up.
		ui.List(c, &a.list, len(mods.list)).
			ItemKey(func(i int) any { return mods.list[i].ID }).
			Fill().
			Rows(func(r ui.ListRow) {
				a.modRow(c, r, mods.list[r.Index], mods.reading || s.gameRunning)
			})
	})
}

// modRow is one mod: a switch, its id, and what it is made of.
func (a *app) modRow(c *ui.Context, r ui.ListRow, mod addons.Mod, reading bool) {
	t := c.Theme()
	// A local copy, because the switch binds to a value the view rebuilds every
	// pass. OnChange runs once the bound switch has written the new state into
	// it, which is why the value is read there and not here.
	enabled := mod.Enabled

	ui.Row(c).Fill().Padding(12).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Switch(c, &enabled).
			// The switch shows no text of its own, so it is named for assistive
			// technology rather than left as an unnamed control.
			Label("启用 " + mod.ID).
			Disabled(reading).
			OnChange(func() {
				a.setEnabled(mod.ID, enabled)
			})
		ui.Column(c).Fill().Gap(2).Children(func() {
			ui.Text(c, mod.ID).Bold()
			ui.Text(c, modSummary(mod)).FontSize(12).TextColor(t.TextMuted)
			if len(mod.Missing) > 0 {
				ui.Text(c, fmt.Sprintf("缺少 %d 个文件，游戏可能读不到它", len(mod.Missing))).
					FontSize(12).TextColor(t.Danger)
			}
		})
		// The id is in the label as well as the name, because a row's button
		// reads just 删除 and the dialog has to say which mod it will take.
		ui.Button(c, "删除").
			Label("删除 " + mod.ID).
			Disabled(reading).
			OnClick(func() { a.deleteAsk = mod.ID })
	})
}

// deleteDialog asks before a mod is deleted, which takes its files with it and
// cannot be undone. It is drawn only while a row has asked for it.
func (a *app) deleteDialog(c *ui.Context) {
	if a.deleteAsk == "" {
		return
	}
	id := a.deleteAsk
	open := true
	switch ui.AlertDialog(c, &open, "删除“"+id+"”？",
		"这个模组的文件会从模组目录里删掉，不能撤销。", "取消", "确认删除") {
	case 0, ui.AlertDismissed:
		a.deleteAsk = ""
	case 1:
		a.deleteAsk = ""
		a.removeMod(id)
	}
}

// enabledCount is how many of the mods the game is meant to be loading.
func enabledCount(mods []addons.Mod) int {
	n := 0
	for _, mod := range mods {
		if mod.Enabled {
			n++
		}
	}
	return n
}

// modSummary says what a mod is made of, in the terms the game loads it in.
func modSummary(mod addons.Mod) string {
	files := fmt.Sprintf("%d 个文件", len(mod.Vpks))
	switch {
	case !mod.Enabled:
		return files + " · 已停用"
	case mod.Pak > 0:
		return fmt.Sprintf("%s · pak%02d · 分片 %d", files, mod.Pak, mod.Shard)
	default:
		return fmt.Sprintf("%s · 分片 %d", files, mod.Shard)
	}
}

// installed reads what the last read of the profile found.
func (a *app) installed() modsState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return modsState{list: a.mods, err: a.modsErr, reading: a.reading}
}

// setEnabled turns a mod on or off. It moves files, so it runs off the main
// thread, and it reads the profile back afterwards rather than assuming the
// change took: the list then shows what is on disk.
func (a *app) setEnabled(id string, on bool) {
	a.mu.Lock()
	// One switch at a time: two of them moving files at once would race over
	// the same manifest. Nothing to switch without a game directory either.
	install, folder := a.install, a.profileFolder
	busy := install == nil || a.reading
	if !busy {
		a.reading = true
	}
	a.mu.Unlock()
	if busy {
		return
	}
	a.invalidate()

	go func() {
		// Checked here rather than trusting the last look at the process list:
		// the game can start in the interval between the two, and it holds the
		// files this is about to move.
		if a.isGameRunning() {
			a.publish(func() {
				a.mu.Lock()
				a.reading = false
				a.blocked = "游戏正在运行，先退出游戏再装卸模组。"
				a.mu.Unlock()
			})
			return
		}

		profile := profileFor(install, folder)
		switchErr := profile.SetEnabled(id, on)
		// Read back whatever the profile holds now, whether the switch worked
		// or not: after a failure the list should show the disk, not the state
		// the user asked for.
		mods, readErr := profile.Read()

		// A mod enabled into a shard the game was not searching would sit there
		// doing nothing, so the game's configuration follows every change.
		info, infoErr := game.GameInfo{}, error(nil)
		if switchErr == nil {
			info, infoErr = a.patchGameInfo(install, profile)
		}

		var message string
		if err := errors.Join(switchErr, readErr); err != nil {
			message = err.Error()
		}
		var infoMessage string
		if infoErr != nil {
			infoMessage = infoErr.Error()
		}
		var forgot bool
		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.blocked = ""
			a.mods, a.modsErr = mods, message
			if switchErr == nil {
				a.gameInfo, a.gameInfoErr = info, infoMessage
				// This wrote the mod paths, so it is no longer a launch
				// without mods the app is waiting on.
				forgot = a.clearVanillaSession()
			}
			a.mu.Unlock()
			if forgot {
				a.rememberSession()
			}
		})
	}()
}

// removeMod deletes a mod's files and its manifest entry, which is the way
// back from installing one by mistake. It cannot be undone, so the view asks
// before it gets here. Like a switch, it runs off the main thread and reads the
// profile back afterwards.
func (a *app) removeMod(id string) {
	a.mu.Lock()
	// The same one-operation-at-a-time rule as a switch: both write the
	// manifest and move files.
	install, folder := a.install, a.profileFolder
	busy := install == nil || a.reading
	if !busy {
		a.reading = true
	}
	a.mu.Unlock()
	if busy {
		return
	}
	a.invalidate()

	go func() {
		// The game can start between the last look at the process list and
		// this, and it holds the files that are about to go.
		if a.isGameRunning() {
			a.publish(func() {
				a.mu.Lock()
				a.reading = false
				a.blocked = "游戏正在运行，先退出游戏再装卸模组。"
				a.mu.Unlock()
			})
			return
		}

		profile := profileFor(install, folder)
		removeErr := profile.Remove(id)
		// The list shows the disk after this either way: a deletion that
		// failed partway still leaves the manifest and the files to report.
		mods, readErr := profile.Read()

		// Deleting a mod can leave a shard with nothing in it, and a shard the
		// game is still searching is harmless but untidy. The configuration
		// follows every change, as it does a switch.
		info, infoErr := game.GameInfo{}, error(nil)
		if removeErr == nil {
			info, infoErr = a.patchGameInfo(install, profile)
		}

		var message string
		if err := errors.Join(removeErr, readErr); err != nil {
			message = err.Error()
		}
		var infoMessage string
		if infoErr != nil {
			infoMessage = infoErr.Error()
		}
		var forgot bool
		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.blocked = ""
			a.mods, a.modsErr = mods, message
			if removeErr == nil {
				a.gameInfo, a.gameInfoErr = info, infoMessage
				// This wrote the mod paths, so it is no longer a launch
				// without mods the app is waiting on.
				forgot = a.clearVanillaSession()
			}
			a.mu.Unlock()
			if forgot {
				a.rememberSession()
			}
		})
	}()
}

// applyGameInfo writes the mod directories into the game's configuration, which
// is what the user does after a game update has taken them out again.
func (a *app) applyGameInfo() {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	busy := install == nil || a.patching || a.reading
	if !busy {
		a.patching = true
	}
	a.mu.Unlock()
	if busy {
		return
	}
	a.invalidate()

	go func() {
		if a.isGameRunning() {
			a.publish(func() {
				a.mu.Lock()
				a.patching = false
				a.blocked = "游戏正在运行，先退出游戏再应用模组路径。"
				a.mu.Unlock()
			})
			return
		}

		info, err := a.patchGameInfo(install, profileFor(install, folder))
		var message string
		if err != nil {
			message = err.Error()
		}
		var forgot bool
		a.publish(func() {
			a.mu.Lock()
			a.patching = false
			a.blocked = ""
			a.gameInfo, a.gameInfoErr = info, message
			if err == nil {
				// This wrote the mod paths, so it is no longer a launch
				// without mods the app is waiting on.
				forgot = a.clearVanillaSession()
			}
			a.mu.Unlock()
			if forgot {
				a.rememberSession()
			}
		})
	}()
}

// patchGameInfo tells the game to search the directories the profile's mods
// are in, and reports what state that left the configuration in. It reads and
// writes the game's own files, so it runs off the main thread, and the read of
// the profile and the write of the file are kept together: two of them at once
// could leave the file naming a set of paths that misses a mod switched on in
// between.
func (a *app) patchGameInfo(install *game.Install, profile addons.Profile) (game.GameInfo, error) {
	a.gameinfoMu.Lock()
	defer a.gameinfoMu.Unlock()
	return patchGameInfo(install, profile)
}

// patchGameInfo is the write itself, without the lock this app's callers take.
func patchGameInfo(install *game.Install, profile addons.Profile) (game.GameInfo, error) {
	paths, err := profile.SearchPaths()
	if err != nil {
		return game.GameInfo{}, err
	}
	if _, err := install.PatchGameInfo(paths); err != nil {
		return game.GameInfo{}, err
	}
	return install.ReadGameInfo()
}

func (a *app) settingsPage(c *ui.Context) {
	s := a.state()
	ui.Column(c).Fill().Padding(24).Gap(20).Children(func() {
		ui.Text(c, "设置").FontSize(22).Bold()
		a.installSection(c, s)
		a.gameInfoSection(c, s)
		a.pathSection(c, s)
		a.themeSection(c, s)
		a.cacheSection(c, s)
	})
}

// gameInfoSection shows whether the game is set up to load mods, which is a
// thing the game itself quietly undoes: an update replaces gameinfo.gi, and the
// mods stop loading with nothing on screen to say why.
func (a *app) gameInfoSection(c *ui.Context, s state) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "游戏配置").Bold()
		switch {
		case s.install == nil:
			ui.Text(c, "先指定游戏目录。").TextColor(t.TextMuted)
		case s.patching:
			ui.Text(c, "正在应用…").TextColor(t.TextMuted)
		case s.gameInfo.ModPaths:
			ui.Text(c, "已应用，游戏会加载模组目录。").TextColor(t.TextMuted)
			field(c, "gameinfo.gi", s.gameInfo.Path)
		case s.vanillaPending:
			ui.Text(c, "这次启动不加载模组，退出游戏后会自动恢复。").TextColor(t.TextMuted)
		default:
			ui.Text(c, "还没应用。游戏不会去读模组目录，除非 gameinfo.gi 里写上它们。").
				TextColor(t.Danger)
		}
		// Shown beside a state that is otherwise fine: a write that failed is
		// worth saying even when the file was left in the right shape.
		if s.gameInfoErr != "" && !s.patching {
			ui.Text(c, s.gameInfoErr).TextColor(t.Danger)
		}
		if s.blocked != "" {
			ui.Text(c, s.blocked).TextColor(t.Danger)
		}
	})
	if s.install != nil {
		// In a row so the button takes its own width, as the ones below do.
		ui.Row(c).Children(func() {
			ui.Button(c, "应用模组路径").
				Disabled(s.patching || s.looking || s.gameRunning).
				OnClick(a.applyGameInfo)
		})
	}
}

// installSection shows where the game is, and whether the app found it itself
// or was pointed at it.
func (a *app) installSection(c *ui.Context, s state) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "游戏目录").Bold()
		switch {
		case s.looking:
			ui.Text(c, "正在查找…").TextColor(t.TextMuted)
		case s.install != nil:
			field(c, "游戏", s.install.GamePath)
			if s.pinned != "" {
				field(c, "来源", "手动指定")
			} else {
				field(c, "Steam 库", s.install.LibraryPath)
				field(c, "Steam", s.install.SteamRoot)
				field(c, "来源", "自动检测")
			}
			if s.install.BuildID != "" {
				field(c, "构建号", s.install.BuildID)
			}
		default:
			ui.Text(c, "尚未查找").TextColor(t.TextMuted)
		}
		// Shown beside a successful lookup too: a settings file that could not
		// be written is worth saying even when the game was found.
		if s.lookErr != "" && !s.looking {
			ui.Text(c, s.lookErr).TextColor(t.Danger)
		}
	})
}

// pathSection lets the user point the app at the game themselves, which is the
// way out when detection finds nothing.
func (a *app) pathSection(c *ui.Context, s state) {
	ui.Field(c, "手动指定目录", func() {
		ui.Row(c).Fill().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &a.pathInput).
				Fill().
				Placeholder("包含 game/citadel 的目录").
				OnSubmit(a.usePath)
			ui.Button(c, "浏览…").Disabled(s.browsing).OnClick(func() {
				a.browse(a.pathInput)
			})
		})
	}).Description("选到游戏自己的那一层，就是包含 game/citadel 的目录。")

	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "使用此目录").Disabled(s.looking).OnClick(a.usePath)
		ui.Button(c, "重新检测").Disabled(s.looking).OnClick(a.detect)
	})
}

// themeSection lets the user pick the appearance, or leave it to the desktop,
// which is what a first run does.
func (a *app) themeSection(c *ui.Context, s state) {
	ui.Field(c, "外观", func() {
		// In a row so the control takes its own width rather than the field's.
		ui.Row(c).Children(func() {
			// A local copy, which the control writes the chosen index into
			// before its notice runs: what was chosen is what the control left
			// here, not what the app held when the frame was built.
			chosen := max(slices.Index(themeSources, s.theme), 0)
			ui.Segmented(c, &chosen, themeLabels...).OnChange(func() {
				a.setTheme(themeSources[chosen])
			})
		})
	}).Description("「跟随系统」随桌面的浅色 / 深色切换。")
}

// field shows a label above a value the user can select and copy.
func field(c *ui.Context, label, value string) {
	if value == "" {
		return
	}
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).Selectable()
	})
}

// cacheSection shows what the pictures take on disk, and lets the user take it
// back. The cache is bounded, so this is a number that stops growing; the
// button is there for a user who wants it gone now.
func (a *app) cacheSection(c *ui.Context, s state) {
	if a.imageCache == nil {
		return
	}
	t := c.Theme()
	bytes, pictures := a.imageCache.Stats()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "图片缓存").Bold()
		ui.Text(c, fmt.Sprintf("已用 %s，共 %s 张，上限 %s",
			formatBytes(bytes), thousands(pictures), formatBytes(a.imageCache.Budget()))).
			TextColor(t.TextMuted)
		ui.Text(c, "看过的图片会留在磁盘上，下次打开不再下载；超过上限时，最久没看的会被删掉。").
			FontSize(12).TextColor(t.TextMuted)
		if s.cacheErr != "" {
			ui.Text(c, s.cacheErr).TextColor(t.Danger)
		}
		ui.Row(c).Children(func() {
			ui.Button(c, "清除缓存").
				Disabled(s.cacheBusy).
				OnClick(a.clearImageCache)
		})
	})
}

// formatBytes says how much disk something takes, in the units a person reads.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// useImageCache gives the pictures a cache on disk. It is called before the
// window opens, so that nothing is fetched while the two disagree about where
// a picture is kept.
func (a *app) useImageCache(cache *imagecache.Cache) {
	a.imageCache = cache
	a.thumbnails.disk = cache
}

// clearImageCache empties the picture cache. It deletes files, so it runs off
// the main thread, and the page shows the new size once it is done.
func (a *app) clearImageCache() {
	cache := a.imageCache
	if cache == nil {
		return
	}
	a.mu.Lock()
	busy := a.cacheBusy
	if !busy {
		a.cacheBusy = true
		a.cacheErr = ""
	}
	a.mu.Unlock()
	if busy {
		return
	}
	a.invalidate()

	go func() {
		err := cache.Clear()
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.cacheBusy = false
			a.cacheErr = message
			a.mu.Unlock()
		})
		a.invalidate()
	}()
}

// state reads what the last lookup found, in the order the settings page shows
// it.
func (a *app) state() state {
	a.mu.Lock()
	defer a.mu.Unlock()
	return state{
		looking:        a.looking,
		browsing:       a.browsing,
		pinned:         a.pinned,
		install:        a.install,
		lookErr:        a.lookErr,
		gameInfo:       a.gameInfo,
		gameInfoErr:    a.gameInfoErr,
		patching:       a.patching,
		launching:      a.launching,
		launchErr:      a.launchErr,
		launched:       a.launched,
		gameRunning:    a.gameRunning,
		vanillaPending: a.vanillaPending,
		blocked:        a.blocked,
		restored:       a.restored,
		restoreErr:     a.restoreErr,
		cacheBusy:      a.cacheBusy,
		cacheErr:       a.cacheErr,
		theme:          a.theme,
	}
}

// start looks for the game the way the settings say to. They are read before
// the app runs rather than here, because the appearance has to be known before
// the first frame is drawn.
func (a *app) start(cfg config.Config, cfgErr error) {
	// A session the app was following when it last ended is picked up again,
	// whether its game is still running or has closed in the meantime.
	a.vanillaPending, a.vanillaSeen = cfg.VanillaPending, cfg.VanillaSeen
	a.profileFolder = cfg.ProfileFolder
	// The named profiles are read before the lookup starts, so that the first
	// frame already names the profile in use.
	if path := config.ProfilesPath(a.configPath); path != "" {
		book, err := config.LoadProfiles(path)
		a.profiles = book.Profiles
		if err != nil {
			a.profilesErr = err.Error()
		}
	}
	if cfgErr != nil {
		// The directory the user chose last time is not in effect, so the app
		// detects the game instead, and says why.
		a.look("", false, cfgErr)
		return
	}
	if cfg.GamePath != "" {
		a.pathInput = cfg.GamePath
	}
	// The settings already hold this, so there is nothing to write back.
	a.look(cfg.GamePath, false, nil)
}

// usePath takes the directory the user typed, and makes it the one the app
// uses. An empty field is refused here rather than treated as a request to
// detect the game, which is what 重新检测 is for.
func (a *app) usePath() {
	path := strings.TrimSpace(a.pathInput)
	if path == "" {
		a.fail("先填一个目录，或者用「浏览…」选一个。")
		return
	}
	a.look(path, true, nil)
}

// detect looks for the game through Steam again, and drops the directory the
// user pinned: this is the way back to automatic detection.
func (a *app) detect() {
	a.mu.Lock()
	// Dropped from the moment it is asked for, so the window stops naming a
	// directory the app is no longer using while the lookup runs.
	a.pinned, a.install = "", nil
	a.mu.Unlock()
	a.look("", true, nil)
}

// browse shows the system's directory picker and uses what the user chose. The
// dialog blocks until it is dismissed, so it runs off the main thread. from is
// where the picker opens, which the caller reads on the main thread.
func (a *app) browse(from string) {
	a.mu.Lock()
	if a.browsing {
		a.mu.Unlock()
		return
	}
	a.browsing = true
	a.mu.Unlock()
	a.invalidate()

	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      a.win,
			Title:       "选择 Deadlock 目录",
			Message:     "选到游戏自己的那一层，就是包含 game/citadel 的目录。",
			DefaultPath: from,
			Directory:   true,
		})
		a.publish(func() {
			a.mu.Lock()
			a.browsing = false
			a.mu.Unlock()
			switch {
			case err != nil:
				a.fail(err.Error())
			case len(paths) > 0:
				a.pathInput = paths[0]
				a.usePath()
			}
			// Otherwise the user canceled, and nothing changes.
		})
	}()
}

// look finds the installation to use: the one at pinned, or the one detection
// finds when pinned is empty. remember writes pinned to the settings file as
// the directory to use from now on. warn is reported beside the result, for a
// failure that does not stop the lookup.
//
// A lookup that finds nothing changes only what the window says, never what
// the app uses, so a mistyped directory is not the one the app starts with
// next time. An empty pinned is the exception: it is written either way,
// because dropping a pinned directory is what 重新检测 asks for.
//
// It reads and writes the disk, so it runs off the main thread, and the window
// shows itself as busy until it is done.
func (a *app) look(pinned string, remember bool, warn error) {
	a.mu.Lock()
	a.looking, a.lookErr = true, ""
	// The profile is fixed here rather than read again after the lookup: a
	// switch that lands in between must not make this read the mods of one
	// profile and publish them as the other's.
	folder := a.profileFolder
	a.mu.Unlock()
	a.invalidate()

	go func() {
		install, lookErr := game.Resolve(pinned)

		var saveErr error
		if remember && (lookErr == nil || pinned == "") {
			saveErr = a.saveSettings(pinned)
		}

		// The mods are read in the same pass as the game directory is found,
		// so that a change of directory shows its mods in one step rather than
		// leaving the list of the old one up.
		var mods []addons.Mod
		var modsErr string
		var info game.GameInfo
		var infoErr string
		var profiles []config.Profile
		var present map[string]bool
		var profilesErr string
		scanned := false
		if lookErr == nil {
			var readErr error
			mods, readErr = profileFor(install, folder).Read()
			if readErr != nil {
				modsErr = readErr.Error()
			}
			if info, readErr = install.ReadGameInfo(); readErr != nil {
				infoErr = readErr.Error()
			}
			if list, found, changed, err := a.scanProfiles(install); err != nil {
				profilesErr = err.Error()
			} else {
				profiles, present, scanned = list, found, true
				if changed {
					if err := a.saveProfiles(profiles); err != nil {
						profilesErr = err.Error()
					}
				}
			}
		}

		// errors.Join keeps the ones that are not nil, and returns nil when
		// they all are.
		var message string
		if err := errors.Join(warn, saveErr, lookErr); err != nil {
			message = err.Error()
		}

		a.publish(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.looking = false
			a.lookErr = message
			// A switch that landed while the lookup ran has already put its
			// own mods and profile in place, and is newer than this read.
			if a.profileFolder == folder {
				a.mods, a.modsErr = mods, modsErr
			}
			a.gameInfo, a.gameInfoErr = info, infoErr
			if lookErr != nil {
				return
			}
			if scanned {
				a.profiles, a.profilePresent = profiles, present
				a.profilesErr = profilesErr
			} else if profilesErr != "" {
				a.profilesErr = profilesErr
			}
			a.pinned, a.install = pinned, install
		})
	}()
}

// saveSettings remembers the directory to use from now on, "" to detect the
// game through Steam, the profile in use and the appearance. It writes a file,
// so it runs off the main thread; the values are read here, in one lock,
// because the settings page and a switch can change them while this runs.
func (a *app) saveSettings(pinned string) error {
	a.mu.Lock()
	folder := a.profileFolder
	a.mu.Unlock()
	return a.saveSettingsFor(pinned, folder)
}

// saveSettingsFor is saveSettings for a profile that may not be the one in
// a.profileFolder yet, which is what a switch writes: the file has to name the
// new profile before the window does, or a frame in between would save the old
// one over it.
func (a *app) saveSettingsFor(pinned, folder string) error {
	if a.configPath == "" {
		return nil
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()

	a.mu.Lock()
	cfg := config.Config{
		GamePath:       pinned,
		ProfileFolder:  folder,
		Theme:          string(a.theme),
		VanillaPending: a.vanillaPending,
		VanillaSeen:    a.vanillaSeen,
	}
	a.mu.Unlock()
	return cfg.Save(a.configPath)
}

// rememberSession writes what the app is waiting on into the settings file, so
// that a restart in the middle of a session still puts the mods back when it
// ends. A failure is logged rather than shown: the run in progress already
// knows what it is waiting for, and only the next one would lose it.
func (a *app) rememberSession() {
	a.mu.Lock()
	pinned := a.pinned
	a.mu.Unlock()
	if err := a.saveSettings(pinned); err != nil {
		log.Printf("remembering the game session: %v", err)
	}
}

// clearVanillaSession forgets a launch without mods, which something else has
// just superseded: a mod was switched, the paths were applied, or a newer
// launch was made. It reports whether there was one to forget, and must be
// called with mu held.
func (a *app) clearVanillaSession() bool {
	if !a.vanillaPending && !a.vanillaSeen {
		return false
	}
	a.vanillaPending, a.vanillaSeen = false, false
	return true
}

// appearance is the appearance in effect.
func (a *app) appearance() mygo.ThemeSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.theme
}

// paint decides the appearance of the frame.
//
// 「跟随系统」is left to the theme the frame was built with, which comes from the
// desktop and follows it. The two explicit ones are put on the frame here
// instead: on Linux the theme the frame is built with comes from the desktop
// whatever the module was told, so a request for light on a desktop set to dark
// would draw dark.
func (a *app) paint(c *ui.Context) {
	switch a.appearance() {
	case mygo.ThemeLight:
		c.SetTheme(ui.LightTheme())
	case mygo.ThemeDark:
		c.SetTheme(ui.DarkTheme())
	}
}

// applyTheme makes a running app use source, and records it for the settings
// page to show. It is the main thread's, and writes nothing.
//
// Telling Mygo is left until there is a window, because the call it makes needs
// a running event loop to take it, and there is none before the app is up or in
// a test. The appearance the app opens in is set in main, before it runs.
func (a *app) applyTheme(source mygo.ThemeSource) {
	a.mu.Lock()
	a.theme = source
	a.mu.Unlock()

	if a.win == nil {
		return
	}
	if tellsModule() {
		mygo.Theme.SetSource(source)
	}
	a.invalidate()
}

// tellsModule says whether Mygo is told the appearance as well as the frame
// being drawn in it. On macOS and Windows that is what draws the window the
// desktop puts around the content in the chosen appearance, so the frame and
// its title bar agree. Linux is left out, and the cost is the window's own
// background: it keeps the desktop's appearance rather than the chosen one,
// which shows in the band around the content while the window is resized.
//
// What leaving it out buys is 「跟随系统」. Handing Linux a source writes
// gtk-application-prefer-dark-theme, and for anything but an explicit light or
// dark the backend reads that property back before it reaches the desktop
// portal. The GTK 3 this backend uses has no portal support of its own -- the
// property is the application's -- so nothing ever clears what was written:
// 「跟随系统」on a dark desktop would answer dark for the rest of the session,
// however the desktop changed. Left alone the property is false, the portal
// answers, and the desktop is followed, which is what 「跟随系统」wants.
//
// mygo 0.6.0 answers an explicit light or dark with the source it was given,
// before reading the property, so that half would work now; it is the other half
// that keeps Linux out.
//
// Handing a source over is also how the desktop's notice about the appearance
// comes back, and the desktop sends it whether or not the value differs, so an
// app that answered one with a source would answer its own answer for as long
// as it ran. Nothing here answers it: the notice is Mygo's to act on, and it
// redraws by itself.
func tellsModule() bool { return runtime.GOOS != "linux" }

// setTheme is applyTheme for an appearance the user chose, which is also
// remembered. It is the main thread's, called by the control that chose it.
func (a *app) setTheme(source mygo.ThemeSource) {
	a.applyTheme(source)

	a.mu.Lock()
	pinned := a.pinned
	a.mu.Unlock()
	if err := a.saveSettings(pinned); err != nil {
		a.fail(err.Error())
	}
}

// fail shows a message in place of the game directory.
func (a *app) fail(message string) {
	a.mu.Lock()
	a.lookErr = message
	a.mu.Unlock()
	a.invalidate()
}

// publish applies fn where the view can see it: on the main thread once the
// window exists, and right away before that, which is how tests run.
func (a *app) publish(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

// invalidate asks for a redraw, which is safe from any goroutine. Before the
// window exists there is nothing to redraw.
func (a *app) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

func main() {
	a := newApp()
	var cfg config.Config
	var cfgErr error
	if path, err := config.Path(); err != nil {
		log.Printf("settings will not be kept: %v", err)
	} else {
		// The settings of a build from before the rename are carried over once,
		// so that the game directory it found is still known.
		if err := config.Adopt(path); err != nil {
			log.Printf("settings from before the rename were not carried over: %v", err)
		}
		a.configPath = path
		cfg, cfgErr = config.Load(path)
	}
	// Pictures are kept between runs under the platform's cache directory,
	// where the system and the user treat them as disposable. A machine with no
	// such directory runs with the memory cache alone.
	if dir, err := imagecache.Path(); err != nil {
		log.Printf("pictures will not be kept between runs: %v", err)
	} else if cache, err := imagecache.Open(dir, imagecache.DefaultBudget); err != nil {
		log.Printf("pictures will not be kept between runs: %v", err)
	} else {
		a.useImageCache(cache)
	}
	// The appearance has to be known before the app runs, so the first frame is
	// drawn in it rather than in the system's and then changing: paint reads what
	// was chosen, and Run applies what the module was told beforehand.
	a.theme = themeSource(cfg.Theme)
	if tellsModule() {
		mygo.Theme.SetSource(a.theme)
	}

	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "MyLock",
			Width:     1280,
			Height:    800,
			MinWidth:  880,
			MinHeight: 560,
			// Opens where the user left it last time.
			StateKey: "main",
			// The window shows the interface Mygo draws, not a web page.
			Content: ui.View(a.view),
		})
		a.start(cfg, cfgErr)
		// The game's process list is followed from here on: it decides
		// whether the mods can be moved, and when a session without them has
		// ended.
		go a.watchGame()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
