// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/observed_data.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See
// ../../third_party/deadlock-mod-manager/NOTICE.md.

package modcompat

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compiled"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/history"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vdata"
)

const (
	// observationMaxBytes and observationMaxEntries bound the decoded-table
	// cache shared by one check: the same game table is submitted once per mod
	// that touches it, and the fingerprints must not be rebuilt each time.
	observationMaxBytes   = 64 << 20
	observationMaxEntries = 8
)

type observation struct {
	path     string
	digest   [32]byte
	snapshot *history.Snapshot
	size     int
}

// History is the learned game-data history for one installation: the shipped
// index plus whatever this run observed from the installed game. Recording is
// how a later game update can still recognise an authored table, so it happens
// even when the current build needs no repair.
type History struct {
	mu      sync.Mutex
	learned *history.Index
	version *uint32
	entries []observation
	bytes   int
	changed bool
}

// NewHistory wraps a learned index for one run. A nil index starts empty, and
// a nil version disables recording: without a build number an observation
// cannot be placed in the history.
func NewHistory(learned *history.Index, version *uint32) *History {
	if learned == nil {
		learned = &history.Index{}
	}
	return &History{learned: learned, version: version}
}

// LoadHistory reads the install's learned history at path. A missing or
// unreadable file gives an empty history rather than failing the check.
func LoadHistory(path string, version *uint32) *History {
	if path == "" {
		return NewHistory(nil, version)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("Could not read observed compatibility history: %v", err)
		}
		return NewHistory(nil, version)
	}
	learned, err := history.Parse(data)
	if err != nil {
		log.Printf("Ignoring unreadable observed compatibility history: %v", err)
		return NewHistory(nil, version)
	}
	return NewHistory(learned, version)
}

// Changed reports whether this run observed anything new.
func (h *History) Changed() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changed
}

// Save writes the learned history atomically, creating its parent directory.
// A failure leaves the previous file in place.
func (h *History) Save(path string) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	data, err := h.learned.ToBytes()
	h.mu.Unlock()
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", parent, err)
	}
	tmp, err := os.CreateTemp(parent, ".vdata-history-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// Record learns one game table. It returns false when the history already had
// this content, including when no game build is known.
func (h *History) Record(filePath string, bytes []byte) (bool, error) {
	if h == nil || h.version == nil {
		return false, nil
	}
	digest := sha256.Sum256(bytes)
	h.mu.Lock()
	cached := h.cached(filePath, digest)
	h.mu.Unlock()
	if cached != nil {
		return h.recordSnapshot(filePath, cached)
	}
	// The whole resource is digested, not just the decoded table: a malformed
	// header must miss the cache, and neither build number nor file metadata is
	// enough to identify content.
	_, root, _, err := compiled.Decode(bytes, filePath)
	if err != nil {
		return false, err
	}
	snapshot, err := history.NewSnapshot(filePath, root)
	if err != nil {
		return false, err
	}
	h.mu.Lock()
	h.insert(filePath, digest, snapshot)
	h.mu.Unlock()
	return h.recordSnapshot(filePath, snapshot)
}

func (h *History) recordSnapshot(filePath string, snapshot *history.Snapshot) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed, err := h.learned.RecordSnapshot(filePath, *h.version, snapshot)
	h.changed = h.changed || changed
	return changed, err
}

// cached returns the snapshot for exactly these bytes and marks it recently
// used, as the observation cache does.
func (h *History) cached(path string, digest [32]byte) *history.Snapshot {
	for index, entry := range h.entries {
		if entry.path != path || entry.digest != digest {
			continue
		}
		h.entries = append(append(h.entries[:index], h.entries[index+1:]...), entry)
		return entry.snapshot
	}
	return nil
}

func (h *History) insert(path string, digest [32]byte, snapshot *history.Snapshot) {
	size := snapshot.CacheSize() + len(path) + 192
	if size > observationMaxBytes || h.cached(path, digest) != nil {
		return
	}
	for h.bytes+size > observationMaxBytes || len(h.entries) >= observationMaxEntries {
		oldest := h.entries[0]
		h.entries = h.entries[1:]
		h.bytes -= oldest.size
	}
	h.bytes += size
	h.entries = append(h.entries, observation{
		path: path, digest: digest, snapshot: snapshot, size: size,
	})
}

// matcher adapts the learned history to the data merger. The shipped history
// is consulted second, so a learned build wins when it is closer.
func (h *History) matcher() vdata.HistoryMatcher {
	var learned *history.Index
	if h != nil {
		learned = h.learned
	}
	view := history.Embedded().WithObserved(learned)
	return func(filePath string, root kv3.Value) (vdata.BaselineMatch, vdata.BaselineEvidence) {
		match, evidence := view.MatchWithEvidence(filePath, root)
		out := vdata.BaselineEvidence{
			Status:         string(evidence.Status),
			BuildVersions:  evidence.BuildVersions,
			ComparableRows: evidence.ComparableRows,
			ChangedRows:    evidence.ChangedRows,
		}
		if match == nil {
			return nil, out
		}
		return match, out
	}
}
