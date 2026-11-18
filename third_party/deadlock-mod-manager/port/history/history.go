// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/vdata_history.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package history reads the learned VData history index shipped with the app.
// It answers which authored field values were seen in previous game builds and
// matches an authored table to the closest recorded build.
package history

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
	"sync"
)

//go:embed vdata-history.bin
var embedded []byte

var (
	magic       = [8]byte{'D', 'M', 'M', 'V', 'D', 'H', '0', '3'}
	exactMagic  = [8]byte{'D', 'M', 'M', 'V', 'D', 'H', '0', '2'}
	legacyMagic = [8]byte{'D', 'M', 'M', 'V', 'D', 'H', '0', '1'}
)

type fingerprint = [32]byte

// Index is an immutable view of the recorded VData history. Observed string
// bindings answer relocation questions; the per-table build deltas let the
// data-compatibility path identify the game build an authored table came from.
type Index struct {
	observed map[fingerprint]map[fingerprint]struct{}
	tables   map[string]*tableHistory
}

// Embedded returns the shipped history. A malformed asset yields an empty index
// rather than failing a compatibility check.
func Embedded() *Index {
	return embeddedIndex()
}

var embeddedIndex = sync.OnceValue(func() *Index {
	index, err := Parse(embedded)
	if err != nil {
		return &Index{}
	}
	return index
})

// ObservedStringField reports whether file_path row's field was seen with this
// exact string value in any recorded build.
func (i *Index) ObservedStringField(filePath, row, field, value string) bool {
	if i == nil || i.observed == nil {
		return false
	}
	key := stringBindingKey(filePath, fieldKey(row, field))
	valueFingerprint := stringValueFingerprint(value)
	for known := range i.observed[key] {
		if known == valueFingerprint {
			return true
		}
	}
	return false
}

// Parse decodes a history index. All three on-disk versions are accepted; the
// oldest one stores field fingerprints without a separate string ledger.
func Parse(data []byte) (*Index, error) {
	reader := reader{remaining: data}
	header, err := reader.take(8)
	if err != nil {
		return nil, err
	}
	var magicValue [8]byte
	copy(magicValue[:], header)
	if magicValue != magic && magicValue != exactMagic && magicValue != legacyMagic {
		return nil, fmt.Errorf("VData history index has an invalid magic header")
	}
	exact := magicValue != legacyMagic
	tableCount, err := reader.u32()
	if err != nil {
		return nil, err
	}
	if err := reader.checkCount(tableCount, 8); err != nil {
		return nil, err
	}
	index := &Index{
		observed: make(map[fingerprint]map[fingerprint]struct{}),
		tables:   make(map[string]*tableHistory),
	}
	for range tableCount {
		path, err := reader.string()
		if err != nil {
			return nil, err
		}
		buildCount, err := reader.u32()
		if err != nil {
			return nil, err
		}
		if uint64(buildCount) > uint64(reader.len()/12) {
			return nil, fmt.Errorf("VData history build count exceeds its payload")
		}
		builds := make([]BuildDelta, 0, buildCount)
		for range buildCount {
			version, err := reader.u32()
			if err != nil {
				return nil, err
			}
			rows, err := reader.changes()
			if err != nil {
				return nil, err
			}
			var exactRows []FingerprintChange
			if exact {
				exactRows, err = reader.changes()
				if err != nil {
					return nil, err
				}
			}
			fields, err := reader.changes()
			if err != nil {
				return nil, err
			}
			if magicValue != magic {
				// The oldest ledgers only retained field fingerprints. Every
				// retained value is a candidate string binding.
				for _, field := range fields {
					if field.Value == nil {
						continue
					}
					index.add(stringBindingKey(path, field.Key), *field.Value)
				}
			}
			builds = append(builds, BuildDelta{
				Version:   version,
				Rows:      rows,
				ExactRows: exactRows,
				Fields:    fields,
			})
		}
		index.tables[normalizePath(path)] = &tableHistory{exact: exact, builds: builds}
	}
	if magicValue == magic {
		count, err := reader.u32()
		if err != nil {
			return nil, err
		}
		if err := reader.checkCount(count, 36); err != nil {
			return nil, err
		}
		for range count {
			key, err := reader.fingerprint()
			if err != nil {
				return nil, err
			}
			valueCount, err := reader.u32()
			if err != nil {
				return nil, err
			}
			if err := reader.checkCount(valueCount, 32); err != nil {
				return nil, err
			}
			for range valueCount {
				value, err := reader.fingerprint()
				if err != nil {
					return nil, err
				}
				index.add(key, value)
			}
		}
	}
	if !reader.empty() {
		return nil, fmt.Errorf("VData history index has trailing bytes")
	}
	return index, nil
}

func (i *Index) add(key, value fingerprint) bool {
	if i.observed == nil {
		i.observed = make(map[fingerprint]map[fingerprint]struct{})
	}
	values := i.observed[key]
	if values == nil {
		values = make(map[fingerprint]struct{})
		i.observed[key] = values
	}
	if _, duplicate := values[value]; duplicate {
		return false
	}
	values[value] = struct{}{}
	return true
}

type reader struct {
	remaining []byte
}

func (r *reader) len() int { return len(r.remaining) }

func (r *reader) take(count int) ([]byte, error) {
	if count < 0 || len(r.remaining) < count {
		return nil, fmt.Errorf("VData history index is truncated")
	}
	value := r.remaining[:count]
	r.remaining = r.remaining[count:]
	return value, nil
}

func (r *reader) u32() (uint32, error) {
	value, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(value), nil
}

func (r *reader) fingerprint() (fingerprint, error) {
	var out fingerprint
	value, err := r.take(32)
	if err != nil {
		return out, err
	}
	copy(out[:], value)
	return out, nil
}

func (r *reader) string() (string, error) {
	length, err := r.u32()
	if err != nil {
		return "", err
	}
	value, err := r.take(int(length))
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (r *reader) changes() ([]FingerprintChange, error) {
	count, err := r.u32()
	if err != nil {
		return nil, err
	}
	if uint64(count) > uint64(r.len()/33) {
		return nil, fmt.Errorf("VData history change count exceeds its payload")
	}
	changes := make([]FingerprintChange, 0, count)
	for range count {
		key, err := r.fingerprint()
		if err != nil {
			return nil, err
		}
		tag, err := r.take(1)
		if err != nil {
			return nil, err
		}
		item := FingerprintChange{Key: key}
		switch tag[0] {
		case 0:
		case 1:
			value, err := r.fingerprint()
			if err != nil {
				return nil, err
			}
			item.Value = &value
		default:
			return nil, fmt.Errorf("VData history index contains an invalid change tag")
		}
		changes = append(changes, item)
	}
	return changes, nil
}

func (r *reader) empty() bool { return len(r.remaining) == 0 }

func (r *reader) checkCount(count uint32, itemSize int) error {
	if uint64(count)*uint64(itemSize) > uint64(r.len()) {
		return fmt.Errorf("VData history count exceeds its payload")
	}
	return nil
}

func normalizePath(path string) string {
	return asciiLower(strings.ReplaceAll(path, "\\", "/"))
}

func hashPart(hasher hash.Hash, part []byte) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(part)))
	hasher.Write(length[:])
	hasher.Write(part)
}

func hashParts(parts ...[]byte) fingerprint {
	hasher := sha256.New()
	for _, part := range parts {
		hashPart(hasher, part)
	}
	var out fingerprint
	copy(out[:], hasher.Sum(nil))
	return out
}

func stringBindingKey(filePath string, field fingerprint) fingerprint {
	return hashParts([]byte(normalizePath(filePath)), field[:])
}

func fieldKey(rowName, field string) fingerprint {
	hasher := sha256.New()
	hashPart(hasher, []byte("field"))
	hashPart(hasher, []byte(strings.ToLower(rowName)))
	hashPart(hasher, []byte("key"))
	hashPart(hasher, []byte(strings.ToLower(field)))
	var out fingerprint
	copy(out[:], hasher.Sum(nil))
	return out
}

func stringValueFingerprint(value string) fingerprint {
	return hashParts([]byte("string"), []byte(value))
}
