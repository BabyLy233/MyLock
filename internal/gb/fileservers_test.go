package gb

import (
	"net/http"
	"strings"
	"testing"
)

// fileserversReply is what Util/Fileservers answers with: servers in every
// state, one record with a domain that is not a plain hostname, and one with
// stats the API did not send.
const fileserversReply = `{
	"_aMetadata": {"_nRecordCount": 4, "_bIsComplete": true},
	"_aRecords": [
		{
			"_idRow": 1,
			"_sDomain": "filecache20",
			"_sState": "up",
			"_aStats": {"_a1hr": {"_fRate": 2722355, "_nRequests": 4155}}
		},
		{
			"_idRow": 6,
			"_sDomain": "filecache31",
			"_sState": "up",
			"_aStats": {"_a1hr": {"_fRate": 3685206, "_nRequests": 5374}}
		},
		{
			"_idRow": 7,
			"_sDomain": "filecache40",
			"_sState": "terminated"
		},
		{
			"_idRow": 8,
			"_sDomain": "filecache41.example.com",
			"_sState": "up"
		}
	]
}`

func TestFileserversReadsTheList(t *testing.T) {
	c, srv := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, fileserversReply
	})

	servers, err := c.Fileservers(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 {
		t.Fatalf("Fileservers returned %d servers, want 3: %+v", len(servers), servers)
	}
	if got := srv.paths[0]; !strings.HasSuffix(got, "/Util/Fileservers") {
		t.Errorf("the list was read from %q", got)
	}
	if got := srv.requests[0].Get("_nPage"); got != "1" {
		t.Errorf("_nPage = %q, want 1", got)
	}

	first := servers[0]
	if first.ID != "1" || first.Name != "filecache20" ||
		first.Domain != "filecache20.gamebanana.com" || first.State != FileserverUp {
		t.Errorf("the first server reads as %+v", first)
	}
	if first.Rate != 2722355 || first.Requests != 4155 {
		t.Errorf("the first server's stats read as %+v", first)
	}

	terminated := servers[2]
	if terminated.ID != "7" || terminated.State != FileserverTerminated {
		t.Errorf("the terminated server reads as %+v", terminated)
	}

	// A record whose subdomain is not a plain hostname is dropped rather than
	// turned into an address a download could be pointed at.
	for _, server := range servers {
		if server.ID == "8" {
			t.Errorf("a server with a foreign domain was kept: %+v", server)
		}
	}
}

// The list stands for six hours, so the several frames of the settings page do
// not each ask for it; the refresh button asks again.
func TestFileserversAreCachedUntilForced(t *testing.T) {
	c, srv := newServer(t, func(int, http.ResponseWriter) (int, string) {
		return http.StatusOK, fileserversReply
	})

	if _, err := c.Fileservers(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fileservers(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.requests); got != 1 {
		t.Fatalf("the API was asked %d times for a cached list, want once", got)
	}

	if _, err := c.Fileservers(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.requests); got != 2 {
		t.Fatalf("the API was asked %d times after a forced read, want twice", got)
	}
}

func TestFileserverURLJoinsTheKindAndTheFileName(t *testing.T) {
	server := Fileserver{Domain: "filecache20.gamebanana.com"}
	tests := []struct {
		kind Kind
		name string
		want string
	}{
		{Mod, "pak01_dir.vpk", "https://filecache20.gamebanana.com/mods/pak01_dir.vpk"},
		{Sound, "a b#1.mp3", "https://filecache20.gamebanana.com/sounds/a%20b%231.mp3"},
		{Wip, "中文.zip", "https://filecache20.gamebanana.com/wips/%E4%B8%AD%E6%96%87.zip"},
		{Kind("Other"), "pak.zip", ""},
		{Mod, "", ""},
	}
	for _, test := range tests {
		if got := server.URL(test.kind, test.name); got != test.want {
			t.Errorf("URL(%q, %q) = %q, want %q", test.kind, test.name, got, test.want)
		}
	}
	if got := (Fileserver{}).URL(Mod, "pak.zip"); got != "" {
		t.Errorf("a server without a domain made %q", got)
	}
}

func TestPickFileserver(t *testing.T) {
	servers := []Fileserver{
		{ID: "1", State: FileserverUp, Rate: 100},
		{ID: "2", State: FileserverUp, Rate: 900},
		{ID: "3", State: FileserverDown, Rate: 9999},
		{ID: "4", State: FileserverTerminated, Rate: 9999},
	}

	tests := []struct {
		preference string
		want       string
		ok         bool
	}{
		{PreferenceDefault, "", false},
		{"", "", false},
		{PreferenceAuto, "2", true},
		{"1", "1", true},
		// A named server is used while it exists, even when it is down: the
		// user chose it rather than asking for the fastest one.
		{"3", "3", true},
		{"4", "", false},
		{"99", "", false},
	}
	for _, test := range tests {
		server, ok := PickFileserver(servers, test.preference)
		if ok != test.ok || server.ID != test.want {
			t.Errorf("PickFileserver(%q) = (%q, %v), want (%q, %v)",
				test.preference, server.ID, ok, test.want, test.ok)
		}
	}

	// auto has nothing to choose when none of them is up.
	down := []Fileserver{{ID: "3", State: FileserverDown}, {ID: "4", State: FileserverTerminated}}
	if server, ok := PickFileserver(down, PreferenceAuto); ok {
		t.Errorf("auto chose %+v with no server up", server)
	}
}
