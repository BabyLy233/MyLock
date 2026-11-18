// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/vdata_history.rs (VdataSnapshot) at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package history

import (
	"fmt"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

// Snapshot is the fingerprint set of one decoded game table. The observation
// cache keeps snapshots so the same table bytes are not fingerprinted again
// for every mod that touches them.
type Snapshot struct {
	discovery map[fingerprint]fingerprint
	exact     map[fingerprint]fingerprint
	fields    map[fingerprint]fingerprint
	bindings  map[fingerprint]fingerprint
}

// NewSnapshot fingerprints one decoded table. The root must be an object: a
// table's rows are its members.
func NewSnapshot(filePath string, root kv3.Value) (*Snapshot, error) {
	if root.Kind != kv3.KindObject {
		return nil, fmt.Errorf("Observed VData root is not an object")
	}
	discovery, _ := rowFingerprints(root, modeDiscovery)
	exact, _ := rowFingerprints(root, modeExact)
	fields, _ := fieldFingerprints(root, modeExact)
	return &Snapshot{
		discovery: discovery,
		exact:     exact,
		fields:    fields,
		bindings:  stringBindings(filePath, root),
	}, nil
}

// CacheSize estimates the memory the snapshot keeps, charging every entry and
// its container overhead conservatively.
func (s *Snapshot) CacheSize() int {
	return (len(s.discovery) + len(s.exact) + len(s.fields) + len(s.bindings)) * 192
}

// Record adds one observed table snapshot to the learned history. It returns
// false when the snapshot adds nothing new apart from string bindings.
func (i *Index) Record(filePath string, version uint32, root kv3.Value) (bool, error) {
	snapshot, err := NewSnapshot(filePath, root)
	if err != nil {
		return false, err
	}
	return i.RecordSnapshot(filePath, version, snapshot)
}

// RecordSnapshot adds a fingerprinted table without walking the decoded value
// again. It is the path the observation cache uses.
func (i *Index) RecordSnapshot(filePath string, version uint32, snapshot *Snapshot) (bool, error) {
	bindingsChanged := false
	for key, value := range snapshot.bindings {
		bindingsChanged = i.add(key, value) || bindingsChanged
	}
	if i.tables == nil {
		i.tables = make(map[string]*tableHistory)
	}
	path := normalizePath(filePath)
	table := i.tables[path]
	if table == nil {
		table = &tableHistory{exact: true}
		i.tables[path] = table
	}
	if !table.exact {
		return false, fmt.Errorf("Observed history must use exact fingerprints")
	}
	previousRows := make(map[fingerprint]fingerprint)
	previousExact := make(map[fingerprint]fingerprint)
	previousFields := make(map[fingerprint]fingerprint)
	for _, build := range table.builds {
		applyChanges(previousRows, build.Rows)
		applyChanges(previousExact, build.ExactRows)
		applyChanges(previousFields, build.Fields)
	}
	if mapsEqual(previousExact, snapshot.exact) && mapsEqual(previousFields, snapshot.fields) {
		return bindingsChanged, nil
	}
	table.builds = append(table.builds, BuildDelta{
		Version:   version,
		Rows:      mapChanges(previousRows, snapshot.discovery),
		ExactRows: mapChanges(previousExact, snapshot.exact),
		Fields:    mapChanges(previousFields, snapshot.fields),
	})
	// Compact the retained prefix into one complete snapshot, keeping the
	// latest 64 changes.
	if len(table.builds) > maxRetainedBuilds {
		oldest := table.builds[0]
		table.builds = table.builds[1:]
		combineChanges(oldest.Rows, &table.builds[0].Rows)
		combineChanges(oldest.ExactRows, &table.builds[0].ExactRows)
		combineChanges(oldest.Fields, &table.builds[0].Fields)
	}
	return true, nil
}
