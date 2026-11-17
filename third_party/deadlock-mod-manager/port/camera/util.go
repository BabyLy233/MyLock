// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/{camera_attachment_frame,model_camera,camera_controls,model_camera/contract}.rs
// and the helpers they call, at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../NOTICE.md.

// Package camera ports the camera-compatibility repairs: the root attachment
// frame migration, the missing camera interface fields, and the camera points
// and control bones copied from the current game model.
package camera

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

// RepairKind identifies the compatibility repair that produced bytes.
type RepairKind string

const (
	RepairAttachmentFrame RepairKind = "CameraAttachmentFrame"
	RepairInterface       RepairKind = "CameraInterface"
	RepairControls        RepairKind = "CameraControls"
)

// WarningKind identifies a camera issue that could not be repaired.
type WarningKind string

const (
	WarningOutdatedAttachments WarningKind = "OutdatedCameraAttachments"
	WarningMissingAttachments  WarningKind = "MissingCameraAttachments"
	WarningMissingInterface    WarningKind = "MissingCameraInterface"
)

var (
	// ErrMixedFrame reports pivots in a mix of old and current frames.
	ErrMixedFrame = errors.New("camera pivots use mixed or authored frames; migration skipped")
	// ErrUnsafe reports a model that cannot be migrated safely.
	ErrUnsafe = errors.New("camera model cannot be migrated safely")
)

// NormalizePath replaces backslashes and lowercases a resource path.
func NormalizePath(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
}

// IsModelPath reports whether p is a models/... .vmdl_c path without empty,
// dot or parent segments.
func IsModelPath(p string) bool {
	path := NormalizePath(p)
	if !strings.HasPrefix(path, "models/") || !strings.HasSuffix(path, ".vmdl_c") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// DecodeCompiled decodes a Source 2 resource's DATA block.
func DecodeCompiled(data []byte) (kv3.Format, kv3.Value, kv3.Encoding, error) {
	res, err := resource.Parse(data)
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, err
	}
	block, err := res.DataBlock()
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, err
	}
	format, err := kv3.FormatFromPayload(block)
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, err
	}
	value, encoding, err := kv3.DecodePreserving(block)
	if err != nil {
		return kv3.Format{}, kv3.Value{}, kv3.Encoding{}, err
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
		for i := range left.Array {
			if !RuntimeValuesEqual(&left.Array[i], &right.Array[i]) {
				return false
			}
		}
		return true
	}
	return kv3.Equal(left, right)
}

func objectSetCaseInsensitive(obj *kv3.Value, key string, value kv3.Value) {
	if obj.Kind != kv3.KindObject {
		return
	}
	for i := range obj.Object {
		if strings.EqualFold(obj.Object[i].Key, key) {
			obj.Object[i].Value = value
			return
		}
	}
	obj.Object = append(obj.Object, kv3.Pair{Key: key, Value: value})
}

func fieldArray(v *kv3.Value, key string) ([]kv3.Value, bool) {
	field, ok := v.Get(key)
	if !ok {
		return nil, false
	}
	return field.AsArray()
}

func fieldObject(v *kv3.Value, key string) (*kv3.Value, bool) {
	field, ok := v.Get(key)
	if !ok || field.Kind != kv3.KindObject {
		return nil, false
	}
	return field, true
}

func vectorN(v *kv3.Value, n int) ([]float64, bool) {
	items, ok := v.AsArray()
	if !ok || len(items) != n {
		return nil, false
	}
	out := make([]float64, n)
	for i := range items {
		val, ok := items[i].AsFloat()
		if !ok || !isFinite(val) {
			return nil, false
		}
		out[i] = val
	}
	return out, true
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func sameFormat(a, b kv3.Format) bool { return a == b }

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsafe, fmt.Sprintf(format, args...))
}
