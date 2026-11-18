// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_clips.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"math"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

var clipBlockingChannels = [...]string{
	"m_secondaryAnimations",
	"m_modelSpaceBoneSamplingIndices",
	"m_modelSpaceSamplingChain",
	"m_floatChannelData",
}

// FrameWords validates the compressed frame layout and returns the number of
// 16-bit words per frame. Missing or unsupported channels fail closed.
func FrameWords(clip *kv3.Value, tracks []kv3.Value) (int, bool) {
	for _, field := range clipBlockingChannels {
		value, ok := clip.Get(field)
		if !ok {
			return 0, false
		}
		items, ok := value.AsArray()
		if !ok || len(items) != 0 {
			return 0, false
		}
	}
	words := 0
	for i := range tracks {
		offsetValue, ok := tracks[i].Get("m_nTrackReadOffset")
		if !ok {
			return 0, false
		}
		offset, ok := offsetValue.AsUInt()
		if !ok || offset != uint64(words) {
			return 0, false
		}
		for _, spec := range []struct {
			field string
			size  int
		}{
			{"m_bIsRotationStatic", 3},
			{"m_bIsTranslationStatic", 3},
			{"m_bIsScaleStatic", 1},
		} {
			staticValue, ok := tracks[i].Get(spec.field)
			if !ok {
				return 0, false
			}
			static, ok := staticValue.AsBool()
			if !ok {
				return 0, false
			}
			if !static {
				words += spec.size
			}
		}
	}
	framesValue, ok := clip.Get("m_nNumFrames")
	if !ok {
		return 0, false
	}
	frames, ok := framesValue.AsUInt()
	if !ok {
		return 0, false
	}
	offsetsValue, ok := clip.Get("m_compressedPoseOffsets")
	if !ok {
		return 0, false
	}
	offsets, ok := offsetsValue.AsArray()
	if !ok || frames == 0 || uint64(len(offsets)) != frames {
		return 0, false
	}
	for i := range offsets {
		offset, ok := offsets[i].AsUInt()
		if !ok || offset != uint64(i)*uint64(words) {
			return 0, false
		}
	}
	dataValue, ok := clip.Get("m_compressedPoseData")
	if !ok || dataValue.Kind != kv3.KindBinary {
		return 0, false
	}
	if words == 0 {
		return words, len(dataValue.Binary) == 0
	}
	if frames > uint64(len(dataValue.Binary))/(2*uint64(words)) {
		return 0, false
	}
	if frames*uint64(words)*2 != uint64(len(dataValue.Binary)) {
		return 0, false
	}
	return words, true
}

func staticTrack(template *kv3.Value, pose [8]float64, offset int, additive bool) (kv3.Value, bool) {
	result := rig.Clone(template)
	for _, field := range []string{
		"m_bIsRotationStatic",
		"m_bIsTranslationStatic",
		"m_bIsScaleStatic",
	} {
		if !replaceValueField(&result, field, kv3.Bool(true)) {
			return kv3.Value{}, false
		}
	}
	if additive {
		pose = [8]float64{0, 0, 0, 0, 0, 0, 0, 1}
	}
	if !replaceValueField(&result, "m_constantRotation", kv3.Array(
		kv3.Double(pose[4]), kv3.Double(pose[5]), kv3.Double(pose[6]), kv3.Double(pose[7]))) {
		return kv3.Value{}, false
	}
	if !replaceValueField(&result, "m_nTrackReadOffset", kv3.UInt(uint64(offset))) {
		return kv3.Value{}, false
	}
	for i, field := range []string{
		"m_translationRangeX",
		"m_translationRangeY",
		"m_translationRangeZ",
		"m_scaleRange",
	} {
		rangeValue, ok := result.Get(field)
		if !ok || !replaceValueField(rangeValue, "m_flRangeStart", kv3.Double(pose[i])) {
			return kv3.Value{}, false
		}
	}
	return result, true
}

// ExpandClip inserts static tracks for skeleton bones the authored clip does not
// sample and reorders the compressed frames to match the current bone order.
// layout holds the old track index for each skeleton bone, or -1 when absent.
func ExpandClip(source []byte, skeleton *kv3.Value, layout []int) ([]byte, bool) {
	format, clip, encoding, err := camera.DecodeCompiled(source)
	if err != nil {
		return nil, false
	}
	tracksValue, ok := clip.Get("m_trackCompressionSettings")
	if !ok {
		return nil, false
	}
	tracks, ok := tracksValue.AsArray()
	if !ok || len(tracks) == 0 {
		return nil, false
	}
	words, ok := FrameWords(&clip, tracks)
	if !ok {
		return nil, false
	}
	additiveValue, ok := clip.Get("m_bIsAdditive")
	if !ok {
		return nil, false
	}
	additive, ok := additiveValue.AsBool()
	if !ok {
		return nil, false
	}
	trackEncodingsField, ok := encoding.Get("m_trackCompressionSettings")
	if !ok {
		return nil, false
	}
	trackEncodings, ok := trackEncodingsField.AsArray()
	if !ok || len(trackEncodings) != len(tracks) {
		return nil, false
	}
	posesValue, ok := skeleton.Get("m_parentSpaceReferencePose")
	if !ok {
		return nil, false
	}
	poses, ok := posesValue.AsArray()
	if !ok || len(layout) != len(poses) {
		return nil, false
	}
	seen := make([]bool, len(tracks))
	segments := make([][2]int, 0, len(layout))
	values := make([]kv3.Value, 0, len(layout))
	encodings := make([]kv3.Encoding, 0, len(layout))
	offset := 0
	for index, old := range layout {
		if old == -1 {
			pose, ok := poseNumbers(&poses[index])
			if !ok {
				return nil, false
			}
			track, ok := staticTrack(&tracks[0], pose, offset, additive)
			if !ok {
				return nil, false
			}
			values = append(values, track)
			encodings = append(encodings, *cloneEncoding(&trackEncodings[0]))
			continue
		}
		if old < 0 || old >= len(tracks) || seen[old] {
			return nil, false
		}
		seen[old] = true
		track := rig.Clone(&tracks[old])
		startValue, ok := track.Get("m_nTrackReadOffset")
		if !ok {
			return nil, false
		}
		start, ok := startValue.AsUInt()
		if !ok || start > uint64(words) {
			return nil, false
		}
		end := words
		if old+1 < len(tracks) {
			nextValue, ok := tracks[old+1].Get("m_nTrackReadOffset")
			if !ok {
				return nil, false
			}
			next, ok := nextValue.AsUInt()
			if !ok || next > uint64(words) {
				return nil, false
			}
			end = int(next)
		}
		startInt := int(start)
		if end < startInt {
			return nil, false
		}
		segments = append(segments, [2]int{startInt * 2, end * 2})
		if !replaceValueField(&track, "m_nTrackReadOffset", kv3.UInt(uint64(offset))) {
			return nil, false
		}
		offset += end - startInt
		values = append(values, track)
		encodings = append(encodings, *cloneEncoding(&trackEncodings[old]))
	}
	for _, present := range seen {
		if !present {
			return nil, false
		}
	}
	if offset != words {
		return nil, false
	}
	dataValue, ok := clip.Get("m_compressedPoseData")
	if !ok || dataValue.Kind != kv3.KindBinary {
		return nil, false
	}
	reordered := make([]byte, 0, len(dataValue.Binary))
	if words > 0 {
		for frame := 0; frame+words*2 <= len(dataValue.Binary); frame += words * 2 {
			for _, segment := range segments {
				reordered = append(reordered, dataValue.Binary[frame+segment[0]:frame+segment[1]]...)
			}
		}
	}
	if !replaceValueField(&clip, "m_compressedPoseData", kv3.Binary(reordered)) ||
		!replaceValueField(&clip, "m_trackCompressionSettings", kv3.Array(values...)) {
		return nil, false
	}
	trackEncodingsField, ok = encoding.Get("m_trackCompressionSettings")
	if !ok {
		return nil, false
	}
	trackEncodingsField.Children = kv3.EncodingChildren{Kind: kv3.ChildrenArray, Array: encodings}
	if err := kv3.NormalizeNumericArrayEncoding(&clip, &encoding); err != nil {
		return nil, false
	}
	payload, err := kv3.EncodePreserving(&clip, &encoding, format)
	if err != nil {
		return nil, false
	}
	parsed, err := resource.Parse(source)
	if err != nil {
		return nil, false
	}
	rebuilt, err := parsed.RebuildWithDataPreserving(payload)
	if err != nil {
		return nil, false
	}
	_, actual, actualEncoding, err := camera.DecodeCompiled(rebuilt)
	if err != nil {
		return nil, false
	}
	if !camera.RuntimeValuesEqual(&clip, &actual) ||
		!kv3.EncodingPreserved(&clip, &encoding, &actualEncoding) {
		return nil, false
	}
	return rebuilt, true
}

func clipNumbers(value *kv3.Value, count int) ([]float64, bool) {
	items, ok := value.AsArray()
	if !ok || len(items) != count {
		return nil, false
	}
	result := make([]float64, count)
	for i := range items {
		number, ok := items[i].AsFloat()
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
		result[i] = number
	}
	return result, true
}
