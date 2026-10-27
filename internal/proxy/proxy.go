// Package proxy routes the app's network requests through a proxy server the
// user configured.
//
// A proxy is per-setting and applies to every request the app makes: the
// catalogue it browses, the pictures it shows and the archives it downloads.
// HTTP, HTTPS and SOCKS5 proxies are supported, with optional credentials and
// a list of hosts that go around the proxy. With no proxy configured the
// environment's own settings are followed, which is what a machine that sets
// HTTP_PROXY expects.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/BabyLy233/mylock/internal/about"
)

// Protocols are the schemes a proxy address may use. They are spelled the way
// the address is, so that a saved value can be read back as it is.
const (
	HTTP   = "http"
	HTTPS  = "https"
	SOCKS5 = "socks5"
)

const (
	// defaultPort is the port shown for a proxy whose settings do not name
	// one. It is HTTP's own, which is what most local proxies listen on.
	defaultPort = 8080

	// testTimeout bounds the whole test request, and dialTimeout the
	// connection through the proxy. A proxy that takes longer than this is one
	// the user needs to know about now, rather than when a download hangs.
	testTimeout = 20 * time.Second
	dialTimeout = 10 * time.Second

	// testAddress is what the test button asks for. Any answer from GameBanana
	// proves the proxy carried the request, which is what is being tested.
	testAddress = "https://gamebanana.com/"
)

// Config is the proxy the settings page edits. It is saved in config.json as
// it is, so the JSON names are part of the format.
type Config struct {
	// Enabled turns the proxy on. Off means the environment's proxy settings
	// are followed, which is what a machine without a proxy does not notice.
	Enabled bool `json:"enabled,omitzero"`
	// Protocol is HTTP, HTTPS or SOCKS5. Empty is HTTP.
	Protocol string `json:"protocol,omitempty"`
	// Host is the proxy's host name or address, without a scheme or port.
	Host string `json:"host,omitempty"`
	// Port is the proxy's port. Zero is defaultPort.
	Port int `json:"port,omitzero"`
	// AuthEnabled says the proxy wants a user name and password. The password
	// is only used when AuthEnabled is set and a user name is given, the way
	// the Tauri version did it.
	AuthEnabled bool   `json:"authEnabled,omitzero"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	// NoProxy is the comma-separated list of hosts that are reached directly.
	NoProxy string `json:"noProxy,omitempty"`
}

// Normalized returns the config with the defaults a saved file may leave out:
// HTTP for a missing protocol, defaultPort for a missing port, and a host
// without stray spaces. It does not validate.
func (c Config) Normalized() Config {
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))
	c.Host = strings.TrimSpace(c.Host)
	if c.Protocol == "" {
		c.Protocol = HTTP
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	return c
}

// Validate reports what would keep the proxy from working. A disabled proxy is
// always valid: its fields are kept for when it is turned back on.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(c.Protocol)) {
	case "", HTTP, HTTPS, SOCKS5:
	default:
		return fmt.Errorf("不支持的代理协议：%s", c.Protocol)
	}
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return errors.New("启用了代理，但没有填代理服务器地址")
	}
	if strings.ContainsAny(host, "/@") {
		return errors.New("代理服务器地址只填主机名或 IP，不要带协议和路径")
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		// A colon is either an IPv6 address or a port: the port belongs in its
		// own field, and a pasted host:port would otherwise be bracketed as an
		// address and never connect.
		if !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") {
			return errors.New("代理端口请填在端口一栏，不要写在服务器地址里")
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("代理端口必须在 1 到 65535 之间")
	}
	return nil
}

// address is the proxy's URL, with credentials when they are configured.
func (c Config) address() (*url.URL, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	host := strings.TrimSpace(c.Host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	u := &url.URL{
		Scheme: strings.ToLower(strings.TrimSpace(c.Protocol)),
		Host:   net.JoinHostPort(host, strconv.Itoa(c.Port)),
	}
	if c.AuthEnabled && c.Username != "" {
		u.User = url.UserPassword(c.Username, c.Password)
	}
	return u, nil
}

// proxyFunc is what an http.Transport asks for each request's proxy.
type proxyFunc func(*http.Request) (*url.URL, error)

// funcFor returns the function a transport should use for c: the environment's
// proxy when the setting is off, and the configured proxy when it is on.
//
// The no-proxy list is matched with the standard library's HTTP proxy rules,
// so entries can be hosts, suffixes, addresses or CIDR ranges.
func (c Config) funcFor() (proxyFunc, error) {
	c = c.Normalized()
	if !c.Enabled {
		return http.ProxyFromEnvironment, nil
	}
	address, err := c.address()
	if err != nil {
		return nil, err
	}
	policy := httpproxy.Config{
		HTTPProxy:  address.String(),
		HTTPSProxy: address.String(),
		NoProxy:    strings.TrimSpace(c.NoProxy),
	}
	check := policy.ProxyFunc()
	return func(req *http.Request) (*url.URL, error) {
		direct, err := check(req.URL)
		if err != nil || direct == nil {
			return nil, err
		}
		return address, nil
	}, nil
}

// Source is a proxy setting that can be changed while requests are running.
// Each client keeps one and gives its method to the transport, so a saved
// setting takes effect on the next request without rebuilding the client.
//
// The zero value follows the environment's proxy; that is also what a client
// that never had a setting applied uses.
type Source struct {
	fn atomic.Pointer[proxyFunc]
}

// Set applies c to every request the source's clients make from now on. A
// setting that cannot be used is refused and leaves the previous one in place.
func (s *Source) Set(c Config) error {
	fn, err := c.funcFor()
	if err != nil {
		return err
	}
	s.fn.Store(&fn)
	return nil
}

// Proxy is the function to give http.Transport.Proxy.
func (s *Source) Proxy(req *http.Request) (*url.URL, error) {
	fn := s.fn.Load()
	if fn == nil {
		return http.ProxyFromEnvironment(req)
	}
	return (*fn)(req)
}

// Test asks GameBanana through c and reports how long the answer took. It is
// the settings page's test button: it proves that the proxy carries a request
// the app actually makes, before a download is started.
//
// Any answer below 400 counts as success. A proxy that is down, refuses the
// connection or demands other credentials turns into an error.
func Test(ctx context.Context, c Config) (time.Duration, error) {
	fn, err := c.Normalized().funcFor()
	if err != nil {
		return 0, err
	}
	transport := &http.Transport{
		Proxy:                 fn,
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: testTimeout,
	}
	defer transport.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testAddress, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", about.UserAgent())

	start := time.Now()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	took := time.Since(start)
	if resp.StatusCode >= http.StatusBadRequest {
		return 0, fmt.Errorf("GameBanana 返回了 %s", resp.Status)
	}
	return took, nil
}
