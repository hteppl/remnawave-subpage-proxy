package proxy

import (
	"strings"
)

// ClientTypes mirrors REQUEST_TEMPLATE_TYPE in @remnawave/backend-contract.
var ClientTypes = map[string]struct{}{
	"stash":      {},
	"singbox":    {},
	"mihomo":     {},
	"json":       {},
	"v2ray-json": {},
	"clash":      {},
}

// assetsDir is where the page serves its own static files, including
// /assets/.app-config-v2.json — a dotted name that must stay reachable.
const assetsDir = "assets"

// reservedSegments are first segments the page owns, so none of them can be a
// short UUID. This is the one place they are named; block.go shares it.
var reservedSegments = map[string]struct{}{
	assetsDir: {}, "api": {}, "internal": {}, "favicon": {}, "robots": {},
}

type Route struct {
	ShortUUID  string
	ClientType string
}

// ParseRoute yields an empty ShortUUID for paths that name no subscription.
func ParseRoute(path, prefix string) Route {
	segments := splitPath(path)

	rest, matched := stripPrefix(segments, prefix)
	if !matched {
		return Route{}
	}
	segments = rest

	if len(segments) == 0 || len(segments) > 2 || !plausibleShortUUID(segments[0]) {
		return Route{}
	}

	route := Route{ShortUUID: segments[0]}
	if len(segments) == 2 {
		clientType := strings.ToLower(segments[1])
		if _, ok := ClientTypes[clientType]; ok {
			route.ClientType = clientType
		}
	}
	return route
}

// plausibleShortUUID rules out the other paths the page serves: short UUIDs are
// alphanumeric, so a dot (favicon.ico) or a reserved segment disqualifies.
func plausibleShortUUID(segment string) bool {
	if segment == "" || len(segment) > 128 || strings.Contains(segment, ".") {
		return false
	}
	_, reserved := reservedSegments[strings.ToLower(segment)]
	return !reserved
}

// stripPrefix removes CUSTOM_SUB_PREFIX from segments, whole segments at a
// time. Both the router and the blocker call it, so neither can read a
// prefixed path the other way; it allocates nothing, so the prefix costs the
// same whether or not it is configured.
func stripPrefix(segments []string, prefix string) ([]string, bool) {
	for prefix != "" {
		var want string
		want, prefix, _ = strings.Cut(prefix, "/")
		if want == "" {
			continue
		}
		if len(segments) == 0 || segments[0] != want {
			return segments, false
		}
		segments = segments[1:]
	}
	return segments, true
}

// splitPath is the one path splitter both the router and the blocker use, so
// they always see the same segments. One allocation, none for an empty path.
func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	out := make([]string, 0, strings.Count(path, "/")+1)
	for segment := range strings.SplitSeq(path, "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}
