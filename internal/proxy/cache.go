package proxy

import (
	"net/http"
	"strconv"

	"github.com/hteppl/remnawave-subpage-proxy/internal/subcache"
)

func (p *Proxy) cached(info *requestInfo) (*subcache.Entry, bool) {
	if p.subCache == nil || info.cacheKey == "" {
		return nil, false
	}
	return p.subCache.Get(info.cacheKey)
}

// writeFromCache covers an upstream that could not be reached at all.
func (p *Proxy) writeFromCache(w http.ResponseWriter, info *requestInfo) bool {
	entry, ok := p.cached(info)
	if !ok {
		return false
	}

	header := w.Header()
	for key, values := range entry.Header {
		header[key] = append([]string(nil), values...)
	}
	body := entry.Body
	if p.shuffles(info) && !isCompressed(header) {
		body, _ = p.shuffler.Apply(body)
	}
	writeBody(w, entry.Status, body)
	return true
}

// replaceWithCache covers a reachable page with a dead panel behind it.
func (p *Proxy) replaceWithCache(resp *http.Response, info *requestInfo) bool {
	if resp.StatusCode < 500 {
		return false
	}
	entry, ok := p.cached(info)
	if !ok {
		return false
	}

	p.log.Warn("upstream returned an error, served subscription from cache",
		"short_uuid", info.rq.ShortUUID,
		"upstream_status", resp.StatusCode,
	)

	_ = resp.Body.Close()
	resp.StatusCode = entry.Status
	resp.Status = strconv.Itoa(entry.Status) + " " + http.StatusText(entry.Status)
	resp.Header = entry.Header.Clone()
	setBody(resp, entry.Body)
	return true
}

// store skips the web page.
func (p *Proxy) store(resp *http.Response, info *requestInfo) {
	if p.subCache == nil || info.cacheKey == "" || !rewritable(resp) {
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
