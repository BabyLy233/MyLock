// Package config stores the settings the app keeps between runs, in one JSON
// file under the platform's config directory.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// appDir is the directory the app keeps its files in, under the config
	// directory of the platform.
	appDir = "mylock"
	// legacyAppDir is where the app kept its files before it was renamed. What
	// is there is carried over once, so that the rename does not cost the user
	// the game directory it took them a dialog to pick.
	legacyAppDir = "deadlock-mod-manager"
	// fileName is the settings file inside appDir.
	fileName = "config.json"
)

// Config is what the app remembers between runs.
type Config struct {
	// GamePath is the Deadlock directory the user picked, empty when the app
	// detects the game through Steam instead.
	GamePath string `json:"gamePath,omitempty"`
	// ProfileFolder is the folder of the profile in use under citadel/addons,
	// empty for the default profile, which is that directory itself.
	ProfileFolder string `json:"profileFolder,omitempty"`
	// Theme is the appearance to run in: "system", "light" or "dark". Empty is
	// the system's, which is what a first run gets.
	Theme string `json:"theme,omitempty"`
	// FileserverPreference is the GameBanana download route chosen in the
	// settings: "default", "auto", or a file server's id. Empty is the
	// default, which is also what a first run gets.
	FileserverPreference string `json:"fileserverPreference,omitempty"`
	// FileserverLatencyMs is the last measured round trip to each file server,
	// by its id. It is only shown; a server that could not be measured keeps
	// what it had.
	FileserverLatencyMs map[string]int64 `json:"fileserverLatencyMs,omitempty"`
	// VanillaPending reports that the last launch the app made was one without
	// mods, and VanillaSeen that its game has been seen running. Together they
	// say that the game should be put back to loading the mods when the session
	// ends, even if the app is restarted in between.
	VanillaPending bool `json:"vanillaPending,omitempty"`
	VanillaSeen    bool `json:"vanillaSeen,omitempty"`
}

// Path returns the file the app keeps its settings in: ~/.config on Linux,
// %AppData% on Windows, and ~/Library/Application Support on macOS.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the config directory: %w", err)
	}
	return filepath.Join(dir, appDir, fileName), nil
}

// Adopt carries the settings an earlier version kept under the old name to
// path, when path does not exist yet. The old file is left where it is, so that
// a build from before the rename still finds its own settings.
func Adopt(path string) error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("finding the config directory: %w", err)
	}
	return adopt(filepath.Join(dir, legacyAppDir, fileName), path)
}

// adopt copies from to to, when to does not exist yet.
func adopt(from, to string) error {
	if _, err := os.Stat(to); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	data, err := os.ReadFile(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", from, err)
	}
	return writeFile(to, data)
}

// Load reads the settings at path. A file that is not there gives the zero
// Config and no error, which is the state of a first run.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the settings to path, creating the directory above it. The file
// is replaced in one step, so a crash or a full disk cannot leave a half
// written file behind for the next run to read.
func (c Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data)
}

// writeFile replaces path with data, creating the directory above it. The file
// appears at path whole or not at all.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	// A no-op once the rename below has moved the file out of the way.
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
