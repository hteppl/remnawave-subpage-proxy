package hosts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const benchHosts = 100

func benchName(i int) string {
	if i%2 == 0 {
		return fmt.Sprintf("🇩🇪 Premium %d", i)
	}
	return fmt.Sprintf("🇳🇱 Basic %d", i)
}

func benchLinks() []byte {
	var sb strings.Builder
	for i := range benchHosts {
		fmt.Fprintf(&sb, "vless://%08d-0000-0000-0000-000000000000@host%d.example.com:443?type=tcp&security=reality&sni=example.com&fp=chrome#%s\n",
			i, i, strings.ReplaceAll(benchName(i), " ", "%20"))
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(sb.String())))
}

func benchXray() []byte {
	configs := make([]map[string]any, benchHosts)
	for i := range configs {
		configs[i] = map[string]any{
			"remarks": benchName(i),
			"outbounds": []map[string]any{{"tag": "proxy", "protocol": "vless", "settings": map[string]any{
				"vnext": []map[string]any{{"address": fmt.Sprintf("host%d.example.com", i), "port": 443}},
			}}},
			"routing": map[string]any{"rules": []map[string]any{{"type": "field", "outboundTag": "direct", "domain": []string{"geosite:private"}}}},
		}
	}
	out, _ := json.MarshalIndent(configs, "", "  ")
	return out
}

func benchSingbox() []byte {
	outbounds := []map[string]any{{"type": "selector", "tag": "proxy", "outbounds": []string{}}}
	var tags []string
	for i := range benchHosts {
		tags = append(tags, benchName(i))
		outbounds = append(outbounds, map[string]any{"type": "vless", "tag": benchName(i), "server": fmt.Sprintf("host%d.example.com", i), "server_port": 443})
	}
	outbounds[0]["outbounds"] = tags
	out, _ := json.MarshalIndent(map[string]any{"log": map[string]any{"level": "warn"}, "outbounds": outbounds}, "", "  ")
	return out
}

func benchClash() []byte {
	var proxies []map[string]any
	var names []string
	for i := range benchHosts {
		names = append(names, benchName(i))
		proxies = append(proxies, map[string]any{"name": benchName(i), "type": "vless", "server": fmt.Sprintf("host%d.example.com", i), "port": 443})
	}
	out, _ := yaml.Marshal(map[string]any{
		"proxies":      proxies,
		"proxy-groups": []map[string]any{{"name": "PROXY", "type": "select", "proxies": names}},
		"rules":        []string{"MATCH,PROXY"},
	})
	return out
}

func benchShuffle(b *testing.B, body []byte) {
	s := New([]*regexp.Regexp{regexp.MustCompile(`Premium`)})
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		if _, changed := s.Apply(body); !changed {
			b.Fatal("nothing shuffled")
		}
	}
}

func BenchmarkShuffleLinks(b *testing.B)   { benchShuffle(b, benchLinks()) }
func BenchmarkShuffleXray(b *testing.B)    { benchShuffle(b, benchXray()) }
func BenchmarkShuffleSingbox(b *testing.B) { benchShuffle(b, benchSingbox()) }
func BenchmarkShuffleClash(b *testing.B)   { benchShuffle(b, benchClash()) }

func BenchmarkSniff(b *testing.B) {
	body := benchLinks()
	b.ReportAllocs()
	for b.Loop() {
		Sniff(body)
	}
}

func BenchmarkPlaceholder(b *testing.B) {
	names := []string{"⚠️ Invalid User-Agent", "Settings → User-Agent → reset", "Then update the subscription"}
	b.ReportAllocs()
	for b.Loop() {
		Placeholder(FormatClash, names...)
	}
}
