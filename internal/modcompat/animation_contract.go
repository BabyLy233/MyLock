package modcompat

import (
	"maps"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/internal/source2"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/animation"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/camera"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/compat"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

// contractIssueKind follows the original animation dependency check.
type contractIssueKind int

const (
	issueMissingResource contractIssueKind = iota
	issueUnreadableResource
	issueInvalidSkeleton
	issueAnimationMapping
	issueNotChecked
)

type contractIssue struct {
	resource string
	kind     contractIssueKind
	detail   string
}

type contractNodeKind int

const (
	nodeSkeleton contractNodeKind = iota
	nodeGraph
	nodeClip
)

type contractNode struct {
	kind     contractNodeKind
	bones    int
	skeleton string
	children []string
}

type contractProvider int

const (
	providerNone contractProvider = iota
	providerGame
	providerMod
)

type contractChecker struct {
	read           func(string) ([]byte, error)
	provider       func(string) contractProvider
	repaired       map[string][]byte
	nodes          map[string]contractNodeResult
	otherAnimation map[string]struct{}
}

type contractNodeResult struct {
	node contractNode
	kind contractIssueKind
	ok   bool
}

// applyAnimationContract checks the effective animation closure of every model.
// It reports only structural failures: uncertain layouts are left alone, as in
// the upstream checker.
func applyAnimationContract(analysis *Analysis, modID string, effective map[string]effectiveModel,
	read func(string) ([]byte, error), game *source2.GameIndex, modArchives map[string]*vpk.Archive,
	scope Scope) {
	paths := make([]string, 0, len(effective))
	for path := range effective {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	repaired := make(map[string][]byte, len(effective))
	for path, model := range effective {
		repaired[path] = model.data
	}
	checker := &contractChecker{
		read:           read,
		provider:       contractProviderLookup(game, modArchives, scope.OtherAnimationPaths),
		repaired:       repaired,
		nodes:          make(map[string]contractNodeResult),
		otherAnimation: scope.OtherAnimationPaths,
	}
	for _, path := range paths {
		issues := checker.model(path, effective[path].data)
		for _, issue := range issues {
			if issue.kind == issueNotChecked {
				continue
			}
			kind := contractWarningKind(issue.kind)
			duplicate := false
			for _, warning := range analysis.AssetWarnings {
				if warning.FilePath == path && warning.Kind == kind &&
					warningDetailResource(warning.Detail) == issue.resource {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			analysis.AssetWarnings = append(analysis.AssetWarnings, AssetWarning{
				ModID: modID, SourceVPK: effective[path].vpkPath, FilePath: path,
				Kind: kind, Detail: issue.resource + ": " + issue.detail,
			})
		}
	}
}

func contractWarningKind(kind contractIssueKind) compat.WarningKind {
	switch kind {
	case issueMissingResource:
		return compat.WarningMissingResource
	case issueUnreadableResource:
		return compat.WarningUnreadableResource
	case issueInvalidSkeleton:
		return compat.WarningInvalidSkeleton
	case issueAnimationMapping:
		return compat.WarningAnimationMapping
	}
	return ""
}

func contractProviderLookup(game *source2.GameIndex, modArchives map[string]*vpk.Archive,
	otherAnimation map[string]struct{}) func(string) contractProvider {
	return func(name string) contractProvider {
		path, ok := source2.ResourcePath(name)
		if !ok {
			return providerNone
		}
		if _, ok := modArchives[path]; ok {
			return providerMod
		}
		if _, ok := otherAnimation[path]; ok {
			return providerMod
		}
		if game != nil {
			if _, ok := game.Provider(path); ok {
				return providerGame
			}
		}
		return providerNone
	}
}

func (c *contractChecker) model(path string, bytes []byte) []contractIssue {
	var issues []contractIssue
	add := func(resource string, kind contractIssueKind, detail string) {
		issues = append(issues, contractIssue{resource: resource, kind: kind, detail: detail})
	}
	_, model, _, err := camera.DecodeCompiled(bytes)
	if err != nil {
		add(path, issueUnreadableResource, "Model animation bindings could not be decoded")
		return issues
	}
	active := false
	for _, field := range []string{"m_animGraph2Refs", "m_vecNmSkeletonRefs"} {
		value, ok := model.Get(field)
		if !ok {
			continue
		}
		items, isArray := value.AsArray()
		if !isArray || len(items) > 0 {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	skeletons, skeletonsOK := modelSkeletons(&model)
	graphs, graphsOK := animation.Graphs(&model)
	if !skeletonsOK || !graphsOK || len(skeletons) == 0 {
		add(path, issueNotChecked, "Model animation bindings are incomplete or use an unsupported layout")
		return issues
	}
	var pending []string
	pending = append(pending, skeletons...)
	for _, graph := range slices.Sorted(maps.Values(graphs)) {
		graphPath, ok := source2.ResourcePath(graph)
		if !ok || !strings.HasSuffix(graphPath, ".vnmgraph_c") {
			add(graph, issueNotChecked, "Unsupported model graph reference")
			continue
		}
		pending = append(pending, graphPath)
	}
	visited := make(map[string]struct{})
	for len(pending) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := visited[path]; seen {
			continue
		}
		if len(visited) >= 4096 {
			add(path, issueNotChecked, "Animation dependency traversal limit reached")
			break
		}
		visited[path] = struct{}{}
		node, kind := c.node(path)
		if !node.ok {
			add(path, kind, contractIssueDetail(kind))
			continue
		}
		switch node.node.kind {
		case nodeSkeleton:
		case nodeGraph:
			if node.node.skeleton == "" {
				add(path, issueNotChecked, "Graph has no explicit skeleton binding")
			} else {
				c.binding(path, node.node.skeleton, skeletons, &pending, add)
			}
			for _, child := range node.node.children {
				if strings.HasSuffix(child, ".vnmclip_c") &&
					c.repaired[child] == nil && c.provider(child) == providerNone &&
					c.provider(path) == providerGame && c.repaired[path] == nil {
					add(child, issueNotChecked, "Absent clip referenced by the installed game; runtime use is unknown")
					continue
				}
				pending = append(pending, child)
			}
		case nodeClip:
			c.binding(path, node.node.skeleton, skeletons, &pending, add)
			if skeletonNode, _ := c.node(node.node.skeleton); skeletonNode.ok &&
				skeletonNode.node.kind == nodeSkeleton && node.node.bones != skeletonNode.node.bones {
				add(path, issueAnimationMapping, "Animation track count does not match the skeleton's bone count")
			}
		}
	}
	slices.SortFunc(issues, func(left, right contractIssue) int {
		if order := strings.Compare(left.resource, right.resource); order != 0 {
			return order
		}
		if left.kind != right.kind {
			return int(left.kind) - int(right.kind)
		}
		return strings.Compare(left.detail, right.detail)
	})
	issues = slices.CompactFunc(issues, func(left, right contractIssue) bool {
		return left == right
	})
	return issues
}

func (c *contractChecker) binding(path, skeleton string, modelSkeletons []string,
	pending *[]string, add func(string, contractIssueKind, string)) {
	if !slices.Contains(modelSkeletons, skeleton) {
		add(path, issueNotChecked, "Dependency uses a skeleton outside the model's declared skeletons")
	}
	*pending = append(*pending, skeleton)
}

func (c *contractChecker) node(path string) (contractNodeResult, contractIssueKind) {
	if cached, ok := c.nodes[path]; ok {
		return cached, cached.kind
	}
	result := c.readNode(path)
	c.nodes[path] = result
	return result, result.kind
}

func (c *contractChecker) readNode(path string) contractNodeResult {
	fail := func(kind contractIssueKind) contractNodeResult { return contractNodeResult{kind: kind} }
	if !strings.HasSuffix(path, ".vnmskel_c") && !strings.HasSuffix(path, ".vnmclip_c") &&
		!strings.HasSuffix(path, ".vnmgraph_c") {
		return fail(issueNotChecked)
	}
	bytes := c.repaired[path]
	if bytes == nil {
		if _, ok := c.otherAnimation[path]; ok {
			return fail(issueNotChecked)
		}
		data, err := c.read(path)
		if err != nil {
			return fail(issueUnreadableResource)
		}
		if data == nil {
			return fail(issueMissingResource)
		}
		bytes = data
	}
	_, value, _, err := camera.DecodeCompiled(bytes)
	if err != nil {
		return fail(issueUnreadableResource)
	}
	if strings.HasSuffix(path, ".vnmskel_c") {
		if !compat.ValidSkeleton(&value, "m_boneIDs", "m_parentIndices", []compat.PoseKey{
			{Key: "m_parentSpaceReferencePose", Width: 8},
			{Key: "m_modelSpaceReferencePose", Width: 8},
		}) {
			return fail(issueInvalidSkeleton)
		}
		names, _ := value.Get("m_boneIDs")
		items, _ := names.AsArray()
		return contractNodeResult{node: contractNode{kind: nodeSkeleton, bones: len(items)}, kind: issueNotChecked, ok: true}
	}
	skeleton := ""
	skeletonField, hasSkeletonField := value.Get("m_skeleton")
	if hasSkeletonField {
		if name, ok := skeletonField.AsString(); ok {
			if path, ok := source2.ResourcePath(name); ok && strings.HasSuffix(path, ".vnmskel_c") {
				skeleton = path
			}
		}
	}
	if strings.HasSuffix(path, ".vnmclip_c") {
		settings, ok := value.Get("m_trackCompressionSettings")
		if !ok {
			return fail(issueNotChecked)
		}
		tracks, ok := settings.AsArray()
		if !ok || skeleton == "" {
			return fail(issueNotChecked)
		}
		return contractNodeResult{node: contractNode{kind: nodeClip, skeleton: skeleton, bones: len(tracks)}, kind: issueNotChecked, ok: true}
	}
	if hasSkeletonField && skeleton == "" {
		return fail(issueNotChecked)
	}
	resources, ok := value.Get("m_resources")
	if !ok {
		return fail(issueNotChecked)
	}
	items, ok := resources.AsArray()
	if !ok {
		return fail(issueNotChecked)
	}
	children := make([]string, 0, len(items))
	for i := range items {
		name, ok := items[i].AsString()
		if !ok {
			return fail(issueNotChecked)
		}
		child, ok := source2.ResourcePath(name)
		if !ok {
			return fail(issueNotChecked)
		}
		children = append(children, child)
	}
	return contractNodeResult{node: contractNode{kind: nodeGraph, skeleton: skeleton, children: children}, kind: issueNotChecked, ok: true}
}

func contractIssueDetail(kind contractIssueKind) string {
	switch kind {
	case issueMissingResource:
		return "Referenced animation resource is missing"
	case issueUnreadableResource:
		return "Animation resource could not be decoded"
	case issueInvalidSkeleton:
		return "Invalid bone names, parent hierarchy, or reference poses"
	}
	return "Animation resource layout is not covered by these checks"
}

func modelSkeletons(model *kv3.Value) ([]string, bool) {
	value, ok := model.Get("m_vecNmSkeletonRefs")
	if !ok {
		return nil, false
	}
	items, ok := value.AsArray()
	if !ok {
		return nil, false
	}
	seen := make(map[string]struct{})
	for i := range items {
		name, ok := items[i].AsString()
		if !ok {
			return nil, false
		}
		path, ok := source2.ResourcePath(name)
		if !ok || !strings.HasSuffix(path, ".vnmskel_c") {
			return nil, false
		}
		seen[path] = struct{}{}
	}
	return slices.Sorted(maps.Keys(seen)), true
}

func warningDetailResource(detail string) string {
	path, ok := source2.ResourcePath(detail)
	if !ok {
		return ""
	}
	return path
}
