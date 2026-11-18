// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/material_repair.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package resource

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"unicode/utf8"
)

// Reference is one RERL entry: a resource id and its uncompiled path.
type Reference struct {
	ID   uint64
	Path string
}

// References reads the RERL block of a compiled resource.
func (r *Resource) References() ([]Reference, error) {
	data, ok := r.FindBlock(BlockTypeRERL)
	if !ok {
		return nil, fmt.Errorf("%w: missing RERL", ErrInvalid)
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: truncated RERL", ErrInvalid)
	}
	start := uint64(binary.LittleEndian.Uint32(data[0:4]))
	count := uint64(binary.LittleEndian.Uint32(data[4:8]))
	end := start + count*16
	if count > (^uint64(0))/16 || end < start || start < 8 || end > uint64(len(data)) {
		return nil, fmt.Errorf("%w: RERL table out of bounds", ErrInvalid)
	}
	references := make([]Reference, 0, count)
	for index := uint64(0); index < count; index++ {
		offset := start + index*16
		id := binary.LittleEndian.Uint64(data[offset : offset+8])
		relative := int64(binary.LittleEndian.Uint64(data[offset+8 : offset+16]))
		absolute := int64(offset+8) + relative
		if absolute < 0 || absolute >= int64(len(data)) {
			return nil, fmt.Errorf("%w: RERL name out of bounds", ErrInvalid)
		}
		tail := data[absolute:]
		length := 0
		for length < len(tail) && tail[length] != 0 {
			length++
		}
		if length == len(tail) {
			return nil, fmt.Errorf("%w: unterminated RERL name", ErrInvalid)
		}
		name := tail[:length]
		if !utf8.Valid(name) {
			return nil, fmt.Errorf("%w: RERL name is not UTF-8", ErrInvalid)
		}
		references = append(references, Reference{ID: id, Path: string(name)})
	}
	return references, nil
}

// AddReferences merges entries into the RERL block, rejecting a path that
// already has a different id and an id that already belongs to another path.
func (r *Resource) AddReferences(additions []Reference) ([]byte, error) {
	entries, err := r.References()
	if err != nil {
		return nil, err
	}
	for _, addition := range additions {
		found := false
		for _, existing := range entries {
			if existing.Path == addition.Path {
				if existing.ID != addition.ID {
					return nil, fmt.Errorf("%w: conflicting resource ID", ErrInvalid)
				}
				found = true
				break
			}
		}
		if found {
			continue
		}
		if slices.ContainsFunc(entries, func(existing Reference) bool {
			return existing.ID == addition.ID
		}) {
			return nil, fmt.Errorf("%w: resource ID collision", ErrInvalid)
		}
		entries = append(entries, addition)
	}

	if uint64(len(entries)) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: too many RERL references", ErrInvalid)
	}

	data := make([]byte, 8+len(entries)*16)
	binary.LittleEndian.PutUint32(data[0:4], 8)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(entries)))
	for index, entry := range entries {
		offset := 8 + index*16
		relative := int64(len(data) - (offset + 8))
		binary.LittleEndian.PutUint64(data[offset:offset+8], entry.ID)
		binary.LittleEndian.PutUint64(data[offset+8:offset+16], uint64(relative))
		data = append(data, entry.Path...)
		data = append(data, 0)
	}

	index := -1
	for i, block := range r.Blocks() {
		if block.Kind == BlockTypeRERL {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("%w: missing RERL block", ErrInvalid)
	}
	output, err := r.RebuildWithBlockPreserving(index, data)
	if err != nil {
		return nil, err
	}
	rebuilt, err := Parse(output)
	if err != nil {
		return nil, err
	}
	actual, err := rebuilt.References()
	if err != nil {
		return nil, err
	}
	if !sameReferences(actual, entries) {
		return nil, fmt.Errorf("%w: RERL round trip failed", ErrInvalid)
	}
	return output, nil
}

// ReferencesOf is the package-level form used by callers that already have
// bytes.
func ReferencesOf(data []byte) ([]Reference, error) {
	parsed, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return parsed.References()
}

// AddReferencesOf replaces the RERL block of data.
func AddReferencesOf(data []byte, additions []Reference) ([]byte, error) {
	parsed, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return parsed.AddReferences(additions)
}

func sameReferences(left, right []Reference) bool {
	return slices.Equal(left, right)
}
