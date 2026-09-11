package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/hosts"
	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
)

// benchUpstream answers like the subscription page: panel headers and a
// 100-host link list.
func benchUpstream(b *testing.B) *httptest.Server {
	b.Helper()
	var sb strings.Builder
	for i := range 100 {
		sb.WriteString("vless://00000000-0000-0000-0000-000000000000@host.example.com:443?type=tcp#")
		if i%2 == 0 {
			sb.WriteString("Premium%20")
		}
		sb.WriteString("node\n")
	}
	body := []byte(sb.String())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("subscription-userinfo", "upload=500000000; download=9500000000; total=100000000000; expire=1798761599")
		h.Set("announce", "Used {TRAFFIC_USED} of {TRAFFIC_LIMIT} · {DAYS_LEFT} days left")
		h.Set("profile-title", "My VPN")
		h.Set("profile-update-interval", "12")
		h.Set("support-url", "https://t.me/support")
		h.Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	}))
	b.Cleanup(srv.Close)
	return srv
}

type benchStages struct {
	rewrite, cache, shuffle, userAgents bool
}

func benchProxy(b *testing.B, upstreamURL string, s benchStages) *Proxy {
	b.Helper()
	target, _ := url.Parse(upstreamURL)
	resolver, _ := realip.Parse("1")
	blocker, _ := NewBlocker(config.Block{Enabled: true}, "")
	o := Options{
		Upstream: target,
		Timeout:  5 * time.Second,
		RealIP:   resolver,
		Blocker:  blocker,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if s.rewrite {
		o.Engine = testEngine(&testing.T{}, nil)
	}
	if s.cache {
		o.SubCache = subcache.New(time.Hour, 64<<20, 1<<20)
	}
	if s.shuffle {
		o.Shuffler = hosts.New([]*regexp.Regexp{regexp.MustCompile(`Premium`)})
	}
	if s.userAgents {
		o.UserAgents, _ = NewUserAgentFilter(config.UserAgents{Rules: []config.UserAgentRule{
			{Pattern: `^\s*(?i:https?|vless|vmess|trojan|ss)://`, Message: "Invalid User-Agent"},
			{Pattern: `^\s*[{\[]`, Message: "Invalid User-Agent"},
		}})
	}
	return New(o)
}

// benchServe drives the handler directly, so the numbers are the proxy's own
// work plus one loopback round trip to the upstream.
func benchServe(b *testing.B, s benchStages) {
	h := benchProxy(b, benchUpstream(b).URL, s)
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/aBcDeF123", nil)
		req.Header.Set("User-Agent", "Happ/1.2.3")
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}

func BenchmarkServeBareRelay(b *testing.B) { benchServe(b, benchStages{}) }
func BenchmarkServeRewrite(b *testing.B)   { benchServe(b, benchStages{rewrite: true}) }
func BenchmarkServeAllStages(b *testing.B) {
	benchServe(b, benchStages{rewrite: true, cache: true, shuffle: true, userAgents: true})
}

func BenchmarkBlocked(b *testing.B) {
	blocker, _ := NewBlocker(config.Block{Enabled: true}, "")
	paths := []string{"/aBcDeF123", "/aBcDeF123/clash", "/assets/index-abc.js", "/.env", "/wp-admin/setup.php"}
	b.ReportAllocs()
	for b.Loop() {
		for _, p := range paths {
			blocker.Blocked(p)
		}
	}
}

func BenchmarkParseRoute(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		ParseRoute("/aBcDeF123/clash", "")
	}
}

func BenchmarkUserAgentMatch(b *testing.B) {
	f, _ := NewUserAgentFilter(config.UserAgents{Rules: []config.UserAgentRule{
		{Pattern: `^\s*(?i:https?|vless|vmess|trojan|ss|hysteria2?)://`, Message: "x"},
		{Pattern: `^\s*[{\[]`, Message: "x"},
		{Pattern: `^[A-Za-z0-9+/=_-]{120,}$`, Message: "x"},
	}})
	b.ReportAllocs()
	for b.Loop() {
		f.Match("Happ/1.2.3 (iPhone; iOS 18.0)")
	}
}
