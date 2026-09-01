package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileDefaults(t *testing.T) {
	cfg, err := loadFile(filepath.Join(t.TempDir(), "absent.yaml"), false)
	if err != nil {
		t.Fatalf("a missing config at the default path must not be an error: %v", err)
	}
	if !cfg.Template.ScanAllHeaders {
		t.Error("scan_all_headers should default to true")
	}
	if cfg.Traffic.Decimals != 2 || cfg.Traffic.Unlimited != "∞" {
		t.Errorf("unexpected traffic defaults: %+v", cfg.Traffic)
	}
	if cfg.DateTime.Location().String() != "UTC" {
		t.Errorf("timezone default = %s", cfg.DateTime.Location())
	}
}

func TestLoadFileMissingExplicitPathIsAnError(t *testing.T) {
	if _, err := loadFile(filepath.Join(t.TempDir(), "absent.yaml"), true); err == nil {
		t.Error("an explicitly configured CONFIG_PATH that does not exist must fail loudly")
	}
}

func TestLoadFileFull(t *testing.T) {
	path := writeConfig(t, `
traffic:
  decimals: 1
  binary_units: true
  unlimited: "unlimited"

datetime:
  layout: "2006-01-02"
  timezone: "Europe/Moscow"

vars:
  BRAND: "MyVPN"

headers:
  - name: announce
    template: "{BRAND}: {TRAFFIC_USED} / {TRAFFIC_AVAILABLE}"
    encode: base64-prefixed
    max_length: 200
    when:
      client_types: [CLASH, json]
      user_statuses: [active]
      user_agent: "Happ"
`)

	cfg, err := loadFile(path, true)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Traffic.Decimals != 1 || !cfg.Traffic.BinaryUnits || cfg.Traffic.Unlimited != "unlimited" {
		t.Errorf("traffic = %+v", cfg.Traffic)
	}
	if cfg.DateTime.Location().String() != "Europe/Moscow" {
		t.Errorf("timezone = %s", cfg.DateTime.Location())
	}
	if len(cfg.Headers) != 1 {
		t.Fatalf("headers = %d, want 1", len(cfg.Headers))
	}

	rule := cfg.Headers[0]
	if rule.Encode != EncodeBase64Prefixed {
		t.Errorf("encode = %q", rule.Encode)
	}
	if rule.When.ClientTypes[0] != "clash" || rule.When.UserStatuses[0] != "ACTIVE" {
		t.Errorf("conditions were not normalised: %+v", rule.When)
	}
	if rule.When.UserAgentRegexp() == nil || !rule.When.UserAgentRegexp().MatchString("Happ/1.0") {
		t.Error("user_agent regexp did not compile")
	}
}

func TestLoadFileRejectsBadInput(t *testing.T) {
	tests := map[string]string{
		"unknown key":        "traffic:\n  decimals: 2\nnope: 1\n",
		"bad encode":         "headers:\n  - name: announce\n    encode: rot13\n",
		"bad timezone":       "datetime:\n  timezone: Mars/Olympus\n",
		"missing name":       "headers:\n  - template: hi\n",
		"duplicate name":     "headers:\n  - name: announce\n    template: a\n  - name: Announce\n    template: b\n",
		"remove + template":  "headers:\n  - name: announce\n    remove: true\n    template: hi\n",
		"bad user_agent":     "headers:\n  - name: announce\n    when:\n      user_agent: \"[\"\n",
		"bad var name":       "vars:\n  lower_case: x\n",
		"decimals too large": "traffic:\n  decimals: 99\n",
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFile(writeConfig(t, body), true); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		})
	}
}

func TestLoadEnvValidation(t *testing.T) {
	// All required vars missing: the report should name every one at once.
	for _, key := range []string{"UPSTREAM_URL", "REMNAWAVE_PANEL_URL", "REMNAWAVE_API_TOKEN"} {
		t.Setenv(key, "")
	}
	_, err := Load()
	if err == nil {
		t.Fatal("expected missing required variables to fail")
	}
	for _, want := range []string{"UPSTREAM_URL", "REMNAWAVE_PANEL_URL", "REMNAWAVE_API_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s:\n%v", want, err)
		}
	}
}

func TestLoadEnvHappyPath(t *testing.T) {
	t.Setenv("UPSTREAM_URL", "http://remnawave-subscription-page:3010/")
	t.Setenv("REMNAWAVE_PANEL_URL", "http://remnawave:3000")
	t.Setenv("REMNAWAVE_API_TOKEN", "token")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("CUSTOM_SUB_PREFIX", "/sub/")
	t.Setenv("CACHE_TTL", "45")
	t.Setenv("CONFIG_PATH", writeConfig(t, "traffic:\n  decimals: 0\n"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != "0.0.0.0:8080" {
		t.Errorf("addr = %q", cfg.HTTP.Addr)
	}
	if cfg.Upstream.URL.String() != "http://remnawave-subscription-page:3010" {
		t.Errorf("upstream = %q, want the trailing slash trimmed", cfg.Upstream.URL)
	}
	if cfg.Upstream.SubPrefix != "sub" {
		t.Errorf("sub prefix = %q, want slashes trimmed", cfg.Upstream.SubPrefix)
	}
	if cfg.Cache.TTL.Seconds() != 45 {
		t.Errorf("cache ttl = %v, want a bare number to be read as seconds", cfg.Cache.TTL)
	}
	if cfg.File.Traffic.Decimals != 0 {
		t.Errorf("the YAML file was not applied: %+v", cfg.File.Traffic)
	}
}

func TestLoadEnvRejectsBadURL(t *testing.T) {
	t.Setenv("UPSTREAM_URL", "remnawave-subscription-page:3010")
	t.Setenv("REMNAWAVE_PANEL_URL", "http://remnawave:3000")
	t.Setenv("REMNAWAVE_API_TOKEN", "token")

	if _, err := Load(); err == nil {
		t.Error("a URL without a scheme should be rejected")
	}
}
