package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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

// An update downloads the files the user chose, and not every variant an
// author publishes. They are kept in the page's order, so one selection is
// always named the same way.
func TestUpdateFilesTakesTheChosenOnes(t *testing.T) {
	older := installedAt.Add(-time.Hour)
	newer := installedAt.Add(time.Hour)
	files := []gb.File{
		{ID: 7, Name: "base.zip", Added: older},
		{ID: 8, Name: "variant.zip", Added: newer},
		{ID: 9, Name: "extra.zip", Added: older},
	}
	got := updateFiles(files, []int{7, 9})
	if len(got) != 2 || got[0].ID != 7 || got[1].ID != 9 {
		t.Errorf("updateFiles = %+v, want the chosen files in page order", got)
	}

	// A variant the user did not choose is left out even though it is newer.
	got = updateFiles(files, []int{7})
	if len(got) != 1 || got[0].ID != 7 {
		t.Errorf("updateFiles = %+v, want only the chosen file", got)
	}

	// A file the author archived is an older version of one that replaced it,
	// and does not come back just because it was chosen.
	gone := updateFiles([]gb.File{
		{ID: 10, Name: "replacement.zip", Added: newer},
		{ID: 7, Name: "installed.zip", Added: older, Archived: true},
	}, []int{7})
	if len(gone) != 0 {
		t.Errorf("updateFiles = %+v, want an archived file left out", gone)
	}

	// An author who archived everything leaves only those files to choose, and
	// the update takes one when it is all there is.
	archived := []gb.File{{ID: 9, Name: "old.zip", Added: older, Archived: true}}
	if got := updateFiles(archived, []int{9}); len(got) != 1 || got[0].ID != 9 {
		t.Errorf("updateFiles = %+v, want the archived file when it is all there is", got)
	}

	if got := updateFiles(nil, []int{7}); got != nil {
		t.Errorf("updateFiles of no files = %+v, want none", got)
	}
}

// The update dialog starts with the files the mod was installed from, and with
// the newest one when the author replaced them all: the user chooses the rest.
func TestUpdateDefaultsToTheInstalledFiles(t *testing.T) {
	older := installedAt.Add(-time.Hour)
	newer := installedAt.Add(time.Hour)
	files := []gb.File{
		{ID: 7, Name: "base.zip", Added: older},
		{ID: 8, Name: "variant.zip", Added: newer},
		{ID: 9, Name: "extra.zip", Added: older},
	}

	picked := defaultPicked(files, []int{7, 9})
	if len(picked) != 2 || !picked[7] || !picked[9] {
		t.Errorf("the dialog starts with %v, want the installed files", picked)
	}

	// None of the recorded files is offered any more; the newest is what the
	// page would start a fresh install with.
	picked = defaultPicked(files, []int{1, 2})
	if len(picked) != 1 || !picked[8] {
		t.Errorf("the dialog starts with %v, want the newest file", picked)
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

// updateProfileReply is a mod of several files, as its page reads when an
// update asks again. The addresses are not the catalogue's, so a download fails
// as soon as it starts; a test of the choice does not need one to finish.
const updateProfileReply = `{
	"_idRow": 42,
	"_sName": "Yamato skin",
	"_sProfileUrl": "https://gamebanana.com/mods/42",
	"_aFiles": [
		{"_idRow": 7, "_sFile": "base.zip", "_nFilesize": 1024,
		 "_sDownloadUrl": "https://example.invalid/dl/7"},
		{"_idRow": 8, "_sFile": "variant.zip", "_nFilesize": 2048,
		 "_sDownloadUrl": "https://example.invalid/dl/8"},
		{"_idRow": 9, "_sFile": "extra.zip", "_nFilesize": 4096,
		 "_sDownloadUrl": "https://example.invalid/dl/9"}
	]
}`

// Updating a mod of several files asks which ones to fetch, starting with the
// ones it was installed from, and the update fetches only the chosen ones.
func TestUpdatingAModOfSeveralFilesAsksWhichOnes(t *testing.T) {
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
	older := installedAt.Add(-time.Hour)
	newer := installedAt.Add(time.Hour)
	a.updates = map[string]gb.UpdateSnapshot{
		"42": {Files: []gb.File{
			{ID: 7, Name: "base.zip", Added: newer},
			{ID: 8, Name: "variant.zip", Added: newer.Add(time.Hour)},
			{ID: 9, Name: "extra.zip", Added: older},
		}},
	}
	fakeCatalogue(t, a, func(path string, _ url.Values) (int, string) {
		if strings.HasSuffix(path, "/ProfilePage") {
			return http.StatusOK, updateProfileReply
		}
		return http.StatusNotFound, ""
	})

	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("更新 42"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !tt.HasText("选择更新文件") {
		t.Fatalf("clicking 更新 did not ask which files: %q", tt.Texts())
	}
	state := a.updateFilesState()
	if !state.picked[7] || state.picked[8] || state.picked[9] {
		t.Fatalf("the dialog starts with %v, want only the installed file", state.picked)
	}
	if a.transfers.activeFor("42") {
		t.Fatal("the update started before the files were chosen")
	}

	// Choose the variant the user did not have, and let go of the installed one.
	if err := tt.Click("variant.zip"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("base.zip"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("更新选中的文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the update to fetch the chosen file", func() bool {
		view, ok := a.transfers.latestFor("42")
		return ok && len(view.Files) == 1
	})

	view, _ := a.transfers.latestFor("42")
	if view.Files[0].Name != "variant.zip" {
		t.Errorf("the update is fetching %+v, want only variant.zip", view.Files)
	}
	if got := a.updateFilesState().ask; got != "" {
		t.Errorf("the dialog is still open after 更新选中的文件: %q", got)
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

// The page checks once by itself; after that only the button asks again. A
// catalogue is not asked about every mod on every frame the page draws.
func TestUpdatesAreCheckedOnceUntilTheButtonAsks(t *testing.T) {
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

	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		fmt.Fprint(w, `["https://gamebanana.com/mods/42", 0, []]`)
	}))
	t.Cleanup(srv.Close)
	a.catalogue.LegacyBase = srv.URL + "/"

	a.checkUpdates(false)
	waitFor(t, "the page's own check", func() bool { return !a.updateState().checking })
	if got := asked.Load(); got != 1 {
		t.Fatalf("the page asked %d times, want once", got)
	}

	// The page's check has been made, so its next draws do not ask again.
	a.checkUpdates(false)
	if got := asked.Load(); got != 1 {
		t.Errorf("the page asked again by itself: %d requests", got)
	}

	// The button still asks whenever the user presses it.
	a.checkUpdates(true)
	waitFor(t, "the button's check", func() bool {
		return !a.updateState().checking && asked.Load() == 2
	})
	if got := asked.Load(); got != 2 {
		t.Errorf("the button did not ask again: %d requests", got)
	}
}
