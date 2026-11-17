// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Synthetic v5 vectors for the decoder. The layout mirrors the upstream
// reader's header handling; see ../../NOTICE.md.

package kv3

import (
	"encoding/binary"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// buildV5ObjectA builds a v5 payload whose root is {"a": Int(1)}. It exercises
// the two-buffer v5 layout (aux holds the string table, main holds object
// lengths, field ids, types and the trailer) with the given compression.
func buildV5ObjectA(t *testing.T, compression uint32) []byte {
	t.Helper()

	aux := make([]byte, 8)
	copy(aux, "a\x00")
	binary.LittleEndian.PutUint32(aux[4:], 1) // string count

	main := make([]byte, 14)
	binary.LittleEndian.PutUint32(main[0:], 1) // object member count
	binary.LittleEndian.PutUint32(main[4:], 0) // field name string id
	main[8] = nodeObject
	main[9] = nodeInt64One
	binary.LittleEndian.PutUint32(main[10:], kv3Trailer)

	var buf1, buf2 []byte
	switch compression {
	case 0:
		buf1, buf2 = aux, main
	case 1:
		buf1 = compressLZ4(t, aux)
		buf2 = compressLZ4(t, main)
	case 2:
		buf1 = compressZSTD(t, aux)
		buf2 = compressZSTD(t, main)
	default:
		t.Fatalf("unsupported compression %d", compression)
	}

	header := make([]byte, 120)
	binary.LittleEndian.PutUint32(header[0:], magicBase|5)
	binary.LittleEndian.PutUint32(header[20:], compression)
	binary.LittleEndian.PutUint16(header[24:], 0) // dictionary id
	binary.LittleEndian.PutUint16(header[26:], lz4FrameSize)
	binary.LittleEndian.PutUint32(header[28:], 2) // aux b1 bytes
	binary.LittleEndian.PutUint32(header[32:], 1) // aux b4 items
	binary.LittleEndian.PutUint32(header[36:], 0) // aux b8 items
	binary.LittleEndian.PutUint32(header[40:], 2) // main type bytes
	binary.LittleEndian.PutUint16(header[44:], 1) // count objects
	binary.LittleEndian.PutUint16(header[46:], 0) // count arrays
	binary.LittleEndian.PutUint32(header[48:], uint32(len(aux)+len(main)))
	binary.LittleEndian.PutUint32(header[52:], uint32(len(buf1)+len(buf2)))
	binary.LittleEndian.PutUint32(header[56:], 0) // count blocks
	binary.LittleEndian.PutUint32(header[60:], 0) // size binary blobs
	binary.LittleEndian.PutUint32(header[64:], 0) // aux b2 items
	binary.LittleEndian.PutUint32(header[68:], 0) // block compressed sizes
	binary.LittleEndian.PutUint32(header[72:], uint32(len(aux)))
	binary.LittleEndian.PutUint32(header[76:], uint32(len(buf1)))
	binary.LittleEndian.PutUint32(header[80:], uint32(len(main)))
	binary.LittleEndian.PutUint32(header[84:], uint32(len(buf2)))
	binary.LittleEndian.PutUint32(header[88:], 0)  // main b1
	binary.LittleEndian.PutUint32(header[92:], 0)  // main b2
	binary.LittleEndian.PutUint32(header[96:], 1)  // main b4 items
	binary.LittleEndian.PutUint32(header[100:], 0) // main b8 items
	binary.LittleEndian.PutUint32(header[104:], 0) // unknown
	binary.LittleEndian.PutUint32(header[108:], 1) // object length entries
	binary.LittleEndian.PutUint32(header[112:], 0)
	binary.LittleEndian.PutUint32(header[116:], 0)

	out := append(header, buf1...)
	out = append(out, buf2...)
	return out
}

func compressLZ4(t *testing.T, src []byte) []byte {
	t.Helper()
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	n, err := lz4.CompressBlock(src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("LZ4 compression returned no data")
	}
	return dst[:n]
}

func compressZSTD(t *testing.T, src []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll(src, nil)
}

func TestDecodeV5Buffers(t *testing.T) {
	want := Object(Pair{"a", Int(1)})
	for _, compression := range []uint32{0, 1, 2} {
		payload := buildV5ObjectA(t, compression)
		got, err := Decode(payload)
		if err != nil {
			t.Fatalf("compression %d: %v", compression, err)
		}
		if !Equal(&got, &want) {
			t.Fatalf("compression %d: decoded %#v, want %#v", compression, got, want)
		}
	}
}
