package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
)

func mustBlocker(t *testing.T, c config.Block, subPrefix string) *Blocker {
	t.Helper()
	b, err := NewBlocker(c, subPrefix)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBlockerRefusesProbes(t *testing.T) {
	b := mustBlocker(t, config.Block{Enabled: true}, "")

	// Every path here came from a real scanner sweep against the proxy.
	probes := []string{
		"/.env", "/.git/HEAD", "/.git/config", "/.env.backup", "/.env.old",
		"/.env.save", "/.env.bak", "/.env.prod", "/.env.production",
		"/.env.staging", "/.env.local", "/.env.live", "/.env.dev",
		"/.env.stage",
		"/../.env", "/env", "/config/.env", "/app/.env", "/backend/.env",
		"/api/.env", "/application/.env", "/functions/.env",
		"/wp-admin/setup-config.php", "/phpmyadmin/index.php",
		"/dump.sql", "/backup.bak", "/config.ini", "/id_rsa.key",
		// The upstream normalises these to the same file.
		"/dump.sql/", "/backup.bak/", "/config.ini/", "/id_rsa.key/",
		"/index.php/", "/index.php/x", "/x/config.yml/y",
		// The assets directory is not a way out of the dotfile rule.
		"/assets/../.env", "/assets/../.git/HEAD", "/assets/../../.git/HEAD",
		"/assets/.git/HEAD", "/assets/.aws/credentials", "/assets/.htpasswd",
		"/assets/.env.production",
	}
	for _, p := range probes {
		if !b.Blocked(p) {
			t.Errorf("%q should be refused", p)
		}
	}
}

func TestBlockerAllowsRealTraffic(t *testing.T) {
	b := mustBlocker(t, config.Block{Enabled: true}, "")

	allowed := []string{
		"/",
		"/aBcDeF123456789",
		"/aBcDeF123456789/clash",
		"/aBcDeF123456789/v2ray-json",
		"/sub/aBcDeF123456789",
		"/assets/index-D4f8Ka2b.js",
		"/assets/index-91ab.css",
		"/assets/logo.png",
		"/favicon.ico",
		"/robots.txt",
		// ACME and security.txt live under a dotted segment and are legitimate.
		"/.well-known/acme-challenge/tokenvalue",
		"/.well-known/security.txt",
		// A CA or CDN that does not preserve case must still reach ACME.
		"/.Well-Known/acme-challenge/tokenvalue",
		// The page's own config route is a dotted name inside /assets.
		"/assets/.app-config-v2.json",
		"/assets/app-config.json",
		"/assets/favicon.svg",
	}
	for _, p := range allowed {
		if b.Blocked(p) {
			t.Errorf("%q must not be refused", p)
		}
	}
}

func TestBlockerDisabled(t *testing.T) {
	b := mustBlocker(t, config.Block{
		Enabled:  false,
		Patterns: []string{"(?i)/telescope", ".*"},
	}, "")

	for _, p := range []string{
		"/.env", "/.git/HEAD", "/../.env", "/env",
		"/dump.sql", "/wp-admin/setup-config.php",
		"/telescope/requests", "/anything-at-all",
	} {
		if b.Blocked(p) {
			t.Errorf("%q must pass through with the blocker disabled", p)
		}
	}

	if (*Blocker)(nil).Blocked("/.env") {
		t.Error("a nil blocker must refuse nothing")
	}
}

func TestDisabledBlockerForwardsProbes(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte("upstream answered"))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newProxyWithBlocker(t, upstream.URL, mustBlocker(t, config.Block{Enabled: false}, "")))
	defer front.Close()

	resp, err := front.Client().Get(front.URL + "/.env")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 from the upstream", resp.StatusCode)
	}
	if got != "/.env" {
		t.Errorf("upstream saw %q, want the probe forwarded", got)
	}
}

func TestBlockerExtraPatterns(t *testing.T) {
	b := mustBlocker(t, config.Block{Enabled: true, Patterns: []string{"(?i)/telescope"}}, "")

	if !b.Blocked("/Telescope/requests") {
		t.Error("an extra pattern should be applied")
	}
	if b.Blocked("/aBcDeF123456789") {
		t.Error("an extra pattern must not catch a subscription")
	}
}

func TestNewBlockerRejectsBadPattern(t *testing.T) {
	if _, err := NewBlocker(config.Block{Enabled: true, Patterns: []string{"("}}, ""); err == nil {
		t.Error("an invalid pattern must be reported, not ignored")
	}
}

func TestBlockedProbeNeverReachesUpstream(t *testing.T) {
	reached := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		_, _ = w.Write([]byte("should not happen"))
	}))
	defer upstream.Close()

	front := httptest.NewServer(newProxyWithBlocker(t, upstream.URL, mustBlocker(t, config.Block{Enabled: true}, "")))
	defer front.Close()

	resp, err := front.Client().Get(front.URL + "/.env")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if reached != 0 {
		t.Errorf("upstream was contacted %d times, want 0", reached)
	}
}

// Names are weighed only after the prefix is stripped, so a listed word may be the prefix.
func TestBlockerRespectsSubPrefix(t *testing.T) {
	b := mustBlocker(t, config.Block{Enabled: true}, "admin")

	if b.Blocked("/admin/aBcDeF123456789") {
		t.Error("a subscription under the configured prefix must pass")
	}
	if b.Blocked("/admin/aBcDeF123456789/clash") {
		t.Error("a client-type path under the prefix must pass")
	}
	if b.Blocked("/admin/assets/.app-config-v2.json") {
		t.Error("the page's config route under the prefix must pass")
	}
	if !b.Blocked("/admin/wp-login") {
		t.Error("a probe after the prefix should still be refused")
	}
	if !mustBlocker(t, config.Block{Enabled: true}, "sub").Blocked("/admin") {
		t.Error("admin is not the prefix here and should be refused")
	}
}

// The router matches the prefix exactly, so the blocker must not fold its case.
func TestBlockerPrefixMatchesRouter(t *testing.T) {
	const prefix = "admin"
	b := mustBlocker(t, config.Block{Enabled: true}, prefix)

	for _, p := range []string{"/ADMIN/aBcDeF123456789", "/Admin/aBcDeF123456789"} {
		if route := ParseRoute(p, prefix); route.ShortUUID != "" {
			t.Fatalf("ParseRoute(%q) = %q, want no subscription", p, route.ShortUUID)
		}
		if !b.Blocked(p) {
			t.Errorf("%q names no subscription to the router, so the blocker must not exempt it", p)
		}
	}
}

func TestBlockerMultiSegmentPrefix(t *testing.T) {
	b := mustBlocker(t, config.Block{Enabled: true}, "api/sub")

	if b.Blocked("/api/sub/aBcDeF123456789") {
		t.Error("a subscription under a two-segment prefix must pass")
	}
	if !b.Blocked("/api/sub/wp-login") {
		t.Error("a probe after a two-segment prefix should still be refused")
	}
}
