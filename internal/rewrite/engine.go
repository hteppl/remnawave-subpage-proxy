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

// Engine rewrites response headers. Safe for concurrent use.
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

// originalValueName is answered per header, not per request, so the engine
// layers it over the resolver's lookup.
const originalValueName = "ORIGINAL_VALUE"

// Apply rewrites h in place, calling the panel only when something needs it.
func (e *Engine) Apply(ctx context.Context, h http.Header, rq Request) {
	var userInfo *subinfo.UserInfo
	if parsed, ok := subinfo.ParseUserInfo(h.Get(subinfo.UserInfoHeader)); ok {
		userInfo = &parsed
	}

	// The second case covers Marzban legacy links, where traffic placeholders
	// resolve but panel-backed ones cannot.
	if rq.ShortUUID == "" && userInfo == nil {
		return
	}

	// Only the header is rewritten: force_unlimited hides the quota from the
	// client's own traffic display, while placeholders keep reporting the truth.
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
			// Placeholders the panel put inside the header are resolved too, so
			// they have to be counted when deciding whether the panel is needed.
			if c.hasOriginal && slices.Contains(used, originalValueName) {
				names = append(names, tmpl.Names(c.original)...)
			}
		}
	}

	// The header carries the quota, so a limit condition only costs a panel
	// request when it is absent.
	limitFromHeader := userInfo != nil && userInfo.Total >= 0

	var info *panel.Info
	if e.fetcher != nil && rq.ShortUUID != "" &&
		(e.alwaysFetch || needsStatus || (needsLimit && !limitFromHeader) ||
			e.resolver.NeedsPanel(names, userInfo != nil)) {
		realIP := ""
		if e.forwardRealIP {
			realIP = rq.ClientIP
		}
		fetched, err := e.fetcher.SubscriptionInfo(ctx, rq.ShortUUID, realIP)
		switch {
		case err == nil:
			info = fetched
		case errors.Is(err, panel.ErrNotFound):
			e.log.Debug("panel has no such subscription", "short_uuid", rq.ShortUUID)
		default:
			// Unresolved placeholders are left as-is, not blanked.
			e.log.Warn("subscription info lookup failed", "short_uuid", rq.ShortUUID, "error", err)
		}
	}

	source := subinfo.Source{
		ShortUUID:       rq.ShortUUID,
		ClientType:      rq.ClientType,
		UserAgent:       rq.UserAgent,
		ClientIP:        rq.ClientIP,
		SubscriptionURL: rq.SubscriptionURL,
		UserInfo:        userInfo,
		Panel:           info,
	}
	lookup := e.resolver.Lookup(source)
	limit, limitKnown := e.resolver.Limit(source)

	// Several rules may target one header, each with its own conditions; the
	// first one whose conditions hold wins.
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

// candidateFor decides whether a header needs rewriting, and in which form.
// The original value is derived only for actual candidates.
func (e *Engine) candidateFor(name, current string, present bool, rule *config.HeaderRule) (candidate, bool) {
	encode := config.EncodeAuto
	if rule != nil {
		encode = rule.Encode
	}

	// A rule with an explicit template replaces the value outright.
	if rule != nil && rule.Template != nil {
		form := FormPlain
		original, hasOriginal := "", false
		if present {
			decoded, detected, ok := DecodeBase64(current)
			if ok && encode == config.EncodeAuto {
				form = detected
			}
			original, hasOriginal = current, true
			if ok && e.opts.DecodeBase64 {
				original = decoded
			}
		}
		return candidate{
			name:        name,
			source:      *rule.Template,
			form:        overrideForm(form, encode),
			rule:        rule,
			present:     present,
			original:    original,
			hasOriginal: hasOriginal,
		}, true
	}

	if !present {
		return candidate{}, false
	}

	// Base64 is only unwrapped when doing so reveals a placeholder, so opaque
	// payloads are never touched.
	if e.opts.DecodeBase64 {
		if decoded, form, ok := DecodeBase64(current); ok && tmpl.Contains(decoded) {
			return candidate{
				name:        name,
				source:      decoded,
				form:        overrideForm(form, encode),
				rule:        rule,
				present:     true,
				original:    decoded,
				hasOriginal: true,
			}, true
		}
	}

	if !tmpl.Contains(current) {
		return candidate{}, false
	}
	return candidate{
		name:        name,
		source:      current,
		form:        overrideForm(FormPlain, encode),
		rule:        rule,
		present:     true,
		original:    current,
		hasOriginal: true,
	}, true
}

// withOriginal adds {ORIGINAL_VALUE} for one header. The panel's own
// placeholders inside it are resolved with the base lookup, which does not know
// the name, so the substitution cannot recurse.
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

// matchesTrafficLimit gates a rule on the plan having a finite quota. A zero
// limit is unlimited; an undeterminable one skips the rule rather than guessing.
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
