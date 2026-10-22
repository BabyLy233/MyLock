package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A build that cannot replace itself says so, and does not offer a button that
// would only fail.
func TestSettingsSaysWhenTheAppCannotUpdateItself(t *testing.T) {
	a := newApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 1000)

	for _, want := range []string{
		"应用更新",
		"这个版本不能自行更新。",
		// Tests run in a development build, which has no update source.
		"开发构建",
	} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("检查更新") {
		t.Errorf("the settings page offers an update it cannot install: %q", tt.Texts())
	}
}

// A build that can update itself shows the plugin's preferences and last check,
// and passes the button and the checkboxes to the plugin.
func TestSettingsDrivesTheAppUpdater(t *testing.T) {
	a := newApp()
	checks, auto, downloads := 0, true, false
	last := time.Date(2026, 10, 22, 15, 4, 0, 0, time.Local)
	a.appUpdates = updaterHooks{
		Ready:                 true,
		Check:                 func() { checks++ },
		AutomaticChecks:       func() bool { return auto },
		SetAutomaticChecks:    func(on bool) { auto = on },
		AutomaticDownloads:    func() bool { return downloads },
		SetAutomaticDownloads: func(on bool) { downloads = on },
		LastCheck:             func() time.Time { return last },
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 1000)

	for _, want := range []string{
		"应用更新",
		"自动检查更新",
		"自动下载更新",
		"上次检查：2026-10-22 15:04",
		"检查更新",
	} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("这个版本不能自行更新。") {
		t.Errorf("the settings page says it cannot update although it can: %q", tt.Texts())
	}

	if err := tt.Click("检查更新"); err != nil {
		t.Fatalf("clicking 检查更新: %v", err)
	}
	if checks != 1 {
		t.Errorf("the check ran %d times, want once", checks)
	}

	if err := tt.Click("自动检查更新"); err != nil {
		t.Fatalf("clicking 自动检查更新: %v", err)
	}
	if auto {
		t.Error("the automatic check is still on after clicking it off")
	}
	if err := tt.Click("自动下载更新"); err != nil {
		t.Fatalf("clicking 自动下载更新: %v", err)
	}
	if !downloads {
		t.Error("the automatic download is still off after clicking it on")
	}
}
