package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/hosts"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
)

// announceHeader is where Happ shows a message above the host list.
const announceHeader = "announce"

// UserAgentFilter catches subscription requests whose User-Agent no real
// client sends — a subscription link or a whole JSON config pasted into the
// app's User-Agent field — and either refuses them or tells the user.
type UserAgentFilter struct {
	rules    []config.CompiledUserAgentRule
	disabled []string
}

// NewUserAgentFilter compiles the rules itself, as NewBlocker does, so a
// filter built outside the config loader cannot hold an unchecked pattern.
func NewUserAgentFilter(c config.UserAgents) (*UserAgentFilter, error) {
	rules, disabled, err := config.CompileUserAgents(c)
	if err != nil {
		return nil, err
	}
	return &UserAgentFilter{rules: rules, disabled: disabled}, nil
}

// Enabled reports whether Match can ever return a rule.
func (f *UserAgentFilter) Enabled() bool {
	return f != nil && len(f.rules) > 0
}

// Len is the number of rules in effect.
func (f *UserAgentFilter) Len() int {
	if f == nil {
		return 0
	}
	return len(f.rules)
}

// Disabled names the notice rules left out for want of a message.
func (f *UserAgentFilter) Disabled() []string {
	if f == nil {
		return nil
	}
	return f.disabled
}

// Match returns the first rule whose pattern matches userAgent, nil for none.
func (f *UserAgentFilter) Match(userAgent string) *config.CompiledUserAgentRule {
	if !f.Enabled() {
		return nil
	}
	for i := range f.rules {
		if f.rules[i].Regexp.MatchString(userAgent) {
			return &f.rules[i]
		}
	}
	return nil
}

// applyNotice swaps the hosts of a subscription for placeholders named after
// the rendered message, one per line. Headers stay, so the app still shows traffic
// and expiry. Error answers and the web page pass through untouched: a
// missing user must not be told their User-Agent is the problem.
func (p *Proxy) applyNotice(resp *http.Response, info *requestInfo, message string) {
	if resp.StatusCode != http.StatusOK || isHTML(resp.Header) {
		return
	}

	format := hosts.FormatForClientType(info.route.ClientType)
	if !isCompressed(resp.Header) {
		if body, ok := drainBody(resp, maxShuffleBody); ok {
			if sniffed := hosts.Sniff(body); sniffed != hosts.FormatUnknown {
				format = sniffed
			}
		}
	}

	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	body := noticeBody(format, info.notice, message)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("ETag")
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.TransferEncoding = nil
	setAnnounce(resp.Header, info.notice, message)
}

// writeBlock answers a blocked request at the proxy, without forwarding it:
// the same placeholder hosts a notice gives, but no traffic or expiry, since
// only the subscription page could supply those. Placeholders in the message
// still resolve, from the panel API when they need it.
func (p *Proxy) writeBlock(w http.ResponseWriter, r *http.Request, route Route, rule *config.CompiledUserAgentRule) {
	message := rule.Message
	if p.engine != nil {
		message = p.engine.Render(r.Context(), http.Header{}, rewrite.Request{
			ShortUUID:       route.ShortUUID,
			ClientType:      route.ClientType,
			UserAgent:       r.Header.Get("User-Agent"),
			ClientIP:        p.realIP.ClientIP(r),
			SubscriptionURL: p.subscriptionURL(r, route.ShortUUID),
		}, message)
	}

	// No body to sniff, so the client-type path decides the format.
	format := hosts.FormatForClientType(route.ClientType)
	body := noticeBody(format, rule, message)

	header := w.Header()
	header.Set("Content-Type", contentTypeFor(format))
	header.Set("Content-Length", strconv.Itoa(len(body)))
	header.Set("Cache-Control", "no-store")
	setAnnounce(header, rule, message)
	lowercaseKeys(header)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// noticeBody turns the rendered message into placeholder hosts, one per line.
// A message rendered to nothing, say by blanked placeholders, would leave an
// empty profile with no explanation at all; the text as written, braces and
// all, still says more than that.
func noticeBody(format hosts.Format, rule *config.CompiledUserAgentRule, message string) []byte {
	if strings.TrimSpace(message) == "" {
		message = rule.Message
	}
	return hosts.Placeholder(format, strings.Split(message, "\n")...)
}

// setAnnounce puts the whole message, line breaks and all, in the banner when
// the rule asks for it; base64 is what lets a header carry them.
func setAnnounce(h http.Header, rule *config.CompiledUserAgentRule, message string) {
	if !rule.Announce {
		return
	}
	if strings.TrimSpace(message) == "" {
		message = rule.Message
	}
	h.Del(announceHeader)
	h.Set(announceHeader, rewrite.Encode(strings.TrimSpace(message), rewrite.FormBase64Prefixed))
}

// contentTypeFor matches what the subscription page sends for each format.
func contentTypeFor(format hosts.Format) string {
	switch format {
	case hosts.FormatXray, hosts.FormatSingbox:
		return "application/json; charset=utf-8"
	case hosts.FormatClash:
		return "text/yaml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}
