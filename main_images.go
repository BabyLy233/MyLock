package main

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"  // what a picture the store shows can be in
	_ "image/jpeg" // ditto
	_ "image/png"  // ditto
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"

	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/about"
	"github.com/BabyLy233/deadlock-mod-manager-mygo/internal/imagecache"
)

// box is the room a picture is wanted for, in DIPs: it is scaled to cover it.
// The zero box is the picture's own size, which is what the viewer shows.
//
// It is part of what a picture is kept under, because one picture is wanted at
// two sizes: a card shows a thumbnail and the viewer shows the whole thing, and
// the two are not the same picture to draw.
type box struct{ W, H int }

// picture names a fetched picture: the address it came from and the room it was
// scaled for.
type picture struct {
	address string
	at      box
}

// images holds the pictures the store shows.
//
// A picture is decoded once and kept: decoding a JPEG is not free, and a grid
// asks for every picture it shows on every frame. What is kept is scaled to the
// room it is shown in rather than the size it was published at, which is what
// keeps a page of screenshots from costing what their originals do: the site
// publishes every picture of a submission but its first at full size, which is
// several megabytes decoded and drawn a few hundred DIPs wide.
//
// A picture that has not arrived is nil, and the caller asks again on the next
// frame -- the window is redrawn when a fetch finishes, so the grid fills in as
// they come.
type images struct {
	client *http.Client
	// disk keeps the bytes of the pictures between runs, so that one seen in
	// one session is not fetched again in the next. It is nil when the platform
	// has no cache directory, in which case pictures are only kept in memory.
	disk *imagecache.Cache
	// fetching bounds how many pictures are read and decoded at once. Each is
	// several megabytes of pixels while it is decoded, and a page asks for all
	// of its pictures at once: a submission whose author posted twenty would
	// otherwise cost what twenty of them cost, all at the same moment.
	fetching chan struct{}

	mu     sync.Mutex
	loaded map[picture]*ui.Bitmap
	// order is what loaded holds, oldest first, which is what is dropped when
	// the cache is over its budget.
	order []picture
	// bytes is what the pictures in loaded hold together.
	bytes int
	// asked is what has been fetched or is being fetched, so that a picture is
	// asked for once however many frames are drawn while it is on its way.
	asked map[picture]bool
	// inflight is how many fetches are running. A test waits for it to reach
	// zero to read the cache once nothing is on its way to it.
	inflight int
}

const (
	// imageBudget is how much the kept pictures may hold together. A count
	// would say nothing about what the cache costs: the site's screenshots run
	// to 2560×1440, which is fourteen megabytes decoded, and a card's thumbnail
	// is a tenth of a megabyte.
	imageBudget = 64 << 20

	// pixelsPerDIP is how much sharper than the room it is shown in a picture
	// is kept, for a display that draws more than one pixel per DIP.
	pixelsPerDIP = 2

	// imageBytes caps a picture on the wire.
	imageBytes = 4 << 20

	// imagePixels caps a picture's size before it is decoded. What a picture
	// costs is its pixels and not its bytes -- a few hundred kilobytes of JPEG
	// can be hundreds of megabytes of pixels -- so this is the cap that keeps a
	// reply from filling the memory of the machine. The largest the site
	// publishes is under four megapixels.
	imagePixels = 24 << 20

	// imageFetches is how many pictures are read and decoded at once. Four
	// keeps what decoding costs to a few tens of megabytes whatever a page asks
	// for, and is few enough that a page of them does not read as a flood to
	// the server they come from.
	imageFetches = 4
)

// imageHosts are the only addresses a picture may be fetched from. The
// addresses come from the API rather than from the user, but a reply is still
// something a server chose, and a picture is not worth fetching from anywhere
// else.
var imageHosts = []string{"gamebanana.com", "deadlockmods.app"}

func newImages() *images {
	return &images{
		client:   &http.Client{Timeout: 30 * time.Second},
		fetching: make(chan struct{}, imageFetches),
		loaded:   map[picture]*ui.Bitmap{},
		asked:    map[picture]bool{},
	}
}

// get returns the picture at address, scaled to cover the room at, and starts
// fetching it if it is not there yet. It returns nil until the picture has
// arrived, which is what the caller draws nothing for. A zero room asks for the
// picture's own size.
//
// done is called once a fetch finishes, so that the caller can redraw.
func (im *images) get(address string, at box, done func()) *ui.Bitmap {
	want := picture{address: address, at: at.sharper()}

	im.mu.Lock()
	defer im.mu.Unlock()

	if bitmap, ok := im.loaded[want]; ok {
		return bitmap
	}
	if address == "" || im.asked[want] || !allowedImage(address) {
		return nil
	}
	im.asked[want] = true
	im.inflight++

	go func() {
		im.fetching <- struct{}{}
		bitmap := im.fetch(want)
		<-im.fetching

		im.mu.Lock()
		if bitmap != nil {
			im.keep(want, bitmap)
		}
		im.inflight--
		im.mu.Unlock()
		done()
	}()
	return nil
}

// sharper is the room a picture is kept for: the one it is shown in, in the
// pixels a display of more than one per DIP draws it with. A picture wanted at
// its own size is left at it.
func (b box) sharper() box {
	if b.W <= 0 || b.H <= 0 {
		return box{}
	}
	return box{W: b.W * pixelsPerDIP, H: b.H * pixelsPerDIP}
}

// fetch reads and decodes one picture at the room it was asked for, and returns
// nil for one that could not be had. A picture is not worth reporting a failure
// for: the card it belongs to is drawn without it.
//
// The bytes are looked for on disk before they are asked for over the network,
// and are kept there once they arrive. One address is drawn at more than one
// size -- a card's and the viewer's -- and the bytes are what the two have in
// common, so the second size is decoded from what the first one fetched.
func (im *images) fetch(want picture) *ui.Bitmap {
	if data, ok := im.disk.Get(want.address); ok {
		if bitmap := decodePicture(data, want.at); bitmap != nil {
			return bitmap
		}
		// A picture that no longer decodes is not worth keeping: it would be
		// read and refused on every run.
		im.disk.Remove(want.address)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, want.address, nil)
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

	bitmap := decodePicture(data, want.at)
	if bitmap != nil {
		// Not worth failing the picture over: whether or not it was kept, it is
		// drawn from the bytes just read.
		_ = im.disk.Put(want.address, data)
	}
	return bitmap
}

// decodePicture reads the bytes of one picture and scales it to the room at.
// What it is going to cost is read before it is paid: a picture of more pixels
// than this is refused rather than decoded.
func decodePicture(data []byte, at box) *ui.Bitmap {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width*config.Height > imagePixels {
		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return ui.NewBitmap(shrink(img, at))
}

// shrink scales img down to cover at, keeping its proportions, and leaves one
// that is already no larger as it is: a picture is worth the room it is shown
// in and no more, and scaling one up adds nothing but memory.
func shrink(img image.Image, at box) image.Image {
	if at.W <= 0 || at.H <= 0 {
		return img
	}
	bounds := img.Bounds()
	// The larger of the two ratios, so that the result covers the room and the
	// element showing it crops rather than stretches what is left over.
	scale := math.Max(
		float64(at.W)/float64(bounds.Dx()),
		float64(at.H)/float64(bounds.Dy()))
	if scale >= 1 {
		return img
	}
	w, h := max(1, int(float64(bounds.Dx())*scale)), max(1, int(float64(bounds.Dy())*scale))
	small := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(small, small.Bounds(), img, bounds, draw.Src, nil)
	return small
}

// keep adds a picture to the cache, dropping the oldest until what is held is
// within the budget. The caller holds the lock.
func (im *images) keep(want picture, bitmap *ui.Bitmap) {
	im.loaded[want] = bitmap
	im.order = append(im.order, want)
	im.bytes += bitmapBytes(bitmap)

	// The picture just kept is never the one dropped, however large it is:
	// dropping it would leave the frame that asked for it asking again on the
	// next one, and fetching it again, for as long as it was shown.
	for len(im.order) > 1 && im.bytes > imageBudget {
		oldest := im.order[0]
		im.order[0] = picture{}
		im.order = im.order[1:]
		im.bytes -= bitmapBytes(im.loaded[oldest])
		delete(im.loaded, oldest)
		// Forgotten as well, so that a picture the user comes back to is
		// fetched again rather than left blank for good.
		delete(im.asked, oldest)
	}
}

// bitmapBytes is what a bitmap holds, which is what the cache is kept to.
func bitmapBytes(bitmap *ui.Bitmap) int {
	w, h := bitmap.Size()
	return w * h * 4
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
