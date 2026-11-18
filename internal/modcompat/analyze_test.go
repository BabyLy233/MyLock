package modcompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/resource"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

func packMod(t *testing.T, entries []vpk.PackEntry) string {
	t.Helper()
	data, err := vpk.Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mod_dir.vpk")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeReportsCameraChecksAndEmptyFindings(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "materials/hero.vmat_c", Data: []byte("material")},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Checks) != len(CameraChecks) || analysis.ModelsChecked != 0 {
		t.Fatalf("analysis = %+v", analysis)
	}
	if len(analysis.Repairs) != 0 || len(analysis.Warnings) != 0 || analysis.Fixed != nil {
		t.Fatalf("unexpected findings: %+v", analysis)
	}
}

func TestAnalyzeRejectsAnUnreadableMod(t *testing.T) {
	if _, err := Analyze("mod", []string{filepath.Join(t.TempDir(), "missing.vpk")}, nil, Detect); err == nil {
		t.Fatal("missing VPK accepted")
	}
}

func TestAnalyzeFixModeReturnsAMap(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "scripts/hero.txt", Data: []byte("data")},
	})
	analysis, err := Analyze("mod", []string{mod}, &source2.GameIndex{}, Fix)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Fixed == nil {
		t.Fatal("fix mode did not initialise Fixed")
	}
}

func TestModelPathsNormalizeAndDeduplicate(t *testing.T) {
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: []byte("a")},
		{Path: "materials/hero.vmat_c", Data: []byte("c")},
	})
	paths, err := ModelPaths([]string{mod})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "models/hero/hero.vmdl_c" {
		t.Fatalf("paths = %v", paths)
	}
	if _, err := ModelPaths([]string{filepath.Join(t.TempDir(), "missing.vpk")}); err == nil {
		t.Fatal("missing VPK accepted")
	}
}

func compiledBlock(t *testing.T, value *kv3.Value) []byte {
	t.Helper()
	data, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(value, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func compiledWithMesh(t *testing.T, value, mesh *kv3.Value) []byte {
	t.Helper()
	data, err := resource.Build([]resource.BuildResult{
		{Kind: resource.BlockTypeData, Payload: kv3.Encode(value, kv3.Format{})},
		{Kind: [4]byte{'M', 'D', 'A', 'T'}, Payload: kv3.Encode(mesh, kv3.Format{})},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnalyzeReportsStructuralModelWarnings(t *testing.T) {
	model := kv3.Object(
		kv3.Pair{Key: "m_modelSkeleton", Value: kv3.Object(
			kv3.Pair{Key: "m_boneName", Value: kv3.Array(kv3.String("bone"), kv3.String("bone"))},
			kv3.Pair{Key: "m_nParent", Value: kv3.Array(kv3.Int(-1), kv3.Int(0))},
			kv3.Pair{Key: "m_bonePosParent", Value: kv3.Array(
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0)),
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0)),
			)},
			kv3.Pair{Key: "m_boneRotParent", Value: kv3.Array(
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
				kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
			)},
			kv3.Pair{Key: "m_boneScaleParent", Value: kv3.Array(kv3.Double(1), kv3.Double(1))},
		)},
		kv3.Pair{Key: "m_refMeshes", Value: kv3.Array(kv3.String("models/missing.vmdl"))},
	)
	mesh := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(kv3.Pair{Key: "m_bones", Value: kv3.Array()})},
		kv3.Pair{Key: "m_attachments", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "key", Value: kv3.String("camera")},
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_nInfluences", Value: kv3.Int(1)},
				kv3.Pair{Key: "m_influenceNames", Value: kv3.Array(kv3.String(""))},
				kv3.Pair{Key: "m_bInfluenceRootTransform", Value: kv3.Array(kv3.Bool(false))},
				kv3.Pair{Key: "m_influenceWeights", Value: kv3.Array(kv3.Double(1))},
			)},
		))},
	)
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vmdl_c", Data: compiledWithMesh(t, &model, &mesh)},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 1 {
		t.Fatalf("assets checked = %d", analysis.AssetsChecked)
	}
	kinds := make(map[compat.WarningKind]bool)
	for _, warning := range analysis.AssetWarnings {
		kinds[warning.Kind] = true
	}
	for _, want := range []compat.WarningKind{
		compat.WarningInvalidSkeleton,
		compat.WarningMissingResource,
		compat.WarningInvalidAttachment,
	} {
		if !kinds[want] {
			t.Fatalf("missing %s in %+v", want, analysis.AssetWarnings)
		}
	}
}

func TestAnalyzeReportsSkeletonAndMeshWarnings(t *testing.T) {
	skeleton := kv3.Object(
		kv3.Pair{Key: "m_boneIDs", Value: kv3.Array(kv3.String("a"), kv3.String("a"))},
		kv3.Pair{Key: "m_parentIndices", Value: kv3.Array(kv3.Int(-1), kv3.Int(0))},
		kv3.Pair{Key: "m_parentSpaceReferencePose", Value: kv3.Array(
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
		)},
		kv3.Pair{Key: "m_modelSpaceReferencePose", Value: kv3.Array(
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
			kv3.Array(kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(0),
				kv3.Double(0), kv3.Double(0), kv3.Double(0), kv3.Double(1)),
		)},
	)
	mesh := kv3.Object(
		kv3.Pair{Key: "m_skeleton", Value: kv3.Object(kv3.Pair{Key: "m_bones", Value: kv3.Array()})},
		kv3.Pair{Key: "m_hitboxsets", Value: kv3.Array(kv3.Object(
			kv3.Pair{Key: "value", Value: kv3.Object(
				kv3.Pair{Key: "m_HitBoxes", Value: kv3.Array(kv3.Object(
					kv3.Pair{Key: "m_sBoneName", Value: kv3.String("pelvis")},
				))},
			)},
		))},
	)
	mod := packMod(t, []vpk.PackEntry{
		{Path: "models/hero/hero.vnmskel_c", Data: compiledBlock(t, &skeleton)},
		{Path: "models/hero/hero.vmesh_c", Data: compiledBlock(t, &mesh)},
	})
	analysis, err := Analyze("mod", []string{mod}, nil, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 2 {
		t.Fatalf("assets checked = %d", analysis.AssetsChecked)
	}
	kinds := make(map[compat.WarningKind]bool)
	for _, warning := range analysis.AssetWarnings {
		kinds[warning.Kind] = true
	}
	if !kinds[compat.WarningInvalidSkeleton] || !kinds[compat.WarningInvalidHitbox] {
		t.Fatalf("warnings = %+v", analysis.AssetWarnings)
	}
}

func gameIndexWith(t *testing.T, entries []vpk.PackEntry) *source2.GameIndex {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "game", "citadel")
	write := filepath.Join(dir, "pak01_dir.vpk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := vpk.Pack(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(write, data, 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := source2.OpenGame(dir)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestAnalyzeReportsMaterialInterfaceDifferences(t *testing.T) {
	source := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("custom.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	current := kv3.Object(
		kv3.Pair{Key: "m_shaderName", Value: kv3.String("csgo_environment.vfx")},
		kv3.Pair{Key: "m_intParams", Value: kv3.Array()},
		kv3.Pair{Key: "m_textureParams", Value: kv3.Array()},
	)
	const path = "materials/hero/hero.vmat_c"
	game := gameIndexWith(t, []vpk.PackEntry{{Path: path, Data: compiledBlock(t, &current)}})
	mod := packMod(t, []vpk.PackEntry{{Path: path, Data: compiledBlock(t, &source)}})
	analysis, err := Analyze("mod", []string{mod}, game, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.AssetsChecked != 1 || len(analysis.AssetWarnings) != 1 {
		t.Fatalf("analysis = %+v", analysis)
	}
	warning := analysis.AssetWarnings[0]
	if warning.Kind != compat.WarningMaterialInterface || !strings.Contains(warning.Detail, "custom.vfx") {
		t.Fatalf("warning = %+v", warning)
	}

	same := packMod(t, []vpk.PackEntry{{Path: path, Data: compiledBlock(t, &current)}})
	analysis, err = Analyze("mod", []string{same}, game, Detect)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.AssetWarnings) != 0 {
		t.Fatalf("identical material warned: %+v", analysis.AssetWarnings)
	}
}
