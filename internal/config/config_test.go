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
	cfg, _, err := loadFile(filepath.Join(t.TempDir(), "absent.yaml"), false)
	if err != nil {
		t.Fatalf("a missing config at the default path must not be an error: %v", err)
	}
	if !cfg.Template.ScanAllHeaders {
		t.Error("scan_all_headers should default to true")
	}
	if cfg.Traffic.Decimals != 2 || cfg.Traffic.Unlimited != "∞" || !cfg.Traffic.BinaryUnits {
		t.Errorf("unexpected traffic defaults: %+v", cfg.Traffic)
	}
	if cfg.DateTime.Location().String() != "UTC" {
		t.Errorf("timezone default = %s", cfg.DateTime.Location())
	}
}

func TestLoadFileMissingExplicitPathIsAnError(t *testing.T) {
	if _, _, err := loadFile(filepath.Join(t.TempDir(), "absent.yaml"), true); err == nil {
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
  BRAND: "MyProject"

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

	cfg, _, err := loadFile(path, true)
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

// A key this binary does not know must not stop it: a config written for a
// newer version has to keep an older image running.
func TestLoadFileSkipsUnknownKeys(t *testing.T) {
	cfg, skipped, err := loadFile(writeConfig(t, `traffic:
  decimals: 3
  nope: 1
vars:
  BRAND: "MyProject"
hosts_from_the_future:
  shuffle:
    - "(?i)premium"
`), true)
	if err != nil {
		t.Fatalf("an unknown key must not be fatal: %v", err)
	}
	if cfg.Traffic.Decimals != 3 || cfg.Vars["BRAND"] != "MyProject" {
		t.Errorf("known keys must still be applied: %+v", cfg.Traffic)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %v, want both unknown keys", skipped)
	}
	for _, want := range []string{"nope", "hosts_from_the_future"} {
		if !strings.Contains(strings.Join(skipped, "\n"), want) {
			t.Errorf("skipped should name %q: %v", want, skipped)
		}
	}
}

// Skipping unknown keys must not swallow a genuine mistake in a known one.
func TestLoadFileStillRejectsBadValuesBesideUnknownKeys(t *testing.T) {
	_, _, err := loadFile(writeConfig(t, "traffic:\n  decimals: nope\nfuture_key: 1\n"), true)
	if err == nil {
		t.Fatal("a bad value must stay fatal even next to an unknown key")
	}
	if strings.Contains(err.Error(), "future_key") {
		t.Errorf("the unknown key must not be reported as the failure: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("error should point at the bad value: %v", err)
	}
}

func TestLoadFileRejectsBadInput(t *testing.T) {
	tests := map[string]string{
		"bad encode":         "headers:\n  - name: announce\n    encode: rot13\n",
		"bad timezone":       "datetime:\n  timezone: Mars/Olympus\n",
		"missing name":       "headers:\n  - template: hi\n",
		"remove + template":  "headers:\n  - name: announce\n    remove: true\n    template: hi\n",
		"bad user_agent":     "headers:\n  - name: announce\n    when:\n      user_agent: \"[\"\n",
		"bad var name":       "vars:\n  lower_case: x\n",
		"decimals too large": "traffic:\n  decimals: 99\n",
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := loadFile(writeConfig(t, body), true); err == nil {
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

// Several rules may target one header, each scoped by its own conditions.
func TestLoadFileAllowsSeveralRulesPerHeader(t *testing.T) {
	cfg, _, err := loadFile(writeConfig(t, `
headers:
  - name: announce
    template: "limited"
    when:
      has_traffic_limit: true
  - name: announce
    template: "unlimited"
    when:
      has_traffic_limit: false
`), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Headers) != 2 {
		t.Fatalf("headers = %d, want 2", len(cfg.Headers))
	}
	if cfg.Headers[0].When.HasTrafficLimit == nil || !*cfg.Headers[0].When.HasTrafficLimit {
		t.Error("has_traffic_limit: true was not parsed")
	}
	if cfg.Headers[1].When.HasTrafficLimit == nil || *cfg.Headers[1].When.HasTrafficLimit {
		t.Error("has_traffic_limit: false was not parsed")
	}
}

// The shipped examples must always load; they are documentation users copy.
func TestShippedConfigsAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, filepath.Join("..", "..", "config.example.yaml"))
	if len(paths) < 2 {
		t.Fatal("no example configs found")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, _, err := loadFile(path, true); err != nil {
				t.Errorf("%s does not load: %v", path, err)
			}
		})
	}
}

// An unconditional rule shadows every later rule for the same header, which is
// silent dead config without this check.
func TestLoadFileRejectsUnreachableRules(t *testing.T) {
	_, _, err := loadFile(writeConfig(t, `
headers:
  - name: announce
    template: "always"
  - name: announce
    template: "never reached"
    when:
      has_traffic_limit: true
`), true)
	if err == nil {
		t.Fatal("an unreachable rule should be rejected")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should explain the problem: %v", err)
	}
}

func TestLoadFileAllowsConditionalRulesThenFallback(t *testing.T) {
	if _, _, err := loadFile(writeConfig(t, `
headers:
  - name: announce
    template: "limited"
    when:
      has_traffic_limit: true
  - name: announce
    template: "unlimited"
    when:
      has_traffic_limit: false
  - name: announce
    template: "fallback"
`), true); err != nil {
		t.Fatalf("specific-to-general ordering must be accepted: %v", err)
	}
}

// binary_units defaults to true, so an explicit false has to survive decoding
// into an already-populated struct.
func TestBinaryUnitsCanBeDisabled(t *testing.T) {
	cfg, _, err := loadFile(writeConfig(t, "traffic:\n  binary_units: false\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Traffic.BinaryUnits {
		t.Error("binary_units: false was ignored")
	}
}

// Blocking is on unless it is turned off, including for a deployment whose
// config.yaml predates the setting or has no file at all.
func TestBlockDefaultsToEnabled(t *testing.T) {
	files := map[string]string{
		"no file":       "",
		"no block key":  "traffic:\n  decimals: 1\n",
		"empty section": "block:\n",
		"patterns only": "block:\n  patterns:\n    - \"(?i)/telescope\"\n",
	}
	for name, body := range files {
		t.Run(name, func(t *testing.T) {
			var (
				cfg File
				err error
			)
			if body == "" {
				cfg, _, err = loadFile(filepath.Join(t.TempDir(), "absent.yaml"), false)
			} else {
				cfg, _, err = loadFile(writeConfig(t, body), true)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.Block.Enabled {
				t.Error("block.enabled must stay true unless it is set to false")
			}
		})
	}
}

func TestBlockCanBeDisabledInFile(t *testing.T) {
	cfg, _, err := loadFile(writeConfig(t, "block:\n  enabled: false\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Block.Enabled {
		t.Error("block.enabled: false must be honoured")
	}
}

// Every bad pattern is named, not just the first.
func TestBlockRejectsInvalidPatterns(t *testing.T) {
	_, _, err := loadFile(writeConfig(t, "block:\n  patterns:\n    - \"(\"\n    - \"[a-\"\n"), true)
	if err == nil {
		t.Fatal("an invalid regexp must fail the config")
	}
	for _, want := range []string{"block.patterns[0]", "block.patterns[1]", "not a valid regexp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s:\n%v", want, err)
		}
	}
}

func TestCompileBlock(t *testing.T) {
	compiled, err := CompileBlock(Block{Patterns: []string{"(?i)/telescope", "^/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 2 {
		t.Fatalf("compiled %d patterns, want 2", len(compiled))
	}
	if !compiled[0].MatchString("/Telescope") {
		t.Error("the compiled pattern should match")
	}
	if _, err := CompileBlock(Block{Patterns: []string{"("}}); err == nil {
		t.Error("an invalid pattern must be an error")
	}
}

func TestHostsShufflePatterns(t *testing.T) {
	cfg, _, err := loadFile(writeConfig(t, `hosts:
  shuffle:
    - "^RU \\d+"
    - "(?i)premium"
`), true)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileHosts(cfg.Hosts)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 2 {
		t.Fatalf("compiled %d groups, want 2", len(compiled))
	}
	if !compiled[0].MatchString("RU 12") || compiled[0].MatchString("ru12.example.com") {
		t.Error("a pattern must match the name shown to the user")
	}
	if !compiled[1].MatchString("Premium 1") {
		t.Error("the second pattern must match its own name")
	}

	if compiled, err := CompileHosts(Hosts{}); err != nil || len(compiled) != 0 {
		t.Errorf("no patterns must compile to nothing: %v %v", compiled, err)
	}

	_, _, err = loadFile(writeConfig(t, "hosts:\n  shuffle:\n    - \"(\"\n    - \"\"\n"), true)
	if err == nil {
		t.Fatal("a bad pattern must fail the config")
	}
	for _, want := range []string{
		"hosts.shuffle[0] is not a valid regexp",
		"hosts.shuffle[1] needs a name pattern",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s:\n%v", want, err)
		}
	}
}
