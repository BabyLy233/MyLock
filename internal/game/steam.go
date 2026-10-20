package game

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/vdf"
)

// ErrNotFound reports that Steam, or the game, was not found.
var ErrNotFound = errors.New("not found")

// steamAppsDir is the directory every Steam library has.
const steamAppsDir = "steamapps"

// libraryFoldersVDF lists the library folders a Steam installation knows.
const libraryFoldersVDF = "libraryfolders.vdf"

// SteamRoots returns the Steam installation roots to try, most likely first.
// The list covers the layouts each platform installs Steam in, including the
// Flatpak and Snap packages on Linux, which keep their own copy.
func SteamRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	join := func(parts ...string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(append([]string{home}, parts...)...)
	}

	var roots []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "ProgramW6432"} {
			if dir := os.Getenv(env); dir != "" {
				roots = append(roots, filepath.Join(dir, "Steam"))
			}
		}
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			roots = append(roots, filepath.Join(dir, "Steam"))
		}
	case "darwin":
		roots = append(roots, join("Library", "Application Support", "Steam"))
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			roots = append(roots, filepath.Join(dir, "Steam"))
		}
		roots = append(roots,
			join(".local", "share", "Steam"),
			join(".steam", "steam"),
			join(".steam", "root"),
			// The Debian package installs here.
			join(".steam", "debian-installation"),
			// Flatpak and Snap keep Steam in their own sandbox.
			join(".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
			join(".var", "app", "com.valvesoftware.Steam", ".steam", "steam"),
			join("snap", "steam", "common", ".local", "share", "Steam"),
			join("snap", "steam", "common", ".steam", "steam"),
		)
	}

	seen := make(map[string]bool, len(roots))
	unique := roots[:0]
	for _, root := range roots {
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		unique = append(unique, root)
	}
	return unique
}

// FindSteam returns the first Steam root holding a steamapps directory.
func FindSteam() (string, error) {
	for _, root := range SteamRoots() {
		if isDir(filepath.Join(root, steamAppsDir)) {
			return root, nil
		}
	}
	return "", ErrNotFound
}

// Library is one Steam library folder: where it is, and the apps Steam
// recorded in it.
type Library struct {
	Path string
	Apps []string
}

// Libraries reads the library folders a Steam root knows, the root itself
// first. A library listed in libraryfolders.vdf that is gone from disk is
// left out.
func Libraries(steamRoot string) ([]Library, error) {
	var libraries []Library
	seen := map[string]bool{}

	add := func(path string, apps []string) {
		path = filepath.Clean(path)
		if seen[path] || !isDir(filepath.Join(path, steamAppsDir)) {
			return
		}
		seen[path] = true
		libraries = append(libraries, Library{Path: path, Apps: apps})
	}

	add(steamRoot, nil)

	doc, err := vdf.ParseFile(filepath.Join(steamRoot, steamAppsDir, libraryFoldersVDF))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return libraries, nil
		}
		return libraries, err
	}

	// The file holds one key naming itself, spelled either way across the
	// versions of Steam that wrote it.
	folders := doc.Get("libraryfolders")
	if folders == nil {
		folders = doc.Get("LibraryFolders")
	}
	if folders == nil {
		return libraries, nil
	}

	for _, entry := range folders.Children {
		if entry.IsObject() {
			// The current format: a numbered object with a path key.
			var apps []string
			if appsNode := entry.Get("apps"); appsNode != nil {
				for _, app := range appsNode.Children {
					apps = append(apps, app.Key)
				}
			}
			add(entry.GetString("path"), apps)
			continue
		}
		// The older format: the number is the key and the path its value.
		add(entry.Value, nil)
	}
	return libraries, nil
}

// FindLibraryFor returns the library holding appID, by what Steam recorded
// in libraryfolders.vdf.
func FindLibraryFor(libraries []Library, appID string) (Library, bool) {
	for _, library := range libraries {
		for _, app := range library.Apps {
			if app == appID {
				return library, true
			}
		}
	}
	return Library{}, false
}

// isDir reports whether path names an existing directory.
func isDir(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
