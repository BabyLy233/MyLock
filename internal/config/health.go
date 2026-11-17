package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// HealthPath returns the file the compatibility health cache lives in, next to
// the settings file.
func HealthPath() (string, error) {
	settings, err := Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(settings)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return filepath.Join(dir, "mod-health.json"), nil
}
