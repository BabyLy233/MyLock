package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/archive"
	"github.com/BabyLy233/mylock/internal/download"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/gb"
)

// detail is one mod's own page: what it is, what its author wrote about it, and
// what can be installed from it.
func (s *store) detail(c *ui.Context, state shown) {
	t := c.Theme()
	mod := state.items[state.chosen]

	// The page scrolls. What an author wrote runs to whatever length they
	// wrote, and the files below it are as many as they published, so the whole
	// of it does not fit a window and is not meant to.
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).FillWidth().Gap(12).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Button(c, "返回").OnClick(s.back)
				ui.Text(c, mod.Name).FontSize(18).Bold().MaxLines(1).Ellipsis("…")
			})
			ui.Text(c, describe(mod, state.profile)).FontSize(12).TextColor(t.TextMuted)
			ui.Divider(c)

			s.gallery(c, state.profile.Images)
			s.writeUpSection(c, state)

			// The page on the site is where the changelog, the comments and the
			// rest of what a mod has are read. The address the API gave with
			// the page is the one to use; the one built from the number is what
			// there is before it arrives.
			ui.Row(c).Children(func() {
				ui.Button(c, "在浏览器打开原帖").OnClick(func() {
					c.OpenURL(cmp.Or(state.profile.URL, mod.Page()))
				})
			})

			if state.installing != "" || state.installErr != "" || state.installed != "" {
				ui.Divider(c)
			}
			switch {
			case state.installing != "":
				s.installingPanel(c, state)
			case state.installErr != "":
				ui.Text(c, state.installErr).TextColor(t.Danger)
			case state.installed != "":
				ui.Text(c, state.installed).TextColor(t.Success)
			}

			s.files(c, mod, state)
		})
	})

	s.pictureViewer(c, state)
}

// pictureViewer shows the screenshot the user opened, over the page, while one
// is open.
func (s *store) pictureViewer(c *ui.Context, state shown) {
	images := state.profile.Images
	if !s.viewer.open || s.viewer.index < 0 || s.viewer.index >= len(images) {
		return
	}
	// The viewer is dismissed from inside itself -- by Escape, by a click on
	// what is behind it, or by its own button -- so what it was told is read
	// back rather than written to the store as it goes.
	open := true
	ui.Modal(c, &open, func() {
		s.viewerPanel(c, images)
	})
	if !open {
		s.closePicture()
	}
}

// shotPicture is the room one picture of the row is shown in, which is also
// what it is fetched for.
var shotPicture = box{W: 240, H: 150}

// gallery shows the pictures the author posted, in their own order, in a row
// that scrolls sideways: an author posts as many as they like, and a page
// cannot be as wide as all of them.
func (s *store) gallery(c *ui.Context, images []gb.Image) {
	if len(images) == 0 {
		return
	}
	t := c.Theme()
	// Named, because a row of pictures has no text of its own for assistive
	// technology to read. Spaced, because pictures butted together read as one
	// wide one rather than as several.
	ui.ScrollHorizontal(c).Label("截图").FillWidth().Height(float32(shotPicture.H)).Gap(12).Children(func() {
		for i, image := range images {
			// Each picture is numbered for the same reason, and because a
			// picture the author posted has no name of its own in the reply.
			ui.Row(c).Width(float32(shotPicture.W)).Height(float32(shotPicture.H)).
				Background(t.Surface).Radius(8).Clip().
				Label(fmt.Sprintf("第 %d 张截图", i+1)).
				OnClick(func() { s.openPicture(i) }).
				Children(func() {
					// A picture that has not arrived yet is nothing at all: the
					// frame is drawn without it, and the window is redrawn when
					// it comes.
					if bitmap := s.app.thumbnails.get(image.Thumb, shotPicture, s.app.invalidate); bitmap != nil {
						ui.Image(c, bitmap).Fit(ui.Cover).Fill()
					}
				})
		}
	})
}

// writeUpSection shows what the author wrote, which the API gives as HTML and
// which is turned into text when it arrives.
//
// It is drawn as text rather than in the rich text view: that view is a box of
// its own with a height it is given and a scrollbar inside it, and this page is
// one scrollbar over everything the author posted. A write-up of any length is
// then as tall as it reads, with the pictures above it and the files below.
func (s *store) writeUpSection(c *ui.Context, state shown) {
	if state.writeUp == "" {
		return
	}
	ui.Text(c, state.writeUp).Selectable()
}

// files offers what can be installed, or says why there is nothing to offer.
func (s *store) files(c *ui.Context, mod gb.Submission, state shown) {
	t := c.Theme()
	switch {
	case state.profileBusy:
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "正在读取模组…").TextColor(t.TextMuted)
		})
	case state.profileErr != "":
		ui.Text(c, state.profileErr).TextColor(t.Danger)
	case len(state.profile.Files) == 0:
		ui.Text(c, "这个模组还没有可以下载的文件。").TextColor(t.TextMuted)
	default:
		// Most mods carry several files -- the variants an author published --
		// and each is a separate archive, so each is offered on its own rather
		// than one being picked for the user.
		ui.Text(c, "选择要安装的文件").Bold()
		if state.gameRunning {
			ui.Text(c, "游戏正在运行，先退出游戏再安装模组。").FontSize(12).TextColor(t.Danger)
		}
		for _, file := range state.profile.Files {
			s.fileRow(c, mod, file, state.installing != "" || state.gameRunning)
		}
	}
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

// describe says what a mod is, in the terms the catalogue gives. What the
// listing carried comes first, and the download count, which only the mod's own
// page has, is counted with the likes it belongs beside.
func describe(mod gb.Submission, profile gb.Profile) string {
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
	if profile.Downloads > 0 {
		parts = append(parts, fmt.Sprintf("下载 %s", thousands(profile.Downloads)))
	}
	if !mod.Updated.IsZero() {
		parts = append(parts, "更新于 "+mod.Updated.Format("2006-01-02"))
	}
	return strings.Join(parts, " · ")
}

// thousands writes a count with a comma every three digits, which is how the
// site writes one and how a count of hundreds of thousands is read.
func thousands(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
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
	install, running := a.install, a.gameRunning
	a.mu.Unlock()
	if install == nil {
		s.failed("先到设置里指定游戏目录，再安装模组。")
		return
	}
	if running {
		s.failed("游戏正在运行，先退出游戏再安装模组。")
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
	work, err := os.MkdirTemp("", "mylock-")
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

	// Checked again here, after the download: the game can have been started
	// while the archive was being fetched, and this is the point where files
	// it holds are moved.
	if a.isGameRunning() {
		return fmt.Errorf("游戏正在运行，先退出游戏再安装模组。")
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
	info, infoErr := a.patchGameInfo(install)
	mods, readErr := profile.Read()

	var forgot bool
	a.publish(func() {
		a.mu.Lock()
		a.mods, a.modsErr = mods, ""
		if readErr != nil {
			a.modsErr = readErr.Error()
		}
		if infoErr == nil {
			a.gameInfo, a.gameInfoErr = info, ""
			// The install wrote the mod paths, so it is no longer a launch
			// without mods the app is waiting on.
			forgot = a.clearVanillaSession()
		} else {
			a.gameInfoErr = infoErr.Error()
		}
		a.mu.Unlock()
		if forgot {
			a.rememberSession()
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
