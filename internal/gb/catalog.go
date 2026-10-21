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

// Filter narrows a listing. It is only the listings that take one: the search
// endpoint accepts a filter and quietly ignores it, so a search is narrowed
// after the reply instead.
type Filter struct {
	// Category is a root category's id, or one of its subcategories'. Zero
	// takes the whole listing.
	//
	// The API accepts either: a root such as Skins, or a section under it such
	// as Yamato. Asking for a section is how a hero's submissions are listed,
	// without the client having to know which of them are heroes.
	Category int
}

// Browse returns one page of one kind of submission, sorted by order. Pages are
// numbered from 1.
func (c *Client) Browse(ctx context.Context, kind Kind, page int, order Order) (Page, error) {
	return c.BrowseFiltered(ctx, kind, page, order, Filter{})
}

// BrowseFiltered returns one page of a listing narrowed by filter, sorted by
// order. Pages are numbered from 1.
func (c *Client) BrowseFiltered(ctx context.Context, kind Kind, page int, order Order, filter Filter) (Page, error) {
	q := url.Values{
		// The game is named through a filter rather than through a field of
		// its own. The obvious _idGameRow is accepted here and then ignored,
		// which answers with other games' mods and looks like it worked.
		"_aFilters[Generic_Game]": {strconv.Itoa(Game)},
		"_nPerpage":               {strconv.Itoa(perPage)},
		"_nPage":                  {strconv.Itoa(max(page, 1))},
		"_sSort":                  {string(order)},
	}
	if filter.Category > 0 {
		q.Set("_aFilters[Generic_Category]", strconv.Itoa(filter.Category))
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

// Categories returns one level of one kind's category tree: the root sections
// when parent is zero, and the sections under parent otherwise.
//
// The tree is the live one, the same the site browses by, and it is asked for
// rather than kept in the client: sections are added and renamed -- Deadlock's
// roster alone has grown by a dozen -- and a stale list would filter by a name
// that no longer means what it did.
func (c *Client) Categories(ctx context.Context, kind Kind, parent int) ([]Category, error) {
	q := url.Values{"_sSort": {"a_to_z"}}
	if parent > 0 {
		q.Set("_idCategoryRow", strconv.Itoa(parent))
	} else {
		q.Set("_idGameRow", strconv.Itoa(Game))
	}

	var reply []categoryJSON
	path := string(kind) + "/Categories?" + q.Encode()
	if err := c.get(ctx, path, &reply); err != nil {
		return nil, err
	}

	categories := make([]Category, 0, len(reply))
	for _, r := range reply {
		categories = append(categories, Category{
			ID:       r.ID,
			Name:     r.Name,
			Items:    r.Items,
			Children: r.Children,
		})
	}
	return categories, nil
}

// categoryJSON is one section of a category tree.
type categoryJSON struct {
	ID       int    `json:"_idRow"`
	Name     string `json:"_sName"`
	Items    int    `json:"_nItemCount"`
	Children int    `json:"_nCategoryCount"`
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
