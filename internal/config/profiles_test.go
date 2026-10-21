package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestProfilesSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), profilesFileName)
	made := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	want := Profiles{Profiles: []Profile{{
		Folder:      "profile_1_abcd_测试",
		Name:        "测试服",
		Description: "用来试模组",
		Created:     made,
		LastUsed:    made.Add(time.Hour),
	}}}

	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Profiles) != 1 {
		t.Fatalf("LoadProfiles = %+v, want one profile", got)
	}
	p := got.Profiles[0]
	if p.Folder != want.Profiles[0].Folder || p.Name != want.Profiles[0].Name ||
		p.Description != want.Profiles[0].Description {
		t.Errorf("LoadProfiles = %+v, want %+v", p, want.Profiles[0])
	}
	if !p.Created.Equal(made) || !p.LastUsed.Equal(made.Add(time.Hour)) {
		t.Errorf("LoadProfiles times = %v, %v, want %v, %v",
			p.Created, p.LastUsed, made, made.Add(time.Hour))
	}
}

// A first run has no profiles file, which is not a failure.
func TestLoadProfilesWithoutAFile(t *testing.T) {
	got, err := LoadProfiles(filepath.Join(t.TempDir(), profilesFileName))
	if err != nil {
		t.Fatalf("LoadProfiles of a missing file = %v, want nil", err)
	}
	if len(got.Profiles) != 0 {
		t.Errorf("LoadProfiles of a missing file = %+v, want none", got)
	}
}

// The profiles file sits beside the settings file, which is how the app finds
// it without another setting to keep in step.
func TestProfilesPathIsBesideTheSettings(t *testing.T) {
	settings := filepath.Join(t.TempDir(), appDir, fileName)
	want := filepath.Join(filepath.Dir(settings), profilesFileName)
	if got := ProfilesPath(settings); got != want {
		t.Errorf("ProfilesPath(%q) = %q, want %q", settings, got, want)
	}
	if got := ProfilesPath(""); got != "" {
		t.Errorf("ProfilesPath(\"\") = %q, want empty", got)
	}
}
