package hosts

import (
	"encoding/base64"
	"math/rand/v2"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var clashRegexp = regexp.MustCompile(`(?m)^proxies:`)

func FuzzLooksLikeClashMatchesTheRegex(f *testing.F) {
	for _, seed := range []string{"proxies:", "a\nproxies:", "a\r\nproxies:", " proxies:", "proxies", "\n", "", "x\nproxie\nproxies:"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if got, want := looksLikeClash(body), clashRegexp.Match(body); got != want {
			t.Fatalf("%q: got %v, the regex says %v", body, got, want)
		}
	})
}

// Wherever url.Parse succeeds, the direct read must give the same name; where
// it fails, the direct read must still not panic.
func FuzzFragmentNameMatchesURLParse(f *testing.F) {
	for _, seed := range []string{
		"vless://u@h:1#a%20b", "trojan://p@h:1#", "ss://x@h:1#a#b", "x://h#%zz", "#only", "no-fragment",
		"vless://u@[::1]:1#v6", "vless://u@h:1?q=%41#%F0%9F%87%A9", "vless://u@h:1#a+b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, link string) {
		got := fragmentName(link)
		u, err := url.Parse(link)
		if err != nil {
			return
		}
		if got != u.Fragment {
			t.Fatalf("%q: got %q, url.Parse says %q", link, got, u.Fragment)
		}
	})
}

// A shuffle may only reorder lines: never drop, duplicate or alter one, and
// never move a host that matches no group.
func FuzzShuffleLinksOnlyReorders(f *testing.F) {
	f.Add("vless://a@h:1#A1\nvless://b@h:1#B\nvless://c@h:1#A2\n", false, uint64(1))
	f.Add("vless://a@h:1#A1\r\nvless://c@h:1#A2", true, uint64(7))
	f.Add("\n\nvless://a@h:1#A1\n\nvmess://e30=#A2\n", false, uint64(3))
	f.Fuzz(func(t *testing.T, text string, wrap bool, seed uint64) {
		s := New([]*regexp.Regexp{regexp.MustCompile(`^A`)})
		rng := rand.New(rand.NewPCG(seed, seed))
		s.shuffle = rng.Shuffle

		body := []byte(text)
		if wrap {
			body = []byte(base64.StdEncoding.EncodeToString(body))
		}
		out, changed := s.Apply(body)
		if !changed {
			if string(out) != string(body) {
				t.Fatal("reported unchanged but the body differs")
			}
			return
		}
		plainOut := out
		if wrap {
			decoded, err := base64.StdEncoding.DecodeString(string(out))
			if err != nil {
				t.Fatalf("wrapped input came back unwrapped: %v", err)
			}
			plainOut = decoded
		}

		before := strings.Split(text, "\n")
		after := strings.Split(string(plainOut), "\n")
		if len(before) != len(after) {
			t.Fatalf("line count %d -> %d", len(before), len(after))
		}
		for i, line := range before {
			if !strings.HasPrefix(linkName(strings.TrimRight(line, "\r")), "A") && after[i] != line {
				t.Fatalf("line %d matches no group but moved: %q -> %q", i, line, after[i])
			}
		}
		slices.Sort(before)
		slices.Sort(after)
		if !slices.Equal(before, after) {
			t.Fatalf("lines were altered, not just reordered")
		}
	})
}

// Sniff and every shuffle must survive arbitrary bytes.
func FuzzApplyNeverPanics(f *testing.F) {
	for _, seed := range [][]byte{benchLinks(), benchXray()[:200], benchSingbox()[:300], benchClash()[:400], []byte("proxies:\n- {"), []byte("[{]"), []byte("{\"outbounds\":1}")} {
		f.Add(seed)
	}
	s := New([]*regexp.Regexp{regexp.MustCompile(`.`)})
	f.Fuzz(func(t *testing.T, body []byte) {
		_ = Sniff(body)
		_, _ = s.Apply(body)
	})
}
