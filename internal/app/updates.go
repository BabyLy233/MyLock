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
	// updateCheckInterval is how long a check stands for. Opening the mods
	// page asks again once it has passed; the button asks whenever the user
	// does.
	updateCheckInterval = 10 * time.Minute
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
	// the zero time before one has. It is what keeps a failing catalogue from
	// being asked again on every frame.
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

// updateFiles picks what an update downloads. Every file the mod was installed
// from that is still offered is taken again, so a mod made of several downloads
// stays made of them rather than being cut down to one. When none of those
// files is left -- the author replaced all of them -- the current downloads are
// taken together, which is the mod as its author publishes it now.
func updateFiles(files []gb.File, info config.ModInfo) []gb.File {
	if len(files) == 0 {
		return nil
	}
	offered := offeredFiles(files)
	matched := slices.DeleteFunc(slices.Clone(offered), func(file gb.File) bool {
		return !slices.Contains(info.FileIDs, file.ID)
	})
	if len(matched) > 0 {
		return matched
	}
	return offered
}

// checkUpdates asks the catalogue about the installed mods and records what it
// says. force makes it ask even when the last check has not stood for its time
// yet, which is what the button does.
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
	fresh := !a.checkedAt.IsZero() && time.Since(a.checkedAt) < updateCheckInterval
	if busy || (!force && fresh) {
		a.mu.Unlock()
		return
	}
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

// maybeCheckUpdates starts a check when the mods page is shown and the last one
// has stood for its time. The check itself decides whether there is anything to
// ask about, so the page can call this on every frame it builds.
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

// updateMod downloads a mod's newest file and puts it in place of the one that
// is installed, keeping the mod on or off as it was. It reads and writes the
// disk and the network, so it runs off the main thread like an install does.
//
// Updates and installs share the transfer queue: a mod can be updated while
// another is being downloaded, and only the step that writes a profile is
// serialized.
func (a *app) updateMod(id string) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	info := a.library[id]
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
		outcome := a.runUpdate(task, install, profileFor(install, folder), id, info)
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

// runUpdate is the update itself: fetch the mod's page again, pick the file to
// install, and replace what is there with it.
func (a *app) runUpdate(task *transfer, install *game.Install, profile addons.Profile, id string, info config.ModInfo) updateOutcome {
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
	// who replaced a file leaves the old address pointing at nothing.
	task.setPhase("读取页面中")
	mod, err := a.catalogue.Profile(context.Background(), kind, number)
	if err != nil {
		return finish(fmt.Errorf("读取模组页面失败：%w", err))
	}
	downloads := updateFiles(mod.Files, info)
	if len(downloads) == 0 {
		return finish(fmt.Errorf("这个模组现在没有可以下载的文件。"))
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
