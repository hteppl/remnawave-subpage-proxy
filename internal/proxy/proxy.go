package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
)

type contextKey struct{}

// requestInfo reaches ModifyResponse through the request context.
type requestInfo struct {
	route           Route
	clientIP        string
	userAgent       string
	subscriptionURL string
	cacheKey        string
}

type Options struct {
	Upstream  *url.URL
	SubPrefix string
	Timeout   time.Duration
	Engine    *rewrite.Engine
	RealIP    *realip.Resolver
	// SubCache replays the last good response while the upstream is down.
	SubCache *subcache.Cache
	// ForceHTTPS claims TLS termination to an upstream that demands it.
	ForceHTTPS bool
	Logger     *slog.Logger
}

type Proxy struct {
	rp         *httputil.ReverseProxy
	subPrefix  string
	realIP     *realip.Resolver
	subCache   *subcache.Cache
	forceHTTPS bool
	log        *slog.Logger
}

func New(o Options) *Proxy {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	p := &Proxy{
		subPrefix:  o.SubPrefix,
		realIP:     o.RealIP,
		subCache:   o.SubCache,
		forceHTTPS: o.ForceHTTPS,
		log:        log,
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   128,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
		ForceAttemptHTTP2:     true,
	}

	p.rp = &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(o.Upstream)
			// Keep the public hostname in any URL the page builds.
			r.Out.Host = r.In.Host
			forwardHeaders(r, o.ForceHTTPS)
		},
		ModifyResponse: func(resp *http.Response) error {
			info, ok := resp.Request.Context().Value(contextKey{}).(*requestInfo)
			if !ok {
				return nil
			}

			if p.replaceWithCache(resp, info) {
				return nil
			}

			if o.Engine != nil && o.Engine.Enabled() {
				o.Engine.Apply(resp.Request.Context(), resp.Header, rewrite.Request{
					ShortUUID:       info.route.ShortUUID,
					ClientType:      info.route.ClientType,
					UserAgent:       info.userAgent,
					ClientIP:        info.clientIP,
					SubscriptionURL: info.subscriptionURL,
				})
			}

			p.store(resp, info)
			return nil
		},
		ErrorHandler: p.handleError,
		ErrorLog:     slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}

	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := ParseRoute(r.URL.Path, p.subPrefix)
	userAgent := r.Header.Get("User-Agent")

	info := &requestInfo{
		route:           route,
		clientIP:        p.realIP.ClientIP(r),
		userAgent:       userAgent,
		subscriptionURL: p.subscriptionURL(r, route.ShortUUID),
	}
	if p.subCache != nil && route.ShortUUID != "" {
		info.cacheKey = subcache.Key(
			route.ShortUUID,
			route.ClientType,
			userAgent,
			r.Header.Get("Accept-Encoding"),
		)
	}

	p.rp.ServeHTTP(
		&lowercaseHeaderWriter{ResponseWriter: w},
		r.WithContext(context.WithValue(r.Context(), contextKey{}, info)),
	)
}

// handleError falls back to the cache, else drops the connection without a
// response, as the subscription page itself does.
func (p *Proxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}

	info, ok := r.Context().Value(contextKey{}).(*requestInfo)
	if ok && p.writeFromCache(w, info) {
		p.log.Warn("upstream unreachable, served subscription from cache",
			"short_uuid", info.route.ShortUUID,
			"error", err,
		)
		return
	}

	// A bare EOF means the upstream closed without answering, which is how the
	// subscription page refuses anything it will not serve: a scanner probing
	// for /.env, an unknown path, a revoked link. Routine, not a fault.
	level := slog.LevelWarn
	if errors.Is(err, io.EOF) {
		level = slog.LevelDebug
	}
	p.log.Log(r.Context(), level, "upstream request failed",
		"path", r.URL.Path,
		"error", err,
	)

	if hijacker, ok := w.(http.Hijacker); ok {
		if conn, _, hijackErr := hijacker.Hijack(); hijackErr == nil {
			_ = conn.Close()
			return
		}
	}
	w.WriteHeader(http.StatusBadGateway)
}

func (p *Proxy) writeFromCache(w http.ResponseWriter, info *requestInfo) bool {
	if p.subCache == nil || info.cacheKey == "" {
		return false
	}
	entry, ok := p.subCache.Get(info.cacheKey)
	if !ok {
		return false
	}

	header := w.Header()
	for key, values := range entry.Header {
		header[key] = append([]string(nil), values...)
	}
	header.Set("Content-Length", strconv.Itoa(len(entry.Body)))
	w.WriteHeader(entry.Status)
	_, _ = w.Write(entry.Body)
	return true
}

// replaceWithCache covers a reachable page with a dead panel behind it.
func (p *Proxy) replaceWithCache(resp *http.Response, info *requestInfo) bool {
	if p.subCache == nil || info.cacheKey == "" || resp.StatusCode < 500 {
		return false
	}
	entry, ok := p.subCache.Get(info.cacheKey)
	if !ok {
		return false
	}

	_ = resp.Body.Close()
	resp.StatusCode = entry.Status
	resp.Status = strconv.Itoa(entry.Status) + " " + http.StatusText(entry.Status)
	resp.Header = entry.Header.Clone()
	resp.Body = io.NopCloser(bytes.NewReader(entry.Body))
	resp.ContentLength = int64(len(entry.Body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(entry.Body)))
	resp.TransferEncoding = nil

	p.log.Warn("upstream returned an error, served subscription from cache",
		"short_uuid", info.route.ShortUUID,
		"upstream_status", resp.StatusCode,
	)
	return true
}

// store keeps the finished response for replay, skipping the web page.
func (p *Proxy) store(resp *http.Response, info *requestInfo) {
	if p.subCache == nil || info.cacheKey == "" || resp.StatusCode != http.StatusOK {
		return
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return
	}

	body, ok := drainBody(resp, p.subCache.MaxBody())
	if !ok {
		return
	}

	p.subCache.Put(info.cacheKey, &subcache.Entry{
		Status: resp.StatusCode,
		Header: resp.Header.Clone(),
		Body:   body,
	})
}

// drainBody buffers the response so it can be cached and still delivered.
// An oversized body is left streaming and never held in memory.
func drainBody(resp *http.Response, maxBody int64) ([]byte, bool) {
	if resp.Body == nil {
		return nil, false
	}

	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || int64(len(buf)) > maxBody {
		resp.Body = readCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), resp.Body),
			Closer: resp.Body,
		}
		return nil, false
	}

	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	return buf, true
}

type readCloser struct {
	io.Reader
	io.Closer
}

// subscriptionURL rebuilds the link from the request, so {SUBSCRIPTION_URL}
// costs no panel call. The client-type segment is dropped on purpose.
func (p *Proxy) subscriptionURL(r *http.Request, shortUUID string) string {
	if shortUUID == "" {
		return ""
	}

	host := firstListValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return ""
	}

	scheme := firstListValue(r.Header.Get("X-Forwarded-Proto"))
	switch {
	case p.forceHTTPS:
		scheme = "https"
	case scheme == "":
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}

	path := "/" + url.PathEscape(shortUUID)
	if p.subPrefix != "" {
		path = "/" + p.subPrefix + path
	}
	return scheme + "://" + host + path
}

func firstListValue(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

// forwardHeaders maintains the X-Forwarded-* chain. ReverseProxy's Rewrite hook
// deletes these first, so forwarding them has to be explicit.
func forwardHeaders(r *httputil.ProxyRequest, forceHTTPS bool) {
	inbound := strings.TrimSpace(strings.Join(r.In.Header.Values("X-Forwarded-For"), ", "))
	peer := realip.PeerIP(r.In)
	switch {
	case inbound != "" && peer != "":
		r.Out.Header.Set("X-Forwarded-For", inbound+", "+peer)
	case inbound != "":
		r.Out.Header.Set("X-Forwarded-For", inbound)
	case peer != "":
		r.Out.Header.Set("X-Forwarded-For", peer)
	}

	proto := r.In.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
		if r.In.TLS != nil {
			proto = "https"
		}
	}
	if forceHTTPS {
		proto = "https"
	}
	r.Out.Header.Set("X-Forwarded-Proto", proto)

	host := r.In.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.In.Host
	}
	if host != "" {
		r.Out.Header.Set("X-Forwarded-Host", host)
	}
}
