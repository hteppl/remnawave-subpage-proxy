// Package b64 decodes base64 in any of the four alphabets, padded or not.
package b64

import (
	"encoding/base64"
	"strings"
)

var encodings = [...]*base64.Encoding{
	base64.StdEncoding,
	base64.RawStdEncoding,
	base64.URLEncoding,
	base64.RawURLEncoding,
}

// Decode returns the encoding it used so a rewrite can restore the same form; a nil accept takes any decode.
// accept must not keep the slice it is given: a rejected result is overwritten by the next alphabet.
func Decode(s string, accept func([]byte) bool) ([]byte, *base64.Encoding, bool) {
	s = strings.TrimSpace(s)
	// Most header values (URLs, dates, content types) hold a byte no alphabet
	// allows; ruling them out here skips four failed decodes each.
	if !plausible(s) {
		return nil, nil, false
	}
	src := []byte(s)
	dst := make([]byte, base64.RawStdEncoding.DecodedLen(len(src)))
	for _, enc := range encodings {
		n, err := enc.Decode(dst, src)
		if err != nil {
			continue
		}
		if accept == nil || accept(dst[:n]) {
			return dst[:n:n], enc, true
		}
	}
	return nil, nil, false
}

// plausible reports whether every byte belongs to some alphabet; the decoder
// itself skips only \r and \n.
func plausible(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
		case c == '+', c == '/', c == '-', c == '_', c == '=', c == '\r', c == '\n':
		default:
			return false
		}
	}
	return true
}
