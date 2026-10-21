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
	// NSFW reports whether the catalogue marks the submission for adults. The
	// site puts such a submission behind a warning or hides it, which is what
	// its own listings say here; it does not say which parts of it are the
	// reason, so gore and swearing cannot be told from the rest.
	NSFW bool
}

// Category is one node of a kind's category tree: a section such as Skins, or
// a section under it such as Yamato.
type Category struct {
	// ID is the number the listing filter takes.
	ID int
	// Name is what the section is called.
	Name string
	// Items is how many submissions are filed in it, itself included.
	Items int
	// Children is how many sections are under it. Zero means there are none to
	// ask for.
	Children int
}

// Page returns the submission's page on the site, which is where a user goes to
// read about it. It is the address the API gives as _sProfileUrl, built from the
// number for the time before a reply carrying that has arrived.
//
// The three kinds are filed in different sections, and the number alone does
// not say which: mod 42 and sound 42 are two submissions.
func (s Submission) Page() string {
	return "https://gamebanana.com/" + submissionSection(s.Kind) + "/" + strconv.Itoa(s.ID)
}

// submissionSection is the part of the site a kind is filed under, which is
// what its page's address and the update check's reply both name.
func submissionSection(kind Kind) string {
	switch kind {
	case Sound:
		return "sounds"
	case Wip:
		return "wips"
	default:
		return "mods"
	}
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

// Profile is a submission's own page: what its author wrote about it, the
// pictures they posted, and everything that can be downloaded from it.
//
// It is the one request that has all of it. A listing carries a name, a
// thumbnail and a count of likes and nothing else, and the download list is a
// part of this page rather than an endpoint of its own: the two answer with the
// same files, checked against each other for all three kinds.
type Profile struct {
	// Submission is the record of the submission itself: its name, author,
	// where it is filed, and the first picture. The page carries these beside
	// the text and the files.
	Submission Submission
	// Text is what the author wrote, as HTML, which is what a page shows. It is
	// their write-up, or the one line they put beside the name when they wrote
	// no more than that -- a page with nothing on it but a title reads as the
	// app having failed. Empty when they wrote neither.
	Text string
	// Downloads is how many times the submission has been taken. A listing
	// counts views and likes, and not this.
	Downloads int
	// Images are the pictures the author posted, in their own order.
	Images []Image
	// URL is the submission's page on the site, which is where the changelog,
	// the comments and the rest of it are read.
	URL string
	// Files is what can be downloaded, in the order the author's page lists it.
	Files []File
}

// Image is one picture of a submission. Each is published at several sizes, and
// the two kept are the ones the app asks for.
type Image struct {
	// Thumb is the small picture a row of them shows, URL the whole one behind
	// it. Either is empty when the author posted no picture at all.
	Thumb string
	URL   string
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
	// Archived marks a file the author has replaced. The site keeps it out of
	// the download list, but the update check is handed it along with the
	// current ones, so that a file the user did install can still be found.
	Archived bool
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
	PreviewMedia previewMediaJSON `json:"_aPreviewMedia"`
	Likes        int              `json:"_nLikeCount"`
	Views        int              `json:"_nViewCount"`
	Added        int64            `json:"_tsDateAdded"`
	Modified     int64            `json:"_tsDateModified"`
	HasFiles     bool             `json:"_bHasFiles"`
	Visibility   string           `json:"_sInitialVisibility"`
}

// profileJSON is a submission's own page.
type profileJSON struct {
	ID        int    `json:"_idRow"`
	Name      string `json:"_sName"`
	Version   string `json:"_sVersion"`
	Submitter struct {
		Name string `json:"_sName"`
	} `json:"_aSubmitter"`
	SuperCategory struct {
		Name string `json:"_sName"`
	} `json:"_aSuperCategory"`
	Category struct {
		Name string `json:"_sName"`
	} `json:"_aCategory"`
	Likes      int              `json:"_nLikeCount"`
	Visibility string           `json:"_sInitialVisibility"`
	Added      int64            `json:"_tsDateAdded"`
	Modified   int64            `json:"_tsDateModified"`
	Text       string           `json:"_sText"`
	Tagline    string           `json:"_sDescription"`
	Downloads  int              `json:"_nDownloadCount"`
	URL        string           `json:"_sProfileUrl"`
	Preview    previewMediaJSON `json:"_aPreviewMedia"`
	Files      []fileJSON       `json:"_aFiles"`
}

// profile converts a page. The files of a page are the ones the download list
// carries, in the same shape, so they convert the same way.
func (r profileJSON) profile(kind Kind, id int) Profile {
	// What the author wrote is what there is to read, and the one line beside
	// the name is what they wrote instead when they wrote no more than that.
	// The Tauri app reads the two the same way.
	text := r.Text
	if text == "" {
		text = r.Tagline
	}
	files := make([]File, 0, len(r.Files))
	for _, f := range r.Files {
		files = append(files, f.file())
	}

	// The page's category is the one under the root, and its super category is
	// the root; a kind without the second is filed directly under the first.
	category, subcategory := r.SuperCategory.Name, r.Category.Name
	if category == "" {
		category, subcategory = r.Category.Name, ""
	}
	number := r.ID
	if number == 0 {
		number = id
	}
	s := Submission{
		ID:          number,
		Kind:        kind,
		Name:        r.Name,
		Version:     r.Version,
		Author:      r.Submitter.Name,
		Category:    category,
		Subcategory: subcategory,
		Likes:       r.Likes,
		Added:       unix(r.Added),
		Updated:     unix(r.Modified),
		NSFW:        r.Visibility == "warn" || r.Visibility == "hide",
	}
	if images := r.Preview.images(); len(images) > 0 {
		s.Thumb, s.Image = images[0].Thumb, images[0].URL
	}
	return Profile{
		Submission: s,
		Text:       text,
		Downloads:  r.Downloads,
		Images:     r.Preview.images(),
		URL:        r.URL,
		Files:      files,
	}
}

// previewMediaJSON is the pictures of a submission, which a listing and a page
// both carry in the same shape.
type previewMediaJSON struct {
	Images []struct {
		Base  string `json:"_sBaseUrl"`
		File  string `json:"_sFile"`
		Small string `json:"_sFile220"`
	} `json:"_aImages"`
}

// images converts them. Each is published at several sizes, and the two kept
// are the small one a row of pictures shows and the whole one behind it.
func (m previewMediaJSON) images() []Image {
	images := make([]Image, 0, len(m.Images))
	for _, i := range m.Images {
		image := Image{Thumb: imageURL(i.Base, i.Small), URL: imageURL(i.Base, i.File)}
		// A picture published at one size only is its own small one.
		if image.Thumb == "" {
			image.Thumb = image.URL
		}
		images = append(images, image)
	}
	return images
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
		// The site says "warn" for what it shows behind a warning and "hide"
		// for what it keeps off its listings. Both are what an adult filter is
		// meant to take out.
		NSFW: r.Visibility == "warn" || r.Visibility == "hide",
	}
	// The first picture stands for the submission, which is how the site's own
	// listings do it. Each is published at several sizes, and the list wants
	// the small one where a page wants the large.
	if images := r.PreviewMedia.images(); len(images) > 0 {
		s.Thumb, s.Image = images[0].Thumb, images[0].URL
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
	Archived    bool   `json:"_bIsArchived"`
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
		Archived:    f.Archived,
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
