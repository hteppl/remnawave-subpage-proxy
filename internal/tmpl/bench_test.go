package tmpl

import "testing"

var benchLookup = func(name string) (string, bool) {
	switch name {
	case "TRAFFIC_USED":
		return "10.50 GB", true
	case "TRAFFIC_LIMIT":
		return "100.00 GB", true
	case "DAYS_LEFT":
		return "12", true
	}
	return "", false
}

const benchAnnounce = "Used {TRAFFIC_USED} of {TRAFFIC_LIMIT} · {DAYS_LEFT} days left · {BRAND|default:VPN|upper}"

func BenchmarkRender(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Render(benchAnnounce, benchLookup, Keep)
	}
}

func BenchmarkNames(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Names(benchAnnounce)
	}
}

// Most headers hold no placeholder; this is the per-header cost of scanning them.
func BenchmarkContainsPlain(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Contains(`{"remarks":"de-1","outbounds":[{"tag":"proxy"}]}`)
	}
}
