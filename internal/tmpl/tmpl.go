package tmpl

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Upper snake case keeps {NAME} from colliding with JSON and Clash payloads.
var placeholderRe = regexp.MustCompile(`\{([A-Z][A-Z0-9_]*)((?:\|[^{}|]*)*)\}`)

type Unknown int

const (
	Keep Unknown = iota
	Blank
)

// Lookup resolves a name; its second result separates "unknown name" from
// "empty value".
type Lookup func(name string) (string, bool)

func Contains(s string) bool {
	return strings.IndexByte(s, '{') >= 0 && placeholderRe.MatchString(s)
}

func Names(s string) []string {
	if strings.IndexByte(s, '{') < 0 {
		return nil
	}
	var names []string
	for _, m := range placeholderRe.FindAllStringSubmatchIndex(s, -1) {
		if name := s[m[2]:m[3]]; !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// Render applies modifiers left to right, e.g. {TRAFFIC_LIMIT|default:unlimited|upper}.
func Render(s string, lookup Lookup, unknown Unknown) string {
	if strings.IndexByte(s, '{') < 0 {
		return s
	}
	matches := placeholderRe.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, m := range matches {
		b.WriteString(s[last:m[0]])
		b.WriteString(renderOne(s[m[0]:m[1]], s[m[2]:m[3]], s[m[4]:m[5]], lookup, unknown))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func renderOne(match, name, modSpec string, lookup Lookup, unknown Unknown) string {
	mods := parseModifiers(modSpec)

	value, known := lookup(name)
	if !known {
		if def, ok := defaultOf(mods); ok {
			value = def
		} else if unknown == Blank {
			return ""
		} else {
			return match
		}
	} else if value == "" {
		if def, ok := defaultOf(mods); ok {
			value = def
		}
	}
	return applyModifiers(value, mods)
}

type modifier struct {
	name string
	arg  string
}

func parseModifiers(spec string) []modifier {
	if spec == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(spec, "|"), "|")
	mods := make([]modifier, 0, len(parts))
	for _, part := range parts {
		name, arg, _ := strings.Cut(part, ":")
		mods = append(mods, modifier{
			name: strings.ToLower(strings.TrimSpace(name)),
			arg:  arg,
		})
	}
	return mods
}

func defaultOf(mods []modifier) (string, bool) {
	for _, m := range mods {
		if m.name == "default" {
			return m.arg, true
		}
	}
	return "", false
}

func applyModifiers(value string, mods []modifier) string {
	for _, m := range mods {
		switch m.name {
		case "upper":
			value = strings.ToUpper(value)
		case "lower":
			value = strings.ToLower(value)
		case "trim":
			value = strings.TrimSpace(value)
		case "truncate":
			if n, err := strconv.Atoi(strings.TrimSpace(m.arg)); err == nil {
				value = Truncate(value, n)
			}
		}
	}
	return value
}

// Truncate shortens s to n runes with an ellipsis; n <= 0 is a no-op.
func Truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}
