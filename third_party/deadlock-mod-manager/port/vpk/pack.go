// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/pack.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package vpk

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PackEntry is one file to pack.
type PackEntry struct {
	// Path is the VPK-internal relative path, e.g. "models/hero/x.vmdl_c".
	Path string
	Data []byte
}

type packedEntry struct {
	filename string
	crc      uint32
	offset   uint32
	length   uint32
}

type treeKey struct {
	ext string
	dir string
}

// Pack builds a self-contained VPK v2 whose entries are all stored inline in
// the directory file. Paths are lowercased and sorted exactly as the original
// pack_directory does.
func Pack(entries []PackEntry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, invalid("workspace has no files to pack")
	}
	sorted := append([]PackEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	tree := make(map[treeKey]map[string]packedEntry)
	data := make([]byte, 0)
	seen := make(map[string]struct{}, len(sorted))

	for _, input := range sorted {
		rel := strings.ReplaceAll(input.Path, "\\", "/")
		if _, dup := seen[strings.ToLower(rel)]; dup {
			return nil, invalid("duplicate packed path: %s", input.Path)
		}
		seen[strings.ToLower(rel)] = struct{}{}

		ext, dir, filename, ok := SplitEntryPath(rel)
		if !ok {
			return nil, invalid("packed file has no usable extension and filename: %s", input.Path)
		}
		if uint64(len(data)) > 0xFFFFFFFF || uint64(len(input.Data)) > 0xFFFFFFFF {
			return nil, invalid("VPK data section is too large")
		}
		offset := uint32(len(data))
		length := uint32(len(input.Data))
		crc := crc32.ChecksumIEEE(input.Data)
		data = append(data, input.Data...)

		key := treeKey{ext: ext, dir: dir}
		if tree[key] == nil {
			tree[key] = make(map[string]packedEntry)
		}
		tree[key][filename] = packedEntry{filename: filename, crc: crc, offset: offset, length: length}
	}

	directoryTree := buildDirectoryTree(tree)
	if uint64(len(directoryTree)) > 0xFFFFFFFF {
		return nil, invalid("VPK tree is too large")
	}
	out := make([]byte, 0, headerLenV2+len(directoryTree)+len(data))
	out = binary.LittleEndian.AppendUint32(out, signature)
	out = binary.LittleEndian.AppendUint32(out, 2)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(directoryTree)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	out = binary.LittleEndian.AppendUint32(out, 0)
	out = binary.LittleEndian.AppendUint32(out, 0)
	out = binary.LittleEndian.AppendUint32(out, 0)
	out = append(out, directoryTree...)
	out = append(out, data...)
	return out, nil
}

func buildDirectoryTree(tree map[treeKey]map[string]packedEntry) []byte {
	keys := make([]treeKey, 0, len(tree))
	for key := range tree {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ext != keys[j].ext {
			return keys[i].ext < keys[j].ext
		}
		return keys[i].dir < keys[j].dir
	})

	var out []byte
	for i := 0; i < len(keys); {
		ext := keys[i].ext
		out = writeCString(out, ext)
		for i < len(keys) && keys[i].ext == ext {
			dir := keys[i].dir
			out = writeCString(out, dir)
			entries := tree[keys[i]]
			names := make([]string, 0, len(entries))
			for name := range entries {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				e := entries[name]
				out = writeCString(out, e.filename)
				out = binary.LittleEndian.AppendUint32(out, e.crc)
				out = binary.LittleEndian.AppendUint16(out, 0) // preload bytes
				out = binary.LittleEndian.AppendUint16(out, InlineArchiveIndex)
				out = binary.LittleEndian.AppendUint32(out, e.offset)
				out = binary.LittleEndian.AppendUint32(out, e.length)
				out = binary.LittleEndian.AppendUint16(out, entryTerminator)
			}
			out = append(out, 0) // end of this directory's filenames
			i++
		}
		out = append(out, 0) // end of this extension's directories
	}
	out = append(out, 0) // end of the extension list
	return out
}

func writeCString(out []byte, value string) []byte {
	out = append(out, value...)
	return append(out, 0)
}

// PackDirectory reads every regular file under root and packs it.
func PackDirectory(root string) ([]byte, int, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, 0, err
	}
	if !info.IsDir() {
		return nil, 0, fmt.Errorf("%w: source directory does not exist: %s", ErrInvalid, root)
	}
	var entries []PackEntry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, PackEntry{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	out, err := Pack(entries)
	if err != nil {
		return nil, 0, err
	}
	return out, len(entries), nil
}
