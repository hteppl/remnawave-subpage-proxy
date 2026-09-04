package proxy

import "testing"

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		prefix         string
		wantShortUUID  string
		wantClientType string
	}{
		{name: "bare short uuid", path: "/aBcDeF123", wantShortUUID: "aBcDeF123"},
		{name: "with client type", path: "/aBcDeF123/clash", wantShortUUID: "aBcDeF123", wantClientType: "clash"},
		{name: "client type is case folded", path: "/aBcDeF123/CLASH", wantShortUUID: "aBcDeF123", wantClientType: "clash"},
		{name: "unknown second segment is not a client type", path: "/aBcDeF123/whatever", wantShortUUID: "aBcDeF123"},
		{name: "custom prefix", path: "/sub/aBcDeF123", prefix: "sub", wantShortUUID: "aBcDeF123"},
		{name: "custom prefix with client type", path: "/sub/aBcDeF123/json", prefix: "sub", wantShortUUID: "aBcDeF123", wantClientType: "json"},
		{name: "prefix mismatch", path: "/other/aBcDeF123", prefix: "sub"},
		{name: "prefix must end at a separator", path: "/subscribe/abc", prefix: "sub"},
		{name: "root", path: "/"},
		{name: "static assets", path: "/assets/index.js"},
		{name: "files are not short uuids", path: "/favicon.ico"},
		{name: "too deep", path: "/abc/json/extra"},
		{name: "empty segments are ignored", path: "//aBcDeF123//clash", wantShortUUID: "aBcDeF123", wantClientType: "clash"},
		{name: "prefix is matched exactly", path: "/SUB/aBcDeF123", prefix: "sub"},
		{name: "prefix alone names no subscription", path: "/sub/", prefix: "sub"},
		{name: "prefix of several segments", path: "/api/sub/aBcDeF123", prefix: "api/sub", wantShortUUID: "aBcDeF123"},
		{name: "partial multi-segment prefix", path: "/api/aBcDeF123", prefix: "api/sub"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRoute(tc.path, tc.prefix)
			if got.ShortUUID != tc.wantShortUUID {
				t.Errorf("ShortUUID = %q, want %q", got.ShortUUID, tc.wantShortUUID)
			}
			if got.ClientType != tc.wantClientType {
				t.Errorf("ClientType = %q, want %q", got.ClientType, tc.wantClientType)
			}
		})
	}
}
