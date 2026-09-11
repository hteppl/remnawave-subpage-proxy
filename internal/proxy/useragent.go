package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/hosts"
	"github.com/hteppl/remnawave-subpage-proxy/internal/rewrite"
)

// announceHeader is where Happ shows a message above the host list.
const announceHeader = "announce"

// UserAgentFilter shows a message instead of hosts when the User-Agent is one
// no client sends, such as a pasted subscription link or JSON config.
type UserAgentFilter struct {
	rules    []config.CompiledUserAgentRule
	disabled []string
}

// NewUserAgentFilter compiles the rules itself, as NewBlocker does, so no
// unchecked pattern slips in.
func NewUserAgentFilter(c config.UserAgents) (*UserAgentFilter, error) {
	rules, disabled, err := config.CompileUserAgents(c)
	if err != nil {
		return nil, err
	}
	return &UserAgentFilter{rules: rules, disabled: disabled}, nil
}

// Enabled reports whether Match can ever return a rule.
func (f *UserAgentFilter) Enabled() bool { return f.Len() > 0 }

func (f *UserAgentFilter) Len() int {
	if f == nil {
		return 0
	}
	return len(f.rules)
}

// Disabled names the rules left out for want of a message.
func (f *UserAgentFilter) Disabled() []string {
	if f == nil {
		return nil
	}
	return f.disabled
}

// Match returns the first rule whose pattern matches userAgent, nil for none.
func (f *UserAgentFilter) Match(userAgent string) *config.CompiledUserAgentRule {
	if f == nil {
		return nil
	}
	for i := range f.rules {
		if f.rules[i].Regexp.MatchString(userAgent) {
			return &f.rules[i]
		}
	}
	return nil
}

// A message rendered to nothing falls back to the raw text, which still says more than an empty profile.
func (p *Proxy) noticeMessage(ctx context.Context, h http.Header, rq rewrite.Request, rule *config.CompiledUserAgentRule) string {
	message := rule.Message
	if p.engine != nil {
		message = p.engine.Render(ctx, h, rq, message)
	}
	if strings.TrimSpace(message) == "" {
		message = rule.Message
	}
	return strings.TrimSpace(message)
}

// Headers stay so the app still shows traffic and expiry; error answers pass
// through, as a missing user must not be told their User-Agent is the problem.
func (p *Proxy) applyNotice(resp *http.Response, info *requestInfo, message string) {
	if !rewritable(resp) {
		return
	}

	// The body names its own format; the client-type path is the fallback.
	format := hosts.FormatForClientType(info.rq.ClientType)
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
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("ETag")
	setBody(resp, noticeBody(format, message))
	setAnnounce(resp.Header, info.notice, message)
}

// writeBlock answers without forwarding, so there is no traffic or expiry to show.
func (p *Proxy) writeBlock(w http.ResponseWriter, r *http.Request, rq rewrite.Request, rule *config.CompiledUserAgentRule) {
	message := p.noticeMessage(r.Context(), http.Header{}, rq, rule)
	format := hosts.FormatForClientType(rq.ClientType)

	header := w.Header()
	header.Set("Content-Type", contentTypeFor(format))
	header.Set("Cache-Control", "no-store")
	setAnnounce(header, rule, message)
	writeBody(w, http.StatusOK, noticeBody(format, message))
}

func noticeBody(format hosts.Format, message string) []byte {
	return hosts.Placeholder(format, strings.Split(message, "\n")...)
}

// base64 lets the banner header carry the message's line breaks.
func setAnnounce(h http.Header, rule *config.CompiledUserAgentRule, message string) {
	if rule.Announce {
		h.Set(announceHeader, rewrite.Encode(message, rewrite.FormBase64Prefixed))
	}
}

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
