//go:build !linux && !windows && !darwin

package game

// running reports whether the game has a process. This platform is not one the
// game is played on, so there is nothing to look for.
func running() bool { return false }
