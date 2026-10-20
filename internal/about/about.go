// Package about holds what the app says about itself, so that the version is
// written in one place rather than in every package that has to name it.
package about

// Version is the app's version.
const Version = "0.1"

// UserAgent identifies the app to the servers it reads from. GameBanana turns
// away requests without one, and its operators read it to find who is making
// too many; the address is there so that they can.
const UserAgent = "DeadlockModManager/" + Version + " (+https://github.com/BabyLy233/deadlock-mod-manager-mygo)"
