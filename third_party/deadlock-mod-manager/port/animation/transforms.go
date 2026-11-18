// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_animation/rig_rebase/transforms.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import (
	"math"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

// transform is a source-space uniform TRS.
type transform struct {
	position [3]float64
	rotation [4]float64
	scale    float64
}

var identityTransform = transform{
	position: [3]float64{},
	rotation: [4]float64{0, 0, 0, 1},
	scale:    1,
}

func newTransform(position [3]float64, rotation [4]float64, scale float64) (transform, bool) {
	norm := 0.0
	for _, value := range rotation {
		norm += value * value
	}
	norm = math.Sqrt(norm)
	for _, value := range position {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return transform{}, false
		}
	}
	for _, value := range rotation {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return transform{}, false
		}
	}
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 || math.Abs(norm-1) > 0.001 {
		return transform{}, false
	}
	return transform{position: position, rotation: [4]float64{
		rotation[0] / norm, rotation[1] / norm, rotation[2] / norm, rotation[3] / norm,
	}, scale: scale}, true
}

func (t transform) vector(v [3]float64) [3]float64 {
	x, y, z, w := t.rotation[0], t.rotation[1], t.rotation[2], t.rotation[3]
	a := [3]float64{
		y*v[2] - z*v[1],
		z*v[0] - x*v[2],
		x*v[1] - y*v[0],
	}
	b := [3]float64{
		y*a[2] - z*a[1],
		z*a[0] - x*a[2],
		x*a[1] - y*a[0],
	}
	var result [3]float64
	for i := range result {
		result[i] = t.scale * (v[i] + 2*(w*a[i]+b[i]))
	}
	return result
}

func (t transform) point(v [3]float64) [3]float64 {
	value := t.vector(v)
	for i := range value {
		value[i] += t.position[i]
	}
	return value
}

func (t transform) compose(local transform) transform {
	x, y, z, w := t.rotation[0], t.rotation[1], t.rotation[2], t.rotation[3]
	a, b, c, d := local.rotation[0], local.rotation[1], local.rotation[2], local.rotation[3]
	return transform{
		position: t.point(local.position),
		scale:    t.scale * local.scale,
		rotation: [4]float64{
			w*a + x*d + y*c - z*b,
			w*b - x*c + y*d + z*a,
			w*c + x*b - y*a + z*d,
			w*d - x*a - y*b - z*c,
		},
	}
}

func (t transform) inverse() transform {
	x, y, z, w := t.rotation[0], t.rotation[1], t.rotation[2], t.rotation[3]
	inverse := transform{
		rotation: [4]float64{-x, -y, -z, w},
		scale:    1 / t.scale,
	}
	inverse.position = inverse.vector([3]float64{-t.position[0], -t.position[1], -t.position[2]})
	return inverse
}

// matrix returns a row-major affine 3x4 matrix.
func (t transform) matrix() [12]float64 {
	axes := [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	var transformed [3][3]float64
	for i := range axes {
		transformed[i] = t.vector(axes[i])
	}
	var result [12]float64
	for i := range result {
		if i%4 == 3 {
			result[i] = t.position[i/4]
		} else {
			result[i] = transformed[i%4][i/4]
		}
	}
	return result
}

func modelWorlds(model *kv3.Value) ([]transform, bool) {
	if _, ok := rig.Parse(model); !ok {
		return nil, false
	}
	rigValue, ok := model.Get("m_modelSkeleton")
	if !ok {
		return nil, false
	}
	parentsValue, ok := rigValue.Get("m_nParent")
	if !ok {
		return nil, false
	}
	parents, ok := parentsValue.AsArray()
	if !ok {
		return nil, false
	}
	locals := make([]transform, len(parents))
	for i := range parents {
		position4, ok := arrayNumbers(rigValue, "m_bonePosParent", i, 3)
		if !ok {
			return nil, false
		}
		position := [3]float64{position4[0], position4[1], position4[2]}
		rotation, ok := arrayNumbers(rigValue, "m_boneRotParent", i, 4)
		if !ok {
			return nil, false
		}
		scaleValue, ok := arrayItem(rigValue, "m_boneScaleParent", i)
		if !ok {
			return nil, false
		}
		scale, ok := scaleValue.AsFloat()
		if !ok {
			return nil, false
		}
		local, ok := newTransform(position, rotation, scale)
		if !ok {
			return nil, false
		}
		locals[i] = local
	}
	out := make([]transform, len(parents))
	done := make([]bool, len(parents))
	for start := range parents {
		if done[start] {
			continue
		}
		var chain []int
		node := start
		var parentWorld transform
		for {
			if done[node] {
				parentWorld = out[node]
				break
			}
			chain = append(chain, node)
			parent, ok := parents[node].AsInt()
			if !ok {
				return nil, false
			}
			if parent == -1 {
				parentWorld = identityTransform
				break
			}
			if parent < 0 || parent >= int64(len(parents)) {
				return nil, false
			}
			node = int(parent)
		}
		for i := len(chain) - 1; i >= 0; i-- {
			index := chain[i]
			parentWorld = parentWorld.compose(locals[index])
			out[index] = parentWorld
			done[index] = true
		}
	}
	return out, true
}

func sourceFloat(number float64) (kv3.Value, bool) {
	quantized := float64(float32(number))
	if math.IsNaN(quantized) || math.IsInf(quantized, 0) {
		return kv3.Value{}, false
	}
	return kv3.Double(quantized), true
}

func replaceNumbers(value *kv3.Value, numbers []float64) bool {
	if value.Kind != kv3.KindArray || len(value.Array) != len(numbers) {
		return false
	}
	converted := make([]kv3.Value, len(numbers))
	for i := range numbers {
		number, ok := sourceFloat(numbers[i])
		if !ok || value.Array[i].Kind != kv3.KindDouble {
			return false
		}
		converted[i] = number
	}
	value.Array = converted
	return true
}

func arrayNumbers(value *kv3.Value, field string, index, count int) ([4]float64, bool) {
	var result [4]float64
	item, ok := arrayItem(value, field, index)
	if !ok {
		return result, false
	}
	numbers, ok := clipNumbers(&item, count)
	if !ok {
		return result, false
	}
	copy(result[:], numbers)
	return result, true
}

func arrayItemMut(value *kv3.Value, field string, index int) (*kv3.Value, bool) {
	if value.Kind != kv3.KindObject {
		return nil, false
	}
	for i := range value.Object {
		if value.Object[i].Key != field || value.Object[i].Value.Kind != kv3.KindArray {
			continue
		}
		if index < 0 || index >= len(value.Object[i].Value.Array) {
			return nil, false
		}
		return &value.Object[i].Value.Array[index], true
	}
	return nil, false
}
