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
		log.Warn("unknown config key ignored; it may need a newer version", "detail", skipped)
	}

	ipResolver, err := realip.Parse(cfg.Upstream.TrustProxy)
	if err != nil {
		return err
	}

	var fetcher rewrite.InfoFetcher
	if cfg.Panel.Enabled {
		client := panel.New(panel.Options{
			BaseURL:          cfg.Panel.URL,
			Token:            cfg.Panel.Token,
			Timeout:          cfg.Panel.Timeout,
			CaddyAuthToken:   cfg.Panel.CaddyAuthToken,
			CloudflareID:     cfg.Panel.CloudflareID,
			CloudflareSecret: cfg.Panel.CloudflareSecret,
		})

		pingCtx, cancel := context.WithTimeout(context.Background(), cfg.Panel.Timeout)
		panelVersion, pingErr := client.Ping(pingCtx)
		cancel()
		if pingErr != nil {
			// Not fatal: everything but panel placeholders keeps flowing.
			log.Error("cannot reach the Remnawave panel; panel-backed placeholders will not resolve until it recovers",
				"panel", cfg.Panel.URL.String(),
				"error", pingErr,
			)
		} else {
			log.Info("connected to Remnawave panel", "panel", cfg.Panel.URL.String(), "panel_version", panelVersion)
		}

		fetcher = panel.NewCache(client, cfg.Cache.TTL, cfg.Cache.NegativeTTL, cfg.Cache.MaxEntries)
	} else {
		log.Warn("panel access disabled; only placeholders answerable from response headers will resolve")
	}

	engine := rewrite.New(rewrite.Options{
		File:          cfg.File,
		Fetcher:       fetcher,
		AlwaysFetch:   cfg.Panel.AlwaysFetch,
		ForwardRealIP: cfg.Panel.ForwardRealIP,
		Logger:        log,
	})

	var subCache *subcache.Cache
	if cfg.SubCache.Enabled {
		subCache = subcache.New(cfg.SubCache.TTL, cfg.SubCache.MaxBytes, cfg.SubCache.MaxBody)
		log.Info("subscription fallback cache enabled",
			"ttl", cfg.SubCache.TTL,
			"max_bytes", cfg.SubCache.MaxBytes,
			"max_body", cfg.SubCache.MaxBody,
		)
	}

	blocker, err := proxy.NewBlocker(cfg.File.Block, cfg.Upstream.SubPrefix)
	if err != nil {
		return err
	}

	shuffleGroups, err := config.CompileHosts(cfg.File.Hosts)
	if err != nil {
		return err
	}
	shuffler := hosts.New(shuffleGroups)
	if shuffler.Enabled() {
		log.Info("host shuffling enabled", "groups", len(shuffleGroups))
	}

	handler := proxy.New(proxy.Options{
		Upstream:   cfg.Upstream.URL,
		SubPrefix:  cfg.Upstream.SubPrefix,
		Timeout:    cfg.Upstream.Timeout,
		Engine:     engine,
		RealIP:     ipResolver,
		Blocker:    blocker,
		SubCache:   subCache,
		Shuffler:   shuffler,
		ForceHTTPS: forceHTTPS(),
		Logger:     log,
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTP.ReadTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}

	var healthServer *http.Server
	if cfg.HTTP.HealthAddr != "" {
		healthServer = &http.Server{
			Addr:              cfg.HTTP.HealthAddr,
			Handler:           healthHandler(cfg.Upstream.URL.Host),
			ReadHeaderTimeout: 5 * time.Second,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		log.Info("listening", "addr", cfg.HTTP.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	if healthServer != nil {
		go func() {
			log.Info("health endpoint listening", "addr", cfg.HTTP.HealthAddr)
			if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("health server: %w", err)
			}
		}()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if healthServer != nil {
		_ = healthServer.Shutdown(shutdownCtx)
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
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

func forceHTTPS() bool {
	v := os.Getenv("UPSTREAM_FORCE_HTTPS")
	return v == "1" || v == "true" || v == "TRUE" || v == "True"
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
