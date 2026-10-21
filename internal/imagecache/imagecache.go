// Package imagecache keeps the bytes of the pictures the store shows, so that a
// picture seen in one run is not fetched again in the next.
//
// What is kept is the picture as it came over the wire, not the decoded one:
// one address is drawn at a card's size and again at the viewer's, and the
// bytes are what the two have in common. Keeping the decoded pictures instead
// would mean one copy per size, several megabytes each.
//
// What is kept is bounded. A cache that only ever grew would cost the user disk
// for pictures they stopped looking at long ago, so the least recently used
// pictures are dropped once the budget is reached. Most recently used is the
// file's modification time, which makes the order survive a restart without an
// index of its own to fall out of step with the directory.
package imagecache

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBudget is how much disk the pictures may take together. The
	// pictures are kept compressed, so this holds hundreds of screenshots and
	// tens of thousands of thumbnails.
	DefaultBudget = 128 << 20

	// maxPicture is the largest picture worth keeping, which is the same cap the
	// fetch puts on one.
	maxPicture = 4 << 20

	// maxPictures caps how many files the cache holds, so that a great many
	// thumbnails cannot cost more in directory entries than the bytes are worth.
	maxPictures = 20000

	// appDir is the directory the app keeps its files in, under the cache
	// directory of the platform. It is the same name the settings use.
	appDir = "mylock"
)

// entry is one picture the cache holds.
type entry struct {
	key  string
	size int64
}

// Cache is the pictures kept on disk. It is safe to use from several
// goroutines, which is what the four fetches running at once need.
type Cache struct {
	// dir is the directory the pictures are in.
	dir string
	// budget is how many bytes the pictures may take together.
	budget int64

	mu sync.Mutex
	// entries is what is held, by key, and order is the same, most recently
	// used first. Entries are dropped off the back of order.
	entries map[string]*list.Element
	order   *list.List
	// bytes is what the entries hold together.
	bytes int64

	// now is the clock a use is recorded at. A test sets it so that the order
	// pictures are dropped in does not depend on how fast the test runs.
	now func() time.Time
}

// Path returns the directory the pictures are kept in, under the platform's
// cache directory: ~/.cache on Linux, %LocalAppData% on Windows, and
// ~/Library/Caches on macOS. These are directories the system and the user
// treat as disposable, which is what a cache is.
func Path() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache directory: %w", err)
	}
	return filepath.Join(dir, appDir, "images"), nil
}

// Open reads the pictures at dir and makes them fit budget, which is
// DefaultBudget when it is not positive.
func Open(dir string, budget int64) (*Cache, error) {
	if budget <= 0 {
		budget = DefaultBudget
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating the picture cache at %s: %w", dir, err)
	}
	c := &Cache{
		dir:     dir,
		budget:  budget,
		entries: map[string]*list.Element{},
		order:   list.New(),
		now:     time.Now,
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// load reads what a previous run left behind. The modification time is the
// order the pictures are dropped in, which is why there is no index to read.
func (c *Cache) load() error {
	type kept struct {
		key  string
		size int64
		used time.Time
	}
	var pictures []kept

	err := filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// A write interrupted before its rename left one of these behind; it is
		// not a picture, and nothing will ever read it.
		if strings.HasPrefix(d.Name(), ".tmp-") {
			_ = os.Remove(path)
			return nil
		}
		if !isKey(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		pictures = append(pictures, kept{key: d.Name(), size: info.Size(), used: info.ModTime()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading the picture cache at %s: %w", c.dir, err)
	}

	slices.SortFunc(pictures, func(a, b kept) int { return a.used.Compare(b.used) })
	for _, picture := range pictures {
		c.entries[picture.key] = c.order.PushFront(&entry{key: picture.key, size: picture.size})
		c.bytes += picture.size
	}
	c.trimLocked()
	return nil
}

// Get returns the bytes kept for address, and reports whether there were any.
//
// The bytes are the picture as it was published, not the size it is drawn at:
// the caller decodes them for the room it has.
func (c *Cache) Get(address string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	key := keyFor(address)

	c.mu.Lock()
	_, held := c.entries[key]
	c.mu.Unlock()
	if !held {
		return nil, false
	}

	data, err := os.ReadFile(c.path(key))
	if err != nil {
		c.Remove(address)
		return nil, false
	}

	c.mu.Lock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
	}
	c.mu.Unlock()
	c.touch(key)
	return data, true
}

// Put keeps data as the picture at address. A picture that could not be written
// is not worth failing a fetch for: the picture is drawn from what was just
// read whether or not it was kept.
func (c *Cache) Put(address string, data []byte) error {
	if c == nil || len(data) == 0 {
		return nil
	}
	// A picture larger than the whole budget would leave the cache over it the
	// moment it was kept, so it is not kept at all.
	if int64(len(data)) > maxPicture || int64(len(data)) > c.budget {
		return nil
	}

	key := keyFor(address)
	c.mu.Lock()
	defer c.mu.Unlock()

	dir := filepath.Dir(c.path(key))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("writing the picture at %s: %w", dir, err)
	}
	// A no-op once the rename below has moved the file out of the way.
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing the picture at %s: %w", dir, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing the picture at %s: %w", dir, err)
	}
	if err := os.Rename(tmp.Name(), c.path(key)); err != nil {
		return fmt.Errorf("keeping the picture at %s: %w", c.path(key), err)
	}
	// The modification time is the order the picture is dropped in, so it is
	// set from the same clock a read uses.
	used := c.now()
	_ = os.Chtimes(c.path(key), used, used)

	c.rememberLocked(key, int64(len(data)))
	return nil
}

// Remove forgets the picture at address. It is what a picture that was kept but
// no longer decodes is dropped with.
func (c *Cache) Remove(address string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(keyFor(address))
}

// Clear removes every picture. What is in flight is not waited for: a fetch
// that lands afterwards is kept again, which is what the next run would do too.
func (c *Cache) Clear() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.RemoveAll(c.dir); err != nil {
		return fmt.Errorf("clearing the picture cache at %s: %w", c.dir, err)
	}
	clear(c.entries)
	c.order.Init()
	c.bytes = 0
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("creating the picture cache at %s: %w", c.dir, err)
	}
	return nil
}

// Stats is what the cache holds: the bytes, and how many pictures those are.
func (c *Cache) Stats() (bytes int64, pictures int) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes, len(c.entries)
}

// Budget is how many bytes the cache may hold together.
func (c *Cache) Budget() int64 {
	if c == nil {
		return 0
	}
	return c.budget
}

// rememberLocked records a picture just written, making it the most recently
// used. The caller holds the lock.
func (c *Cache) rememberLocked(key string, size int64) {
	if element, ok := c.entries[key]; ok {
		kept := element.Value.(*entry)
		c.bytes += size - kept.size
		kept.size = size
		c.order.MoveToFront(element)
	} else {
		c.entries[key] = c.order.PushFront(&entry{key: key, size: size})
		c.bytes += size
	}
	c.trimLocked()
}

// removeLocked forgets one picture and its file. The caller holds the lock.
func (c *Cache) removeLocked(key string) {
	if element, ok := c.entries[key]; ok {
		kept := element.Value.(*entry)
		c.bytes -= kept.size
		c.order.Remove(element)
		delete(c.entries, key)
	}
	_ = os.Remove(c.path(key))
}

// trimLocked drops the least recently used pictures until the cache is within
// its budget and its count. The caller holds the lock.
func (c *Cache) trimLocked() {
	for c.bytes > c.budget || len(c.entries) > maxPictures {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		c.removeLocked(oldest.Value.(*entry).key)
	}
}

// touch records that the picture was read, which is what keeps a picture the
// user comes back to from being dropped before one they have not looked at.
func (c *Cache) touch(key string) {
	used := c.now()
	_ = os.Chtimes(c.path(key), used, used)
}

// path is where the picture kept under key lives. The first two characters of a
// key name a directory of their own, so that the cache is not one directory
// with twenty thousand files in it.
func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key)
}

// keyFor names a picture by its address. The address is what a picture is
// fetched by, so it is what one is kept under: the same picture at two sizes is
// one entry on the wire.
func keyFor(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])
}

// isKey reports whether name is one of the cache's own file names.
func isKey(name string) bool {
	if len(name) != 2*sha256.Size {
		return false
	}
	for i := range len(name) {
		ch := name[i]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
