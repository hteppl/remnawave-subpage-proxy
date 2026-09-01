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

type Route struct {
	ShortUUID  string
	ClientType string
}

// ParseRoute yields an empty ShortUUID for paths that name no subscription.
func ParseRoute(path, prefix string) Route {
	path = strings.TrimPrefix(path, "/")

	if prefix != "" {
		rest, found := strings.CutPrefix(path, prefix)
		if !found || (rest != "" && !strings.HasPrefix(rest, "/")) {
			return Route{}
		}
		path = strings.TrimPrefix(rest, "/")
	}

	if path == "" {
		return Route{}
	}

	first, rest, _ := strings.Cut(path, "/")
	if !plausibleShortUUID(first) {
		return Route{}
	}

	route := Route{ShortUUID: first}
	if rest != "" {
		clientType, extra, _ := strings.Cut(rest, "/")
		if extra != "" {
			return Route{}
		}
		if _, ok := ClientTypes[strings.ToLower(clientType)]; ok {
			route.ClientType = strings.ToLower(clientType)
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
	switch strings.ToLower(segment) {
	case "assets", "api", "internal", "favicon", "robots":
		return false
	}
	return true
}
