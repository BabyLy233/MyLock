package config

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// profilesFileName is the file beside config.json that remembers the profiles
// the user named. The mods themselves stay in their profile's folder, and the
// default profile, which is the addons directory itself, is not listed here.
const profilesFileName = "profiles.json"

// Profile is one named profile under citadel/addons.
type Profile struct {
	// Folder is the directory the profile's files are in, always a directory
	// under the addons directory.
	Folder string `json:"folder"`
	// Name is what the user calls it. Renaming changes this and nothing else.
	Name string `json:"name"`
	// Description is the user's own note, empty when there is none.
	Description string `json:"description,omitempty"`
	// Created is when the profile was made, and LastUsed when it was last
	// switched to. A profile found on disk from another build has the folder's
	// date as Created.
	Created  time.Time `json:"created,omitzero"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

// Profiles is the whole profiles file.
type Profiles struct {
	Profiles []Profile `json:"profiles"`
}

// ProfilesPath returns the profiles file that belongs beside the settings file
// at configPath, empty when there is no settings file to sit beside.
func ProfilesPath(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), profilesFileName)
}

// LoadProfiles reads the profiles at path. A file that is not there is a first
// run, which has no named profiles and is not an error.
func LoadProfiles(path string) (Profiles, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Profiles{}, nil
	}
	if err != nil {
		return Profiles{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var p Profiles
	if err := json.Unmarshal(data, &p); err != nil {
		return Profiles{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return p, nil
}

// Save writes the profiles to path, creating the directory above it. The file
// is replaced in one step, as the settings file is.
func (p Profiles) Save(path string) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data)
}
