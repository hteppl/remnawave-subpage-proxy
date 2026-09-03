package proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/panel"
	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
)

func ptr[T any](v T) *T { return &v }

func testEngine(t *testing.T, rules []config.HeaderRule) *rewrite.Engine {
	t.Helper()
	return rewrite.New(rewrite.Options{
		File: config.File{
			Traffic:     config.TrafficFormat{Decimals: 2, Unlimited: "∞"},
			DateTime:    config.DateTimeFormat{Layout: "02.01.2006", TimeLayout: "15:04", Timezone: "UTC", Never: "∞"},
			ProgressBar: config.ProgressBar{Width: 10, Filled: "▰", Empty: "▱"},
			Template:    config.TemplateOpts{Unknown: config.UnknownKeep, ScanAllHeaders: true, DecodeBase64: true},
			Headers:     rules,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func newProxy(t *testing.T, upstream *httptest.Server, rules []config.HeaderRule) *Proxy {
	t.Helper()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := realip.Parse("1")
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		Upstream: target,
		Timeout:  5 * time.Second,
		Engine:   testEngine(t, rules),
		RealIP:   resolver,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestProxyRewritesAnnounceEndToEnd(t *testing.T) {
	var gotForwardedFor, gotForwardedProto, gotHost string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotForwardedFor = r.Header.Get("X-Forwarded-For")
		gotForwardedProto = r.Header.Get("X-Forwarded-Proto")
		gotHost = r.Host

		w.Header().Set("subscription-userinfo", "upload=500000000; download=9500000000; total=100000000000; expire=0")
		w.Header().Set("announce", "Used {TRAFFIC_USED} of {TRAFFIC_LIMIT}")
		w.Header().Set("profile-title", "My Project")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("vless://..."))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newProxy(t, upstream, nil))
	defer front.Close()

	req, err := http.NewRequest(http.MethodGet, front.URL+"/aBcDeF123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("User-Agent", "Happ/1.0")

	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got, want := resp.Header.Get("announce"), "Used 10.00 GB of 100.00 GB"; got != want {
		t.Errorf("announce = %q, want %q", got, want)
	}
	if got := resp.Header.Get("profile-title"); got != "My Project" {
		t.Errorf("profile-title = %q, want it untouched", got)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "vless://..." {
		t.Errorf("body = %q, want it passed through verbatim", body)
	}

	if !strings.HasPrefix(gotForwardedFor, "203.0.113.9, ") {
		t.Errorf("X-Forwarded-For = %q, want the inbound chain preserved and this hop appended", gotForwardedFor)
	}
	if gotForwardedProto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", gotForwardedProto)
	}
	if gotHost != req.Host {
		t.Errorf("upstream Host = %q, want the public host %q", gotHost, req.Host)
	}
}

// Pins the wire format: clients see the spelling the upstream page sends, not
// Go's canonical form.
func TestProxyEmitsLowercaseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("announce", "hello")
		w.Header().Set("profile-title", "title")
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=0; expire=0")
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newProxy(t, upstream, nil))
	defer front.Close()

	// Raw read: net/http's client would canonicalise the casing away.
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	host := strings.TrimPrefix(front.URL, "http://")
	if _, err := io.WriteString(conn, "GET /aBcDeF123 HTTP/1.1\r\nHost: "+host+"\r\nUser-Agent: test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	raw, err := io.ReadAll(bufio.NewReader(conn))
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")

	for _, want := range []string{"announce: ", "profile-title: ", "subscription-userinfo: "} {
		if !strings.Contains(head, want) {
			t.Errorf("response head is missing %q:\n%s", want, head)
		}
	}
	if !strings.Contains(head, "Content-Type: ") {
		t.Errorf("Content-Type lost its canonical spelling:\n%s", head)
	}
}

func TestProxyLeavesNonSubscriptionPathsAlone(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("announce", "{TRAFFIC_USED}")
		_, _ = w.Write([]byte("asset"))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newProxy(t, upstream, nil))
	defer front.Close()

	resp, err := front.Client().Get(front.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("announce"); got != "{TRAFFIC_USED}" {
		t.Errorf("announce = %q, want it untouched on a non-subscription path", got)
	}
}

func TestProxyDropsConnectionWhenUpstreamIsDown(t *testing.T) {
	target, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	resolver, _ := realip.Parse("1")

	front := httptest.NewServer(New(Options{
		Upstream: target,
		Timeout:  time.Second,
		Engine:   testEngine(t, nil),
		RealIP:   resolver,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	defer front.Close()

	// The page drops such connections rather than answering; the proxy mirrors it.
	if _, err := front.Client().Get(front.URL + "/aBcDeF123"); err == nil {
		t.Error("expected the connection to be dropped, got a response")
	}
}

func newProxyWithCache(t *testing.T, upstreamURL string, cache *subcache.Cache) *Proxy {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := realip.Parse("1")
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		Upstream: target,
		Timeout:  2 * time.Second,
		Engine:   testEngine(t, nil),
		RealIP:   resolver,
		SubCache: cache,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func get(t *testing.T, client *http.Client, url, userAgent string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestSubscriptionCacheServesWhenUpstreamDies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=500000000; download=9500000000; total=100000000000; expire=0")
		w.Header().Set("announce", "Used {TRAFFIC_USED} of {TRAFFIC_LIMIT}")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("vless://live"))
	}))

	cache := subcache.New(time.Hour, 1<<20, 1<<20)
	front := httptest.NewServer(newProxyWithCache(t, upstream.URL, cache))
	defer front.Close()

	warm := get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.0")
	body, _ := io.ReadAll(warm.Body)
	_ = warm.Body.Close()
	if string(body) != "vless://live" {
		t.Fatalf("warm-up body = %q", body)
	}
	// What gets cached is the finished response, so the announce is already
	// substituted rather than replayed with raw placeholders.
	if got := warm.Header.Get("announce"); got != "Used 10.00 GB of 100.00 GB" {
		t.Fatalf("warm-up announce = %q", got)
	}

	upstream.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.0")
	defer func() { _ = resp.Body.Close() }()

	cached, _ := io.ReadAll(resp.Body)
	if string(cached) != "vless://live" {
		t.Errorf("body = %q, want the cached subscription", cached)
	}
	if got := resp.Header.Get("announce"); got != "Used 10.00 GB of 100.00 GB" {
		t.Errorf("announce = %q, want the cached header", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSubscriptionCacheDisabledByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=0; expire=0")
		_, _ = w.Write([]byte("vless://live"))
	}))

	front := httptest.NewServer(newProxyWithCache(t, upstream.URL, nil))
	defer front.Close()

	warm := get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.0")
	_ = warm.Body.Close()

	upstream.Close()

	if _, err := front.Client().Get(front.URL + "/aBcDeF123"); err == nil {
		t.Error("without the cache the connection must still be dropped")
	}
}

func TestSubscriptionCacheReplacesUpstream5xx(t *testing.T) {
	fail := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("panel is down"))
			return
		}
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=0; expire=0")
		_, _ = w.Write([]byte("vless://live"))
	}))
	defer upstream.Close()

	cache := subcache.New(time.Hour, 1<<20, 1<<20)
	front := httptest.NewServer(newProxyWithCache(t, upstream.URL, cache))
	defer front.Close()

	_ = get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.0").Body.Close()

	fail = true
	resp := get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.0")
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "vless://live" {
		t.Errorf("got %d %q, want the cached 200 response", resp.StatusCode, body)
	}
}

func TestSubscriptionCacheSkipsWebPage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>subscription page</html>"))
	}))

	cache := subcache.New(time.Hour, 1<<20, 1<<20)
	front := httptest.NewServer(newProxyWithCache(t, upstream.URL, cache))
	defer front.Close()

	_ = get(t, front.Client(), front.URL+"/aBcDeF123", "Mozilla/5.0").Body.Close()

	if n, _ := cache.Stats(); n != 0 {
		t.Errorf("cache holds %d entries; the HTML page must not be cached", n)
	}

	upstream.Close()
	if _, err := front.Client().Get(front.URL + "/aBcDeF123"); err == nil {
		t.Error("with nothing cached the connection must be dropped")
	}
}

func TestOversizedResponseStillStreams(t *testing.T) {
	payload := strings.Repeat("x", 5000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=0; expire=0")
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	cache := subcache.New(time.Hour, 1<<20, 1000)
	front := httptest.NewServer(newProxyWithCache(t, upstream.URL, cache))
	defer front.Close()

	resp := get(t, front.Client(), front.URL+"/aBcDeF123", "Happ/1.0")
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != payload {
		t.Errorf("body was truncated: got %d bytes, want %d", len(body), len(payload))
	}
	if n, _ := cache.Stats(); n != 0 {
		t.Errorf("cache holds %d entries; a body over MaxBody must not be stored", n)
	}
}

func TestSubscriptionURLIsDerivedFromRequest(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		path     string
		host     string
		fwdProto string
		fwdHost  string
		wantURL  string
	}{
		{
			name: "plain request", path: "/aBcDeF123", host: "sub.example.com",
			fwdProto: "https", wantURL: "https://sub.example.com/aBcDeF123",
		},
		{
			name: "client type segment is dropped", path: "/aBcDeF123/clash", host: "sub.example.com",
			fwdProto: "https", wantURL: "https://sub.example.com/aBcDeF123",
		},
		{
			name: "custom prefix is kept", prefix: "sub", path: "/sub/aBcDeF123/json",
			host: "example.com", fwdProto: "https", wantURL: "https://example.com/sub/aBcDeF123",
		},
		{
			name: "forwarded host wins over Host", path: "/aBcDeF123", host: "internal:3020",
			fwdHost: "public.example.com", fwdProto: "https",
			wantURL: "https://public.example.com/aBcDeF123",
		},
		{
			name: "leftmost entry of a forwarded list", path: "/aBcDeF123", host: "internal:3020",
			fwdHost: "public.example.com, edge.internal", fwdProto: "https, http",
			wantURL: "https://public.example.com/aBcDeF123",
		},
		{
			name: "no forwarded proto falls back to http", path: "/aBcDeF123",
			host: "sub.example.com", wantURL: "http://sub.example.com/aBcDeF123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("subscription-userinfo", "upload=0; download=0; total=0; expire=0")
				w.Header().Set("announce", "{SUBSCRIPTION_URL}")
				_, _ = w.Write([]byte("ok"))
			}))
			defer upstream.Close()

			target, _ := url.Parse(upstream.URL)
			resolver, _ := realip.Parse("1")
			handler := New(Options{
				Upstream:  target,
				SubPrefix: tc.prefix,
				Timeout:   2 * time.Second,
				Engine:    testEngine(t, nil),
				RealIP:    resolver,
				Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = tc.host
			req.Header.Set("User-Agent", "Happ/1.0")
			if tc.fwdProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.fwdProto)
			}
			if tc.fwdHost != "" {
				req.Header.Set("X-Forwarded-Host", tc.fwdHost)
			}
			handler.ServeHTTP(rec, req)

			got = announceOf(rec.Header())
			if got != tc.wantURL {
				t.Errorf("announce = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

// The whole point of deriving it: no panel round trip.
func TestSubscriptionURLCostsNoPanelCall(t *testing.T) {
	panelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=0; expire=0")
		w.Header().Set("announce", "{SUBSCRIPTION_URL} · {TRAFFIC_USED}")
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	resolver, _ := realip.Parse("1")
	engine := rewrite.New(rewrite.Options{
		File: config.File{
			Traffic:     config.TrafficFormat{Decimals: 2, Unlimited: "∞"},
			DateTime:    config.DateTimeFormat{Layout: "02.01.2006", TimeLayout: "15:04", Timezone: "UTC", Never: "∞"},
			ProgressBar: config.ProgressBar{Width: 10, Filled: "▰", Empty: "▱"},
			Template:    config.TemplateOpts{Unknown: config.UnknownKeep, ScanAllHeaders: true, DecodeBase64: true},
		},
		Fetcher: fetcherFunc(func() { panelCalls++ }),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	handler := New(Options{
		Upstream: target, Timeout: 2 * time.Second, Engine: engine, RealIP: resolver,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/aBcDeF123", nil)
	req.Host = "sub.example.com"
	req.Header.Set("User-Agent", "Happ/1.0")
	req.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(rec, req)

	if want := "https://sub.example.com/aBcDeF123 · 0 B"; announceOf(rec.Header()) != want {
		t.Errorf("announce = %q, want %q", announceOf(rec.Header()), want)
	}
	if panelCalls != 0 {
		t.Errorf("panel called %d times, want 0", panelCalls)
	}
}

type fetcherFunc func()

func (f fetcherFunc) SubscriptionInfo(context.Context, string, string) (*panel.Info, error) {
	f()
	return nil, panel.ErrNotFound
}

// announceOf reads the header straight off the map. The proxy writes response
// headers lowercase, and http.Header.Get canonicalises the name it looks up, so
// a recorder-based test would never find them.
func announceOf(h http.Header) string {
	if values, ok := h["announce"]; ok && len(values) > 0 {
		return values[0]
	}
	return h.Get("announce")
}

// The page drops connections on purpose for anything it will not serve, so a
// scanner sweep must not fill the log with warnings.
func TestUpstreamDropIsLoggedAtDebug(t *testing.T) {
	// An upstream that accepts and closes without responding.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	var logged bytes.Buffer
	target, _ := url.Parse("http://" + ln.Addr().String())
	resolver, _ := realip.Parse("1")
	handler := New(Options{
		Upstream: target,
		Timeout:  2 * time.Second,
		Engine:   testEngine(t, nil),
		RealIP:   resolver,
		Logger:   slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})

	front := httptest.NewServer(handler)
	defer front.Close()

	if _, err := front.Client().Get(front.URL + "/.env"); err == nil {
		t.Fatal("expected the connection to be dropped")
	}
	if strings.Contains(logged.String(), "upstream request failed") {
		t.Errorf("a deliberate drop should not warn:\n%s", logged.String())
	}
}
