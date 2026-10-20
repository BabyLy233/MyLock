package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// The three readers are walked the same way: each entry is handed to
// entry.write, which is where every check lives, so a format added later is
// covered by them without anything being repeated here. Folders are skipped
// rather than made: the file inside one makes it on the way.

// extractZip unpacks a zip.
func extractZip(src, dir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = entry{Name: f.Name, Mode: f.Mode()}.write(dir, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// extractRar unpacks a rar. It is read as a stream, so an entry is checked as
// it arrives rather than the whole archive being listed first.
func extractRar(src, dir string) error {
	r, err := rardecode.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for {
		hdr, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.IsDir {
			continue
		}
		// A rar link is not a file's contents, whatever its size says, and the
		// reader would hand over the target's path as though it were one.
		if hdr.LinkType != 0 {
			return fmt.Errorf("the archive holds a link: %q", hdr.Name)
		}
		if err := (entry{Name: hdr.Name, Mode: hdr.Mode()}).write(dir, r); err != nil {
			return err
		}
	}
}

// extractSevenZip unpacks a 7z.
func extractSevenZip(src, dir string) error {
	r, err := sevenzip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = entry{Name: f.Name, Mode: f.Mode()}.write(dir, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
