// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager vpk-parser and vpk_extract tests at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package vpk

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeU16(buf *bytes.Buffer, v uint16) { _ = binary.Write(buf, binary.LittleEndian, v) }
func writeU32(buf *bytes.Buffer, v uint32) { _ = binary.Write(buf, binary.LittleEndian, v) }

func inlineV1(preload, data []byte) []byte {
	var tree bytes.Buffer
	tree.WriteString("txt\x00folder\x00sample\x00")
	writeU32(&tree, 0)
	writeU16(&tree, uint16(len(preload)))
	writeU16(&tree, InlineArchiveIndex)
	writeU32(&tree, 0)
	writeU32(&tree, uint32(len(data)))
	writeU16(&tree, entryTerminator)
	tree.Write(preload)
	tree.Write([]byte{0, 0, 0})

	var out bytes.Buffer
	writeU32(&out, signature)
	writeU32(&out, 1)
	writeU32(&out, uint32(tree.Len()))
	out.Write(tree.Bytes())
	out.Write(data)
	return out.Bytes()
}

func TestReadInlineV1WithPreload(t *testing.T) {
	raw := inlineV1([]byte("pre"), []byte("data"))
	dir := t.TempDir()
	p := filepath.Join(dir, "mod.vpk")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Contains("folder/sample.txt") {
		t.Fatalf("entries = %v", a.List())
	}
	got, err := a.Read("folder/sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "predata" {
		t.Fatalf("read %q, want %q", got, "predata")
	}
}

func TestReadInlineAndCompanionV2(t *testing.T) {
	inline := []byte("inln")
	companion := []byte("comp!")

	var tree bytes.Buffer
	tree.WriteString("txt\x00 \x00inline\x00")
	writeU32(&tree, 0)
	writeU16(&tree, 0)
	writeU16(&tree, InlineArchiveIndex)
	writeU32(&tree, 0)
	writeU32(&tree, uint32(len(inline)))
	writeU16(&tree, entryTerminator)
	tree.WriteString("companion\x00")
	writeU32(&tree, 0)
	writeU16(&tree, 0)
	writeU16(&tree, 0)
	writeU32(&tree, 0)
	writeU32(&tree, uint32(len(companion)))
	writeU16(&tree, entryTerminator)
	tree.WriteByte(0) // end filenames
	tree.WriteByte(0) // end dirs
	tree.WriteByte(0) // end extensions

	var header bytes.Buffer
	writeU32(&header, signature)
	writeU32(&header, 2)
	writeU32(&header, uint32(tree.Len()))
	writeU32(&header, uint32(len(inline)))
	writeU32(&header, 0)
	writeU32(&header, 0)
	writeU32(&header, 0)
	header.Write(tree.Bytes())
	header.Write(inline)

	dir := t.TempDir()
	dirPath := filepath.Join(dir, "pak01_dir.vpk")
	if err := os.WriteFile(dirPath, header.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pak01_000.vpk"), companion, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.Read("inline.txt"); err != nil || string(got) != string(inline) {
		t.Fatalf("inline = %q, %v", got, err)
	}
	if got, err := a.Read("COMPANION.TXT"); err != nil || string(got) != string(companion) {
		t.Fatalf("companion = %q, %v", got, err)
	}
}

func TestPackRoundTrip(t *testing.T) {
	entries := []PackEntry{
		{Path: "models/hero/Hero.vmdl_c", Data: []byte("model")},
		{Path: "scripts/heroes.vdata_c", Data: []byte("vdata")},
		{Path: "readme.txt", Data: []byte("root")},
	}
	raw, err := Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	h, parsed, err := ParseDirectory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 2 || h.FileDataSectionSize != uint32(len("model")+len("vdata")+len("root")) {
		t.Fatalf("header = %#v", h)
	}
	if len(parsed) != 3 {
		t.Fatalf("entries = %d, want 3", len(parsed))
	}
	paths := map[string]bool{}
	for _, e := range parsed {
		paths[e.FullPath] = true
	}
	for _, want := range []string{"models/hero/hero.vmdl_c", "scripts/heroes.vdata_c", "readme.txt"} {
		if !paths[want] {
			t.Fatalf("missing %q in %v", want, paths)
		}
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "pak01_dir.vpk")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range entries {
		got, err := a.Read(in.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, in.Data) {
			t.Fatalf("%s = %q, want %q", in.Path, got, in.Data)
		}
	}
}

func TestPackDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "models", "hero"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models", "hero", "x.vmdl_c"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, count, err := PackDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("file count = %d, want 2", count)
	}
	_, entries, err := ParseDirectory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(entries))
	}
}

func TestRejectsTraversalAndMalformed(t *testing.T) {
	if _, _, _, ok := SplitEntryPath("../evil.txt"); ok {
		t.Fatal("parent traversal accepted")
	}
	if _, _, _, ok := SplitEntryPath("a/../../evil.txt"); ok {
		t.Fatal("nested traversal accepted")
	}
	if _, ok := NormalizeResourcePath("models/../hero.vmdl_c"); ok {
		t.Fatal("normalize accepted parent traversal")
	}
	if _, ok := NormalizeResourcePath("models/CON.vmdl_c"); ok {
		t.Fatal("normalize accepted Windows device name")
	}
	if _, _, err := ParseDirectory([]byte("not a vpk")); err == nil {
		t.Fatal("bad signature parsed")
	}
	short := inlineV1(nil, nil)
	if _, _, err := ParseDirectory(short[:8]); err == nil {
		t.Fatal("short header parsed")
	}
	overrun := append([]byte(nil), short...)
	overrun[8] = 0xFF
	overrun[9] = 0xFF
	overrun[10] = 0xFF
	overrun[11] = 0xFF
	if _, _, err := ParseDirectory(overrun); err == nil {
		t.Fatal("oversized tree parsed")
	}
}
