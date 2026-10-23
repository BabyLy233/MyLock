package addons

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

// Replace puts another set of files in a mod's place, and reports whether the
// game was loading the mod before it did.
//
// An update is a replacement rather than a second install: the manifest is
// keyed by mod id, so a newer version takes the older one's place. The old
// files are moved aside before the new ones are copied in, because the next
// version of a mod very often has the same file names, and both sets cannot be
// there at once. The manifest is written last, and a failure before that puts
// the old files back, so an update that fails leaves the profile as it was.
//
// The mod is left disabled, as it is after Install; the caller turns on one
// that was on before, because whether the game is running is the caller's to
// know.
func (p Profile) Replace(id string, files []File) (enabled bool, err error) {
	if err := checkModID(id); err != nil {
		return false, err
	}
	if len(files) == 0 {
		return false, fmt.Errorf("%q has no files to install", id)
	}
	if len(files) > ShardCapacity {
		return false, fmt.Errorf("%q has %d files, and one shard holds %d", id, len(files), ShardCapacity)
	}
	for _, f := range files {
		// The name is part of a path, and it came out of an archive a stranger
		// wrote, so it is checked rather than trusted.
		if !plainVpkName(f.Name) {
			return false, fmt.Errorf("%q is not a name a mod's file can be installed as", f.Name)
		}
	}

	m, err := Load(p.ManifestPath())
	if err != nil {
		return false, err
	}
	entry, ok := m.Mods[id]
	if !ok {
		return false, fmt.Errorf("there is no mod %q in %s", id, p.ManifestPath())
	}
	wasEnabled := entry.Enabled

	// The old files are kept together out of the way until the manifest names
	// the new ones, rather than deleted: an author who replaced a file with a
	// broken one must not cost the user the version that worked.
	backup, err := os.MkdirTemp(p.Dir, ".replace-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(backup)

	moved := make([]move, 0, len(entry.CurrentVpks)+len(entry.DisabledVpks))
	restore := func() { renameBack(moved) }

	// An enabled mod's files are in its shard. A disabled mod's are all in the
	// profile directory, and the scan below takes them along with the ones a
	// selection left out of the manifest.
	if entry.Enabled {
		for i, name := range entry.CurrentVpks {
			from := filepath.Join(p.ShardDir(entry.Shard), name)
			if _, statErr := os.Stat(from); errors.Is(statErr, os.ErrNotExist) {
				// A file the manifest names but the disk does not have is
				// already as good as gone.
				continue
			}
			to := filepath.Join(backup, strconv.Itoa(i))
			if err := os.Rename(from, to); err != nil {
				restore()
				return false, fmt.Errorf("moving %s aside: %w", name, err)
			}
			moved = append(moved, move{from: from, to: to})
		}
	}

	// Every prefixed file of the mod goes aside too, whether the manifest names
	// it or a per-file selection left it out: the new version very often uses
	// the same names, and a copy cannot write over one that is still there.
	prefixed, err := p.prefixedFiles(id)
	if err != nil {
		restore()
		return false, err
	}
	i := 0
	for name := range prefixed {
		from := filepath.Join(p.Dir, id+"_"+name)
		to := filepath.Join(backup, "prefixed-"+strconv.Itoa(i))
		if err := os.Rename(from, to); err != nil {
			restore()
			return false, fmt.Errorf("moving %s aside: %w", name, err)
		}
		moved = append(moved, move{from: from, to: to})
		i++
	}

	names := make([]string, len(files))
	written := make([]string, 0, len(files))
	undo := func() {
		for _, name := range written {
			os.Remove(filepath.Join(p.Dir, name))
		}
		restore()
	}
	for i, f := range files {
		names[i] = id + "_" + f.Name
		dest := filepath.Join(p.Dir, names[i])
		if err := copyFile(f.Path, dest); err != nil {
			undo()
			return false, err
		}
		written = append(written, names[i])
	}

	originals := make([]string, len(files))
	for i, f := range files {
		originals[i] = f.Name
	}
	entry.Enabled = false
	entry.CurrentVpks = nil
	entry.DisabledVpks = names
	entry.OriginalVpkNames = originals
	if entry.Shard < FirstShard {
		entry.Shard = FirstShard
	}
	m.Mods[id] = entry
	if err := m.Save(p.ManifestPath()); err != nil {
		undo()
		return false, err
	}
	return wasEnabled, nil
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
	// A file a per-file selection left out is still the mod's, and going away
	// with it is what removing the mod means.
	prefixed, err := p.prefixedFiles(id)
	if err != nil {
		failed = append(failed, err)
	}
	for name := range prefixed {
		path := filepath.Join(p.Dir, id+"_"+name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
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
