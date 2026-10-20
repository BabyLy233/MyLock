package archive

import (
	"archive/zip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures in testdata hold this tree, whichever format they are in. The
// zip is built by the tests instead, which is also how the archives that would
// write outside their directory are made.
var fixture = map[string]string{
	"mod/pak01_dir.vpk": "the index of a vpk\n",
	"mod/pak01_000.vpk": "a chunk of a vpk\n",
	"readme.txt":        "a mod, by someone\n",
}

// writeZip builds a zip holding the named files, in order.
func writeZip(t *testing.T, path string, files map[string]string, order ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for _, name := range order {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		if mode, ok := link(name); ok {
			// A zip says what an entry is in the top half of its external
			// attributes, which is where the mode is read back from.
			h.SetMode(mode)
		}
		e, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// link reports a name that stands for a symlink rather than a file, which the
// tests write with this prefix so that the archive can carry one.
func link(name string) (fs.FileMode, bool) {
	rest, ok := strings.CutPrefix(name, "->")
	return fs.ModeSymlink | 0o777, ok && rest != ""
}

// names returns the fixture's names in a fixed order, so that a zip built from
// it is the same every run.
func names() []string {
	return []string{"mod/pak01_dir.vpk", "mod/pak01_000.vpk", "readme.txt"}
}

// extracted reads back what an extraction left, as a map of the paths under dir
// to their contents.
func extracted(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// assertFixture checks that an extraction left exactly the tree the archive
// holds, folders and all.
func assertFixture(t *testing.T, dir string) {
	t.Helper()
	got := extracted(t, dir)
	if len(got) != len(fixture) {
		t.Fatalf("the extraction left %d files, want %d: %v", len(got), len(fixture), got)
	}
	for name, want := range fixture {
		if got[name] != want {
			t.Errorf("%s holds %q, want %q", name, got[name], want)
		}
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "mod.zip")
	writeZip(t, src, fixture, names()...)

	if err := Extract(src, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	assertFixture(t, filepath.Join(dir, "out"))
}

// The rar and 7z fixtures are in testdata because neither format can be written
// in Go. See testdata/generate.sh.
func TestExtractRar(t *testing.T) {
	dir := t.TempDir()

	if err := Extract("testdata/mod.rar", dir); err != nil {
		t.Fatal(err)
	}
	assertFixture(t, dir)
}

func TestExtractSevenZip(t *testing.T) {
	dir := t.TempDir()

	if err := Extract("testdata/mod.7z", dir); err != nil {
		t.Fatal(err)
	}
	assertFixture(t, dir)
}

// An archive that names a path outside the directory is refused, and nothing is
// written there.
func TestExtractRefusesAPathOutsideTheDirectory(t *testing.T) {
	for _, name := range []string{
		"../evil.txt",
		"../../evil.txt",
		"mod/../../evil.txt",
		"/etc/evil.txt",
		`..\evil.txt`,
		`C:\evil.txt`,
		"C:evil.txt",
		"..",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "out")
			src := filepath.Join(t.TempDir(), "mod.zip")
			writeZip(t, src, map[string]string{name: "evil"}, name)

			if err := Extract(src, dir); err == nil {
				t.Fatalf("an entry named %q was unpacked", name)
			}
			// The name climbs out of the directory the caller named, so
			// anything it wrote would be beside it rather than in it.
			if _, err := os.Stat(filepath.Join(root, "evil.txt")); err == nil {
				t.Errorf("an entry named %q wrote outside the directory", name)
			}
			if got := extracted(t, root); len(got) != 0 {
				t.Errorf("an entry named %q left %v behind", name, got)
			}
		})
	}
}

// A link is not something a mod archive has any business carrying, and one
// pointing out of the directory would let everything after it be written
// through it.
func TestExtractRefusesALink(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "mod.zip")
	writeZip(t, src, map[string]string{
		"->mod/pak01_dir.vpk": "/etc/passwd",
	}, "->mod/pak01_dir.vpk")

	err := Extract(src, filepath.Join(root, "out"))
	if err == nil {
		t.Fatal("an archive holding a link was unpacked")
	}
	if !strings.Contains(err.Error(), "link") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

// An entry with no name at all is a file that would land on the directory
// itself, which is not a thing to guess at.
func TestExtractRefusesAnEntryWithNoUsableName(t *testing.T) {
	for _, name := range []string{".", "mod/.."} {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "mod.zip")
			writeZip(t, src, map[string]string{name: "x"}, name)

			if err := Extract(src, t.TempDir()); err == nil {
				t.Errorf("an entry named %q was unpacked", name)
			}
		})
	}
}

// The names an archive can hold are checked directly as well, because the zip
// writer will not write all of them: it turns a backslash into a slash on the
// way in, so the end-to-end test above cannot reach the case that a
// Windows-authored archive is the one that matters for.
func TestDest(t *testing.T) {
	dir := filepath.Join("tmp", "out")

	for _, tt := range []struct {
		name string
		want string
	}{
		{"mod/pak01_dir.vpk", filepath.Join(dir, "mod", "pak01_dir.vpk")},
		{"readme.txt", filepath.Join(dir, "readme.txt")},
		{`mod\pak01_dir.vpk`, filepath.Join(dir, "mod", "pak01_dir.vpk")},
		{"./readme.txt", filepath.Join(dir, "readme.txt")},
		{"mod/./readme.txt", filepath.Join(dir, "mod", "readme.txt")},
		// A name that only looks like it climbs out is fine, and is what a
		// folder named ".." inside a real archive would be.
		{"a/../readme.txt", filepath.Join(dir, "readme.txt")},
		{"..hidden/x", filepath.Join(dir, "..hidden", "x")},
	} {
		got, err := entry{Name: tt.name}.dest(dir)
		if err != nil {
			t.Errorf("dest(%q) refused a name it should take: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("dest(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}

	for _, name := range []string{
		"",
		".",
		"..",
		"../evil.txt",
		"../../evil.txt",
		"mod/../../evil.txt",
		"mod/..",
		"/etc/evil.txt",
		"//etc/evil.txt",
		`..\evil.txt`,
		`mod\..\..\evil.txt`,
		`C:\evil.txt`,
		"C:evil.txt",
		`C:evil.txt`,
		"/",
	} {
		if got, err := (entry{Name: name}).dest(dir); err == nil {
			t.Errorf("dest(%q) = %q, want it refused", name, got)
		}
	}
}

// The directory is made rather than required, because the caller names one
// inside a working directory it has just made.
func TestExtractMakesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	src := filepath.Join(t.TempDir(), "mod.zip")
	writeZip(t, src, fixture, names()...)

	if err := Extract(src, dir); err != nil {
		t.Fatal(err)
	}
	assertFixture(t, dir)
}

// The extension is what decides, so a file that is not one of the three is
// refused rather than read and guessed at.
func TestFormatOf(t *testing.T) {
	for _, tt := range []struct {
		name string
		want Format
		ok   bool
	}{
		{"mod.zip", Zip, true},
		{"mod.ZIP", Zip, true},
		{"yamato_red_d0d08.rar", Rar, true},
		{"mod.7z", SevenZip, true},
		{"mod.7Z", SevenZip, true},
		{"mod.vpk", "", false},
		{"mod.tar.gz", "", false},
		{"mod", "", false},
	} {
		got, ok := FormatOf(tt.name)
		if got != tt.want || ok != tt.ok {
			t.Errorf("FormatOf(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestExtractRefusesAFileThatIsNotAnArchive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "mod.vpk")
	if err := os.WriteFile(src, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Extract(src, dir)
	if err == nil {
		t.Fatal("a file that is not an archive was unpacked")
	}
	if !strings.Contains(err.Error(), "mod.vpk") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

// A file that is named as an archive but is not one is reported, rather than
// leaving the caller to find an empty directory.
func TestExtractReportsAnArchiveItCannotRead(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "mod.zip")
	if err := os.WriteFile(src, []byte("not a zip at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Extract(src, dir); err == nil {
		t.Fatal("a file that is not a zip was unpacked")
	}
	if got := extracted(t, dir); len(got) != 0 {
		t.Errorf("the extraction left %v behind", got)
	}
}

// An archive holding folders of its own is unpacked as it names them, because
// the folders are where the caller looks for the VPKs afterwards.
func TestExtractKeepsTheFolders(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "mod.zip")
	writeZip(t, src, map[string]string{
		"a/b/c/pak01_dir.vpk": "vpk",
	}, "a/b/c/pak01_dir.vpk")

	if err := Extract(src, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b", "c", "pak01_dir.vpk")); err != nil {
		t.Errorf("the folders the archive names were not kept: %v", err)
	}
}
