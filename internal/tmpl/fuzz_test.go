package tmpl

import (
	"slices"
	"strings"
	"testing"
)

// referenceRender is the two-pass implementation Render replaced.
func referenceRender(s string, lookup Lookup, unknown Unknown) string {
	if strings.IndexByte(s, '{') < 0 {
		return s
	}
	return placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := placeholderRe.FindStringSubmatch(match)
		return renderOne(match, groups[1], groups[2], lookup, unknown)
	})
}

// referenceNames is the map-based implementation Names replaced.
func referenceNames(s string) []string {
	if strings.IndexByte(s, '{') < 0 {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

// fuzzLookup knows some names, some with empty values, so every default and
// unknown branch is reachable.
func fuzzLookup(name string) (string, bool) {
	switch name {
	case "A":
		return "alpha", true
	case "EMPTY":
		return "", true
	case "LONG":
		return "  Ünïcode value with spaces  ", true
	}
	return "", false
}

func FuzzRenderMatchesReference(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "{A}", "{A}{A}", "x{A}y{B}z", "{EMPTY|default:d}", "{B|default:}", "{A|upper|truncate:3}",
		"{LONG|trim|lower}", "{A|truncate:0}", "{a}", "{A", "A}", "{{A}}", "{A|default:x|y}", "{A|}", "{A||upper}",
		`{"json":{A}}`, "{A|truncate:-1}", "{Á}", "{A|default:{B}}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, unknown := range []Unknown{Keep, Blank} {
			if got, want := Render(s, fuzzLookup, unknown), referenceRender(s, fuzzLookup, unknown); got != want {
				t.Fatalf("Render(%q, %v) = %q, reference %q", s, unknown, got, want)
			}
		}
		if got, want := Names(s), referenceNames(s); !slices.Equal(got, want) {
			t.Fatalf("Names(%q) = %q, reference %q", s, got, want)
		}
	})
}
