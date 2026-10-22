package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/BabyLy233/mylock/internal/imagecache"
)

// pictureStub answers every fetch with one picture of a fixed size, so that what
// the cache makes of it can be read without asking the site for it.
type pictureStub struct{ body []byte }

func (s pictureStub) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Header:     http.Header{},
	}, nil
}

// countingStub answers with one picture and counts how many times it was asked
// for it, which is what says whether a fetch was served from the disk cache.
type countingStub struct {
	body []byte

	mu       sync.Mutex
	requests int
}

func (s *countingStub) RoundTrip(*http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Header:     http.Header{},
	}, nil
}

func (s *countingStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// offStub refuses every fetch, so that a picture that arrives did so from disk.
type offStub struct{}

func (offStub) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("the network is not to be used")
}

// jpegOf is one picture of a size, to be answered with instead of fetching one.
func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stubImages is a cache whose pictures are all of one size, which is what the
// site publishes a submission's screenshots at.
func stubImages(t *testing.T, w, h int) *images {
	t.Helper()
	im := newImages()
	im.client = &http.Client{Transport: pictureStub{body: jpegOf(t, w, h)}}
	return im
}

// address is the address of the nth picture of a stub cache.
func address(n int) string {
	return fmt.Sprintf("https://images.gamebanana.com/img/ss/mods/%d.jpg", n)
}

// settled waits until nothing is on its way to the cache, which is when what it
// holds can be read.
func settled(t *testing.T, im *images) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		im.mu.Lock()
		n := im.inflight
		im.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the pictures never arrived")
}

// asked is what the cache has been asked for, by address.
func asked(im *images) map[string]bool {
	im.mu.Lock()
	defer im.mu.Unlock()
	out := map[string]bool{}
	for want := range im.asked {
		out[want.address] = true
	}
	return out
}

// A picture is decoded at the size it is shown at and not the size it was
// published at, which is what keeps a page of screenshots from costing what
// their originals do.
func TestAPictureIsDecodedForTheRoomItIsShownIn(t *testing.T) {
	room := box{W: 200, H: 130}
	im := stubImages(t, 1920, 1080)

	if bitmap := im.get(address(1), room, func() {}); bitmap != nil {
		t.Fatal("the picture arrived before it was fetched")
	}
	settled(t, im)

	bitmap := im.get(address(1), room, func() {})
	if bitmap == nil {
		t.Fatal("the picture did not arrive")
	}
	w, h := bitmap.Size()
	// The room, in the pixels a display of two per DIP draws it with.
	if w < room.W*2 || h < room.H*2 {
		t.Errorf("the picture was decoded at %d×%d, smaller than the room it is shown in", w, h)
	}
	if w > room.W*3 || h > room.H*3 {
		t.Errorf("the picture was decoded at %d×%d, more than the room it is shown in", w, h)
	}
}

// One picture is wanted at two sizes, and the two are kept apart: the viewer's
// is not what the card draws.
func TestOnePictureIsKeptAtEachSizeItIsWantedIn(t *testing.T) {
	im := stubImages(t, 1920, 1080)

	im.get(address(1), cardPicture, func() {})
	im.get(address(1), box{}, func() {})
	settled(t, im)

	im.mu.Lock()
	defer im.mu.Unlock()
	if len(im.loaded) != 2 {
		t.Fatalf("the cache holds %d pictures of one address, want one at each size", len(im.loaded))
	}
	card := im.loaded[picture{address: address(1), at: cardPicture.sharper()}]
	if card == nil {
		t.Fatal("the card's picture is not the one that was kept for it")
	}
	whole := im.loaded[picture{address: address(1)}]
	if whole == nil {
		t.Fatal("the viewer's picture is not the one that was kept for it")
	}
	if w, h := whole.Size(); w != 1920 || h != 1080 {
		t.Errorf("the viewer's picture is %d×%d, want it as it was published", w, h)
	}
}

// What the cache holds is kept within its budget, however many pictures are
// looked at.
func TestTheCacheIsKeptWithinItsBudget(t *testing.T) {
	room := box{W: 800, H: 600}
	im := stubImages(t, 1920, 1080)

	// Rooms this size are megabytes each, so a handful of them is over the
	// budget and the rest are looked at with it full.
	for n := range 40 {
		im.get(address(n), room, func() {})
	}
	settled(t, im)

	im.mu.Lock()
	defer im.mu.Unlock()
	if im.bytes > imageBudget {
		t.Errorf("the cache holds %d bytes, over its budget of %d", im.bytes, imageBudget)
	}
	if len(im.loaded) == 0 {
		t.Error("the cache holds nothing at all")
	}
}

// A picture the cache dropped is fetched again when the page showing it is
// drawn again, rather than left blank for good.
func TestAPictureThatWasDroppedIsFetchedAgain(t *testing.T) {
	room := box{W: 800, H: 600}
	im := stubImages(t, 1920, 1080)

	first := address(0)
	im.get(first, room, func() {})
	settled(t, im)
	if im.get(first, room, func() {}) == nil {
		t.Fatal("the first picture did not arrive")
	}

	// Enough more of them to put the first one out of the cache.
	for n := 1; n < 40; n++ {
		im.get(address(n), room, func() {})
	}
	settled(t, im)

	im.mu.Lock()
	_, still := im.loaded[picture{address: first, at: room.sharper()}]
	im.mu.Unlock()
	if still {
		t.Fatal("the first picture is still in the cache, so nothing was dropped")
	}

	if im.get(first, room, func() {}) != nil {
		t.Fatal("the dropped picture came back without being fetched")
	}
	if !asked(im)[first] {
		t.Error("the dropped picture was not asked for again")
	}
	settled(t, im)
	if im.get(first, room, func() {}) == nil {
		t.Error("the dropped picture did not come back")
	}
}

// A picture that is not worth keeping is not fetched at all: one from anywhere
// but the site, and one too large to decode.
func TestAPictureWorthlessToFetchIsNotAskedFor(t *testing.T) {
	im := stubImages(t, 1920, 1080)

	for _, bad := range []string{
		"",
		"http://images.gamebanana.com/img/ss/mods/1.jpg", // not https
		"https://example.com/img/ss/mods/1.jpg",          // not the site
	} {
		if bitmap := im.get(bad, cardPicture, func() {}); bitmap != nil {
			t.Errorf("a picture was had from %q", bad)
		}
	}
	if got := asked(im); len(got) != 0 {
		t.Errorf("the cache asked for %v", got)
	}
}

// The bytes are fetched once however many sizes one picture is drawn at: the
// card and the viewer read the same address.
func TestAPictureIsFetchedOnceHoweverManySizesItIsDrawnAt(t *testing.T) {
	cache, err := imagecache.Open(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	stub := &countingStub{body: jpegOf(t, 1920, 1080)}
	im := newImages()
	im.client = &http.Client{Transport: stub}
	im.disk = cache

	im.get(address(1), cardPicture, func() {})
	settled(t, im)
	// The second size is decoded from the bytes the first one kept.
	im.get(address(1), box{}, func() {})
	settled(t, im)
	if bitmap := im.get(address(1), box{}, func() {}); bitmap == nil {
		t.Fatal("the picture was not decoded at the viewer's size")
	}

	if got := stub.count(); got != 1 {
		t.Errorf("the picture was fetched %d times, want once for both sizes", got)
	}
	if _, ok := cache.Get(address(1)); !ok {
		t.Error("the bytes of the picture were not kept on disk")
	}
}

// A picture kept by an earlier run is not fetched again, which is what the
// disk cache is for.
func TestAPictureKeptByAnEarlierRunIsNotFetchedAgain(t *testing.T) {
	dir := t.TempDir()
	first, err := imagecache.Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := newImages()
	before.client = &http.Client{Transport: &countingStub{body: jpegOf(t, 1920, 1080)}}
	before.disk = first
	before.get(address(1), cardPicture, func() {})
	settled(t, before)

	// A cache opened again stands in for the next run, and a transport that
	// refuses everything proves the picture did not come over the network.
	reopened, err := imagecache.Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	after := newImages()
	after.client = &http.Client{Transport: offStub{}}
	after.disk = reopened

	after.get(address(1), cardPicture, func() {})
	settled(t, after)
	if bitmap := after.get(address(1), cardPicture, func() {}); bitmap == nil {
		t.Error("the picture was not read back from disk")
	}
}
