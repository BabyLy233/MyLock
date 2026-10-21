package addons

import (
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// qualifierMax is how many characters of a name may say which variant a file
// belongs to.
const qualifierMax = 48

// File is one VPK of a mod: where it is in the archive it came from, and what
// it is called once installed.
type File struct {
	// Path is where the VPK is on disk, which is where Install reads it from.
	Path string
	// Name is what the file is called once installed, which is unique among
	// the mod's files.
	Name string
	// Size is how many bytes it is.
	Size int64
}

// found is a VPK as the walk finds it, before it has a name of its own. The
// path is kept relative to the directory its archive was unpacked into, because
// the name is worked out from the folders under it rather than from wherever
// that directory happens to be.
type found struct {
	// archive is the name of the download the file came in.
	archive string
	rel     string
	path    string
	name    string
	size    int64
}

// Archive is one unpacked download of a mod: what the file is called, and the
// directory it was unpacked into.
//
// A submission on the catalogue often offers several downloads -- variants of a
// skin, a base file and its extras -- and the user can install more than one as
// a single mod. Each is unpacked into a directory of its own, and the files are
// named together so that two archives' pak01_dir.vpk do not become one file.
type Archive struct {
	Name string
	Dir  string
}

// FindVpks returns the VPKs under dir, named as a mod's files.
func FindVpks(dir, archiveName string) ([]File, error) {
	return FindArchives([]Archive{{Name: archiveName, Dir: dir}})
}

// FindArchives returns the VPKs of several unpacked downloads, named as one
// mod's files.
//
// A mod is often several VPKs, and the variants an author publishes very often
// all ship a file called pak01_dir.vpk. Each is named after the archive and the
// folders it was in, so that those stay apart, and the names are worked out
// from the files alone, so that the same set of archives always gives the same
// ones.
//
// The Tauri app this is a port of names them the same way, including the files
// of several archives taken together, and the manifest the two share records
// the names, so the two agree about which file is which.
func FindArchives(archives []Archive) ([]File, error) {
	var founds []found
	for _, archive := range archives {
		err := filepath.WalkDir(archive.Dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".vpk") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(archive.Dir, p)
			if err != nil {
				return err
			}
			founds = append(founds, found{
				archive: archive.Name,
				rel:     filepath.ToSlash(rel),
				path:    p,
				name:    d.Name(),
				size:    info.Size(),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("looking for the mod's files: %w", err)
		}
	}

	// Names are claimed in a fixed order, by archive and then by path, so that
	// the same set of files comes out with the same names whatever order the
	// walks found them in. The order is also the order they are enabled in,
	// which is what decides which of them the game loads first.
	slices.SortStableFunc(founds, func(a, b found) int {
		if c := cmp.Compare(a.archive, b.archive); c != 0 {
			return c
		}
		return cmp.Compare(a.rel, b.rel)
	})

	counts := make(map[string]int, len(founds))
	for _, f := range founds {
		counts[strings.ToLower(f.name)]++
	}

	taken := make(map[string]bool, len(founds))
	files := make([]File, len(founds))
	for i, f := range founds {
		// A file whose name is shared with another is named after its archive
		// and the folders it was in; one that is already on its own keeps the
		// name its author gave it.
		name := f.name
		if counts[strings.ToLower(f.name)] > 1 {
			name = variantName(f.rel, f.name, f.archive)
		}
		files[i] = File{
			Path: f.path,
			Name: claimName(taken, name),
			Size: f.size,
		}
	}
	return files, nil
}

// variantName works the archive's name and the folders a file was in into the
// file's name, so that the variants of one mod do not all come out the same:
// the pak01_dir.vpk of Red Skin.zip becomes pak01-red-skin_dir.vpk. The
// _dir.vpk ending is kept, because other mod managers look for it.
func variantName(rel, name, archiveName string) string {
	stem := strings.TrimSuffix(filepath.Base(archiveName), filepath.Ext(archiveName))

	// Everything before the file's own name, which is the folders it was under.
	folders := path.Dir(rel)
	if folders == "." {
		folders = ""
	}
	return qualifyName(name, slug(stem+" "+folders))
}

// claimName returns the first of a name's candidates that is not taken, adding
// a number to it until one is free.
//
// Taken names are compared without case, because Windows treats two names
// differing only in case as one file, and a mod installed on Windows and on
// Linux should be the same mod.
func claimName(taken map[string]bool, candidate string) string {
	for n := 1; ; n++ {
		name := candidate
		if n > 1 {
			name = qualifyName(candidate, strconv.Itoa(n))
		}
		key := strings.ToLower(name)
		if !taken[key] {
			taken[key] = true
			return name
		}
	}
}

// qualifyName puts a qualifier into a VPK's name, before its .vpk ending so
// that the name still looks like one.
func qualifyName(name, qualifier string) string {
	if qualifier == "" {
		return name
	}
	lower := strings.ToLower(name)
	for _, suffix := range []string{"_dir.vpk", ".vpk"} {
		if strings.HasSuffix(lower, suffix) {
			stem := name[:len(name)-len(suffix)]
			return stem + "-" + qualifier + name[len(name)-len(suffix):]
		}
	}
	return name + "-" + qualifier
}

// slug turns text into something usable in a file name: letters and digits kept
// as they are, and anything else a single dash between them.
//
// Letters outside ASCII are kept rather than dropped, because two archives
// whose names differ only in those still need names of their own.
func slug(text string) string {
	var b strings.Builder
	// A dash is written before the next letter rather than after the last one,
	// so that a run of punctuation becomes one dash and none is left at the end.
	dash := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		dash = true
	}

	s := b.String()
	if utf8.RuneCountInString(s) > qualifierMax {
		// Cut on a letter boundary, so that a name which is not ASCII is not
		// cut through the middle of one.
		s = string([]rune(s)[:qualifierMax])
	}
	return s
}

// plainVpkName reports whether a name is one a file may be installed as: one
// plain component ending in .vpk, with none of the characters Windows forbids.
// The colon is the one that matters: a file called "a:b.vpk" would write an
// alternate data stream rather than a file.
func plainVpkName(name string) bool {
	if name == "" || filepath.Base(name) != name {
		return false
	}
	if strings.ContainsAny(name, `/\<>:"|?*`) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return strings.EqualFold(filepath.Ext(name), ".vpk")
}

// checkModID refuses an id that could name something outside the profile: it is
// part of a file name, and it comes from a catalogue rather than from the user.
func checkModID(id string) error {
	if id == "" {
		return fmt.Errorf("a mod needs an id")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("%q is not a usable mod id", id)
	}
	return nil
}
