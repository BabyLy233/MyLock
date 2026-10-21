package config

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// libraryFileName is the file beside config.json that remembers what the app
// knows about the mods it installed. The manifest says which files a mod has;
// this says what to call it and what picture to show beside it.
const libraryFileName = "library.json"

// ModInfo is what the app knows about one installed mod, by the id its manifest
// entry is keyed by. A mod the app installed from the store has all of it; one
// installed by the Tauri build, or put there by hand, has none until it is
// looked up.
type ModInfo struct {
	// Name is the title the author gave it.
	Name string `json:"name,omitempty"`
	// Kind is "Mod", "Sound" or "Wip", which is the listing it came from.
	Kind string `json:"kind,omitempty"`
	// Category and Subcategory are where it is filed, such as Skins / Yamato.
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	// Author is the submitter's display name.
	Author string `json:"author,omitempty"`
	// Version is the author's own, often empty.
	Version string `json:"version,omitempty"`
	// Thumb is the small picture a card shows, and Page is its page on the
	// site. Either is empty when the API gave none.
	Thumb string `json:"thumb,omitempty"`
	Page  string `json:"page,omitempty"`
	// Installed is when the app installed it. It is zero for one that was
	// found already installed and only looked up.
	Installed time.Time `json:"installed,omitzero"`
}

// Library is the whole library file: what the app knows, by mod id.
type Library struct {
	Mods map[string]ModInfo `json:"mods,omitempty"`
}

// LibraryPath returns the library file that belongs beside the settings file at
// configPath, empty when there is no settings file to sit beside.
func LibraryPath(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), libraryFileName)
}

// LoadLibrary reads the library at path. A file that is not there is a first
// run, which knows no mods yet and is not an error.
func LoadLibrary(path string) (Library, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Library{}, nil
	}
	if err != nil {
		return Library{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var library Library
	if err := json.Unmarshal(data, &library); err != nil {
		return Library{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return library, nil
}

// Save writes the library to path, creating the directory above it. The file is
// replaced in one step, as the settings file is.
func (l Library) Save(path string) error {
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data)
}
