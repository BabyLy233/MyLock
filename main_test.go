package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/addons"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/config"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/game"
)

// The view runs without a window in tests, which click as a user would.

// waitFor polls until cond holds, which it must: the lookups read the disk on
// a goroutine of their own, and the click that starts one only takes effect in
// a frame after it.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForLook waits for the running lookup to finish, and reports what it left.
func waitForLook(t *testing.T, a *app) state {
	t.Helper()
	waitFor(t, "the lookup to finish", func() bool { return !a.state().looking })
	return a.state()
}

// deadlockDir makes a directory Validate accepts as a Deadlock installation.
func deadlockDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Deadlock")
	if err := os.MkdirAll(filepath.Join(dir, "game", "citadel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "game", "citadel", "gameinfo.gi"), []byte("// gameinfo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// settingsPath points the app at a settings file of its own, so a test never
// reads or writes the one the user runs with.
func settingsPath(t *testing.T, a *app) string {
	t.Helper()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	return a.configPath
}

func TestShellShowsTheSidebar(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"已安装模组", "设置"} {
		if !tt.HasText(want) {
			t.Errorf("the sidebar does not list %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("还没有找到游戏目录，先到设置里指定。") {
		t.Errorf("the mods page does not show its empty state: %q", tt.Texts())
	}
}

// Choosing a page in the sidebar pushes it, and the router shows it.
func TestSidebarNavigates(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.router.Path(); got != "/settings" {
		t.Fatalf("the router is at %q after clicking 设置, want /settings", got)
	}
	if !tt.HasText("游戏目录") {
		t.Errorf("the settings page did not show: %q", tt.Texts())
	}
}

// gameInfoFile is gameinfo.gi as the game ships it: the section the app has to
// write the mod directories into, among the rest of the file.
const gameInfoFile = `"GameInfo"
{
	game		"Citadel"
	FileSystem
	{
		SearchPaths
		{
			Game		citadel
			Mod		core
			Write		core
		}
	}
}
`

// fakeGame makes a Deadlock installation with an empty profile, and returns
// both, so a test can put mods in it.
func fakeGame(t *testing.T) (*game.Install, addons.Profile) {
	t.Helper()
	gamePath := filepath.Join(t.TempDir(), "Deadlock")
	addonsDir := filepath.Join(gamePath, "game", "citadel", "addons")
	if err := os.MkdirAll(addonsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gamePath, "game", "citadel", "gameinfo.gi"), []byte(gameInfoFile), 0o644); err != nil {
		t.Fatal(err)
	}
	return &game.Install{GamePath: gamePath}, addons.Open(addonsDir)
}

// addMod writes a disabled mod of one file into a profile, as an install would
// have left it.
func addMod(t *testing.T, p addons.Profile, id, original string) {
	t.Helper()
	m, err := addons.Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	m.Mods[id] = addons.Entry{
		Shard:            addons.FirstShard,
		DisabledVpks:     []string{id + "_" + original},
		OriginalVpkNames: []string{original},
	}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, id+"_"+original), []byte("vpk"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// show points the app at an installation and has it read the profile, which is
// what finding the game does before the window shows anything.
func show(t *testing.T, a *app, install *game.Install, p addons.Profile) ui.Tester {
	t.Helper()
	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	return *ui.NewTester(a.view, 1280, 800)
}

func TestModsPageWithoutAGameDirectory(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("还没有找到游戏目录") {
		t.Errorf("the mods page does not say there is no game directory: %q", tt.Texts())
	}
}

func TestModsPageWithNothingInstalled(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	tt := show(t, a, install, profile)

	if !tt.HasText("这个目录里还没有模组。") {
		t.Errorf("the mods page does not show its empty state: %q", tt.Texts())
	}
}

// The page names the directory the game loads mods from, so the user can check
// it against what they see on disk.
func TestModsPageListsTheModsAndTheirDirectory(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	addMod(t, profile, "skin-pack", "skin.vpk")
	tt := show(t, a, install, profile)

	for _, want := range []string{"snd-42", "skin-pack", install.AddonsDir(), "1 个文件 · 已停用"} {
		if !tt.HasText(want) {
			t.Errorf("the mods page does not show %q: %q", want, tt.Texts())
		}
	}
}

// An enabled mod is shown with the number the game loads it at, which is what
// the user needs to reason about two mods touching one file.
func TestModsPageShowsTheLoadOrderOfEnabledMods(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	if err := profile.SetEnabled("snd-42", true); err != nil {
		t.Fatal(err)
	}
	tt := show(t, a, install, profile)

	if !tt.HasText("pak01") {
		t.Errorf("the mods page does not show where the mod loads: %q", tt.Texts())
	}
}

// A mod the manifest claims but the disk does not have is called out, because
// nothing else would tell the user why the game ignores it.
func TestModsPageReportsAModMissingItsFiles(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	if err := os.Remove(filepath.Join(profile.Dir, "snd-42_voice.vpk")); err != nil {
		t.Fatal(err)
	}
	tt := show(t, a, install, profile)

	if !tt.HasText("缺少 1 个文件") {
		t.Errorf("the mods page does not report the missing file: %q", tt.Texts())
	}
}

func TestModsPageShowsWhyTheProfileCouldNotBeRead(t *testing.T) {
	a := newApp()
	install, _ := fakeGame(t)
	a.install = install
	a.modsErr = "reading the manifest: permission denied"
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("permission denied") {
		t.Errorf("the mods page does not show the error: %q", tt.Texts())
	}
}

// Flipping a switch moves the mod's files, and the list then shows what is on
// disk rather than what was asked for.
func TestTheSwitchEnablesAMod(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	a.install = install
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.mods = mods

	a.setEnabled("snd-42", true)
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })

	if _, err := os.Stat(filepath.Join(profile.Dir, "pak01_dir.vpk")); err != nil {
		t.Errorf("the file was not enabled: %v", err)
	}
	got := a.installed().list
	if len(got) != 1 || !got[0].Enabled {
		t.Fatalf("the list shows %+v, want the mod enabled", got)
	}
	if got[0].Pak != 1 {
		t.Errorf("the mod loads at pak%02d, want pak01", got[0].Pak)
	}
}

// Clicking a switch is what the user does, so the whole path is exercised:
// the widget, the rename, and the list coming back.
func TestClickingASwitchEnablesAMod(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	tt := show(t, a, install, profile)

	if err := tt.Click("启用 snd-42"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })

	if _, err := os.Stat(filepath.Join(profile.Dir, "pak01_dir.vpk")); err != nil {
		t.Errorf("clicking the switch did not enable the mod: %v", err)
	}
	if got := a.installed().list; len(got) != 1 || !got[0].Enabled {
		t.Errorf("the list shows %+v, want the mod enabled", got)
	}
}

// Enabled mods the game is not set up to load are the one thing the list cannot
// show by itself, and the one thing a game update causes without any other
// sign.
func TestModsPageWarnsWhenTheGameIsNotSetUp(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	if err := profile.SetEnabled("snd-42", true); err != nil {
		t.Fatal(err)
	}
	tt := show(t, a, install, profile)

	if !tt.HasText("不会生效") {
		t.Errorf("the mods page does not warn that the game will not load them: %q", tt.Texts())
	}
}

func TestModsPageSaysNothingWhenTheGameIsSetUp(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	if err := profile.SetEnabled("snd-42", true); err != nil {
		t.Fatal(err)
	}
	a.install = install
	a.gameInfo = game.GameInfo{ModPaths: true}
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.mods = mods
	tt := ui.NewTester(a.view, 1280, 800)

	if tt.HasText("不会生效") {
		t.Errorf("the mods page warns about a game that is set up: %q", tt.Texts())
	}
}

func TestSettingsShowsTheGameConfiguration(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	a.install = install
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("还没应用") {
		t.Errorf("the settings page does not say the game is not set up: %q", tt.Texts())
	}

	_ = profile
	a.gameInfo = game.GameInfo{ModPaths: true, Markers: true, Path: install.GameInfoPath()}
	tt.Frame()
	if !tt.HasText("已应用") {
		t.Errorf("the settings page does not say the game is set up: %q", tt.Texts())
	}
	if !tt.HasText(install.GameInfoPath()) {
		t.Errorf("the settings page does not name the file: %q", tt.Texts())
	}
}

// Applying is what the user does after a game update has taken the mod
// directories back out of the game's configuration.
func TestApplyingWritesTheSearchPaths(t *testing.T) {
	a := newApp()
	install, _ := fakeGame(t)
	a.install = install
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("应用模组路径"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the patch to finish", func() bool { return !a.state().patching })

	data, err := os.ReadFile(install.GameInfoPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// Deadlock Mod Manager - Start",
		"// Deadlock Mod Manager - End",
		"Game                citadel/addons",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("gameinfo.gi does not hold %q:\n%s", want, data)
		}
	}
	if !a.state().gameInfo.ModPaths {
		t.Error("the app does not report the game as set up")
	}
}

// A mod enabled into a shard the game was not searching would sit there doing
// nothing, so the game's configuration follows every change.
func TestEnablingAModUpdatesTheGameConfiguration(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	tt := show(t, a, install, profile)

	if err := tt.Click("启用 snd-42"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })

	data, err := os.ReadFile(install.GameInfoPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Game                citadel/addons") {
		t.Errorf("enabling a mod left the game not searching its directory:\n%s", data)
	}
	if !a.state().gameInfo.ModPaths {
		t.Error("the app does not report the game as set up")
	}
}

// A switch that cannot move the files reports why, and leaves the list showing
// the disk rather than the state the user asked for.
func TestTheSwitchReportsWhyItCouldNotMoveTheFiles(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "snd-42", "voice.vpk")
	// Take the file away, so the rename has nothing to move.
	if err := os.Remove(filepath.Join(profile.Dir, "snd-42_voice.vpk")); err != nil {
		t.Fatal(err)
	}
	a.install = install
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.mods = mods

	a.setEnabled("snd-42", true)
	waitFor(t, "the switch to finish", func() bool { return !a.installed().reading })

	if a.installed().err == "" {
		t.Error("a switch that moved nothing reported no error")
	}
	if got := a.installed().list; len(got) != 1 || got[0].Enabled {
		t.Errorf("the list shows %+v, want the mod still disabled", got)
	}
}

func TestSettingsShowsTheInstall(t *testing.T) {
	a := newApp()
	a.install = &game.Install{
		GamePath:    "/games/Deadlock",
		SteamRoot:   "/steam",
		LibraryPath: "/mnt/games/SteamLibrary",
		BuildID:     "20260101",
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"/games/Deadlock", "/mnt/games/SteamLibrary", "/steam", "20260101", "自动检测"} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show %q: %q", want, tt.Texts())
		}
	}
}

// A directory the user pinned is named as such, and the Steam fields, which
// were never read, are left out.
func TestSettingsNamesAPinnedDirectory(t *testing.T) {
	a := newApp()
	a.pinned = "/games/Deadlock"
	a.install = &game.Install{GamePath: "/games/Deadlock"}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("手动指定") {
		t.Errorf("the settings page does not say the directory was pinned: %q", tt.Texts())
	}
	if tt.HasText("自动检测") {
		t.Errorf("the settings page calls a pinned directory detected: %q", tt.Texts())
	}
}

func TestSettingsShowsTheLookingState(t *testing.T) {
	a := newApp()
	a.looking = true
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("正在查找…") {
		t.Errorf("the settings page does not say it is looking: %q", tt.Texts())
	}
}

func TestSettingsShowsTheError(t *testing.T) {
	a := newApp()
	a.lookErr = "no Steam installation: not found"
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if !tt.HasText("no Steam installation") {
		t.Errorf("the settings page does not show the error: %q", tt.Texts())
	}
}

// A directory that is not a Deadlock installation is refused, and does not
// become the one the app uses.
func TestUsePathRejectsADirectoryThatIsNotDeadlock(t *testing.T) {
	a := newApp()
	settingsPath(t, a)
	a.pathInput = t.TempDir()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("使用此目录"); err != nil {
		t.Fatal(err)
	}
	s := waitForLook(t, a)
	if s.lookErr == "" {
		t.Error("the settings page accepted a directory that is not a Deadlock installation")
	}
	if s.pinned != "" || s.install != nil {
		t.Errorf("the app took a directory it refused: pinned=%q install=%+v", s.pinned, s.install)
	}
}

// A directory the user picks is used, and remembered for the next run.
func TestUsePathAcceptsADeadlockDirectory(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	a.pathInput = gamePath
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("使用此目录"); err != nil {
		t.Fatal(err)
	}
	s := waitForLook(t, a)
	if s.lookErr != "" {
		t.Fatalf("the settings page refused a Deadlock directory: %s", s.lookErr)
	}
	if s.install == nil || s.install.GamePath != gamePath {
		t.Fatalf("install = %+v, want the game at %q", s.install, gamePath)
	}
	if s.pinned != gamePath {
		t.Errorf("pinned = %q, want %q", s.pinned, gamePath)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.GamePath != gamePath {
		t.Errorf("the saved settings hold %q, want %q", saved.GamePath, gamePath)
	}
}

// The field is the only place a directory comes from, so an empty one is a
// mistake to point out rather than a request to detect the game.
func TestUsePathRefusesAnEmptyField(t *testing.T) {
	a := newApp()
	settingsPath(t, a)
	a.pathInput = "   "
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("使用此目录"); err != nil {
		t.Fatal(err)
	}
	if s := a.state(); s.lookErr == "" {
		t.Error("an empty directory was accepted")
	}
	if s := a.state(); s.looking {
		t.Error("an empty directory started a lookup")
	}
}

// 重新检测 is the way back from a pinned directory to detection.
func TestDetectDropsThePinnedDirectory(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	a.pinned = gamePath
	a.pathInput = gamePath
	a.install = &game.Install{GamePath: gamePath}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("重新检测"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pinned directory to be dropped", func() bool { return a.state().pinned == "" })
	waitForLook(t, a)

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.GamePath != "" {
		t.Errorf("the saved settings still hold %q after 重新检测", saved.GamePath)
	}
}

// The directory the user chose last time is the one the app starts with.
func TestStartUsesTheSavedDirectory(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	if err := (config.Config{GamePath: gamePath}).Save(path); err != nil {
		t.Fatal(err)
	}

	a.start()
	s := waitForLook(t, a)
	if s.lookErr != "" {
		t.Fatalf("starting with a saved directory failed: %s", s.lookErr)
	}
	if s.install == nil || s.install.GamePath != gamePath {
		t.Fatalf("install = %+v, want the game at %q", s.install, gamePath)
	}
	if a.pathInput != gamePath {
		t.Errorf("the field holds %q, want the saved %q", a.pathInput, gamePath)
	}
}

// A settings file that cannot be read is reported rather than passed over: the
// directory the user chose last time is not in effect.
func TestStartReportsSettingsItCannotRead(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.start()
	s := waitForLook(t, a)
	if s.lookErr == "" {
		t.Error("starting with an unreadable settings file said nothing")
	}
}
