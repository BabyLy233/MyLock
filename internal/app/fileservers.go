package app

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/BabyLy233/mylock/internal/gb"
)

// fileserverPreference reads the download route out of what was saved. An
// empty value is the default, and anything else is either one of the two named
// routes or a server's id, which is kept as it is.
func fileserverPreference(saved string) string {
	if saved == "" {
		return gb.PreferenceDefault
	}
	return saved
}

// sortedFileservers puts the servers that are up first, then the rest, and
// names each group in order. The settings page shows them in this order, which
// is also the order the arrows move through the group in.
func sortedFileservers(servers []gb.Fileserver) []gb.Fileserver {
	sorted := slices.Clone(servers)
	slices.SortFunc(sorted, func(a, b gb.Fileserver) int {
		if byState := cmp.Compare(fileserverStateOrder(a.State), fileserverStateOrder(b.State)); byState != 0 {
			return byState
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return sorted
}

func fileserverStateOrder(state string) int {
	switch state {
	case gb.FileserverUp:
		return 0
	case gb.FileserverDown:
		return 1
	default:
		return 2
	}
}

// maybeLoadFileservers asks for the list once, the first time the settings
// page shows it. Before the window exists there is nobody to show it to, and a
// test builds the page without one; a test that wants the list asks for it.
func (a *app) maybeLoadFileservers() {
	if a.win == nil {
		return
	}
	a.mu.Lock()
	needed := !a.fileserversTried && !a.fileserversBusy
	a.mu.Unlock()
	if needed {
		a.loadFileservers(false)
	}
}

// loadFileservers reads the download servers. force is the refresh button,
// which asks even when the client still holds a recent list.
//
// It reads the network, so it runs off the main thread and publishes what it
// found. A failed read leaves the last list in place and says why: the default
// address still works, so this is not a reason to stop downloading.
func (a *app) loadFileservers(force bool) {
	a.mu.Lock()
	if a.fileserversBusy {
		a.mu.Unlock()
		return
	}
	a.fileserversBusy = true
	a.fileserversTried = true
	a.fileserversErr = ""
	a.mu.Unlock()
	a.invalidate()

	go func() {
		servers, err := a.catalogue.Fileservers(context.Background(), force)
		var message string
		if err != nil {
			message = err.Error()
		}
		a.publish(func() {
			a.mu.Lock()
			a.fileserversBusy = false
			a.fileserversErr = message
			if err == nil {
				a.fileservers = sortedFileservers(servers)
			}
			a.mu.Unlock()
		})
		a.invalidate()
	}()
}

// setFileserverPreference records the route the user chose. It writes the
// settings file, so the save runs off the main thread.
func (a *app) setFileserverPreference(preference string) {
	preference = fileserverPreference(preference)
	a.mu.Lock()
	if a.fileserverPreference == preference {
		a.mu.Unlock()
		return
	}
	a.fileserverPreference = preference
	pinned := a.pinned
	a.mu.Unlock()
	a.invalidate()

	go func() {
		if err := a.saveSettings(pinned); err != nil {
			log.Printf("saving the download route: %v", err)
		}
	}()
}

// testFileservers times every server in the list, all at once, and keeps what
// answered. A server that could not be reached keeps the latency it had, the
// way the list itself is left alone when a read fails.
func (a *app) testFileservers() {
	a.mu.Lock()
	if a.probingFileservers || len(a.fileservers) == 0 {
		a.mu.Unlock()
		return
	}
	a.probingFileservers = true
	servers := slices.Clone(a.fileservers)
	a.mu.Unlock()
	a.invalidate()

	go func() {
		latencies := make(map[string]int64, len(servers))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, server := range servers {
			wg.Go(func() {
				took, ok := a.downloads.Probe(context.Background(), server.ProbeURL())
				if !ok {
					return
				}
				mu.Lock()
				latencies[server.ID] = took.Milliseconds()
				mu.Unlock()
			})
		}
		wg.Wait()

		a.publish(func() {
			a.mu.Lock()
			a.probingFileservers = false
			if a.fileserverLatency == nil {
				a.fileserverLatency = make(map[string]int64, len(latencies))
			}
			maps.Copy(a.fileserverLatency, latencies)
			pinned := a.pinned
			a.mu.Unlock()

			go func() {
				if err := a.saveSettings(pinned); err != nil {
					log.Printf("saving the server latencies: %v", err)
				}
			}()
		})
		a.invalidate()
	}()
}

// downloadURL is where a file of a submission is fetched from: the server the
// preference names, or the address the API gave when no server is chosen or
// the one asked for cannot be used.
//
// It can read the server list, which is why it is called from the goroutine
// that downloads rather than from the view. A list that cannot be read is not
// an error for the download: the default address is always there.
func (a *app) downloadURL(ctx context.Context, kind gb.Kind, file gb.File) string {
	a.mu.Lock()
	preference := a.fileserverPreference
	servers := a.fileservers
	a.mu.Unlock()

	if preference == "" || preference == gb.PreferenceDefault {
		return file.URL
	}
	if len(servers) == 0 {
		fetched, err := a.catalogue.Fileservers(ctx, false)
		if err != nil {
			log.Printf("reading the file server list: %v; downloading from GameBanana", err)
			return file.URL
		}
		servers = fetched
		a.publish(func() {
			a.mu.Lock()
			a.fileservers = sortedFileservers(fetched)
			a.fileserversErr = ""
			a.fileserversTried = true
			a.mu.Unlock()
		})
	}

	server, ok := gb.PickFileserver(servers, preference)
	if !ok {
		return file.URL
	}
	if address := server.URL(kind, file.Name); address != "" {
		return address
	}
	return file.URL
}

// fileserverSelection is the route the settings show as chosen. A server that
// is no longer in the list is shown as the default, which is where downloads go
// now; the id is left in the settings, in case a later read brings it back.
func fileserverSelection(s state) string {
	preference := fileserverPreference(s.fileserverPreference)
	if preference == gb.PreferenceDefault || preference == gb.PreferenceAuto || len(s.fileservers) == 0 {
		return preference
	}
	for _, server := range s.fileservers {
		if server.ID == preference {
			return preference
		}
	}
	return gb.PreferenceDefault
}

// fileserverSection shows which server downloads go through, and lets the user
// choose another. The list is read when the section first shows, not at start,
// so a run that never opens the settings page does not ask for it.
func (a *app) fileserverSection(c *ui.Context, s state) {
	a.maybeLoadFileservers()
	t := c.Theme()
	// A local copy, which the radios write the chosen value into before their
	// notice runs: what was chosen is what the control left here, not what the
	// app held when the frame was built.
	preference := fileserverSelection(s)

	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "下载路由").Bold()
		ui.Text(c, "下载模组时用哪台 GameBanana 文件服务器。默认走 GameBanana 自己给的地址；自动选最近一小时吞吐量最高的一台；也可以指定一台。").
			FontSize(12).TextColor(t.TextMuted)

		ui.RadioGroup(c, func() {
			ui.Radio(c, &preference, gb.PreferenceDefault, "默认：GameBanana 的标准下载地址").
				OnChange(func() { a.setFileserverPreference(preference) })
			ui.Radio(c, &preference, gb.PreferenceAuto, "自动：吞吐量最高的可用服务器").
				OnChange(func() { a.setFileserverPreference(preference) })
			if len(s.fileservers) > 0 {
				ui.Text(c, "可用服务器").FontSize(12).TextColor(t.TextMuted)
				// A grid of cards rather than a row each: there can be more
				// than twenty, and a card carries the same naming and numbers
				// in a third of the height.
				ui.Grid(c).Columns(3).Gap(8).Children(func() {
					for _, server := range s.fileservers {
						a.fileserverCard(c, &preference, server, s.fileserverLatency)
					}
				})
			}
		}).Label("下载路由")

		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "刷新列表").
				Disabled(s.fileserversBusy).
				OnClick(func() { a.loadFileservers(true) })
			ui.Button(c, "测试服务器").
				Disabled(s.probingFileservers || len(s.fileservers) == 0).
				OnClick(a.testFileservers)
		})

		switch {
		case s.fileserversBusy:
			ui.Text(c, "正在读取服务器列表…").FontSize(12).TextColor(t.TextMuted)
		case s.fileserversErr != "":
			ui.Text(c, "读不到服务器列表："+s.fileserversErr).FontSize(12).TextColor(t.Danger)
		case s.probingFileservers:
			ui.Text(c, "正在测试服务器…").FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// fileserverCard is one server in the settings grid: a state dot, its name,
// and what it is doing. The card itself is the radio, so choosing a server is
// clicking anywhere on it.
func (a *app) fileserverCard(c *ui.Context, preference *string, server gb.Fileserver, latencies map[string]int64) {
	t := c.Theme()
	selected := *preference == server.ID
	background, border := t.Surface, t.Border
	if selected {
		// Tinted rather than filled: the card keeps its place in the grid while
		// saying it is the one chosen.
		background = t.Surface.Mix(t.Accent, 0.12)
		border = t.Accent
	}

	card := ui.RadioBase(c, preference, server.ID).
		Padding(t.Space(2.5)).Gap(t.Space(1.5)).AlignItems(ui.Center).
		Background(background).Radius(8).Border(1, border)
	card.Children(func() {
		ui.Box(c).Size(t.Space(2), t.Space(2)).Radius(t.Space(1)).
			Background(fileserverStateColor(t, server.State))
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			latencyMs, measured := latencies[server.ID]
			ui.Text(c, server.Name).Bold().FontSize(13).MaxLines(1).Ellipsis("…")
			ui.Text(c, fileserverDetail(server, latencyMs, measured)).
				FontSize(11).TextColor(t.TextMuted).MaxLines(1).Ellipsis("…")
		})
	})
	// The radio has no look of its own, so the keyboard's place in the group is
	// drawn here.
	card.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if card.FocusVisible() {
			p.FocusRing(r, [4]float32{t.Radius, t.Radius, t.Radius, t.Radius})
		}
	})
	card.OnChange(func() { a.setFileserverPreference(*preference) })
}

// fileserverStateColor is the dot beside a server's name: what is up is green,
// what is down red, and what is gone gray.
func fileserverStateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case gb.FileserverUp:
		return t.Success
	case gb.FileserverDown:
		return t.Danger
	default:
		return t.TextMuted
	}
}

// fileserverDetail is the second line of a server's card: its state, the bytes
// it sent over the last hour, and the last latency measured for it, if any.
func fileserverDetail(server gb.Fileserver, latencyMs int64, measured bool) string {
	state := "离线"
	switch server.State {
	case gb.FileserverUp:
		state = "在线"
	case gb.FileserverTerminated:
		state = "已停用"
	}
	detail := fmt.Sprintf("%s · %s/s", state, formatBytes(server.Rate))
	if measured {
		detail += fmt.Sprintf(" · %d 毫秒", latencyMs)
	}
	return detail
}
