// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/resource_id.rs
// (tests) at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package resource

import "testing"

func TestResourceIDMatchesReferenceHashesForEveryTailLength(t *testing.T) {
	vectors := []struct {
		path string
		want uint64
	}{
		{"a.vmat", 0xe2c938c2debbd292},
		{"ab.vmat", 0xa8560f3a6ee5e6ef},
		{"abc.vmat", 0x55ee217bb53c57ed},
		{"abcd.vmat", 0x00880fc0681372d9},
		{"abcde.vmat", 0xc2cd36fcc77d25fd},
		{"abcdef.vmat", 0x5fa8c3ef679b0f77},
		{"abcdefg.vmat", 0x13c8af424ec4cd25},
		{"abcdefgh.vmat", 0x67fdbac5c679fe66},
	}
	for _, vector := range vectors {
		got, err := ResourceID(vector.path)
		if err != nil {
			t.Fatalf("%s: %v", vector.path, err)
		}
		if got != vector.want {
			t.Fatalf("%s = %#x, want %#x", vector.path, got, vector.want)
		}
	}
}

func TestResourceIDMatchesInstalledDeadlockResources(t *testing.T) {
	vectors := []struct {
		path string
		want uint64
	}{
		{"models/npc/trooper/materials/candle_trooper_body.vmat", 0x34f4fa62067f3ed9},
		{"materials/default/default_black_mask_tga_e7be3cc.vtex", 0x1fb058b82ca87a2d},
		{"materials/default/default_color_tga_96b3aa03.vtex", 0xb4aab7e7abcf3e44},
		{"materials/default/default_mask_tga_344101f8.vtex", 0xb75c378598977cd0},
		{"panorama/images/hud/abilities/ratking/ratking_nibble_psd.vtex", 0xe8a2fcce188abbe0},
	}
	for _, vector := range vectors {
		got, err := ResourceID(vector.path)
		if err != nil {
			t.Fatalf("%s: %v", vector.path, err)
		}
		if got != vector.want {
			t.Fatalf("%s = %#x, want %#x", vector.path, got, vector.want)
		}
	}
}

func TestResourceIDRejectsNamesThatNeedNormalization(t *testing.T) {
	for _, path := range []string{
		"",
		"a",
		"a.",
		".vmat",
		"/a.vmat",
		"a//b.vmat",
		"./a.vmat",
		"../a.vmat",
		"a/../b.vmat",
		"a/./b.vmat",
		"A.vmat",
		"a\\b.vmat",
		"c:/a.vmat",
		"a.vmat_c",
		"a\x00.vmat",
		"a\n.vmat",
		"á.vmat",
	} {
		if _, err := ResourceID(path); err == nil {
			t.Fatalf("%q accepted", path)
		}
	}
}
