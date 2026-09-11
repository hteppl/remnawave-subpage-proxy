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

// reservedSegments are first segments the page owns, so none can be a short UUID; block.go shares it.
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

// Short UUIDs are alphanumeric, so a dot (favicon.ico) or a reserved segment disqualifies.
func plausibleShortUUID(segment string) bool {
	if segment == "" || len(segment) > 128 || strings.Contains(segment, ".") {
		return false
	}
	_, reserved := reservedSegments[strings.ToLower(segment)]
	return !reserved
}

// Shared by router and blocker so neither reads a prefixed path differently.
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

// Shared by router and blocker so they always see the same segments.
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
