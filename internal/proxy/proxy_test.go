package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestNormalizedDefaults(t *testing.T) {
	got := Config{}.Normalized()
	if got.Protocol != HTTP {
		t.Errorf("an empty protocol is %q, want %q", got.Protocol, HTTP)
	}
	if got.Port != defaultPort {
		t.Errorf("an empty port is %d, want %d", got.Port, defaultPort)
	}

	got = Config{Protocol: " SOCKS5 ", Host: " 127.0.0.1 ", Port: 1080}.Normalized()
	if got.Protocol != SOCKS5 || got.Host != "127.0.0.1" {
		t.Errorf("normalizing left %+v", got)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{name: "disabled is always valid", cfg: Config{}, ok: true},
		{name: "http", cfg: Config{Enabled: true, Protocol: HTTP, Host: "127.0.0.1", Port: 8080}, ok: true},
		{name: "https", cfg: Config{Enabled: true, Protocol: HTTPS, Host: "proxy.example", Port: 443}, ok: true},
		{name: "socks5", cfg: Config{Enabled: true, Protocol: SOCKS5, Host: "127.0.0.1", Port: 1080}, ok: true},
		{name: "ipv6", cfg: Config{Enabled: true, Protocol: SOCKS5, Host: "::1", Port: 1080}, ok: true},
		{name: "missing host", cfg: Config{Enabled: true, Protocol: HTTP, Port: 8080}},
		{name: "unknown protocol", cfg: Config{Enabled: true, Protocol: "ftp", Host: "127.0.0.1", Port: 8080}},
		{name: "port in host", cfg: Config{Enabled: true, Protocol: HTTP, Host: "127.0.0.1:8080", Port: 8080}},
		{name: "trailing colon", cfg: Config{Enabled: true, Protocol: HTTP, Host: "127.0.0.1:", Port: 8080}},
		{name: "host with scheme", cfg: Config{Enabled: true, Protocol: HTTP, Host: "http://127.0.0.1", Port: 8080}},
		{name: "port too low", cfg: Config{Enabled: true, Protocol: HTTP, Host: "127.0.0.1", Port: 0}},
		{name: "port too high", cfg: Config{Enabled: true, Protocol: HTTP, Host: "127.0.0.1", Port: 65536}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.cfg.Validate()
			if test.ok && err != nil {
				t.Errorf("Validate: %v", err)
			}
			if !test.ok && err == nil {
				t.Error("Validate accepted an unusable proxy")
			}
		})
	}
}

func TestAddressBracketsIPv6(t *testing.T) {
	cfg := Config{Enabled: true, Protocol: SOCKS5, Host: "[::1]", Port: 1080}.Normalized()
	got, err := cfg.address()
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "[::1]:1080" {
		t.Errorf("the address is %q, want [::1]:1080", got.Host)
	}
}

func TestFuncForReturnsTheConfiguredProxy(t *testing.T) {
	cfg := Config{
		Enabled:  true,
		Protocol: SOCKS5,
		Host:     "127.0.0.1",
		Port:     1080,
	}
	fn, err := cfg.funcFor()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://gamebanana.com/apiv11/Util/Fileservers", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Scheme != SOCKS5 || got.Host != "127.0.0.1:1080" {
		t.Fatalf("the request goes through %v, want socks5://127.0.0.1:1080", got)
	}
	if got.User != nil {
		t.Errorf("a proxy without credentials has %v", got.User)
	}
}

func TestFuncForCarriesCredentials(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		Protocol:    HTTP,
		Host:        "proxy.example",
		Port:        3128,
		AuthEnabled: true,
		Username:    "user",
		Password:    "pass",
	}
	fn, err := cfg.funcFor()
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn(httptest.NewRequest(http.MethodGet, "https://gamebanana.com/", nil))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := got.User.Password()
	if got.User.Username() != "user" || password != "pass" {
		t.Fatalf("credentials are %v, want user/pass", got.User)
	}
}

func TestNoProxyGoesAroundTheProxy(t *testing.T) {
	cfg := Config{
		Enabled:  true,
		Protocol: HTTP,
		Host:     "127.0.0.1",
		Port:     8080,
		NoProxy:  ".example, 10.0.0.0/8",
	}
	fn, err := cfg.funcFor()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://api.example/", "http://10.1.2.3/"} {
		got, err := fn(httptest.NewRequest(http.MethodGet, address, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("%s goes through %v, want a direct connection", address, got)
		}
	}
	// A host not on the list still goes through the proxy.
	got, err := fn(httptest.NewRequest(http.MethodGet, "https://gamebanana.com/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("gamebanana.com went around the proxy")
	}
}

// A request is carried by the proxy the source names, and the proxy sees the
// address it was asked for.
func TestSourceCarriesRequestsThroughTheProxy(t *testing.T) {
	asked := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(address.Port())
	if err != nil {
		t.Fatal(err)
	}

	var source Source
	if err := source.Set(Config{
		Enabled:  true,
		Protocol: HTTP,
		Host:     address.Hostname(),
		Port:     port,
	}); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &http.Transport{Proxy: source.Proxy}}
	resp, err := client.Get("http://gamebanana.example/mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := <-asked; got != "http://gamebanana.example/mod.zip" {
		t.Errorf("the proxy was asked for %q", got)
	}
}

// A setting that cannot be used is refused, and the source keeps what it had.
func TestSourceKeepsTheLastGoodSetting(t *testing.T) {
	var source Source
	good := Config{Enabled: true, Protocol: HTTP, Host: "127.0.0.1", Port: 8080}
	if err := source.Set(good); err != nil {
		t.Fatal(err)
	}
	if err := source.Set(Config{Enabled: true, Protocol: "ftp", Host: "127.0.0.1", Port: 8080}); err == nil {
		t.Fatal("Set accepted an unknown protocol")
	}

	got, err := source.Proxy(httptest.NewRequest(http.MethodGet, "https://gamebanana.com/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "127.0.0.1:8080" {
		t.Fatalf("the source forgot the last good setting: %v", got)
	}
}
