package addons

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// InstalledFile is one VPK a mod was installed with, and whether the game is
// meant to load it.
type InstalledFile struct {
	// Name is the file's own name, without the mod id prefix it carries while
	// it is disabled.
	Name string
	// Selected reports that this file is one of the mod's enabled files.
	Selected bool
}

// Files lists every VPK file the mod has in this profile, named the way it was
// before the profile prefixed it, and which of them are selected.
//
// A mod of several files can have only some of them turned on. The selected
// ones are the manifest's current files while the mod is enabled, and its
// disabled files while it is not; the rest are the prefixed files the profile
// holds but the game does not load. The two are found together so that a file
// turned off by a selection can be turned back on later.
func (p Profile) Files(id string) ([]InstalledFile, error) {
	if err := checkModID(id); err != nil {
		return nil, err
	}
	m, err := Load(p.ManifestPath())
	if err != nil {
		return nil, err
	}
	entry, ok := m.Mods[id]
	if !ok {
		return nil, fmt.Errorf("there is no mod %q in %s", id, p.ManifestPath())
	}

	// What is selected is what the manifest names. While the mod is enabled
	// that is its current files; while it is disabled it is the files the next
	// enable will turn on.
	names := entry.DisabledVpks
	if entry.Enabled {
		names = entry.CurrentVpks
	}
	if len(entry.OriginalVpkNames) != len(names) {
		return nil, fmt.Errorf(
			"the manifest for %q names %d files but %d original names, so it cannot say which are selected",
			id, len(names), len(entry.OriginalVpkNames))
	}
	selected := make(map[string]bool, len(entry.OriginalVpkNames))
	for _, name := range entry.OriginalVpkNames {
		selected[name] = true
	}

	// The prefixed files in the profile are everything the mod has, whether or
	// not the manifest names it: a file left out of a selection stays there.
	available := make(map[string]bool, len(selected))
	for name := range selected {
		available[name] = true
	}
	prefixed, err := p.prefixedFiles(id)
	if err != nil {
		return nil, err
	}
	for name := range prefixed {
		available[name] = true
	}

	files := make([]InstalledFile, 0, len(available))
	for name := range available {
		files = append(files, InstalledFile{Name: name, Selected: selected[name]})
	}
	slices.SortFunc(files, func(a, b InstalledFile) int {
		return strings.Compare(a.Name, b.Name)
	})
	return files, nil
}

// SetFiles turns the named files on and the rest of the mod's files off. It is
// how one file of a mod that ships several is chosen without turning the whole
// mod off.
//
// An enabled mod has everything taken out of its shard first, and only the
// chosen files are named for the game again; the rest keep the mod id prefix
// in the profile directory. A disabled mod only records the choice: its files
// are all prefixed already, and the next enable turns on those named here.
//
// The names are the files' own, the ones Files reports, not the pak names a
// shard holds. At least one file has to stay selected; a mod with none is one
// the user disables rather than one with no files.
func (p Profile) SetFiles(id string, originals []string) error {
	if err := checkModID(id); err != nil {
		return err
	}
	if len(originals) == 0 {
		return fmt.Errorf("a mod has to keep at least one file; disable it to turn all of them off")
	}
	selected := slices.Clone(originals)
	slices.Sort(selected)
	for i, name := range selected {
		if !plainVpkName(name) {
			return fmt.Errorf("%q is not a name a mod's file can have", name)
		}
		if i > 0 && selected[i-1] == name {
			return fmt.Errorf("%q was chosen twice", name)
		}
	}

	m, err := Load(p.ManifestPath())
	if err != nil {
		return err
	}
	entry, ok := m.Mods[id]
	if !ok {
		return fmt.Errorf("there is no mod %q in %s", id, p.ManifestPath())
	}

	// The files currently in a shard, by the name they had before it renamed
	// them. A manifest that cannot say that cannot be changed safely.
	current := make(map[string]string, len(entry.CurrentVpks))
	if entry.Enabled {
		if len(entry.CurrentVpks) != len(entry.OriginalVpkNames) {
			return fmt.Errorf(
				"the manifest for %q names %d enabled files but %d original names, so they cannot be swapped",
				id, len(entry.CurrentVpks), len(entry.OriginalVpkNames))
		}
		for i, name := range entry.OriginalVpkNames {
			current[name] = entry.CurrentVpks[i]
		}
	}

	available := make(map[string]bool, len(current)+len(entry.OriginalVpkNames))
	for name := range current {
		available[name] = true
	}
	for _, name := range entry.OriginalVpkNames {
		available[name] = true
	}
	prefixed, err := p.prefixedFiles(id)
	if err != nil {
		return err
	}
	for name := range prefixed {
		available[name] = true
	}
	for _, name := range selected {
		if !available[name] {
			return fmt.Errorf("the mod has no file called %q", name)
		}
	}

	if sameNames(selected, entry.OriginalVpkNames) {
		return nil
	}

	if !entry.Enabled {
		// Nothing has to move: the files are already prefixed. What changes is
		// which of them the next enable will pick up.
		entry.DisabledVpks = prefixedNames(id, selected)
		entry.OriginalVpkNames = selected
		entry.CurrentVpks = nil
		m.Mods[id] = entry
		return m.Save(p.ManifestPath())
	}

	// Take everything the mod has enabled out of its shard, so that the chosen
	// files can be given the free numbers. Unchosen ones stay prefixed.
	disable := make([]move, 0, len(entry.CurrentVpks))
	for i, currentName := range entry.CurrentVpks {
		disable = append(disable, move{
			from: filepath.Join(p.ShardDir(entry.Shard), currentName),
			to:   filepath.Join(p.Dir, id+"_"+entry.OriginalVpkNames[i]),
		})
	}
	if err := renameAll(disable); err != nil {
		return err
	}

	// The mod's own files are not counted here: they have just been taken out
	// of their shard, and the new selection is what is going in.
	shard, err := p.chooseShard(m, id, entry.Shard, len(selected))
	if err != nil {
		renameBack(disable)
		return err
	}
	dir := p.ShardDir(shard)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		renameBack(disable)
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	names, err := allocateNames(dir, len(selected))
	if err != nil {
		renameBack(disable)
		return err
	}

	enable := make([]move, len(selected))
	for i, original := range selected {
		enable[i] = move{
			from: filepath.Join(p.Dir, id+"_"+original),
			to:   filepath.Join(dir, names[i]),
		}
	}
	if err := renameAll(enable); err != nil {
		renameBack(disable)
		return err
	}

	entry.Enabled = true
	entry.Shard = shard
	entry.CurrentVpks = names
	entry.OriginalVpkNames = selected
	entry.DisabledVpks = nil
	m.Mods[id] = entry
	if err := m.Save(p.ManifestPath()); err != nil {
		// Put the shard back the way it was, then the prefixed files, so that
		// the manifest that was not written still describes the disk.
		renameBack(enable)
		renameBack(disable)
		return err
	}
	return nil
}

// prefixedFiles is the files in the profile directory that carry the mod's id
// prefix, by the name they have without it.
func (p Profile) prefixedFiles(id string) (map[string]bool, error) {
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", p.Dir, err)
	}

	prefix := id + "_"
	names := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok || !plainVpkName(name) {
			continue
		}
		names[name] = true
	}
	return names, nil
}

// prefixedNames is the file names as a disabled mod's manifest holds them.
func prefixedNames(id string, originals []string) []string {
	names := make([]string, len(originals))
	for i, original := range originals {
		names[i] = id + "_" + original
	}
	return names
}

// sameNames reports whether two lists hold the same names, in any order.
func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
