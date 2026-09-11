package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// IANA timezone database, for datetime.timezone in a minimal image.
	_ "time/tzdata"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/hosts"
	"github.com/hteppl/remnawave-subpage-proxy/internal/logging"
	"github.com/hteppl/remnawave-subpage-proxy/internal/panel"
	"github.com/hteppl/remnawave-subpage-proxy/internal/proxy"
	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
	"github.com/hteppl/remnawave-subpage-proxy/internal/version"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		healthcheck = flag.Bool("healthcheck", false, "probe the local health endpoint and exit (used by Docker HEALTHCHECK)")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("remnawave-subpage-proxy %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return
	}
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(log)

	log.Info("starting remnawave-subpage-proxy",
		"version", version.Version,
		"commit", version.Commit,
		"listen", cfg.HTTP.Addr,
		"upstream", cfg.Upstream.URL.String(),
		"sub_prefix", orNone(cfg.Upstream.SubPrefix),
		"config", cfg.ConfigPath,
		"header_rules", len(cfg.File.Headers),
		"scan_all_headers", cfg.File.Template.ScanAllHeaders,
	)
	for _, skipped := range cfg.Skipped {
		log.Warn("unknown config section ignored; it may need a newer version", "detail", skipped)
	}

	handler, err := newHandler(cfg, log)
	if err != nil {
		return err
	}

	servers := []*http.Server{{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTP.ReadTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}}
	if cfg.HTTP.HealthAddr != "" {
		servers = append(servers, &http.Server{
			Addr:              cfg.HTTP.HealthAddr,
			Handler:           healthHandler(cfg.Upstream.URL.Host),
			ReadHeaderTimeout: 5 * time.Second,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
		})
	}
	return serve(log, cfg.HTTP.ShutdownTimeout, servers...)
}

func newHandler(cfg *config.Config, log *slog.Logger) (http.Handler, error) {
	ipResolver, err := realip.Parse(cfg.Upstream.TrustProxy)
	if err != nil {
		return nil, err
	}
	blocker, err := proxy.NewBlocker(cfg.File.Block, cfg.Upstream.SubPrefix)
	if err != nil {
		return nil, err
	}
	shuffleGroups, err := config.CompileHosts(cfg.File.Hosts)
	if err != nil {
		return nil, err
	}
	userAgents, err := proxy.NewUserAgentFilter(cfg.File.UserAgents)
	if err != nil {
		return nil, err
	}

	var subCache *subcache.Cache
	if cfg.SubCache.Enabled {
		subCache = subcache.New(cfg.SubCache.TTL, cfg.SubCache.MaxBytes, cfg.SubCache.MaxBody)
		log.Info("subscription fallback cache enabled",
			"ttl", cfg.SubCache.TTL,
			"max_bytes", cfg.SubCache.MaxBytes,
			"max_body", cfg.SubCache.MaxBody,
		)
	}
	shuffler := hosts.New(shuffleGroups)
	if shuffler.Enabled() {
		log.Info("host shuffling enabled", "groups", len(shuffleGroups))
	}
	for _, rule := range userAgents.Disabled() {
		log.Warn("user-agent rule disabled: it needs a message to show the user", "rule", rule)
	}
	if userAgents.Enabled() {
		log.Info("user-agent filter enabled", "rules", userAgents.Len())
	}

	return proxy.New(proxy.Options{
		Upstream:  cfg.Upstream.URL,
		SubPrefix: cfg.Upstream.SubPrefix,
		Timeout:   cfg.Upstream.Timeout,
		Engine: rewrite.New(rewrite.Options{
			File:          cfg.File,
			Fetcher:       newFetcher(cfg, log),
			AlwaysFetch:   cfg.Panel.AlwaysFetch,
			ForwardRealIP: cfg.Panel.ForwardRealIP,
			Logger:        log,
		}),
		RealIP:     ipResolver,
		Blocker:    blocker,
		SubCache:   subCache,
		Shuffler:   shuffler,
		UserAgents: userAgents,
		ForceHTTPS: cfg.Upstream.ForceHTTPS,
		Logger:     log,
	}), nil
}

// newFetcher returns nil when panel access is off; an unreachable panel is not fatal.
func newFetcher(cfg *config.Config, log *slog.Logger) rewrite.InfoFetcher {
	if !cfg.Panel.Enabled {
		log.Warn("panel access disabled; only placeholders answerable from response headers will resolve")
		return nil
	}
	client := panel.New(panel.Options{
		BaseURL:          cfg.Panel.URL,
		Token:            cfg.Panel.Token,
		Timeout:          cfg.Panel.Timeout,
		CaddyAuthToken:   cfg.Panel.CaddyAuthToken,
		CloudflareID:     cfg.Panel.CloudflareID,
		CloudflareSecret: cfg.Panel.CloudflareSecret,
	})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Panel.Timeout)
	defer cancel()
	if panelVersion, err := client.Ping(ctx); err != nil {
		log.Error("cannot reach the Remnawave panel; panel-backed placeholders will not resolve until it recovers",
			"panel", cfg.Panel.URL.String(),
			"error", err,
		)
	} else {
		log.Info("connected to Remnawave panel", "panel", cfg.Panel.URL.String(), "panel_version", panelVersion)
	}
	return panel.NewCache(client, cfg.Cache.TTL, cfg.Cache.NegativeTTL, cfg.Cache.MaxEntries)
}

func serve(log *slog.Logger, shutdownTimeout time.Duration, servers ...*http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		go func() {
			log.Info("listening", "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("server %s: %w", srv.Addr, err)
			}
		}()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Reverse order: health stops first, and the public server's error wins.
	var firstErr error
	for i := len(servers) - 1; i >= 0; i-- {
		if err := servers[i].Shutdown(shutdownCtx); err != nil {
			firstErr = fmt.Errorf("graceful shutdown: %w", err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	log.Info("stopped")
	return nil
}

func healthHandler(upstreamHost string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		host := upstreamHost
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, "80")
		}
		conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(r.Context(), "tcp", host)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "upstream unreachable: %v\n", err)
			return
		}
		_ = conn.Close()
		_, _ = w.Write([]byte("ok\n"))
	})

	return mux
}

// runHealthcheck lets the image probe itself, needing no shell or curl.
func runHealthcheck() int {
	port := os.Getenv("HEALTH_PORT")
	if port == "" {
		port = "3021"
	}
	if port == "0" {
		return 0
	}
	url := "http://127.0.0.1:" + port + "/healthz"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: "+err.Error())
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
