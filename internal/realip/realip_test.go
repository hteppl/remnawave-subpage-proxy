package realip

import (
	"net/http"
	"testing"
)

func request(remote string, forwarded ...string) *http.Request {
	req := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	for _, f := range forwarded {
		req.Header.Add("X-Forwarded-For", f)
	}
	return req
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		remote    string
		forwarded []string
		want      string
	}{
		{
			name:   "no trust falls back to the socket",
			spec:   "false",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7"},
			want: "10.0.0.5",
		},
		{
			name:   "trust all takes the leftmost hop",
			spec:   "true",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7, 10.0.0.9"},
			want: "203.0.113.7",
		},
		{
			name:   "one hop takes the entry the proxy added",
			spec:   "1",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name:   "two hops steps back another entry",
			spec:   "2",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7, 10.0.0.9"},
			want: "203.0.113.7",
		},
		{
			name:   "hop count beyond the chain clamps to the leftmost",
			spec:   "9",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name:   "cidr list walks past trusted hops",
			spec:   "uniquelocal,loopback",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7, 192.168.1.4"},
			want: "203.0.113.7",
		},
		{
			name:   "cidr list stops at the first untrusted hop",
			spec:   "uniquelocal",
			remote: "10.0.0.5:5555", forwarded: []string{"198.51.100.1, 203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name:   "ipv4 mapped addresses are unwrapped",
			spec:   "false",
			remote: "[::ffff:203.0.113.7]:443",
			want:   "203.0.113.7",
		},
		{
			name:   "no forwarded header uses the socket",
			spec:   "true",
			remote: "203.0.113.7:443",
			want:   "203.0.113.7",
		},
		{
			name:   "multiple headers are flattened",
			spec:   "1",
			remote: "10.0.0.5:5555", forwarded: []string{"203.0.113.7", "10.0.0.9"},
			want: "10.0.0.9",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.spec)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.spec, err)
			}
			if got := r.ClientIP(request(tc.remote, tc.forwarded...)); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, spec := range []string{"-1", "not-an-ip", "300.1.1.1"} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) should have failed", spec)
		}
	}
}
