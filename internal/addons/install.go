package addons

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Install adds a mod's files to the profile, turned off.
//
// The files are copied rather than moved, because they come from a directory
// the caller is going to throw away. They are written under the mod id, which
// is what keeps two mods' pak01_dir.vpk apart while both are off.
//
// The manifest is written last, and what was copied is taken away again when
// anything fails, so a failure leaves the profile as it was rather than
// describing files that are not there.
func (p Profile) Install(id string, files []File) error {
	if err := checkModID(id); err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%q has no files to install", id)
	}
	if len(files) > ShardCapacity {
		return fmt.Errorf("%q has %d files, and one shard holds %d", id, len(files), ShardCapacity)
	}

	m, err := Load(p.ManifestPath())
	if err != nil {
		return err
	}
	if _, ok := m.Mods[id]; ok {
		return fmt.Errorf("%q is already installed", id)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}

	names := make([]string, len(files))
	written := make([]string, 0, len(files))
	undo := func() {
		for _, name := range written {
			os.Remove(filepath.Join(p.Dir, name))
		}
	}

	for i, f := range files {
		// The name is part of a path, and it came out of an archive a stranger
		// wrote, so it is checked rather than trusted.
		if !plainVpkName(f.Name) {
			undo()
			return fmt.Errorf("%q is not a name a mod's file can be installed as", f.Name)
		}
		names[i] = id + "_" + f.Name
		dest := filepath.Join(p.Dir, names[i])
		if _, err := os.Stat(dest); err == nil {
			undo()
			return fmt.Errorf("%s is already there", dest)
		}
		if err := copyFile(f.Path, dest); err != nil {
			undo()
			return err
		}
		written = append(written, names[i])
	}

	originals := make([]string, len(files))
	for i, f := range files {
		originals[i] = f.Name
	}
	m.Mods[id] = Entry{
		Shard:            FirstShard,
		DisabledVpks:     names,
		OriginalVpkNames: originals,
	}
	if err := m.Save(p.ManifestPath()); err != nil {
		undo()
		return err
	}
	return nil
}

// Remove takes a mod out of the profile, files and all. It is what putting back
// a mod the user installed by mistake is, and what replacing one with a newer
// version of itself starts with.
func (p Profile) Remove(id string) error {
	m, err := Load(p.ManifestPath())
	if err != nil {
		return err
	}
	entry, ok := m.Mods[id]
	if !ok {
		return fmt.Errorf("there is no mod %q in %s", id, p.ManifestPath())
	}

	// Where the files are depends on whether the mod is on: an enabled mod's
	// are in a shard directory under the names the game loads them by, and a
	// disabled one's are in the profile directory under the mod's own prefix.
	dir, names := p.Dir, entry.DisabledVpks
	if entry.Enabled {
		dir, names = p.ShardDir(entry.Shard), entry.CurrentVpks
	}

	// The manifest is written first here, so that a file which could not be
	// removed is left as something the next read reports as missing rather than
	// as a mod nothing knows about.
	delete(m.Mods, id)
	if err := m.Save(p.ManifestPath()); err != nil {
		return err
	}

	var failed []error
	for _, name := range names {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// copyFile copies src to dst, which must not already be there.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("copying %s: %w", filepath.Base(src), err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
