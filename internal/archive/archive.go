// Package archive unpacks the archives mods are published as.
//
// Everything here is written as though the archive is hostile, because it is:
// it comes from a stranger on the internet, and this is a program that unpacks
// strangers' archives into the game's directory. Every entry is checked before
// it is written, so nothing an archive names is ever written outside the
// directory it was given.
//
// A refused archive may have had some of its entries written already, which is
// why the caller is expected to unpack into a directory of its own that it
// throws away on a failure, rather than into one it means to keep.
package archive

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Format is an archive's kind.
type Format string

const (
	Zip      Format = "zip"
	Rar      Format = "rar"
	SevenZip Format = "7z"
)

const (
	// filePerm and dirPerm are what unpacked files are given, whatever the
	// archive claims. A mod's files are read by the game and never run, so
	// there is nothing to be gained from the modes an archive carries and a
	// setuid bit to be lost by trusting them.
	filePerm = 0o644
	dirPerm  = 0o755
)

// FormatOf reports which kind of archive a file is, by its name. Every file
// GameBanana serves is named, and a mod's archive is named for its format, so
// the name is what decides: the contents are not sniffed, and an archive that
// is misnamed is refused rather than guessed at.
func FormatOf(name string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip":
		return Zip, true
	case ".rar":
		return Rar, true
	case ".7z":
		return SevenZip, true
	}
	return "", false
}

// Extract unpacks the archive at src into dir, which is made if it is not
// there.
//
// The files keep the paths the archive gives them, folders and all: a mod's
// VPKs are usually inside one, and flattening would put two mods' files on top
// of each other.
func Extract(src, dir string) error {
	format, ok := FormatOf(src)
	if !ok {
		return fmt.Errorf("%s is not an archive this can unpack", filepath.Base(src))
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	switch format {
	case Zip:
		return extractZip(src, dir)
	case Rar:
		return extractRar(src, dir)
	case SevenZip:
		return extractSevenZip(src, dir)
	}
	return fmt.Errorf("%s is not an archive this can unpack", filepath.Base(src))
}

// entry is one file of an archive, as the extractors hand it over.
type entry struct {
	// Name is the path the archive says the file has, with either slash.
	Name string
	// Mode is what the archive says the entry is. Only the bit saying whether
	// it is a link is read; the permissions are not.
	Mode fs.FileMode
}

// write unpacks one entry into dir.
func (e entry) write(dir string, r io.Reader) error {
	dest, err := e.dest(dir)
	if err != nil {
		return err
	}
	// A link is not something a mod archive has any business carrying. One
	// pointing out of the directory would let everything written after it land
	// wherever it points, so the archive is refused rather than the entry
	// skipped: an archive holding one is not one to unpack. This is also why
	// links are never made, whatever the format says -- a link that is never
	// created cannot be written through.
	if e.Mode&fs.ModeSymlink != 0 {
		return fmt.Errorf("the archive holds a link: %q", e.Name)
	}
	if err := os.MkdirAll(filepath.Dir(dest), dirPerm); err != nil {
		return err
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("unpacking %q: %w", e.Name, err)
	}
	return f.Close()
}

// dest turns an entry's name into a path inside dir.
//
// The name is a claim, not a fact. "../" climbs out, "/etc/passwd" is absolute,
// "C:\..." names a drive, and a backslash separates on Windows even where it
// does not here. Every one of those is refused rather than cleaned up: an
// archive holding one was not built to be unpacked politely.
func (e entry) dest(dir string) (string, error) {
	// A backslash separates wherever this runs, so that an archive written on
	// Windows reads the same here as it does there.
	name := strings.ReplaceAll(e.Name, `\`, "/")
	if name == "" {
		return "", errors.New("the archive holds an entry with no name")
	}

	// Cleaned first, so that "a/../../b" is seen as the "../b" it is.
	clean := path.Clean(name)
	if clean == "." {
		return "", fmt.Errorf("the archive holds an entry with no usable name: %q", e.Name)
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("the archive holds a path outside itself: %q", e.Name)
	}
	// A drive letter is a prefix only in a leading position, which is the one
	// place Clean leaves it, and it is not a separator on any platform this
	// runs on, so it is looked for by hand.
	if len(clean) >= 2 && clean[1] == ':' {
		return "", fmt.Errorf("the archive holds an absolute path: %q", e.Name)
	}
	return filepath.Join(dir, filepath.FromSlash(clean)), nil
}
