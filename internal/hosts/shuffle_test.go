package hosts

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"gopkg.in/yaml.v3"
)

// reversing makes every shuffle deterministic: the group comes out reversed.
func reversing(n int, swap func(i, j int)) {
	for i := 0; i < n/2; i++ {
		swap(i, n-1-i)
	}
}

func testShuffler(t *testing.T, patterns ...string) *Shuffler {
	t.Helper()
	var groups []config.CompiledShuffleGroup
	for _, p := range patterns {
		groups = append(groups, config.CompiledShuffleGroup{Hostname: regexp.MustCompile(p)})
	}
	s := New(groups)
	s.shuffle = reversing
	return s
}

func byHostname(names ...string) []Host {
	hosts := make([]Host, len(names))
	for i, n := range names {
		hosts[i] = Host{Hostname: n}
	}
	return hosts
}

func order(hosts []Host, perm []int) string {
	got := make([]string, len(hosts))
	for slot, from := range perm {
		got[slot] = hosts[from].Hostname
	}
	return strings.Join(got, " ")
}

func TestDisabledShufflerChangesNothing(t *testing.T) {
	var nilShuffler *Shuffler
	if nilShuffler.Enabled() {
		t.Error("a nil shuffler must report disabled")
	}
	s := New(nil)
	if s.Enabled() {
		t.Error("no patterns must mean disabled")
	}
	if _, changed := s.Apply([]byte("vless://a@ru1.example.com:443\nvless://b@ru2.example.com:443\n")); changed {
		t.Error("a disabled shuffler must not report a change")
	}
}

func TestPermutationKeepsGroupsInTheirSlots(t *testing.T) {
	s := testShuffler(t, `^ru`, `^de`)
	hosts := byHostname("ru1", "de1", "fi1", "ru2", "de2", "ru3")
	if got, want := order(hosts, s.permutation(hosts)), "ru3 de2 fi1 ru2 de1 ru1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPermutationIsNilWhenNothingMoves(t *testing.T) {
	s := testShuffler(t, `^ru`)
	if perm := s.permutation(byHostname("ru1", "de1", "de2")); perm != nil {
		t.Errorf("a single match must not move: %v", perm)
	}
	if perm := s.permutation(byHostname("", "fi1")); perm != nil {
		t.Errorf("no match must not move: %v", perm)
	}
}

func TestFirstPatternWins(t *testing.T) {
	s := testShuffler(t, `1$`, `^ru`)
	// ru1 matches both and belongs to the first, with de1.
	hosts := byHostname("ru1", "ru2", "de1", "ru3")
	if got, want := order(hosts, s.permutation(hosts)), "de1 ru3 ru1 ru2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGroupsMatchByNameToo(t *testing.T) {
	s := New([]config.CompiledShuffleGroup{
		{Name: regexp.MustCompile(`(?i)premium`)},
		{Hostname: regexp.MustCompile(`^ru`), Name: regexp.MustCompile(`\d$`)},
	})
	s.shuffle = reversing

	hosts := []Host{
		{Hostname: "ru1.example.com", Name: "🇷🇺 Premium 1"}, // name group
		{Hostname: "ru2.example.com", Name: "RU 2"},         // both patterns
		{Hostname: "de.example.com", Name: "DE Premium"},    // name group
		{Hostname: "ru3.example.com", Name: "RU 3"},         // both patterns
		{Hostname: "ru4.example.com", Name: "RU four"},      // name does not end in a digit
	}
	got := make([]string, len(hosts))
	for slot, from := range s.permutation(hosts) {
		got[slot] = hosts[from].Name
	}
	if want := "DE Premium,RU 3,🇷🇺 Premium 1,RU 2,RU four"; strings.Join(got, ",") != want {
		t.Errorf("got %q, want %q", strings.Join(got, ","), want)
	}
}

func TestLinksCarryTheirNames(t *testing.T) {
	s := New([]config.CompiledShuffleGroup{{Name: regexp.MustCompile(`^RU`)}})
	s.shuffle = reversing
	body := "vless://u1@a.example.com:443#RU%201\n" +
		"vless://u2@b.example.com:443#DE\n" +
		"vless://u3@c.example.com:443#RU%202\n"
	out, changed := s.Apply([]byte(body))
	if !changed {
		t.Fatal("expected a change")
	}
	want := "vless://u3@c.example.com:443#RU%202\n" +
		"vless://u2@b.example.com:443#DE\n" +
		"vless://u1@a.example.com:443#RU%201\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestPlainLinksAreShuffledLineByLine(t *testing.T) {
	s := testShuffler(t, `^ru\d+\.example\.com$`)
	body := "vless://u1@ru1.example.com:443?type=tcp#RU 1\r\n" +
		"trojan://pw@de1.example.com:443#DE\r\n" +
		"vless://u2@ru2.example.com:443?type=tcp#RU 2\r\n" +
		"vless://u3@ru3.example.com:443?type=tcp#RU 3\r\n"

	out, changed := s.Apply([]byte(body))
	if !changed {
		t.Fatal("expected a change")
	}
	want := "vless://u3@ru3.example.com:443?type=tcp#RU 3\r\n" +
		"trojan://pw@de1.example.com:443#DE\r\n" +
		"vless://u2@ru2.example.com:443?type=tcp#RU 2\r\n" +
		"vless://u1@ru1.example.com:443?type=tcp#RU 1\r\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestBase64LinksAreReEncoded(t *testing.T) {
	s := testShuffler(t, `.`)
	plain := "vless://u1@a.example.com:443#A\nvless://u2@b.example.com:443#B\n"

	for name, enc := range map[string]*base64.Encoding{
		"padded": base64.StdEncoding,
		"raw":    base64.RawStdEncoding,
	} {
		t.Run(name, func(t *testing.T) {
			out, changed := s.Apply([]byte(enc.EncodeToString([]byte(plain))))
			if !changed {
				t.Fatal("expected a change")
			}
			decoded, err := enc.DecodeString(string(out))
			if err != nil {
				t.Fatalf("output is not %s base64: %v", name, err)
			}
			want := "vless://u2@b.example.com:443#B\nvless://u1@a.example.com:443#A\n"
			if string(decoded) != want {
				t.Errorf("got:\n%s\nwant:\n%s", decoded, want)
			}
		})
	}
}

func TestLinkHost(t *testing.T) {
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VM node","add":"vm.example.com","port":"443"}`))
	legacySS := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret@ss.example.com:8388"))
	modernSS := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes:pw")) + "@h.example.com:1#n"
	cases := map[string]Host{
		"vless://uuid@ru1.example.com:443?security=tls#RU%201": {Hostname: "ru1.example.com", Name: "RU 1"},
		"trojan://pw@[2001:db8::1]:443#v6":                     {Hostname: "2001:db8::1", Name: "v6"},
		modernSS:                                               {Hostname: "h.example.com", Name: "n"},
		"ss://" + legacySS + "#legacy%20ss":                    {Hostname: "ss.example.com", Name: "legacy ss"},
		"vmess://" + vmess:                                     {Hostname: "vm.example.com", Name: "VM node"},
		"hysteria2://pw@hy.example.com:443/?sni=x#hy":          {Hostname: "hy.example.com", Name: "hy"},
		"not a link":                                           {},
		"":                                                     {},
	}
	for link, want := range cases {
		if got := linkHost(link); got != want {
			t.Errorf("linkHost(%q) = %+v, want %+v", link, got, want)
		}
	}
}

func TestOpaqueBase64IsLeftAlone(t *testing.T) {
	s := testShuffler(t, `.`)
	body := base64.StdEncoding.EncodeToString([]byte("just some text\nwith lines\n"))
	if _, changed := s.Apply([]byte(body)); changed {
		t.Error("base64 that is not a link list must pass through")
	}
	if _, changed := s.Apply([]byte("<html><body>page</body></html>")); changed {
		t.Error("html must pass through")
	}
}

func TestXrayArrayShufflesWholeConfigs(t *testing.T) {
	s := testShuffler(t, `^ru`)
	body := `[
  {
    "remarks": "RU 1",
    "outbounds": [{"protocol": "vless", "settings": {"vnext": [{"address": "ru1.example.com"}]}}, {"protocol": "freedom"}]
  },
  {
    "remarks": "DE",
    "outbounds": [{"protocol": "trojan", "settings": {"servers": [{"address": "de.example.com"}]}}]
  },
  {
    "remarks": "RU 2",
    "outbounds": [{"protocol": "vless", "settings": {"vnext": [{"address": "ru2.example.com"}]}}]
  }
]`
	out, changed := s.Apply([]byte(body))
	if !changed {
		t.Fatal("expected a change")
	}
	var configs []struct {
		Remarks string `json:"remarks"`
	}
	if err := json.Unmarshal(out, &configs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	var names []string
	for _, c := range configs {
		names = append(names, c.Remarks)
	}
	if got := strings.Join(names, ","); got != "RU 2,DE,RU 1" {
		t.Errorf("order = %s, want RU 2,DE,RU 1", got)
	}
	if !strings.HasPrefix(string(out), "[\n  {") {
		t.Errorf("indentation should be kept:\n%s", out)
	}
}

func TestSingboxReordersSelectorsToo(t *testing.T) {
	s := testShuffler(t, `^ru`)
	body := `{
  "log": {"level": "info"},
  "outbounds": [
    {"type": "selector", "tag": "select", "outbounds": ["auto", "RU 1", "DE", "RU 2", "direct"], "default": "auto"},
    {"type": "urltest", "tag": "auto", "outbounds": ["RU 1", "RU 2"]},
    {"type": "vless", "tag": "RU 1", "server": "ru1.example.com", "server_port": 443},
    {"type": "trojan", "tag": "DE", "server": "de.example.com", "server_port": 443},
    {"type": "vless", "tag": "RU 2", "server": "ru2.example.com", "server_port": 443},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "select"}
}`
	out, changed := s.Apply([]byte(body))
	if !changed {
		t.Fatal("expected a change")
	}
	var parsed struct {
		Outbounds []struct {
			Tag       string   `json:"tag"`
			Outbounds []string `json:"outbounds"`
			Default   string   `json:"default"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	var tags []string
	for _, ob := range parsed.Outbounds {
		tags = append(tags, ob.Tag)
	}
	if got := strings.Join(tags, ","); got != "select,auto,RU 2,DE,RU 1,direct" {
		t.Errorf("outbound order = %s", got)
	}
	if got := strings.Join(parsed.Outbounds[0].Outbounds, ","); got != "auto,RU 2,DE,RU 1,direct" {
		t.Errorf("selector order = %s", got)
	}
	if got := strings.Join(parsed.Outbounds[1].Outbounds, ","); got != "RU 2,RU 1" {
		t.Errorf("urltest order = %s", got)
	}
	if parsed.Outbounds[0].Default != "auto" {
		t.Error("other selector keys must survive")
	}
	if !(strings.Index(string(out), `"log"`) < strings.Index(string(out), `"outbounds"`) &&
		strings.Index(string(out), `"outbounds"`) < strings.Index(string(out), `"route"`)) {
		t.Errorf("key order changed:\n%s", out)
	}
}

func TestClashReordersGroupsToo(t *testing.T) {
	s := testShuffler(t, `^ru`)
	body := `# generated
mixed-port: 7890
proxies:
  - {name: "RU 1", type: vless, server: ru1.example.com, port: 443}
  - {name: DE, type: trojan, server: de.example.com, port: 443}
  - {name: "RU 2", type: vless, server: ru2.example.com, port: 443}
proxy-groups:
  - name: PROXY
    type: select
    proxies: [auto, "RU 1", DE, "RU 2", DIRECT]
  - name: auto
    type: url-test
    proxies:
      - RU 1
      - RU 2
rules:
  - MATCH,PROXY
`
	out, changed := s.Apply([]byte(body))
	if !changed {
		t.Fatal("expected a change")
	}
	var parsed struct {
		MixedPort int `yaml:"mixed-port"`
		Proxies   []struct {
			Name   string `yaml:"name"`
			Server string `yaml:"server"`
		} `yaml:"proxies"`
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not YAML: %v\n%s", err, out)
	}
	var names []string
	for _, p := range parsed.Proxies {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, ","); got != "RU 2,DE,RU 1" {
		t.Errorf("proxies order = %s", got)
	}
	if got := strings.Join(parsed.Groups[0].Proxies, ","); got != "auto,RU 2,DE,RU 1,DIRECT" {
		t.Errorf("select group = %s", got)
	}
	if got := strings.Join(parsed.Groups[1].Proxies, ","); got != "RU 2,RU 1" {
		t.Errorf("url-test group = %s", got)
	}
	if parsed.MixedPort != 7890 || len(parsed.Rules) != 1 {
		t.Error("the rest of the document must survive")
	}
	if !strings.HasPrefix(string(out), "# generated\n") {
		t.Errorf("comments should survive:\n%s", out)
	}
}

func TestReorderNamesLeavesUnknownNamesInPlace(t *testing.T) {
	names := []string{"auto", "A", "DIRECT", "B", "C"}
	changed := reorderNames(names, map[string]int{"A": 2, "B": 1, "C": 0})
	if !changed {
		t.Fatal("expected a change")
	}
	if got := strings.Join(names, ","); got != "auto,C,DIRECT,B,A" {
		t.Errorf("got %s", got)
	}
	if reorderNames([]string{"A", "x"}, map[string]int{"A": 0}) {
		t.Error("one known name cannot move")
	}
}

func TestMalformedBodiesPassThrough(t *testing.T) {
	s := testShuffler(t, `.`)
	for _, body := range []string{"[not json", "{\"outbounds\": 5}", "proxies: [", "\n\n"} {
		if _, changed := s.Apply([]byte(body)); changed {
			t.Errorf("%q must pass through", body)
		}
	}
}

func TestPermutationSkipsWhenNoHostMatches(t *testing.T) {
	groups, err := config.CompileHosts(config.Hosts{Shuffle: []config.ShuffleGroup{{Hostname: `^eu\.`}}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(groups)
	s.shuffle = func(int, func(int, int)) { t.Fatal("shuffle called with no matching host") }
	if perm := s.permutation([]Host{{Hostname: "us.example"}, {Hostname: "asia.example"}}); perm != nil {
		t.Fatalf("permutation = %v, want nil", perm)
	}
}

func TestIsLinkListStopsAtFirstLine(t *testing.T) {
	cases := map[string]bool{
		"":                                     false,
		"\n\n  vless://a@h:1\nnot a link":      true,
		"\r\nhello\nvless://a@h:1":             false,
		"ss://abc#x":                           true,
		"://nope":                              false,
		"weird scheme://x":                     false,
		strings.Repeat("\n", 5) + "trojan://a": true,
	}
	for in, want := range cases {
		if got := isLinkList([]byte(in)); got != want {
			t.Errorf("isLinkList(%q) = %v, want %v", in, got, want)
		}
	}
}
