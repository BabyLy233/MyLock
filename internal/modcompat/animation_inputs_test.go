// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Cases translated from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_model_evidence.rs
// tests at 41afe0e706726f7c5882fb589f996b84362114fa.

package modcompat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func stringArray(values ...string) kv3.Value {
	items := make([]kv3.Value, len(values))
	for i, value := range values {
		items[i] = kv3.String(value)
	}
	return kv3.Array(items...)
}

func animationClip(t *testing.T, skeleton string) []byte {
	t.Helper()
	return compiledBlock(t, ptr(kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.String(skeleton)},
	)))
}

func animationGraphResource(t *testing.T, skeleton string, resources ...string) []byte {
	t.Helper()
	fields := []kv3.Pair{
		{Key: "m_resources", Value: stringArray(resources...)},
	}
	if skeleton != "" {
		fields = append(fields, kv3.Pair{Key: "m_skeleton", Value: kv3.String(skeleton)})
	}
	return compiledBlock(t, ptr(kv3.Object(fields...)))
}

func animationTestModel(graph string) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: stringArray("models/a.vnmskel")},
		kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array(
			kv3.Object(
				kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("default")},
				kv3.Pair{Key: "m_hGraph", Value: kv3.String(graph)},
			),
		)},
	)
}

func animationGameIndex(t *testing.T, entries []vpk.PackEntry) *source2.GameIndex {
	t.Helper()
	return gameIndexWith(t, entries)
}

func TestPermitsCameraControlsDistinguishesUnrelatedAndAffectedOverrides(t *testing.T) {
	game := animationGameIndex(t, []vpk.PackEntry{
		{Path: "animgraphs/a.vnmgraph_c", Data: animationGraphResource(t, "models/a.vnmskel")},
		{Path: "models/a.vnmskel_c", Data: compiledBlock(t, ptr(kv3.Object()))},
	})
	model := animationTestModel("animgraphs/a.vnmgraph")
	for _, test := range []struct {
		path    string
		owner   string
		data    []byte
		allowed bool
	}{
		{"animations/b.vnmclip_c", "other", animationClip(t, "models/b.vnmskel"), true},
		{"animations/b.vnmclip_c", "other", animationClip(t, "models/a.vnmskel"), false},
		{"models/a.vnmskel_c", "other", compiledBlock(t, ptr(kv3.Object())), false},
		{"animgraphs/a.vnmgraph_c", "other", animationGraphResource(t, "models/a.vnmskel"), false},
		{"animations/b.vanim_c", "other", animationClip(t, "models/b.vnmskel"), false},
		{"animations/b.vnmclip_c", "skin", animationClip(t, "models/b.vnmskel"), false},
	} {
		other := packMod(t, []vpk.PackEntry{{Path: test.path, Data: test.data}})
		mods := []ModFiles{
			{ID: "skin", VPKs: nil},
			{ID: test.owner, VPKs: []string{other}},
		}
		if test.owner == "skin" {
			mods = []ModFiles{{ID: "skin", VPKs: []string{other}}}
		}
		scopes, err := Scopes(mods)
		if err != nil {
			t.Fatal(err)
		}
		modPaths := map[string]struct{}{}
		if test.owner == "skin" {
			modPaths[camera.NormalizePath(test.path)] = struct{}{}
		}
		inputs := newAnimationInputs(scopes["skin"], "skin", modPaths)
		resolver := newAnimationResolver(scopes["skin"], "skin", nil, game)
		if got := inputs.permitsCameraControls(&model, "skin", resolver); got != test.allowed {
			t.Fatalf("%s:%s = %t, want %t", test.owner, test.path, got, test.allowed)
		}
		// Losing a replaced skeleton is a separate proof; restoring bindings is
		// blocked for every affected override except the skeleton itself.
		skeletonReplaced := test.path == "models/a.vnmskel_c"
		if got := inputs.blocksModelBindings(&model, "skin", resolver); got != (!test.allowed && !skeletonReplaced) {
			t.Fatalf("%s:%s blocked = %t", test.owner, test.path, got)
		}
	}
}

func TestUnrelatedAcceptsSkeletonBoundGraphsWithoutChildResources(t *testing.T) {
	eventGraph := animationGraphResource(t, "models/a.vnmskel")
	rootGraph := animationGraphResource(t, "", "animgraphs/events.vnmgraph")
	game := animationGameIndex(t, []vpk.PackEntry{
		{Path: "animgraphs/a.vnmgraph_c", Data: rootGraph},
		{Path: "animgraphs/events.vnmgraph_c", Data: eventGraph},
	})
	other := packMod(t, []vpk.PackEntry{{
		Path: "animations/b.vnmclip_c", Data: animationClip(t, "models/b.vnmskel"),
	}})
	scopes, err := Scopes([]ModFiles{
		{ID: "skin", VPKs: nil},
		{ID: "other", VPKs: []string{other}},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := newAnimationInputs(scopes["skin"], "skin", nil)
	resolver := newAnimationResolver(scopes["skin"], "skin", nil, game)
	model := animationTestModel("animgraphs/a.vnmgraph")
	if !inputs.unrelated(&model, "models/a.vnmskel_c", resolver) {
		t.Fatal("an override bound to another skeleton was treated as related")
	}
}

func TestUnrelatedRequiresBindingsForMissingClipsAndRejectsAffectedOverrides(t *testing.T) {
	baseClip := animationClip(t, "models/a.vnmskel")
	baseGraph := animationGraphResource(t, "", "animations/a.vnmclip")
	unrelatedClip := animationClip(t, "models/b.vnmskel")
	affectedLeaf := animationGraphResource(t, "models/a.vnmskel")
	model := animationTestModel("animgraphs/a.vnmgraph")
	for _, test := range []struct {
		name    string
		entries []vpk.PackEntry
		graph   []vpk.PackEntry
		allowed bool
	}{
		{
			name: "unrelated", entries: []vpk.PackEntry{
				{Path: "animations/b.vnmclip_c", Data: unrelatedClip},
			}, graph: []vpk.PackEntry{
				{Path: "animgraphs/a.vnmgraph_c", Data: baseGraph},
				{Path: "animations/a.vnmclip_c", Data: baseClip},
			}, allowed: true,
		},
		{
			name: "affected_leaf", entries: []vpk.PackEntry{
				{Path: "animgraphs/leaf.vnmgraph_c", Data: affectedLeaf},
			}, allowed: false,
		},
		{
			name: "missing_clip", entries: []vpk.PackEntry{
				{Path: "animations/b.vnmclip_c", Data: unrelatedClip},
			}, graph: []vpk.PackEntry{
				{Path: "animgraphs/a.vnmgraph_c", Data: animationGraphResource(t, "", "animations/missing.vnmclip")},
			}, allowed: false,
		},
		{
			name: "malformed", entries: []vpk.PackEntry{
				{Path: "animations/b.vnmclip_c", Data: []byte("truncated")},
			}, allowed: false,
		},
		{
			name: "legacy_clip", entries: []vpk.PackEntry{
				{Path: "animations/b.vanim_c", Data: unrelatedClip},
			}, allowed: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gameEntries := test.graph
			if len(gameEntries) == 0 {
				gameEntries = []vpk.PackEntry{{
					Path: "animgraphs/a.vnmgraph_c", Data: baseGraph,
				}, {
					Path: "animations/a.vnmclip_c", Data: baseClip,
				}}
			} else if test.name == "missing_clip" {
				gameEntries = append(gameEntries, vpk.PackEntry{Path: "animations/a.vnmclip_c", Data: baseClip})
			}
			game := animationGameIndex(t, gameEntries)
			other := packMod(t, test.entries)
			scopes, err := Scopes([]ModFiles{
				{ID: "skin", VPKs: nil},
				{ID: "other", VPKs: []string{other}},
			})
			if err != nil {
				t.Fatal(err)
			}
			inputs := newAnimationInputs(scopes["skin"], "skin", nil)
			resolver := newAnimationResolver(scopes["skin"], "skin", nil, game)
			if got := inputs.unrelated(&model, "models/a.vnmskel_c", resolver); got != test.allowed {
				t.Fatalf("unrelated = %t, want %t", got, test.allowed)
			}
		})
	}
}

func TestAnimationScopeRecordsTheWinningOwner(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, path string) string {
		data, err := vpk.Pack([]vpk.PackEntry{{Path: path, Data: []byte("x")}})
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, name)
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return target
	}
	first := write("first_dir.vpk", "animations/a.vnmclip_c")
	second := write("second_dir.vpk", "animations/a.vnmclip_c")
	scopes, err := Scopes([]ModFiles{
		{ID: "first", VPKs: []string{first}},
		{ID: "second", VPKs: []string{second}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scopes["second"].OtherAnimationOwners["animations/a.vnmclip_c"]; got != "first" {
		t.Fatalf("owner = %q", got)
	}
	if got := scopes["second"].AnimationSources["animations/a.vnmclip_c"].ModID; got != "first" {
		t.Fatalf("source = %q", got)
	}
}
