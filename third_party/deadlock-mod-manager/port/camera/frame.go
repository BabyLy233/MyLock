// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/camera_attachment_frame.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package camera

import (
	"math"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/rig"
)

var mdat = [4]byte{'M', 'D', 'A', 'T'}

var pivots = [2]string{"standing_pivot", "crouching_pivot"}

var (
	oldFrame = [4]float64{-0.5, -0.5, -0.5, 0.5}
	identity = [4]float64{0, 0, 0, 1}
)

const (
	rootAngleTolerance = 0.001
	frameTolerance     = math.Pi / 6
)

type frame int

const (
	frameOld frame = iota
	frameCurrent
	frameOther
)

type frameMigration struct {
	index int
	only  map[string]struct{} // nil migrates every attachment
}

// PlanFrame reports whether the source model needs the old-frame migration.
// A mixed frame model returns ErrMixedFrame; other unsafe models return an
// error. Models with no MDAT or no base camera return false.
func PlanFrame(source, base []byte) (bool, error) {
	migrations, err := planFrame(source, base)
	if err != nil {
		return false, err
	}
	return len(migrations) > 0, nil
}

func planFrame(source, base []byte) ([]frameMigration, error) {
	sourceResource, err := resource.Parse(source)
	if err != nil {
		return nil, nil
	}
	baseResource, err := resource.Parse(base)
	if err != nil {
		return nil, nil
	}
	var currentMesh *kv3.Value
	for i, block := range baseResource.Blocks() {
		if block.Kind != mdat {
			continue
		}
		raw, ok := baseResource.GetBlockByIndex(i)
		if !ok {
			continue
		}
		mesh, err := kv3.Decode(raw)
		if err != nil {
			continue
		}
		if pivotsMatch(&mesh, identity) {
			currentMesh = &mesh
			break
		}
	}
	if currentMesh == nil {
		return nil, nil
	}

	var cameraNames map[string]struct{}
	getCameraNames := func() map[string]struct{} {
		if cameraNames == nil {
			cameraNames = authoredCameraNames(source)
		}
		return cameraNames
	}

	var indices []int
	for i, block := range sourceResource.Blocks() {
		if block.Kind == mdat {
			indices = append(indices, i)
		}
	}
	var migrations []frameMigration
	for _, index := range indices {
		raw, ok := sourceResource.GetBlockByIndex(index)
		if !ok {
			return nil, nil
		}
		mesh, err := kv3.Decode(raw)
		if err != nil {
			return nil, nil
		}
		var frames []frame
		for _, name := range pivots {
			if f, present := pivotFrame(&mesh, name); present {
				frames = append(frames, f)
			}
		}
		oldCount := 0
		for _, f := range frames {
			if f == frameOld {
				oldCount++
			}
		}
		switch {
		case oldCount > 0 && oldCount == len(frames):
			migrations = append(migrations, frameMigration{index: index})
		case oldCount > 0:
			return nil, ErrMixedFrame
		case len(frames) > 0:
			allCurrent := true
			for _, f := range frames {
				if f != frameCurrent {
					allCurrent = false
					break
				}
			}
			if allCurrent {
				stale := staleAttachments(&mesh, currentMesh, getCameraNames())
				if len(stale) > 0 {
					migrations = append(migrations, frameMigration{index: index, only: stale})
				}
			}
		}
	}
	if len(migrations) == 0 {
		return nil, nil
	}
	_, model, _, err := DecodeCompiled(source)
	if err != nil {
		return nil, invalidf("old camera model could not be decoded")
	}
	_, currentModel, _, err := DecodeCompiled(base)
	if err != nil {
		return nil, invalidf("current camera model could not be decoded")
	}
	ok, _ := compatibleRootAxes(&model, &currentModel)
	if !ok {
		return nil, invalidf("root_motion hierarchy, rotation or scale differs; camera migration skipped")
	}
	return migrations, nil
}

// RepairFrame migrates old root attachment frames to the current basis. It
// returns changed=false when nothing needed changing.
func RepairFrame(source, base []byte) ([]byte, bool, error) {
	migrations, err := planFrame(source, base)
	if err != nil {
		return nil, false, err
	}
	if len(migrations) == 0 {
		return nil, false, nil
	}
	output := append([]byte(nil), source...)
	for _, migration := range migrations {
		res, err := resource.Parse(output)
		if err != nil {
			return nil, false, invalidf("invalid camera model container")
		}
		raw, ok := res.GetBlockByIndex(migration.index)
		if !ok {
			return nil, false, invalidf("missing camera mesh block")
		}
		mesh, encoding, err := kv3.DecodePreserving(raw)
		if err != nil {
			return nil, false, invalidf("camera mesh block could not be decoded")
		}
		if err := migrateMesh(&mesh, migration.only); err != nil {
			return nil, false, err
		}
		if err := kv3.NormalizeNumericArrayEncoding(&mesh, &encoding); err != nil {
			return nil, false, invalidf("camera transform encoding is unsupported")
		}
		roundFloatValues(&mesh, &encoding)
		format, err := kv3.FormatFromPayload(raw)
		if err != nil {
			return nil, false, invalidf("invalid camera mesh format")
		}
		rebuilt, err := kv3.EncodePreserving(&mesh, &encoding, format)
		if err != nil {
			return nil, false, invalidf("camera mesh could not preserve its encoding")
		}
		decoded, decodedEncoding, err := kv3.DecodePreserving(rebuilt)
		if err != nil {
			return nil, false, invalidf("rebuilt camera mesh could not be decoded")
		}
		if !RuntimeValuesEqual(&mesh, &decoded) || !kv3.EncodingPreserved(&mesh, &encoding, &decodedEncoding) {
			return nil, false, invalidf("camera migration failed its preservation check")
		}
		output, err = res.RebuildWithBlockPreserving(migration.index, rebuilt)
		if err != nil {
			return nil, false, invalidf("camera model could not be rebuilt")
		}
	}
	return output, true, nil
}

func pivotFrame(mesh *kv3.Value, name string) (frame, bool) {
	points, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return frameOther, false
	}
	present := false
	for i := range points {
		if key, ok := points[i].Get("key"); ok {
			if s, ok := key.AsString(); ok && s == name {
				present = true
				break
			}
		}
	}
	if !present {
		return frameOther, false
	}
	q, ok := attachmentRotation(mesh, name)
	if !ok {
		return frameOther, true
	}
	near := func(target [4]float64) bool {
		a, ok := angle(q, target)
		return ok && a <= frameTolerance
	}
	switch {
	case near(oldFrame):
		return frameOld, true
	case near(identity):
		return frameCurrent, true
	default:
		return frameOther, true
	}
}

func authoredCameraNames(source []byte) map[string]struct{} {
	_, model, _, err := DecodeCompiled(source)
	if err != nil {
		return map[string]struct{}{}
	}
	info, ok := fieldObject(&model, "m_modelInfo")
	if !ok {
		return map[string]struct{}{}
	}
	textV, ok := info.Get("m_keyValueText")
	if !ok {
		return map[string]struct{}{}
	}
	text, ok := textV.AsString()
	if !ok {
		return map[string]struct{}{}
	}
	names, ok := CameraDataNames(text)
	if !ok {
		return map[string]struct{}{}
	}
	return names
}

// staleAttachments are root attachments still in the old frame after the
// pivots were updated, as a partial migration leaves them.
func staleAttachments(mesh, current *kv3.Value, cameraNames map[string]struct{}) map[string]struct{} {
	stale := make(map[string]struct{})
	pivot, ok := attachmentRotation(current, "standing_pivot")
	if !ok {
		return stale
	}
	points, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return stale
	}
	for i := range points {
		keyV, ok := points[i].Get("key")
		if !ok {
			continue
		}
		name, ok := keyV.AsString()
		if !ok || name == "standing_pivot" || name == "crouching_pivot" {
			continue
		}
		q, ok := attachmentRotation(mesh, name)
		if !ok {
			continue
		}
		converted := convert(q)
		if hasPoint(current, name) {
			game, ok := attachmentRotation(current, name)
			if !ok {
				continue
			}
			a1, ok1 := angle(converted, game)
			a2, ok2 := angle(q, game)
			if ok1 && ok2 && a1 <= frameTolerance && a2 > 2*frameTolerance {
				stale[name] = struct{}{}
			}
			continue
		}
		if _, known := cameraNames[name]; !cameraPose(name) || !known {
			continue
		}
		d1 := dot3(right(q), right(pivot))
		d2 := dot3(right(converted), right(pivot))
		a, ok := angle(converted, pivot)
		if d1 < 0.5 && d2 >= math.Cos(frameTolerance) && ok && a <= math.Pi/2+frameTolerance {
			stale[name] = struct{}{}
		}
	}
	return stale
}

func cameraPose(name string) bool {
	for _, family := range []string{"near_", "far_", "gunaim_"} {
		if idx, ok := stringsCutPrefix(name, family); ok && len(idx) == 2 &&
			idx[0] >= '0' && idx[0] <= '9' && idx[1] >= '0' && idx[1] <= '9' {
			return true
		}
	}
	return false
}

func stringsCutPrefix(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || s[:len(prefix)] != prefix {
		return s, false
	}
	return s[len(prefix):], true
}

func convert(q [4]float64) [4]float64 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	return [4]float64{
		(x + w + z - y) * 0.5,
		(y + w + x - z) * 0.5,
		(z + w + y - x) * 0.5,
		(w - x - y - z) * 0.5,
	}
}

func right(q [4]float64) [3]float64 {
	norm := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	x, y, z, w := q[0]/norm, q[1]/norm, q[2]/norm, q[3]/norm
	return [3]float64{
		2 * (x*y - w*z),
		1 - 2*(x*x+z*z),
		2 * (y*z + w*x),
	}
}

func dot3(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func angle(a, b [4]float64) (float64, bool) {
	norm := func(q [4]float64) float64 {
		return math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	}
	an, bn := norm(a), norm(b)
	if !isFinite(an) || !isFinite(bn) || math.Abs(an-1) > 0.001 || math.Abs(bn-1) > 0.001 {
		return 0, false
	}
	dot := (a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]) / (an * bn)
	return 2 * math.Acos(math.Min(math.Abs(dot), 1)), true
}

func compatibleRootAxes(source, current *kv3.Value) (bool, bool) {
	sourceRig, ok := rig.Parse(source)
	if !ok {
		return false, false
	}
	currentRig, ok := rig.Parse(current)
	if !ok {
		return false, false
	}
	sp, ok := sourceRig.Parent("root_motion")
	if !ok || sp != "" {
		return false, true
	}
	cp, ok := currentRig.Parent("root_motion")
	if !ok || cp != "" {
		return false, true
	}
	sourcePoseV, ok := sourceRig.Pose("root_motion")
	if !ok {
		return false, false
	}
	currentPoseV, ok := currentRig.Pose("root_motion")
	if !ok {
		return false, false
	}
	authored, ok := vectorN(&sourcePoseV, 8)
	if !ok {
		return false, false
	}
	canonical, ok := vectorN(&currentPoseV, 8)
	if !ok {
		return false, false
	}
	if authored[3] <= 0 || canonical[3] <= 0 || math.Abs(authored[3]-canonical[3]) > 0.001 {
		return false, true
	}
	normalized := func(pose []float64) ([4]float64, bool) {
		q := [4]float64{pose[4], pose[5], pose[6], pose[7]}
		n := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
		if math.Abs(n-1) > 0.001 {
			return q, false
		}
		return [4]float64{q[0] / n, q[1] / n, q[2] / n, q[3] / n}, true
	}
	a, ok := normalized(authored)
	if !ok {
		return false, false
	}
	b, ok := normalized(canonical)
	if !ok {
		return false, false
	}
	dot := a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]
	return 2*math.Acos(math.Min(math.Abs(dot), 1)) <= rootAngleTolerance, true
}

func roundFloatValues(value *kv3.Value, encoding *kv3.Encoding) {
	switch value.Kind {
	case kv3.KindDouble:
		if encoding.IsFloat() {
			value.Double = float64(float32(value.Double))
		}
	case kv3.KindArray:
		if encoding.Children.Kind != kv3.ChildrenArray {
			return
		}
		for i := range value.Array {
			if i >= len(encoding.Children.Array) {
				return
			}
			roundFloatValues(&value.Array[i], &encoding.Children.Array[i])
		}
	case kv3.KindObject:
		for i := range value.Object {
			child, ok := encoding.Get(value.Object[i].Key)
			if ok {
				roundFloatValues(&value.Object[i].Value, child)
			}
		}
	}
}

func attachment(mesh *kv3.Value, name string) (*kv3.Value, bool) {
	points, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return nil, false
	}
	found := false
	var value *kv3.Value
	for i := range points {
		keyV, ok := points[i].Get("key")
		if !ok {
			continue
		}
		key, ok := keyV.AsString()
		if !ok || key != name {
			continue
		}
		if found {
			return nil, false
		}
		found = true
		value, ok = points[i].Get("value")
		if !ok || !singleRoot(value) {
			return nil, false
		}
	}
	return value, found
}

func singleRoot(value *kv3.Value) bool {
	influences, ok := value.Get("m_nInfluences")
	if !ok {
		return false
	}
	n, ok := influences.AsInt()
	if !ok || n != 1 {
		return false
	}
	names, ok := fieldArray(value, "m_influenceNames")
	if !ok || len(names) == 0 {
		return false
	}
	name0, ok := names[0].AsString()
	if !ok || name0 != "root_motion" {
		return false
	}
	for i := 1; i < len(names); i++ {
		name, ok := names[i].AsString()
		if !ok || name != "" {
			return false
		}
	}
	flags, ok := fieldArray(value, "m_bInfluenceRootTransform")
	if !ok || len(flags) == 0 {
		return false
	}
	flag0, ok := flags[0].AsBool()
	return ok && !flag0
}

func attachmentRotation(mesh *kv3.Value, name string) ([4]float64, bool) {
	value, ok := attachment(mesh, name)
	if !ok {
		return [4]float64{}, false
	}
	return rotation(value)
}

func rotation(value *kv3.Value) ([4]float64, bool) {
	rotations, ok := fieldArray(value, "m_vInfluenceRotations")
	if !ok || len(rotations) == 0 {
		return [4]float64{}, false
	}
	q, ok := vectorN(&rotations[0], 4)
	if !ok {
		return [4]float64{}, false
	}
	return [4]float64{q[0], q[1], q[2], q[3]}, true
}

func sameRotation(a, b [4]float64) bool {
	angle, ok := angle(a, b)
	return ok && angle <= 0.0002
}

func pivotsMatch(mesh *kv3.Value, target [4]float64) bool {
	for _, name := range pivots {
		q, ok := attachmentRotation(mesh, name)
		if !ok || !sameRotation(q, target) {
			return false
		}
	}
	return true
}

// migrateMesh converts every root attachment, or only the names in only.
func migrateMesh(mesh *kv3.Value, only map[string]struct{}) error {
	attachments, ok := fieldArray(mesh, "m_attachments")
	if !ok {
		return invalidf("missing camera attachments")
	}
	seen := make(map[string]struct{})
	for i := range attachments {
		keyV, ok := attachments[i].Get("key")
		if !ok {
			return invalidf("unnamed attachment")
		}
		name, ok := keyV.AsString()
		if !ok {
			return invalidf("unnamed attachment")
		}
		if only != nil {
			if _, wanted := only[name]; !wanted {
				continue
			}
		}
		if _, dup := seen[name]; dup {
			return invalidf("duplicate attachment names")
		}
		seen[name] = struct{}{}
		value, ok := attachments[i].Get("value")
		if !ok {
			return invalidf("missing attachment value")
		}
		hasRoot := false
		if names, ok := fieldArray(value, "m_influenceNames"); ok {
			for j := range names {
				if s, ok := names[j].AsString(); ok && s == "root_motion" {
					hasRoot = true
					break
				}
			}
		}
		if !hasRoot {
			continue
		}
		if !singleRoot(value) {
			return invalidf("blended or root-space attachment requires author review")
		}
		q, ok := rotation(value)
		if !ok {
			return invalidf("invalid attachment rotation")
		}
		norm := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
		if math.Abs(norm-1) > 0.001 {
			return invalidf("invalid attachment quaternion")
		}
		offsets, ok := fieldArray(value, "m_vInfluenceOffsets")
		if !ok || len(offsets) == 0 {
			return invalidf("unsupported attachment influences")
		}
		offset, ok := vectorN(&offsets[0], 3)
		if !ok {
			return invalidf("invalid attachment offset")
		}
		offsets[0] = kv3.Array(kv3.Double(offset[2]), kv3.Double(offset[0]), kv3.Double(offset[1]))
		rotations, ok := fieldArray(value, "m_vInfluenceRotations")
		if !ok || len(rotations) == 0 {
			return invalidf("missing attachment rotations")
		}
		c := convert(q)
		rotations[0] = kv3.Array(kv3.Double(c[0]), kv3.Double(c[1]), kv3.Double(c[2]), kv3.Double(c[3]))
	}
	return nil
}
