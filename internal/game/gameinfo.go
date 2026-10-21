package game

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// The game reads its mod search paths from gameinfo.gi, and only from there:
// files sitting in citadel/addons do nothing until a Game line inside its
// SearchPaths section names that directory. This is the section this app writes
// and maintains, marked so that it can be found again.
const (
	markerStart = "// Deadlock Mod Manager - Start"
	markerEnd   = "// Deadlock Mod Manager - End"

	// defaultSearchPath is what the game is told to search when a profile has
	// nothing enabled, which is also what the game ships with.
	defaultSearchPath = "citadel/addons"
)

// errNoMarkers reports a gameinfo.gi with no section of ours in it, which is
// what a game update leaves: it replaces the file with the one it shipped.
var errNoMarkers = errors.New("gameinfo.gi has no section this app wrote")

// GameInfo is the state of an installation's gameinfo.gi.
type GameInfo struct {
	// Path is the file itself.
	Path string
	// ModPaths reports whether the game is told to search the mod directories.
	ModPaths bool
	// Markers reports whether the section this app writes is still in the file.
	// A game update takes it with it, which is how a mod install that has
	// quietly stopped working shows itself.
	Markers bool
	// Backup reports whether the file as it shipped is kept beside it.
	Backup bool
}

// ReadGameInfo reports what state the installation's gameinfo.gi is in.
func (i *Install) ReadGameInfo() (GameInfo, error) {
	path := i.GameInfoPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return GameInfo{Path: path}, fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(data)
	_, err = os.Stat(path + ".bak")
	return GameInfo{
		Path:     path,
		ModPaths: strings.Contains(content, defaultSearchPath),
		Markers:  strings.Contains(content, markerStart) && strings.Contains(content, markerEnd),
		Backup:   err == nil,
	}, nil
}

// PatchGameInfo writes the mod search paths into gameinfo.gi and reports
// whether the file changed. paths are the directories to search, relative to
// the game directory, the first having the highest precedence.
//
// A section this app wrote before is replaced where it stands. A file without
// one, which is what a game update leaves behind, has its SearchPaths section
// rebuilt around a new one, so the game loads mods again without the user
// having to reinstall anything.
func (i *Install) PatchGameInfo(paths []string) (bool, error) {
	return i.writeGameInfo(markerStart + "\n" + section(paths) + "\n" + markerEnd)
}

// PatchGameInfoVanilla puts the search paths the game ships with back into
// gameinfo.gi, and reports whether the file changed. It is what a launch
// without mods is given.
//
// Nothing of this app's is left in the file: no mod directory is named, the two
// Mod lines that let a mod's files stand in for the game's own are gone with
// them, and so are the markers. What is left is the game's own configuration,
// which is also what the next patch finds its way back from.
func (i *Install) PatchGameInfoVanilla() (bool, error) {
	return i.writeGameInfo(vanillaSection())
}

// writeGameInfo puts block in place of the section of the game's configuration
// that holds the search paths, and reports whether the file changed.
func (i *Install) writeGameInfo(block string) (bool, error) {
	path := i.GameInfoPath()
	original, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(original)

	patched, err := patchGameInfo(content, block)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if patched == content {
		return false, nil
	}
	// Refuse to hand the game a file this app broke. The section is the only
	// thing changed, so this is about the splice going wrong, not the file
	// having been strange to begin with.
	if err := validateGameInfo(patched); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}

	// The file as it was before the first change is kept, so that the game can
	// be put back the way it shipped.
	backup := path + ".bak"
	if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(backup, original, 0o644); err != nil {
			return false, fmt.Errorf("writing %s: %w", backup, err)
		}
	}

	// Written beside the file and renamed over it, so that a crash partway
	// cannot leave the game with half a configuration and no way to start.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(patched), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("replacing %s: %w", path, err)
	}
	return true, nil
}

// patchGameInfo returns gameinfo.gi with block in place of the section this app
// wrote, or -- in a file holding none, which is what a game update or a launch
// without mods leaves -- in place of the SearchPaths section it reproduces.
func patchGameInfo(content, block string) (string, error) {
	start, end, err := markerSpan(content)
	if err != nil {
		if start, end, err = searchPathsSpan(content); err != nil {
			return "", err
		}
	}
	return content[:start] + matchLineEndings(block, content) + content[end:], nil
}

// markerSpan returns the lines the section this app wrote occupies, the newline
// at each end left out. Leaving them out is what makes a second patch of the
// same file change nothing: the lines around the section are not touched.
func markerSpan(content string) (start, end int, err error) {
	at := strings.Index(content, markerStart)
	if at < 0 {
		return 0, 0, errNoMarkers
	}
	stop := strings.Index(content[at:], markerEnd)
	if stop < 0 {
		return 0, 0, errors.New("the start marker is there but the end marker is not")
	}
	end = at + stop + len(markerEnd)
	return lineStart(content, at), lineEnd(content, end), nil
}

// searchPathsSpan returns the lines the SearchPaths section occupies, which is
// what is replaced when the file holds no section of ours.
func searchPathsSpan(content string) (start, end int, err error) {
	at := strings.Index(content, "SearchPaths")
	if at < 0 {
		return 0, 0, errors.New("the SearchPaths section is gone")
	}
	open := strings.IndexByte(content[at:], '{')
	if open < 0 {
		return 0, 0, errors.New("the SearchPaths section has no opening brace")
	}
	closed := strings.IndexByte(content[at+open:], '}')
	if closed < 0 {
		return 0, 0, errors.New("the SearchPaths section is not closed")
	}
	return lineStart(content, at), lineEnd(content, at+open+closed+1), nil
}

// lineStart returns where the line holding at begins.
func lineStart(content string, at int) int {
	if nl := strings.LastIndexByte(content[:at], '\n'); nl >= 0 {
		return nl + 1
	}
	return 0
}

// lineEnd returns where the line holding the character before at ends, the
// newline that follows it left out.
func lineEnd(content string, at int) int {
	nl := strings.IndexByte(content[at:], '\n')
	if nl < 0 {
		return len(content)
	}
	end := at + nl
	// A carriage return belongs to the line ending, not to the line.
	if end > 0 && content[end-1] == '\r' {
		end--
	}
	return end
}

// matchLineEndings gives the block the endings the file uses, so that patching
// a file with Windows endings does not leave it holding both kinds.
func matchLineEndings(block, content string) string {
	if nl := strings.IndexByte(content, '\n'); nl > 0 && content[nl-1] == '\r' {
		return strings.ReplaceAll(block, "\n", "\r\n")
	}
	return block
}

// validateGameInfo refuses a file this app should not hand the game.
func validateGameInfo(content string) error {
	at := strings.Index(content, "SearchPaths")
	if at < 0 {
		return errors.New("the SearchPaths section is gone")
	}
	open := strings.IndexByte(content[at:], '{')
	if open < 0 {
		return errors.New("the SearchPaths section has no opening brace")
	}
	if !strings.Contains(content[at+open:], "}") {
		return errors.New("the SearchPaths section is not closed")
	}
	return nil
}

// section returns the SearchPaths block the game is given, with a Game line for
// every directory it is to search, the first searched last and so winning over
// the ones after it.
//
// The text is the Tauri app's, byte for byte, down to the spaces the game's own
// file carries at the end of two of the lines. They mean nothing to the parser;
// they are here so that the two apps do not rewrite each other's work.
func section(paths []string) string {
	if len(paths) == 0 {
		paths = []string{defaultSearchPath}
	}

	lines := []string{
		"",
		"\t\tSearchPaths",
		"        {  ",
		entry("Game_UILanguage", "citadel_*LANGUAGE*"),
		entry("Game_LowViolence", "citadel_lv"),
	}
	for _, path := range paths {
		lines = append(lines, entry("Game", path))
	}
	return strings.Join(append(lines,
		entry("Mod", "citadel"),
		entry("Write", "citadel"+"          "),
		entry("Game", "citadel"),
		entry("Mod", "core"),
		entry("Write", "core"),
		entry("Game", "core"+"        "),
		"        }",
	), "\n")
}

// vanillaSection is the block the game ships with, which is what a launch
// without mods is given. It is the block above with the mod directory taken out
// and the two Mod lines along with it: with no mod directory named, and nothing
// let to stand in for the game's own files, the game loads nothing but itself.
//
// The lines are the Tauri app's, byte for byte, as section's are. The one
// difference is a blank line: the Tauri app's block opens with a newline, which
// a file with no section of ours has nothing to replace, so it lands above the
// SearchPaths line and stays there -- and every launch without mods, finding no
// markers again, adds another. Here the block begins at the line it replaces,
// so a launch that changes nothing writes nothing.
func vanillaSection() string {
	return strings.Join([]string{
		"\t\tSearchPaths",
		"        {  ",
		entry("Game_UILanguage", "citadel_*LANGUAGE*"),
		entry("Game_LowViolence", "citadel_lv"),
		entry("Game", "citadel"),
		entry("Write", "citadel"+"          "),
		entry("Game", "citadel"),
		entry("Write", "core"),
		entry("Game", "core"+"        "),
		"        }",
	}, "\n")
}

// entry is one line of the block: twelve spaces, the key padded to twenty
// columns, then the value.
func entry(key, value string) string {
	return "            " + key + strings.Repeat(" ", 20-len(key)) + value
}
