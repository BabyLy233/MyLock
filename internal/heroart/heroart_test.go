package heroart

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The hero list is read into names and small portrait addresses, preferring the
// WebP artwork and falling back to the PNG one.
func TestHeroesReadsNamesAndPortraits(t *testing.T) {
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		fmt.Fprint(w, `[
			{"name": "Infernus", "images": {
				"icon_image_small": "https://example.com/inferno_sm.png",
				"icon_image_small_webp": "https://example.com/inferno_sm.webp"
			}},
			{"name": "Seven", "images": {
				"icon_image_small": "https://example.com/gigawatt_sm.png"
			}},
			{"name": "No Art", "images": {}},
			{"images": {"icon_image_small": "https://example.com/nobody.png"}}
		]`)
	}))
	defer srv.Close()

	c := New()
	c.Base = srv.URL
	arts, err := c.Heroes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/assets/heroes" || query != "only_active=true" {
		t.Errorf("the client asked for %q?%q, want the active hero list", path, query)
	}
	want := []Art{
		{Name: "Infernus", Icon: "https://example.com/inferno_sm.webp"},
		{Name: "Seven", Icon: "https://example.com/gigawatt_sm.png"},
		{Name: "No Art"},
	}
	if len(arts) != len(want) {
		t.Fatalf("the read left %d heroes, want %d: %+v", len(arts), len(want), arts)
	}
	for i := range want {
		if arts[i] != want[i] {
			t.Errorf("hero %d is %+v, want %+v", i, arts[i], want[i])
		}
	}
}

// A reply that is not the list is an error rather than an empty roster, so that
// a page can keep the portraits it already had.
func TestHeroesReportsAFailedReply(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "status", status: http.StatusBadGateway, body: `[]`},
		{name: "body", status: http.StatusOK, body: `not json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer srv.Close()

			c := New()
			c.Base = srv.URL
			if _, err := c.Heroes(t.Context()); err == nil {
				t.Error("a failed reply was read as a hero list")
			}
		})
	}
}
