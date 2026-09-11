package rewrite

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
)

// benchHeaders is what the subscription page sends: a panel announce holding
// placeholders among a dozen opaque headers.
func benchHeaders() http.Header {
	h := http.Header{}
	h.Set("Subscription-Userinfo", "upload=500000000; download=9500000000; total=100000000000; expire=1798761599")
	h.Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte("Used {TRAFFIC_USED} of {TRAFFIC_LIMIT} · {DAYS_LEFT} days left")))
	h.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("My VPN")))
	h.Set("Profile-Update-Interval", "12")
	h.Set("Support-Url", "https://t.me/support")
	h.Set("Profile-Web-Page-Url", "https://sub.example.com/aBcDeF123")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="aBcDeF123"`)
	h.Set("Routing", "happ://routing/onadd/eyJOYW1lIjoiUlUifQ==")
	h.Set("X-Powered-By", "Remnawave")
	h.Set("Cache-Control", "no-cache")
	h.Set("Date", "Fri, 11 Sep 2026 12:00:00 GMT")
	return h
}

var benchRequest = Request{ShortUUID: "aBcDeF123", ClientType: "", UserAgent: "Happ/1.2.3", ClientIP: "203.0.113.9"}

func BenchmarkApplyScanAll(b *testing.B) {
	e := New(Options{File: baseFile(), Logger: quietLogger()})
	src := benchHeaders()
	b.ReportAllocs()
	for b.Loop() {
		e.Apply(context.Background(), src.Clone(), benchRequest)
	}
}

func BenchmarkApplyRules(b *testing.B) {
	f := baseFile()
	f.Headers = []config.HeaderRule{
		{Name: "announce", Template: ptr("{PROGRESS_BAR} {TRAFFIC_USED_PERCENT}% · {EXPIRES_AT_DATE}"), Encode: config.EncodeBase64Prefixed, MaxLength: 200},
		{Name: "profile-title", Template: ptr("VPN — {TRAFFIC_AVAILABLE}"), Encode: config.EncodeBase64},
	}
	e := New(Options{File: f, Logger: quietLogger()})
	src := benchHeaders()
	b.ReportAllocs()
	for b.Loop() {
		e.Apply(context.Background(), src.Clone(), benchRequest)
	}
}

// Baseline for the two above: the cost of the header clone alone.
func BenchmarkHeaderClone(b *testing.B) {
	src := benchHeaders()
	b.ReportAllocs()
	for b.Loop() {
		_ = src.Clone()
	}
}
