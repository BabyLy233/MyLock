package gb

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"time"
)

const (
	// fileserversTTL is how long the list of file servers stands for. Which
	// servers exist changes slowly, and every install would otherwise ask for
	// the same list again.
	fileserversTTL = 6 * time.Hour

	// probePath is a file GameBanana keeps on every file server, used to time
	// how long one takes to answer. The size does not matter: a probe asks for
	// the headers only.
	probePath = "tools/deadlockmodmanager-v050.zip"
)

// Preferences a download route can name. The first two are names; anything
// else is a server's ID.
const (
	// PreferenceDefault is the address GameBanana itself gives for a file,
	// which redirects to whichever file server the site picks.
	PreferenceDefault = "default"
	// PreferenceAuto is the file server with the highest recent throughput.
	PreferenceAuto = "auto"
)

// The states a file server reports. A server that is down is still used when
// the user named it, but never chosen automatically; one that is terminated is
// gone.
const (
	FileserverUp         = "up"
	FileserverDown       = "down"
	FileserverTerminated = "terminated"
)

// Fileserver is one of GameBanana's download servers. Mods are fetched from
// these rather than from gamebanana.com when the user picks one, which leaves
// the choice of server to them rather than to the site's redirect.
type Fileserver struct {
	// ID is the server's number, which is what a preference names.
	ID string
	// Name is the subdomain the API gives, such as filecache20.
	Name string
	// Domain is the host a file is fetched from, such as
	// filecache20.gamebanana.com.
	Domain string
	// State is FileserverUp, FileserverDown or FileserverTerminated.
	State string
	// Rate is what the server sent per second over the last hour, in bytes.
	Rate int64
	// Requests is how many requests it answered over the last hour.
	Requests int64
}

// URL is where a file of a submission is fetched from this server. The path
// GameBanana uses names the kind of submission and the file's own name, which
// is the name the API reports as _sFile.
//
// An empty address is returned for a kind that has no path, so that a caller
// falls back to the address the API gave rather than fetching from nowhere.
func (f Fileserver) URL(kind Kind, filename string) string {
	segment := kind.Path()
	if f.Domain == "" || segment == "" || filename == "" {
		return ""
	}
	return "https://" + f.Domain + "/" + segment + "/" + url.PathEscape(filename)
}

// ProbeURL is the address to time this server with.
func (f Fileserver) ProbeURL() string {
	if f.Domain == "" {
		return ""
	}
	return "https://" + f.Domain + "/" + probePath
}

// Path is the path segment GameBanana uses for this kind in a file server URL.
func (k Kind) Path() string {
	switch k {
	case Mod:
		return "mods"
	case Sound:
		return "sounds"
	case Wip:
		return "wips"
	}
	return ""
}

// PickFileserver returns the server a preference names, and false for the
// default address or one that is not there.
//
// auto takes the server that sent the most bytes over the last hour, among
// those that are up. A named server is used as long as it still exists, even
// while it is down: the user asked for that one, and falling back silently
// would look like the setting did nothing.
func PickFileserver(servers []Fileserver, preference string) (Fileserver, bool) {
	switch preference {
	case "", PreferenceDefault:
		return Fileserver{}, false
	case PreferenceAuto:
		var chosen Fileserver
		found := false
		for _, server := range servers {
			if server.State != FileserverUp {
				continue
			}
			if !found || server.Rate > chosen.Rate {
				chosen, found = server, true
			}
		}
		return chosen, found
	default:
		for _, server := range servers {
			if server.ID == preference && server.State != FileserverTerminated {
				return server, true
			}
		}
		return Fileserver{}, false
	}
}

// Fileservers returns GameBanana's file servers. A list read less than
// fileserversTTL ago is returned again unless force is set, which is what the
// settings page's refresh button does.
//
// The list is read from the API rather than shipped with the app: servers are
// added and taken away, and a stale list would name a host that no longer
// answers. Records whose subdomain is not a plain hostname are dropped whole,
// so that nothing the API sends can point a download at another host.
func (c *Client) Fileservers(ctx context.Context, force bool) ([]Fileserver, error) {
	c.fileserversMu.Lock()
	defer c.fileserversMu.Unlock()

	if !force && !c.fileserversAt.IsZero() && time.Since(c.fileserversAt) < fileserversTTL {
		return slices.Clone(c.fileservers), nil
	}

	var reply struct {
		Records []struct {
			ID     int    `json:"_idRow"`
			Domain string `json:"_sDomain"`
			State  string `json:"_sState"`
			Stats  struct {
				Hour *struct {
					Rate     float64 `json:"_fRate"`
					Requests int64   `json:"_nRequests"`
				} `json:"_a1hr"`
			} `json:"_aStats"`
		} `json:"_aRecords"`
	}
	if err := c.get(ctx, "Util/Fileservers?_nPage=1", &reply); err != nil {
		return nil, err
	}

	servers := make([]Fileserver, 0, len(reply.Records))
	for _, record := range reply.Records {
		if !fileserverDomain(record.Domain) {
			continue
		}
		server := Fileserver{
			ID:     strconv.Itoa(record.ID),
			Name:   record.Domain,
			Domain: record.Domain + ".gamebanana.com",
			State:  FileserverDown,
		}
		switch record.State {
		case FileserverUp:
			server.State = FileserverUp
		case FileserverTerminated:
			server.State = FileserverTerminated
		}
		if record.Stats.Hour != nil {
			server.Rate = max(int64(record.Stats.Hour.Rate), 0)
			server.Requests = record.Stats.Hour.Requests
		}
		servers = append(servers, server)
	}

	c.fileservers, c.fileserversAt = servers, time.Now()
	return slices.Clone(servers), nil
}

// fileserverDomain reports whether a subdomain is a plain hostname: letters,
// digits and hyphens, and nothing else. It is what keeps a record from naming
// a host outside gamebanana.com.
func fileserverDomain(domain string) bool {
	if domain == "" {
		return false
	}
	for _, r := range domain {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}
