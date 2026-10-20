package addons

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// unpacked makes a directory holding the named files, as an archive that has
// been unpacked would leave it, and returns it.
func unpacked(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// names returns what FindVpks made of the VPKs under dir, as a map of the path
// each one was found at to the name it installs under.
func names(t *testing.T, dir, archive string) map[string]string {
	t.Helper()
	files, err := FindVpks(dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(dir, f.Path)
		if err != nil {
			t.Fatal(err)
		}
		got[filepath.ToSlash(rel)] = f.Name
	}
	return got
}

// A mod of one file keeps the name its author gave it: there is nothing for it
// to be told apart from.
func TestFindVpksKeepsAFileName(t *testing.T) {
	dir := unpacked(t, map[string]string{
		"pak01_dir.vpk": "vpk",
		"readme.txt":    "not a vpk",
	})

	got := names(t, dir, "Red Skin.zip")
	if len(got) != 1 || got["pak01_dir.vpk"] != "pak01_dir.vpk" {
		t.Errorf("FindVpks named the files %v, want pak01_dir.vpk as itself", got)
	}
}

// The variants an author publishes usually all ship a file called
// pak01_dir.vpk, and two of those would otherwise install as one file.
func TestFindVpksTellsVariantsApart(t *testing.T) {
	dir := unpacked(t, map[string]string{
		"red/pak01_dir.vpk":  "red",
		"blue/pak01_dir.vpk": "blue",
	})

	got := names(t, dir, "Yamato Redesign.zip")
	want := map[string]string{
		"red/pak01_dir.vpk":  "pak01-yamato-redesign-red_dir.vpk",
		"blue/pak01_dir.vpk": "pak01-yamato-redesign-blue_dir.vpk",
	}
	for path, name := range want {
		if got[path] != name {
			t.Errorf("%s installs as %q, want %q", path, got[path], name)
		}
	}
}

// Two files whose folders reduce to the same qualifier still need names of
// their own, and the second is numbered rather than left to collide.
func TestFindVpksNumbersNamesThatStillCollide(t *testing.T) {
	dir := unpacked(t, map[string]string{
		"A/pak01_dir.vpk": "one",
		"a/pak01_dir.vpk": "two",
	})

	got := names(t, dir, "mod.zip")
	want := map[string]string{
		"A/pak01_dir.vpk": "pak01-mod-a_dir.vpk",
		"a/pak01_dir.vpk": "pak01-mod-a-2_dir.vpk",
	}
	for path, name := range want {
		if got[path] != name {
			t.Errorf("%s installs as %q, want %q", path, got[path], name)
		}
	}
}

// A mod of several files keeps all of them, and the names are worked out from
// the files alone, so the same archive always gives the same ones.
func TestFindVpksIsTheSameEveryTime(t *testing.T) {
	dir := unpacked(t, map[string]string{
		"pak01_dir.vpk":       "a",
		"pak01_000.vpk":       "b",
		"extra/pak01_dir.vpk": "c",
	})

	first := names(t, dir, "Mod.zip")
	for range 5 {
		again := names(t, dir, "Mod.zip")
		for path, name := range first {
			if again[path] != name {
				t.Fatalf("%s was named %q and then %q", path, name, again[path])
			}
		}
	}
	if len(first) != 3 {
		t.Errorf("FindVpks found %d files, want 3", len(first))
	}
	// The order the files are enabled in is the order of the paths, which is
	// what decides which of them the game loads first.
	files, err := FindVpks(dir, "Mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f.Path)
		paths = append(paths, filepath.ToSlash(rel))
	}
	if !slices.IsSorted(paths) {
		t.Errorf("the files came back in the order %v, want them sorted", paths)
	}
}

// An archive with no VPK in it is not a mod, and saying so is the caller's job,
// so nothing is invented here.
func TestFindVpksWithNothingToFind(t *testing.T) {
	dir := unpacked(t, map[string]string{"readme.txt": "no mod here"})

	files, err := FindVpks(dir, "mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("FindVpks found %v in an archive with no VPKs", files)
	}
}

// Installing puts the files where the profile keeps them while they are off,
// and records them in the manifest.
func TestInstall(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))
	dir := unpacked(t, map[string]string{"mod/pak01_dir.vpk": "the vpk"})

	files, err := FindVpks(dir, "mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install("599927", files); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(p.Dir, "599927_pak01_dir.vpk"))
	if err != nil {
		t.Fatalf("the file was not installed: %v", err)
	}
	if string(data) != "the vpk" {
		t.Errorf("the installed file holds %q", data)
	}

	m, err := Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := m.Mods["599927"]
	if !ok {
		t.Fatalf("the manifest holds %v, want the mod installed", m.Mods)
	}
	if entry.Enabled {
		t.Error("a mod was installed turned on")
	}
	if entry.Shard != FirstShard {
		t.Errorf("the mod is filed under shard %d, want %d", entry.Shard, FirstShard)
	}
	if !slices.Equal(entry.DisabledVpks, []string{"599927_pak01_dir.vpk"}) {
		t.Errorf("DisabledVpks = %v", entry.DisabledVpks)
	}
	if !slices.Equal(entry.OriginalVpkNames, []string{"pak01_dir.vpk"}) {
		t.Errorf("OriginalVpkNames = %v", entry.OriginalVpkNames)
	}
}

// What is installed is what the mods page reads back, which is the whole point
// of writing the manifest the way the Tauri app does.
func TestInstallShowsUpInTheModList(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))
	dir := unpacked(t, map[string]string{"mod/pak01_dir.vpk": "the vpk"})
	files, err := FindVpks(dir, "mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install("599927", files); err != nil {
		t.Fatal(err)
	}

	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].ID != "599927" || mods[0].Enabled {
		t.Fatalf("the mods read as %+v, want the installed mod, turned off", mods)
	}
	if len(mods[0].Missing) != 0 {
		t.Errorf("the mod is reported as missing %v", mods[0].Missing)
	}
}

// Installing and then turning the mod on is the whole of what the store does,
// and it has to leave the game able to load it.
func TestInstallThenEnable(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))
	dir := unpacked(t, map[string]string{
		"a/pak01_dir.vpk": "one",
		"b/pak01_dir.vpk": "two",
	})
	files, err := FindVpks(dir, "Mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install("599927", files); err != nil {
		t.Fatal(err)
	}
	if err := p.SetEnabled("599927", true); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"pak01_dir.vpk", "pak02_dir.vpk"} {
		if _, err := os.Stat(filepath.Join(p.Dir, name)); err != nil {
			t.Errorf("%s is not where the game loads it from: %v", name, err)
		}
	}
	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || !mods[0].Enabled || mods[0].Pak != 1 {
		t.Errorf("the mod reads as %+v, want it enabled at pak01", mods)
	}
}

// A mod already installed is not installed over itself: the user asked for it
// twice, and what is there is what they already have.
func TestInstallRefusesAModThatIsAlreadyThere(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))
	dir := unpacked(t, map[string]string{"pak01_dir.vpk": "the vpk"})
	files, err := FindVpks(dir, "mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install("599927", files); err != nil {
		t.Fatal(err)
	}

	if err := p.Install("599927", files); err == nil {
		t.Fatal("a mod was installed twice")
	}
}

// The manifest is written last, so a failure partway leaves the profile as it
// was rather than describing files that are not there.
func TestInstallLeavesNothingBehindWhenItFails(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))
	dir := unpacked(t, map[string]string{
		"a/pak01_dir.vpk": "one",
		"b/pak01_dir.vpk": "two",
	})
	files, err := FindVpks(dir, "Mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	// Take one of them away, so the copy fails after the other has been made.
	if err := os.Remove(files[1].Path); err != nil {
		t.Fatal(err)
	}

	if err := p.Install("599927", files); err == nil {
		t.Fatal("installing a mod with a file that is not there reported no error")
	}
	if _, err := os.Stat(p.ManifestPath()); err == nil {
		t.Error("the manifest was written for a mod that was not installed")
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var left []string
		for _, e := range entries {
			left = append(left, e.Name())
		}
		t.Errorf("the profile holds %v after a failed install", left)
	}
}

// A name out of a stranger's archive is checked rather than trusted: it becomes
// part of a path.
func TestInstallRefusesANameItShouldNotWrite(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))

	for _, name := range []string{
		"..",
		"../evil.vpk",
		"a/b.vpk",
		`a\b.vpk`,
		"C:evil.vpk",
		"evil.txt",
		"",
	} {
		t.Run(name, func(t *testing.T) {
			err := p.Install("599927", []File{{Path: "/nowhere", Name: name}})
			if err == nil {
				t.Fatalf("a file named %q was installed", name)
			}
			if _, err := os.Stat(p.ManifestPath()); err == nil {
				t.Error("the manifest was written for a name that was refused")
			}
		})
	}
}

// The id is part of a file name and comes from a catalogue, so it is checked
// for the same reason.
func TestInstallRefusesAnIDItShouldNotUse(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))

	for _, id := range []string{"", "..", "a/b", `a\b`, "..599927"} {
		t.Run(id, func(t *testing.T) {
			err := p.Install(id, []File{{Path: "/nowhere", Name: "pak01_dir.vpk"}})
			if err == nil {
				t.Fatalf("a mod was installed under the id %q", id)
			}
		})
	}
}

// Removing takes the mod out of the profile, whether it is on or off, which is
// the way back from installing one by mistake.
func TestRemove(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[on], func(t *testing.T) {
			p := Open(filepath.Join(t.TempDir(), "addons"))
			dir := unpacked(t, map[string]string{"pak01_dir.vpk": "the vpk"})
			files, err := FindVpks(dir, "mod.zip")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Install("599927", files); err != nil {
				t.Fatal(err)
			}
			if err := p.SetEnabled("599927", on); err != nil {
				t.Fatal(err)
			}

			if err := p.Remove("599927"); err != nil {
				t.Fatal(err)
			}

			mods, err := p.Read()
			if err != nil {
				t.Fatal(err)
			}
			if len(mods) != 0 {
				t.Errorf("the profile still holds %+v", mods)
			}
			entries, err := os.ReadDir(p.Dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != ManifestName {
					t.Errorf("%s was left behind", e.Name())
				}
			}
		})
	}
}

func TestRemoveRefusesAModThatIsNotThere(t *testing.T) {
	p := Open(filepath.Join(t.TempDir(), "addons"))
	if err := p.Remove("599927"); err == nil {
		t.Error("removing a mod that was never installed reported no error")
	}
}

func TestSlug(t *testing.T) {
	for _, tt := range []struct {
		in, want string
	}{
		{"Red Skin", "red-skin"},
		{"Red Skin.zip", "red-skin-zip"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"a--b", "a-b"},
		{"a.b/c", "a-b-c"},
		{"Yamato 重做", "yamato-重做"},
		{"!!!", ""},
		{"", ""},
		{strings.Repeat("a", 60), strings.Repeat("a", 48)},
	} {
		if got := slug(tt.in); got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQualifyName(t *testing.T) {
	for _, tt := range []struct {
		name, qualifier, want string
	}{
		{"pak01_dir.vpk", "red", "pak01-red_dir.vpk"},
		{"pak01.vpk", "red", "pak01-red.vpk"},
		{"pak01_000.vpk", "red", "pak01_000-red.vpk"},
		{"pak01_dir.vpk", "", "pak01_dir.vpk"},
		{"readme.txt", "red", "readme.txt-red"},
	} {
		if got := qualifyName(tt.name, tt.qualifier); got != tt.want {
			t.Errorf("qualifyName(%q, %q) = %q, want %q", tt.name, tt.qualifier, got, tt.want)
		}
	}
}

func TestPlainVpkName(t *testing.T) {
	for _, tt := range []struct {
		name string
		want bool
	}{
		{"pak01_dir.vpk", true},
		{"pak01_000.vpk", true},
		{"PAK01_DIR.VPK", true},
		{"", false},
		{"..", false},
		{"a/b.vpk", false},
		{`a\b.vpk`, false},
		{"C:evil.vpk", false},
		{"evil.txt", false},
		{"vpk", false},
		{"a<b.vpk", false},
		{"a\x00b.vpk", false},
	} {
		if got := plainVpkName(tt.name); got != tt.want {
			t.Errorf("plainVpkName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
