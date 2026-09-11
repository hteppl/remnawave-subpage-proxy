package hosts

import (
	"net/url"
	"regexp"
	"testing"
)

// The byte search must agree with the regex it replaced on every shape.
func TestLooksLikeClashMatchesTheRegex(t *testing.T) {
	re := regexp.MustCompile(`(?m)^proxies:`)
	for _, body := range []string{
		"proxies:\n  - name: a",
		"port: 7890\nproxies:\n  - name: a",
		"port: 7890\r\nproxies:\r\n",
		"  proxies: indented",
		"proxy-groups:\n  - proxies: [a]",
		"vless://x#proxies:",
		"xproxies:",
		"",
		"\n",
		"\nproxies:",
	} {
		if got, want := looksLikeClash([]byte(body)), re.MatchString(body); got != want {
			t.Errorf("%q: got %v, the regex says %v", body, got, want)
		}
	}
}

// Reading the fragment directly must name every link url.Parse could.
func TestFragmentNameMatchesURLParse(t *testing.T) {
	for _, link := range []string{
		"vless://uuid@host:443?type=tcp#%F0%9F%87%A9%F0%9F%87%AA%20Premium",
		"trojan://pass@host:443#Plain",
		"ss://YWVzOnB3@host:8388#a%23b#c",
		"hysteria2://auth@host:443?sni=x#with+plus",
		"vless://uuid@host:443",
		"vless://uuid@host:443#",
		"tuic://u:p@[2001:db8::1]:443#v6",
	} {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("%q does not parse: %v", link, err)
		}
		if got := fragmentName(link); got != u.Fragment {
			t.Errorf("%q: got %q, url.Parse says %q", link, got, u.Fragment)
		}
	}
	if got := fragmentName("vless://uuid@host#bad%zz"); got != "" {
		t.Errorf("a broken escape must leave the host unnamed, got %q", got)
	}
}
