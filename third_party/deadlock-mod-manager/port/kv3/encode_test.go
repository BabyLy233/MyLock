// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpkmanager/src/source2/kv3/writer.rs (tests) at
// 41afe0e706726f7c5882fb589f996b84362114fa.

package kv3

import "testing"

func leaf(datatype, flag uint8) Encoding {
	return Encoding{DataType: datatype, Flag: flag}
}

func TestCompactIntegersUseV4TagsAndPreserveFlags(t *testing.T) {
	value := Object(
		Pair{"zero", UInt(0)},
		Pair{"one", UInt(1)},
		Pair{"signed", Int(-2)},
		Pair{"edited", Double(2.5)},
	)
	encoding := Encoding{
		DataType: nodeObject,
		Children: EncodingChildren{Kind: ChildrenObject, Object: []EncodingField{
			{"zero", leaf(nodeUint32Zero, 1)},
			{"one", leaf(nodeUint32One, 0)},
			{"signed", leaf(nodeInt8, 0)},
			{"edited", leaf(nodeDouble, 0)},
		}},
	}
	encoded, err := EncodePreserving(&value, &encoding, Format{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, actual, err := DecodePreserving(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(&decoded, &value) {
		t.Fatalf("decoded value = %#v, want %#v", decoded, value)
	}
	zero, _ := actual.Get("zero")
	one, _ := actual.Get("one")
	if zero.DataType != nodeUint32 || one.DataType != nodeUint32 {
		t.Fatalf("compact unsigned tags = %d/%d, want UINT32", zero.DataType, one.DataType)
	}
	if !EncodingPreserved(&value, &encoding, &actual) {
		t.Fatal("encoding not preserved")
	}
}

func TestCompactUnsignedTypedArraysUseUint32Lanes(t *testing.T) {
	for _, datatype := range []uint8{nodeArrayTyped, nodeArrayTypeAuxiliary} {
		value := Array(UInt(0), UInt(1))
		encoding := Encoding{
			DataType: datatype,
			Children: EncodingChildren{Kind: ChildrenArray, Array: []Encoding{
				leaf(nodeUint32Zero, 4),
				leaf(nodeUint32One, 4),
			}},
		}
		encoded, err := EncodePreserving(&value, &encoding, Format{})
		if err != nil {
			t.Fatalf("datatype %d: %v", datatype, err)
		}
		decoded, actual, err := DecodePreserving(encoded)
		if err != nil {
			t.Fatalf("datatype %d: %v", datatype, err)
		}
		if !Equal(&decoded, &value) {
			t.Fatalf("datatype %d: decoded %#v, want %#v", datatype, decoded, value)
		}
		if !EncodingPreserved(&value, &encoding, &actual) {
			t.Fatalf("datatype %d: encoding not preserved", datatype)
		}
		items, _ := actual.AsArray()
		for i := range items {
			if items[i].DataType != nodeUint32 || items[i].Flag != 4 {
				t.Fatalf("datatype %d item %d = (%d,%d), want (UINT32,4)", datatype, i, items[i].DataType, items[i].Flag)
			}
		}
	}
}

func TestEditedCompactTypedNumbersPromote(t *testing.T) {
	value := Array(Double(0), Double(2.5))
	encoding := Encoding{
		DataType: nodeArrayTyped,
		Children: EncodingChildren{Kind: ChildrenArray, Array: []Encoding{
			leaf(nodeDoubleZero, 4),
			leaf(nodeDoubleZero, 4),
		}},
	}
	if _, err := EncodePreserving(&value, &encoding, Format{}); err == nil {
		t.Fatal("mixed compact typed array encoded without normalizing")
	}
	if err := NormalizeNumericArrayEncoding(&value, &encoding); err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePreserving(&value, &encoding, Format{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, metadata, err := DecodePreserving(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(&decoded, &value) {
		t.Fatalf("decoded %#v, want %#v", decoded, value)
	}
	if !EncodingPreserved(&value, &encoding, &metadata) {
		t.Fatal("encoding not preserved after normalize")
	}
	items, _ := metadata.AsArray()
	for i := range items {
		if items[i].Flag != 4 || items[i].DataType != nodeDouble {
			t.Fatalf("item %d = (%d,%d), want (DOUBLE,4)", i, items[i].DataType, items[i].Flag)
		}
	}

	bad := Encoding{
		DataType: nodeArrayTyped,
		Children: EncodingChildren{Kind: ChildrenArray, Array: []Encoding{
			leaf(nodeDoubleZero, 4),
			leaf(nodeDouble, 0),
		}},
	}
	if err := NormalizeNumericArrayEncoding(&value, &bad); err == nil {
		t.Fatal("inconsistent flags normalized without error")
	}
}

func TestPreservingEncoderHandlesEmptyAuxiliaryArrays(t *testing.T) {
	for _, datatype := range []uint8{nodeArrayTyped, nodeArrayTypeByteLength, nodeArrayTypeAuxiliary} {
		value := Object(Pair{"weights", Array()})
		encoding := Encoding{
			DataType: nodeObject,
			Children: EncodingChildren{Kind: ChildrenObject, Object: []EncodingField{
				{"weights", Encoding{
					DataType: datatype,
					Children: EncodingChildren{Kind: ChildrenArray},
				}},
			}},
		}
		encoded, err := EncodePreserving(&value, &encoding, Format{})
		if err != nil {
			t.Fatalf("datatype %d: %v", datatype, err)
		}
		decoded, err := Decode(encoded)
		if err != nil {
			t.Fatalf("datatype %d: %v", datatype, err)
		}
		if !Equal(&decoded, &value) {
			t.Fatalf("datatype %d: decoded %#v, want %#v", datatype, decoded, value)
		}
		stats, err := EncodingStatsOf(encoded)
		if err != nil {
			t.Fatalf("datatype %d: %v", datatype, err)
		}
		if stats.TypedArrays != 0 {
			t.Fatalf("datatype %d: typed arrays = %d, want 0", datatype, stats.TypedArrays)
		}
	}
}

func TestPreservingEncoderKeepsFlagsTypedArraysAndWidths(t *testing.T) {
	value := Object(
		Pair{"resource", String("hero")},
		Pair{"values", Array(Int(2), Int(3), Int(4))},
	)
	encoding := Encoding{
		DataType: nodeObject,
		Children: EncodingChildren{Kind: ChildrenObject, Object: []EncodingField{
			{"resource", leaf(nodeString, 1)},
			{"values", Encoding{
				DataType: nodeArrayTypeAuxiliary,
				Children: EncodingChildren{Kind: ChildrenArray, Array: []Encoding{
					leaf(nodeInt16, 0),
					leaf(nodeInt16, 0),
					leaf(nodeInt16, 0),
				}},
			}},
		}},
	}
	encoded, err := EncodePreserving(&value, &encoding, Format{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(&decoded, &value) {
		t.Fatalf("decoded %#v, want %#v", decoded, value)
	}
	stats, err := EncodingStatsOf(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Version != 4 {
		t.Fatalf("version = %d, want 4", stats.Version)
	}
	if stats.FlaggedNodes != 1 {
		t.Fatalf("flagged nodes = %d, want 1", stats.FlaggedNodes)
	}
	if stats.TypedArrays != 1 {
		t.Fatalf("typed arrays = %d, want 1", stats.TypedArrays)
	}
	if stats.NarrowNumbers != 1 {
		t.Fatalf("narrow numbers = %d, want 1", stats.NarrowNumbers)
	}
	_, actual, err := DecodePreserving(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !EncodingPreserved(&value, &encoding, &actual) {
		t.Fatal("encoding not preserved")
	}
	widened, err := Decode(Encode(&value, Format{}))
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(&widened, &value) {
		t.Fatalf("widened decode %#v, want %#v", widened, value)
	}
	_, widenedEncoding, err := DecodePreserving(Encode(&value, Format{}))
	if err != nil {
		t.Fatal(err)
	}
	if EncodingPreserved(&value, &encoding, &widenedEncoding) {
		t.Fatal("widened encoding reported as preserved")
	}
	if resource, ok := actual.Get("resource"); ok {
		resource.Flag = 0
	}
	if EncodingPreserved(&value, &encoding, &actual) {
		t.Fatal("changed flag reported as preserved")
	}
}

func TestBinaryBlobRoundTrip(t *testing.T) {
	value := Object(
		Pair{"mesh_data", Binary([]byte{1, 2, 3, 4})},
		Pair{"name", String("blob")},
	)
	encoded := Encode(&value, Format{})
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(&decoded, &value) {
		t.Fatalf("decoded %#v, want %#v", decoded, value)
	}
}

func TestRejectsBadMagicAndTrailer(t *testing.T) {
	if _, err := Decode([]byte("not a kv3 payload")); err == nil {
		t.Fatal("bad magic decoded without error")
	}
	goodValue := Object(Pair{"a", Int(1)})
	good := Encode(&goodValue, Format{})
	good[len(good)-1] ^= 0xFF
	if _, err := Decode(good); err == nil {
		t.Fatal("bad trailer decoded without error")
	}
}
