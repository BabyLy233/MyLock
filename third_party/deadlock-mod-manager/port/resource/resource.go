// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpkmanager/src/source2/resource/{header,mod}.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa.
//
// The upstream files are MIT-licensed; they are distributed here as part of
// the GPL-3.0 MyLock work. See ../../NOTICE.md.

// Package resource parses and rebuilds Source 2 resource containers: a 16-byte
// header followed by a block table and 16-byte-aligned block payloads.
package resource

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

var (
	// ErrInvalid reports a malformed container or an unsupported layout.
	ErrInvalid = errors.New("resource: invalid")
	// ErrMissingDataBlock reports a container with no DATA block.
	ErrMissingDataBlock = errors.New("resource: missing DATA block")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// BlockTypeData is the FOURCC of the KV3 DATA block.
var BlockTypeData = [4]byte{'D', 'A', 'T', 'A'}

const (
	headerLen           = 16
	knownHeaderVersion  = 12
	blockTableEntryLen  = 12
	blockTableFieldPos  = 8
	maxBlockTableBlocks = 1 << 20
)

// Block is one entry of the resource block table.
type Block struct {
	Kind   [4]byte
	Offset uint32 // absolute byte offset within the resource file
	Size   uint32
}

// Resource is a parsed resource container backed by its original bytes.
type Resource struct {
	bytes  []byte
	blocks []Block
}

// Parse parses a resource container.
func Parse(data []byte) (*Resource, error) {
	pos := 0
	if _, err := readU32(data, &pos); err != nil { // file size
		return nil, err
	}
	headerVersion, err := readU16(data, &pos)
	if err != nil {
		return nil, err
	}
	if headerVersion != knownHeaderVersion {
		return nil, invalid("unexpected header version %d", headerVersion)
	}
	if _, err := readU16(data, &pos); err != nil { // resource version
		return nil, err
	}

	blockOffsetFieldPos := pos
	blockOffset, err := readU32(data, &pos)
	if err != nil {
		return nil, err
	}
	blockCount, err := readU32(data, &pos)
	if err != nil {
		return nil, err
	}
	if blockCount > maxBlockTableBlocks {
		return nil, invalid("too many blocks: %d", blockCount)
	}

	tableStart, ok := addU32(uint64(blockOffsetFieldPos), uint64(blockOffset))
	if !ok {
		return nil, invalid("block_offset overflow")
	}
	tableLen := uint64(blockCount) * blockTableEntryLen
	tableEnd, ok := addU32(tableStart, tableLen)
	if !ok {
		return nil, invalid("block table extent overflow")
	}
	if tableStart < headerLen || tableEnd > uint64(len(data)) {
		return nil, invalid("block table out of range")
	}

	cursor := int(tableStart)
	blocks := make([]Block, 0, blockCount)
	for range blockCount {
		if cursor+4 > len(data) {
			return nil, truncated(cursor, 4, len(data)-cursor)
		}
		var kind [4]byte
		copy(kind[:], data[cursor:cursor+4])
		cursor += 4
		offsetFieldPos := cursor
		relOffset, err := readU32(data, &cursor)
		if err != nil {
			return nil, err
		}
		size, err := readU32(data, &cursor)
		if err != nil {
			return nil, err
		}
		absOffset, ok := addU32(uint64(offsetFieldPos), uint64(relOffset))
		if !ok || absOffset > math.MaxUint32 {
			return nil, invalid("block offset overflow")
		}
		end, ok := addU32(absOffset, uint64(size))
		if !ok || end > uint64(len(data)) || (size != 0 && absOffset < tableEnd) {
			return nil, invalid("block payload out of range")
		}
		blocks = append(blocks, Block{Kind: kind, Offset: uint32(absOffset), Size: size})
	}
	return &Resource{bytes: data, blocks: blocks}, nil
}

// Raw returns the resource bytes.
func (r *Resource) Raw() []byte { return r.bytes }

// Blocks returns the block table in declaration order.
func (r *Resource) Blocks() []Block { return r.blocks }

// FindBlock returns the payload of the first block with the given kind.
func (r *Resource) FindBlock(kind [4]byte) ([]byte, bool) {
	meta, ok := r.FindBlockMeta(kind)
	if !ok {
		return nil, false
	}
	return r.blockBytes(meta)
}

// FindBlockMeta returns the first block with the given kind.
func (r *Resource) FindBlockMeta(kind [4]byte) (Block, bool) {
	for _, b := range r.blocks {
		if b.Kind == kind {
			return b, true
		}
	}
	return Block{}, false
}

// GetBlockByIndex returns the payload of the block at declaration index n.
func (r *Resource) GetBlockByIndex(n int) ([]byte, bool) {
	if n < 0 || n >= len(r.blocks) {
		return nil, false
	}
	return r.blockBytes(r.blocks[n])
}

func (r *Resource) blockBytes(b Block) ([]byte, bool) {
	start := int(b.Offset)
	end, ok := addU32(uint64(start), uint64(b.Size))
	if !ok || end > uint64(len(r.bytes)) {
		return nil, false
	}
	return r.bytes[start:end], true
}

// DataBlock returns the DATA block payload.
func (r *Resource) DataBlock() ([]byte, error) {
	b, ok := r.FindBlock(BlockTypeData)
	if !ok {
		return nil, ErrMissingDataBlock
	}
	return b, nil
}

// DataBlockMeta returns the DATA block table entry.
func (r *Resource) DataBlockMeta() (Block, error) {
	b, ok := r.FindBlockMeta(BlockTypeData)
	if !ok {
		return Block{}, ErrMissingDataBlock
	}
	return b, nil
}

// RebuildWithData replaces the single DATA block, preserving every other block.
func (r *Resource) RebuildWithDataPreserving(newData []byte) ([]byte, error) {
	count := 0
	index := -1
	for i, b := range r.blocks {
		if b.Kind == BlockTypeData {
			count++
			index = i
		}
	}
	if count != 1 {
		return nil, invalid("expected exactly one DATA block")
	}
	return r.RebuildWithBlockPreserving(index, newData)
}

// RebuildWithBlockPreserving is RebuildWithBlock plus the compatibility
// guarantees: no payload outside the block table, and every other block
// survives byte-for-byte.
func (r *Resource) RebuildWithBlockPreserving(index int, payload []byte) ([]byte, error) {
	if len(r.blocks) == 0 {
		return nil, invalid("resource has no blocks")
	}
	var end uint64
	for _, b := range r.blocks {
		e := uint64(b.Offset) + uint64(b.Size)
		if e > end {
			end = e
		}
	}
	if end > uint64(len(r.bytes)) {
		return nil, invalid("block out of range")
	}
	tail := r.bytes[int(end):]
	if len(tail) > 15 {
		return nil, invalid("unsupported payload outside resource blocks")
	}
	for _, b := range tail {
		if b != 0 {
			return nil, invalid("unsupported payload outside resource blocks")
		}
	}

	out, err := r.RebuildWithBlock(index, payload)
	if err != nil {
		return nil, err
	}
	rebuilt, err := Parse(out)
	if err != nil {
		return nil, err
	}
	if len(rebuilt.blocks) != len(r.blocks) {
		return nil, invalid("rebuild changed the block count")
	}
	for i := range r.blocks {
		if r.blocks[i].Kind != rebuilt.blocks[i].Kind {
			return nil, invalid("rebuild changed a block kind")
		}
		if i == index {
			continue
		}
		oldBytes, _ := r.GetBlockByIndex(i)
		newBytes, _ := rebuilt.GetBlockByIndex(i)
		if !bytes.Equal(oldBytes, newBytes) {
			return nil, invalid("rebuild changed another block")
		}
	}
	return out, nil
}

// RebuildWithData replaces the DATA block.
func (r *Resource) RebuildWithData(newData []byte) ([]byte, error) {
	index := -1
	for i, b := range r.blocks {
		if b.Kind == BlockTypeData {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, ErrMissingDataBlock
	}
	return r.RebuildWithBlock(index, newData)
}

// RebuildWithBlock replaces the block at declaration index with newPayload,
// keeping block order, count and every other payload byte-for-byte. Blocks are
// laid out in declaration order, each 16-byte aligned.
func (r *Resource) RebuildWithBlock(index int, newPayload []byte) ([]byte, error) {
	if index < 0 || index >= len(r.blocks) {
		return nil, invalid("block index out of range")
	}
	if err := kv3.ValidateRebuiltPayload(newPayload); err != nil {
		return nil, err
	}
	if len(r.bytes) < 8 {
		return nil, truncated(0, 8, len(r.bytes))
	}
	resourceVersion := binary.LittleEndian.Uint16(r.bytes[6:8])

	payloads := make([][]byte, len(r.blocks))
	for i, b := range r.blocks {
		if i == index {
			payloads[i] = newPayload
			continue
		}
		p, ok := r.blockBytes(b)
		if !ok {
			return nil, invalid("block out of range")
		}
		payloads[i] = p
	}

	tableLen := len(r.blocks) * blockTableEntryLen
	cursor := align16(headerLen + tableLen)
	absOffsets := make([]int, len(payloads))
	for i, p := range payloads {
		absOffsets[i] = cursor
		cursor = align16(cursor + len(p))
	}
	totalLen := cursor
	if uint64(totalLen) > math.MaxUint32 {
		return nil, invalid("resource too large")
	}

	out := make([]byte, totalLen)
	binary.LittleEndian.PutUint32(out[0:4], uint32(totalLen))
	binary.LittleEndian.PutUint16(out[4:6], knownHeaderVersion)
	binary.LittleEndian.PutUint16(out[6:8], resourceVersion)
	binary.LittleEndian.PutUint32(out[8:12], 8) // block_offset
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(r.blocks)))

	for i, b := range r.blocks {
		entry := headerLen + i*blockTableEntryLen
		copy(out[entry:entry+4], b.Kind[:])
		offFieldPos := entry + 4
		rel := absOffsets[i] - offFieldPos
		if rel < 0 || uint64(rel) > math.MaxUint32 {
			return nil, invalid("block rel offset overflow")
		}
		binary.LittleEndian.PutUint32(out[offFieldPos:offFieldPos+4], uint32(rel))
		if uint64(len(payloads[i])) > math.MaxUint32 {
			return nil, invalid("block too large")
		}
		binary.LittleEndian.PutUint32(out[offFieldPos+4:offFieldPos+8], uint32(len(payloads[i])))
	}
	for i, p := range payloads {
		copy(out[absOffsets[i]:], p)
	}
	return out, nil
}

// BuildResult is one block for Build.
type BuildResult struct {
	Kind    [4]byte
	Payload []byte
}

// Build constructs a resource container from block payloads, optionally
// appending tail bytes after the aligned block region.
func Build(blocks []BuildResult, tail []byte, resourceVersion uint16) ([]byte, error) {
	tableLen := len(blocks) * blockTableEntryLen
	cursor := align16(headerLen + tableLen)
	offsets := make([]int, len(blocks))
	for i, b := range blocks {
		offsets[i] = cursor
		cursor = align16(cursor + len(b.Payload))
	}
	tailStart := cursor
	totalLen := tailStart + len(tail)
	if uint64(totalLen) > math.MaxUint32 || uint64(len(blocks)) > math.MaxUint32 {
		return nil, invalid("resource too large")
	}

	out := make([]byte, totalLen)
	binary.LittleEndian.PutUint32(out[0:4], uint32(totalLen))
	binary.LittleEndian.PutUint16(out[4:6], knownHeaderVersion)
	binary.LittleEndian.PutUint16(out[6:8], resourceVersion)
	binary.LittleEndian.PutUint32(out[8:12], 8)
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(blocks)))

	for i, b := range blocks {
		entry := headerLen + i*blockTableEntryLen
		copy(out[entry:entry+4], b.Kind[:])
		offFieldPos := entry + 4
		rel := offsets[i] - offFieldPos
		if rel < 0 || uint64(rel) > math.MaxUint32 || uint64(len(b.Payload)) > math.MaxUint32 {
			return nil, invalid("block offset overflow")
		}
		binary.LittleEndian.PutUint32(out[offFieldPos:offFieldPos+4], uint32(rel))
		binary.LittleEndian.PutUint32(out[offFieldPos+4:offFieldPos+8], uint32(len(b.Payload)))
		copy(out[offsets[i]:], b.Payload)
	}
	copy(out[tailStart:], tail)
	return out, nil
}

func align16(n int) int { return (n + 15) &^ 15 }

func addU32(a, b uint64) (uint64, bool) {
	if a > math.MaxUint32 || b > math.MaxUint32 || a+b > math.MaxUint32 {
		return 0, false
	}
	return a + b, true
}

func truncated(offset, needed, had int) error {
	return fmt.Errorf("%w: offset %d needs %d bytes, has %d", ErrInvalid, offset, needed, had)
}

func readU16(data []byte, pos *int) (uint16, error) {
	if *pos+2 > len(data) {
		return 0, truncated(*pos, 2, len(data)-*pos)
	}
	v := binary.LittleEndian.Uint16(data[*pos:])
	*pos += 2
	return v, nil
}

func readU32(data []byte, pos *int) (uint32, error) {
	if *pos+4 > len(data) {
		return 0, truncated(*pos, 4, len(data)-*pos)
	}
	v := binary.LittleEndian.Uint32(data[*pos:])
	*pos += 4
	return v, nil
}
