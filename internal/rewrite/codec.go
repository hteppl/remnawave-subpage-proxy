package rewrite

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/hteppl/remnawave-subpage-proxy/internal/b64"
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

// DecodeBase64 requires printable UTF-8 output, since short ASCII words are often valid base64.
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
	raw, _, ok := b64.Decode(value, isText)
	return string(raw), ok
}

func isText(raw []byte) bool {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return false
	}
	for _, r := range string(raw) {
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
