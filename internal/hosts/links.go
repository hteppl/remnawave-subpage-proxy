package hosts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// applyLinks handles one link per line, plain or base64-wrapped. Lines are
// moved byte for byte.
func (s *Shuffler) applyLinks(body []byte) ([]byte, bool) {
	text, enc := decodeList(body)
	if text == nil {
		return body, false
	}

	lines := bytes.Split(text, []byte("\n"))
	hosts := make([]Host, len(lines))
	for i, line := range lines {
		hosts[i] = linkHost(string(bytes.TrimRight(line, "\r")))
	}

	perm := s.permutation(hosts)
	if perm == nil {
		return body, false
	}

	shuffled := make([][]byte, len(lines))
	for slot, from := range perm {
		shuffled[slot] = lines[from]
	}
	out := bytes.Join(shuffled, []byte("\n"))
	if enc != nil {
		out = []byte(enc.EncodeToString(out))
	}
	return out, true
}

// decodeList returns the link list and the base64 encoding to restore, nil
// encoding for plain text, nil text for neither.
func decodeList(body []byte) ([]byte, *base64.Encoding) {
	if isLinkList(body) {
		return body, nil
	}

	raw := bytes.TrimSpace(body)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		decoded, err := enc.DecodeString(string(raw))
		if err != nil {
			continue
		}
		if isLinkList(decoded) {
			return decoded, enc
		}
		return nil, nil
	}
	return nil, nil
}

// isLinkList checks that the first non-empty line is a URI.
func isLinkList(text []byte) bool {
	for _, line := range bytes.Split(text, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		scheme, _, found := bytes.Cut(line, []byte("://"))
		return found && len(scheme) > 0 && isSchemeName(scheme)
	}
	return false
}

func isSchemeName(scheme []byte) bool {
	for _, c := range scheme {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '+', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// linkHost extracts server and name from a share link.
func linkHost(link string) Host {
	scheme, rest, found := strings.Cut(link, "://")
	if !found {
		return Host{}
	}
	switch strings.ToLower(scheme) {
	case "vmess":
		return vmessHost(rest)
	case "ss":
		// Legacy ss is one base64 blob with no @.
		if authority, _, _ := strings.Cut(rest, "#"); strings.Contains(authority, "@") {
			return urlHost(link)
		}
		return legacySSHost(rest)
	default:
		return urlHost(link)
	}
}

func urlHost(link string) Host {
	u, err := url.Parse(link)
	if err != nil {
		return Host{}
	}
	return Host{Hostname: u.Hostname(), Name: u.Fragment}
}

func vmessHost(payload string) Host {
	payload, _, _ = strings.Cut(payload, "#")
	decoded, ok := decodeLoose(payload)
	if !ok {
		return Host{}
	}
	var node struct {
		Add string `json:"add"`
		PS  string `json:"ps"`
	}
	if err := json.Unmarshal(decoded, &node); err != nil {
		return Host{}
	}
	return Host{Hostname: node.Add, Name: node.PS}
}

// legacySSHost reads ss://base64(method:pass@host:port)#name.
func legacySSHost(payload string) Host {
	payload, fragment, _ := strings.Cut(payload, "#")
	decoded, ok := decodeLoose(payload)
	if !ok {
		return Host{}
	}
	host := urlHost("ss://" + string(decoded))
	if name, err := url.PathUnescape(fragment); err == nil {
		host.Name = name
	} else {
		host.Name = fragment
	}
	return host
}

// decodeLoose accepts any base64 alphabet, padded or not.
func decodeLoose(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(s); err == nil {
			return decoded, true
		}
	}
	return nil, false
}
