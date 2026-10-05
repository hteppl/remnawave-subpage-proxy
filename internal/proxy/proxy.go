package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/hosts"
	"github.com/hteppl/remnawave-subpage-proxy/internal/realip"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
)

type contextKey struct{}

// bufferPool reuses the copy buffers ReverseProxy would otherwise allocate,
// 32 KiB for every request.
type bufferPool struct{ pool sync.Pool }

func newBufferPool() *bufferPool {
	return &bufferPool{pool: sync.Pool{New: func() any {
		buf := make([]byte, 32<<10)
		return &buf
	}}}
}

func (b *bufferPool) Get() []byte    { return *b.pool.Get().(*[]byte) }
func (b *bufferPool) Put(buf []byte) { b.pool.Put(&buf) }

func infoFrom(r *http.Request) (*requestInfo, bool) {
	info, ok := r.Context().Value(contextKey{}).(*requestInfo)
	return info, ok
}

type requestInfo struct {
	// Its short UUID is empty for any path that names no subscription.
	rq       rewrite.Request
	cacheKey string
	// notice is the user-agent rule that swaps this response's hosts.
	notice *config.CompiledUserAgentRule
}

type Options struct {
	Upstream  *url.URL
	SubPrefix string
	Timeout   time.Duration
	Engine    *rewrite.Engine
	RealIP    *realip.Resolver
	Blocker   *Blocker
	// SubCache replays the last good response while the upstream is down.
	SubCache *subcache.Cache
	Shuffler *hosts.Shuffler
	// UserAgents refuses or annotates subscriptions fetched with a broken User-Agent.
	UserAgents *UserAgentFilter
	// ForceHTTPS claims TLS termination to an upstream that demands it.
	ForceHTTPS bool
	Logger     *slog.Logger
}

type Proxy struct {
	rp         *httputil.ReverseProxy
	blocker    *Blocker
	subPrefix  string
	realIP     *realip.Resolver
	subCache   *subcache.Cache
	shuffler   *hosts.Shuffler
	userAgents *UserAgentFilter
	engine     *rewrite.Engine
	forceHTTPS bool
	log        *slog.Logger

	// rewrites: engine enabled; observes: any response stage enabled.
	rewrites bool
	observes bool
}

func New(o Options) *Proxy {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	p := &Proxy{
		blocker:    o.Blocker,
		subPrefix:  o.SubPrefix,
		realIP:     o.RealIP,
		subCache:   o.SubCache,
		shuffler:   o.Shuffler,
		userAgents: o.UserAgents,
		engine:     o.Engine,
		forceHTTPS: o.ForceHTTPS,
		log:        log,
		rewrites:   o.Engine != nil && o.Engine.Enabled(),
	}
	p.observes = p.rewrites || p.subCache != nil || p.shuffler.Enabled() || p.userAgents.Enabled()

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
		// Stops the transport from re-adding gzip after the shuffler strips it.
		DisableCompression: true,
	}

	p.rp = &httputil.ReverseProxy{
		Transport:  transport,
		BufferPool: newBufferPool(),
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(o.Upstream)
			// Keep the public hostname in any URL the page builds.
			r.Out.Host = r.In.Host
			forwardHeaders(r, o.RealIP, o.ForceHTTPS)
			// A body to be rewritten must arrive uncompressed.
			if info, ok := infoFrom(r.In); ok && (p.shuffles(info) || info.notice != nil) {
				r.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			info, ok := infoFrom(resp.Request)
			if !ok {
				return nil
			}

			if p.replaceWithCache(resp, info) {
				p.shuffle(resp, info)
				return nil
			}

			ctx := resp.Request.Context()
			// Rendered first: Apply may hide the quota the message reports.
			var message string
			if info.notice != nil {
				message = p.noticeMessage(ctx, resp.Header, info.rq, info.notice)
			}

			if p.rewrites {
				p.engine.Apply(ctx, resp.Header, info.rq)
			}

			// Nothing to shuffle, and never cached: the next fetch may carry a repaired agent.
			if info.notice != nil {
				p.applyNotice(resp, info, message)
				return nil
			}

			p.shuffle(resp, info)
			p.store(resp, info)
			return nil
		},
		ErrorHandler: p.handleError,
		ErrorLog:     slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}

	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.blocker.Blocked(r.URL.Path) {
		p.log.Debug("refused a probe without forwarding it", "path", r.URL.Path)
		refuse(w)
		return
	}

	w = &lowercaseHeaderWriter{ResponseWriter: w}
	if !p.observes {
		p.rp.ServeHTTP(w, r)
		return
	}

	route := ParseRoute(r.URL.Path, p.subPrefix)
	userAgent := r.Header.Get("User-Agent")

	var notice *config.CompiledUserAgentRule
	if route.ShortUUID != "" {
		notice = p.userAgents.Match(userAgent)
	}
	// The agent itself is never logged: a pasted one may be a live link.
	if notice != nil && notice.Action == config.UserAgentBlock {
		p.log.Debug("answered a broken user agent without forwarding it",
			"rule", notice.Name, "short_uuid", route.ShortUUID)
		p.writeBlock(w, r, p.templateRequest(r, route, true), notice)
		return
	}
	if notice != nil {
		p.log.Debug("serving a notice for a broken user agent",
			"rule", notice.Name, "short_uuid", route.ShortUUID)
	}

	info := &requestInfo{
		rq:     p.templateRequest(r, route, p.rewrites || notice != nil),
		notice: notice,
	}
	if p.subCache != nil && route.ShortUUID != "" && notice == nil {
		info.cacheKey = subcache.Key(route.ShortUUID, route.ClientType, userAgent, r.Header.Get("Accept-Encoding"))
	}

	p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, info)))
}

// The client IP and link cost a little to work out, so they are filled only when full.
func (p *Proxy) templateRequest(r *http.Request, route Route, full bool) rewrite.Request {
	rq := rewrite.Request{
		ShortUUID:  route.ShortUUID,
		ClientType: route.ClientType,
		UserAgent:  r.Header.Get("User-Agent"),
	}
	if full {
		rq.ClientIP = p.realIP.ClientIP(r)
		rq.SubscriptionURL = p.subscriptionURL(r, route.ShortUUID)
	}
	return rq
}

// With no cache, the connection is dropped without a response, as the subscription page itself does.
func (p *Proxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}

	info, ok := infoFrom(r)
	if ok && p.writeFromCache(w, info) {
		p.log.Warn("upstream unreachable, served subscription from cache",
			"short_uuid", info.rq.ShortUUID,
			"error", err,
		)
		return
	}

	// A bare EOF is how the subscription page refuses anything it will not serve: routine, not a fault.
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

// Rebuilt from the request so {SUBSCRIPTION_URL} costs no panel call; the client-type segment is dropped on purpose.
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

// ReverseProxy's Rewrite hook deletes X-Forwarded-*, so they are set explicitly. X-Forwarded-For carries
// only the client resolved by TRUST_PROXY: appending our peer would make the page's TRUST_PROXY=1 pick
// the edge in front of us (e.g. a Docker bridge address) instead of the user.
func forwardHeaders(r *httputil.ProxyRequest, resolver *realip.Resolver, forceHTTPS bool) {
	if client := resolver.ClientIP(r.In); client != "" {
		r.Out.Header.Set("X-Forwarded-For", client)
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
