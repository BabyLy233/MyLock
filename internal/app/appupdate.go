package app

import (
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// updaterHooks is what the settings page uses of the update plugin. The
// plugin's package panics when it was not registered, which is the case in
// tests and for a window that was never started through Run, so the page
// reaches it through this value: a zero one means no updater, and the page
// says why the app cannot update itself rather than failing.
type updaterHooks struct {
	// Ready is whether this build can replace itself. It is probed once,
	// before the window opens: Enabled writes and removes a probe file in the
	// install directory, which is not work for every frame the settings page
	// is built.
	Ready                 bool
	Check                 func()
	AutomaticChecks       func() bool
	SetAutomaticChecks    func(bool)
	AutomaticDownloads    func() bool
	SetAutomaticDownloads func(bool)
	LastCheck             func() time.Time
	OnChange              func(func()) (off func())
}

// canUpdate reports whether this build can replace itself.
func (h updaterHooks) canUpdate() bool { return h.Ready }

// appUpdateSection is the settings page's "应用更新": whether the build can
// update itself, the preferences the update window keeps, when it last
// checked, and a way to ask now.
func (a *app) appUpdateSection(c *ui.Context) {
	t := c.Theme()
	h := a.appUpdates
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "应用更新").Bold()
		if !h.canUpdate() {
			ui.Text(c, "这个版本不能自行更新。").TextColor(t.TextMuted)
			ui.Text(c, updateUnavailableReason()).FontSize(12).TextColor(t.TextMuted)
			return
		}
		if h.AutomaticChecks != nil {
			checks := h.AutomaticChecks()
			ui.Checkbox(c, &checks, "自动检查更新").OnChange(func() {
				if h.SetAutomaticChecks != nil {
					h.SetAutomaticChecks(checks)
				}
			})
		}
		if h.AutomaticDownloads != nil {
			downloads := h.AutomaticDownloads()
			ui.Checkbox(c, &downloads, "自动下载更新").OnChange(func() {
				if h.SetAutomaticDownloads != nil {
					h.SetAutomaticDownloads(downloads)
				}
			})
		}
		if h.LastCheck != nil {
			if last := h.LastCheck(); last.IsZero() {
				ui.Text(c, "还没有检查过更新。").FontSize(12).TextColor(t.TextMuted)
			} else {
				ui.Text(c, "上次检查："+last.Local().Format("2006-01-02 15:04")).
					FontSize(12).TextColor(t.TextMuted)
			}
		}
		ui.Row(c).Children(func() {
			ui.Button(c, "检查更新").OnClick(func() {
				if h.Check != nil {
					h.Check()
				}
			})
		})
		ui.Text(c, "更新包带签名，下载后会先校验再替换，重启应用后生效。").
			FontSize(12).TextColor(t.TextMuted)
	})
}

// updateUnavailableReason says why this copy cannot update itself, in the
// terms of the platform it runs on: where an update may write is the part
// that differs, and a development build has no update source at all.
func updateUnavailableReason() string {
	if mygo.IsDev() {
		return "开发构建不含更新源，只有用 mygo build 打出的正式安装包才能更新。"
	}
	switch runtime.GOOS {
	case "linux":
		return "Linux 上只有 install.sh 或解压到用户可写目录的安装能自更新；" +
			".deb 由包管理器更新，AppImage 需要重新下载。"
	case "windows":
		return "Windows 上只有安装到用户目录（%LOCALAPPDATA%\\Programs）的版本能自更新；" +
			"放在 Program Files 等无写权限位置的版本请手动下载新版本。"
	case "darwin":
		return "macOS 上只有用户可写位置（如 ~/Applications）的 .app 能自更新；" +
			"装在 /Applications 的版本需要管理员权限，或请手动下载新版本。"
	default:
		return "请用安装这个版本时的同一种方式更新。"
	}
}
