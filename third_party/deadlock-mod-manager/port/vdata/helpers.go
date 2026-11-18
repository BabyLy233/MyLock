// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Shared helpers for the VData compatibility port. See ../../../NOTICE.md.

package vdata

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func errCompiled(message string) error {
	return fmt.Errorf("mod invalid: %s", message)
}

func encodingEqual(left, right *kv3.Encoding) bool {
	return reflect.DeepEqual(left, right)
}

func cloneValue(value *kv3.Value) kv3.Value {
	out := *value
	switch value.Kind {
	case kv3.KindBinary:
		out.Binary = append([]byte(nil), value.Binary...)
	case kv3.KindArray:
		out.Array = make([]kv3.Value, len(value.Array))
		for index := range value.Array {
			out.Array[index] = cloneValue(&value.Array[index])
		}
	case kv3.KindObject:
		out.Object = make([]kv3.Pair, len(value.Object))
		for index := range value.Object {
			out.Object[index] = kv3.Pair{
				Key:   value.Object[index].Key,
				Value: cloneValue(&value.Object[index].Value),
			}
		}
	}
	return out
}

func cloneEncoding(encoding *kv3.Encoding) kv3.Encoding {
	out := *encoding
	switch encoding.Children.Kind {
	case kv3.ChildrenArray:
		out.Children.Array = make([]kv3.Encoding, len(encoding.Children.Array))
		for index := range encoding.Children.Array {
			out.Children.Array[index] = cloneEncoding(&encoding.Children.Array[index])
		}
	case kv3.ChildrenObject:
		out.Children.Object = make([]kv3.EncodingField, len(encoding.Children.Object))
		for index := range encoding.Children.Object {
			out.Children.Object[index] = kv3.EncodingField{
				Key:   encoding.Children.Object[index].Key,
				Value: cloneEncoding(&encoding.Children.Object[index].Value),
			}
		}
	}
	return out
}

func boolField(value *kv3.Value, key string) (bool, bool) {
	if value.Kind != kv3.KindObject {
		return false, false
	}
	for _, field := range value.Object {
		if strings.EqualFold(field.Key, key) && field.Value.Kind == kv3.KindBool {
			return field.Value.Bool, true
		}
	}
	return false, false
}

// normalizePath lowercases and unifies separators, matching the original's
// normalize_path for resource names.
func normalizePath(path string) string {
	return asciiLower(strings.ReplaceAll(path, "\\", "/"))
}
