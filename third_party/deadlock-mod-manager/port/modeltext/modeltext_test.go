// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_text.rs
// tests at 41afe0e706726f7c5882fb589f996b84362114fa.

package modeltext

import (
	"strings"
	"testing"
)

func TestScanHandlesNestedValuesQuotesCommentsAndResourceNames(t *testing.T) {
	text := `<!-- kv3 format:version{ignored-header-braces} -->
{
  Existing = { name = "escaped \" }" resource = resource:"path/{not_a_block}" /* } */ }
  // CitadelCameraSettings_t = { bogus = true }
  CitadelCameraSettings_t = { offset = [ -10.0, 0.0, 1.0 ] }
  AttachmentCameraData = [ { attachment_name = "near_00" } ]
}
`
	closing, fields, ok := RootFields(text)
	if !ok {
		t.Fatal("root_fields declined valid text")
	}
	if text[closing] != '}' {
		t.Fatalf("closing byte = %q", text[closing])
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %d, want 3", len(fields))
	}
	r, ok := Field(fields, "AttachmentCameraData")
	if !ok {
		t.Fatal("AttachmentCameraData missing")
	}
	if got := text[r.Start:r.End]; got != `AttachmentCameraData = [ { attachment_name = "near_00" } ]` {
		t.Fatalf("field text = %q", got)
	}
	value, ok := AssignedValue(text[r.Start:r.End])
	if !ok {
		t.Fatal("assigned value missing")
	}
	items, ok := ArrayItems(value)
	if !ok || len(items) != 1 || items[0] != `{ attachment_name = "near_00" }` {
		t.Fatalf("items = %#v, %v", items, ok)
	}
	if name, ok := ResourceName(` resource:"scripts/a.vdata" `); !ok || name != "scripts/a.vdata" {
		t.Fatalf("resource name = %q, %v", name, ok)
	}
	if _, ok := ResourceName(`"scripts/a.vdata"`); ok {
		t.Fatal("plain string accepted as resource")
	}
}

func TestScanDeclinesMalformedAmbiguousAndTrailingText(t *testing.T) {
	for _, text := range []string{
		"{ a = {",
		"{ a = [ } }",
		`{ a = "unterminated }`,
		"{ a = {} a = {} }",
		"{ a = {} A = {} }",
		"{ a = 1 } b = 2",
		"{ a = \"\"\"multi\nline\"\"\" }",
		"prefix --> { a = 1 }",
	} {
		if _, _, ok := RootFields(text); ok {
			t.Fatalf("accepted malformed text: %s", text)
		}
	}
	if _, _, ok := RootFields("{ a = 1 } // trailing comment\n"); !ok {
		t.Fatal("declined trailing comment")
	}
	nested := "{ a = " + strings.Repeat("[", 100) + strings.Repeat("]", 100) + " }"
	if _, _, ok := RootFields(nested); ok {
		t.Fatal("accepted over-deep nesting")
	}
	if _, ok := ArrayItems("[ 1 2 ] 3"); ok {
		t.Fatal("accepted trailing array text")
	}
}
