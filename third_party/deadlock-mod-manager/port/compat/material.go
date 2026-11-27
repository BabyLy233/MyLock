// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/material_compatibility/interface.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../../NOTICE.md.

package compat

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// MaterialDifferences compares a mod material with the current game material.
// It only reports differences that leave the shader interface unverified; it
// never proposes replacing authored parameters.
func MaterialDifferences(source, current *kv3.Value) ([]string, error) {
	sourceShader, err := materialShader(source)
	if err != nil {
		return nil, err
	}
	currentShader, err := materialShader(current)
	if err != nil {
		return nil, err
	}
	if sourceShader != currentShader {
		return []string{fmt.Sprintf(
			"Authored shader %s differs from current reference %s; parameter correspondence is unverified",
			sourceShader, currentShader)}, nil
	}

	sourceTextures, err := named(source, "m_textureParams", "m_pValue", false)
	if err != nil {
		return nil, err
	}
	currentTextures, err := named(current, "m_textureParams", "m_pValue", false)
	if err != nil {
		return nil, err
	}
	sourceFeatures, err := named(source, "m_intParams", "m_nValue", true)
	if err != nil {
		return nil, err
	}
	currentFeatures, err := named(current, "m_intParams", "m_nValue", true)
	if err != nil {
		return nil, err
	}

	sameFeatures := true
	for _, name := range unionKeys(sourceFeatures, currentFeatures) {
		if featureInt(sourceFeatures[name]) != featureInt(currentFeatures[name]) {
			sameFeatures = false
			break
		}
	}
	var details []string
	if sameFeatures {
		for _, name := range sortedKeys(currentTextures) {
			if _, ok := sourceTextures[name]; !ok {
				details = append(details, fmt.Sprintf(
					"Current reference texture slot is absent: m_textureParams.%s; enabled shader features match, but a replacement requires additional evidence",
					name))
			}
		}
	}
	for _, name := range sortedKeys(sourceFeatures) {
		value, ok := sourceFeatures[name].AsInt()
		if !ok || value == 0 {
			continue
		}
		// A feature the current reference does not list at all is a difference:
		// its requirements are unknown, not disabled.
		currentValue, currentOK := int64(0), false
		if current, present := currentFeatures[name]; present && current != nil {
			currentValue, currentOK = current.AsInt()
		}
		if !currentOK || currentValue != value {
			details = append(details, fmt.Sprintf(
				"Authored shader feature differs: m_intParams.%s; current reference does not enable the same value. This may be intentional; its texture requirements are unverified",
				name))
		}
	}
	return details, nil
}

func materialShader(value *kv3.Value) (string, error) {
	nameValue, ok := value.Get("m_shaderName")
	if !ok {
		return "", fmt.Errorf("Missing or invalid material shader name")
	}
	name, ok := nameValue.AsString()
	if !ok || name == "" {
		return "", fmt.Errorf("Missing or invalid material shader name")
	}
	return name, nil
}

func named(material *kv3.Value, table, valueKey string, featuresOnly bool) (map[string]*kv3.Value, error) {
	tableValue, ok := material.Get(table)
	if !ok {
		return nil, fmt.Errorf("Missing or invalid parameter table: %s", table)
	}
	entries, ok := tableValue.AsArray()
	if !ok {
		return nil, fmt.Errorf("Missing or invalid parameter table: %s", table)
	}
	result := make(map[string]*kv3.Value, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		entry := &entries[i]
		nameValue, ok := entry.Get("m_name")
		if !ok {
			return nil, fmt.Errorf("Invalid parameter name in %s", table)
		}
		name, ok := nameValue.AsString()
		if !ok || name == "" {
			return nil, fmt.Errorf("Invalid parameter name in %s", table)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("Duplicate material parameter: %s.%s", table, name)
		}
		seen[name] = struct{}{}
		value, ok := entry.Get(valueKey)
		if !ok {
			return nil, fmt.Errorf("Missing parameter value: %s.%s.%s", table, name, valueKey)
		}
		if featuresOnly {
			if _, ok := value.AsInt(); !ok {
				return nil, fmt.Errorf("Invalid parameter value: %s.%s.%s", table, name, valueKey)
			}
		} else {
			texture, ok := value.AsString()
			if !ok || texture == "" {
				return nil, fmt.Errorf("Invalid parameter value: %s.%s.%s", table, name, valueKey)
			}
		}
		if !featuresOnly || strings.HasPrefix(name, "F_") {
			result[name] = value
		}
	}
	return result, nil
}

func featureInt(value *kv3.Value) int64 {
	if value == nil {
		return 0
	}
	n, _ := value.AsInt()
	return n
}

func unionKeys(a, b map[string]*kv3.Value) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		set[key] = struct{}{}
	}
	for key := range b {
		set[key] = struct{}{}
	}
	return sortedKeys(set)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
