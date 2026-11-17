// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpk-parser/src-rs/{parser,types,error}.rs and
// packages/source2-model/src/vpk_extract.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa.
//
// The upstream vpk-parser and vpk_extract files are GPL-3.0; the kv3-derived
// parts of this tree retain their MIT notices. See ../../NOTICE.md.

// Package vpk reads VPK v1/v2 directories and entry payloads (inline data and
// external _NNN.vpk companions), and writes self-contained VPK v2 archives.
package vpk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	signature uint32 = 0x55AA1234
	// InlineArchiveIndex marks an entry stored in the directory file itself.
	InlineArchiveIndex uint16 = 0x7FFF
	entryTerminator    uint16 = 0xFFFF
	headerLenV1               = 12
	headerLenV2               = 28
	entryMetaLen              = 18
	maxEntries                = 1 << 20
)

var (
	// ErrInvalid reports a malformed VPK directory.
	ErrInvalid = errors.New("vpk: invalid")
	// ErrSignature reports a wrong VPK signature.
	ErrSignature = errors.New("vpk: bad signature")
	// ErrVersion reports an unsupported VPK version.
	ErrVersion = errors.New("vpk: unsupported version")
	// ErrEntryNotFound reports a path missing from the archive.
	ErrEntryNotFound = errors.New("vpk: entry not found")
	// ErrTooLarge reports an entry over the caller's byte limit.
	ErrTooLarge = errors.New("vpk: entry too large")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Header is the VPK file header.
type Header struct {
	Version              uint32
	TreeLength           uint32
	FileDataSectionSize  uint32
	ArchiveMD5Section    uint32
	OtherMD5Section      uint32
	SignatureSectionSize uint32
	HasV2Sections        bool
	HeaderLength         int
}

// Entry is one file in the VPK directory tree.
type Entry struct {
	FullPath     string
	Path         string
	Filename     string
	Ext          string
	CRC32        uint32
	PreloadBytes uint16
	ArchiveIndex uint16
	EntryOffset  uint32
	EntryLength  uint32
	Terminator   uint16
	Preload      []byte
}

// ParseHeader parses the header at the start of data.
func ParseHeader(data []byte) (Header, error) {
	if len(data) < headerLenV1 {
		return Header{}, invalid("header truncated")
	}
	if magic := binary.LittleEndian.Uint32(data[0:4]); magic != signature {
		return Header{}, fmt.Errorf("%w: %#x", ErrSignature, magic)
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 1 && version != 2 {
		return Header{}, fmt.Errorf("%w: %d", ErrVersion, version)
	}
	h := Header{Version: version, TreeLength: binary.LittleEndian.Uint32(data[8:12]), HeaderLength: headerLenV1}
	if version == 2 {
		if len(data) < headerLenV2 {
			return Header{}, invalid("v2 header truncated")
		}
		h.HasV2Sections = true
		h.HeaderLength = headerLenV2
		h.FileDataSectionSize = binary.LittleEndian.Uint32(data[12:16])
		h.ArchiveMD5Section = binary.LittleEndian.Uint32(data[16:20])
		h.OtherMD5Section = binary.LittleEndian.Uint32(data[20:24])
		h.SignatureSectionSize = binary.LittleEndian.Uint32(data[24:28])
	}
	return h, nil
}

// ParseDirectory parses a VPK header and directory tree from the given bytes.
// data may be the whole directory file or just its header + tree.
func ParseDirectory(data []byte) (Header, []Entry, error) {
	h, err := ParseHeader(data)
	if err != nil {
		return Header{}, nil, err
	}
	treeStart := h.HeaderLength
	treeEnd, ok := addInt(treeStart, int(h.TreeLength))
	if !ok || treeEnd > len(data) {
		return Header{}, nil, invalid("directory exceeds its buffer")
	}
	entries, err := parseTree(data, treeStart, treeEnd)
	if err != nil {
		return Header{}, nil, err
	}
	return h, entries, nil
}

func parseTree(data []byte, treeStart, treeEnd int) ([]Entry, error) {
	cursor := treeStart
	readString := func() (string, error) {
		if cursor >= treeEnd {
			return "", invalid("directory string runs past the tree")
		}
		start := cursor
		for cursor < treeEnd && data[cursor] != 0 {
			cursor++
		}
		if cursor >= treeEnd {
			return "", invalid("unterminated directory string")
		}
		raw := data[start:cursor]
		cursor++ // NUL
		if utf8.Valid(raw) {
			return string(raw), nil
		}
		// Rust's fallback maps each byte to the same Unicode code point.
		var b strings.Builder
		b.Grow(len(raw))
		for _, c := range raw {
			b.WriteRune(rune(c))
		}
		return b.String(), nil
	}

	var entries []Entry
	if capHint := (treeEnd - treeStart) / 32; capHint > 0 && capHint < maxEntries {
		entries = make([]Entry, 0, capHint)
	}
	closed := false
	for cursor < treeEnd {
		ext, err := readString()
		if err != nil {
			return nil, err
		}
		if ext == "" {
			closed = true
			break
		}
		for cursor < treeEnd {
			dirPath, err := readString()
			if err != nil {
				return nil, err
			}
			if dirPath == "" {
				break
			}
			for cursor < treeEnd {
				filename, err := readString()
				if err != nil {
					return nil, err
				}
				if filename == "" {
					break
				}
				if treeEnd-cursor < entryMetaLen {
					return nil, invalid("entry metadata truncated")
				}
				if len(entries) >= maxEntries {
					return nil, invalid("too many entries")
				}
				crc := binary.LittleEndian.Uint32(data[cursor:])
				preloadBytes := binary.LittleEndian.Uint16(data[cursor+4:])
				archiveIndex := binary.LittleEndian.Uint16(data[cursor+6:])
				entryOffset := binary.LittleEndian.Uint32(data[cursor+8:])
				entryLength := binary.LittleEndian.Uint32(data[cursor+12:])
				terminator := binary.LittleEndian.Uint16(data[cursor+16:])
				cursor += entryMetaLen
				preloadEnd, ok := addInt(cursor, int(preloadBytes))
				if !ok || preloadEnd > treeEnd {
					return nil, invalid("preload bytes exceed the directory tree")
				}
				preload := append([]byte(nil), data[cursor:preloadEnd]...)
				cursor = preloadEnd

				normalizedPath := dirPath
				if normalizedPath == " " {
					normalizedPath = ""
				}
				filenameWithExt := filename + "." + ext
				parts := make([]string, 0, 2)
				if normalizedPath != "" {
					parts = append(parts, normalizedPath)
				}
				if filenameWithExt != "" {
					parts = append(parts, filenameWithExt)
				}
				entries = append(entries, Entry{
					FullPath:     strings.Join(parts, "/"),
					Path:         normalizedPath,
					Filename:     filename,
					Ext:          ext,
					CRC32:        crc,
					PreloadBytes: preloadBytes,
					ArchiveIndex: archiveIndex,
					EntryOffset:  entryOffset,
					EntryLength:  entryLength,
					Terminator:   terminator,
					Preload:      preload,
				})
			}
		}
	}
	if !closed {
		return nil, invalid("directory is missing its final terminator")
	}
	return entries, nil
}

// Archive is an opened VPK directory file.
type Archive struct {
	Path             string
	CompanionDir     string
	Header           Header
	Entries          []Entry
	DataSectionStart int
	index            map[string]int
}

// Open opens a VPK directory file, reading only its header and tree.
func Open(vpkPath string) (*Archive, error) {
	f, err := os.Open(vpkPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, headerLenV2)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	head = head[:n]
	h, err := ParseHeader(head)
	if err != nil {
		return nil, err
	}
	treeEnd := int64(h.HeaderLength) + int64(h.TreeLength)
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if treeEnd > info.Size() {
		return nil, invalid("directory exceeds the file")
	}
	if int64(len(head)) < treeEnd {
		buf := make([]byte, treeEnd)
		copy(buf, head)
		if _, err := io.ReadFull(f, buf[len(head):]); err != nil {
			return nil, err
		}
		head = buf
	}
	_, entries, err := ParseDirectory(head)
	if err != nil {
		return nil, err
	}
	archive := &Archive{
		Path:             vpkPath,
		CompanionDir:     filepath.Dir(vpkPath),
		Header:           h,
		Entries:          entries,
		DataSectionStart: h.HeaderLength + int(h.TreeLength),
		index:            make(map[string]int, len(entries)),
	}
	for i := range entries {
		key := normalizeKey(entries[i].FullPath)
		if _, exists := archive.index[key]; !exists {
			archive.index[key] = i
		}
	}
	return archive, nil
}

func normalizeKey(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
}

// List returns every entry path in directory order.
func (a *Archive) List() []string {
	out := make([]string, len(a.Entries))
	for i := range a.Entries {
		out[i] = a.Entries[i].FullPath
	}
	return out
}

// Contains reports whether the archive has the path (case-insensitive).
func (a *Archive) Contains(p string) bool {
	_, ok := a.index[normalizeKey(p)]
	return ok
}

// Entry returns the directory entry for a path.
func (a *Archive) Entry(p string) (Entry, bool) {
	i, ok := a.index[normalizeKey(p)]
	if !ok {
		return Entry{}, false
	}
	return a.Entries[i], true
}

// Read returns the full bytes of an entry: preload prefix plus inline or
// companion-archive payload.
func (a *Archive) Read(p string) ([]byte, error) {
	return a.ReadLimited(p, 0)
}

// ReadLimited is Read with a maximum total size. A non-positive limit means no
// limit.
func (a *Archive) ReadLimited(p string, maxBytes int) ([]byte, error) {
	e, ok := a.Entry(p)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrEntryNotFound, p)
	}
	total := int64(len(e.Preload)) + int64(e.EntryLength)
	if maxBytes > 0 && total > int64(maxBytes) {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, p, total)
	}
	out := append([]byte(nil), e.Preload...)
	if e.EntryLength == 0 {
		return out, nil
	}

	if e.ArchiveIndex == InlineArchiveIndex {
		if a.Header.HasV2Sections && uint64(e.EntryOffset)+uint64(e.EntryLength) > uint64(a.Header.FileDataSectionSize) {
			return nil, invalid("inline entry exceeds the VPK data section")
		}
		start := int64(a.DataSectionStart) + int64(e.EntryOffset)
		return readAt(a.Path, start, int(e.EntryLength), out)
	}

	stem := strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))
	base := strings.TrimSuffix(stem, "_dir")
	name := fmt.Sprintf("%s_%03d.vpk", base, e.ArchiveIndex)
	companion := filepath.Join(a.CompanionDir, name)
	return readAt(companion, int64(e.EntryOffset), int(e.EntryLength), out)
}

func readAt(path string, offset int64, length int, prefix []byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 0 || offset+int64(length) > info.Size() {
		return nil, invalid("entry exceeds its archive")
	}
	out := make([]byte, len(prefix)+length)
	copy(out, prefix)
	if length == 0 {
		return out, nil
	}
	if _, err := f.ReadAt(out[len(prefix):], offset); err != nil {
		return nil, err
	}
	return out, nil
}

// NormalizeResourcePath normalizes separators and rejects empty, dot, parent,
// drive, stream, control-character and Windows device-name segments.
func NormalizeResourcePath(p string) (string, bool) {
	normalized := strings.ReplaceAll(p, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		stem := segment
		if dot := strings.IndexByte(stem, '.'); dot >= 0 {
			stem = stem[:dot]
		}
		stem = strings.TrimRight(stem, " ")
		device := strings.EqualFold(stem, "con") ||
			strings.EqualFold(stem, "prn") ||
			strings.EqualFold(stem, "aux") ||
			strings.EqualFold(stem, "nul")
		if len(stem) >= 4 {
			prefix := stem[:3]
			digit := stem[3:]
			if (strings.EqualFold(prefix, "com") || strings.EqualFold(prefix, "lpt")) &&
				(digit == "1" || digit == "2" || digit == "3" || digit == "4" || digit == "5" ||
					digit == "6" || digit == "7" || digit == "8" || digit == "9" ||
					digit == "¹" || digit == "²" || digit == "³") {
				device = true
			}
		}
		if segment == "" || segment == "." || segment == ".." ||
			strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") ||
			strings.Contains(segment, ":") ||
			hasControl(segment) || device {
			return "", false
		}
	}
	return normalized, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// SplitEntryPath splits a relative packed path into (extension, directory,
// filename), lowercased and using " " for the root directory.
func SplitEntryPath(rel string) (ext, dir, filename string, ok bool) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	parts := strings.Split(rel, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", "", "", false
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return "", "", "", false
	}
	base := clean[len(clean)-1]
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 || dot == len(base)-1 {
		return "", "", "", false
	}
	ext = strings.ToLower(base[dot+1:])
	filename = strings.ToLower(base[:dot])
	if len(clean) == 1 {
		return ext, " ", filename, true
	}
	return ext, strings.ToLower(strings.Join(clean[:len(clean)-1], "/")), filename, true
}

func addInt(a, b int) (int, bool) {
	if a < 0 || b < 0 || a > int(^uint(0)>>1)-b {
		return 0, false
	}
	return a + b, true
}
