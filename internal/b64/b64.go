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
func Decode(s string, accept func([]byte) bool) ([]byte, *base64.Encoding, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range encodings {
		raw, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		if accept == nil || accept(raw) {
			return raw, enc, true
		}
	}
	return nil, nil, false
}
