// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_camera/contract.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"math"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

// contractSatisfied reports whether every embedded mesh supplies exactly one
// valid transform for each camera point of the current interface. A false
// result may mean malformed data or a genuine contract violation; callers only
// need to know it is not proven safe.
func contractSatisfied(res *resource.Resource, model *kv3.Value, base []byte, currentText string) bool {
	if refs, ok := model.Get("m_refMeshes"); ok {
		values, ok := refs.AsArray()
		if !ok || len(values) != 0 {
			return false
		}
	}
	modelRig, ok := rig.Parse(model)
	if !ok {
		return false
	}
	bones := make(map[string]struct{})
	for _, name := range modelRig.Names() {
		bones[name] = struct{}{}
	}
	baseResource, err := resource.Parse(base)
	if err != nil {
		return false
	}
	baseRaw, ok := baseResource.FindBlock(mdat)
	if !ok {
		return false
	}
	baseMesh, err := kv3.Decode(baseRaw)
	if err != nil {
		return false
	}
	names, ok := cameraPointNames(currentText, &baseMesh)
	if !ok {
		return false
	}

	count := 0
	for index, block := range res.Blocks() {
		if block.Kind != mdat {
			continue
		}
		count++
		raw, ok := res.GetBlockByIndex(index)
		if !ok {
			return false
		}
		mesh, err := kv3.Decode(raw)
		if err != nil {
			return false
		}
		points, ok := fieldArray(&mesh, "m_attachments")
		if !ok {
			return false
		}
		for _, name := range names {
			found := 0
			var point *kv3.Value
			for i := range points {
				keyV, ok := points[i].Get("key")
				if !ok {
					continue
				}
				key, ok := keyV.AsString()
				if !ok || key != name {
					continue
				}
				found++
				point, _ = points[i].Get("value")
			}
			if found != 1 || point == nil || !validPoint(point, name, bones) {
				return false
			}
		}
	}
	return count > 0
}

func validPoint(point *kv3.Value, name string, bones map[string]struct{}) bool {
	nameV, ok := point.Get("m_name")
	if !ok {
		return false
	}
	pointName, ok := nameV.AsString()
	if !ok || pointName != name {
		return false
	}
	influencesV, ok := point.Get("m_nInfluences")
	if !ok {
		return false
	}
	count64, ok := influencesV.AsInt()
	if !ok || count64 < 0 || count64 > math.MaxInt {
		return false
	}
	count := int(count64)
	names, ok := fieldArray(point, "m_influenceNames")
	if !ok {
		return false
	}
	roots, ok := fieldArray(point, "m_bInfluenceRootTransform")
	if !ok {
		return false
	}
	weights, ok := fieldArray(point, "m_influenceWeights")
	if !ok {
		return false
	}
	offsets, ok := fieldArray(point, "m_vInfluenceOffsets")
	if !ok {
		return false
	}
	rotations, ok := fieldArray(point, "m_vInfluenceRotations")
	if !ok {
		return false
	}
	if count == 0 ||
		count > len(names) || count > len(roots) || count > len(weights) ||
		count > len(offsets) || count > len(rotations) {
		return false
	}
	totalWeight := 0.0
	for i := 0; i < count; i++ {
		weight, ok := weights[i].AsFloat()
		if !ok || !isFinite(weight) || weight < 0 {
			return false
		}
		rooted, ok := roots[i].AsBool()
		if !ok {
			return false
		}
		if !rooted {
			bone, ok := names[i].AsString()
			if !ok {
				return false
			}
			if _, known := bones[bone]; !known {
				return false
			}
		}
		if _, ok := vectorN(&offsets[i], 3); !ok {
			return false
		}
		rot, ok := vectorN(&rotations[i], 4)
		if !ok {
			return false
		}
		norm := math.Sqrt(rot[0]*rot[0] + rot[1]*rot[1] + rot[2]*rot[2] + rot[3]*rot[3])
		if math.Abs(norm-1) > 0.001 {
			return false
		}
		totalWeight += weight
	}
	return math.Abs(totalWeight-1) <= 0.001
}
