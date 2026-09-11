package hosts

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const noticeName = `⚠️ Invalid User-Agent #1 "quoted": reset it`

// Each placeholder must read back in its own format, carry the name
// unmangled, and be recognised as that format again.
func TestPlaceholderRoundTrips(t *testing.T) {
	t.Run("links", func(t *testing.T) {
		body := Placeholder(FormatLinks, noticeName)
		decoded, err := base64.StdEncoding.DecodeString(string(body))
		if err != nil {
			t.Fatalf("not base64: %v", err)
		}
		_, fragment, _ := strings.Cut(string(decoded), "#")
		if name, _ := url.PathUnescape(fragment); name != noticeName {
			t.Errorf("name = %q", name)
		}
		if linkName(string(decoded)) != noticeName {
			t.Errorf("the shuffler reads the name as %q", linkName(string(decoded)))
		}
	})

	t.Run("xray", func(t *testing.T) {
		body := Placeholder(FormatXray, noticeName)
		var configs []struct {
			Remarks string `json:"remarks"`
		}
		if err := json.Unmarshal(body, &configs); err != nil || len(configs) != 1 || configs[0].Remarks != noticeName {
			t.Errorf("got %s (%v)", body, err)
		}
		if Sniff(body) != FormatXray {
			t.Error("not sniffed as xray")
		}
	})

	t.Run("singbox", func(t *testing.T) {
		body := Placeholder(FormatSingbox, noticeName)
		var config struct {
			Outbounds []struct {
				Tag string `json:"tag"`
			} `json:"outbounds"`
		}
		if err := json.Unmarshal(body, &config); err != nil || len(config.Outbounds) != 1 || config.Outbounds[0].Tag != noticeName {
			t.Errorf("got %s (%v)", body, err)
		}
		if Sniff(body) != FormatSingbox {
			t.Error("not sniffed as sing-box")
		}
	})

	t.Run("clash", func(t *testing.T) {
		body := Placeholder(FormatClash, noticeName)
		var config struct {
			Proxies []struct {
				Name string `yaml:"name"`
			} `yaml:"proxies"`
			Groups []struct {
				Proxies []string `yaml:"proxies"`
			} `yaml:"proxy-groups"`
		}
		if err := yaml.Unmarshal(body, &config); err != nil || len(config.Proxies) != 1 || config.Proxies[0].Name != noticeName {
			t.Fatalf("got %s (%v)", body, err)
		}
		if len(config.Groups) != 1 || config.Groups[0].Proxies[0] != noticeName {
			t.Errorf("group does not list the placeholder: %s", body)
		}
		if Sniff(body) != FormatClash {
			t.Error("not sniffed as clash")
		}
	})
}

// Several names give several hosts, in order, with blank and repeated ones
// dropped so sing-box and Clash accept the profile.
func TestPlaceholderSplitsIntoHosts(t *testing.T) {
	names := []string{"⚠️ Line one", "", "Line two", "Line one  ", "⚠️ Line one", "Line three"}
	want := []string{"⚠️ Line one", "Line two", "Line one", "Line three"}

	decoded, _ := base64.StdEncoding.DecodeString(string(Placeholder(FormatLinks, names...)))
	lines := strings.Split(string(decoded), "\n")
	var got []string
	for _, line := range lines {
		got = append(got, linkName(line))
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("links: got %q, want %q", got, want)
	}

	var clash struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
		Groups []struct {
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(Placeholder(FormatClash, names...), &clash); err != nil {
		t.Fatal(err)
	}
	if len(clash.Proxies) != len(want) || strings.Join(clash.Groups[0].Proxies, "|") != strings.Join(want, "|") {
		t.Errorf("clash: %+v", clash)
	}

	var xray []struct {
		Remarks string `json:"remarks"`
	}
	if err := json.Unmarshal(Placeholder(FormatXray, names...), &xray); err != nil || len(xray) != len(want) {
		t.Errorf("xray: %+v (%v)", xray, err)
	}
}

// No placeholder may point at a reachable server.
func TestPlaceholderPointsNowhere(t *testing.T) {
	server := regexp.MustCompile(`0\.0\.0\.0`)
	for _, f := range []Format{FormatXray, FormatSingbox, FormatClash} {
		if !server.Match(Placeholder(f, "x")) {
			t.Errorf("format %d does not point at 0.0.0.0", f)
		}
	}
}
