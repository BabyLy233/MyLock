// SPDX-FileCopyrightText: 2026 esoc
// SPDX-FileCopyrightText: 2015 ValveResourceFormat Contributors
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// packages/vpkmanager/src/source2/resource/mod.rs (tests) at
// 41afe0e706726f7c5882fb589f996b84362114fa.

package resource

import (
	"bytes"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func build(blocks ...[2]any) []byte {
	built := make([]BuildResult, 0, len(blocks))
	for _, b := range blocks {
		built = append(built, BuildResult{Kind: b[0].([4]byte), Payload: b[1].([]byte)})
	}
	out, err := Build(built, nil, 0)
	if err != nil {
		panic(err)
	}
	return out
}

func TestRebuildWithBlockSwapsOneAndPreservesOthers(t *testing.T) {
	raw := build(
		[2]any{[4]byte{'A', 'A', 'A', 'A'}, []byte{1, 2, 3, 4}},
		[2]any{[4]byte{'M', 'V', 'T', 'X'}, []byte{9, 9, 9, 9, 9, 9, 9, 9}},
		[2]any{[4]byte{'C', 'C', 'C', 'C'}, []byte{7, 7, 7}},
	)
	res, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	newMid := bytes.Repeat([]byte{0xAB}, 40)
	rebuilt, err := res.RebuildWithBlock(1, newMid)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if len(reparsed.Blocks()) != 3 {
		t.Fatalf("block count = %d, want 3", len(reparsed.Blocks()))
	}
	if reparsed.Blocks()[0].Kind != [4]byte{'A', 'A', 'A', 'A'} ||
		reparsed.Blocks()[1].Kind != [4]byte{'M', 'V', 'T', 'X'} ||
		reparsed.Blocks()[2].Kind != [4]byte{'C', 'C', 'C', 'C'} {
		t.Fatalf("block kinds changed: %#v", reparsed.Blocks())
	}
	if got, _ := reparsed.GetBlockByIndex(0); !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("block 0 = %x", got)
	}
	if got, _ := reparsed.GetBlockByIndex(1); !bytes.Equal(got, newMid) {
		t.Fatalf("block 1 = %x", got)
	}
	if got, _ := reparsed.GetBlockByIndex(2); !bytes.Equal(got, []byte{7, 7, 7}) {
		t.Fatalf("block 2 = %x", got)
	}
}

func TestRebuildWithBlockRejectsOutOfRange(t *testing.T) {
	raw := build([2]any{[4]byte{'D', 'A', 'T', 'A'}, []byte{0, 0, 0, 0}})
	res, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.RebuildWithBlock(5, []byte{0, 0, 0, 0}); err == nil {
		t.Fatal("out-of-range rebuild succeeded")
	}
}

func TestPreservingRebuildRefusesStreamTails(t *testing.T) {
	raw := build(
		[2]any{[4]byte{'D', 'A', 'T', 'A'}, []byte("payload")},
		[2]any{[4]byte{'R', 'E', 'R', 'L'}, []byte("references")},
	)
	res, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := res.RebuildWithDataPreserving([]byte("edited"))
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reparsed.FindBlock([4]byte{'R', 'E', 'R', 'L'}); !ok || string(got) != "references" {
		t.Fatalf("RERL block = %q, %v", got, ok)
	}

	tail := append(append([]byte(nil), raw...), []byte("unrepresented stream")...)
	res, err = Parse(tail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.RebuildWithDataPreserving([]byte("edited")); err == nil {
		t.Fatal("rebuild with stream tail succeeded")
	}

	duplicates := build(
		[2]any{[4]byte{'D', 'A', 'T', 'A'}, []byte("one")},
		[2]any{[4]byte{'D', 'A', 'T', 'A'}, []byte("two")},
	)
	res, err = Parse(duplicates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.RebuildWithDataPreserving([]byte("edited")); err == nil {
		t.Fatal("rebuild with duplicate DATA succeeded")
	}
}

func TestTruncatedContainersDoNotPanic(t *testing.T) {
	raw := build(
		[2]any{[4]byte{'D', 'A', 'T', 'A'}, []byte("payload")},
		[2]any{[4]byte{'A', 'N', 'I', 'M'}, []byte("animation")},
	)
	for size := 0; size < len(raw); size++ {
		res, err := Parse(raw[:size])
		if err != nil {
			continue
		}
		for i := range res.Blocks() {
			if _, ok := res.GetBlockByIndex(i); !ok {
				t.Fatalf("size %d: block %d unreadable after successful parse", size, i)
			}
		}
	}
	malformed := append([]byte(nil), raw...)
	malformed[12] = 0xFF
	malformed[13] = 0xFF
	malformed[14] = 0xFF
	malformed[15] = 0xFF
	if _, err := Parse(malformed); err == nil {
		t.Fatal("huge block count parsed")
	}
}

func TestRebuildKeepsOpaqueLegacyBlocks(t *testing.T) {
	valid := kv3.Encode(&kv3.Value{Kind: kv3.KindObject, Object: []kv3.Pair{{Key: "a", Value: kv3.Int(1)}}}, kv3.Format{})
	legacy := []byte{0x56, 0x4B, 0x56, 0x03}
	raw := build(
		[2]any{[4]byte{'D', 'A', 'T', 'A'}, valid},
		[2]any{[4]byte{'A', 'N', 'I', 'M'}, legacy},
	)
	res, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := res.RebuildWithBlock(0, valid)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reparsed.FindBlock([4]byte{'A', 'N', 'I', 'M'}); !ok || !bytes.Equal(got, legacy) {
		t.Fatalf("legacy block = %x, %v", got, ok)
	}
}
