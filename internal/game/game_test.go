package game

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes a file, creating the directories above it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSteam builds a Steam root at the first path, with every path a library,
// and writes the libraryfolders.vdf listing them all.
func fakeSteam(t *testing.T, libraries ...string) string {
	t.Helper()
	for _, library := range libraries {
		if err := os.MkdirAll(filepath.Join(library, steamAppsDir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	b.WriteString("\"libraryfolders\"\n{\n")
	for i, library := range libraries {
		fmt.Fprintf(&b, "\t\"%d\"\n\t{\n\t\t\"path\"\t\t%q\n\t\t\"apps\"\n\t\t{\n\t\t\t\"%s\"\t\t\"1000\"\n\t\t}\n\t}\n",
			i, library, DeadlockAppID)
	}
	b.WriteString("}\n")
	writeFile(t, filepath.Join(libraries[0], steamAppsDir, libraryFoldersVDF), b.String())
	return libraries[0]
}

// fakeDeadlock makes gamePath look like a Deadlock installation.
func fakeDeadlock(t *testing.T, gamePath string) {
	t.Helper()
	writeFile(t, filepath.Join(gamePath, filepath.FromSlash(gameInfoRel)), "// gameinfo\n")
}

// fakeManifest writes the app manifest Steam leaves in a library.
func fakeManifest(t *testing.T, libraryPath, buildID string) {
	t.Helper()
	writeFile(t, filepath.Join(libraryPath, steamAppsDir, "appmanifest_"+DeadlockAppID+".acf"),
		"\"AppState\"\n{\n\t\"appid\"\t\t\""+DeadlockAppID+"\"\n\t\"buildid\"\t\t\""+buildID+"\"\n}\n")
}

func TestLibraries(t *testing.T) {
	root := t.TempDir()
	second := t.TempDir()
	fakeSteam(t, root, second)

	libraries, err := Libraries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(libraries) != 2 {
		t.Fatalf("%d libraries, want 2: %+v", len(libraries), libraries)
	}
	if libraries[0].Path != root {
		t.Errorf("the first library is %q, want the Steam root %q", libraries[0].Path, root)
	}
	if libraries[1].Path != second {
		t.Errorf("the second library is %q, want %q", libraries[1].Path, second)
	}
	// Steam lists the root as library 0, and that entry carries the apps it
	// holds. The root must keep them, not come back as a bare path.
	for i, library := range libraries {
		if apps := library.Apps; len(apps) != 1 || apps[0] != DeadlockAppID {
			t.Errorf("library %d (%s) holds %v, want the game", i, library.Path, apps)
		}
	}
}

// A library Steam still lists but that is gone from disk is left out, so a
// detection does not stall on an unmounted drive.
func TestLibrariesSkipsMissing(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(t.TempDir(), "unmounted")
	fakeSteam(t, root, gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	libraries, err := Libraries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(libraries) != 1 || libraries[0].Path != root {
		t.Fatalf("libraries = %+v, want only the root", libraries)
	}
}

// Without libraryfolders.vdf the Steam root is still a library.
func TestLibrariesWithoutVDF(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, steamAppsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	libraries, err := Libraries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(libraries) != 1 || libraries[0].Path != root {
		t.Fatalf("libraries = %+v, want only the root", libraries)
	}
}

// The game is often in a library of its own, not in the Steam root.
func TestDetectInASecondLibrary(t *testing.T) {
	root := t.TempDir()
	second := t.TempDir()
	fakeSteam(t, root, second)

	gamePath := filepath.Join(second, steamAppsDir, "common", DirName)
	fakeDeadlock(t, gamePath)
	fakeManifest(t, second, "20260101")

	install, err := detectIn(root)
	if err != nil {
		t.Fatal(err)
	}
	if install.GamePath != gamePath {
		t.Errorf("GamePath = %q, want %q", install.GamePath, gamePath)
	}
	if install.SteamRoot != root {
		t.Errorf("SteamRoot = %q, want %q", install.SteamRoot, root)
	}
	if install.LibraryPath != second {
		t.Errorf("LibraryPath = %q, want %q", install.LibraryPath, second)
	}
	if install.BuildID != "20260101" {
		t.Errorf("BuildID = %q, want 20260101", install.BuildID)
	}
	if want := filepath.Join(gamePath, "game", "citadel", "addons"); install.AddonsDir() != want {
		t.Errorf("AddonsDir() = %q, want %q", install.AddonsDir(), want)
	}
	if want := filepath.Join(gamePath, "game", "citadel", "gameinfo.gi"); install.GameInfoPath() != want {
		t.Errorf("GameInfoPath() = %q, want %q", install.GameInfoPath(), want)
	}
}

func TestDetectInWithoutTheGame(t *testing.T) {
	root := t.TempDir()
	fakeSteam(t, root)

	if _, err := detectIn(root); !errors.Is(err, ErrNotFound) {
		t.Errorf("detectIn = %v, want ErrNotFound", err)
	}
}

// A directory that is not a Deadlock installation is refused, whatever the
// user picked.
func TestValidate(t *testing.T) {
	gamePath := filepath.Join(t.TempDir(), "Deadlock")
	fakeDeadlock(t, gamePath)

	install, err := Validate(gamePath)
	if err != nil {
		t.Fatal(err)
	}
	if install.GamePath != gamePath {
		t.Errorf("GamePath = %q, want %q", install.GamePath, gamePath)
	}
	// The Steam fields stay empty: the user picked the directory by hand.
	if install.SteamRoot != "" || install.LibraryPath != "" || install.BuildID != "" {
		t.Errorf("Validate filled Steam fields: %+v", install)
	}

	if _, err := Validate(t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Validate of a plain directory = %v, want ErrNotFound", err)
	}
}

// Validate cleans the path, so a trailing separator or a relative segment
// does not reach the rest of the app.
func TestValidateCleansThePath(t *testing.T) {
	gamePath := filepath.Join(t.TempDir(), "Deadlock")
	fakeDeadlock(t, gamePath)

	install, err := Validate(filepath.Join(gamePath, "game", "..") + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if install.GamePath != gamePath {
		t.Errorf("GamePath = %q, want the cleaned %q", install.GamePath, gamePath)
	}
}

// The build id comes from the manifest, and its absence is not an error: a
// library may hold the game without one.
func TestBuildID(t *testing.T) {
	library := t.TempDir()
	if got := buildID(library); got != "" {
		t.Errorf("buildID without a manifest = %q, want empty", got)
	}
	fakeManifest(t, library, "999")
	if got := buildID(library); got != "999" {
		t.Errorf("buildID = %q, want 999", got)
	}
}

func TestSteamRootsAreUsable(t *testing.T) {
	roots := SteamRoots()
	if len(roots) == 0 {
		t.Fatal("SteamRoots returned nothing")
	}
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			t.Error("SteamRoots returned an empty path")
		}
		if !filepath.IsAbs(root) {
			t.Errorf("SteamRoots returned the relative path %q", root)
		}
		if seen[root] {
			t.Errorf("SteamRoots returned %q twice", root)
		}
		seen[root] = true
	}
}

// Detect runs against the machine, so it either finds the game or says it is
// not there. It must not report anything else.
func TestDetectOnThisMachine(t *testing.T) {
	install, err := Detect()
	switch {
	case err == nil && install.GamePath == "":
		t.Error("Detect returned an install with no path")
	case err != nil && !errors.Is(err, ErrNotFound):
		t.Errorf("Detect = %v, want nil or ErrNotFound", err)
	}
}
