package compat

import (
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

func material(shader string, features []kv3.Value, textures []kv3.Value) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String(shader)},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array(features...)},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array(textures...)},
	)
}

func feature(name string, value int64) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_name", Value: kv3.String(name)},
		kv3.Pair{Key: "m_nValue", Value: kv3.Int(value)},
	)
}

func texture(name, path string) kv3.Value {
	return kv3.Object(
		kv3.Pair{Key: "m_name", Value: kv3.String(name)},
		kv3.Pair{Key: "m_pValue", Value: kv3.String(path)},
	)
}

func TestMaterialDifferencesOnlyReportUnverifiedInterfaces(t *testing.T) {
	same := material("csgo_environment.vfx", []kv3.Value{feature("F_GLOW", 1)}, []kv3.Value{texture("g_tColor", "a.vtex")})
	details, err := MaterialDifferences(&same, &same)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 0 {
		t.Fatalf("identical material reported %v", details)
	}

	other := material("custom.vfx", []kv3.Value{feature("F_GLOW", 1)}, []kv3.Value{texture("g_tColor", "a.vtex")})
	details, err = MaterialDifferences(&same, &other)
	if err != nil || len(details) != 1 || !strings.Contains(details[0], "Authored shader") {
		t.Fatalf("shader difference = %v, %v", details, err)
	}

	current := material("csgo_environment.vfx", []kv3.Value{feature("F_GLOW", 1)},
		[]kv3.Value{texture("g_tColor", "a.vtex"), texture("g_tReveal", "b.vtex")})
	details, err = MaterialDifferences(&same, &current)
	if err != nil || len(details) != 1 || !strings.Contains(details[0], "g_tReveal") {
		t.Fatalf("texture gap = %v, %v", details, err)
	}

	enabled := material("csgo_environment.vfx", []kv3.Value{feature("F_GLOW", 2)}, []kv3.Value{texture("g_tColor", "a.vtex")})
	details, err = MaterialDifferences(&same, &enabled)
	if err != nil || len(details) != 1 || !strings.Contains(details[0], "F_GLOW") {
		t.Fatalf("feature difference = %v, %v", details, err)
	}

	// The same feature turned off in the mod is not a reported mismatch.
	disabled := material("csgo_environment.vfx", []kv3.Value{feature("F_GLOW", 0)}, []kv3.Value{texture("g_tColor", "a.vtex")})
	details, err = MaterialDifferences(&disabled, &enabled)
	if err != nil || len(details) != 0 {
		t.Fatalf("disabled feature = %v, %v", details, err)
	}
}

func TestMaterialDifferencesHandleFeatureMissingFromReference(t *testing.T) {
	authored := material("csgo_environment.vfx",
		[]kv3.Value{feature("F_GLOW", 1), feature("F_RIM", 2)},
		[]kv3.Value{texture("g_tColor", "a.vtex")})
	// The current material lists only one of the authored features. A missing
	// entry must be reported, not dereferenced.
	reference := material("csgo_environment.vfx",
		[]kv3.Value{feature("F_GLOW", 1)},
		[]kv3.Value{texture("g_tColor", "a.vtex")})
	details, err := MaterialDifferences(&authored, &reference)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || !strings.Contains(details[0], "F_RIM") {
		t.Fatalf("missing feature detail = %v", details)
	}
}

func TestMaterialDifferencesRejectMalformedTables(t *testing.T) {
	noShader := kv3.Object(
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	valid := material("shader", nil, nil)
	if _, err := MaterialDifferences(&noShader, &valid); err == nil {
		t.Fatal("missing shader accepted")
	}
	duplicate := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("shader")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array(feature("F_A", 1), feature("F_A", 1))},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	if _, err := MaterialDifferences(&duplicate, &valid); err == nil {
		t.Fatal("duplicate parameter accepted")
	}
	badTexture := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("shader")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_name", Value: kv3.String("g_tColor")},
			kv3.Pair{Key: "m_pValue", Value: kv3.String("")},
		))},
	)
	if _, err := MaterialDifferences(&badTexture, &valid); err == nil {
		t.Fatal("empty texture path accepted")
	}
}
