package game

import "testing"

// The game is named by the executable, whether the operating system reports a
// bare name or the path it was started from.
func TestIsDeadlockName(t *testing.T) {
	for _, name := range []string{
		"deadlock.exe",
		"Deadlock.EXE",
		"deadlock.exe\n",
		"/home/user/.steam/steamapps/common/Deadlock/game/bin/win64/deadlock.exe",
		`Z:\home\user\Deadlock\game\bin\win64\deadlock.exe`,
	} {
		if !isDeadlockName(name) {
			t.Errorf("%q is not read as the game", name)
		}
	}

	for _, name := range []string{
		"",
		"deadlock",
		"deadlock.exe.so",
		"notdeadlock.exe",
		"reaper",
	} {
		if isDeadlockName(name) {
			t.Errorf("%q is read as the game", name)
		}
	}
}
