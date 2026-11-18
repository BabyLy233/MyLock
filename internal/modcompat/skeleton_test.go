package modcompat

import (
	"bytes"
	"testing"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const testSkeletonPath = "models/rig.vnmskel_c"

func testMask(id string, bones []string, weights []float64) kv3.Value {
	names := make([]kv3.Value, len(bones))
	values := make([]kv3.Value, len(weights))
	for i := range bones {
		names[i] = kv3.String(bones[i])
		values[i] = kv3.Double(weights[i])
	}
	return kv3.Object(
		kv3.Pair{Key: "m_ID", Value: kv3.String(id)},
		kv3.Pair{Key: "m_primaryWeightList", Value: kv3.Object(
			kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(names...)},
			kv3.Pair{Key: "m_weights", Value: kv3.Array(values...)},
		)},
	)
}

func testSkeletonBytes(t *testing.T, bones []string, parents []int64, masks ...kv3.Value) []byte {
	t.Helper()
	names := make([]kv3.Value, len(bones))
	parentValues := make([]kv3.Value, len(parents))
	poses := make([]kv3.Value, len(bones))
	for i := range bones {
		names[i] = kv3.String(bones[i])
		parentValues[i] = kv3.Int(parents[i])
		poses[i] = contractPose()
	}
	value := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(names...)},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(parentValues...)},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(poses...)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(poses...)},
		kv3.Pair{Key: "m_maskDefinitions", Value: kv3.Array(masks...)},
	)
	return compiledBlock(t, &value)
}

func hasRepair(repairs []Repair, kind RepairKind, path string) bool {
	for _, repair := range repairs {
		if repair.Kind == kind && repair.FilePath == path {
			return true
		}
	}
	return false
}

func TestAnalyzeRefreshesEquivalentAnimationSkeletons(t *testing.T) {
	gameBytes := testSkeletonBytes(t, []string{"root", "head"}, []int64{-1, 0},
		testMask("default", []string{"head", "root"}, []float64{2, 1}))
	modBytes := testSkeletonBytes(t, []string{"root", "head"}, []int64{-1, 0},
		testMask("DEFAULT", []string{"root", "head"}, []float64{1, 2}))
	game := gameIndexWith(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: gameBytes}})
	mod := packMod(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: modBytes}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRepair(analysis.Repairs, RepairAnimationSkeleton, testSkeletonPath) {
		t.Fatalf("repairs = %+v warnings = %+v", analysis.Repairs, analysis.AssetWarnings)
	}
	if !bytes.Equal(analysis.Fixed[testSkeletonPath], gameBytes) {
		t.Fatal("the refreshed skeleton should use the current game bytes")
	}
	if hasWarning(analysis.AssetWarnings, compat.WarningUnverifiedSkeleton) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeSkipsIdenticalSkeletonBytes(t *testing.T) {
	skeleton := testSkeletonBytes(t, []string{"root"}, []int64{-1},
		testMask("default", []string{"root"}, []float64{1}))
	game := gameIndexWith(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: skeleton}})
	mod := packMod(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: skeleton}})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Fixed) != 0 {
		t.Fatalf("repairs = %+v fixed = %v", analysis.Repairs, analysis.Fixed)
	}
}

func TestAnalyzeWarnsAboutUnverifiableSkeletonExtensions(t *testing.T) {
	gameBytes := testSkeletonBytes(t, []string{"root", "head"}, []int64{-1, 0},
		testMask("default", []string{"root", "head"}, []float64{1, 2}))
	modBytes := testSkeletonBytes(t, []string{"root", "head", "extra"}, []int64{-1, 0, 1},
		testMask("default", []string{"root", "head"}, []float64{1, 2}))
	game := gameIndexWith(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: gameBytes}})
	mod := packMod(t, []vpk.PackEntry{
		{Path: testSkeletonPath, Data: modBytes},
		{Path: "animgraphs/custom.vnmgraph_c", Data: []byte("mod animation")},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if hasRepair(analysis.Repairs, RepairAnimationSkeleton, testSkeletonPath) {
		t.Fatalf("repairs = %+v", analysis.Repairs)
	}
	if !hasWarning(analysis.AssetWarnings, compat.WarningUnverifiedSkeleton) {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func TestAnalyzeRequiresProofWhenTheModShipsAnimations(t *testing.T) {
	gameBytes := testSkeletonBytes(t, []string{"root"}, []int64{-1},
		testMask("default", []string{"root"}, []float64{1}))
	modBytes := testSkeletonBytes(t, []string{"root"}, []int64{-1},
		testMask("DEFAULT", []string{"root"}, []float64{1}))
	game := gameIndexWith(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: gameBytes}})
	mod := packMod(t, []vpk.PackEntry{
		{Path: testSkeletonPath, Data: modBytes},
		{Path: "animgraphs/custom.vnmgraph_c", Data: []byte("mod animation")},
	})
	analysis, err := Analyze("mod", []string{mod}, game, Scope{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Fixed) != 0 {
		t.Fatalf("repairs = %+v fixed = %v", analysis.Repairs, analysis.Fixed)
	}
}

func TestAnalyzeDefersToAnotherModsSkeleton(t *testing.T) {
	gameBytes := testSkeletonBytes(t, []string{"root"}, []int64{-1},
		testMask("default", []string{"root"}, []float64{1}))
	modBytes := testSkeletonBytes(t, []string{"root"}, []int64{-1},
		testMask("DEFAULT", []string{"root"}, []float64{1}))
	game := gameIndexWith(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: gameBytes}})
	mod := packMod(t, []vpk.PackEntry{{Path: testSkeletonPath, Data: modBytes}})
	scope := Scope{OtherAnimationPaths: map[string]struct{}{testSkeletonPath: {}}}
	analysis, err := Analyze("mod", []string{mod}, game, scope, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Fixed) != 0 {
		t.Fatalf("repairs = %+v fixed = %v", analysis.Repairs, analysis.Fixed)
	}
	unknown := Scope{Unknown: true}
	analysis, err = Analyze("mod", []string{mod}, game, unknown, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Fixed) != 0 {
		t.Fatalf("unknown scope repairs = %+v fixed = %v", analysis.Repairs, analysis.Fixed)
	}
}
