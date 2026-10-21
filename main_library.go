package main

import (
	"context"
	"log"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/gb"
)

// modInfoFromSubmission is what the app keeps about a catalogue submission. A
// zero installed means the mod was found already installed and only looked up.
func modInfoFromSubmission(s gb.Submission, installed time.Time) config.ModInfo {
	return config.ModInfo{
		Name:        s.Name,
		Kind:        string(s.Kind),
		Category:    s.Category,
		Subcategory: s.Subcategory,
		Author:      s.Author,
		Version:     s.Version,
		Thumb:       s.Thumb,
		Page:        s.Page(),
		Installed:   installed,
	}
}

// submissionRef reads a manifest's mod id back into the catalogue: the number
// is a mod, and the prefixed forms are a sound pack or a work in progress,
// whose listings number separately.
func submissionRef(id string) (gb.Kind, int, bool) {
	kind, number := gb.Mod, id
	switch {
	case strings.HasPrefix(id, "snd-"):
		kind, number = gb.Sound, strings.TrimPrefix(id, "snd-")
	case strings.HasPrefix(id, "wip-"):
		kind, number = gb.Wip, strings.TrimPrefix(id, "wip-")
	}
	n, err := strconv.Atoi(number)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return kind, n, true
}

// modInfos returns a copy of what the app knows, so that a frame can read it
// once rather than taking the lock for every card.
func (a *app) modInfos() map[string]config.ModInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return maps.Clone(a.library)
}

// modInfo returns what the app knows about one mod, zero when it knows nothing.
func (a *app) modInfo(id string) config.ModInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.library[id]
}

// saveLibrary writes the library file, one write at a time with the other
// operations that touch it.
func (a *app) saveLibrary(mods map[string]config.ModInfo) error {
	path := config.LibraryPath(a.configPath)
	if path == "" {
		return nil
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	return config.Library{Mods: mods}.Save(path)
}

// rememberMod records what the store knew about a mod it installed, so that the
// mods page can call it by its name and show its picture. A failure to write it
// is logged rather than shown: the mod is installed either way, and only the
// next run would have to look the details up again.
func (a *app) rememberMod(mod gb.Submission) {
	if mod.ID <= 0 {
		return
	}
	id := mod.Slug()
	info := modInfoFromSubmission(mod, time.Now())

	a.mu.Lock()
	if a.library == nil {
		a.library = make(map[string]config.ModInfo)
	}
	a.library[id] = info
	mods := maps.Clone(a.library)
	a.mu.Unlock()

	if err := a.saveLibrary(mods); err != nil {
		log.Printf("remembering mod %s: %v", id, err)
	}
	a.invalidate()
}

// fillModInfo looks up the mods the library does not know yet. A manifest only
// has ids, so a mod installed by the Tauri build or by hand shows its id until
// the catalogue answers. The lookups run off the main thread, and the page
// redraws when they are done.
func (a *app) fillModInfo(mods []addons.Mod) {
	a.mu.Lock()
	var ids []string
	for _, mod := range mods {
		if _, known := a.library[mod.ID]; known {
			continue
		}
		if a.libraryBusy[mod.ID] {
			continue
		}
		if a.libraryBusy == nil {
			a.libraryBusy = make(map[string]bool)
		}
		a.libraryBusy[mod.ID] = true
		ids = append(ids, mod.ID)
	}
	a.mu.Unlock()
	if len(ids) == 0 {
		return
	}

	go func() {
		found := make(map[string]config.ModInfo, len(ids))
		for _, id := range ids {
			kind, number, ok := submissionRef(id)
			if !ok {
				continue
			}
			profile, err := a.catalogue.Profile(context.Background(), kind, number)
			if err != nil || profile.Submission.ID <= 0 {
				// Offline or gone: the id stays as it is, and a later read of
				// the mods asks again.
				continue
			}
			found[id] = modInfoFromSubmission(profile.Submission, time.Time{})
		}

		a.mu.Lock()
		for _, id := range ids {
			delete(a.libraryBusy, id)
		}
		if len(found) > 0 {
			if a.library == nil {
				a.library = make(map[string]config.ModInfo)
			}
			maps.Copy(a.library, found)
		}
		library := maps.Clone(a.library)
		a.mu.Unlock()

		if len(found) == 0 {
			return
		}
		if err := a.saveLibrary(library); err != nil {
			log.Printf("remembering the mods that were looked up: %v", err)
		}
		a.invalidate()
	}()
}

// modMeta is the line under a card's name: who made it, where it is filed, its
// version, and the id the manifest uses. The id is always there, so a mod the
// catalogue did not answer for is still identifiable.
func modMeta(id string, info config.ModInfo) string {
	parts := make([]string, 0, 4)
	if info.Author != "" {
		parts = append(parts, info.Author)
	}
	if info.Category != "" {
		category := info.Category
		if info.Subcategory != "" {
			category += " / " + info.Subcategory
		}
		parts = append(parts, category)
	}
	if info.Version != "" {
		parts = append(parts, info.Version)
	}
	parts = append(parts, "#"+id)
	return strings.Join(parts, " · ")
}
