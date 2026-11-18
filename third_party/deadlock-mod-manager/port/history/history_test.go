// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/vdata_history.rs tests at
// 41afe0e706726f7c5882fb589f996b84362114fa, limited to the observed string
// bindings. See ../../NOTICE.md.

package history

import (
	"encoding/binary"
	"testing"
)

type builder struct{ bytes []byte }

func (b *builder) u32(value uint32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	b.bytes = append(b.bytes, raw[:]...)
}

func (b *builder) string(value string) {
	b.u32(uint32(len(value)))
	b.bytes = append(b.bytes, value...)
}

func (b *builder) fingerprint(value fingerprint) {
	b.bytes = append(b.bytes, value[:]...)
}

func (b *builder) change(key fingerprint, value *fingerprint) {
	b.fingerprint(key)
	if value == nil {
		b.bytes = append(b.bytes, 0)
		return
	}
	b.bytes = append(b.bytes, 1)
	b.fingerprint(*value)
}

func fixture(t *testing.T, version [8]byte, withLedger bool) []byte {
	t.Helper()
	const (
		file  = "scripts/heroes.vdata_c"
		row   = "hero_copper"
		field = "m_strModelName"
		value = "models/retired/body.vmdl"
	)
	fieldFingerprint := fieldKey(row, field)
	key := stringBindingKey(file, fieldFingerprint)
	valueFingerprint := stringValueFingerprint(value)
	var b builder
	b.bytes = append(b.bytes, version[:]...)
	b.u32(1)
	b.string(file)
	b.u32(1)
	b.u32(1) // build version
	b.u32(0) // row changes
	if version == exactMagic || version == magic {
		b.u32(0) // exact row changes
	}
	b.u32(1)
	b.change(fieldFingerprint, &valueFingerprint)
	if withLedger {
		b.u32(1)
		b.fingerprint(key)
		b.u32(1)
		b.fingerprint(valueFingerprint)
	}
	return b.bytes
}

func TestParseObservedStringBindings(t *testing.T) {
	for _, test := range []struct {
		name    string
		version [8]byte
		ledger  bool
	}{
		{name: "v1 legacy", version: legacyMagic},
		{name: "v2 exact", version: exactMagic},
		{name: "v3 ledger", version: magic, ledger: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			index, err := Parse(fixture(t, test.version, test.ledger))
			if err != nil {
				t.Fatal(err)
			}
			if !index.ObservedStringField("scripts/heroes.vdata_c", "hero_copper", "m_strModelName", "models/retired/body.vmdl") {
				t.Fatal("observed binding missing")
			}
			if index.ObservedStringField("scripts/other.vdata_c", "hero_copper", "m_strModelName", "models/retired/body.vmdl") {
				t.Fatal("binding leaked to another file")
			}
			if index.ObservedStringField("scripts/heroes.vdata_c", "hero_other", "m_strModelName", "models/retired/body.vmdl") {
				t.Fatal("binding leaked to another row")
			}
			if index.ObservedStringField("scripts/heroes.vdata_c", "hero_copper", "m_strModelName", "models/current/body.vmdl") {
				t.Fatal("unknown value was accepted")
			}
		})
	}
}

func TestParseRejectsMalformedHistory(t *testing.T) {
	if _, err := Parse([]byte("not a history")); err == nil {
		t.Fatal("invalid magic accepted")
	}
	valid := fixture(t, magic, true)
	for length := 0; length < len(valid)-1; length++ {
		if _, err := Parse(valid[:length]); err == nil {
			t.Fatalf("truncated history at %d accepted", length)
		}
	}
	if _, err := Parse(append(valid, 0)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
}

func TestEmbeddedHistoryKnowsArchivedHeroModels(t *testing.T) {
	index := Embedded()
	if index == nil {
		t.Fatal("embedded history is missing")
	}
	if !index.ObservedStringField("scripts/heroes.vdata_c", "hero_hornet", "m_strModelName",
		"models/heroes_staging/hornet_v3/hornet.vmdl") {
		t.Fatal("archived hornet model is missing from the learned history")
	}
	if index.ObservedStringField("scripts/heroes.vdata_c", "hero_hornet", "m_strModelName",
		"models/heroes/hornet/hornet.vmdl") {
		t.Fatal("current hornet model was reported as historical")
	}
}
