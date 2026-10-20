package game

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/vdf"
)

const (
	// DeadlockAppID is Deadlock's Steam application id.
	DeadlockAppID = "1422450"
	// DirName is the game's directory inside steamapps/common.
	DirName = "Deadlock"

	// gameInfoRel is the file every Deadlock installation has, and the one
	// the game reads its mod search paths from.
	gameInfoRel = "game/citadel/gameinfo.gi"
	// AddonsRel is where the game loads mods from, relative to GamePath.
	AddonsRel = "game/citadel/addons"
)

// Install is a detected Deadlock installation.
type Install struct {
	// GamePath is the game's own directory, the one holding game/citadel.
	GamePath string
	// SteamRoot is the Steam installation owning the library, empty when the
	// user picked the directory by hand.
	SteamRoot string
	// LibraryPath is the Steam library holding the game, empty as above.
	LibraryPath string
	// BuildID is the Steam build the installation is at, empty when unknown.
	// The game overwrites gameinfo.gi when it updates, so the build says
	// whether the mod search paths survived.
	BuildID string
}

// AddonsDir returns the directory the game loads mods from.
func (i *Install) AddonsDir() string {
	return filepath.Join(i.GamePath, filepath.FromSlash(AddonsRel))
}

// GameInfoPath returns the gameinfo.gi the mod search paths live in.
func (i *Install) GameInfoPath() string {
	return filepath.Join(i.GamePath, filepath.FromSlash(gameInfoRel))
}

// Detect finds Deadlock through Steam, in every library Steam knows.
func Detect() (*Install, error) {
	steamRoot, err := FindSteam()
	if err != nil {
		return nil, fmt.Errorf("no Steam installation: %w", err)
	}
	return detectIn(steamRoot)
}

// Resolve returns the installation to use: the one at the directory the user
// pinned, or the one detection finds when they pinned none. A pinned directory
// is used as it is, so one that has gone stale is reported rather than quietly
// replaced by whatever detection would have found.
func Resolve(pinned string) (*Install, error) {
	if pinned != "" {
		return Validate(pinned)
	}
	return Detect()
}

// detectIn finds the game in the libraries of one Steam installation.
func detectIn(steamRoot string) (*Install, error) {
	libraries, err := Libraries(steamRoot)
	if err != nil {
		return nil, fmt.Errorf("reading the Steam libraries: %w", err)
	}
	for _, library := range libraries {
		gamePath := filepath.Join(library.Path, steamAppsDir, "common", DirName)
		install, err := Validate(gamePath)
		if err != nil {
			continue
		}
		install.SteamRoot = steamRoot
		install.LibraryPath = library.Path
		install.BuildID = buildID(library.Path)
		return install, nil
	}
	return nil, fmt.Errorf("Deadlock in the %d libraries of %s: %w", len(libraries), steamRoot, ErrNotFound)
}

// Validate reports whether path is a Deadlock installation, and describes
// it. The user may pick the directory by hand, so this is what checks it.
func Validate(path string) (*Install, error) {
	path = filepath.Clean(path)
	if _, err := os.Stat(filepath.Join(path, filepath.FromSlash(gameInfoRel))); err != nil {
		return nil, fmt.Errorf("%s: not a Deadlock installation: %w", path, ErrNotFound)
	}
	return &Install{GamePath: path}, nil
}

// buildID reads the build a library has the game installed at, from the app
// manifest Steam writes there. It returns "" when there is none.
func buildID(libraryPath string) string {
	manifest := filepath.Join(libraryPath, steamAppsDir, "appmanifest_"+DeadlockAppID+".acf")
	root, err := vdf.ParseFile(manifest)
	if err != nil {
		return ""
	}
	state := root.Get("AppState")
	if state == nil {
		return ""
	}
	return state.GetString("buildid")
}
