//go:build linux

package game

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// running reports whether the game has a process, read from the process list
// the kernel keeps at /proc.
func running() bool { return runningIn("/proc") }

// runningIn answers the same question out of a process list at root, which is
// /proc outside a test.
func runningIn(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		// /proc holds a directory per process, named by its number, beside the
		// kernel's own files.
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		pid := filepath.Join(root, entry.Name())
		// The kernel keeps the executable's name here, which under Proton is
		// the Windows program's.
		if name, err := os.ReadFile(filepath.Join(pid, "comm")); err == nil && isDeadlockName(string(name)) {
			return true
		}
		// The command line's first word is the other place a Wine or Proton
		// process keeps the program it is running.
		if line, err := os.ReadFile(filepath.Join(pid, "cmdline")); err == nil {
			first, _, _ := strings.Cut(string(line), "\x00")
			if isDeadlockName(first) {
				return true
			}
		}
	}
	return false
}
