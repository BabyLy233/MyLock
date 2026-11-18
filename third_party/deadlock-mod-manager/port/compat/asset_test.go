package compat

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func rig(parents []int64) kv3.Value {
	names := make([]kv3.Value, len(parents))
	poses := make([]kv3.Value, len(parents))
	parentValues := make([]kv3.Value, len(parents))
	for i, parent := range parents {
		names[i] = kv3.String("bone" + string(rune('0'+i)))
		parentValues[i] = kv3.Int(parent)
		poses[i] = kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0))
	}
	return kv3.Object(
		kv3.Pair{Key: "names", Value: kv3.Array(names...)},
		kv3.Pair{Key: "parents", Value: kv3.Array(parentValues...)},
		kv3.Pair{Key: "poses", Value: kv3.Array(poses...)},
	)
}

var poseKeys = []PoseKey{{Key: "poses", Width: 3}}

func TestSkeletonChecksRejectCyclesAndOutOfBoundsParents(t *testing.T) {
	for _, parents := range [][]int64{{-1, 0, 1}, {2, 0, -1}, {}} {
		value := rig(parents)
		if !ValidSkeleton(&value, "names", "parents", poseKeys) {
			t.Fatalf("valid parents %v rejected", parents)
		}
	}
	for _, parents := range [][]int64{{1, 0}, {-1, 9}, {-1, -2}, {0}} {
		value := rig(parents)
		if ValidSkeleton(&value, "names", "parents", poseKeys) {
			t.Fatalf("invalid parents %v accepted", parents)
		}
	}
}

func TestSkeletonChecksRejectDuplicateBonesAndIncompletePoses(t *testing.T) {
	value := rig([]int64{-1, 0})
	names, _ := value.Get("names")
	items, _ := names.AsArray()
	items[1] = kv3.String("bone0")
	*names = kv3.Array(items...)
	if ValidSkeleton(&value, "names", "parents", poseKeys) {
		t.Fatal("duplicate bone names accepted")
	}

	value = rig([]int64{-1, 0})
	poses, _ := value.Get("poses")
	poseItems, _ := poses.AsArray()
	*poses = kv3.Array(poseItems[:1]...)
	if ValidSkeleton(&value, "names", "parents", poseKeys) {
		t.Fatal("incomplete reference pose accepted")
	}

	value = rig([]int64{-1, 0})
	poses, _ = value.Get("poses")
	poseItems, _ = poses.AsArray()
	poseItems[0] = kv3.Array(kv3.Double(0), kv3.Double(0))
	*poses = kv3.Array(poseItems...)
	if ValidSkeleton(&value, "names", "parents", poseKeys) {
		t.Fatal("short reference pose accepted")
	}
}

func TestRemappingChecksRejectIndicesOutsideTheModelSkeleton(t *testing.T) {
	rigValue := kv3.Object(kv3.Pair{Key: "m_boneName", Value: kv3.Array(kv3.String("root"))})
	model := func(index int64, starts []int64) kv3.Value {
		startValues := make([]kv3.Value, len(starts))
		for i, start := range starts {
			startValues[i] = kv3.Int(start)
		}
		return kv3.Object(
			kv3.Pair{Key: "m_remappingTable", Value: kv3.Array(kv3.Int(index))},
			kv3.Pair{Key: "m_remappingTableStarts", Value: kv3.Array(startValues...)},
		)
	}
	if !ValidRemapping(&[]kv3.Value{model(0, []int64{0})}[0], &rigValue) {
		t.Fatal("valid remapping rejected")
	}
	for _, values := range [][2]any{{int64(1), []int64{0}}, {int64(0), []int64{2}}} {
		value := model(values[0].(int64), values[1].([]int64))
		if ValidRemapping(&value, &rigValue) {
			t.Fatalf("invalid remapping %v accepted", values)
		}
	}
}

func TestHitboxesCanReferenceModelBonesOutsideTheMeshSubset(t *testing.T) {
	mesh := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_bones", Value: kv3.Array()},
		)},
		kv3.Pair{Key: "m_hitboxsets", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_HitBoxes", Value: kv3.Array(kv3.Object(
					kv3.Pair{Key: "m_sBoneName", Value: kv3.String("pelvis")},
				))},
			)},
		))},
	)
	if warnings := MeshWarnings(&mesh, map[string]struct{}{"pelvis": {}}); len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	warnings := MeshWarnings(&mesh, nil)
	if len(warnings) != 1 || warnings[0].Kind != WarningInvalidHitbox || warnings[0].Detail != "pelvis" {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestMeshAttachmentChecksDistinguishRootSpaceFromMissingBones(t *testing.T) {
	mesh := func(rooted bool) kv3.Value {
		return kv3.Object(
			kv3.Pair{Key: "m_skeleton", Value: kv3.Object(
				kv3.Pair{Key: "m_bones", Value: kv3.Array()},
			)},
			kv3.Pair{Key: "m_attachments", Value: kv3.Array(kv3.Object(
				kv3.Pair{Key: "key", Value: kv3.String("camera")},
				kv3.Pair{Key: "value", Value: kv3.Object(
					kv3.Pair{Key: "m_nInfluences", Value: kv3.Int(1)},
					kv3.Pair{Key: "m_influenceNames", Value: kv3.Array(kv3.String(""))},
					kv3.Pair{Key: "m_bInfluenceRootTransform", Value: kv3.Array(kv3.Bool(rooted))},
					kv3.Pair{Key: "m_influenceWeights", Value: kv3.Array(kv3.Double(1))},
				)},
			))},
		)
	}
	rooted := mesh(true)
	if warnings := MeshWarnings(&rooted, nil); len(warnings) != 0 {
		t.Fatalf("rooted attachment warned: %v", warnings)
	}
	missing := mesh(false)
	warnings := MeshWarnings(&missing, nil)
	if len(warnings) != 1 || warnings[0].Kind != WarningInvalidAttachment || warnings[0].Detail != "camera" {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestMeshSkeletonRejectsDuplicateAndUnknownParents(t *testing.T) {
	bone := func(name, parent string) kv3.Value {
		return kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.String(name)},
			kv3.Pair{Key: "m_parentName", Value: kv3.String(parent)},
		)
	}
	mesh := func(bones ...kv3.Value) kv3.Value {
		return kv3.Object(kv3.Pair{Key: "m_skeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_bones", Value: kv3.Array(bones...)},
		)})
	}
	valid := mesh(bone("root", ""), bone("child", "root"))
	if warnings := MeshWarnings(&valid, nil); len(warnings) != 0 {
		t.Fatalf("valid mesh warned: %v", warnings)
	}
	duplicate := mesh(bone("root", ""), bone("root", ""))
	if warnings := MeshWarnings(&duplicate, nil); len(warnings) != 1 || warnings[0].Kind != WarningInvalidSkeleton {
		t.Fatalf("duplicate skeleton warnings = %v", warnings)
	}
	unknown := mesh(bone("root", "missing"))
	if warnings := MeshWarnings(&unknown, nil); len(warnings) != 1 || warnings[0].Kind != WarningInvalidSkeleton {
		t.Fatalf("unknown parent warnings = %v", warnings)
	}
}

func TestMeshPath(t *testing.T) {
	for _, path := range []string{"models/a/b.vmesh_c", "Models\\A\\B.VMESH_C"} {
		if !IsMeshPath(path) {
			t.Fatalf("%q rejected", path)
		}
	}
	for _, path := range []string{"models/a.vmdl_c", "models/../a.vmesh_c", "models//a.vmesh_c"} {
		if IsMeshPath(path) {
			t.Fatalf("%q accepted", path)
		}
	}
}
