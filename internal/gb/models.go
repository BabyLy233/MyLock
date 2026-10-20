package gb

import (
	"strconv"
	"strings"
	"time"
)

// The API names its fields with a leading underscore and a tag for the type:
// _sName for a string, _nLikeCount for a number, _aImages for an array,
// _bHasFiles for a boolean, _tsDateAdded for a timestamp. The types below keep
// those names, so that the shape of a reply can be read against the API's own
// documentation, and the app is handed plain names instead.

// Kind is the sort of submission something is filed as. Deadlock's mods are
// spread over all three: about five thousand mods, four thousand sound packs,
// and a hundred works in progress.
type Kind string

const (
	Mod   Kind = "Mod"
	Sound Kind = "Sound"
	Wip   Kind = "Wip"
)

// Kinds are all of them, in the order a page offers them.
var Kinds = []Kind{Mod, Sound, Wip}

// Submission is one catalogue entry as a listing shows it: a mod, a sound pack,
// or a work in progress.
type Submission struct {
	// ID is the submission's number on GameBanana, which is what its page's
	// address ends in and what an installed mod is remembered by.
	ID int
	// Kind is which of the three listings it came from. The id alone does not
	// say: the three are numbered separately.
	Kind Kind
	// Name is the title the author gave it.
	Name string
	// Version is the author's own, often empty.
	Version string
	// Author is the submitter's display name.
	Author string
	// Category is the section it is filed under, such as Skins.
	Category string
	// Subcategory is the one under that, such as Yamato.
	Subcategory string
	// Likes, Views are how it has been received.
	Likes int
	Views int
	// Added is when it was first published, Updated when it last changed.
	// Either is the zero time when the API did not say.
	Added   time.Time
	Updated time.Time
	// Thumb is a small image for a list, Image a larger one for a page. Either
	// is empty when the author posted no picture.
	Thumb string
	Image string
	// HasFiles reports whether there is anything to download. A submission
	// without files is one whose author has not uploaded yet.
	HasFiles bool
}

// Page returns the submission's page on the site, which is where a user goes to
// read about it, and where a failure is reported.
func (s Submission) Page() string {
	return "https://gamebanana.com/mods/" + strconv.Itoa(s.ID)
}

// Slug is the name an installed mod is known by: its number for a mod, and the
// number with a prefix for a sound pack or a work in progress, because the
// three listings number their submissions separately and two of them could
// otherwise collide.
//
// This is the Tauri app's own scheme, and the manifest the two share is keyed
// by it, so the two agree about which mod is which.
func (s Submission) Slug() string {
	switch s.Kind {
	case Sound:
		return "snd-" + strconv.Itoa(s.ID)
	case Wip:
		return "wip-" + strconv.Itoa(s.ID)
	default:
		return strconv.Itoa(s.ID)
	}
}

// File is one downloadable file of a submission. Most submissions carry
// several -- the variants an author published -- and each is a separate
// archive the user picks between.
type File struct {
	// ID is the file's number, which is what its download address ends in.
	ID int
	// Name is the file's own name, which is what the archive is written to
	// disk as and what says which format it is in.
	Name string
	// Description is the author's note on this variant, often empty.
	Description string
	// Size is how many bytes the archive is, which the API reports and the
	// download is checked against.
	Size int64
	// MD5 is the checksum of the archive, which is what says the download
	// arrived whole. Empty when the API did not give one.
	MD5 string
	// URL is the download address. It redirects to a file server.
	URL string
	// Downloads is how many times it has been taken.
	Downloads int
	// Added is when the file was uploaded.
	Added time.Time
	// Scan is what the API's own virus scan made of the file: "clean" is the
	// only answer worth showing, and anything else is worth saying.
	Scan string
}

// submissionJSON is one record of a listing.
type submissionJSON struct {
	ID        int    `json:"_idRow"`
	Kind      Kind   `json:"_sModelName"`
	Name      string `json:"_sName"`
	Version   string `json:"_sVersion"`
	Submitter struct {
		Name string `json:"_sName"`
	} `json:"_aSubmitter"`
	RootCategory struct {
		Name string `json:"_sName"`
	} `json:"_aRootCategory"`
	SubCategory struct {
		Name string `json:"_sName"`
	} `json:"_aSubCategory"`
	PreviewMedia struct {
		Images []struct {
			Base  string `json:"_sBaseUrl"`
			File  string `json:"_sFile"`
			Small string `json:"_sFile220"`
		} `json:"_aImages"`
	} `json:"_aPreviewMedia"`
	Likes    int   `json:"_nLikeCount"`
	Views    int   `json:"_nViewCount"`
	Added    int64 `json:"_tsDateAdded"`
	Modified int64 `json:"_tsDateModified"`
	HasFiles bool  `json:"_bHasFiles"`
}

// submission converts a record, filling in what the listing did not carry.
func (r submissionJSON) submission() Submission {
	s := Submission{
		ID:          r.ID,
		Kind:        r.Kind,
		Name:        r.Name,
		Version:     r.Version,
		Author:      r.Submitter.Name,
		Category:    r.RootCategory.Name,
		Subcategory: r.SubCategory.Name,
		Likes:       r.Likes,
		Views:       r.Views,
		Added:       unix(r.Added),
		Updated:     unix(r.Modified),
		HasFiles:    r.HasFiles,
	}
	// The first picture stands for the submission, which is how the site's own
	// listings do it. Each is published at several sizes, and the list wants
	// the small one where a page wants the large.
	if images := r.PreviewMedia.Images; len(images) > 0 {
		first := images[0]
		s.Thumb = imageURL(first.Base, first.Small)
		s.Image = imageURL(first.Base, first.File)
	}
	if s.Thumb == "" {
		s.Thumb = s.Image
	}
	return s
}

// fileJSON is one file of a submission's download page.
type fileJSON struct {
	ID          int    `json:"_idRow"`
	Name        string `json:"_sFile"`
	Description string `json:"_sDescription"`
	Size        int64  `json:"_nFilesize"`
	MD5         string `json:"_sMd5Checksum"`
	URL         string `json:"_sDownloadUrl"`
	Downloads   int    `json:"_nDownloadCount"`
	Added       int64  `json:"_tsDateAdded"`
	Scan        string `json:"_sAvResult"`
}

func (f fileJSON) file() File {
	return File{
		ID:          f.ID,
		Name:        f.Name,
		Description: f.Description,
		Size:        f.Size,
		MD5:         strings.ToLower(f.MD5),
		URL:         f.URL,
		Downloads:   f.Downloads,
		Added:       unix(f.Added),
		Scan:        f.Scan,
	}
}

// imageURL joins the two halves of an image's address. The API splits it: a
// directory every image of that kind shares, and the file's own name.
func imageURL(base, file string) string {
	if base == "" || file == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/") + "/" + file
}

// unix reads a timestamp, of which the API sends 0 for "never".
func unix(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
