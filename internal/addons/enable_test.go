package addons

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// exists reports whether a file of that name is in dir.
func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// save writes a manifest holding one mod, so a test starts from a profile that
// already has something in it.
func save(t *testing.T, p Profile, id string, entry Entry) {
	t.Helper()
	m, err := Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	m.Mods[id] = entry
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
}

// load returns the entry for a mod, failing when it is not there.
func load(t *testing.T, p Profile, id string) Entry {
	t.Helper()
	m, err := Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := m.Mods[id]
	if !ok {
		t.Fatalf("the manifest has no mod %q", id)
	}
	return entry
}

// fillShard makes a mod of n enabled files in a shard, on disk and in the
// manifest, so that a test can fill a shard up.
func fillShard(t *testing.T, p Profile, id string, shard uint32, n int) {
	t.Helper()
	m, err := Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, n)
	originals := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("pak%02d_dir.vpk", i+1)
		originals[i] = fmt.Sprintf("file%d.vpk", i+1)
		touch(t, p.ShardDir(shard), names[i])
	}
	m.Mods[id] = Entry{Enabled: true, Shard: shard, CurrentVpks: names, OriginalVpkNames: originals}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
}

func TestEnableRenamesTheFilesAndRecordsThem(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "snd-42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"snd-42_voice.vpk", "snd-42_music.vpk"},
		OriginalVpkNames: []string{"voice.vpk", "music.vpk"},
	})
	touch(t, p.Dir, "snd-42_voice.vpk")
	touch(t, p.Dir, "snd-42_music.vpk")

	if err := p.SetEnabled("snd-42", true); err != nil {
		t.Fatal(err)
	}

	entry := load(t, p, "snd-42")
	if !entry.Enabled {
		t.Error("the mod is not enabled in the manifest")
	}
	if got := entry.CurrentVpks; len(got) != 2 || got[0] != "pak01_dir.vpk" || got[1] != "pak02_dir.vpk" {
		t.Errorf("CurrentVpks = %v, want pak01 and pak02", got)
	}
	if len(entry.DisabledVpks) != 0 {
		t.Errorf("DisabledVpks = %v, want none", entry.DisabledVpks)
	}
	// The names before the prefix are kept, because disabling has to put them
	// back and nothing else remembers them.
	if got := entry.OriginalVpkNames; len(got) != 2 || got[0] != "voice.vpk" {
		t.Errorf("OriginalVpkNames = %v, want the names kept", got)
	}

	if !exists(p.Dir, "pak01_dir.vpk") || !exists(p.Dir, "pak02_dir.vpk") {
		t.Error("the enabled files are not in the profile directory")
	}
	if exists(p.Dir, "snd-42_voice.vpk") || exists(p.Dir, "snd-42_music.vpk") {
		t.Error("the disabled names are still there")
	}
}

// Enabling and disabling again must give back the names the files started with,
// or the prefix would grow a little every time the user flipped the switch.
func TestSetEnabledRoundTrips(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "snd-42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"snd-42_voice.vpk"},
		OriginalVpkNames: []string{"voice.vpk"},
	})
	touch(t, p.Dir, "snd-42_voice.vpk")

	if err := p.SetEnabled("snd-42", true); err != nil {
		t.Fatal(err)
	}
	if err := p.SetEnabled("snd-42", false); err != nil {
		t.Fatal(err)
	}

	entry := load(t, p, "snd-42")
	if entry.Enabled {
		t.Error("the mod is still enabled")
	}
	if got := entry.DisabledVpks; len(got) != 1 || got[0] != "snd-42_voice.vpk" {
		t.Errorf("DisabledVpks = %v, want snd-42_voice.vpk back", got)
	}
	if len(entry.CurrentVpks) != 0 {
		t.Errorf("CurrentVpks = %v, want none", entry.CurrentVpks)
	}
	if !exists(p.Dir, "snd-42_voice.vpk") {
		t.Error("the file is not back under its disabled name")
	}
	if exists(p.Dir, "pak01_dir.vpk") {
		t.Error("the enabled name is still there")
	}
}

// A shard number freed by a mod being disabled is used again, rather than the
// numbers marching up until the shard is full of holes.
func TestEnableTakesTheLowestFreeNumbers(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	fillShard(t, p, "already", FirstShard, 3)
	// Free the middle one the way disabling does, leaving pak01 and pak03.
	if err := os.Remove(filepath.Join(p.Dir, "pak02_dir.vpk")); err != nil {
		t.Fatal(err)
	}

	save(t, p, "new", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"new_skin.vpk"},
		OriginalVpkNames: []string{"skin.vpk"},
	})
	touch(t, p.Dir, "new_skin.vpk")

	if err := p.SetEnabled("new", true); err != nil {
		t.Fatal(err)
	}
	if got := load(t, p, "new").CurrentVpks; len(got) != 1 || got[0] != "pak02_dir.vpk" {
		t.Errorf("CurrentVpks = %v, want the freed pak02", got)
	}
}

// A mod already filed under a later shard stays there, so that enabling
// something else does not move it in the load order.
func TestEnableKeepsAModInItsOwnShard(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	fillShard(t, p, "resident", 3, 1)

	save(t, p, "later", Entry{
		Shard:            3,
		DisabledVpks:     []string{"later_skin.vpk"},
		OriginalVpkNames: []string{"skin.vpk"},
	})
	touch(t, p.Dir, "later_skin.vpk")

	if err := p.SetEnabled("later", true); err != nil {
		t.Fatal(err)
	}
	entry := load(t, p, "later")
	if entry.Shard != 3 {
		t.Errorf("Shard = %d, want it left in shard 3", entry.Shard)
	}
	if !exists(p.ShardDir(3), "pak02_dir.vpk") {
		t.Error("the file is not in shard 3")
	}
}

// Filling the first shard sends the next mod to the last one in use rather than
// opening a shard of its own.
func TestEnableMovesOnWhenItsShardIsFull(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	fillShard(t, p, "full", FirstShard, ShardCapacity)
	fillShard(t, p, "second", 2, 1)

	save(t, p, "new", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"new_skin.vpk"},
		OriginalVpkNames: []string{"skin.vpk"},
	})
	touch(t, p.Dir, "new_skin.vpk")

	if err := p.SetEnabled("new", true); err != nil {
		t.Fatal(err)
	}
	entry := load(t, p, "new")
	if entry.Shard != 2 {
		t.Errorf("Shard = %d, want it moved to shard 2", entry.Shard)
	}
	if !exists(p.ShardDir(2), "pak02_dir.vpk") {
		t.Error("the file is not in shard 2")
	}
}

// A mod with no files left to move is a manifest the user should hear about,
// not one to write a lie into.
func TestSetEnabledRefusesAModWithNoFiles(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	// Disabled, and the manifest names nothing to turn on.
	save(t, p, "nothing-to-enable", Entry{Shard: FirstShard})
	if err := p.SetEnabled("nothing-to-enable", true); err == nil {
		t.Error("enabling a mod with no files reported no error")
	}

	// Enabled, and the manifest names nothing to turn off.
	save(t, p, "nothing-to-disable", Entry{Enabled: true, Shard: FirstShard})
	if err := p.SetEnabled("nothing-to-disable", false); err == nil {
		t.Error("disabling a mod with no files reported no error")
	}
}

// Without the names the files had before, disabling cannot say what to call
// them again, and guessing risks writing over another mod's file.
func TestDisableRefusesAManifestWithoutOriginalNames(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "snd-42", Entry{
		Enabled:     true,
		Shard:       FirstShard,
		CurrentVpks: []string{"pak01_dir.vpk"},
	})
	touch(t, p.Dir, "pak01_dir.vpk")

	if err := p.SetEnabled("snd-42", false); err == nil {
		t.Error("disabling without the original names reported no error")
	}
	if !exists(p.Dir, "pak01_dir.vpk") {
		t.Error("the file was moved anyway")
	}
	if entry := load(t, p, "snd-42"); !entry.Enabled {
		t.Error("the manifest was changed anyway")
	}
}

func TestSetEnabledRejectsAnUnknownMod(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	if err := New().Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
	if err := p.SetEnabled("nobody", true); err == nil {
		t.Error("enabling a mod that is not installed reported no error")
	}
}

// Setting a mod to the state it is already in changes nothing, so a switch the
// user did not move cannot rewrite the manifest.
func TestSetEnabledIsANoOpInTheStateItIsAlreadyIn(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "snd-42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"snd-42_voice.vpk"},
		OriginalVpkNames: []string{"voice.vpk"},
	})
	touch(t, p.Dir, "snd-42_voice.vpk")

	if err := p.SetEnabled("snd-42", false); err != nil {
		t.Fatal(err)
	}
	if !exists(p.Dir, "snd-42_voice.vpk") {
		t.Error("the file was moved by a no-op")
	}
	if got := load(t, p, "snd-42").DisabledVpks; len(got) != 1 || got[0] != "snd-42_voice.vpk" {
		t.Errorf("DisabledVpks = %v, want them untouched", got)
	}
}

// When one of a mod's files cannot be moved, the ones already moved go back:
// half a mod enabled is worse than none, and the manifest would no longer
// describe the disk.
func TestEnablePutsBackTheFilesItAlreadyMoved(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "snd-42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"snd-42_voice.vpk", "snd-42_music.vpk"},
		OriginalVpkNames: []string{"voice.vpk", "music.vpk"},
	})
	// The second file is not there, so its rename fails after the first one.
	touch(t, p.Dir, "snd-42_voice.vpk")

	if err := p.SetEnabled("snd-42", true); err == nil {
		t.Fatal("enabling a mod with a missing file reported no error")
	}

	if !exists(p.Dir, "snd-42_voice.vpk") {
		t.Error("the file that had moved was not put back")
	}
	if exists(p.Dir, "pak01_dir.vpk") {
		t.Error("a file was left under its enabled name")
	}
	entry := load(t, p, "snd-42")
	if entry.Enabled {
		t.Error("the manifest says the mod is enabled")
	}
	if got := entry.DisabledVpks; len(got) != 2 {
		t.Errorf("DisabledVpks = %v, want both kept", got)
	}
}

func TestAllocateNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"pak01_dir.vpk", "pak02_dir.vpk", "pak05_dir.vpk", "notes.txt"} {
		touch(t, dir, name)
	}

	names, err := allocateNames(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pak03_dir.vpk", "pak04_dir.vpk", "pak06_dir.vpk"}
	if len(names) != len(want) {
		t.Fatalf("allocateNames = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("allocateNames = %v, want %v", names, want)
		}
	}

	// A directory that is not there yet is empty, not an error: the first mod
	// of a fresh profile has nowhere to be allocated in.
	fresh, err := allocateNames(filepath.Join(dir, "not-yet"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0] != "pak01_dir.vpk" {
		t.Errorf("allocateNames = %v, want pak01_dir.vpk", fresh)
	}

	if _, err := allocateNames(dir, ShardCapacity+1); err == nil {
		t.Error("allocating more names than a shard holds reported no error")
	}
}

func TestAllocateNamesStopsAtTheShardCapacity(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= ShardCapacity; i++ {
		touch(t, dir, fmt.Sprintf("pak%02d_dir.vpk", i))
	}
	if _, err := allocateNames(dir, 1); err == nil {
		t.Errorf("allocating from a full shard reported no error")
	}
}

func TestChooseShard(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	full := New()
	full.Mods["full"] = Entry{Enabled: true, Shard: 1, CurrentVpks: make([]string, ShardCapacity)}
	full.Mods["second"] = Entry{Enabled: true, Shard: 2, CurrentVpks: []string{"pak01_dir.vpk"}}

	roomy := New()
	roomy.Mods["one"] = Entry{Enabled: true, Shard: 1, CurrentVpks: []string{"pak01_dir.vpk"}}
	roomy.Mods["three"] = Entry{Enabled: true, Shard: 3, CurrentVpks: []string{"pak01_dir.vpk"}}

	for _, tc := range []struct {
		name    string
		of      *Manifest
		current uint32
		want    uint32
	}{
		// A mod already filed somewhere with room stays where it is.
		{"keeps its own shard", roomy, 3, 3},
		// A mod that has never been enabled goes to the last shard in use, not
		// the first one with room, so nothing already loaded moves.
		{"joins the last shard", roomy, 0, 3},
		// A full shard sends it on to the last one in use.
		{"leaves a full shard", full, 1, 2},
		// With nothing enabled there is nothing to join.
		{"starts at the first", New(), 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.chooseShard(tc.of, "", tc.current, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("chooseShard = %d, want %d", got, tc.want)
			}
		})
	}
}

// Every shard full is the one case with nowhere left to put a mod.
func TestChooseShardRefusesWhenEveryShardIsFull(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)

	m := New()
	for shard := uint32(1); shard <= MaxShards; shard++ {
		m.Mods[fmt.Sprintf("mod%d", shard)] = Entry{
			Enabled:     true,
			Shard:       shard,
			CurrentVpks: make([]string, ShardCapacity),
		}
	}
	if _, err := p.chooseShard(m, "", 0, 1); err == nil {
		t.Error("chooseShard found room in a profile with every shard full")
	}
}
