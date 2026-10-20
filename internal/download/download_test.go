package download

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// file is what the servers below serve, and the request that matches it.
var content = bytes.Repeat([]byte("a mod's archive, over and over. "), 400)

func checksum(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// newClient points a client at a test server. The address it serves on is not
// one a mod may be fetched from, so the server's own host stands in for
// GameBanana's: what is being tested is that the check is made at all.
func newClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	transport, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the test server's transport is a %T", srv.Client().Transport)
	}
	return &Client{
		http: &http.Client{
			Transport: transport.Clone(),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		domains: []string{"127.0.0.1"},
	}
}

// serveFile answers with content, honouring a range request the way a file
// server does.
func serveFile(w http.ResponseWriter, r *http.Request) {
	from := int64(0)
	if value := r.Header.Get("Range"); value != "" {
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(value, "bytes="), "-"), 10, 64)
		if err != nil || n < 0 || n > int64(len(content)) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		from = n
	}

	if from > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(content)-1, len(content)))
		w.Header().Set("Content-Length", strconv.Itoa(len(content)-int(from)))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	}
	w.Write(content[from:])
}

// request is a request for content, written where the test says. The address is
// the server's own, because a test server does not serve on gamebanana.com.
func request(srv *httptest.Server, dest string) Request {
	return Request{
		URL:  srv.URL + "/dl/1",
		Dest: dest,
		Size: int64(len(content)),
		MD5:  checksum(content),
	}
}

// redirectTo sends the client on to another address, which is what GameBanana
// does to reach a file server.
func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}
}

func TestGetFetchesTheArchive(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(serveFile))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "mod.zip")

	var last Progress
	if err := newClient(t, srv).Get(t.Context(), request(srv, dest), func(p Progress) { last = p }); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("the file is %d bytes, want %d of the same bytes", len(got), len(content))
	}
	if last.Done != int64(len(content)) || last.Total != int64(len(content)) {
		t.Errorf("the last progress was %+v, want %d of %d", last, len(content), len(content))
	}
}

// The archive is written where it is going only once it has arrived whole, so
// that a file at the destination is a file to install.
func TestGetLeavesNothingBehindWhileItRuns(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Write(content[:100])
		// The handler returning cuts the reply short, which is what a dropped
		// connection looks like from here.
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "mod.zip")

	err := newClient(t, srv).Get(t.Context(), request(srv, dest), nil)
	if err == nil {
		t.Fatal("a reply that stopped partway reported no error")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("the destination holds a file that never arrived whole")
	}
	// What did arrive is kept, so that asking again picks up from there.
	if sizeOf(dest+partialExt) == 0 {
		t.Error("nothing was kept to resume from")
	}
}

// A download that was interrupted is picked up where it stopped rather than
// started again, which is the whole point of keeping the partial.
func TestGetResumesFromWhatIsAlreadyThere(t *testing.T) {
	var ranges []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		serveFile(w, r)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "mod.zip")
	// Half of the archive is already there, as a download that stopped would
	// have left it.
	half := len(content) / 2
	if err := os.WriteFile(dest+partialExt, content[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	var first Progress
	if err := newClient(t, srv).Get(t.Context(), request(srv, dest), func(p Progress) {
		if first.Done == 0 {
			first = p
		}
	}); err != nil {
		t.Fatal(err)
	}

	if len(ranges) != 1 || ranges[0] != fmt.Sprintf("bytes=%d-", half) {
		t.Errorf("the server was asked for %q, want a range from %d", ranges, half)
	}
	if first.Done != int64(half) {
		t.Errorf("the download started at %d, want %d", first.Done, half)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Error("the resumed download is not the archive")
	}
}

// A server that does not do ranges answers a range request with the whole file,
// and what was already there is started over rather than appended to.
func TestGetStartsOverWhenTheServerIgnoresRanges(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "mod.zip")
	if err := os.WriteFile(dest+partialExt, content[:100], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := newClient(t, srv).Get(t.Context(), request(srv, dest), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("the file is %d bytes, want %d", len(got), len(content))
	}
}

// The redirect to a file server is what a download really is, so it is
// followed.
func TestGetFollowsTheRedirectToTheFileServer(t *testing.T) {
	file := httptest.NewTLSServer(http.HandlerFunc(serveFile))
	defer file.Close()
	api := httptest.NewTLSServer(redirectTo(file.URL))
	defer api.Close()

	dest := filepath.Join(t.TempDir(), "mod.zip")

	if err := newClient(t, api).Get(t.Context(), request(api, dest), nil); err != nil {
		t.Fatal(err)
	}
	if got := sizeOf(dest); got != int64(len(content)) {
		t.Errorf("the file is %d bytes, want %d", got, len(content))
	}
}

// A redirect that leaves the file servers is not followed: the app fetches
// mods, and nothing else. Both servers here are on the address the test client
// allows, so what is refused is the address the second hop names rather than
// the hop itself.
func TestGetRefusesARedirectOffTheFileServers(t *testing.T) {
	api := httptest.NewTLSServer(redirectTo("https://example.com/mod.zip"))
	defer api.Close()

	dest := filepath.Join(t.TempDir(), "mod.zip")
	err := newClient(t, api).Get(t.Context(), request(api, dest), nil)
	if err == nil {
		t.Fatal("a redirect off the file servers was followed")
	}
	if !strings.Contains(err.Error(), "not a GameBanana address") {
		t.Errorf("the error does not say the address was refused: %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("the destination holds a file from somewhere else")
	}
}

func TestGetRefusesAnAddressItShouldNotFetch(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "mod.zip")
	c := &Client{domains: []string{"gamebanana.com", "deadlockmods.app"}}

	for _, address := range []string{
		"https://example.com/mod.zip",
		"https://gamebanana.com.evil.example/mod.zip",
		"http://gamebanana.com/mod.zip",
		"file:///etc/passwd",
		"https://127.0.0.1/mod.zip",
		"not an address at all",
	} {
		req := Request{URL: address, Dest: dest}
		if err := c.Get(t.Context(), req, nil); err == nil {
			t.Errorf("Get(%q) fetched from an address it should not have", address)
		}
	}

	for _, address := range []string{
		"https://gamebanana.com/dl/1",
		"https://files.gamebanana.com/mod.zip",
		"https://filecache37.gamebanana.com/mod.zip",
		"https://deadlockmods.app/mod.zip",
		"https://cdn.deadlockmods.app/mod.zip",
	} {
		if err := c.allowed(address); err != nil {
			t.Errorf("allowed(%q) = %v, want it taken", address, err)
		}
	}
}

// An archive that is not what the API said it was is not handed on as one, and
// what arrived is thrown away: resuming could never mend it.
func TestGetRefusesAnArchiveThatIsNotWhatItShouldBe(t *testing.T) {
	for _, tt := range []struct {
		name string
		// wrong is what the API claims about the archive, which is what the
		// file that arrives is checked against.
		wrong func(r Request) Request
	}{
		{"the wrong size", func(r Request) Request {
			r.Size = int64(len(content)) + 1
			return r
		}},
		{"the wrong checksum", func(r Request) Request {
			r.MD5 = checksum([]byte("something else"))
			return r
		}},
		{"no checksum to match", func(r Request) Request {
			r.MD5 = checksum(content[:100])
			return r
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(serveFile))
			defer srv.Close()

			dest := filepath.Join(t.TempDir(), "mod.zip")
			if err := newClient(t, srv).Get(t.Context(), tt.wrong(request(srv, dest)), nil); err == nil {
				t.Fatal("an archive that is not what it should be was accepted")
			}
			if _, err := os.Stat(dest); err == nil {
				t.Error("the destination holds an archive that is not what it should be")
			}
			if _, err := os.Stat(dest + partialExt); err == nil {
				t.Error("what arrived was kept to resume from, which could never mend it")
			}
		})
	}
}

// A size or a checksum the API did not give is not checked: a mod whose author
// never uploaded one is still a mod to install.
func TestGetAcceptsAnArchiveWithNothingToCheckItAgainst(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(serveFile))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "mod.zip")

	req := Request{URL: srv.URL + "/dl/1", Dest: dest}
	if err := newClient(t, srv).Get(t.Context(), req, nil); err != nil {
		t.Fatal(err)
	}
	if got := sizeOf(dest); got != int64(len(content)) {
		t.Errorf("the file is %d bytes, want %d", got, len(content))
	}
}

// A connection that stopped is picked up again on its own, which is what the
// reconnects are for.
func TestGetPicksUpAConnectionThatStopped(t *testing.T) {
	var attempts int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			// The first reply is cut short partway through.
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			w.Write(content[:len(content)/3])
			return
		}
		serveFile(w, r)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "mod.zip")

	if err := newClient(t, srv).Get(t.Context(), request(srv, dest), nil); err != nil {
		t.Fatalf("a download that stopped was not picked up again: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("the file is %d bytes, want %d", len(got), len(content))
	}
	if attempts < 2 {
		t.Errorf("the server was asked %d times, want the download tried again", attempts)
	}
}

// The caller giving up is the caller's answer, not something to try again.
func TestGetStopsWhenTheCallerGivesUp(t *testing.T) {
	var attempts int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		serveFile(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dest := filepath.Join(t.TempDir(), "mod.zip")

	if err := newClient(t, srv).Get(ctx, request(srv, dest), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if attempts != 0 {
		t.Errorf("the server was asked %d times after the caller gave up", attempts)
	}
}

// A file server that refuses is reported rather than retried into a longer
// wait, because asking again will not change its mind.
func TestGetReportsAServerThatRefuses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "mod.zip")

	err := newClient(t, srv).Get(t.Context(), request(srv, dest), nil)
	if err == nil {
		t.Fatal("a refusal reported no error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error does not say what the server answered: %v", err)
	}
}

// What is held being longer than the file is the one thing resuming cannot get
// past, so it is thrown away and the whole file asked for again.
func TestGetStartsOverWhenWhatIsHeldIsTooLong(t *testing.T) {
	var ranges []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		serveFile(w, r)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "mod.zip")
	if err := os.WriteFile(dest+partialExt, append(content, []byte("and then some")...), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := newClient(t, srv).Get(t.Context(), request(srv, dest), nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("the file is %d bytes, want %d", len(got), len(content))
	}
	// The first attempt asks for the rest of a file that is already too long,
	// is refused, and the second asks for the whole of it.
	if len(ranges) < 2 || ranges[len(ranges)-1] != "" {
		t.Errorf("the server was asked for %q, want the whole file in the end", ranges)
	}
}
