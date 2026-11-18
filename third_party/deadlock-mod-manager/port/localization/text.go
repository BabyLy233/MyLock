// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay.rs (localization
// parsing and rendering) at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../NOTICE.md.

// Package localization parses and merges Source 2 localization token files.
package localization

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// File is one parsed localization file. Tokens are stored by their authored
// key; the merge compares keys case-insensitively.
type File struct {
	Language string
	Tokens   map[string]string
}

// Parsed is a parsed file plus the line numbers the fallback parser skipped.
type Parsed struct {
	File         File
	SkippedLines []int
}

// Keys returns the token keys in byte order, matching the original BTreeMap.
func (f *File) Keys() []string {
	keys := make([]string, 0, len(f.Tokens))
	for key := range f.Tokens {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Lookup returns the token value by a case-insensitive key, preferring the
// first key in byte order when case variants exist.
func (f *File) Lookup(token string) (string, bool) {
	for _, key := range f.Keys() {
		if strings.EqualFold(key, token) {
			return f.Tokens[key], true
		}
	}
	return "", false
}

// Set replaces a token by a case-insensitive key, keeping the authored key.
func (f *File) Set(token, value string) {
	for _, key := range f.Keys() {
		if strings.EqualFold(key, token) {
			f.Tokens[key] = value
			return
		}
	}
	f.Tokens[token] = value
}

// InferLanguage reads the language suffix from the file name.
func InferLanguage(filePath string) string {
	stem := strings.TrimSuffix(path.Base(strings.ReplaceAll(filePath, "\\", "/")), path.Ext(filePath))
	if index := strings.LastIndexByte(stem, '_'); index >= 0 {
		return stem[index+1:]
	}
	return stem
}

// ParseBytes decodes and parses a localization file. It accepts UTF-8 and
// BOM-marked UTF-16, and falls back to a line parser for files the KeyValues
// parser rejects.
func ParseBytes(bytes []byte, source string) (Parsed, error) {
	text, ok := decodeText(bytes)
	if !ok {
		return Parsed{}, fmt.Errorf("%s is not UTF-8 or UTF-16 text", source)
	}
	text = strings.TrimPrefix(text, "\ufeff")
	parsed, outcome, root := parseKeyValues(text)
	switch outcome {
	case keyValuesOK:
		if parsed.File.Language == "" {
			parsed.File.Language = InferLanguage(source)
		}
		return parsed, nil
	case keyValuesWrongRoot:
		return Parsed{}, fmt.Errorf("Localization file %s has root %q, expected lang", source, root)
	case keyValuesInvalidStructure:
		return Parsed{}, fmt.Errorf("Localization file %s has no Tokens object", source)
	}
	return parseLines(text, source)
}

func decodeText(bytes []byte) (string, bool) {
	decodeUnits := func(units []byte, bigEndian bool) (string, bool) {
		if len(units)%2 != 0 {
			return "", false
		}
		values := make([]uint16, 0, len(units)/2)
		for index := 0; index < len(units); index += 2 {
			if bigEndian {
				values = append(values, uint16(units[index])<<8|uint16(units[index+1]))
			} else {
				values = append(values, uint16(units[index+1])<<8|uint16(units[index]))
			}
		}
		return string(utf16.Decode(values)), true
	}
	switch {
	case len(bytes) >= 2 && bytes[0] == 0xFF && bytes[1] == 0xFE:
		return decodeUnits(bytes[2:], false)
	case len(bytes) >= 2 && bytes[0] == 0xFE && bytes[1] == 0xFF:
		return decodeUnits(bytes[2:], true)
	}
	if !utf8.Valid(bytes) {
		return "", false
	}
	return string(bytes), true
}

type kvValue struct {
	text     string
	object   []kvPair
	isObject bool
}

type kvPair struct {
	key   string
	value kvValue
}

type kvToken struct {
	kind byte // 's' string, '{', '}'
	text string
}

type keyValuesOutcome uint8

const (
	keyValuesOK keyValuesOutcome = iota
	keyValuesSyntax
	keyValuesWrongRoot
	keyValuesInvalidStructure
)

func parseKeyValues(text string) (Parsed, keyValuesOutcome, string) {
	tokens, ok := tokenizeKeyValues(text)
	if !ok || len(tokens) < 2 || tokens[0].kind != 's' {
		return Parsed{}, keyValuesSyntax, ""
	}
	root, position, ok := parsePair(tokens, 0)
	if !ok || position != len(tokens) {
		return Parsed{}, keyValuesSyntax, ""
	}
	if !strings.EqualFold(root.key, "lang") {
		return Parsed{}, keyValuesWrongRoot, root.key
	}
	if !root.value.isObject {
		return Parsed{}, keyValuesInvalidStructure, root.key
	}
	language := ""
	for _, pair := range root.value.object {
		if strings.EqualFold(pair.key, "Language") && !pair.value.isObject {
			language = pair.value.text
			break
		}
	}
	tokensObject := (*kvValue)(nil)
	for index := range root.value.object {
		if strings.EqualFold(root.value.object[index].key, "Tokens") &&
			root.value.object[index].value.isObject {
			tokensObject = &root.value.object[index].value
			break
		}
	}
	if tokensObject == nil {
		return Parsed{}, keyValuesInvalidStructure, root.key
	}
	localization := File{Language: language, Tokens: make(map[string]string)}
	for _, pair := range tokensObject.object {
		if !pair.value.isObject {
			localization.Tokens[pair.key] = pair.value.text
		}
	}
	return Parsed{File: localization}, keyValuesOK, root.key
}

func parsePair(tokens []kvToken, position int) (kvPair, int, bool) {
	if position >= len(tokens) || tokens[position].kind != 's' {
		return kvPair{}, position, false
	}
	pair := kvPair{key: tokens[position].text}
	position++
	if position >= len(tokens) {
		return kvPair{}, position, false
	}
	if tokens[position].kind == '{' {
		object, next, ok := parseObject(tokens, position)
		if !ok {
			return kvPair{}, position, false
		}
		pair.value = kvValue{object: object, isObject: true}
		return pair, next, true
	}
	if tokens[position].kind != 's' {
		return kvPair{}, position, false
	}
	pair.value = kvValue{text: tokens[position].text}
	return pair, position + 1, true
}

func parseObject(tokens []kvToken, position int) ([]kvPair, int, bool) {
	position++ // '{'
	var pairs []kvPair
	for position < len(tokens) && tokens[position].kind != '}' {
		pair, next, ok := parsePair(tokens, position)
		if !ok {
			return nil, position, false
		}
		pairs = append(pairs, pair)
		position = next
	}
	if position >= len(tokens) {
		return nil, position, false
	}
	return pairs, position + 1, true
}

func tokenizeKeyValues(text string) ([]kvToken, bool) {
	var tokens []kvToken
	for index := 0; index < len(text); {
		char := text[index]
		switch {
		case char == ' ' || char == '\t' || char == '\r' || char == '\n':
			index++
		case char == '/' && index+1 < len(text) && text[index+1] == '/':
			for index < len(text) && text[index] != '\n' {
				index++
			}
		case char == '{' || char == '}':
			tokens = append(tokens, kvToken{kind: char})
			index++
		case char == '"':
			var builder strings.Builder
			index++
			closed := false
			for index < len(text) {
				if text[index] == '"' {
					closed = true
					index++
					break
				}
				if text[index] == '\\' {
					if index+1 >= len(text) {
						return nil, false
					}
					escaped := text[index+1]
					switch escaped {
					case 'n':
						builder.WriteByte('\n')
					case 'r':
						builder.WriteByte('\r')
					case 't':
						builder.WriteByte('\t')
					default:
						builder.WriteByte(escaped)
					}
					index += 2
					continue
				}
				builder.WriteByte(text[index])
				index++
			}
			if !closed {
				return nil, false
			}
			tokens = append(tokens, kvToken{kind: 's', text: builder.String()})
		default:
			start := index
			for index < len(text) && !strings.ContainsRune(" \t\r\n{}\"", rune(text[index])) {
				index++
			}
			tokens = append(tokens, kvToken{kind: 's', text: text[start:index]})
		}
	}
	return tokens, true
}

func parseLines(text, source string) (Parsed, error) {
	localization := File{Language: InferLanguage(source), Tokens: make(map[string]string)}
	var skipped []int
	foundTokens := false
	inTokens := false
	for index, line := range strings.Split(text, "\n") {
		lineNumber := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if !inTokens {
			fields, err := parseQuotedFields(trimmed)
			if err == nil {
				if len(fields) >= 2 && strings.EqualFold(fields[0], "Language") {
					localization.Language = fields[1]
				} else if len(fields) == 1 && strings.EqualFold(fields[0], "Tokens") {
					foundTokens = true
				}
			}
			if foundTokens && strings.Contains(trimmed, "{") {
				inTokens = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "}") {
			break
		}
		fields, err := parseQuotedFields(trimmed)
		if err == nil && len(fields) >= 2 {
			localization.Tokens[fields[0]] = fields[1]
			continue
		}
		skipped = append(skipped, lineNumber)
	}
	if !foundTokens || !inTokens {
		return Parsed{}, fmt.Errorf("Localization file %s has no readable Tokens object", source)
	}
	return Parsed{File: localization, SkippedLines: skipped}, nil
}

func parseQuotedFields(line string) ([]string, error) {
	var fields []string
	for index := 0; index < len(line); {
		if line[index] == '/' && index+1 < len(line) && line[index+1] == '/' {
			break
		}
		if line[index] != '"' {
			index++
			continue
		}
		index++
		var builder strings.Builder
		closed := false
		for index < len(line) {
			char := line[index]
			if char == '"' {
				closed = true
				index++
				break
			}
			if char == '\\' {
				if index+1 >= len(line) {
					return nil, fmt.Errorf("unterminated escape")
				}
				switch line[index+1] {
				case 'n':
					builder.WriteByte('\n')
				case 'r':
					builder.WriteByte('\r')
				case 't':
					builder.WriteByte('\t')
				default:
					builder.WriteByte(line[index+1])
				}
				index += 2
				continue
			}
			builder.WriteByte(char)
			index++
		}
		if !closed {
			return nil, fmt.Errorf("unterminated string")
		}
		fields = append(fields, builder.String())
	}
	return fields, nil
}

// Render writes the canonical localization format: a UTF-8 BOM, CRLF line
// endings and tokens in byte order.
func Render(file *File) []byte {
	var output strings.Builder
	output.WriteString("\ufeff\"lang\"\r\n{\r\n\t\"Language\"\t\t\"")
	output.WriteString(escapeKeyValues(file.Language))
	output.WriteString("\"\r\n\t\"Tokens\"\r\n\t{\r\n")
	for _, token := range file.Keys() {
		output.WriteString("\t\t\"")
		output.WriteString(escapeKeyValues(token))
		output.WriteString("\"\t\t\"")
		output.WriteString(escapeKeyValues(file.Tokens[token]))
		output.WriteString("\"\r\n")
	}
	output.WriteString("\t}\r\n}\r\n")
	return []byte(output.String())
}

func escapeKeyValues(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	return strings.ReplaceAll(value, "\t", "\\t")
}
