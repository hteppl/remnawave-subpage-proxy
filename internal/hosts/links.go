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
	names := make([]string, len(lines))
	for i, line := range lines {
		names[i] = linkName(string(bytes.TrimRight(line, "\r")))
	}

	perm := s.permutation(names)
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

// isLinkList checks that the first non-empty line is a URI, without
// splitting the whole body.
func isLinkList(text []byte) bool {
	for len(text) > 0 {
		var line []byte
		if nl := bytes.IndexByte(text, '\n'); nl >= 0 {
			line, text = text[:nl], text[nl+1:]
		} else {
			line, text = text, nil
		}
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

// linkName extracts the name shown to the user from a share link.
func linkName(link string) string {
	scheme, rest, found := strings.Cut(link, "://")
	if !found {
		return ""
	}
	if strings.EqualFold(scheme, "vmess") {
		return vmessName(rest)
	}
	return fragmentName(link)
}

// fragmentName reads the #fragment every link but vmess carries.
func fragmentName(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Fragment
}

// vmessName reads ps from vmess://base64(json), falling back to a fragment.
func vmessName(payload string) string {
	payload, fragment, _ := strings.Cut(payload, "#")
	decoded, ok := decodeLoose(payload)
	if !ok {
		return unescape(fragment)
	}
	var node struct {
		PS string `json:"ps"`
	}
	if err := json.Unmarshal(decoded, &node); err != nil || node.PS == "" {
		return unescape(fragment)
	}
	return node.PS
}

func unescape(fragment string) string {
	if name, err := url.PathUnescape(fragment); err == nil {
		return name
	}
	return fragment
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
