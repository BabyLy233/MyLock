package game

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// The game reads its mod search paths from gameinfo.gi, and only from there:
// files sitting in citadel/addons do nothing until a Game line inside its
// SearchPaths section names that directory. This is the section this app writes
// and maintains, marked so that it can be found again.
const (
	markerStart = "// Deadlock Mod Manager - Start"
	markerEnd   = "// Deadlock Mod Manager - End"

	// The original app's newer writer tags only the entries it owns instead of
	// the whole SearchPaths section. Both marker pairs are recognized so a file
	// left by either app can be patched without duplicating mounts.
	markerStartV2       = "// Deadlock Mod Manager - Start (SearchPaths v2)"
	markerStartV2Inline = "// Deadlock Mod Manager - Start (SearchPaths v2, inline)"
	markerEndV2         = "// Deadlock Mod Manager (SearchPaths v2) - End"

	// defaultSearchPath is what the game is told to search when a profile has
	// nothing enabled, which is also what the game ships with.
	defaultSearchPath = "citadel/addons"
)

// errNoMarkers reports a gameinfo.gi with no section of ours in it, which is
// what a game update leaves: it replaces the file with the one it shipped.
var errNoMarkers = errors.New("gameinfo.gi has no section this app wrote")

// writeMu keeps the read-patch-write in writeGameInfo one at a time. Two of
// them at once -- a switch being flipped while a session's mods are put back --
// would each patch the copy it read, and the later rename would undo the
// earlier one, leaving the game told to load a set of paths that does not
// match the profile.
var writeMu sync.Mutex

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
	_, _, legacyErr := markerSpan(content)
	_, _, v2OK, v2Err := v2MarkerSpan(content)
	return GameInfo{
		Path:     path,
		ModPaths: strings.Contains(content, defaultSearchPath),
		Markers:  legacyErr == nil || (v2Err == nil && v2OK),
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
	return i.writeGameInfo(
		markerStart+"\n"+section(paths)+"\n"+markerEnd,
		v2Block(paths),
	)
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
	return i.writeGameInfo(vanillaSection(), "")
}

// writeGameInfo puts block in place of the section of the game's configuration
// that holds the search paths, and reports whether the file changed. A file
// holding the original app's per-entry markers has only that block replaced,
// so the entries around it are left alone.
func (i *Install) writeGameInfo(block, v2Block string) (bool, error) {
	writeMu.Lock()
	defer writeMu.Unlock()

	path := i.GameInfoPath()
	original, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(original)

	patched, err := patchGameInfo(content, block, v2Block)
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
func patchGameInfo(content, block, v2Block string) (string, error) {
	if start, end, ok, err := v2MarkerSpan(content); err != nil {
		return "", err
	} else if ok {
		return content[:start] + matchLineEndings(v2Block, content) + content[end:], nil
	}
	start, end, err := markerSpan(content)
	if err != nil {
		if start, end, err = searchPathsSpan(content); err != nil {
			return "", err
		}
		// A file with no section of ours may hold mounts another tool added.
		// They are not this app's to drop, so they move into the rebuilt
		// section ahead of the game's own entries.
		foreign, err := foreignSearchLines(content[start:end])
		if err != nil {
			return "", err
		}
		block = injectForeign(block, foreign)
	}
	return content[:start] + matchLineEndings(block, content) + content[end:], nil
}

// foreignSearchLines returns the entries of a SearchPaths section this app does
// not own: everything but the language lines and the base citadel/core mounts.
// A nested block is refused rather than moved, since its braces cannot be
// preserved line by line.
func foreignSearchLines(section string) ([]string, error) {
	open := strings.IndexByte(section, '{')
	if open < 0 {
		return nil, nil
	}
	close := strings.LastIndexByte(section, '}')
	if close < open {
		return nil, nil
	}
	var lines []string
	for _, line := range strings.Split(section[open+1:close], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, "{}") {
			return nil, errors.New("the SearchPaths section holds a nested block; the file was left unchanged")
		}
		if strings.HasPrefix(trimmed, "//") {
			lines = append(lines, trimmed)
			continue
		}
		fields := strings.Fields(trimmed)
		key := strings.Trim(fields[0], `"`)
		value := ""
		if len(fields) > 1 {
			value = strings.Trim(fields[1], `"`)
		}
		switch key {
		case "Game_UILanguage", "Game_LowViolence":
			continue
		case "Game", "Mod", "Write":
			switch strings.ToLower(value) {
			case "citadel", "core":
				continue
			}
		}
		lines = append(lines, trimmed)
	}
	return lines, nil
}

// injectForeign puts the preserved entries just before the section's closing
// brace.
func injectForeign(block string, foreign []string) string {
	if len(foreign) == 0 {
		return block
	}
	lines := strings.Split(block, "\n")
	at := len(lines) - 1
	for at >= 0 && strings.TrimSpace(lines[at]) != "}" {
		at--
	}
	if at < 0 {
		return block
	}
	kept := make([]string, 0, len(lines)+len(foreign))
	kept = append(kept, lines[:at]...)
	for _, line := range foreign {
		kept = append(kept, "            "+line)
	}
	kept = append(kept, lines[at:]...)
	return strings.Join(kept, "\n")
}

// v2MarkerSpan returns the lines the original app's newer per-entry block
// occupies.
func v2MarkerSpan(content string) (start, end int, ok bool, err error) {
	at := -1
	for _, marker := range []string{markerStartV2, markerStartV2Inline} {
		if found := strings.Index(content, marker); found >= 0 && (at < 0 || found < at) {
			at = found
		}
	}
	if at < 0 {
		return 0, 0, false, nil
	}
	stop := strings.Index(content[at:], markerEndV2)
	if stop < 0 {
		return 0, 0, false, errors.New("the start marker is there but the end marker is not")
	}
	end = at + stop + len(markerEndV2)
	return lineStart(content, at), lineEnd(content, end), true, nil
}

// v2Block renders paths in the newer per-entry format, so a file written by
// the original app keeps its format instead of gaining a nested SearchPaths
// block.
func v2Block(paths []string) string {
	if len(paths) == 0 {
		paths = []string{defaultSearchPath}
	}
	lines := []string{markerStartV2}
	for _, path := range paths {
		lines = append(lines, entry("Game", path))
	}
	lines = append(lines, markerEndV2)
	return strings.Join(lines, "\n")
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
