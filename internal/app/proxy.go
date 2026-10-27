package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/proxy"
)

// proxyProtocols are the schemes the settings page offers, in the order it
// offers them, and proxyProtocolLabels names them.
var (
	proxyProtocols      = []string{proxy.HTTP, proxy.HTTPS, proxy.SOCKS5}
	proxyProtocolLabels = []string{"HTTP", "HTTPS", "SOCKS5"}
)

// resetProxyDraft fills the settings page's editor from what was saved. A
// protocol this build does not know is shown as HTTP rather than as no choice
// at all; saving then replaces it.
func (a *app) resetProxyDraft() {
	c := a.proxyConfig.Normalized()
	a.proxyEnabled = c.Enabled
	a.proxyProtocol = c.Protocol
	if !slices.Contains(proxyProtocols, a.proxyProtocol) {
		a.proxyProtocol = proxy.HTTP
	}
	a.proxyHost = c.Host
	a.proxyPort = strconv.Itoa(c.Port)
	a.proxyAuth = c.AuthEnabled
	a.proxyUsername = c.Username
	a.proxyPassword = c.Password
	a.proxyNoProxy = c.NoProxy
}

// useProxy gives cfg to every client that reaches the network. The setting is
// checked once here, before any client is changed, so a bad one leaves every
// client on the proxy it already had.
func (a *app) useProxy(cfg proxy.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := a.downloads.SetProxy(cfg); err != nil {
		return err
	}
	if err := a.catalogue.SetProxy(cfg); err != nil {
		return err
	}
	return a.thumbnails.setProxy(cfg)
}

// proxyDraftConfig is what the editor holds, as a setting. The port is text
// while it is being typed, so it is read only when it is not empty; when the
// proxy is off a field that cannot be read does not stop the rest from being
// saved.
func (a *app) proxyDraftConfig() (proxy.Config, error) {
	cfg := proxy.Config{
		Enabled:     a.proxyEnabled,
		Protocol:    strings.ToLower(strings.TrimSpace(a.proxyProtocol)),
		Host:        strings.TrimSpace(a.proxyHost),
		AuthEnabled: a.proxyAuth,
		Username:    a.proxyUsername,
		Password:    a.proxyPassword,
		NoProxy:     strings.TrimSpace(a.proxyNoProxy),
	}
	if text := strings.TrimSpace(a.proxyPort); text != "" {
		port, err := strconv.Atoi(text)
		if err != nil {
			if cfg.Enabled {
				return cfg, errors.New("代理端口必须是数字。")
			}
		} else {
			cfg.Port = port
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg.Normalized(), nil
}

// saveProxy applies the setting the editor holds, remembers it, and writes it
// to the settings file. The file write runs off the main thread; the proxy
// itself changes here, so the next request already uses it.
func (a *app) saveProxy() {
	cfg, err := a.proxyDraftConfig()
	if err != nil {
		a.proxyErr = err.Error()
		a.proxyNotice = ""
		a.invalidate()
		return
	}
	if err := a.useProxy(cfg); err != nil {
		a.proxyErr = "代理没有生效：" + err.Error()
		a.proxyNotice = ""
		a.invalidate()
		return
	}

	a.mu.Lock()
	a.proxyConfig = cfg
	pinned := a.pinned
	a.mu.Unlock()

	// Show what was saved, which may be tidier than what was typed.
	a.proxyEnabled = cfg.Enabled
	a.proxyProtocol = cfg.Protocol
	a.proxyHost = cfg.Host
	a.proxyPort = strconv.Itoa(cfg.Port)
	a.proxyErr = ""
	a.proxyNotice = "代理设置已保存。"
	a.invalidate()

	go func() {
		if err := a.saveSettings(pinned); err != nil {
			a.publish(func() {
				a.proxyErr = "保存代理设置失败：" + err.Error()
				a.proxyNotice = ""
			})
			a.invalidate()
		}
	}()
}

// testProxy asks GameBanana through the setting the editor holds, without
// saving it. It runs off the main thread; the result comes back through
// publish.
func (a *app) testProxy() {
	cfg, err := a.proxyDraftConfig()
	if err != nil {
		a.proxyErr = err.Error()
		a.proxyNotice = ""
		a.invalidate()
		return
	}

	a.proxyTesting = true
	a.proxyErr = ""
	a.proxyNotice = ""
	a.invalidate()

	go func() {
		took, err := proxy.Test(context.Background(), cfg)
		a.publish(func() {
			a.proxyTesting = false
			if err != nil {
				a.proxyErr = "代理连接失败：" + err.Error()
				a.proxyNotice = ""
				return
			}
			a.proxyErr = ""
			a.proxyNotice = fmt.Sprintf("代理连接成功（%d ms）。", took.Milliseconds())
		})
		a.invalidate()
	}()
}

// proxySection shows and edits the proxy the app's requests go through. It is
// the same setting for the catalogue, the pictures and the archives: a proxy
// that can reach GameBanana is what makes all three work.
func (a *app) proxySection(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "网络代理").Bold()
		ui.Text(c, "商店请求、图片和模组下载都通过代理服务器。关闭时跟随系统的代理环境变量（如果有）。应用自身的更新不受此设置影响。").
			FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Switch(c, &a.proxyEnabled).
				Label("启用代理").
				OnChange(func() {
					a.proxyErr = ""
					a.proxyNotice = ""
				})
			ui.Text(c, "启用代理")
		})

		// The fields and the buttons below the switch stay on the page when
		// the proxy is off: turning it off is saved with the same button, and
		// the fields are kept for turning it back on.
		if a.proxyEnabled {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.End).Children(func() {
				ui.Field(c, "协议", func() {
					ui.Row(c).Children(func() {
						// A local copy, which the control writes the chosen
						// index into before its notice runs.
						chosen := max(slices.Index(proxyProtocols, a.proxyProtocol), 0)
						ui.Segmented(c, &chosen, proxyProtocolLabels...).OnChange(func() {
							a.proxyProtocol = proxyProtocols[chosen]
						})
					})
				}).Width(220)
				ui.Field(c, "服务器", func() {
					ui.TextInput(c, &a.proxyHost).Fill().Placeholder("127.0.0.1")
				}).Grow(1)
				ui.Field(c, "端口", func() {
					ui.TextInput(c, &a.proxyPort).Fill().Placeholder("8080")
				}).Width(110)
			})

			ui.Checkbox(c, &a.proxyAuth, "代理需要用户名和密码")
			if a.proxyAuth {
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Field(c, "用户名", func() {
						ui.TextInput(c, &a.proxyUsername).Fill()
					}).Grow(1)
					ui.Field(c, "密码", func() {
						ui.TextInput(c, &a.proxyPassword).Fill().Password()
					}).Grow(1)
				})
			}

			ui.Field(c, "不走代理的主机", func() {
				ui.TextInput(c, &a.proxyNoProxy).Fill().
					Placeholder("localhost, 127.0.0.1, .local")
			}).Description("用逗号分隔，支持域名、IP 和 CIDR；这些地址直接连接。")
		}

		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "测试连接").
				Disabled(!a.proxyEnabled || a.proxyTesting).
				OnClick(a.testProxy)
			ui.Button(c, "保存设置").OnClick(a.saveProxy)
		})

		switch {
		case a.proxyTesting:
			ui.Text(c, "正在通过代理连接 GameBanana…").FontSize(12).TextColor(t.TextMuted)
		case a.proxyErr != "":
			ui.Text(c, a.proxyErr).FontSize(12).TextColor(t.Danger)
		case a.proxyNotice != "":
			ui.Text(c, a.proxyNotice).FontSize(12).TextColor(t.Success)
		}
	})
}
