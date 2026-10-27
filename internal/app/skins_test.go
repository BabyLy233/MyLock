package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/gb"
	"github.com/BabyLy233/mylock/internal/heroes"
)

// withHeroes gives the app a hero list of its own, so that a test does not ask
// the catalogue.
func withHeroes(a *app, names ...string) {
	a.heroNames = names
	a.heroNamesTried = true
}

// The skins page groups what is installed by the hero it belongs to, and a mod
// the library says is not a skin stays on the mods page.
func TestSkinsPageGroupsInstalledSkinsByHero(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "1", "yamato.vpk")
	addMod(t, profile, "2", "vindicta.vpk")
	addMod(t, profile, "snd-3", "voice.vpk")
	a.library = map[string]config.ModInfo{
		"1":     {Name: "Yamato Cyber", Category: "Skins", Subcategory: "Yamato"},
		"2":     {Name: "Bikini Vindicta", Category: "Skins", Subcategory: "Vindicta"},
		"snd-3": {Name: "Voice Pack", Category: "Audio", Subcategory: "Voice"},
	}
	withHeroes(a, "Vindicta", "Yamato", "Abrams")
	a.router.Push("/skins")
	tt := show(t, a, install, profile)

	for _, want := range []string{"角色皮肤", "Vindicta", "Yamato", "Abrams", "Bikini Vindicta", "1 个皮肤"} {
		if !tt.HasText(want) {
			t.Errorf("the skins page does not show %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Voice Pack") {
		t.Errorf("the skins page lists a sound pack: %q", tt.Texts())
	}
}

// A hero with skins is listed before one without, and its row says what it is
// wearing.
func TestSkinsPagePutsHeroesWithSkinsFirst(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "1", "yamato.vpk")
	if err := profile.SetEnabled("1", true); err != nil {
		t.Fatal(err)
	}
	a.library = map[string]config.ModInfo{
		"1": {Name: "Yamato Cyber", Category: "Skins", Subcategory: "Yamato"},
	}
	withHeroes(a, "Abrams", "Yamato")
	a.router.Push("/skins")
	tt := show(t, a, install, profile)

	texts := tt.Texts()
	abrams := indexOf(texts, "Abrams")
	yamato := indexOf(texts, "Yamato")
	if abrams < 0 || yamato < 0 {
		t.Fatalf("the hero list is missing a hero: %q", texts)
	}
	if yamato > abrams {
		t.Errorf("Yamato, which has a skin, is listed after Abrams: %q", texts)
	}
	if !tt.HasText("Yamato Cyber") {
		t.Errorf("the row does not name the skin in use: %q", texts)
	}
}

// indexOf is where the first text holding want is, -1 when none is.
func indexOf(texts []string, want string) int {
	for i, text := range texts {
		if strings.Contains(text, want) {
			return i
		}
	}
	return -1
}

// Picking a skin for a hero takes the hero's other skins off first, and
// picking the one already on takes it off: a hero wears one skin, or none.
func TestWearingASkinTakesTheHeroesOtherSkinOff(t *testing.T) {
	a, _, profile, _ := profileApp(t)
	addMod(t, profile, "1", "a.vpk")
	addMod(t, profile, "2", "b.vpk")
	a.library = map[string]config.ModInfo{
		"1": {Name: "First", Category: "Skins", Subcategory: "Yamato"},
		"2": {Name: "Second", Category: "Skins", Subcategory: "Yamato"},
	}
	a.readMods(t, profile)

	a.wearSkin("Yamato", "1", true)
	waitFor(t, "the first skin to go on", func() bool { return !a.installed().reading })
	if !enabled(a, "1") {
		t.Fatalf("the first skin is not enabled: %+v", a.installed().list)
	}

	a.wearSkin("Yamato", "2", true)
	waitFor(t, "the swap", func() bool { return !a.installed().reading })
	if enabled(a, "1") || !enabled(a, "2") {
		t.Fatalf("the hero is wearing 1=%v 2=%v, want only 2: %+v",
			enabled(a, "1"), enabled(a, "2"), a.installed().list)
	}
	// The files of the skin taken off are kept, only moved out of the game's
	// way: a skin can be put back without downloading it again.
	if _, err := os.Stat(filepath.Join(profile.Dir, "1_a.vpk")); err != nil {
		t.Errorf("the skin that was taken off lost its files: %v", err)
	}

	a.wearSkin("Yamato", "2", false)
	waitFor(t, "the skin to come off", func() bool { return !a.installed().reading })
	if enabled(a, "2") {
		t.Errorf("the skin is still on: %+v", a.installed().list)
	}
}

// A skin for another hero is left alone by a swap.
func TestWearingASkinLeavesOtherHeroesAlone(t *testing.T) {
	a, _, profile, _ := profileApp(t)
	addMod(t, profile, "1", "a.vpk")
	addMod(t, profile, "2", "b.vpk")
	a.library = map[string]config.ModInfo{
		"1": {Name: "Vindicta Skin", Category: "Skins", Subcategory: "Vindicta"},
		"2": {Name: "Yamato Skin", Category: "Skins", Subcategory: "Yamato"},
	}
	a.readMods(t, profile)
	if err := profile.SetEnabled("1", true); err != nil {
		t.Fatal(err)
	}

	a.wearSkin("Yamato", "2", true)
	waitFor(t, "the skin to go on", func() bool { return !a.installed().reading })
	if !enabled(a, "1") || !enabled(a, "2") {
		t.Errorf("the other hero's skin was taken off: %+v", a.installed().list)
	}
}

// With several at once allowed, a pick only turns its own skin on.
func TestSeveralSkinsCanBeWornAtOnceWhenAllowed(t *testing.T) {
	a, _, profile, _ := profileApp(t)
	addMod(t, profile, "1", "a.vpk")
	addMod(t, profile, "2", "b.vpk")
	a.library = map[string]config.ModInfo{
		"1": {Name: "First", Category: "Skins", Subcategory: "Yamato"},
		"2": {Name: "Second", Category: "Skins", Subcategory: "Yamato"},
	}
	a.allowMultipleSkins = true
	a.readMods(t, profile)

	a.wearSkin("Yamato", "1", true)
	waitFor(t, "the first skin", func() bool { return !a.installed().reading })
	a.wearSkin("Yamato", "2", true)
	waitFor(t, "the second skin", func() bool { return !a.installed().reading })
	if !enabled(a, "1") || !enabled(a, "2") {
		t.Errorf("one skin took the other off although several are allowed: %+v", a.installed().list)
	}
}

// The page marks a hero with more than one skin on while several are not
// allowed, and leaves the data alone.
func TestSkinsPageMarksASecondSkinAsAConflict(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "1", "a.vpk")
	addMod(t, profile, "2", "b.vpk")
	if err := profile.SetEnabled("1", true); err != nil {
		t.Fatal(err)
	}
	if err := profile.SetEnabled("2", true); err != nil {
		t.Fatal(err)
	}
	a.library = map[string]config.ModInfo{
		"1": {Name: "First", Category: "Skins", Subcategory: "Yamato"},
		"2": {Name: "Second", Category: "Skins", Subcategory: "Yamato"},
	}
	withHeroes(a, "Yamato")
	a.router.Push("/skins")
	tt := show(t, a, install, profile)

	if !tt.HasText("冲突：多套皮肤同时启用") {
		t.Errorf("the page does not mark the conflict: %q", tt.Texts())
	}
	// The page is a view: it does not take one of them off behind the user.
	if !enabled(a, "1") || !enabled(a, "2") {
		t.Errorf("the page changed the enabled skins: %+v", a.installed().list)
	}
}

// enabled reports whether a mod is on in what the app last read.
func enabled(a *app, id string) bool {
	for _, mod := range a.installed().list {
		if mod.ID == id {
			return mod.Enabled
		}
	}
	return false
}

// readMods has the app read the profile, as finding the game does.
func (a *app) readMods(t *testing.T, profile addons.Profile) {
	t.Helper()
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.mods = mods
}

// The mods page names the hero of each mod, which is the same decision the
// skins page groups by.
func TestTheModsPageNamesTheHero(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "1", "skin.vpk")
	addMod(t, profile, "2", "guess.vpk")
	a.library = map[string]config.ModInfo{
		"1": {Name: "A skin", Category: "Skins", Subcategory: "Vindicta"},
		"2": {Name: "Yamato Cyber"},
	}
	tt := show(t, a, install, profile)

	for _, want := range []string{"角色：Vindicta", "角色：Yamato"} {
		if !tt.HasText(want) {
			t.Errorf("the mods page does not show %q: %q", want, tt.Texts())
		}
	}
}

// Assigning a hero by hand is remembered in the library, and is what the skins
// page groups the mod by from then on.
func TestAssigningAHeroRemembersIt(t *testing.T) {
	a, install, profile, path := profileApp(t)
	addMod(t, profile, "554012", "skin.vpk")
	withHeroes(a, "Vindicta", "Yamato")
	tt := show(t, a, install, profile)

	if err := tt.Click("指定 554012 的角色"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("指定角色") {
		t.Fatalf("clicking 角色分类 did not open the dialog: %q", tt.Texts())
	}

	// The control writes what it was given before its notice runs, which is
	// what the save button then reads.
	a.heroChoice = "Vindicta"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := a.modInfo("554012").HeroOverride; got != "Vindicta" {
		t.Errorf("the app remembers override %q, want Vindicta", got)
	}
	library, err := config.LoadLibrary(config.LibraryPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := library.Mods["554012"].HeroOverride; got != "Vindicta" {
		t.Errorf("the library file holds override %q, want Vindicta", got)
	}
}

// 「通用/其他」 is a decision of its own, not a return to automatic, and 自动 is
// the way back.
func TestAssigningTheGenericHero(t *testing.T) {
	a, install, profile, _ := profileApp(t)
	addMod(t, profile, "554012", "skin.vpk")
	a.library = map[string]config.ModInfo{
		"554012": {Name: "Some skin", Category: "Skins", Subcategory: "Yamato"},
	}
	withHeroes(a, "Vindicta", "Yamato")
	tt := show(t, a, install, profile)

	if err := tt.Click("指定 554012 的角色"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.heroChoice = genericHeroLabel
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.modInfo("554012").HeroOverride; got != heroes.Generic {
		t.Fatalf("the app remembers override %q, want %q", got, heroes.Generic)
	}
	if got := heroFor("554012", a.modInfo("554012")); got != "" {
		t.Errorf("heroFor = %q, want the generic group", got)
	}

	if err := tt.Click("指定 554012 的角色"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.heroChoice = heroAutoLabel
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.modInfo("554012").HeroOverride; got != "" {
		t.Errorf("the override is %q after 自动, want empty", got)
	}
}

// Installing a skin for a hero that is already wearing one asks first, and
// does not start until the question is answered.
func TestInstallingASkinAsksAboutTheOneAlreadyWorn(t *testing.T) {
	a, _, profile, _ := profileApp(t)
	addMod(t, profile, "1", "a.vpk")
	if err := profile.SetEnabled("1", true); err != nil {
		t.Fatal(err)
	}
	a.library = map[string]config.ModInfo{
		"1": {Name: "Old Skin", Category: "Skins", Subcategory: "Yamato"},
	}
	a.readMods(t, profile)
	a.store.started = true

	mod := gb.Submission{
		ID: 2, Kind: gb.Mod, Name: "New Skin",
		Category: "Skins", Subcategory: "Yamato",
	}
	a.store.install(mod, []gb.File{{ID: 1, Name: "new.zip"}})

	if a.store.conflict == nil {
		t.Fatal("installing a second skin for a hero did not ask")
	}
	if len(a.store.conflict.wearers) != 1 || a.store.conflict.wearers[0] != "Old Skin" {
		t.Errorf("the question names %v, want Old Skin", a.store.conflict.wearers)
	}
	if a.transfers.activeFor(mod.Slug()) {
		t.Error("the install started before the question was answered")
	}
}

// Canceling the question leaves both the installed skin and nothing else
// begun.
func TestCancelingTheSkinConflictStopsTheInstall(t *testing.T) {
	a, _, profile, _ := profileApp(t)
	addMod(t, profile, "1", "a.vpk")
	if err := profile.SetEnabled("1", true); err != nil {
		t.Fatal(err)
	}
	a.library = map[string]config.ModInfo{
		"1": {Name: "Old Skin", Category: "Skins", Subcategory: "Yamato"},
	}
	a.readMods(t, profile)
	a.store.started = true
	mod := gb.Submission{ID: 2, Kind: gb.Mod, Name: "New Skin", Category: "Skins", Subcategory: "Yamato"}
	a.store.install(mod, []gb.File{{ID: 1, Name: "new.zip"}})

	a.router.Push("/store")
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Yamato 已经有皮肤") {
		t.Fatalf("the question was not drawn: %q", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if a.store.conflict != nil {
		t.Error("the question is still open after 取消")
	}
	if a.transfers.activeFor(mod.Slug()) {
		t.Error("the install started after 取消")
	}
}

// Exchanging takes the hero's other skins off before the new one is installed,
// and leaves other heroes' skins where they are.
func TestExchangingTakesTheOldSkinOff(t *testing.T) {
	a, _, profile, _ := profileApp(t)
	addMod(t, profile, "1", "old.vpk")
	addMod(t, profile, "2", "other.vpk")
	if err := profile.SetEnabled("1", true); err != nil {
		t.Fatal(err)
	}
	if err := profile.SetEnabled("2", true); err != nil {
		t.Fatal(err)
	}
	a.library = map[string]config.ModInfo{
		"1": {Name: "Old Skin", Category: "Skins", Subcategory: "Yamato"},
		"2": {Name: "Other Skin", Category: "Skins", Subcategory: "Vindicta"},
	}
	a.readMods(t, profile)

	if err := a.store.disableWearers(profile, "new", "Yamato"); err != nil {
		t.Fatal(err)
	}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]bool{}
	for _, mod := range mods {
		state[mod.ID] = mod.Enabled
	}
	if state["1"] {
		t.Error("the hero's old skin is still on")
	}
	if !state["2"] {
		t.Error("another hero's skin was taken off")
	}
}

// Allowing several skins or turning the conflict question off means installing
// does not ask, and turns its own skin on.
func TestInstallingDoesNotAskWhenTheSettingSaysSo(t *testing.T) {
	for _, tt := range []struct {
		name  string
		apply func(*app)
	}{
		{"several allowed", func(a *app) { a.allowMultipleSkins = true }},
		{"warning off", func(a *app) { a.skipSkinConflictWarning = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, _, profile, _ := profileApp(t)
			addMod(t, profile, "1", "a.vpk")
			if err := profile.SetEnabled("1", true); err != nil {
				t.Fatal(err)
			}
			a.library = map[string]config.ModInfo{
				"1": {Name: "Old Skin", Category: "Skins", Subcategory: "Yamato"},
			}
			a.readMods(t, profile)
			a.store.started = true
			tt.apply(a)

			mod := gb.Submission{ID: 2, Kind: gb.Mod, Name: "New Skin", Category: "Skins", Subcategory: "Yamato"}
			a.store.install(mod, []gb.File{{ID: 1, Name: "new.zip"}})
			if a.store.conflict != nil {
				t.Error("the install asked although the setting said not to")
			}
		})
	}
}

// A hero is read from a skin's categories before it is installed, and a mod
// that is not a skin is left alone.
func TestStoreHero(t *testing.T) {
	for _, tt := range []struct {
		mod  gb.Submission
		want string
	}{
		{gb.Submission{Category: "Skins", Subcategory: "Yamato"}, "Yamato"},
		{gb.Submission{Category: "Skins", Subcategory: "Other", Name: "Bikini Vindicta"}, "Vindicta"},
		{gb.Submission{Category: "Skins", Subcategory: "Other", Name: "Unknown thing"}, ""},
		{gb.Submission{Category: "Model Replacement", Subcategory: "Weapons", Name: "Haze gun"}, "Haze"},
		{gb.Submission{Category: "Audio", Subcategory: "Voice", Name: "Yamato voice"}, ""},
	} {
		if got := storeHero(tt.mod); got != tt.want {
			t.Errorf("storeHero(%q / %q, %q) = %q, want %q",
				tt.mod.Category, tt.mod.Subcategory, tt.mod.Name, got, tt.want)
		}
	}
}

// The settings page remembers both skin switches.
func TestSkinsSettingsAreRemembered(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	scrollSettingsTo(t, tt, "允许同一角色同时启用多套皮肤")
	if err := tt.Click("允许同一角色同时启用多套皮肤"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowMultipleSkins {
		t.Error("the settings file does not allow several skins")
	}

	scrollSettingsTo(t, tt, "安装时提醒角色冲突")
	if err := tt.Click("安装时提醒角色冲突"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SkipSkinConflictWarning {
		t.Error("the settings file does not turn the conflict warning off")
	}
}

// The hero list is the catalogue's Skins tree when it can be read.
func TestHeroNamesComeFromTheSkinsTree(t *testing.T) {
	a := newApp()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Categories") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("_idCategoryRow") != "" {
			fmt.Fprint(w, `[{"_idRow": 1, "_sName": "Vindicta"}, {"_idRow": 2, "_sName": "Yamato"}]`)
			return
		}
		fmt.Fprint(w, `[{"_idRow": 33295, "_sName": "Skins", "_nCategoryCount": 2},
			{"_idRow": 1, "_sName": "HUD"}]`)
	}))
	t.Cleanup(srv.Close)
	a.catalogue.Base = srv.URL + "/"

	names, err := a.fetchHeroNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "Vindicta" || names[1] != "Yamato" {
		t.Errorf("fetchHeroNames = %v, want the Skins tree's sections", names)
	}
}

// 浏览更多皮肤 opens the store narrowed to the hero. The section's id is only
// known once the category tree has been read, so the listing waits for it.
func TestBrowsingAHeroesSkinsNarrowsTheStore(t *testing.T) {
	a := newApp()
	var asked askedPaths
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.add(r.URL.Path, r.URL.Query())
		switch {
		case strings.HasSuffix(r.URL.Path, "/Categories"):
			if r.URL.Query().Get("_idCategoryRow") != "" {
				fmt.Fprint(w, `[{"_idRow": 20, "_sName": "Vindicta"}]`)
				return
			}
			fmt.Fprint(w, `[{"_idRow": 10, "_sName": "Skins", "_nCategoryCount": 1}]`)
		default:
			fmt.Fprint(w, `{"_aMetadata": {"_nRecordCount": 0, "_nPerpage": 50}, "_aRecords": []}`)
		}
	}))
	t.Cleanup(srv.Close)
	a.catalogue.Base = srv.URL + "/"

	a.store.openHero("Vindicta")
	waitFor(t, "the hero's listing to be asked for", func() bool {
		query := asked.lastQuery("/Mod/Index")
		return query != nil && query.Get("_aFilters[Generic_Category]") == "20"
	})

	state := a.store.shown()
	if state.category != "Skins" || state.hero != "Vindicta" {
		t.Errorf("the store is narrowed to %q / %q, want Skins / Vindicta", state.category, state.hero)
	}
}

// A catalogue that cannot be read falls back to the built-in roster, so the
// page is still usable; the retry button asks again.
func TestHeroNamesFallBackToTheRoster(t *testing.T) {
	a := newApp()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	a.catalogue.Base = srv.URL + "/"

	a.loadHeroNames(false)
	waitFor(t, "the hero list read", func() bool { return !a.heroNamesState().busy })
	state := a.heroNamesState()
	if state.err == "" {
		t.Error("a failed read reported no error")
	}
	if len(state.names) != len(heroes.Names) {
		t.Errorf("the fallback holds %d names, want the roster's %d", len(state.names), len(heroes.Names))
	}
}
