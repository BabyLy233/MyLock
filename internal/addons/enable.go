package addons

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SetEnabled turns a mod on or off.
//
// The game loads pakNN_dir.vpk and ignores everything else, so a mod is enabled
// by giving its files those names in a shard directory, and disabled by putting
// the mod id back in front of them in the profile directory. Nothing is copied:
// the same bytes move between two names.
func (p Profile) SetEnabled(id string, on bool) error {
	m, err := Load(p.ManifestPath())
	if err != nil {
		return err
	}
	entry, ok := m.Mods[id]
	if !ok {
		return fmt.Errorf("there is no mod %q in %s", id, p.ManifestPath())
	}
	if entry.Enabled == on {
		return nil
	}

	var moves []move
	if on {
		entry, moves, err = p.enable(entry, id, m)
	} else {
		entry, moves, err = p.disable(entry, id)
	}
	if err != nil {
		return err
	}

	if err := renameAll(moves); err != nil {
		return err
	}
	// The manifest is written only once the files have moved, so a failure
	// leaves it describing what is on disk rather than what was intended.
	m.Mods[id] = entry
	if err := m.Save(p.ManifestPath()); err != nil {
		// Nothing on disk matches the manifest now, so put the files back
		// rather than leave the two disagreeing.
		renameBack(moves)
		return err
	}
	return nil
}

// enable works out the names a mod's files take when the game is to load them:
// free pak numbers in a shard directory.
func (p Profile) enable(entry Entry, id string, m *Manifest) (Entry, []move, error) {
	count := len(entry.DisabledVpks)
	if count == 0 {
		return entry, nil, fmt.Errorf("%q has no files to enable", id)
	}

	shard, err := p.chooseShard(m, id, entry.Shard, count)
	if err != nil {
		return entry, nil, err
	}
	dir := p.ShardDir(shard)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return entry, nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	names, err := allocateNames(dir, count)
	if err != nil {
		return entry, nil, err
	}

	moves := make([]move, count)
	for i, disabled := range entry.DisabledVpks {
		moves[i] = move{
			from: filepath.Join(p.Dir, disabled),
			to:   filepath.Join(dir, names[i]),
		}
	}
	entry.Enabled = true
	entry.Shard = shard
	entry.CurrentVpks = names
	entry.DisabledVpks = nil
	return entry, moves, nil
}

// disable works out the names a mod's files take when the game is to ignore
// them: the mod id, then the name the file had before it was ever enabled.
func (p Profile) disable(entry Entry, id string) (Entry, []move, error) {
	count := len(entry.CurrentVpks)
	if count == 0 {
		return entry, nil, fmt.Errorf("%q has no files to disable", id)
	}
	// The original names are aligned with the enabled ones, so a manifest that
	// lost some of them cannot say what a file should be called again. Naming
	// it by guess would risk writing over another mod's file.
	if len(entry.OriginalVpkNames) != count {
		return entry, nil, fmt.Errorf(
			"%q names %d files but %d original names, so they cannot be renamed back",
			id, count, len(entry.OriginalVpkNames))
	}

	dir := p.ShardDir(entry.Shard)
	names := make([]string, count)
	moves := make([]move, count)
	for i, current := range entry.CurrentVpks {
		names[i] = id + "_" + entry.OriginalVpkNames[i]
		moves[i] = move{
			from: filepath.Join(dir, current),
			to:   filepath.Join(p.Dir, names[i]),
		}
	}
	entry.Enabled = false
	entry.CurrentVpks = nil
	entry.DisabledVpks = names
	return entry, moves, nil
}

// chooseShard picks where a mod's enabled files go. The shard the mod is
// already filed under is preferred, and failing that the last shard in use:
// taking the first shard with room instead would move mods that are already
// loaded before it, changing what wins where two of them touch one file.
//
// id is the mod whose files are being placed, and it is left out of the count:
// a mod that is changing what it has enabled frees its own slots first.
func (p Profile) chooseShard(m *Manifest, id string, current uint32, count int) (uint32, error) {
	used := make(map[uint32]int, MaxShards)
	last := uint32(FirstShard)
	for modID, entry := range m.Mods {
		if !entry.Enabled || modID == id {
			continue
		}
		shard := entry.Shard
		if shard < FirstShard || shard > MaxShards {
			shard = FirstShard
		}
		used[shard] += len(entry.CurrentVpks)
		if shard > last {
			last = shard
		}
	}

	fits := func(shard uint32) bool { return used[shard]+count <= ShardCapacity }
	switch {
	case current >= FirstShard && current <= MaxShards && fits(current):
		return current, nil
	case fits(last):
		return last, nil
	case last < MaxShards:
		return last + 1, nil
	}
	return 0, fmt.Errorf(
		"there is no room for %d more files: all %d shards hold %d each",
		count, MaxShards, ShardCapacity)
}

// allocateNames returns count free enabled VPK names in dir, taking the lowest
// numbers so that the gaps a disabled mod leaves behind get used again.
func allocateNames(dir string, count int) ([]string, error) {
	if count > ShardCapacity {
		return nil, fmt.Errorf("%d files do not fit in one shard of %d", count, ShardCapacity)
	}

	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	used := make(map[int]bool, len(entries))
	for _, entry := range entries {
		if n := pakNumber(entry.Name()); n > 0 {
			used[n] = true
		}
	}

	names := make([]string, 0, count)
	for n := 1; len(names) < count; n++ {
		if n > ShardCapacity {
			return nil, fmt.Errorf("%s already holds %d enabled files", dir, ShardCapacity)
		}
		if used[n] {
			continue
		}
		names = append(names, fmt.Sprintf("pak%02d_dir.vpk", n))
	}
	return names, nil
}

// move is one file taking a new name.
type move struct{ from, to string }

// renameAll makes every move, and undoes the ones already made when one of them
// fails, so a profile is never left holding half a mod.
func renameAll(moves []move) error {
	for i, mv := range moves {
		if err := os.Rename(mv.from, mv.to); err != nil {
			renameBack(moves[:i])
			return fmt.Errorf("renaming %s: %w", filepath.Base(mv.from), err)
		}
	}
	return nil
}

// renameBack undoes moves, latest first, which is the order they were made in.
// Its own failures are not reported: there is nothing left to do about them,
// and the error that called for the rollback is the one worth showing.
func renameBack(moves []move) {
	for i := len(moves) - 1; i >= 0; i-- {
		os.Rename(moves[i].to, moves[i].from)
	}
}
