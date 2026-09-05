package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/hosts"
	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
)

func newShufflingProxy(t *testing.T, upstreamURL string, patterns ...string) *Proxy {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := realip.Parse("1")
	if err != nil {
		t.Fatal(err)
	}
	var groups []config.CompiledShuffleGroup
	for _, p := range patterns {
		groups = append(groups, config.CompiledShuffleGroup{Hostname: regexp.MustCompile(p)})
	}
	return New(Options{
		Upstream: target,
		Timeout:  2 * time.Second,
		Engine:   testEngine(t, nil),
		RealIP:   resolver,
		Shuffler: hosts.New(groups),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// Three hosts give six orders; all must appear and the pinned host never moves.
func TestProxyShufflesSubscriptionHosts(t *testing.T) {
	var gotAcceptEncoding []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding = r.Header.Values("Accept-Encoding")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("vless://a@ru1.example.com:443#A\n" +
			"vless://p@pinned.example.com:443#P\n" +
			"vless://b@ru2.example.com:443#B\n" +
			"vless://c@ru3.example.com:443#C\n"))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newShufflingProxy(t, upstream.URL, `^ru\d+\.example\.com$`))
	defer front.Close()

	seen := make(map[string]bool)
	for i := 0; i < 200 && len(seen) < 6; i++ {
		req, _ := http.NewRequest(http.MethodGet, front.URL+"/aBcDeF123", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		if len(lines) != 4 || lines[1] != "vless://p@pinned.example.com:443#P" {
			t.Fatalf("pinned host moved or a line was lost:\n%s", body)
		}
		if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
			t.Fatalf("Content-Length = %q, body is %d bytes", got, len(body))
		}
		seen[lines[0]+lines[2]+lines[3]] = true
	}
	if len(seen) != 6 {
		t.Errorf("saw %d orders out of 6", len(seen))
	}
	if len(gotAcceptEncoding) != 0 {
		t.Errorf("Accept-Encoding must not reach the upstream when shuffling: %v", gotAcceptEncoding)
	}
}

func TestProxyLeavesPageAndCompressedBodiesAlone(t *testing.T) {
	const page = "<html><body>vless://a@ru1.example.com\nvless://b@ru2.example.com</body></html>"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("kind") {
		case "html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(page))
		case "compressed":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write([]byte("vless://a@ru1.example.com\nvless://b@ru2.example.com"))
		}
	}))
	defer upstream.Close()

	front := httptest.NewServer(newShufflingProxy(t, upstream.URL, `.`))
	defer front.Close()

	for _, kind := range []string{"html", "compressed"} {
		var got string
		for i := 0; i < 20; i++ {
			resp, err := front.Client().Get(front.URL + "/aBcDeF123?kind=" + kind)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if got != "" && got != string(body) {
				t.Fatalf("%s body changed between requests", kind)
			}
			got = string(body)
		}
		if !strings.HasPrefix(got, "<html>") && !strings.HasPrefix(got, "vless://a@") {
			t.Errorf("%s body was rewritten: %q", kind, got)
		}
	}
}
