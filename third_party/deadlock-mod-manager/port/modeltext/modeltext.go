// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/model_text.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

// Package modeltext scans the authored text stored in
// m_modelInfo.m_keyValueText. It is not a KV3 serializer: it preserves the
// original slices and declines syntax it does not understand.
package modeltext

import (
	"encoding/json"
	"strings"
)

// Range is a byte range in the original text.
type Range struct {
	Start int
	End   int
}

// RootFields returns the byte offset of the closing brace and the range of each
// top-level `name = value` assignment. A KV3 header comment is recognized only
// at the start of the text, and only whitespace or comments may follow the
// closing brace. Names that differ only in case decline the scan.
func RootFields(text string) (int, map[string]Range, bool) {
	c := textCursor{text: []byte(text)}
	if !c.space() {
		return 0, nil, false
	}
	if strings.HasPrefix(text[c.position:], "<!--") {
		rest := text[c.position:]
		idx := strings.Index(rest, "-->")
		if idx < 0 {
			return 0, nil, false
		}
		c.position += idx + 3
	}
	if !c.space() || !c.expect('{') {
		return 0, nil, false
	}
	fields := make(map[string]Range)
	names := make(map[string]struct{})
	for {
		if !c.space() {
			return 0, nil, false
		}
		peek, ok := c.peek()
		if !ok {
			return 0, nil, false
		}
		if peek == '}' {
			closing := c.position
			c.position++
			if !c.space() {
				return 0, nil, false
			}
			if _, ok := c.peek(); ok {
				return 0, nil, false
			}
			return closing, fields, true
		}
		start := c.position
		for {
			b, ok := c.peek()
			if !ok || !(isAlphaNumeric(b) || b == '_' || b == '.') {
				break
			}
			c.position++
		}
		if start == c.position {
			return 0, nil, false
		}
		name := text[start:c.position]
		if !isASCII(name) {
			return 0, nil, false
		}
		if !c.space() || !c.expect('=') || !c.space() || !c.value(0) {
			return 0, nil, false
		}
		if _, dup := names[strings.ToLower(name)]; dup {
			return 0, nil, false
		}
		names[strings.ToLower(name)] = struct{}{}
		fields[name] = Range{Start: start, End: c.position}
	}
}

// Field returns the range of a field whose name matches case-insensitively.
func Field(fields map[string]Range, name string) (Range, bool) {
	for field, r := range fields {
		if strings.EqualFold(field, name) {
			return r, true
		}
	}
	return Range{}, false
}

// AssignedValue returns the trimmed value text of a `name = value` assignment.
func AssignedValue(assignment string) (string, bool) {
	idx := strings.IndexByte(assignment, '=')
	if idx < 0 {
		return "", false
	}
	return strings.TrimSpace(assignment[idx+1:]), true
}

// ArrayItems returns the item texts of an array value such as `[ { a = 1 } { a = 2 } ]`.
func ArrayItems(value string) ([]string, bool) {
	c := textCursor{text: []byte(value)}
	if !c.space() || !c.expect('[') {
		return nil, false
	}
	var items []string
	for {
		if !c.space() {
			return nil, false
		}
		peek, ok := c.peek()
		if !ok {
			return nil, false
		}
		if peek == ']' {
			c.position++
			break
		}
		start := c.position
		if !c.value(0) {
			return nil, false
		}
		items = append(items, value[start:c.position])
	}
	if !c.space() {
		return nil, false
	}
	if _, ok := c.peek(); ok {
		return nil, false
	}
	return items, true
}

// ResourceName returns the path of a `resource:"path"` reference.
func ResourceName(item string) (string, bool) {
	quoted := strings.TrimPrefix(strings.TrimSpace(item), "resource:")
	if quoted == item {
		return "", false
	}
	var out string
	if err := json.Unmarshal([]byte(quoted), &out); err != nil {
		return "", false
	}
	return out, true
}

type textCursor struct {
	text     []byte
	position int
}

func (c *textCursor) peek() (byte, bool) {
	if c.position < 0 || c.position >= len(c.text) {
		return 0, false
	}
	return c.text[c.position], true
}

func (c *textCursor) expect(b byte) bool {
	got, ok := c.peek()
	if !ok || got != b {
		return false
	}
	c.position++
	return true
}

func (c *textCursor) space() bool {
	for {
		for {
			b, ok := c.peek()
			if !ok || !(isASCIIWhitespace(b) || b == ',') {
				break
			}
			c.position++
		}
		if c.position+1 < len(c.text) && c.text[c.position] == '/' && c.text[c.position+1] == '/' {
			for {
				b, ok := c.peek()
				if !ok || b == '\n' {
					break
				}
				c.position++
			}
		} else if c.position+1 < len(c.text) && c.text[c.position] == '/' && c.text[c.position+1] == '*' {
			c.position += 2
			for !(c.position+1 < len(c.text) && c.text[c.position] == '*' && c.text[c.position+1] == '/') {
				if _, ok := c.peek(); !ok {
					return false
				}
				c.position++
			}
			c.position += 2
		} else {
			return true
		}
	}
}

func (c *textCursor) quoted() bool {
	if c.position+2 < len(c.text) && string(c.text[c.position:c.position+3]) == `"""` {
		return false
	}
	if !c.expect('"') {
		return false
	}
	for {
		b, ok := c.peek()
		if !ok {
			return false
		}
		switch b {
		case '"':
			c.position++
			return true
		case '\\':
			c.position++
			if _, ok := c.peek(); !ok {
				return false
			}
			c.position++
		default:
			c.position++
		}
	}
}

func (c *textCursor) value(depth int) bool {
	if depth > 64 {
		return false
	}
	b, ok := c.peek()
	if !ok {
		return false
	}
	switch b {
	case '{', '[':
		closing := byte('}')
		if b == '[' {
			closing = ']'
		}
		c.position++
		for {
			if !c.space() {
				return false
			}
			peek, ok := c.peek()
			if !ok {
				return false
			}
			if peek == closing {
				c.position++
				return true
			}
			if peek == '}' || peek == ']' {
				return false
			}
			if !c.value(depth + 1) {
				return false
			}
		}
	case '"':
		return c.quoted()
	default:
		start := c.position
		for {
			peek, ok := c.peek()
			if !ok || isASCIIWhitespace(peek) || peek == ',' || peek == '}' || peek == ']' ||
				peek == '{' || peek == '[' || peek == '"' {
				break
			}
			c.position++
		}
		if c.position == start {
			return false
		}
		if peek, ok := c.peek(); ok && peek == '"' {
			return c.quoted()
		}
		return true
	}
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func isAlphaNumeric(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
