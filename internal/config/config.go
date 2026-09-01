package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type HTTP struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	// HealthAddr is separate so no request path can shadow a short UUID.
	HealthAddr string
}

type Upstream struct {
	URL       *url.URL
	Timeout   time.Duration
	SubPrefix string
	// TrustProxy keeps the reported client IP from being spoofed via XFF.
	TrustProxy string
}

type Panel struct {
	URL     *url.URL
	Token   string
	Timeout time.Duration
	Enabled bool
	// AlwaysFetch skips the optimisation that avoids the API call entirely.
	AlwaysFetch bool
	// ForwardRealIP puts the proxy's own lookups in the panel request history.
	ForwardRealIP bool

	CaddyAuthToken   string
	CloudflareID     string
	CloudflareSecret string
}

type Cache struct {
	TTL         time.Duration
	NegativeTTL time.Duration
	MaxEntries  int
}

// SubCache trades a stale payload for availability, so it is off by default.
type SubCache struct {
	Enabled bool
	// TTL is how long a stored response stays usable as a fallback.
	TTL time.Duration
	// MaxBytes budgets the cache; MaxBody caps one response.
	MaxBytes int64
	MaxBody  int64
}

type Log struct {
	Level  string
	Format string
}

type Config struct {
	HTTP     HTTP
	Upstream Upstream
	Panel    Panel
	Cache    Cache
	SubCache SubCache
	Log      Log
	File     File

	ConfigPath string
}

func Load() (*Config, error) {
	e := &envError{}

	cfg := &Config{
		HTTP: HTTP{
			Addr: fmt.Sprintf("%s:%d",
				envString("APP_HOST", "0.0.0.0"),
				e.intVal("APP_PORT", 3020),
			),
			ReadTimeout:     e.duration("HTTP_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:    e.duration("HTTP_WRITE_TIMEOUT", 90*time.Second),
			IdleTimeout:     e.duration("HTTP_IDLE_TIMEOUT", 120*time.Second),
			ShutdownTimeout: e.duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Upstream: Upstream{
			Timeout:    e.duration("UPSTREAM_TIMEOUT", 60*time.Second),
			SubPrefix:  strings.Trim(envString("CUSTOM_SUB_PREFIX", ""), "/"),
			TrustProxy: envString("TRUST_PROXY", "1"),
		},
		Panel: Panel{
			Timeout:          e.duration("PANEL_TIMEOUT", 10*time.Second),
			Enabled:          e.boolVal("PANEL_ENABLED", true),
			AlwaysFetch:      e.boolVal("PANEL_ALWAYS_FETCH", false),
			ForwardRealIP:    e.boolVal("PANEL_FORWARD_REAL_IP", true),
			CaddyAuthToken:   envString("CADDY_AUTH_API_TOKEN", ""),
			CloudflareID:     envString("CLOUDFLARE_ZERO_TRUST_CLIENT_ID", ""),
			CloudflareSecret: envString("CLOUDFLARE_ZERO_TRUST_CLIENT_SECRET", ""),
		},
		Cache: Cache{
			TTL:         e.duration("CACHE_TTL", 30*time.Second),
			NegativeTTL: e.duration("CACHE_NEGATIVE_TTL", 10*time.Second),
			MaxEntries:  e.intVal("CACHE_MAX_ENTRIES", 10000),
		},
		SubCache: SubCache{
			Enabled:  e.boolVal("SUBSCRIPTION_CACHE_ENABLED", false),
			TTL:      e.duration("SUBSCRIPTION_CACHE_TTL", time.Hour),
			MaxBytes: e.sizeVal("SUBSCRIPTION_CACHE_MAX_BYTES", 64<<20),
			MaxBody:  e.sizeVal("SUBSCRIPTION_CACHE_MAX_BODY", 1<<20),
		},
		Log: Log{
			Level:  strings.ToLower(envString("LOG_LEVEL", "info")),
			Format: strings.ToLower(envString("LOG_FORMAT", "text")),
		},
	}

	if healthPort := e.intVal("HEALTH_PORT", 3021); healthPort > 0 {
		cfg.HTTP.HealthAddr = fmt.Sprintf("%s:%d", envString("HEALTH_HOST", "0.0.0.0"), healthPort)
	}

	cfg.Upstream.URL = e.parseURL("UPSTREAM_URL", e.requiredString("UPSTREAM_URL"))

	if cfg.Panel.Enabled {
		cfg.Panel.URL = e.parseURL("REMNAWAVE_PANEL_URL", e.requiredString("REMNAWAVE_PANEL_URL"))
		cfg.Panel.Token = e.requiredString("REMNAWAVE_API_TOKEN")
	}

	switch cfg.Log.Format {
	case "text", "json":
	default:
		e.addf("LOG_FORMAT must be text or json, got %q", cfg.Log.Format)
	}
	switch cfg.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		e.addf("LOG_LEVEL must be one of debug, info, warn, error, got %q", cfg.Log.Level)
	}
	if cfg.Cache.MaxEntries < 0 {
		e.addf("CACHE_MAX_ENTRIES must not be negative, got %d", cfg.Cache.MaxEntries)
	}
	if cfg.SubCache.Enabled {
		if cfg.SubCache.TTL <= 0 {
			e.addf("SUBSCRIPTION_CACHE_TTL must be greater than zero when the cache is enabled")
		}
		if cfg.SubCache.MaxBody <= 0 {
			e.addf("SUBSCRIPTION_CACHE_MAX_BODY must be greater than zero when the cache is enabled")
		}
		if cfg.SubCache.MaxBytes < cfg.SubCache.MaxBody {
			e.addf("SUBSCRIPTION_CACHE_MAX_BYTES (%d) must be at least SUBSCRIPTION_CACHE_MAX_BODY (%d)",
				cfg.SubCache.MaxBytes, cfg.SubCache.MaxBody)
		}
	}

	configPath, explicit := lookup("CONFIG_PATH")
	if !explicit {
		configPath = "config.yaml"
	}
	cfg.ConfigPath = configPath

	if err := e.orNil(); err != nil {
		return nil, err
	}

	file, err := loadFile(configPath, explicit)
	if err != nil {
		return nil, err
	}
	cfg.File = file

	return cfg, nil
}

func (e *envError) parseURL(key, raw string) *url.URL {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		e.addf("%s is not a valid URL: %v", key, err)
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		e.addf("%s must start with http:// or https://, got %q", key, raw)
		return nil
	}
	if u.Host == "" {
		e.addf("%s is missing a host, got %q", key, raw)
		return nil
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u
}
