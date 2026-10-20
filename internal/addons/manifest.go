// Package addons reads and writes the mods installed in a Deadlock
// installation.
//
// What is installed lives in one manifest per profile, .dmm.json, beside the
// VPK files it describes. That file is shared with the Tauri app this one is a
// port of, so it is read and written byte for byte the way that app does.
package addons

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// ManifestName is the file holding the state of one profile's mods.
	ManifestName = ".dmm.json"
	// tempName is what a save writes before renaming it over the manifest. It
	// is a fixed name rather than a random one because the Tauri app looks for
	// exactly this file to recover a save that a crash interrupted.
	tempName = ManifestName + ".tmp"

	// Version is the manifest version this app writes.
	Version = 3

	// FirstShard is the shard a mod's enabled VPKs are in when the manifest
	// predates sharding, which is what an absent shard key means.
	FirstShard = 1
	// MaxShards is how many shard roots one profile may use.
	MaxShards = 10
	// ShardCapacity is how many enabled VPKs the engine loads from one shard.
	ShardCapacity = 99
)

// Manifest is the state of every mod of one profile.
type Manifest struct {
	// Version is the version of the file, Version for one this app wrote.
	Version uint32
	// Mods is keyed by mod id.
	Mods map[string]Entry
}

// Entry is the state of one mod.
type Entry struct {
	Enabled               bool
	CompatibilityEnabled  bool
	CompatibilityOptedOut bool
	// Order is the mod's place in the load order, nil when the app that
	// installed it assigned none.
	Order *uint32
	// Shard holds this mod's enabled VPKs. All of a mod's VPKs share one
	// shard, so a mod of several files stays together.
	Shard uint32
	// CurrentVpks names the mod's VPKs while it is enabled.
	CurrentVpks []string
	// DisabledVpks names them while it is disabled, each carrying the mod id
	// as a prefix.
	DisabledVpks []string
	// OriginalVpkNames holds the names before that prefix, aligned with
	// whichever of the two lists is in use.
	OriginalVpkNames []string
}

// New returns an empty manifest at the current version.
func New() *Manifest {
	return &Manifest{Version: Version, Mods: map[string]Entry{}}
}

// Load reads the manifest at path. A file that is not there gives an empty
// manifest, which is the state of a profile with nothing installed. A file a
// crash interrupted, left as the temporary a save writes first, is read in its
// place.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if data, err = os.ReadFile(filepath.Join(filepath.Dir(path), tempName)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return New(), nil
			}
			return nil, fmt.Errorf("reading the manifest: %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse reads a manifest from the bytes of a file.
func Parse(data []byte) (*Manifest, error) {
	var w wireManifest
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parsing the manifest: %w", err)
	}

	m := New()
	if w.Version != nil {
		if *w.Version > Version {
			return nil, fmt.Errorf("the manifest is at version %d, and this app knows up to %d", *w.Version, Version)
		}
		// An older file is brought up to the current version here, and written
		// at it the next time the manifest is saved.
		m.Version = Version
	}

	for id, e := range w.Mods {
		shard := uint32(FirstShard)
		if e.Shard != nil {
			shard = *e.Shard
			if shard < FirstShard || shard > MaxShards {
				return nil, fmt.Errorf("mod %q is in shard %d, outside %d..%d", id, shard, FirstShard, MaxShards)
			}
		}
		m.Mods[id] = Entry{
			Enabled:               e.Enabled,
			CompatibilityEnabled:  e.CompatibilityEnabled,
			CompatibilityOptedOut: e.CompatibilityOptedOut,
			Order:                 e.Order,
			Shard:                 shard,
			CurrentVpks:           e.CurrentVpks,
			DisabledVpks:          e.DisabledVpks,
			OriginalVpkNames:      e.OriginalVpkNames,
		}
	}
	return m, nil
}

// Marshal returns the bytes of the manifest, as the Tauri app writes them:
// two space indent, no trailing newline, and every key of every entry present.
func (m *Manifest) Marshal() ([]byte, error) {
	version := m.Version
	if version == 0 {
		version = Version
	}

	w := wireManifest{Version: &version, Mods: make(map[string]wireEntry, len(m.Mods))}
	for id, e := range m.Mods {
		shard := e.Shard
		if shard == 0 {
			shard = FirstShard
		}
		w.Mods[id] = wireEntry{
			Enabled:               e.Enabled,
			CompatibilityEnabled:  e.CompatibilityEnabled,
			CompatibilityOptedOut: e.CompatibilityOptedOut,
			Order:                 e.Order,
			Shard:                 &shard,
			CurrentVpks:           nonNil(e.CurrentVpks),
			DisabledVpks:          nonNil(e.DisabledVpks),
			OriginalVpkNames:      nonNil(e.OriginalVpkNames),
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	// The Tauri app writes these bytes with serde_json, which escapes only what
	// JSON requires. Go would also escape <, > and &, which would change a file
	// the two apps share.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(w); err != nil {
		return nil, fmt.Errorf("writing the manifest: %w", err)
	}
	// Encode appends the newline that the Tauri app does not write.
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Save writes the manifest to path, creating the directory above it. The bytes
// go to the temporary file beside it first and are renamed over it, so a crash
// or a full disk cannot leave a half written manifest for the next run to read.
func (m *Manifest) Save(path string) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp := filepath.Join(dir, tempName)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// wireManifest is the file itself. The field order and the tags are what make
// the bytes match the Tauri app's, so they are not to be rearranged.
type wireManifest struct {
	// Version and Shard are pointers because a key that is absent is not the
	// same as one holding zero: an absent version is the current one, and an
	// absent shard is the first, while a shard of zero is a file neither app
	// writes and this one refuses.
	Version *uint32              `json:"version"`
	Mods    map[string]wireEntry `json:"mods"`
}

// wireEntry is one mod in the file. Every key is written, including the empty
// ones, because the Tauri app writes them all.
type wireEntry struct {
	Enabled               bool     `json:"enabled"`
	CompatibilityEnabled  bool     `json:"compatibilityEnabled"`
	CompatibilityOptedOut bool     `json:"compatibilityOptedOut"`
	Order                 *uint32  `json:"order"`
	Shard                 *uint32  `json:"shard"`
	CurrentVpks           []string `json:"currentVpks"`
	DisabledVpks          []string `json:"disabledVpks"`
	OriginalVpkNames      []string `json:"originalVpkNames"`
}

// nonNil turns a nil slice into an empty one, because the Tauri app writes []
// where Go would write null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
