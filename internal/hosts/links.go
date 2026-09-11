package hosts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/hteppl/remnawave-subpage-proxy/internal/b64"
)

// applyLinks moves lines byte for byte, plain or base64-wrapped.
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

// decodeList returns nil text for a non-link body and a nil encoding for plain text.
func decodeList(body []byte) ([]byte, *base64.Encoding) {
	if isLinkList(body) {
		return body, nil
	}
	decoded, enc, _ := b64.Decode(string(body), isLinkList)
	return decoded, enc
}

// isLinkList checks only the first non-empty line to avoid splitting the whole body.
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
	decoded, _, ok := b64.Decode(payload, nil)
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
