package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/internal/addons"
)

// addModFiles writes a disabled mod of several files into a profile, as an
// install of a mod with that many VPKs would have left it.
func addModFiles(t *testing.T, p addons.Profile, id string, originals ...string) {
	t.Helper()
	m, err := addons.Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	disabled := make([]string, len(originals))
	for i, original := range originals {
		disabled[i] = id + "_" + original
		if err := os.MkdirAll(p.Dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.Dir, disabled[i]), []byte("vpk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.Mods[id] = addons.Entry{
		Shard:            addons.FirstShard,
		DisabledVpks:     disabled,
		OriginalVpkNames: originals,
	}
	if err := m.Save(p.ManifestPath()); err != nil {
		t.Fatal(err)
	}
}

// The mods page opens the files of a mod of several files, and the ones left
// unchecked are not the ones the next enable turns on.
func TestManagingAModsFilesAppliesTheChoice(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addModFiles(t, profile, "42", "a.vpk", "b.vpk")
	tt := show(t, a, install, profile)

	if err := tt.Click("选择 42 的文件"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	waitFor(t, "the files to be read", func() bool {
		state := a.filesState()
		return !state.busy && len(state.files) == 2
	})
	tt.Frame()

	for _, want := range []string{"管理文件", "a.vpk", "b.vpk"} {
		if !tt.HasText(want) {
			t.Fatalf("the dialog does not show %q: %q", want, tt.Texts())
		}
	}
	if state := a.filesState(); !state.picked["a.vpk"] || !state.picked["b.vpk"] {
		t.Fatalf("the dialog starts with %v chosen, want both", state.picked)
	}

	if err := tt.Click("b.vpk"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.filesState().picked["b.vpk"] {
		t.Error("b is still checked after clicking it")
	}

	if err := tt.Click("应用文件选择"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the choice to be applied", func() bool {
		state := a.filesState()
		return !state.applying && state.ask == ""
	})

	entry := loadEntry(t, profile, "42")
	if got := entry.OriginalVpkNames; len(got) != 1 || got[0] != "a.vpk" {
		t.Errorf("OriginalVpkNames = %v, want only a.vpk", got)
	}
	if got := entry.DisabledVpks; len(got) != 1 || got[0] != "42_a.vpk" {
		t.Errorf("DisabledVpks = %v, want only 42_a.vpk", got)
	}
	// b is still in the profile, so it can be chosen again.
	if _, err := os.Stat(filepath.Join(profile.Dir, "42_b.vpk")); err != nil {
		t.Errorf("the unchosen file was taken away: %v", err)
	}
}

// Turning every file off is what the mod's own switch is for, so the dialog
// says to use it rather than writing a mod that loads nothing.
func TestManagingFilesRefusesAnEmptyChoice(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addModFiles(t, profile, "42", "a.vpk", "b.vpk")
	tt := show(t, a, install, profile)

	if err := tt.Click("选择 42 的文件"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	waitFor(t, "the files to be read", func() bool {
		state := a.filesState()
		return !state.busy && len(state.files) == 2
	})
	tt.Frame()
	for _, name := range []string{"a.vpk", "b.vpk"} {
		if err := tt.Click(name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if err := tt.Click("应用文件选择"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if a.filesState().ask == "" {
		t.Error("the dialog closed although nothing could be applied")
	}
	if !tt.HasText("至少要保留一个文件") {
		t.Errorf("the dialog does not say why: %q", tt.Texts())
	}
	entry := loadEntry(t, profile, "42")
	if len(entry.OriginalVpkNames) != 2 {
		t.Errorf("the manifest changed: %+v", entry)
	}
}

// loadEntry reads one mod out of a profile's manifest, failing when it is gone.
func loadEntry(t *testing.T, p addons.Profile, id string) addons.Entry {
	t.Helper()
	m, err := addons.Load(p.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := m.Mods[id]
	if !ok {
		t.Fatalf("the manifest has no mod %q", id)
	}
	return entry
}
