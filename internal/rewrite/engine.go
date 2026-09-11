package rewrite

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/panel"
	"github.com/hteppl/remnawave-subpage-proxy/internal/subinfo"
	"github.com/hteppl/remnawave-subpage-proxy/internal/tmpl"
)

type InfoFetcher interface {
	SubscriptionInfo(ctx context.Context, shortUUID, realIP string) (*panel.Info, error)
}

type Request struct {
	ShortUUID       string
	ClientType      string
	UserAgent       string
	ClientIP        string
	SubscriptionURL string
}

type Options struct {
	File          config.File
	Fetcher       InfoFetcher
	AlwaysFetch   bool
	ForwardRealIP bool
	Logger        *slog.Logger
}

// Engine is safe for concurrent use.
type Engine struct {
	rules          []config.HeaderRule
	opts           config.TemplateOpts
	resolver       *subinfo.Resolver
	forceUnlimited bool
	fetcher        InfoFetcher
	alwaysFetch    bool
	forwardRealIP  bool
	unknown        tmpl.Unknown
	log            *slog.Logger
}

func New(o Options) *Engine {
	unknown := tmpl.Keep
	if o.File.Template.Unknown == config.UnknownBlank {
		unknown = tmpl.Blank
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		rules:          o.File.Headers,
		opts:           o.File.Template,
		resolver:       subinfo.NewResolver(o.File),
		forceUnlimited: o.File.Traffic.ForceUnlimited,
		fetcher:        o.Fetcher,
		alwaysFetch:    o.AlwaysFetch,
		forwardRealIP:  o.ForwardRealIP,
		unknown:        unknown,
		log:            log,
	}
}

func (e *Engine) Enabled() bool {
	return len(e.rules) > 0 || e.opts.ScanAllHeaders || e.forceUnlimited
}

type candidate struct {
	name        string
	source      string
	form        Form
	remove      bool
	rule        *config.HeaderRule
	present     bool
	original    string
	hasOriginal bool
}

// originalValueName is answered per header by the engine, not by the resolver.
const originalValueName = "ORIGINAL_VALUE"

// Apply rewrites h in place, calling the panel only when something needs it.
func (e *Engine) Apply(ctx context.Context, h http.Header, rq Request) {
	userInfo := userInfoFrom(h)

	// Marzban legacy links have no short UUID, yet traffic placeholders still resolve from the header.
	if rq.ShortUUID == "" && userInfo == nil {
		return
	}

	// force_unlimited only rewrites the header; placeholders keep reporting the real quota.
	if e.forceUnlimited && userInfo != nil {
		h.Set(subinfo.UserInfoHeader, subinfo.ForceUnlimitedTotal(h.Get(subinfo.UserInfoHeader)))
	}

	candidates := e.collect(h, rq)
	if len(candidates) == 0 {
		return
	}

	var names []string
	needsStatus, needsLimit := false, false
	for _, c := range candidates {
		if c.rule != nil && len(c.rule.When.UserStatuses) > 0 {
			needsStatus = true
		}
		if c.rule != nil && c.rule.When.HasTrafficLimit != nil {
			needsLimit = true
		}
		if !c.remove {
			used := tmpl.Names(c.source)
			names = append(names, used...)
			// Placeholders inside the original value count toward whether the panel is needed.
			if c.hasOriginal && slices.Contains(used, originalValueName) {
				names = append(names, tmpl.Names(c.original)...)
			}
		}
	}

	// A limit condition needs the panel only when the header lacks the quota.
	limitFromHeader := userInfo != nil && userInfo.Total >= 0

	info := e.fetchInfo(ctx, rq, needsStatus || (needsLimit && !limitFromHeader) ||
		e.resolver.NeedsPanel(names, userInfo != nil))

	source := sourceFor(rq, userInfo, info)
	lookup := e.resolver.Lookup(source)
	limit, limitKnown := e.resolver.Limit(source)

	// Per header, the first rule whose conditions hold wins.
	written := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		key := http.CanonicalHeaderKey(c.name)
		if _, done := written[key]; done {
			continue
		}
		if !matchesStatus(c.rule, info) || !matchesTrafficLimit(c.rule, limit, limitKnown) {
			continue
		}
		written[key] = struct{}{}
		if c.remove {
			h.Del(c.name)
			continue
		}

		rendered := tmpl.Render(c.source, e.withOriginal(lookup, c), e.unknown)
		if c.rule != nil && c.rule.MaxLength > 0 {
			rendered = tmpl.Truncate(rendered, c.rule.MaxLength)
		}
		h.Set(c.name, Encode(rendered, c.form))

		e.log.Debug("rewrote subscription header",
			"header", c.name,
			"short_uuid", rq.ShortUUID,
			"client_type", rq.ClientType,
		)
	}
}

// Render must see h before Apply, which may already have hidden the quota for force_unlimited.
func (e *Engine) Render(ctx context.Context, h http.Header, rq Request, template string) string {
	userInfo := userInfoFrom(h)
	info := e.fetchInfo(ctx, rq, e.resolver.NeedsPanel(tmpl.Names(template), userInfo != nil))
	return tmpl.Render(template, e.resolver.Lookup(sourceFor(rq, userInfo, info)), e.unknown)
}

// fetchInfo returns nil on failure, so unresolved placeholders are left as-is, not blanked.
func (e *Engine) fetchInfo(ctx context.Context, rq Request, needed bool) *panel.Info {
	if e.fetcher == nil || rq.ShortUUID == "" || !(needed || e.alwaysFetch) {
		return nil
	}
	realIP := ""
	if e.forwardRealIP {
		realIP = rq.ClientIP
	}
	info, err := e.fetcher.SubscriptionInfo(ctx, rq.ShortUUID, realIP)
	switch {
	case err == nil:
		return info
	case errors.Is(err, panel.ErrNotFound):
		e.log.Debug("panel has no such subscription", "short_uuid", rq.ShortUUID)
	default:
		e.log.Warn("subscription info lookup failed", "short_uuid", rq.ShortUUID, "error", err)
	}
	return nil
}

func userInfoFrom(h http.Header) *subinfo.UserInfo {
	parsed, ok := subinfo.ParseUserInfo(h.Get(subinfo.UserInfoHeader))
	if !ok {
		return nil
	}
	return &parsed
}

func sourceFor(rq Request, userInfo *subinfo.UserInfo, info *panel.Info) subinfo.Source {
	return subinfo.Source{
		ShortUUID:       rq.ShortUUID,
		ClientType:      rq.ClientType,
		UserAgent:       rq.UserAgent,
		ClientIP:        rq.ClientIP,
		SubscriptionURL: rq.SubscriptionURL,
		UserInfo:        userInfo,
		Panel:           info,
	}
}

// collect takes explicit rules first, then any header holding a placeholder.
func (e *Engine) collect(h http.Header, rq Request) []candidate {
	var candidates []candidate
	handled := make(map[string]struct{}, len(e.rules))

	for i := range e.rules {
		rule := &e.rules[i]
		key := http.CanonicalHeaderKey(rule.Name)
		handled[key] = struct{}{}

		current, present := firstValue(h, rule.Name)
		if !matchesRequest(rule.When, rq, present) {
			continue
		}

		if rule.Remove {
			if present {
				candidates = append(candidates, candidate{name: rule.Name, remove: true, rule: rule, present: true})
			}
			continue
		}

		c, ok := e.candidateFor(rule.Name, current, present, rule)
		if !ok {
			continue
		}
		candidates = append(candidates, c)
	}

	if !e.opts.ScanAllHeaders {
		return candidates
	}

	for key, values := range h {
		if _, done := handled[key]; done || len(values) == 0 {
			continue
		}
		c, ok := e.candidateFor(key, values[0], true, nil)
		if !ok {
			continue
		}
		candidates = append(candidates, c)
	}

	return candidates
}

func (e *Engine) candidateFor(name, current string, present bool, rule *config.HeaderRule) (candidate, bool) {
	encode := config.EncodeAuto
	if rule != nil {
		encode = rule.Encode
	}

	c := candidate{name: name, rule: rule, present: present}
	// text is the value as placeholders see it, unwrapped from base64 when decode_base64 allows.
	text, form, unwrapped := current, FormPlain, false
	if present {
		if decoded, detected, ok := DecodeBase64(current); ok {
			form = detected
			if e.opts.DecodeBase64 {
				text, unwrapped = decoded, true
			}
		}
		c.original, c.hasOriginal = text, true
	}

	switch {
	case rule != nil && rule.Template != nil:
		c.source = *rule.Template
	// Rewrite only on a placeholder, so an opaque payload, base64 or not, is never touched.
	case present && tmpl.Contains(text):
		c.source = text
		if !unwrapped {
			form = FormPlain
		}
	default:
		return candidate{}, false
	}
	c.form = overrideForm(form, encode)
	return c, true
}

// withOriginal resolves placeholders inside the original with the base lookup, so it cannot recurse.
func (e *Engine) withOriginal(base func(string) (string, bool), c candidate) tmpl.Lookup {
	return func(name string) (string, bool) {
		if name != originalValueName {
			return base(name)
		}
		if !c.hasOriginal {
			return "", false
		}
		return tmpl.Render(c.original, base, e.unknown), true
	}
}

func overrideForm(detected Form, encode config.Encoding) Form {
	switch encode {
	case config.EncodeNone:
		return FormPlain
	case config.EncodeBase64:
		return FormBase64
	case config.EncodeBase64Prefixed:
		return FormBase64Prefixed
	default:
		return detected
	}
}

func matchesRequest(cond config.Condition, rq Request, present bool) bool {
	if cond.Exists != nil && *cond.Exists != present {
		return false
	}
	if len(cond.ClientTypes) > 0 && !slices.Contains(cond.ClientTypes, strings.ToLower(rq.ClientType)) {
		return false
	}
	if re := cond.UserAgentRegexp(); re != nil && !re.MatchString(rq.UserAgent) {
		return false
	}
	return true
}

// matchesStatus runs after the panel lookup; without it the rule is skipped.
func matchesStatus(rule *config.HeaderRule, info *panel.Info) bool {
	if rule == nil || len(rule.When.UserStatuses) == 0 {
		return true
	}
	if info == nil {
		return false
	}
	return slices.Contains(rule.When.UserStatuses, strings.ToUpper(info.User.UserStatus))
}

// matchesTrafficLimit treats zero as unlimited and skips the rule when the limit is unknown.
func matchesTrafficLimit(rule *config.HeaderRule, limit int64, known bool) bool {
	if rule == nil || rule.When.HasTrafficLimit == nil {
		return true
	}
	if !known {
		return false
	}
	return (limit > 0) == *rule.When.HasTrafficLimit
}

func firstValue(h http.Header, name string) (string, bool) {
	values, ok := h[http.CanonicalHeaderKey(name)]
	if !ok || len(values) == 0 {
		return "", false
	}
	return values[0], true
}
