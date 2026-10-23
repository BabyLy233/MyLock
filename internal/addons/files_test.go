package addons

import (
	"os"
	"path/filepath"
	"testing"
)

// A mod of several files has all of them listed, including the ones a previous
// selection left disabled.
func TestFilesListsWhatIsSelectedAndWhatIsNot(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Enabled:          true,
		Shard:            FirstShard,
		CurrentVpks:      []string{"pak01_dir.vpk", "pak02_dir.vpk"},
		OriginalVpkNames: []string{"a.vpk", "b.vpk"},
	})
	touch(t, p.Dir, "pak01_dir.vpk")
	touch(t, p.Dir, "pak02_dir.vpk")
	touch(t, p.Dir, "42_c.vpk")

	files, err := p.Files("42")
	if err != nil {
		t.Fatal(err)
	}
	want := []InstalledFile{
		{Name: "a.vpk", Selected: true},
		{Name: "b.vpk", Selected: true},
		{Name: "c.vpk"},
	}
	if len(files) != len(want) {
		t.Fatalf("Files = %+v, want %+v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("Files[%d] = %+v, want %+v", i, files[i], want[i])
		}
	}

	// A disabled mod keeps its choice in the disabled names: those are the
	// files a later enable turns on.
	if err := p.SetEnabled("42", false); err != nil {
		t.Fatal(err)
	}
	files, err = p.Files("42")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || !files[0].Selected || !files[1].Selected || files[2].Selected {
		t.Errorf("Files of the disabled mod = %+v, want a and b selected", files)
	}
}

// Choosing files for a disabled mod only records the choice; the files are all
// prefixed already and nothing moves.
func TestSetFilesOnADisabledModRecordsTheChoice(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"42_a.vpk", "42_b.vpk"},
		OriginalVpkNames: []string{"a.vpk", "b.vpk"},
	})
	for _, name := range []string{"42_a.vpk", "42_b.vpk", "42_c.vpk"} {
		touch(t, p.Dir, name)
	}

	if err := p.SetFiles("42", []string{"a.vpk", "c.vpk"}); err != nil {
		t.Fatal(err)
	}

	entry := load(t, p, "42")
	if entry.Enabled {
		t.Error("the mod was enabled by choosing files for it")
	}
	if got := entry.DisabledVpks; len(got) != 2 || got[0] != "42_a.vpk" || got[1] != "42_c.vpk" {
		t.Errorf("DisabledVpks = %v, want 42_a and 42_c", got)
	}
	if got := entry.OriginalVpkNames; len(got) != 2 || got[0] != "a.vpk" || got[1] != "c.vpk" {
		t.Errorf("OriginalVpkNames = %v, want a and c", got)
	}
	// b is left out of the choice but stays on disk, to be chosen again later.
	if !exists(p.Dir, "42_b.vpk") || !exists(p.Dir, "42_a.vpk") || !exists(p.Dir, "42_c.vpk") {
		t.Error("choosing files moved one of them")
	}
}

// Choosing files for an enabled mod leaves the chosen ones in the shard and puts
// the rest back under their prefixed names.
func TestSetFilesOnAnEnabledModMovesTheUnchosenOnesOut(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Enabled:          true,
		Shard:            FirstShard,
		CurrentVpks:      []string{"pak01_dir.vpk", "pak02_dir.vpk"},
		OriginalVpkNames: []string{"a.vpk", "b.vpk"},
	})
	touch(t, p.Dir, "pak01_dir.vpk")
	touch(t, p.Dir, "pak02_dir.vpk")
	touch(t, p.Dir, "42_c.vpk")

	if err := p.SetFiles("42", []string{"a.vpk", "c.vpk"}); err != nil {
		t.Fatal(err)
	}

	entry := load(t, p, "42")
	if !entry.Enabled {
		t.Error("the mod is not enabled")
	}
	if got := entry.OriginalVpkNames; len(got) != 2 || got[0] != "a.vpk" || got[1] != "c.vpk" {
		t.Errorf("OriginalVpkNames = %v, want a and c", got)
	}
	if len(entry.CurrentVpks) != 2 {
		t.Fatalf("CurrentVpks = %v, want two names", entry.CurrentVpks)
	}
	for _, name := range entry.CurrentVpks {
		if !exists(p.ShardDir(entry.Shard), name) {
			t.Errorf("%s is not in the shard", name)
		}
	}
	if exists(p.Dir, "42_a.vpk") || exists(p.Dir, "42_c.vpk") {
		t.Error("a chosen file is still under its prefixed name")
	}
	if !exists(p.Dir, "42_b.vpk") {
		t.Error("the unchosen file is not back under its prefixed name")
	}

	// The choice is what a later enable turns on, and b stays out of it.
	if err := p.SetEnabled("42", false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetEnabled("42", true); err != nil {
		t.Fatal(err)
	}
	entry = load(t, p, "42")
	if len(entry.CurrentVpks) != 2 {
		t.Errorf("CurrentVpks = %v, want the two chosen files", entry.CurrentVpks)
	}
	if !exists(p.Dir, "42_b.vpk") {
		t.Error("b was enabled although it was not chosen")
	}
}

// A mod has to keep at least one file; turning them all off is what disabling
// the mod is for.
func TestSetFilesRefusesAnEmptySelection(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"42_a.vpk"},
		OriginalVpkNames: []string{"a.vpk"},
	})
	touch(t, p.Dir, "42_a.vpk")

	if err := p.SetFiles("42", nil); err == nil {
		t.Error("an empty selection was accepted")
	}
	if got := load(t, p, "42").DisabledVpks; len(got) != 1 || got[0] != "42_a.vpk" {
		t.Errorf("DisabledVpks = %v, want the choice untouched", got)
	}
}

// A file the mod does not have cannot be chosen, and a change that fails partway
// puts the files it moved back.
func TestSetFilesRefusesAFileThatIsNotThereAndRollsBack(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Enabled:          true,
		Shard:            FirstShard,
		CurrentVpks:      []string{"pak01_dir.vpk", "pak02_dir.vpk"},
		OriginalVpkNames: []string{"a.vpk", "b.vpk"},
	})
	touch(t, p.Dir, "pak01_dir.vpk")
	touch(t, p.Dir, "pak02_dir.vpk")

	if err := p.SetFiles("42", []string{"nope.vpk"}); err == nil {
		t.Error("choosing a file the mod does not have reported no error")
	}

	// Take b away, so taking the mod's files out of the shard fails after a has
	// moved. The choice is one the app cannot carry out, and nothing changes.
	if err := os.Remove(filepath.Join(p.Dir, "pak02_dir.vpk")); err != nil {
		t.Fatal(err)
	}
	if err := p.SetFiles("42", []string{"a.vpk"}); err == nil {
		t.Fatal("changing the files with one of them missing reported no error")
	}

	if !exists(p.Dir, "pak01_dir.vpk") {
		t.Error("the file that had moved was not put back")
	}
	if exists(p.Dir, "42_a.vpk") {
		t.Error("a file was left under its prefixed name")
	}
	entry := load(t, p, "42")
	if !entry.Enabled || len(entry.CurrentVpks) != 2 {
		t.Errorf("the manifest changed: %+v", entry)
	}
}

// A selection that needs a shard which is not on disk yet gets the directory
// made for it, rather than an install that fails because the folder is missing.
func TestSetFilesCreatesTheShardItMovesTo(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	fillShard(t, p, "full", FirstShard, ShardCapacity-1)
	save(t, p, "42", Entry{
		Enabled:          true,
		Shard:            FirstShard,
		CurrentVpks:      []string{"pak99_dir.vpk"},
		OriginalVpkNames: []string{"a.vpk"},
	})
	touch(t, p.Dir, "pak99_dir.vpk")
	touch(t, p.Dir, "42_b.vpk")

	if err := p.SetFiles("42", []string{"a.vpk", "b.vpk"}); err != nil {
		t.Fatal(err)
	}

	entry := load(t, p, "42")
	if entry.Shard != 2 {
		t.Fatalf("Shard = %d, want 2", entry.Shard)
	}
	for _, name := range entry.CurrentVpks {
		if !exists(p.ShardDir(2), name) {
			t.Errorf("%s is not in the shard that was made", name)
		}
	}
}

// Removing a mod takes the files a per-file selection left out of the manifest
// with it, rather than leaving them behind as files nothing names.
func TestRemoveTakesTheFilesASelectionLeftOut(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Shard:            FirstShard,
		DisabledVpks:     []string{"42_a.vpk"},
		OriginalVpkNames: []string{"a.vpk"},
	})
	touch(t, p.Dir, "42_a.vpk")
	touch(t, p.Dir, "42_b.vpk")

	if err := p.Remove("42"); err != nil {
		t.Fatal(err)
	}
	if exists(p.Dir, "42_a.vpk") || exists(p.Dir, "42_b.vpk") {
		t.Error("removing the mod left one of its files behind")
	}
}

// Replacing a mod moves the files a per-file selection left out aside too: the
// new version often has the same names, and a copy cannot write over one that
// is still there.
func TestReplaceTakesTheFilesASelectionLeftOut(t *testing.T) {
	_, addonsDir := citadel(t)
	p := Open(addonsDir)
	save(t, p, "42", Entry{
		Enabled:          true,
		Shard:            FirstShard,
		CurrentVpks:      []string{"pak01_dir.vpk"},
		OriginalVpkNames: []string{"a.vpk"},
	})
	touch(t, p.Dir, "pak01_dir.vpk")
	touch(t, p.Dir, "42_b.vpk")
	touch(t, p.Dir, "42_c.vpk")

	src := filepath.Join(t.TempDir(), "b.vpk")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	wasEnabled, err := p.Replace("42", []File{{Path: src, Name: "b.vpk"}})
	if err != nil {
		t.Fatal(err)
	}
	if !wasEnabled {
		t.Error("Replace does not report the mod as having been enabled")
	}
	if !exists(p.Dir, "42_b.vpk") {
		t.Error("the new file was not written")
	}
	if exists(p.Dir, "42_c.vpk") {
		t.Error("an old file left out by a selection was left behind")
	}
	entry := load(t, p, "42")
	if len(entry.DisabledVpks) != 1 || entry.DisabledVpks[0] != "42_b.vpk" {
		t.Errorf("DisabledVpks = %v, want the new file", entry.DisabledVpks)
	}
}
