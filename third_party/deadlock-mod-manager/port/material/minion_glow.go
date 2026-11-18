// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/material_compatibility/minion_glow.rs
// and material_compatibility.rs at 41afe0e706726f7c5882fb589f996b84362114fa.
// See ../../NOTICE.md.

package material

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
)

// Snapshot resolves resources in the mod's load order. Resolve returns nil for
// a missing resource; an error means the resource exists but could not be read
// and must abort the recipe.
type Snapshot interface {
	GameBytes(path string) ([]byte, bool, error)
	Resolve(path string) ([]byte, error)
}

// recipe is the reviewed migration v1: legacy candle wax to live
// Amber/Sapphire head surfaces. Current unit/model paths and role names come
// from npc_units, never mod IDs.
const (
	recipe       = "minion-static-glow-v1"
	oldModel     = "models/npc/trooper/trooper_humanoid.vmdl_c"
	sourcePrefix = "models/npc/trooper/materials/candle_trooper_wax_"
	whiteMask    = "materials/default/default_mask_tga_344101f8.vtex"
	unitsPath    = "scripts/npc_units.vdata_c"
)

var sourceRoles = [...]struct {
	field string
	role  string
}{
	{"m_sDefaultMaterialGroupName", "friendly"},
	{"m_sEnemyMaterialGroupName", "enemy"},
	{"m_sTeam1MaterialGroupName", "amber"},
	{"m_sTeam2MaterialGroupName", "sapphire"},
}

var headGroups = [...]string{
	"head_health_@0_#&high",
	"head_health_@1_#&mid",
	"head_health_@2_#&low",
}

// SourceRole returns the role of a legacy wax material path.
func SourceRole(path string) (string, bool) {
	for _, role := range sourceRoles {
		if path == sourcePrefix+role.role+".vmat_c" {
			return role.role, true
		}
	}
	return "", false
}

type glowTarget struct {
	key        string
	material   string
	modelField string
	units      []string
	isolate    bool
}

var glowTargets = [...]glowTarget{
	{
		key:        "amber",
		material:   "models/npc_units/troopers/amber_trooper_01/materials/amber_trooper_01_wax.vmat",
		modelField: "m_sAmberModelName",
		units:      []string{"trooper_normal", "trooper_medic", "trooper_melee"},
	},
	{
		key:        "sapphire",
		material:   "models/npc_units/troopers/sapphire_trooper_01/materials/sapphire_trooper_01.vmat",
		modelField: "m_sSapphireModelName",
		units:      []string{"trooper_normal", "trooper_medic"},
		isolate:    true,
	},
	{
		key:        "sapphire_melee",
		material:   "models/npc_units/troopers/sapphire_trooper_01_melee/materials/sapphire_trooper_melee_head.vmat",
		modelField: "m_sSapphireModelName",
		units:      []string{"trooper_melee"},
	},
}

// BuildMinionGlow runs the reviewed static-glow migration. paths are the mod's
// normalized legacy wax material paths; all four roles must be present.
func BuildMinionGlow(snapshot Snapshot, paths []string) (map[string][]byte, error) {
	if len(paths) != len(sourceRoles) {
		return nil, fmt.Errorf("The four material roles must resolve from one source archive; partial or competing overrides cannot be migrated")
	}
	ordered := append([]string(nil), paths...)
	slices.Sort(ordered)
	old, err := baseline(snapshot, oldModel)
	if err != nil {
		return nil, err
	}
	unitsBytes, err := baseline(snapshot, unitsPath)
	if err != nil {
		return nil, err
	}
	units, _, _, err := Decode(unitsBytes)
	if err != nil {
		return nil, err
	}

	authored := make(map[string][]byte, len(sourceRoles))
	hash := sha256.New()
	hash.Write([]byte(recipe))
	for _, path := range ordered {
		role, ok := SourceRole(path)
		if !ok {
			return nil, fmt.Errorf("Unknown material role")
		}
		bytes, err := snapshot.Resolve(path)
		if err != nil {
			return nil, err
		}
		if bytes == nil {
			return nil, fmt.Errorf("Missing authored material")
		}
		value, _, _, err := Decode(bytes)
		if err != nil {
			return nil, err
		}
		dynamicValue, err := Field(&value, "m_dynamicParams")
		if err != nil {
			return nil, err
		}
		dynamic, ok := dynamicValue.AsArray()
		if !ok {
			return nil, fmt.Errorf("Invalid authored expressions")
		}
		for i := range dynamic {
			nameValue, ok := dynamic[i].Get("m_name")
			if !ok {
				continue
			}
			if name, _ := nameValue.AsString(); name == "g_flSelfIllumScale1" {
				return nil, fmt.Errorf("Authored material still drives emission dynamically; this static-glow recipe does not apply")
			}
		}
		hash.Write([]byte(path))
		digest := sha256.Sum256(bytes)
		hash.Write(digest[:])
		authored[role] = bytes
	}
	namespace := fmt.Sprintf("dmm/compatibility/materials/%x", hash.Sum(nil))
	assets := make(map[string][]byte)
	for _, target := range glowTargets {
		current, err := baseline(snapshot, target.material+"_c")
		if err != nil {
			return nil, err
		}
		// Every texture retained by the current shader must still resolve.
		references, err := resource.ReferencesOf(current)
		if err != nil {
			return nil, err
		}
		for _, reference := range references {
			if _, err := baseline(snapshot, reference.Path); err != nil {
				return nil, err
			}
		}
		binding := target.material
		if target.isolate {
			path := fmt.Sprintf("%s/%s/default.vmat", namespace, target.key)
			taken, err := occupied(snapshot, path)
			if err != nil {
				return nil, err
			}
			if taken {
				return nil, fmt.Errorf("Generated material namespace is already occupied")
			}
			renamed, err := RenameMaterial(current, path)
			if err != nil {
				return nil, err
			}
			assets[path+"_c"] = renamed
			// The old wax surface used an unrestricted emission mask. Sapphire
			// now shares a body mask: restore glow only after isolating the
			// head draw calls.
			if _, err := baseline(snapshot, whiteMask); err != nil {
				return nil, err
			}
			whiteID, err := resource.ResourceID(whiteMask)
			if err != nil {
				return nil, err
			}
			current, err = ReplaceTextureParameter(current, "g_tSelfIllumMask",
				resource.Reference{ID: whiteID, Path: whiteMask})
			if err != nil {
				return nil, err
			}
			binding = path
		}
		ids := make(map[string]uint64)
		variants := make(map[string]string)
		for _, role := range sourceRoles {
			path := fmt.Sprintf("%s/%s/%s.vmat", namespace, target.key, role.role)
			taken, err := occupied(snapshot, path)
			if err != nil {
				return nil, err
			}
			if taken {
				return nil, fmt.Errorf("Generated material namespace is already occupied")
			}
			id, err := resource.ResourceID(path)
			if err != nil {
				return nil, err
			}
			ids[path] = id
			variant, err := MaterialVariant(current, authored[role.role], path)
			if err != nil {
				return nil, err
			}
			assets[path+"_c"] = variant
			variants[role.role] = path
		}
		for _, name := range target.units {
			unitValue, err := Field(&units, name)
			if err != nil {
				return nil, err
			}
			modelName, err := text(unitValue, target.modelField)
			if err != nil {
				return nil, err
			}
			modelPath := modelName + "_c"
			modelBytes, err := baseline(snapshot, modelPath)
			if err != nil {
				return nil, err
			}
			if _, exists := assets[modelPath]; exists {
				return nil, fmt.Errorf("Unit definitions alias a model across incompatible targets")
			}
			root, _, _, err := Decode(modelBytes)
			if err != nil {
				return nil, err
			}
			groupsValue, err := Field(&root, "m_meshGroups")
			if err != nil {
				return nil, err
			}
			groups, ok := groupsValue.AsArray()
			if !ok {
				return nil, fmt.Errorf("Invalid mesh groups")
			}
			for i := range groups {
				group, ok := groups[i].AsString()
				if !ok {
					continue
				}
				reviewed := group == "head_health_@3_#&death"
				for _, known := range headGroups {
					if group == known {
						reviewed = true
					}
				}
				if strings.HasPrefix(group, "head_health_") && !reviewed {
					return nil, fmt.Errorf("Current game has an unreviewed head health group")
				}
			}
			if target.isolate {
				bindingID, err := resource.ResourceID(binding)
				if err != nil {
					return nil, err
				}
				scoped, _, err := IsolateMeshMaterial(modelBytes, headGroups[:],
					target.material, resource.Reference{ID: bindingID, Path: binding})
				if err != nil {
					return nil, err
				}
				modelBytes = scoped
			}
			roles := make([][2]string, 0, len(sourceRoles))
			unique := make(map[string]struct{}, len(sourceRoles))
			for _, role := range sourceRoles {
				group, err := text(unitValue, role.field)
				if err != nil {
					return nil, err
				}
				roles = append(roles, [2]string{group, variants[role.role]})
				unique[group] = struct{}{}
			}
			if len(unique) != len(sourceRoles) {
				return nil, fmt.Errorf("Ambiguous unit material roles")
			}
			if _, ok := unique["default"]; ok {
				return nil, fmt.Errorf("Ambiguous unit material roles")
			}
			output, err := BindVariants(modelBytes, old, binding, roles, ids)
			if err != nil {
				return nil, err
			}
			assets[modelPath] = output
		}
	}
	if err := validateDependencies(snapshot, assets); err != nil {
		return nil, err
	}
	return assets, nil
}

func text(value *kv3.Value, key string) (string, error) {
	field, err := Field(value, key)
	if err != nil {
		return "", err
	}
	text, ok := field.AsString()
	if !ok {
		return "", fmt.Errorf("Invalid string %s", key)
	}
	return text, nil
}

func occupied(snapshot Snapshot, path string) (bool, error) {
	bytes, err := snapshot.Resolve(path)
	if err != nil {
		return false, err
	}
	return bytes != nil, nil
}

func baseline(snapshot Snapshot, path string) ([]byte, error) {
	current, ok, err := snapshot.GameBytes(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("Missing current game dependency %s", path)
	}
	effective, err := snapshot.Resolve(path)
	if err != nil {
		return nil, err
	}
	if effective != nil && !bytes.Equal(effective, current) {
		texture := strings.HasSuffix(path, ".vtex") || strings.HasSuffix(path, ".vtex_c")
		if !texture || !SameTextureRuntime(effective, current) {
			return nil, fmt.Errorf("Enabled mod overrides migration dependency %s", path)
		}
	}
	return current, nil
}

// SameTextureRuntime reports whether two textures differ only in compiler
// provenance: header version, DATA bytes, tail bytes and YCoCg status must
// match. Unknown runtime blocks make the comparison fail.
func SameTextureRuntime(left, right []byte) bool {
	if len(left) < 8 || len(right) < 8 || !bytes.Equal(left[4:8], right[4:8]) {
		return false
	}
	leftPayload, leftOK := textureRuntime(left)
	rightPayload, rightOK := textureRuntime(right)
	if !leftOK || !rightOK {
		return false
	}
	return bytes.Equal(leftPayload.data, rightPayload.data) &&
		bytes.Equal(leftPayload.tail, rightPayload.tail) &&
		leftPayload.ycocg == rightPayload.ycocg
}

type texturePayload struct {
	data  []byte
	tail  []byte
	ycocg bool
}

func textureRuntime(bytes []byte) (texturePayload, bool) {
	parsed, err := resource.Parse(bytes)
	if err != nil {
		return texturePayload{}, false
	}
	dataBlocks := 0
	for _, block := range parsed.Blocks() {
		switch block.Kind {
		case resource.BlockTypeData:
			dataBlocks++
		case [4]byte{'R', 'E', 'D', 'I'}, [4]byte{'R', 'E', 'D', '2'}:
		default:
			return texturePayload{}, false
		}
	}
	if dataBlocks != 1 {
		return texturePayload{}, false
	}
	end := binary.LittleEndian.Uint32(bytes[0:4])
	maxEnd := uint64(0)
	for _, block := range parsed.Blocks() {
		blockEnd := uint64(block.Offset) + uint64(block.Size)
		if blockEnd > maxEnd {
			maxEnd = blockEnd
		}
	}
	if uint64(end) < maxEnd || uint64(end) > uint64(len(bytes)) {
		return texturePayload{}, false
	}
	data, err := parsed.DataBlock()
	if err != nil {
		return texturePayload{}, false
	}
	return texturePayload{
		data:  data,
		tail:  bytes[end:],
		ycocg: detectYCoCg(parsed),
	}, true
}

func detectYCoCg(parsed *resource.Resource) bool {
	const marker = "YCoCg Conversion"
	for _, kind := range [][4]byte{{'R', 'E', 'D', '2'}, {'R', 'E', 'D', 'I'}} {
		block, ok := parsed.FindBlock(kind)
		if ok && bytes.Contains(block, []byte(marker)) {
			return true
		}
	}
	return false
}

func validateDependencies(snapshot Snapshot, assets map[string][]byte) error {
	paths := make([]string, 0, len(assets))
	for path := range assets {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		references, err := resource.ReferencesOf(assets[path])
		if err != nil {
			return err
		}
		ids := make(map[uint64]string)
		names := make(map[string]struct{})
		for _, reference := range references {
			if old, ok := ids[reference.ID]; ok && old != reference.Path {
				return fmt.Errorf("Ambiguous resource references in %s", path)
			}
			if _, ok := names[reference.Path]; ok {
				return fmt.Errorf("Ambiguous resource references in %s", path)
			}
			ids[reference.ID] = reference.Path
			names[reference.Path] = struct{}{}
			compiled := reference.Path + "_c"
			if _, ok := assets[compiled]; ok {
				continue
			}
			resolved, err := snapshot.Resolve(reference.Path)
			if err != nil {
				return err
			}
			if resolved == nil {
				return fmt.Errorf("%s requires missing resource %s", path, reference.Path)
			}
		}
	}
	return nil
}
