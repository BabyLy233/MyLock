// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/manager/localization/receipt.rs and
// localization_overlay.rs at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../third_party/deadlock-mod-manager/NOTICE.md.

package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	portoverlay "github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/overlay"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// Apply publishes plan to vpkPath and writes its receipt. The package is
// staged, read back and verified before it replaces the old one; if the
// receipt cannot be written, the new package is removed.
func Apply(plan *Plan, vpkPath string) (Result, error) {
	if plan.Key.OutputPath != "" && plan.Key.OutputPath != vpkPath {
		return Result{}, fmt.Errorf("overlay plan output %s does not match %s", plan.Key.OutputPath, vpkPath)
	}
	parent := filepath.Dir(vpkPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{}, err
	}

	result := Result{Repairs: plan.Repairs, Warnings: plan.Warnings}
	var overlayHash *string
	if len(plan.Files) > 0 {
		data, count, err := pack(plan.Files)
		if err != nil {
			return Result{}, err
		}
		if err := stageAndVerify(parent, vpkPath, data, plan.Files); err != nil {
			return Result{}, err
		}
		hash, err := portoverlay.Digest(vpkPath)
		if err != nil {
			_ = os.Remove(vpkPath)
			return Result{}, err
		}
		overlayHash = hash
		result.HasOverlay = true
		result.OutputPath = vpkPath
		result.PackedFiles = count
	} else {
		if err := os.Remove(vpkPath); err != nil && !os.IsNotExist(err) {
			return Result{}, fmt.Errorf("removing stale overlay: %w", err)
		}
	}

	repairs, err := json.Marshal(plan.Repairs)
	if err != nil {
		return Result{}, err
	}
	warnings, err := json.Marshal(plan.Warnings)
	if err != nil {
		return Result{}, err
	}
	files := make([]string, 0, len(plan.Files))
	for path := range plan.Files {
		files = append(files, path)
	}
	sort.Strings(files)
	receipt := portoverlay.Receipt{
		Generator:   Generator,
		Revision:    Revision,
		Key:         plan.KeyJSON(),
		OverlayHash: overlayHash,
		Repairs:     repairs,
		Warnings:    warnings,
		Files:       files,
	}
	if err := portoverlay.Save(vpkPath, receipt); err != nil {
		if result.HasOverlay {
			_ = os.Remove(vpkPath)
		}
		return Result{}, err
	}
	return result, nil
}

func pack(files map[string][]byte) ([]byte, int, error) {
	entries := make([]vpk.PackEntry, 0, len(files))
	for path, data := range files {
		entries = append(entries, vpk.PackEntry{Path: path, Data: data})
	}
	data, err := vpk.Pack(entries)
	if err != nil {
		return nil, 0, fmt.Errorf("packing overlay: %w", err)
	}
	return data, len(entries), nil
}

// stageAndVerify writes data to a temporary file, reads the packed archive
// back and compares every entry before renaming it into place.
func stageAndVerify(parent, vpkPath string, data []byte, files map[string][]byte) error {
	tmp, err := os.CreateTemp(parent, ".mylock-overlay-*")
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

	archive, err := vpk.Open(tmpName)
	if err != nil {
		return fmt.Errorf("verifying overlay: %w", err)
	}
	if len(archive.Entries) != len(files) {
		return fmt.Errorf("verifying overlay: wrote %d entries for %d files", len(archive.Entries), len(files))
	}
	for path, want := range files {
		got, err := archive.Read(path)
		if err != nil {
			return fmt.Errorf("verifying overlay entry %s: %w", path, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("verifying overlay entry %s: bytes changed", path)
		}
	}

	if err := os.Rename(tmpName, vpkPath); err != nil {
		// Windows refuses to replace an existing file. Removing it first keeps
		// the operation fail-closed: a failed rename leaves no overlay.
		if removeErr := os.Remove(vpkPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if err := os.Rename(tmpName, vpkPath); err != nil {
			return err
		}
	}
	return nil
}

// Installed reads the receipt at vpkPath and reports whether the overlay file
// still matches it. The receipt is returned even when the bytes changed, so the
// caller can inspect what it recorded.
func Installed(vpkPath string) (*portoverlay.Receipt, bool, error) {
	receipt, err := portoverlay.Load(vpkPath, Revision, Generator)
	if err != nil || receipt == nil {
		return nil, false, err
	}
	if receipt.OverlayHash == nil {
		return receipt, false, nil
	}
	current, err := portoverlay.Digest(vpkPath)
	if err != nil {
		return receipt, false, err
	}
	return receipt, current != nil && *current == *receipt.OverlayHash, nil
}

// Applied reports whether a valid overlay built from key is installed.
// A receipt without an overlay (nothing to repair) is not "applied".
func Applied(vpkPath string, key Key) (bool, error) {
	raw, err := json.Marshal(key)
	if err != nil {
		return false, err
	}
	receipt, err := portoverlay.Load(vpkPath, Revision, Generator)
	if err != nil || receipt == nil {
		return false, err
	}
	current, err := receipt.IsCurrent(vpkPath, raw)
	if err != nil || !current {
		return false, err
	}
	if receipt.OverlayHash == nil {
		return false, nil
	}
	info, err := os.Stat(vpkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// Current reports whether the receipt at vpkPath matches key, including the
// "nothing to repair" case where no overlay file exists.
func Current(vpkPath string, key Key) (bool, error) {
	raw, err := json.Marshal(key)
	if err != nil {
		return false, err
	}
	receipt, err := portoverlay.Load(vpkPath, Revision, Generator)
	if err != nil || receipt == nil {
		return false, err
	}
	return receipt.IsCurrent(vpkPath, raw)
}

// Remove deletes the overlay package and its receipt.
func Remove(vpkPath string) error {
	return portoverlay.Remove(vpkPath)
}
