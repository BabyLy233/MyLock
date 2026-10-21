package addons

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// citadel builds a citadel directory holding an empty addons directory, and
// returns the two.
func citadel(t *testing.T) (citadelDir, addonsDir string) {
	t.Helper()
	citadelDir = filepath.Join(t.TempDir(), "citadel")
	addonsDir = filepath.Join(citadelDir, "addons")
	if err := os.MkdirAll(addonsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return citadelDir, addonsDir
}

// touch puts a file of that name in dir, creating dir if it is not there.
func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("vpk"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDerivesTheCitadelDirectory(t *testing.T) {
	citadelDir, addonsDir := citadel(t)
	p := Open(addonsDir)
	if p.Dir != addonsDir {
		t.Errorf("Dir = %q, want %q", p.Dir, addonsDir)
	}
	if p.CitadelDir != citadelDir {
		t.Errorf("CitadelDir = %q, want %q", p.CitadelDir, citadelDir)
	}
	if p.Folder != "" {
		t.Errorf("Folder = %q, want empty for the default profile", p.Folder)
	}
	if want := filepath.Join(addonsDir, ManifestName); p.ManifestPath() != want {
		t.Errorf("ManifestPath() = %q, want %q", p.ManifestPath(), want)
	}
}

// Only the first shard is the profile directory; the rest are roots of their
// own beside it, which is why the game is told to search all of them.
func TestShardDir(t *testing.T) {
	citadelDir, addonsDir := citadel(t)
	p := Open(addonsDir)

	if got := p.ShardDir(1); got != addonsDir {
		t.Errorf("ShardDir(1) = %q, want the profile directory %q", got, addonsDir)
	}
	for _, shard := range []uint32{2, 10} {
		want := filepath.Join(citadelDir, "addons"+itoa(shard))
		if got := p.ShardDir(shard); got != want {
			t.Errorf("ShardDir(%d) = %q, want %q", shard, got, want)
		}
	}
}

// A named profile is a folder of its own under each shard root.
func TestShardDirOfANamedProfile(t *testing.T) {
	citadelDir, addonsDir := citadel(t)
	p := OpenNamed(addonsDir, "profile_1_cool-mods")

	if want := filepath.Join(addonsDir, "profile_1_cool-mods"); p.Dir != want {
		t.Errorf("Dir = %q, want %q", p.Dir, want)
	}
	if got := p.ShardDir(1); got != p.Dir {
		t.Errorf("ShardDir(1) = %q, want the profile directory %q", got, p.Dir)
	}
	want := filepath.Join(citadelDir, "addons2", "profile_1_cool-mods")
	if got := p.ShardDir(2); got != want {
		t.Errorf("ShardDir(2) = %q, want %q", got, want)
	}
}

// The enabled mods come back in the order the game loads them, which is what
// the user needs to reason about two mods that touch the same file.
func TestReadOrdersEnabledModsByLoadOrder(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	// Written out of order on purpose, and with the shard 2 mod first in the
	// manifest so that neither the map nor the file order can pass by accident.
	m := New()
	m.Mods["third"] = Entry{Enabled: true, Shard: 1, CurrentVpks: []string{"pak03_dir.vpk"}}
	m.Mods["first"] = Entry{Enabled: true, Shard: 1, CurrentVpks: []string{"pak01_dir.vpk"}}
	m.Mods["second"] = Entry{Enabled: true, Shard: 2, CurrentVpks: []string{"pak01_dir.vpk"}}
	m.Mods["off"] = Entry{DisabledVpks: []string{"off_skin.vpk"}}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}

	touch(t, p.Dir, "pak01_dir.vpk")
	touch(t, p.Dir, "pak03_dir.vpk")
	touch(t, p.ShardDir(2), "pak01_dir.vpk")
	touch(t, p.Dir, "off_skin.vpk")

	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, mod := range mods {
		ids = append(ids, mod.ID)
	}
	want := []string{"first", "third", "second", "off"}
	if len(ids) != len(want) {
		t.Fatalf("Read returned %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("Read returned %v, want %v", ids, want)
		}
	}

	if mods[0].Pak != 1 || mods[1].Pak != 3 {
		t.Errorf("the load order numbers are %d and %d, want 1 and 3", mods[0].Pak, mods[1].Pak)
	}
	if mods[3].Pak != 0 {
		t.Errorf("a disabled mod came back with pak %d, want 0", mods[3].Pak)
	}
	if mods[2].Shard != 2 {
		t.Errorf("the shard 2 mod came back in shard %d", mods[2].Shard)
	}
}

// A file the manifest names but the disk does not have is reported, not
// silently dropped: the mod is broken and the user has to be told.
func TestReadReportsMissingFiles(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	m := New()
	m.Mods["half-there"] = Entry{
		Enabled:     true,
		Shard:       1,
		CurrentVpks: []string{"pak01_dir.vpk", "pak02_dir.vpk"},
	}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	touch(t, p.Dir, "pak01_dir.vpk")

	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 {
		t.Fatalf("Read returned %d mods, want 1", len(mods))
	}
	mod := mods[0]
	if len(mod.Vpks) != 1 || mod.Vpks[0] != "pak01_dir.vpk" {
		t.Errorf("Vpks = %v, want only the file that is there", mod.Vpks)
	}
	if len(mod.Missing) != 1 || mod.Missing[0] != "pak02_dir.vpk" {
		t.Errorf("Missing = %v, want pak02_dir.vpk", mod.Missing)
	}
	// The number is still read from the missing name, so the mod keeps the
	// place in the order the manifest gives it.
	if mod.Pak != 1 {
		t.Errorf("Pak = %d, want 1", mod.Pak)
	}
}

// A profile with nothing installed has no manifest and no mods, which is not
// an error.
func TestReadWithoutAManifest(t *testing.T) {
	_, addonsDir := citadel(t)
	mods, err := Open(addonsDir).Read()
	if err != nil {
		t.Fatalf("Read = %v, want nil", err)
	}
	if len(mods) != 0 {
		t.Errorf("Read returned %d mods, want none", len(mods))
	}
}

// The disabled files are looked for in the profile directory whatever shard the
// mod is filed under, because that is where a disabled mod's files go.
func TestReadLooksForDisabledFilesInTheProfileDirectory(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	m := New()
	m.Mods["parked"] = Entry{Shard: 4, DisabledVpks: []string{"parked_skin.vpk"}}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	touch(t, p.Dir, "parked_skin.vpk")

	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || len(mods[0].Vpks) != 1 {
		t.Fatalf("Read = %+v, want the disabled file found in %q", mods, p.Dir)
	}
	if len(mods[0].Missing) != 0 {
		t.Errorf("the file in the profile directory was reported missing: %v", mods[0].Missing)
	}
}

// The newest file says when a mod was installed, which is what an update check
// has to compare a catalogue change against.
func TestReadReportsWhenTheNewestFileWasWritten(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	m := New()
	m.Mods["parked"] = Entry{Shard: 4, DisabledVpks: []string{"parked_a.vpk", "parked_b.vpk"}}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	older := time.Date(2026, 3, 4, 5, 6, 7, 0, time.Local)
	newer := older.Add(48 * time.Hour)
	touch(t, p.Dir, "parked_a.vpk")
	touch(t, p.Dir, "parked_b.vpk")
	for name, when := range map[string]time.Time{"parked_a.vpk": older, "parked_b.vpk": newer} {
		if err := os.Chtimes(filepath.Join(p.Dir, name), when, when); err != nil {
			t.Fatal(err)
		}
	}

	mods, err := p.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 {
		t.Fatalf("Read = %+v, want one mod", mods)
	}
	if !mods[0].Modified.Equal(newer) {
		t.Errorf("Modified = %v, want the newest file's %v", mods[0].Modified, newer)
	}
}

func TestPakNumber(t *testing.T) {
	for name, want := range map[string]int{
		"pak01_dir.vpk":        1,
		"pak99_dir.vpk":        99,
		"pak100_dir.vpk":       100,
		"pak7_dir.vpk":         7,
		"skin.vpk":             0,
		"pak01_dir.vpk.gz":     0,
		"pak_dir.vpk":          0,
		"pak00_dir.vpk":        0,
		"snd-42_pak01_dir.vpk": 0,
	} {
		if got := pakNumber(name); got != want {
			t.Errorf("pakNumber(%q) = %d, want %d", name, got, want)
		}
	}
}

// itoa keeps the test's expectations readable without pulling fmt in.
func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
