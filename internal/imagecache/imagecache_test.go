package imagecache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// picture is bytes to keep, standing in for what came over the wire.
func picture(n int) []byte {
	return bytes.Repeat([]byte{byte(n)}, 100)
}

func open(t *testing.T, budget int64) *Cache {
	t.Helper()
	cache, err := Open(t.TempDir(), budget)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

// A picture that was kept is read back byte for byte.
func TestAPictureIsReadBackAsItWasKept(t *testing.T) {
	cache := open(t, 0)
	data := picture(1)

	if err := cache.Put("https://images.gamebanana.com/a.jpg", data); err != nil {
		t.Fatal(err)
	}
	kept, ok := cache.Get("https://images.gamebanana.com/a.jpg")
	if !ok {
		t.Fatal("the picture was not kept")
	}
	if !bytes.Equal(kept, data) {
		t.Error("the picture read back is not the one that was kept")
	}
	if _, ok := cache.Get("https://images.gamebanana.com/b.jpg"); ok {
		t.Error("a picture that was never kept was read back")
	}
}

// What a run keeps is there for the next one.
func TestWhatIsKeptOutlivesTheRun(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	data := picture(2)
	if err := first.Put("https://images.gamebanana.com/a.jpg", data); err != nil {
		t.Fatal(err)
	}

	next, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	kept, ok := next.Get("https://images.gamebanana.com/a.jpg")
	if !ok {
		t.Fatal("the picture did not survive the restart")
	}
	if !bytes.Equal(kept, data) {
		t.Error("the picture read back is not the one that was kept")
	}
	if bytes, pictures := next.Stats(); bytes != int64(len(data)) || pictures != 1 {
		t.Errorf("the cache holds %d bytes in %d pictures, want %d in 1", bytes, pictures, len(data))
	}
}

// The cache is kept within its budget, dropping the picture that was looked at
// least recently.
func TestTheLeastRecentlyUsedPictureIsDroppedFirst(t *testing.T) {
	cache := open(t, 250)
	clock := time.Unix(1_000_000, 0)
	cache.now = func() time.Time { return clock }
	later := func() {
		clock = clock.Add(time.Minute)
	}

	for _, address := range []string{"a", "b", "c"} {
		later()
		if err := cache.Put("https://images.gamebanana.com/"+address+".jpg", picture(len(address))); err != nil {
			t.Fatal(err)
		}
	}

	// a was the oldest, so it is the one the budget dropped. b is looked at
	// again, which makes c the oldest, and it is the one dropped next.
	if _, ok := cache.Get("https://images.gamebanana.com/a.jpg"); ok {
		t.Fatal("the oldest picture is still there, over the budget")
	}
	later()
	if _, ok := cache.Get("https://images.gamebanana.com/b.jpg"); !ok {
		t.Fatal("the picture that was looked at was dropped")
	}
	later()
	if err := cache.Put("https://images.gamebanana.com/d.jpg", picture(4)); err != nil {
		t.Fatal(err)
	}

	if bytes, pictures := cache.Stats(); bytes > cache.budget {
		t.Errorf("the cache holds %d bytes, over its budget of %d", bytes, cache.budget)
	} else if pictures != 2 {
		t.Errorf("the cache holds %d pictures, want 2", pictures)
	}
	if _, ok := cache.Get("https://images.gamebanana.com/c.jpg"); ok {
		t.Error("the picture looked at least recently was not the one dropped")
	}
	for _, address := range []string{"b", "d"} {
		if _, ok := cache.Get("https://images.gamebanana.com/" + address + ".jpg"); !ok {
			t.Errorf("picture %s was dropped, but it was worth keeping", address)
		}
	}
}

// A picture larger than the whole budget is not kept, rather than leaving the
// cache permanently over it.
func TestAPictureLargerThanTheBudgetIsNotKept(t *testing.T) {
	cache := open(t, 100)
	if err := cache.Put("https://images.gamebanana.com/big.jpg", bytes.Repeat([]byte{1}, 200)); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get("https://images.gamebanana.com/big.jpg"); ok {
		t.Error("a picture larger than the budget was kept")
	}
	if bytes, pictures := cache.Stats(); bytes != 0 || pictures != 0 {
		t.Errorf("the cache holds %d bytes in %d pictures, want nothing", bytes, pictures)
	}
}

// Clearing the cache leaves it empty and ready for the next picture.
func TestClearingLeavesTheCacheEmpty(t *testing.T) {
	dir := t.TempDir()
	cache, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put("https://images.gamebanana.com/a.jpg", picture(1)); err != nil {
		t.Fatal(err)
	}
	if err := cache.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get("https://images.gamebanana.com/a.jpg"); ok {
		t.Error("a cleared picture is still there")
	}
	if bytes, pictures := cache.Stats(); bytes != 0 || pictures != 0 {
		t.Errorf("the cache holds %d bytes in %d pictures after clearing", bytes, pictures)
	}
	if err := cache.Put("https://images.gamebanana.com/b.jpg", picture(2)); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get("https://images.gamebanana.com/b.jpg"); !ok {
		t.Error("the cache does not take pictures after being cleared")
	}
}

// A write interrupted by a crash leaves a temporary file behind, which the next
// run removes rather than counting as a picture.
func TestAnInterruptedWriteIsClearedAway(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "ab")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".tmp-123"), []byte("half a picture"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sub, ".tmp-123")); !os.IsNotExist(err) {
		t.Error("the interrupted write was left in the cache")
	}
	if bytes, pictures := cache.Stats(); bytes != 0 || pictures != 0 {
		t.Errorf("the cache holds %d bytes in %d pictures, want nothing", bytes, pictures)
	}
}
