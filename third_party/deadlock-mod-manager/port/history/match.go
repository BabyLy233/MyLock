// SPDX-FileCopyrightText: 2026 esoc
// SPDX-License-Identifier: GPL-3.0-only
//
// Ported from deadlock-mod-manager
// apps/desktop/src-tauri/src/mod_manager/vdata_history.rs at
// 41afe0e706726f7c5882fb589f996b84362114fa. See ../../NOTICE.md.

package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/kv3"
)

const (
	// minComparableRows is the confidence floor for a baseline match: a small
	// authored table can match many builds by coincidence.
	minComparableRows = 32
	// maxChangedPercent bounds how much of a matched table may have been edited.
	maxChangedPercent = 10
	maxRetainedBuilds = 64
)

// Seg is a path into a KV3 value. The data-compatibility code shares the same
// path type, so the aliases keep the history API in its original vocabulary.
type Seg = kv3.Seg

// SegKind selects key and index path segments.
type SegKind = kv3.SegKind

const (
	SegKey   = kv3.SegKey
	SegIndex = kv3.SegIndex
)

// KeySeg returns an object-member path segment.
func KeySeg(key string) Seg { return kv3.KeySeg(key) }

// IndexSeg returns an array-element path segment.
func IndexSeg(index uint64) Seg { return kv3.IndexSeg(index) }

// FingerprintChange is one row or field update inside a recorded build.
type FingerprintChange struct {
	Key   fingerprint
	Value *fingerprint
}

// BuildDelta is one recorded game build as a delta over the previous build.
type BuildDelta struct {
	Version   uint32
	Rows      []FingerprintChange
	ExactRows []FingerprintChange
	Fields    []FingerprintChange
}

type tableHistory struct {
	exact  bool
	builds []BuildDelta
}

// BaselineStatus says how strongly an authored table matched a recorded build.
type BaselineStatus string

const (
	// BaselineMatched means exact row and field fingerprints agreed.
	BaselineMatched BaselineStatus = "matched"
	// BaselineLegacyApproximate means the match comes from the older history
	// format, so floating-point values cannot be proven unchanged.
	BaselineLegacyApproximate BaselineStatus = "legacyApproximate"
	// BaselineAmbiguous means several equally close builds disagree about the
	// authored edits.
	BaselineAmbiguous BaselineStatus = "ambiguous"
	// BaselineUnavailable means no recorded build was close enough.
	BaselineUnavailable BaselineStatus = "unavailable"
)

// BaselineEvidence explains a baseline match without exposing the fingerprints.
type BaselineEvidence struct {
	Status         BaselineStatus
	BuildVersions  []uint32
	ComparableRows int
	ChangedRows    int
}

// BaselineMatch is the closest recorded game build for an authored table.
type BaselineMatch struct {
	BuildVersion   uint32
	ComparableRows int
	ChangedRows    int
	rows           map[fingerprint]fingerprint
	fields         map[fingerprint]fingerprint
	exact          bool
}

// Version returns the matched build version.
func (b *BaselineMatch) Version() uint32 { return b.BuildVersion }

// ComparableCount returns the number of rows the match was scored on.
func (b *BaselineMatch) ComparableCount() int { return b.ComparableRows }

// ChangedCount returns the number of rows that differ from the recorded build.
func (b *BaselineMatch) ChangedCount() int { return b.ChangedRows }

// ContainsRow reports whether the recorded build has the row.
func (b *BaselineMatch) ContainsRow(rowName string) bool {
	_, ok := b.rows[rowKey(rowName)]
	return ok
}

// RowMatches reports whether the authored row is byte-identical to the
// recorded build's row. Legacy history cannot prove a row with floats
// unchanged.
func (b *BaselineMatch) RowMatches(rowName string, value kv3.Value) bool {
	if !b.exact && containsDouble(value) {
		return false
	}
	known, ok := b.rows[rowKey(rowName)]
	return ok && known == valueFingerprint(value, modeExact)
}

// FieldMatches reports whether the authored field is byte-identical to the
// recorded build's field. Legacy history cannot prove a float field unchanged.
func (b *BaselineMatch) FieldMatches(rowName string, path []Seg, value kv3.Value) bool {
	if !b.exact && containsDouble(value) {
		return false
	}
	known, ok := b.fields[fieldPathKey(rowName, path)]
	return ok && known == valueFingerprint(value, modeExact)
}

// View combines the learned history with the shipped one. The learned index is
// consulted first, matching the original's observed-over-embedded order.
type View struct {
	learned *Index
	shipped *Index
}

// WithObserved returns a view that consults observed first and the shipped
// history second.
func (i *Index) WithObserved(observed *Index) *View {
	return &View{learned: observed, shipped: i}
}

// ObservedStringField reports whether either history saw the field with this
// exact string value.
func (v *View) ObservedStringField(filePath, row, field, value string) bool {
	return v.learned.ObservedStringField(filePath, row, field, value) ||
		v.shipped.ObservedStringField(filePath, row, field, value)
}

// MatchBaseline is MatchWithEvidence without the evidence.
func (v *View) MatchBaseline(filePath string, root kv3.Value) *BaselineMatch {
	baseline, _ := v.MatchWithEvidence(filePath, root)
	return baseline
}

// MatchWithEvidence returns the closest recorded build for an authored table,
// taking the better of the learned and shipped histories. Equally close builds
// that disagree about which authored rows changed are reported as ambiguous.
func (v *View) MatchWithEvidence(filePath string, root kv3.Value) (*BaselineMatch, BaselineEvidence) {
	primary, primaryEvidence := v.learned.MatchWithEvidence(filePath, root)
	fallback, fallbackEvidence := v.shipped.MatchWithEvidence(filePath, root)
	if primaryEvidence.Status == BaselineUnavailable {
		return fallback, fallbackEvidence
	}
	if fallbackEvidence.Status == BaselineUnavailable {
		return primary, primaryEvidence
	}
	quality := primaryEvidence.ChangedRows*fallbackEvidence.ComparableRows -
		fallbackEvidence.ChangedRows*primaryEvidence.ComparableRows
	switch {
	case quality < 0:
		return primary, primaryEvidence
	case quality > 0:
		return fallback, fallbackEvidence
	}
	sameIntent := false
	if primary != nil && fallback != nil {
		intent := newCandidateIntent(root)
		sameIntent = slices.Equal(primary.intent(intent), fallback.intent(intent))
	}
	primaryEvidence.BuildVersions = append(primaryEvidence.BuildVersions, fallbackEvidence.BuildVersions...)
	slices.Sort(primaryEvidence.BuildVersions)
	primaryEvidence.BuildVersions = slices.Compact(primaryEvidence.BuildVersions)
	if !sameIntent {
		primary = nil
		primaryEvidence.Status = BaselineAmbiguous
	}
	return primary, primaryEvidence
}

// MatchWithEvidence returns the closest recorded build for an authored table.
func (i *Index) MatchWithEvidence(filePath string, root kv3.Value) (*BaselineMatch, BaselineEvidence) {
	if i == nil {
		return nil, BaselineEvidence{Status: BaselineUnavailable, BuildVersions: []uint32{}}
	}
	return i.matchTable(i.tables[normalizePath(filePath)], root)
}

// MatchBaseline is MatchWithEvidence without the evidence.
func (i *Index) MatchBaseline(filePath string, root kv3.Value) *BaselineMatch {
	baseline, _ := i.MatchWithEvidence(filePath, root)
	return baseline
}

func (i *Index) matchTable(table *tableHistory, root kv3.Value) (*BaselineMatch, BaselineEvidence) {
	unavailable := func() (*BaselineMatch, BaselineEvidence) {
		return nil, BaselineEvidence{Status: BaselineUnavailable, BuildVersions: []uint32{}}
	}
	if table == nil {
		return unavailable()
	}
	candidateRows, ok := rowFingerprints(root, modeDiscovery)
	if !ok {
		return unavailable()
	}
	rows := make(map[fingerprint]fingerprint)
	var best struct {
		found      bool
		comparable int
		changed    int
	}
	var bestIndices []int
	for index, build := range table.builds {
		applyChanges(rows, build.Rows)
		comparable := 0
		changed := 0
		for key, value := range candidateRows {
			known, exists := rows[key]
			if !exists {
				continue
			}
			comparable++
			if known != value {
				changed++
			}
		}
		if comparable < minComparableRows || changed*100 > comparable*maxChangedPercent {
			continue
		}
		replace := !best.found
		if best.found {
			left := changed * best.comparable
			right := best.changed * comparable
			replace = left < right || (left == right && comparable > best.comparable)
		}
		if replace {
			best.found = true
			best.comparable = comparable
			best.changed = changed
			bestIndices = bestIndices[:0]
		}
		if best.comparable == comparable && best.changed == changed {
			bestIndices = append(bestIndices, index)
		}
	}
	if !best.found {
		return unavailable()
	}
	evidence := BaselineEvidence{
		Status:         BaselineMatched,
		BuildVersions:  make([]uint32, 0, len(bestIndices)),
		ComparableRows: best.comparable,
		ChangedRows:    best.changed,
	}
	if !table.exact {
		evidence.Status = BaselineLegacyApproximate
	}
	for _, index := range bestIndices {
		evidence.BuildVersions = append(evidence.BuildVersions, table.builds[index].Version)
	}

	rows = make(map[fingerprint]fingerprint)
	fields := make(map[fingerprint]fingerprint)
	var selected *BaselineMatch
	var selectedIntent []tri
	var intentCheck *candidateIntent
	if len(bestIndices) > 1 {
		intentCheck = newCandidateIntent(root)
	}
	lastIndex := bestIndices[len(bestIndices)-1]
	for index, build := range table.builds {
		if table.exact {
			applyChanges(rows, build.ExactRows)
		} else {
			applyChanges(rows, build.Rows)
		}
		applyChanges(fields, build.Fields)
		if !slices.Contains(bestIndices, index) {
			continue
		}
		baseline := &BaselineMatch{
			BuildVersion:   build.Version,
			ComparableRows: best.comparable,
			ChangedRows:    best.changed,
			rows:           copyFingerprints(rows),
			fields:         copyFingerprints(fields),
			exact:          table.exact,
		}
		if intentCheck != nil {
			intent := baseline.intent(intentCheck)
			if selectedIntent != nil && !slices.Equal(selectedIntent, intent) {
				evidence.Status = BaselineAmbiguous
				return nil, evidence
			}
			selectedIntent = intent
		}
		selected = baseline
		if index == lastIndex {
			break
		}
	}
	return selected, evidence
}

func (b *BaselineMatch) intent(candidate *candidateIntent) []tri {
	values := make([]tri, 0, len(candidate.rows)+len(candidate.fields))
	for _, item := range candidate.rows {
		known, ok := b.rows[item.key]
		values = append(values, triFor(ok, b.exact, item, known))
	}
	for _, item := range candidate.fields {
		known, ok := b.fields[item.key]
		values = append(values, triFor(ok, b.exact, item, known))
	}
	return values
}

// tri is a three-valued match: unknown, different, or equal.
type tri int8

const (
	triUnknown tri = iota
	triDifferent
	triEqual
)

func triFor(knownFound, exact bool, item candidateValue, value fingerprint) tri {
	if !knownFound {
		return triUnknown
	}
	if !exact && item.double {
		return triDifferent
	}
	if value == item.value {
		return triEqual
	}
	return triDifferent
}

type candidateValue struct {
	key    fingerprint
	value  fingerprint
	double bool
}

type candidateIntent struct {
	rows   []candidateValue
	fields []candidateValue
}

func newCandidateIntent(root kv3.Value) *candidateIntent {
	intent := &candidateIntent{}
	if root.Kind != kv3.KindObject {
		return intent
	}
	for _, row := range root.Object {
		intent.rows = append(intent.rows, candidateValue{
			key:    rowKey(row.Key),
			value:  valueFingerprint(row.Value, modeExact),
			double: containsDouble(row.Value),
		})
		collectCandidateFields(row.Key, row.Value, nil, &intent.fields)
	}
	return intent
}

func collectCandidateFields(rowName string, value kv3.Value, path []Seg, output *[]candidateValue) {
	if value.Kind == kv3.KindObject {
		for _, pair := range value.Object {
			if strings.EqualFold(pair.Key, "_editor") {
				continue
			}
			collectCandidateFields(rowName, pair.Value, append(path, KeySeg(pair.Key)), output)
		}
		return
	}
	*output = append(*output, candidateValue{
		key:    fieldPathKey(rowName, path),
		value:  valueFingerprint(value, modeExact),
		double: containsDouble(value),
	})
}

func containsDouble(value kv3.Value) bool {
	switch value.Kind {
	case kv3.KindDouble:
		return true
	case kv3.KindArray:
		for _, item := range value.Array {
			if containsDouble(item) {
				return true
			}
		}
	case kv3.KindObject:
		for _, pair := range value.Object {
			if strings.EqualFold(pair.Key, "_editor") {
				continue
			}
			if containsDouble(pair.Value) {
				return true
			}
		}
	}
	return false
}

func combineChanges(old []FingerprintChange, next *[]FingerprintChange) {
	values := make(map[fingerprint]fingerprint)
	applyChanges(values, old)
	applyChanges(values, *next)
	*next = mapChanges(map[fingerprint]fingerprint{}, values)
}

// ToBytes encodes the index in the current exact on-disk format.
func (i *Index) ToBytes() ([]byte, error) {
	output := append([]byte(nil), magic[:]...)
	if err := pushU32(&output, len(i.tables)); err != nil {
		return nil, err
	}
	for _, path := range slices.Sorted(maps.Keys(i.tables)) {
		table := i.tables[path]
		if !table.exact {
			return nil, fmt.Errorf("Approximate history cannot be written as exact history")
		}
		if err := pushString(&output, path); err != nil {
			return nil, err
		}
		if err := pushU32(&output, len(table.builds)); err != nil {
			return nil, err
		}
		for _, build := range table.builds {
			pushU32Value(&output, build.Version)
			if err := pushChanges(&output, build.Rows); err != nil {
				return nil, err
			}
			if err := pushChanges(&output, build.ExactRows); err != nil {
				return nil, err
			}
			if err := pushChanges(&output, build.Fields); err != nil {
				return nil, err
			}
		}
	}
	if err := writeStringBindings(&output, i.observed); err != nil {
		return nil, err
	}
	return output, nil
}

// HistoricalBuild is one recorded full-table snapshot used when the embedded
// index is rebuilt.
type HistoricalBuild struct {
	Version uint32
	Root    kv3.Value
}

// HistoricalTable is the authored history of one game table.
type HistoricalTable struct {
	Path   string
	Builds []HistoricalBuild
}

// EncodeHistoryIndex builds a history index from full-table snapshots.
func EncodeHistoryIndex(tables []HistoricalTable) ([]byte, error) {
	output := append([]byte(nil), magic[:]...)
	if err := pushU32(&output, len(tables)); err != nil {
		return nil, err
	}
	strings := make(map[fingerprint]map[fingerprint]struct{})
	for _, table := range tables {
		if err := pushString(&output, table.Path); err != nil {
			return nil, err
		}
		if err := pushU32(&output, len(table.Builds)); err != nil {
			return nil, err
		}
		previousRows := make(map[fingerprint]fingerprint)
		previousExactRows := make(map[fingerprint]fingerprint)
		previousFields := make(map[fingerprint]fingerprint)
		for _, build := range table.Builds {
			for key, value := range stringBindings(table.Path, build.Root) {
				values := strings[key]
				if values == nil {
					values = make(map[fingerprint]struct{})
					strings[key] = values
				}
				values[value] = struct{}{}
			}
			rows, ok := rowFingerprints(build.Root, modeDiscovery)
			if !ok {
				return nil, fmt.Errorf("Historical %s build %d root is not an object", table.Path, build.Version)
			}
			exactRows, _ := rowFingerprints(build.Root, modeExact)
			fields, ok := fieldFingerprints(build.Root, modeExact)
			if !ok {
				return nil, fmt.Errorf("Historical %s build %d root is not an object", table.Path, build.Version)
			}
			pushU32Value(&output, build.Version)
			if err := pushChanges(&output, mapChanges(previousRows, rows)); err != nil {
				return nil, err
			}
			if err := pushChanges(&output, mapChanges(previousExactRows, exactRows)); err != nil {
				return nil, err
			}
			if err := pushChanges(&output, mapChanges(previousFields, fields)); err != nil {
				return nil, err
			}
			previousRows = rows
			previousExactRows = exactRows
			previousFields = fields
		}
	}
	if err := writeStringBindings(&output, strings); err != nil {
		return nil, err
	}
	return output, nil
}

func stringBindings(filePath string, root kv3.Value) map[fingerprint]fingerprint {
	output := make(map[fingerprint]fingerprint)
	if root.Kind != kv3.KindObject {
		return output
	}
	for _, row := range root.Object {
		collectStringBindings(filePath, row.Key, row.Value, nil, output)
	}
	return output
}

func collectStringBindings(filePath, row string, value kv3.Value, path []Seg, output map[fingerprint]fingerprint) {
	switch value.Kind {
	case kv3.KindObject:
		for _, pair := range value.Object {
			if strings.EqualFold(pair.Key, "_editor") {
				continue
			}
			collectStringBindings(filePath, row, pair.Value, append(path, KeySeg(pair.Key)), output)
		}
	case kv3.KindString:
		output[stringBindingKey(filePath, fieldPathKey(row, path))] = valueFingerprint(value, modeExact)
	}
}

func writeStringBindings(output *[]byte, bindings map[fingerprint]map[fingerprint]struct{}) error {
	if err := pushU32(output, len(bindings)); err != nil {
		return err
	}
	for _, key := range sortedFingerprints(bindings) {
		*output = append(*output, key[:]...)
		values := bindings[key]
		if err := pushU32(output, len(values)); err != nil {
			return err
		}
		for _, value := range sortedFingerprints(values) {
			*output = append(*output, value[:]...)
		}
	}
	return nil
}

func sortedFingerprints[V any](values map[fingerprint]V) []fingerprint {
	out := make([]fingerprint, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	slices.SortFunc(out, func(a, b fingerprint) int { return bytes.Compare(a[:], b[:]) })
	return out
}

type fingerprintMode uint8

const (
	modeDiscovery fingerprintMode = iota
	modeExact
)

func rowFingerprints(root kv3.Value, mode fingerprintMode) (map[fingerprint]fingerprint, bool) {
	if root.Kind != kv3.KindObject {
		return nil, false
	}
	rows := make(map[fingerprint]fingerprint, len(root.Object))
	for _, row := range root.Object {
		rows[rowKey(row.Key)] = valueFingerprint(row.Value, mode)
	}
	return rows, true
}

func fieldFingerprints(root kv3.Value, mode fingerprintMode) (map[fingerprint]fingerprint, bool) {
	if root.Kind != kv3.KindObject {
		return nil, false
	}
	fields := make(map[fingerprint]fingerprint)
	for _, row := range root.Object {
		collectFieldFingerprints(row.Key, row.Value, nil, fields, mode)
	}
	return fields, true
}

func collectFieldFingerprints(rowName string, value kv3.Value, path []Seg,
	output map[fingerprint]fingerprint, mode fingerprintMode) {
	if value.Kind == kv3.KindObject {
		for _, pair := range value.Object {
			if strings.EqualFold(pair.Key, "_editor") {
				continue
			}
			collectFieldFingerprints(rowName, pair.Value, append(path, KeySeg(pair.Key)), output, mode)
		}
		return
	}
	output[fieldPathKey(rowName, path)] = valueFingerprint(value, mode)
}

func rowKey(rowName string) fingerprint {
	return hashParts([]byte("row"), []byte(asciiLower(rowName)))
}

func fieldPathKey(rowName string, path []Seg) fingerprint {
	hasher := sha256.New()
	hashPart(hasher, []byte("field"))
	hashPart(hasher, []byte(asciiLower(rowName)))
	for _, segment := range path {
		if segment.Kind == SegIndex {
			hashPart(hasher, []byte("index"))
			var raw [8]byte
			binary.LittleEndian.PutUint64(raw[:], segment.Index)
			hashPart(hasher, raw[:])
			continue
		}
		hashPart(hasher, []byte("key"))
		hashPart(hasher, []byte(asciiLower(segment.Key)))
	}
	var out fingerprint
	copy(out[:], hasher.Sum(nil))
	return out
}

func valueFingerprint(value kv3.Value, mode fingerprintMode) fingerprint {
	hasher := sha256.New()
	hashValue(hasher, value, mode)
	var out fingerprint
	copy(out[:], hasher.Sum(nil))
	return out
}

func hashValue(hasher hash.Hash, value kv3.Value, mode fingerprintMode) {
	switch value.Kind {
	case kv3.KindNull:
		hashPart(hasher, []byte("null"))
	case kv3.KindBool:
		flag := byte(0)
		if value.Bool {
			flag = 1
		}
		hashPart(hasher, []byte("bool"))
		hashPart(hasher, []byte{flag})
	case kv3.KindInt:
		var raw [8]byte
		binary.LittleEndian.PutUint64(raw[:], uint64(value.Int))
		hashPart(hasher, []byte("int"))
		hashPart(hasher, raw[:])
	case kv3.KindUInt:
		var raw [8]byte
		binary.LittleEndian.PutUint64(raw[:], value.UInt)
		hashPart(hasher, []byte("uint"))
		hashPart(hasher, raw[:])
	case kv3.KindDouble:
		// Compiler noise can identify a likely build, but cannot prove an edit
		// was absent.
		number := value.Double
		if mode == modeDiscovery {
			number = canonicalDouble(number)
		}
		var raw [8]byte
		binary.LittleEndian.PutUint64(raw[:], math.Float64bits(number))
		hashPart(hasher, []byte("double"))
		hashPart(hasher, raw[:])
	case kv3.KindString:
		hashPart(hasher, []byte("string"))
		hashPart(hasher, []byte(value.String))
	case kv3.KindBinary:
		hashPart(hasher, []byte("binary"))
		hashPart(hasher, value.Binary)
	case kv3.KindArray:
		hashPart(hasher, []byte("array"))
		var count [8]byte
		binary.LittleEndian.PutUint64(count[:], uint64(len(value.Array)))
		hashPart(hasher, count[:])
		for _, item := range value.Array {
			hashValue(hasher, item, mode)
		}
	case kv3.KindObject:
		fields := make([]kv3.Pair, 0, len(value.Object))
		for _, pair := range value.Object {
			if strings.EqualFold(pair.Key, "_editor") {
				continue
			}
			fields = append(fields, pair)
		}
		slices.SortStableFunc(fields, func(a, b kv3.Pair) int {
			return strings.Compare(asciiLower(a.Key), asciiLower(b.Key))
		})
		hashPart(hasher, []byte("object"))
		var count [8]byte
		binary.LittleEndian.PutUint64(count[:], uint64(len(fields)))
		hashPart(hasher, count[:])
		for _, pair := range fields {
			hashPart(hasher, []byte(asciiLower(pair.Key)))
			hashValue(hasher, pair.Value, mode)
		}
	}
}

func canonicalDouble(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
		return value
	}
	exponent := int(math.Floor(math.Log10(math.Abs(value))))
	scale := math.Pow(10, float64(2-exponent))
	return math.Round(value*scale) / scale
}

func mapChanges(previous, current map[fingerprint]fingerprint) []FingerprintChange {
	keys := make(map[fingerprint]struct{}, len(previous)+len(current))
	for key := range previous {
		keys[key] = struct{}{}
	}
	for key := range current {
		keys[key] = struct{}{}
	}
	changes := make([]FingerprintChange, 0, len(keys))
	for _, key := range sortedFingerprints(keys) {
		old, hadOld := previous[key]
		value, hasValue := current[key]
		if hadOld == hasValue && old == value {
			continue
		}
		change := FingerprintChange{Key: key}
		if hasValue {
			value := value
			change.Value = &value
		}
		changes = append(changes, change)
	}
	return changes
}

func applyChanges(values map[fingerprint]fingerprint, changes []FingerprintChange) {
	for _, change := range changes {
		if change.Value == nil {
			delete(values, change.Key)
			continue
		}
		values[change.Key] = *change.Value
	}
}

func copyFingerprints(source map[fingerprint]fingerprint) map[fingerprint]fingerprint {
	out := make(map[fingerprint]fingerprint, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func mapsEqual(a, b map[fingerprint]fingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func pushChanges(output *[]byte, changes []FingerprintChange) error {
	if err := pushU32(output, len(changes)); err != nil {
		return err
	}
	for _, change := range changes {
		*output = append(*output, change.Key[:]...)
		if change.Value == nil {
			*output = append(*output, 0)
			continue
		}
		*output = append(*output, 1)
		*output = append(*output, change.Value[:]...)
	}
	return nil
}

func pushString(output *[]byte, value string) error {
	if err := pushU32(output, len(value)); err != nil {
		return err
	}
	*output = append(*output, value...)
	return nil
}

func pushU32(output *[]byte, value int) error {
	if uint64(value) > math.MaxUint32 {
		return fmt.Errorf("VData history index is too large")
	}
	pushU32Value(output, uint32(value))
	return nil
}

func pushU32Value(output *[]byte, value uint32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	*output = append(*output, raw[:]...)
}

// asciiLower matches Rust's to_ascii_lowercase, which the fingerprints were
// generated with. Unicode case folding would change the hash for non-ASCII
// names.
func asciiLower(value string) string {
	buffer := []byte(value)
	for index, char := range buffer {
		if char >= 'A' && char <= 'Z' {
			buffer[index] = char + ('a' - 'A')
		}
	}
	return string(buffer)
}
