package app

import (
	"fmt"
	"maps"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/game"
)

// filesState is what the file dialog reads, taken in one lock. A read of the
// list and an apply both finish on a goroutine, so what they leave is guarded
// by app.mu like the rest of the state they publish.
type filesState struct {
	ask      string
	files    []addons.InstalledFile
	picked   map[string]bool
	busy     bool
	applying bool
	err      string
}

func (a *app) filesState() filesState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return filesState{
		ask:      a.filesAsk,
		files:    slices.Clone(a.files),
		picked:   maps.Clone(a.filesPicked),
		busy:     a.filesBusy,
		applying: a.filesApplying,
		err:      a.filesErr,
	}
}

// openFiles opens the dialog that chooses which of a mod's files the game
// loads. What the mod has is read off the disk, so it runs off the main thread
// and the dialog shows itself as busy until it is known.
func (a *app) openFiles(id string) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	if install == nil {
		a.mu.Unlock()
		return
	}
	a.filesAsk = id
	a.files = nil
	a.filesPicked = nil
	a.filesBusy = true
	a.filesApplying = false
	a.filesErr = ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		files, err := profileFor(install, folder).Files(id)
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			// Another mod's dialog can have been opened while this was on its
			// way, and this answer is not about it.
			if a.filesAsk != id {
				a.mu.Unlock()
				return
			}
			a.filesBusy = false
			a.filesErr = message
			if err == nil {
				a.files = files
				a.filesPicked = make(map[string]bool, len(files))
				for _, file := range files {
					if file.Selected {
						a.filesPicked[file.Name] = true
					}
				}
			}
			a.mu.Unlock()
			a.invalidate()
		})
	}()
}

// pickFile records whether one file of the open dialog is checked.
func (a *app) pickFile(name string, picked bool) {
	a.mu.Lock()
	if a.filesPicked == nil {
		a.filesPicked = make(map[string]bool)
	}
	if picked {
		a.filesPicked[name] = true
	} else {
		delete(a.filesPicked, name)
	}
	a.filesErr = ""
	a.mu.Unlock()
	a.invalidate()
}

// closeFiles closes the file dialog and forgets what it was showing.
func (a *app) closeFiles() {
	a.mu.Lock()
	a.resetFilesLocked()
	a.mu.Unlock()
	a.invalidate()
}

func (a *app) resetFilesLocked() {
	a.filesAsk = ""
	a.files = nil
	a.filesPicked = nil
	a.filesBusy = false
	a.filesApplying = false
	a.filesErr = ""
}

// applyFiles makes the checked files the ones the game loads. It moves files,
// so it runs off the main thread and reads the profile back afterwards; the
// dialog stays open, with the error, when the change could not be made.
func (a *app) applyFiles() {
	a.mu.Lock()
	if a.filesAsk == "" || a.filesBusy || a.filesApplying {
		a.mu.Unlock()
		return
	}
	id := a.filesAsk
	install, folder, running := a.install, a.profileFolder, a.gameRunning

	selected := make([]string, 0, len(a.filesPicked))
	for name, picked := range a.filesPicked {
		if picked {
			selected = append(selected, name)
		}
	}
	slices.Sort(selected)

	var refuse string
	switch {
	case len(selected) == 0:
		refuse = "至少要保留一个文件；想全部关闭请停用这个模组。"
	case install == nil:
		refuse = "先到设置里指定游戏目录，再管理文件。"
	case running:
		refuse = "游戏正在运行，先退出游戏再修改文件。"
	}
	if refuse != "" {
		a.filesErr = refuse
		a.mu.Unlock()
		a.invalidate()
		return
	}
	a.filesApplying = true
	a.filesErr = ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		profile := profileFor(install, folder)
		applyErr := func() error {
			// Checked again here rather than trusting the last look at the
			// process list: the game can start between the two, and it holds
			// the files this is about to move.
			if a.isGameRunning() {
				return fmt.Errorf("游戏正在运行，先退出游戏再修改文件。")
			}
			a.profileWriteMu.Lock()
			defer a.profileWriteMu.Unlock()
			return profile.SetFiles(id, selected)
		}()

		var mods []addons.Mod
		var modsErr string
		var info game.GameInfo
		var infoErr error
		if applyErr == nil {
			mods, modsErr = readMods(profile)
			info, infoErr = a.patchGameInfo(install, profile)
		}
		var message string
		if applyErr != nil {
			message = applyErr.Error()
		}
		var infoMessage string
		if infoErr != nil {
			infoMessage = infoErr.Error()
		}

		a.publish(func() {
			a.mu.Lock()
			a.filesApplying = false
			if applyErr != nil {
				a.filesErr = message
				a.mu.Unlock()
				a.invalidate()
				return
			}
			a.resetFilesLocked()

			a.mods, a.modsErr = mods, modsErr
			a.gameInfo, a.gameInfoErr = info, infoMessage
			// The change wrote the mod paths, so it is no longer a launch
			// without mods the app is waiting on.
			forgot := a.clearVanillaSession()
			a.mu.Unlock()
			if forgot {
				a.rememberSession()
			}
			a.invalidate()
		})
	}()
}

// readMods reads the profile back after a change, reporting why it could not
// rather than failing the change that already happened.
func readMods(profile addons.Profile) ([]addons.Mod, string) {
	mods, err := profile.Read()
	if err != nil {
		return mods, err.Error()
	}
	return mods, ""
}

// filesDialog is the list of a mod's files, with a box for each of the ones the
// game is to load. It is how a mod that ships several files is trimmed down
// without turning the whole mod off.
func (a *app) filesDialog(c *ui.Context) {
	state := a.filesState()
	if state.ask == "" {
		return
	}
	t := c.Theme()
	id := state.ask
	name := a.modInfo(id).Name
	if name == "" {
		name = id
	}

	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(460).Gap(10).Children(func() {
			ui.Text(c, "管理文件").FontSize(18).Bold()
			ui.Text(c, name+"（#"+id+"）").FontSize(12).TextColor(t.TextMuted)

			if state.busy {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "正在读取文件…").FontSize(12).TextColor(t.TextMuted)
				})
			} else {
				ui.Text(c, "勾选的文件会被游戏加载；未勾选的文件保留在配置档里，随时可以再勾上。").
					FontSize(12).TextColor(t.TextMuted)
				// A mod can ship up to a shard's worth of files, so the list
				// scrolls rather than growing the dialog past the window.
				height := float32(len(state.files)*30 + 4)
				if height > 340 {
					height = 340
				}
				ui.Scroll(c).WidthPercent(100).Height(height).Children(func() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						for _, file := range state.files {
							file := file
							picked := state.picked[file.Name]
							ui.Checkbox(c, &picked, file.Name).
								Disabled(state.applying).
								OnChange(func() { a.pickFile(file.Name, picked) })
						}
					})
				})
			}

			if state.err != "" {
				ui.Text(c, state.err).FontSize(12).TextColor(t.Danger)
			}
			if state.applying {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "正在应用…").FontSize(12).TextColor(t.TextMuted)
				})
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "取消").
					Disabled(state.applying).
					OnClick(a.closeFiles)
				ui.PrimaryButton(c, "应用").
					Label("应用文件选择").
					Disabled(state.busy || state.applying || len(state.files) == 0).
					OnClick(a.applyFiles)
			})
		})
	})
	if !open {
		a.closeFiles()
	}
}
