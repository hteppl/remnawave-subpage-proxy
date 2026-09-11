package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Encoding describes how a header value is written back to the client.
type Encoding string

const (
	// EncodeAuto reproduces the upstream form; a new value emits plain text.
	EncodeAuto   Encoding = "auto"
	EncodeNone   Encoding = "none"
	EncodeBase64 Encoding = "base64"
	// EncodeBase64Prefixed uses the "base64:" marker Happ understands.
	EncodeBase64Prefixed Encoding = "base64-prefixed"
)

func (e *Encoding) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return err
	}
	switch v := Encoding(strings.ToLower(strings.TrimSpace(raw))); v {
	case EncodeAuto, EncodeNone, EncodeBase64, EncodeBase64Prefixed:
		*e = v
		return nil
	case "":
		*e = EncodeAuto
		return nil
	default:
		return fmt.Errorf("line %d: encode must be one of auto, none, base64, base64-prefixed, got %q", node.Line, raw)
	}
}

// UnknownPolicy decides what happens to a {PLACEHOLDER} with no known value.
type UnknownPolicy string

const (
	UnknownKeep  UnknownPolicy = "keep"
	UnknownBlank UnknownPolicy = "blank"
)

func (u *UnknownPolicy) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return err
	}
	switch v := UnknownPolicy(strings.ToLower(strings.TrimSpace(raw))); v {
	case UnknownKeep, UnknownBlank:
		*u = v
		return nil
	case "":
		*u = UnknownKeep
		return nil
	default:
		return fmt.Errorf("line %d: template.unknown must be keep or blank, got %q", node.Line, raw)
	}
}

type TrafficFormat struct {
	Decimals    int      `yaml:"decimals"`
	BinaryUnits bool     `yaml:"binary_units"`
	Unlimited   string   `yaml:"unlimited"`
	Units       []string `yaml:"units"`
	// ForceUnlimited sends subscription-userinfo with total=0, so the client's
	// own traffic display shows no quota. Placeholders and conditions keep
	// reporting the real one.
	ForceUnlimited bool `yaml:"force_unlimited"`
}

// DateTimeFormat layouts use Go reference time (02.01.2006 is day.month.year).
type DateTimeFormat struct {
	Layout     string `yaml:"layout"`
	TimeLayout string `yaml:"time_layout"`
	Timezone   string `yaml:"timezone"`
	// Never renders in place of an expiry date that never comes.
	Never string `yaml:"never"`

	location *time.Location
}

// Location returns the resolved timezone, never nil after validation.
func (d DateTimeFormat) Location() *time.Location {
	if d.location == nil {
		return time.UTC
	}
	return d.location
}

type ProgressBar struct {
	Width  int    `yaml:"width"`
	Filled string `yaml:"filled"`
	Empty  string `yaml:"empty"`
}

type TemplateOpts struct {
	Unknown UnknownPolicy `yaml:"unknown"`
	// ScanAllHeaders covers an announce configured in the panel, unnamed here.
	ScanAllHeaders bool `yaml:"scan_all_headers"`
	// DecodeBase64 looks inside base64 values for placeholders.
	DecodeBase64 bool `yaml:"decode_base64"`
}

type Condition struct {
	ClientTypes  []string `yaml:"client_types"`
	UserStatuses []string `yaml:"user_statuses"`
	UserAgent    string   `yaml:"user_agent"`
	// Exists gates the rule on the upstream having sent the header.
	Exists          *bool `yaml:"exists"`
	HasTrafficLimit *bool `yaml:"has_traffic_limit"`

	userAgentRe *regexp.Regexp
}

func (c Condition) UserAgentRegexp() *regexp.Regexp { return c.userAgentRe }

// isEmpty reports a rule that matches every request, and therefore shadows any
// later rule for the same header.
func (c Condition) isEmpty() bool {
	return len(c.ClientTypes) == 0 && len(c.UserStatuses) == 0 &&
		strings.TrimSpace(c.UserAgent) == "" && c.Exists == nil && c.HasTrafficLimit == nil
}

type HeaderRule struct {
	Name string `yaml:"name"`
	// Template replaces the value outright; omit it to only substitute.
	Template *string   `yaml:"template"`
	Encode   Encoding  `yaml:"encode"`
	Remove   bool      `yaml:"remove"`
	When     Condition `yaml:"when"`
	// MaxLength truncates the rendered text in runes, before encoding.
	MaxLength int `yaml:"max_length"`
}

// Block refuses obvious scanner probes at the proxy, so they never reach the
// subscription page or the panel.
type Block struct {
	Enabled bool `yaml:"enabled"`
	// Patterns are extra Go regular expressions matched against the path.
	Patterns []string `yaml:"patterns"`
}

// CompileBlock parses the extra patterns of a Block. It is the only place a
// pattern becomes a regexp, so the config loader and the proxy cannot disagree
// about what one means. Every bad pattern is reported, not just the first.
func CompileBlock(b Block) ([]*regexp.Regexp, error) {
	var (
		compiled []*regexp.Regexp
		problems []error
	)
	for i, pattern := range b.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			problems = append(problems, fmt.Errorf("block.patterns[%d] is not a valid regexp: %w", i, err))
			continue
		}
		compiled = append(compiled, re)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return compiled, nil
}

// Hosts controls shuffling of the servers inside a subscription body.
type Hosts struct {
	// Shuffle is a list of regexps matched against the host name shown to
	// the user (link fragment, vmess ps, remarks, sing-box tag, Clash proxy
	// name). Each group is shuffled independently among its own positions;
	// a host matching none stays put, one matching several joins the first.
	Shuffle []string `yaml:"shuffle"`
}

// CompileHosts parses the shuffle patterns, reporting every bad one.
func CompileHosts(h Hosts) ([]*regexp.Regexp, error) {
	var (
		compiled []*regexp.Regexp
		problems []error
	)
	for i, pattern := range h.Shuffle {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			problems = append(problems, fmt.Errorf("hosts.shuffle[%d] needs a name pattern", i))
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			problems = append(problems, fmt.Errorf("hosts.shuffle[%d] is not a valid regexp: %w", i, err))
			continue
		}
		compiled = append(compiled, re)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return compiled, nil
}

// UserAgentAction is what happens to a subscription request whose User-Agent
// matches a rule.
type UserAgentAction string

const (
	// UserAgentNotice forwards the request and keeps the panel's headers, so
	// the app still shows traffic and expiry, but swaps the hosts for
	// placeholders named after the message.
	UserAgentNotice UserAgentAction = "notice"
	// UserAgentBlock answers at the proxy with the same placeholders, never
	// forwarding the request, so the subscription page builds nothing.
	UserAgentBlock UserAgentAction = "block"
)

func (a *UserAgentAction) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return err
	}
	switch v := UserAgentAction(strings.ToLower(strings.TrimSpace(raw))); v {
	case UserAgentNotice, UserAgentBlock:
		*a = v
		return nil
	case "":
		*a = UserAgentNotice
		return nil
	default:
		return fmt.Errorf("line %d: user_agents action must be notice or block, got %q", node.Line, raw)
	}
}

// UserAgentRule catches a User-Agent no real client sends: a subscription URL
// pasted into the field, a whole JSON config, and the like.
type UserAgentRule struct {
	// Name identifies the rule in logs; the User-Agent itself is never logged,
	// since a pasted one may hold a subscription link.
	Name string `yaml:"name"`
	// Pattern is a Go regexp matched against the whole User-Agent.
	Pattern string          `yaml:"pattern"`
	Action  UserAgentAction `yaml:"action"`
	// Message is a template shown as the placeholder host's name; each line
	// of a multi-line message becomes a host of its own. Required: a rule
	// without one is disabled.
	Message string `yaml:"message"`
	// Announce also sends the message as the announce header.
	Announce bool `yaml:"announce"`
}

type UserAgents struct {
	// Rules are tried in order; the first match decides.
	Rules []UserAgentRule `yaml:"rules"`
}

// CompiledUserAgentRule is a rule with its pattern parsed.
type CompiledUserAgentRule struct {
	UserAgentRule
	Regexp *regexp.Regexp
}

// CompileUserAgents parses the rules and reports every bad pattern. A rule
// without a message has nothing to tell the user, so it is left out and
// named in disabled for the caller to warn about; there is no default text,
// since any wording or language the proxy picked would be wrong for someone.
func CompileUserAgents(u UserAgents) (compiled []CompiledUserAgentRule, disabled []string, err error) {
	var problems []error
	for i, rule := range u.Rules {
		label := fmt.Sprintf("user_agents.rules[%d]", i)
		if rule.Name = strings.TrimSpace(rule.Name); rule.Name != "" {
			label += " (" + rule.Name + ")"
		} else {
			rule.Name = fmt.Sprintf("rule-%d", i)
		}
		if strings.TrimSpace(rule.Pattern) == "" {
			problems = append(problems, fmt.Errorf("%s needs a pattern", label))
			continue
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s pattern is not a valid regexp: %w", label, err))
			continue
		}
		if rule.Action == "" {
			rule.Action = UserAgentNotice
		}
		if strings.TrimSpace(rule.Message) == "" {
			disabled = append(disabled, label)
			continue
		}
		compiled = append(compiled, CompiledUserAgentRule{UserAgentRule: rule, Regexp: re})
	}
	if len(problems) > 0 {
		return nil, nil, errors.Join(problems...)
	}
	return compiled, disabled, nil
}

type File struct {
	Traffic     TrafficFormat     `yaml:"traffic"`
	DateTime    DateTimeFormat    `yaml:"datetime"`
	ProgressBar ProgressBar       `yaml:"progress_bar"`
	Template    TemplateOpts      `yaml:"template"`
	Vars        map[string]string `yaml:"vars"`
	Headers     []HeaderRule      `yaml:"headers"`
	Block       Block             `yaml:"block"`
	Hosts       Hosts             `yaml:"hosts"`
	UserAgents  UserAgents        `yaml:"user_agents"`
}

func defaultFile() File {
	return File{
		Traffic: TrafficFormat{
			Decimals:    2,
			BinaryUnits: true,
			Unlimited:   "∞",
		},
		DateTime: DateTimeFormat{
			Layout:     "02.01.2006",
			TimeLayout: "15:04",
			Timezone:   "UTC",
			Never:      "∞",
			location:   time.UTC,
		},
		ProgressBar: ProgressBar{
			Width:  10,
			Filled: "▰",
			Empty:  "▱",
		},
		Block: Block{Enabled: true},
		Template: TemplateOpts{
			Unknown:        UnknownKeep,
			ScanAllHeaders: true,
			DecodeBase64:   true,
		},
	}
}

// unknownSection matches the yaml message for an unrecognised key at the top
// level of the file, such as a whole section a newer version added. Only those
// are skipped, so an old image still starts on a new config; the name is
// matched loosely because a YAML key may contain spaces, and the type is built
// from File itself so a rename cannot quietly stop the match.
//
// Nested keys are deliberately excluded. Inside a section the proxy already
// knows, an unrecognised key is far more likely a typo — "templat" for
// "template" — and skipping it would leave a header rule silently doing the
// wrong thing in production.
var unknownSection = regexp.MustCompile(
	`^line \d+: field .+ not found in type ` + regexp.QuoteMeta(fmt.Sprintf("%T", File{})) + `$`)

// splitUnknownFields separates the skippable "unknown section" complaints from
// the real decode errors, returning the skipped keys and what remains fatal.
func splitUnknownFields(err error) ([]string, error) {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return nil, err
	}
	var skipped, fatal []string
	for _, e := range typeErr.Errors {
		if unknownSection.MatchString(e) {
			skipped = append(skipped, e)
		} else {
			fatal = append(fatal, e)
		}
	}
	if len(fatal) == 0 {
		return skipped, nil
	}
	return skipped, &yaml.TypeError{Errors: fatal}
}

// loadFile treats a missing file at the default path as "run on defaults". It
// returns the keys it had to skip so the caller can log them.
func loadFile(path string, explicit bool) (File, []string, error) {
	cfg := defaultFile()

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err) && !explicit:
		return cfg, nil, nil
	default:
		return cfg, nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var skipped []string
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && err.Error() != "EOF" {
		// The known keys are decoded even when others are rejected.
		var fatal error
		if skipped, fatal = splitUnknownFields(err); fatal != nil {
			return cfg, skipped, fmt.Errorf("parse config %s: %w", path, fatal)
		}
	}

	if err := cfg.validate(); err != nil {
		// Naming the skipped keys here too: the caller drops them on error,
		// and one of them may be what the operator expected to take effect.
		if len(skipped) > 0 {
			return cfg, skipped, fmt.Errorf("config %s (ignored unknown keys: %s): %w",
				path, strings.Join(skipped, "; "), err)
		}
		return cfg, skipped, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, skipped, nil
}

func (f *File) validate() error {
	var problems []string

	if f.Traffic.Unlimited == "" {
		f.Traffic.Unlimited = "∞"
	}
	if f.Traffic.Decimals < 0 || f.Traffic.Decimals > 6 {
		problems = append(problems, fmt.Sprintf("traffic.decimals must be between 0 and 6, got %d", f.Traffic.Decimals))
	}
	if n := len(f.Traffic.Units); n != 0 && n < 5 {
		problems = append(problems, fmt.Sprintf("traffic.units needs at least 5 entries (B..TB), got %d", n))
	}
	if f.DateTime.Layout == "" {
		f.DateTime.Layout = "02.01.2006"
	}
	if f.DateTime.TimeLayout == "" {
		f.DateTime.TimeLayout = "15:04"
	}
	if f.DateTime.Timezone == "" {
		f.DateTime.Timezone = "UTC"
	}
	if f.DateTime.Never == "" {
		f.DateTime.Never = "∞"
	}
	loc, err := time.LoadLocation(f.DateTime.Timezone)
	if err != nil {
		problems = append(problems, fmt.Sprintf("datetime.timezone %q is not a known IANA zone", f.DateTime.Timezone))
		loc = time.UTC
	}
	f.DateTime.location = loc

	if f.ProgressBar.Width < 0 || f.ProgressBar.Width > 100 {
		problems = append(problems, fmt.Sprintf("progress_bar.width must be between 0 and 100, got %d", f.ProgressBar.Width))
	}
	if f.ProgressBar.Filled == "" {
		f.ProgressBar.Filled = "▰"
	}
	if f.ProgressBar.Empty == "" {
		f.ProgressBar.Empty = "▱"
	}
	if f.Template.Unknown == "" {
		f.Template.Unknown = UnknownKeep
	}

	// errors.Join separates one message per bad pattern with a newline; keep
	// them as separate problems so an operator sees every one.
	if _, err := CompileBlock(f.Block); err != nil {
		problems = append(problems, strings.Split(err.Error(), "\n")...)
	}
	if _, err := CompileHosts(f.Hosts); err != nil {
		problems = append(problems, strings.Split(err.Error(), "\n")...)
	}
	if _, _, err := CompileUserAgents(f.UserAgents); err != nil {
		problems = append(problems, strings.Split(err.Error(), "\n")...)
	}

	for name := range f.Vars {
		if !validVarName(name) {
			problems = append(problems, fmt.Sprintf("vars key %q must match [A-Z0-9_]+", name))
		}
	}

	// Rules are matched top to bottom, so an unconditional rule makes every
	// later rule for the same header dead code.
	shadowed := make(map[string]int, len(f.Headers))

	for i := range f.Headers {
		rule := &f.Headers[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if rule.Name == "" {
			problems = append(problems, fmt.Sprintf("headers[%d].name is required", i))
			continue
		}
		key := strings.ToLower(rule.Name)
		if prev, dead := shadowed[key]; dead {
			problems = append(problems, fmt.Sprintf(
				"headers[%d] (%s) is unreachable: headers[%d] targets the same header with no conditions",
				i, rule.Name, prev))
		} else if rule.When.isEmpty() {
			shadowed[key] = i
		}
		if rule.Encode == "" {
			rule.Encode = EncodeAuto
		}
		if rule.Remove && rule.Template != nil {
			problems = append(problems, fmt.Sprintf("headers[%d] (%s) sets both remove and template", i, rule.Name))
		}
		if rule.MaxLength < 0 {
			problems = append(problems, fmt.Sprintf("headers[%d] (%s) max_length must not be negative", i, rule.Name))
		}
		if ua := strings.TrimSpace(rule.When.UserAgent); ua != "" {
			re, err := regexp.Compile(ua)
			if err != nil {
				problems = append(problems, fmt.Sprintf("headers[%d] (%s) when.user_agent is not a valid regexp: %v", i, rule.Name, err))
			} else {
				rule.When.userAgentRe = re
			}
		}
		for j, st := range rule.When.UserStatuses {
			rule.When.UserStatuses[j] = strings.ToUpper(strings.TrimSpace(st))
		}
		for j, ct := range rule.When.ClientTypes {
			rule.When.ClientTypes[j] = strings.ToLower(strings.TrimSpace(ct))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func validVarName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}
