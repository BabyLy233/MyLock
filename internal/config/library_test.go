package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLibrarySaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), libraryFileName)
	installed := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	want := Library{Mods: map[string]ModInfo{
		"554012": {
			Name:        "Bikinidicta",
			Kind:        "Mod",
			Category:    "Skins",
			Subcategory: "Vindicta",
			Author:      "Keller Max",
			Version:     "v2",
			Thumb:       "https://images.gamebanana.com/img/ss/mods/x.jpg",
			Page:        "https://gamebanana.com/mods/554012",
			Installed:   installed,
			FileIDs:     []int{912345},
		},
	}}

	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLibrary(path)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := got.Mods["554012"]
	if !ok {
		t.Fatalf("LoadLibrary = %+v, want mod 554012", got)
	}
	if info.Name != want.Mods["554012"].Name || info.Author != want.Mods["554012"].Author ||
		info.Category != want.Mods["554012"].Category || info.Subcategory != want.Mods["554012"].Subcategory ||
		info.Version != want.Mods["554012"].Version || info.Thumb != want.Mods["554012"].Thumb ||
		info.Page != want.Mods["554012"].Page {
		t.Errorf("LoadLibrary = %+v, want %+v", info, want.Mods["554012"])
	}
	if !info.Installed.Equal(installed) {
		t.Errorf("Installed = %v, want %v", info.Installed, installed)
	}
	if len(info.FileIDs) != 1 || info.FileIDs[0] != 912345 {
		t.Errorf("FileIDs = %v, want [912345]", info.FileIDs)
	}
}

// A first run has no library file, which is not a failure.
func TestLoadLibraryWithoutAFile(t *testing.T) {
	got, err := LoadLibrary(filepath.Join(t.TempDir(), libraryFileName))
	if err != nil {
		t.Fatalf("LoadLibrary of a missing file = %v, want nil", err)
	}
	if len(got.Mods) != 0 {
		t.Errorf("LoadLibrary of a missing file = %+v, want none", got)
	}
}

// The library file sits beside the settings file, as the profiles file does.
func TestLibraryPathIsBesideTheSettings(t *testing.T) {
	settings := filepath.Join(t.TempDir(), appDir, fileName)
	want := filepath.Join(filepath.Dir(settings), libraryFileName)
	if got := LibraryPath(settings); got != want {
		t.Errorf("LibraryPath(%q) = %q, want %q", settings, got, want)
	}
	if got := LibraryPath(""); got != "" {
		t.Errorf("LibraryPath(\"\") = %q, want empty", got)
	}
}
