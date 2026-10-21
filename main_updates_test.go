package main

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/addons"
	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/gb"
)

// installedAt is when the tests installed a mod: after the file they installed
// was published, and before the one an author published later.
var installedAt = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func remoteFile(id int, added time.Time) gb.File {
	return gb.File{ID: id, Name: fmt.Sprintf("file-%d.zip", id), Added: added}
}

// What a check makes of one mod's files depends on which file was installed: a
// newer file the user never chose is not an update, while a change to the one
// they have is.
func TestUpdateFor(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot gb.UpdateSnapshot
		info     config.ModInfo
		mod      addons.Mod
		want     bool
	}{
		{
			name: "the installed file is unchanged",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(7, installedAt.Add(-time.Hour)),
			}},
			info: config.ModInfo{Installed: installedAt, FileIDs: []int{7}},
		},
		{
			name: "the installed file was updated",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(7, installedAt.Add(time.Hour)),
			}},
			info: config.ModInfo{Installed: installedAt, FileIDs: []int{7}},
			want: true,
		},
		{
			name: "another file was updated",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(7, installedAt.Add(-time.Hour)),
				remoteFile(8, installedAt.Add(time.Hour)),
			}},
			info: config.ModInfo{Installed: installedAt, FileIDs: []int{7}},
		},
		{
			name: "the installed file was replaced by another",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(8, installedAt.Add(time.Hour)),
			}},
			info: config.ModInfo{Installed: installedAt, FileIDs: []int{7}},
			want: true,
		},
		{
			// A mod made of several downloads is as out of date when one of
			// them changes as when the only one does.
			name: "one of several installed files was updated",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(7, installedAt.Add(-time.Hour)),
				remoteFile(9, installedAt.Add(time.Hour)),
			}},
			info: config.ModInfo{Installed: installedAt, FileIDs: []int{7, 9}},
			want: true,
		},
		{
			name: "one of several installed files was taken down",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(7, installedAt.Add(-time.Hour)),
			}},
			info: config.ModInfo{Installed: installedAt, FileIDs: []int{7, 9}},
			want: true,
		},
		{
			name: "an unrecorded mod whose page changed",
			snapshot: gb.UpdateSnapshot{
				Updated: installedAt.Add(time.Hour),
				Files:   []gb.File{remoteFile(7, installedAt.Add(-time.Hour))},
			},
			mod:  addons.Mod{Modified: installedAt},
			want: true,
		},
		{
			name: "an unrecorded mod whose files did not",
			snapshot: gb.UpdateSnapshot{
				Updated: installedAt.Add(-time.Hour),
				Files:   []gb.File{remoteFile(7, installedAt.Add(-time.Hour))},
			},
			mod: addons.Mod{Modified: installedAt},
		},
		{
			name:     "a submission with no files",
			snapshot: gb.UpdateSnapshot{Updated: installedAt.Add(time.Hour)},
			info:     config.ModInfo{Installed: installedAt, FileIDs: []int{7}},
		},
		{
			name: "a mod with nothing on disk",
			snapshot: gb.UpdateSnapshot{Files: []gb.File{
				remoteFile(7, installedAt.Add(time.Hour)),
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			update, ok := updateFor(tt.snapshot, tt.info, tt.mod)
			if ok != tt.want {
				t.Fatalf("updateFor = %+v, %v, want available %v", update, ok, tt.want)
			}
		})
	}
}

// An update installs the files the mod came from, not whichever variant an
// author uploaded last, and every one of them: a mod made of a base file and
// an extra would lose the extra if only one were taken.
func TestUpdateFilesKeepsTheInstalledOnes(t *testing.T) {
	older := installedAt.Add(-time.Hour)
	newer := installedAt.Add(time.Hour)
	files := []gb.File{
		{ID: 7, Name: "installed.zip", Added: older},
		{ID: 8, Name: "other-variant.zip", Added: newer},
		{ID: 9, Name: "extra.zip", Added: older},
	}
	got := updateFiles(files, config.ModInfo{FileIDs: []int{7, 9}})
	if len(got) != 2 || got[0].ID != 7 || got[1].ID != 9 {
		t.Errorf("updateFiles = %+v, want both installed files", got)
	}

	// A file the author archived is an older version of one that replaced it,
	// and does not come back just because it was recorded once.
	gone := updateFiles([]gb.File{
		{ID: 10, Name: "replacement.zip", Added: newer},
		{ID: 7, Name: "installed.zip", Added: older, Archived: true},
	}, config.ModInfo{FileIDs: []int{7}})
	if len(gone) != 1 || gone[0].ID != 10 {
		t.Errorf("updateFiles = %+v, want the replacement when every recorded file is gone", gone)
	}
}

// A mod with no recorded files -- installed by the Tauri build, or by hand --
// is updated with the current files, and archived ones only when there is
// nothing else.
func TestUpdateFilesFallsBackToTheCurrentOnes(t *testing.T) {
	newer := installedAt.Add(time.Hour)
	current := []gb.File{
		{ID: 8, Name: "new.zip", Added: newer},
		{ID: 9, Name: "old.zip", Added: installedAt, Archived: true},
	}
	if got := updateFiles(current, config.ModInfo{}); len(got) != 1 || got[0].ID != 8 {
		t.Errorf("updateFiles = %+v, want the current file", got)
	}

	archived := []gb.File{{ID: 9, Name: "old.zip", Added: installedAt, Archived: true}}
	if got := updateFiles(archived, config.ModInfo{}); len(got) != 1 || got[0].ID != 9 {
		t.Errorf("updateFiles = %+v, want the archived file when it is all there is", got)
	}

	if got := updateFiles(nil, config.ModInfo{}); got != nil {
		t.Errorf("updateFiles of no files = %+v, want none", got)
	}
}

// The card says there is a new version, and the page says how many mods have
// one and offers the button that installs it.
func TestModsPageMarksAModWithAnUpdate(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "42", "skin.vpk")
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	a.library = map[string]config.ModInfo{
		"42": {Name: "Yamato skin", Installed: installedAt, FileIDs: []int{7}},
	}
	a.updates = map[string]gb.UpdateSnapshot{
		"42": {Files: []gb.File{remoteFile(7, installedAt.Add(time.Hour))}},
	}
	tt := ui.NewTester(a.view, 1280, 800)

	for _, want := range []string{"Yamato skin", "有新版本", "有 1 个模组可以更新。", "更新"} {
		if !tt.HasText(want) {
			t.Errorf("the mods page does not show %q: %q", want, tt.Texts())
		}
	}
}

// The button asks the catalogue, and what it says marks the card.
func TestCheckingUpdatesMarksANewVersion(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "42", "skin.vpk")
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	a.library = map[string]config.ModInfo{
		"42": {Name: "Yamato skin", Installed: installedAt, FileIDs: []int{7}},
	}

	files := fmt.Sprintf(`[{ "_idRow": 7, "_sFile": "skin.zip", "_nFilesize": 1024,
		"_tsDateAdded": %d, "_sDownloadUrl": "https://gamebanana.com/dl/7" }]`,
		installedAt.Add(time.Hour).Unix())
	srv := fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		if path != "/Core/Item/Data" {
			return http.StatusNotFound, ""
		}
		return http.StatusOK, fmt.Sprintf(
			`["https://gamebanana.com/mods/42", %d, %s]`,
			installedAt.Add(time.Hour).Unix(), files)
	})
	a.catalogue.LegacyBase = srv.URL + "/"

	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("检查更新"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the update check to finish", func() bool {
		state := a.updateState()
		return !state.checking && !state.checkedAt.IsZero()
	})
	tt.Frame()

	if got := a.updateState().checkErr; got != "" {
		t.Fatalf("the check failed: %s", got)
	}
	if !tt.HasText("有新版本") {
		t.Errorf("the mods page does not mark the update: %q", tt.Texts())
	}
	if !tt.HasText("有 1 个模组可以更新。") {
		t.Errorf("the mods page does not count the update: %q", tt.Texts())
	}
}

// A check that says nothing has changed leaves the list as it is.
func TestCheckingUpdatesSaysEverythingIsCurrent(t *testing.T) {
	a := newApp()
	install, profile := fakeGame(t)
	addMod(t, profile, "42", "skin.vpk")
	mods, err := profile.Read()
	if err != nil {
		t.Fatal(err)
	}
	a.install, a.mods = install, mods
	a.library = map[string]config.ModInfo{
		"42": {Name: "Yamato skin", Installed: installedAt, FileIDs: []int{7}},
	}

	files := fmt.Sprintf(`[{ "_idRow": 7, "_sFile": "skin.zip", "_nFilesize": 1024,
		"_tsDateAdded": %d, "_sDownloadUrl": "https://gamebanana.com/dl/7" }]`,
		installedAt.Add(-time.Hour).Unix())
	srv := fakeCatalogue(t, a, func(string, url.Values) (int, string) {
		return http.StatusOK, fmt.Sprintf(
			`["https://gamebanana.com/mods/42", %d, %s]`,
			installedAt.Add(-time.Hour).Unix(), files)
	})
	a.catalogue.LegacyBase = srv.URL + "/"

	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("检查更新"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the update check to finish", func() bool {
		state := a.updateState()
		return !state.checking && !state.checkedAt.IsZero()
	})
	tt.Frame()

	if !tt.HasText("所有模组都是最新的。") {
		t.Errorf("the mods page does not say the mods are current: %q", tt.Texts())
	}
}
