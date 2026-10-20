package gb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// server is a stand-in for the API. The handler answers each request in turn,
// and records what it was asked for.
type server struct {
	*httptest.Server
	// requests are the query strings the handler was given, in order.
	requests []url.Values
	// paths are the paths, without the API root.
	paths []string
}

// newServer starts a server whose replies come from reply, which is given the
// request number counting from 1 and returns the status, the body and any
// extra headers.
func newServer(t *testing.T, reply func(n int, w http.ResponseWriter) (int, string)) (*Client, *server) {
	t.Helper()
	s := &server{}
	n := 0
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		s.requests = append(s.requests, r.URL.Query())
		s.paths = append(s.paths, r.URL.Path)
		status, body := reply(n, w)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)

	c := New()
	c.Base = s.URL + "/"
	// The waits between retries are the API's, not the test's: they are skipped
	// so that a test of the retrying does not take as long as the retrying.
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, s
}

// recordJSON is one submission as the API sends it, with the name and the id
// left for the caller to fill in.
const recordJSON = `{
	"_idRow": %d,
	"_sModelName": "Mod",
	"_sName": %q,
	"_sVersion": "1.3",
	"_aSubmitter": {"_sName": "Kirill Senzu"},
	"_aRootCategory": {"_sName": "Skins"},
	"_aSubCategory": {"_sName": "Yamato"},
	"_aPreviewMedia": {"_aImages": [{
		"_sType": "screenshot",
		"_sBaseUrl": "https://images.gamebanana.com/img/ss/mods",
		"_sFile": "6988989395e38.jpg",
		"_sFile220": "220-90_6988989395e38.jpg"
	}]},
	"_nLikeCount": 569,
	"_nViewCount": 210597,
	"_tsDateAdded": 1749645061,
	"_tsDateModified": 1792372517,
	"_bHasFiles": true
}`

// listingReply is a catalogue page holding the named submissions, as the API
// sends it: the per-page count is the number of records, which is what says
// whether there is a page after this one.
func listingReply(names ...string) string {
	records := make([]string, 0, len(names))
	for i, name := range names {
		records = append(records, fmt.Sprintf(recordJSON, 599927+i, name))
	}
	return fmt.Sprintf(
		`{"_aMetadata": {"_nRecordCount": 4953, "_bIsComplete": false, "_nPerpage": %d}, "_aRecords": [%s]}`,
		len(names), strings.Join(records, ","))
}

func TestBrowseReadsAPage(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, listingReply("Yamato redesign")
	})

	page, err := c.Browse(t.Context(), Mod, 1, Updated)
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Items) != 1 {
		t.Fatalf("the page holds %d submissions, want 1", len(page.Items))
	}
	got := page.Items[0]
	if got.ID != 599927 {
		t.Errorf("ID = %d, want 599927", got.ID)
	}
	if got.Name != "Yamato redesign" || got.Kind != Mod || got.Author != "Kirill Senzu" {
		t.Errorf("the submission reads as %+v", got)
	}
	if got.Category != "Skins" || got.Subcategory != "Yamato" {
		t.Errorf("the submission is filed under %q / %q", got.Category, got.Subcategory)
	}
	if got.Likes != 569 || got.Views != 210597 {
		t.Errorf("the submission has %d likes and %d views", got.Likes, got.Views)
	}
	if want := time.Unix(1749645061, 0); !got.Added.Equal(want) {
		t.Errorf("Added = %v, want %v", got.Added, want)
	}
	// The list wants the small picture and a page the large one, and the API
	// splits each address in two.
	if want := "https://images.gamebanana.com/img/ss/mods/220-90_6988989395e38.jpg"; got.Thumb != want {
		t.Errorf("Thumb = %q, want %q", got.Thumb, want)
	}
	if want := "https://images.gamebanana.com/img/ss/mods/6988989395e38.jpg"; got.Image != want {
		t.Errorf("Image = %q, want %q", got.Image, want)
	}
	if page.Total != 4953 || page.PerPage != 1 {
		t.Errorf("the page reads as %+v", page)
	}
	if !page.More {
		t.Error("a full page is reported as the last one")
	}
	if len(s.paths) != 1 || s.paths[0] != "/Mod/Index" {
		t.Errorf("the client asked for %v", s.paths)
	}
}

// The game is named through a filter, not through the _idGameRow the API also
// accepts: that one is ignored here and answers with other games' mods.
func TestBrowseNamesTheGameAsAFilter(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, listingReply("x")
	})

	if _, err := c.Browse(t.Context(), Sound, 3, Liked); err != nil {
		t.Fatal(err)
	}

	q := s.requests[0]
	if got := q.Get("_aFilters[Generic_Game]"); got != "20948" {
		t.Errorf("_aFilters[Generic_Game] = %q, want 20948", got)
	}
	if q.Has("_idGameRow") {
		t.Error("the request names the game through _idGameRow, which this endpoint ignores")
	}
	if got := q.Get("_nPage"); got != "3" {
		t.Errorf("_nPage = %q, want 3", got)
	}
	if got := q.Get("_sSort"); got != string(Liked) {
		t.Errorf("_sSort = %q, want %q", got, Liked)
	}
	if len(s.paths) != 1 || s.paths[0] != "/Sound/Index" {
		t.Errorf("the client asked for %v, want the Sound listing", s.paths)
	}
}

// A page that came back short is the end of the listing, whatever the API's own
// count says: it counts what the filters matched, not what it will hand over.
func TestAListingEndsOnAShortPage(t *testing.T) {
	short := `{"_aMetadata": {"_nRecordCount": 4953, "_nPerpage": 50}, "_aRecords": [
		` + fmt.Sprintf(recordJSON, 1, "a") + `,
		` + fmt.Sprintf(recordJSON, 2, "b") + `]}`
	for _, tt := range []struct {
		name  string
		reply string
		want  bool
	}{
		{"empty", `{"_aMetadata": {"_nRecordCount": 4953, "_nPerpage": 50}, "_aRecords": []}`, false},
		{"short", short, false},
		{"full", listingReply(make([]string, 50)...), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newServer(t, func(int, http.ResponseWriter) (int, string) {
				return http.StatusOK, tt.reply
			})

			page, err := c.Browse(t.Context(), Mod, 99, Updated)
			if err != nil {
				t.Fatal(err)
			}
			if page.More != tt.want {
				t.Errorf("More = %v for a %s page, want %v", page.More, tt.name, tt.want)
			}
		})
	}
}

// The search takes the game as _idGameRow and honours it, which is the one
// place that field works.
func TestSearchNamesTheGameAsARow(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, listingReply("Yamato redesign")
	})

	if _, err := c.Search(t.Context(), Mod, "yamato", 2); err != nil {
		t.Fatal(err)
	}

	q := s.requests[0]
	if got := q.Get("_idGameRow"); got != "20948" {
		t.Errorf("_idGameRow = %q, want 20948", got)
	}
	if got := q.Get("_sSearchString"); got != "yamato" {
		t.Errorf("_sSearchString = %q, want yamato", got)
	}
	if got := q.Get("_sOrder"); got != "best_match" {
		t.Errorf("_sOrder = %q, want best_match", got)
	}
	if got := q.Get("_nPage"); got != "2" {
		t.Errorf("_nPage = %q, want 2", got)
	}
	if len(s.paths) != 1 || s.paths[0] != "/Util/Search/Results" {
		t.Errorf("the client asked for %v", s.paths)
	}
}

// The API sometimes writes a PHP warning before the body, and no JSON decoder
// will skip it.
func TestAPageSurvivesAPHPWarning(t *testing.T) {
	c, _ := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, "\nWarning: Undefined array key \"x\" in /srv/api.php on line 12\n" + listingReply("Yamato redesign")
	})

	page, err := c.Browse(t.Context(), Mod, 1, Updated)
	if err != nil {
		t.Fatalf("a reply with a warning before it was not read: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "Yamato redesign" {
		t.Errorf("the page reads as %+v", page)
	}
}

func TestJSONBody(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"leading space", "  \n\t{\"a\":1}", `{"a":1}`},
		{"after a warning", "\nWarning: something\n{\"a\":1}", `{"a":1}`},
		{"after a warning without a newline first", "Warning: something\n{\"a\":1}", `{"a":1}`},
		{"an array", "\nNotice: x\n[1,2]", `[1,2]`},
		{"nothing to find", "Warning: something", "Warning: something"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(jsonBody([]byte(tt.body))); got != tt.want {
				t.Errorf("jsonBody(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// A 429 is the API asking for room, not an answer: the request is made again
// once the pause it asked for has passed.
func TestARateLimitIsWaitedOut(t *testing.T) {
	c, s := newServer(t, func(n int, w http.ResponseWriter) (int, string) {
		if n == 1 {
			w.Header().Set("Retry-After", "2")
			return http.StatusTooManyRequests, `{"_sErrorMessage": "Slow down"}`
		}
		return http.StatusOK, listingReply("Yamato redesign")
	})
	var waited []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	}

	page, err := c.Browse(t.Context(), Mod, 1, Updated)
	if err != nil {
		t.Fatalf("a rate limited request was not tried again: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("the page reads as %+v", page)
	}
	if len(s.requests) != 2 {
		t.Fatalf("the client made %d requests, want 2", len(s.requests))
	}
	// What the API asked for is what is waited, rather than the growing pause
	// used when it did not say.
	if len(waited) != 1 || waited[0] != 2*time.Second {
		t.Errorf("the client waited %v, want 2s once", waited)
	}
}

// A failure the API could not recover from is reported rather than tried again.
func TestABadRequestIsNotTriedAgain(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusBadRequest, `{"_sErrorCode": "BAD_REQUEST", "_sErrorMessage": "Bad request"}`
	})

	_, err := c.Browse(t.Context(), Mod, 1, Updated)
	if err == nil {
		t.Fatal("a refused request reported no error")
	}
	if len(s.requests) != 1 {
		t.Errorf("the client made %d requests, want 1", len(s.requests))
	}
	if !strings.Contains(err.Error(), "Bad request") {
		t.Errorf("the error does not carry what the API said: %v", err)
	}
}

// A submission that is gone is a thing to say plainly: the user has a link to
// something that is no longer there.
func TestAMissingSubmissionReadsAsNotFound(t *testing.T) {
	c, _ := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusNotFound, `{"_sErrorCode": "NO_SUCH_RECORD", "_sErrorMessage": "This Mod doesn't exist"}`
	})

	_, err := c.Files(t.Context(), Mod, 999999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "This Mod doesn't exist") {
		t.Errorf("the error does not carry what the API said: %v", err)
	}
}

// A 5xx is the service's own trouble, and is tried again before being reported.
func TestAServerErrorIsTriedAgainAndThenReported(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusInternalServerError, `{"_sErrorMessage": "Something went wrong"}`
	})

	if _, err := c.Browse(t.Context(), Mod, 1, Updated); err == nil {
		t.Fatal("a server error reported no error")
	}
	if want := retries + 1; len(s.requests) != want {
		t.Errorf("the client made %d requests, want %d", len(s.requests), want)
	}
}

// A refusal is the API not reading the request at all, which is what a block
// looks like, and is worth telling apart from a submission that is gone.
func TestARefusalReadsAsRefused(t *testing.T) {
	c, _ := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusForbidden, ""
	})

	_, err := c.Browse(t.Context(), Mod, 1, Updated)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("a refusal reads as a missing submission")
	}
}

// The caller giving up is the caller's answer, not the API's.
func TestACanceledRequestStops(t *testing.T) {
	c, _ := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusInternalServerError, ""
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := c.Browse(ctx, Mod, 1, Updated); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFilesReadsADownloadPage(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, `{
			"_aFiles": [
				{
					"_idRow": 1854979,
					"_sFile": "yamato_red_d0d08.rar",
					"_sDescription": "Original mod colors",
					"_nFilesize": 24525612,
					"_sMd5Checksum": "EE2901F8C68816AA0DA5EB3DC306CB1E",
					"_sDownloadUrl": "https://gamebanana.com/dl/1854979",
					"_nDownloadCount": 3491,
					"_tsDateAdded": 1792372426,
					"_sAvResult": "clean"
				},
				{
					"_idRow": 1854980,
					"_sFile": "yamato_blue_c1f72.rar",
					"_nFilesize": 31777079,
					"_sMd5Checksum": "",
					"_sDownloadUrl": "https://gamebanana.com/dl/1854980"
				}
			]
		}`
	})

	files, err := c.Files(t.Context(), Mod, 599927)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("the page holds %d files, want 2", len(files))
	}

	first := files[0]
	if first.ID != 1854979 || first.Name != "yamato_red_d0d08.rar" {
		t.Errorf("the file reads as %+v", first)
	}
	if first.Size != 24525612 {
		t.Errorf("Size = %d, want 24525612", first.Size)
	}
	// The checksum is compared against one computed here, and the API sends it
	// in either case.
	if first.MD5 != "ee2901f8c68816aa0da5eb3dc306cb1e" {
		t.Errorf("MD5 = %q, want it in lower case", first.MD5)
	}
	if first.URL != "https://gamebanana.com/dl/1854979" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Scan != "clean" {
		t.Errorf("Scan = %q, want clean", first.Scan)
	}
	if len(s.paths) != 1 || s.paths[0] != "/Mod/599927/DownloadPage" {
		t.Errorf("the client asked for %v", s.paths)
	}
}

// A page numbered below one is the first page: the API answers 0 and below with
// its own first page anyway, and asking for one makes what is shown match what
// was asked for.
func TestPagesAreNumberedFromOne(t *testing.T) {
	c, s := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, listingReply("x")
	})

	if _, err := c.Browse(t.Context(), Mod, 0, Updated); err != nil {
		t.Fatal(err)
	}
	if got := s.requests[0].Get("_nPage"); got != "1" {
		t.Errorf("_nPage = %q, want 1", got)
	}
}

func TestBackoffGrowsAndIsBounded(t *testing.T) {
	for attempt, want := range map[int]time.Duration{1: minBackoff, 2: 2 * minBackoff, 3: 4 * minBackoff} {
		got := backoff(attempt)
		// Half the delay is fixed and half drawn at random, so the wait falls
		// in the upper half of the doubling.
		if got < want/2 || got > want {
			t.Errorf("backoff(%d) = %v, want between %v and %v", attempt, got, want/2, want)
		}
	}
	// A large attempt is held at the ceiling rather than overflowing it.
	for _, attempt := range []int{20, 60, 1000} {
		if got := backoff(attempt); got < maxBackoff/2 || got > maxBackoff {
			t.Errorf("backoff(%d) = %v, want between %v and %v", attempt, got, maxBackoff/2, maxBackoff)
		}
	}
}

// The live catalogue is the only thing that says whether the endpoints and the
// field names are still right. It is skipped in short mode, which is how the
// suite runs without a network.
func TestTheLiveCatalogue(t *testing.T) {
	if testing.Short() {
		t.Skip("the live catalogue needs a network")
	}
	ctx := t.Context()
	c := New()

	page, err := c.Browse(ctx, Mod, 1, Updated)
	if err != nil {
		t.Fatalf("browsing the live catalogue: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("the live catalogue came back empty")
	}
	if page.Items[0].Name == "" {
		t.Errorf("the first submission has no name: %+v", page.Items[0])
	}

	// The filter is what keeps the listing to Deadlock, and an unfiltered one
	// answers with other games' mods. Every kind is asked for because the three
	// listings are numbered separately.
	for _, kind := range Kinds {
		p, err := c.Browse(ctx, kind, 1, Liked)
		if err != nil {
			t.Errorf("browsing the live %s listing: %v", kind, err)
			continue
		}
		if len(p.Items) == 0 {
			t.Errorf("the live %s listing came back empty", kind)
		}
	}

	// A submission's files are what the install is made of, so one is opened to
	// see that the download addresses and the checksums are still there.
	var withFiles Submission
	for _, s := range page.Items {
		if s.HasFiles {
			withFiles = s
			break
		}
	}
	if withFiles.ID == 0 {
		t.Skip("no submission on the first page has files")
	}
	files, err := c.Files(ctx, withFiles.Kind, withFiles.ID)
	if err != nil {
		t.Fatalf("reading the files of %d: %v", withFiles.ID, err)
	}
	if len(files) == 0 {
		t.Fatalf("submission %d has files but its page lists none", withFiles.ID)
	}
	if files[0].URL == "" || files[0].Size == 0 {
		t.Errorf("the first file reads as %+v", files[0])
	}
}
