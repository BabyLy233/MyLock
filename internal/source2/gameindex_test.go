package source2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func writeArchive(t *testing.T, path string, entries []vpk.PackEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := vpk.Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResourcePathSuffixesAndRejectsFragments(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"models/hero/hero.vmdl", "models/hero/hero.vmdl_c", true},
		{"Models\\Hero\\Hero.VMDL_C", "models/hero/hero.vmdl_c", true},
		{"resource/localization/foo.txt", "resource/localization/foo.txt", true},
		{"models/hero/hero.vmdl#attachment", "", false},
		{"../escape.vmdl", "", false},
	}
	for _, tc := range cases {
		got, ok := ResourcePath(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ResourcePath(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestOpenGameIndexesCitadelBeforeCoreAndReadsEntries(t *testing.T) {
	root := t.TempDir()
	citadel := filepath.Join(root, "game", "citadel")
	core := filepath.Join(root, "game", "core")
	writeArchive(t, filepath.Join(citadel, "pak01_dir.vpk"), []vpk.PackEntry{
		{Path: "models/hero.vmdl_c", Data: []byte("citadel")},
		{Path: "scripts/shared.txt", Data: []byte("shared citadel")},
	})
	writeArchive(t, filepath.Join(core, "core_dir.vpk"), []vpk.PackEntry{
		{Path: "models/hero.vmdl_c", Data: []byte("core")},
		{Path: "scripts/shared.txt", Data: []byte("shared core")},
	})

	index, err := OpenGame(citadel)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := index.GameBytes("models/hero.vmdl"); !ok || string(got) != "citadel" {
		t.Fatalf("hero = %q, %v", got, ok)
	}
	if got, ok := index.GameBytes("scripts/shared.txt"); !ok || string(got) != "shared citadel" {
		t.Fatalf("shared = %q, %v", got, ok)
	}
	if _, ok := index.GameBytes("models/missing.vmdl"); ok {
		t.Fatal("missing resource was found")
	}
	// A second read comes from the cache and returns its own copy.
	first, _ := index.GameBytes("models/hero.vmdl")
	first[0] = 'X'
	second, _ := index.GameBytes("models/hero.vmdl")
	if string(second) != "citadel" {
		t.Fatalf("cached bytes were mutated: %q", second)
	}
}

func TestOpenGamePrefersLooseFilesAndSkipsMissingDirectories(t *testing.T) {
	root := t.TempDir()
	citadel := filepath.Join(root, "game", "citadel")
	writeArchive(t, filepath.Join(citadel, "pak01_dir.vpk"), []vpk.PackEntry{
		{Path: "models/hero.vmdl_c", Data: []byte("packed")},
	})
	loose := filepath.Join(citadel, "models", "loose.vmdl_c")
	if err := os.MkdirAll(filepath.Dir(loose), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, []byte("loose"), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := OpenGame(citadel)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := index.GameBytes("models/loose.vmdl"); !ok || string(got) != "loose" {
		t.Fatalf("loose = %q, %v", got, ok)
	}
	if got := index.Directories(); len(got) != 2 || got[0] != citadel {
		t.Fatalf("directories = %v", got)
	}
}
