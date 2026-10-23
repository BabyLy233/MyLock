package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/download"
	"github.com/BabyLy233/mylock/internal/gb"
)

// transferKind says what a transfer is fetching for. The two are shown apart on
// the pages they belong to, but they share one queue, one set of controls and
// one window.
type transferKind int

const (
	// transferInstall is a mod being installed from the store.
	transferInstall transferKind = iota
	// transferUpdate is an installed mod's files being replaced.
	transferUpdate
)

// transferStatus is where a transfer stands.
type transferStatus int

const (
	// transferRunning is a transfer that is fetching, unpacking or writing.
	transferRunning transferStatus = iota
	// transferPaused is one that was stopped by the user with the file it was
	// fetching left on disk, to be picked up from there.
	transferPaused
	transferDone
	transferFailed
	transferCanceled
)

// speedWindow is how often the speed is sampled. A shorter window makes the
// number jump with the network's own bursts; a longer one keeps showing a speed
// the download no longer has.
const speedWindow = 400 * time.Millisecond

// errTransferCanceled is what a transfer returns when the user cancels it. It
// is not a failure: the page says the download was canceled rather than showing
// an error.
var errTransferCanceled = errors.New("下载已取消")

// transferFile is one archive of a transfer, with how much of it has come.
type transferFile struct {
	Name  string
	Done  int64
	Total int64
}

// transfer is one install or update in flight: what it is doing, how far it has
// got, and how to stop it.
//
// A transfer runs on a goroutine of its own. Everything the view reads is copied
// out under mu, so the view never waits for a download to take its lock back.
type transfer struct {
	m  *transferManager
	id int64

	// Fixed when the transfer starts.
	kind  transferKind
	label string
	// mod is the installed id or the store's slug, which is how a page finds
	// its own transfer again.
	mod string

	// ctx is the transfer's own lifetime. Canceling it stops the download even
	// while it is paused, which is what the cancel button does.
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex
	// status is what the transfer is doing now, and phase is the word the
	// window shows while it is running: fetching, unpacking, writing.
	status transferStatus
	phase  string
	err    string
	// downloading says the transfer is fetching a file rather than unpacking
	// or writing one, which is the only part that can be paused.
	downloading bool
	files       []transferFile
	current     int
	done        int64
	total       int64
	speed       float64
	lastDone    int64
	lastSample  time.Time
	// attemptCancel stops the fetch that is running, so that pausing does not
	// have to wait for the connection to send something.
	attemptCancel context.CancelFunc
	// resume is closed when the user resumes, waking the goroutine waiting on
	// it. It is nil while the transfer is not paused.
	resume chan struct{}
}

// transferView is a transfer as one frame reads it.
type transferView struct {
	ID    int64
	Kind  transferKind
	Label string
	Mod   string

	Status      transferStatus
	StatusText  string
	Phase       string
	Err         string
	Downloading bool

	Done  int64
	Total int64
	Speed float64

	Files   []transferFileView
	Current int

	// Active is a transfer that is still going, paused or not. CanPause says
	// the pause button applies to what it is doing now.
	Active   bool
	CanPause bool
}

// transferFileView is one file of a transfer as one frame reads it.
type transferFileView struct {
	Name  string
	Done  int64
	Total int64
}

// transferManager keeps every transfer the app has started, and the window that
// shows them. Transfers are started from the store and the mods page and run
// together, so a mod can be fetched while another is already on its way.
//
// The manager's lock is taken before a transfer's, never the other way round.
type transferManager struct {
	a *app

	mu    sync.Mutex
	tasks []*transfer
	next  int64
	// win is the window showing the transfers, nil when it is closed. Only the
	// main thread opens or closes it; changed and window read it under mu.
	win *mygo.Window
}

func newTransferManager(a *app) *transferManager {
	return &transferManager{a: a}
}

// start begins a transfer and hands it back to the goroutine that runs it. files
// may be nil when what will be fetched is only known after a request, as an
// update's are.
func (m *transferManager) start(kind transferKind, label, mod string, files []gb.File) *transfer {
	ctx, cancel := context.WithCancel(context.Background())
	t := &transfer{
		m:      m,
		kind:   kind,
		label:  label,
		mod:    mod,
		ctx:    ctx,
		cancel: cancel,
		status: transferRunning,
	}
	t.setFiles(files)

	m.mu.Lock()
	m.next++
	t.id = m.next
	// A new attempt at a mod replaces what the last one left: a failure that
	// was kept for the user to see is no longer what the mod is doing.
	m.tasks = slices.DeleteFunc(m.tasks, func(old *transfer) bool {
		return old.mod == mod && !old.active()
	})
	m.tasks = append(m.tasks, t)
	m.mu.Unlock()
	m.changed()
	return t
}

// changed asks both windows for a frame. It is safe from any goroutine.
func (m *transferManager) changed() {
	m.a.invalidate()
}

// window is the window the transfers are shown in, nil when there is none.
func (m *transferManager) window() *mygo.Window {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.win
}

// snapshot is every transfer, oldest first, as the view reads it.
func (m *transferManager) snapshot() []transferView {
	m.mu.Lock()
	tasks := slices.Clone(m.tasks)
	m.mu.Unlock()

	views := make([]transferView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, t.view())
	}
	return views
}

// active is the transfers that are still going.
func (m *transferManager) active() []transferView {
	m.mu.Lock()
	tasks := slices.Clone(m.tasks)
	m.mu.Unlock()

	views := make([]transferView, 0, len(tasks))
	for _, t := range tasks {
		// Checked before the view is copied: a session's finished transfers
		// are not worth copying on every frame a download causes.
		if !t.active() {
			continue
		}
		if view := t.view(); view.Active {
			views = append(views, view)
		}
	}
	return views
}

// panelViews is what the sidebar shows: the transfers still going, and the
// ones that failed, which stay until they are cleared from the window.
func (m *transferManager) panelViews() []transferView {
	m.mu.Lock()
	tasks := slices.Clone(m.tasks)
	m.mu.Unlock()

	views := make([]transferView, 0, len(tasks))
	for _, t := range tasks {
		if !t.keepsInPanel() {
			continue
		}
		views = append(views, t.view())
	}
	return views
}

// activeFor reports whether a transfer for the named mod is still going, which
// is what stops a second install of the same mod.
func (m *transferManager) activeFor(mod string) bool {
	_, ok := m.activeByMod()[mod]
	return ok
}

// activeByMod is the transfers still going, by the mod they are for. One mod is
// one transfer at a time, so a map is enough.
func (m *transferManager) activeByMod() map[string]transferView {
	byMod := make(map[string]transferView)
	for _, v := range m.active() {
		byMod[v.Mod] = v
	}
	return byMod
}

// activeOfKind counts the transfers of one kind that are still going.
func (m *transferManager) activeOfKind(kind transferKind) int {
	n := 0
	for _, v := range m.active() {
		if v.Kind == kind {
			n++
		}
	}
	return n
}

// latestFor is the newest transfer for a mod, finished or not, which is what
// the mod's own page shows.
func (m *transferManager) latestFor(mod string) (transferView, bool) {
	m.mu.Lock()
	tasks := slices.Clone(m.tasks)
	m.mu.Unlock()

	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i].mod == mod {
			return tasks[i].view(), true
		}
	}
	return transferView{}, false
}

// byID finds a transfer the view has drawn.
func (m *transferManager) byID(id int64) *transfer {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.id == id {
			return t
		}
	}
	return nil
}

// clearFinished drops the transfers that are over, so the window holds only
// what is still going.
func (m *transferManager) clearFinished() {
	m.mu.Lock()
	m.tasks = slices.DeleteFunc(m.tasks, func(t *transfer) bool {
		return !t.active()
	})
	m.mu.Unlock()
	m.changed()
}

// setFiles replaces what the transfer is fetching, used when an update learns
// its files from the catalogue's reply.
func (t *transfer) setFiles(files []gb.File) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.files = make([]transferFile, len(files))
	total := int64(0)
	known := len(files) > 0
	for i, file := range files {
		size := file.Size
		if size <= 0 {
			known = false
		}
		t.files[i] = transferFile{Name: file.Name, Total: size}
		total += size
	}
	// A total short of what is left to fetch would fill the bar before the
	// download did, so an unknown size leaves it without one.
	if !known {
		total = 0
	}
	t.total = total
}

// beginFile makes one file the one being fetched.
func (t *transfer) beginFile(i int) {
	t.mu.Lock()
	t.current = i
	t.downloading = true
	t.phase = ""
	t.speed = 0
	t.lastSample, t.lastDone = time.Time{}, 0
	t.mu.Unlock()
	t.m.changed()
}

// setPhase says what the transfer is doing that cannot be paused.
func (t *transfer) setPhase(phase string) {
	t.mu.Lock()
	t.phase = phase
	t.downloading = false
	t.speed = 0
	t.mu.Unlock()
	t.m.changed()
}

// report is the download's progress callback. base is how much of the transfer
// came before this file, so that the bar counts the whole install rather than
// starting over on each one.
func (t *transfer) report(file int, base int64, p download.Progress) {
	now := time.Now()

	t.mu.Lock()
	if file >= 0 && file < len(t.files) {
		t.files[file].Done = p.Done
		if p.Total > 0 {
			t.files[file].Total = p.Total
		}
	}
	t.done = base + p.Done
	if t.lastSample.IsZero() {
		t.lastSample, t.lastDone = now, t.done
	} else if elapsed := now.Sub(t.lastSample); elapsed >= speedWindow {
		instant := float64(t.done-t.lastDone) / elapsed.Seconds()
		if instant < 0 {
			instant = 0
		}
		// Smoothed, because a connection delivers in bursts and the number
		// beside the bar is read as a rate, not as the last burst.
		if t.speed == 0 {
			t.speed = instant
		} else {
			t.speed = 0.7*t.speed + 0.3*instant
		}
		t.lastSample, t.lastDone = now, t.done
	}
	t.mu.Unlock()
	t.m.changed()
}

// fileWritten records a file that arrived whole, so that its own line says so
// before the next one starts.
func (t *transfer) fileWritten(i int, size int64) {
	t.mu.Lock()
	if i >= 0 && i < len(t.files) {
		t.files[i].Done = size
		if t.files[i].Total <= 0 {
			t.files[i].Total = size
		}
	}
	t.mu.Unlock()
	t.m.changed()
}

// Pause stops the fetch that is running. What has come stays in the partial
// file, and Resume picks it up from there. A transfer that is unpacking or
// writing is not interrupted: those steps are short, and half of one is worse
// than waiting for it.
func (t *transfer) Pause() {
	t.mu.Lock()
	if t.status != transferRunning || !t.downloading {
		t.mu.Unlock()
		return
	}
	t.status = transferPaused
	t.speed = 0
	if t.resume == nil {
		t.resume = make(chan struct{})
	}
	cancel := t.attemptCancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	t.m.changed()
}

// Resume starts the paused fetch again, from the partial file.
func (t *transfer) Resume() {
	t.mu.Lock()
	if t.status != transferPaused {
		t.mu.Unlock()
		return
	}
	t.status = transferRunning
	t.lastSample, t.lastDone = time.Time{}, 0
	resume := t.resume
	t.resume = nil
	t.mu.Unlock()

	if resume != nil {
		close(resume)
	}
	t.m.changed()
}

// Cancel stops the transfer for good. A paused one is woken so that its
// goroutine can clean up after itself.
func (t *transfer) Cancel() {
	t.mu.Lock()
	if !t.activeLocked() {
		t.mu.Unlock()
		return
	}
	t.status = transferCanceled
	t.speed = 0
	resume := t.resume
	t.resume = nil
	t.mu.Unlock()

	t.cancel()
	if resume != nil {
		close(resume)
	}
	t.m.changed()
}

// finish records what the transfer came to and wakes anyone waiting on it.
func (t *transfer) finish(err error) {
	t.mu.Lock()
	switch {
	case errors.Is(err, errTransferCanceled):
		t.status = transferCanceled
	case err != nil:
		t.status = transferFailed
		t.err = err.Error()
		t.speed = 0
	default:
		t.status = transferDone
		t.speed = 0
		if t.total > 0 {
			t.done = t.total
		}
	}
	t.phase = ""
	t.downloading = false
	resume := t.resume
	t.resume = nil
	t.mu.Unlock()

	if resume != nil {
		close(resume)
	}
	t.m.changed()
}

func (t *transfer) activeLocked() bool {
	return t.status == transferRunning || t.status == transferPaused
}

func (t *transfer) active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeLocked()
}

// keepsInPanel reports whether the sidebar has anything to say about the
// transfer: it is going, or it failed and has not been cleared.
func (t *transfer) keepsInPanel() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeLocked() || t.status == transferFailed
}

// view copies a transfer out for one frame.
func (t *transfer) view() transferView {
	t.mu.Lock()
	defer t.mu.Unlock()

	files := make([]transferFileView, len(t.files))
	for i, file := range t.files {
		files[i] = transferFileView{Name: file.Name, Done: file.Done, Total: file.Total}
	}

	view := transferView{
		ID:          t.id,
		Kind:        t.kind,
		Label:       t.label,
		Mod:         t.mod,
		Status:      t.status,
		Phase:       t.phase,
		Err:         t.err,
		Downloading: t.downloading,
		Done:        t.done,
		Total:       t.total,
		Speed:       t.speed,
		Files:       files,
		Current:     t.current,
		Active:      t.activeLocked(),
		CanPause:    t.status == transferPaused || (t.status == transferRunning && t.downloading),
	}
	view.StatusText = t.statusTextLocked()
	return view
}

func (t *transfer) statusTextLocked() string {
	switch t.status {
	case transferPaused:
		return "已暂停"
	case transferDone:
		return "已完成"
	case transferFailed:
		return "失败"
	case transferCanceled:
		return "已取消"
	case transferRunning:
		if t.phase != "" {
			return t.phase
		}
		if t.downloading {
			return "下载中"
		}
		return "准备中"
	default:
		return ""
	}
}

// waitReady blocks while the transfer is paused, and returns the transfer's
// cancellation once it is canceled.
func (t *transfer) waitReady() error {
	for {
		t.mu.Lock()
		if t.ctx.Err() != nil {
			t.mu.Unlock()
			return errTransferCanceled
		}
		if t.status != transferPaused {
			t.mu.Unlock()
			return nil
		}
		resume := t.resume
		t.mu.Unlock()

		if resume == nil {
			continue
		}
		select {
		case <-resume:
		case <-t.ctx.Done():
			return errTransferCanceled
		}
	}
}

// fetch downloads one archive, resuming it across a pause. The download itself
// is resumable and checks what it fetched, so a pause followed by a resume asks
// the server only for what is missing.
func (t *transfer) fetch(client *download.Client, req download.Request, report func(download.Progress)) error {
	for {
		if err := t.waitReady(); err != nil {
			return err
		}

		// The attempt has its own context so that pausing can stop it without
		// ending the transfer.
		t.mu.Lock()
		ctx, cancel := context.WithCancel(t.ctx)
		t.attemptCancel = cancel
		t.mu.Unlock()

		err := client.Get(ctx, req, report)
		cancel()

		t.mu.Lock()
		t.attemptCancel = nil
		paused := t.status == transferPaused
		t.mu.Unlock()

		switch {
		case err == nil:
			return nil
		case t.ctx.Err() != nil:
			return errTransferCanceled
		case paused:
			// The cancellation was the pause; the loop waits for the resume.
			continue
		default:
			return err
		}
	}
}

// openWindow shows the transfers in a window of their own, or brings the one
// already open to the front. It is the main thread's.
//
// Before there is a window there is nothing to open, which is how tests run:
// the window's view is then built directly.
func (a *app) openTransfers() {
	if a.win == nil {
		return
	}
	m := a.transfers

	m.mu.Lock()
	if win := m.win; win != nil && !win.IsDestroyed() {
		m.mu.Unlock()
		win.Show()
		win.Focus()
		return
	}
	m.mu.Unlock()

	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     "下载管理",
		Width:     640,
		Height:    620,
		MinWidth:  480,
		MinHeight: 360,
		StateKey:  "transfers",
		Content:   ui.View(a.transfersWindow),
	})
	win.OnClosed(func() {
		m.mu.Lock()
		if m.win == win {
			m.win = nil
		}
		m.mu.Unlock()
	})

	m.mu.Lock()
	m.win = win
	m.mu.Unlock()
	win.Show()
}

// closeTransfers closes the transfer window, if there is one.
func (a *app) closeTransfers() {
	m := a.transfers
	m.mu.Lock()
	win := m.win
	m.mu.Unlock()
	if win != nil && !win.IsDestroyed() {
		win.Close()
	}
}

// transfersWindow is the content of the window that shows every file of every
// transfer, with the controls that stop or pause them.
func (a *app) transfersWindow(c *ui.Context) {
	a.paint(c)
	t := c.Theme()
	views := a.transfers.snapshot()

	ui.Column(c).Fill().Padding(20).Gap(14).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "下载管理").FontSize(20).Bold()
			ui.Spacer(c)
			ui.Button(c, "清除已结束").
				Label("清除已结束的下载").
				Disabled(allActive(views)).
				OnClick(a.transfers.clearFinished)
			ui.Button(c, "关闭").OnClick(a.closeTransfers)
		})
		ui.Text(c, "下载会继续在后台进行，关闭这个窗口不会停止它们。").
			FontSize(12).TextColor(t.TextMuted)

		if len(views) == 0 {
			ui.Text(c, "没有下载任务。").TextColor(t.TextMuted)
			return
		}

		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				for _, view := range views {
					a.transferCard(c, view)
				}
			})
		})
	})
}

// allActive reports whether every transfer in the list is still going, which
// leaves nothing for 清除已结束 to remove.
func allActive(views []transferView) bool {
	for _, view := range views {
		if !view.Active {
			return false
		}
	}
	return true
}

// transferCard is one transfer in the window: what it is, how far it has got,
// the way to pause it or stop it, and each of its files.
func (a *app) transferCard(c *ui.Context, view transferView) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(12).Gap(8).
		Background(t.Surface).Radius(8).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, view.Label).Bold().Grow(1).MaxLines(1).Ellipsis("…")
			ui.Text(c, view.StatusText).FontSize(12).TextColor(statusColor(t, view))
		})

		// A bar of unknown length until the sizes are known, which some file
		// servers do not say and an update does not until its page is read.
		ui.Progress(c, progressValue(view.Done, view.Total)).WidthPercent(100)
		if line := transferLine(view); line != "" {
			ui.Text(c, line).FontSize(12).TextColor(t.TextMuted)
		}

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if view.CanPause {
				if view.Status == transferPaused {
					ui.Button(c, "继续").
						Label("继续下载 " + view.Label).
						OnClick(func() { a.transferAction(view.ID, (*transfer).Resume) })
				} else {
					ui.Button(c, "暂停").
						Label("暂停下载 " + view.Label).
						OnClick(func() { a.transferAction(view.ID, (*transfer).Pause) })
				}
			}
			if view.Active {
				ui.Button(c, "取消").
					Label("取消 " + view.Label).
					OnClick(func() { a.transferAction(view.ID, (*transfer).Cancel) })
			}
		})

		if view.Err != "" {
			ui.Text(c, view.Err).FontSize(12).TextColor(t.Danger)
		}

		for i, file := range view.Files {
			file := file
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.Text(c, file.Name).FontSize(12).Grow(1).MaxLines(1).Ellipsis("…")
				ui.Text(c, fileLine(view, i, file)).FontSize(12).
					TextColor(t.TextMuted).Shrink(0)
			})
		}
	})
}

// transferAction runs one of the controls on the transfer with the given id.
func (a *app) transferAction(id int64, action func(*transfer)) {
	if t := a.transfers.byID(id); t != nil {
		action(t)
	}
}

// statusColor is the colour a transfer's state is written in.
func statusColor(t *ui.Theme, view transferView) ui.Color {
	switch view.Status {
	case transferFailed:
		return t.Danger
	case transferDone:
		return t.Success
	case transferPaused:
		return t.Accent
	case transferCanceled:
		return t.TextMuted
	default:
		return t.TextMuted
	}
}

// progressValue is how much of a whole has come, -1 when the whole is not
// known. It never leaves the range a progress bar reads.
func progressValue(done, total int64) float64 {
	if total <= 0 {
		return -1
	}
	switch value := float64(done) / float64(total); {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

// transferLine is the bytes and the rate beside a transfer's bar.
func transferLine(view transferView) string {
	switch {
	case view.Total > 0:
		line := fmt.Sprintf("%d%% · %s / %s",
			int(progressValue(view.Done, view.Total)*100+0.5),
			humanSize(view.Done), humanSize(view.Total))
		if view.Speed > 0 {
			line += " · " + humanRate(view.Speed)
		}
		return line
	case view.Done > 0:
		line := humanSize(view.Done)
		if view.Speed > 0 {
			line += " · " + humanRate(view.Speed)
		}
		return line
	default:
		// Nothing is known yet -- the page is still being read, or the server
		// has not answered -- so the state line above says all there is to say.
		return ""
	}
}

// fileLine is one file's share of a transfer. A file on its way says how much
// of it has come; the ones waiting say how big they are.
func fileLine(view transferView, i int, file transferFileView) string {
	if file.Total > 0 && file.Done >= file.Total {
		return "已完成 · " + humanSize(file.Total)
	}
	if view.Status == transferPaused && i == view.Current {
		if file.Total > 0 {
			return fmt.Sprintf("已暂停 · %s / %s", humanSize(file.Done), humanSize(file.Total))
		}
		return "已暂停 · " + humanSize(file.Done)
	}
	if view.Downloading && i == view.Current && file.Total > 0 {
		return fmt.Sprintf("%d%% · %s / %s",
			int(float64(file.Done)/float64(file.Total)*100+0.5),
			humanSize(file.Done), humanSize(file.Total))
	}
	if view.Downloading && i == view.Current {
		return "下载中 · " + humanSize(file.Done)
	}
	if file.Done > 0 && file.Total > 0 {
		return fmt.Sprintf("%d%% · %s / %s",
			int(float64(file.Done)/float64(file.Total)*100+0.5),
			humanSize(file.Done), humanSize(file.Total))
	}
	if file.Done > 0 {
		return humanSize(file.Done)
	}
	if file.Total > 0 {
		return "等待中 · " + humanSize(file.Total)
	}
	return "等待中"
}

// humanRate writes a speed the way a person reads one: 200 KB/s, 1.5 MB/s.
func humanRate(bytes float64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.0f B/s", bytes)
	}
	value, exp := bytes, 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	if value < 10 {
		return fmt.Sprintf("%.1f %cB/s", value, "KMGT"[exp-1])
	}
	return fmt.Sprintf("%.0f %cB/s", value, "KMGT"[exp-1])
}

// transfersPanel is the sidebar's list of what is downloading, at the bottom of
// the sidebar above the build. A download outlasts the page it was started
// from, so this is where it stays in sight; the button opens the window with
// every file and the controls.
func (a *app) transfersPanel(c *ui.Context) {
	// A failure is kept in sight until it is cleared from the window: unlike a
	// finished download it is something the user may have to act on.
	views := a.transfers.panelViews()
	if len(views) == 0 {
		return
	}
	t := c.Theme()
	// The sidebar is narrow, so only the first few are listed; the window has
	// all of them.
	const shown = 3
	extra := len(views) - shown
	// A spinner is only honest while something is moving: a pause is a state
	// the user asked for, not a delay.
	running := slices.ContainsFunc(views, func(v transferView) bool {
		return v.Status == transferRunning
	})
	activeCount := 0
	for _, view := range views {
		if view.Active {
			activeCount++
		}
	}

	ui.Column(c).FillWidth().Shrink(0).Gap(8).Padding(12).
		Background(t.Background).
		Label("下载").Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if running {
				ui.Spinner(c)
			}
			title := fmt.Sprintf("下载中 · %d", activeCount)
			switch {
			case !running && activeCount > 0:
				title = fmt.Sprintf("已暂停 · %d", activeCount)
			case activeCount == 0:
				title = fmt.Sprintf("下载失败 · %d", len(views))
			case activeCount < len(views):
				title = fmt.Sprintf("下载 · %d", len(views))
			}
			ui.Text(c, title).FontSize(12).Bold().Grow(1)
			ui.Button(c, "查看").
				Label("打开下载管理").
				OnClick(a.openTransfers)
		})
		for _, view := range views[:min(len(views), shown)] {
			view := view
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				ui.Text(c, view.Label).FontSize(12).MaxLines(1).Ellipsis("…")
				ui.Progress(c, progressValue(view.Done, view.Total)).WidthPercent(100)
				if line := transferLine(view); line != "" {
					ui.Text(c, line).FontSize(11).
						TextColor(t.TextMuted).MaxLines(1).Ellipsis("…")
				}
				switch view.Status {
				case transferPaused:
					ui.Text(c, "已暂停").FontSize(11).TextColor(t.Accent)
				case transferFailed:
					ui.Text(c, "失败："+view.Err).FontSize(11).TextColor(t.Danger).
						MaxLines(1).Ellipsis("…")
				}
			})
		}
		if extra > 0 {
			ui.Text(c, fmt.Sprintf("还有 %d 个", extra)).FontSize(11).TextColor(t.TextMuted)
		}
	})
}
