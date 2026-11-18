// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs
// (HistoricalLocalizationIndex) at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../NOTICE.md.

package localization

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"hash"
	"sync"
)

//go:embed localization-history.bin
var embeddedHistory []byte

// HistoryIndex is the set of token bindings seen in previous game builds. It
// tells a stale copy of a protected file from an authored edit.
type HistoryIndex struct {
	fingerprints map[[32]byte]struct{}
}

// EmbeddedHistory returns the shipped index. A malformed asset yields an empty
// index rather than failing a compatibility check.
func EmbeddedHistory() *HistoryIndex {
	return embeddedHistoryIndex()
}

var embeddedHistoryIndex = sync.OnceValue(func() *HistoryIndex {
	index, err := ParseHistory(embeddedHistory)
	if err != nil {
		return &HistoryIndex{fingerprints: map[[32]byte]struct{}{}}
	}
	return index
})

// ParseHistory decodes the observed localization fingerprint index.
func ParseHistory(bytes []byte) (*HistoryIndex, error) {
	if len(bytes) < 12 || string(bytes[:8]) != "DMMLOC01" {
		return nil, fmt.Errorf("Localization history index has an invalid magic header")
	}
	count := uint64(binary.LittleEndian.Uint32(bytes[8:12]))
	payload := bytes[12:]
	if uint64(len(payload)) != count*32 {
		return nil, fmt.Errorf("Localization history index has an invalid fingerprint count")
	}
	index := &HistoryIndex{fingerprints: make(map[[32]byte]struct{}, count)}
	for offset := 0; offset < len(payload); offset += 32 {
		var fingerprint [32]byte
		copy(fingerprint[:], payload[offset:offset+32])
		index.fingerprints[fingerprint] = struct{}{}
	}
	return index, nil
}

// Contains reports whether the file/token/value binding was observed in a
// recorded build.
func (h *HistoryIndex) Contains(filePath, token, value string) bool {
	if h == nil {
		return false
	}
	_, ok := h.fingerprints[Fingerprint(filePath, token, value)]
	return ok
}

// Fingerprint hashes one file/token/value binding the way the index was built.
func Fingerprint(filePath, token, value string) [32]byte {
	hasher := sha256.New()
	for _, part := range []string{NormalizePath(filePath), asciiLower(token), value} {
		hashPart(hasher, []byte(part))
	}
	var out [32]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func hashPart(hasher hash.Hash, part []byte) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(part)))
	hasher.Write(length[:])
	hasher.Write(part)
}

// NormalizePath lowercases and unifies separators for resource names.
func NormalizePath(filePath string) string {
	return asciiLower(replaceBackslashes(filePath))
}

func replaceBackslashes(value string) string {
	out := []byte(value)
	for index, char := range out {
		if char == '\\' {
			out[index] = '/'
		}
	}
	return string(out)
}

func asciiLower(value string) string {
	out := []byte(value)
	for index, char := range out {
		if char >= 'A' && char <= 'Z' {
			out[index] = char + ('a' - 'A')
		}
	}
	return string(out)
}
