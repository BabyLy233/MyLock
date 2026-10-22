package app

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/game"
	"github.com/BabyLy233/mylock/internal/gb"
	"github.com/BabyLy233/mylock/internal/heroes"
)

const (
	// genericHeroLabel is how the window names a mod that no hero owns. The
	// page keeps it as a row of its own, which is where a skin the catalogue
	// filed under a kind rather than a hero ends up.
	genericHeroLabel = "通用/其他"
	// heroAutoLabel is the assignment dialog's way back to letting the app
	// decide, which is not a hero of its own.
	heroAutoLabel = "自动"
)

// skinsPage is the 角色皮肤 page: a hero list on the left, and the skins that
// hero owns on the right. Only the main thread touches it.
type skinsPage struct {
	app *app
	// query narrows the hero list, selected is the hero whose skins are shown,
	// and grid keeps the grid's place across rebuilds.
	query    string
	selected string
	grid     ui.GridState
	// deleteAsk is the mod whose deletion is being confirmed, empty when the
	// dialog is closed.
	deleteAsk string
}

func newSkinsPage(a *app) *skinsPage {
	return &skinsPage{app: a}
}

// skinEntry is one installed mod of a hero.
type skinEntry struct {
	mod  addons.Mod
	info config.ModInfo
	// hero is what the mod belongs to, empty for 「通用/其他」.
	hero string
	// name is what the card calls it: the library's name, or its id.
	name string
}

// heroRow is one line of the hero list.
type heroRow struct {
	// hero is the hero's name, empty for 「通用/其他」.
	hero string
	// label is what the row shows.
	label string
	// skins are the hero's installed skins, enabled ones first.
	skins []skinEntry
	// active are the enabled skins, and conflict reports that there is more
	// than one while several at once are not allowed.
	active   []skinEntry
	conflict bool
}

// view draws the page.
func (s *skinsPage) view(c *ui.Context) {
	a := s.app
	t := c.Theme()
	// Read as soon as the page opens, so that the hero list is the catalogue's
	// rather than the built-in roster by the time it is looked at.
	a.loadHeroNames(false)

	a.deleteDialog(c)

	mods := a.installed()
	infos := a.modInfos()
	catalog := a.heroNamesState()
	rows := heroRowsFor(catalog.names, skinEntries(mods.list, infos), a.allowMultipleSkinsNow())
	rows = filterHeroRows(rows, s.query)

	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "角色皮肤").FontSize(22).Bold()
			ui.Spacer(c)
			ui.TextInput(c, &s.query).
				Width(240).
				Placeholder("搜索角色…")
		})
		if catalog.err != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "读不到角色列表，用的是内置名单："+catalog.err).
					FontSize(12).TextColor(t.Danger)
				ui.Button(c, "重试").OnClick(func() { a.loadHeroNames(true) })
			})
		}
		switch {
		case a.state().install == nil:
			ui.Text(c, "还没有找到游戏目录，先到设置里指定。").TextColor(t.TextMuted)
			return
		case mods.reading:
			ui.Text(c, "正在读取…").TextColor(t.TextMuted)
			return
		case mods.err != "":
			ui.Text(c, mods.err).TextColor(t.Danger)
			return
		}
		if len(rows) == 0 {
			ui.Text(c, "没有匹配的角色。").TextColor(t.TextMuted)
			return
		}
		// The selection is kept by name, so a hero whose row moves stays
		// chosen. A selection the search hid falls back to the first row.
		if !slices.ContainsFunc(rows, func(row heroRow) bool { return row.hero == s.selected }) {
			s.selected = rows[0].hero
		}

		ui.Row(c).Fill().Grow(1).Gap(16).AlignItems(ui.Stretch).Children(func() {
			s.heroList(c, rows)
			s.heroSkins(c, rows, mods.reading)
		})
	})
}

// heroList is the left column: every hero the catalogue lists, the ones with
// skins first, and a search box over them.
func (s *skinsPage) heroList(c *ui.Context, rows []heroRow) {
	t := c.Theme()
	ui.Column(c).Width(240).Shrink(0).Gap(4).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Gap(2).Children(func() {
			for _, row := range rows {
				row := row
				selected := row.hero == s.selected
				background := t.Background
				if selected {
					background = t.Surface
				}
				ui.Row(c).FillWidth().Padding(8).Gap(10).AlignItems(ui.Center).
					Radius(8).Background(background).
					OnClick(func() { s.selected = row.hero }).
					Children(func() {
						ui.Avatar(c, row.label, nil).Shrink(0)
						ui.Column(c).Grow(1).Gap(1).Children(func() {
							ui.Text(c, row.label).Bold().MaxLines(1).Ellipsis("…")
							ui.Text(c, rowStatus(row)).FontSize(12).
								TextColor(rowTextColor(t, row)).
								MaxLines(2).Ellipsis("…")
						})
						if len(row.skins) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(row.skins))).
								FontSize(12).TextColor(t.TextMuted)
						}
					})
			}
		})
	})
}

// rowStatus is the line under a hero's name: what it is wearing, or why it is
// marked.
func rowStatus(row heroRow) string {
	switch {
	case len(row.skins) == 0:
		return "没有皮肤"
	case row.conflict:
		return "冲突：多套皮肤同时启用"
	case len(row.active) == 0:
		return "默认"
	default:
		names := make([]string, len(row.active))
		for i, entry := range row.active {
			names[i] = entry.name
		}
		return strings.Join(names, "、")
	}
}

// rowTextColor is the color the status line is drawn in: a conflict is the one
// thing a user has to act on.
func rowTextColor(t *ui.Theme, row heroRow) ui.Color {
	if row.conflict {
		return t.Danger
	}
	return t.TextMuted
}

// heroSkins is the right side: what the chosen hero owns, and the way to the
// store for more.
func (s *skinsPage) heroSkins(c *ui.Context, rows []heroRow, reading bool) {
	t := c.Theme()
	var chosen heroRow
	for _, candidate := range rows {
		if candidate.hero == s.selected {
			chosen = candidate
			break
		}
	}

	ui.Column(c).Grow(1).Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, chosen.label).FontSize(18).Bold()
			ui.Text(c, fmt.Sprintf("%d 个皮肤", len(chosen.skins))).
				FontSize(12).TextColor(t.TextMuted)
			ui.Spacer(c)
			if chosen.hero != "" {
				ui.Button(c, "浏览更多皮肤").OnClick(func() { s.browse(chosen.hero) })
			}
		})
		if chosen.conflict {
			ui.Text(c, "这个角色同时启用了多套皮肤。关掉它们，或者到设置里允许同穿多套。").
				FontSize(12).TextColor(t.Danger)
		}
		if s.app.state().gameRunning {
			ui.Text(c, "游戏正在运行，先退出游戏再换皮肤。").FontSize(12).TextColor(t.Danger)
		}
		if len(chosen.skins) == 0 {
			ui.Text(c, "这个角色还没有安装皮肤。").TextColor(t.TextMuted)
			return
		}
		ui.GridView(c, &s.grid, len(chosen.skins), 200, 250, func(i int) {
			s.skinCard(c, chosen, chosen.skins[i], reading)
		}).Grow(1)
	})
}

// skinPicture is the room a skin card's picture is shown in, which is also what
// it is fetched for.
var skinPicture = box{W: 200, H: 130}

// skinCard is one skin: its picture, its state, and what can be done to it.
func (s *skinsPage) skinCard(c *ui.Context, row heroRow, entry skinEntry, reading bool) {
	t := c.Theme()
	disabled := reading || s.app.state().gameRunning
	ui.Column(c).Fill().Gap(6).Children(func() {
		ui.Row(c).WidthPercent(100).Height(float32(skinPicture.H)).
			Background(t.Surface).Radius(8).Clip().Children(func() {
			if bitmap := s.app.thumbnails.get(entry.info.Thumb, skinPicture, s.app.invalidate); bitmap != nil {
				ui.Image(c, bitmap).Fit(ui.Cover).Fill()
			}
		})
		ui.Text(c, entry.name).Bold().MaxLines(1).Ellipsis("…")
		state := "未启用"
		color := t.TextMuted
		if entry.mod.Enabled {
			state = "已启用"
			color = t.Success
		}
		ui.Text(c, state).FontSize(12).TextColor(color)
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			action := "穿上"
			if entry.mod.Enabled {
				action = "脱下"
			}
			ui.Button(c, action).
				Label(action + " " + entry.name).
				Disabled(disabled).
				OnClick(func() { s.app.wearSkin(row.hero, entry.mod.ID, !entry.mod.Enabled) })
			ui.Button(c, "删除").
				Label("删除 " + entry.mod.ID).
				Disabled(disabled).
				OnClick(func() { s.deleteAsk = entry.mod.ID })
			if entry.info.Page != "" {
				ui.Button(c, "原帖").OnClick(func() { c.OpenURL(entry.info.Page) })
			}
		})
	})
}

// deleteDialog asks before a skin's files go.
func (s *skinsPage) deleteDialog(c *ui.Context) {
	if s.deleteAsk == "" {
		return
	}
	id := s.deleteAsk
	info := s.app.modInfo(id)
	name := info.Name
	if name == "" {
		name = id
	}
	open := true
	switch ui.AlertDialog(c, &open, "删除“"+name+"”？",
		"这个模组的文件会从模组目录里删掉，不能撤销。", "取消", "确认删除") {
	case 0, ui.AlertDismissed:
		s.deleteAsk = ""
	case 1:
		s.deleteAsk = ""
		s.app.removeMod(id)
	}
}

// browse opens the store narrowed to this hero's skins. The category tree and
// its sections are read there, so the filter is applied once they arrive.
func (s *skinsPage) browse(hero string) {
	s.app.store.openHero(hero)
	s.app.router.Push("/store")
}

// skinEntries turns the profile's mods into the skins the page groups. A mod
// the library says is not a skin stays on the mods page: the skins page is for
// skins, and an extra (a voice pack, a HUD part) is not one.
func skinEntries(mods []addons.Mod, infos map[string]config.ModInfo) []skinEntry {
	entries := make([]skinEntry, 0, len(mods))
	for _, mod := range mods {
		info := infos[mod.ID]
		if !isSkinMod(mod.ID, info) {
			continue
		}
		name := info.Name
		if name == "" {
			name = mod.ID
		}
		entries = append(entries, skinEntry{
			mod:  mod,
			info: info,
			hero: heroFor(mod.ID, info),
			name: name,
		})
	}
	return entries
}

// isSkinMod reports whether a mod belongs on the skins page. A mod with no
// metadata could be anything; the ids the catalogue prefixes are the one thing
// that says it is not a skin.
func isSkinMod(id string, info config.ModInfo) bool {
	switch {
	case info.HeroOverride != "":
		return true
	case heroes.IsSkinCategory(info.Category):
		return true
	case info.Category != "":
		return false
	}
	kind, _, ok := submissionRef(id)
	return !ok || kind != gb.Sound
}

// heroFor is the hero a mod belongs to, from what the library knows and what
// the user decided.
func heroFor(id string, info config.ModInfo) string {
	title := info.Name
	if title == "" {
		title = id
	}
	return heroes.Resolve(info.HeroOverride, info.Category, info.Subcategory, title)
}

// heroRowsFor groups the skins by hero. The catalogue's order is kept, heroes
// with skins come first, and a hero the catalogue does not list but a mod was
// assigned to still gets a row. 「通用/其他」 is last, and only there when
// something is in it.
func heroRowsFor(catalog []string, entries []skinEntry, allowMultiple bool) []heroRow {
	known := make(map[string]bool, len(catalog))
	byHero := make(map[string][]skinEntry)
	var extra []string
	for _, name := range catalog {
		known[name] = true
	}
	for _, entry := range entries {
		byHero[entry.hero] = append(byHero[entry.hero], entry)
		if entry.hero != "" && !known[entry.hero] && !slices.Contains(extra, entry.hero) {
			extra = append(extra, entry.hero)
		}
	}

	rows := make([]heroRow, 0, len(catalog)+len(extra)+1)
	add := func(hero, label string) {
		skins := byHero[hero]
		row := heroRow{hero: hero, label: label, skins: skins}
		for _, entry := range skins {
			if entry.mod.Enabled {
				row.active = append(row.active, entry)
			}
		}
		row.conflict = len(row.active) > 1 && !allowMultiple
		rows = append(rows, row)
	}
	for _, name := range catalog {
		add(name, name)
	}
	slices.Sort(extra)
	for _, name := range extra {
		add(name, name)
	}
	if len(byHero[""]) > 0 {
		add("", genericHeroLabel)
	}

	// Heroes with skins first, everything else in the order above. The sort is
	// stable, so the catalogue's order survives inside each group.
	slices.SortStableFunc(rows, func(a, b heroRow) int {
		switch {
		case len(a.skins) > 0 && len(b.skins) == 0:
			return -1
		case len(a.skins) == 0 && len(b.skins) > 0:
			return 1
		default:
			return 0
		}
	})
	return rows
}

// filterHeroRows narrows the list to what the search matches.
func filterHeroRows(rows []heroRow, query string) []heroRow {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return rows
	}
	visible := make([]heroRow, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.label), query) {
			visible = append(visible, row)
		}
	}
	return visible
}

// heroNamesState is what the window reads about the catalogue's hero list.
type heroNamesState struct {
	names []string
	busy  bool
	err   string
}

// heroNamesState takes the list in one lock.
func (a *app) heroNamesState() heroNamesState {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := a.heroNames
	if names == nil {
		// The roster is what there is before the catalogue answers, so the
		// page and the assignment dialog are usable offline.
		names = heroes.Names
	}
	return heroNamesState{names: names, busy: a.heroNamesBusy, err: a.heroNamesErr}
}

// loadHeroNames reads the Skins tree's sections, which are the heroes the
// catalogue lists. force asks again after a failed read. The request runs off
// the main thread; until it lands, the built-in roster is shown.
func (a *app) loadHeroNames(force bool) {
	a.mu.Lock()
	if a.heroNamesBusy || (a.heroNamesTried && !force) {
		a.mu.Unlock()
		return
	}
	a.heroNamesBusy, a.heroNamesTried = true, true
	a.mu.Unlock()
	a.invalidate()

	go func() {
		names, err := a.fetchHeroNames()
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.heroNamesBusy = false
			a.heroNamesErr = message
			// A failed read leaves the roster in place, so the page still
			// works; the next ask is the retry button's.
			if err == nil {
				a.heroNames = names
			}
			a.mu.Unlock()
			a.invalidate()
		})
	}()
}

// fetchHeroNames reads the sections under Skins, in the API's order.
func (a *app) fetchHeroNames() ([]string, error) {
	ctx := context.Background()
	roots, err := a.catalogue.Categories(ctx, gb.Mod, 0)
	if err != nil {
		return nil, err
	}
	id := 0
	for _, root := range roots {
		if root.Name == heroes.SkinsCategory {
			id = root.ID
			break
		}
	}
	if id == 0 {
		return nil, fmt.Errorf("商店分类里没有 %s", heroes.SkinsCategory)
	}
	sections, err := a.catalogue.Categories(ctx, gb.Mod, id)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.Name != "" {
			names = append(names, section.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s 下没有角色", heroes.SkinsCategory)
	}
	return names, nil
}

// heroLabel is how the window names a hero, including the one row that is not
// a hero.
func heroLabel(hero string) string {
	if hero == "" {
		return genericHeroLabel
	}
	return hero
}

// openHeroDialog opens the assignment dialog for a mod, with the choice it has
// now, and asks the catalogue for the roster the dialog offers.
func (a *app) openHeroDialog(id string) {
	a.heroAsk = id
	a.heroOpen = true
	a.heroChoice = heroAutoLabel
	if override := a.modInfo(id).HeroOverride; override != "" {
		a.heroChoice = override
		if hero, ok := heroes.Normalize(override); ok {
			a.heroChoice = hero
		} else if override == heroes.Generic {
			a.heroChoice = genericHeroLabel
		}
	}
	a.loadHeroNames(false)
	a.invalidate()
}

// heroDialog assigns a mod to a hero by hand, which is the way a mod the
// catalogue filed nowhere gets a row on the skins page.
func (a *app) heroDialog(c *ui.Context) {
	if !a.heroOpen || a.heroAsk == "" {
		return
	}
	t := c.Theme()
	names := a.heroNamesState()
	id := a.heroAsk
	info := a.modInfo(id)
	name := info.Name
	if name == "" {
		name = id
	}

	options := make([]string, 0, len(names.names)+2)
	options = append(options, heroAutoLabel)
	options = append(options, names.names...)
	options = append(options, genericHeroLabel)
	// An override the roster does not know -- one written by hand, or by a
	// catalogue this version has not read -- stays selectable rather than
	// silently turning into 自动.
	if !slices.Contains(options, a.heroChoice) {
		options = append(options, a.heroChoice)
	}

	ui.Modal(c, &a.heroOpen, func() {
		ui.Text(c, "指定角色").FontSize(18).Bold()
		ui.Text(c, name+"（#"+id+"）").FontSize(12).TextColor(t.TextMuted)
		switch {
		case names.busy:
			ui.Text(c, "正在读取角色列表…").FontSize(12).TextColor(t.TextMuted)
		case names.err != "":
			ui.Text(c, "读不到角色列表，用的是内置名单："+names.err).
				FontSize(12).TextColor(t.Danger)
		}
		chosen := a.heroChoice
		ui.Select(c, &chosen, options).Label("角色").OnChange(func() {
			a.heroChoice = chosen
		})
		ui.Text(c, "「自动」按分类和名字判断；「通用/其他」表示它不属于任何角色。").
			FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "取消").OnClick(func() {
				a.heroOpen = false
				a.heroAsk = ""
			})
			ui.PrimaryButton(c, "保存").OnClick(func() {
				value := a.heroChoice
				switch value {
				case heroAutoLabel:
					value = ""
				case genericHeroLabel:
					value = heroes.Generic
				default:
					if hero, ok := heroes.Normalize(value); ok {
						value = hero
					}
				}
				a.heroOpen = false
				a.heroAsk = ""
				a.setHeroOverride(id, value)
			})
		})
	})
	if !a.heroOpen {
		a.heroAsk = ""
	}
}

// skinsSection holds what the app does about a hero and its skins. Both are
// the user's calls: the exclusive behavior is the default, but an old
// installation that already has several skins enabled keeps them either way.
func (a *app) skinsSection(c *ui.Context, s state) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "角色皮肤").Bold()
		allow := s.allowMultipleSkins
		ui.Checkbox(c, &allow, "允许同一角色同时启用多套皮肤").OnChange(func() {
			a.setAllowMultipleSkins(allow)
		})
		warn := !s.skipSkinConflictWarning
		ui.Checkbox(c, &warn, "安装时提醒角色冲突").OnChange(func() {
			a.setSkipSkinConflictWarning(!warn)
		})
		ui.Text(c, "关掉「允许多套」时，一个角色穿上一套皮肤会先脱下其它的；"+
			"已经同时启用的多套只在角色页标出来，不会自动改动。").
			FontSize(12).TextColor(t.TextMuted)
	})
}

// setAllowMultipleSkins records whether one hero may wear several skins. The
// page's behavior follows on the next frame; what is already enabled is left
// as it is.
func (a *app) setAllowMultipleSkins(allow bool) {
	a.mu.Lock()
	a.allowMultipleSkins = allow
	pinned := a.pinned
	a.mu.Unlock()
	if err := a.saveSettings(pinned); err != nil {
		a.fail(err.Error())
	}
	a.invalidate()
}

// setSkipSkinConflictWarning records whether an install asks before replacing
// a hero's skin.
func (a *app) setSkipSkinConflictWarning(skip bool) {
	a.mu.Lock()
	a.skipSkinConflictWarning = skip
	pinned := a.pinned
	a.mu.Unlock()
	if err := a.saveSettings(pinned); err != nil {
		a.fail(err.Error())
	}
	a.invalidate()
}

// allowMultipleSkinsNow reads whether one hero may wear several skins.
func (a *app) allowMultipleSkinsNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allowMultipleSkins
}

// setHeroOverride records the hero the user assigned to a mod, and writes the
// library so it survives a restart. An empty value returns it to automatic.
func (a *app) setHeroOverride(id, value string) {
	a.mu.Lock()
	info, known := a.library[id]
	if value == "" && !known {
		a.mu.Unlock()
		return
	}
	info.HeroOverride = value
	if a.library == nil {
		a.library = make(map[string]config.ModInfo)
	}
	a.library[id] = info
	mods := maps.Clone(a.library)
	a.mu.Unlock()

	if err := a.saveLibrary(mods); err != nil {
		log.Printf("remembering how %s was assigned: %v", id, err)
	}
	a.invalidate()
}

// wearSkin puts a hero's skin on or off. Turning one on while several at once
// are not allowed takes the hero's other skins off first, one file move at a
// time; turning the skin that is on off returns the hero to its default look.
// The files move, so it runs off the main thread, and the page shows the disk
// after it rather than what was asked for.
func (a *app) wearSkin(hero, id string, on bool) {
	a.mu.Lock()
	install, folder := a.install, a.profileFolder
	allowMultiple := a.allowMultipleSkins
	infos := maps.Clone(a.library)
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
		// Checked here rather than trusting the last look at the process list:
		// the game can start in the interval, and it holds the files.
		if a.isGameRunning() {
			a.publish(func() {
				a.mu.Lock()
				a.reading = false
				a.blocked = "游戏正在运行，先退出游戏再换皮肤。"
				a.mu.Unlock()
			})
			return
		}

		profile := profileFor(install, folder)
		var errs []error
		mods, readErr := profile.Read()
		if readErr != nil {
			errs = append(errs, readErr)
		}

		// Taking the others off first is what makes a hero wear one skin: a
		// swap that fails stops rather than leaving two on.
		stopped := readErr != nil
		if on && !allowMultiple && !stopped {
			for _, mod := range mods {
				if !mod.Enabled || mod.ID == id {
					continue
				}
				if heroFor(mod.ID, infos[mod.ID]) != hero {
					continue
				}
				if err := profile.SetEnabled(mod.ID, false); err != nil {
					errs = append(errs, err)
					stopped = true
					break
				}
			}
		}
		if !stopped {
			if err := profile.SetEnabled(id, on); err != nil {
				errs = append(errs, err)
			}
		}

		after, afterErr := profile.Read()
		if afterErr != nil {
			errs = append(errs, afterErr)
		}
		// The game's configuration follows every change, as it does for a
		// switch on the mods page, but only when nothing failed.
		info, infoErr := game.GameInfo{}, error(nil)
		if len(errs) == 0 {
			info, infoErr = a.patchGameInfo(install, profile)
		}

		var message string
		if len(errs) > 0 {
			message = errs[0].Error()
			for _, err := range errs[1:] {
				message += "；" + err.Error()
			}
		}
		var infoMessage string
		if infoErr != nil {
			infoMessage = infoErr.Error()
		}
		var forgot bool
		a.publish(func() {
			a.mu.Lock()
			a.reading = false
			a.blocked = ""
			a.mods, a.modsErr = after, message
			if len(errs) == 0 {
				a.gameInfo, a.gameInfoErr = info, infoMessage
				forgot = a.clearVanillaSession()
			}
			a.mu.Unlock()
			if forgot {
				a.rememberSession()
			}
		})
	}()
}
