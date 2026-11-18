// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only

package modcompat

import (
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const (
	dependencyModelPath    = "models/heroes/hero/hero.vmdl_c"
	dependencySkeletonPath = "anims/hero.vnmskel_c"
	dependencyOtherPath    = "anims/other.vnmclip_c"
)

func dependencyModel(t *testing.T, skeleton string) []byte {
	t.Helper()
	model := kv3.Object(kv3.Pair{
		Key: "m_vecNmSkeletonRefs", Value: kv3.Array(kv3.String(skeleton)),
	})
	return compiledBlock(t, &model)
}

func TestMixedAnimationDependenciesReportsOtherModsFilesInTheClosure(t *testing.T) {
	ownerVPK := packMod(t, []vpk.PackEntry{{
		Path: dependencyModelPath, Data: dependencyModel(t, dependencySkeletonPath),
	}})
	otherVPK := packMod(t, []vpk.PackEntry{{
		Path: dependencySkeletonPath, Data: compiledBlock(t, ptr(kv3.Object(
			kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(kv3.String("root"))},
		))),
	}, {
		Path: dependencyOtherPath, Data: compiledBlock(t, ptr(kv3.Object())),
	}})
	mods := []ModFiles{
		{ID: "owner", VPKs: []string{ownerVPK}},
		{ID: "other", VPKs: []string{otherVPK}},
	}
	mixed, err := MixedAnimationDependencies(mods[0], mods, dependencyModelPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed) != 1 || mixed[0].Path != dependencySkeletonPath || mixed[0].ModID != "other" {
		t.Fatalf("mixed = %+v", mixed)
	}
	if description := DescribeMixedDependencies(mixed); !strings.Contains(description, "模组 other") ||
		!strings.Contains(description, dependencySkeletonPath) {
		t.Fatalf("description = %q", description)
	}
}

func TestMixedAnimationDependenciesIgnoresIdenticalAndUnreferencedFiles(t *testing.T) {
	skeleton := compiledBlock(t, ptr(kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(kv3.String("root"))},
	)))
	ownerVPK := packMod(t, []vpk.PackEntry{
		{Path: dependencyModelPath, Data: dependencyModel(t, dependencySkeletonPath)},
		{Path: dependencySkeletonPath, Data: skeleton},
	})
	otherVPK := packMod(t, []vpk.PackEntry{
		{Path: dependencySkeletonPath, Data: skeleton},
		{Path: dependencyOtherPath, Data: compiledBlock(t, ptr(kv3.Object()))},
	})
	mods := []ModFiles{
		{ID: "owner", VPKs: []string{ownerVPK}},
		{ID: "other", VPKs: []string{otherVPK}},
	}
	mixed, err := MixedAnimationDependencies(mods[0], mods, dependencyModelPath)
	if err != nil {
		t.Fatal(err)
	}
	// The owner wins its own skeleton, and the other mod's clip is outside the
	// model's reference closure.
	if len(mixed) != 0 {
		t.Fatalf("mixed = %+v", mixed)
	}
}

func TestMixedAnimationDependenciesFollowsIncludedModels(t *testing.T) {
	includePath := "models/heroes/hero/extra.vmdl_c"
	ownerVPK := packMod(t, []vpk.PackEntry{{
		Path: dependencyModelPath,
		Data: compiledBlock(t, ptr(kv3.Object(
			kv3.Pair{Key: "m_refAnimIncludeModels", Value: kv3.Array(kv3.String(includePath))},
		))),
	}})
	otherVPK := packMod(t, []vpk.PackEntry{{
		Path: includePath, Data: compiledBlock(t, ptr(kv3.Object())),
	}})
	mods := []ModFiles{
		{ID: "owner", VPKs: []string{ownerVPK}},
		{ID: "other", VPKs: []string{otherVPK}},
	}
	mixed, err := MixedAnimationDependencies(mods[0], mods, dependencyModelPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed) != 1 || mixed[0].Path != includePath || mixed[0].ModID != "other" {
		t.Fatalf("mixed = %+v", mixed)
	}
}
