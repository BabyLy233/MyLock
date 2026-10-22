// Command mylock manages mods for Valve's Deadlock. Its window is Mygo's native
// UI: the interface is Go, drawn on the GPU, with no web page and no webview.
//
// This file is only the entry point. The window and everything behind it live
// in internal/app.
package main

import "github.com/BabyLy233/mylock/internal/app"

func main() {
	app.Run()
}
