// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpkmanager/src/source2/kv3/types.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa.
//
// The upstream file is MIT-licensed; it is distributed here as part of the
// GPL-3.0 MyLock work. See ../../NOTICE.md.

// Package kv3 decodes and encodes binary Source 2 KeyValues3 payloads.
package kv3

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Format is the 16-byte KV3 format GUID from the payload header. It names the
// schema (soundevents, material, generic, ...), and the engine keys on it, so a
// faithful rebuild must pass the original bytes through.
type Format [16]byte

// FormatFromPayload reads the format GUID from a binary KV3 payload.
func FormatFromPayload(data []byte) (Format, error) {
	if len(data) < 20 {
		return Format{}, truncated(0, 20, len(data))
	}
	var f Format
	copy(f[:], data[4:20])
	return f, nil
}

var (
	// ErrTruncated reports a read past the end of a buffer.
	ErrTruncated = errors.New("kv3: truncated")
	// ErrUnsupported reports a magic, version or compression method this codec
	// does not understand.
	ErrUnsupported = errors.New("kv3: unsupported")
	// ErrCompression reports a compression method this codec does not understand.
	ErrCompression = errors.New("kv3: unsupported compression")
	// ErrNodeType reports a node type this codec does not understand.
	ErrNodeType = errors.New("kv3: unsupported node type")
	// ErrInvalid reports malformed structure other than truncation.
	ErrInvalid = errors.New("kv3: invalid data")
)

func truncated(offset, needed, had int) error {
	return fmt.Errorf("%w: offset %d needs %d bytes, has %d", ErrTruncated, offset, needed, had)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Kind identifies which field of Value is set.
type Kind uint8

const (
	KindNull Kind = iota
	KindBool
	KindInt
	KindUInt
	KindDouble
	KindString
	KindBinary
	KindArray
	KindObject
)

// Pair is one insertion-ordered key/value pair of an Object.
type Pair struct {
	Key   string
	Value Value
}

// Value is a decoded KV3 value tree. Objects keep insertion order because
// Source 2 relies on key order and a faithful round-trip must not reshuffle it.
type Value struct {
	Kind Kind

	Bool   bool
	Int    int64
	UInt   uint64
	Double float64
	String string
	Binary []byte
	Array  []Value
	Object []Pair
}

// Null returns a null value.
func Null() Value { return Value{Kind: KindNull} }

// Bool returns a boolean value.
func Bool(b bool) Value { return Value{Kind: KindBool, Bool: b} }

// Int returns a signed integer value.
func Int(i int64) Value { return Value{Kind: KindInt, Int: i} }

// UInt returns an unsigned integer value.
func UInt(u uint64) Value { return Value{Kind: KindUInt, UInt: u} }

// Double returns a floating-point value.
func Double(d float64) Value { return Value{Kind: KindDouble, Double: d} }

// String returns a string value.
func String(s string) Value { return Value{Kind: KindString, String: s} }

// Binary returns a binary blob value.
func Binary(b []byte) Value { return Value{Kind: KindBinary, Binary: b} }

// Array returns an array value.
func Array(items ...Value) Value { return Value{Kind: KindArray, Array: items} }

// Object returns an object value.
func Object(pairs ...Pair) Value { return Value{Kind: KindObject, Object: pairs} }

// Get looks up a child by key, returning the first match.
func (v *Value) Get(key string) (*Value, bool) {
	if v.Kind != KindObject {
		return nil, false
	}
	for i := range v.Object {
		if v.Object[i].Key == key {
			return &v.Object[i].Value, true
		}
	}
	return nil, false
}

// AsString returns the string value.
func (v *Value) AsString() (string, bool) {
	if v.Kind != KindString {
		return "", false
	}
	return v.String, true
}

// AsInt returns the value as a signed integer when it fits.
func (v *Value) AsInt() (int64, bool) {
	switch v.Kind {
	case KindInt:
		return v.Int, true
	case KindUInt:
		if v.UInt <= math.MaxInt64 {
			return int64(v.UInt), true
		}
	}
	return 0, false
}

// AsUInt returns the value as an unsigned integer when it fits.
func (v *Value) AsUInt() (uint64, bool) {
	switch v.Kind {
	case KindUInt:
		return v.UInt, true
	case KindInt:
		if v.Int >= 0 {
			return uint64(v.Int), true
		}
	}
	return 0, false
}

// AsBool returns the boolean value.
func (v *Value) AsBool() (bool, bool) {
	if v.Kind != KindBool {
		return false, false
	}
	return v.Bool, true
}

// AsFloat returns the value as a float64, widening integers.
func (v *Value) AsFloat() (float64, bool) {
	switch v.Kind {
	case KindDouble:
		return v.Double, true
	case KindInt:
		return float64(v.Int), true
	case KindUInt:
		return float64(v.UInt), true
	}
	return 0, false
}

// AsArray returns the array items.
func (v *Value) AsArray() ([]Value, bool) {
	if v.Kind != KindArray {
		return nil, false
	}
	return v.Array, true
}

// AsObject returns the object pairs.
func (v *Value) AsObject() ([]Pair, bool) {
	if v.Kind != KindObject {
		return nil, false
	}
	return v.Object, true
}

// ForEachString calls f for every string in the tree, depth first.
func (v *Value) ForEachString(f func(*string)) {
	switch v.Kind {
	case KindString:
		f(&v.String)
	case KindArray:
		for i := range v.Array {
			v.Array[i].ForEachString(f)
		}
	case KindObject:
		for i := range v.Object {
			v.Object[i].Value.ForEachString(f)
		}
	}
}

// Equal reports whether two value trees are structurally equal. Binary blobs
// and strings compare by content; doubles compare with == (NaN is unequal, as
// in Rust's PartialEq).
func Equal(a, b *Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindNull:
		return true
	case KindBool:
		return a.Bool == b.Bool
	case KindInt:
		return a.Int == b.Int
	case KindUInt:
		return a.UInt == b.UInt
	case KindDouble:
		return a.Double == b.Double
	case KindString:
		return a.String == b.String
	case KindBinary:
		if len(a.Binary) != len(b.Binary) {
			return false
		}
		for i := range a.Binary {
			if a.Binary[i] != b.Binary[i] {
				return false
			}
		}
		return true
	case KindArray:
		if len(a.Array) != len(b.Array) {
			return false
		}
		for i := range a.Array {
			if !Equal(&a.Array[i], &b.Array[i]) {
				return false
			}
		}
		return true
	case KindObject:
		if len(a.Object) != len(b.Object) {
			return false
		}
		for i := range a.Object {
			if a.Object[i].Key != b.Object[i].Key || !Equal(&a.Object[i].Value, &b.Object[i].Value) {
				return false
			}
		}
		return true
	}
	return false
}

// Seg addresses one member of a Value. Field paths in a table row are made of
// key segments; array elements add index segments. It mirrors the original
// kv3::Seg used by the patch and data-compatibility code.
type Seg struct {
	Kind  SegKind
	Key   string
	Index uint64
}

// SegKind says whether a segment descends into an object member or an array
// element.
type SegKind uint8

const (
	// SegKey descends into the named object member.
	SegKey SegKind = iota
	// SegIndex descends into the numbered array element.
	SegIndex
)

// KeySeg returns an object-member path segment.
func KeySeg(key string) Seg { return Seg{Key: key} }

// IndexSeg returns an array-element path segment.
func IndexSeg(index uint64) Seg { return Seg{Kind: SegIndex, Index: index} }

// Encoding is the wire encoding metadata for one decoded Value node.
type Encoding struct {
	DataType uint8
	Flag     uint8
	Children EncodingChildren
}

// EncodingChildren holds the child encodings of an array or object.
type EncodingChildren struct {
	Kind   EncodingChildKind
	Array  []Encoding
	Object []EncodingField
}

// EncodingChildKind selects which EncodingChildren field is populated.
type EncodingChildKind uint8

const (
	ChildrenNone EncodingChildKind = iota
	ChildrenArray
	ChildrenObject
)

// EncodingField is one key/encoding pair.
type EncodingField struct {
	Key   string
	Value Encoding
}

// IsFloat reports whether this value occupies an IEEE 754 single-precision lane.
func (e *Encoding) IsFloat() bool { return e.DataType == nodeFloat }

// Get returns the encoding of an object field.
func (e *Encoding) Get(key string) (*Encoding, bool) {
	if e.Children.Kind != ChildrenObject {
		return nil, false
	}
	for i := range e.Children.Object {
		if e.Children.Object[i].Key == key {
			return &e.Children.Object[i].Value, true
		}
	}
	return nil, false
}

// AsArray returns the item encodings of an array.
func (e *Encoding) AsArray() ([]Encoding, bool) {
	if e.Children.Kind != ChildrenArray {
		return nil, false
	}
	return e.Children.Array, true
}

// AsObject returns the field encodings of an object.
func (e *Encoding) AsObject() ([]EncodingField, bool) {
	if e.Children.Kind != ChildrenObject {
		return nil, false
	}
	return e.Children.Object, true
}

// EncodingStats summarizes what the decoder saw. It is used to check that a
// rebuild preserved the shapes that matter.
type EncodingStats struct {
	Version            uint32
	Nodes              int
	FlaggedNodes       int
	TypedArrays        int
	AuxiliaryArrays    int
	NarrowNumbers      int
	UnsupportedV4Types int
}

// Numeric tags for KV3 binary node types (VRF KV3BinaryNodeType). Shared by the
// reader and writer so the two never drift.
const (
	nodeNull                uint8 = 1
	nodeBoolean             uint8 = 2
	nodeInt64               uint8 = 3
	nodeUint64              uint8 = 4
	nodeDouble              uint8 = 5
	nodeString              uint8 = 6
	nodeBinaryBlob          uint8 = 7
	nodeArray               uint8 = 8
	nodeObject              uint8 = 9
	nodeArrayTyped          uint8 = 10
	nodeInt32               uint8 = 11
	nodeUint32              uint8 = 12
	nodeBooleanTrue         uint8 = 13
	nodeBooleanFalse        uint8 = 14
	nodeInt64Zero           uint8 = 15
	nodeInt64One            uint8 = 16
	nodeDoubleZero          uint8 = 17
	nodeDoubleOne           uint8 = 18
	nodeFloat               uint8 = 19
	nodeInt16               uint8 = 20
	nodeUint16              uint8 = 21
	nodeInt8                uint8 = 22
	nodeInt32AsByte         uint8 = 23
	nodeArrayTypeByteLength uint8 = 24
	nodeArrayTypeAuxiliary  uint8 = 25
	nodeUint32Zero          uint8 = 26
	nodeUint32One           uint8 = 27
)

// lossyUTF8 mirrors Rust's String::from_utf8_lossy.
func lossyUTF8(b []byte) string {
	return strings.ToValidUTF8(string(b), "�")
}
