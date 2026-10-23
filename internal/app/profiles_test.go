package app

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/game"
)

// profileApp is an app pointed at a game, with the default profile as the one
// in use and a settings file of its own.
func profileApp(t *testing.T) (*app, *game.Install, addons.Profile, string) {
	t.Helper()
	a := newApp()
	path := settingsPath(t, a)
	install, profile := fakeGame(t)
	a.install = install
	if mods, err := profile.Read(); err == nil {
		a.mods = mods
	}
	a.running = func() bool { return false }
	return a, install, profile, path
}

// waitForProfileOp waits for the profile operation in flight to finish, and
// reports what it left.
func waitForProfileOp(t *testing.T, a *app) (folder string, entries []config.Profile, present map[string]bool, err string) {
	t.Helper()
	waitFor(t, "the profile operation to finish", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return !a.reading
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.profileFolder, slices.Clone(a.profiles), maps.Clone(a.profilePresent), a.profilesErr
}

// makeNamedProfile makes a profile folder and puts one disabled mod in it, as
// an install into that profile would have.
func makeNamedProfile(t *testing.T, install *game.Install, folder, modID string) addons.Profile {
	t.Helper()
	p := addons.OpenNamed(install.AddonsDir(), folder)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	addMod(t, p, modID, modID+".vpk")
	return p
}

// A new profile is a folder of its own and an entry in the profiles file, and
// making it does not switch to it: the user asked for one to exist, not to
// leave the mods they have in use.
func TestCreatingAProfileMakesAFolderAndRemembersIt(t *testing.T) {
	a, install, _, path := profileApp(t)

	a.createProfile("测试服", "用来试模组")
	folder, entries, present, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("creating a profile failed: %s", msg)
	}
	if folder != "" {
		t.Errorf("creating a profile switched to %q", folder)
	}
	if len(entries) != 1 {
		t.Fatalf("the app remembers %d profiles, want one", len(entries))
	}
	entry := entries[0]
	if entry.Name != "测试服" || entry.Description != "用来试模组" {
		t.Errorf("the profile is %+v, want the name and description given", entry)
	}
	if !validProfileFolder(entry.Folder) || !strings.HasPrefix(entry.Folder, "profile_") {
		t.Errorf("the profile folder is %q, not one this app may touch", entry.Folder)
	}
	if !present[entry.Folder] {
		t.Errorf("the new profile is not marked as being on disk")
	}
	info, err := os.Stat(filepath.Join(install.AddonsDir(), entry.Folder))
	if err != nil || !info.IsDir() {
		t.Errorf("the profile folder is not there: %v", err)
	}

	book, err := config.LoadProfiles(config.ProfilesPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Profiles) != 1 || book.Profiles[0].Folder != entry.Folder {
		t.Errorf("the profiles file holds %+v, want %q", book.Profiles, entry.Folder)
	}
}

// Two profiles with one name would be told apart only by their folder names,
// so a duplicate is refused before a folder is made.
func TestCreatingAProfileRefusesADuplicateName(t *testing.T) {
	a, install, _, _ := profileApp(t)
	folder := "profile_1_abcd_测试服"
	if err := os.MkdirAll(filepath.Join(install.AddonsDir(), folder), 0o755); err != nil {
		t.Fatal(err)
	}
	a.profiles = []config.Profile{{Folder: folder, Name: "测试服"}}

	a.createProfile("测试服", "")
	_, entries, _, msg := waitForProfileOp(t, a)
	if !strings.Contains(msg, "已经有一个") {
		t.Errorf("creating a duplicate said %q, want a refusal", msg)
	}
	if len(entries) != 1 {
		t.Errorf("the app remembers %d profiles after a refusal, want one", len(entries))
	}
}

// Switching writes the new profile's directories into gameinfo.gi, reads its
// mods and remembers it for the next run.
func TestSwitchingProfilesWritesTheirSearchPathsAndMods(t *testing.T) {
	a, install, def, path := profileApp(t)
	addMod(t, def, "default-mod", "default.vpk")

	a.createProfile("测试服", "")
	_, entries, _, msg := waitForProfileOp(t, a)
	if msg != "" || len(entries) != 1 {
		t.Fatalf("creating the profile failed: %s (%d entries)", msg, len(entries))
	}
	folder := entries[0].Folder
	p := makeNamedProfile(t, install, folder, "test-mod")
	if err := p.SetEnabled("test-mod", true); err != nil {
		t.Fatal(err)
	}

	a.switchProfile(folder)
	active, _, _, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("switching profiles failed: %s", msg)
	}
	if active != folder {
		t.Errorf("the profile in use is %q, want %q", active, folder)
	}
	state := a.installed()
	if len(state.list) != 1 || state.list[0].ID != "test-mod" {
		t.Errorf("the mods page lists %+v, want the new profile's test-mod", state.list)
	}

	info, err := os.ReadFile(filepath.Join(install.GamePath, "game", "citadel", "gameinfo.gi"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(info), "citadel/addons/"+folder) {
		t.Errorf("gameinfo.gi does not search the new profile:\n%s", info)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProfileFolder != folder {
		t.Errorf("the settings file holds profile %q, want %q", saved.ProfileFolder, folder)
	}

	book, err := config.LoadProfiles(config.ProfilesPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Profiles) != 1 || book.Profiles[0].LastUsed.IsZero() {
		t.Errorf("the profiles file does not remember when the profile was used: %+v", book.Profiles)
	}
}

// Switching back to the default takes its mods and its paths again.
func TestSwitchingBackToTheDefaultProfile(t *testing.T) {
	a, install, def, _ := profileApp(t)
	addMod(t, def, "default-mod", "default.vpk")
	addMod(t, def, "other-mod", "other.vpk")
	folder := "profile_1_abcd_测试"
	makeNamedProfile(t, install, folder, "test-mod")

	a.mu.Lock()
	a.profileFolder = folder
	a.mu.Unlock()

	a.switchProfile("")
	active, _, _, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("switching back failed: %s", msg)
	}
	if active != "" {
		t.Errorf("the profile in use is %q, want the default", active)
	}
	state := a.installed()
	if len(state.list) != 2 {
		t.Errorf("the mods page lists %+v, want the default profile's two mods", state.list)
	}
	info, err := os.ReadFile(filepath.Join(install.GamePath, "game", "citadel", "gameinfo.gi"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(info), folder) {
		t.Errorf("gameinfo.gi still names the profile left behind:\n%s", info)
	}
}

// The game holds the files of the profile in use, so a switch is refused while
// it runs.
func TestSwitchingProfilesIsRefusedWhileTheGameRuns(t *testing.T) {
	a, install, _, _ := profileApp(t)
	folder := "profile_1_abcd_测试"
	makeNamedProfile(t, install, folder, "test-mod")
	a.running = func() bool { return true }

	a.switchProfile(folder)
	active, _, _, msg := waitForProfileOp(t, a)
	if active != "" {
		t.Errorf("the app switched to %q while the game was running", active)
	}
	if !strings.Contains(msg, "游戏正在运行") {
		t.Errorf("the refusal says %q, want the running game", msg)
	}
}

// A switch is what every mod operation follows: the switch on a mod moves that
// profile's files and leaves the default profile's manifest alone.
func TestSwitchingAModActsOnTheProfileInUse(t *testing.T) {
	a, install, def, _ := profileApp(t)
	folder := "profile_1_abcd_测试"
	p := makeNamedProfile(t, install, folder, "test-mod")
	a.mu.Lock()
	a.profileFolder = folder
	a.mu.Unlock()

	a.setEnabled("test-mod", true)
	waitFor(t, "the switch to finish", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return !a.reading
	})

	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || !mods[0].Enabled {
		t.Errorf("the profile in use holds %+v, want test-mod enabled", mods)
	}
	defaultMods, err := def.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultMods) != 0 {
		t.Errorf("the default profile was changed too: %+v", defaultMods)
	}
	info, err := os.ReadFile(filepath.Join(install.GamePath, "game", "citadel", "gameinfo.gi"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(info), "citadel/addons/"+folder) {
		t.Errorf("gameinfo.gi does not search the profile in use:\n%s", info)
	}
}

// Deleting the profile in use first puts the default one back, so the game is
// never left searching a directory that is going away.
func TestDeletingTheActiveProfileSwitchesBackToTheDefault(t *testing.T) {
	a, install, def, path := profileApp(t)
	addMod(t, def, "default-mod", "default.vpk")
	folder := "profile_1_abcd_测试"
	makeNamedProfile(t, install, folder, "test-mod")
	a.mu.Lock()
	a.profileFolder = folder
	a.mu.Unlock()

	a.deleteProfile(folder)
	active, entries, _, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("deleting the profile failed: %s", msg)
	}
	if active != "" {
		t.Errorf("the profile in use is %q, want the default", active)
	}
	if len(entries) != 0 {
		t.Errorf("the app still remembers %+v", entries)
	}
	if _, statErr := os.Stat(filepath.Join(install.AddonsDir(), folder)); !os.IsNotExist(statErr) {
		t.Errorf("the profile folder survived the deletion: %v", statErr)
	}
	state := a.installed()
	if len(state.list) != 1 || state.list[0].ID != "default-mod" {
		t.Errorf("the mods page lists %+v, want the default profile's mod", state.list)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProfileFolder != "" {
		t.Errorf("the settings file still holds profile %q", saved.ProfileFolder)
	}
	info, err := os.ReadFile(filepath.Join(install.GamePath, "game", "citadel", "gameinfo.gi"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(info), folder) {
		t.Errorf("gameinfo.gi still searches the deleted profile:\n%s", info)
	}
}

// Deleting a profile also removes the copies of its files in the other shards,
// so that nothing of it is left for the game to find.
func TestDeletingAProfileRemovesEveryShard(t *testing.T) {
	a, install, _, _ := profileApp(t)
	folder := "profile_1_abcd_测试"
	makeNamedProfile(t, install, folder, "test-mod")
	shard := filepath.Join(filepath.Dir(install.AddonsDir()), "addons2", folder)
	if err := os.MkdirAll(shard, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shard, "pak01_dir.vpk"), []byte("vpk"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.profiles = []config.Profile{{Folder: folder, Name: "测试服"}}

	a.deleteProfile(folder)
	if _, _, _, err := waitForProfileOp(t, a); err != "" {
		t.Fatalf("deleting the profile failed: %s", err)
	}
	if _, statErr := os.Stat(filepath.Join(install.AddonsDir(), folder)); !os.IsNotExist(statErr) {
		t.Errorf("the profile folder survived the deletion: %v", statErr)
	}
	if _, statErr := os.Stat(shard); !os.IsNotExist(statErr) {
		t.Errorf("the shard copy survived the deletion: %v", statErr)
	}
}

// The default profile is the addons directory itself, so deleting it would
// take every profile with it.
func TestDeletingTheDefaultProfileIsRefused(t *testing.T) {
	a, install, _, _ := profileApp(t)
	a.deleteProfile("")

	_, _, _, err := waitForProfileOp(t, a)
	if !strings.Contains(err, "不能删除") {
		t.Errorf("deleting the default said %q, want a refusal", err)
	}
	if _, statErr := os.Stat(install.AddonsDir()); statErr != nil {
		t.Errorf("the addons directory was touched: %v", statErr)
	}
}

// Folders made by the Tauri build, or by hand, are found by their name rather
// than left out of the manager.
func TestSyncingProfilesFindsFoldersMadeOutsideTheApp(t *testing.T) {
	a, install, _, path := profileApp(t)
	folder := "profile_1700000000000_abcd_我的配置"
	if err := os.MkdirAll(filepath.Join(install.AddonsDir(), folder), 0o755); err != nil {
		t.Fatal(err)
	}

	a.syncProfiles()
	_, entries, present, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("syncing failed: %s", msg)
	}
	if len(entries) != 1 || entries[0].Folder != folder {
		t.Fatalf("syncing found %+v, want %q", entries, folder)
	}
	if entries[0].Name != "我的配置" {
		t.Errorf("the found profile is named %q, want the name from its folder", entries[0].Name)
	}
	if !present[folder] {
		t.Errorf("the found profile is not marked present")
	}

	book, err := config.LoadProfiles(config.ProfilesPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Profiles) != 1 || book.Profiles[0].Folder != folder {
		t.Errorf("the profiles file holds %+v, want %q", book.Profiles, folder)
	}
}

// A profile whose folder has gone stays on the list, marked missing, rather
// than disappearing with the mods the user is looking for.
func TestSyncingKeepsAProfileWhoseFolderHasGone(t *testing.T) {
	a, _, _, _ := profileApp(t)
	folder := "profile_1_abcd_不见了"
	a.profiles = []config.Profile{{Folder: folder, Name: "不见了"}}

	a.syncProfiles()
	_, entries, present, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("syncing failed: %s", msg)
	}
	if len(entries) != 1 || entries[0].Folder != folder {
		t.Errorf("syncing dropped the profile: %+v", entries)
	}
	if present[folder] {
		t.Errorf("the profile with no folder is marked present")
	}
}

// The directory the app opens with is the one the settings name, and the mods
// shown are that profile's.
func TestStartUsesTheSavedProfile(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	gamePath := deadlockDir(t)
	addonsDir := filepath.Join(gamePath, "game", "citadel", "addons")
	folder := "profile_1700000000000_abcd_测试"
	p := addons.OpenNamed(addonsDir, folder)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	addMod(t, p, "test-mod", "skin.vpk")
	if err := (config.Config{GamePath: gamePath, ProfileFolder: folder}).Save(path); err != nil {
		t.Fatal(err)
	}
	book := config.Profiles{Profiles: []config.Profile{{Folder: folder, Name: "测试服"}}}
	if err := book.Save(config.ProfilesPath(path)); err != nil {
		t.Fatal(err)
	}

	startApp(t, a)
	s := waitForLook(t, a)
	if s.lookErr != "" {
		t.Fatalf("starting with a saved profile failed: %s", s.lookErr)
	}
	a.mu.Lock()
	active := a.profileFolder
	a.mu.Unlock()
	if active != folder {
		t.Errorf("the app started with profile %q, want %q", active, folder)
	}
	state := a.installed()
	if len(state.list) != 1 || state.list[0].ID != "test-mod" {
		t.Errorf("the mods page lists %+v, want the saved profile's mod", state.list)
	}
}

// The mods page names the profile in use and opens the manager from there.
func TestTheModsPageShowsTheProfileAndOpensTheManager(t *testing.T) {
	a, install, def, _ := profileApp(t)
	a.profiles = []config.Profile{{Folder: "profile_1_abcd_测试", Name: "测试服"}}
	tt := show(t, a, install, def)

	if !tt.HasText("配置档：默认配置") {
		t.Errorf("the mods page does not name the profile in use: %q", tt.Texts())
	}
	if err := tt.Click("配置档管理"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"默认配置", "测试服", "新建配置档", "从目录同步"} {
		if !tt.HasText(want) {
			t.Errorf("the manager does not show %q: %q", want, tt.Texts())
		}
	}
}

// The heading and the launch buttons stay at the top: the header row spans the
// width, not the whole column, which would leave the upper half of the page
// empty and push everything else down.
func TestTheModsPageKeepsItsHeaderAtTheTop(t *testing.T) {
	a, install, def, _ := profileApp(t)
	tt := show(t, a, install, def)

	control, ok := tt.Find("配置档管理")
	if !ok {
		t.Fatal("the mods page does not show the profile control")
	}
	if control.Y > 100 {
		t.Errorf("the profile control is at y=%.0f, want it in the header", control.Y)
	}
	launch, ok := tt.Find("启动模组版游戏")
	if !ok {
		t.Fatal("the mods page does not show the launch button")
	}
	if launch.Y > 160 {
		t.Errorf("the launch button is at y=%.0f, want it above the mods", launch.Y)
	}
}

// The manager's new-profile dialog creates the profile with the name typed.
func TestTheManagerCreatesAProfile(t *testing.T) {
	a, install, def, _ := profileApp(t)
	tt := show(t, a, install, def)

	if err := tt.Click("配置档管理"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("新建配置档"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("新配置档是空的") {
		t.Fatalf("the new-profile dialog did not show: %q", tt.Texts())
	}
	a.newProfileName = "测试服"
	tt.Frame()
	if err := tt.Click("创建"); err != nil {
		t.Fatal(err)
	}
	_, entries, _, msg := waitForProfileOp(t, a)
	if msg != "" {
		t.Fatalf("creating the profile failed: %s", msg)
	}
	if len(entries) != 1 || entries[0].Name != "测试服" {
		t.Errorf("the app remembers %+v, want the typed name", entries)
	}
}

// Deleting a profile asks first, and cancelling leaves its files alone.
func TestDeletingAProfileAsksFirst(t *testing.T) {
	a, install, def, _ := profileApp(t)
	folder := "profile_1_abcd_测试服"
	makeNamedProfile(t, install, folder, "test-mod")
	a.profiles = []config.Profile{{Folder: folder, Name: "测试服"}}
	tt := show(t, a, install, def)

	if err := tt.Click("配置档管理"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("删除配置档“测试服”？") {
		t.Fatalf("no confirmation was shown: %q", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, err := os.Stat(filepath.Join(install.AddonsDir(), folder)); err != nil {
		t.Errorf("cancelling deleted the profile: %v", err)
	}
}
