// Package heroart reads the portraits of Deadlock's heroes from the game's
// asset API, which is where the Tauri app takes them from.
//
// The API is public: no key, no account, no sign-in. One reply lists every
// active hero with the addresses of its artwork, so this client asks once and
// hands the addresses to the app, which fetches and caches the pictures through
// its ordinary image path.
package heroart

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BabyLy233/mylock/internal/about"
	"github.com/BabyLy233/mylock/internal/proxy"
)

// API is the root of the asset API.
const API = "https://api.deadlock-api.com"

const (
	// maxBodyBytes caps a reply. The hero list carries per-hero statistics the
	// app does not read, which makes it about a megabyte and a half; the cap is
	// what keeps a reply that never ends from filling memory.
	maxBodyBytes = 8 << 20

	// heroesPath is the list of heroes in play, rather than every hero the
	// files carry, some of which are not released.
	heroesPath = "/v1/assets/heroes?only_active=true"
)

// Art is one hero's name and the addresses of its artwork, in the spelling the
// asset API uses.
type Art struct {
	Name string
	// Icon is the address of the small portrait, empty when the API has none.
	Icon string
}

// Client reads the hero list. It is safe to use from several goroutines; make
// one with New.
type Client struct {
	// Base is the root of the API. It is the live one unless it is pointed
	// somewhere else, which is what a test does to read from a server of its
	// own.
	Base string
	http *http.Client
	// proxy is the proxy setting the transport reads on every request, so that
	// saving one takes effect without rebuilding the client.
	proxy *proxy.Source
}

// New returns a client reading the live asset API.
func New() *Client {
	p := &proxy.Source{}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = p.Proxy
	return &Client{
		Base:  API,
		http:  &http.Client{Timeout: 30 * time.Second, Transport: transport},
		proxy: p,
	}
}

// SetProxy makes the client use cfg for the requests it makes from now on. A
// setting that cannot be used is refused and leaves the previous one in place.
func (c *Client) SetProxy(cfg proxy.Config) error {
	return c.proxy.Set(cfg)
}

// Heroes reads every active hero's name and small portrait address.
func (c *Client) Heroes(ctx context.Context) ([]Art, error) {
	base := c.Base
	if base == "" {
		base = API
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(base, "/")+heroesPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", about.UserAgent())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("heroart: heroes: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBodyBytes {
		return nil, fmt.Errorf("heroart: heroes: reply over %d bytes", maxBodyBytes)
	}

	// Only the three fields the page needs; the rest of a hero's entry -- its
	// lore, tags and popular items -- is several times their size and is not
	// read.
	var payload []struct {
		Name   string `json:"name"`
		Images struct {
			SmallWebP string `json:"icon_image_small_webp"`
			Small     string `json:"icon_image_small"`
		} `json:"images"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("heroart: heroes: %w", err)
	}

	arts := make([]Art, 0, len(payload))
	for _, hero := range payload {
		if hero.Name == "" {
			continue
		}
		icon := hero.Images.SmallWebP
		if icon == "" {
			icon = hero.Images.Small
		}
		arts = append(arts, Art{Name: hero.Name, Icon: icon})
	}
	return arts, nil
}
