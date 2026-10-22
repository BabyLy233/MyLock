package download

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A probe asks for headers only, and any answer means the server is there:
// what is being measured is the round trip, not the file.
func TestProbeTimesAServerThatAnswers(t *testing.T) {
	var method string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	took, ok := newClient(t, srv).Probe(t.Context(), srv.URL+"/mods/pak.zip")
	if !ok {
		t.Fatal("Probe reported an answering server as unreachable")
	}
	if method != http.MethodHead {
		t.Errorf("the server was asked with %s, want HEAD", method)
	}
	if took >= probeTimeout {
		t.Errorf("the probe took %v, which is the whole timeout", took)
	}
}

func TestProbeReportsAnUnreachableServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := newClient(t, srv)
	srv.Close()

	if _, ok := client.Probe(t.Context(), srv.URL+"/mods/pak.zip"); ok {
		t.Error("Probe reported a closed server as reachable")
	}
}

// A probe is an address a download may use, and is checked the same way.
func TestProbeRefusesAnAddressADownloadMayNotUse(t *testing.T) {
	client := &Client{domains: []string{"gamebanana.com"}}
	for _, address := range []string{
		"https://example.com/mod.zip",
		"http://gamebanana.com/mod.zip",
	} {
		if _, ok := client.Probe(t.Context(), address); ok {
			t.Errorf("Probe accepted %s", address)
		}
	}
}
