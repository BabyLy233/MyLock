// Command deadlock-mod-manager-mygo manages mods for Valve's Deadlock. Its
// window is Mygo's native UI: the interface is Go, drawn on the GPU, with no
// web page and no webview.
package main

import (
	"errors"
	"log"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/config"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/game"
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

	// mu guards what the lookups publish for the view to read.
	mu       sync.Mutex
	looking  bool
	browsing bool
	pinned   string
	install  *game.Install
	lookErr  string
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
}

func newApp() *app {
	return &app{router: ui.NewRouter("/mods")}
}

func (a *app) view(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)
		a.router.View(c, func(r *ui.Route) {
			switch {
			case r.Match("/mods"):
				r.Title("已安装模组")
				a.modsPage(c)
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
		ui.SidebarItem(c, "/settings", nil, "设置")
	}).Width(220).OnChange(func() {
		a.router.Push(page)
	})
}

// modsPage lists the mods installed in the game. Reading them is the next
// step; for now it says where they will come from.
func (a *app) modsPage(c *ui.Context) {
	t := c.Theme()
	s := a.state()

	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Text(c, "已安装模组").FontSize(22).Bold()
		if s.install == nil {
			ui.Text(c, "还没有找到游戏目录，先到设置里指定。").TextColor(t.TextMuted)
			return
		}
		ui.Text(c, s.install.AddonsDir()).Selectable().TextColor(t.TextMuted)
		ui.Text(c, "读取模组列表是下一步。").TextColor(t.TextMuted)
	})
}

func (a *app) settingsPage(c *ui.Context) {
	s := a.state()
	ui.Column(c).Fill().Padding(24).Gap(20).Children(func() {
		ui.Text(c, "设置").FontSize(22).Bold()
		a.installSection(c, s)
		a.pathSection(c, s)
	})
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

// state reads what the last lookup found, in the order the settings page shows
// it.
func (a *app) state() state {
	a.mu.Lock()
	defer a.mu.Unlock()
	return state{
		looking:  a.looking,
		browsing: a.browsing,
		pinned:   a.pinned,
		install:  a.install,
		lookErr:  a.lookErr,
	}
}

// start loads the settings and looks for the game the way they say to.
func (a *app) start() {
	cfg, err := config.Load(a.configPath)
	if err != nil {
		// The directory the user chose last time is not in effect, so the app
		// detects the game instead, and says why.
		a.look("", false, err)
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
	a.mu.Unlock()
	a.invalidate()

	go func() {
		install, lookErr := game.Resolve(pinned)

		var saveErr error
		if remember && (lookErr == nil || pinned == "") {
			saveErr = a.saveSettings(pinned)
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
			if lookErr != nil {
				return
			}
			a.pinned, a.install = pinned, install
		})
	}()
}

// saveSettings remembers the directory to use from now on, "" to detect the
// game through Steam. It writes a file, so it runs off the main thread.
func (a *app) saveSettings(pinned string) error {
	if a.configPath == "" {
		return nil
	}
	return config.Config{GamePath: pinned}.Save(a.configPath)
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
	if path, err := config.Path(); err != nil {
		log.Printf("settings will not be kept: %v", err)
	} else {
		a.configPath = path
	}

	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Deadlock Mod Manager",
			Width:     1280,
			Height:    800,
			MinWidth:  880,
			MinHeight: 560,
			// Opens where the user left it last time.
			StateKey: "main",
			// The window shows the interface Mygo draws, not a web page.
			Content: ui.View(a.view),
		})
		a.start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
