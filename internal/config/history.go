package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// HistoryPath returns where the install's learned game-data history lives.
// Each installation gets its own file, keyed by the citadel directory, because
// the history describes the game build under that directory.
func HistoryPath(citadelDir string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache directory: %w", err)
	}
	installation := sha256.Sum256([]byte(citadelDir))
	return filepath.Join(dir, appDir, "compatibility", hex.EncodeToString(installation[:]),
		"vdata-history.bin"), nil
}
