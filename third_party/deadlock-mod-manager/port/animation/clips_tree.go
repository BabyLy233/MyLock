// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/localization_overlay/animation_clips/layout/tree.rs
// at 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package animation

import "slices"

type layoutKey struct {
	node int
	low  int
	high int
}

type layoutResult struct {
	order []int
	ok    bool
}

// layoutTree recovers the NM import's two ordered sampling partitions using
// named pose anchors and the authored hierarchy.
type layoutTree struct {
	children [][]int
	anchors  []int
	lowCount int
	members  [][]int
	counts   [][2]int
	cache    map[layoutKey]layoutResult
	budget   int
}

func newLayoutTree(children [][]int, anchors []int, lowCount int) *layoutTree {
	members := make([][]int, len(children))
	for node := len(children) - 1; node >= 0; node-- {
		members[node] = append(members[node], node)
		for _, child := range children[node] {
			members[node] = append(members[node], members[child]...)
		}
	}
	counts := make([][2]int, len(members))
	for node := range members {
		low := 0
		for _, bone := range members[node] {
			if bone < lowCount {
				low++
			}
		}
		counts[node] = [2]int{low, len(members[node]) - low}
	}
	return &layoutTree{
		children: children,
		anchors:  anchors,
		lowCount: lowCount,
		members:  members,
		counts:   counts,
		cache:    make(map[layoutKey]layoutResult),
		budget:   20_000,
	}
}

func (t *layoutTree) solve(node, low, high int) ([]int, bool) {
	if t.budget == 0 {
		return nil, false
	}
	t.budget--
	key := layoutKey{node: node, low: low, high: high}
	if cached, ok := t.cache[key]; ok {
		return slices.Clone(cached.order), cached.ok
	}
	order, ok := t.solveNode(node, low, high)
	t.cache[key] = layoutResult{order: slices.Clone(order), ok: ok}
	return order, ok
}

func (t *layoutTree) solveNode(node, low, high int) ([]int, bool) {
	lowSize, highSize := t.counts[node][0], t.counts[node][1]
	for _, bone := range t.members[node] {
		anchor := t.anchors[bone]
		if anchor == -1 {
			continue
		}
		start, size := high, highSize
		if bone < t.lowCount {
			start, size = low, lowSize
		}
		if anchor < start || anchor >= start+size {
			return nil, false
		}
	}
	isLow := node < t.lowCount
	target := high
	if isLow {
		target = low
	}
	if anchor := t.anchors[node]; anchor != -1 && anchor != target {
		return nil, false
	}
	result := []int{node}
	positions := [2]int{low, high}
	positions[0] += boolInt(isLow)
	positions[1] += boolInt(!isLow)
	tail, ok := t.place(t.children[node], positions[0], positions[1])
	if !ok {
		return nil, false
	}
	result = append(result, tail...)
	slices.SortStableFunc(result, func(left, right int) int {
		return boolInt(left >= t.lowCount) - boolInt(right >= t.lowCount)
	})
	return result, true
}

func (t *layoutTree) place(remaining []int, low, high int) ([]int, bool) {
	if len(remaining) == 0 {
		return nil, true
	}
	for position, child := range remaining {
		order, ok := t.solve(child, low, high)
		if !ok {
			continue
		}
		rest := make([]int, 0, len(remaining)-1)
		rest = append(rest, remaining[:position]...)
		rest = append(rest, remaining[position+1:]...)
		lowSize, highSize := t.counts[child][0], t.counts[child][1]
		tail, ok := t.place(rest, low+lowSize, high+highSize)
		if ok {
			return append(order, tail...), true
		}
	}
	return nil, false
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
