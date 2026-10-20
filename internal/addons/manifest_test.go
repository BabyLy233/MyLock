package addons

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v1Manifest is a manifest as the Tauri app wrote it before mods were sharded:
// no version on the entries, no shard, and no compatibility keys. It is the
// fixture from that app's own test suite, byte for byte.
const v1Manifest = `{
  "version": 1,
  "mods": {
    "123456": {
      "enabled": true,
      "order": 0,
      "currentVpks": [
        "pak01_dir.vpk",
        "pak02_dir.vpk"
      ],
      "disabledVpks": [],
      "originalVpkNames": [
        "cool_mod.vpk",
        "cool_mod_2.vpk"
      ]
    },
    "local-abc": {
      "enabled": false,
      "order": 3,
      "currentVpks": [],
      "disabledVpks": [
        "local-abc_skin.vpk"
      ],
      "originalVpkNames": [
        "skin.vpk"
      ]
    }
  }
}`

// upgradedManifest is what that same file becomes once this app saves it: the
// current version, every key of every entry present, and the entries in the
// order the Tauri app writes them, sorted by mod id.
const upgradedManifest = `{
  "version": 3,
  "mods": {
    "123456": {
      "enabled": true,
      "compatibilityEnabled": false,
      "compatibilityOptedOut": false,
      "order": 0,
      "shard": 1,
      "currentVpks": [
        "pak01_dir.vpk",
        "pak02_dir.vpk"
      ],
      "disabledVpks": [],
      "originalVpkNames": [
        "cool_mod.vpk",
        "cool_mod_2.vpk"
      ]
    },
    "local-abc": {
      "enabled": false,
      "compatibilityEnabled": false,
      "compatibilityOptedOut": false,
      "order": 3,
      "shard": 1,
      "currentVpks": [],
      "disabledVpks": [
        "local-abc_skin.vpk"
      ],
      "originalVpkNames": [
        "skin.vpk"
      ]
    }
  }
}`

// The bytes of the file are the contract with the other app, so they are
// compared exactly rather than by what they parse to.
func TestMarshalUpgradesAV1Manifest(t *testing.T) {
	m, err := Parse([]byte(v1Manifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != Version {
		t.Errorf("Version = %d after loading a v1 file, want %d", m.Version, Version)
	}

	got, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != upgradedManifest {
		t.Errorf("the saved manifest differs from what the Tauri app writes:\n--- got ---\n%s\n--- want ---\n%s", got, upgradedManifest)
	}
}

// An entry that predates sharding is in the first shard, and one that predates
// the compatibility keys is not opted out of anything.
func TestParseFillsInWhatAV1FileLeavesOut(t *testing.T) {
	m, err := Parse([]byte(v1Manifest))
	if err != nil {
		t.Fatal(err)
	}

	enabled := m.Mods["123456"]
	if !enabled.Enabled {
		t.Error("123456 came back disabled")
	}
	if enabled.Shard != FirstShard {
		t.Errorf("123456 is in shard %d, want %d", enabled.Shard, FirstShard)
	}
	if enabled.CompatibilityEnabled || enabled.CompatibilityOptedOut {
		t.Errorf("123456 came back with compatibility set: %+v", enabled)
	}
	if enabled.Order == nil || *enabled.Order != 0 {
		t.Errorf("123456 has order %v, want 0", enabled.Order)
	}

	disabled := m.Mods["local-abc"]
	if disabled.Enabled {
		t.Error("local-abc came back enabled")
	}
	if len(disabled.DisabledVpks) != 1 || disabled.DisabledVpks[0] != "local-abc_skin.vpk" {
		t.Errorf("local-abc holds %v", disabled.DisabledVpks)
	}
}

// A file this app wrote is read back unchanged, which is what makes it safe to
// hand the profile back and forth between the two apps.
func TestMarshalRoundTrips(t *testing.T) {
	first, err := Parse([]byte(v1Manifest))
	if err != nil {
		t.Fatal(err)
	}
	data, err := first.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := second.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Errorf("a second save differs:\n--- first ---\n%s\n--- second ---\n%s", data, again)
	}
}

// The remaining bytes are what the Tauri app writes for an empty manifest and
// for one mod with nothing set. Both were taken from that app's own
// serializer rather than composed by hand, because every key has to be there
// for it to read the file back.
const (
	emptyManifest = `{
  "version": 3,
  "mods": {}
}`

	oneEmptyEntryManifest = `{
  "version": 3,
  "mods": {
    "snd-42": {
      "enabled": false,
      "compatibilityEnabled": false,
      "compatibilityOptedOut": false,
      "order": null,
      "shard": 1,
      "currentVpks": [],
      "disabledVpks": [],
      "originalVpkNames": []
    }
  }
}`
)

func TestMarshalOfAnEmptyManifest(t *testing.T) {
	data, err := New().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != emptyManifest {
		t.Errorf("the empty manifest differs from what the Tauri app writes:\n--- got ---\n%s\n--- want ---\n%s", data, emptyManifest)
	}
}

func TestMarshalOfAnEmptyEntry(t *testing.T) {
	m := New()
	m.Mods["snd-42"] = Entry{Shard: FirstShard}

	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != oneEmptyEntryManifest {
		t.Errorf("an entry with nothing set differs from what the Tauri app writes:\n--- got ---\n%s\n--- want ---\n%s", data, oneEmptyEntryManifest)
	}
	if strings.HasSuffix(string(data), "\n") {
		t.Error("the manifest ends in a newline, which the Tauri app does not write")
	}
}

// A manifest at a version this app does not know is refused rather than read
// as though it were an older one.
func TestParseRejectsAFutureVersion(t *testing.T) {
	if _, err := Parse([]byte(`{"version": 999, "mods": {}}`)); err == nil {
		t.Error("a manifest from a future version parsed without an error")
	}
}

// Zero is not a shard either app writes, so a file holding one is damaged.
func TestParseRejectsAShardOutOfRange(t *testing.T) {
	for _, shard := range []string{"0", "11"} {
		body := `{"version": 3, "mods": {"m": {"shard": ` + shard + `}}}`
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("a manifest in shard %s parsed without an error", shard)
		}
	}
}

func TestParseRejectsBrokenJSON(t *testing.T) {
	if _, err := Parse([]byte("{truncated")); err == nil {
		t.Error("a truncated manifest parsed without an error")
	}
}

// A profile with nothing installed has no manifest, which is not a failure.
func TestLoadWithoutAManifest(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), ManifestName))
	if err != nil {
		t.Fatalf("Load of a missing manifest = %v, want nil", err)
	}
	if m.Version != Version || len(m.Mods) != 0 {
		t.Errorf("Load of a missing manifest = %+v, want an empty one at version %d", m, Version)
	}
}

// A save that a crash interrupted leaves the temporary behind. Reading it is
// what the other app does, so the mods are not lost.
func TestLoadFallsBackToTheTemporary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tempName), []byte(upgradedManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Mods) != 2 {
		t.Errorf("Load recovered %d mods, want the 2 in the temporary", len(m.Mods))
	}
}

// The saved file is the manifest and nothing else: the temporary is gone.
func TestSaveLeavesOnlyTheManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestName)

	m, err := Parse([]byte(v1Manifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ManifestName {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name()
		}
		t.Errorf("%s holds %v, want only %s", dir, names, ManifestName)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != upgradedManifest {
		t.Errorf("the saved file is not what the Tauri app writes:\n%s", data)
	}
}

// Save creates the profile directory, so a profile can be written before
// anything else is in it.
func TestSaveCreatesTheProfileDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "citadel", "addons", ManifestName)
	if err := New().Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the manifest is not there after Save: %v", err)
	}
}
