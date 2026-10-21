//go:build linux

package game

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeProcess is one entry of a process list: the executable name the kernel
// reports, the command line, or both, as a real process can have.
type fakeProcess struct {
	pid     string
	comm    string
	cmdline string
}

// fakeProc writes a process list holding these processes, and returns its
// root, which stands in for /proc.
func fakeProc(t *testing.T, processes ...fakeProcess) string {
	t.Helper()
	root := t.TempDir()
	// /proc holds the kernel's own files beside the processes, which are the
	// directories named by a number.
	if err := os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 0 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		dir := filepath.Join(root, process.pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if process.comm != "" {
			if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(process.comm+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if process.cmdline != "" {
			if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(process.cmdline), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// The kernel's own name for the process is the first place the game is found.
func TestRunningFindsTheGameByItsName(t *testing.T) {
	root := fakeProc(t,
		fakeProcess{pid: "1", comm: "systemd"},
		fakeProcess{pid: "4242", comm: "deadlock.exe"},
	)

	if !runningIn(root) {
		t.Error("the game is running, but the process list did not find it")
	}
}

// Under Proton the command line names the Windows executable, which is the
// other place the game shows up.
func TestRunningFindsTheGameInItsCommandLine(t *testing.T) {
	root := fakeProc(t,
		fakeProcess{pid: "7", comm: "wineserver"},
		fakeProcess{pid: "4242", comm: "proton", cmdline: `Z:\home\user\Deadlock\game\bin\win64\deadlock.exe` + "\x00-console"},
	)

	if !runningIn(root) {
		t.Error("the game is running, but the process list did not find it")
	}
}

// Nothing that is merely named like the game counts.
func TestRunningWithoutTheGame(t *testing.T) {
	root := fakeProc(t,
		fakeProcess{pid: "1", comm: "systemd"},
		fakeProcess{pid: "2", comm: "deadlock"},
		fakeProcess{pid: "3", comm: "notdeadlock.exe"},
	)

	if runningIn(root) {
		t.Error("the game is not running, but the process list said it was")
	}
}

// A process list that cannot be read answers that the game is not running,
// which leaves the app usable rather than stuck.
func TestRunningWithNoProcessList(t *testing.T) {
	if runningIn(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a process list that is not there reported the game as running")
	}
}
