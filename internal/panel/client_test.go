package panel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{BaseURL: base, Token: "test-token", Timeout: 5 * time.Second})
}

func TestSubscriptionInfo(t *testing.T) {
	var gotPath, gotAuth, gotRealIP string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotRealIP = r.Header.Get(realIPHeader)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"isFound":true,"user":{
			"shortUuid":"aBcDeF123","username":"alice","daysLeft":12,
			"trafficUsedBytes":"10500000000","trafficLimitBytes":"100000000000",
			"lifetimeTrafficUsedBytes":"1200000000000",
			"expiresAt":"2026-12-31T23:59:59.000Z","isActive":true,
			"userStatus":"ACTIVE","trafficLimitStrategy":"MONTH"
		},"links":[],"ssConfLinks":{},"subscriptionUrl":"https://example.com/sub/aBcDeF123"}}`))
	}))
	defer srv.Close()

	info, err := newTestClient(t, srv).SubscriptionInfo(context.Background(), "aBcDeF123", "")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/sub/aBcDeF123/info" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotRealIP != "" {
		t.Errorf("real IP header = %q, want it omitted when no IP is passed", gotRealIP)
	}
	if info.User.Username != "alice" || info.User.UsedBytes() != 10_500_000_000 {
		t.Errorf("info = %+v", info.User)
	}
	if info.SubscriptionURL != "https://example.com/sub/aBcDeF123" {
		t.Errorf("subscriptionUrl = %q", info.SubscriptionURL)
	}
}

func TestSubscriptionInfoEscapesShortUUID(t *testing.T) {
	var gotRawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"response":{"isFound":false}}`))
	}))
	defer srv.Close()

	// These requests carry the panel's admin API token, so an untrusted short
	// UUID must never escape its own path segment.
	traversals := []string{
		"../../system/configuration",
		"..%2f..%2fsystem%2fconfiguration",
		"abc/../../users",
		"abc?x=1",
		"abc#frag",
	}
	for _, attempt := range traversals {
		gotRawPath = ""
		_, err := newTestClient(t, srv).SubscriptionInfo(context.Background(), attempt, "")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("SubscriptionInfo(%q) err = %v, want ErrNotFound with no request made", attempt, err)
		}
		if gotRawPath != "" {
			t.Errorf("SubscriptionInfo(%q) reached the panel at %q", attempt, gotRawPath)
		}
	}

	// A legitimate nanoid still works.
	if _, err := newTestClient(t, srv).SubscriptionInfo(context.Background(), "aBcDeF-123_x", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected error for a valid short UUID: %v", err)
	}
	if gotRawPath != "/api/sub/aBcDeF-123_x/info" {
		t.Errorf("path = %q", gotRawPath)
	}
}

func TestSubscriptionInfoForwardsRealIP(t *testing.T) {
	var gotRealIP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRealIP = r.Header.Get(realIPHeader)
		_, _ = w.Write([]byte(`{"response":{"isFound":true,"user":{},"links":[],"ssConfLinks":{},"subscriptionUrl":""}}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).SubscriptionInfo(context.Background(), "abc", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if gotRealIP != "203.0.113.9" {
		t.Errorf("real IP header = %q", gotRealIP)
	}
}

func TestSubscriptionInfoErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantErr  string
		notFound bool
	}{
		{name: "404", status: http.StatusNotFound, body: "", notFound: true},
		{name: "isFound false", status: http.StatusOK, body: `{"response":{"isFound":false}}`, notFound: true},
		{name: "bad token", status: http.StatusUnauthorized, body: "", wantErr: "REMNAWAVE_API_TOKEN"},
		{name: "forbidden", status: http.StatusForbidden, body: "", wantErr: "REMNAWAVE_API_TOKEN"},
		{name: "server error", status: http.StatusBadGateway, body: "", wantErr: "HTTP 502"},
		{name: "malformed json", status: http.StatusOK, body: "not json", wantErr: "decode"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv).SubscriptionInfo(context.Background(), "abc", "")
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.notFound && !errors.Is(err, ErrNotFound) {
				t.Errorf("err = %v, want ErrNotFound", err)
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/metadata" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"response":{"version":"2.4.0"}}`))
	}))
	defer srv.Close()

	version, err := newTestClient(t, srv).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.4.0" {
		t.Errorf("version = %q", version)
	}
}

func TestHTTPPanelGetsForwardedProtoShim(t *testing.T) {
	var proto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto = r.Header.Get("X-Forwarded-Proto")
		_, _ = w.Write([]byte(`{"response":{"version":"2.4.0"}}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if proto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https for an http:// panel URL", proto)
	}
}
