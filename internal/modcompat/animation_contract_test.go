package modcompat

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func contractPose() kv3.Value {
	return kv3.Array(
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
		kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1),
	)
}

func contractSkeleton(t *testing.T, names []string, parents []int64) []byte {
	t.Helper()
	nameValues := make([]kv3.Value, len(names))
	parentValues := make([]kv3.Value, len(parents))
	poses := make([]kv3.Value, len(names))
	for i := range names {
		nameValues[i] = kv3.String(names[i])
		parentValues[i] = kv3.Int(parents[i])
		poses[i] = contractPose()
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(nameValues...)},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(parentValues...)},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(poses...)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(poses...)},
	)
	return compiledBlock(t, &value)
}

func contractGraph(t *testing.T, skeleton string, children ...string) []byte {
	t.Helper()
	values := make([]kv3.Value, len(children))
	for i, child := range children {
		values[i] = kv3.String(child)
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.String(skeleton)},
		kv3.Pair{Key: "m_resources", Value: kv3.Array(values...)},
	)
	return compiledBlock(t, &value)
}

func contractClip(t *testing.T, skeleton string, tracks int) []byte {
	t.Helper()
	settings := make([]kv3.Value, tracks)
	for i := range settings {
		settings[i] = kv3.Object()
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.String(skeleton)},
		kv3.Pair{Key: "m_trackCompressionSettings", Value: kv3.Array(settings...)},
	)
	return compiledBlock(t, &value)
}

func contractModel(t *testing.T, graph string) []byte {
	t.Helper()
	value := kv3.Object(
		kv3.Pair{Key: "m_vecNmSkeletonRefs", Value: kv3.Array(kv3.String("models/rig.vnmskel"))},
		kv3.Pair{Key: "m_animGraph2Refs", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "m_sIdentifier", Value: kv3.String("default")},
			kv3.Pair{Key: "m_hGraph", Value: kv3.String(graph)},
		))},
	)
	return compiledBlock(t, &value)
}

func contractFixture(t *testing.T, gameEntries []vpk.PackEntry, graph string) (string, *source2.GameIndex) {
	t.Helper()
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: contractModel(t, graph)},
	})
	return mod, gameIndexWith(t, gameEntries)
}

func TestAnimationContractAcceptsMatchingClosure(t *testing.T) {
	game := []vpk.PackEntry{
		{Path: "models/rig.vnmskel_c", Data: contractSkeleton(t, []string{"a", "b"}, []int64{-1, 0})},
		{Path: "animgraphs/a.vnmgraph_c", Data: contractGraph(t, "models/rig.vnmskel", "animations/a.vnmclip")},
		{Path: "animations/a.vnmclip_c", Data: contractClip(t, "models/rig.vnmskel", 2)},
	}
	mod, gameIndex := contractFixture(t, game, "animgraphs/a.vnmgraph")
	analysis, err := Analyze("mod", []string{mod}, gameIndex, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.AssetWarnings) != 0 {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnimationContractReportsTrackCountMismatch(t *testing.T) {
	game := []vpk.PackEntry{
		{Path: "models/rig.vnmskel_c", Data: contractSkeleton(t, []string{"a", "b"}, []int64{-1, 0})},
		{Path: "animgraphs/a.vnmgraph_c", Data: contractGraph(t, "models/rig.vnmskel", "animations/a.vnmclip")},
		{Path: "animations/a.vnmclip_c", Data: contractClip(t, "models/rig.vnmskel", 1)},
	}
	mod, gameIndex := contractFixture(t, game, "animgraphs/a.vnmgraph")
	analysis, err := Analyze("mod", []string{mod}, gameIndex, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningAnimationMapping) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnimationContractReportsMissingDependencies(t *testing.T) {
	mod, gameIndex := contractFixture(t, []vpk.PackEntry{
		{Path: "scripts/unrelated.txt", Data: []byte("x")},
	}, "animgraphs/a.vnmgraph")
	analysis, err := Analyze("mod", []string{mod}, gameIndex, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningMissingResource) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnimationContractReportsInvalidSkeletons(t *testing.T) {
	game := []vpk.PackEntry{
		{Path: "models/rig.vnmskel_c", Data: contractSkeleton(t, []string{"a", "a"}, []int64{-1, 0})},
		{Path: "animgraphs/a.vnmgraph_c", Data: contractGraph(t, "models/rig.vnmskel")},
	}
	mod, gameIndex := contractFixture(t, game, "animgraphs/a.vnmgraph")
	analysis, err := Analyze("mod", []string{mod}, gameIndex, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningInvalidSkeleton) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnimationContractIgnoresOptionalClipsReferencedByTheGame(t *testing.T) {
	game := []vpk.PackEntry{
		{Path: "models/rig.vnmskel_c", Data: contractSkeleton(t, []string{"a"}, []int64{-1})},
		{Path: "animgraphs/a.vnmgraph_c", Data: contractGraph(t, "models/rig.vnmskel", "animations/optional.vnmclip")},
	}
	mod, gameIndex := contractFixture(t, game, "animgraphs/a.vnmgraph")
	analysis, err := Analyze("mod", []string{mod}, gameIndex, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.AssetWarnings) != 0 {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnimationContractDefersToOtherModsAnimationResources(t *testing.T) {
	game := []vpk.PackEntry{
		{Path: "models/rig.vnmskel_c", Data: contractSkeleton(t, []string{"a"}, []int64{-1})},
	}
	mod, gameIndex := contractFixture(t, game, "animgraphs/a.vnmgraph")
	scope := Scope{OtherAnimationPaths: map[string]struct{}{
		"animgraphs/a.vnmgraph_c": {},
	}}
	analysis, err := Analyze("mod", []string{mod}, gameIndex, scope, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.AssetWarnings) != 0 {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnimationContractSkipsUnsupportedGraphReferences(t *testing.T) {
	game := []vpk.PackEntry{
		{Path: "models/rig.vnmskel_c", Data: contractSkeleton(t, []string{"a"}, []int64{-1})},
		{Path: "animgraphs/a.vnmgrph_c", Data: []byte("legacy graph")},
	}
	mod, gameIndex := contractFixture(t, game, "animgraphs/a.vnmgrph")
	analysis, err := Analyze("mod", []string{mod}, gameIndex, Scope{}, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.AssetWarnings) != 0 {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestContractCacheInterpretsBytesByResourceType(t *testing.T) {
	value := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.String("models/test.vnmskel")},
		kv3.Pair{Key: "m_resources", Value: kv3.Array()},
		kv3.Pair{Key: "m_trackCompressionSettings", Value: kv3.Array()},
	)
	bytes := compiledBlock(t, ptr(value))
	clip := cachedContractNode("test.vnmclip_c", bytes)
	if !clip.ok || clip.node.kind != nodeClip || clip.node.bones != 0 ||
		clip.node.skeleton != "models/test.vnmskel_c" {
		t.Fatalf("clip = %+v", clip)
	}
	graph := cachedContractNode("test.vnmgraph_c", bytes)
	if !graph.ok || graph.node.kind != nodeGraph || len(graph.node.children) != 0 {
		t.Fatalf("graph = %+v", graph)
	}
	renamed := cachedContractNode("renamed.vnmclip_c", bytes)
	if !renamed.ok || renamed.node.kind != nodeClip {
		t.Fatalf("renamed clip = %+v", renamed)
	}
}

func TestContractCacheEvictsMetadataAndLargeGraphs(t *testing.T) {
	key := func(index int) contractCacheKey {
		var digest [32]byte
		binary.LittleEndian.PutUint32(digest[:], uint32(index))
		return contractCacheKey{kind: "vnmclip_c", digest: digest}
	}
	var cache contractNodeCache
	for index := range contractCacheMaxEntries {
		cache.insert(key(index), contractNodeResult{node: contractNode{kind: nodeSkeleton, bones: index}})
	}
	if _, ok := cache.get(key(0)); !ok {
		t.Fatal("the oldest entry was dropped before pressure")
	}
	cache.insert(key(contractCacheMaxEntries), contractNodeResult{node: contractNode{kind: nodeSkeleton}})
	if _, ok := cache.get(key(1)); ok {
		t.Fatal("the oldest entry survived the count limit")
	}
	if _, ok := cache.get(key(0)); !ok {
		t.Fatal("a recently used entry was evicted")
	}
	oversized := contractNodeResult{node: contractNode{
		kind: nodeGraph, children: []string{strings.Repeat("x", contractCacheMaxBytes)},
	}}
	cache.insert(key(contractCacheMaxEntries+1), oversized)
	if _, ok := cache.get(key(contractCacheMaxEntries + 1)); ok {
		t.Fatal("an oversized graph was retained")
	}
	cache.mu.Lock()
	held := cache.bytes
	cache.mu.Unlock()
	if held > contractCacheMaxBytes {
		t.Fatalf("cache holds %d bytes", held)
	}
}
