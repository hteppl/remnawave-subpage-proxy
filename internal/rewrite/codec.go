package rewrite

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"
)

// Form records the wire encoding, so a rewrite can keep the same shape.
type Form int

const (
	FormPlain Form = iota
	// FormBase64Prefixed is what Happ uses for `announce`.
	FormBase64Prefixed
	FormBase64
)

const Base64Prefix = "base64:"

var encodings = []*base64.Encoding{
	base64.StdEncoding,
	base64.RawStdEncoding,
	base64.URLEncoding,
	base64.RawURLEncoding,
}

// DecodeBase64 refuses anything not decoding to printable UTF-8: short ASCII
// words are frequently valid base64 and must not be mangled.
func DecodeBase64(value string) (text string, form Form, ok bool) {
	if rest, found := strings.CutPrefix(value, Base64Prefix); found {
		decoded, ok := decodeAny(strings.TrimSpace(rest))
		if !ok {
			return "", FormPlain, false
		}
		return decoded, FormBase64Prefixed, true
	}

	decoded, ok := decodeAny(value)
	if !ok {
		return "", FormPlain, false
	}
	return decoded, FormBase64, true
}

func decodeAny(value string) (string, bool) {
	value = strings.TrimSpace(value)
	// Anything shorter is far more likely a plain token than encoded text.
	if len(value) < 4 {
		return "", false
	}
	for _, enc := range encodings {
		raw, err := enc.DecodeString(value)
		if err != nil {
			continue
		}
		if !utf8.Valid(raw) {
			continue
		}
		decoded := string(raw)
		if decoded == "" || !isPrintable(decoded) {
			continue
		}
		return decoded, true
	}
	return "", false
}

// isPrintable rejects control characters: the input was not encoded text.
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

func Encode(text string, form Form) string {
	switch form {
	case FormBase64Prefixed:
		return Base64Prefix + base64.StdEncoding.EncodeToString([]byte(text))
	case FormBase64:
		return base64.StdEncoding.EncodeToString([]byte(text))
	default:
		return text
	}
}
