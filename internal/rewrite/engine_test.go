package rewrite

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hteppl/remnawave-subpage-proxy/internal/config"
	"github.com/hteppl/remnawave-subpage-proxy/internal/panel"
)

type stubFetcher struct {
	info  *panel.Info
	err   error
	calls int
}

func (s *stubFetcher) SubscriptionInfo(context.Context, string, string) (*panel.Info, error) {
	s.calls++
	return s.info, s.err
}

func baseFile() config.File {
	return config.File{
		Traffic:     config.TrafficFormat{Decimals: 2, Unlimited: "∞"},
		DateTime:    config.DateTimeFormat{Layout: "02.01.2006", TimeLayout: "15:04", Timezone: "UTC", Never: "∞"},
		ProgressBar: config.ProgressBar{Width: 10, Filled: "▰", Empty: "▱"},
		Template: config.TemplateOpts{
			Unknown:        config.UnknownKeep,
			ScanAllHeaders: true,
			DecodeBase64:   true,
		},
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func ptr[T any](v T) *T { return &v }

const userInfo = "upload=500000000; download=9500000000; total=100000000000; expire=0"

func TestScanUpstreamAnnounce(t *testing.T) {
	fetcher := &stubFetcher{}
	engine := New(Options{File: baseFile(), Fetcher: fetcher, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	h.Set("announce", "Used {TRAFFIC_USED} of {TRAFFIC_LIMIT}, {TRAFFIC_AVAILABLE} left")

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	want := "Used 10.00 GB of 100.00 GB, 90.00 GB left"
	if got := h.Get("announce"); got != want {
		t.Errorf("announce = %q, want %q", got, want)
	}
	if fetcher.calls != 0 {
		t.Errorf("panel was called %d times; traffic placeholders must be served from the response header", fetcher.calls)
	}
	if got := len(h.Values("announce")); got != 1 {
		t.Errorf("announce has %d values, want exactly 1", got)
	}
}

func TestScanBase64PrefixedAnnounce(t *testing.T) {
	engine := New(Options{File: baseFile(), Fetcher: &stubFetcher{}, Logger: quietLogger()})

	template := "Осталось {TRAFFIC_AVAILABLE}"
	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	h.Set("announce", Base64Prefix+base64.StdEncoding.EncodeToString([]byte(template)))

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	got := h.Get("announce")
	decoded, form, ok := DecodeBase64(got)
	if !ok || form != FormBase64Prefixed {
		t.Fatalf("announce should stay base64: prefixed, got %q", got)
	}
	if decoded != "Осталось 90.00 GB" {
		t.Errorf("decoded announce = %q", decoded)
	}
}

func TestOpaqueBase64IsLeftAlone(t *testing.T) {
	engine := New(Options{File: baseFile(), Fetcher: &stubFetcher{}, Logger: quietLogger()})

	original := base64.StdEncoding.EncodeToString([]byte("My VPN Profile"))
	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	h.Set("profile-title", original)

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	if got := h.Get("profile-title"); got != original {
		t.Errorf("profile-title = %q, want it unchanged (%q)", got, original)
	}
}

func TestRuleTemplateCreatesHeader(t *testing.T) {
	file := baseFile()
	file.Vars = map[string]string{"BRAND": "MyVPN"}
	file.Headers = []config.HeaderRule{{
		Name:      "announce",
		Template:  ptr("{BRAND}: {TRAFFIC_USED} / {TRAFFIC_LIMIT}"),
		Encode:    config.EncodeBase64Prefixed,
		MaxLength: 200,
	}}

	engine := New(Options{File: file, Fetcher: &stubFetcher{}, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	decoded, form, ok := DecodeBase64(h.Get("announce"))
	if !ok || form != FormBase64Prefixed {
		t.Fatalf("announce = %q, want base64: prefixed", h.Get("announce"))
	}
	if decoded != "MyVPN: 10.00 GB / 100.00 GB" {
		t.Errorf("announce = %q", decoded)
	}
}

func TestPanelFetchedOnlyWhenNeeded(t *testing.T) {
	file := baseFile()
	file.Headers = []config.HeaderRule{{
		Name:     "announce",
		Template: ptr("Hi {USERNAME}, {TRAFFIC_AVAILABLE} left"),
	}}

	fetcher := &stubFetcher{info: &panel.Info{
		IsFound: true,
		User:    panel.User{Username: "alice", UserStatus: "ACTIVE"},
	}}
	engine := New(Options{File: file, Fetcher: fetcher, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	if fetcher.calls != 1 {
		t.Errorf("panel calls = %d, want 1 (USERNAME is panel-only)", fetcher.calls)
	}
	if got, want := h.Get("announce"), "Hi alice, 90.00 GB left"; got != want {
		t.Errorf("announce = %q, want %q", got, want)
	}
}

func TestPanelFailureLeavesPlaceholderIntact(t *testing.T) {
	file := baseFile()
	file.Headers = []config.HeaderRule{{Name: "announce", Template: ptr("Hi {USERNAME}")}}

	fetcher := &stubFetcher{err: errors.New("panel down")}
	engine := New(Options{File: file, Fetcher: fetcher, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	if got := h.Get("announce"); got != "Hi {USERNAME}" {
		t.Errorf("announce = %q, want the placeholder preserved on panel failure", got)
	}
}

func TestConditions(t *testing.T) {
	t.Run("client type", func(t *testing.T) {
		file := baseFile()
		file.Headers = []config.HeaderRule{{
			Name:     "announce",
			Template: ptr("clash only"),
			When:     config.Condition{ClientTypes: []string{"clash"}},
		}}
		engine := New(Options{File: file, Logger: quietLogger()})

		h := http.Header{}
		engine.Apply(context.Background(), h, Request{ShortUUID: "abc", ClientType: "json"})
		if h.Get("announce") != "" {
			t.Errorf("rule should not fire for client type json, got %q", h.Get("announce"))
		}

		h = http.Header{}
		engine.Apply(context.Background(), h, Request{ShortUUID: "abc", ClientType: "clash"})
		if h.Get("announce") != "clash only" {
			t.Errorf("rule should fire for clash, got %q", h.Get("announce"))
		}
	})

	t.Run("user status", func(t *testing.T) {
		file := baseFile()
		file.Headers = []config.HeaderRule{{
			Name:     "announce",
			Template: ptr("your plan is used up"),
			When:     config.Condition{UserStatuses: []string{"LIMITED"}},
		}}
		fetcher := &stubFetcher{info: &panel.Info{IsFound: true, User: panel.User{UserStatus: "LIMITED"}}}
		engine := New(Options{File: file, Fetcher: fetcher, Logger: quietLogger()})

		h := http.Header{}
		engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})
		if h.Get("announce") != "your plan is used up" {
			t.Errorf("announce = %q", h.Get("announce"))
		}

		fetcher.info.User.UserStatus = "ACTIVE"
		h = http.Header{}
		engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})
		if h.Get("announce") != "" {
			t.Errorf("rule should not fire for ACTIVE, got %q", h.Get("announce"))
		}
	})

	t.Run("exists", func(t *testing.T) {
		file := baseFile()
		file.Template.ScanAllHeaders = false
		file.Headers = []config.HeaderRule{{
			Name:     "announce",
			Template: ptr("fallback"),
			When:     config.Condition{Exists: ptr(false)},
		}}
		engine := New(Options{File: file, Logger: quietLogger()})

		h := http.Header{}
		h.Set("announce", "from the panel")
		engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})
		if got := h.Get("announce"); got != "from the panel" {
			t.Errorf("existing header should win, got %q", got)
		}

		h = http.Header{}
		engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})
		if got := h.Get("announce"); got != "fallback" {
			t.Errorf("fallback should apply when absent, got %q", got)
		}
	})
}

func TestRemoveRule(t *testing.T) {
	file := baseFile()
	file.Headers = []config.HeaderRule{{Name: "announce", Remove: true}}
	engine := New(Options{File: file, Logger: quietLogger()})

	h := http.Header{}
	h.Set("announce", "goodbye")
	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	if got := h.Get("announce"); got != "" {
		t.Errorf("announce should have been removed, got %q", got)
	}
}

func TestMaxLengthTruncates(t *testing.T) {
	file := baseFile()
	file.Headers = []config.HeaderRule{{
		Name:      "announce",
		Template:  ptr("0123456789"),
		MaxLength: 5,
	}}
	engine := New(Options{File: file, Logger: quietLogger()})

	h := http.Header{}
	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})
	if got := h.Get("announce"); got != "0123…" {
		t.Errorf("announce = %q, want truncated", got)
	}
}

func TestNoShortUUIDIsANoop(t *testing.T) {
	fetcher := &stubFetcher{}
	engine := New(Options{File: baseFile(), Fetcher: fetcher, Logger: quietLogger()})

	h := http.Header{}
	h.Set("announce", "{TRAFFIC_USED}")
	engine.Apply(context.Background(), h, Request{})

	if got := h.Get("announce"); got != "{TRAFFIC_USED}" {
		t.Errorf("non-subscription responses must not be touched, got %q", got)
	}
	if fetcher.calls != 0 {
		t.Error("panel must not be called for non-subscription responses")
	}
}

// Marzban legacy links carry an opaque token instead of a short UUID; the
// response header still has the counters.
func TestMarzbanLegacyLinkStillResolvesTraffic(t *testing.T) {
	fetcher := &stubFetcher{}
	engine := New(Options{File: baseFile(), Fetcher: fetcher, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	h.Set("announce", "{TRAFFIC_AVAILABLE} left for {USERNAME}")

	engine.Apply(context.Background(), h, Request{})

	if got, want := h.Get("announce"), "90.00 GB left for {USERNAME}"; got != want {
		t.Errorf("announce = %q, want %q", got, want)
	}
	if fetcher.calls != 0 {
		t.Error("no short UUID means no panel lookup is possible")
	}
}

func TestForceUnlimitedRewritesHeaderAndPlaceholders(t *testing.T) {
	file := baseFile()
	file.Traffic.ForceUnlimited = true
	engine := New(Options{File: file, Fetcher: &stubFetcher{}, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	h.Set("announce", "{TRAFFIC_USED} of {TRAFFIC_LIMIT}")

	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	// The header is what actually makes a client display an unlimited plan.
	want := "upload=500000000; download=9500000000; total=0; expire=0"
	if got := h.Get("subscription-userinfo"); got != want {
		t.Errorf("subscription-userinfo = %q, want %q", got, want)
	}
	if got := h.Get("announce"); got != "10.00 GB of ∞" {
		t.Errorf("announce = %q", got)
	}
}

// With no rules and no placeholders there is still work to do: the header.
func TestForceUnlimitedWithNoRules(t *testing.T) {
	file := baseFile()
	file.Traffic.ForceUnlimited = true
	file.Template.ScanAllHeaders = false
	engine := New(Options{File: file, Logger: quietLogger()})

	if !engine.Enabled() {
		t.Fatal("engine must stay enabled for the header rewrite alone")
	}

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	if got := h.Get("subscription-userinfo"); !strings.Contains(got, "total=0") {
		t.Errorf("subscription-userinfo = %q, want total=0", got)
	}
}

func TestForceUnlimitedOffByDefault(t *testing.T) {
	engine := New(Options{File: baseFile(), Fetcher: &stubFetcher{}, Logger: quietLogger()})

	h := http.Header{}
	h.Set("subscription-userinfo", userInfo)
	engine.Apply(context.Background(), h, Request{ShortUUID: "abc"})

	if got := h.Get("subscription-userinfo"); got != userInfo {
		t.Errorf("subscription-userinfo = %q, want it untouched", got)
	}
}
