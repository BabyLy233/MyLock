package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	want := Config{GamePath: "/games/Deadlock", Theme: "dark", VanillaPending: true, VanillaSeen: true}

	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

// A first run has no settings file, which is not a failure.
func TestLoadWithoutAFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), fileName))
	if err != nil {
		t.Fatalf("Load of a missing file = %v, want nil", err)
	}
	if got != (Config{}) {
		t.Errorf("Load of a missing file = %+v, want the zero Config", got)
	}
}

// A settings file that was damaged is reported, not read as empty: the app
// should say the directory the user chose is not in effect.
func TestLoadRejectsDamagedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load of damaged JSON returned no error")
	}
}

// The settings directory is created on the first save.
func TestSaveCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), appDir, "nested", fileName)
	if err := (Config{GamePath: "/games/Deadlock"}).Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the settings file is not there after Save: %v", err)
	}
}

// Save replaces the file in one step, so it leaves nothing else behind and a
// second save is not an error.
func TestSaveLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)

	for _, gamePath := range []string{"/games/Deadlock", "/mnt/games/Deadlock", ""} {
		if err := (Config{GamePath: gamePath}).Save(path); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name()
		}
		t.Errorf("%s holds %v, want only %s", dir, names, fileName)
	}

	// The last save was of an empty path, which is how the app forgets a
	// directory the user pinned.
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.GamePath != "" {
		t.Errorf("GamePath = %q after saving an empty one", got.GamePath)
	}
}

func TestPathIsUnderTheConfigDirectory(t *testing.T) {
	path, err := Path()
	if err != nil {
		t.Skipf("this machine has no config directory: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("Path = %q, want an absolute path", path)
	}
	if got := filepath.Base(path); got != fileName {
		t.Errorf("Path ends in %q, want %q", got, fileName)
	}
	if got := filepath.Base(filepath.Dir(path)); got != appDir {
		t.Errorf("Path is in %q, want %q", got, appDir)
	}
}
