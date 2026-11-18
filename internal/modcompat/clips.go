package modcompat

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/animation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// applyClipTracks expands old NM clips that have fewer tracks than the current
// skeleton. The authored track order is inferred from named poses and the
// model hierarchy; ambiguous layouts only warn.
func applyClipTracks(analysis *Analysis, modID string, models []modelSource,
	modVPKs map[string]string, read func(string) ([]byte, error), game *source2.GameIndex,
	scope Scope, mode Mode, effective map[string]effectiveModel,
	repairedPaths map[string]struct{}) error {
	if scope.Unknown {
		return nil
	}
	paths := slices.Sorted(maps.Keys(modVPKs))
	for _, path := range paths {
		if !strings.HasSuffix(path, ".vnmclip_c") {
			continue
		}
		if _, protected := scope.OtherAnimationPaths[path]; protected {
			continue
		}
		sourceVPK := modVPKs[path]
		data, err := read(path)
		if err != nil {
			return fmt.Errorf("reading animation clip %s: %w", path, err)
		}
		if data == nil {
			continue
		}
		_, clip, _, err := camera.DecodeCompiled(data)
		if err != nil {
			continue
		}
		skeletonName, ok := clipString(&clip, "m_skeleton")
		if !ok {
			continue
		}
		skeletonPath, ok := source2.ResourcePath(skeletonName)
		if !ok {
			continue
		}
		if _, bundled := modVPKs[skeletonPath]; bundled {
			continue
		}
		if _, protected := scope.OtherAnimationPaths[skeletonPath]; protected {
			continue
		}
		var skeletonBytes []byte
		if repaired, ok := effective[skeletonPath]; ok {
			skeletonBytes = repaired.data
		} else if game != nil {
			skeletonBytes, _ = game.GameBytes(skeletonPath)
		}
		if skeletonBytes == nil {
			continue
		}
		_, skeleton, _, err := camera.DecodeCompiled(skeletonBytes)
		if err != nil {
			continue
		}
		tracksValue, ok := clip.Get("m_trackCompressionSettings")
		if !ok {
			continue
		}
		tracks, ok := tracksValue.AsArray()
		if !ok {
			continue
		}
		bonesValue, ok := skeleton.Get("m_boneIDs")
		if !ok {
			continue
		}
		bones, ok := bonesValue.AsArray()
		if !ok || len(tracks) == len(bones) {
			continue
		}
		layout, ok := inferClipLayout(models, modVPKs, sourceVPK, skeletonPath,
			&skeleton, len(tracks), read, scope)
		if !ok {
			analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
				ModID: modID, SourceVPK: sourceVPK, FilePath: path,
				Kind: compat.WarningAnimationMapping,
				Detail: fmt.Sprintf(
					"Authored animation has %d tracks for %d active skeleton bones; authored bone order could not be established",
					len(tracks), len(bones)),
			})
			continue
		}
		rebuilt, ok := animation.ExpandClip(data, &skeleton, layout)
		if !ok {
			analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
				ModID: modID, SourceVPK: sourceVPK, FilePath: path,
				Kind: compat.WarningAnimationMapping,
				Detail: fmt.Sprintf(
					"Authored animation has %d tracks for %d active skeleton bones; authored bone order could not be established",
					len(tracks), len(bones)),
			})
			continue
		}
		effective[path] = effectiveModel{data: rebuilt, vpkPath: sourceVPK}
		repairedPaths[path] = struct{}{}
		analysis.Repairs = append(analysis.Repairs, Repair{
			ModID: modID, SourceVPK: sourceVPK, FilePath: path,
			Kind: RepairAnimationClipTracks,
		})
		if mode == Fix {
			analysis.Fixed[path] = rebuilt
		}
	}
	return nil
}

func inferClipLayout(models []modelSource, modVPKs map[string]string, clipVPK, skeletonPath string,
	skeleton *kv3.Value, trackCount int, read func(string) ([]byte, error),
	scope Scope) ([]int, bool) {
	var result []int
	found := false
	for _, source := range models {
		if source.vpkPath != clipVPK {
			continue
		}
		path := camera.NormalizePath(source.path)
		if _, protected := scope.OtherAnimationPaths[path]; protected {
			continue
		}
		modelBytes, err := read(path)
		if err != nil {
			return nil, false
		}
		if modelBytes == nil {
			continue
		}
		_, model, _, err := camera.DecodeCompiled(modelBytes)
		if err != nil {
			return nil, false
		}
		refsValue, ok := model.Get("m_vecNmSkeletonRefs")
		if !ok {
			continue
		}
		refs, ok := refsValue.AsArray()
		if !ok || len(refs) != 1 {
			continue
		}
		name, ok := refs[0].AsString()
		if !ok {
			continue
		}
		resolved, ok := source2.ResourcePath(name)
		if !ok || resolved != skeletonPath {
			continue
		}
		layout, ok := clipEvidence(modVPKs, clipVPK, skeletonPath, skeleton, trackCount, &model, read, scope)
		if !ok {
			return nil, false
		}
		if found && !slices.Equal(result, layout) {
			return nil, false
		}
		result = layout
		found = true
	}
	return result, found
}

func clipEvidence(modVPKs map[string]string, clipVPK, skeletonPath string, skeleton *kv3.Value,
	trackCount int, model *kv3.Value, read func(string) ([]byte, error), scope Scope) ([]int, bool) {
	for _, path := range slices.Sorted(maps.Keys(modVPKs)) {
		if !strings.HasSuffix(path, ".vnmclip_c") || modVPKs[path] != clipVPK {
			continue
		}
		if _, protected := scope.OtherAnimationPaths[path]; protected {
			continue
		}
		data, err := read(path)
		if err != nil {
			return nil, false
		}
		if data == nil {
			continue
		}
		_, clip, _, err := camera.DecodeCompiled(data)
		if err != nil {
			return nil, false
		}
		additive, ok := clipBool(&clip, "m_bIsAdditive")
		if !ok || additive {
			continue
		}
		name, ok := clipString(&clip, "m_skeleton")
		if !ok {
			continue
		}
		resolved, ok := source2.ResourcePath(name)
		if !ok || resolved != skeletonPath {
			continue
		}
		tracksValue, ok := clip.Get("m_trackCompressionSettings")
		if !ok {
			return nil, false
		}
		tracks, ok := tracksValue.AsArray()
		if !ok {
			return nil, false
		}
		if len(tracks) != trackCount {
			continue
		}
		if _, ok := animation.FrameWords(&clip, tracks); !ok {
			continue
		}
		if layout, ok := animation.Mapping(skeleton, model, tracks); ok {
			return layout, true
		}
	}
	return nil, false
}

func clipString(value *kv3.Value, key string) (string, bool) {
	field, ok := value.Get(key)
	if !ok {
		return "", false
	}
	return field.AsString()
}

func clipBool(value *kv3.Value, key string) (bool, bool) {
	field, ok := value.Get(key)
	if !ok {
		return false, false
	}
	return field.AsBool()
}
