// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager packages/vpkmanager/src/material_repair.rs
// (tests) at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package resource

import (
	"bytes"
	"testing"
)

func emptyReferences(blocks ...BuildResult) []byte {
	raw, err := Build(append(blocks, BuildResult{
		Kind:    [4]byte{'R', 'E', 'R', 'L'},
		Payload: []byte{8, 0, 0, 0, 0, 0, 0, 0},
	}), nil, 0)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestAddReferencesAppendsAndRoundTrips(t *testing.T) {
	raw := emptyReferences(BuildResult{Kind: [4]byte{'D', 'A', 'T', 'A'}, Payload: []byte("data")})
	additions := []Reference{
		{ID: 0x0102030405060708, Path: "scripts/tagged_sounds/a.vdata"},
		{ID: 0x1112131415161718, Path: "scripts/tagged_sounds/b.vdata"},
	}
	out, err := AddReferencesOf(raw, additions)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReferencesOf(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != additions[0] || got[1] != additions[1] {
		t.Fatalf("references = %#v", got)
	}
	before, _ := Parse(raw)
	after, _ := Parse(out)
	oldData, _ := before.FindBlock(BlockTypeData)
	newData, _ := after.FindBlock(BlockTypeData)
	if !bytes.Equal(oldData, newData) {
		t.Fatal("DATA changed")
	}
}

func TestAddReferencesIsIdempotentAndRejectsConflicts(t *testing.T) {
	raw := emptyReferences()
	first := Reference{ID: 7, Path: "scripts/tagged_sounds/a.vdata"}
	out, err := AddReferencesOf(raw, []Reference{first, first})
	if err != nil {
		t.Fatal(err)
	}
	references, err := ReferencesOf(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 {
		t.Fatalf("duplicate addition produced %#v", references)
	}
	if _, err := AddReferencesOf(out, []Reference{{ID: 8, Path: first.Path}}); err == nil {
		t.Fatal("conflicting resource ID accepted")
	}
	if _, err := AddReferencesOf(out, []Reference{{ID: first.ID, Path: "other.vdata"}}); err == nil {
		t.Fatal("resource ID collision accepted")
	}
}

func TestReferencesRejectMalformedTables(t *testing.T) {
	for _, payload := range [][]byte{
		{},
		{8, 0, 0, 0},
		{8, 0, 0, 0, 1, 0, 0, 0},
		{4, 0, 0, 0, 0, 0, 0, 0},
	} {
		raw, err := Build([]BuildResult{{Kind: [4]byte{'R', 'E', 'R', 'L'}, Payload: payload}}, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReferencesOf(raw); err == nil {
			t.Fatalf("payload %x accepted", payload)
		}
	}
}

func TestAddReferencesRequiresTheBlock(t *testing.T) {
	raw, err := Build([]BuildResult{{Kind: BlockTypeData, Payload: []byte("data")}}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddReferencesOf(raw, nil); err == nil {
		t.Fatal("resource without RERL accepted")
	}
}
