// Package download fetches a mod's archive from GameBanana.
//
// It is written around what a large download over a home connection actually
// does, which is stop partway: the connection is dropped, the machine sleeps,
// the file server has a bad minute. A download that stopped is picked up where
// it left off rather than started again, and the archive is checked against the
// size and checksum the API gave for it before it is handed on as whole.
package download

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BabyLy233/mylock/internal/about"
)

const (
	// maxSize is the largest archive that will be fetched. The biggest mod is
	// a few hundred megabytes; this is well past that, and is here so that a
	// server which never stops sending cannot fill the disk.
	maxSize = 2 << 30

	// stallTimeout is how long a connection may send nothing before it is
	// treated as broken. A slow download is fine; one that has stopped without
	// saying so is what this catches, and a read that blocks forever would
	// leave the app with a progress bar that never moves.
	stallTimeout = 20 * time.Second

	// reconnects is how many times a broken connection is picked up again.
	reconnects = 3

	// maxRedirects is how many hops are followed to reach a file server. The
	// real chain is two: GameBanana's own address, then a file server.
	maxRedirects = 5

	// partialExt is appended to the destination while a download is running,
	// so that a file at the destination is a file that arrived whole.
	partialExt = ".partial"

	// chunk is how much is read at a time: big enough that the progress
	// callback and the watchdog are not run per kilobyte.
	chunk = 64 << 10

	// probeTimeout bounds how long a file server is given to answer a probe.
	// A server that takes longer is as good as down for choosing between them.
	probeTimeout = 5 * time.Second
)

// errRangeMismatch is a file server refusing the range asked for because what
// is already held is as long as the file, or longer.
var errRangeMismatch = errors.New("what has been downloaded does not fit the file")

// Request is an archive to fetch and what it has to turn out to be.
type Request struct {
	// URL is the file's address on GameBanana, which redirects to a file
	// server.
	URL string
	// Dest is where the archive is written. The file appears there only once
	// it has arrived whole.
	Dest string
	// Size and MD5 are what the API says the archive is. Either being zero or
	// empty means the API did not say, and that one is not checked.
	Size int64
	MD5  string
}

// Progress is how far a download has got.
type Progress struct {
	// Done is how many bytes have been written, counting what an earlier
	// attempt wrote.
	Done int64
	// Total is how many there are, and is zero while the server has not said.
	Total int64
}

// Client fetches archives. Make one with New; it is safe to use from several
// goroutines.
type Client struct {
	http *http.Client
	// domains are the only addresses a mod may be fetched from. A redirect
	// that leaves them is not followed, so a wrong or tampered-with address
	// cannot point the app at the rest of the network.
	domains []string
}

// New returns a client that fetches from GameBanana and its file servers.
func New() *Client {
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				// Nothing is timed as a whole: an archive takes as long as it
				// takes, and the watchdog in Get is what notices one that has
				// stopped.
				ResponseHeaderTimeout: 30 * time.Second,
			},
			// The hops are followed by hand, so that where each one leads can
			// be checked before it is taken.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		domains: []string{"gamebanana.com", "deadlockmods.app"},
	}
}

// Probe asks address for its headers, to say how long a file server takes to
// answer. The address is checked the way a download's is, and any response
// counts as reachable: what is measured is the round trip, not what was asked
// for. The wait is bounded, so a server that never answers does not hold the
// caller for the download client's own timeouts.
func (c *Client) Probe(ctx context.Context, address string) (time.Duration, bool) {
	if err := c.allowed(address); err != nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, address, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("User-Agent", about.UserAgent())

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	// The body of a HEAD is empty, but the connection is still the client's to
	// release.
	resp.Body.Close()
	return time.Since(start), true
}

// Get fetches the archive, reporting progress as it goes.
//
// progress is called from the goroutine that is downloading, so a caller that
// draws must hand it off to whatever draws.
//
// A download that fails partway leaves what it has in a partial file beside the
// destination, and calling Get again picks it up from there. The partial is
// removed only when the archive arrived but was not what it should have been,
// which resuming could never mend.
func (c *Client) Get(ctx context.Context, req Request, progress func(Progress)) error {
	if progress == nil {
		progress = func(Progress) {}
	}
	if err := c.allowed(req.URL); err != nil {
		return err
	}

	partial := req.Dest + partialExt
	var last error
	for attempt := 0; attempt <= reconnects; attempt++ {
		// The caller giving up is the caller's answer, and is not something to
		// try again.
		if err := ctx.Err(); err != nil {
			return err
		}
		if last = c.fetch(ctx, req.URL, partial, progress); last == nil {
			break
		}
	}
	if last != nil {
		return last
	}

	if err := verify(partial, req); err != nil {
		os.Remove(partial)
		return err
	}
	return os.Rename(partial, req.Dest)
}

// fetch makes one attempt at the whole archive, resuming from whatever is
// already in partial.
func (c *Client) fetch(ctx context.Context, address, partial string, progress func(Progress)) error {
	done := sizeOf(partial)

	resp, err := c.connect(ctx, address, done)
	if err != nil {
		// What is held being longer than the file is the one thing resuming
		// cannot get past, so it is thrown away here and the whole file is
		// asked for on the next attempt.
		if errors.Is(err, errRangeMismatch) {
			os.Remove(partial)
		}
		return err
	}
	defer resp.Body.Close()

	// A server that answers a range request with the whole file does not do
	// ranges, so what is already there is started over rather than appended to.
	resuming := resp.StatusCode == http.StatusPartialContent
	if !resuming {
		done = 0
	}
	total := total(resp, done)

	// The file is appended to when the server picked up where the last attempt
	// stopped, and written from the start otherwise.
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if resuming {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := os.OpenFile(partial, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	progress(Progress{Done: done, Total: total})

	// A connection that stops sending without saying so is what this catches.
	// Cancelling the request is what unblocks the read, and the cause it
	// carries is returned, so that the caller tries again rather than being
	// told the read was cut off.
	stalled, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(stallTimeout, func() {
		cancel(errors.New("the download stopped sending"))
	})
	defer watchdog.Stop()

	buf := make([]byte, chunk)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallTimeout)
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if done > maxSize {
				return fmt.Errorf("the archive is larger than %d bytes", int64(maxSize))
			}
			progress(Progress{Done: done, Total: total})
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if cause := context.Cause(stalled); cause != nil {
				return cause
			}
			return readErr
		}
	}
}

// connect asks for the file, following the redirects to a file server by hand
// so that each hop can be checked. from is how many bytes are already held,
// which the server is asked to send the rest of.
func (c *Client) connect(ctx context.Context, address string, from int64) (*http.Response, error) {
	for hop := 0; ; hop++ {
		if hop > maxRedirects {
			return nil, fmt.Errorf("%s redirected more than %d times", address, maxRedirects)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", about.UserAgent())
		if from > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", from))
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}

		switch resp.StatusCode {
		case http.StatusOK, http.StatusPartialContent:
			return resp, nil
		case http.StatusRequestedRangeNotSatisfiable:
			// What is held is already as long as the file, or longer.
			resp.Body.Close()
			return nil, errRangeMismatch
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			next, err := resp.Location()
			resp.Body.Close()
			if err != nil {
				return nil, err
			}
			if err := c.allowed(next.String()); err != nil {
				return nil, err
			}
			address = next.String()
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("the file server answered %s", resp.Status)
		}
	}
}

// allowed refuses an address a mod may not be fetched from.
func (c *Client) allowed(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("%s is not an address: %w", address, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s is not an https address", address)
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range c.domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return nil
		}
	}
	return fmt.Errorf("%s is not a GameBanana address", host)
}

// total is how many bytes the archive is, as the reply says. A server answering
// a range says it in the content range; one sending the whole file says it in
// the length. Neither is required to say at all.
func total(resp *http.Response, done int64) int64 {
	if resp.StatusCode == http.StatusPartialContent {
		if _, after, ok := strings.Cut(resp.Header.Get("Content-Range"), "/"); ok {
			if n, err := strconv.ParseInt(after, 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength + done
	}
	return 0
}

// verify checks that the archive is what the API said it would be. A size or a
// checksum the API did not give is not checked: a mod whose author never
// uploaded one is still a mod to install.
func verify(path string, req Request) error {
	if req.Size > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() != req.Size {
			return fmt.Errorf("the archive is %d bytes, but GameBanana says it is %d", info.Size(), req.Size)
		}
	}
	if req.MD5 == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// md5 is not a security measure and is not being used as one: it is the
	// checksum GameBanana publishes for its files, so it is the one that says
	// whether this copy is the copy they have.
	sum := md5.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, req.MD5) {
		return errors.New("the archive does not match the checksum GameBanana gives for it")
	}
	return nil
}

// sizeOf is how much of a download is already on disk, and zero when there is
// nothing there.
func sizeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}
