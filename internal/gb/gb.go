// Package gb reads GameBanana's catalogue, which is where Deadlock's mods are
// published.
//
// Everything here is public: no key, no account, no sign-in. The API is v11,
// which answers JSON but is not a JSON API in the ways that matter -- it
// sometimes writes a PHP warning before the body, it reports its content type
// as HTML whatever the body holds, it ignores a parameter it does not like
// rather than refusing it, and it rate limits by the second. This client is
// written around what the API actually does, and each of those is noted where
// it is handled.
package gb

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BabyLy233/mylock/internal/about"
	"github.com/BabyLy233/mylock/internal/proxy"
)

// Game is Deadlock's id on GameBanana. Every request here filters by it: the
// catalogue is shared with every other game, and an unfiltered listing answers
// with theirs.
const Game = 20948

// api is the root of API v11.
const api = "https://gamebanana.com/apiv11/"

// legacyAPI is the root of the older API. It is the only host carrying
// Core/Item/Data, which is the one request that asks about several submissions
// at once.
const legacyAPI = "https://api.gamebanana.com/"

const (
	// maxBodyBytes caps a reply. Catalogue pages run to a few hundred
	// kilobytes; the cap is what keeps a reply that never ends from filling
	// memory.
	maxBodyBytes = 5 << 20
	// maxErrorBytes caps how much of a failed reply is read to explain it.
	maxErrorBytes = 8 << 10
	// maxDetail caps the explanation kept from a failed reply, so that an HTML
	// block page in front of the API does not become the error message.
	maxDetail = 300

	// perSecond and burst pace the requests. The API answers a burst with 429s
	// rather than with data, and one page of the store is a request plus one
	// for each file the user opens.
	perSecond = 10
	burst     = 4

	// retries is how many times a request is tried again after a failure the
	// API could recover from.
	retries = 3

	// minBackoff and maxBackoff bound the pause between tries.
	minBackoff = 500 * time.Millisecond
	maxBackoff = 30 * time.Second
)

// Client reads the catalogue. It is safe to use from several goroutines; make
// one with New.
type Client struct {
	// Base is the root of the API. It is GameBanana's own unless it is pointed
	// somewhere else, which is what a test does to read from a server of its
	// own.
	Base string
	// LegacyBase is the root of the older API, where the request that asks
	// about several submissions at once lives. It is set beside Base, and a
	// test points both at the same server.
	LegacyBase string
	http       *http.Client
	// proxy is the proxy setting the transport reads on every request, so that
	// saving one takes effect without rebuilding the client.
	proxy *proxy.Source
	limit *rate.Limiter

	// sleep pauses before a retry. Tests replace it so that a backoff does not
	// make them slow.
	sleep func(context.Context, time.Duration) error

	// fileservers is the list of download servers last read, and fileserversAt
	// when it was. The lock also keeps several callers from asking for the
	// list at once: the first asks, the rest read what it left.
	fileserversMu sync.Mutex
	fileservers   []Fileserver
	fileserversAt time.Time
}

// New returns a client reading the live catalogue.
func New() *Client {
	p := &proxy.Source{}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = p.Proxy
	return &Client{
		Base:       api,
		LegacyBase: legacyAPI,
		http:       &http.Client{Timeout: 30 * time.Second, Transport: transport},
		proxy:      p,
		limit:      rate.NewLimiter(perSecond, burst),
	}
}

// SetProxy makes the client use cfg for the requests it makes from now on. A
// setting that cannot be used is refused and leaves the previous one in place.
func (c *Client) SetProxy(cfg proxy.Config) error {
	return c.proxy.Set(cfg)
}

// ErrNotFound is a submission or a file that is not there: taken down, made
// private, or never published. It is what a link the user kept from a while ago
// comes back as.
var ErrNotFound = errors.New("gamebanana: not found")

// ErrRefused is the API turning the request away without reading it, which is
// what a block or a proxy in front of it looks like.
var ErrRefused = errors.New("gamebanana: refused")

// statusError is a reply that was not the data: the API's way of saying no.
type statusError struct {
	path string
	// status is the HTTP status, and is zero for a failure the API sent with a
	// 200: it names those in the body instead, and there is no number to give.
	status int
	// code is the API's own _sErrorCode, when the body carried one. It says what
	// kind of failure this is more precisely than the status does -- a record
	// that is gone is NO_SUCH_RECORD whether it arrives as a 404 or a 200.
	code       string
	retryAfter time.Duration
	detail     string
}

func (e *statusError) Error() string {
	// The API explains itself in the body, and that sentence is more use to
	// the user than the status code is.
	reason := e.code
	if e.status != 0 {
		reason = fmt.Sprintf("%d %s", e.status, http.StatusText(e.status))
	}
	if e.detail != "" {
		return fmt.Sprintf("gamebanana %s: %s (%s)", e.path, e.detail, reason)
	}
	return fmt.Sprintf("gamebanana %s: %s", e.path, reason)
}

func (e *statusError) Unwrap() error {
	// The API names a missing record in the body as well as in the status, and
	// does not always use the status to say it.
	if e.code == "NO_SUCH_RECORD" {
		return ErrNotFound
	}
	switch e.status {
	case http.StatusNotFound, http.StatusGone:
		return ErrNotFound
	case http.StatusForbidden:
		return ErrRefused
	}
	return nil
}

// retryable reports whether asking again could give a different answer. A 4xx
// is the request's own fault and fails the same way every time; a 429 or a 5xx
// is the service's, and passes. A failure the API sent with a 200 is one it has
// already decided about, so it is not asked about again either.
func (e *statusError) retryable() bool {
	return e.status == http.StatusTooManyRequests || e.status >= 500
}

// get reads one endpoint as JSON into v. path is relative to the API root and
// carries its own query.
func (c *Client) get(ctx context.Context, path string, v any) error {
	return c.getFrom(ctx, c.Base, path, v)
}

// getFrom reads one endpoint from the API rooted at base, which is how the
// older host is asked without a second client.
func (c *Client) getFrom(ctx context.Context, base, path string, v any) error {
	body, err := c.fetchFrom(ctx, base, path)
	if err != nil {
		return err
	}
	body = jsonBody(body)
	// A refusal is not always a status. The API answers a request it does not
	// like with 400 and an error object, and sometimes sends that same object
	// with a 200, where reading it as the data would leave an empty result and
	// no error at all -- the one failure the caller cannot see.
	if failure := apiFailure(path, body); failure != nil {
		return failure
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("gamebanana %s: reading the reply: %w", path, err)
	}
	return nil
}

// fetch reads one endpoint, trying again while the failure looks like one that
// passes. The API is a shared service run on donations: a 429 or a 5xx is it
// asking for room, not an answer.
func (c *Client) fetchFrom(ctx context.Context, base, path string) ([]byte, error) {
	var last error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if err := c.wait(ctx, c.retryDelay(attempt, last)); err != nil {
				return nil, err
			}
		}
		if err := c.limit.Wait(ctx); err != nil {
			return nil, err
		}

		body, err := c.onceFrom(ctx, base, path)
		if err == nil {
			return body, nil
		}
		last = err
		if attempt >= retries || !retryable(err) {
			return nil, err
		}
	}
}

// retryable reports whether an error is worth another try.
func retryable(err error) bool {
	if status, ok := errors.AsType[*statusError](err); ok {
		return status.retryable()
	}
	// Anything that did not reach the API at all -- a refused connection, a
	// timeout, a proxy that went away -- is worth trying again.
	return true
}

// onceFrom makes one request to the API rooted at base.
func (c *Client) onceFrom(ctx context.Context, base, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", about.UserAgent())
	// What is asked for is asked for by this header and nothing else: the API
	// labels its JSON as text/html, so a reply is never judged by its content
	// type.
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// The body is read whatever the status: the API says why it refused in
		// it, which is worth more to the user than the number.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, &statusError{
			path:       path,
			status:     resp.StatusCode,
			retryAfter: retryAfter(resp),
			detail:     detail(body),
		}
	}

	// One byte past the cap, so that a body which ran into it is reported as
	// too big rather than handed on to be decoded as a truncated page.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("gamebanana %s: the reply is larger than %d bytes", path, maxBodyBytes)
	}
	return body, nil
}

// wait pauses for d, or returns early if the caller gives up first.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retryDelay is how long to wait before trying again: what the API asked for
// when it said so, and a growing pause when it did not.
func (c *Client) retryDelay(attempt int, last error) time.Duration {
	if status, ok := errors.AsType[*statusError](last); ok && status.retryAfter > 0 {
		return status.retryAfter
	}
	return backoff(attempt)
}

// backoff is how long to wait before the attempt-th try, counting the first
// retry as 1. Each waits twice as long as the last, and half the delay is drawn
// at random, so that a service which turned several requests away at once is
// not asked again in step.
func backoff(attempt int) time.Duration {
	// The shift is capped so that a large attempt cannot overflow the duration
	// before the min with the ceiling brings it back down.
	d := min(minBackoff<<min(attempt-1, 16), maxBackoff)
	return d/2 + rand.N(d/2+1)
}

// retryAfter reads how long the API asked to be left alone for. It is sent
// either as a number of seconds or as a date, and both are legal.
func retryAfter(resp *http.Response) time.Duration {
	value := resp.Header.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(time.Until(when), 0)
	}
	return 0
}

// apiError is how the API says no: a code and a sentence, in JSON.
type apiError struct {
	Code    string `json:"_sErrorCode"`
	Message string `json:"_sErrorMessage"`
	// Data is where the API puts the detail of a complaint about a parameter:
	// the name of the field it objected to, mapped to the same shape again.
	Data map[string]apiError `json:"_aErrorData"`
}

// message is the sentence to show. The API writes it beside the code when the
// complaint is about the request as a whole, and one level in, under the name of
// the field it objected to, when it is about a parameter -- which is the case
// worth reading, since it is what a misspelled filter comes back as.
func (e apiError) message() string {
	if e.Message != "" {
		return e.Message
	}
	// Sorted, so that a body objecting to several fields reads the same way
	// every time.
	for _, field := range slices.Sorted(maps.Keys(e.Data)) {
		if message := e.Data[field].Message; message != "" {
			return message
		}
	}
	return ""
}

// apiFailure reads the API's own failure out of a reply that arrived with a 200.
// body is the reply with jsonBody already applied. A reply that is not the
// API's error object -- a page, or an array, which does not fit one at all --
// is not a failure and reads as nothing here.
func apiFailure(path string, body []byte) *statusError {
	var e apiError
	if err := json.Unmarshal(body, &e); err != nil || e.Code == "" {
		return nil
	}
	return &statusError{path: path, code: e.Code, detail: summarize(e.message())}
}

// detail reads the explanation out of a failed reply. The API answers its own
// errors with JSON, but a proxy or a block page in front of it answers with
// HTML, so this gives up quietly rather than reporting a parse failure instead
// of the refusal.
func detail(body []byte) string {
	var e apiError
	if err := json.Unmarshal(jsonBody(body), &e); err != nil {
		return ""
	}
	return summarize(e.message())
}

// summarize cuts a sentence down to something that can go in an error message.
// The API's is one line, but a body that is not the API's could be anything.
func summarize(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > maxDetail {
		message = message[:maxDetail] + "…"
	}
	return message
}

// jsonBody finds where the JSON starts. The API sometimes writes a PHP warning,
// a notice or a blank line before the body, and no JSON decoder will skip it.
func jsonBody(data []byte) []byte {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return trimmed
	}
	// Otherwise the JSON begins on a line of its own, after whatever the API
	// wrote before it. The two bytes of "\n{" are the separator and the opening
	// brace, and only the second is part of the JSON, so this cuts at an offset
	// rather than through Cut, which would drop the brace with the newline.
	for _, start := range []string{"\n{", "\n["} {
		if i := bytes.Index(data, []byte(start)); i >= 0 {
			return data[i+1:]
		}
	}
	return trimmed
}
