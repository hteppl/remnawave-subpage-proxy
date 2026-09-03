package config

import (
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

	compiled []*regexp.Regexp
}

// Compiled returns the parsed extra patterns.
func (b Block) Compiled() []*regexp.Regexp { return b.compiled }

// CompileBlock parses the extra patterns of a Block built outside the config
// loader, which is what tests and callers assembling one by hand need.
func CompileBlock(b Block) (Block, error) {
	b.compiled = nil
	for i, pattern := range b.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return b, fmt.Errorf("block.patterns[%d]: %w", i, err)
		}
		b.compiled = append(b.compiled, re)
	}
	return b, nil
}

type File struct {
	Traffic     TrafficFormat     `yaml:"traffic"`
	DateTime    DateTimeFormat    `yaml:"datetime"`
	ProgressBar ProgressBar       `yaml:"progress_bar"`
	Template    TemplateOpts      `yaml:"template"`
	Vars        map[string]string `yaml:"vars"`
	Headers     []HeaderRule      `yaml:"headers"`
	Block       Block             `yaml:"block"`
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

// loadFile treats a missing file at the default path as "run on defaults".
func loadFile(path string, explicit bool) (File, error) {
	cfg := defaultFile()

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err) && !explicit:
		return cfg, nil
	default:
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && err.Error() != "EOF" {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
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

	f.Block.compiled = nil
	for i, pattern := range f.Block.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			problems = append(problems, fmt.Sprintf("block.patterns[%d] is not a valid regexp: %v", i, err))
			continue
		}
		f.Block.compiled = append(f.Block.compiled, re)
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
