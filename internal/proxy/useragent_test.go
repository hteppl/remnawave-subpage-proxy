package proxy

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
)

const pastedLink = "https://sub.example.com/aBcDeF123"

func newUAProxy(t *testing.T, upstreamURL string, cache *subcache.Cache, rules ...config.UserAgentRule) *Proxy {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := realip.Parse("1")
	if err != nil {
		t.Fatal(err)
	}
	filter, err := NewUserAgentFilter(config.UserAgents{Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		Upstream:   target,
		Timeout:    2 * time.Second,
		Engine:     testEngine(t, nil),
		RealIP:     resolver,
		SubCache:   cache,
		UserAgents: filter,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func subscriptionUpstream(t *testing.T, hits *atomic.Int32, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("subscription-userinfo", "upload=0; download=1000000000; total=10000000000; expire=0")
		w.Header().Set("announce", "from the panel")
		_, _ = w.Write([]byte(body))
	}))
}

func TestBrokenUserAgentGetsANoticeInsteadOfHosts(t *testing.T) {
	var hits atomic.Int32
	upstream := subscriptionUpstream(t, &hits, "vless://secret@real.example.com:443#Real\n")
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{
		Name:     "link",
		Pattern:  `^https?://`,
		Message:  "Broken UA · {TRAFFIC_USED} used",
		Announce: true,
	}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", pastedLink)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		t.Fatalf("body is not a base64 link list: %q", body)
	}
	if strings.Contains(string(decoded), "real.example.com") {
		t.Fatalf("a real host leaked: %s", decoded)
	}
	_, fragment, _ := strings.Cut(string(decoded), "#")
	if name, _ := url.PathUnescape(fragment); name != "Broken UA · 1.00 GB used" {
		t.Errorf("placeholder name = %q", name)
	}
	if got := resp.Header.Get("subscription-userinfo"); !strings.Contains(got, "total=10000000000") {
		t.Errorf("traffic header lost: %q", got)
	}
	want := rewrite.Encode("Broken UA · 1.00 GB used", rewrite.FormBase64Prefixed)
	if got := resp.Header.Get("announce"); got != want {
		t.Errorf("announce = %q, want %q", got, want)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d times, want 1", hits.Load())
	}
}

// A multi-line message gives one host per line, while the banner gets it whole.
func TestMultiLineNoticeGivesAHostPerLine(t *testing.T) {
	var hits atomic.Int32
	upstream := subscriptionUpstream(t, &hits, "vless://x@real.example.com:443#Real\n")
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{
		Pattern:  `^https?://`,
		Message:  "⚠️ Invalid User-Agent\nReset it in settings\n{TRAFFIC_USED} used\n",
		Announce: true,
	}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", pastedLink)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		t.Fatalf("body is not a base64 link list: %q", body)
	}
	var names []string
	for _, link := range strings.Split(string(decoded), "\n") {
		_, fragment, _ := strings.Cut(link, "#")
		name, _ := url.PathUnescape(fragment)
		names = append(names, name)
	}
	want := []string{"⚠️ Invalid User-Agent", "Reset it in settings", "1.00 GB used"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("hosts = %q, want %q", names, want)
	}

	wantAnnounce := rewrite.Encode(strings.Join(want, "\n"), rewrite.FormBase64Prefixed)
	if got := resp.Header.Get("announce"); got != wantAnnounce {
		t.Errorf("announce = %q, want %q", got, wantAnnounce)
	}
}

func TestNoticeFollowsTheUpstreamFormat(t *testing.T) {
	var hits atomic.Int32
	upstream := subscriptionUpstream(t, &hits, "proxies:\n  - name: Real\n    server: real.example.com\n")
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{Pattern: `^\s*[{\[]`, Message: "Invalid User-Agent"}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", `{"outbounds":[]}`)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if !strings.Contains(string(body), "proxy-groups:") || strings.Contains(string(body), "real.example.com") {
		t.Errorf("want a Clash placeholder, got:\n%s", body)
	}
	if !strings.Contains(string(body), "Invalid User-Agent") {
		t.Errorf("message missing:\n%s", body)
	}
	// announce was not asked for, so the panel's own stays.
	if got := resp.Header.Get("announce"); got != "from the panel" {
		t.Errorf("announce = %q", got)
	}
}

// The subscription page is never asked to build anything.
func TestBlockedUserAgentGetsTheMessageWithoutReachingUpstream(t *testing.T) {
	var hits atomic.Int32
	upstream := subscriptionUpstream(t, &hits, "vless://x@real.example.com:443#Real\n")
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{
		Pattern:  `^https?://`,
		Action:   config.UserAgentBlock,
		Message:  "⚠️ Blocked\nReset the User-Agent · {SHORT_UUID}",
		Announce: true,
	}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", pastedLink)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if hits.Load() != 0 {
		t.Fatalf("upstream was hit %d times", hits.Load())
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		t.Fatalf("body is not a base64 link list: %q", body)
	}
	var names []string
	for _, link := range strings.Split(string(decoded), "\n") {
		_, fragment, _ := strings.Cut(link, "#")
		name, _ := url.PathUnescape(fragment)
		names = append(names, name)
	}
	want := []string{"⚠️ Blocked", "Reset the User-Agent · aBcDeF123"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("hosts = %q, want %q", names, want)
	}
	wantAnnounce := rewrite.Encode(strings.Join(want, "\n"), rewrite.FormBase64Prefixed)
	if got := resp.Header.Get("announce"); got != wantAnnounce {
		t.Errorf("announce = %q, want %q", got, wantAnnounce)
	}
	// Nothing came from the panel, so no quota is invented.
	if got := resp.Header.Get("subscription-userinfo"); got != "" {
		t.Errorf("subscription-userinfo = %q, want none", got)
	}
}

func TestBlockFollowsTheClientTypePath(t *testing.T) {
	var hits atomic.Int32
	upstream := subscriptionUpstream(t, &hits, "")
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{
		Pattern: `^\s*[{\[]`,
		Action:  config.UserAgentBlock,
		Message: "Invalid User-Agent",
	}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123/clash", `{"outbounds":[]}`)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "proxy-groups:") || !strings.Contains(string(body), "Invalid User-Agent") {
		t.Errorf("want a Clash placeholder, got:\n%s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestNoticeRuleWithoutMessageIsDisabled(t *testing.T) {
	var hits atomic.Int32
	const hostList = "vless://x@real.example.com:443#Real\n"
	upstream := subscriptionUpstream(t, &hits, hostList)
	defer upstream.Close()

	filter, err := NewUserAgentFilter(config.UserAgents{Rules: []config.UserAgentRule{{Name: "silent", Pattern: `^https?://`}}})
	if err != nil {
		t.Fatal(err)
	}
	if filter.Enabled() || len(filter.Disabled()) != 1 {
		t.Fatalf("enabled = %v, disabled = %q", filter.Enabled(), filter.Disabled())
	}

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{Pattern: `^https?://`}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", pastedLink)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != hostList {
		t.Errorf("a disabled rule changed the body: %q", body)
	}
}

func TestNormalUserAgentIsUntouched(t *testing.T) {
	var hits atomic.Int32
	const hostList = "vless://x@real.example.com:443#Real\n"
	upstream := subscriptionUpstream(t, &hits, hostList)
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{Pattern: `^https?://`, Message: "Invalid User-Agent"}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.2.3")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != hostList {
		t.Errorf("body changed: %q", body)
	}
}

// A user the panel does not know must not be told to fix their agent.
func TestNoticeSkipsErrorsAndTheWebPage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>page</html>"))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newUAProxy(t, upstream.URL, nil, config.UserAgentRule{Pattern: `.`, Message: "Invalid User-Agent"}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/missing", pastedLink)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "User-Agent") {
		t.Errorf("404 rewritten: %d %q", resp.StatusCode, body)
	}

	resp = get(t, front.Client(), front.URL+"/aBcDeF123", pastedLink)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "<html>page</html>" {
		t.Errorf("web page rewritten: %q", body)
	}
}

// Were the notice cached, a later outage would replay it to a fixed client,
// or the real hosts to the broken one.
func TestNoticeIsNeverCached(t *testing.T) {
	var hits atomic.Int32
	upstream := subscriptionUpstream(t, &hits, "vless://x@real.example.com:443#Real\n")

	cache := subcache.New(time.Hour, 1<<20, 1<<20)
	front := httptest.NewServer(newUAProxy(t, upstream.URL, cache, config.UserAgentRule{Pattern: `^https?://`, Message: "Invalid User-Agent"}))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", pastedLink)
	_ = resp.Body.Close()
	upstream.Close()

	// With nothing cached the proxy drops the connection, as for any uncached subscription.
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/aBcDeF123", nil)
	req.Header.Set("User-Agent", pastedLink)
	if resp, err := front.Client().Do(req); err == nil {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Errorf("a notice was replayed from the cache: %d %q", resp.StatusCode, body)
	}
}
