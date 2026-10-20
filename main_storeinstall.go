package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/addons"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/archive"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/download"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/game"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/gb"
)

// detail is one mod's own page: what it is, and what can be installed from it.
func (s *store) detail(c *ui.Context, state shown) {
	t := c.Theme()
	mod := state.items[state.chosen]

	ui.Column(c).Fill().Gap(12).Clip().Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Button(c, "返回").OnClick(s.back)
			ui.Text(c, mod.Name).FontSize(18).Bold().MaxLines(1).Ellipsis("…")
		})
		ui.Text(c, describe(mod)).FontSize(12).TextColor(t.TextMuted)
		ui.Divider(c)

		switch {
		case state.installing != "":
			s.installingPanel(c, state)
		case state.installErr != "":
			ui.Text(c, state.installErr).TextColor(t.Danger)
		case state.installed != "":
			ui.Text(c, state.installed).TextColor(t.Success)
		}

		switch {
		case state.filesLoading:
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "正在读取文件…").TextColor(t.TextMuted)
			})
		case state.filesErr != "":
			ui.Text(c, state.filesErr).TextColor(t.Danger)
		case len(state.files) == 0:
			ui.Text(c, "这个模组还没有可以下载的文件。").TextColor(t.TextMuted)
		default:
			// Most mods carry several files -- the variants an author
			// published -- and each is a separate archive, so each is offered
			// on its own rather than one being picked for the user.
			ui.Text(c, "选择要安装的文件").Bold()
			for _, file := range state.files {
				s.fileRow(c, mod, file, state.installing != "")
			}
		}
	})
}

// fileRow is one downloadable file, with what installing it does.
func (s *store) fileRow(c *ui.Context, mod gb.Submission, file gb.File, busy bool) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(12).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, file.Name).Bold().MaxLines(1).Ellipsis("…")
			ui.Text(c, describeFile(file)).FontSize(12).TextColor(t.TextMuted).MaxLines(1).Ellipsis("…")
		})
		ui.Button(c, "安装").Disabled(busy).OnClick(func() { s.install(mod, file) })
	})
}

// installingPanel is what the download is doing, while it is doing it.
func (s *store) installingPanel(c *ui.Context, state shown) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, "正在安装 "+state.installing).Bold()
		// A bar of unknown length until the server says how big the file is,
		// which some file servers do not.
		value := float64(-1)
		if state.progress.Total > 0 {
			value = float64(state.progress.Done) / float64(state.progress.Total)
		}
		ui.Progress(c, value).WidthPercent(100)
		if state.progress.Total > 0 {
			ui.Text(c, fmt.Sprintf("%s / %s",
				humanSize(state.progress.Done), humanSize(state.progress.Total))).
				FontSize(12).TextColor(t.TextMuted)
		} else if state.progress.Done > 0 {
			ui.Text(c, humanSize(state.progress.Done)).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// describe says what a mod is, in the terms the catalogue gives.
func describe(mod gb.Submission) string {
	var parts []string
	if mod.Author != "" {
		parts = append(parts, "作者 "+mod.Author)
	}
	if mod.Category != "" {
		category := mod.Category
		if mod.Subcategory != "" {
			category += " / " + mod.Subcategory
		}
		parts = append(parts, category)
	}
	if mod.Version != "" {
		parts = append(parts, "版本 "+mod.Version)
	}
	if mod.Likes > 0 {
		parts = append(parts, fmt.Sprintf("赞 %d", mod.Likes))
	}
	if !mod.Updated.IsZero() {
		parts = append(parts, "更新于 "+mod.Updated.Format("2006-01-02"))
	}
	return strings.Join(parts, " · ")
}

// describeFile says what a file is: how big it is, what the author noted about
// it, and whether the API's own virus scan had anything to say.
func describeFile(file gb.File) string {
	var parts []string
	if file.Size > 0 {
		parts = append(parts, humanSize(file.Size))
	}
	if file.Description != "" {
		parts = append(parts, file.Description)
	}
	if file.Scan != "" && !strings.EqualFold(file.Scan, "clean") {
		parts = append(parts, "扫描结果 "+file.Scan)
	}
	return strings.Join(parts, " · ")
}

// humanSize writes a size the way a person reads one.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n), 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", value, "KMGT"[exp-1])
}

// install downloads one of a mod's files and puts the mod into the game.
//
// The steps are the ones a mod goes through to be playable: fetch the archive,
// unpack it, find the VPKs in it, put them in the profile, turn the mod on, and
// tell the game to search where they landed. Each can fail, and each failure is
// reported rather than left as a mod that is installed and does nothing.
//
// It reads and writes the disk and the network, so it runs off the main thread.
func (s *store) install(mod gb.Submission, file gb.File) {
	a := s.app
	a.mu.Lock()
	install := a.install
	a.mu.Unlock()
	if install == nil {
		s.failed("先到设置里指定游戏目录，再安装模组。")
		return
	}

	s.mu.Lock()
	if s.installing != "" {
		// One install at a time: two of them would race over the same manifest.
		s.mu.Unlock()
		return
	}
	s.installing, s.installErr, s.installed = file.Name, "", ""
	s.progress = download.Progress{}
	s.mu.Unlock()
	a.invalidate()

	go func() {
		err := s.fetch(a, install, mod, file)
		s.mu.Lock()
		s.installing = ""
		if err != nil {
			s.installErr = err.Error()
		} else {
			s.installed = mod.Name + " 已安装并启用。"
		}
		s.mu.Unlock()
		a.invalidate()
	}()
}

// fetch is the install itself, from the download to the game being told where
// the mods are.
func (s *store) fetch(a *app, install *game.Install, mod gb.Submission, file gb.File) error {
	// Everything the download and the unpacking write goes in a directory of
	// its own, which is thrown away whatever happens: an archive is a stranger's
	// and nothing it holds outlives the install.
	work, err := os.MkdirTemp("", "deadlock-mod-manager-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	// The name the archive is written under is the one the API gave it, which
	// is what says which format it is in.
	archivePath := filepath.Join(work, "archive", filepath.Base(file.Name))
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}

	progress := func(p download.Progress) {
		s.mu.Lock()
		s.progress = p
		s.mu.Unlock()
		a.invalidate()
	}
	err = a.downloads.Get(context.Background(), download.Request{
		URL:  file.URL,
		Dest: archivePath,
		Size: file.Size,
		MD5:  file.MD5,
	}, progress)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}

	unpacked := filepath.Join(work, "unpacked")
	if err := archive.Extract(archivePath, unpacked); err != nil {
		return fmt.Errorf("解压失败：%w", err)
	}

	files, err := addons.FindVpks(unpacked, file.Name)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s 里没有 VPK 文件，可能不是 Deadlock 的模组。", file.Name)
	}

	profile := addons.Open(install.AddonsDir())
	if err := profile.Install(mod.Slug(), files); err != nil {
		return err
	}
	// A mod that is installed and not turned on does nothing, which reads as
	// the app having failed. Installing one from the store is asking for it to
	// be used.
	if err := profile.SetEnabled(mod.Slug(), true); err != nil {
		return err
	}

	// The game is told where the mods are, and the list is read back rather
	// than assumed: what the mods page shows is then what is on disk.
	info, infoErr := patchGameInfo(install)
	mods, readErr := profile.Read()

	a.publish(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.mods, a.modsErr = mods, ""
		if readErr != nil {
			a.modsErr = readErr.Error()
		}
		if infoErr == nil {
			a.gameInfo, a.gameInfoErr = info, ""
		} else {
			a.gameInfoErr = infoErr.Error()
		}
	})
	return nil
}

// failed reports why an install could not be started.
func (s *store) failed(message string) {
	s.mu.Lock()
	s.installErr, s.installed = message, ""
	s.mu.Unlock()
	s.app.invalidate()
}
