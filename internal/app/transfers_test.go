package app

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/download"
	"github.com/BabyLy233/mylock/internal/gb"
)

// An install started from the store keeps running when the user goes back to
// the list of installed mods, which does not change until it is done. The
// bottom of the sidebar is where it stays in sight there.
func TestInstallProgressStaysVisibleInTheSidebar(t *testing.T) {
	a := newApp()
	a.store.started = true
	task := a.transfers.start(transferInstall, "安装 Bikinidicta", "554012",
		[]gb.File{{ID: 1, Name: "bikinidicta.zip", Size: 10 << 20}})
	task.beginFile(0)
	// Two reports a second apart, so that the rate beside the bar is the one
	// the download is actually going at rather than whatever the test clock
	// last saw.
	task.report(0, 0, download.Progress{Done: 1 << 20, Total: 10 << 20})
	task.mu.Lock()
	task.lastSample, task.lastDone = time.Now().Add(-time.Second), 0
	task.mu.Unlock()
	task.report(0, 0, download.Progress{Done: 5 << 20, Total: 10 << 20})

	a.router.Push("/mods")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{
		"安装 Bikinidicta",
		"50% · 5.0 MB / 10.0 MB",
		"5.0 MB/s",
		"查看",
	} {
		if !tt.HasText(want) {
			t.Errorf("the mods page does not show the install in flight with %q: %q",
				want, tt.Texts())
		}
	}

	// Once the transfer is over it leaves the sidebar; the way into the window
	// stays.
	task.finish(nil)
	tt = ui.NewTester(a.view, 1280, 800)
	if tt.HasText("安装 Bikinidicta") {
		t.Errorf("the sidebar still shows the install after it finished: %q", tt.Texts())
	}
	if !tt.HasText("下载管理") {
		t.Errorf("the sidebar lost the way into the download window: %q", tt.Texts())
	}
}

// The way into the download window is in the sidebar whether or not there is
// anything to watch: the window is also where a finished or failed transfer is
// looked at after the fact.
func TestTheDownloadManagerEntryIsAlwaysInTheSidebar(t *testing.T) {
	a := newApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("下载管理") {
		t.Fatalf("the sidebar does not offer the download manager: %q", tt.Texts())
	}
	if err := tt.Click("打开下载管理"); err != nil {
		t.Fatalf("the entry cannot be clicked: %v", err)
	}

	window := ui.NewTester(a.transfersWindow, 640, 620)
	if !window.HasText("没有下载任务。") {
		t.Errorf("the empty download window does not say it is empty: %q", window.Texts())
	}
}

// An update downloads in the mods page's own panel, but the user can leave that
// page while it runs; the sidebar follows it to every page as well.
func TestUpdateProgressStaysVisibleInTheSidebar(t *testing.T) {
	a := newApp()
	task := a.transfers.start(transferUpdate, "更新 Bikinidicta", "554012",
		[]gb.File{{ID: 1, Name: "a.zip", Size: 4 << 20}})
	task.beginFile(0)
	task.report(0, 0, download.Progress{Done: 1 << 20, Total: 4 << 20})
	t.Cleanup(task.Cancel)

	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{
		"更新 Bikinidicta",
		"25% · 1.0 MB / 4.0 MB",
	} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show the update in flight with %q: %q",
				want, tt.Texts())
		}
	}
}

// A download that failed is something the user may have to act on, so it stays
// in the sidebar until it is cleared from the window rather than vanishing.
func TestAFailedDownloadStaysInTheSidebarUntilCleared(t *testing.T) {
	a := newApp()
	a.store.started = true
	task := a.transfers.start(transferInstall, "安装 Bikinidicta", "554012", nil)
	task.finish(errors.New("文件服务器没有响应"))

	a.router.Push("/mods")
	tt := ui.NewTester(a.view, 1280, 800)
	for _, want := range []string{"下载失败 · 1", "失败：文件服务器没有响应"} {
		if !tt.HasText(want) {
			t.Errorf("the sidebar does not show the failed download with %q: %q",
				want, tt.Texts())
		}
	}

	a.transfers.clearFinished()
	tt = ui.NewTester(a.view, 1280, 800)
	if tt.HasText("文件服务器没有响应") {
		t.Errorf("the sidebar still shows a failure that was cleared: %q", tt.Texts())
	}
}

// A download whose size the server did not give shows how much has come
// without a share of a whole that is not known.
func TestProgressWithoutATotalShowsOnlyWhatHasCome(t *testing.T) {
	a := newApp()
	a.store.started = true
	task := a.transfers.start(transferInstall, "安装 Bikinidicta", "554012",
		[]gb.File{{ID: 1, Name: "a.zip"}})
	task.beginFile(0)
	task.report(0, 0, download.Progress{Done: 1536})
	t.Cleanup(task.Cancel)

	a.router.Push("/mods")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("1.5 KB") {
		t.Errorf("the sidebar does not show how much has come: %q", tt.Texts())
	}
	if tt.HasText("%") {
		t.Errorf("the sidebar shows a share of a total it does not have: %q", tt.Texts())
	}
}

// The window shows every file of a transfer, with its progress, and the
// controls pause, resume and cancel it.
func TestTransferWindowShowsEveryFileAndControls(t *testing.T) {
	a := newApp()
	task := a.transfers.start(transferInstall, "安装 Bikinidicta", "554012",
		[]gb.File{
			{ID: 1, Name: "part-one.zip", Size: 2 << 20},
			{ID: 2, Name: "part-two.zip", Size: 4 << 20},
		})
	task.beginFile(0)
	task.report(0, 0, download.Progress{Done: 1 << 20, Total: 2 << 20})

	tt := ui.NewTester(a.transfersWindow, 640, 620)
	for _, want := range []string{
		"下载管理", "安装 Bikinidicta",
		"part-one.zip", "part-two.zip",
		"50% · 1.0 MB / 2.0 MB",
		"等待中 · 4.0 MB",
		"暂停", "取消",
	} {
		if !tt.HasText(want) {
			t.Fatalf("the download window does not show %q: %q", want, tt.Texts())
		}
	}

	if err := tt.Click("暂停下载 安装 Bikinidicta"); err != nil {
		t.Fatalf("clicking 暂停: %v", err)
	}
	tt.Frame()
	if got := task.view().Status; got != transferPaused {
		t.Fatalf("the transfer is %v after 暂停, want paused", got)
	}
	if !tt.HasText("已暂停") || !tt.HasText("继续") {
		t.Errorf("the window does not show the paused transfer: %q", tt.Texts())
	}

	if err := tt.Click("继续下载 安装 Bikinidicta"); err != nil {
		t.Fatalf("clicking 继续: %v", err)
	}
	tt.Frame()
	if got := task.view().Status; got != transferRunning {
		t.Fatalf("the transfer is %v after 继续, want running", got)
	}

	if err := tt.Click("取消 安装 Bikinidicta"); err != nil {
		t.Fatalf("clicking 取消: %v", err)
	}
	tt.Frame()
	if got := task.view().Status; got != transferCanceled {
		t.Fatalf("the transfer is %v after 取消, want canceled", got)
	}
	if !tt.HasText("已取消") {
		t.Errorf("the window does not show the canceled transfer: %q", tt.Texts())
	}
}

// The mod's own page shows the download started from it, with the same
// controls, so the user need not open the window to stop or pause it.
func TestStoreDetailShowsTheModsOwnDownload(t *testing.T) {
	a := newApp()
	a.store.started = true
	a.store.mu.Lock()
	a.store.items = []gb.Submission{mod("Yamato redesign")}
	a.store.chosen = 0
	a.store.profile = gb.Profile{Files: []gb.File{{ID: 1, Name: "a.zip", Size: 10 << 20}}}
	a.store.mu.Unlock()

	task := a.transfers.start(transferInstall, "安装 Yamato redesign", "599927",
		[]gb.File{{ID: 1, Name: "a.zip", Size: 10 << 20}})
	task.beginFile(0)
	task.report(0, 0, download.Progress{Done: 5 << 20, Total: 10 << 20})
	t.Cleanup(task.Cancel)

	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)
	for _, want := range []string{
		"安装 Yamato redesign",
		"50% · 5.0 MB / 10.0 MB",
		"暂停", "取消",
	} {
		if !tt.HasText(want) {
			t.Fatalf("the mod's page does not show its download with %q: %q",
				want, tt.Texts())
		}
	}

	if err := tt.Click("暂停下载 安装 Yamato redesign"); err != nil {
		t.Fatalf("clicking 暂停: %v", err)
	}
	tt.Frame()
	if got := task.view().Status; got != transferPaused {
		t.Errorf("the transfer is %v after 暂停, want paused", got)
	}
}

// A download and an update are two transfers: starting one does not stop the
// other, which is what makes a second mod downloadable while the first is on
// its way.
func TestTwoModsTransferAtOnce(t *testing.T) {
	a := newApp()
	first := a.transfers.start(transferInstall, "安装 First", "1", nil)
	second := a.transfers.start(transferInstall, "安装 Second", "2", nil)
	t.Cleanup(first.Cancel)
	t.Cleanup(second.Cancel)

	if got := len(a.transfers.active()); got != 2 {
		t.Fatalf("there are %d transfers in flight, want 2", got)
	}
	if !a.transfers.activeFor("1") || !a.transfers.activeFor("2") {
		t.Errorf("both mods are not downloading: %v", a.transfers.activeByMod())
	}

	first.Pause()
	if got := second.view().Status; got != transferRunning {
		t.Errorf("pausing one transfer made another %v", got)
	}
	if !a.transfers.activeFor("2") {
		t.Error("pausing one transfer stopped another")
	}
}

// Trying a mod again replaces what the last attempt left, so the window shows
// one transfer for it rather than a failure beside the new one.
func TestANewTransferReplacesTheLastOneForTheMod(t *testing.T) {
	a := newApp()
	old := a.transfers.start(transferInstall, "安装 First", "1", nil)
	old.finish(errors.New("文件服务器没有响应"))

	fresh := a.transfers.start(transferInstall, "安装 First", "1", nil)
	t.Cleanup(fresh.Cancel)

	views := a.transfers.snapshot()
	if len(views) != 1 || views[0].ID != fresh.id {
		t.Fatalf("the window shows %+v, want only the new transfer", views)
	}
}

// The speed beside the bar is written the way a person reads a rate.
func TestHumanRate(t *testing.T) {
	for _, tt := range []struct {
		bytes float64
		want  string
	}{
		{0, "0 B/s"},
		{512, "512 B/s"},
		{200 * 1024, "200 KB/s"},
		{1.5 * 1024 * 1024, "1.5 MB/s"},
		{12 * 1024 * 1024, "12 MB/s"},
	} {
		if got := humanRate(tt.bytes); got != tt.want {
			t.Errorf("humanRate(%v) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
