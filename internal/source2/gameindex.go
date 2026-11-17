// Package source2 indexes the game's VPK archives and exposes resource reads
// through the ported VPK reader.
package source2

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/BabyLy233/mylock/third_party/deadlock-mod-manager/port/vpk"
)

const (
	maxCachedBytes   = 64 << 20
	maxCachedEntries = 128
	maxEntryBytes    = 64 << 20
)

// Provider identifies where a resource lives.
type Provider struct {
	Archive string // directory VPK path, or a loose file path
	Entry   string // entry path inside the archive; empty for loose files
}

// GameIndex indexes game/citadel and game/core archives.
type GameIndex struct {
	directories []string
	archives    map[string]*vpk.Archive
	byDir       map[string]map[string]Provider

	mu    sync.Mutex
	cache map[string][]byte
	order []string
	bytes int
}

// OpenGame indexes the archives under citadelDir and its sibling core
// directory. Missing directories are skipped.
func OpenGame(citadelDir string) (*GameIndex, error) {
	g := &GameIndex{
		directories: []string{citadelDir, filepath.Join(filepath.Dir(citadelDir), "core")},
		archives:    make(map[string]*vpk.Archive),
		byDir:       make(map[string]map[string]Provider),
		cache:       make(map[string][]byte),
	}
	for _, dir := range g.directories {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", dir, err)
		}
		var paths []string
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, "_dir.vpk") {
				continue
			}
			paths = append(paths, filepath.Join(dir, name))
		}
		sort.Strings(paths)
		providers := make(map[string]Provider)
		for _, p := range paths {
			archive, err := vpk.Open(p)
			if err != nil {
				return nil, fmt.Errorf("indexing %s: %w", p, err)
			}
			g.archives[p] = archive
			for _, entry := range archive.Entries {
				normalized, ok := vpk.NormalizeResourcePath(entry.FullPath)
				if !ok {
					continue
				}
				key := strings.ToLower(normalized)
				if _, exists := providers[key]; !exists {
					providers[key] = Provider{Archive: p, Entry: entry.FullPath}
				}
			}
		}
		g.byDir[dir] = providers
	}
	return g, nil
}

// ResourcePath normalizes a resource reference the way the engine resolves it:
// separators are normalized, the name is lowercased, fragment references are
// rejected, and a missing _c suffix is added (except for .txt files).
func ResourcePath(name string) (string, bool) {
	normalized, ok := vpk.NormalizeResourcePath(name)
	if !ok {
		return "", false
	}
	path := strings.ToLower(normalized)
	if strings.Contains(path, "#") {
		return "", false
	}
	if !strings.HasSuffix(path, "_c") && !strings.HasSuffix(path, ".txt") {
		path += "_c"
	}
	return path, true
}

// Provider returns the provider for a resource reference.
func (g *GameIndex) Provider(name string) (Provider, bool) {
	path, ok := ResourcePath(name)
	if !ok {
		return Provider{}, false
	}
	for _, dir := range g.directories {
		loose := filepath.Join(dir, filepath.FromSlash(path))
		if info, err := os.Stat(loose); err == nil && info.Mode().IsRegular() {
			return Provider{Archive: loose}, true
		}
		if provider, ok := g.byDir[dir][path]; ok {
			return provider, true
		}
	}
	return Provider{}, false
}

// GameBytes reads a game resource, returning false when the game does not have
// it.
func (g *GameIndex) GameBytes(name string) ([]byte, bool) {
	provider, ok := g.Provider(name)
	if !ok {
		return nil, false
	}
	data, err := g.Read(provider)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Read reads the bytes of one provider, caching game-owned bytes.
func (g *GameIndex) Read(provider Provider) ([]byte, error) {
	key := provider.Archive
	if provider.Entry != "" {
		key += "\x00" + provider.Entry
	}
	g.mu.Lock()
	if data, ok := g.cache[key]; ok {
		for i, k := range g.order {
			if k == key {
				g.order = append(g.order[:i], g.order[i+1:]...)
				break
			}
		}
		g.order = append(g.order, key)
		out := append([]byte(nil), data...)
		g.mu.Unlock()
		return out, nil
	}
	g.mu.Unlock()

	var data []byte
	var err error
	if provider.Entry == "" {
		data, err = os.ReadFile(provider.Archive)
	} else {
		archive, ok := g.archives[provider.Archive]
		if !ok {
			return nil, fmt.Errorf("source2: archive %s is not indexed", provider.Archive)
		}
		data, err = archive.ReadLimited(provider.Entry, maxEntryBytes)
	}
	if err != nil {
		return nil, err
	}
	g.cachePut(key, data)
	return append([]byte(nil), data...), nil
}

func (g *GameIndex) cachePut(key string, data []byte) {
	if len(data) > maxCachedBytes {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.cache[key]; exists {
		return
	}
	for len(g.order) > 0 && (g.bytes+len(data) > maxCachedBytes || len(g.order) >= maxCachedEntries) {
		oldest := g.order[0]
		g.order = g.order[1:]
		if old, ok := g.cache[oldest]; ok {
			g.bytes -= len(old)
			delete(g.cache, oldest)
		}
	}
	g.cache[key] = append([]byte(nil), data...)
	g.order = append(g.order, key)
	g.bytes += len(data)
}

// Directories returns the directories searched for loose game resources.
func (g *GameIndex) Directories() []string {
	out := make([]string, len(g.directories))
	copy(out, g.directories)
	return out
}
