package gb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

const (
	// perPage is how many submissions one listing request asks for. The API
	// sends up to fifty and no more, whatever it is asked for.
	perPage = 50
	// searchPerPage is what the search endpoint sends. It ignores what it is
	// asked for and always sends fifteen, so paging by anything else would
	// skip results.
	searchPerPage = 15
)

// Order is how a listing is sorted. The API accepts any string here and falls
// back to its own order for one it does not know, so a wrong name is not
// refused, it is quietly ignored.
type Order string

const (
	// Updated lists the most recently changed first, which is what a mod store
	// opens on: a mod's last update is what the people using it are waiting
	// for.
	Updated Order = "Generic_LatestModified"
	// Liked lists the most liked first.
	Liked Order = "Generic_MostLiked"
	// Downloaded lists the most downloaded first.
	Downloaded Order = "Generic_MostDownloaded"
	// Published lists the oldest first. It is the one order the API keeps
	// stable from page to page, which is what reading the whole catalogue
	// through needs.
	Published Order = "Generic_Oldest"
)

// Page is one page of a listing.
type Page struct {
	// Items are the submissions, in the order the listing gave them.
	Items []Submission
	// Total is how many the listing says it holds. It counts what the filters
	// matched rather than what the API will hand over, so it is a number to
	// show rather than one to page by.
	Total int
	// PerPage is how many this page asked for.
	PerPage int
	// More reports whether asking for the next page is worth doing. A page that
	// came back full may have one after it; a page that came back short is the
	// end, whatever Total says.
	More bool
}

// Browse returns one page of one kind of submission, sorted by order. Pages are
// numbered from 1.
func (c *Client) Browse(ctx context.Context, kind Kind, page int, order Order) (Page, error) {
	q := url.Values{
		// The game is named through a filter rather than through a field of
		// its own. The obvious _idGameRow is accepted here and then ignored,
		// which answers with other games' mods and looks like it worked.
		"_aFilters[Generic_Game]": {strconv.Itoa(Game)},
		"_nPerpage":               {strconv.Itoa(perPage)},
		"_nPage":                  {strconv.Itoa(max(page, 1))},
		"_sSort":                  {string(order)},
	}
	return c.listing(ctx, string(kind)+"/Index?"+q.Encode())
}

// Search returns one page of the submissions of one kind matching query, best
// match first. Pages are numbered from 1.
//
// This is the API's own search rather than a filter over a listing: it ranks by
// how well a submission matches, which the listings cannot do, and it is the
// only way to find one by name among five thousand. It is also the one endpoint
// that takes the game as _idGameRow and honours it.
func (c *Client) Search(ctx context.Context, kind Kind, query string, page int) (Page, error) {
	q := url.Values{
		"_sModelName":    {string(kind)},
		"_sSearchString": {query},
		// The search is ranked. It accepts any other order and ignores it.
		"_sOrder":    {"best_match"},
		"_idGameRow": {strconv.Itoa(Game)},
		"_nPage":     {strconv.Itoa(max(page, 1))},
	}
	return c.listing(ctx, "Util/Search/Results?"+q.Encode())
}

// listing reads one page of a listing endpoint and converts it.
func (c *Client) listing(ctx context.Context, path string) (Page, error) {
	var reply struct {
		Metadata struct {
			Total   int `json:"_nRecordCount"`
			PerPage int `json:"_nPerpage"`
		} `json:"_aMetadata"`
		Records []submissionJSON `json:"_aRecords"`
	}
	if err := c.get(ctx, path, &reply); err != nil {
		return Page{}, err
	}

	p := Page{Total: reply.Metadata.Total, PerPage: reply.Metadata.PerPage}
	p.Items = make([]Submission, 0, len(reply.Records))
	for _, r := range reply.Records {
		p.Items = append(p.Items, r.submission())
	}
	p.More = len(p.Items) > 0 && len(p.Items) == p.PerPage
	return p, nil
}

// Profile returns a submission's own page: what its author wrote about it, the
// pictures they posted, and what can be downloaded from it.
//
// This is the request a listing does not have. It is also the only one needed
// to open a submission: the page carries the download list as well, and the two
// endpoints answer with the same files -- checked against each other for all
// three kinds -- so asking for both would fetch the same list twice.
//
// Files the author has replaced are left out of that list by the API itself,
// which is what keeps an old version from being offered beside the one that
// replaced it.
func (c *Client) Profile(ctx context.Context, kind Kind, id int) (Profile, error) {
	var reply profileJSON
	path := fmt.Sprintf("%s/%d/ProfilePage", kind, id)
	if err := c.get(ctx, path, &reply); err != nil {
		return Profile{}, err
	}
	return reply.profile(), nil
}
