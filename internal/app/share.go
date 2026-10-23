package app

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/BabyLy233/mylock/internal/gb"
)

// parseShare reads the submission a piece of text names. What the detail page
// copies is the submission's page on GameBanana, which is an address a user can
// paste anywhere; the app opens the same submission when that address is pasted
// into the search box. The app's own slugs are accepted too, so that an id
// copied out of a profile's manifest names the same thing, and a bare number is
// taken as the kind of the tab that is up, because the number alone does not say
// which of the three listings it is from.
func parseShare(text string, tab gb.Kind) (kind gb.Kind, id int, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", 0, false
	}

	u, err := url.Parse(text)
	if err != nil {
		return "", 0, false
	}
	if u.Host != "" {
		if !isGameBananaHost(u.Hostname()) {
			return "", 0, false
		}
		return submissionInPath(u.Path)
	}
	// An address pasted without its scheme is parsed as a path, host and all,
	// and a chat client may have made the address into a link without changing
	// what follows the host.
	if kind, id, ok := submissionInPath(u.Path); ok {
		return kind, id, true
	}

	lower := strings.ToLower(text)
	for _, slug := range []struct {
		prefix string
		kind   gb.Kind
	}{
		{"snd-", gb.Sound},
		{"wip-", gb.Wip},
	} {
		rest, found := strings.CutPrefix(lower, slug.prefix)
		if !found {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil || n <= 0 {
			return "", 0, false
		}
		return slug.kind, n, true
	}

	if n, err := strconv.Atoi(text); err == nil && n > 0 {
		return tab, n, true
	}
	return "", 0, false
}

// isGameBananaHost reports whether a host is the catalogue's. Its addresses are
// served from the site and from its www subdomain.
func isGameBananaHost(host string) bool {
	host = strings.ToLower(host)
	return host == "gamebanana.com" || strings.HasSuffix(host, ".gamebanana.com")
}

// submissionInPath reads a kind and a number out of a submission page's path:
// /mods/599927, /sounds/42, /wips/7. Anything else is not a submission's page.
func submissionInPath(path string) (gb.Kind, int, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// An address without a scheme keeps its host in the path.
	if len(parts) >= 3 && isGameBananaHost(parts[0]) {
		parts = parts[1:]
	}
	if len(parts) < 2 {
		return "", 0, false
	}
	var kind gb.Kind
	switch strings.ToLower(parts[0]) {
	case "mods":
		kind = gb.Mod
	case "sounds":
		kind = gb.Sound
	case "wips":
		kind = gb.Wip
	default:
		return "", 0, false
	}
	id, err := strconv.Atoi(parts[1])
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return kind, id, true
}
