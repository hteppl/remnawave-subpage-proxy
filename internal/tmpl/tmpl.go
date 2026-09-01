package tmpl

import (
	"regexp"
	"strconv"
	"strings"
)

// Upper snake case keeps {NAME} from colliding with the JSON and Clash
// payloads flowing through the same proxy.
var placeholderRe = regexp.MustCompile(`\{([A-Z][A-Z0-9_]*)((?:\|[^{}|]*)*)\}`)

type Unknown int

const (
	Keep Unknown = iota
	Blank
)

// Lookup's second result separates "unknown name" from "empty value".
type Lookup func(name string) (string, bool)

func Contains(s string) bool {
	return strings.IndexByte(s, '{') >= 0 && placeholderRe.MatchString(s)
}

// Names lists distinct placeholder names, to decide if the panel is needed.
func Names(s string) []string {
	if strings.IndexByte(s, '{') < 0 {
		return nil
	}
	matches := placeholderRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, dup := seen[m[1]]; dup {
			continue
		}
		seen[m[1]] = struct{}{}
		names = append(names, m[1])
	}
	return names
}

// Render substitutes every placeholder. Modifiers chain left to right:
// {USERNAME|upper}, {TRAFFIC_LIMIT|default:unlimited}, {TEXT|truncate:200}.
func Render(s string, lookup Lookup, unknown Unknown) string {
	if strings.IndexByte(s, '{') < 0 {
		return s
	}
	return placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := placeholderRe.FindStringSubmatch(match)
		name, modSpec := groups[1], groups[2]

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
	})
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
