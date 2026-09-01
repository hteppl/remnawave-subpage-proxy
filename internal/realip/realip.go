package realip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

type Resolver struct {
	trustAll bool
	hops     int
	nets     []netip.Prefix
}

var presets = map[string][]string{
	"loopback":    {"127.0.0.0/8", "::1/128"},
	"linklocal":   {"169.254.0.0/16", "fe80::/10"},
	"uniquelocal": {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"},
}

// Parse reads TRUST_PROXY with Express "trust proxy" semantics: "true"/"false",
// a hop count, or a list of presets (loopback, linklocal, uniquelocal), IPs and
// CIDR ranges.
func Parse(spec string) (*Resolver, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return &Resolver{}, nil
	}

	switch strings.ToLower(spec) {
	case "true":
		return &Resolver{trustAll: true}, nil
	case "false":
		return &Resolver{}, nil
	}

	if hops, err := strconv.Atoi(spec); err == nil {
		if hops < 0 {
			return nil, fmt.Errorf("TRUST_PROXY hop count must not be negative, got %d", hops)
		}
		return &Resolver{hops: hops}, nil
	}

	r := &Resolver{}
	for _, token := range strings.Split(spec, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if cidrs, ok := presets[strings.ToLower(token)]; ok {
			for _, cidr := range cidrs {
				r.nets = append(r.nets, netip.MustParsePrefix(cidr))
			}
			continue
		}
		if prefix, err := netip.ParsePrefix(token); err == nil {
			r.nets = append(r.nets, prefix)
			continue
		}
		addr, err := netip.ParseAddr(token)
		if err != nil {
			return nil, fmt.Errorf("TRUST_PROXY entry %q is not a preset, IP address or CIDR range", token)
		}
		r.nets = append(r.nets, netip.PrefixFrom(addr, addr.BitLen()))
	}
	if len(r.nets) == 0 {
		return nil, fmt.Errorf("TRUST_PROXY %q did not yield any trusted networks", spec)
	}
	return r, nil
}

func (r *Resolver) ClientIP(req *http.Request) string {
	peer := PeerIP(req)

	forwarded := forwardedChain(req)
	if len(forwarded) == 0 {
		return peer
	}

	switch {
	case r.trustAll:
		return forwarded[0]

	case r.hops > 0:
		// Client-first chain, socket peer included: step n entries left from it.
		chain := append(append([]string{}, forwarded...), peer)
		idx := len(chain) - 1 - r.hops
		if idx < 0 {
			idx = 0
		}
		return chain[idx]

	case len(r.nets) > 0:
		chain := append(append([]string{}, forwarded...), peer)
		for i := len(chain) - 1; i >= 0; i-- {
			if !r.trusted(chain[i]) {
				return chain[i]
			}
		}
		return chain[0]

	default:
		return peer
	}
}

func (r *Resolver) trusted(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range r.nets {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func forwardedChain(req *http.Request) []string {
	var chain []string
	for _, header := range req.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(header, ",") {
			if ip := normalize(strings.TrimSpace(part)); ip != "" {
				chain = append(chain, ip)
			}
		}
	}
	return chain
}

func PeerIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	return normalize(host)
}

// normalize strips zones and unwraps IPv4-in-IPv6 (::ffff:203.0.113.4).
func normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return raw
	}
	return addr.Unmap().WithZone("").String()
}
