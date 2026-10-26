package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/archive"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/download"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/gb"
)

const (
	// updatesPerRequest is how many submissions one update request may ask
	// about, which is the API's own limit.
	updatesPerRequest = 50
)

// updatesState is what the view reads about the update check, taken in one
// lock.
type updatesState struct {
	// snapshots are what the catalogue said about each mod the last time it was
	// asked, by mod id.
	snapshots map[string]gb.UpdateSnapshot
	// checkedAt is when a check last finished, whether it worked or not, and is
	// the zero time before one has. It is what the page says its answer from.
	checkedAt time.Time
	checking  bool
	checkErr  string
}

// updateState reads what the last check left, in one lock.
func (a *app) updateState() updatesState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return updatesState{
		snapshots: maps.Clone(a.updates),
		checkedAt: a.checkedAt,
		checking:  a.checking,
		checkErr:  a.checkErr,
	}
}

// availableUpdate is the change that counts as an update.
type availableUpdate struct {
	// When is the date of that change. It is the zero time when the catalogue
	// gave none, which is still an update.
	When time.Time
}

// updateFor reads the update a snapshot holds for one installed mod.
//
// A mod the app installed is judged only by the files it was installed from: an
// unchanged file means nothing to do, however much the author edits the page.
// A mod installed by the Tauri build, or by hand, has no recorded files, so the
// newest file or the page's own date is compared against the files on disk.
func updateFor(snapshot gb.UpdateSnapshot, info config.ModInfo, mod addons.Mod) (availableUpdate, bool) {
	installed := info.Installed
	if installed.IsZero() {
		// The files are written when they are installed, which is the only
		// record of a mod this app did not install itself.
		installed = mod.Modified
	}
	// A mod with no files on disk has nothing to be outdated, and a submission
	// with no files has nothing to offer.
	if installed.IsZero() || len(snapshot.Files) == 0 {
		return availableUpdate{}, false
	}
	newest := newestFile(snapshot.Files)

	if len(info.FileIDs) == 0 {
		latest := snapshot.Updated
		if newest.Added.After(latest) {
			latest = newest.Added
		}
		if latest.After(installed) {
			return availableUpdate{When: latest}, true
		}
		return availableUpdate{}, false
	}

	// A file the user installed is gone from the catalogue, which is what an
	// author replacing it looks like.
	replaced := slices.ContainsFunc(info.FileIDs, func(id int) bool {
		return !slices.ContainsFunc(snapshot.Files, func(file gb.File) bool { return file.ID == id })
	})
	if replaced {
		return availableUpdate{When: newest.Added}, true
	}

	var changed time.Time
	for _, file := range snapshot.Files {
		if !slices.Contains(info.FileIDs, file.ID) {
			continue
		}
		if file.Added.After(installed) && file.Added.After(changed) {
			changed = file.Added
		}
	}
	if changed.IsZero() {
		return availableUpdate{}, false
	}
	return availableUpdate{When: changed}, true
}

// newestFile is the file an update would install when it has a choice: the one
// uploaded last, and the higher number when two were uploaded at once.
func newestFile(files []gb.File) gb.File {
	best := files[0]
	for _, file := range files[1:] {
		if file.Added.After(best.Added) || (file.Added.Equal(best.Added) && file.ID > best.ID) {
			best = file
		}
	}
	return best
}

// offeredFiles is the downloads a page offers: the current ones, or every one
// when the author has archived them all. An archived file is an older version
// of one that replaced it, and installing it beside the replacement would ship
// the same VPK names twice.
func offeredFiles(files []gb.File) []gb.File {
	current := slices.DeleteFunc(slices.Clone(files), func(file gb.File) bool {
		return file.Archived
	})
	if len(current) > 0 {
		return current
	}
	return slices.Clone(files)
}

// newestOffered is the latest of the files a page offers, which is what a mod
// offers a user who has not chosen a variant yet.
func newestOffered(files []gb.File) (gb.File, bool) {
	offered := offeredFiles(files)
	if len(offered) == 0 {
		return gb.File{}, false
	}
	return newestFile(offered), true
}

// updateFiles picks what an update downloads: the files the user chose, in the
// order the page offers them. A file the author archived is an older version of
// one that replaced it, and does not come back by being chosen; the update says
// the page changed rather than quietly installing a different set.
func updateFiles(files []gb.File, ids []int) []gb.File {
	if len(files) == 0 {
		return nil
	}
	offered := offeredFiles(files)
	return slices.DeleteFunc(slices.Clone(offered), func(file gb.File) bool {
		return !slices.Contains(ids, file.ID)
	})
}

// checkUpdates asks the catalogue about the installed mods and records what it
// says. force makes it ask even when the page has already made its own check,
// which is what the button does.
//
// It only asks about mods whose id reads back into the catalogue; a mod put
// there by hand has nothing to ask about. The request runs off the main thread,
// and the page redraws when the answer is in.
func (a *app) checkUpdates(force bool) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	mods := slices.Clone(a.mods)
	// An update in flight is what a check has to wait for; a store install on
	// its way does not change what is installed yet, so the check can run
	// beside it.
	busy := a.checking || a.transfers.activeOfKind(transferUpdate) > 0 ||
		install == nil || len(mods) == 0
	// The mods page checks once by itself and the button checks whenever the
	// user asks. A check that failed still counts: asking a catalogue that
	// cannot be reached again on every frame is not what a retry means.
	if busy || (!force && a.autoChecked) {
		a.mu.Unlock()
		return
	}
	a.autoChecked = true
	a.checking, a.checkErr = true, ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		snapshots, err := a.fetchUpdates(mods)
		var message string
		if err != nil {
			message = err.Error()
		}

		a.publish(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			// The request is over whatever answer it brought, so the page can
			// ask again.
			a.checking = false
			// A profile switched while it was on its way is not what these
			// answers are about.
			if a.profileFolder != folder {
				return
			}
			a.checkErr = message
			// The attempt counts even when it failed, so that a catalogue that
			// cannot be reached is not asked again on every frame.
			a.checkedAt = time.Now()
			if err != nil && len(snapshots) == 0 {
				return
			}
			// Snapshots of mods that are not installed any more are dropped, so
			// a stale answer cannot mark a mod the user has since removed.
			installed := make(map[string]bool, len(mods))
			for _, mod := range mods {
				installed[mod.ID] = true
			}
			next := make(map[string]gb.UpdateSnapshot, len(snapshots))
			for id, snapshot := range snapshots {
				if installed[id] {
					next[id] = snapshot
				}
			}
			a.updates = next
		})
	}()
}

// maybeCheckUpdates starts the one check the mods page makes by itself, the
// first time it is shown. The check itself decides whether there is anything
// to ask about, so the page can call this on every frame it builds; after the
// first check, updates are looked for only when the user asks for them.
func (a *app) maybeCheckUpdates() {
	// Before the window exists there is nobody to show a check to, and a test
	// builds the page without one; a test that wants a check asks for it.
	if a.win == nil {
		return
	}
	a.checkUpdates(false)
}

// fetchUpdates asks about every installed mod the catalogue can answer for,
// grouped by kind because the three listings are numbered separately.
func (a *app) fetchUpdates(mods []addons.Mod) (map[string]gb.UpdateSnapshot, error) {
	byKind := make(map[gb.Kind][]int, len(gb.Kinds))
	for _, mod := range mods {
		kind, number, ok := submissionRef(mod.ID)
		if !ok {
			continue
		}
		byKind[kind] = append(byKind[kind], number)
	}

	found := make(map[string]gb.UpdateSnapshot, len(mods))
	var failures []error
	for _, kind := range gb.Kinds {
		numbers := byKind[kind]
		for chunk := range slices.Chunk(numbers, updatesPerRequest) {
			snapshots, err := a.catalogue.Updates(context.Background(), kind, chunk)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			for i, snapshot := range snapshots {
				if snapshot == nil {
					continue
				}
				id := gb.Submission{ID: chunk[i], Kind: kind}.Slug()
				found[id] = *snapshot
			}
		}
	}
	return found, errors.Join(failures...)
}

// updateOutcome is what an update left behind.
type updateOutcome struct {
	// mods is the profile as it reads afterwards, and modsErr why it could not
	// be read.
	mods    []addons.Mod
	modsErr string
	// gameInfo and gameInfoErr are the game's configuration after the update;
	// patched says whether it was written at all.
	gameInfo    game.GameInfo
	gameInfoErr string
	patched     bool
	err         error
}

// updateMod is the update button. A mod of several files first asks which of
// them to fetch: an author who replaced the installed files leaves nothing to
// match them against, and downloading every variant is not what the user
// installed. One file has nothing to choose between.
func (a *app) updateMod(id string) {
	a.mu.Lock()
	busy := a.install == nil || a.reading || a.checking || a.transfers.activeFor(id)
	info := a.library[id]
	snapshot := a.updates[id]
	a.mu.Unlock()
	if busy {
		return
	}

	offered := offeredFiles(snapshot.Files)
	switch {
	case len(offered) > 1:
		a.openUpdateFiles(id, offered, info)
	case len(offered) == 1:
		a.startUpdate(id, info, []int{offered[0].ID})
	}
}

// startUpdate begins the update of a mod with the files the user chose. The
// files are matched against the page again when it is read, so a choice that
// has gone stale fails rather than installing something else.
func (a *app) startUpdate(id string, info config.ModInfo, fileIDs []int) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	busy := install == nil || a.reading || a.checking || a.transfers.activeFor(id)
	a.mu.Unlock()
	if busy {
		return
	}

	name := info.Name
	if name == "" {
		name = id
	}
	task := a.transfers.start(transferUpdate, "更新 "+name, id, nil)
	a.invalidate()

	go func() {
		outcome := a.runUpdate(task, install, profileFor(install, folder), id, fileIDs)
		task.finish(outcome.err)
		a.publish(func() {
			a.mu.Lock()
			if outcome.err == nil {
				// What the check said is no longer about what is installed; the
				// next check asks again rather than marking it as out of date
				// against a file it was just given.
				delete(a.updates, id)
			}
			if outcome.mods != nil {
				a.mods, a.modsErr = outcome.mods, outcome.modsErr
			}
			if outcome.patched {
				a.gameInfo, a.gameInfoErr = outcome.gameInfo, outcome.gameInfoErr
			}
			a.mu.Unlock()
		})
	}()
}

// updateFilesState is what the update-file dialog reads, taken in one lock.
type updateFilesState struct {
	ask    string
	files  []gb.File
	picked map[int]bool
	err    string
}

func (a *app) updateFilesState() updateFilesState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return updateFilesState{
		ask:    a.updateAsk,
		files:  slices.Clone(a.updateOffered),
		picked: maps.Clone(a.updatePicked),
		err:    a.updateErr,
	}
}

// openUpdateFiles asks which of a mod's files its update should fetch. The
// list is what the last check found, and the files the mod was installed from
// are checked to begin with; with none of those left -- the author replaced
// them -- the newest is, which is what a mod's page starts with too.
func (a *app) openUpdateFiles(id string, files []gb.File, info config.ModInfo) {
	a.mu.Lock()
	a.updateAsk = id
	a.updateOffered = files
	a.updatePicked = defaultPicked(files, info.FileIDs)
	a.updateErr = ""
	a.mu.Unlock()
	a.invalidate()
}

// pickUpdateFile records whether one file of the update dialog is checked.
func (a *app) pickUpdateFile(id int, picked bool) {
	a.mu.Lock()
	if a.updatePicked == nil {
		a.updatePicked = make(map[int]bool)
	}
	if picked {
		a.updatePicked[id] = true
	} else {
		delete(a.updatePicked, id)
	}
	a.updateErr = ""
	a.mu.Unlock()
	a.invalidate()
}

// closeUpdateFiles closes the update-file dialog and forgets what it showed.
func (a *app) closeUpdateFiles() {
	a.mu.Lock()
	a.resetUpdateFilesLocked()
	a.mu.Unlock()
	a.invalidate()
}

func (a *app) resetUpdateFilesLocked() {
	a.updateAsk = ""
	a.updateOffered = nil
	a.updatePicked = nil
	a.updateErr = ""
}

// applyUpdateFiles starts the update with the checked files. The checks are
// made again here, because the dialog can stand open while the game starts or
// a check runs.
func (a *app) applyUpdateFiles() {
	a.mu.Lock()
	id := a.updateAsk
	if id == "" {
		a.mu.Unlock()
		return
	}
	info := a.library[id]
	running := a.gameRunning
	busy := a.install == nil || a.reading || a.checking || a.transfers.activeFor(id)

	selected := make([]int, 0, len(a.updatePicked))
	for _, file := range a.updateOffered {
		if a.updatePicked[file.ID] {
			selected = append(selected, file.ID)
		}
	}
	var refuse string
	switch {
	case len(selected) == 0:
		refuse = "至少选择一个文件。"
	case a.install == nil:
		refuse = "先到设置里指定游戏目录，再更新模组。"
	case running:
		refuse = "游戏正在运行，先退出游戏再更新模组。"
	case busy:
		refuse = "正在读取或检查更新，稍后再试。"
	}
	if refuse != "" {
		a.updateErr = refuse
		a.mu.Unlock()
		a.invalidate()
		return
	}
	a.resetUpdateFilesLocked()
	a.mu.Unlock()
	a.invalidate()
	a.startUpdate(id, info, selected)
}

// updateFilesDialog is the list of files an update can fetch, with a box for
// each. A mod may publish variants the user never installed, and an author who
// replaced the files leaves nothing to match them against, so which of them an
// update installs is the user's choice rather than all of them.
func (a *app) updateFilesDialog(c *ui.Context) {
	state := a.updateFilesState()
	if state.ask == "" {
		return
	}
	t := c.Theme()
	name := a.modInfo(state.ask).Name
	if name == "" {
		name = state.ask
	}

	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(460).Gap(10).Children(func() {
			ui.Text(c, "选择更新文件").FontSize(18).Bold()
			ui.Text(c, name+"（#"+state.ask+"）").FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "作者提供了多个文件。更新会下载勾选的文件，并用它们替换已安装的文件。").
				FontSize(12).TextColor(t.TextMuted)

			// A mod can publish more files than fit, so the list scrolls rather
			// than growing the dialog past the window.
			height := float32(len(state.files)*46 + 4)
			if height > 340 {
				height = 340
			}
			ui.Scroll(c).WidthPercent(100).Height(height).Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					for _, file := range state.files {
						file := file
						picked := state.picked[file.ID]
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							ui.Checkbox(c, &picked, file.Name).
								OnChange(func() { a.pickUpdateFile(file.ID, picked) })
							if line := describeFile(file); line != "" {
								ui.Text(c, line).FontSize(11).TextColor(t.TextMuted).
									MaxLines(1).Ellipsis("…")
							}
						})
					}
				})
			})

			if state.err != "" {
				ui.Text(c, state.err).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "取消").OnClick(a.closeUpdateFiles)
				ui.PrimaryButton(c, "更新").
					Label("更新选中的文件").
					Disabled(len(state.files) == 0 || len(state.picked) == 0).
					OnClick(a.applyUpdateFiles)
			})
		})
	})
	if !open {
		a.closeUpdateFiles()
	}
}

// runUpdate is the update itself: fetch the mod's page again, match the files
// the user chose against it, and replace what is there with them.
func (a *app) runUpdate(task *transfer, install *game.Install, profile addons.Profile, id string, fileIDs []int) updateOutcome {
	outcome := updateOutcome{}
	finish := func(err error) updateOutcome {
		outcome.err = err
		// The list shows the disk after this either way.
		mods, readErr := profile.Read()
		outcome.mods = mods
		if readErr != nil {
			outcome.modsErr = readErr.Error()
		}
		return outcome
	}

	kind, number, ok := submissionRef(id)
	if !ok {
		return finish(fmt.Errorf("%s 在商店里没有对应的条目，不能更新。", id))
	}
	// The page is read again rather than trusting the check's copy: an author
	// who replaced a file leaves the old address pointing at nothing, and the
	// choice was made against the copy the check brought.
	task.setPhase("读取页面中")
	mod, err := a.catalogue.Profile(context.Background(), kind, number)
	if err != nil {
		return finish(fmt.Errorf("读取模组页面失败：%w", err))
	}
	downloads := updateFiles(mod.Files, fileIDs)
	switch {
	case len(mod.Files) == 0:
		return finish(fmt.Errorf("这个模组现在没有可以下载的文件。"))
	case len(downloads) != len(fileIDs):
		// A chosen file was withdrawn or replaced while the dialog stood open.
		// Installing the rest would be a different mod than the one chosen.
		return finish(fmt.Errorf("作者刚刚改了文件列表，请重新检查更新后再试。"))
	}

	// Everything the download and the unpacking write goes in a directory of
	// its own, which is thrown away whatever happens.
	work, err := os.MkdirTemp("", "mylock-update-")
	if err != nil {
		return finish(err)
	}
	defer os.RemoveAll(work)

	// What is being fetched is only known now, so the transfer learns its files
	// here; until then its window says what it is reading.
	task.setFiles(downloads)

	var done int64
	archives := make([]addons.Archive, 0, len(downloads))
	for i, file := range downloads {
		// A pause that landed between files holds here, before the next one is
		// started, rather than being swallowed by the unpacking.
		if err := task.waitReady(); err != nil {
			return finish(err)
		}
		dir := filepath.Join(work, "archive", fmt.Sprint(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return finish(err)
		}
		archivePath := filepath.Join(dir, filepath.Base(file.Name))
		base := done
		task.beginFile(i)
		err = task.fetch(a.downloads, download.Request{
			URL:  a.downloadURL(context.Background(), kind, file),
			Dest: archivePath,
			Size: file.Size,
			MD5:  file.MD5,
		}, func(p download.Progress) { task.report(i, base, p) })
		if err != nil {
			return finish(fmt.Errorf("下载 %s 失败：%w", file.Name, err))
		}
		if written, statErr := os.Stat(archivePath); statErr == nil {
			task.fileWritten(i, written.Size())
			done = base + written.Size()
		}

		// The file arrived before the pause did; the pause still holds here.
		if err := task.waitReady(); err != nil {
			return finish(err)
		}
		task.setPhase("解压中")
		unpacked := filepath.Join(work, "unpacked", fmt.Sprint(i))
		if err := archive.Extract(archivePath, unpacked); err != nil {
			return finish(fmt.Errorf("解压 %s 失败：%w", file.Name, err))
		}
		archives = append(archives, addons.Archive{Name: file.Name, Dir: unpacked})
	}

	files, err := addons.FindArchives(archives)
	if err != nil {
		return finish(err)
	}
	if len(files) == 0 {
		return finish(fmt.Errorf("下载的文件里没有 VPK 文件，可能不是 Deadlock 的模组。"))
	}

	if err := a.commitUpdate(task, install, profile, id, mod.Submission, downloads, files, &outcome); err != nil {
		return finish(err)
	}
	return finish(nil)
}

// commitUpdate replaces the files in the profile with what was just fetched.
// Like an install's last step it is serialized: two updates, or an update and
// an install, must not write the same manifest at once.
func (a *app) commitUpdate(task *transfer, install *game.Install, profile addons.Profile, id string, mod gb.Submission, downloads []gb.File, files []addons.File, outcome *updateOutcome) error {
	// A pause asked for while the archive was unpacking holds here, before
	// anything is written into the profile.
	if err := task.waitReady(); err != nil {
		return err
	}
	task.setPhase("更新中")
	a.profileWriteMu.Lock()
	defer a.profileWriteMu.Unlock()

	// Canceled while the last file was unpacking: the profile has not been
	// touched yet, so stopping here leaves the installed mod as it was.
	if task.ctx.Err() != nil {
		return errTransferCanceled
	}

	// Checked again here, after the download: the game can have been started
	// while the archive was being fetched, and this is the point where files it
	// holds are moved.
	if a.isGameRunning() {
		return fmt.Errorf("游戏正在运行，先退出游戏再更新模组。")
	}

	wasEnabled, err := profile.Replace(id, files)
	if err != nil {
		return err
	}
	// The name and picture may have changed with the version, and the files the
	// mod is installed from are what the next check compares against. Every one
	// of them is recorded, so the next update keeps all of them.
	fileIDs := make([]int, len(downloads))
	for i, download := range downloads {
		fileIDs[i] = download.ID
	}
	a.rememberMod(mod, fileIDs...)

	if wasEnabled {
		if err := profile.SetEnabled(id, true); err != nil {
			return err
		}
		info, infoErr := a.patchGameInfo(install, profile)
		outcome.patched = true
		outcome.gameInfo, outcome.gameInfoErr = info, ""
		if infoErr != nil {
			outcome.gameInfoErr = infoErr.Error()
		}
	}
	return nil
}

// updateCount is how many of the installed mods have an update, and pairs each
// with what the last check found.
func updateCount(mods []addons.Mod, infos map[string]config.ModInfo, snapshots map[string]gb.UpdateSnapshot) int {
	n := 0
	for _, mod := range mods {
		if _, ok := updateFor(snapshots[mod.ID], infos[mod.ID], mod); ok {
			n++
		}
	}
	return n
}

// updatePanel says what the check found and what is updating, above the list.
// The download's own progress is in the sidebar and the download window, so
// this only says that there is something to wait for.
func (a *app) updatePanel(c *ui.Context, ups updatesState, count int, gameRunning bool) {
	t := c.Theme()
	switch {
	case ups.checking:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "正在检查更新…").FontSize(12).TextColor(t.TextMuted)
		})
	case ups.checkErr != "":
		ui.Text(c, "检查更新失败："+ups.checkErr).FontSize(12).TextColor(t.Danger)
	case count > 0:
		ui.Text(c, fmt.Sprintf("有 %d 个模组可以更新。", count)).FontSize(12).TextColor(t.Accent)
	case !ups.checkedAt.IsZero():
		ui.Text(c, "所有模组都是最新的。").FontSize(12).TextColor(t.TextMuted)
	}
	if active := a.transfers.activeOfKind(transferUpdate); active > 0 {
		ui.Text(c, fmt.Sprintf("正在更新 %d 个模组，进度可以在左侧「下载」里看。", active)).
			FontSize(12).TextColor(t.TextMuted)
	}
	if gameRunning && count > 0 {
		ui.Text(c, "游戏正在运行，先退出游戏再更新模组。").FontSize(12).TextColor(t.Danger)
	}
}

// updateLine is what a card says about an update: when it was published, or
// that it is being installed right now.
func updateLine(update availableUpdate, updating, paused bool) string {
	switch {
	case paused:
		return "更新已暂停"
	case updating:
		return "正在更新…"
	case update.When.IsZero():
		return "有新版本"
	default:
		return "有新版本 · " + update.When.Format("2006-01-02")
	}
}
