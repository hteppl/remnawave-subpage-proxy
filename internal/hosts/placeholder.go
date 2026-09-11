package hosts

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The placeholder hosts point nowhere: connecting to one fails at once, so a
// client that selects it gets no traffic through, only its name on screen.
const (
	placeholderServer = "0.0.0.0"
	placeholderPort   = 1
	placeholderUUID   = "00000000-0000-0000-0000-000000000000"
)

// Placeholder builds a subscription in format holding one dead host per name,
// in order, so a message too long for one host name reads across several. It
// stands in for the real hosts when they must not be sent, the way the panel's
// own custom remarks do. Blank and repeated names are dropped: sing-box tags
// and Clash proxy names must be unique, or the whole profile is refused.
func Placeholder(format Format, names ...string) []byte {
	names = uniqueNames(names)
	switch format {
	case FormatXray:
		return placeholderXray(names)
	case FormatSingbox:
		return placeholderSingbox(names)
	case FormatClash:
		return placeholderClash(names)
	default:
		return placeholderLinks(names)
	}
}

func uniqueNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// placeholderLinks is base64-wrapped, the form Remnawave serves by default.
func placeholderLinks(names []string) []byte {
	links := make([]string, len(names))
	for i, name := range names {
		links[i] = "vless://" + placeholderUUID + "@" + placeholderServer + ":" + strconv.Itoa(placeholderPort) +
			"?type=tcp&security=none#" + url.PathEscape(name)
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n"))))
}

func placeholderXray(names []string) []byte {
	configs := make([]map[string]any, len(names))
	for i, name := range names {
		configs[i] = map[string]any{
			"remarks": name,
			"outbounds": []map[string]any{{
				"tag":      "proxy",
				"protocol": "vless",
				"settings": map[string]any{
					"vnext": []map[string]any{{
						"address": placeholderServer,
						"port":    placeholderPort,
						"users":   []map[string]any{{"id": placeholderUUID, "encryption": "none"}},
					}},
				},
			}},
		}
	}
	out, _ := json.MarshalIndent(configs, "", "  ")
	return out
}

func placeholderSingbox(names []string) []byte {
	outbounds := make([]map[string]any, len(names))
	for i, name := range names {
		outbounds[i] = map[string]any{
			"type":        "vless",
			"tag":         name,
			"server":      placeholderServer,
			"server_port": placeholderPort,
			"uuid":        placeholderUUID,
		}
	}
	out, _ := json.MarshalIndent(map[string]any{"outbounds": outbounds}, "", "  ")
	return out
}

// placeholderClash carries a group and a catch-all rule, without which a
// Clash core refuses the profile outright instead of showing the names.
func placeholderClash(names []string) []byte {
	proxies := make([]map[string]any, len(names))
	for i, name := range names {
		proxies[i] = map[string]any{
			"name":   name,
			"type":   "vless",
			"server": placeholderServer,
			"port":   placeholderPort,
			"uuid":   placeholderUUID,
		}
	}
	config := map[string]any{
		"proxies": proxies,
		"proxy-groups": []map[string]any{{
			"name":    "PROXY",
			"type":    "select",
			"proxies": names,
		}},
		"rules": []string{"MATCH,PROXY"},
	}
	out, _ := yaml.Marshal(config)
	return out
}
