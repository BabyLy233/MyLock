// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/manager/localization/receipt.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Receipt is stored beside an overlay package. The key is kept as raw JSON so
// the plan type stays in internal/overlay; repairs and warnings are opaque to
// this layer.
type Receipt struct {
	Generator   string          `json:"generator"`
	Revision    uint32          `json:"revision"`
	Key         json.RawMessage `json:"key"`
	OverlayHash *string         `json:"overlayHash"`
	Repairs     json.RawMessage `json:"repairs,omitempty"`
	Warnings    json.RawMessage `json:"warnings,omitempty"`
	Files       []string        `json:"files,omitempty"`
}

// Load reads the receipt beside vpkPath. A missing receipt, another
// generator's receipt, or a receipt from a different revision returns nil so
// the caller rebuilds the overlay.
func Load(vpkPath string, revision uint32, generator string) (*Receipt, error) {
	data, err := os.ReadFile(ReceiptPath(vpkPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading compatibility receipt: %w", err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, nil
	}
	if receipt.Revision != revision || receipt.Generator != generator {
		return nil, nil
	}
	return &receipt, nil
}

func compactJSON(raw []byte) ([]byte, bool) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// MatchesKey reports whether the receipt was produced from key.
func (r *Receipt) MatchesKey(key []byte) bool {
	stored, ok := compactJSON(r.Key)
	if !ok {
		return false
	}
	want, ok := compactJSON(key)
	if !ok {
		return false
	}
	return bytes.Equal(stored, want)
}

// IsCurrent reports whether the receipt matches key and the overlay file on
// disk still has the recorded digest.
func (r *Receipt) IsCurrent(vpkPath string, key []byte) (bool, error) {
	if !r.MatchesKey(key) {
		return false, nil
	}
	current, err := Digest(vpkPath)
	if err != nil {
		return false, err
	}
	if r.OverlayHash == nil || current == nil {
		return r.OverlayHash == nil && current == nil, nil
	}
	return *r.OverlayHash == *current, nil
}

// Save writes the receipt atomically beside vpkPath.
func Save(vpkPath string, receipt Receipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("recording compatibility receipt: %w", err)
	}
	data = append(data, '\n')
	return WriteAtomic(ReceiptPath(vpkPath), data)
}

// Remove deletes the overlay package and its receipt. Missing files are fine.
func Remove(vpkPath string) error {
	for _, path := range []string{vpkPath, ReceiptPath(vpkPath)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing compatibility file %s: %w", path, err)
		}
	}
	return nil
}
