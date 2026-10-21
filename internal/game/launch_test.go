package game

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// steamRoot makes a Steam installation holding the named launcher, which is
// what a launch is handed to. A root can be given both, as the branches that
// pick between them are read off one directory.
func steamRoot(t *testing.T, launchers ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range launchers {
		if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A launch is the platform's own launcher, given the address and nothing else.
func TestLaunchCommandHandsTheAddressToThePlatformsLauncher(t *testing.T) {
	root := steamRoot(t, "steam.sh", "steam.exe")

	for _, tt := range []struct{ goos, want string }{
		{"windows", filepath.Join(root, "steam.exe")},
		{"linux", filepath.Join(root, "steam.sh")},
		// macOS hands the address to the system, which knows where Steam is.
		{"darwin", "open"},
	} {
		program, args := launchCommand(tt.goos, root, steamRunURI)
		if program != tt.want {
			t.Errorf("on %s the launcher is %q, want %q", tt.goos, program, tt.want)
		}
		if len(args) != 1 || args[0] != steamRunURI {
			t.Errorf("on %s the launcher is given %q, want the address alone", tt.goos, args)
		}
	}
}

// A game directory picked by hand leaves no Steam installation to run, so the
// address goes to whatever the desktop registered for it.
func TestLaunchCommandWithoutASteamInstallation(t *testing.T) {
	for _, tt := range []struct{ goos, want string }{
		{"windows", "rundll32.exe"},
		{"linux", "xdg-open"},
	} {
		for _, root := range []string{"", steamRoot(t)} {
			program, args := launchCommand(tt.goos, root, steamRunURI)
			if program != tt.want {
				t.Errorf("on %s with Steam root %q the launcher is %q, want %q",
					tt.goos, root, program, tt.want)
			}
			if len(args) == 0 || args[len(args)-1] != steamRunURI {
				t.Errorf("on %s with Steam root %q the launcher is given %q, want the address",
					tt.goos, root, args)
			}
		}
	}
}

// The address is the one Steam starts Deadlock with, which is the whole of what
// the app asks for.
func TestTheAddressAsksSteamForDeadlock(t *testing.T) {
	if steamRunURI != "steam://run/"+DeadlockAppID+"//" {
		t.Errorf("the address is %q, want one naming Deadlock", steamRunURI)
	}
}

// fakeLauncher writes a launcher that records what it is given, and returns the
// installation to launch with and the file the record is written to.
func fakeLauncher(t *testing.T, body string) (*Install, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("the fake launcher is a steam.sh, which is the launcher on Linux alone")
	}
	root := steamRoot(t)
	record := filepath.Join(t.TempDir(), "record")
	script := fmt.Sprintf("#!/bin/sh\n%s\n", strings.ReplaceAll(body, "RECORD", record))
	if err := os.WriteFile(filepath.Join(root, "steam.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Install{GamePath: t.TempDir(), SteamRoot: root}, record
}

// Launching runs the launcher, and what the game is started with is the address
// it was given.
func TestLaunchHandsTheAddressOver(t *testing.T) {
	install, record := fakeLauncher(t, `printf '%s' "$@" > RECORD`)

	if err := install.Launch(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != steamRunURI {
		t.Errorf("the launcher was given %q, want %q", got, steamRunURI)
	}
}

// A launcher that cannot hand the request over says so, and what it said is
// part of what the user is told: it is the only thing that knows why.
func TestLaunchReportsWhatTheLauncherSaid(t *testing.T) {
	install, _ := fakeLauncher(t, `echo 'no handler for steam://' >&2; exit 3`)

	err := install.Launch()
	if err == nil {
		t.Fatal("a launcher that failed reported no error")
	}
	if !strings.Contains(err.Error(), "no handler for steam://") {
		t.Errorf("the error does not carry what the launcher said: %v", err)
	}
	if !strings.Contains(err.Error(), "steam.sh") {
		t.Errorf("the error does not name the launcher: %v", err)
	}
}
