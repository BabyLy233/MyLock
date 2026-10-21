package game

import "strings"

// processName is what the game's process is called, as the operating system
// reports it. The game is Windows-only, so the process is this name even on
// Linux and macOS, where it runs under Proton or Wine.
const processName = "deadlock.exe"

// Running reports whether Deadlock is running.
//
// It is a look at the process list taken when it is called, not a lock: the
// game can start a moment after it answers false, and one that starts outside
// the app cannot be made atomic with anything here. What it is good for is the
// answer being as fresh as a process scan can make it.
func Running() bool { return running() }

// isDeadlockName reports whether name is the game's, which the process list
// gives as a bare name on some systems and as a path on others.
func isDeadlockName(name string) bool {
	name = strings.TrimSpace(name)
	// Wine and Proton report the Windows path the game was started from, which
	// the operating system's own separator does not describe.
	if at := strings.LastIndexAny(name, `/\`); at >= 0 {
		name = name[at+1:]
	}
	return strings.EqualFold(name, processName)
}
