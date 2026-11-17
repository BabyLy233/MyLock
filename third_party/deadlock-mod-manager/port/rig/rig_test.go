// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager asset_compatibility.rs and
// animation_skeleton.rs tests at 41afe0e706726f7c5882fb589f996b84362114fa.

package rig

import (
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func skeletonRig(parents []int64) kv3.Value {
	names := make([]kv3.Value, len(parents))
	poses := make([]kv3.Value, len(parents))
	parentValues := make([]kv3.Value, len(parents))
	for i := range parents {
		names[i] = kv3.String(string(rune('a' + i)))
		parentValues[i] = kv3.Int(parents[i])
		poses[i] = kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0))
	}
	return kv3.Object(
		kv3.Pair{Key: "names", Value: kv3.Value{Kind: kv3.KindArray, Array: names}},
		kv3.Pair{Key: "parents", Value: kv3.Value{Kind: kv3.KindArray, Array: parentValues}},
		kv3.Pair{Key: "poses", Value: kv3.Value{Kind: kv3.KindArray, Array: poses}},
	)
}

func TestSkeletonChecksRejectCyclesAndOutOfBoundsParents(t *testing.T) {
	valid := [][]int64{{-1, 0, 1}, {2, 0, -1}, {}}
	for _, parents := range valid {
		v := skeletonRig(parents)
		if !ValidSkeleton(&v, "names", "parents", []PoseKey{{Key: "poses", Width: 3}}) {
			t.Fatalf("valid parents %v rejected", parents)
		}
	}
	invalid := [][]int64{{1, 0}, {-1, 9}, {-1, -2}, {0}}
	for _, parents := range invalid {
		v := skeletonRig(parents)
		if ValidSkeleton(&v, "names", "parents", []PoseKey{{Key: "poses", Width: 3}}) {
			t.Fatalf("invalid parents %v accepted", parents)
		}
	}
}

func TestSkeletonChecksRejectDuplicateBonesAndIncompletePoses(t *testing.T) {
	v := skeletonRig([]int64{-1, 0})
	names, _ := v.Get("names")
	names.Array[1] = names.Array[0]
	if ValidSkeleton(&v, "names", "parents", []PoseKey{{Key: "poses", Width: 3}}) {
		t.Fatal("duplicate bone accepted")
	}

	v = skeletonRig([]int64{-1, 0})
	poses, _ := v.Get("poses")
	poses.Array = poses.Array[:1]
	if ValidSkeleton(&v, "names", "parents", []PoseKey{{Key: "poses", Width: 3}}) {
		t.Fatal("short pose array accepted")
	}
}

func TestEquivalentPoseAcceptsSignFlippedQuaternion(t *testing.T) {
	old := kv3.Array(
		kv3.Double(1), kv3.Double(2), kv3.Double(3), kv3.Double(4),
		kv3.Double(0.5), kv3.Double(0.5), kv3.Double(0.5), kv3.Double(0.5),
	)
	current := kv3.Array(
		kv3.Double(1.0005), kv3.Double(2), kv3.Double(3), kv3.Double(4),
		kv3.Double(-0.5), kv3.Double(-0.5), kv3.Double(-0.5), kv3.Double(-0.5),
	)
	ok, valid := EquivalentPose(&old, &current)
	if !valid || !ok {
		t.Fatalf("equivalent pose = %v, valid = %v", ok, valid)
	}
	current.Array[0] = kv3.Double(1.01)
	ok, valid = EquivalentPose(&old, &current)
	if !valid || ok {
		t.Fatalf("distant pose = %v, valid = %v", ok, valid)
	}
}
