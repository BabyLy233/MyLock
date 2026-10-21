package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/game"
)

// defaultProfileName is what the default profile, whose files are the addons
// directory itself, is called. It is not in the profiles file, and it cannot be
// deleted.
const defaultProfileName = "默认配置"

// profileFor returns the profile in folder under the installation's addons
// directory. An empty folder is the default profile, which is that directory.
func profileFor(install *game.Install, folder string) addons.Profile {
	if folder == "" {
		return addons.Open(install.AddonsDir())
	}
	return addons.OpenNamed(install.AddonsDir(), folder)
}

// profileDisplayName is what the window calls the profile in folder.
func profileDisplayName(folder string, profiles []config.Profile) string {
	if folder == "" {
		return defaultProfileName
	}
	for _, p := range profiles {
		if p.Folder == folder && p.Name != "" {
			return p.Name
		}
	}
	return profileNameFromFolder(folder)
}

// profileNameFromFolder recovers a name from a folder made by this app or by
// the Tauri build, which both name one profile_<timestamp>_<random>_<slug>.
// Anything else is shown by its folder name rather than guessed at.
func profileNameFromFolder(folder string) string {
	rest, ok := strings.CutPrefix(folder, "profile_")
	if !ok {
		return folder
	}
	parts := strings.SplitN(rest, "_", 3)
	if len(parts) == 3 && allDigits(parts[0]) && parts[2] != "" {
		return strings.ReplaceAll(parts[2], "_", " ")
	}
	return folder
}

// allDigits reports whether s is a non-empty run of digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// validProfileFolder reports whether folder names a profile this app may touch:
// one of its own under the addons directory. The checks are the ones a path
// removed with RemoveAll has to pass before anything is deleted.
func validProfileFolder(folder string) bool {
	if !strings.HasPrefix(folder, "profile_") || folder == "profile_" {
		return false
	}
	if folder == "." || folder == ".." || strings.ContainsAny(folder, `/\`) {
		return false
	}
	return filepath.Base(folder) == folder
}

// profileSlug turns a display name into the readable tail of a folder name. A
// name of nothing but punctuation still leaves the timestamp and the random
// part, which is what keeps the folder unique.
func profileSlug(name string) string {
	var b strings.Builder
	underscore := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			underscore = false
		case r == '-' || r == '_':
			b.WriteRune(r)
			underscore = false
		default:
			if !underscore && b.Len() > 0 {
				b.WriteByte('_')
				underscore = true
			}
		}
	}
	slug := strings.Trim(b.String(), "_")
	if runes := []rune(slug); len(runes) > 32 {
		slug = strings.Trim(string(runes[:32]), "_")
	}
	if slug == "" {
		slug = "profile"
	}
	return slug
}

// randomSuffix is the random part of a new profile's folder name, so that two
// profiles made in the same millisecond do not collide.
func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}

// profileState is what the view reads about the profiles, taken in one lock.
type profileState struct {
	folder   string
	profiles []config.Profile
	present  map[string]bool
	err      string
	busy     bool
}

// profileState reads the profiles the app knows about.
func (a *app) profileStateNow() profileState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return profileState{
		folder:   a.profileFolder,
		profiles: slices.Clone(a.profiles),
		present:  maps.Clone(a.profilePresent),
		err:      a.profilesErr,
		busy:     a.reading || a.patching || a.launching,
	}
}

// profileRow is one line of the profile manager.
type profileRow struct {
	folder    string
	name      string
	detail    string
	lastUsed  time.Time
	current   bool
	present   bool
	isDefault bool
}

// profileRows lists the default profile and then the named ones. A profile
// whose folder has gone stays on the list, marked missing, so the user can see
// why the mods are gone and remove the now empty entry.
func (a *app) profileRows() []profileRow {
	ps := a.profileStateNow()
	rows := []profileRow{{
		name:      defaultProfileName,
		detail:    "citadel/addons 根目录",
		current:   ps.folder == "",
		present:   true,
		isDefault: true,
	}}
	for _, p := range ps.profiles {
		name := p.Name
		if name == "" {
			name = profileNameFromFolder(p.Folder)
		}
		detail := p.Description
		if detail == "" {
			detail = p.Folder
		}
		rows = append(rows, profileRow{
			folder:   p.Folder,
			name:     name,
			detail:   detail,
			lastUsed: p.LastUsed,
			current:  ps.folder == p.Folder,
			present:  ps.present[p.Folder],
		})
	}
	return rows
}

// scanProfiles merges the profile_* folders under the addons directory into the
// named profiles. A folder the app has not seen becomes a profile named after
// it; the entries it already has are kept as they are, including their names,
// descriptions and last use. changed reports that the profiles file needs a
// write.
func (a *app) scanProfiles(install *game.Install) (profiles []config.Profile, present map[string]bool, changed bool, err error) {
	entries, err := os.ReadDir(install.AddonsDir())
	if errors.Is(err, os.ErrNotExist) {
		entries, err = nil, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("扫描 %s：%w", install.AddonsDir(), err)
	}

	a.mu.Lock()
	profiles = slices.Clone(a.profiles)
	a.mu.Unlock()

	present = make(map[string]bool)
	known := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		known[p.Folder] = true
	}
	for _, entry := range entries {
		folder := entry.Name()
		if !validProfileFolder(folder) || !entry.IsDir() {
			continue
		}
		present[folder] = true
		if known[folder] {
			continue
		}
		created := time.Time{}
		if info, err := entry.Info(); err == nil {
			created = info.ModTime()
		}
		profiles = append(profiles, config.Profile{
			Folder:  folder,
			Name:    profileNameFromFolder(folder),
			Created: created,
		})
		changed = true
	}
	return profiles, present, changed, nil
}

// saveProfiles writes the profiles file, one write at a time with the other
// operations that touch it.
func (a *app) saveProfiles(profiles []config.Profile) error {
	path := config.ProfilesPath(a.configPath)
	if path == "" {
		return nil
	}
	a.profilesMu.Lock()
	defer a.profilesMu.Unlock()
	return config.Profiles{Profiles: profiles}.Save(path)
}

// openProfiles opens the manager and scans the addons directory, so that
// profiles made outside the app -- by the Tauri build, or by hand -- show up.
func (a *app) openProfiles() {
	a.profilesOpen = true
	a.newProfileOpen = false
	a.syncProfiles()
}

// syncProfiles looks at the addons directory again and merges what it finds
// into the profiles file. It reads the disk, so it runs off the main thread.
func (a *app) syncProfiles() {
	a.mu.Lock()
	install := a.install
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
		profiles, present, changed, scanErr := a.scanProfiles(install)
		var saveErr error
		if scanErr == nil && changed {
			saveErr = a.saveProfiles(profiles)
		}
		var message string
		if err := errors.Join(scanErr, saveErr); err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.profilesErr = message
			if scanErr == nil {
				a.profiles, a.profilePresent = profiles, present
			}
			a.mu.Unlock()
		})
	}()
}

// makeProfile creates a new empty profile folder and returns its entry. The
// folder is the only thing written here; the profiles file is the caller's.
func makeProfile(install *game.Install, name, description string) (config.Profile, error) {
	addonsDir := install.AddonsDir()
	if err := os.MkdirAll(addonsDir, 0o755); err != nil {
		return config.Profile{}, fmt.Errorf("创建 %s：%w", addonsDir, err)
	}
	slug := profileSlug(name)
	for range 10 {
		now := time.Now()
		folder := fmt.Sprintf("profile_%d_%s_%s", now.UnixMilli(), randomSuffix(), slug)
		err := os.Mkdir(filepath.Join(addonsDir, folder), 0o755)
		switch {
		case err == nil:
			return config.Profile{
				Folder:      folder,
				Name:        name,
				Description: description,
				Created:     now,
			}, nil
		case errors.Is(err, os.ErrExist):
			// The random part collided, which is unlikely; try another.
		default:
			return config.Profile{}, fmt.Errorf("创建配置档目录：%w", err)
		}
	}
	return config.Profile{}, errors.New("没能建出一个不重名的配置档目录，请再试一次。")
}

// createProfile makes a profile and adds it to the manager, without switching
// to it. It creates a folder and writes the profiles file, so it runs off the
// main thread.
func (a *app) createProfile(name, description string) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)

	a.mu.Lock()
	install := a.install
	busy := a.reading
	if !busy {
		switch {
		case install == nil:
			a.profilesErr = "先找到游戏目录再新建配置档。"
			busy = true
		case name == "":
			a.profilesErr = "配置档的名字不能为空。"
			busy = true
		case slices.ContainsFunc(a.profiles, func(p config.Profile) bool {
			return strings.EqualFold(p.Name, name)
		}):
			a.profilesErr = fmt.Sprintf("已经有一个叫「%s」的配置档了。", name)
			busy = true
		default:
			a.profilesErr = ""
			a.reading = true
		}
	}
	a.mu.Unlock()
	if busy {
		a.invalidate()
		return
	}
	a.invalidate()

	go func() {
		entry, err := makeProfile(install, name, description)
		var profiles []config.Profile
		if err == nil {
			a.mu.Lock()
			profiles = append(slices.Clone(a.profiles), entry)
			a.mu.Unlock()
			if err = a.saveProfiles(profiles); err != nil {
				// The folder is there, so the profile can be used for this
				// run; only remembering it for the next one failed.
				err = fmt.Errorf("配置档已建好，但没能记住：%w", err)
			}
		}
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.profilesErr = message
			if entry.Folder != "" {
				a.profiles = profiles
				if a.profilePresent == nil {
					a.profilePresent = make(map[string]bool)
				}
				a.profilePresent[entry.Folder] = true
			}
			a.mu.Unlock()
		})
	}()
}

// switchProfile makes folder the profile in use: it writes that profile's
// search paths into the game's configuration, reads its mods and remembers the
// choice. It moves the game's configuration, so it runs off the main thread,
// one operation at a time with the switches and the launches.
func (a *app) switchProfile(folder string) {
	// A profile file edited by hand could name a directory outside the addons
	// directory, which this would create and then point the game at.
	if folder != "" && !validProfileFolder(folder) {
		a.mu.Lock()
		a.profilesErr = "这个目录不是配置档，不能使用。"
		a.mu.Unlock()
		a.invalidate()
		return
	}

	a.mu.Lock()
	install := a.install
	current := a.profileFolder
	pinned := a.pinned
	busy := install == nil || a.reading || a.patching || a.launching || folder == current
	if !busy {
		a.profilesErr = ""
		a.reading = true
	}
	a.mu.Unlock()
	if busy {
		return
	}
	a.invalidate()

	go func() {
		// The game can start between the look that drew the manager and this,
		// and it holds the files of the profile being left.
		if a.isGameRunning() {
			a.publish(func() {
				a.mu.Lock()
				a.reading = false
				a.profilesErr = "游戏正在运行，先退出游戏再切换配置档。"
				a.mu.Unlock()
			})
			return
		}

		target := profileFor(install, folder)
		if folder != "" {
			if err := os.MkdirAll(target.Dir, 0o755); err != nil {
				a.failProfile("切换配置档失败：" + err.Error())
				return
			}
		}
		info, err := a.patchGameInfo(install, target)
		if err != nil {
			a.failProfile("切换配置档失败：" + err.Error())
			return
		}
		mods, readErr := target.Read()

		// The game's configuration now names the new profile, so it is the
		// one in use even if the settings file cannot be written. It also
		// wrote the mod paths, which supersedes a launch without mods.
		now := time.Now()
		a.mu.Lock()
		a.profileFolder = folder
		a.mods, a.modsErr = mods, ""
		if readErr != nil {
			a.modsErr = readErr.Error()
		}
		// The last check was about the mods of the profile being left.
		a.checkedAt = time.Time{}
		for i := range a.profiles {
			if a.profiles[i].Folder == folder {
				a.profiles[i].LastUsed = now
			}
		}
		profiles := slices.Clone(a.profiles)
		a.clearVanillaSession()
		a.mu.Unlock()

		saveErr := a.saveSettingsFor(pinned, folder)
		profilesErr := a.saveProfiles(profiles)
		var message string
		if err := errors.Join(saveErr, profilesErr); err != nil {
			message = err.Error()
		}

		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.profiles = profiles
			a.profilesErr = message
			a.gameInfo, a.gameInfoErr = info, ""
			a.blocked = ""
			a.mu.Unlock()
			a.profilesOpen = false
		})
	}()
}

// failProfile ends a profile operation that could not go ahead, with message
// shown in the manager instead of the game directory.
func (a *app) failProfile(message string) {
	a.publish(func() {
		a.mu.Lock()
		a.reading = false
		a.profilesErr = message
		a.mu.Unlock()
	})
}

// deleteProfile removes a profile's folder from every shard and forgets it.
// The default profile cannot be deleted, and deleting the one in use first
// switches back to the default. It deletes files, so it runs off the main
// thread, and the view asks before it gets here.
func (a *app) deleteProfile(folder string) {
	if !validProfileFolder(folder) {
		a.mu.Lock()
		a.profilesErr = "这个目录不是配置档，不能删除。"
		a.mu.Unlock()
		a.invalidate()
		return
	}

	a.mu.Lock()
	install := a.install
	active := a.profileFolder == folder
	pinned := a.pinned
	busy := install == nil || a.reading || a.patching || a.launching
	if !busy {
		a.profilesErr = ""
		a.reading = true
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
				a.reading = false
				a.profilesErr = "游戏正在运行，先退出游戏再删除配置档。"
				a.mu.Unlock()
			})
			return
		}

		var info game.GameInfo
		var mods []addons.Mod
		var modsErr string
		if active {
			def := addons.Open(install.AddonsDir())
			var err error
			info, err = a.patchGameInfo(install, def)
			if err != nil {
				a.failProfile("删除前切回默认配置档失败：" + err.Error())
				return
			}
			mods, err = def.Read()
			if err != nil {
				modsErr = err.Error()
			}
			a.mu.Lock()
			a.profileFolder, a.mods, a.modsErr = "", mods, modsErr
			a.clearVanillaSession()
			a.mu.Unlock()
		}

		// The first shard is the profile directory itself, and the rest are
		// the copies the game loads from beside it. Both are removed, so no
		// files of the profile are left for the game to find.
		profile := addons.OpenNamed(install.AddonsDir(), folder)
		var removeErrs []error
		for shard := uint32(addons.FirstShard); shard <= addons.MaxShards; shard++ {
			if err := os.RemoveAll(profile.ShardDir(shard)); err != nil {
				removeErrs = append(removeErrs, err)
			}
		}
		removeErr := errors.Join(removeErrs...)

		var profiles []config.Profile
		var present map[string]bool
		var saveErr error
		if removeErr == nil {
			a.mu.Lock()
			profiles = slices.DeleteFunc(slices.Clone(a.profiles), func(p config.Profile) bool {
				return p.Folder == folder
			})
			present = maps.Clone(a.profilePresent)
			delete(present, folder)
			a.mu.Unlock()
			saveErr = a.saveProfiles(profiles)
		}
		// The default profile is the one in use now whether or not the files
		// could all be removed, and the settings have to say so.
		if active {
			saveErr = errors.Join(saveErr, a.saveSettingsFor(pinned, ""))
		}

		var parts []error
		if removeErr != nil {
			parts = append(parts, fmt.Errorf("删除配置档文件：%w", removeErr))
		}
		if saveErr != nil {
			parts = append(parts, saveErr)
		}
		var message string
		if err := errors.Join(parts...); err != nil {
			message = err.Error()
		}

		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.profilesErr = message
			if removeErr == nil {
				a.profiles, a.profilePresent = profiles, present
			}
			if active {
				a.gameInfo, a.gameInfoErr = info, ""
			}
			a.mu.Unlock()
			if message == "" {
				a.profilesOpen = false
			}
		})
	}()
}

// profilesDialog is the manager: the profiles there are, which is in use, and
// what can be done to each.
func (a *app) profilesDialog(c *ui.Context) {
	if !a.profilesOpen {
		return
	}
	t := c.Theme()
	ps := a.profileStateNow()
	rows := a.profileRows()
	// As tall as the rows need, up to the point where a long list scrolls
	// rather than making the dialog taller than the window.
	height := min(float32(360), 24+float32(len(rows))*64)
	ui.Modal(c, &a.profilesOpen, func() {
		ui.Text(c, "配置档").FontSize(18).Bold()
		ui.Text(c, "每个配置档有自己的模组清单和文件。切换会改写游戏的搜索路径，游戏运行中不能切换。").
			FontSize(12).TextColor(t.TextMuted)
		if ps.err != "" {
			ui.Text(c, ps.err).FontSize(12).TextColor(t.Danger)
		}
		ui.Scroll(c).Height(height).Gap(8).Children(func() {
			for _, row := range rows {
				a.profileRow(c, row, ps.busy)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c, "新建配置档").Disabled(ps.busy).OnClick(func() {
				a.newProfileName, a.newProfileDesc = "", ""
				a.newProfileOpen = true
			})
			ui.Button(c, "从目录同步").Disabled(ps.busy).OnClick(a.syncProfiles)
			ui.Spacer(c)
			ui.Button(c, "关闭").OnClick(func() { a.profilesOpen = false })
		})
	})
}

// profileRow is one profile in the manager: what it is, and the two things that
// can be done to it.
func (a *app) profileRow(c *ui.Context, row profileRow, busy bool) {
	t := c.Theme()
	ui.Row(c).Fill().Padding(8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Fill().Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, row.name).Bold()
				switch {
				case row.current:
					ui.Text(c, "使用中").FontSize(12).TextColor(t.Success)
				case !row.present:
					ui.Text(c, "目录不存在").FontSize(12).TextColor(t.Danger)
				}
			})
			ui.Text(c, row.detail).FontSize(12).TextColor(t.TextMuted).Selectable()
			if !row.lastUsed.IsZero() {
				ui.Text(c, "最后使用 "+row.lastUsed.Format("2006-01-02 15:04")).
					FontSize(12).TextColor(t.TextMuted)
			}
		})
		ui.Spacer(c)
		if !row.current {
			ui.Button(c, "使用").Disabled(busy).OnClick(func() { a.switchProfile(row.folder) })
		}
		if !row.isDefault {
			ui.Button(c, "删除").Disabled(busy).OnClick(func() { a.profileDeleteAsk = row.folder })
		}
	})
}

// newProfileDialog asks for a name and a description, and creates the profile
// without switching to it.
func (a *app) newProfileDialog(c *ui.Context) {
	if !a.newProfileOpen {
		return
	}
	t := c.Theme()
	create := func() {
		name, description := a.newProfileName, a.newProfileDesc
		a.newProfileOpen = false
		a.createProfile(name, description)
	}
	ui.Modal(c, &a.newProfileOpen, func() {
		ui.Text(c, "新建配置档").FontSize(18).Bold()
		ui.Text(c, "新配置档是空的，模组要重新安装。").FontSize(12).TextColor(t.TextMuted)
		ui.Field(c, "名称", func() {
			ui.TextInput(c, &a.newProfileName).Placeholder("例如：测试服").OnSubmit(create)
		})
		ui.Field(c, "描述", func() {
			ui.TextInput(c, &a.newProfileDesc).Placeholder("可以留空")
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "取消").OnClick(func() { a.newProfileOpen = false })
			ui.PrimaryButton(c, "创建").OnClick(create)
		})
	})
}

// profileDeleteDialog asks before a profile's files go, which cannot be
// undone. It is drawn only while a row has asked for it.
func (a *app) profileDeleteDialog(c *ui.Context) {
	if a.profileDeleteAsk == "" {
		return
	}
	folder := a.profileDeleteAsk
	ps := a.profileStateNow()
	name := profileDisplayName(folder, ps.profiles)
	open := true
	switch ui.AlertDialog(c, &open, "删除配置档“"+name+"”？",
		"这个配置档里的模组文件会一起删掉，不能撤销。", "取消", "确认删除") {
	case 0, ui.AlertDismissed:
		a.profileDeleteAsk = ""
	case 1:
		a.profileDeleteAsk = ""
		a.profilesOpen = false
		a.deleteProfile(folder)
	}
}
