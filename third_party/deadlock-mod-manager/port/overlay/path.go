// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/manager/localization.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

// Package overlay holds the compatibility overlay naming and receipt rules
// shared by the overlay plan and the app.
package overlay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// VPKName is the directory VPK the game mounts.
	VPKName = "pak01_dir.vpk"
	// ReceiptName records which inputs and repairs produced the overlay.
	ReceiptName = "compatibility.json"
	// DirectoryPrefix names overlay directories under game/citadel.
	DirectoryPrefix = "dmm_localization_"
)

// Suffix turns a profile folder into the overlay directory suffix. The
// default profile is spelled out; any other profile is identified by the
// first eight SHA-256 bytes, exactly as the original does.
func Suffix(profileFolder *string) string {
	if profileFolder == nil {
		return "default"
	}
	sum := sha256.Sum256([]byte(*profileFolder))
	return hex.EncodeToString(sum[:8])
}

// SearchPath is the gameinfo search path for a profile's overlay.
func SearchPath(profileFolder *string) string {
	return "citadel/" + DirectoryPrefix + Suffix(profileFolder)
}

// Directory is the overlay directory under citadelDir.
func Directory(citadelDir string, profileFolder *string) string {
	return filepath.Join(citadelDir, DirectoryPrefix+Suffix(profileFolder))
}

// VPKPath is the overlay package path for a profile.
func VPKPath(citadelDir string, profileFolder *string) string {
	return filepath.Join(Directory(citadelDir, profileFolder), VPKName)
}

// ReceiptPath is the receipt stored beside an overlay package.
func ReceiptPath(vpkPath string) string {
	return filepath.Join(filepath.Dir(vpkPath), ReceiptName)
}

// Digest returns the SHA-256 of a file, or nil when it does not exist.
func Digest(path string) (*string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	return &sum, nil
}

// WriteAtomic replaces path with data through a synced temporary file in the
// same directory, so a crash leaves either the old file or the new one.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
