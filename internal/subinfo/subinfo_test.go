package subinfo

import (
	"testing"
	"time"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/panel"
)

func testFile() config.File {
	return config.File{
		Traffic: config.TrafficFormat{Decimals: 2, Unlimited: "∞"},
		DateTime: config.DateTimeFormat{
			Layout:     "02.01.2006",
			TimeLayout: "15:04",
			Timezone:   "UTC",
			Never:      "∞",
		},
		ProgressBar: config.ProgressBar{Width: 10, Filled: "▰", Empty: "▱"},
	}
}

func TestParseUserInfo(t *testing.T) {
	info, ok := ParseUserInfo("upload=1000000; download=9000000; total=100000000; expire=1767225600")
	if !ok {
		t.Fatal("expected the header to parse")
	}
	if info.Used() != 10000000 {
		t.Errorf("Used = %d, want 10000000", info.Used())
	}
	if info.Total != 100000000 {
		t.Errorf("Total = %d", info.Total)
	}
	if info.Expire != 1767225600 {
		t.Errorf("Expire = %d", info.Expire)
	}

	if _, ok := ParseUserInfo(""); ok {
		t.Error("empty header should not parse")
	}
	if _, ok := ParseUserInfo("garbage"); ok {
		t.Error("garbage header should not parse")
	}
}

func TestFormatBytes(t *testing.T) {
	decimal := config.TrafficFormat{Decimals: 2}
	binary := config.TrafficFormat{Decimals: 2, BinaryUnits: true}

	tests := []struct {
		bytes  int64
		format config.TrafficFormat
		want   string
	}{
		{0, decimal, "0 B"},
		{512, decimal, "512 B"},
		{1000, decimal, "1.00 KB"},
		{10_500_000_000, decimal, "10.50 GB"},
		{1024, binary, "1.00 KiB"},
		{1 << 30, binary, "1.00 GiB"},
	}
	for _, tc := range tests {
		if got := FormatBytes(tc.bytes, tc.format); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestResolveFromHeaderOnly(t *testing.T) {
	r := NewResolver(testFile())
	ui, _ := ParseUserInfo("upload=500000000; download=9500000000; total=100000000000; expire=0")

	lookup := r.Lookup(Source{
		ShortUUID:       "abc",
		SubscriptionURL: "https://example.com/sub/abc",
		UserInfo:        &ui,
	})

	want := map[string]string{
		"TRAFFIC_USED":         "10.00 GB",
		"TRAFFIC_LIMIT":        "100.00 GB",
		"TRAFFIC_AVAILABLE":    "90.00 GB",
		"TRAFFIC_USED_PERCENT": "10",
		"TRAFFIC_LEFT_PERCENT": "90",
		"PROGRESS_BAR":         "▰▱▱▱▱▱▱▱▱▱",
		"SHORT_UUID":           "abc",
		"SUBSCRIPTION_URL":     "https://example.com/sub/abc",
		"EXPIRES_AT":           "∞",
	}
	for name, expected := range want {
		got, ok := lookup(name)
		if !ok {
			t.Errorf("%s did not resolve", name)
			continue
		}
		if got != expected {
			t.Errorf("%s = %q, want %q", name, got, expected)
		}
	}

	// Must stay unresolved so the placeholder is preserved, not blanked.
	if _, ok := lookup("USERNAME"); ok {
		t.Error("USERNAME should not resolve without panel data")
	}
}

func TestUnlimitedPlan(t *testing.T) {
	r := NewResolver(testFile())
	ui, _ := ParseUserInfo("upload=0; download=5000000000; total=0; expire=0")
	lookup := r.Lookup(Source{
		ShortUUID:       "abc",
		SubscriptionURL: "https://example.com/sub/abc",
		UserInfo:        &ui,
	})

	for _, name := range []string{"TRAFFIC_LIMIT", "TRAFFIC_AVAILABLE"} {
		got, ok := lookup(name)
		if !ok || got != "∞" {
			t.Errorf("%s = %q (ok=%v), want ∞", name, got, ok)
		}
	}
	if got, _ := lookup("TRAFFIC_USED"); got != "5.00 GB" {
		t.Errorf("TRAFFIC_USED = %q", got)
	}
}

func TestResolveFromPanel(t *testing.T) {
	r := NewResolver(testFile())
	expires := time.Now().Add(72 * time.Hour).UTC()

	info := &panel.Info{
		IsFound: true,
		User: panel.User{
			Username:                 "alice",
			DaysLeft:                 3,
			TrafficUsedBytes:         "10500000000",
			TrafficLimitBytes:        "100000000000",
			LifetimeTrafficUsedBytes: "250000000000",
			UserStatus:               "ACTIVE",
			IsActive:                 true,
			TrafficLimitStrategy:     "MONTH",
			ExpiresAt:                expires.Format(time.RFC3339),
		},
	}
	lookup := r.Lookup(Source{ShortUUID: "abc", Panel: info})

	want := map[string]string{
		"USERNAME":              "alice",
		"USER_STATUS":           "ACTIVE",
		"IS_ACTIVE":             "true",
		"DAYS_LEFT":             "3",
		"TRAFFIC_USED":          "10.50 GB",
		"TRAFFIC_AVAILABLE":     "89.50 GB",
		"LIFETIME_TRAFFIC_USED": "250.00 GB",
		"EXPIRES_AT_DATE":       expires.Format("02.01.2006"),
	}
	for name, expected := range want {
		got, ok := lookup(name)
		if !ok || got != expected {
			t.Errorf("%s = %q (ok=%v), want %q", name, got, ok, expected)
		}
	}
}

func TestNeedsPanel(t *testing.T) {
	file := testFile()
	file.Vars = map[string]string{"BRAND": "MyProject"}
	r := NewResolver(file)

	tests := []struct {
		names         []string
		haveUserInfo  bool
		wantNeedPanel bool
	}{
		{[]string{"TRAFFIC_USED", "TRAFFIC_AVAILABLE"}, true, false},
		{[]string{"TRAFFIC_USED"}, false, true},
		{[]string{"USERNAME"}, true, true},
		{[]string{"SUBSCRIPTION_URL"}, true, false},
		{[]string{"BRAND", "SHORT_UUID", "DATE"}, false, false},
		{[]string{"UNKNOWN_THING"}, false, false},
	}
	for _, tc := range tests {
		if got := r.NeedsPanel(tc.names, tc.haveUserInfo); got != tc.wantNeedPanel {
			t.Errorf("NeedsPanel(%v, haveUserInfo=%v) = %v, want %v",
				tc.names, tc.haveUserInfo, got, tc.wantNeedPanel)
		}
	}
}

func TestForceUnlimitedTotal(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "replaces total, keeps the rest",
			header: "upload=500000000; download=9500000000; total=100000000000; expire=1798761599",
			want:   "upload=500000000; download=9500000000; total=0; expire=1798761599",
		},
		{
			name:   "adds total when absent",
			header: "upload=1; download=2",
			want:   "upload=1; download=2; total=0",
		},
		{
			name:   "already unlimited stays unlimited",
			header: "upload=0; download=0; total=0; expire=0",
			want:   "upload=0; download=0; total=0; expire=0",
		},
		{
			name:   "empty header yields a bare total",
			header: "",
			want:   "total=0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ForceUnlimitedTotal(tc.header); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestForceUnlimitedResolver(t *testing.T) {
	file := testFile()
	file.Traffic.ForceUnlimited = true
	r := NewResolver(file)

	ui, _ := ParseUserInfo("upload=500000000; download=9500000000; total=100000000000; expire=0")
	lookup := r.Lookup(Source{ShortUUID: "abc", UserInfo: &ui})

	want := map[string]string{
		// Real usage is still reported; only the ceiling is hidden.
		"TRAFFIC_USED":         "10.00 GB",
		"TRAFFIC_LIMIT":        "∞",
		"TRAFFIC_AVAILABLE":    "∞",
		"TRAFFIC_USED_PERCENT": "0",
		"PROGRESS_BAR":         "▱▱▱▱▱▱▱▱▱▱",
	}
	for name, expected := range want {
		got, ok := lookup(name)
		if !ok || got != expected {
			t.Errorf("%s = %q (ok=%v), want %q", name, got, ok, expected)
		}
	}
}

// The override does not depend on having any counters to start from.
func TestForceUnlimitedWithoutCounters(t *testing.T) {
	file := testFile()
	file.Traffic.ForceUnlimited = true
	r := NewResolver(file)

	lookup := r.Lookup(Source{ShortUUID: "abc"})
	for _, name := range []string{"TRAFFIC_LIMIT", "TRAFFIC_AVAILABLE"} {
		if got, ok := lookup(name); !ok || got != "∞" {
			t.Errorf("%s = %q (ok=%v), want ∞", name, got, ok)
		}
	}
	if _, ok := lookup("TRAFFIC_USED"); ok {
		t.Error("TRAFFIC_USED must stay unresolved with no counters")
	}
}
