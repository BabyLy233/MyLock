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
	appDir = "deadlock-mod-manager"
	// fileName is the settings file inside appDir.
	fileName = "config.json"
)

// Config is what the app remembers between runs.
type Config struct {
	// GamePath is the Deadlock directory the user picked, empty when the app
	// detects the game through Steam instead.
	GamePath string `json:"gamePath,omitempty"`
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

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, fileName+".*")
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
