//go:build darwin

package game

import (
	"os/exec"
	"strings"
)

// running reports whether the game has a process. The game is Windows-only, so
// what can be running here is a copy under Wine or CrossOver, and ps is the
// system's own way of listing it.
func running() bool {
	out, err := exec.Command("ps", "-axo", "comm=").Output()
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if isDeadlockName(line) {
			return true
		}
	}
	return false
}
