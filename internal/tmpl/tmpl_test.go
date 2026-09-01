package tmpl

import (
	"reflect"
	"testing"
)

func lookupFrom(m map[string]string) Lookup {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestRender(t *testing.T) {
	values := map[string]string{
		"TRAFFIC_USED":      "10.50 GB",
		"TRAFFIC_AVAILABLE": "89.50 GB",
		"USERNAME":          "alice",
		"EMPTY":             "",
	}

	tests := []struct {
		name     string
		template string
		unknown  Unknown
		want     string
	}{
		{
			name:     "the motivating case",
			template: "Used {TRAFFIC_USED} of {TRAFFIC_AVAILABLE}",
			want:     "Used 10.50 GB of 89.50 GB",
		},
		{
			name:     "unknown placeholder is kept",
			template: "hi {NOPE}",
			want:     "hi {NOPE}",
		},
		{
			name:     "unknown placeholder is blanked",
			template: "hi {NOPE}!",
			unknown:  Blank,
			want:     "hi !",
		},
		{
			name:     "default modifier fills an unknown",
			template: "{NOPE|default:n/a}",
			want:     "n/a",
		},
		{
			name:     "default modifier fills a known-but-empty value",
			template: "{EMPTY|default:none}",
			want:     "none",
		},
		{
			name:     "modifiers chain",
			template: "{USERNAME|upper}",
			want:     "ALICE",
		},
		{
			name:     "truncate caps rune length",
			template: "{TRAFFIC_USED|truncate:5}",
			want:     "10.5…",
		},
		{
			name:     "lowercase names are not placeholders",
			template: "{traffic_used}",
			want:     "{traffic_used}",
		},
		{
			name:     "json payloads pass through untouched",
			template: `{"outbounds": [{"tag": "proxy"}]}`,
			want:     `{"outbounds": [{"tag": "proxy"}]}`,
		},
		{
			name:     "text without braces is returned as-is",
			template: "no placeholders here",
			want:     "no placeholders here",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(tc.template, lookupFrom(values), tc.unknown)
			if got != tc.want {
				t.Errorf("Render(%q) = %q, want %q", tc.template, got, tc.want)
			}
		})
	}
}

func TestNames(t *testing.T) {
	got := Names("{A} then {B|upper} then {A} again")
	want := []string{"A", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want %v", got, want)
	}

	if got := Names("nothing here"); got != nil {
		t.Errorf("Names on plain text = %v, want nil", got)
	}
}

func TestContains(t *testing.T) {
	tests := map[string]bool{
		"{TRAFFIC_USED}":     true,
		"{TRAFFIC_USED|abc}": true,
		"plain":              false,
		`{"json": true}`:     false,
		"{lower}":            false,
	}
	for input, want := range tests {
		if got := Contains(input); got != want {
			t.Errorf("Contains(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	// Multi-byte runes must be counted as characters, not bytes: the announce
	// header is limited by displayed characters.
	if got := Truncate("привет мир", 6); got != "приве…" {
		t.Errorf("Truncate = %q, want %q", got, "приве…")
	}
	if got := Truncate("short", 50); got != "short" {
		t.Errorf("Truncate should not modify a short string, got %q", got)
	}
	if got := Truncate("short", 0); got != "short" {
		t.Errorf("Truncate with n=0 should not modify, got %q", got)
	}
}
