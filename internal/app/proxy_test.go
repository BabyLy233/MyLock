package app

import (
	"errors"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/config"
	"github.com/BabyLy233/mylock/internal/proxy"
)

func TestSaveProxyWritesTheSettings(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	a.proxyEnabled = true
	a.proxyProtocol = proxy.SOCKS5
	a.proxyHost = " 127.0.0.1 "
	a.proxyPort = "1080"
	a.proxyNoProxy = "localhost"

	a.saveProxy()

	waitFor(t, "the proxy to be saved", func() bool {
		cfg, err := config.Load(path)
		return err == nil &&
			cfg.Proxy.Enabled &&
			cfg.Proxy.Protocol == proxy.SOCKS5 &&
			cfg.Proxy.Host == "127.0.0.1" &&
			cfg.Proxy.Port == 1080 &&
			cfg.Proxy.NoProxy == "localhost"
	})
	if a.proxyErr != "" {
		t.Errorf("saving the proxy failed: %s", a.proxyErr)
	}
}

func TestSaveProxyRefusesAnUnusableSetting(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	a.proxyEnabled = true
	a.proxyHost = ""

	a.saveProxy()

	if a.proxyErr == "" {
		t.Fatal("an empty host was accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused setting was written: %v", err)
	}
}

func TestStartUsesTheSavedProxy(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	cfg := config.Config{Proxy: proxy.Config{
		Enabled:  true,
		Protocol: proxy.SOCKS5,
		Host:     "127.0.0.1",
		Port:     1080,
	}}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	startApp(t, a)

	if !a.proxyEnabled {
		t.Fatal("the saved proxy is not enabled")
	}
	if a.proxyProtocol != proxy.SOCKS5 || a.proxyHost != "127.0.0.1" || a.proxyPort != "1080" {
		t.Errorf("the editor holds %s %s:%s, want socks5 127.0.0.1:1080",
			a.proxyProtocol, a.proxyHost, a.proxyPort)
	}
	if a.proxyErr != "" {
		t.Errorf("applying the saved proxy failed: %s", a.proxyErr)
	}
}

func TestSaveProxyCanTurnItOff(t *testing.T) {
	a := newApp()
	path := settingsPath(t, a)
	cfg := config.Config{Proxy: proxy.Config{
		Enabled:  true,
		Protocol: proxy.SOCKS5,
		Host:     "127.0.0.1",
		Port:     1080,
	}}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	startApp(t, a)

	a.proxyEnabled = false
	a.saveProxy()

	waitFor(t, "the proxy to be turned off", func() bool {
		saved, err := config.Load(path)
		return err == nil && !saved.Proxy.Enabled
	})
	if a.proxyErr != "" {
		t.Errorf("turning the proxy off failed: %s", a.proxyErr)
	}
}

func TestSettingsPageShowsTheProxyEditor(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1280, 800)
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("网络代理") {
		t.Fatalf("the settings page does not show the proxy section: %q", tt.Texts())
	}
	// Turning the proxy off is saved with the same button, so it is there
	// while the proxy is off too.
	if !tt.HasText("保存设置") {
		t.Errorf("there is no way to save a disabled proxy: %q", tt.Texts())
	}

	a.proxyEnabled = true
	tt.Frame()
	for _, want := range []string{"服务器", "端口", "测试连接", "保存设置"} {
		if !tt.HasText(want) {
			t.Errorf("the proxy editor does not show %q: %q", want, tt.Texts())
		}
	}
}
