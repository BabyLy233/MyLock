package addons

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Profile is one profile's mods: the manifest that describes them, and the
// directories their files are in.
type Profile struct {
	// Dir holds the manifest and the disabled VPKs.
	Dir string
	// CitadelDir is the directory the shard roots sit in.
	CitadelDir string
	// Folder is the profile's folder name, empty for the default profile,
	// which is citadel/addons itself.
	Folder string
}

// Open returns the default profile, the one whose files are directly in
// addonsDir, which is an installation's citadel/addons directory.
func Open(addonsDir string) Profile {
	dir := filepath.Clean(addonsDir)
	return Profile{Dir: dir, CitadelDir: filepath.Dir(dir)}
}

// OpenNamed returns the profile in the folder of that name under addonsDir.
func OpenNamed(addonsDir, folder string) Profile {
	p := Open(addonsDir)
	p.Folder = folder
	p.Dir = filepath.Join(p.Dir, folder)
	return p
}

// ManifestPath is where the profile's manifest is.
func (p Profile) ManifestPath() string {
	return filepath.Join(p.Dir, ManifestName)
}

// ShardDir is the directory holding the enabled VPKs of one shard. Only the
// first shard is the profile directory itself; the rest are roots of their
// own, beside it, which is why the game is told to search all of them.
func (p Profile) ShardDir(shard uint32) string {
	if shard <= FirstShard {
		return p.Dir
	}
	root := filepath.Join(p.CitadelDir, fmt.Sprintf("addons%d", shard))
	if p.Folder == "" {
		return root
	}
	return filepath.Join(root, p.Folder)
}

// SearchPaths returns the directories under citadel the game has to search to
// load this profile's mods: one per shard in use, the first always among them,
// in the order the game searches them.
//
// This is what goes into gameinfo.gi. A shard nothing is in is left out rather
// than searched for nothing on every load, and a shard is kept in when the
// directory holds files the manifest does not name, so that enabled files are
// never left somewhere the game does not look.
func (p Profile) SearchPaths() ([]string, error) {
	m, err := Load(p.ManifestPath())
	if err != nil {
		return nil, err
	}

	highest := uint32(FirstShard)
	for _, entry := range m.Mods {
		if entry.Enabled && len(entry.CurrentVpks) > 0 && entry.Shard <= MaxShards {
			highest = max(highest, entry.Shard)
		}
	}
	for shard := uint32(FirstShard + 1); shard <= MaxShards; shard++ {
		if p.hasEnabledVpks(shard) {
			highest = max(highest, shard)
		}
	}

	paths := make([]string, 0, highest)
	for shard := uint32(FirstShard); shard <= highest; shard++ {
		paths = append(paths, p.searchPath(shard))
	}
	return paths, nil
}

// searchPath is the directory one shard is loaded from, as the game names it in
// gameinfo.gi.
func (p Profile) searchPath(shard uint32) string {
	root := "citadel/addons"
	if shard > FirstShard {
		root = fmt.Sprintf("citadel/addons%d", shard)
	}
	if p.Folder == "" {
		return root
	}
	return root + "/" + p.Folder
}

// hasEnabledVpks reports whether a shard directory holds files the game would
// load.
func (p Profile) hasEnabledVpks(shard uint32) bool {
	entries, err := os.ReadDir(p.ShardDir(shard))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if pakNumber(entry.Name()) > 0 {
			return true
		}
	}
	return false
}

// Mod is one mod of a profile.
type Mod struct {
	// ID is the mod's key in the manifest.
	ID string
	// Enabled reports whether the game loads the mod.
	Enabled bool
	// Shard is the shard holding the mod's files while it is enabled.
	Shard uint32
	// Vpks names the mod's files as they are on disk now: pakNN_dir.vpk while
	// it is enabled, and prefixed names while it is not.
	Vpks []string
	// Pak is the number the mod's first enabled VPK carries, which is where it
	// falls in the load order. Zero when it is disabled, or when none of its
	// files is named like an enabled one.
	Pak int
	// Missing names the files the manifest claims but the disk does not have.
	Missing []string
}

// Read returns the mods of the profile, the enabled ones in the order the game
// loads them and the rest after them by id.
//
// Files the manifest names but the disk does not have are reported on the mod
// that names them rather than passed over: a mod missing half its files is
// exactly what the user needs to see, and the manifest alone would not show it.
func (p Profile) Read() ([]Mod, error) {
	m, err := Load(p.ManifestPath())
	if err != nil {
		return nil, err
	}

	mods := make([]Mod, 0, len(m.Mods))
	for id, entry := range m.Mods {
		mods = append(mods, p.mod(id, entry))
	}
	sort.Slice(mods, func(i, j int) bool {
		a, b := mods[i], mods[j]
		if a.Enabled != b.Enabled {
			return a.Enabled
		}
		if a.Enabled {
			if a.Shard != b.Shard {
				return a.Shard < b.Shard
			}
			if a.Pak != b.Pak {
				return a.Pak < b.Pak
			}
		}
		return a.ID < b.ID
	})
	return mods, nil
}

// mod describes one mod, looking in the directory its files should be in.
func (p Profile) mod(id string, entry Entry) Mod {
	m := Mod{ID: id, Enabled: entry.Enabled, Shard: entry.Shard}

	dir, names := p.Dir, entry.DisabledVpks
	if entry.Enabled {
		dir, names = p.ShardDir(entry.Shard), entry.CurrentVpks
	}
	for _, name := range names {
		// The number is taken from every name, not only the ones that are
		// there, so that a mod missing a file keeps its place in the order.
		if n := pakNumber(name); n > 0 && (m.Pak == 0 || n < m.Pak) {
			m.Pak = n
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			m.Missing = append(m.Missing, name)
			continue
		}
		m.Vpks = append(m.Vpks, name)
	}
	return m
}

// pakNumber reads the number out of an enabled VPK name, "pak07_dir.vpk" being
// 7. A name that is not one comes back as 0, which no real one is.
func pakNumber(name string) int {
	rest, ok := strings.CutPrefix(name, "pak")
	if !ok {
		return 0
	}
	digits, ok := strings.CutSuffix(rest, "_dir.vpk")
	if !ok || digits == "" {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return 0
	}
	return n
}
