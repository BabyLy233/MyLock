// Command deadlock-mod-manager-mygo manages mods for Valve's Deadlock. Its
// window is Mygo's native UI: the interface is Go, drawn on the GPU, with no
// web page and no webview.
package main

import (
	"log"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/game"
)

// app is the state the window shows. The view builds the interface from it
// on the main thread, whenever the window needs a frame.
type app struct {
	router *ui.Router
	win    *mygo.Window

	// mu guards what the detection goroutine publishes for the view to read.
	mu        sync.Mutex
	detecting bool
	install   *game.Install
	detectErr string
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
	install, _, _ := a.detection()

	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Text(c, "已安装模组").FontSize(22).Bold()
		if install == nil {
			ui.Text(c, "还没有找到游戏目录，先到设置里检测。").TextColor(t.TextMuted)
			return
		}
		ui.Text(c, install.AddonsDir()).Selectable().TextColor(t.TextMuted)
		ui.Text(c, "读取模组列表是下一步。").TextColor(t.TextMuted)
	})
}

func (a *app) settingsPage(c *ui.Context) {
	t := c.Theme()
	install, detecting, detectErr := a.detection()

	ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		ui.Text(c, "设置").FontSize(22).Bold()

		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "游戏目录").Bold()
			switch {
			case detecting:
				ui.Text(c, "正在检测…").TextColor(t.TextMuted)
			case install != nil:
				field(c, "游戏", install.GamePath)
				field(c, "Steam 库", install.LibraryPath)
				field(c, "Steam", install.SteamRoot)
				if install.BuildID != "" {
					field(c, "构建号", install.BuildID)
				}
			case detectErr != "":
				ui.Text(c, detectErr).TextColor(t.Danger)
			default:
				ui.Text(c, "尚未检测").TextColor(t.TextMuted)
			}
		})

		if ui.Button(c, "重新检测").Disabled(detecting).Clicked() {
			a.detect()
		}
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

// detection reads what the last detection found, in the order the settings
// page shows it.
func (a *app) detection() (install *game.Install, detecting bool, detectErr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.install, a.detecting, a.detectErr
}

// detect looks for the game off the main thread, which reads the disk, and
// publishes what it found. The window shows a detection in progress until
// it does.
func (a *app) detect() {
	a.mu.Lock()
	a.detecting, a.detectErr = true, ""
	a.mu.Unlock()
	if a.win != nil {
		a.win.Invalidate()
	}

	go func() {
		install, err := game.Detect()
		message := ""
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.detecting = false
			a.install, a.detectErr = install, message
		})
	}()
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

func main() {
	a := newApp()
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
		a.detect()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
