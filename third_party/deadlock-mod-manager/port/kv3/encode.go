// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpkmanager/src/source2/kv3/writer.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa.
//
// The upstream file is MIT-licensed; it is distributed here as part of the
// GPL-3.0 MyLock work. See ../../NOTICE.md.

package kv3

import (
	"encoding/binary"
	"math"
)

const magicV4 uint32 = 0x4B563304

// Ser is the interned string table plus the typed output lanes.
type Ser struct {
	stringMap map[string]int32
	strings   []string
	b1        []byte
	b2        []byte
	b4        []byte
	b8        []byte
	types     []byte
	blobs     []byte
	blobLens  []int32
}

func newSer() *Ser {
	return &Ser{stringMap: make(map[string]int32)}
}

func (s *Ser) stringID(v string) int32 {
	if v == "" {
		return -1
	}
	if id, ok := s.stringMap[v]; ok {
		return id
	}
	id := int32(len(s.strings))
	s.strings = append(s.strings, v)
	s.stringMap[v] = id
	return id
}

func (s *Ser) writeType(t uint8) { s.types = append(s.types, t) }

// Encode encodes a Value tree to a binary KV3 v4 (uncompressed) DATA payload.
func Encode(value *Value, format Format) []byte {
	ctx := newSer()
	// First 4-byte slot is the string count, back-patched once interning is done.
	ctx.b4 = append(ctx.b4, 0, 0, 0, 0)
	writeValue(value, ctx)
	return finish(ctx, format)
}

// EncodePreserving encodes a Value tree using the wire encoding metadata.
func EncodePreserving(value *Value, encoding *Encoding, format Format) ([]byte, error) {
	ctx := newSer()
	ctx.b4 = append(ctx.b4, 0, 0, 0, 0)
	if err := writeValuePreserving(value, encoding, ctx); err != nil {
		return nil, err
	}
	return finish(ctx, format), nil
}

func finish(ctx *Ser, format Format) []byte {
	binary.LittleEndian.PutUint32(ctx.b4[0:4], uint32(len(ctx.strings)))

	body, countTypes := writeData(ctx)

	out := make([]byte, 0, 72+len(body)+len(ctx.blobs)+8)
	out = binary.LittleEndian.AppendUint32(out, magicV4)
	out = append(out, format[:]...)
	out = binary.LittleEndian.AppendUint32(out, 0) // compressionMethod = none
	out = binary.LittleEndian.AppendUint16(out, 0) // compressionDictionaryId
	out = binary.LittleEndian.AppendUint16(out, 0) // compressionFrameSize
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctx.b1)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctx.b4)/4))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctx.b8)/8))
	out = binary.LittleEndian.AppendUint32(out, countTypes)
	out = binary.LittleEndian.AppendUint16(out, 0) // countObjects (unused for v<5)
	out = binary.LittleEndian.AppendUint16(out, 0) // countArrays
	out = binary.LittleEndian.AppendUint32(out, 0) // sizeUncompressedTotal (patched)
	out = binary.LittleEndian.AppendUint32(out, 0) // sizeCompressedTotal (patched)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctx.blobLens)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctx.blobs)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctx.b2)/2))
	out = binary.LittleEndian.AppendUint32(out, 0) // sizeBlockCompressedSizesBytes

	// offset of sizeUncompressedTotal field
	const uncTotalOff = 48
	dataSize := uint32(len(body))
	binary.LittleEndian.PutUint32(out[uncTotalOff:uncTotalOff+4], dataSize)
	binary.LittleEndian.PutUint32(out[uncTotalOff+4:uncTotalOff+8], dataSize)

	out = append(out, body...)
	if len(ctx.blobLens) != 0 {
		out = append(out, ctx.blobs...)
		out = binary.LittleEndian.AppendUint32(out, kv3Trailer)
	}
	return out
}

// writeData lays out the typed lanes + strings + types into the data block
// body, with the same alignment discipline VRF uses. It returns the body and
// the value the header's countTypes field must hold for the v<5 reader (string
// bytes + type bytes).
func writeData(ctx *Ser) ([]byte, uint32) {
	body := make([]byte, 0, len(ctx.b1)+len(ctx.b2)+len(ctx.b4)+len(ctx.b8)+len(ctx.types)+64)
	body = append(body, ctx.b1...)
	offset := len(ctx.b1)

	if len(ctx.b2) != 0 {
		body, offset = alignPad(body, offset, 2)
		body = append(body, ctx.b2...)
		offset += len(ctx.b2)
	}
	if len(ctx.b4) != 0 {
		body, offset = alignPad(body, offset, 4)
		body = append(body, ctx.b4...)
		offset += len(ctx.b4)
	}
	// The reference pads to 8 even when the lane is empty.
	body, offset = alignPad(body, offset, 8)
	if len(ctx.b8) != 0 {
		body = append(body, ctx.b8...)
		offset += len(ctx.b8)
	}

	stringsStart := offset
	for _, s := range ctx.strings {
		body = append(body, s...)
		body = append(body, 0)
		offset += len(s) + 1
	}

	body = append(body, ctx.types...)
	offset += len(ctx.types)
	countTypes := uint32(offset - stringsStart)

	if len(ctx.blobLens) == 0 {
		body = binary.LittleEndian.AppendUint32(body, kv3Trailer)
	} else {
		for _, n := range ctx.blobLens {
			body = binary.LittleEndian.AppendUint32(body, uint32(n))
		}
		body = binary.LittleEndian.AppendUint32(body, kv3Trailer)
	}
	return body, countTypes
}

func writeValue(value *Value, ctx *Ser) {
	switch value.Kind {
	case KindBool:
		if value.Bool {
			ctx.writeType(nodeBooleanTrue)
		} else {
			ctx.writeType(nodeBooleanFalse)
		}
	case KindInt:
		switch value.Int {
		case 0:
			ctx.writeType(nodeInt64Zero)
		case 1:
			ctx.writeType(nodeInt64One)
		default:
			ctx.writeType(nodeInt64)
			ctx.b8 = binary.LittleEndian.AppendUint64(ctx.b8, uint64(value.Int))
		}
	case KindUInt:
		ctx.writeType(nodeUint64)
		ctx.b8 = binary.LittleEndian.AppendUint64(ctx.b8, value.UInt)
	case KindDouble:
		switch {
		case value.Double == 0:
			ctx.writeType(nodeDoubleZero)
		case value.Double == 1:
			ctx.writeType(nodeDoubleOne)
		default:
			ctx.writeType(nodeDouble)
			ctx.b8 = binary.LittleEndian.AppendUint64(ctx.b8, math.Float64bits(value.Double))
		}
	case KindNull:
		ctx.writeType(nodeNull)
	case KindString:
		ctx.writeType(nodeString)
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(ctx.stringID(value.String)))
	case KindBinary:
		ctx.writeType(nodeBinaryBlob)
		ctx.blobLens = append(ctx.blobLens, int32(len(value.Binary)))
		ctx.blobs = append(ctx.blobs, value.Binary...)
	case KindArray:
		ctx.writeType(nodeArray)
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(len(value.Array)))
		for i := range value.Array {
			writeValue(&value.Array[i], ctx)
		}
	case KindObject:
		ctx.writeType(nodeObject)
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(len(value.Object)))
		for i := range value.Object {
			id := ctx.stringID(value.Object[i].Key)
			ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(id))
			writeValue(&value.Object[i].Value, ctx)
		}
	}
}

func writeValuePreserving(value *Value, encoding *Encoding, ctx *Ser) error {
	datatype := preservedDatatype(value, encoding.DataType)
	ctx.writeEncodedType(datatype, encoding.Flag)
	return writeValueData(value, encoding, datatype, ctx)
}

func preservedDatatype(value *Value, preferred uint8) uint8 {
	switch value.Kind {
	case KindUInt:
		if (value.UInt == 0 && preferred == nodeUint32Zero) || (value.UInt == 1 && preferred == nodeUint32One) {
			return nodeUint32
		}
	case KindArray:
		switch preferred {
		case nodeArrayTypeAuxiliary:
			if len(value.Array) != 0 {
				return nodeArrayTyped
			}
		case nodeArrayTypeByteLength:
			if len(value.Array) > int(^uint8(0)) {
				return nodeArrayTyped
			}
		}
		if len(value.Array) == 0 &&
			(preferred == nodeArrayTyped || preferred == nodeArrayTypeByteLength || preferred == nodeArrayTypeAuxiliary) {
			return nodeArray
		}
	}
	return compatibleDatatype(value, preferred)
}

// EncodingPreserved reports whether actual matches the encoding a preserving
// encode of value with expected would produce.
func EncodingPreserved(value *Value, expected, actual *Encoding) bool {
	if actual.DataType != preservedDatatype(value, expected.DataType) || actual.Flag != expected.Flag {
		return false
	}
	switch value.Kind {
	case KindArray:
		if expected.Children.Kind != ChildrenArray || actual.Children.Kind != ChildrenArray {
			return false
		}
		if len(value.Array) != len(expected.Children.Array) || len(value.Array) != len(actual.Children.Array) {
			return false
		}
		for i := range value.Array {
			if !EncodingPreserved(&value.Array[i], &expected.Children.Array[i], &actual.Children.Array[i]) {
				return false
			}
		}
		return true
	case KindObject:
		if expected.Children.Kind != ChildrenObject || actual.Children.Kind != ChildrenObject {
			return false
		}
		if len(value.Object) != len(actual.Children.Object) {
			return false
		}
		for i := range value.Object {
			actualField := &actual.Children.Object[i]
			if value.Object[i].Key != actualField.Key {
				return false
			}
			expectedField, ok := expected.Get(value.Object[i].Key)
			if !ok {
				return false
			}
			if !EncodingPreserved(&value.Object[i].Value, expectedField, &actualField.Value) {
				return false
			}
		}
		return true
	}
	return expected.Children.Kind == ChildrenNone && actual.Children.Kind == ChildrenNone
}

// NormalizeNumericArrayEncoding rewrites typed-array item encodings so that a
// mixed numeric array becomes one lane. It is needed when an edit changes a
// compact value (0.0 -> 2.5) inside a typed array.
func NormalizeNumericArrayEncoding(value *Value, encoding *Encoding) error {
	switch value.Kind {
	case KindArray:
		if encoding.Children.Kind != ChildrenArray {
			return nil
		}
		items := encoding.Children.Array
		if len(value.Array) != len(items) {
			return invalid("array encoding length mismatch")
		}
		for i := range value.Array {
			if err := NormalizeNumericArrayEncoding(&value.Array[i], &items[i]); err != nil {
				return err
			}
		}
		switch encoding.DataType {
		case nodeArrayTyped, nodeArrayTypeByteLength, nodeArrayTypeAuxiliary:
		default:
			return nil
		}
		if len(value.Array) == 0 {
			return nil
		}
		first := preservedDatatype(&value.Array[0], items[0].DataType)
		allSame := true
		for i := range value.Array {
			if preservedDatatype(&value.Array[i], items[i].DataType) != first {
				allSame = false
				break
			}
		}
		if allSame {
			return nil
		}
		flag := items[0].Flag
		for i := range items {
			if items[i].Flag != flag {
				return invalid("typed numeric array has inconsistent flags")
			}
		}

		allDouble, allInt, allUInt := true, true, true
		anyFloat := false
		for i := range value.Array {
			switch value.Array[i].Kind {
			case KindDouble:
				allInt, allUInt = false, false
				if items[i].DataType == nodeFloat {
					anyFloat = true
				}
			case KindInt:
				allDouble, allUInt = false, false
			case KindUInt:
				allDouble, allInt = false, false
			}
		}
		var datatype uint8
		switch {
		case allDouble:
			// Compact DOUBLE_ZERO/ONE carry double semantics. FLOAT arrays keep
			// their width unless they actually mixed compiler schemas.
			if anyFloat {
				datatype = nodeFloat
			} else {
				datatype = nodeDouble
			}
		case allInt:
			datatype = nodeInt64
		case allUInt:
			datatype = nodeUint64
		default:
			return invalid("incompatible typed array is not numeric")
		}
		for i := range items {
			items[i].DataType = datatype
		}
	case KindObject:
		if encoding.Children.Kind != ChildrenObject {
			return nil
		}
		for i := range value.Object {
			field, ok := encoding.Get(value.Object[i].Key)
			if !ok {
				return invalid("field encoding missing")
			}
			if err := NormalizeNumericArrayEncoding(&value.Object[i].Value, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeValueData(value *Value, encoding *Encoding, datatype uint8, ctx *Ser) error {
	switch datatype {
	case nodeNull:
		if value.Kind != KindNull {
			return preservedMismatch()
		}
	case nodeBooleanTrue, nodeBooleanFalse:
		if value.Kind != KindBool {
			return preservedMismatch()
		}
	case nodeBoolean:
		if value.Kind != KindBool {
			return preservedMismatch()
		}
		if value.Bool {
			ctx.b1 = append(ctx.b1, 1)
		} else {
			ctx.b1 = append(ctx.b1, 0)
		}
	case nodeInt64Zero, nodeInt64One:
		if value.Kind != KindInt {
			return preservedMismatch()
		}
	case nodeUint32Zero, nodeUint32One:
		if value.Kind != KindUInt {
			return preservedMismatch()
		}
	case nodeInt8:
		if value.Kind != KindInt || value.Int < math.MinInt8 || value.Int > math.MaxInt8 {
			return preservedRange("preserved INT8 value is out of range")
		}
		ctx.b1 = append(ctx.b1, byte(int8(value.Int)))
	case nodeInt32AsByte:
		if value.Kind != KindInt || value.Int < 0 || value.Int > math.MaxUint8 {
			return preservedRange("preserved INT32_AS_BYTE value is out of range")
		}
		ctx.b1 = append(ctx.b1, byte(value.Int))
	case nodeInt16:
		if value.Kind != KindInt || value.Int < math.MinInt16 || value.Int > math.MaxInt16 {
			return preservedRange("preserved INT16 value is out of range")
		}
		ctx.b2 = binary.LittleEndian.AppendUint16(ctx.b2, uint16(int16(value.Int)))
	case nodeUint16:
		if value.Kind != KindUInt || value.UInt > math.MaxUint16 {
			return preservedRange("preserved UINT16 value is out of range")
		}
		ctx.b2 = binary.LittleEndian.AppendUint16(ctx.b2, uint16(value.UInt))
	case nodeInt32:
		if value.Kind != KindInt || value.Int < math.MinInt32 || value.Int > math.MaxInt32 {
			return preservedRange("preserved INT32 value is out of range")
		}
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(int32(value.Int)))
	case nodeUint32:
		if value.Kind != KindUInt || value.UInt > math.MaxUint32 {
			return preservedRange("preserved UINT32 value is out of range")
		}
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(value.UInt))
	case nodeFloat:
		if value.Kind != KindDouble {
			return preservedMismatch()
		}
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, math.Float32bits(float32(value.Double)))
	case nodeInt64:
		if value.Kind != KindInt {
			return preservedMismatch()
		}
		ctx.b8 = binary.LittleEndian.AppendUint64(ctx.b8, uint64(value.Int))
	case nodeUint64:
		if value.Kind != KindUInt {
			return preservedMismatch()
		}
		ctx.b8 = binary.LittleEndian.AppendUint64(ctx.b8, value.UInt)
	case nodeDoubleZero, nodeDoubleOne:
		if value.Kind != KindDouble {
			return preservedMismatch()
		}
	case nodeDouble:
		if value.Kind != KindDouble {
			return preservedMismatch()
		}
		ctx.b8 = binary.LittleEndian.AppendUint64(ctx.b8, math.Float64bits(value.Double))
	case nodeString:
		if value.Kind != KindString {
			return preservedMismatch()
		}
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(ctx.stringID(value.String)))
	case nodeBinaryBlob:
		if value.Kind != KindBinary {
			return preservedMismatch()
		}
		ctx.blobLens = append(ctx.blobLens, int32(len(value.Binary)))
		ctx.blobs = append(ctx.blobs, value.Binary...)
	case nodeArray:
		if value.Kind != KindArray {
			return preservedMismatch()
		}
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(len(value.Array)))
		encodings, err := arrayEncodings(encoding, len(value.Array))
		if err != nil {
			return err
		}
		for i := range value.Array {
			if err := writeValuePreserving(&value.Array[i], &encodings[i], ctx); err != nil {
				return err
			}
		}
	case nodeArrayTyped, nodeArrayTypeByteLength:
		if value.Kind != KindArray {
			return preservedMismatch()
		}
		if datatype == nodeArrayTypeByteLength {
			if len(value.Array) > int(^uint8(0)) {
				return preservedRange("byte-length array is too large")
			}
			ctx.b1 = append(ctx.b1, byte(len(value.Array)))
		} else {
			ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(len(value.Array)))
		}
		encodings, err := arrayEncodings(encoding, len(value.Array))
		if err != nil {
			return err
		}
		if len(encodings) == 0 {
			return invalid("typed array has no item encoding")
		}
		itemDatatype := preservedDatatype(&value.Array[0], encodings[0].DataType)
		ctx.writeEncodedType(itemDatatype, encodings[0].Flag)
		for i := range value.Array {
			actual := preservedDatatype(&value.Array[i], encodings[i].DataType)
			if actual != itemDatatype || encodings[i].Flag != encodings[0].Flag {
				return invalid("typed array contains incompatible item encodings")
			}
			if err := writeValueData(&value.Array[i], &encodings[i], actual, ctx); err != nil {
				return err
			}
		}
	case nodeObject:
		if value.Kind != KindObject {
			return preservedMismatch()
		}
		ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(len(value.Object)))
		if encoding.Children.Kind != ChildrenObject {
			return invalid("object encoding metadata is missing")
		}
		for i := range value.Object {
			id := ctx.stringID(value.Object[i].Key)
			ctx.b4 = binary.LittleEndian.AppendUint32(ctx.b4, uint32(id))
			child, ok := encoding.Get(value.Object[i].Key)
			if !ok {
				return invalid("object field encoding metadata is missing")
			}
			if err := writeValuePreserving(&value.Object[i].Value, child, ctx); err != nil {
				return err
			}
		}
	default:
		return preservedMismatch()
	}
	return nil
}

func (s *Ser) writeEncodedType(datatype, flag uint8) {
	if flag == 0 {
		s.types = append(s.types, datatype)
	} else {
		s.types = append(s.types, datatype|0x80, flag)
	}
}

func arrayEncodings(encoding *Encoding, expected int) ([]Encoding, error) {
	if encoding.Children.Kind != ChildrenArray {
		return nil, invalid("array encoding metadata is missing")
	}
	if len(encoding.Children.Array) != expected {
		return nil, invalid("array encoding metadata length mismatch")
	}
	return encoding.Children.Array, nil
}

func compatibleDatatype(value *Value, preferred uint8) uint8 {
	switch value.Kind {
	case KindNull:
		return nodeNull
	case KindBool:
		switch preferred {
		case nodeBoolean:
			return nodeBoolean
		case nodeBooleanTrue:
			if value.Bool {
				return nodeBooleanTrue
			}
		case nodeBooleanFalse:
			if !value.Bool {
				return nodeBooleanFalse
			}
		}
		if value.Bool {
			return nodeBooleanTrue
		}
		return nodeBooleanFalse
	case KindInt:
		switch preferred {
		case nodeInt64Zero:
			if value.Int == 0 {
				return nodeInt64Zero
			}
		case nodeInt64One:
			if value.Int == 1 {
				return nodeInt64One
			}
		case nodeInt8:
			if value.Int >= math.MinInt8 && value.Int <= math.MaxInt8 {
				return nodeInt8
			}
		case nodeInt32AsByte:
			if value.Int >= 0 && value.Int <= math.MaxUint8 {
				return nodeInt32AsByte
			}
		case nodeInt16:
			if value.Int >= math.MinInt16 && value.Int <= math.MaxInt16 {
				return nodeInt16
			}
		case nodeInt32:
			if value.Int >= math.MinInt32 && value.Int <= math.MaxInt32 {
				return nodeInt32
			}
		case nodeInt64:
			return nodeInt64
		}
		return nodeInt64
	case KindUInt:
		switch preferred {
		case nodeUint32Zero:
			if value.UInt == 0 {
				return nodeUint32Zero
			}
		case nodeUint32One:
			if value.UInt == 1 {
				return nodeUint32One
			}
		case nodeUint16:
			if value.UInt <= math.MaxUint16 {
				return nodeUint16
			}
		case nodeUint32:
			if value.UInt <= math.MaxUint32 {
				return nodeUint32
			}
		case nodeUint64:
			return nodeUint64
		}
		return nodeUint64
	case KindDouble:
		switch preferred {
		case nodeDoubleZero:
			if value.Double == 0 {
				return nodeDoubleZero
			}
		case nodeDoubleOne:
			if value.Double == 1 {
				return nodeDoubleOne
			}
		case nodeFloat:
			return nodeFloat
		case nodeDouble:
			return nodeDouble
		}
		return nodeDouble
	case KindString:
		return nodeString
	case KindBinary:
		return nodeBinaryBlob
	case KindArray:
		switch preferred {
		case nodeArray, nodeArrayTyped, nodeArrayTypeByteLength, nodeArrayTypeAuxiliary:
			return preferred
		}
		return nodeArray
	case KindObject:
		return nodeObject
	}
	return nodeNull
}

// ValidateRebuiltPayload rejects a v4 payload that still contains node types a
// v4 reader cannot consume. Non-KV3 payloads (opaque legacy blocks) pass.
func ValidateRebuiltPayload(data []byte) error {
	if len(data) >= 4 && binary.LittleEndian.Uint32(data[:4]) == magicV4 {
		stats, err := EncodingStatsOf(data)
		if err != nil {
			return err
		}
		if stats.UnsupportedV4Types != 0 {
			return invalid("rebuilt v4 payload contains unsupported node types")
		}
	}
	return nil
}

func alignPad(body []byte, offset, alignment int) ([]byte, int) {
	a := alignment - 1
	aligned := (offset + a) &^ a
	body = append(body, make([]byte, aligned-offset)...)
	return body, aligned
}

func preservedMismatch() error {
	return invalid("value does not match preserved KV3 encoding")
}

func preservedRange(msg string) error {
	return invalid("%s", msg)
}
