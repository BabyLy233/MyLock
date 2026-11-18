// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (compiled-data
// helpers) and localization_overlay/compiled_resource.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package compiled holds the KV3 value and encoding operations the game-data
// compatibility path uses to compare, patch and rebuild compiled tables.
package compiled

import (
	"fmt"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

// Decode reads the DATA block of a compiled resource and preserves its KV3
// encoding metadata.
func Decode(data []byte, source string) (kv3.Format, kv3.Value, kv3.Encoding, error) {
	parsed, err := resource.Parse(data)
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, fmt.Errorf("Failed to parse %s: %w", source, err)
	}
	block, err := parsed.DataBlock()
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, fmt.Errorf("Failed to read %s DATA block: %w", source, err)
	}
	format, err := kv3.FormatFromPayload(block)
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, fmt.Errorf("Failed to read %s KV3 format: %w", source, err)
	}
	value, encoding, err := kv3.DecodePreserving(block)
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, fmt.Errorf("Failed to decode %s: %w", source, err)
	}
	return format, value, encoding, nil
}

// RuntimeValuesEqual compares value trees the way the engine does: object
// field names are case-insensitive, `_editor` members are ignored, and array
// order matters.
func RuntimeValuesEqual(left, right *kv3.Value) bool {
	if left.Kind != right.Kind {
		return false
	}
	switch left.Kind {
	case kv3.KindObject:
		li, ri := 0, 0
		for {
			for li < len(left.Object) && strings.EqualFold(left.Object[li].Key, "_editor") {
				li++
			}
			for ri < len(right.Object) && strings.EqualFold(right.Object[ri].Key, "_editor") {
				ri++
			}
			if li >= len(left.Object) || ri >= len(right.Object) {
				return li >= len(left.Object) && ri >= len(right.Object)
			}
			if !strings.EqualFold(left.Object[li].Key, right.Object[ri].Key) ||
				!RuntimeValuesEqual(&left.Object[li].Value, &right.Object[ri].Value) {
				return false
			}
			li++
			ri++
		}
	case kv3.KindArray:
		if len(left.Array) != len(right.Array) {
			return false
		}
		for index := range left.Array {
			if !RuntimeValuesEqual(&left.Array[index], &right.Array[index]) {
				return false
			}
		}
		return true
	default:
		return kv3.Equal(left, right)
	}
}

// ObjectGet returns a field by a case-insensitive name.
func ObjectGet(rows []kv3.Pair, key string) (*kv3.Value, bool) {
	for index := range rows {
		if strings.EqualFold(rows[index].Key, key) {
			return &rows[index].Value, true
		}
	}
	return nil, false
}

// ObjectGetMut returns a mutable field by a case-insensitive name.
func ObjectGetMut(rows []kv3.Pair, key string) (*kv3.Value, bool) {
	for index := range rows {
		if strings.EqualFold(rows[index].Key, key) {
			return &rows[index].Value, true
		}
	}
	return nil, false
}

// ObjectSet replaces a field by a case-insensitive name, keeping the authored
// key when the field exists and appending it otherwise. The object must be a
// KindObject value.
func ObjectSet(object *kv3.Value, key string, value kv3.Value) error {
	if object.Kind != kv3.KindObject {
		return fmt.Errorf("Cannot set compiled-data field %s: parent is not an object", key)
	}
	for index := range object.Object {
		if strings.EqualFold(object.Object[index].Key, key) {
			object.Object[index].Value = value
			return nil
		}
	}
	object.Object = append(object.Object, kv3.Pair{Key: key, Value: value})
	return nil
}

// EncodingGet returns an object field encoding by a case-insensitive name.
func EncodingGet(fields []kv3.EncodingField, key string) (*kv3.Encoding, bool) {
	for index := range fields {
		if strings.EqualFold(fields[index].Key, key) {
			return &fields[index].Value, true
		}
	}
	return nil, false
}

// EncodingGetMut returns a mutable object field encoding by a case-insensitive
// name.
func EncodingGetMut(fields []kv3.EncodingField, key string) (*kv3.Encoding, bool) {
	for index := range fields {
		if strings.EqualFold(fields[index].Key, key) {
			return &fields[index].Value, true
		}
	}
	return nil, false
}

// EncodingSet replaces an object field encoding, keeping its authored key.
func EncodingSet(encoding *kv3.Encoding, key string, value kv3.Encoding) error {
	if encoding.Children.Kind != kv3.ChildrenObject {
		return fmt.Errorf("Cannot set compiled-data encoding for field %s: parent is not an object", key)
	}
	for index := range encoding.Children.Object {
		if strings.EqualFold(encoding.Children.Object[index].Key, key) {
			encoding.Children.Object[index].Value = value
			return nil
		}
	}
	encoding.Children.Object = append(encoding.Children.Object, kv3.EncodingField{Key: key, Value: value})
	return nil
}

// ValueAtPath follows a path through objects and arrays. Object member lookup
// is case-insensitive.
func ValueAtPath(root *kv3.Value, path []kv3.Seg) (*kv3.Value, bool) {
	value := root
	for _, segment := range path {
		if segment.Kind == kv3.SegIndex {
			if value.Kind != kv3.KindArray || segment.Index >= uint64(len(value.Array)) {
				return nil, false
			}
			value = &value.Array[segment.Index]
			continue
		}
		if value.Kind != kv3.KindObject {
			return nil, false
		}
		child, ok := ObjectGetMut(value.Object, segment.Key)
		if !ok {
			return nil, false
		}
		value = child
	}
	return value, true
}

// EncodingAtPath follows a path through object and array encodings.
func EncodingAtPath(root *kv3.Encoding, path []kv3.Seg) (*kv3.Encoding, bool) {
	encoding := root
	for _, segment := range path {
		if segment.Kind == kv3.SegIndex {
			if encoding.Children.Kind != kv3.ChildrenArray ||
				segment.Index >= uint64(len(encoding.Children.Array)) {
				return nil, false
			}
			encoding = &encoding.Children.Array[segment.Index]
			continue
		}
		if encoding.Children.Kind != kv3.ChildrenObject {
			return nil, false
		}
		child, ok := EncodingGetMut(encoding.Children.Object, segment.Key)
		if !ok {
			return nil, false
		}
		encoding = child
	}
	return encoding, true
}

// SetValueAtPath replaces the value at a path. A missing intermediate member
// is an error; only the final member may be appended.
func SetValueAtPath(root *kv3.Value, path []kv3.Seg, value kv3.Value) error {
	if len(path) == 0 {
		*root = value
		return nil
	}
	segment := path[0]
	remaining := path[1:]
	if segment.Kind == kv3.SegIndex {
		if root.Kind != kv3.KindArray {
			return fmt.Errorf("Cannot apply compiled-data index %d: parent is not an array", segment.Index)
		}
		if segment.Index >= uint64(len(root.Array)) {
			return fmt.Errorf("Cannot apply compiled-data index %d: array has %d items", segment.Index, len(root.Array))
		}
		item := &root.Array[segment.Index]
		if len(remaining) == 0 {
			*item = value
			return nil
		}
		return SetValueAtPath(item, remaining, value)
	}
	if root.Kind != kv3.KindObject {
		return fmt.Errorf("Cannot apply compiled-data field %s: parent is not an object", segment.Key)
	}
	if len(remaining) == 0 {
		return ObjectSet(root, segment.Key, value)
	}
	field, ok := ObjectGetMut(root.Object, segment.Key)
	if !ok {
		return fmt.Errorf("Cannot apply compiled-data field %s: parent field is missing", segment.Key)
	}
	return SetValueAtPath(field, remaining, value)
}

// SetEncodingAtPath replaces the encoding at a path.
func SetEncodingAtPath(root *kv3.Encoding, path []kv3.Seg, encoding kv3.Encoding) error {
	if len(path) == 0 {
		*root = encoding
		return nil
	}
	segment := path[0]
	remaining := path[1:]
	if segment.Kind == kv3.SegIndex {
		if root.Children.Kind != kv3.ChildrenArray {
			return fmt.Errorf("Cannot apply compiled-data encoding for index %d: parent is not an array", segment.Index)
		}
		if segment.Index >= uint64(len(root.Children.Array)) {
			return fmt.Errorf("Cannot apply compiled-data encoding for index %d: array has %d items", segment.Index, len(root.Children.Array))
		}
		item := &root.Children.Array[segment.Index]
		if len(remaining) == 0 {
			*item = encoding
			return nil
		}
		return SetEncodingAtPath(item, remaining, encoding)
	}
	if root.Children.Kind != kv3.ChildrenObject {
		return fmt.Errorf("Cannot apply compiled-data encoding for field %s: parent is not an object", segment.Key)
	}
	if len(remaining) == 0 {
		return EncodingSet(root, segment.Key, encoding)
	}
	field, ok := EncodingGetMut(root.Children.Object, segment.Key)
	if !ok {
		return fmt.Errorf("Cannot apply compiled-data encoding for field %s: parent field is missing", segment.Key)
	}
	return SetEncodingAtPath(field, remaining, encoding)
}

// Patch is one edited subtree and the encoding needed to rebuild it.
type Patch struct {
	Path     []kv3.Seg
	Value    kv3.Value
	Encoding kv3.Encoding
}

// CollectPatches lists the subtrees where candidate differs from base.
func CollectPatches(base, candidate *kv3.Value, candidateEncoding *kv3.Encoding,
	path []kv3.Seg, patches *[]Patch) error {
	if RuntimeValuesEqual(base, candidate) {
		return nil
	}
	if base.Kind == kv3.KindObject && candidate.Kind == kv3.KindObject {
		if candidateEncoding.Children.Kind != kv3.ChildrenObject {
			return fmt.Errorf("Compiled-data object has non-object encoding metadata")
		}
		encodings := candidateEncoding.Children.Object
		for _, field := range candidate.Object {
			if strings.EqualFold(field.Key, "_editor") {
				continue
			}
			fieldEncoding, ok := EncodingGetMut(encodings, field.Key)
			if !ok {
				return fmt.Errorf("Compiled-data object has no encoding metadata for field %s", field.Key)
			}
			next := append(append([]kv3.Seg(nil), path...), kv3.KeySeg(field.Key))
			if baseValue, ok := ObjectGetMut(base.Object, field.Key); ok {
				if err := CollectPatches(baseValue, &field.Value, fieldEncoding, next, patches); err != nil {
					return err
				}
				continue
			}
			*patches = append(*patches, Patch{Path: next, Value: field.Value, Encoding: *fieldEncoding})
		}
		return nil
	}
	// Array positions are structural rather than stable field identities. A
	// different mod may add, remove or reorder items, so the whole array is one
	// patch.
	*patches = append(*patches, Patch{
		Path:     append([]kv3.Seg(nil), path...),
		Value:    *candidate,
		Encoding: *candidateEncoding,
	})
	return nil
}

// CollectFields lists every leaf subtree of candidate.
func CollectFields(candidate *kv3.Value, candidateEncoding *kv3.Encoding,
	path []kv3.Seg, fields *[]Patch) error {
	if candidate.Kind != kv3.KindObject {
		*fields = append(*fields, Patch{
			Path:     append([]kv3.Seg(nil), path...),
			Value:    *candidate,
			Encoding: *candidateEncoding,
		})
		return nil
	}
	if candidateEncoding.Children.Kind != kv3.ChildrenObject {
		return fmt.Errorf("Compiled-data object has non-object encoding metadata")
	}
	encodings := candidateEncoding.Children.Object
	for _, field := range candidate.Object {
		if strings.EqualFold(field.Key, "_editor") {
			continue
		}
		fieldEncoding, ok := EncodingGetMut(encodings, field.Key)
		if !ok {
			return fmt.Errorf("Compiled-data object has no encoding metadata for field %s", field.Key)
		}
		next := append(append([]kv3.Seg(nil), path...), kv3.KeySeg(field.Key))
		if err := CollectFields(&field.Value, fieldEncoding, next, fields); err != nil {
			return err
		}
	}
	return nil
}

// PromotePatch widens a patch to the first ancestor the current table cannot
// descend through, so applying it can restore a missing parent subtree.
func PromotePatch(current *kv3.Value, currentEncoding *kv3.Encoding,
	candidate *kv3.Value, candidateEncoding *kv3.Encoding, patch Patch) (Patch, error) {
	for prefixLength := 1; prefixLength < len(patch.Path); prefixLength++ {
		prefix := patch.Path[:prefixLength]
		next := patch.Path[prefixLength]
		valueCanDescend := false
		if value, ok := ValueAtPath(current, prefix); ok {
			valueCanDescend = valueSupportsSegment(value, next)
		}
		encodingCanDescend := false
		if encoding, ok := EncodingAtPath(currentEncoding, prefix); ok {
			encodingCanDescend = encodingSupportsSegment(encoding, next)
		}
		if valueCanDescend && encodingCanDescend {
			continue
		}
		value, ok := ValueAtPath(candidate, prefix)
		if !ok {
			return Patch{}, fmt.Errorf("Cannot restore missing compiled-data parent %s: candidate value is missing", PathLabel(prefix))
		}
		encoding, ok := EncodingAtPath(candidateEncoding, prefix)
		if !ok {
			return Patch{}, fmt.Errorf("Cannot restore missing compiled-data parent %s: candidate encoding is missing", PathLabel(prefix))
		}
		return Patch{
			Path:     append([]kv3.Seg(nil), prefix...),
			Value:    *value,
			Encoding: *encoding,
		}, nil
	}
	return patch, nil
}

func valueSupportsSegment(value *kv3.Value, segment kv3.Seg) bool {
	if segment.Kind == kv3.SegIndex {
		return value.Kind == kv3.KindArray
	}
	return value.Kind == kv3.KindObject
}

func encodingSupportsSegment(encoding *kv3.Encoding, segment kv3.Seg) bool {
	if segment.Kind == kv3.SegIndex {
		return encoding.Children.Kind == kv3.ChildrenArray
	}
	return encoding.Children.Kind == kv3.ChildrenObject
}

// PathLabel renders a path the way warnings show it.
func PathLabel(path []kv3.Seg) string {
	var label strings.Builder
	for _, segment := range path {
		if segment.Kind == kv3.SegIndex {
			fmt.Fprintf(&label, "[%d]", segment.Index)
			continue
		}
		if label.Len() > 0 {
			label.WriteByte('.')
		}
		label.WriteString(segment.Key)
	}
	return label.String()
}
