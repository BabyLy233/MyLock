package app

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/gb"
)

// fileserversReply is what Util/Fileservers answers with in these tests: two
// servers up, one of them faster, and one that is down.
const fileserversReply = `{"_aRecords": [
	{
		"_idRow": 1,
		"_sDomain": "filecache20",
		"_sState": "up",
		"_aStats": {"_a1hr": {"_fRate": 100, "_nRequests": 10}}
	},
	{
		"_idRow": 6,
		"_sDomain": "filecache31",
		"_sState": "up",
		"_aStats": {"_a1hr": {"_fRate": 900, "_nRequests": 90}}
	},
	{
		"_idRow": 7,
		"_sDomain": "filecache40",
		"_sState": "down"
	}
]}`

// file is one download as the catalogue gives it: the address the API gave,
// which redirects, and the file's own name.
func file(name string) gb.File {
	return gb.File{Name: name, URL: "https://gamebanana.com/dl/1"}
}

func TestDownloadURLKeepsTheAPIsAddressByDefault(t *testing.T) {
	a := newApp()
	a.fileserverPreference = gb.PreferenceDefault

	got := a.downloadURL(t.Context(), gb.Mod, file("pak.zip"))
	if want := "https://gamebanana.com/dl/1"; got != want {
		t.Errorf("downloadURL = %q, want the API's address %q", got, want)
	}
}

// The automatic route is the server that sent the most bytes over the last
// hour, and its address names the kind of submission and the file.
func TestDownloadURLUsesTheAutomaticServer(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(string, url.Values) (int, string) {
		return http.StatusOK, fileserversReply
	})
	a.fileserverPreference = gb.PreferenceAuto

	got := a.downloadURL(t.Context(), gb.Sound, file("a b.rar"))
	want := "https://filecache31.gamebanana.com/sounds/a%20b.rar"
	if got != want {
		t.Errorf("downloadURL = %q, want %q", got, want)
	}
}

// A server the user named is used even while it is down, and one that is no
// longer in the list falls back to the API's address rather than failing.
func TestDownloadURLFallsBackWhenTheNamedServerIsGone(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(string, url.Values) (int, string) {
		return http.StatusOK, fileserversReply
	})

	a.fileserverPreference = "7"
	if got, want := a.downloadURL(t.Context(), gb.Mod, file("pak.zip")),
		"https://filecache40.gamebanana.com/mods/pak.zip"; got != want {
		t.Errorf("downloadURL for a named server = %q, want %q", got, want)
	}

	a.fileserverPreference = "99"
	if got, want := a.downloadURL(t.Context(), gb.Mod, file("pak.zip")),
		"https://gamebanana.com/dl/1"; got != want {
		t.Errorf("downloadURL for a server that is gone = %q, want %q", got, want)
	}
}

// A list that cannot be read is not a failure for the download: the API's
// address works without it.
func TestDownloadURLFallsBackWhenTheListCannotBeRead(t *testing.T) {
	a := newApp()
	fakeCatalogue(t, a, func(string, url.Values) (int, string) {
		return http.StatusForbidden, `{"_sErrorCode": "FORBIDDEN"}`
	})
	a.fileserverPreference = gb.PreferenceAuto

	if got, want := a.downloadURL(t.Context(), gb.Mod, file("pak.zip")),
		"https://gamebanana.com/dl/1"; got != want {
		t.Errorf("downloadURL = %q, want the fallback %q", got, want)
	}
}

// The list is kept after it is read, so a second download does not ask for it
// again.
func TestDownloadURLKeepsTheListAfterTheFirstRead(t *testing.T) {
	a := newApp()
	asked := &askedPaths{}
	fakeCatalogue(t, a, func(path string, query url.Values) (int, string) {
		asked.add(path, query)
		return http.StatusOK, fileserversReply
	})
	a.fileserverPreference = gb.PreferenceAuto

	a.downloadURL(t.Context(), gb.Mod, file("one.zip"))
	a.downloadURL(t.Context(), gb.Mod, file("two.zip"))

	if got := len(asked.all()); got != 1 {
		t.Errorf("the list was read %d times for two downloads, want once", got)
	}
}

func TestChoosingADownloadRouteIsRemembered(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)

	a.setFileserverPreference(gb.PreferenceAuto)
	waitFor(t, "the route to be saved", func() bool {
		cfg, err := config.Load(path)
		return err == nil && cfg.FileserverPreference == gb.PreferenceAuto
	})
}

// A preference naming a server that is no longer listed is shown as the
// default, which is where downloads go, without changing what was saved.
func TestFileserverSelectionFallsBackWhenTheServerIsGone(t *testing.T) {
	listed := state{
		fileserverPreference: "6",
		fileservers:          []gb.Fileserver{{ID: "1"}, {ID: "6"}},
	}
	if got := fileserverSelection(listed); got != "6" {
		t.Errorf("fileserverSelection = %q for a listed server, want 6", got)
	}

	gone := state{
		fileserverPreference: "99",
		fileservers:          []gb.Fileserver{{ID: "1"}},
	}
	if got := fileserverSelection(gone); got != gb.PreferenceDefault {
		t.Errorf("fileserverSelection = %q for a server that is gone, want %q", got, gb.PreferenceDefault)
	}

	// Before the list is read there is nothing to say the server is gone.
	if got := fileserverSelection(state{fileserverPreference: "99"}); got != "99" {
		t.Errorf("fileserverSelection = %q before the list was read, want 99", got)
	}
}

func TestSettingsShowsTheDownloadRoute(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	for _, want := range []string{"下载路由", "默认：GameBanana 的标准下载地址", "自动：吞吐量最高的可用服务器"} {
		if !tt.HasText(want) {
			t.Errorf("the settings page does not show %q: %q", want, tt.Texts())
		}
	}
}

// Choosing a route in the settings page is what saves it, not only what the
// window shows.
// A server in the list is a route of its own: choosing one saves its id.
func TestChoosingAServerInTheSettingsSavesItsID(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	a.fileservers = []gb.Fileserver{{
		ID:     "6",
		Name:   "filecache31",
		Domain: "filecache31.gamebanana.com",
		State:  gb.FileserverUp,
		Rate:   900,
	}}
	a.fileserversTried = true

	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("在线 · 900 B/s") {
		t.Errorf("the server card does not show its state and rate: %q", tt.Texts())
	}
	if err := tt.Click("filecache31"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := a.state().fileserverPreference; got != "6" {
		t.Errorf("the preference is %q after choosing the server, want 6", got)
	}
	waitFor(t, "the route to be saved", func() bool {
		cfg, err := config.Load(path)
		return err == nil && cfg.FileserverPreference == "6"
	})
}

func TestChoosingADownloadRouteInTheSettingsSavesIt(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	tt := ui.NewTester(a.view, 1280, 800)

	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("自动：吞吐量最高的可用服务器"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := a.state().fileserverPreference; got != gb.PreferenceAuto {
		t.Errorf("the preference is %q after choosing 自动, want %q", got, gb.PreferenceAuto)
	}
	waitFor(t, "the route to be saved", func() bool {
		cfg, err := config.Load(path)
		return err == nil && cfg.FileserverPreference == gb.PreferenceAuto
	})
}
