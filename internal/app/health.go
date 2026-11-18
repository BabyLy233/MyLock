package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/modcompat"
	"github.com/BabyLy233/mylock/internal/modhealth"
	"github.com/BabyLy233/mylock/internal/overlay"
	"github.com/BabyLy233/mylock/internal/source2"
	portoverlay "github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/overlay"
)

// healthRuntime is the compatibility check's state. It is guarded by a.mu.
type healthRuntime struct {
	checker *modhealth.Checker
	// analyze is the per-mod analyzer; tests replace it. Nil means the
	// production modcompat.Analyze.
	analyze modhealth.Analyzer
	report  modhealth.Report
	// checking, checked and total describe the check in flight.
	checking bool
	checked  int
	total    int
	err      string
	// checkedAt is when the last check finished, and checkedEpoch the epoch it
	// covered. A check from another epoch is not shown.
	checkedAt    time.Time
	checkedEpoch uint64
	// compatOpen is the compatibility dialog, and busy a compatibility write in
	// flight. compatErr is what the last write failed with.
	compatOpen bool
	busy       bool
	compatErr  string
}

// healthView is one snapshot of the check's state for the view.
type healthView struct {
	report    modhealth.Report
	byID      map[string]modhealth.Health
	checking  bool
	checked   int
	total     int
	err       string
	checkedAt time.Time
	busy      bool
	compatErr string
}

// initHealth opens the health cache and check orchestration. It is called once
// while the app starts, before the first frame.
func (a *app) initHealth() {
	path, err := config.HealthPath()
	if err != nil {
		log.Printf("compatibility health cache: %v", err)
		return
	}
	store, err := modhealth.Open(path)
	if err != nil {
		log.Printf("compatibility health cache: %v", err)
		return
	}
	a.health.checker = modhealth.NewChecker(store)
}

// maybeCheckHealth runs the page's automatic check when the inputs changed and
// nothing that writes the profile is running.
func (a *app) maybeCheckHealth() {
	a.mu.Lock()
	h := &a.health
	ready := h.checker != nil && a.install != nil && !h.checking && !h.busy &&
		!a.reading && !a.launching && !a.patching
	stale := h.checkedEpoch != a.healthEpoch || h.checkedAt.IsZero()
	a.mu.Unlock()
	if ready && stale {
		a.checkHealth(false)
	}
}

// checkHealth analyses every installed mod and publishes the report. A result
// is only published while the installation, profile and input epoch are still
// the ones the check started under.
func (a *app) checkHealth(force bool) {
	a.mu.Lock()
	if a.health.checker == nil {
		a.health.err = "兼容性检查不可用：没有找到配置目录。"
		a.mu.Unlock()
		return
	}
	if a.health.checking || a.install == nil {
		a.mu.Unlock()
		return
	}
	checker := a.health.checker
	install, folder := a.install, a.profileFolder
	epoch := a.healthEpoch
	total := len(a.mods)
	a.health.checking = true
	a.health.err = ""
	a.health.checked, a.health.total = 0, total
	a.mu.Unlock()
	a.invalidate()

	go func() {
		profile := profileFor(install, folder)
		mods, err := profile.Read()
		if err != nil {
			a.finishHealth(install, folder, epoch, modhealth.Report{}, err)
			return
		}
		gameIndex, err := a.gameIndexFor(install)
		if err != nil {
			a.finishHealth(install, folder, epoch, modhealth.Report{}, err)
			return
		}
		checks := make([]modhealth.CheckMod, 0, len(mods))
		for _, mod := range mods {
			if len(mod.VpkPaths) == 0 {
				continue
			}
			checks = append(checks, modhealth.CheckMod{
				ID:      mod.ID,
				VPKs:    mod.VpkPaths,
				Choice:  compatibilityChoice(mod),
				Enabled: mod.Enabled,
			})
		}
		report, err := checker.Check(profileCacheKey(profile), checks, gameIndex,
			modhealth.CheckOptions{
				Force:      force,
				Build:      install.ClientVersion(),
				OnProgress: a.healthProgress(install, folder, epoch),
			})
		a.finishHealth(install, folder, epoch, report, err)
	}()
}

func (a *app) healthProgress(install *game.Install, folder string, epoch uint64) func(int, int) {
	return func(checked, total int) {
		a.publish(func() {
			a.mu.Lock()
			if a.install == install && a.profileFolder == folder && a.healthEpoch == epoch {
				a.health.checked, a.health.total = checked, total
			}
			a.mu.Unlock()
			a.invalidate()
		})
	}
}

func (a *app) finishHealth(install *game.Install, folder string, epoch uint64, report modhealth.Report, err error) {
	var message string
	if err != nil {
		message = err.Error()
	} else {
		// A manifest choice alone does not make a fix present. The receipt and
		// the package bytes are the evidence.
		report = adjustHealthReport(report, install, folder)
	}
	a.publish(func() {
		a.mu.Lock()
		a.health.checking = false
		current := a.install == install && a.profileFolder == folder && a.healthEpoch == epoch
		if current {
			if err == nil {
				a.health.report = report
			}
			a.health.err = message
			a.health.checkedAt = time.Now()
			a.health.checkedEpoch = epoch
		}
		a.mu.Unlock()
		a.invalidate()
	})
}

// adjustHealthReport downgrades "fixed" verdicts whose compatibility package
// is missing, stale or does not record a repair for that mod. The manifest can
// say a mod opted in while a failed write left nothing to mount.
func adjustHealthReport(report modhealth.Report, install *game.Install, folder string) modhealth.Report {
	profile := profileFor(install, folder)
	receipt, installed, err := overlay.Installed(overlayVPKPath(install, profile))
	fixed := make(map[string]bool)
	if err == nil && installed && receipt != nil && len(receipt.Repairs) > 0 {
		var repairs []overlay.Repair
		if json.Unmarshal(receipt.Repairs, &repairs) == nil {
			for _, repair := range repairs {
				fixed[repair.ModID] = true
			}
		}
	}
	for i := range report.Mods {
		health := &report.Mods[i]
		if health.Status != modhealth.StatusFixed || fixed[health.ModID] {
			continue
		}
		if len(health.RemainingSymptoms) > 0 {
			health.Status = modhealth.StatusPartlyFixable
		} else {
			health.Status = modhealth.StatusFixable
		}
	}
	return report
}

// bumpHealth invalidates any check in flight and asks the next frame for a new
// one, which is what every change to the mods or the game does.
func (a *app) bumpHealth() {
	a.mu.Lock()
	a.healthEpoch++
	a.mu.Unlock()
}

func (a *app) healthState() healthView {
	a.mu.Lock()
	defer a.mu.Unlock()
	byID := make(map[string]modhealth.Health, len(a.health.report.Mods))
	for _, health := range a.health.report.Mods {
		byID[health.ModID] = health
	}
	return healthView{
		report:    a.health.report,
		byID:      byID,
		checking:  a.health.checking,
		checked:   a.health.checked,
		total:     a.health.total,
		err:       a.health.err,
		checkedAt: a.health.checkedAt,
		busy:      a.health.busy,
		compatErr: a.health.compatErr,
	}
}

func compatibilityChoice(mod addons.Mod) modhealth.Choice {
	switch {
	case mod.CompatibilityEnabled:
		return modhealth.ChoiceEnabled
	case mod.CompatibilityOptedOut:
		return modhealth.ChoiceOriginalFiles
	default:
		return modhealth.ChoiceUnset
	}
}

func profileCacheKey(profile addons.Profile) string {
	if profile.Folder == "" {
		return "default"
	}
	return profile.Folder
}

// gameIndexFor returns the shared game index, opening it once per citadel
// directory.
func (a *app) gameIndexFor(install *game.Install) (*source2.GameIndex, error) {
	path := install.CitadelDir()
	a.gameIndexMu.Lock()
	defer a.gameIndexMu.Unlock()
	if a.gameIndex != nil && a.gameIndexPath == path {
		return a.gameIndex, nil
	}
	index, err := source2.OpenGame(path)
	if err != nil {
		a.gameIndex, a.gameIndexPath = nil, ""
		return nil, err
	}
	a.gameIndex, a.gameIndexPath = index, path
	return index, nil
}

// profileFolderPtr spells the default profile as nil, which is how the
// overlay naming distinguishes it.
func profileFolderPtr(folder string) *string {
	if folder == "" {
		return nil
	}
	return &folder
}

// overlayVPKPath is where a profile's compatibility package lives.
func overlayVPKPath(install *game.Install, profile addons.Profile) string {
	return portoverlay.VPKPath(install.CitadelDir(), profileFolderPtr(profile.Folder))
}

// analyzer returns the per-mod analysis function, which tests replace.
func (a *app) analyzer() modhealth.Analyzer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.health.analyze != nil {
		return a.health.analyze
	}
	return modcompat.Analyze
}

// rebuildOverlayLocked builds and publishes the compatibility package for the
// profile's opt-in mods. It must be called with profileWriteMu held. With
// nothing left to fix it removes the package instead, so a launch falls back to
// the mod files themselves.
func (a *app) rebuildOverlayLocked(install *game.Install, profile addons.Profile) error {
	vpkPath := overlayVPKPath(install, profile)
	mods, err := profile.Read()
	if err != nil {
		return err
	}
	var selected []addons.Mod
	for _, mod := range mods {
		if mod.Enabled && mod.CompatibilityEnabled && len(mod.VpkPaths) > 0 {
			selected = append(selected, mod)
		}
	}
	if len(selected) == 0 {
		return overlay.Remove(vpkPath)
	}
	gameIndex, err := a.gameIndexFor(install)
	if err != nil {
		return err
	}
	// Two mods providing the same model would be collapsed into one fixed file,
	// which silently changes the mod the game loads. Refuse instead.
	owners := make(map[string]string)
	for _, mod := range selected {
		paths, err := modcompat.ModelPaths(mod.VpkPaths)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if owner, taken := owners[path]; taken && owner != mod.ID {
				return fmt.Errorf("模组 %s 与 %s 都包含 %s，无法安全修复；请先停用其中一个", owner, mod.ID, path)
			}
			owners[path] = mod.ID
		}
	}
	analyze := a.analyzer()
	files := make([]modcompat.ModFiles, 0, len(selected))
	for _, mod := range selected {
		files = append(files, modcompat.ModFiles{ID: mod.ID, VPKs: mod.VpkPaths})
	}
	scopes, err := modcompat.Scopes(files)
	if err != nil {
		return err
	}
	var sources []overlay.Source
	anyFixed := false
	for _, mod := range selected {
		analysis, err := analyze(mod.ID, mod.VpkPaths, gameIndex, scopes[mod.ID], modcompat.Fix)
		if err != nil {
			return err
		}
		source := overlay.Source{
			ModID:                mod.ID,
			VPKs:                 mod.VpkPaths,
			CompatibilityEnabled: true,
			Fixed:                analysis.Fixed,
		}
		for _, repair := range analysis.Repairs {
			source.Repairs = append(source.Repairs, overlay.Repair{
				ModID: repair.ModID, Path: repair.FilePath, Kind: string(repair.Kind),
			})
		}
		for _, warning := range analysis.Warnings {
			source.Warnings = append(source.Warnings, overlay.Warning{
				ModID: warning.ModID, Path: warning.FilePath,
				Kind: string(warning.Kind), Detail: warning.Detail,
			})
		}
		if len(analysis.Fixed) > 0 {
			anyFixed = true
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 || !anyFixed {
		return overlay.Remove(vpkPath)
	}
	plan, err := overlay.Build(install.CitadelDir(), vpkPath, install.ClientVersion(), sources)
	if err != nil {
		return err
	}
	_, err = overlay.Apply(plan, vpkPath)
	return err
}

// refreshOverlay rebuilds the package after a manifest change. It must be
// called with profileWriteMu held. A rebuild that fails removes the package so
// the game never mounts a fix built from files that are no longer there.
func (a *app) refreshOverlay(install *game.Install, profile addons.Profile) {
	if err := a.rebuildOverlayLocked(install, profile); err != nil {
		log.Printf("rebuilding the compatibility overlay: %v", err)
		if removeErr := overlay.Remove(overlayVPKPath(install, profile)); removeErr != nil {
			log.Printf("removing the compatibility overlay: %v", removeErr)
		}
	}
}

// openCompatibility opens the dialog and starts a forced check, so its numbers
// are from the files as they are now.
func (a *app) openCompatibility() {
	a.health.compatOpen = true
	a.checkHealth(true)
}

func (a *app) closeCompatibility() {
	a.mu.Lock()
	a.health.compatOpen = false
	a.mu.Unlock()
}

// fixCompatibility opts the mods in and rebuilds the package in one manifest
// write.
func (a *app) fixCompatibility(ids []string) {
	a.applyCompatibility(ids, true)
}

// restoreCompatibility opts a mod out, records that the player chose the
// original files, and rebuilds the package without it.
func (a *app) restoreCompatibility(id string) {
	a.applyCompatibility([]string{id}, false)
}

func (a *app) applyCompatibility(ids []string, enabled bool) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	if install == nil || a.health.busy || len(ids) == 0 {
		a.mu.Unlock()
		return
	}
	a.health.busy = true
	a.health.compatErr = ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		mods, info, err := a.writeCompatibility(install, folder, ids, enabled)
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.health.busy = false
			a.health.compatErr = message
			if mods != nil {
				a.mods = mods
			}
			if err == nil {
				a.gameInfo = info
				a.gameInfoErr = ""
			}
			a.healthEpoch++
			a.mu.Unlock()
			a.invalidate()
		})
		if err == nil {
			// The status shown after a fix comes from a fresh read-only check.
			a.checkHealth(true)
		}
	}()
}

// writeCompatibility takes the profile lock, changes the choice, rebuilds the
// overlay and writes the game's search paths, in that order. Removing the old
// package first means a crash at any step falls back to the mod files rather
// than mounting a fix for the previous selection.
func (a *app) writeCompatibility(install *game.Install, folder string, ids []string, enabled bool) ([]addons.Mod, game.GameInfo, error) {
	if a.isGameRunning() {
		return nil, game.GameInfo{}, errors.New("游戏正在运行，先退出游戏再修改兼容性。")
	}
	a.profileWriteMu.Lock()
	defer a.profileWriteMu.Unlock()

	profile := profileFor(install, folder)
	vpkPath := overlayVPKPath(install, profile)
	// Asked again after taking the lock, which is the last point before files
	// this write replaces are touched: the game can have started while the
	// choice was being prepared.
	if a.isGameRunning() {
		return nil, game.GameInfo{}, errors.New("游戏正在运行，先退出游戏再修改兼容性。")
	}
	if err := overlay.Remove(vpkPath); err != nil {
		return nil, game.GameInfo{}, err
	}
	if err := profile.SetCompatibility(ids, enabled); err != nil {
		return nil, game.GameInfo{}, err
	}
	rebuildErr := a.rebuildOverlayLocked(install, profile)
	info, patchErr := a.patchGameInfo(install, profile)
	mods, readErr := profile.Read()
	return mods, info, errors.Join(rebuildErr, patchErr, readErr)
}

func healthSymptoms(symptoms []modhealth.Symptom) string {
	if len(symptoms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(symptoms))
	for _, symptom := range symptoms {
		switch symptom {
		case modhealth.SymptomCameraAim:
			parts = append(parts, "相机/瞄准")
		case modhealth.SymptomAnimations:
			parts = append(parts, "动画")
		case modhealth.SymptomAppearance:
			parts = append(parts, "外观")
		case modhealth.SymptomModel:
			parts = append(parts, "模型")
		case modhealth.SymptomGameData:
			parts = append(parts, "游戏数据")
		case modhealth.SymptomCrashRisk:
			parts = append(parts, "崩溃风险")
		}
	}
	return joinChinese(parts)
}

func joinChinese(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += "、"
		}
		out += part
	}
	return out
}

// healthLine is a mod's badge text and its tone: accent for something the user
// can fix, danger for a mod that needs its author, success for a fix in place.
// It names the categories that were not checked, so "no problems" is never
// mistaken for a full review.
func healthLine(health modhealth.Health) (string, string) {
	line, tone := healthStatusLine(health)
	if line != "" {
		if missing := modhealth.LimitedSymptoms(health.Checks); len(missing) > 0 {
			line += "（未检查：" + healthSymptoms(missing) + "）"
		}
	}
	if health.ScopeIncomplete {
		line += "（多个模组提供同一文件，无法单独修复）"
	}
	return line, tone
}

func healthStatusLine(health modhealth.Health) (string, string) {
	symptoms := healthSymptoms(health.Symptoms)
	with := func(prefix string) string {
		if symptoms == "" {
			return prefix
		}
		return prefix + "：" + symptoms
	}
	switch health.Status {
	case modhealth.StatusWorks:
		return "兼容性正常", "muted"
	case modhealth.StatusFixable:
		return with("可修复"), "accent"
	case modhealth.StatusPartlyFixable:
		return with("部分可修复"), "accent"
	case modhealth.StatusFixed:
		return with("已修复"), "success"
	case modhealth.StatusOriginalFiles:
		return "使用原文件", "muted"
	case modhealth.StatusNeedsUpdate:
		return with("需要更新"), "danger"
	case modhealth.StatusNotCovered:
		return "未完全检查", "muted"
	}
	return "", "muted"
}

func healthToneColor(t *ui.Theme, tone string) ui.Color {
	switch tone {
	case "accent":
		return t.Accent
	case "danger":
		return t.Danger
	case "success":
		return t.Success
	}
	return t.TextMuted
}

// compatibilityDialog lists each mod with something to say about compatibility.
// The buttons act on one mod, and the footer fixes every mod that can be fixed
// in one write.
func (a *app) compatibilityDialog(c *ui.Context) {
	if !a.health.compatOpen {
		return
	}
	t := c.Theme()
	h := a.healthState()
	mods := a.installed()
	infos := a.modInfos()

	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(620).Gap(10).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "兼容性").FontSize(18).Bold()
				ui.Spacer(c)
				if h.checking {
					ui.Spinner(c)
				}
				ui.Button(c, "重新检查").
					Label("重新检查模组兼容性").
					Disabled(h.checking || h.busy).
					OnClick(func() { a.checkHealth(true) })
			})
			ui.Text(c, "检查只读取模组和游戏文件；修复写进独立的兼容包，不改动原模组文件。").
				FontSize(12).TextColor(t.TextMuted)

			var shown []addons.Mod
			var fixable []string
			for _, mod := range mods.list {
				health, ok := h.byID[mod.ID]
				if !ok {
					continue
				}
				if health.Status != modhealth.StatusWorks || health.Repairs > 0 {
					shown = append(shown, mod)
				}
				if health.Repairs > 0 && health.Status != modhealth.StatusFixed && !health.ScopeIncomplete {
					fixable = append(fixable, mod.ID)
				}
			}
			if h.checkedAt.IsZero() && !h.checking && h.err == "" {
				ui.Text(c, "还没有检查结果。").FontSize(12).TextColor(t.TextMuted)
			}
			if h.checking {
				if h.total > 0 {
					ui.Text(c, fmt.Sprintf("正在检查 %d/%d 个模组…", h.checked, h.total)).
						FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Text(c, "正在检查…").FontSize(12).TextColor(t.TextMuted)
				}
			}
			if len(shown) == 0 && !h.checking && h.err == "" {
				ui.Text(c, "没有发现兼容性问题。").FontSize(12).TextColor(t.TextMuted)
			}
			if len(shown) > 0 {
				height := float32(len(shown)*56 + 4)
				if height > 380 {
					height = 380
				}
				ui.Scroll(c).WidthPercent(100).Height(height).Children(func() {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						for _, mod := range shown {
							mod := mod
							health := h.byID[mod.ID]
							line, tone := healthLine(health)
							name := modName(mod.ID, infos[mod.ID])
							ui.Row(c).FillWidth().Padding(8).Gap(10).AlignItems(ui.Center).
								Background(t.Background).Radius(6).Children(func() {
								ui.Column(c).Grow(1).Gap(3).Children(func() {
									ui.Text(c, name).FontSize(14).Bold().MaxLines(1).Ellipsis("…")
									ui.Text(c, line).FontSize(12).TextColor(healthToneColor(t, tone))
									if health.Warnings > 0 {
										ui.Text(c, fmt.Sprintf("%d 项问题需要模组作者更新", health.Warnings)).
											FontSize(11).TextColor(t.TextMuted)
									}
								})
								ui.Row(c).Shrink(0).Gap(6).Children(func() {
									if health.Repairs > 0 && health.Status != modhealth.StatusFixed {
										ui.PrimaryButton(c, "修复").
											Label("修复 " + name).
											Disabled(h.busy || h.checking || !mod.Enabled || health.ScopeIncomplete || a.gameRunningNow()).
											OnClick(func() { a.fixCompatibility([]string{mod.ID}) })
									}
									if health.Status == modhealth.StatusFixed {
										ui.Button(c, "使用原文件").
											Label("让 " + name + " 使用原文件").
											Disabled(h.busy || h.checking || a.gameRunningNow()).
											OnClick(func() { a.restoreCompatibility(mod.ID) })
									}
								})
							})
						}
					})
				})
			}
			if h.err != "" {
				ui.Text(c, h.err).FontSize(12).TextColor(t.Danger)
			}
			if h.compatErr != "" {
				ui.Text(c, h.compatErr).FontSize(12).TextColor(t.Danger)
			}
			if h.busy {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "正在写入兼容包…").FontSize(12).TextColor(t.TextMuted)
				})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if len(fixable) > 0 {
					ui.PrimaryButton(c, "全部修复").
						Label("修复所有可修复的模组").
						Disabled(h.busy || h.checking || a.gameRunningNow()).
						OnClick(func() { a.fixCompatibility(fixable) })
				}
				ui.Button(c, "关闭").OnClick(a.closeCompatibility)
			})
		})
	})
	if !open {
		a.closeCompatibility()
	}
}

// healthSection is the settings page's view of the check: when it last ran,
// what it found and the way to run it again.
func (a *app) healthSection(c *ui.Context, s state) {
	t := c.Theme()
	h := a.healthState()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "兼容性检查").FontSize(16).Bold()
		ui.Text(c, "检查已安装模组与当前游戏文件的差异。修复写进独立的兼容包，原模组文件不变。").
			FontSize(12).TextColor(t.TextMuted)
		switch {
		case h.checkedAt.IsZero():
			ui.Text(c, "还没有检查过。").FontSize(12).TextColor(t.TextMuted)
		case h.checking:
			ui.Text(c, fmt.Sprintf("正在检查 %d/%d 个模组…", h.checked, h.total)).
				FontSize(12).TextColor(t.TextMuted)
		default:
			ui.Text(c, "上次检查："+h.checkedAt.Local().Format("2006-01-02 15:04")).
				FontSize(12).TextColor(t.TextMuted)
		}
		if h.err != "" {
			ui.Text(c, h.err).FontSize(12).TextColor(t.Danger)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "检查兼容性").
				Disabled(s.install == nil || h.checking || h.busy).
				OnClick(func() { a.checkHealth(true) })
			ui.Button(c, "查看详情").
				Disabled(s.install == nil).
				OnClick(a.openCompatibility)
			ui.Text(c, fmt.Sprintf("规则版本 %d", modhealth.RulesRevision)).
				FontSize(11).TextColor(t.TextMuted)
		})
	})
}

// gameRunningNow reads the last poll, which is enough to disable a button; the
// write checks the process list again before it starts.
func (a *app) gameRunningNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gameRunning
}
