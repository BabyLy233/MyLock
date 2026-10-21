package game

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// steamRunURI asks Steam to start Deadlock. Whatever a launch is given goes
// after the two slashes, which is what the empty space between them is for.
const steamRunURI = "steam://run/" + DeadlockAppID + "//"

// handOverWait is how long the launcher is given to hand the request over, or
// to say that it could not. A launcher still running after that is Steam itself
// starting up, which is neither a failure nor something to keep the window
// waiting for.
const handOverWait = 8 * time.Second

// saidBytes is how much of a launcher's complaint is kept, which is a line or
// two of it: enough to say what went wrong, and not the whole of a log.
const saidBytes = 2 << 10

// Launch asks Steam to start the game, and returns once the request has been
// handed over. It does not wait for the game: Steam owns that process, and it
// outlives this one by hours.
//
// The launcher itself is waited for, because it is what says the request could
// not be handed over -- Steam not being installed, or nothing being registered
// to take a steam:// address -- and a button that quietly does nothing is worse
// than one that says why. It is given only so long to say so, because the
// launcher of a Steam that is not yet running stays in the foreground until
// Steam is closed.
func (i *Install) Launch() error {
	program, args := launchCommand(runtime.GOOS, i.SteamRoot, steamRunURI)

	// What the launcher says about failing goes to a file rather than to a pipe
	// this process reads. A pipe whose reader is gone is one for a writer to be
	// killed by, and the writer here is a launcher that outlives this app's
	// interest in it, with the Steam it started holding the same pipe.
	said, err := os.CreateTemp("", "mylock-steam-*.log")
	if err != nil {
		return fmt.Errorf("记录启动程序的输出：%w", err)
	}

	cmd := exec.Command(program, args...)
	cmd.Stderr = said

	if err := cmd.Start(); err != nil {
		said.Close()
		os.Remove(said.Name())
		return fmt.Errorf("运行 %s：%w", program, err)
	}

	// What the launcher left behind, read and cleared away by whoever is
	// finished with it -- which is this goroutine even when the wait below has
	// been given up on.
	type outcome struct {
		err  error
		said string
	}
	done := make(chan outcome, 1)
	go func() {
		err := cmd.Wait()
		out := outcome{err: err}
		if err != nil {
			out.said = firstWords(said)
		}
		said.Close()
		os.Remove(said.Name())
		done <- out
	}()

	select {
	case out := <-done:
		if out.err == nil {
			return nil
		}
		if out.said != "" {
			return fmt.Errorf("%s 没能把启动请求交给 Steam：%w：%s", program, out.err, out.said)
		}
		return fmt.Errorf("%s 没能把启动请求交给 Steam：%w", program, out.err)
	case <-time.After(handOverWait):
		// Still running, which is Steam coming up. It goes on without us, and
		// the goroutine above reaps it whenever it ends.
		return nil
	}
}

// launchCommand returns the program to hand a Steam URI to, and the arguments
// to give it, for a Steam installation rooted at steamRoot. A root that is
// empty -- the game directory having been picked by hand rather than found
// through Steam -- leaves it to whatever the desktop registered for the
// address.
//
// The game is asked for through Steam rather than run itself: Steam is what
// keeps it up to date, provides the runtime it needs, and expects to have
// started it.
func launchCommand(goos, steamRoot, uri string) (string, []string) {
	switch goos {
	case "windows":
		if exe := inSteamRoot(steamRoot, "steam.exe"); exe != "" {
			return exe, []string{uri}
		}
		// The shell's own handler for the address, which is registered by
		// whatever installed Steam, wherever it put it.
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", uri}
	case "darwin":
		// The Steam client registers the address with the system, and open is
		// what hands it over.
		return "open", []string{uri}
	default:
		if sh := inSteamRoot(steamRoot, "steam.sh"); sh != "" {
			return sh, []string{uri}
		}
		// xdg-open reaches whatever handles the address, which is how a Steam
		// inside a Flatpak or Snap sandbox is started from outside it.
		return "xdg-open", []string{uri}
	}
}

// inSteamRoot returns the path of name inside a Steam installation, or "" when
// there is no installation to look in or nothing of that name in it.
func inSteamRoot(steamRoot, name string) string {
	if steamRoot == "" {
		return ""
	}
	path := filepath.Join(steamRoot, name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return ""
	}
	return path
}

// firstWords reads back the first of what a launcher wrote to the file it was
// given, which is the part of it worth showing.
func firstWords(f *os.File) string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, saidBytes))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
