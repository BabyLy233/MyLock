// Package about holds what the app says about itself, so that the version is
// written in one place rather than in every package that has to name it.
package about

import (
	"runtime/debug"
	"sync"
)

// Version is the app's version, which main sets from mygo.App.Version() when
// the build carries one. Tests, and builds without it, say "dev".
var Version = "dev"

// UserAgent identifies the app to the servers it reads from. GameBanana turns
// away requests without one, and its operators read it to find who is making
// too many; the address is there so that they can.
func UserAgent() string {
	return "MyLock/" + Version + " (+https://github.com/BabyLy233/mylock)"
}

// Commit returns the short hash of the git commit the app was built from:
// seven characters, with "-dirty" when the working tree had changes, or
// "unknown" when the build carries no version control information.
func Commit() string { return commit() }

// commit is computed once, as the settings page asks for it on every frame.
var commit = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return shortCommit(info.Settings)
})

// shortCommit returns the revision of settings, the build settings of a
// binary, in seven characters, and marks a modified working tree.
func shortCommit(settings []debug.BuildSetting) string {
	var revision string
	dirty := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision == "" {
		return "unknown"
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if dirty {
		revision += "-dirty"
	}
	return revision
}
