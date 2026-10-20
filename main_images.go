package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/about"
)

// images holds the thumbnails the store shows.
//
// A bitmap is decoded once and kept: decoding a JPEG is not free, and a grid
// asks for every picture it shows on every frame. A picture that has not
// arrived is nil, and the caller asks again on the next frame -- the window is
// redrawn when a fetch finishes, so the grid fills in as they come.
type images struct {
	client *http.Client

	mu     sync.Mutex
	loaded map[string]*ui.Bitmap
	// order is the keys of loaded, oldest first, which is what is dropped when
	// there are too many.
	order []string
	// asked is what has been fetched or is being fetched, so that a picture is
	// asked for once however many frames are drawn while it is on its way.
	asked map[string]bool
}

const (
	// imageCache is how many thumbnails are kept. A page of the store shows
	// fifty, and the user pages through a few before the oldest are of no use
	// to anyone.
	imageCache = 256

	// imageBytes caps a picture. The API publishes them at a few sizes, the
	// largest of which is well under this.
	imageBytes = 4 << 20
)

// imageHosts are the only addresses a picture may be fetched from. The
// addresses come from the API rather than from the user, but a reply is still
// something a server chose, and a picture is not worth fetching from anywhere
// else.
var imageHosts = []string{"gamebanana.com", "deadlockmods.app"}

func newImages() *images {
	return &images{
		client: &http.Client{Timeout: 30 * time.Second},
		loaded: map[string]*ui.Bitmap{},
		asked:  map[string]bool{},
	}
}

// get returns the picture at address, and starts fetching it if it is not there
// yet. It returns nil until the picture has arrived, which is what the caller
// draws nothing for.
//
// done is called once a fetch finishes, so that the caller can redraw.
func (im *images) get(address string, done func()) *ui.Bitmap {
	im.mu.Lock()
	defer im.mu.Unlock()

	if bitmap, ok := im.loaded[address]; ok {
		return bitmap
	}
	if address == "" || im.asked[address] || !allowedImage(address) {
		return nil
	}
	im.asked[address] = true

	go func() {
		bitmap := im.fetch(address)
		if bitmap != nil {
			im.mu.Lock()
			im.keep(address, bitmap)
			im.mu.Unlock()
		}
		done()
	}()
	return nil
}

// fetch reads and decodes one picture, and returns nil for one that could not
// be had. A picture is not worth reporting a failure for: the card it belongs
// to is drawn without it.
func (im *images) fetch(address string) *ui.Bitmap {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", about.UserAgent)
	resp, err := im.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, imageBytes+1))
	if err != nil || len(data) > imageBytes {
		return nil
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil
	}
	return bitmap
}

// keep adds a picture to the cache, dropping the oldest when there are too
// many. The caller holds the lock.
func (im *images) keep(address string, bitmap *ui.Bitmap) {
	im.loaded[address] = bitmap
	im.order = append(im.order, address)
	for len(im.order) > imageCache {
		oldest := im.order[0]
		im.order = im.order[1:]
		delete(im.loaded, oldest)
		// Nothing is dropped from asked: a picture that has been fetched once
		// and then evicted is one the user has scrolled past, and asking for
		// it again on the way back would be a request per frame.
	}
}

// allowedImage refuses an address a picture may not be fetched from.
func allowedImage(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range imageHosts {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
