// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpkmanager/src/source2/kv3/reader.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa.
//
// The upstream file is MIT-licensed; it is distributed here as part of the
// GPL-3.0 MyLock work. See ../../NOTICE.md.

package kv3

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

const (
	// magicLegacy is the pre-versioned VKV3 magic. Unsupported.
	magicLegacy uint32 = 0x03564B56
	// magicBase is shared by all versioned magics: 'V' 'K' '3' little-endian
	// with the version in the low byte (05 33 56 4B = v5).
	magicBase       uint32 = 0x4B563300
	kv3Trailer      uint32 = 0xFFEEDD00
	lz4FrameSize    uint16 = 16384
	maxDecodedBytes        = 512 << 20
)

// Decode decodes a binary KV3 DATA payload into a Value tree.
func Decode(data []byte) (Value, error) {
	value, _, err := decodeWithStats(data)
	return value, err
}

// DecodePreserving decodes a payload and also returns the wire encoding tree.
func DecodePreserving(data []byte) (Value, Encoding, error) {
	return decodeWithStats(data)
}

// EncodingStats decodes a payload and returns only the structural statistics.
func EncodingStatsOf(data []byte) (EncodingStats, error) {
	_, _, stats, err := decodeAll(data)
	return stats, err
}

func decodeWithStats(data []byte) (Value, Encoding, error) {
	value, encoding, _, err := decodeAll(data)
	return value, encoding, err
}

func decodeAll(data []byte) (Value, Encoding, EncodingStats, error) {
	h := newCursor(data)
	magic, err := h.u32()
	if err != nil {
		return Value{}, Encoding{}, EncodingStats{}, err
	}
	if magic == magicLegacy {
		return Value{}, Encoding{}, EncodingStats{}, fmt.Errorf("%w: magic %#x", ErrUnsupported, magic)
	}
	version := magic & 0xFF
	if magic&0xFFFFFF00 != magicBase || version < 1 || version > 5 {
		return Value{}, Encoding{}, EncodingStats{}, fmt.Errorf("%w: magic %#x", ErrUnsupported, magic)
	}

	if _, err := h.bytes(16); err != nil { // format GUID
		return Value{}, Encoding{}, EncodingStats{}, err
	}
	compression, err := h.u32()
	if err != nil {
		return Value{}, Encoding{}, EncodingStats{}, err
	}

	var countB1, countB4, countB8 int64
	var countTypes, countBlocks int64
	var frame uint16
	var sizeBlobs, sizeBlockCompressed int64
	var sizeUncTotal, sizeCompTotal int64

	if version == 1 {
		if countB1, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countB4, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countB8, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeUncTotal, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		sizeCompTotal = int64(len(data) - h.pos)
	} else {
		if _, err = h.u16(); err != nil { // dictionary id
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if frame, err = h.u16(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if compression == 1 && frame != lz4FrameSize {
			return Value{}, Encoding{}, EncodingStats{}, invalid("unexpected LZ4 frame size")
		}
		if countB1, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countB4, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countB8, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countTypes, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if _, err = h.u16(); err != nil { // count objects
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if _, err = h.u16(); err != nil { // count arrays
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeUncTotal, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeCompTotal, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countBlocks, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeBlobs, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
	}

	var countB2 int64
	if version >= 4 {
		if countB2, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeBlockCompressed, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
	}

	var sizeUncBuf2, sizeCompBuf2 int64
	var auxCounts SubCounts
	var mainObjCount int64
	var sizeUncBuf1, sizeCompBuf1 int64

	if version >= 5 {
		if sizeUncBuf1, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeCompBuf1, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeUncBuf2, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if sizeCompBuf2, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		auxCounts = SubCounts{B1: countB1, B2: countB2, B4: countB4, B8: countB8}
		var mainB1, mainB2, mainB4, mainB8 int64
		if mainB1, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if mainB2, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if mainB4, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if mainB8, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if _, err = h.i32(); err != nil { // unknown
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if mainObjCount, err = h.i32(); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if _, err = h.i32(); err != nil { // count arrays
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if _, err = h.i32(); err != nil { // unknown
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		countB1, countB2, countB4, countB8 = mainB1, mainB2, mainB4, mainB8
	} else {
		sizeUncBuf1, sizeCompBuf1 = sizeUncTotal, sizeCompTotal
	}

	if countBlocks > 0 && version < 4 {
		return Value{}, Encoding{}, EncodingStats{}, invalid("binary blobs require KV3 v4+")
	}

	buf1, err := readBuffer(&h, compression, sizeUncBuf1, sizeCompBuf1)
	if err != nil {
		return Value{}, Encoding{}, EncodingStats{}, err
	}
	var buf2 []byte
	if version >= 5 {
		if buf2, err = readBuffer(&h, compression, sizeUncBuf2, sizeCompBuf2); err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
	}

	var ctx *decodeCtx
	if version >= 5 {
		aux, strings, err := layoutAuxV5(buf1, auxCounts)
		if err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		mainCounts := SubCounts{B1: countB1, B2: countB2, B4: countB4, B8: countB8}
		mainBuffers, objectLengths, types, afterTypes, err := layoutMainV5(buf2, mainCounts, mainObjCount, countTypes, countBlocks)
		if err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		ctx = &decodeCtx{
			version:       version,
			stats:         EncodingStats{Version: version},
			strings:       strings,
			types:         newCursor(types),
			objectLengths: newCursor(objectLengths),
			main:          mainBuffers,
			aux:           aux,
		}
		if countBlocks > 0 {
			ctx.blobs, err = readBlobs(&h, buf2, afterTypes, compression, int(frame), countBlocks, sizeBlobs, sizeBlockCompressed)
			if err != nil {
				return Value{}, Encoding{}, EncodingStats{}, err
			}
		}
	} else {
		var afterTypes int
		ctx, afterTypes, err = layoutSingle(buf1, version, countB1, countB2, countB4, countB8, countTypes, sizeUncTotal, countBlocks)
		if err != nil {
			return Value{}, Encoding{}, EncodingStats{}, err
		}
		if countBlocks > 0 {
			ctx.blobs, err = readBlobs(&h, buf1, afterTypes, compression, int(frame), countBlocks, sizeBlobs, sizeBlockCompressed)
			if err != nil {
				return Value{}, Encoding{}, EncodingStats{}, err
			}
		}
	}

	rootType, rootFlag, err := readType(ctx)
	if err != nil {
		return Value{}, Encoding{}, EncodingStats{}, err
	}
	value, encoding, err := readValue(ctx, rootType, rootFlag)
	if err != nil {
		return Value{}, Encoding{}, EncodingStats{}, err
	}
	return value, encoding, ctx.stats, nil
}

type SubCounts struct {
	B1, B2, B4, B8 int64
}

type buffers struct {
	B1 cursor
	B2 cursor
	B4 cursor
	B8 cursor
}

type decodeCtx struct {
	version       uint32
	stats         EncodingStats
	strings       []string
	types         cursor
	objectLengths cursor
	main          buffers
	aux           buffers
	blobs         [][]byte
	nextBlob      int
}

func readBuffer(h *cursor, compression uint32, sizeUnc, sizeComp int64) ([]byte, error) {
	unc, err := usizeOf(sizeUnc)
	if err != nil {
		return nil, err
	}
	if unc > maxDecodedBytes {
		return nil, invalid("buffer too large: %d bytes", unc)
	}
	switch compression {
	case 0:
		raw, err := h.bytes(unc)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), raw...), nil
	case 1:
		comp, err := usizeOf(sizeComp)
		if err != nil {
			return nil, err
		}
		input, err := h.bytes(comp)
		if err != nil {
			return nil, err
		}
		out := make([]byte, unc)
		n, err := lz4.UncompressBlock(input, out)
		if err != nil {
			return nil, fmt.Errorf("%w: LZ4: %v", ErrInvalid, err)
		}
		if n != unc {
			return nil, invalid("LZ4: expected %d bytes, got %d", unc, n)
		}
		return out, nil
	case 2:
		comp, err := usizeOf(sizeComp)
		if err != nil {
			return nil, err
		}
		input, err := h.bytes(comp)
		if err != nil {
			return nil, err
		}
		return zstdDecompress(input, unc)
	default:
		return nil, fmt.Errorf("%w: %d", ErrCompression, compression)
	}
}

func zstdDecompress(input []byte, sizeUnc int) ([]byte, error) {
	dec, err := zstd.NewReader(bytes.NewReader(input), zstd.WithDecoderMaxMemory(maxDecodedBytes))
	if err != nil {
		return nil, invalid("ZSTD init failed: %v", err)
	}
	defer dec.Close()
	out := make([]byte, sizeUnc)
	if _, err := io.ReadFull(dec, out); err != nil {
		return nil, invalid("ZSTD decompress failed: %v", err)
	}
	return out, nil
}

// readBlobs reads the binary-blob section. afterTypes is the offset into the
// decompressed buf just past the type stream: the per-blob uncompressed
// lengths, the trailer, then (for LZ4) the per-frame compressed-size table.
// The compressed frames follow the buffers in the stream h.
func readBlobs(h *cursor, buf []byte, afterTypes int, compression uint32, frameSize int, countBlocks, sizeBlobs, sizeBlockCompressed int64) ([][]byte, error) {
	count, err := usizeOf(countBlocks)
	if err != nil {
		return nil, err
	}
	blobSize, err := usizeOf(sizeBlobs)
	if err != nil {
		return nil, err
	}
	if blobSize > maxDecodedBytes {
		return nil, invalid("blob region too large: %d bytes", blobSize)
	}

	lengthsLen, ok := mulCheck(count, 4)
	if !ok {
		return nil, invalid("blob length table overflow")
	}
	off := afterTypes
	lengthsRegion, err := slice(buf, off, lengthsLen)
	if err != nil {
		return nil, err
	}
	off += lengthsLen
	lengths := make([]int, 0, count)
	for i := 0; i+4 <= len(lengthsRegion); i += 4 {
		v := int64(int32(binary.LittleEndian.Uint32(lengthsRegion[i:])))
		n, err := usizeOf(v)
		if err != nil {
			return nil, err
		}
		lengths = append(lengths, n)
	}
	if err := checkTrailer(buf, off); err != nil {
		return nil, err
	}
	off += 4

	var blobBuf []byte
	switch compression {
	case 0:
		blobBuf, err = h.bytes(blobSize)
		if err != nil {
			return nil, err
		}
		blobBuf = append([]byte(nil), blobBuf...)
	case 1:
		tableLen, err := usizeOf(sizeBlockCompressed)
		if err != nil {
			return nil, err
		}
		table, err := slice(buf, off, tableLen)
		if err != nil {
			return nil, err
		}
		blobBuf, err = decompressBlobFrames(h, table, frameSize, blobSize)
		if err != nil {
			return nil, err
		}
	case 2:
		blobBuf, err = zstdDecompress(h.rest(), blobSize)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: %d", ErrCompression, compression)
	}

	blobs := make([][]byte, 0, count)
	p := 0
	for _, n := range lengths {
		end, ok := addCheck(p, n)
		if !ok {
			return nil, invalid("blob slice overflow")
		}
		if end > len(blobBuf) {
			return nil, invalid("blob region underrun")
		}
		blobs = append(blobs, append([]byte(nil), blobBuf[p:end]...))
		p = end
	}
	return blobs, nil
}

// decompressBlobFrames decompresses chained-LZ4 blob frames. Each frame decodes
// against all previously decoded blob bytes as its dictionary.
func decompressBlobFrames(h *cursor, table []byte, frameSize, sizeBlobs int) ([]byte, error) {
	if frameSize == 0 {
		return nil, invalid("zero blob frame size")
	}
	out := make([]byte, sizeBlobs)
	done := 0
	for i := 0; i+2 <= len(table); i += 2 {
		if done >= sizeBlobs {
			break
		}
		comp := int(binary.LittleEndian.Uint16(table[i:]))
		input, err := h.bytes(comp)
		if err != nil {
			return nil, err
		}
		dict := out[:done]
		rest := out[done:]
		capLen := frameSize
		if capLen > len(rest) {
			capLen = len(rest)
		}
		n, err := lz4.UncompressBlockWithDict(input, rest[:capLen], dict)
		if err != nil {
			return nil, fmt.Errorf("%w: LZ4 blob: %v", ErrInvalid, err)
		}
		if n == 0 {
			return nil, invalid("empty blob frame (no progress)")
		}
		done += n
	}
	if done != sizeBlobs {
		return nil, invalid("blob size mismatch: expected %d, got %d", sizeBlobs, done)
	}
	return out, nil
}

// layoutAuxV5 carves [b1][align2 b2][align4 b4][align8 b8]. The string table is
// the null-terminated run at the front of b1, and the string count is the first
// int of b4.
func layoutAuxV5(buf []byte, counts SubCounts) (buffers, []string, error) {
	off := 0
	b1Start := off
	n, err := usizeOf(counts.B1)
	if err != nil {
		return buffers{}, nil, err
	}
	off += n
	b2Start, b2Len, err := lane(&off, counts.B2, 2)
	if err != nil {
		return buffers{}, nil, err
	}
	b4Start, b4Len, err := lane(&off, counts.B4, 4)
	if err != nil {
		return buffers{}, nil, err
	}
	b8Start, b8Len, err := lane(&off, counts.B8, 8)
	if err != nil {
		return buffers{}, nil, err
	}

	countRaw, err := readI32At(buf, b4Start)
	if err != nil {
		return buffers{}, nil, err
	}
	if countRaw < 0 {
		return buffers{}, nil, invalid("negative string count")
	}
	sp := b1Start
	stringsOut := make([]string, 0, countRaw)
	for range countRaw {
		s, err := readCString(buf, &sp)
		if err != nil {
			return buffers{}, nil, err
		}
		stringsOut = append(stringsOut, s)
	}

	b1End, ok := addCheck(b1Start, n)
	if !ok {
		return buffers{}, nil, invalid("offset overflow")
	}
	b1Bytes, err := slice(buf, sp, b1End-sp)
	if err != nil {
		return buffers{}, nil, err
	}
	b2Bytes, err := slice(buf, b2Start, b2Len)
	if err != nil {
		return buffers{}, nil, err
	}
	b4Bytes, err := slice(buf, b4Start+4, maxInt(b4Len-4, 0))
	if err != nil {
		return buffers{}, nil, err
	}
	b8Bytes, err := slice(buf, b8Start, b8Len)
	if err != nil {
		return buffers{}, nil, err
	}
	return buffers{
		B1: newCursor(b1Bytes),
		B2: newCursor(b2Bytes),
		B4: newCursor(b4Bytes),
		B8: newCursor(b8Bytes),
	}, stringsOut, nil
}

// layoutMainV5 carves [object_lengths][b1][align2 b2][align4 b4][align8 b8][types][trailer]
// and returns the offset just past types.
func layoutMainV5(buf []byte, counts SubCounts, objCount, countTypes, countBlocks int64) (buffers, []byte, []byte, int, error) {
	off := 0
	olStart := off
	objN, err := usizeOf(objCount)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	olLen, ok := mulCheck(objN, 4)
	if !ok {
		return buffers{}, nil, nil, 0, invalid("object length table overflow")
	}
	off += olLen
	b1Start := off
	b1Len, err := usizeOf(counts.B1)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	off += b1Len
	b2Start, b2Len, err := lane(&off, counts.B2, 2)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	b4Start, b4Len, err := lane(&off, counts.B4, 4)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	b8Start, b8Len, err := lane(&off, counts.B8, 8)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	typesStart := off
	typesLen, err := usizeOf(countTypes)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	off += typesLen
	if countBlocks == 0 {
		if err := checkTrailer(buf, off); err != nil {
			return buffers{}, nil, nil, 0, err
		}
	}

	b1Bytes, err := slice(buf, b1Start, b1Len)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	b2Bytes, err := slice(buf, b2Start, b2Len)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	b4Bytes, err := slice(buf, b4Start, b4Len)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	b8Bytes, err := slice(buf, b8Start, b8Len)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	objectLengths, err := slice(buf, olStart, olLen)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	types, err := slice(buf, typesStart, typesLen)
	if err != nil {
		return buffers{}, nil, nil, 0, err
	}
	return buffers{
		B1: newCursor(b1Bytes),
		B2: newCursor(b2Bytes),
		B4: newCursor(b4Bytes),
		B8: newCursor(b8Bytes),
	}, objectLengths, types, off, nil
}

// layoutSingle carves [b1][align2 b2][align4 b4][align8 b8][strings][types][trailer]
// for v1..=4. Strings and types are inline regions; object lengths come from b4.
func layoutSingle(buf []byte, version uint32, countB1, countB2, countB4, countB8, countTypes, sizeUncTotal, countBlocks int64) (*decodeCtx, int, error) {
	off := 0
	b1Start := off
	b1Len, err := usizeOf(countB1)
	if err != nil {
		return nil, 0, err
	}
	off += b1Len
	b2Start, b2Len, err := lane(&off, countB2, 2)
	if err != nil {
		return nil, 0, err
	}
	b4Start, b4Len, err := lane(&off, countB4, 4)
	if err != nil {
		return nil, 0, err
	}
	var b8Start, b8Len int
	if countB8 > 0 {
		b8Start, b8Len, err = lane(&off, countB8, 8)
		if err != nil {
			return nil, 0, err
		}
	} else {
		align(&off, 8)
		b8Start, b8Len = off, 0
	}

	countRaw, err := readI32At(buf, b4Start)
	if err != nil {
		return nil, 0, err
	}
	if countRaw < 0 {
		return nil, 0, invalid("negative string count")
	}
	stringsStart := off
	sp := off
	stringsOut := make([]string, 0, countRaw)
	for range countRaw {
		s, err := readCString(buf, &sp)
		if err != nil {
			return nil, 0, err
		}
		stringsOut = append(stringsOut, s)
	}
	off = sp

	var typesLen int
	if version == 1 {
		total, err := usizeOf(sizeUncTotal)
		if err != nil {
			return nil, 0, err
		}
		typesLen = maxInt(total-off-4, 0)
	} else {
		ct, err := usizeOf(countTypes)
		if err != nil {
			return nil, 0, err
		}
		typesLen = maxInt(ct-(off-stringsStart), 0)
	}
	types, err := slice(buf, off, typesLen)
	if err != nil {
		return nil, 0, err
	}
	off += typesLen
	afterTypes := off
	if countBlocks == 0 {
		if err := checkTrailer(buf, off); err != nil {
			return nil, 0, err
		}
	}

	b1Bytes, err := slice(buf, b1Start, b1Len)
	if err != nil {
		return nil, 0, err
	}
	b2Bytes, err := slice(buf, b2Start, b2Len)
	if err != nil {
		return nil, 0, err
	}
	b4Bytes, err := slice(buf, b4Start+4, maxInt(b4Len-4, 0))
	if err != nil {
		return nil, 0, err
	}
	b8Bytes, err := slice(buf, b8Start, b8Len)
	if err != nil {
		return nil, 0, err
	}
	return &decodeCtx{
		version:       version,
		stats:         EncodingStats{Version: version},
		strings:       stringsOut,
		types:         newCursor(types),
		objectLengths: cursor{},
		main: buffers{
			B1: newCursor(b1Bytes),
			B2: newCursor(b2Bytes),
			B4: newCursor(b4Bytes),
			B8: newCursor(b8Bytes),
		},
		aux: buffers{},
	}, afterTypes, nil
}

func readType(ctx *decodeCtx) (uint8, uint8, error) {
	datatype, err := ctx.types.u8()
	if err != nil {
		return 0, 0, err
	}
	var flag uint8
	if datatype&0x80 != 0 {
		if ctx.version >= 3 {
			datatype &= 0x3F
		} else {
			datatype &= 0x7F
		}
		if flag, err = ctx.types.u8(); err != nil {
			return 0, 0, err
		}
	}
	ctx.stats.Nodes++
	if datatype > nodeArrayTypeByteLength {
		ctx.stats.UnsupportedV4Types++
	}
	if flag != 0 {
		ctx.stats.FlaggedNodes++
	}
	switch datatype {
	case nodeArrayTyped, nodeArrayTypeByteLength, nodeArrayTypeAuxiliary:
		ctx.stats.TypedArrays++
	}
	if datatype == nodeArrayTypeAuxiliary {
		ctx.stats.AuxiliaryArrays++
	}
	switch datatype {
	case nodeInt32, nodeUint32, nodeFloat, nodeInt16, nodeUint16, nodeInt32AsByte:
		ctx.stats.NarrowNumbers++
	}
	return datatype, flag, nil
}

func readValue(ctx *decodeCtx, datatype, flag uint8) (Value, Encoding, error) {
	leaf := func(v Value) (Value, Encoding, error) {
		return v, Encoding{DataType: datatype, Flag: flag}, nil
	}
	switch datatype {
	case nodeNull:
		return leaf(Null())
	case nodeBooleanTrue:
		return leaf(Bool(true))
	case nodeBooleanFalse:
		return leaf(Bool(false))
	case nodeInt64Zero:
		return leaf(Int(0))
	case nodeInt64One:
		return leaf(Int(1))
	case nodeDoubleZero:
		return leaf(Double(0))
	case nodeDoubleOne:
		return leaf(Double(1))
	case nodeUint32Zero:
		return leaf(UInt(0))
	case nodeUint32One:
		return leaf(UInt(1))
	case nodeInt8:
		b, err := ctx.main.B1.u8()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Int(int64(int8(b))))
	case nodeBoolean:
		b, err := ctx.main.B1.u8()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Bool(b == 1))
	case nodeInt32AsByte:
		b, err := ctx.main.B1.u8()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Int(int64(b)))
	case nodeInt16:
		v, err := ctx.main.B2.u16()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Int(int64(int16(v))))
	case nodeUint16:
		v, err := ctx.main.B2.u16()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(UInt(uint64(v)))
	case nodeInt32:
		v, err := ctx.main.B4.u32()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Int(int64(int32(v))))
	case nodeUint32:
		v, err := ctx.main.B4.u32()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(UInt(uint64(v)))
	case nodeFloat:
		v, err := ctx.main.B4.u32()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Double(float64(math.Float32frombits(v))))
	case nodeInt64:
		v, err := ctx.main.B8.u64()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Int(int64(v)))
	case nodeUint64:
		v, err := ctx.main.B8.u64()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(UInt(v))
	case nodeDouble:
		v, err := ctx.main.B8.u64()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(Double(math.Float64frombits(v)))
	case nodeString:
		id, err := ctx.main.B4.u32()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		s, err := ctx.lookupString(int32(id))
		if err != nil {
			return Value{}, Encoding{}, err
		}
		return leaf(String(s))
	case nodeArray:
		n, err := ctx.main.B4.u32()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		items := make([]Value, 0, n)
		encodings := make([]Encoding, 0, n)
		for range n {
			itemType, itemFlag, err := readType(ctx)
			if err != nil {
				return Value{}, Encoding{}, err
			}
			value, encoding, err := readValue(ctx, itemType, itemFlag)
			if err != nil {
				return Value{}, Encoding{}, err
			}
			items = append(items, value)
			encodings = append(encodings, encoding)
		}
		return Value{Kind: KindArray, Array: items}, Encoding{
			DataType: datatype,
			Flag:     flag,
			Children: EncodingChildren{Kind: ChildrenArray, Array: encodings},
		}, nil
	case nodeArrayTyped, nodeArrayTypeByteLength:
		var n uint32
		if datatype == nodeArrayTypeByteLength {
			b, err := ctx.main.B1.u8()
			if err != nil {
				return Value{}, Encoding{}, err
			}
			n = uint32(b)
		} else {
			var err error
			if n, err = ctx.main.B4.u32(); err != nil {
				return Value{}, Encoding{}, err
			}
		}
		sub, subFlag, err := readType(ctx)
		if err != nil {
			return Value{}, Encoding{}, err
		}
		items := make([]Value, 0, n)
		encodings := make([]Encoding, 0, n)
		for range n {
			value, encoding, err := readValue(ctx, sub, subFlag)
			if err != nil {
				return Value{}, Encoding{}, err
			}
			items = append(items, value)
			encodings = append(encodings, encoding)
		}
		return Value{Kind: KindArray, Array: items}, Encoding{
			DataType: datatype,
			Flag:     flag,
			Children: EncodingChildren{Kind: ChildrenArray, Array: encodings},
		}, nil
	case nodeArrayTypeAuxiliary:
		b, err := ctx.main.B1.u8()
		if err != nil {
			return Value{}, Encoding{}, err
		}
		n := uint32(b)
		sub, subFlag, err := readType(ctx)
		if err != nil {
			return Value{}, Encoding{}, err
		}
		ctx.main, ctx.aux = ctx.aux, ctx.main
		items := make([]Value, 0, n)
		encodings := make([]Encoding, 0, n)
		var firstErr error
		for range n {
			value, encoding, err := readValue(ctx, sub, subFlag)
			if err != nil {
				firstErr = err
				break
			}
			items = append(items, value)
			encodings = append(encodings, encoding)
		}
		ctx.main, ctx.aux = ctx.aux, ctx.main
		if firstErr != nil {
			return Value{}, Encoding{}, firstErr
		}
		return Value{Kind: KindArray, Array: items}, Encoding{
			DataType: datatype,
			Flag:     flag,
			Children: EncodingChildren{Kind: ChildrenArray, Array: encodings},
		}, nil
	case nodeObject:
		var n uint32
		if ctx.version >= 5 {
			var err error
			if n, err = ctx.objectLengths.u32(); err != nil {
				return Value{}, Encoding{}, err
			}
		} else {
			var err error
			if n, err = ctx.main.B4.u32(); err != nil {
				return Value{}, Encoding{}, err
			}
		}
		pairs := make([]Pair, 0, n)
		encodings := make([]EncodingField, 0, n)
		for range n {
			valueType, valueFlag, err := readType(ctx)
			if err != nil {
				return Value{}, Encoding{}, err
			}
			id, err := ctx.main.B4.u32()
			if err != nil {
				return Value{}, Encoding{}, err
			}
			name, err := ctx.lookupString(int32(id))
			if err != nil {
				return Value{}, Encoding{}, err
			}
			value, encoding, err := readValue(ctx, valueType, valueFlag)
			if err != nil {
				return Value{}, Encoding{}, err
			}
			encodings = append(encodings, EncodingField{Key: name, Value: encoding})
			pairs = append(pairs, Pair{Key: name, Value: value})
		}
		return Value{Kind: KindObject, Object: pairs}, Encoding{
			DataType: datatype,
			Flag:     flag,
			Children: EncodingChildren{Kind: ChildrenObject, Object: encodings},
		}, nil
	case nodeBinaryBlob:
		if ctx.nextBlob >= len(ctx.blobs) {
			return Value{}, Encoding{}, invalid("blob index out of range")
		}
		blob := ctx.blobs[ctx.nextBlob]
		ctx.blobs[ctx.nextBlob] = nil
		ctx.nextBlob++
		return leaf(Binary(blob))
	default:
		return Value{}, Encoding{}, fmt.Errorf("%w: %d", ErrNodeType, datatype)
	}
}

func (ctx *decodeCtx) lookupString(id int32) (string, error) {
	if id == -1 {
		return "", nil
	}
	if id < 0 || int(id) >= len(ctx.strings) {
		return "", invalid("string id out of range")
	}
	return ctx.strings[id], nil
}

// --- byte-slice helpers ---------------------------------------------------

type cursor struct {
	data []byte
	pos  int
}

func newCursor(data []byte) cursor { return cursor{data: data} }

func (c *cursor) take(n int) ([]byte, error) {
	end, ok := addCheck(c.pos, n)
	if !ok {
		return nil, invalid("offset overflow")
	}
	if end > len(c.data) {
		return nil, truncated(c.pos, n, len(c.data)-c.pos)
	}
	out := c.data[c.pos:end]
	c.pos = end
	return out, nil
}

func (c *cursor) bytes(n int) ([]byte, error) { return c.take(n) }

// rest returns the unread remainder.
func (c *cursor) rest() []byte { return c.data[c.pos:] }

func (c *cursor) u8() (uint8, error) {
	b, err := c.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (c *cursor) u16() (uint16, error) {
	b, err := c.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (c *cursor) u32() (uint32, error) {
	b, err := c.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (c *cursor) i32() (int64, error) {
	v, err := c.u32()
	if err != nil {
		return 0, err
	}
	return int64(int32(v)), nil
}

func (c *cursor) u64() (uint64, error) {
	b, err := c.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func align(off *int, alignment int) {
	a := alignment - 1
	*off = (*off + a) &^ a
}

// lane advances off past an aligned typed lane of count items of elem bytes,
// returning (start, byteLen).
func lane(off *int, count int64, elem int) (int, int, error) {
	if count <= 0 {
		return *off, 0, nil
	}
	align(off, elem)
	start := *off
	n, err := usizeOf(count)
	if err != nil {
		return 0, 0, err
	}
	length, ok := mulCheck(n, elem)
	if !ok {
		return 0, 0, invalid("typed lane overflow")
	}
	*off += length
	return start, length, nil
}

func slice(buf []byte, start, length int) ([]byte, error) {
	end, ok := addCheck(start, length)
	if !ok {
		return nil, invalid("offset overflow")
	}
	if start < 0 || end > len(buf) {
		return nil, invalid("sub-buffer out of range")
	}
	return buf[start:end], nil
}

func readI32At(buf []byte, at int) (int, error) {
	b, err := slice(buf, at, 4)
	if err != nil {
		return 0, err
	}
	return int(int32(binary.LittleEndian.Uint32(b))), nil
}

func readCString(buf []byte, pos *int) (string, error) {
	start := *pos
	for *pos < len(buf) && buf[*pos] != 0 {
		*pos = *pos + 1
	}
	if *pos >= len(buf) {
		return "", invalid("unterminated string")
	}
	s := lossyUTF8(buf[start:*pos])
	*pos = *pos + 1 // skip NUL
	return s, nil
}

func checkTrailer(buf []byte, at int) error {
	v, err := readU32At(buf, at)
	if err != nil {
		return err
	}
	if v != kv3Trailer {
		return invalid("missing 0xFFEEDD00 trailer")
	}
	return nil
}

func readU32At(buf []byte, at int) (uint32, error) {
	b, err := slice(buf, at, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func usizeOf(v int64) (int, error) {
	if v < 0 || uint64(v) > uint64(math.MaxInt) {
		return 0, invalid("negative or oversized count")
	}
	return int(v), nil
}

func addCheck(a, b int) (int, bool) {
	if a < 0 || b < 0 || a > math.MaxInt-b {
		return 0, false
	}
	return a + b, true
}

func mulCheck(a, b int) (int, bool) {
	if a < 0 || b < 0 || (a != 0 && b > math.MaxInt/a) {
		return 0, false
	}
	return a * b, true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
