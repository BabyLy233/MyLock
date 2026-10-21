package game

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vanilla is gameinfo.gi roughly as the game ships it: the section the app has
// to put the mod directories into, among the rest of the file.
const vanilla = `"GameInfo"
{
	game		"Citadel"
	title		"Deadlock"
	FileSystem
	{
		SteamAppId		1422450
		SearchPaths
		{
			Game_UILanguage		citadel_*LANGUAGE*
			Game			citadel
			Mod			citadel
			Write			citadel
			Game			core
			Write			core
		}
	}
}
`

// wantSection is the block the app writes, byte for byte as the Tauri app does.
// Two of the lines carry trailing spaces, which is how the game's own file has
// them; the quoting keeps them from being trimmed away by an editor.
const wantSection = "\n" +
	"\t\tSearchPaths\n" +
	"        {  \n" +
	"            Game_UILanguage     citadel_*LANGUAGE*\n" +
	"            Game_LowViolence    citadel_lv\n" +
	"            Game                citadel/addons\n" +
	"            Mod                 citadel\n" +
	"            Write               citadel          \n" +
	"            Game                citadel\n" +
	"            Mod                 core\n" +
	"            Write               core\n" +
	"            Game                core        \n" +
	"        }"

// wantVanillaSection is the block a launch without mods leaves, line for line
// as the Tauri app writes it. It is the block above with the mod directory and
// the two Mod lines taken out of it, and without the newline the Tauri app's
// opens with, which it never replaces and so adds one more of each time.
const wantVanillaSection = "\t\tSearchPaths\n" +
	"        {  \n" +
	"            Game_UILanguage     citadel_*LANGUAGE*\n" +
	"            Game_LowViolence    citadel_lv\n" +
	"            Game                citadel\n" +
	"            Write               citadel          \n" +
	"            Game                citadel\n" +
	"            Write               core\n" +
	"            Game                core        \n" +
	"        }"

// installation builds a Deadlock installation holding that gameinfo.gi, and
// returns it with the path of the file.
func installation(t *testing.T, content string) (*Install, string) {
	t.Helper()
	gamePath := filepath.Join(t.TempDir(), "Deadlock")
	install := &Install{GamePath: gamePath}
	if err := os.MkdirAll(filepath.Dir(install.GameInfoPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install.GameInfoPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return install, install.GameInfoPath()
}

// read returns what the file holds now.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPatchPutsTheSectionInAVanillaFile(t *testing.T) {
	install, path := installation(t, vanilla)

	changed, err := install.PatchGameInfo([]string{"citadel/addons"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("patching a vanilla file reported no change")
	}

	got := read(t, path)
	want := markerStart + "\n" + wantSection + "\n" + markerEnd
	if !strings.Contains(got, want) {
		t.Errorf("the file does not hold the section the Tauri app writes:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// The rest of the file is left alone.
	for _, keep := range []string{`"GameInfo"`, `title		"Deadlock"`, "SteamAppId"} {
		if !strings.Contains(got, keep) {
			t.Errorf("patching dropped %q from the file:\n%s", keep, got)
		}
	}
	// The section it replaced is gone, rather than sitting beside the new one.
	if strings.Count(got, "SearchPaths") != 1 {
		t.Errorf("the file holds %d SearchPaths sections, want 1:\n%s", strings.Count(got, "SearchPaths"), got)
	}
}

// Patching a file that already holds the section must change nothing, or every
// launch would rewrite the game's configuration.
func TestPatchChangesNothingTheSecondTime(t *testing.T) {
	install, path := installation(t, vanilla)
	paths := []string{"citadel/addons", "citadel/addons2"}

	if _, err := install.PatchGameInfo(paths); err != nil {
		t.Fatal(err)
	}
	first := read(t, path)

	changed, err := install.PatchGameInfo(paths)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("patching an already patched file reported a change")
	}
	if got := read(t, path); got != first {
		t.Errorf("the file changed on a second patch:\n--- first ---\n%s\n--- second ---\n%s", first, got)
	}
}

// A mod enabled into another shard changes what the game has to search, and the
// section is updated in place rather than added to.
func TestPatchUpdatesThePaths(t *testing.T) {
	install, path := installation(t, vanilla)

	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}
	if _, err := install.PatchGameInfo([]string{"citadel/addons", "citadel/addons2"}); err != nil {
		t.Fatal(err)
	}

	got := read(t, path)
	if !strings.Contains(got, "Game                citadel/addons2") {
		t.Errorf("the second shard is not searched:\n%s", got)
	}
	if n := strings.Count(got, markerStart); n != 1 {
		t.Errorf("the file holds %d start markers, want 1:\n%s", n, got)
	}
	// The first shard comes first, as the Tauri app orders them.
	first := strings.Index(got, "Game                citadel/addons\n")
	second := strings.Index(got, "Game                citadel/addons2\n")
	if first < 0 || second < 0 || first > second {
		t.Errorf("the shards are out of order, at %d and %d:\n%s", first, second, got)
	}
}

// A launch without mods leaves the game's own search paths behind, and nothing
// of this app's: the game is to load nothing but itself.
func TestPatchVanillaPutsBackWhatTheGameShipsWith(t *testing.T) {
	install, path := installation(t, vanilla)
	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}

	changed, err := install.PatchGameInfoVanilla()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("putting the vanilla paths back reported no change")
	}

	got := read(t, path)
	if !strings.Contains(got, wantVanillaSection) {
		t.Errorf("the file does not hold the block the Tauri app writes:\n--- got ---\n%s\n--- want ---\n%s", got, wantVanillaSection)
	}
	// Nothing of the app's is left: no mod directory is searched, no mod is let
	// to stand in for the game's own files, and there is no section of ours for
	// the next patch to find.
	for _, gone := range []string{"citadel/addons", "Mod", markerStart, markerEnd} {
		if strings.Contains(got, gone) {
			t.Errorf("the file still holds %q:\n%s", gone, got)
		}
	}
	// The rest of the file is left alone.
	for _, keep := range []string{`"GameInfo"`, `title		"Deadlock"`, "SteamAppId"} {
		if !strings.Contains(got, keep) {
			t.Errorf("patching dropped %q from the file:\n%s", keep, got)
		}
	}
	if n := strings.Count(got, "SearchPaths"); n != 1 {
		t.Errorf("the file holds %d SearchPaths sections, want 1:\n%s", n, got)
	}
}

// Patching vanilla twice must change nothing the second time, or every launch
// without mods would rewrite the game's configuration.
func TestPatchVanillaChangesNothingTheSecondTime(t *testing.T) {
	install, path := installation(t, vanilla)
	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}
	if _, err := install.PatchGameInfoVanilla(); err != nil {
		t.Fatal(err)
	}
	first := read(t, path)

	changed, err := install.PatchGameInfoVanilla()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("patching an already vanilla file reported a change")
	}
	if got := read(t, path); got != first {
		t.Errorf("the file changed on a second patch:\n--- first ---\n%s\n--- second ---\n%s", first, got)
	}
}

// Mods are turned back on after a launch without them, which is what the file a
// vanilla patch leaves has to allow.
func TestPatchFindsItsWayBackAfterAVanillaLaunch(t *testing.T) {
	install, path := installation(t, vanilla)
	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}
	if _, err := install.PatchGameInfoVanilla(); err != nil {
		t.Fatal(err)
	}

	changed, err := install.PatchGameInfo([]string{"citadel/addons", "citadel/addons2"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("patching a file left vanilla reported no change")
	}

	got := read(t, path)
	for _, want := range []string{
		markerStart,
		"Game                citadel/addons\n",
		"Game                citadel/addons2\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the file does not hold %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "SearchPaths"); n != 1 {
		t.Errorf("the file holds %d SearchPaths sections, want 1:\n%s", n, got)
	}
}

// A game update puts the file back the way it shipped, taking the section with
// it. Patching has to put it back, or every mod quietly stops loading.
func TestPatchRebuildsAfterAGameUpdate(t *testing.T) {
	install, path := installation(t, vanilla)
	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}

	// The update replaces the file.
	if err := os.WriteFile(path, []byte(vanilla), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if status.Markers {
		t.Fatal("the section is still there after the file was replaced")
	}

	changed, err := install.PatchGameInfo([]string{"citadel/addons", "citadel/addons2"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("patching a replaced file reported no change")
	}
	got := read(t, path)
	if !strings.Contains(got, markerStart) || !strings.Contains(got, "citadel/addons2") {
		t.Errorf("the section was not put back:\n%s", got)
	}
}

// The backup is the file as it was before the first change, and is not
// overwritten by later ones: it is what puts the game back the way it shipped.
func TestPatchKeepsTheFileAsItWas(t *testing.T) {
	install, path := installation(t, vanilla)

	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}
	if _, err := install.PatchGameInfo([]string{"citadel/addons", "citadel/addons2"}); err != nil {
		t.Fatal(err)
	}

	if got := read(t, path+".bak"); got != vanilla {
		t.Errorf("the backup is not the file as it shipped:\n%s", got)
	}
	status, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Backup {
		t.Error("ReadGameInfo does not report the backup")
	}
}

// A file with Windows line endings keeps them, rather than ending up with both
// kinds in one file.
func TestPatchKeepsTheLineEndings(t *testing.T) {
	install, path := installation(t, strings.ReplaceAll(vanilla, "\n", "\r\n"))

	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}

	got := read(t, path)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("the file holds line endings the rest of it does not:\n%q", got)
	}
	if !strings.Contains(got, markerStart+"\r\n") {
		t.Errorf("the section was not written with the file's endings:\n%q", got)
	}
}

// A profile with nothing enabled still gets the path the game ships with, so
// that patching never leaves the section empty.
func TestPatchWithoutPathsUsesTheDefault(t *testing.T) {
	install, path := installation(t, vanilla)

	if _, err := install.PatchGameInfo(nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, "Game                citadel/addons") {
		t.Errorf("the default path is not searched:\n%s", got)
	}
}

// A file with no SearchPaths section is not something to guess at: writing one
// in could leave the game unable to start.
func TestPatchRefusesAFileWithoutSearchPaths(t *testing.T) {
	install, path := installation(t, `"GameInfo" { "game" "Citadel" }`)
	before := read(t, path)

	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err == nil {
		t.Error("patching a file with no SearchPaths section reported no error")
	}
	if got := read(t, path); got != before {
		t.Error("the file was written to anyway")
	}
}

// A section that was opened and never closed is refused too.
func TestPatchRefusesAnUnclosedSection(t *testing.T) {
	install, _ := installation(t, "\"GameInfo\"\n{\n\tSearchPaths\n\t{\n\t\tGame citadel\n")

	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err == nil {
		t.Error("patching an unclosed section reported no error")
	}
}

func TestReadGameInfo(t *testing.T) {
	install, path := installation(t, vanilla)

	status, err := install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if status.Path != path {
		t.Errorf("Path = %q, want %q", status.Path, path)
	}
	if status.Markers {
		t.Error("a vanilla file is reported as holding the section")
	}
	if status.ModPaths {
		t.Error("a vanilla file is reported as searching the mod directories")
	}
	if status.Backup {
		t.Error("a vanilla file is reported as having a backup")
	}

	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err != nil {
		t.Fatal(err)
	}
	status, err = install.ReadGameInfo()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Markers || !status.ModPaths || !status.Backup {
		t.Errorf("after patching: %+v, want the section, the paths and the backup", status)
	}
}

// Patching a file that is not there is a failure to report, not a file to
// create: the game directory is wrong.
func TestPatchWithoutAGameInfoFile(t *testing.T) {
	install := &Install{GamePath: t.TempDir()}
	if _, err := install.PatchGameInfo([]string{"citadel/addons"}); err == nil {
		t.Error("patching a missing file reported no error")
	}
}
